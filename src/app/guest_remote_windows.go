//go:build windows

package main

// Prepare and fix up without running anything inside the machine being
// moved: the guest engine runs on this computer (as before) or, through
// Invoke-GuestRemote.ps1, inside another Windows computer reached with
// PowerShell Direct from a Hyper-V host or PowerShell remoting over the
// network. A capture can also be built from what VMware Tools reported to
// vCenter (guest_capture.go), and one is saved automatically for every VMware
// VM Move VMs copies, so the new VM's IP settings can be put back with no
// recording step at all.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed Invoke-GuestRemote.ps1
var guestRemoteScriptData []byte

const guestRemoteScriptName = "Invoke-GuestRemote.ps1"

// guestTargetState is what the page sees of the target (never a password).
type guestTargetState struct {
	Mode        string `json:"mode"`
	ModeName    string `json:"modeName"`
	Label       string `json:"label"`
	VM          string `json:"vm"`
	Computer    string `json:"computer"`
	Host        string `json:"host"`
	User        string `json:"user"`
	HasPassword bool   `json:"hasPassword"`
	// Kept is the recording the console holds for this target ("" when
	// none), and KeptSource says where it came from (vCenter or Windows).
	Kept       string `json:"kept"`
	KeptSource string `json:"keptSource"`
	KeptAt     string `json:"keptAt"`
}

func (a *application) guestTargetsRoot() string { return filepath.Join(a.guestWorkPath(), "Targets") }

// guestKeptCapture finds the console's own recording for a target name: a
// full one (recorded inside Windows) first, else one built from vCenter.
func (a *application) guestKeptCapture(names ...string) (string, string, string) {
	for _, file := range []string{"Guest-Capture.json", "Guest-Capture-vCenter.json"} {
		for _, name := range names {
			if strings.TrimSpace(name) == "" {
				continue
			}
			path := filepath.Join(a.guestTargetsRoot(), guestCaptureKey(name), file)
			data, err := os.ReadFile(path)
			if err != nil || len(data) > 32<<20 {
				continue
			}
			var head struct {
				Source     string `json:"Source"`
				CapturedAt string `json:"CapturedAt"`
			}
			if json.Unmarshal(stripUTF8BOM(data), &head) != nil {
				continue
			}
			return path, vmNonEmpty(head.Source, "Windows"), head.CapturedAt
		}
	}
	return "", "", ""
}

// Caller holds a.mu.
func (a *application) guestTargetStateLocked() *guestTargetState {
	t := a.guestRemote
	if t.Mode == "" {
		t.Mode = "local"
	}
	state := &guestTargetState{Mode: t.Mode, ModeName: guestTargetModes[t.Mode], Label: guestTargetLabel(t, a.computerName), VM: t.VM, Computer: t.Computer, Host: t.Host, User: t.User, HasPassword: t.password != ""}
	if t.Mode != "local" {
		state.Kept, state.KeptSource, state.KeptAt = a.guestKeptCapture(t.VM, t.Computer)
	}
	return state
}

// saveVCenterCapture keeps a recording of a VMware VM's network settings,
// built from the inventory, for the fix-up after the cutover. It runs before
// every copy round of Move VMs, while VMware Tools still answers; a failure
// is only logged.
func (a *application) saveVCenterCapture(vm vmMachine, log func(string)) {
	if vm.Source == vmSourceHyperV {
		return
	}
	capture, err := vmGuestCaptureFromInventory(vm, time.Now())
	if err != nil {
		if log != nil {
			log("The VM's network settings were not recorded from vCenter: " + err.Error() + ".")
		}
		return
	}
	path, err := a.writeGuestCapture(capture, "Guest-Capture-vCenter.json", vm.Name, capture.State.ComputerName)
	if err != nil {
		if log != nil {
			log("The VM's network settings could not be saved: " + err.Error() + ".")
		}
		return
	}
	if log != nil {
		log(fmt.Sprintf("Recorded %s's network settings from vCenter (%d adapter(s)) in %s, to put back after the cutover.", vm.Name, len(capture.State.Adapters), path))
	}
}

func (a *application) writeGuestCapture(capture any, file string, names ...string) (string, error) {
	data, err := json.MarshalIndent(capture, "", "  ")
	if err != nil {
		return "", err
	}
	first := ""
	seen := map[string]bool{}
	for _, name := range names {
		key := guestCaptureKey(name)
		if strings.TrimSpace(name) == "" || seen[key] {
			continue
		}
		seen[key] = true
		folder := filepath.Join(a.guestTargetsRoot(), key)
		if err := os.MkdirAll(folder, 0700); err != nil {
			return "", err
		}
		path := filepath.Join(folder, file)
		if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
			return "", err
		}
		if err := atomicReplaceFile(path+".tmp", path); err != nil {
			return "", err
		}
		if first == "" {
			first = path
		}
	}
	if first == "" {
		return "", errors.New("the VM has no name")
	}
	return first, nil
}

