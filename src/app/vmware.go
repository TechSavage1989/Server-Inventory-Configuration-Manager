//go:build windows

package main

import (
	"context"
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

// The VMware tab: read-only vCenter/ESXi inventory and readiness, the
// migration plan and runbook, the guest pre-flight and fix-up that run inside
// a Windows VM (Migrate-VmGuest.ps1), and the before and after comparison.
// Every action is a critical operation: updates cannot install, and
// collections, restores and file migrations cannot start, while one runs.

const (
	guestEngineName = "Migrate-VmGuest.ps1"
	vmwareLogLimit  = 400
)

type vmwareRuntime struct {
	action          string
	status          string
	message         string
	server          string
	account         string
	password        string
	thumbprint      string
	certificate     *vmCertificateError
	inventory       *vmInventory
	inventorySerial uint64
	outputFolder    string
	reportPath      string
	runbookPath     string
	planCSVPath     string
	plan            *vmPlan
	guestResult     json.RawMessage
	guestSerial     uint64
	guestMessage    string
	guestError      bool
	prepare         *vmPrepareResult
	prepareSerial   uint64
	prepareError    string
	prepareBatch    []vmPrepareBatchItem
	move            *vmMoveResult
	moveSerial      uint64
	moveError       string
	send            []vmAgentSendResult
	sendSerial      uint64
	sendError       string
	compare         *compareResult
	compareSerial   uint64
	compareFolder   string
	compareError    string
	target          vmTargetRuntime
	cancel          context.CancelFunc
	cancelRequested bool
	logs            []logEntry
	nextLog         int
	startedAt       time.Time
	endedAt         time.Time
}

type vmwareStateResponse struct {
	Running         bool                 `json:"running"`
	Action          string               `json:"action"`
	Status          string               `json:"status"`
	Message         string               `json:"message"`
	Server          string               `json:"server"`
	Account         string               `json:"account"`
	Certificate     *vmCertificateError  `json:"certificate"`
	Summary         *vmInventorySummary  `json:"summary"`
	InventorySerial uint64               `json:"inventorySerial"`
	OutputFolder    string               `json:"outputFolder"`
	ReportPath      string               `json:"reportPath"`
	RunbookPath     string               `json:"runbookPath"`
	RunbookDocxPath string               `json:"runbookDocxPath"`
	DiscoveryPath   string               `json:"discoveryPath"`
	ReportDocxPath  string               `json:"reportDocxPath"`
	ReportPDFPath   string               `json:"reportPdfPath"`
	PlanCSVPath     string               `json:"planCsvPath"`
	GuestSerial     uint64               `json:"guestSerial"`
	GuestMessage    string               `json:"guestMessage"`
	GuestError      bool                 `json:"guestError"`
	GuestWorkPath   string               `json:"guestWorkPath"`
	GuestTarget     *guestTargetState    `json:"guestTarget"`
	CompareSerial   uint64               `json:"compareSerial"`
	CompareError    string               `json:"compareError"`
	Prepare         *vmPrepareResult     `json:"prepare"`
	PrepareSerial   uint64               `json:"prepareSerial"`
	PrepareError    string               `json:"prepareError"`
	PrepareBatch    []vmPrepareBatchItem `json:"prepareBatch"`
	CanPrepare      bool                 `json:"canPrepare"`
	HyperVHere      bool                 `json:"hyperVHere"`
	Move            *vmMoveResult        `json:"move"`
	MoveSerial      uint64               `json:"moveSerial"`
	MoveError       string               `json:"moveError"`
	MoveCopyRoot    string               `json:"moveCopyRoot"`
	HostStorage     *vmHostStorage       `json:"hostStorage"`
	// HVRemote is the remote Hyper-V host or cluster Move VMs is connected
	// to from this computer (never its password).
	HVRemote *hvRemoteStateResponse `json:"hvRemote"`
	// HVBuild is Build Hyper-V hosts: its plan, steps, node facts and step
	// results (never the nodes' password or a cloud witness key).
	HVBuild *hvBuildStateResponse `json:"hvBuild"`
	// Transfers are the disks this computer is sending or moving, running
	// first, and those finished in the last hour.
	Transfers       []vmTransfer           `json:"transfers"`
	Target          *vmTargetStateResponse `json:"target"`
	Send            []vmAgentSendResult    `json:"send"`
	SendSerial      uint64                 `json:"sendSerial"`
	SendError       string                 `json:"sendError"`
	Receiver        vmReceiverState        `json:"receiver"`
	CancelRequested bool                   `json:"cancelRequested"`
	Logs            []logEntry             `json:"logs"`
	StartedAtUnixMs int64                  `json:"startedAtUnixMs"`
	EndedAtUnixMs   int64                  `json:"endedAtUnixMs"`
}

func (a *application) vmwareRoot() string { return filepath.Join(a.update.appDataRoot, "VMware") }

func (a *application) guestWorkPath() string {
	return filepath.Join(a.update.appDataRoot, "Guest-Migration")
}

func (a *application) vmwarePlanPath(server string) string {
	return filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(server)), "plan.json")
}

// Caller holds a.mu.
func (a *application) vmwareBusyLocked() bool { return a.vmware.action != "" }

