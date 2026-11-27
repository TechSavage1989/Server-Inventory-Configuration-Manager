//go:build windows

package main

// Build Hyper-V hosts (see hvbuild.go): the plan is saved without secrets
// under LocalAppData "HyperV-Build"; the nodes' account and a cloud witness
// key stay in memory (a.hvBuild) until Forget or exit and reach PowerShell
// only as one JSON line on standard input. Every run is a VMware-pane action,
// so it is a critical operation that blocks collection, restore, migration,
// update install and closing.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed Build-HyperVHost.ps1
var hvBuildScriptData []byte

const hvBuildScriptName = "Build-HyperVHost.ps1"

// hvStrings reads a JSON string list that PowerShell may have written as a
// single string or null.
type hvStrings []string

func (s *hvStrings) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*s = list
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single == "" {
			*s = hvStrings{}
		} else {
			*s = hvStrings{single}
		}
		return nil
	}
	*s = hvStrings{}
	return nil
}

type hvBuildAdapterFacts struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	MAC         string    `json:"mac"`
	SpeedBits   uint64    `json:"speedBits"`
	IPs         hvStrings `json:"ips"`
	SwitchBound bool      `json:"switchBound"`
}

type hvBuildSwitchFacts struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Adapters hvStrings `json:"adapters"`
	Embedded bool      `json:"embedded"`
}

type hvBuildDiskFacts struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Serial     string `json:"serial"`
	UniqueID   string `json:"uniqueId"`
	SizeBytes  uint64 `json:"sizeBytes"`
	Style      string `json:"style"`
	Boot       bool   `json:"boot"`
	System     bool   `json:"system"`
	Clustered  bool   `json:"clustered"`
	Offline    bool   `json:"offline"`
	Bus        string `json:"bus"`
	Partitions int    `json:"partitions"`
}

type hvBuildNodeFacts struct {
	Computer          string                `json:"computer"`
	Caption           string                `json:"caption"`
	Version           string                `json:"version"`
	Build             int                   `json:"build"`
	ProductType       int                   `json:"productType"`
	Domain            string                `json:"domain"`
	PartOfDomain      bool                  `json:"partOfDomain"`
	HypervisorPresent bool                  `json:"hypervisorPresent"`
	Virtualization    bool                  `json:"virtualization"`
	PendingRestart    bool                  `json:"pendingRestart"`
	Cores             int                   `json:"cores"`
	MemoryBytes       uint64                `json:"memoryBytes"`
	Features          map[string]string     `json:"features"`
	Adapters          []hvBuildAdapterFacts `json:"adapters"`
	Switches          []hvBuildSwitchFacts  `json:"switches"`
	HostNICs          []struct {
		Name   string `json:"name"`
		Switch string `json:"switch"`
	} `json:"hostNics"`
	Disks     []hvBuildDiskFacts `json:"disks"`
	Initiator string             `json:"initiator"`
	WWPNs     hvStrings          `json:"wwpns"`
	Portals   hvStrings          `json:"portals"`
	Cluster   string             `json:"cluster"`
	Warnings  hvStrings          `json:"warnings"`
}

type hvBuildNodeRead struct {
	OK    bool              `json:"ok"`
	Error string            `json:"error"`
	Facts *hvBuildNodeFacts `json:"facts"`
}

type hvBuildLocalFacts struct {
	Computer      string `json:"computer"`
	Domain        string `json:"domain"`
	PartOfDomain  bool   `json:"partOfDomain"`
	Server        bool   `json:"server"`
	Caption       string `json:"caption"`
	Account       string `json:"account"`
	Admin         bool   `json:"admin"`
	ClusterModule bool   `json:"clusterModule"`
}

type hvBuildFacts struct {
	Nodes map[string]hvBuildNodeRead `json:"nodes"`
	Local hvBuildLocalFacts          `json:"local"`
	At    time.Time                  `json:"at"`
}

