//go:build windows

package main

// Roll back (rollback.go) on Windows: the records each cutover leaves, the
// /api/rollback endpoints and the platform steps. The new VM is reached on
// this Hyper-V host with the Hyper-V module, on a remote host or cluster node
// through Connect-HyperVTarget.ps1 (PowerShell remoting only), or on
// HyperCore, Nutanix AHV or Proxmox VE through the connected platform; the
// old VM through vSphere, the source Hyper-V host (Move-HyperVVM.ps1, WMI) or
// the replication agent. Nothing is copied to any server and no service,
// task or WMI process is created.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type rbRuntime struct {
	store   *rbStore
	list    []rbRecord
	loaded  bool
	loadErr string
	running string
	cancel  context.CancelFunc
	serial  uint64
}

// rbView is a record with what the page shows beside it.
type rbView struct {
	rbRecord
	Window      string   `json:"window"`
	DataWarning string   `json:"dataWarning"`
	DCWarning   string   `json:"dcWarning,omitempty"`
	Planned     []rbStep `json:"planned"`
}

type rbStateResponse struct {
	Running string   `json:"running"`
	Serial  uint64   `json:"serial"`
	Error   string   `json:"error,omitempty"`
	Records []rbView `json:"records"`
}

// Caller holds a.mu.
func (a *application) rbStoreLocked() *rbStore {
	if a.rollback.store == nil {
		a.rollback.store = &rbStore{path: filepath.Join(a.update.appDataRoot, "Rollback", "rollbacks.json")}
	}
	if !a.rollback.loaded {
		a.rollback.loaded = true
		a.rollback.store.RecoverInterrupted()
		list, err := a.rollback.store.List()
		if err != nil {
			a.rollback.loadErr = err.Error()
		}
		a.rollback.list = list
	}
	return a.rollback.store
}

// Caller holds a.mu.
func (a *application) rbStateLocked() *rbStateResponse {
	a.rbStoreLocked()
	now := time.Now()
	state := &rbStateResponse{Running: a.rollback.running, Serial: a.rollback.serial, Error: a.rollback.loadErr, Records: []rbView{}}
	defaults := rbOptions{TurnOff: true, Reconnect: true, Start: true}
	for index, rec := range a.rollback.list {
		if index >= 200 {
			break
		}
		view := rbView{rbRecord: rec, Window: rbWindow(rec), DataWarning: rbDataWarning(rec, now), Planned: rbPlanSteps(rec, defaults)}
		if rec.DC {
			view.DCWarning = rbDCWarning
		}
		state.Records = append(state.Records, view)
	}
	return state
}

func (a *application) rbRefresh() {
	a.mu.Lock()
	store := a.rbStoreLocked()
	a.mu.Unlock()
	list, err := store.List()
	a.mu.Lock()
	if err == nil {
		a.rollback.list = list
		a.rollback.loadErr = ""
	} else {
		a.rollback.loadErr = err.Error()
	}
	a.rollback.serial++
	a.mu.Unlock()
}

// recordCutover saves a record so the cutover can be rolled back. A failure
// here never fails the cutover; it is logged.
func (a *application) recordCutover(rec rbRecord, log func(string)) {
	a.mu.Lock()
	store := a.rbStoreLocked()
	a.mu.Unlock()
	if err := store.Add(rec); err != nil {
		if log != nil {
			log("The rollback record for " + rec.Name + " could not be saved: " + err.Error() + ". Roll back by hand if needed (Roll back lists the steps).")
		}
		return
	}
	a.rbRefresh()
}

// recordVMCutover records a cutover to Hyper-V (this host when node is "").
func (a *application) recordVMCutover(vm vmMachine, hyperVID, node, plan, adapterRecord string) {
	a.mu.Lock()
	server := ""
	if a.vmware.inventory != nil {
		server = a.vmware.inventory.Server
	}
	rolling := rbRollingHostFor(a.vmware.plan, vm)
	a.mu.Unlock()
	kind := "vmware"
	if vm.Source == vmSourceHyperV {
		kind = "hyperv"
	}
	method := rbMethodMove
	if node != "" && !strings.EqualFold(node, a.computerName) {
		method = rbMethodHVRemote
	} else {
		node = ""
	}
	rec := rbNewRecord(method, rbSource{Kind: kind, Server: server, VMID: vm.ID, Name: vm.Name, Host: vm.Host, Datacenter: vm.Datacenter, AdapterRecord: adapterRecord}, rbNew{Kind: "hyperv", ID: hyperVID, Name: vm.Name, Host: node}, &vm)
	rec.Plan, rec.RollingHost = plan, rolling
	a.recordCutover(rec, func(text string) { a.vmwareLog("%s", text) })
}

