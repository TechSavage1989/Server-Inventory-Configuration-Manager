//go:build windows

package main

// Migration plans on the Hyper-V host (see movebatch.go): the target that
// copies and creates VMs on this computer, and /api/plans.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type mbRuntime struct {
	runner  *mbRunner
	loadErr string
	// testing is set while a test VM is made or removed.
	testing bool
	// copying is what a sweep copies or cuts over now (nil when idle). It
	// does not hold the VMware action, so Discovery reads and collections
	// still run; steps that change VMs or the session wait for it.
	copying *mbClaim
}

// startPlans loads the plans and starts their scheduler, so scheduled
// copies and cutovers run whether or not the tool is open.
func (a *application) startPlans() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.plans.runner != nil {
		return
	}
	runner := newMBRunner(filepath.Join(a.vmwareRoot(), "migration-plans.json"), &mbHostTarget{a: a})
	runner.log = func(text string) { a.vmwareLog("Plan: %s", text) }
	runner.ready = a.mbReady
	runner.guard = a.prodGuard
	runner.locate = func(plan *mbPlan, vm *mbVM) pgSlotKeys {
		_, m, err := (&mbHostTarget{a: a}).machine(plan, vm)
		if err != nil {
			return pgSlotKeys{}
		}
		target := a.computerName
		if mbRemote(plan) {
			target = plan.Settings.Node
			if target == "" {
				target = plan.Settings.RemoteHost
			}
		}
		return pgSlotKeysFor(m, target)
	}
	runner.claim = func(claim mbClaim) (func(), string) {
		if !a.beginCriticalOperation() {
			return nil, "An update is being installed."
		}
		a.mu.Lock()
		reason := a.planCopyRefusalLocked()
		if reason == "" {
			a.plans.copying = &claim
		}
		a.mu.Unlock()
		if reason != "" {
			a.endCriticalOperation()
			return nil, reason
		}
		return func() {
			a.mu.Lock()
			a.plans.copying = nil
			a.mu.Unlock()
			a.endCriticalOperation()
		}, ""
	}
	if err := runner.Load(); err != nil {
		a.plans.loadErr = err.Error()
	}
	a.plans.runner = runner
	go runner.Loop(context.Background())
}

// mbReady says why a plan's work cannot run now, or "".
func (a *application) mbReady(plan *mbPlan) string {
	if mbRemote(plan) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.mbRemoteReadyLocked(plan)
	}
	if !vmHyperVHere() {
		return "This computer is not a Hyper-V host. Migration plans run on the Hyper-V host that will run the VMs."
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inventory := a.vmware.inventory
	if inventory == nil || !strings.EqualFold(inventory.Server, plan.Server) {
		return "Read the inventory of " + plan.Server + " in Discovery (VMware and Hyper-V) to let this plan copy."
	}
	if !strings.EqualFold(inventory.ServerType, hypervServerType) && (a.vmware.password == "" || !strings.EqualFold(a.vmware.server, plan.Server)) {
		return "Sign in to " + plan.Server + " in Discovery again: the password is kept only while the application runs."
	}
	if a.installing || a.closing {
		return "The application is closing or installing an update."
	}
	return ""
}

type mbHostTarget struct{ a *application }

// machine finds the plan VM in the live inventory, by ID and name.
func (t *mbHostTarget) machine(plan *mbPlan, vm *mbVM) (*vmInventory, vmMachine, error) {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	inventory := a.vmware.inventory
	if inventory == nil || !strings.EqualFold(inventory.Server, plan.Server) {
		return nil, vmMachine{}, errors.New("the inventory of " + plan.Server + " is not open")
	}
	for _, m := range inventory.VMs {
		if m.ID == vm.ID {
			if m.Name != vm.Name {
				return nil, vmMachine{}, fmt.Errorf("the VM with this ID is now called %q; take it out of the plan and add it again", m.Name)
			}
			return inventory, m, nil
		}
	}
	return nil, vmMachine{}, errors.New("the VM is no longer in the inventory")
}