type hvBuildOutput struct {
	Computer string    `json:"computer"`
	Changes  hvStrings `json:"changes"`
	Notes    hvStrings `json:"notes"`
	Blocks   hvStrings `json:"blocks"`
	Applied  hvStrings `json:"applied"`
	Warnings hvStrings `json:"warnings"`
	Hash     string    `json:"hash"`
	Refused  string    `json:"refused"`
	Error    string    `json:"error"`
	Report   string    `json:"report"`
	Restart  bool      `json:"restart"`
}

type hvBuildRun struct {
	Computer  string         `json:"computer"`
	OK        bool           `json:"ok"`
	Error     string         `json:"error"`
	Output    *hvBuildOutput `json:"output"`
	Restarted bool           `json:"restarted"`
	Skipped   bool           `json:"skipped"`
}

type hvBuildStepResult struct {
	Step     string       `json:"step"`
	Revision string       `json:"revision"`
	Mode     string       `json:"mode"`
	At       time.Time    `json:"at"`
	Runs     []hvBuildRun `json:"runs"`
	// Changes counts the changes the preview lists; Approvable says the
	// preview can be applied (every computer answered, nothing blocks).
	Changes    int    `json:"changes"`
	Approvable bool   `json:"approvable"`
	Done       bool   `json:"done"`
	Failed     bool   `json:"failed"`
	ReportPath string `json:"reportPath"`
	// Approved holds the change hash of each computer from the preview.
	Approved map[string]string `json:"-"`
}

type hvBuildRuntime struct {
	plan     *hvBuildPlan
	user     string
	password string
	cloudKey string
	facts    *hvBuildFacts
	results  map[string]*hvBuildStepResult
	saved    []string
	message  string
	failed   bool
	folder   string
	report   string
	serial   uint64
}

type hvBuildStateResponse struct {
	Plan     *hvBuildPlan                  `json:"plan"`
	Revision string                        `json:"revision"`
	Steps    []hvBuildStepInfo             `json:"steps"`
	Facts    *hvBuildFacts                 `json:"facts"`
	Results  map[string]*hvBuildStepResult `json:"results"`
	User     string                        `json:"user"`
	HasKey   bool                          `json:"hasCloudKey"`
	Saved    []string                      `json:"saved"`
	Message  string                        `json:"message"`
	Failed   bool                          `json:"failed"`
	Folder   string                        `json:"folder"`
	Report   string                        `json:"report"`
	Serial   uint64                        `json:"serial"`
	Here     string                        `json:"here"`
}

func (a *application) hvBuildRoot() string {
	return filepath.Join(a.update.appDataRoot, "HyperV-Build")
}

// Caller holds a.mu.
func (a *application) hvBuildStateLocked() *hvBuildStateResponse {
	r := &a.hvBuild
	if r.saved == nil {
		r.saved = a.hvBuildSavedNames()
	}
	response := &hvBuildStateResponse{Plan: r.plan, Facts: r.facts, Results: r.results, User: r.user, HasKey: r.cloudKey != "", Saved: r.saved, Message: r.message, Failed: r.failed, Folder: r.folder, Report: r.report, Serial: r.serial, Steps: []hvBuildStepInfo{}}
	response.Here, _ = os.Hostname()
	if r.plan != nil {
		response.Revision = hvBuildRevision(*r.plan)
		response.Steps = hvBuildSteps(*r.plan)
	}
	if response.Results == nil {
		response.Results = map[string]*hvBuildStepResult{}
	}
	return response
}

func (a *application) hvBuildSavedNames() []string {
	names := []string{}
	entries, err := os.ReadDir(a.hvBuildRoot())
	if err != nil {
		return names
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			names = append(names, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		}
	}
	sort.Strings(names)
	return names
}

func (a *application) hvBuildPlanPath(name string) string {
	return filepath.Join(a.hvBuildRoot(), safeName(name)+".json")
}

func (a *application) saveHVBuildPlan(plan hvBuildPlan) error {
	path := a.hvBuildPlanPath(plan.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return atomicReplaceFile(path+".tmp", path)
}

func hvBuildFail(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]any{"ok": false, "error": message})
}