// Caller holds a.mu.
func (a *application) vmwareSnapshotLocked() vmwareStateResponse {
	millis := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixNano() / int64(time.Millisecond)
	}
	status := a.vmware.status
	if status == "" {
		status = "idle"
	}
	response := vmwareStateResponse{
		Running: a.vmware.action != "", Action: a.vmware.action, Status: status, Message: a.vmware.message,
		Server: a.vmware.server, Account: a.vmware.account, Certificate: a.vmware.certificate,
		InventorySerial: a.vmware.inventorySerial, OutputFolder: a.vmware.outputFolder, ReportPath: a.vmware.reportPath,
		RunbookPath: a.vmware.runbookPath, RunbookDocxPath: vmRunbookDocxFor(a.vmware.runbookPath), DiscoveryPath: vmDiscoveryReportFor(a.vmware.reportPath), ReportDocxPath: vmReportBeside(a.vmware.reportPath, vmReportDocx), ReportPDFPath: vmReportBeside(a.vmware.reportPath, vmReportPDF), PlanCSVPath: a.vmware.planCSVPath,
		GuestSerial: a.vmware.guestSerial, GuestMessage: a.vmware.guestMessage, GuestError: a.vmware.guestError, GuestWorkPath: a.guestWorkPath(), GuestTarget: a.guestTargetStateLocked(),
		CompareSerial: a.vmware.compareSerial, CompareError: a.vmware.compareError,
		Prepare: a.vmware.prepare, PrepareSerial: a.vmware.prepareSerial, PrepareError: a.vmware.prepareError, PrepareBatch: append([]vmPrepareBatchItem{}, a.vmware.prepareBatch...),
		Move: a.vmware.move, MoveSerial: a.vmware.moveSerial, MoveError: a.vmware.moveError, MoveCopyRoot: a.vmwareCopyRootLocked(),
		Send: a.vmware.send, SendSerial: a.vmware.sendSerial, SendError: a.vmware.sendError, Receiver: a.vmReceiverStateLocked(),
		HyperVHere:      vmHyperVHere(),
		HostStorage:     vmHostStorageNow(),
		HVRemote:        a.hvRemoteStateLocked(),
		HVBuild:         a.hvBuildStateLocked(),
		Transfers:       []vmTransfer{},
		CanPrepare:      a.vmware.inventory != nil && a.vmware.password != "" && strings.EqualFold(a.vmware.inventory.Server, a.vmware.server),
		CancelRequested: a.vmware.cancelRequested && a.vmware.action != "",
		Target:          a.vmTargetStateLocked(),
		Logs:            append([]logEntry{}, a.vmware.logs...),
		StartedAtUnixMs: millis(a.vmware.startedAt), EndedAtUnixMs: millis(a.vmware.endedAt),
	}
	if a.transfers != nil {
		// The board has its own lock; it never takes a.mu.
		response.Transfers = a.transfers.List()
	}
	if a.vmware.inventory != nil {
		summary := summarizeInventory(a.vmware.inventory)
		response.Summary = &summary
	}
	return response
}

// Caller holds a.mu.
func (a *application) appendVMwareLogLocked(text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return
	}
	a.vmware.logs = append(a.vmware.logs, logEntry{Index: a.vmware.nextLog, Text: text})
	a.vmware.nextLog++
	if len(a.vmware.logs) > vmwareLogLimit {
		a.vmware.logs = append([]logEntry{}, a.vmware.logs[len(a.vmware.logs)-vmwareLogLimit:]...)
	}
}

func (a *application) vmwareLog(format string, values ...any) {
	a.mu.Lock()
	a.appendVMwareLogLocked(time.Now().Format("2006-01-02 15:04:05") + " " + fmt.Sprintf(format, values...))
	a.mu.Unlock()
}

// startVMwareAction claims the VMware tab for one action. Caller must not
// hold a.mu.
func (a *application) startVMwareAction(action string) (int, string) {
	if !a.beginCriticalOperation() {
		return http.StatusConflict, "An update is being installed."
	}
	a.mu.Lock()
	refusal := ""
	switch {
	case a.state.Running:
		refusal = "A collection is running. Wait for it to finish first."
	case a.restoreBusyLocked():
		refusal = "A restore is running. Wait for it to finish first."
	case a.migrateBusyLocked():
		refusal = "A file migration step is running. Wait for it to finish first."
	case a.vmwareBusyLocked():
		refusal = "Another VMware step is running. Wait for it to finish first."
	case a.convertBusyLocked():
		refusal = "A disk conversion is running. Wait for it to finish first."
	default:
		a.vmware.action = action
		a.vmware.cancelRequested = false
		a.vmware.cancel = nil
		a.vmware.startedAt = time.Now()
		a.vmware.endedAt = time.Time{}
	}
	a.mu.Unlock()
	if refusal != "" {
		a.endCriticalOperation()
		return http.StatusConflict, refusal
	}
	return 0, ""
}

func (a *application) finishVMwareAction(status, message string) {
	a.mu.Lock()
	a.vmware.action = ""
	a.vmware.cancel = nil
	a.vmware.status = status
	if message != "" {
		a.vmware.message = message
	}
	a.vmware.endedAt = time.Now()
	a.mu.Unlock()
	a.endCriticalOperation()
}

func decodeVMwareRequest(response http.ResponseWriter, request *http.Request, value any) bool {
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return false
	}
	if value == nil {
		return true
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 4<<20)).Decode(value); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The request was invalid."})
		return false
	}
	return true
}

// vmwareOutputFolder is a new folder in the collection output folder.
func (a *application) vmwareOutputFolder(prefix string) string {
	a.mu.Lock()
	root := a.defaultOutputRoot
	a.mu.Unlock()
	if strings.TrimSpace(root) == "" {
		root = defaultOutputRoot()
	}
	return filepath.Join(root, prefix+"_"+time.Now().Format("20060102_150405"))
}

func (a *application) loadVMwarePlan(inventory *vmInventory) vmPlan {
	data, err := os.ReadFile(a.vmwarePlanPath(inventory.Server))
	if err == nil {
		var saved vmPlan
		if json.Unmarshal(data, &saved) == nil && strings.EqualFold(saved.Server, inventory.Server) {
			return reconcileVMPlan(saved, inventory)
		}
	}
	return defaultVMPlan(inventory)
}

