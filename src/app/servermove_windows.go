//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The Windows side of moving a server live (servermove.go, AGENTS rule 75).
// Every step runs Invoke-ServerMoveRemote.ps1 (signed, RemoteSigned) with
// the account and secrets on standard input, or Robocopy between the two
// servers' administrative shares. A step is a critical operation: updates
// wait for it and it cannot start while a collection, restore, file
// migration, VMware step or conversion runs.

type smRuntime struct {
	status       string
	step         string
	message      string
	old          string
	new          string
	newIsHere    bool
	oldAccount   smAccount
	newAccount   smAccount
	includeFiles bool
	limitGB      int
	packageName  string
	oldPackage   string
	packagePath  string
	readAt       time.Time
	read         *smReadResult
	stoppedSites []string
	logs         []logEntry
	nextLog      int
	// passphrase seals this read's bundle secrets; memory only.
	passphrase string
}

// smStateResponse is the live move part of the state the page reads. It
// never carries a password or the passphrase.
type smStateResponse struct {
	Status       string        `json:"status"`
	Step         string        `json:"step"`
	Message      string        `json:"message"`
	Old          string        `json:"old"`
	New          string        `json:"new"`
	NewIsHere    bool          `json:"newIsHere"`
	OldUser      string        `json:"oldUser"`
	NewUser      string        `json:"newUser"`
	IncludeFiles bool          `json:"includeFiles"`
	LimitGB      int           `json:"limitGB"`
	PackageName  string        `json:"packageName"`
	PackagePath  string        `json:"packagePath"`
	ReadAt       string        `json:"readAt"`
	Read         *smReadResult `json:"read"`
	Sites        []string      `json:"sites"`
	StoppedSites []string      `json:"stoppedSites"`
	Logs         []logEntry    `json:"logs"`
}

// smRestoreTarget is a restore that runs inside another server.
type smRestoreTarget struct {
	Server      string
	Account     smAccount
	WorkName    string
	PackageName string
}

// Caller holds a.mu.
func (a *application) serverMoveBusyLocked() bool {
	switch a.serverMove.status {
	case "reading", "copying", "sites", "cleaning":
		return true
	}
	return false
}

// Caller holds a.mu.
func (a *application) serverMoveSnapshotLocked() smStateResponse {
	m := a.serverMove
	status := m.status
	if status == "" {
		status = "idle"
	}
	readAt := ""
	if !m.readAt.IsZero() {
		readAt = m.readAt.Format("2006-01-02 15:04:05")
	}
	sites := []string{}
	if m.read != nil {
		sites = append(sites, m.read.SiteNames...)
	}
	return smStateResponse{
		Status: status, Step: m.step, Message: m.message, Old: m.old, New: m.new, NewIsHere: m.newIsHere,
		OldUser: m.oldAccount.User, NewUser: m.newAccount.User, IncludeFiles: m.includeFiles, LimitGB: m.limitGB,
		PackageName: m.packageName, PackagePath: m.packagePath, ReadAt: readAt, Read: m.read, Sites: sites,
		StoppedSites: append([]string{}, m.stoppedSites...), Logs: append([]logEntry{}, m.logs...),
	}
}

// Caller holds a.mu.
func (a *application) serverMoveLogLocked(text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" || strings.HasPrefix(text, restoreEventPrefix) {
		return
	}
	a.serverMove.nextLog++
	a.serverMove.logs = append(a.serverMove.logs, logEntry{Index: a.serverMove.nextLog, Text: text})
	if len(a.serverMove.logs) > restoreLogLimit {
		a.serverMove.logs = append([]logEntry{}, a.serverMove.logs[len(a.serverMove.logs)-restoreLogLimit:]...)
	}
}

func (a *application) serverMoveLog(format string, values ...any) {
	a.mu.Lock()
	a.serverMoveLogLocked(time.Now().Format("2006-01-02 15:04:05") + " [GUI] " + fmt.Sprintf(format, values...))
	a.mu.Unlock()
}