func (a *application) handleHVBuild(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/hvbuild/") {
	case "suggest":
		a.handleHVBuildSuggest(response, request)
	case "save":
		a.handleHVBuildSave(response, request)
	case "open":
		a.handleHVBuildOpen(response, request)
	case "account":
		a.handleHVBuildAccount(response, request)
	case "read":
		a.handleHVBuildRead(response, request)
	case "preview":
		a.handleHVBuildStep(response, request, false)
	case "apply":
		a.handleHVBuildStep(response, request, true)
	case "networkmap":
		a.handleHVBuildNetworkMap(response, request)
	default:
		hvBuildFail(response, http.StatusNotFound, "Not found.")
	}
}

func (a *application) handleHVBuildSuggest(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Cluster string `json:"cluster"`
		// Reuse drafts the nodes as the ESXi hosts rebuilt in place, in
		// the order of the rolling plan's stages.
		Reuse bool `json:"reuse"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	inventory, busy, migration, busyText := a.vmware.inventory, a.vmwareBusyLocked(), a.vmware.plan, a.vmwareBusyTextLocked()
	a.mu.Unlock()
	if busy {
		hvBuildFail(response, http.StatusConflict, busyText+" Wait for it to finish first.")
		return
	}
	order := []string{}
	if input.Reuse && migration != nil && migration.Rolling != nil {
		for _, stage := range migration.Rolling.Stages {
			if stage.Host != "" {
				order = append(order, stage.Host)
			}
		}
	}
	plan, err := hvBuildSuggestFor(inventory, strings.TrimSpace(input.Cluster), input.Reuse, order)
	if err != nil {
		hvBuildFail(response, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "plan": plan})
}

func (a *application) handleHVBuildSave(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Plan hvBuildPlan `json:"plan"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	plan, err := hvBuildCheckPlan(input.Plan)
	if err != nil {
		hvBuildFail(response, http.StatusBadRequest, err.Error())
		return
	}
	a.mu.Lock()
	if a.vmwareBusyLocked() {
		busyText := a.vmwareBusyTextLocked()
		a.mu.Unlock()
		hvBuildFail(response, http.StatusConflict, busyText+" Wait for it to finish first.")
		return
	}
	previous := a.hvBuild.plan
	if previous != nil && !strings.EqualFold(previous.Name, plan.Name) {
		a.hvBuild.results = nil
		a.hvBuild.facts = nil
		a.hvBuild.folder, a.hvBuild.report = "", ""
	}
	a.hvBuild.plan = &plan
	a.hvBuild.serial++
	a.mu.Unlock()
	saveErr := a.saveHVBuildPlan(plan)
	a.mu.Lock()
	a.hvBuild.saved = a.hvBuildSavedNames()
	a.mu.Unlock()
	result := map[string]any{"ok": true, "plan": plan, "revision": hvBuildRevision(plan), "steps": hvBuildSteps(plan)}
	if saveErr != nil {
		result["warning"] = "The plan is in use but could not be saved for next time: " + saveErr.Error()
	}
	writeJSON(response, http.StatusOK, result)
}

func (a *application) handleHVBuildOpen(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	data, err := os.ReadFile(a.hvBuildPlanPath(input.Name))
	if err != nil {
		hvBuildFail(response, http.StatusNotFound, "That plan was not found.")
		return
	}
	var plan hvBuildPlan
	if json.Unmarshal(data, &plan) != nil {
		hvBuildFail(response, http.StatusBadRequest, "That plan could not be read.")
		return
	}
	if plan, err = hvBuildCheckPlan(plan); err != nil {
		hvBuildFail(response, http.StatusBadRequest, "That plan is not valid: "+err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vmwareBusyLocked() {
		hvBuildFail(response, http.StatusConflict, a.vmwareBusyTextLocked()+" Wait for it to finish first.")
		return
	}
	a.hvBuild.plan = &plan
	a.hvBuild.results, a.hvBuild.facts = nil, nil
	a.hvBuild.folder, a.hvBuild.report = "", ""
	a.hvBuild.message, a.hvBuild.failed = "Opened plan "+plan.Name+".", false
	a.hvBuild.serial++
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "plan": plan})
}

