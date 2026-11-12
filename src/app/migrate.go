//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// File share migration: the embedded Migrate-FileShares.ps1 discovers the old
// server's shares, checks the plan, copies with Robocopy (repeatable), and at
// cutover creates the shares here. This file owns the plan, the per-share
// progress, the saved state that lets a copy resume after the application
// restarts, and the API the Migrate tab uses. Every step is a critical
// operation, so updates cannot install and collections and restores cannot
// start while one runs.

const (
	migrateEngineName  = "Migrate-FileShares.ps1"
	migrateEventPrefix = "@@MIGRATE@@"
)

type migrateAccess struct {
	Account string `json:"account"`
	Type    string `json:"type"`
	Right   string `json:"right"`
}

type migrateSourceShare struct {
	Name                  string          `json:"name"`
	Path                  string          `json:"path"`
	Description           string          `json:"description"`
	System                bool            `json:"system"`
	RemotePath            string          `json:"remotePath"`
	FolderEnumerationMode string          `json:"folderEnumerationMode"`
	CachingMode           string          `json:"cachingMode"`
	EncryptData           bool            `json:"encryptData"`
	ConcurrentUserLimit   uint32          `json:"concurrentUserLimit"`
	Access                []migrateAccess `json:"access"`
	Warnings              []string        `json:"warnings"`
}

type migrateVolume struct {
	Drive     string `json:"drive"`
	FreeBytes uint64 `json:"freeBytes"`
	SizeBytes uint64 `json:"sizeBytes"`
}

type migrateDiscovery struct {
	Source          string               `json:"source"`
	SourceFqdn      string               `json:"sourceFqdn"`
	CurrentComputer string               `json:"currentComputer"`
	SameServer      bool                 `json:"sameServer"`
	IsAdministrator bool                 `json:"isAdministrator"`
	Shares          []migrateSourceShare `json:"shares"`
	LocalShares     []string             `json:"localShares"`
	Volumes         []migrateVolume      `json:"volumes"`
	Warnings        []string             `json:"warnings"`
}

type migratePrecheck struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
	Files    uint64   `json:"files"`
	Bytes    uint64   `json:"bytes"`
}

type migrateShareState struct {
	Name             string           `json:"name"`
	DestName         string           `json:"destName"`
	DestPath         string           `json:"destPath"`
	Status           string           `json:"status"`
	Message          string           `json:"message"`
	Percent          int              `json:"percent"`
	Processed        uint64           `json:"processed"`
	Expected         uint64           `json:"expected"`
	Precheck         *migratePrecheck `json:"precheck"`
	PreviouslySynced bool             `json:"previouslySynced"`
	LastSyncUnixMs   int64            `json:"lastSyncUnixMs"`
	CreatedShare     bool             `json:"createdShare"`
	Verified         bool             `json:"verified"`
}

type migrateSpace struct {
	Drive       string `json:"drive"`
	NeededBytes uint64 `json:"neededBytes"`
	FreeBytes   uint64 `json:"freeBytes"`
	OK          bool   `json:"ok"`
}

type migrateDfsTarget struct {
	NamespacePath string `json:"namespacePath"`
	Target        string `json:"target"`
	Suggested     string `json:"suggested"`
}

type migrateDfs struct {
	Available bool               `json:"available"`
	Message   string             `json:"message"`
	Targets   []migrateDfsTarget `json:"targets"`
}

type migrateAlias struct {
	Possible        bool     `json:"possible"`
	Reasons         []string `json:"reasons"`
	OldName         string   `json:"oldName"`
	OldFqdn         string   `json:"oldFqdn"`
	ThisFqdn        string   `json:"thisFqdn"`
	Added           string   `json:"added"`
	RestartRequired bool     `json:"restartRequired"`
}

// migrateSaved is written to Migrations\<source>\state.json so a migration
// can continue after the application restarts.
type migrateSaved struct {
	Source         string              `json:"source"`
	Plan           []migrateShareState `json:"plan"`
	SourceReadOnly bool                `json:"sourceReadOnly"`
	Alias          string              `json:"alias"`
}

type migrateRuntime struct {
	action          string
	status          string
	message         string
	source          string
	discovery       *migrateDiscovery
	plan            []migrateShareState
	space           []migrateSpace
	dfs             *migrateDfs
	alias           *migrateAlias
	sourceReadOnly  bool
	aliasAdded      string
	prechecked      bool
	logs            []logEntry
	nextLog         int
	cancelPath      string
	cancelRequested bool
	logFile         *os.File
	startedAt       time.Time
	endedAt         time.Time
}