// runServerMoveRunner runs one runner step against server and returns its
// reply. Lines go to apply.
func (a *application) runServerMoveRunner(server string, account smAccount, mode, workName, packageName, enginePath string, plan any, extra []string, input []byte, cancelPath string, apply func(string)) (smRunnerReply, error) {
	var reply smRunnerReply
	directory, runner, err := a.prepareRestoreEngineNamed("Server-Move-", smRunnerName)
	if err != nil {
		return reply, fmt.Errorf("the move could not be prepared: %v", err)
	}
	defer os.RemoveAll(directory)
	resultPath := filepath.Join(directory, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", runner,
		"-Target", server, "-Mode", mode, "-ResultPath", resultPath, "-WorkName", workName, "-InputFromStdin"}
	if enginePath != "" {
		arguments = append(arguments, "-EnginePath", enginePath)
	}
	if packageName != "" {
		arguments = append(arguments, "-PackageName", packageName)
	}
	if plan != nil {
		data, _ := json.Marshal(plan)
		planPath := filepath.Join(directory, "Plan.json")
		if err := os.WriteFile(planPath, data, 0600); err != nil {
			return reply, err
		}
		arguments = append(arguments, "-PlanPath", planPath)
	}
	if cancelPath != "" {
		arguments = append(arguments, "-CancelPath", cancelPath)
	}
	arguments = append(arguments, extra...)
	command := exec.Command(a.powerShellPath, arguments...)
	command.Stdin = bytes.NewReader(input)
	var lastError string
	waitErr := a.runEngineProcess(command, func(line string) {
		if index := strings.Index(line, "[ERROR] "); index >= 0 {
			lastError = line[index+8:]
		}
		apply(line)
	}, func(*exec.Cmd) {})
	for index := range input {
		input[index] = 0
	}
	data, readErr := os.ReadFile(resultPath)
	if readErr == nil {
		_ = json.Unmarshal(stripUTF8BOM(data), &reply)
	}
	if waitErr != nil && readErr != nil {
		if lastError != "" {
			return reply, errors.New(lastError)
		}
		return reply, fmt.Errorf("the step on %s did not finish: %v", server, waitErr)
	}
	if waitErr != nil && mode != "Scan" && mode != "Restore" && mode != "Read" {
		if lastError != "" {
			return reply, errors.New(lastError)
		}
		return reply, fmt.Errorf("the step on %s failed: %v", server, waitErr)
	}
	return reply, nil
}

