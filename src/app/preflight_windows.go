//go:build windows

package main

// Runs the optional domain controller and SQL Server checks (preflight.go)
// from this computer: Test-RolePreflight.ps1 reaches each server through
// PowerShell remoting (or PowerShell Direct for a VM on this Hyper-V host)
// and only reads. The account stays in memory (a.preflight) until Use my
// Windows account or exit and reaches PowerShell only as one JSON line on
// standard input. The checks change nothing anywhere, so they are not a
// critical operation; Stop takes effect between servers.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed Test-RolePreflight.ps1
var preflightScriptData []byte

const preflightScriptName = "Test-RolePreflight.ps1"

type preflightRuntime struct {
	user     string
	password string
	running  bool
	stop     bool
	checks   []string
	items    []preflightItemState
	message  string
	failed   bool
	// server is the inventory the run belongs to; results for another
	// inventory are saved but not applied to the one open now.
	server string
}

// preflightItemState is one server of the current or last run.
type preflightItemState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Computer string `json:"computer"`
	Via      string `json:"via"`
	State    string `json:"state"` // waiting, running, done, failed, skipped
	Summary  string `json:"summary"`
}

// preflightStateResponse is what the page sees (never the password).
type preflightStateResponse struct {
	Running     bool                 `json:"running"`
	Stopping    bool                 `json:"stopping"`
	User        string               `json:"user"`
	HasPassword bool                 `json:"hasPassword"`
	Checks      []string             `json:"checks"`
	Items       []preflightItemState `json:"items"`
	Message     string               `json:"message"`
	Failed      bool                 `json:"failed"`
	Summary     preflightSummary     `json:"summary"`
	// DirectHere is true on a Hyper-V host, where PowerShell Direct can
	// reach its own VMs.
	DirectHere bool `json:"directHere"`
}

// Caller holds a.mu.
func (a *application) preflightStateLocked() *preflightStateResponse {
	p := a.preflight
	return &preflightStateResponse{Running: p.running, Stopping: p.running && p.stop, User: p.user, HasPassword: p.password != "", Checks: append([]string{}, p.checks...), Items: append([]preflightItemState{}, p.items...), Message: p.message, Failed: p.failed, Summary: summarizePreflight(a.vmware.inventory), DirectHere: vmHyperVHere()}
}

func (a *application) preflightStorePath(server string) string {
	return filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(server)), "preflight.json")
}

func (a *application) loadPreflightStore(server string) map[string]*vmPreflight {
	saved := map[string]*vmPreflight{}
	data, err := os.ReadFile(a.preflightStorePath(server))
	if err != nil || len(data) > 64<<20 {
		return saved
	}
	_ = json.Unmarshal(stripUTF8BOM(data), &saved)
	return saved
}

func (a *application) savePreflightStore(server string, saved map[string]*vmPreflight) error {
	path := a.preflightStorePath(server)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return atomicReplaceFile(path+".tmp", path)
}

// applySavedPreflight puts this server's saved checks on a newly read or
// opened inventory, so a new read of vCenter keeps them.
func (a *application) applySavedPreflight(inventory *vmInventory) {
	if inventory == nil {
		return
	}
	applyPreflightResults(inventory, a.loadPreflightStore(inventory.Server), time.Now())
}