func (t *mbHostTarget) Check(plan *mbPlan, vm *mbVM) error {
	_, m, err := t.machine(plan, vm)
	if err != nil {
		return err
	}
	if role := vmInfrastructureRole(m); role != "" || m.Template {
		return errors.New("this VM is not moved")
	}
	if mbRemote(plan) {
		if err := t.remoteCheck(plan, vm, m); err != nil {
			return err
		}
	}
	if vmDisksEncrypted(m) {
		return errors.New("the VM's disks are encrypted; decrypt it first (Decrypt in New migration's Review, or Prepare on VMware in Move VMs)")
	}
	if r := assessVM(m, vmTargetHyperV, time.Now()); r.Status == vmStatusBlocked {
		reason := "it has a blocking readiness finding"
		for _, f := range r.Findings {
			if f.Severity == vmSeverityBlocker {
				reason = f.Message
				break
			}
		}
		return errors.New(reason + " (see its details in Discovery)")
	}
	return nil
}

func (t *mbHostTarget) copyFolder(plan *mbPlan, vm *mbVM) string {
	if vm.CopyFolder != "" {
		return vm.CopyFolder
	}
	root := plan.Settings.CopyRoot
	if root == "" {
		t.a.mu.Lock()
		root = t.a.vmwareCopyRootLocked()
		t.a.mu.Unlock()
	}
	return filepath.Join(root, safeName(vm.Name))
}

func (t *mbHostTarget) Round(ctx context.Context, plan *mbPlan, vm *mbVM, cutover bool, throttle *vmThrottle, progress func(string, vmCopyProgress), log func(string)) (*vmMoveResult, error) {
	a := t.a
	inventory, m, err := t.machine(plan, vm)
	if err != nil {
		return nil, err
	}
	if mbRemote(plan) {
		return t.remoteRound(ctx, plan, vm, inventory, m, cutover, throttle, progress, log)
	}
	folder := t.copyFolder(plan, vm)
	if !filepath.IsAbs(folder) || (migrateProtectedFolder(folder) != "" && !vmHostDefaultAllowed(folder)) {
		return nil, errors.New("choose a full path for the copied disks outside Windows, Program Files and profile folders")
	}
	if plan.Settings.Cluster && (!vmOnClusterStorage(folder) || (plan.Settings.VMFolder != "" && !vmOnClusterStorage(plan.Settings.VMFolder))) {
		return nil, errors.New("a highly available VM must keep its disks and configuration on a Cluster Shared Volume (" + vmClusterStorageRoot() + "\\...)")
	}
	if !vm.Seeded && m.UsedBytes > 0 {
		need := uint64(float64(m.UsedBytes)*1.05) + 1<<30
		if have := vmFolderBytes(folder); have < need {
			need -= have
			if free, _, ok := vmFreeSpace(folder); ok && free < need {
				return nil, fmt.Errorf("there is not enough free space in %s: about %s is needed and %s is free", folder, vmFormatBytes(int64(need)), vmFormatBytes(int64(free)))
			}
		}
	}
	vm.CopyFolder = folder
	a.mu.Lock()
	server, user, password, thumbprint := a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint
	a.mu.Unlock()
	board, prefix := a.moveProgress(m, folder)
	result, err := a.runMoveRound(ctx, inventory, m, vmConnectOptions{Server: server, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion},
		vmMoveRequest{VMID: m.ID, Name: m.Name, Datacenter: m.Datacenter, CopyFolder: folder, Quiesce: plan.Settings.Quiesce, Cutover: cutover, EjectMedia: plan.Settings.EjectMedia,
			RecordFolder: filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(plan.Server)), "Move"), Throttle: throttle,
			Progress: func(disk string, p vmCopyProgress) { board(disk, p); progress(disk, p) }}, log)
	a.copyTransfers().EndRunning(prefix, err, errors.Is(err, context.Canceled), err == nil && cutover)
	if result != nil && result.ShutDown {
		a.mu.Lock()
		if a.vmware.inventory != nil {
			for index := range a.vmware.inventory.VMs {
				if a.vmware.inventory.VMs[index].ID == m.ID {
					a.vmware.inventory.VMs[index].PowerState, a.vmware.inventory.VMs[index].ToolsRunning = "poweredOff", false
				}
			}
			a.vmware.inventorySerial++
		}
		a.mu.Unlock()
	}
	return result, err
}