type migrateStateResponse struct {
	Running         bool                `json:"running"`
	Action          string              `json:"action"`
	Status          string              `json:"status"`
	Message         string              `json:"message"`
	Source          string              `json:"source"`
	Discovery       *migrateDiscovery   `json:"discovery"`
	Plan            []migrateShareState `json:"plan"`
	Space           []migrateSpace      `json:"space"`
	Prechecked      bool                `json:"prechecked"`
	CanCopy         bool                `json:"canCopy"`
	CanCutover      bool                `json:"canCutover"`
	Dfs             *migrateDfs         `json:"dfs"`
	Alias           *migrateAlias       `json:"alias"`
	SourceReadOnly  bool                `json:"sourceReadOnly"`
	AliasAdded      string              `json:"aliasAdded"`
	Logs            []logEntry          `json:"logs"`
	WorkPath        string              `json:"workPath"`
	CancelRequested bool                `json:"cancelRequested"`
	StartedAtUnixMs int64               `json:"startedAtUnixMs"`
	EndedAtUnixMs   int64               `json:"endedAtUnixMs"`
}

var migrateSourcePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$`)
var migrateShareNamePattern = regexp.MustCompile(`^[^\\/:*?"<>|\[\]+=;,]{1,80}$`)
var migrateDestPathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^<>"|?*]+$`)

func (a *application) migrateRoot() string {
	return filepath.Join(a.update.appDataRoot, "Migrations")
}

func (a *application) migrateWorkPath(source string) string {
	if source == "" {
		return ""
	}
	return filepath.Join(a.migrateRoot(), safeName(strings.ToUpper(source)))
}

// Caller holds a.mu.
func (a *application) migrateBusyLocked() bool { return a.migrate.action != "" }

func (a *application) migrateBusy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.migrateBusyLocked()
}

// Caller holds a.mu.
func (a *application) migrateCanCopyLocked() bool {
	if !a.migrate.prechecked || len(a.migrate.plan) == 0 {
		return false
	}
	for _, share := range a.migrate.plan {
		if share.Precheck == nil || !share.Precheck.OK {
			return false
		}
	}
	return true
}

// Caller holds a.mu.
func (a *application) migrateCanCutoverLocked() bool {
	if !a.migrateCanCopyLocked() {
		return false
	}
	for _, share := range a.migrate.plan {
		if !share.PreviouslySynced {
			return false
		}
	}
	return true
}

// Caller holds a.mu.
func (a *application) migrateSnapshotLocked() migrateStateResponse {
	toMillis := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixNano() / int64(time.Millisecond)
	}
	status := a.migrate.status
	if status == "" {
		status = "idle"
	}
	return migrateStateResponse{
		Running:         a.migrate.action != "",
		Action:          a.migrate.action,
		Status:          status,
		Message:         a.migrate.message,
		Source:          a.migrate.source,
		Discovery:       a.migrate.discovery,
		Plan:            append([]migrateShareState{}, a.migrate.plan...),
		Space:           append([]migrateSpace{}, a.migrate.space...),
		Prechecked:      a.migrate.prechecked,
		CanCopy:         a.migrateCanCopyLocked(),
		CanCutover:      a.migrateCanCutoverLocked(),
		Dfs:             a.migrate.dfs,
		Alias:           a.migrate.alias,
		SourceReadOnly:  a.migrate.sourceReadOnly,
		AliasAdded:      a.migrate.aliasAdded,
		Logs:            append([]logEntry{}, a.migrate.logs...),
		WorkPath:        a.migrateWorkPath(a.migrate.source),
		CancelRequested: a.migrate.cancelRequested && a.migrate.action != "",
		StartedAtUnixMs: toMillis(a.migrate.startedAt),
		EndedAtUnixMs:   toMillis(a.migrate.endedAt),
	}
}

// Caller holds a.mu.
func (a *application) appendMigrateLogLocked(text string) {
	a.migrate.logs = append(a.migrate.logs, logEntry{Index: a.migrate.nextLog, Text: text})
	a.migrate.nextLog++
	if len(a.migrate.logs) > restoreLogLimit {
		a.migrate.logs = append([]logEntry{}, a.migrate.logs[len(a.migrate.logs)-restoreLogLimit:]...)
	}
	if a.migrate.logFile != nil {
		_, _ = a.migrate.logFile.WriteString(text + "\r\n")
	}
}