func (a *application) handleHVBuildAccount(response http.ResponseWriter, request *http.Request) {
	var input struct {
		User     string `json:"user"`
		Password string `json:"password"`
		CloudKey string `json:"cloudKey"`
		Forget   bool   `json:"forget"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	user, password, key := strings.TrimSpace(input.User), input.Password, strings.TrimSpace(input.CloudKey)
	input.Password, input.CloudKey = "", ""
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vmwareBusyLocked() {
		hvBuildFail(response, http.StatusConflict, a.vmwareBusyTextLocked()+" Wait for it to finish first.")
		return
	}
	if input.Forget {
		a.hvBuild.user, a.hvBuild.password, a.hvBuild.cloudKey = "", "", ""
		a.hvBuild.serial++
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if user != "" {
		if !vmwareUserPattern.MatchString(user) || len(password) > 256 || strings.ContainsRune(password, 0) {
			hvBuildFail(response, http.StatusBadRequest, "The account or password is not valid.")
			return
		}
		if password == "" && strings.EqualFold(user, a.hvBuild.user) {
			password = a.hvBuild.password
		}
		if password == "" {
			hvBuildFail(response, http.StatusBadRequest, "Type the password for "+user+", or choose your current sign-in.")
			return
		}
		a.hvBuild.user, a.hvBuild.password = user, password
	} else if input.User == "" && password == "" && key == "" {
		a.hvBuild.user, a.hvBuild.password = "", ""
	}
	if key != "" {
		if len(key) > 512 || strings.ContainsAny(key, "\x00\r\n") {
			hvBuildFail(response, http.StatusBadRequest, "The access key is not valid.")
			return
		}
		a.hvBuild.cloudKey = key
	}
	a.hvBuild.serial++
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

// runHVBuild runs Build-HyperVHost.ps1 with the RemoteSigned execution
// policy for this process only (the released script is signed), never
// Bypass; runEngineProcess gives the process no window.
func (a *application) runHVBuild(ctx context.Context, mode string, computers []string, stepBody string, approved map[string]string, apply, restart bool, user, password, cloudKey string, into interface{}) error {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "HyperVBuild-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, hvBuildScriptName)
	if err := os.WriteFile(script, hvBuildScriptData, 0600); err != nil {
		return err
	}
	resultPath := filepath.Join(directory, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Mode", mode, "-ResultPath", resultPath, "-Computers", strings.Join(computers, ",")}
	if stepBody != "" {
		path := filepath.Join(directory, "Step.ps1")
		if err := os.WriteFile(path, []byte(stepBody), 0600); err != nil {
			return err
		}
		arguments = append(arguments, "-StepPath", path)
	}
	if approved != nil {
		data, _ := json.Marshal(approved)
		path := filepath.Join(directory, "Approved.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
		arguments = append(arguments, "-ApprovedPath", path)
	}
	if apply {
		arguments = append(arguments, "-Apply")
	}
	if restart {
		arguments = append(arguments, "-RestartAfter")
	}
	if user != "" || cloudKey != "" {
		arguments = append(arguments, "-CredentialFromInput")
	}
	command := exec.CommandContext(ctx, a.powerShellPath, arguments...)
	if user != "" || cloudKey != "" {
		payload := map[string]any{"user": user, "password": password}
		if cloudKey != "" {
			payload["secrets"] = map[string]string{"cloudKey": cloudKey}
		}
		line, _ := json.Marshal(payload)
		command.Stdin = bytes.NewReader(append(line, '\n'))
	}
	waitErr := a.runEngineProcess(command, func(line string) {
		a.mu.Lock()
		a.appendVMwareLogLocked(line)
		a.mu.Unlock()
	}, func(*exec.Cmd) {})
	if ctx.Err() != nil {
		return context.Canceled
	}
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return fmt.Errorf("the Hyper-V build engine stopped: %v", waitErr)
		}
		return errors.New("the Hyper-V build engine wrote no result")
	}
	return hvRemoteParseResult(data, into)
}

// hvBuildLocalNode reports whether a node name is this computer.
func hvBuildLocalNode(name string) bool {
	here, _ := os.Hostname()
	return hvRemoteSameComputer(name, here) || strings.EqualFold(name, "localhost")
}

func (a *application) handleHVBuildRead(response http.ResponseWriter, request *http.Request) {
	if !decodeVMwareRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	plan := a.hvBuild.plan
	user, password := a.hvBuild.user, a.hvBuild.password
	a.mu.Unlock()
	if plan == nil {
		hvBuildFail(response, http.StatusBadRequest, "Save the plan first.")
		return
	}
	computers := []string{}
	for _, node := range plan.Nodes {
		if node.Name != "" {
			computers = append(computers, node.Name)
		}
	}
	if len(computers) == 0 {
		hvBuildFail(response, http.StatusBadRequest, "Type the nodes' names first.")
		return
	}
	if status, message := a.startVMwareAction("hvbuild"); status != 0 {
		hvBuildFail(response, status, message)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	idle := a.vmwareIdleStatus()
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "hvbuilding"
	a.vmware.message = "Reading " + strings.Join(computers, ", ") + "."
	a.mu.Unlock()
	a.vmwareLog("Reading the Hyper-V nodes %s through PowerShell remoting.", strings.Join(computers, ", "))
	go func() {
		defer cancel()
		facts := &hvBuildFacts{}
		err := a.runHVBuild(ctx, "Read", computers, "", nil, false, false, user, password, "", facts)
		message := ""
		a.mu.Lock()
		if err == nil {
			facts.At = time.Now()
			a.hvBuild.facts = facts
			failed := []string{}
			for name, node := range facts.Nodes {
				if !node.OK {
					failed = append(failed, name+": "+hvRemoteFriendlyError(node.Error))
				}
			}
			sort.Strings(failed)
			if len(failed) > 0 {
				message = "Some nodes did not answer. " + strings.Join(failed, " ")
				a.hvBuild.message, a.hvBuild.failed = message, true
			} else {
				message = "Read " + strings.Join(computers, ", ") + "."
				a.hvBuild.message, a.hvBuild.failed = message, false
			}
		} else {
			if errors.Is(err, context.Canceled) {
				err = errors.New("Stopped. Nothing was changed.")
			}
			message = err.Error()
			a.hvBuild.message, a.hvBuild.failed = message, true
		}
		a.hvBuild.serial++
		a.mu.Unlock()
		a.vmwareLog("%s", message)
		a.finishVMwareAction(idle, message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// hvBuildSummarize fills in what a step result means.
func hvBuildSummarize(result *hvBuildStepResult) {
	result.Changes, result.Approvable, result.Done, result.Failed = 0, true, true, false
	result.Approved = map[string]string{}
	for _, run := range result.Runs {
		if run.Skipped {
			result.Approvable, result.Done = false, false
			continue
		}
		if !run.OK || run.Output == nil {
			result.Approvable, result.Done, result.Failed = false, false, true
			continue
		}
		output := run.Output
		if output.Error != "" || output.Refused != "" {
			result.Failed = true
		}
		if len(output.Blocks) > 0 {
			result.Approvable, result.Done = false, false
		}
		if hvBuildChangeHash(output.Changes) != output.Hash {
			result.Approvable, result.Failed = false, true
		}
		result.Changes += len(output.Changes)
		key := run.Computer
		result.Approved[key] = output.Hash
	}
	if result.Mode == "preview" {
		if result.Changes > 0 {
			result.Done = false
		} else {
			result.Approvable = false
		}
	} else {
		result.Approvable, result.Done = false, result.Done && !result.Failed
		for _, run := range result.Runs {
			if run.Output != nil && len(run.Output.Applied) < len(run.Output.Changes) {
				result.Done = false
			}
		}
	}
}

func (a *application) handleHVBuildStep(response http.ResponseWriter, request *http.Request, apply bool) {
	var input struct {
		Step    string `json:"step"`
		Confirm string `json:"confirm"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	planPointer := a.hvBuild.plan
	user, password, cloudKey := a.hvBuild.user, a.hvBuild.password, a.hvBuild.cloudKey
	var previous *hvBuildStepResult
	if a.hvBuild.results != nil {
		previous = a.hvBuild.results[input.Step]
	}
	a.mu.Unlock()
	if planPointer == nil {
		hvBuildFail(response, http.StatusBadRequest, "Save the plan first.")
		return
	}
	plan := *planPointer
	revision := hvBuildRevision(plan)
	step, ok := hvBuildStepByID(plan, input.Step)
	if !ok {
		hvBuildFail(response, http.StatusBadRequest, "That step is not part of this plan.")
		return
	}
	if step.Missing != "" {
		hvBuildFail(response, http.StatusBadRequest, step.Missing)
		return
	}
	computers := hvBuildStepComputers(plan, step)
	var approved map[string]string
	if apply {
		if !strings.EqualFold(strings.TrimSpace(input.Confirm), plan.Name) {
			hvBuildFail(response, http.StatusBadRequest, "Type the plan's name exactly to approve: "+plan.Name)
			return
		}
		if previous == nil || previous.Mode != "preview" || previous.Revision != revision || !previous.Approvable {
			hvBuildFail(response, http.StatusBadRequest, "Preview this step first and check what it will change. The plan changed since the last preview, or the preview lists nothing to change or something that blocks it.")
			return
		}
		if time.Since(previous.At) > 30*time.Minute {
			hvBuildFail(response, http.StatusBadRequest, "The preview is more than 30 minutes old. Preview the step again.")
			return
		}
		approved = map[string]string{}
		for computer, hash := range previous.Approved {
			key := computer
			if key == hvBuildHere {
				key = "."
			}
			approved[key] = hash
		}
	}
	for _, computer := range computers {
		if computer != hvBuildHere && hvBuildLocalNode(computer) && !isAdministrator() {
			hvBuildFail(response, http.StatusForbidden, computer+" is this computer: run the application as administrator to configure it.")
			return
		}
	}
	if step.Where == hvWhereRestart && apply {
		for _, computer := range computers {
			if hvBuildLocalNode(computer) {
				hvBuildFail(response, http.StatusBadRequest, computer+" is this computer: restart it yourself, then read the nodes again.")
				return
			}
		}
	}
	// Each computer gets its own script (node values differ), so the
	// engine runs once per computer, stopping at the first failure.
	bodies := map[string]string{}
	for _, computer := range computers {
		body, err := hvBuildScript(plan, step.ID, computer)
		if err != nil {
			hvBuildFail(response, http.StatusBadRequest, err.Error())
			return
		}
		bodies[computer] = body
	}
	action := "Previewing"
	if apply {
		action = "Applying"
	}
	if status, message := a.startVMwareAction("hvbuild"); status != 0 {
		hvBuildFail(response, status, message)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	idle := a.vmwareIdleStatus()
	a.mu.Lock()
	if !apply {
		// A preview changes nothing and can be stopped; an apply runs to
		// the end of the current computer's changes.
		a.vmware.cancel = cancel
	}
	a.vmware.status = "hvbuilding"
	a.vmware.message = action + " " + step.Title + "."
	a.mu.Unlock()
	a.vmwareLog("%s %s on %s.", action, step.Title, hvBuildWhereText(computers))
	go func() {
		defer cancel()
		result := &hvBuildStepResult{Step: step.ID, Revision: revision, Mode: "preview", At: time.Now(), Runs: []hvBuildRun{}}
		if apply {
			result.Mode = "apply"
		}
		stopped := false
		var runErr error
		for _, computer := range computers {
			if stopped {
				result.Runs = append(result.Runs, hvBuildRun{Computer: computer, Skipped: true})
				continue
			}
			var answer struct {
				Runs []hvBuildRun `json:"runs"`
			}
			target := []string{}
			if computer != hvBuildHere {
				target = []string{computer}
			}
			key := computer
			if key == hvBuildHere {
				key = "."
			}
			var approvedOne map[string]string
			if apply {
				approvedOne = map[string]string{key: approved[key]}
			}
			runUser, runPassword, runKey := user, password, ""
			if computer == hvBuildHere {
				// Cluster steps run here with the signed-in account; only
				// the cloud witness key reaches them.
				runUser, runPassword = "", ""
				if step.ID == "quorum" {
					runKey = cloudKey
				}
			}
			err := a.runHVBuild(ctx, "Run", target, bodies[computer], approvedOne, apply, apply && step.Where == hvWhereRestart, runUser, runPassword, runKey, &answer)
			if err != nil {
				runErr = err
				result.Runs = append(result.Runs, hvBuildRun{Computer: computer, Error: hvRemoteFriendlyError(err.Error())})
				stopped = true
				continue
			}
			for _, run := range answer.Runs {
				run.Computer = computer
				if run.Error != "" {
					run.Error = hvRemoteFriendlyError(run.Error)
				}
				result.Runs = append(result.Runs, run)
				if !run.OK || run.Error != "" || (run.Output != nil && (run.Output.Error != "" || run.Output.Refused != "")) {
					stopped = apply
				}
			}
		}
		hvBuildSummarize(result)
		message := hvBuildResultMessage(step, result)
		if errors.Is(runErr, context.Canceled) {
			message = "Stopped. Nothing was changed."
		}
		if apply && step.ID == "validate" {
			for _, run := range result.Runs {
				if run.Output != nil && run.Output.Report != "" {
					if copied, err := a.hvBuildKeepReport(plan, run.Output.Report); err == nil {
						result.ReportPath = copied
					} else {
						message += " The validation report stayed at " + run.Output.Report + ": " + err.Error()
					}
				}
			}
		}
		if apply {
			a.hvBuildRecord(plan, step, result)
		}
		a.mu.Lock()
		if a.hvBuild.plan != nil && hvBuildRevision(*a.hvBuild.plan) == revision {
			if a.hvBuild.results == nil {
				a.hvBuild.results = map[string]*hvBuildStepResult{}
			}
			a.hvBuild.results[step.ID] = result
			if result.ReportPath != "" {
				a.hvBuild.report = result.ReportPath
			}
			a.hvBuild.message, a.hvBuild.failed = message, result.Failed
			a.hvBuild.serial++
		}
		a.mu.Unlock()
		a.vmwareLog("%s", message)
		a.finishVMwareAction(idle, message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func hvBuildWhereText(computers []string) string {
	if len(computers) == 1 && computers[0] == hvBuildHere {
		return "this computer"
	}
	return strings.Join(computers, ", ")
}

func hvBuildResultMessage(step hvBuildStepInfo, result *hvBuildStepResult) string {
	failures := []string{}
	for _, run := range result.Runs {
		where := run.Computer
		if where == hvBuildHere {
			where = "this computer"
		}
		switch {
		case run.Error != "":
			failures = append(failures, where+": "+run.Error)
		case run.Output != nil && run.Output.Error != "":
			failures = append(failures, where+": "+run.Output.Error)
		case run.Output != nil && run.Output.Refused != "":
			failures = append(failures, where+": "+run.Output.Refused)
		}
	}
	if len(failures) > 0 {
		return step.Title + " stopped. " + strings.Join(failures, " ")
	}
	if result.Mode == "preview" {
		if result.Changes == 0 {
			for _, run := range result.Runs {
				if run.Output != nil && len(run.Output.Blocks) > 0 {
					return step.Title + ": fix what is listed before it can run."
				}
			}
			return step.Title + ": nothing to change. This step is done."
		}
		return fmt.Sprintf("%s: %d change(s) to review. Approve to make them.", step.Title, result.Changes)
	}
	applied := 0
	for _, run := range result.Runs {
		if run.Output != nil {
			applied += len(run.Output.Applied)
		}
	}
	return fmt.Sprintf("%s: made %d change(s). Preview it again to check that nothing is left.", step.Title, applied)
}

// hvBuildOutputFolder is the plan's folder in the output folder, made once
// per session.
func (a *application) hvBuildOutputFolder(plan hvBuildPlan) (string, error) {
	a.mu.Lock()
	folder := a.hvBuild.folder
	a.mu.Unlock()
	if folder == "" {
		folder = a.vmwareOutputFolder("HyperV-Build_" + safeName(plan.Name))
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.hvBuild.folder = folder
	a.mu.Unlock()
	return folder, nil
}

// hvBuildKeepReport copies the validation report Test-Cluster wrote on this
// computer into the plan's output folder.
func (a *application) hvBuildKeepReport(plan hvBuildPlan, report string) (string, error) {
	extension := strings.ToLower(filepath.Ext(report))
	if extension != ".htm" && extension != ".html" && extension != ".mht" {
		return "", errors.New("the report is not an HTML or MHT file")
	}
	source, err := os.Open(report)
	if err != nil {
		return "", err
	}
	defer source.Close()
	folder, err := a.hvBuildOutputFolder(plan)
	if err != nil {
		return "", err
	}
	target := filepath.Join(folder, "Cluster-Validation-"+time.Now().Format("20060102-150405")+extension)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(file, io.LimitReader(source, 256<<20)); err != nil {
		file.Close()
		return "", err
	}
	return target, file.Close()
}

// hvBuildRecord appends what an approved step changed to Build-Log.txt in
// the plan's output folder.
func (a *application) hvBuildRecord(plan hvBuildPlan, step hvBuildStepInfo, result *hvBuildStepResult) {
	folder, err := a.hvBuildOutputFolder(plan)
	if err != nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s (%s)\r\n", time.Now().Format("2006-01-02 15:04:05"), step.Title, plan.Name)
	for _, run := range result.Runs {
		where := run.Computer
		if where == hvBuildHere {
			where, _ = os.Hostname()
			where += " (this computer)"
		}
		switch {
		case run.Skipped:
			fmt.Fprintf(&b, "  %s: not run\r\n", where)
		case run.Error != "":
			fmt.Fprintf(&b, "  %s: failed: %s\r\n", where, run.Error)
		case run.Output != nil:
			for _, line := range run.Output.Applied {
				fmt.Fprintf(&b, "  %s: done: %s\r\n", where, line)
			}
			if run.Output.Error != "" {
				fmt.Fprintf(&b, "  %s: failed: %s\r\n", where, run.Output.Error)
			}
			if run.Output.Refused != "" {
				fmt.Fprintf(&b, "  %s: %s\r\n", where, run.Output.Refused)
			}
			if run.Restarted {
				fmt.Fprintf(&b, "  %s: restarted\r\n", where)
			}
		}
	}
	file, err := os.OpenFile(filepath.Join(folder, "Build-Log.txt"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(b.String() + "\r\n")
	_ = file.Close()
}

// handleHVBuildNetworkMap writes the plan's VM networks into the migration
// plan's network map so Move VMs connects each VM to the right switch and
// VLAN.
func (a *application) handleHVBuildNetworkMap(response http.ResponseWriter, request *http.Request) {
	if !decodeVMwareRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	build, inventory, migration, busy, busyText := a.hvBuild.plan, a.vmware.inventory, a.vmware.plan, a.vmwareBusyLocked(), a.vmwareBusyTextLocked()
	a.mu.Unlock()
	if busy {
		hvBuildFail(response, http.StatusConflict, busyText+" Wait for it to finish first.")
		return
	}
	if build == nil {
		hvBuildFail(response, http.StatusBadRequest, "Save the plan first.")
		return
	}
	if inventory == nil || migration == nil || !strings.EqualFold(inventory.Server, build.Source) {
		hvBuildFail(response, http.StatusBadRequest, "Open the VMware inventory of "+vmNonEmpty(build.Source, "the source")+" in Discovery first.")
		return
	}
	updated, changed := hvBuildApplyNetworkMap(*build, *migration)
	updated.Target = vmTargetHyperV
	updated, err := validateVMPlan(updated, inventory)
	if err != nil {
		hvBuildFail(response, http.StatusBadRequest, err.Error())
		return
	}
	saveErr := a.saveVMwarePlan(updated)
	a.mu.Lock()
	if a.vmware.inventory == inventory {
		a.vmware.plan = &updated
	}
	a.mu.Unlock()
	result := map[string]any{"ok": true, "changed": changed}
	if saveErr != nil {
		result["warning"] = "The network map is in use but could not be saved for next time: " + saveErr.Error()
	}
	writeJSON(response, http.StatusOK, result)
}