func (a *application) saveVMwarePlan(plan vmPlan) error {
	path := a.vmwarePlanPath(plan.Server)
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

// ---- Environment: connect and read ------------------------------------

var vmwareUserPattern = regexp.MustCompile(`^[^\x00-\x1f"<>|]{1,256}$`)

func (a *application) handleVMwareConnect(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Server     string `json:"server"`
		User       string `json:"user"`
		Password   string `json:"password"`
		Thumbprint string `json:"thumbprint"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	server := strings.TrimSpace(input.Server)
	endpoint, err := vmServerURL(server)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	user := strings.TrimSpace(input.User)
	if !vmwareUserPattern.MatchString(user) || len(input.Password) > 256 || strings.ContainsRune(input.Password, 0) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type a vCenter or ESXi user name, for example administrator@vsphere.local or root, and its password."})
		return
	}
	thumbprint := ""
	if strings.TrimSpace(input.Thumbprint) != "" {
		if thumbprint = vmNormalizeThumbprint(input.Thumbprint); thumbprint == "" {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The certificate thumbprint is not a SHA-256 thumbprint."})
			return
		}
	}
	a.mu.Lock()
	password := input.Password
	input.Password = ""
	if password == "" && strings.EqualFold(a.vmware.server, endpoint.Host) && strings.EqualFold(a.vmware.account, user) {
		password = a.vmware.password
	}
	if thumbprint == "" && strings.EqualFold(a.vmware.server, endpoint.Host) {
		thumbprint = a.vmware.thumbprint
	}
	a.mu.Unlock()
	if password == "" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the password for " + user + "."})
		return
	}
	if status, message := a.startVMwareAction("connect"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if !strings.EqualFold(a.vmware.server, endpoint.Host) {
		a.vmware.inventory, a.vmware.plan, a.vmware.reportPath, a.vmware.outputFolder, a.vmware.runbookPath, a.vmware.planCSVPath = nil, nil, "", "", "", ""
		a.vmware.thumbprint = ""
	}
	a.vmware.server, a.vmware.account, a.vmware.password = endpoint.Host, user, password
	a.vmware.certificate = nil
	a.vmware.cancel = cancel
	a.vmware.status = "connecting"
	a.vmware.message = "Connecting to " + endpoint.Host + " as " + user + "."
	a.appendVMwareLogLocked(time.Now().Format("2006-01-02 15:04:05") + " " + a.vmware.message)
	a.mu.Unlock()
	go func() {
		defer cancel()
		inventory, err := collectVSphere(ctx, vmConnectOptions{Server: endpoint.Host, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}, func(text string) { a.vmwareLog("%s", text) })
		password = ""
		if err != nil {
			var certificate *vmCertificateError
			a.mu.Lock()
			if errors.As(err, &certificate) {
				a.vmware.certificate = certificate
			}
			canceled := a.vmware.cancelRequested
			a.mu.Unlock()
			message := err.Error()
			if canceled {
				message = "Stopped. Nothing was read."
			}
			a.vmwareLog("%s", message)
			a.finishVMwareAction("connectfailed", message)
			return
		}
		folder := a.vmwareOutputFolder("VMware_" + safeName(inventory.Server))
		report, writeErr := writeVMwareInventoryOutputs(folder, inventory)
		plan := a.loadVMwarePlan(inventory)
		a.mu.Lock()
		a.vmware.inventory = inventory
		a.vmware.inventorySerial++
		a.vmware.plan = &plan
		a.vmware.thumbprint = thumbprint
		a.vmware.runbookPath, a.vmware.planCSVPath = "", ""
		if writeErr == nil {
			a.vmware.outputFolder, a.vmware.reportPath = folder, report
		} else {
			a.vmware.outputFolder, a.vmware.reportPath = "", ""
		}
		a.mu.Unlock()
		summary := summarizeInventory(inventory)
		message := fmt.Sprintf("Read %d VM(s) on %d host(s) from %s.", summary.VMs, summary.Hosts, inventory.Server)
		if writeErr != nil {
			message += " The report files could not be written: " + writeErr.Error()
		}
		a.vmwareLog("%s", message)
		a.finishVMwareAction("connected", message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleVMwareCancel(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	cancel := a.vmware.cancel
	if cancel != nil {
		a.vmware.cancelRequested = true
	}
	a.mu.Unlock()
	if cancel == nil {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "This step cannot be stopped."})
		return
	}
	cancel()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// handleVMwareOpenInventory loads a saved VMware-Inventory.json so a plan can
// be made away from the customer's network.
func (a *application) handleVMwareOpenInventory(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	if status, message := a.startVMwareAction("open"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	selected, ok, err := chooseFolder("Choose a VMware inventory folder (the one that contains " + vmInventoryFile + ").")
	if err != nil || !ok {
		a.finishVMwareAction(a.vmwareIdleStatus(), "")
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": false})
		return
	}
	inventory, err := readVMwareInventoryFolder(selected)
	if err != nil {
		a.finishVMwareAction(a.vmwareIdleStatus(), "")
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	plan := a.loadVMwarePlan(inventory)
	a.mu.Lock()
	a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint, a.vmware.certificate = inventory.Server, "", "", "", nil
	a.vmware.inventory = inventory
	a.vmware.inventorySerial++
	a.vmware.plan = &plan
	a.vmware.outputFolder = filepath.Clean(selected)
	a.vmware.reportPath = existingFile(filepath.Join(selected, vmReadinessReport))
	a.vmware.runbookPath = existingFile(filepath.Join(selected, vmRunbookReport))
	a.vmware.planCSVPath = existingFile(filepath.Join(selected, vmPlanCSV))
	a.mu.Unlock()
	message := fmt.Sprintf("Opened the inventory of %s read on %s.", inventory.Server, inventory.CollectedAt.Local().Format("2006-01-02 15:04"))
	a.vmwareLog("%s", message)
	a.finishVMwareAction("connected", message)
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": true})
}

func (a *application) vmwareIdleStatus() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vmware.inventory != nil {
		return "connected"
	}
	return "idle"
}

func readVMwareInventoryFolder(folder string) (*vmInventory, error) {
	path := filepath.Join(folder, vmInventoryFile)
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("That folder has no " + vmInventoryFile + ". Choose a folder written by Read inventory.")
	}
	if info.Size() > 512<<20 {
		return nil, errors.New("The inventory file is too large.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inventory vmInventory
	if err := json.Unmarshal(stripUTF8BOM(data), &inventory); err != nil || inventory.Schema != 1 || inventory.Server == "" {
		return nil, errors.New("The inventory file could not be read.")
	}
	// Readiness rules may have improved since the file was written.
	assessInventory(&inventory, time.Now())
	return &inventory, nil
}

func (a *application) handleVMwareInventory(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	a.mu.Lock()
	inventory, plan, serial := a.vmware.inventory, a.vmware.plan, a.vmware.inventorySerial
	a.mu.Unlock()
	if inventory == nil || plan == nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "inventory": nil})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "serial": serial, "inventory": inventory, "plan": plan, "sizing": sizePlan(*plan, inventory), "attention": vmDiscoveryWatch(inventory, time.Now())})
}

func (a *application) handleVMwarePlan(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Plan        vmPlan `json:"plan"`
		Suggest     bool   `json:"suggest"`
		WaveSize    int    `json:"waveSize"`
		ResetManual bool   `json:"resetManual"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	inventory, busy := a.vmware.inventory, a.vmwareBusyLocked()
	a.mu.Unlock()
	if busy {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the current step to finish."})
		return
	}
	plan, err := validateVMPlan(input.Plan, inventory)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if input.Suggest {
		// 0 is Auto: about four waves of running servers.
		if input.WaveSize < 0 || input.WaveSize > 200 {
			input.WaveSize = 0
		}
		plan, _ = suggestWavesWith(plan, inventory, input.WaveSize, input.ResetManual)
	}
	saveErr := a.saveVMwarePlan(plan)
	a.mu.Lock()
	if a.vmware.inventory == inventory {
		a.vmware.plan = &plan
	}
	a.mu.Unlock()
	result := map[string]any{"ok": true, "plan": plan, "sizing": sizePlan(plan, inventory), "saved": saveErr == nil}
	if saveErr != nil {
		result["warning"] = "The plan is in use but could not be saved for next time: " + saveErr.Error()
	}
	writeJSON(response, http.StatusOK, result)
}

func (a *application) handleVMwareExportPlan(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	if status, message := a.startVMwareAction("export"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	a.mu.Lock()
	inventory, plan, folder := a.vmware.inventory, a.vmware.plan, a.vmware.outputFolder
	a.mu.Unlock()
	if inventory == nil || plan == nil {
		a.finishVMwareAction(a.vmwareIdleStatus(), "")
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Read a VMware inventory first."})
		return
	}
	if folder == "" {
		folder = a.vmwareOutputFolder("VMware_" + safeName(inventory.Server))
	}
	runbook, planCSV, err := writeVMwarePlanOutputs(folder, inventory, *plan)
	if err != nil {
		a.finishVMwareAction("connected", "")
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The runbook could not be written: " + err.Error()})
		return
	}
	a.mu.Lock()
	a.vmware.outputFolder, a.vmware.runbookPath, a.vmware.planCSVPath = folder, runbook, planCSV
	a.mu.Unlock()
	a.vmwareLog("Wrote the runbook and plan to %s.", folder)
	a.finishVMwareAction("connected", "Wrote the runbook and plan.")
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "runbookPath": runbook, "planCsvPath": planCSV})
}