// handleGuestTarget sets where Prepare and fix up runs, or records a VMware
// VM's settings from vCenter.
func (a *application) handleGuestTarget(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/vmware/guest-target") {
	case "", "/":
		var input struct {
			Mode     string `json:"mode"`
			VM       string `json:"vm"`
			Computer string `json:"computer"`
			Host     string `json:"host"`
			User     string `json:"user"`
			Password string `json:"password"`
			// KeepPassword keeps the password already held for the same
			// account, so changing nothing else needs no retyping.
			KeepPassword bool `json:"keepPassword"`
		}
		if !decodeVMwareRequest(response, request, &input) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.vmwareBusyLocked() {
			writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": a.vmwareBusyTextLocked() + " Wait for it to finish first."})
			return
		}
		password := input.Password
		if password == "" && input.KeepPassword && strings.EqualFold(strings.TrimSpace(input.User), a.guestRemote.User) {
			password = a.guestRemote.password
		}
		target, err := validateGuestTarget(guestTarget{Mode: input.Mode, VM: input.VM, Computer: input.Computer, Host: input.Host, User: input.User, password: password})
		if err == nil && target.Mode == "direct" && !vmHyperVHere() {
			err = errors.New("This computer is not a Hyper-V host. Run this application on the host that runs the VM, or connect to the host in Move VMs and choose the connected Hyper-V host.")
		}
		if err == nil && target.Mode == "hostdirect" {
			err = a.guestCheckHostLocked(target.Host)
		}
		if err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if target.Mode != a.guestRemote.Mode || !strings.EqualFold(guestTargetLabel(target, ""), guestTargetLabel(a.guestRemote, "")) {
			// A different machine: what was read before is not about it.
			a.vmware.guestResult = nil
			a.vmware.guestSerial++
		}
		a.guestRemote = target
		a.vmware.guestMessage, a.vmware.guestError = "Checks run on "+guestTargetLabel(target, a.computerName)+" ("+strings.ToLower(guestTargetModes[target.Mode][:1])+guestTargetModes[target.Mode][1:]+").", false
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "target": a.guestTargetStateLocked()})
	case "/forget":
		if !decodeVMwareRequest(response, request, nil) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.vmwareBusyLocked() {
			writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": a.vmwareBusyTextLocked() + " Wait for it to finish first."})
			return
		}
		a.guestRemote = guestTarget{Mode: "local"}
		a.vmware.guestResult = nil
		a.vmware.guestSerial++
		a.vmware.guestMessage, a.vmware.guestError = "Forgot the account. Checks run on this computer.", false
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "/vcenter":
		var input struct {
			VMID string `json:"vmId"`
		}
		if !decodeVMwareRequest(response, request, &input) {
			return
		}
		a.mu.Lock()
		inventory := a.vmware.inventory
		a.mu.Unlock()
		if inventory == nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Read vCenter or ESXi first (Discovery, VMware and Hyper-V)."})
			return
		}
		var vm *vmMachine
		for index := range inventory.VMs {
			if inventory.VMs[index].ID == input.VMID {
				vm = &inventory.VMs[index]
			}
		}
		if vm == nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose a VM from the list."})
			return
		}
		capture, err := vmGuestCaptureFromInventory(*vm, time.Now())
		if err == nil {
			_, err = a.writeGuestCapture(capture, "Guest-Capture-vCenter.json", vm.Name, capture.State.ComputerName)
		}
		if err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		static := 0
		for _, adapter := range capture.State.Adapters {
			if adapter.Static {
				static++
			}
		}
		message := fmt.Sprintf("Recorded %s from vCenter: %d adapter(s), %d with static settings. Nothing ran inside the VM. After the cutover they are put back from the Hyper-V host.", vm.Name, len(capture.State.Adapters), static)
		a.vmwareLog("%s", message)
		a.mu.Lock()
		a.vmware.guestMessage, a.vmware.guestError = message, false
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "message": message, "adapters": len(capture.State.Adapters), "static": static})
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "Not found."})
	}
}

// Caller holds a.mu. The host must be the connected remote Hyper-V host, or
// a node of its cluster, so its account is the one Move VMs already holds.
func (a *application) guestCheckHostLocked(host string) error {
	info := a.hvRemote.info
	if info == nil {
		return errors.New("Connect to the Hyper-V host or cluster first: Move VMs, select the VM, Another Hyper-V host or cluster.")
	}
	if strings.EqualFold(host, a.hvRemote.host) || strings.EqualFold(host, info.Computer) {
		return nil
	}
	for _, node := range info.Nodes {
		if strings.EqualFold(host, node.Name) {
			return nil
		}
	}
	return fmt.Errorf("%s is not the connected Hyper-V host or one of its cluster nodes", host)
}