func (t *mbHostTarget) Create(ctx context.Context, plan *mbPlan, vm *mbVM, result *vmMoveResult, log func(string)) (string, []string, error) {
	a := t.a
	inventory, m, err := t.machine(plan, vm)
	if err != nil {
		return "", nil, err
	}
	if mbRemote(plan) {
		return t.remoteCreate(ctx, plan, vm, inventory, m, result, log)
	}
	a.mu.Lock()
	switches, networks := vmMoveNetworkMap(inventory, a.vmware.plan)
	server, user, password, thumbprint := a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint
	a.mu.Unlock()
	switches, switchNotes := vmAutoSwitches(switches, m, vmHostSwitches())
	for _, note := range switchNotes {
		log(note)
	}
	hyperV, err := vmHyperVPlanFor(m, networks, vmHyperVOptions{Folder: plan.Settings.VMFolder, Disks: result.Disks, Switches: switches, KeepMAC: plan.Settings.KeepMAC, Start: plan.Settings.StartVMs, ClusterRole: plan.Settings.Cluster})
	if err != nil {
		return "", nil, err
	}
	hyperV.Notes = "Moved by the migration plan " + plan.Name + " in Server Inventory and Migration Manager."
	for _, warning := range hyperV.Warnings {
		log(warning)
	}
	log("Creating the Hyper-V VM " + m.Name + ".")
	id, err := vmCreateHyperVVM(hyperV)
	if err != nil {
		return "", nil, err
	}
	notes := []string{}
	if hyperV.ClusterResult != "" {
		notes = append(notes, hyperV.ClusterResult)
	}
	notes = append(notes, a.afterCutover(ctx, m, id, "", plan.Settings.StartVMs, plan.Settings.DisconnectSource, plan.Settings.FixUp,
		vmConnectOptions{Server: server, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion},
		filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(plan.Server)), "Move"), plan.Name, log)...)
	return id, notes, nil
}

func mbTestName(vm *mbVM) string { return vm.Name + "-TEST" }

func (t *mbHostTarget) StartTest(ctx context.Context, plan *mbPlan, vm *mbVM, log func(string)) (*mbTest, error) {
	_, m, err := t.machine(plan, vm)
	if err != nil {
		return nil, err
	}
	if vm.CopyFolder == "" || len(vm.Disks) == 0 {
		return nil, errors.New("the VM has no finished copy yet")
	}
	if mbRemote(plan) {
		return t.remoteStartTest(ctx, plan, vm, m, log)
	}
	name := mbTestName(vm)
	testRoot := filepath.Join(vm.CopyFolder, "Test cutovers")
	copies, children := []string{}, []string{}
	for index, disk := range vm.Disks {
		if !strings.EqualFold(filepath.Dir(disk), vm.CopyFolder) {
			return nil, fmt.Errorf("%s is not in the VM's copy folder", disk)
		}
		copies = append(copies, filepath.Base(disk))
		children = append(children, filepath.Join(testRoot, name, fmt.Sprintf("test-%d-%s", index, filepath.Base(disk))))
	}
	switches := map[string]string{}
	for _, nic := range m.NICs {
		switches[nic.Network] = plan.Settings.TestSwitch
	}
	hyperV, err := vmHyperVPlanFor(m, nil, vmHyperVOptions{Folder: testRoot, Disks: children, Switches: switches, Start: true})
	if err != nil {
		return nil, err
	}
	hyperV.Name = name
	hyperV.Notes = "Test cutover from the migration plan " + plan.Name + ". Ending the test in Server Inventory and Migration Manager removes this VM and its test disks; the copies are not changed."
	for index := range hyperV.Adapters {
		hyperV.Adapters[index].VLAN, hyperV.Adapters[index].MAC = 0, ""
	}
	test := &mbTest{Name: name, Folder: vm.CopyFolder, Copies: copies, Children: children, Started: time.Now().UTC()}
	log("Making the test VM " + name + " on differencing disks.")
	out, err := vmRunPowerShell(replFailoverScript(hyperV, vm.CopyFolder, copies, children))
	if err != nil {
		// The test disks may exist: keep the record so End test removes them.
		return test, errors.New("the test VM was not made: " + err.Error() + ". End the test to remove what was made")
	}
	id := strings.TrimSpace(out[strings.LastIndexAny(out, "\r\n")+1:])
	if !vmHyperVIDPattern.MatchString(id) {
		return test, errors.New("Hyper-V did not return the test VM's ID: " + out + ". End the test to remove what was made")
	}
	test.VMID = id
	return test, nil
}