func (a *application) handleVMwareReset(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vmwareBusyLocked() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the current step to finish."})
		return
	}
	logs, next, guest, guestSerial, guestMessage, compare, compareSerial, compareFolder := a.vmware.logs, a.vmware.nextLog, a.vmware.guestResult, a.vmware.guestSerial, a.vmware.guestMessage, a.vmware.compare, a.vmware.compareSerial, a.vmware.compareFolder
	serial := a.vmware.inventorySerial + 1
	a.vmware = vmwareRuntime{logs: logs, nextLog: next, guestResult: guest, guestSerial: guestSerial, guestMessage: guestMessage, compare: compare, compareSerial: compareSerial, compareFolder: compareFolder, inventorySerial: serial}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

// ---- Guest pre-flight and fix-up -----------------------------------------

var (
	guestMACPattern        = regexp.MustCompile(`^[0-9A-F]{2}(-[0-9A-F]{2}){5}$`)
	guestProductKeyPattern = regexp.MustCompile(`^[A-Z0-9]{5}(-[A-Z0-9]{5}){4}$`)
	guestInstancePattern   = regexp.MustCompile(`^(PCI|VMBUS|USB)\\[A-Za-z0-9_&.{}\\#\-]{1,400}$`)
)

var guestModes = map[string]string{
	"capture":       "Capture",
	"status":        "Status",
	"apply":         "ApplyNetwork",
	"undo":          "UndoNetwork",
	"remove-ghosts": "RemoveGhostNics",
	"remove-tools":  "RemoveVmwareTools",
	"online-disks":  "OnlineDisks",
	"bitlocker":     "SaveBitLocker",
	"activate":      "Activate",
}

// guestChangingModes change this computer, so they need the typed computer
// name.
var guestChangingModes = map[string]bool{"apply": true, "undo": true, "remove-ghosts": true, "remove-tools": true, "online-disks": true, "bitlocker": true, "activate": true}