type migrateEvent struct {
	Event     string   `json:"event"`
	Share     string   `json:"share"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	Processed uint64   `json:"processed"`
	Expected  uint64   `json:"expected"`
	Percent   int      `json:"percent"`
	Files     []uint64 `json:"files"`
}

var migrateShareStatuses = map[string]bool{"checking": true, "ready": true, "blocked": true, "copying": true, "succeeded": true, "failed": true, "skipped": true, "canceled": true, "sharing": true, "verifying": true, "done": true, "attention": true, "readonly": true, "restored": true}

// Caller holds a.mu. Applies one line of engine output.
func (a *application) applyMigrateLineLocked(line string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	if !strings.HasPrefix(line, migrateEventPrefix) {
		a.appendMigrateLogLocked(line)
		return
	}
	if len(line) > 256*1024 {
		return
	}
	var event migrateEvent
	if json.Unmarshal([]byte(strings.TrimPrefix(line, migrateEventPrefix)), &event) != nil {
		return
	}
	switch event.Event {
	case "error":
		a.migrate.message = event.Message
	case "progress", "share":
		for index := range a.migrate.plan {
			share := &a.migrate.plan[index]
			if share.Name != event.Share {
				continue
			}
			if event.Event == "progress" {
				share.Processed, share.Expected, share.Percent = event.Processed, event.Expected, event.Percent
				continue
			}
			if !migrateShareStatuses[event.Status] {
				return
			}
			share.Status, share.Message = event.Status, event.Message
			if event.Status == "copying" {
				share.Percent, share.Processed = 0, 0
			}
		}
	}
}

func (a *application) migrateLog(format string, values ...any) {
	a.mu.Lock()
	a.appendMigrateLogLocked(fmt.Sprintf("%s [GUI] %s", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, values...)))
	a.mu.Unlock()
}

// Caller holds a.mu.
func (a *application) migrateSaveLocked() {
	work := a.migrateWorkPath(a.migrate.source)
	if work == "" {
		return
	}
	data, err := json.MarshalIndent(migrateSaved{Source: a.migrate.source, Plan: a.migrate.plan, SourceReadOnly: a.migrate.sourceReadOnly, Alias: a.migrate.aliasAdded}, "", "  ")
	if err != nil || os.MkdirAll(work, 0700) != nil {
		return
	}
	path := filepath.Join(work, "state.json")
	if os.WriteFile(path+".tmp", data, 0600) == nil {
		_ = atomicReplaceFile(path+".tmp", path)
	}
}

func (a *application) migrateLoadSaved(source string) *migrateSaved {
	data, err := os.ReadFile(filepath.Join(a.migrateWorkPath(source), "state.json"))
	if err != nil {
		return nil
	}
	var saved migrateSaved
	if json.Unmarshal(data, &saved) != nil || !strings.EqualFold(saved.Source, source) {
		return nil
	}
	return &saved
}

// migratePlanFile is what the engine reads.
type migratePlanShare struct {
	migrateSourceShare
	DestName         string `json:"destName"`
	DestPath         string `json:"destPath"`
	PreviouslySynced bool   `json:"previouslySynced"`
	CreatedShare     bool   `json:"createdShare"`
	ExpectedFiles    uint64 `json:"expectedFiles"`
}

// Caller holds a.mu.
func (a *application) migratePlanFileLocked() ([]byte, error) {
	if a.migrate.discovery == nil {
		return nil, errors.New("Connect to the old server first.")
	}
	sources := map[string]migrateSourceShare{}
	for _, share := range a.migrate.discovery.Shares {
		sources[strings.ToUpper(share.Name)] = share
	}
	shares := []migratePlanShare{}
	for _, item := range a.migrate.plan {
		source, ok := sources[strings.ToUpper(item.Name)]
		if !ok {
			return nil, fmt.Errorf("%s is no longer shared on the old server.", item.Name)
		}
		if source.Access == nil {
			source.Access = []migrateAccess{}
		}
		expected := uint64(0)
		if item.Precheck != nil {
			expected = item.Precheck.Files
		}
		shares = append(shares, migratePlanShare{migrateSourceShare: source, DestName: item.DestName, DestPath: item.DestPath, PreviouslySynced: item.PreviouslySynced, CreatedShare: item.CreatedShare, ExpectedFiles: expected})
	}
	return json.Marshal(map[string]any{"source": a.migrate.discovery.Source, "sourceFqdn": a.migrate.discovery.SourceFqdn, "shares": shares})
}

// startMigrateAction claims the migration for one engine run. Caller must not
// hold a.mu. It returns an HTTP status and message when refused.
func (a *application) startMigrateAction(action string) (int, string) {
	if !a.beginCriticalOperation() {
		return http.StatusConflict, "An update is being installed."
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	refuse := func(status int, message string) (int, string) {
		a.endCriticalOperation()
		return status, message
	}
	switch {
	case a.state.Running:
		return refuse(http.StatusConflict, "A collection is running. Wait for it to finish first.")
	case a.restoreBusyLocked():
		return refuse(http.StatusConflict, "A restore is running. Wait for it to finish first.")
	case a.migrateBusyLocked():
		return refuse(http.StatusConflict, "A migration step is already running.")
	}
	a.migrate.action = action
	a.migrate.cancelRequested = false
	a.migrate.startedAt = time.Now()
	a.migrate.endedAt = time.Time{}
	return 0, ""
}

// runMigrateEngine runs one engine mode. The plan (when given) and result
// files live in a private runtime folder; logs go to the source's work path.
func (a *application) runMigrateEngine(mode, source string, plan []byte) (result []byte, exitCode int, err error) {
	runtimeDir, script, err := a.prepareRestoreEngineNamed("Migrate-", migrateEngineName)
	if err != nil {
		return nil, -1, fmt.Errorf("the migration engine could not be prepared: %v", err)
	}
	defer os.RemoveAll(runtimeDir)
	work := a.migrateWorkPath(source)
	if work != "" {
		_ = os.MkdirAll(work, 0700)
	}
	resultPath := filepath.Join(runtimeDir, "Result.json")
	cancelPath := filepath.Join(runtimeDir, "Stop-Requested")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-Mode", mode, "-ResultPath", resultPath, "-CancelPath", cancelPath}
	if source != "" {
		arguments = append(arguments, "-Source", source)
	}
	if work != "" {
		arguments = append(arguments, "-WorkPath", work)
	}
	if plan != nil {
		planPath := filepath.Join(runtimeDir, "Plan.json")
		if err := os.WriteFile(planPath, plan, 0600); err != nil {
			return nil, -1, err
		}
		arguments = append(arguments, "-PlanPath", planPath)
	}
	a.mu.Lock()
	a.migrate.cancelPath = cancelPath
	if work != "" && a.migrate.logFile == nil {
		if file, openErr := os.OpenFile(filepath.Join(work, "Migration.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); openErr == nil {
			a.migrate.logFile = file
		}
	}
	a.mu.Unlock()
	command := exec.Command(a.powerShellPath, arguments...)
	waitErr := a.runEngineProcess(command, func(line string) {
		a.mu.Lock()
		a.applyMigrateLineLocked(line)
		a.mu.Unlock()
	}, func(cmd *exec.Cmd) {})
	exitCode = 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return nil, -1, waitErr
		}
		exitCode = exitErr.ExitCode()
	}
	result, _ = os.ReadFile(resultPath)
	return stripUTF8BOM(result), exitCode, nil
}

// finishMigrateAction ends an engine run started with startMigrateAction.
func (a *application) finishMigrateAction(status, message string) {
	a.mu.Lock()
	a.migrate.action = ""
	a.migrate.cancelPath = ""
	a.migrate.status = status
	if message != "" {
		a.migrate.message = message
	}
	a.migrate.endedAt = time.Now()
	a.migrateSaveLocked()
	if a.migrate.logFile != nil {
		_ = a.migrate.logFile.Close()
		a.migrate.logFile = nil
	}
	a.mu.Unlock()
	a.endCriticalOperation()
}

func decodeMigrateRequest(response http.ResponseWriter, request *http.Request, value any) bool {
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return false
	}
	if value == nil {
		return true
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 256*1024)).Decode(value); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The request was invalid."})
		return false
	}
	return true
}

func (a *application) requireMigrateAdmin(response http.ResponseWriter) bool {
	if !a.admin {
		writeJSON(response, http.StatusForbidden, map[string]any{"ok": false, "error": "Moving file shares needs administrator rights. Close the application and run it as administrator."})
		return false
	}
	return true
}

func (a *application) handleMigrateDiscover(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Source string `json:"source"`
	}
	if !decodeMigrateRequest(response, request, &input) || !a.requireMigrateAdmin(response) {
		return
	}
	source := strings.Trim(strings.TrimSpace(input.Source), `\`)
	if !migrateSourcePattern.MatchString(source) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the old server's name, for example FS01 or fs01.example.com."})
		return
	}
	if status, message := a.startMigrateAction("discover"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	a.mu.Lock()
	if !strings.EqualFold(a.migrate.source, source) {
		a.migrate = migrateRuntime{action: "discover", startedAt: time.Now()}
	}
	a.migrate.source = source
	a.migrate.status = "discovering"
	a.migrate.message = "Connecting to " + source + "."
	a.mu.Unlock()
	go func() {
		result, code, err := a.runMigrateEngine("Discover", source, nil)
		var discovery migrateDiscovery
		if err == nil && code == 0 && json.Unmarshal(result, &discovery) == nil && discovery.Source != "" {
			a.mu.Lock()
			a.migrate.discovery = &discovery
			a.migrate.source = discovery.Source
			a.migrate.prechecked = false
			if len(a.migrate.plan) == 0 {
				if saved := a.migrateLoadSaved(discovery.Source); saved != nil {
					a.migrate.plan = saved.Plan
					a.migrate.sourceReadOnly = saved.SourceReadOnly
					a.migrate.aliasAdded = saved.Alias
				}
			}
			a.mu.Unlock()
			a.finishMigrateAction("discovered", fmt.Sprintf("Found %d share(s) on %s.", len(discovery.Shares), discovery.Source))
			return
		}
		message := "Could not read the shares of " + source + "."
		a.mu.Lock()
		if a.migrate.message != "" && !strings.HasPrefix(a.migrate.message, "Connecting") {
			message = a.migrate.message
		}
		a.mu.Unlock()
		if err != nil {
			message = err.Error()
		}
		a.finishMigrateAction("discoverfailed", message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

type migratePlanInput struct {
	Name     string `json:"name"`
	DestName string `json:"destName"`
	DestPath string `json:"destPath"`
}

// validateMigratePlan checks the operator's selection against the discovered
// shares and against itself.
func validateMigratePlan(discovery *migrateDiscovery, input []migratePlanInput, previous []migrateShareState) ([]migrateShareState, error) {
	if discovery == nil {
		return nil, errors.New("Connect to the old server first.")
	}
	if len(input) == 0 {
		return nil, errors.New("Select at least one share to move.")
	}
	sources := map[string]migrateSourceShare{}
	for _, share := range discovery.Shares {
		sources[strings.ToUpper(share.Name)] = share
	}
	kept := map[string]migrateShareState{}
	for _, item := range previous {
		kept[strings.ToUpper(item.Name)] = item
	}
	names, paths, plan := map[string]bool{}, []string{}, []migrateShareState{}
	for _, item := range input {
		source, ok := sources[strings.ToUpper(item.Name)]
		if !ok {
			return nil, fmt.Errorf("%q is not a share on the old server.", item.Name)
		}
		if source.RemotePath == "" {
			return nil, fmt.Errorf("%s cannot be copied: its folder is not on a local drive of the old server.", source.Name)
		}
		destName := strings.TrimSpace(item.DestName)
		destPath := filepath.Clean(strings.TrimSpace(item.DestPath))
		if !migrateShareNamePattern.MatchString(destName) || strings.HasSuffix(destName, "$") && !source.System && !strings.HasSuffix(source.Name, "$") {
			return nil, fmt.Errorf("The new share name for %s is not valid.", source.Name)
		}
		if !migrateDestPathPattern.MatchString(destPath) || len(destPath) <= 3 {
			return nil, fmt.Errorf("The new folder for %s must be a folder on a local drive, such as D:\\Shares\\%s, not a drive root.", source.Name, source.Name)
		}
		if names[strings.ToUpper(destName)] {
			return nil, fmt.Errorf("The share name %s is used twice.", destName)
		}
		names[strings.ToUpper(destName)] = true
		lower := strings.ToLower(destPath) + `\`
		for _, other := range paths {
			if strings.HasPrefix(lower, other) || strings.HasPrefix(other, lower) {
				return nil, fmt.Errorf("The new folder for %s overlaps another share's new folder.", source.Name)
			}
		}
		paths = append(paths, lower)
		state := migrateShareState{Name: source.Name, DestName: destName, DestPath: destPath, Status: "planned"}
		if old, ok := kept[strings.ToUpper(source.Name)]; ok && strings.EqualFold(old.DestPath, destPath) {
			state.PreviouslySynced, state.LastSyncUnixMs, state.CreatedShare = old.PreviouslySynced, old.LastSyncUnixMs, old.CreatedShare && strings.EqualFold(old.DestName, destName)
		}
		plan = append(plan, state)
	}
	return plan, nil
}

func (a *application) handleMigratePlan(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Shares []migratePlanInput `json:"shares"`
	}
	if !decodeMigrateRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.migrateBusyLocked() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the current step to finish."})
		return
	}
	plan, err := validateMigratePlan(a.migrate.discovery, input.Shares, a.migrate.plan)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.migrate.plan = plan
	a.migrate.prechecked = false
	a.migrate.space = nil
	a.migrate.status = "planned"
	a.migrate.message = "Check the plan before copying."
	a.migrateSaveLocked()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

type migratePrecheckResult struct {
	Shares []struct {
		Name string `json:"name"`
		migratePrecheck
	} `json:"shares"`
	Space    []migrateSpace `json:"space"`
	Canceled bool           `json:"canceled"`
}

// launchMigrateStep validates, claims and runs a plan-based engine mode.
func (a *application) launchMigrateStep(response http.ResponseWriter, action, mode, runningStatus, runningMessage string, ready func() string, done func(result []byte, code int)) {
	if !a.requireMigrateAdmin(response) {
		return
	}
	if status, message := a.startMigrateAction(action); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	a.mu.Lock()
	if message := ready(); message != "" {
		a.migrate.action = ""
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": message})
		return
	}
	plan, err := a.migratePlanFileLocked()
	if err != nil {
		a.migrate.action = ""
		a.mu.Unlock()
		a.endCriticalOperation()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	source := a.migrate.source
	a.migrate.status = runningStatus
	a.migrate.message = runningMessage
	a.mu.Unlock()
	a.logUpdate("Migration step %s started for %s", action, source)
	go func() {
		result, code, err := a.runMigrateEngine(mode, source, plan)
		if err != nil {
			a.finishMigrateAction("failed", err.Error())
			return
		}
		done(result, code)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleMigratePrecheck(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeMigrateRequest(response, request, nil) {
		return
	}
	a.launchMigrateStep(response, "precheck", "Precheck", "checking", "Checking the old shares, this server's folders and free space.", func() string {
		if len(a.migrate.plan) == 0 {
			return "Select the shares to move first."
		}
		return ""
	}, func(raw []byte, code int) {
		var result migratePrecheckResult
		if code != 0 || json.Unmarshal(raw, &result) != nil {
			a.finishMigrateAction("failed", "The check did not finish. See the log.")
			return
		}
		a.mu.Lock()
		allOK := !result.Canceled
		for index := range a.migrate.plan {
			share := &a.migrate.plan[index]
			share.Precheck = nil
			for _, item := range result.Shares {
				if strings.EqualFold(item.Name, share.Name) {
					check := item.migratePrecheck
					share.Precheck = &check
				}
			}
			if share.Precheck == nil || !share.Precheck.OK {
				allOK = false
			}
		}
		a.migrate.space = result.Space
		a.migrate.prechecked = !result.Canceled
		a.mu.Unlock()
		if result.Canceled {
			a.finishMigrateAction("canceled", "The check was stopped.")
		} else if allOK {
			a.finishMigrateAction("checked", "Everything is ready to copy.")
		} else {
			a.finishMigrateAction("checked", "Fix the problems shown before copying.")
		}
	})
}

type migrateSyncResult struct {
	Shares []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"shares"`
	Failed       bool `json:"failed"`
	Canceled     bool `json:"canceled"`
	Verification []struct {
		Name     string `json:"name"`
		Verified bool   `json:"verified"`
	} `json:"verification"`
	Created []struct {
		Name string `json:"name"`
	} `json:"created"`
}