// runGuestRemote runs one guest engine mode inside the target through
// Invoke-GuestRemote.ps1. Like Connect-HyperVTarget.ps1 it runs with the
// RemoteSigned execution policy for this process only (the released script is
// signed), never Bypass, and the accounts reach it only on standard input.
func (a *application) runGuestRemote(target guestTarget, mode string, plan []byte) (json.RawMessage, error) {
	return a.runGuestRemoteOptions(target, mode, plan, guestRemoteExtra{})
}

func (a *application) runGuestRemoteOptions(target guestTarget, mode string, plan []byte, extra guestRemoteExtra) (json.RawMessage, error) {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "GuestRemote-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, guestRemoteScriptName)
	engine := filepath.Join(directory, guestEngineName)
	if err := os.WriteFile(script, guestRemoteScriptData, 0600); err != nil {
		return nil, err
	}
	if _, err := writeEmbeddedFile(guestEngineName, engine); err != nil {
		return nil, err
	}
	resultPath := filepath.Join(directory, "Result.json")
	label := guestTargetLabel(target, a.computerName)
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Transport", target.Mode, "-Mode", mode, "-EnginePath", engine, "-ResultPath", resultPath}
	switch target.Mode {
	case "direct", "hostdirect":
		arguments = append(arguments, "-VMName", target.VM)
		if target.Mode == "hostdirect" {
			arguments = append(arguments, "-HostName", target.Host)
		}
		if vmHyperVIDPattern.MatchString(extra.VMID) {
			arguments = append(arguments, "-VMId", extra.VMID)
		}
	case "winrm":
		arguments = append(arguments, "-ComputerName", target.Computer)
	}
	if plan != nil {
		planPath := filepath.Join(directory, "Plan.json")
		if err := os.WriteFile(planPath, plan, 0600); err != nil {
			return nil, err
		}
		arguments = append(arguments, "-PlanPath", planPath)
	}
	if mode != "Capture" {
		if kept, _, _ := a.guestKeptCapture(target.VM, target.Computer); kept != "" {
			arguments = append(arguments, "-CapturePath", kept)
		}
	}
	switch mode {
	case "Capture":
		arguments = append(arguments, "-ExportPath", filepath.Join(a.guestTargetsRoot(), guestCaptureKey(label)))
	case "SaveBitLocker":
		arguments = append(arguments, "-ExportPath", a.vmwareOutputFolder("BitLocker-Keys_"+safeName(label)))
	}
	hostUser, hostPassword := "", ""
	if target.Mode == "hostdirect" {
		a.mu.Lock()
		hostUser, hostPassword = a.hvRemote.user, a.hvRemote.password
		a.mu.Unlock()
	}
	if extra.WaitSeconds > 0 {
		arguments = append(arguments, "-WaitSeconds", strconv.Itoa(extra.WaitSeconds))
	}
	arguments = append(arguments, "-CredentialFromInput")
	command := exec.Command(a.powerShellPath, arguments...)
	line, _ := json.Marshal(map[string]string{"user": target.User, "password": target.password, "hostUser": hostUser, "hostPassword": hostPassword})
	command.Stdin = bytes.NewReader(append(line, '\n'))
	waitErr := a.runEngineProcess(command, func(text string) {
		a.mu.Lock()
		a.appendVMwareLogLocked(text)
		a.mu.Unlock()
		if extra.Started != nil && strings.Contains(text, "[INFO] The VM answers.") {
			extra.Started()
		}
	}, func(*exec.Cmd) {})
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return nil, fmt.Errorf("the checks on %s stopped: %v", label, waitErr)
		}
		return nil, fmt.Errorf("the checks on %s wrote no result", label)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stripUTF8BOM(data), &envelope); err != nil {
		return nil, errors.New("the result from " + label + " could not be read")
	}
	if !envelope.OK {
		return nil, errors.New(vmNonEmpty(envelope.Error, "The step failed on "+label+"."))
	}
	if mode == "Capture" {
		// Keep the recording under the VM's name and the computer's own name
		// too, so a later PowerShell Direct fix-up of the new VM finds it.
		exported := filepath.Join(a.guestTargetsRoot(), guestCaptureKey(label), "Guest-Capture.json")
		if raw, err := os.ReadFile(exported); err == nil {
			var value struct {
				State struct {
					ComputerName string `json:"ComputerName"`
				} `json:"State"`
			}
			var whole any
			if json.Unmarshal(stripUTF8BOM(raw), &value) == nil && json.Unmarshal(stripUTF8BOM(raw), &whole) == nil {
				_, _ = a.writeGuestCapture(whole, "Guest-Capture.json", target.VM, value.State.ComputerName)
			}
		}
	}
	return envelope.Result, nil
}