// handlePreflight runs the checks (/run), stops between servers (/stop) or
// forgets the account (/forget).
func (a *application) handlePreflight(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/vmware/preflight") {
	case "/forget":
		if !decodeVMwareRequest(response, request, nil) {
			return
		}
		a.mu.Lock()
		if a.preflight.running {
			a.mu.Unlock()
			fail(http.StatusConflict, "Wait for the checks to finish, or stop them first.")
			return
		}
		a.preflight.user, a.preflight.password = "", ""
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "/stop":
		if !decodeVMwareRequest(response, request, nil) {
			return
		}
		a.mu.Lock()
		if a.preflight.running {
			a.preflight.stop = true
			a.preflight.message = "Stopping after the server being checked now."
		}
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "/run":
		var input struct {
			Items []struct {
				ID       string `json:"id"`
				Computer string `json:"computer"`
				Via      string `json:"via"`
			} `json:"items"`
			Checks   []string `json:"checks"`
			User     string   `json:"user"`
			Password string   `json:"password"`
		}
		if !decodeVMwareRequest(response, request, &input) {
			return
		}
		checks := normalizePreflightChecks(input.Checks)
		if len(checks) == 0 {
			fail(http.StatusBadRequest, "Tick domain controller, SQL Server or both.")
			return
		}
		if len(input.Items) == 0 {
			fail(http.StatusBadRequest, "Choose at least one VM to check.")
			return
		}
		if len(input.Items) > preflightMaxVMs {
			fail(http.StatusBadRequest, fmt.Sprintf("Check at most %d VMs at a time.", preflightMaxVMs))
			return
		}
		user := strings.TrimSpace(input.User)
		if user != "" && !validGuestAccountName(strings.ReplaceAll(user, `\`, "")) {
			fail(http.StatusBadRequest, `Type the account as DOMAIN\user or user@domain, or leave it empty to use your own Windows account.`)
			return
		}
		direct := vmHyperVHere()
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.preflight.running {
			fail(http.StatusConflict, "The checks are already running.")
			return
		}
		inventory := a.vmware.inventory
		if inventory == nil {
			fail(http.StatusBadRequest, "Read or open an inventory first.")
			return
		}
		byID := map[string]vmMachine{}
		for _, vm := range inventory.VMs {
			byID[vm.ID] = vm
		}
		items := []preflightItemState{}
		seen := map[string]bool{}
		for _, item := range input.Items {
			vm, ok := byID[item.ID]
			if !ok {
				fail(http.StatusBadRequest, "A VM is no longer in the inventory open now. Refresh and try again.")
				return
			}
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			via := strings.ToLower(strings.TrimSpace(item.Via))
			if via == "" {
				via = "winrm"
			}
			computer := strings.TrimSpace(item.Computer)
			switch via {
			case "winrm":
				if computer == "" {
					computer = preflightComputerFor(vm)
				}
				if !validPreflightComputer(computer) {
					fail(http.StatusBadRequest, "Type the computer name or IP address of "+vm.Name+": its DNS name is not known.")
					return
				}
			case "direct":
				if !direct || vm.Source != vmSourceHyperV {
					fail(http.StatusBadRequest, "PowerShell Direct reaches only VMs on this Hyper-V host. Use PowerShell remoting for "+vm.Name+".")
					return
				}
				computer = vm.Name
			default:
				fail(http.StatusBadRequest, "Choose PowerShell remoting or PowerShell Direct.")
				return
			}
			items = append(items, preflightItemState{ID: vm.ID, Name: vm.Name, Computer: computer, Via: via, State: "waiting"})
		}
		password := input.Password
		if user != "" {
			if password == "" && strings.EqualFold(user, a.preflight.user) {
				password = a.preflight.password
			}
			if password == "" || len(password) > 256 {
				fail(http.StatusBadRequest, "Type the account's password, or leave the account empty to use your own Windows account.")
				return
			}
			a.preflight.user, a.preflight.password = user, password
		} else if a.preflight.user != "" {
			user, password = a.preflight.user, a.preflight.password
		}
		a.preflight.running, a.preflight.stop, a.preflight.failed = true, false, false
		a.preflight.checks, a.preflight.items, a.preflight.server = checks, items, inventory.Server
		a.preflight.message = fmt.Sprintf("Checking %s (read only).", vmPlural(len(items), "server", "servers"))
		go a.runPreflight(items, checks, user, password, inventory.Server)
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
	default:
		fail(http.StatusNotFound, "Not found.")
	}
}

func (a *application) runPreflight(items []preflightItemState, checks []string, user, password, server string) {
	saved := a.loadPreflightStore(server)
	done, failed, stopped := 0, 0, false
	for index := range items {
		a.mu.Lock()
		if a.preflight.stop {
			for rest := index; rest < len(a.preflight.items); rest++ {
				a.preflight.items[rest].State, a.preflight.items[rest].Summary = "skipped", "Not checked: stopped."
			}
			a.mu.Unlock()
			stopped = true
			break
		}
		a.preflight.items[index].State = "running"
		a.preflight.message = "Checking " + items[index].Name + " (" + items[index].Computer + "), read only."
		a.mu.Unlock()
		item := items[index]
		check := &vmPreflight{CheckedAt: time.Now().UTC(), Computer: item.Computer, Via: item.Via, Checks: checks}
		facts, err := a.runPreflightScript(item, checks, user, password)
		if err != nil {
			check.Error = err.Error()
		} else {
			check.Facts = facts
		}
		check.Summary = preflightFacts(check)
		saved[item.ID] = check
		summary, state := preflightItemSummary(check), "done"
		if check.Error != "" {
			state = "failed"
			failed++
		} else {
			done++
		}
		a.mu.Lock()
		a.preflight.items[index].State, a.preflight.items[index].Summary = state, summary
		if inventory := a.vmware.inventory; inventory != nil && strings.EqualFold(inventory.Server, server) {
			if applyPreflightResults(inventory, map[string]*vmPreflight{item.ID: check}, time.Now()) > 0 {
				a.vmware.inventorySerial++
			}
		}
		a.mu.Unlock()
		a.vmwareLog("%s: %s", item.Name, summary)
	}
	password = ""
	saveErr := a.savePreflightStore(server, saved)
	a.mu.Lock()
	inventory, folder := a.vmware.inventory, a.vmware.outputFolder
	if inventory == nil || !strings.EqualFold(inventory.Server, server) {
		inventory = nil
	}
	a.mu.Unlock()
	reportErr := error(nil)
	if inventory != nil && folder != "" {
		a.mu.Lock()
		data, _ := json.Marshal(inventory)
		a.mu.Unlock()
		var copied vmInventory
		if json.Unmarshal(data, &copied) == nil {
			_, reportErr = writeVMwareInventoryOutputs(folder, &copied)
		}
	}
	message := fmt.Sprintf("Checked %s", vmPlural(done, "server", "servers"))
	if failed > 0 {
		message += fmt.Sprintf("; %d could not be reached", failed)
	}
	if stopped {
		message += "; stopped before the rest"
	}
	message += ". The results are in each VM's readiness"
	if folder != "" && reportErr == nil && inventory != nil {
		message += " and the reports"
	}
	message += "."
	if saveErr != nil {
		message += " They could not be saved for the next read: " + saveErr.Error()
	}
	if reportErr != nil {
		message += " The reports could not be written again: " + reportErr.Error()
	}
	a.mu.Lock()
	a.preflight.running, a.preflight.stop = false, false
	a.preflight.message, a.preflight.failed = message, failed > 0 && done == 0
	a.mu.Unlock()
	a.vmwareLog("%s", message)
}

// preflightItemSummary is one line for the run list: the worst findings.
func preflightItemSummary(check *vmPreflight) string {
	if check.Error != "" {
		return "Not reached: " + check.Error
	}
	parts := []string{}
	if check.domainController() {
		parts = append(parts, "domain controller")
	}
	if check.sqlServer() {
		parts = append(parts, vmPlural(len(check.Facts.SQL.Instances), "SQL Server instance", "SQL Server instances"))
	}
	if len(parts) == 0 {
		parts = append(parts, "no domain controller or SQL Server found")
	}
	blockers, warnings := 0, 0
	for _, finding := range preflightFindings(check) {
		switch finding.Severity {
		case vmSeverityBlocker:
			blockers++
		case vmSeverityWarning:
			warnings++
		}
	}
	text := strings.Join(parts, ", ")
	switch {
	case blockers > 0:
		text += fmt.Sprintf(": %s to fix first", vmPlural(blockers, "blocker", "blockers"))
		if warnings > 0 {
			text += fmt.Sprintf(", %s", vmPlural(warnings, "item to review", "items to review"))
		}
	case warnings > 0:
		text += ": " + vmPlural(warnings, "item to review", "items to review")
	default:
		text += ": checks passed"
	}
	return text + "."
}

func (a *application) runPreflightScript(item preflightItemState, checks []string, user, password string) (*vmPreflightFacts, error) {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "Preflight-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, preflightScriptName)
	if err := os.WriteFile(script, preflightScriptData, 0600); err != nil {
		return nil, err
	}
	resultPath := filepath.Join(directory, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Transport", item.Via, "-ResultPath", resultPath, "-Checks", strings.Join(checks, ",")}
	if item.Via == "direct" {
		arguments = append(arguments, "-VMName", item.Computer)
	} else {
		arguments = append(arguments, "-ComputerName", item.Computer)
	}
	arguments = append(arguments, "-CredentialFromInput")
	command := exec.Command(a.powerShellPath, arguments...)
	line, _ := json.Marshal(map[string]string{"user": user, "password": password})
	command.Stdin = bytes.NewReader(append(line, '\n'))
	waitErr := a.runEngineProcess(command, func(text string) {
		a.mu.Lock()
		a.appendVMwareLogLocked(text)
		a.mu.Unlock()
	}, func(*exec.Cmd) {})
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return nil, fmt.Errorf("the checks stopped: %v", waitErr)
		}
		return nil, fmt.Errorf("the checks wrote no result")
	}
	result, err := parsePreflightResult(data)
	if err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("%s", strings.TrimSpace(result.Error))
	}
	return result.Facts, nil
}