// Caller holds a.mu.
func (a *application) applySyncResultLocked(result migrateSyncResult) {
	now := time.Now().UnixNano() / int64(time.Millisecond)
	for index := range a.migrate.plan {
		share := &a.migrate.plan[index]
		for _, item := range result.Shares {
			if strings.EqualFold(item.Name, share.Name) && item.Status == "succeeded" {
				share.PreviouslySynced = true
				share.LastSyncUnixMs = now
			}
		}
		for _, item := range result.Created {
			if strings.EqualFold(item.Name, share.Name) {
				share.CreatedShare = true
			}
		}
		for _, item := range result.Verification {
			if strings.EqualFold(item.Name, share.Name) {
				share.Verified = item.Verified
			}
		}
	}
}

func (a *application) handleMigrateSync(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeMigrateRequest(response, request, nil) {
		return
	}
	a.launchMigrateStep(response, "sync", "Sync", "copying", "Copying. Users can keep working on the old server; run Copy again later to bring over what changed.", func() string {
		if !a.migrateCanCopyLocked() {
			return "Run the check, and fix any problem it finds, before copying."
		}
		return ""
	}, func(raw []byte, code int) {
		var result migrateSyncResult
		_ = json.Unmarshal(raw, &result)
		a.mu.Lock()
		a.applySyncResultLocked(result)
		a.mu.Unlock()
		switch {
		case code == 0:
			a.finishMigrateAction("copied", "Copy finished. Run Copy again any time to bring over changes, then cut over.")
		case code == 2:
			a.finishMigrateAction("canceled", "Copy stopped. Run Copy again to continue where it left off.")
		default:
			a.finishMigrateAction("failed", "Some files could not be copied. See each share and the log, then run Copy again.")
		}
	})
}