func (t *mbHostTarget) EndTest(ctx context.Context, plan *mbPlan, vm *mbVM, log func(string)) error {
	if mbRemote(plan) {
		return t.remoteEndTest(ctx, plan, vm, log)
	}
	test := vm.Test
	script := ""
	if test.VMID == "" {
		// The VM's ID is unknown: never remove a VM by name.
		script = fmt.Sprintf("Import-Module Hyper-V\nif (Get-VM -Name %s -ErrorAction SilentlyContinue) { throw ('A VM named ' + %s + ' exists but this application did not record its ID; remove it in Hyper-V Manager, then end the test again.') }\n", vmPSQuote(test.Name), vmPSQuote(test.Name))
	}
	script += replFailoverEndScript(replFailover{VMID: test.VMID, Name: test.Name, Copies: test.Copies, Children: test.Children}, test.Folder)
	if test.VMID == "" {
		script = strings.Replace(script, "$vm = Get-VM -Id ''", "$vm = $null", 1)
	}
	log("Removing the test VM " + test.Name + " and its test disks.")
	if _, err := vmRunPowerShell(script); err != nil {
		return errors.New("the test VM was not removed: " + err.Error())
	}
	return nil
}

func (a *application) mbRunner() (*mbRunner, string) {
	a.startPlans()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.plans.runner, a.plans.loadErr
}

