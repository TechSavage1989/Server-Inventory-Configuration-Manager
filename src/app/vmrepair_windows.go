//go:build windows

package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Fix-ups for a converted Windows disk, from the Disk converter tab. The
// script (Repair-ConvertedDisk.ps1) mounts a VHD or VHDX on this computer
// and changes only the disk image: never this computer, never the source.
// Before any change it saves the image's SYSTEM hive and a record of every
// value under LocalAppData\...\Repair, which Undo reads. A fix-up changes
// the image after the conversion, so the conversion record beside it is set
// aside: converting again into the same file is refused (a delta pass would
// mix the fixed-up blocks with new source blocks). Running is a critical
// operation, shared with conversions.

//go:embed Repair-ConvertedDisk.ps1
var vmRepairScriptData []byte

const vmRepairScript = "Repair-ConvertedDisk.ps1"

// vmRepairRecordFolder is where the fix-up records of one image live.
func (a *application) vmRepairRecordFolder(image string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(image))))
	name := safeName(strings.TrimSuffix(filepath.Base(image), filepath.Ext(image)))
	return filepath.Join(a.update.appDataRoot, "Repair", name+"-"+hex.EncodeToString(sum[:])[:12])
}

// vmLatestRepairRecord is the newest complete record for image, or "".
// vmSameImagePath compares two spellings of an image path, such as a short
// (8.3) and a long name of the same file.
func vmSameImagePath(a, b string) bool {
	if strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) {
		return true
	}
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	return err == nil && os.SameFile(left, right)
}

func vmLatestRepairRecord(folder, image string) string {
	matches, _ := filepath.Glob(filepath.Join(folder, "Repair-*.json"))
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil || len(data) > vmRepairReportLimit {
			continue
		}
		record, err := parseRepairRecord(data)
		if err == nil && record.Complete && vmSameImagePath(record.Image, image) {
			return match
		}
	}
	return ""
}

