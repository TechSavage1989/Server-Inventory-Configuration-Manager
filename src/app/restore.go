//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Restore applies configuration from an inventory package back onto this
// server. The embedded Restore-ServerConfiguration.ps1 does the work; this
// file owns the process, the plan the operator confirmed, and the state the
// interface shows. A restore is a critical operation, so updates cannot be
// installed while it runs, and it cannot start while a collection runs.

const (
	restoreEngineName  = "Restore-ServerConfiguration.ps1"
	restoreEventPrefix = "@@RESTORE@@"
	restoreLogLimit    = 500
)

type restoreItemState struct {
	Component string `json:"component"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

type restorePackages struct {
	LastCollection string `json:"lastCollection"`
	LoadedReport   string `json:"loadedReport"`
}

type restoreStateResponse struct {
	Status          string             `json:"status"`
	Message         string             `json:"message"`
	PackagePath     string             `json:"packagePath"`
	Packages        restorePackages    `json:"packages"`
	Scan            *restoreScan       `json:"scan"`
	Items           []restoreItemState `json:"items"`
	Logs            []logEntry         `json:"logs"`
	RollbackPath    string             `json:"rollbackPath"`
	CancelRequested bool               `json:"cancelRequested"`
	StartedAtUnixMs int64              `json:"startedAtUnixMs"`
	EndedAtUnixMs   int64              `json:"endedAtUnixMs"`
	// RestartNeeded: installed roles and features need a restart.
	RestartNeeded bool `json:"restartNeeded"`
	// HasPassphrase says a bundle passphrase is held for this package (never
	// the passphrase).
	HasPassphrase bool `json:"hasPassphrase"`
	// Live: the package is a live move's copy (Restore tab, "Another
	// server, live"); Target names the server it restores onto.
	Live   bool   `json:"live"`
	Target string `json:"target"`
}

type restoreRuntime struct {
	status       string
	message      string
	packagePath  string
	scan         *restoreScan
	items        []restoreItemState
	logs         []logEntry
	nextLog      int
	rollbackPath string
	cancelPath   string
	logFile      *os.File
	cmd          *exec.Cmd
	startedAt    time.Time
	endedAt      time.Time
	finalEvent   string
	cancelAsked  bool
	// passphrase opens the package's migration bundle; it lives in memory
	// from the scan until the restore ends, the package changes or the
	// application closes, and reaches the engine only on standard input.
	passphrase    string
	changes       restoreChanges
	restartNeeded bool
	// remote is set when the package is a live move's copy on another
	// server: the check and the restore run inside that server
	// (servermove_windows.go).
	remote *smRestoreTarget
}

// Caller holds a.mu.
func (a *application) restoreBusyLocked() bool {
	return a.restore.status == "scanning" || a.restore.status == "running"
}

func (a *application) restoreBusy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.restoreBusyLocked()
}

// Caller holds a.mu.
func (a *application) restoreSnapshotLocked() restoreStateResponse {
	status := a.restore.status
	if status == "" {
		status = "idle"
	}
	packages := restorePackages{LoadedReport: a.loadedReportPackage}
	if !a.state.Running && a.state.Status == "succeeded" {
		packages.LastCollection = a.state.PackagePath
	}
	toMillis := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixNano() / int64(time.Millisecond)
	}
	items := append([]restoreItemState{}, a.restore.items...)
	logs := append([]logEntry{}, a.restore.logs...)
	return restoreStateResponse{
		Status:          status,
		Message:         a.restore.message,
		PackagePath:     a.restore.packagePath,
		Packages:        packages,
		Scan:            a.restore.scan,
		Items:           items,
		Logs:            logs,
		RollbackPath:    a.restore.rollbackPath,
		RestartNeeded:   a.restore.restartNeeded,
		HasPassphrase:   a.restore.passphrase != "",
		Live:            a.serverMove.packagePath != "" && strings.EqualFold(a.restore.packagePath, a.serverMove.packagePath),
		Target:          restoreTargetName(a.restore.remote, a.computerName),
		CancelRequested: a.restore.cancelAsked && a.restore.status == "running",
		StartedAtUnixMs: toMillis(a.restore.startedAt),
		EndedAtUnixMs:   toMillis(a.restore.endedAt),
	}
}

// Caller holds a.mu.
func (a *application) appendRestoreLogLocked(text string) {
	entry := logEntry{Index: a.restore.nextLog, Text: text}
	a.restore.nextLog++
	a.restore.logs = append(a.restore.logs, entry)
	if len(a.restore.logs) > restoreLogLimit {
		a.restore.logs = append([]logEntry{}, a.restore.logs[len(a.restore.logs)-restoreLogLimit:]...)
	}
	if a.restore.logFile != nil {
		_, _ = a.restore.logFile.WriteString(text + "\r\n")
	}
}

func (a *application) restoreLog(format string, values ...any) {
	a.mu.Lock()
	a.appendRestoreLogLocked(fmt.Sprintf("%s [GUI] %s", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, values...)))
	a.mu.Unlock()
}

type restoreEvent struct {
	Event     string             `json:"event"`
	Component string             `json:"component"`
	ID        string             `json:"id"`
	Status    string             `json:"status"`
	Message   string             `json:"message"`
	Items     []restoreItemState `json:"items"`
}

var restoreItemStatuses = map[string]bool{"pending": true, "snapshot": true, "restoring": true, "succeeded": true, "failed": true, "skipped": true, "canceled": true}

// Caller holds a.mu. Applies one line of engine output.
func (a *application) applyRestoreLineLocked(line string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	if !strings.HasPrefix(line, restoreEventPrefix) {
		a.appendRestoreLogLocked(line)
		return
	}
	if len(line) > 256*1024 {
		return
	}
	var event restoreEvent
	if json.Unmarshal([]byte(strings.TrimPrefix(line, restoreEventPrefix)), &event) != nil {
		return
	}
	switch event.Event {
	case "plan":
		items := make([]restoreItemState, 0, len(event.Items))
		for _, item := range event.Items {
			items = append(items, restoreItemState{Component: item.Component, ID: item.ID, Title: item.Title, Status: "pending"})
		}
		a.restore.items = items
	case "item":
		if !restoreItemStatuses[event.Status] {
			return
		}
		for index := range a.restore.items {
			if a.restore.items[index].Component == event.Component && a.restore.items[index].ID == event.ID {
				a.restore.items[index].Status = event.Status
				a.restore.items[index].Message = event.Message
				if event.Status == "restoring" {
					a.restore.message = "Restoring " + a.restore.items[index].Title + "."
				}
			}
		}
	case "restart":
		a.restore.restartNeeded = true
	case "done":
		a.restore.finalEvent = event.Status
		if event.Message != "" {
			a.restore.message = event.Message
		}
	}
}

func (a *application) runRestoreEngine(command *exec.Cmd, apply func(string)) error {
	err := a.runEngineProcess(command, apply, func(started *exec.Cmd) {
		a.mu.Lock()
		a.restore.cmd = started
		a.mu.Unlock()
	})
	a.mu.Lock()
	if a.restore.cmd == command {
		a.restore.cmd = nil
	}
	a.mu.Unlock()
	return err
}

// runEngineProcess runs an embedded PowerShell engine hidden, passes each
// output line to apply, and reports the started process to onStart.
func (a *application) runEngineProcess(command *exec.Cmd, apply func(string), onStart func(*exec.Cmd)) error {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	onStart(command)
	var readers sync.WaitGroup
	scan := func(reader io.Reader) {
		defer readers.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			apply(scanner.Text())
		}
	}
	readers.Add(2)
	go scan(stdout)
	go scan(stderr)
	waitErr := command.Wait()
	readers.Wait()
	return waitErr
}

func (a *application) prepareRestoreEngine(prefix string) (directory, script string, err error) {
	return a.prepareRestoreEngineNamed(prefix, restoreEngineName)
}

// prepareRestoreEngineNamed writes an embedded engine script, verified by
// SHA-256, into a new private runtime folder.
func (a *application) prepareRestoreEngineNamed(prefix, engineName string) (directory, script string, err error) {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return "", "", err
	}
	directory, err = os.MkdirTemp(runtimeRoot, prefix)
	if err != nil {
		return "", "", err
	}
	script = filepath.Join(directory, engineName)
	if _, err := writeEmbeddedFile(engineName, script); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	return directory, script, nil
}

func cleanPackagePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("Choose an inventory package folder first.")
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("The package folder must be a full path.")
	}
	value = filepath.Clean(value)
	info, err := os.Stat(value)
	if err != nil || !info.IsDir() {
		return "", errors.New("The package folder was not found. Choose the extracted package folder, not its ZIP file.")
	}
	return value, nil
}

func (a *application) handleRestoreBrowse(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()
	if a.restoreBusy() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The package cannot be changed while a restore is running."})
		return
	}
	selectedPath, selected, err := chooseFolder("Select the inventory package folder to restore from (the folder that contains 00-Summary).")
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": selected, "path": selectedPath})
}

func (a *application) handleRestoreScan(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var input struct {
		PackagePath string `json:"packagePath"`
		// Passphrase: typed now; empty keeps the one held for this package.
		Passphrase string         `json:"passphrase"`
		Changes    restoreChanges `json:"changes"`
		// Live: check the live move's copy again (a changed mapping).
		Live bool `json:"live"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, restoreSecretsRequestLimit)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The restore request was invalid."})
		return
	}
	if input.Live {
		input.Passphrase = ""
		if err := a.startServerMoveScan(input.Changes); err != nil {
			status := http.StatusConflict
			writeJSON(response, status, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
		return
	}
	packagePath, err := cleanPackagePath(input.PackagePath)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	a.mu.Lock()
	if a.restoreBusyLocked() {
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "A restore or package check is already running."})
		return
	}
	if len(input.Passphrase) > bundlePassphraseMax || strings.ContainsAny(input.Passphrase, "\r\n\x00") {
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "That passphrase cannot be a migration bundle passphrase."})
		return
	}
	passphrase := input.Passphrase
	if passphrase == "" && strings.EqualFold(a.restore.packagePath, packagePath) {
		passphrase = a.restore.passphrase
	}
	a.restore = restoreRuntime{status: "scanning", message: "Checking the package and this server.", packagePath: packagePath, passphrase: passphrase}
	a.mu.Unlock()
	input.Passphrase = ""
	go func() {
		defer a.endCriticalOperation()
		scan, err := a.scanRestorePackage(packagePath, passphrase, input.Changes)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.restore.packagePath != packagePath || a.restore.status != "scanning" {
			return
		}
		if err != nil {
			a.restore.status = "scanfailed"
			a.restore.message = err.Error()
			return
		}
		a.restore.status = "ready"
		a.restore.scan = scan
		// A passphrase that does not open the bundle is not kept.
		if scan.Bundle.Passphrase != "ok" {
			a.restore.passphrase = ""
		}
		a.restore.message = "Choose what to restore."
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// scanRestorePackageAt checks a package on this computer (remote nil) or a
// live move's copy inside another server.
func (a *application) scanRestorePackageAt(packagePath, passphrase string, changes restoreChanges, remote *smRestoreTarget) (*restoreScan, error) {
	if remote != nil {
		return a.runRemoteRestoreScan(remote, passphrase, changes)
	}
	return a.scanRestorePackage(packagePath, passphrase, changes)
}

func (a *application) scanRestorePackage(packagePath, passphrase string, changes restoreChanges) (*restoreScan, error) {
	directory, script, err := a.prepareRestoreEngine("Restore-Scan-")
	if err != nil {
		return nil, fmt.Errorf("The restore engine could not be prepared: %v", err)
	}
	defer os.RemoveAll(directory)
	resultPath := filepath.Join(directory, "Scan-Result.json")
	planPath := filepath.Join(directory, "Scan-Changes.json")
	plan, _ := json.Marshal(map[string]any{"mappings": changes})
	if err := os.WriteFile(planPath, plan, 0600); err != nil {
		return nil, fmt.Errorf("The package check could not be prepared: %v", err)
	}
	command := exec.Command(a.powerShellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", script, "-Mode", "Scan", "-PackagePath", packagePath, "-ResultPath", resultPath, "-PlanPath", planPath, "-SecretsFromInput")
	command.Stdin = restoreSecretsInput(passphrase, nil)
	var lastError string
	waitErr := a.runRestoreEngine(command, func(line string) {
		a.mu.Lock()
		a.applyRestoreLineLocked(line)
		a.mu.Unlock()
		if strings.Contains(line, "[ERROR]") {
			if index := strings.Index(line, "[ERROR] "); index >= 0 {
				lastError = line[index+8:]
			}
		}
	})
	if waitErr != nil {
		if lastError != "" {
			return nil, errors.New(lastError)
		}
		return nil, fmt.Errorf("The package could not be checked: %v", waitErr)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, fmt.Errorf("The package check did not produce a result: %v", err)
	}
	return parseRestoreScan(data)
}

func parseRestoreScan(data []byte) (*restoreScan, error) {
	var scan restoreScan
	if err := json.Unmarshal(stripUTF8BOM(data), &scan); err != nil {
		return nil, fmt.Errorf("The package check result was invalid: %v", err)
	}
	known := map[string]bool{}
	for _, id := range restoreComponentIDs {
		known[id] = true
	}
	for index := range scan.Components {
		component := &scan.Components[index]
		if !known[component.ID] {
			return nil, fmt.Errorf("The package check returned an unknown component %q.", component.ID)
		}
		if component.Warnings == nil {
			component.Warnings = []string{}
		}
		if component.Items == nil {
			component.Items = []restoreScanItem{}
		}
	}
	return &scan, nil
}

func (a *application) handleRestoreStart(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var input struct {
		PackagePath         string             `json:"packagePath"`
		Components          []restoreSelection `json:"components"`
		ConfirmComputerName string             `json:"confirmComputerName"`
		Changes             restoreChanges     `json:"changes"`
		Passwords           map[string]string  `json:"passwords"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 512*1024)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The restore request was invalid."})
		return
	}
	a.mu.Lock()
	remote := a.restore.remote
	remoteScan := a.restore.scan
	live := a.serverMoveOwnsLocked(input.PackagePath)
	a.mu.Unlock()
	if remote != nil {
		// A live move restores inside the new server: the check there said
		// whether the account is an administrator, and the confirmation names
		// that server.
		if remoteScan == nil || !live {
			writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Check the package on the new server again before restoring."})
			return
		}
		if !remoteScan.IsAdministrator {
			writeJSON(response, http.StatusForbidden, map[string]any{"ok": false, "error": "The account used for " + remote.Server + " is not an administrator there."})
			return
		}
		if !smSameServer(strings.TrimSpace(input.ConfirmComputerName), remoteScan.CurrentComputer) {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Confirm with the new server's name, " + remoteScan.CurrentComputer + "."})
			return
		}
	} else {
		if !a.admin {
			writeJSON(response, http.StatusForbidden, map[string]any{"ok": false, "error": "Restoring needs administrator rights. Close the application and run it as administrator."})
			return
		}
		if !strings.EqualFold(strings.TrimSpace(input.ConfirmComputerName), a.computerName) {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type this server's name, " + a.computerName + ", to confirm the restore."})
			return
		}
	}
	packagePath := strings.TrimSpace(input.PackagePath)
	if remote == nil {
		var err error
		if packagePath, err = cleanPackagePath(input.PackagePath); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	a.mu.Lock()
	fail := func(status int, message string) {
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	if a.state.Running {
		fail(http.StatusConflict, "A collection is running. Wait for it to finish before restoring.")
		return
	}
	if a.restoreBusyLocked() {
		fail(http.StatusConflict, "A restore or package check is already running.")
		return
	}
	if a.migrateBusyLocked() {
		fail(http.StatusConflict, "A file migration step is running. Wait for it to finish before restoring.")
		return
	}
	if a.vmwareBusyLocked() {
		fail(http.StatusConflict, a.vmwareBusyTextLocked()+" Wait for it to finish before restoring.")
		return
	}
	if a.convertBusyLocked() {
		fail(http.StatusConflict, "A disk conversion is running. Wait for it to finish before restoring.")
		return
	}
	if a.serverMoveBusyLocked() {
		fail(http.StatusConflict, "A server move step is running. Wait for it to finish before restoring.")
		return
	}
	if a.restore.remote != remote {
		fail(http.StatusConflict, "Check the package again before restoring.")
		return
	}
	if a.restore.scan == nil || !strings.EqualFold(a.restore.packagePath, packagePath) {
		fail(http.StatusConflict, "Check this package again before restoring.")
		return
	}
	selections, total, err := validateRestoreSelection(a.restore.scan, input.Components)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	changes, err := validateRestoreChanges(a.restore.scan, input.Changes)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	passwords, err := validateRestorePasswords(a.restore.scan, selections, input.Passwords)
	input.Passwords = nil
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	scan := a.restore.scan
	passphrase := a.restore.passphrase
	stamp := time.Now().Format("20060102_150405")
	rollback := filepath.Join(a.update.appDataRoot, "Restore-Rollback", stamp+"_"+safeName(scan.PackageName))
	if remote != nil {
		// The rollback folder is on the new server (the runner reports it);
		// this computer keeps the log.
		rollback = filepath.Join(a.update.appDataRoot, "Restore-Rollback", stamp+"_"+safeName(scan.PackageName)+"_on_"+safeName(smShortName(remote.Server)))
	}
	a.restore = restoreRuntime{
		remote:       remote,
		status:       "running",
		message:      fmt.Sprintf("Restoring %d item(s).", total),
		packagePath:  packagePath,
		scan:         scan,
		rollbackPath: rollback,
		startedAt:    time.Now(),
		passphrase:   passphrase,
		changes:      changes,
	}
	a.mu.Unlock()
	go func() {
		defer a.endCriticalOperation()
		a.runRestore(packagePath, selections, rollback, passphrase, changes, passwords)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true, "rollbackPath": rollback})
}

func (a *application) finishRestore(status, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.restore.status = status
	// The passphrase is needed again only for another restore, which starts
	// with a new check of the package.
	a.restore.passphrase = ""
	if message != "" {
		a.restore.message = message
	}
	a.restore.endedAt = time.Now()
	for index := range a.restore.items {
		switch a.restore.items[index].Status {
		case "pending":
			a.restore.items[index].Status = "skipped"
			a.restore.items[index].Message = "Not run."
		case "restoring", "snapshot":
			a.restore.items[index].Status = "failed"
			if a.restore.items[index].Message == "" || status != "succeeded" {
				a.restore.items[index].Message = "The restore stopped while this item was running."
			}
		}
	}
	if a.restore.logFile != nil {
		_ = a.restore.logFile.Close()
		a.restore.logFile = nil
	}
}

func (a *application) runRestore(packagePath string, selections []restoreSelection, rollback, passphrase string, changes restoreChanges, passwords map[string]string) {
	if err := os.MkdirAll(rollback, 0700); err != nil {
		a.finishRestore("failed", "The rollback folder could not be created: "+err.Error())
		return
	}
	logFile, err := os.OpenFile(filepath.Join(rollback, "Restore.log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err == nil {
		a.mu.Lock()
		a.restore.logFile = logFile
		a.mu.Unlock()
	}
	directory, script, err := a.prepareRestoreEngine("Restore-")
	if err != nil {
		a.finishRestore("failed", "The restore engine could not be prepared: "+err.Error())
		return
	}
	defer os.RemoveAll(directory)
	plan, _ := json.Marshal(map[string]any{"components": selections, "mappings": changes})
	planPath := filepath.Join(directory, "Restore-Plan.json")
	if err := os.WriteFile(planPath, plan, 0600); err != nil {
		a.finishRestore("failed", "The restore plan could not be written: "+err.Error())
		return
	}
	cancelPath := filepath.Join(directory, "Stop-Requested")
	a.mu.Lock()
	a.restore.cancelPath = cancelPath
	a.mu.Unlock()
	a.restoreLog("Application version: %s", appVersion)
	a.restoreLog("Package: %s", packagePath)
	a.restoreLog("Rollback folder: %s", rollback)
	a.logUpdate("Restore started from %s; rollback folder %s", packagePath, rollback)
	a.mu.Lock()
	remote := a.restore.remote
	a.mu.Unlock()
	if remote != nil {
		a.restoreLog("Restoring onto %s through PowerShell remoting.", remote.Server)
		code, remoteRollback, err := a.runRemoteRestore(remote, map[string]any{"components": selections, "mappings": changes}, passphrase, passwords, cancelPath)
		if remoteRollback != "" {
			a.mu.Lock()
			a.restore.rollbackPath = remoteRollback
			a.mu.Unlock()
			a.restoreLog("Rollback folder on %s: %s", remote.Server, remoteRollback)
		}
		a.finishRestoreRun(code, err)
		return
	}
	command := exec.Command(a.powerShellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", script, "-Mode", "Restore", "-PackagePath", packagePath, "-PlanPath", planPath, "-RollbackPath", rollback, "-CancelPath", cancelPath, "-SecretsFromInput")
	command.Stdin = restoreSecretsInput(passphrase, passwords)
	for key := range passwords {
		delete(passwords, key)
	}
	waitErr := a.runRestoreEngine(command, func(line string) {
		a.mu.Lock()
		a.applyRestoreLineLocked(line)
		a.mu.Unlock()
	})
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	a.finishRestoreRun(exitCode, waitErr)
}

// finishRestoreRun turns the engine's exit code and final event into the
// restore's end state.
func (a *application) finishRestoreRun(exitCode int, waitErr error) {
	a.mu.Lock()
	final := a.restore.finalEvent
	a.mu.Unlock()
	switch {
	case exitCode == 0 && final == "succeeded":
		a.finishRestore("succeeded", "Restore complete. The configuration saved before each change is in the rollback folder.")
	case exitCode == 2 && final == "canceled":
		a.finishRestore("canceled", "Restore stopped. Items already restored stay restored; their previous configuration is in the rollback folder.")
	case exitCode == 1 && final == "failed":
		a.finishRestore("failed", "Restore stopped at a failed item. Review the log; the configuration saved before each change is in the rollback folder.")
	default:
		message := "The restore engine ended unexpectedly."
		if waitErr != nil && exitCode == -1 {
			message = "The restore engine could not run: " + waitErr.Error()
		}
		a.mu.Lock()
		if a.restore.message != "" && final == "failed" {
			message = a.restore.message
		}
		a.mu.Unlock()
		a.finishRestore("failed", message)
	}
	a.mu.Lock()
	status := a.restore.status
	a.mu.Unlock()
	a.logUpdate("Restore finished: %s", status)
}

func (a *application) handleRestoreCancel(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	a.mu.Lock()
	if a.restore.status != "running" || a.restore.cancelPath == "" {
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "No restore is running."})
		return
	}
	cancelPath := a.restore.cancelPath
	a.restore.cancelAsked = true
	a.restore.message = "Stopping after the current item finishes."
	a.appendRestoreLogLocked(time.Now().Format("2006-01-02 15:04:05") + " [GUI] Stop requested; the restore stops after the current item.")
	a.mu.Unlock()
	// Stopping mid-item could leave a component half restored, so the engine
	// checks this marker between items instead of being terminated.
	if err := os.WriteFile(cancelPath, []byte("stop"), 0600); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The stop request could not be recorded: " + err.Error()})
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// stopRestoreAndWait asks a running restore to stop after its current item and
// waits for the engine to exit, so closing the application never abandons a
// component half restored.
func (a *application) stopRestoreAndWait() {
	a.mu.Lock()
	running := a.restore.status == "running"
	cancelPath := a.restore.cancelPath
	a.mu.Unlock()
	if !running {
		return
	}
	a.logUpdate("The application is closing during a restore; stopping after the current item.")
	if cancelPath != "" {
		_ = os.WriteFile(cancelPath, []byte("stop"), 0600)
	}
	for {
		time.Sleep(250 * time.Millisecond)
		a.mu.Lock()
		running = a.restore.status == "running"
		if running && a.restore.cancelPath != "" && cancelPath == "" {
			cancelPath = a.restore.cancelPath
			_ = os.WriteFile(cancelPath, []byte("stop"), 0600)
		}
		a.mu.Unlock()
		if !running {
			return
		}
	}
}

// restoreSecretsInput is the one JSON line the engine reads with
// -SecretsFromInput.
func restoreSecretsInput(passphrase string, passwords map[string]string) io.Reader {
	if passwords == nil {
		passwords = map[string]string{}
	}
	data, _ := json.Marshal(map[string]any{"passphrase": passphrase, "passwords": passwords})
	return strings.NewReader(string(data) + "\n")
}

// restoreTargetName is the server a restore changes.
func restoreTargetName(remote *smRestoreTarget, here string) string {
	if remote != nil {
		return remote.Server
	}
	return here
}