// handleRollback serves /api/rollback/run and /api/rollback/stop.
func (a *application) handleRollback(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/rollback") {
	case "/guides":
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "guides": rbGuides()})
	case "/stop":
		if !decodeVMwareRequest(response, request, nil) {
			return
		}
		a.mu.Lock()
		if a.rollback.cancel != nil {
			a.rollback.cancel()
		}
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "/run":
		var input struct {
			ID      string `json:"id"`
			Confirm string `json:"confirm"`
			rbOptions
		}
		if !decodeVMwareRequest(response, request, &input) {
			return
		}
		a.mu.Lock()
		store := a.rbStoreLocked()
		var rec *rbRecord
		for index := range a.rollback.list {
			if a.rollback.list[index].ID == input.ID {
				copied := a.rollback.list[index]
				rec = &copied
			}
		}
		running := a.rollback.running
		a.mu.Unlock()
		if rec == nil {
			fail(http.StatusNotFound, "That cutover is no longer in the list.")
			return
		}
		if running != "" {
			fail(http.StatusConflict, "Another rollback is running; wait for it to finish.")
			return
		}
		if !strings.EqualFold(strings.TrimSpace(input.Confirm), rec.Name) {
			fail(http.StatusBadRequest, "Confirm the rollback of "+rec.Name+".")
			return
		}
		if why := rbCheck(*rec, input.rbOptions); why != "" {
			fail(http.StatusBadRequest, why)
			return
		}
		if why := a.rbReady(*rec, input.rbOptions); why != "" {
			fail(http.StatusBadRequest, why)
			return
		}
		if !a.beginCriticalOperation() {
			fail(http.StatusConflict, "An update is being installed.")
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		logPath := filepath.Join(filepath.Dir(store.path), rec.ID+".log")
		a.mu.Lock()
		a.rollback.running, a.rollback.cancel = rec.ID, cancel
		a.mu.Unlock()
		started := *rec
		started.State, started.Started, started.LogPath, started.Message = rbStateRunning, time.Now().UTC(), logPath, "Rolling back."
		started.Steps = rbPlanSteps(started, input.rbOptions)
		store.Update(rec.ID, func(r *rbRecord) error { *r = started; return nil })
		a.rbRefresh()
		options := input.rbOptions
		go func() {
			defer a.endCriticalOperation()
			defer cancel()
			file, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			log := func(text string) {
				a.vmwareLog("Roll back %s: %s", started.Name, text)
				if file != nil {
					fmt.Fprintf(file, "%s %s\r\n", time.Now().Format("2006-01-02 15:04:05"), text)
				}
			}
			log(fmt.Sprintf("Options: turn off if it does not shut down %v, put the network back %v, start the old machine %v, domain controller warning confirmed %v.", options.TurnOff, options.Reconnect, options.Start, options.DCConfirmed))
			ended := rbRun(ctx, started, options, &rbWindowsOps{a: a}, log)
			ended.LogPath = logPath
			if file != nil {
				file.Close()
			}
			store.Update(ended.ID, func(r *rbRecord) error { *r = ended; return nil })
			a.mu.Lock()
			a.rollback.running, a.rollback.cancel = "", nil
			if ended.State == rbStateDone && a.vmware.inventory != nil && ended.Source.Kind != "agent" {
				for index := range a.vmware.inventory.VMs {
					if a.vmware.inventory.VMs[index].ID == ended.Source.VMID {
						a.vmware.inventory.VMs[index].PowerState = "poweredOn"
					}
				}
				a.vmware.inventorySerial++
			}
			a.mu.Unlock()
			a.rbRefresh()
		}()
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
	default:
		fail(http.StatusNotFound, "Not found.")
	}
}

// rbReady says what must be connected first, or "".
func (a *application) rbReady(rec rbRecord, options rbOptions) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec.New.Kind == "hyperv" && rec.New.Host != "" {
		if err := a.guestCheckHostLocked(rec.New.Host); err != nil {
			return "Connect to the Hyper-V host or cluster that runs the new VM first (Move VMs, Another Hyper-V host or cluster): " + err.Error()
		}
	} else if rec.New.Kind == "hyperv" && !vmHyperVHere() {
		return "Run Roll back on the Hyper-V host that runs the new VM (" + vmNonEmpty(rec.New.Host, "the host this cutover ran on") + ")."
	}
	if rec.New.Kind == "platform" {
		if a.vmware.target.platform == nil || a.vmware.target.connection.Platform != rec.New.Platform || (rec.New.PlatformHost != "" && !strings.EqualFold(a.vmware.target.connection.Server, rec.New.PlatformHost)) {
			return "Connect to " + vmTargetLabel(rec.New.Platform) + " " + rec.New.PlatformHost + " first (Move VMs, the VM's Move panel), so the new VM can be turned off."
		}
		if !options.TurnOff {
			return "On " + vmTargetLabel(rec.New.Platform) + " the new VM is turned off, not shut down from inside: tick Turn it off, or shut it down from inside first."
		}
	}
	if rec.Source.Kind == "vmware" && (options.Reconnect && rec.Source.AdapterRecord != "" || options.Start) {
		if !strings.EqualFold(a.vmware.server, rec.Source.Server) || a.vmware.password == "" {
			return "Sign in to " + rec.Source.Server + " again (the password is kept only while this application runs), so the old VM can be started."
		}
	}
	return ""
}