func (a *application) handleMigrateCutover(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		ConfirmComputerName string `json:"confirmComputerName"`
	}
	if !decodeMigrateRequest(response, request, &input) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(input.ConfirmComputerName), a.computerName) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type this server's name, " + a.computerName + ", to confirm the cutover."})
		return
	}
	a.launchMigrateStep(response, "cutover", "Cutover", "cuttingover", "Final copy, then creating the shares here and verifying.", func() string {
		if !a.migrateCanCutoverLocked() {
			return "Copy every selected share at least once before cutting over."
		}
		return ""
	}, func(raw []byte, code int) {
		var result migrateSyncResult
		_ = json.Unmarshal(raw, &result)
		a.mu.Lock()
		a.applySyncResultLocked(result)
		verified := len(result.Verification) == len(a.migrate.plan)
		for _, item := range result.Verification {
			verified = verified && item.Verified
		}
		a.mu.Unlock()
		switch {
		case code == 0 && verified:
			a.finishMigrateAction("cutover", "Cutover complete. The shares are live on this server and match the old ones.")
		case code == 0:
			a.finishMigrateAction("cutover", "The shares are live on this server, but some files changed during cutover. Run the cutover again to copy them.")
		case code == 2:
			a.finishMigrateAction("canceled", "Cutover stopped before the shares were created. Run it again.")
		default:
			a.finishMigrateAction("failed", "Cutover stopped. See each share and the log.")
		}
	})
}