func (a *application) handleVMwareGuest(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	action := strings.TrimPrefix(request.URL.Path, "/api/vmware/guest/")
	if request.Method == http.MethodGet && (action == "" || action == "/api/vmware/guest") {
		a.mu.Lock()
		result, serial := a.vmware.guestResult, a.vmware.guestSerial
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "serial": serial, "result": result})
		return
	}
	mode, known := guestModes[action]
	if !known {
		writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "Unknown guest action."})
		return
	}
	var input struct {
		Confirm string            `json:"confirm"`
		Items   []json.RawMessage `json:"items"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	target := a.guestRemote
	a.mu.Unlock()
	if target.Mode == "" {
		target.Mode = "local"
	}
	// PowerShell Direct needs Hyper-V administrator rights here; remoting
	// uses the typed account (or this one) on the other computer.
	if !a.admin && (target.Mode == "local" || target.Mode == "direct") {
		writeJSON(response, http.StatusForbidden, map[string]any{"ok": false, "error": "The guest checks need administrator rights. Close the application and run it as administrator."})
		return
	}
	label := guestTargetLabel(target, a.computerName)
	if guestChangingModes[action] && !strings.EqualFold(strings.TrimSpace(input.Confirm), label) {
		what := "this computer's name"
		if target.Mode != "local" {
			what = "the name of the machine being changed"
		}
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type " + what + ", " + label + ", to confirm."})
		return
	}
	plan, err := validateGuestItems(action, input.Items)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if status, message := a.startVMwareAction("guest-" + action); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	a.mu.Lock()
	a.vmware.guestMessage, a.vmware.guestError = guestRunningMessage(action), false
	a.mu.Unlock()
	go func() {
		result, err := a.runGuestEngine(mode, plan)
		if err == nil && action != "status" && action != "capture" {
			// Show the effect of a change straight away.
			if status, statusErr := a.runGuestEngine("Status", nil); statusErr == nil {
				a.mu.Lock()
				a.vmware.guestResult = status
				a.vmware.guestSerial++
				a.mu.Unlock()
			}
		}
		a.mu.Lock()
		if err != nil {
			a.vmware.guestMessage, a.vmware.guestError = err.Error(), true
		} else {
			a.vmware.guestMessage = guestDoneMessage(action, result)
			if action == "status" || action == "capture" {
				a.vmware.guestResult = result
				a.vmware.guestSerial++
			}
		}
		message := a.vmware.guestMessage
		a.mu.Unlock()
		if action == "capture" && err == nil {
			// Read the comparison view too, so the tab shows the capture and the current state.
			if status, statusErr := a.runGuestEngine("Status", nil); statusErr == nil {
				a.mu.Lock()
				a.vmware.guestResult = status
				a.vmware.guestSerial++
				a.mu.Unlock()
			}
		}
		a.finishVMwareAction(a.vmwareIdleStatus(), "")
		a.vmwareLog("%s", message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func guestRunningMessage(action string) string {
	return map[string]string{
		"capture": "Recording this VM's settings.", "status": "Reading this VM's current state.", "apply": "Putting back the IP settings.",
		"undo": "Restoring the adapter settings saved before the change.", "remove-ghosts": "Removing hidden network adapters.",
		"remove-tools": "Removing VMware Tools. This can take a few minutes.", "online-disks": "Bringing disks online.",
		"bitlocker": "Saving the BitLocker recovery keys and suspending BitLocker.",
	}[action]
}

func guestDoneMessage(action string, result json.RawMessage) string {
	switch action {
	case "capture":
		return "Recorded this VM's settings. Keep this application on the VM: after cutover, open this tab again to put them back."
	case "status":
		return "Read this VM's current state."
	case "apply":
		return "Put back the IP settings. Check the results below."
	case "undo":
		return "Restored the adapter settings saved before the change."
	case "remove-ghosts":
		return "Removed the hidden network adapters."
	case "remove-tools":
		var data struct {
			RestartRequired bool `json:"RestartRequired"`
			AlreadyRemoved  bool `json:"AlreadyRemoved"`
		}
		_ = json.Unmarshal(result, &data)
		if data.AlreadyRemoved {
			return "VMware Tools was already removed."
		}
		if data.RestartRequired {
			return "Removed VMware Tools. Restart the VM to finish."
		}
		return "Removed VMware Tools."
	case "online-disks":
		return "Brought the chosen disks online."
	case "bitlocker":
		var saved struct {
			NoBitLocker bool
			KeyFile     string
			Suspended   []string
		}
		if json.Unmarshal(result, &saved) == nil {
			if saved.NoBitLocker {
				return "BitLocker is not on, so there were no keys to save."
			}
			suspended := "BitLocker was already suspended."
			if len(saved.Suspended) > 0 {
				suspended = "Suspended BitLocker on " + strings.Join(saved.Suspended, ", ") + " until it is resumed."
			}
			return "Saved the BitLocker recovery keys to " + saved.KeyFile + ". " + suspended + " Keep that file safe and delete it after the move."
		}
		return "Saved the BitLocker recovery keys."
	}
	return "Done."
}

// validateGuestItems checks the items the page sends and returns the plan
// file the engine reads.
func validateGuestItems(action string, items []json.RawMessage) ([]byte, error) {
	switch action {
	case "capture", "status", "undo", "remove-tools", "bitlocker":
		return nil, nil
	case "activate":
		if len(items) == 0 {
			return nil, nil // activate with the key already installed
		}
		if len(items) > 1 {
			return nil, errors.New("Enter one product key.")
		}
	}
	if len(items) == 0 || len(items) > 64 {
		return nil, errors.New("Choose at least one item.")
	}
	clean := []map[string]any{}
	for _, raw := range items {
		switch action {
		case "apply":
			var item struct {
				Capture        string `json:"capture"`
				InterfaceIndex int    `json:"interfaceIndex"`
				Rename         bool   `json:"rename"`
			}
			if json.Unmarshal(raw, &item) != nil || !guestMACPattern.MatchString(strings.ToUpper(item.Capture)) || item.InterfaceIndex <= 0 || item.InterfaceIndex > 1<<24 {
				return nil, errors.New("Choose a new adapter for each old adapter.")
			}
			clean = append(clean, map[string]any{"capture": strings.ToUpper(item.Capture), "interfaceIndex": item.InterfaceIndex, "rename": item.Rename})
		case "remove-ghosts":
			var item struct {
				InstanceID string `json:"instanceId"`
			}
			if json.Unmarshal(raw, &item) != nil || !guestInstancePattern.MatchString(item.InstanceID) {
				return nil, errors.New("Choose hidden adapters from the list.")
			}
			clean = append(clean, map[string]any{"instanceId": item.InstanceID})
		case "activate":
			var item struct {
				ProductKey string `json:"productKey"`
			}
			key := ""
			if json.Unmarshal(raw, &item) == nil {
				key = strings.ToUpper(strings.TrimSpace(item.ProductKey))
			}
			if !guestProductKeyPattern.MatchString(key) {
				return nil, errors.New("Type the product key as XXXXX-XXXXX-XXXXX-XXXXX-XXXXX.")
			}
			clean = append(clean, map[string]any{"productKey": key})
		case "online-disks":
			var item struct {
				Number int `json:"number"`
			}
			if json.Unmarshal(raw, &item) != nil || item.Number < 0 || item.Number > 4096 {
				return nil, errors.New("Choose disks from the list.")
			}
			clean = append(clean, map[string]any{"number": item.Number})
		}
	}
	return json.Marshal(map[string]any{"items": clean})
}

func (a *application) runGuestEngine(mode string, plan []byte) (json.RawMessage, error) {
	a.mu.Lock()
	target := a.guestRemote
	a.mu.Unlock()
	if target.Mode != "" && target.Mode != "local" {
		return a.runGuestRemote(target, mode, plan)
	}
	runtimeDir, script, err := a.prepareRestoreEngineNamed("Guest-", guestEngineName)
	if err != nil {
		return nil, fmt.Errorf("the guest engine could not be prepared: %v", err)
	}
	defer os.RemoveAll(runtimeDir)
	work := a.guestWorkPath()
	if err := os.MkdirAll(work, 0700); err != nil {
		return nil, err
	}
	resultPath := filepath.Join(runtimeDir, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-Mode", mode, "-WorkPath", work, "-ResultPath", resultPath}
	if mode == "Capture" {
		arguments = append(arguments, "-ExportPath", a.vmwareOutputFolder("Guest-Capture_"+safeName(a.computerName)))
	}
	if mode == "SaveBitLocker" {
		arguments = append(arguments, "-ExportPath", a.vmwareOutputFolder("BitLocker-Keys_"+safeName(a.computerName)))
	}
	if plan != nil {
		planPath := filepath.Join(runtimeDir, "Plan.json")
		if err := os.WriteFile(planPath, plan, 0600); err != nil {
			return nil, err
		}
		arguments = append(arguments, "-PlanPath", planPath)
	}
	command := exec.Command(a.powerShellPath, arguments...)
	waitErr := a.runEngineProcess(command, func(line string) {
		a.mu.Lock()
		a.appendVMwareLogLocked(line)
		a.mu.Unlock()
	}, func(*exec.Cmd) {})
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return nil, fmt.Errorf("the guest engine stopped: %v", waitErr)
		}
		return nil, errors.New("the guest engine wrote no result")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stripUTF8BOM(data), &envelope); err != nil {
		return nil, errors.New("the guest engine's result could not be read")
	}
	if !envelope.OK {
		return nil, errors.New(vmNonEmpty(envelope.Error, "The step failed."))
	}
	return envelope.Result, nil
}

// handleVMwareGuestLoad copies a capture saved elsewhere (for example the
// copy written to the output folder) into this account's work folder.
func (a *application) handleVMwareGuestLoad(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	if status, message := a.startVMwareAction("guest-load"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	defer a.finishVMwareAction(a.vmwareIdleStatus(), "")
	selected, ok, err := chooseFolder("Choose the folder that holds the Guest-Capture file recorded on this VM before cutover.")
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !ok {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": false})
		return
	}
	matches, _ := filepath.Glob(filepath.Join(selected, "Guest-Capture*.json"))
	newest, newestTime := "", time.Time{}
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.Size() < 32<<20 && info.ModTime().After(newestTime) {
			newest, newestTime = match, info.ModTime()
		}
	}
	if newest == "" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "That folder has no Guest-Capture file."})
		return
	}
	data, err := os.ReadFile(newest)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	var capture struct {
		Schema int `json:"Schema"`
		State  struct {
			ComputerName string `json:"ComputerName"`
		} `json:"State"`
	}
	if json.Unmarshal(stripUTF8BOM(data), &capture) != nil || capture.Schema != 1 || capture.State.ComputerName == "" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The file is not a guest capture."})
		return
	}
	a.mu.Lock()
	remote := a.guestRemote
	a.mu.Unlock()
	if remote.Mode != "" && remote.Mode != "local" {
		// For another machine the console keeps the recording and hands it
		// to the guest engine there at each run.
		var whole any
		if json.Unmarshal(stripUTF8BOM(data), &whole) != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The file is not a guest capture."})
			return
		}
		if _, err := a.writeGuestCapture(whole, "Guest-Capture.json", remote.VM, remote.Computer, capture.State.ComputerName); err != nil {
			writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The capture could not be saved."})
			return
		}
		a.vmwareLog("Loaded the capture %s for %s.", newest, guestTargetLabel(remote, a.computerName))
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": true})
		return
	}
	if !strings.EqualFold(capture.State.ComputerName, a.computerName) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "That capture was recorded on " + capture.State.ComputerName + ", not on this computer (" + a.computerName + ")."})
		return
	}
	work := a.guestWorkPath()
	if err := os.MkdirAll(work, 0700); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	target := filepath.Join(work, "Guest-Capture.json")
	if err := os.WriteFile(target+".tmp", data, 0600); err != nil || atomicReplaceFile(target+".tmp", target) != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The capture could not be saved."})
		return
	}
	a.vmwareLog("Loaded the capture %s.", newest)
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": true})
}

// ---- Before and after comparison -----------------------------------------

func (a *application) handleVMwareCompareBrowse(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Side string `json:"side"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()
	title := "Choose the inventory package collected BEFORE the move (the folder that contains 00-Summary)."
	if input.Side == "after" {
		title = "Choose the inventory package collected AFTER the move (the folder that contains 00-Summary)."
	}
	selected, ok, err := chooseFolder(title)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !ok {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": false})
		return
	}
	pkg, err := openComparePackage(selected)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	pkg.Close()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": true, "path": pkg.Path, "computer": pkg.Computer, "collectedAt": pkg.CollectedAt})
}