type rbWindowsOps struct{ a *application }

func (o *rbWindowsOps) runHyperV(ctx context.Context, rec *rbRecord, script string, log func(string)) ([]string, error) {
	a := o.a
	if rec.New.Host == "" {
		out, err := vmRunPowerShell(script)
		lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
		for _, line := range lines {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "RB-") {
				log(line)
			}
		}
		return lines, err
	}
	a.mu.Lock()
	user, password := a.hvRemote.user, a.hvRemote.password
	a.mu.Unlock()
	var result struct {
		Lines []string `json:"lines"`
	}
	err := a.runHVRemote(ctx, rec.New.Host, user, password, "Create", script, &result)
	return result.Lines, err
}

func (o *rbWindowsOps) StopNew(ctx context.Context, rec *rbRecord, turnOff bool, log func(string)) ([]rbAdapter, error) {
	switch rec.New.Kind {
	case "hyperv":
		lines, err := o.runHyperV(ctx, rec, rbHyperVStopScript(rec.New.ID, rec.New.Name, turnOff), log)
		adapters, startAction, state := rbParseHyperV(lines)
		if startAction != "" {
			rec.New.StartAction = startAction
		}
		if err != nil {
			return adapters, err
		}
		if state != "off" {
			return adapters, fmt.Errorf("it is %s", state)
		}
		return adapters, nil
	case "platform":
		platform := o.platform()
		if platform == nil {
			return nil, errors.New("the platform is not connected")
		}
		state, err := platform.State(ctx, rec.New.ID)
		if err != nil {
			return nil, err
		}
		if rbPowerWord(state) == "off" {
			return nil, nil
		}
		log("Turning the new VM off on " + vmTargetLabel(rec.New.Platform) + ".")
		if err := platform.PowerOff(ctx, rec.New.ID); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(rbShutdownWait)
		for time.Now().Before(deadline) {
			if state, err = platform.State(ctx, rec.New.ID); err == nil && rbPowerWord(state) == "off" {
				return nil, nil
			}
			if vmSleep(ctx, 5*time.Second) != nil {
				return nil, ctx.Err()
			}
		}
		return nil, errors.New("it did not turn off within ten minutes")
	}
	return nil, errors.New("this kind of new VM is not known")
}

func (o *rbWindowsOps) platform() vmTargetPlatform {
	o.a.mu.Lock()
	defer o.a.mu.Unlock()
	return o.a.vmware.target.platform
}

func (o *rbWindowsOps) NewState(ctx context.Context, rec *rbRecord) (string, error) {
	switch rec.New.Kind {
	case "hyperv":
		lines, err := o.runHyperV(ctx, rec, rbHyperVStateScript(rec.New.ID, rec.New.Name), func(string) {})
		if err != nil {
			return "", err
		}
		_, _, state := rbParseHyperV(lines)
		return state, nil
	case "platform":
		platform := o.platform()
		if platform == nil {
			return "", errors.New("the platform is not connected")
		}
		state, err := platform.State(ctx, rec.New.ID)
		return rbPowerWord(state), err
	}
	return "", errors.New("this kind of new VM is not known")
}

func (o *rbWindowsOps) vsphere(rec *rbRecord) (vmConnectOptions, error) {
	a := o.a
	a.mu.Lock()
	defer a.mu.Unlock()
	if !strings.EqualFold(a.vmware.server, rec.Source.Server) || a.vmware.password == "" {
		return vmConnectOptions{}, errors.New("sign in to " + rec.Source.Server + " again first")
	}
	return vmConnectOptions{Server: a.vmware.server, User: a.vmware.account, Password: a.vmware.password, Thumbprint: a.vmware.thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}, nil
}