func (a *application) handleConvertRepair(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Mode               string   `json:"mode"`
		Path               string   `json:"path"`
		Target             string   `json:"target"`
		Drivers            []string `json:"drivers"`
		DisableVMwareTools bool     `json:"disableVMwareTools"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	path := strings.TrimSpace(input.Path)
	mode := map[string]string{"check": "Check", "apply": "Apply", "undo": "Undo"}[input.Mode]
	target, targetOK := vmRepairTargetFor(input.Target)
	extension := strings.ToLower(filepath.Ext(path))
	switch {
	case mode == "":
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Unknown fix-up step."})
		return
	case path == "" || !filepath.IsAbs(path):
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the full path of the converted VHD or VHDX."})
		return
	case extension != ".vhd" && extension != ".vhdx":
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Fix-ups work on VHD and VHDX files. Convert the disk to VHDX first, fix it up, then convert it to the final format if the platform needs another one."})
		return
	case !targetOK:
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose the platform the disk is moving to."})
		return
	case !isAdministrator():
		writeJSON(response, http.StatusForbidden, map[string]any{"ok": false, "error": "Fix-ups attach the disk to this computer, which needs administrator rights. Start the application as an administrator."})
		return
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The file " + path + " does not exist."})
		return
	}
	drivers := []string{}
	for _, folder := range input.Drivers {
		folder = strings.TrimSpace(folder)
		if folder == "" {
			continue
		}
		if !filepath.IsAbs(folder) {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the full path of the drivers folder."})
			return
		}
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The drivers folder " + folder + " does not exist."})
			return
		}
		drivers = append(drivers, folder)
	}
	if len(drivers) > 4 {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose at most four drivers folders."})
		return
	}
	recordFolder := a.vmRepairRecordFolder(path)
	undoRecord := ""
	if mode == "Undo" {
		if undoRecord = vmLatestRepairRecord(recordFolder, path); undoRecord == "" {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "No fix-up of this disk was recorded on this computer, so there is nothing to undo."})
			return
		}
	}
	if status, message := a.beginConvertTask("repair", path); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	a.mu.Lock()
	a.convert.message = map[string]string{"Check": "Checking the Windows installation in ", "Apply": "Fixing up ", "Undo": "Undoing the fix-ups of "}[mode] + filepath.Base(path) + "."
	a.convert.repairTarget = target.ID
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})

	go func() {
		defer a.endCriticalOperation()
		if mode == "Apply" {
			// The image changes now; a later conversion into this file
			// must not continue as a delta pass.
			record := vmCopyStatePath(path)
			if _, err := os.Stat(record); err == nil {
				aside := record + ".fixed-up"
				os.Remove(aside)
				if err := os.Rename(record, aside); err != nil {
					a.finishConvertTask("failed", "The conversion record "+record+" could not be set aside: "+err.Error(), nil)
					return
				}
				a.convertLog("Set the conversion record aside: converting again needs a new output file.")
			}
		}
		report, err := a.runRepairScript(mode, path, target.Driver, drivers, input.DisableVMwareTools, recordFolder, undoRecord)
		if err != nil {
			a.finishConvertTask("failed", err.Error(), report)
			return
		}
		a.finishConvertTask("success", vmRepairDoneMessageText(mode, report), report)
	}()
}

// runRepairScript runs Repair-ConvertedDisk.ps1 and reads its result.
func (a *application) runRepairScript(mode, image, target string, drivers []string, disableTools bool, recordFolder, undoRecord string) (*vmRepairReport, error) {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "Repair-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, vmRepairScript)
	if err := os.WriteFile(script, vmRepairScriptData, 0600); err != nil {
		return nil, err
	}
	resultPath := filepath.Join(directory, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script,
		"-Mode", mode, "-ImagePath", image, "-ResultPath", resultPath, "-WorkPath", filepath.Join(directory, "Mount"), "-Target", target,
		"-RecordPath", recordFolder}
	if len(drivers) > 0 {
		arguments = append(arguments, "-DriverPath", strings.Join(drivers, "|"))
	}
	if disableTools {
		arguments = append(arguments, "-DisableVMwareTools")
	}
	if undoRecord != "" {
		arguments = append(arguments, "-UndoRecord", undoRecord)
	}
	// There is no Stop: the script must not be killed while the disk is
	// attached or a hive is loaded. It always detaches the disk itself.
	command := exec.Command(a.powerShellPath, arguments...)
	waitErr := a.runEngineProcess(command, func(line string) { a.convertLog(line) }, func(*exec.Cmd) {})
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return nil, errors.New("the fix-up stopped: " + waitErr.Error())
		}
		return nil, errors.New("the fix-up wrote no result")
	}
	report, err := parseRepairReport(data)
	if err != nil {
		return nil, err
	}
	if !report.OK {
		message := report.Error
		if message == "" {
			message = "the fix-up failed"
		}
		return report, errors.New(message)
	}
	return report, nil
}

// beginConvertTask starts a Disk converter task (convert, repair, upload)
// as a critical operation. It returns an HTTP status and message when
// another step is running.
func (a *application) beginConvertTask(task, target string) (int, string) {
	if !a.beginCriticalOperation() {
		return http.StatusConflict, "An update is being installed."
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	refusal := ""
	switch {
	case a.state.Running:
		refusal = "A collection is running. Wait for it to finish first."
	case a.restoreBusyLocked():
		refusal = "A restore is running. Wait for it to finish first."
	case a.migrateBusyLocked():
		refusal = "A file migration step is running. Wait for it to finish first."
	case a.vmwareBusyLocked():
		refusal = "A VMware step is running. Wait for it to finish first."
	case a.convertBusyLocked():
		refusal = "A Disk converter step is already running."
	}
	if refusal != "" {
		a.endCriticalOperation()
		return http.StatusConflict, refusal
	}
	previous := a.convert
	a.convert = convertRuntime{
		running: true, status: "running", task: task, target: target,
		source: previous.source, info: previous.info, format: previous.format,
		serial: previous.serial + 1, startedAt: time.Now(),
	}
	return 0, ""
}

func (a *application) finishConvertTask(status, message string, report *vmRepairReport) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.convert.running, a.convert.cancel, a.convert.endedAt = false, nil, time.Now()
	a.convert.status, a.convert.message = status, message
	if report != nil {
		a.convert.repair = report
	}
	a.appendConvertLogLocked(time.Now().Format("2006-01-02 15:04:05") + " " + message)
}

func (a *application) convertLog(line string) {
	a.mu.Lock()
	a.appendConvertLogLocked(line)
	a.mu.Unlock()
}

// Caller holds a.mu.
func (a *application) appendConvertLogLocked(line string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	a.convert.log = append(a.convert.log, line)
	if len(a.convert.log) > 400 {
		a.convert.log = append([]string(nil), a.convert.log[len(a.convert.log)-400:]...)
	}
}