func (a *application) handleMigrateSourceReadOnly(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		ConfirmSourceName string `json:"confirmSourceName"`
	}
	if !decodeMigrateRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	source := a.migrate.source
	a.mu.Unlock()
	if source == "" || !strings.EqualFold(strings.TrimSpace(input.ConfirmSourceName), strings.Split(source, ".")[0]) && !strings.EqualFold(strings.TrimSpace(input.ConfirmSourceName), source) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the old server's name to confirm."})
		return
	}
	a.launchMigrateStep(response, "readonly", "SourceReadOnly", "working", "Making the old shares read-only.", func() string {
		if a.migrate.sourceReadOnly {
			return "The old shares are already read-only."
		}
		return ""
	}, func(raw []byte, code int) {
		if code != 0 {
			a.finishMigrateAction("failed", "The old shares could not be made read-only. See the log.")
			return
		}
		a.mu.Lock()
		a.migrate.sourceReadOnly = true
		a.mu.Unlock()
		a.finishMigrateAction("readonly", "The old shares are read-only. Their previous permissions are saved and can be put back.")
	})
}

func (a *application) handleMigrateSourceRestore(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeMigrateRequest(response, request, nil) {
		return
	}
	a.launchMigrateStep(response, "restoresource", "SourceRestore", "working", "Putting back the old shares' permissions.", func() string {
		if !a.migrate.sourceReadOnly {
			return "The old shares were not made read-only by this application."
		}
		return ""
	}, func(raw []byte, code int) {
		if code != 0 {
			a.finishMigrateAction("failed", "The old shares' permissions could not be put back. See the log.")
			return
		}
		a.mu.Lock()
		a.migrate.sourceReadOnly = false
		a.mu.Unlock()
		a.finishMigrateAction("restoredsource", "The old shares' permissions are back.")
	})
}