func (o *rbWindowsOps) ReconnectSource(ctx context.Context, rec *rbRecord, log func(string)) (int, error) {
	if rec.Source.Kind != "vmware" || rec.Source.AdapterRecord == "" {
		return 0, nil
	}
	options, err := o.vsphere(rec)
	if err != nil {
		return 0, err
	}
	defer func() { options.Password = "" }()
	return vmReconnectSourceAdapters(ctx, options, rec.Source.VMID, rec.Source.Name, rec.Source.AdapterRecord, filepath.Join(o.a.vmwareRoot(), safeName(strings.ToLower(rec.Source.Server)), "Move"), log)
}

func (o *rbWindowsOps) StartSource(ctx context.Context, rec *rbRecord, log func(string)) error {
	switch rec.Source.Kind {
	case "vmware":
		options, err := o.vsphere(rec)
		if err != nil {
			return err
		}
		defer func() { options.Password = "" }()
		return vmPowerOnSource(ctx, options, rec.Source.VMID, rec.Source.Name, log)
	case "hyperv":
		return o.a.hypervHostFor(rec.Source.Server, log).run(ctx, "Start", rec.Source.VMID, "", nil, "-VMName", rec.Source.Name)
	}
	return errors.New("this kind of old machine is not known")
}

func (o *rbWindowsOps) RollbackAgent(ctx context.Context, rec *rbRecord, log func(string)) error {
	a := o.a
	a.mu.Lock()
	console := a.replConsoleLocked()
	a.mu.Unlock()
	if console == nil {
		return errors.New("the replication console's records could not be opened")
	}
	agent, reachable := rec.Source.Agent, false
	rows, _ := console.Rows()
	for _, row := range rows {
		for _, job := range row.Live.Jobs {
			if job.Spec.ID == rec.Source.Job && (agent == "" || agent == row.ID) {
				agent, reachable = row.ID, row.Live.Reachable
			}
		}
		if row.ID == agent && !row.Live.Reachable {
			reachable = false
		}
	}
	if agent == "" {
		return fmt.Errorf("%w: no agent on this console has the job", errRBAgentUnreachable)
	}
	client, err := console.Client(agent)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	console.RefreshOne(callCtx, agent)
	if live, ok := console.Live(agent); ok {
		reachable = live
	}
	if !reachable {
		return errRBAgentUnreachable
	}
	log("Asking the agent on " + rec.Source.Name + " to roll its job back.")
	if err := client.Action(callCtx, rec.Source.Job, "rollback"); err != nil {
		if strings.Contains(err.Error(), "unknown action") {
			return errors.New("the agent on " + rec.Source.Name + " is older than this console and cannot roll back its job; update it with Install agents in Replicate servers")
		}
		return err
	}
	console.RefreshOne(context.Background(), agent)
	return nil
}

// recordCopiesCutover records a production VM started from agent
// replication copies on this host.
func (a *application) recordCopiesCutover(failover replFailover, job string) {
	source := rbSource{Kind: "agent", Name: failover.Name, Job: job}
	if job == "" {
		// Copies sent by hand or by Move VMs: no agent to ask.
		source.Kind = "other"
	}
	a.mu.Lock()
	console := a.replConsoleLocked()
	a.mu.Unlock()
	if console != nil && job != "" {
		rows, _ := console.Rows()
		for _, row := range rows {
			for _, view := range row.Live.Jobs {
				if view.Spec.ID == job {
					source.Agent, source.Name, source.Services = row.ID, vmNonEmpty(row.Name, failover.Name), append([]string(nil), view.Spec.StopServices...)
				}
			}
		}
	}
	rec := rbNewRecord(rbMethodCopies, source, rbNew{Kind: "hyperv", ID: failover.VMID, Name: failover.Name}, nil)
	a.recordCutover(rec, func(text string) { a.vmwareLog("%s", text) })
}

// recordPlatformCutover records a cutover to HyperCore, Nutanix AHV or
// Proxmox VE.
func (a *application) recordPlatformCutover(vm vmMachine, created vmTargetRecord) {
	a.mu.Lock()
	server := ""
	if a.vmware.inventory != nil {
		server = a.vmware.inventory.Server
	}
	host := a.vmware.target.connection.Server
	rolling := rbRollingHostFor(a.vmware.plan, vm)
	a.mu.Unlock()
	kind := "vmware"
	if vm.Source == vmSourceHyperV {
		kind = "hyperv"
	}
	rec := rbNewRecord(rbMethodPlatform, rbSource{Kind: kind, Server: server, VMID: vm.ID, Name: vm.Name, Host: vm.Host, Datacenter: vm.Datacenter}, rbNew{Kind: "platform", ID: created.VM.ID, Name: vmNonEmpty(created.VM.Name, vm.Name), Platform: created.Platform, PlatformHost: vmNonEmpty(created.Server, host)}, &vm)
	rec.RollingHost = rolling
	a.recordCutover(rec, func(text string) { a.vmwareLog("%s", text) })
}
