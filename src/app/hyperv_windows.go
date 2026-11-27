//go:build windows

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed Read-HyperVHost.ps1
var hypervReadScriptData []byte

var hypervHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)

// hypervIsLocal is true when name means the computer the application runs on.
func (a *application) hypervIsLocal(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || strings.EqualFold(name, "localhost") || name == "127.0.0.1" || name == "::1" {
		return true
	}
	if strings.Trim(name, "0123456789.") == "" {
		return false
	}
	return strings.EqualFold(strings.SplitN(name, ".", 2)[0], a.computerName)
}

// handleVMwareHyperV reads a Hyper-V host (this computer, or another one over
// CIM) into the Environment view. It replaces any vSphere session: a Hyper-V
// inventory cannot drive the vSphere steps (Prepare, Move), which need a
// vCenter password.
func (a *application) handleVMwareHyperV(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Host     string `json:"host"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	host := strings.TrimSpace(input.Host)
	user := strings.TrimSpace(input.User)
	password := input.Password
	input.Password = ""
	if host != "" && !hypervHostPattern.MatchString(host) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the Hyper-V host's name or IP address, or leave it empty to read this computer."})
		return
	}
	local := a.hypervIsLocal(host)
	if local {
		host, user, password = "", "", ""
	}
	if user != "" && (!vmwareUserPattern.MatchString(user) || password == "" || len(password) > 256 || strings.ContainsRune(password, 0)) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the password for " + user + ", or choose your current sign-in."})
		return
	}
	if status, message := a.startVMwareAction("hyperv"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	label := vmNonEmpty(host, a.computerName)
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "connecting"
	a.vmware.message = "Reading Hyper-V host " + label + "."
	a.appendVMwareLogLocked(time.Now().Format("2006-01-02 15:04:05") + " " + a.vmware.message)
	a.mu.Unlock()
	go func() {
		defer cancel()
		report, err := a.runHyperVRead(ctx, host, user, password)
		password = ""
		if err != nil {
			a.mu.Lock()
			canceled := a.vmware.cancelRequested
			a.mu.Unlock()
			message := err.Error()
			if canceled {
				message = "Stopped. Nothing was read."
			}
			a.vmwareLog("%s", message)
			status := "connectfailed"
			if a.vmwareIdleStatus() == "connected" {
				status = "connected"
			}
			a.finishVMwareAction(status, message)
			return
		}
		inventory := hypervInventoryFromReport(*report, local, time.Now(), appVersion)
		a.applySavedPreflight(inventory)
		folder := a.vmwareOutputFolder("HyperV_" + safeName(inventory.Server))
		reportPath, writeErr := writeVMwareInventoryOutputs(folder, inventory)
		plan := a.loadVMwarePlan(inventory)
		a.mu.Lock()
		a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint, a.vmware.certificate = inventory.Server, user, "", "", nil
		a.vmware.inventory = inventory
		a.vmware.inventorySerial++
		a.vmware.plan = &plan
		a.vmware.runbookPath, a.vmware.planCSVPath = "", ""
		if writeErr == nil {
			a.vmware.outputFolder, a.vmware.reportPath = folder, reportPath
		} else {
			a.vmware.outputFolder, a.vmware.reportPath = "", ""
		}
		a.mu.Unlock()
		message := fmt.Sprintf("Read %d VM(s) from Hyper-V host %s.", len(inventory.VMs), inventory.Server)
		if writeErr != nil {
			message += " The report files could not be written: " + writeErr.Error()
		}
		a.vmwareLog("%s", message)
		a.finishVMwareAction("connected", message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// runHyperVRead runs Read-HyperVHost.ps1. The account, when given, reaches
// the script only as one JSON line on standard input.
func (a *application) runHyperVRead(ctx context.Context, host, user, password string) (*hypervReport, error) {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "HyperV-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, hypervReadScript)
	if err := os.WriteFile(script, hypervReadScriptData, 0600); err != nil {
		return nil, err
	}
	resultPath := filepath.Join(directory, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-ResultPath", resultPath}
	if host != "" {
		arguments = append(arguments, "-ComputerName", host)
	}
	command := exec.CommandContext(ctx, a.powerShellPath, arguments...)
	if user != "" {
		line, _ := json.Marshal(map[string]string{"user": user, "password": password})
		command.Stdin = bytes.NewReader(append(line, '\n'))
	}
	waitErr := a.runEngineProcess(command, func(line string) {
		a.mu.Lock()
		a.appendVMwareLogLocked(line)
		a.mu.Unlock()
	}, func(*exec.Cmd) {})
	if ctx.Err() != nil {
		return nil, errors.New("Stopped. Nothing was read.")
	}
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return nil, fmt.Errorf("reading the Hyper-V host stopped: %v", waitErr)
		}
		return nil, errors.New("reading the Hyper-V host wrote no result")
	}
	if len(data) > hypervReportLimit {
		return nil, errors.New("the Hyper-V host's report is too large")
	}
	return parseHypervResult(data)
}