func (a *application) handleVMwareCompare(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method == http.MethodGet {
		a.mu.Lock()
		result, serial := a.vmware.compare, a.vmware.compareSerial
		a.mu.Unlock()
		if result == nil {
			writeJSON(response, http.StatusOK, map[string]any{"ok": true, "serial": serial, "result": nil})
			return
		}
		datasets := []map[string]any{}
		for _, dataset := range result.Datasets {
			changes := dataset.Changes
			if len(changes) > 300 {
				changes = changes[:300]
			}
			datasets = append(datasets, map[string]any{"key": dataset.Key, "label": dataset.Label, "available": dataset.Available, "note": dataset.Note, "beforeCount": dataset.BeforeCount, "afterCount": dataset.AfterCount, "added": dataset.Added, "removed": dataset.Removed, "changed": dataset.Changed, "expected": dataset.Expected, "changes": changes, "more": len(dataset.Changes) - len(changes)})
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "serial": serial, "result": result, "datasets": datasets})
		return
	}
	var input struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	before, after := strings.TrimSpace(input.Before), strings.TrimSpace(input.After)
	if !filepath.IsAbs(before) || !filepath.IsAbs(after) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose the package from before the move and the one from after."})
		return
	}
	if status, message := a.startVMwareAction("compare"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	a.mu.Lock()
	a.vmware.compareError = ""
	a.mu.Unlock()
	go func() {
		result, err := comparePackages(before, after)
		if err == nil {
			folder := a.vmwareOutputFolder("Comparison_" + safeName(result.After.Computer))
			if writeErr := writeCompareOutputs(folder, result, before, after); writeErr != nil {
				a.vmwareLog("The comparison report could not be written: %v", writeErr)
			} else {
				a.mu.Lock()
				a.vmware.compareFolder = folder
				a.mu.Unlock()
			}
		}
		a.mu.Lock()
		if err != nil {
			a.vmware.compareError = err.Error()
		} else {
			a.vmware.compare = result
			a.vmware.compareSerial++
		}
		a.mu.Unlock()
		if err != nil {
			a.vmwareLog("Comparison failed: %v", err)
		} else {
			a.vmwareLog("Compared %s with %s: %d change(s), %d need attention.", result.Before.Computer, result.After.Computer, result.ChangeCount, result.AttentionCount)
		}
		a.finishVMwareAction(a.vmwareIdleStatus(), "")
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// vmwareOpenTarget resolves an /api/open kind owned by the VMware tab.
func (a *application) vmwareOpenTarget(kind string) (target string, folder bool, known bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch kind {
	case "vmwarereport":
		return a.vmware.reportPath, false, true
	case "vmwarediscovery":
		return vmDiscoveryReportFor(a.vmware.reportPath), false, true
	case "vmwarereportdocx":
		return vmReportBeside(a.vmware.reportPath, vmReportDocx), false, true
	case "vmwarereportpdf":
		return vmReportBeside(a.vmware.reportPath, vmReportPDF), false, true
	case "vmwarefolder":
		return a.vmware.outputFolder, true, true
	case "vmwarerunbook":
		return a.vmware.runbookPath, false, true
	case "hvbuildreport":
		return a.hvBuild.report, false, true
	case "hvbuildfolder":
		return a.hvBuild.folder, true, true
	case "replrunbook":
		return a.repl.runbook, false, true
	case "replrunbookdocx":
		if a.repl.runbook == "" {
			return "", false, true
		}
		return filepath.Join(filepath.Dir(a.repl.runbook), replRunbookDocx), false, true
	case "replfolder":
		if a.repl.runbook == "" {
			return "", true, true
		}
		return filepath.Dir(a.repl.runbook), true, true
	case "vmwarerunbookdocx":
		return vmRunbookDocxFor(a.vmware.runbookPath), false, true
	case "vmwareplancsv":
		return a.vmware.planCSVPath, false, true
	case "guestfolder":
		return a.guestWorkPath(), true, true
	case "comparereport":
		if a.vmware.compare != nil {
			return a.vmware.compare.ReportPath, false, true
		}
		return "", false, true
	case "comparefolder":
		return a.vmware.compareFolder, true, true
	case "convertfolder":
		if a.convert.target == "" {
			return "", true, true
		}
		return filepath.Dir(a.convert.target), true, true
	case "receivefolder":
		if a.vmReceive.receiver != nil {
			return a.vmReceive.receiver.Folder(), true, true
		}
		return "", true, true
	}
	return "", false, false
}

// handleVMwarePrepare runs the opt-in prepare-source steps on one VM: the
// only requests that change vSphere. The typed VM name is checked here, the
// VM's current settings are saved first, and a running step is never
// interrupted (vSphere tasks finish on the server either way).
func (a *application) handleVMwarePrepare(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		VMID    string                  `json:"vmId"`
		Confirm string                  `json:"confirm"`
		Steps   vmPrepareSteps          `json:"steps"`
		Items   []vmPrepareBatchRequest `json:"items"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if len(input.Items) > 0 {
		a.prepareVMwareBatch(response, input.Items, input.Confirm)
		return
	}
	if !input.Steps.any() {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose at least one step."})
		return
	}
	a.mu.Lock()
	inventory, server, user, password, thumbprint := a.vmware.inventory, a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint
	a.mu.Unlock()
	if inventory == nil || password == "" || !strings.EqualFold(inventory.Server, server) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Read the inventory from vCenter or ESXi in this session first. A saved inventory cannot change anything."})
		return
	}
	var target *vmMachine
	for index := range inventory.VMs {
		if inventory.VMs[index].ID == input.VMID {
			target = &inventory.VMs[index]
		}
	}
	if target == nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "That VM is not in the inventory. Read the inventory again."})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(input.Confirm), target.Name) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the VM's name exactly to confirm: " + target.Name})
		return
	}
	name, id := target.Name, target.ID
	if status, message := a.startVMwareAction("prepare"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	a.mu.Lock()
	a.vmware.status = "preparing"
	a.vmware.message = "Preparing " + name + "."
	a.vmware.prepareError = ""
	a.vmware.prepareBatch = nil
	a.mu.Unlock()
	a.vmwareLog("Preparing %s: %s.", name, strings.Join(vmPrepareStepNames(input.Steps), ", "))
	go func() {
		folder := filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(server)), "Prepare")
		result, err := prepareVSphereVM(context.Background(), vmConnectOptions{Server: server, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}, id, name, input.Steps, folder, func(text string) { a.vmwareLog("%s", text) })
		password = ""
		a.mu.Lock()
		a.vmware.prepare = &result
		a.vmware.prepareSerial++
		if err != nil {
			a.vmware.prepareError = err.Error()
		}
		a.applyPrepareResultLocked(id, result)
		a.mu.Unlock()
		for _, line := range append(result.Done, result.Skipped...) {
			a.vmwareLog("%s", line)
		}
		if err != nil {
			a.vmwareLog("%s", err.Error())
			a.finishVMwareAction("connected", "Preparing "+name+" stopped: "+err.Error())
			return
		}
		a.finishVMwareAction("connected", "Prepared "+name+".")
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// applyPrepareResultLocked reflects what a prepare changed in the inventory
// so readiness is checked again. Caller holds a.mu.
func (a *application) applyPrepareResultLocked(id string, result vmPrepareResult) {
	if a.vmware.inventory == nil {
		return
	}
	for index := range a.vmware.inventory.VMs {
		vm := &a.vmware.inventory.VMs[index]
		if vm.ID != id || result.PowerState == "" {
			continue
		}
		vm.PowerState, vm.VTPM, vm.Encrypted = result.PowerState, result.VTPM, result.Encrypted
		if result.Snapshots == 0 {
			vm.Snapshots, vm.ConsolidationNeeded = nil, false
		}
		if result.PowerState == "poweredOff" {
			vm.ToolsRunning = false
		}
		vm.Readiness = nil
		for _, target := range vmTargets {
			vm.Readiness = append(vm.Readiness, assessVM(*vm, target, time.Now()))
		}
	}
	a.vmware.inventorySerial++
}

// prepareVMwareBatch runs Prepare on VMware for many VMs, one after another,
// after one typed confirmation of the batch (vmware_bulk.go).
func (a *application) prepareVMwareBatch(response http.ResponseWriter, requests []vmPrepareBatchRequest, confirm string) {
	a.mu.Lock()
	inventory, server, user, password, thumbprint := a.vmware.inventory, a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint
	a.mu.Unlock()
	if inventory == nil || password == "" || !strings.EqualFold(inventory.Server, server) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Read the inventory from vCenter or ESXi in this session first. A saved inventory cannot change anything."})
		return
	}
	items, err := vmPlanPrepareBatch(inventory, requests, confirm)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if status, message := a.startVMwareAction("prepare"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	label := vmPrepareBatchConfirmText(len(items))
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "preparing"
	a.vmware.message = "Preparing " + label + ", one at a time."
	a.vmware.prepareError = ""
	a.vmware.prepareBatch = append([]vmPrepareBatchItem{}, items...)
	a.mu.Unlock()
	a.vmwareLog("Preparing %s one at a time.", label)
	go func() {
		defer cancel()
		folder := filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(server)), "Prepare")
		options := vmConnectOptions{Server: server, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
		done, failed, stopped := vmPrepareBatchRun(ctx, items, func(item vmPrepareBatchItem) (vmPrepareResult, error) {
			a.vmwareLog("Preparing %s: %s.", item.VM, strings.Join(vmPrepareStepNames(item.Steps), ", "))
			// Each VM runs to the end even if Stop is pressed meanwhile.
			result, err := prepareVSphereVM(context.Background(), options, item.ID, item.VM, item.Steps, folder, func(text string) { a.vmwareLog("%s", text) })
			for _, line := range append(append([]string{}, result.Done...), result.Skipped...) {
				a.vmwareLog("%s: %s", item.VM, line)
			}
			if err != nil {
				a.vmwareLog("%s: %s", item.VM, err.Error())
			}
			a.mu.Lock()
			a.applyPrepareResultLocked(item.ID, result)
			a.mu.Unlock()
			return result, err
		}, func(list []vmPrepareBatchItem) {
			a.mu.Lock()
			a.vmware.prepareBatch = list
			a.mu.Unlock()
		})
		options.Password, password = "", ""
		message := fmt.Sprintf("Prepared %d of %d VMs.", done, len(items))
		if failed > 0 {
			message += fmt.Sprintf(" %d failed: see each VM's result.", failed)
		}
		if stopped > 0 {
			message += fmt.Sprintf(" Stopped before %d.", stopped)
		}
		a.mu.Lock()
		a.vmware.prepareSerial++
		a.mu.Unlock()
		a.vmwareLog("%s", message)
		a.finishVMwareAction("connected", message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// copyTransfers is the live list of disks this computer is sending or
// moving, for the Disk copy and Move views.
func (a *application) copyTransfers() *vmTransferBoard {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transfers == nil {
		a.transfers = newVMTransferBoard()
	}
	return a.transfers
}

// moveProgress reports a move round's disks to the live list. Every disk
// of the round shares the returned prefix, so the round ends them together.
func (a *application) moveProgress(vm vmMachine, target string) (func(string, vmCopyProgress), string) {
	board, prefix := a.copyTransfers(), "move-"+vm.ID+"|"
	return func(disk string, p vmCopyProgress) {
		board.Report(vmTransfer{ID: prefix + disk, Kind: "move", Label: vm.Name + ": " + disk, Source: vm.Name + " (" + disk + ")", Target: target}, p)
	}, prefix
}