func (a *application) handlePlans(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	runner, loadErr := a.mbRunner()
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	if request.Method == http.MethodGet {
		plans, sweeping, blocked := runner.View()
		a.mu.Lock()
		inventory := a.vmware.inventory
		info := map[string]any{"open": inventory != nil}
		waves := map[string]int{}
		if inventory != nil {
			info["server"], info["serverType"] = inventory.Server, inventory.ServerType
			info["live"] = strings.EqualFold(inventory.ServerType, hypervServerType) || (a.vmware.password != "" && strings.EqualFold(a.vmware.server, inventory.Server))
			if a.vmware.plan != nil {
				for _, p := range a.vmware.plan.VMs {
					waves[p.ID] = p.Wave
				}
			}
		}
		testing := a.plans.testing
		a.mu.Unlock()
		state := map[string]any{"ok": true, "plans": plans, "sweeping": sweeping, "blocked": blocked, "hyperVHere": vmHyperVHere(), "inventory": info, "waves": waves, "testing": testing}
		if loadErr != "" {
			state["error"] = "The saved plans could not be read: " + loadErr
		}
		if request.URL.Query().Get("switches") == "1" && vmHyperVHere() {
			if switches, err := replHyperVSwitches(); err == nil {
				state["switches"] = switches
			}
		}
		writeJSON(response, http.StatusOK, state)
		return
	}
	var input struct {
		Action   string     `json:"action"`
		ID       string     `json:"id"`
		Name     string     `json:"name"`
		Settings mbSettings `json:"settings"`
		VMIDs    []string   `json:"vmIds"`
		// Methods gives a VM's copy method on create (mbVM.Method);
		// Method is the one the "method" action sets.
		Methods map[string]string `json:"methods"`
		Method  string            `json:"method"`
		At      string            `json:"at"`
		Confirm string            `json:"confirm"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if loadErr != "" {
		fail(http.StatusInternalServerError, "The saved plans could not be read: "+loadErr)
		return
	}
	planName := func() string {
		plans, _, _ := runner.View()
		for _, p := range plans {
			if p.ID == input.ID {
				return p.Name
			}
		}
		return ""
	}
	confirmed := func() bool {
		name := planName()
		return name != "" && strings.EqualFold(strings.TrimSpace(input.Confirm), name)
	}
	var err error
	switch input.Action {
	case "create":
		remote := strings.TrimSpace(input.Settings.RemoteHost) != ""
		if remote {
			a.mu.Lock()
			hvHost, connected := a.hvRemote.host, a.hvRemote.info != nil
			a.mu.Unlock()
			if !connected || !strings.EqualFold(hvHost, strings.TrimSpace(input.Settings.RemoteHost)) {
				fail(http.StatusBadRequest, "Connect to the Hyper-V host or cluster "+input.Settings.RemoteHost+" first.")
				return
			}
		}
		if !remote && !vmHyperVHere() {
			fail(http.StatusBadRequest, "This computer is not a Hyper-V host. Run the application on the Hyper-V host that will run the VMs: plans copy the disks and create the VMs on the computer running them.")
			return
		}
		a.mu.Lock()
		inventory := a.vmware.inventory
		waves := map[string]int{}
		if a.vmware.plan != nil {
			for _, p := range a.vmware.plan.VMs {
				waves[p.ID] = p.Wave
			}
		}
		a.mu.Unlock()
		if inventory == nil {
			fail(http.StatusBadRequest, "Read the inventory from vCenter, ESXi or a Hyper-V host in Discovery first.")
			return
		}
		if why := a.mbReady(&mbPlan{Server: inventory.Server, Settings: input.Settings}); why != "" {
			fail(http.StatusBadRequest, why)
			return
		}
		byID := map[string]vmMachine{}
		for _, m := range inventory.VMs {
			byID[m.ID] = m
		}
		vms := []mbVM{}
		for _, id := range input.VMIDs {
			m, ok := byID[id]
			if !ok {
				fail(http.StatusBadRequest, "A chosen VM is no longer in the inventory. Read it again.")
				return
			}
			vms = append(vms, mbVM{ID: m.ID, Name: m.Name, Wave: waves[m.ID], Method: input.Methods[m.ID]})
		}
		var plan *mbPlan
		plan, err = runner.Create(input.Name, inventory.Server, input.Settings, vms)
		if err == nil {
			writeJSON(response, http.StatusOK, map[string]any{"ok": true, "id": plan.ID})
			go runner.Sweep()
			return
		}
	case "update":
		err = runner.Update(input.ID, input.Settings)
	case "pause", "resume":
		err = runner.SetPaused(input.ID, input.Action == "pause")
		if err == nil && input.Action == "resume" {
			go runner.Sweep()
		}
	case "sync":
		err = runner.SyncNow(input.ID, input.VMIDs)
		if err == nil {
			go runner.Sweep()
		}
	case "cutover":
		if !confirmed() {
			fail(http.StatusBadRequest, "Type the plan's name to confirm the cutover: each chosen VM's guest is shut down, its last changes are copied and the Hyper-V VM is created.")
			return
		}
		at := time.Now()
		if strings.TrimSpace(input.At) != "" && input.At != "now" {
			if at, err = time.Parse(time.RFC3339, input.At); err != nil {
				fail(http.StatusBadRequest, "Choose a valid cutover time.")
				return
			}
			if at.Before(time.Now().Add(-time.Minute)) {
				fail(http.StatusBadRequest, "The cutover time has passed; choose a later one, or Cut over now.")
				return
			}
		}
		err = runner.ScheduleCutover(input.ID, input.VMIDs, at)
		if err == nil {
			go runner.Sweep()
		}
	case "method":
		err = runner.SetMethod(input.ID, input.VMIDs, input.Method)
		if err == nil {
			go runner.Sweep()
		}
	case "cancelCutover":
		err = runner.ScheduleCutover(input.ID, input.VMIDs, time.Time{})
	case "test", "endTest":
		if len(input.VMIDs) != 1 {
			fail(http.StatusBadRequest, "Choose one VM.")
			return
		}
		if !vmHyperVHere() && !runner.IsRemote(input.ID) {
			fail(http.StatusBadRequest, "Test cutovers run on the Hyper-V host that holds the copies.")
			return
		}
		if !a.beginCriticalOperation() {
			fail(http.StatusConflict, "An update is being installed.")
			return
		}
		a.mu.Lock()
		if a.plans.testing || a.closing {
			a.mu.Unlock()
			a.endCriticalOperation()
			fail(http.StatusConflict, "Another test step is running, or the application is closing.")
			return
		}
		a.plans.testing = true
		a.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		if input.Action == "test" {
			err = runner.StartTest(ctx, input.ID, input.VMIDs[0])
		} else {
			err = runner.EndTest(ctx, input.ID, input.VMIDs[0])
		}
		cancel()
		a.mu.Lock()
		a.plans.testing = false
		a.mu.Unlock()
		a.endCriticalOperation()
	case "remove":
		if len(input.VMIDs) == 0 {
			fail(http.StatusBadRequest, "Choose the VMs to take out of the plan.")
			return
		}
		err = runner.Remove(input.ID, input.VMIDs)
	case "delete":
		if !confirmed() {
			fail(http.StatusBadRequest, "Type the plan's name to delete it. Copied disks and created VMs are left in place.")
			return
		}
		err = runner.Remove(input.ID, nil)
	default:
		fail(http.StatusBadRequest, "Unknown action.")
		return
	}
	if err != nil {
		fail(http.StatusBadRequest, strings.TrimSuffix(err.Error(), ".")+".")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}