func (a *application) handleMigrateDfs(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeMigrateRequest(response, request, nil) {
		return
	}
	a.launchMigrateStep(response, "dfs", "DfsReport", "working", "Looking for DFS links that point at the old server.", func() string { return "" }, func(raw []byte, code int) {
		var result migrateDfs
		if code != 0 || json.Unmarshal(raw, &result) != nil {
			a.finishMigrateAction("failed", "The DFS check did not finish. See the log.")
			return
		}
		if result.Targets == nil {
			result.Targets = []migrateDfsTarget{}
		}
		a.mu.Lock()
		a.migrate.dfs = &result
		a.mu.Unlock()
		a.finishMigrateAction("dfschecked", result.Message)
	})
}

func (a *application) handleMigrateAliasCheck(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeMigrateRequest(response, request, nil) {
		return
	}
	a.launchMigrateStep(response, "aliascheck", "AliasCheck", "working", "Checking whether this server can take over the old server's name.", func() string { return "" }, func(raw []byte, code int) {
		var result migrateAlias
		if code != 0 || json.Unmarshal(raw, &result) != nil {
			a.finishMigrateAction("failed", "The check did not finish. See the log.")
			return
		}
		if result.Reasons == nil {
			result.Reasons = []string{}
		}
		a.mu.Lock()
		a.migrate.alias = &result
		a.mu.Unlock()
		message := "This server can take over the old server's name."
		if !result.Possible {
			message = "This server cannot take over the old server's name yet."
		}
		a.finishMigrateAction("aliaschecked", message)
	})
}