func (a *application) handleServerMoveRead(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var input struct {
		Old          string `json:"old"`
		OldUser      string `json:"oldUser"`
		OldPassword  string `json:"oldPassword"`
		NewIsHere    bool   `json:"newIsHere"`
		New          string `json:"new"`
		NewUser      string `json:"newUser"`
		NewPassword  string `json:"newPassword"`
		KeepAccounts bool   `json:"keepAccounts"`
		IncludeFiles bool   `json:"includeFiles"`
		LimitGB      int    `json:"limitGB"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 64*1024)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The move request was invalid."})
		return
	}
	defer func() { input.OldPassword, input.NewPassword = "", "" }()
	old, err := smCleanServer(input.Old, "old")
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	newServer := ""
	if input.NewIsHere {
		newServer = a.computerName
	} else if newServer, err = smCleanServer(input.New, "new"); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if smSameServer(old, newServer) || (input.NewIsHere && smSameServer(old, a.computerName)) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The old and new server must be different servers."})
		return
	}
	if input.NewIsHere && !a.admin {
		writeJSON(response, http.StatusForbidden, map[string]any{"ok": false, "error": "Restoring onto this server needs administrator rights. Close the application and run it as administrator."})
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	a.mu.Lock()
	if refusal := a.serverMoveRefusalLocked(); refusal != "" {
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": refusal})
		return
	}
	oldAccount := smAccount{User: strings.TrimSpace(input.OldUser), Password: input.OldPassword}
	newAccount := smAccount{User: strings.TrimSpace(input.NewUser), Password: input.NewPassword}
	// An empty password with the same user keeps the account already held
	// for the same server, so a second read needs no typing.
	same := smSameServer(a.serverMove.old, old)
	if input.KeepAccounts && same && oldAccount.User != "" && oldAccount.Password == "" && strings.EqualFold(a.serverMove.oldAccount.User, oldAccount.User) {
		oldAccount = a.serverMove.oldAccount
	}
	if input.KeepAccounts && smSameServer(a.serverMove.new, newServer) && newAccount.User != "" && newAccount.Password == "" && strings.EqualFold(a.serverMove.newAccount.User, newAccount.User) {
		newAccount = a.serverMove.newAccount
	}
	if input.NewIsHere {
		newAccount = smAccount{}
	}
	passphrase, err := smNewPassphrase()
	if err != nil {
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "A passphrase could not be made: " + err.Error()})
		return
	}
	packageName := a.serverMove.packageName
	if !same || packageName == "" || !smSameServer(a.serverMove.new, newServer) {
		packageName = smPackageName(old, time.Now())
	}
	logs, next := a.serverMove.logs, a.serverMove.nextLog
	a.serverMove = smRuntime{
		status: "reading", step: "read", message: "Reading " + old + ".", old: old, new: newServer, newIsHere: input.NewIsHere,
		oldAccount: oldAccount, newAccount: newAccount, includeFiles: input.IncludeFiles, limitGB: smCheckLimit(input.LimitGB),
		packageName: packageName, passphrase: passphrase, logs: logs, nextLog: next,
	}
	// A new read starts a new check of the package.
	if !a.restoreBusyLocked() {
		a.restore = restoreRuntime{}
	}
	a.mu.Unlock()
	go func() {
		defer a.endCriticalOperation()
		a.runServerMoveRead()
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// Caller holds a.mu.
func (a *application) serverMoveRefusalLocked() string {
	switch {
	case a.serverMoveBusyLocked():
		return "A server move step is already running."
	case a.state.Running:
		return "A collection is running. Wait for it to finish first."
	case a.restoreBusyLocked():
		return "A restore or package check is running. Wait for it to finish first."
	case a.migrateBusyLocked():
		return "A file migration step is running. Wait for it to finish first."
	case a.vmwareBusyLocked():
		return a.vmwareBusyTextLocked() + " Wait for it to finish first."
	case a.convertBusyLocked():
		return "A disk conversion is running. Wait for it to finish first."
	}
	return ""
}

func (a *application) serverMoveFail(message string) {
	a.mu.Lock()
	a.serverMove.status = "failed"
	a.serverMove.message = message
	a.serverMoveLogLocked(time.Now().Format("2006-01-02 15:04:05") + " [ERROR] " + message)
	a.mu.Unlock()
}

func (a *application) runServerMoveRead() {
	a.mu.Lock()
	m := a.serverMove
	a.mu.Unlock()
	work := smWorkName(m.old, m.new)
	apply := func(line string) { a.mu.Lock(); a.serverMoveLogLocked(line); a.mu.Unlock() }
	directory, engine, err := a.prepareRestoreEngineNamed("Server-Move-Bundle-", bundleEngineName)
	if err != nil {
		a.serverMoveFail("The bundle engine could not be prepared: " + err.Error())
		return
	}
	defer os.RemoveAll(directory)
	a.serverMoveLog("Reading %s: IIS configuration, website files, certificates, accounts and the other roles a migration bundle carries.", m.old)
	extra := []string{"-WebContentLimitGB", fmt.Sprint(m.limitGB)}
	if m.includeFiles {
		extra = append(extra, "-IncludeWebContent")
	}
	reply, err := a.runServerMoveRunner(m.old, m.oldAccount, "Read", work, m.packageName, engine, nil, extra, smRunnerInput(m.oldAccount, m.passphrase, nil), "", apply)
	if err != nil {
		a.serverMoveFail("Reading " + m.old + " failed: " + err.Error())
		return
	}
	read, err := smParseRead(reply)
	if err != nil {
		a.serverMoveFail("Reading " + m.old + " failed: " + err.Error())
		return
	}
	a.mu.Lock()
	a.serverMove.read = read
	a.serverMove.oldPackage = reply.Package
	a.serverMove.readAt = time.Now()
	a.serverMove.status = "copying"
	a.serverMove.step = "copy"
	a.serverMove.message = "Copying the package to " + m.new + "."
	a.mu.Unlock()

	// Where the package goes on the new server.
	destination, destinationAdmin := "", ""
	if m.newIsHere {
		destination = filepath.Join(a.update.appDataRoot, "Server-Move", work, "Package", m.packageName)
		destinationAdmin = destination
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			a.serverMoveFail("The package folder could not be made here: " + err.Error())
			return
		}
	} else {
		prepared, err := a.runServerMoveRunner(m.new, m.newAccount, "Prepare", work, "", "", nil, nil, smRunnerInput(m.newAccount, "", nil), "", apply)
		if err != nil {
			a.serverMoveFail("Preparing " + m.new + " failed: " + err.Error())
			return
		}
		destination = strings.TrimRight(prepared.Package, `\`) + `\` + m.packageName
		if destinationAdmin, err = smAdminPath(m.new, destination); err != nil {
			a.serverMoveFail("The package folder on " + m.new + " is not usable: " + err.Error())
			return
		}
	}
	source, err := smAdminPath(m.old, reply.Package)
	if err != nil {
		a.serverMoveFail("The package folder on " + m.old + " is not usable: " + err.Error())
		return
	}
	if err := a.serverMoveCopy(m, source, destinationAdmin); err != nil {
		a.serverMoveFail("Copying the package failed: " + err.Error())
		return
	}
	a.mu.Lock()
	a.serverMove.packagePath = destination
	a.serverMove.status = "ready"
	a.serverMove.step = "check"
	a.serverMove.message = "Copied. Checking the package on " + m.new + "."
	a.mu.Unlock()
	a.serverMoveLog("The package is on %s. Checking what can be restored there.", m.new)
	if err := a.startServerMoveScan(restoreChanges{}); err != nil {
		a.serverMoveFail(err.Error())
	}
}

// serverMoveCopy copies the package with Robocopy between the servers'
// administrative shares, with the accounts typed for each (or this Windows
// account). The files go through this computer when it is neither server.
func (a *application) serverMoveCopy(m smRuntime, source, destination string) error {
	closeOld, err := replConnectIPC(m.old, m.oldAccount.User, m.oldAccount.Password)
	if err != nil {
		return err
	}
	defer closeOld()
	if !m.newIsHere {
		closeNew, err := replConnectIPC(m.new, m.newAccount.User, m.newAccount.Password)
		if err != nil {
			return err
		}
		defer closeNew()
	}
	robocopy := filepath.Join(os.Getenv("SystemRoot"), "System32", "robocopy.exe")
	a.serverMoveLog("Copying %s to %s.", source, destination)
	command := exec.Command(robocopy, smRobocopyArgs(source, destination)...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	output, runErr := command.CombinedOutput()
	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return runErr
		}
		code = exitErr.ExitCode()
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) != "" {
			a.serverMoveLog("%s", strings.TrimSpace(line))
		}
	}
	if !smRobocopyOK(code) {
		return fmt.Errorf("Robocopy exit code %d", code)
	}
	return nil
}

// startServerMoveScan checks the copied package on the new server with the
// restore engine; the Restore tab shows the result like any package check.
func (a *application) startServerMoveScan(changes restoreChanges) error {
	if !a.beginCriticalOperation() {
		return errors.New("An update is being installed.")
	}
	a.mu.Lock()
	m := a.serverMove
	if m.packagePath == "" {
		a.mu.Unlock()
		a.endCriticalOperation()
		return errors.New("Read the old server first.")
	}
	if a.restoreBusyLocked() {
		a.mu.Unlock()
		a.endCriticalOperation()
		return errors.New("A restore or package check is already running.")
	}
	remote := a.serverMoveTargetLocked()
	a.restore = restoreRuntime{status: "scanning", message: "Checking the package on " + m.new + ".", packagePath: m.packagePath, passphrase: m.passphrase, remote: remote}
	a.mu.Unlock()
	go func() {
		defer a.endCriticalOperation()
		scan, err := a.scanRestorePackageAt(m.packagePath, m.passphrase, changes, remote)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.restore.packagePath != m.packagePath || a.restore.status != "scanning" {
			return
		}
		if err != nil {
			a.restore.status = "scanfailed"
			a.restore.message = err.Error()
			return
		}
		a.restore.status = "ready"
		a.restore.scan = scan
		a.restore.message = "Choose what to restore."
	}()
	return nil
}

// Caller holds a.mu. nil when the new server is this computer.
func (a *application) serverMoveTargetLocked() *smRestoreTarget {
	m := a.serverMove
	if m.newIsHere {
		return nil
	}
	return &smRestoreTarget{Server: m.new, Account: m.newAccount, WorkName: smWorkName(m.old, m.new), PackageName: m.packageName}
}

// Caller holds a.mu. Whether packagePath is the live move's package.
func (a *application) serverMoveOwnsLocked(packagePath string) bool {
	return a.serverMove.packagePath != "" && strings.EqualFold(a.serverMove.packagePath, packagePath) && a.serverMove.status == "ready"
}

// runRemoteRestoreScan checks the package inside the new server.
func (a *application) runRemoteRestoreScan(target *smRestoreTarget, passphrase string, changes restoreChanges) (*restoreScan, error) {
	directory, engine, err := a.prepareRestoreEngine("Restore-Scan-")
	if err != nil {
		return nil, fmt.Errorf("The restore engine could not be prepared: %v", err)
	}
	defer os.RemoveAll(directory)
	apply := func(line string) { a.mu.Lock(); a.applyRestoreLineLocked(line); a.mu.Unlock() }
	reply, err := a.runServerMoveRunner(target.Server, target.Account, "Scan", target.WorkName, target.PackageName, engine, map[string]any{"mappings": changes}, nil, smRunnerInput(target.Account, passphrase, nil), "", apply)
	if err != nil {
		return nil, fmt.Errorf("The package could not be checked on %s: %v", target.Server, err)
	}
	if reply.ExitCode != 0 || strings.TrimSpace(reply.Result) == "" {
		return nil, fmt.Errorf("The package could not be checked on %s. See the log.", target.Server)
	}
	return parseRestoreScan([]byte(reply.Result))
}

// runRemoteRestore restores inside the new server. Lines carry the engine's
// @@RESTORE@@ events, so the Restore tab follows it as it does locally.
func (a *application) runRemoteRestore(target *smRestoreTarget, plan map[string]any, passphrase string, passwords map[string]string, cancelPath string) (int, string, error) {
	directory, engine, err := a.prepareRestoreEngine("Restore-")
	if err != nil {
		return -1, "", fmt.Errorf("The restore engine could not be prepared: %v", err)
	}
	defer os.RemoveAll(directory)
	apply := func(line string) { a.mu.Lock(); a.applyRestoreLineLocked(line); a.mu.Unlock() }
	input := smRunnerInput(target.Account, passphrase, passwords)
	for key := range passwords {
		delete(passwords, key)
	}
	reply, err := a.runServerMoveRunner(target.Server, target.Account, "Restore", target.WorkName, target.PackageName, engine, plan, nil, input, cancelPath, apply)
	if err != nil {
		return -1, "", err
	}
	rollback := reply.Rollback
	if rollback != "" {
		if admin, err := smAdminPath(target.Server, rollback); err == nil {
			rollback = admin
		}
	}
	return reply.ExitCode, rollback, nil
}

func (a *application) handleServerMoveSites(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var input struct {
		Action  string   `json:"action"`
		Sites   []string `json:"sites"`
		Confirm string   `json:"confirm"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 64*1024)).Decode(&input); err != nil || (input.Action != "stop" && input.Action != "start") {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The request was invalid."})
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	a.mu.Lock()
	m := a.serverMove
	fail := func(status int, message string) {
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	if m.old == "" || m.read == nil {
		fail(http.StatusConflict, "Read the old server first.")
		return
	}
	if !smSameServer(strings.TrimSpace(input.Confirm), m.old) {
		fail(http.StatusBadRequest, "Confirm with the old server's name, "+m.old+".")
		return
	}
	if a.serverMoveBusyLocked() {
		fail(http.StatusConflict, "A server move step is already running.")
		return
	}
	names := []string{}
	if input.Action == "stop" {
		known := map[string]string{}
		for _, name := range m.read.SiteNames {
			known[strings.ToLower(name)] = name
		}
		for _, name := range smSitesFromScan(input.Sites) {
			if found, ok := known[strings.ToLower(name)]; ok {
				names = append(names, found)
			}
		}
		if len(input.Sites) == 0 {
			names = append(names, m.read.SiteNames...)
		}
	} else {
		names = append(names, m.stoppedSites...)
	}
	if len(names) == 0 {
		fail(http.StatusBadRequest, map[bool]string{true: "There are no IIS sites to stop.", false: "No sites were stopped by this move."}[input.Action == "stop"])
		return
	}
	previous := a.serverMove.status
	a.serverMove.status = "sites"
	a.mu.Unlock()
	go func() {
		defer a.endCriticalOperation()
		mode := "StopSites"
		if input.Action == "start" {
			mode = "StartSites"
		}
		apply := func(line string) { a.mu.Lock(); a.serverMoveLogLocked(line); a.mu.Unlock() }
		reply, err := a.runServerMoveRunner(m.old, m.oldAccount, mode, smWorkName(m.old, m.new), "", "", map[string]any{"sites": names}, nil, smRunnerInput(m.oldAccount, "", nil), "", apply)
		a.mu.Lock()
		defer a.mu.Unlock()
		a.serverMove.status = previous
		if input.Action == "stop" {
			a.serverMove.stoppedSites = smSitesFromScan(append(a.serverMove.stoppedSites, reply.Changed...))
		} else {
			left := []string{}
			started := map[string]bool{}
			for _, name := range reply.Changed {
				started[strings.ToLower(name)] = true
			}
			// Every site starts again on success; after a failure only the
			// ones that did not start stay listed.
			for _, name := range a.serverMove.stoppedSites {
				if err != nil && !started[strings.ToLower(name)] {
					left = append(left, name)
				}
			}
			a.serverMove.stoppedSites = left
		}
		if err != nil {
			a.serverMove.message = err.Error()
			a.serverMoveLogLocked(time.Now().Format("2006-01-02 15:04:05") + " [ERROR] " + err.Error())
			return
		}
		if input.Action == "stop" {
			a.serverMove.message = fmt.Sprintf("Stopped %d site(s) on %s.", len(reply.Changed), m.old)
		} else {
			a.serverMove.message = fmt.Sprintf("Started %d site(s) on %s again.", len(reply.Changed), m.old)
		}
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleServerMoveForget(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	a.mu.Lock()
	if a.serverMoveBusyLocked() || (a.restoreBusyLocked() && a.restore.remote != nil) {
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the running step to finish first."})
		return
	}
	m := a.serverMove
	if len(m.stoppedSites) > 0 {
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Sites this move stopped on " + m.old + " are still stopped. Start them again, or keep the move until the new server is in use."})
		return
	}
	a.serverMove = smRuntime{}
	if a.restore.remote != nil || (m.packagePath != "" && strings.EqualFold(a.restore.packagePath, m.packagePath)) {
		a.restore = restoreRuntime{}
	}
	a.mu.Unlock()
	// The packages hold the website files and sealed secrets; remove both
	// copies (best effort). Rollback folders stay on the new server.
	if m.old != "" {
		go func() {
			work := smWorkName(m.old, m.new)
			discard := func(string) {}
			_, _ = a.runServerMoveRunner(m.old, m.oldAccount, "Clean", work, "", "", nil, nil, smRunnerInput(m.oldAccount, "", nil), "", discard)
			if m.newIsHere {
				_ = os.RemoveAll(filepath.Join(a.update.appDataRoot, "Server-Move", work, "Package"))
			} else if m.new != "" {
				_, _ = a.runServerMoveRunner(m.new, m.newAccount, "Clean", work, "", "", nil, nil, smRunnerInput(m.newAccount, "", nil), "", discard)
			}
		}()
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}