func (a *application) handleMigrateAlias(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		ConfirmOldName string `json:"confirmOldName"`
	}
	if !decodeMigrateRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	alias := a.migrate.alias
	a.mu.Unlock()
	if alias == nil || !alias.Possible {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Run the name check first; it must pass."})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(input.ConfirmOldName), alias.OldName) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the old server's name, " + alias.OldName + ", to confirm."})
		return
	}
	a.launchMigrateStep(response, "alias", "Alias", "working", "Adding the old server's name to this server.", func() string { return "" }, func(raw []byte, code int) {
		var result migrateAlias
		if code != 0 || json.Unmarshal(raw, &result) != nil {
			a.finishMigrateAction("failed", "The old name could not be added. See the log.")
			return
		}
		a.mu.Lock()
		a.migrate.aliasAdded = result.Added
		a.mu.Unlock()
		a.finishMigrateAction("aliasadded", "Added "+result.Added+" as a name of this server. Restart this server for it to take effect.")
	})
}

func (a *application) handleMigrateCancel(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeMigrateRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.migrate.action == "" || a.migrate.cancelPath == "" {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "No migration step is running."})
		return
	}
	switch a.migrate.action {
	case "readonly", "restoresource", "alias":
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "This step is short and cannot be stopped part way."})
		return
	}
	a.migrate.cancelRequested = true
	a.appendMigrateLogLocked(time.Now().Format("2006-01-02 15:04:05") + " [GUI] Stop requested.")
	if err := os.WriteFile(a.migrate.cancelPath, []byte("stop"), 0600); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The stop request could not be recorded: " + err.Error()})
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleMigrateReset(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeMigrateRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.migrateBusyLocked() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the current step to finish."})
		return
	}
	// The saved state stays on disk, so connecting to the same server again
	// picks the migration up where it was.
	a.migrate = migrateRuntime{}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

// stopMigrateAndWait asks a running step to stop and waits for it, so closing
// the application never leaves a step half done without telling the engine.
func (a *application) stopMigrateAndWait() {
	a.mu.Lock()
	running := a.migrate.action != ""
	cancelPath := a.migrate.cancelPath
	a.mu.Unlock()
	if !running {
		return
	}
	a.logUpdate("The application is closing during a migration step; stopping it.")
	for {
		if cancelPath != "" {
			_ = os.WriteFile(cancelPath, []byte("stop"), 0600)
		}
		time.Sleep(250 * time.Millisecond)
		a.mu.Lock()
		running = a.migrate.action != ""
		cancelPath = a.migrate.cancelPath
		a.mu.Unlock()
		if !running {
			return
		}
	}
}
