//go:build windows

package main

// Migration plans run from an admin computer against another Hyper-V host
// or failover cluster (mbSettings.RemoteHost). Every round is Move VMs'
// remote round (hvRemoteRound: the node's administrative share with the
// account connected in Move VMs), every VM is created by the same remote
// creation script (rule 39), and test cutovers run the same differencing
// disk scripts as on the host, through PowerShell remoting.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// mbRemoteReadyLocked says why a remote plan cannot run now, or "".
// Caller holds a.mu.
func (a *application) mbRemoteReadyLocked(plan *mbPlan) string {
	inventory := a.vmware.inventory
	if inventory == nil || !strings.EqualFold(inventory.Server, plan.Server) {
		return "Read the inventory of " + plan.Server + " in Discovery (VMware and Hyper-V) to let this plan copy." + a.vcRememberHint(plan.Server)
	}
	if strings.EqualFold(inventory.ServerType, hypervServerType) {
		return "A plan run against another Hyper-V host moves VMware VMs only."
	}
	if a.vmware.password == "" || !strings.EqualFold(a.vmware.server, plan.Server) {
		return "Sign in to " + plan.Server + " in Discovery again: the password is kept only while the application runs." + a.vcRememberHint(plan.Server)
	}
	if a.hvRemote.info == nil || !strings.EqualFold(a.hvRemote.host, plan.Settings.RemoteHost) {
		return "Connect to " + plan.Settings.RemoteHost + " in New migration or Move VMs again: the Hyper-V account is kept only while the application runs."
	}
	if a.installing || a.closing {
		return "The application is closing or installing an update."
	}
	return ""
}

// remoteHV returns the connection to the plan's host.
func (t *mbHostTarget) remoteHV(plan *mbPlan) (hvRemoteRuntime, error) {
	a := t.a
	a.mu.Lock()
	defer a.mu.Unlock()
	hv := a.hvRemote
	if hv.info == nil || !strings.EqualFold(hv.host, plan.Settings.RemoteHost) {
		return hvRemoteRuntime{}, errors.New("connect to " + plan.Settings.RemoteHost + " again: the Hyper-V account is kept only while the application runs")
	}
	return hv, nil
}

// remotePlace checks the node and folders for one VM.
func (t *mbHostTarget) remotePlace(plan *mbPlan, vm *mbVM, info *hvRemoteInfo, cutover bool) (node, copyLocal, vmLocal, copyAdmin string, err error) {
	folder := vm.CopyFolder
	if folder == "" {
		if folder, err = mbRemoteCopyFolder(plan.Settings.CopyRoot, info.VHDPath, vm.Name); err != nil {
			return
		}
	}
	return hvRemotePlanMove(info, vm.Name, hvRemoteMoveChoice{Node: plan.Settings.Node, CopyFolder: folder, VMFolder: plan.Settings.VMFolder, Cluster: plan.Settings.Cluster, Cutover: cutover})
}

// remoteDraft plans the Hyper-V VM with the paths it will get, so a switch
// the host lacks is refused before the guest is shut down.
func (t *mbHostTarget) remoteDraft(plan *mbPlan, inventory *vmInventory, m vmMachine, info *hvRemoteInfo, vmLocal string, disks []string, log func(string)) (*vmHyperVPlan, []vmNetwork, vmHyperVOptions, error) {
	a := t.a
	a.mu.Lock()
	switches, networks := vmMoveNetworkMap(inventory, a.vmware.plan)
	a.mu.Unlock()
	switches, notes := vmAutoSwitches(switches, m, hvRemoteSwitchNames(info))
	if log != nil {
		for _, note := range notes {
			log(note)
		}
	}
	options := vmHyperVOptions{Folder: vmLocal, Disks: disks, Switches: switches, KeepMAC: plan.Settings.KeepMAC, Start: plan.Settings.StartVMs, ClusterRole: plan.Settings.Cluster}
	draft, err := vmHyperVPlanFor(m, networks, options)
	if err != nil {
		return nil, nil, options, err
	}
	if err := hvRemoteCheckSwitches(info, draft); err != nil {
		return nil, nil, options, err
	}
	return draft, networks, options, nil
}

func (t *mbHostTarget) remoteDisks(m vmMachine, copyLocal string) []string {
	count := len(m.Disks)
	if count == 0 {
		count = 1
	}
	disks := make([]string, count)
	for index := range disks {
		disks[index] = strings.TrimRight(copyLocal, `\`) + `\` + vmReplicaFileName(m.Name, index)
	}
	return disks
}

func (t *mbHostTarget) remoteCheck(plan *mbPlan, vm *mbVM, m vmMachine) error {
	if m.Source == vmSourceHyperV {
		return errors.New("a plan run against another Hyper-V host moves VMware VMs only; run the plan on the target host for a Hyper-V VM")
	}
	hv, err := t.remoteHV(plan)
	if err != nil {
		return err
	}
	if vm.State == "" || vm.State == mbWaiting {
		if hvRemoteNameUsed(hv.info, vm.Name) {
			return errors.New("a VM named " + vm.Name + " already exists on " + vmNonEmpty(hv.info.Cluster, hv.info.Computer))
		}
	}
	_, copyLocal, vmLocal, _, err := t.remotePlace(plan, vm, hv.info, false)
	if err != nil {
		return err
	}
	t.a.mu.Lock()
	inventory := t.a.vmware.inventory
	t.a.mu.Unlock()
	_, _, _, err = t.remoteDraft(plan, inventory, m, hv.info, vmLocal, t.remoteDisks(m, copyLocal), nil)
	return err
}

func (t *mbHostTarget) remoteRound(ctx context.Context, plan *mbPlan, vm *mbVM, inventory *vmInventory, m vmMachine, cutover bool, throttle *vmThrottle, progress func(string, vmCopyProgress), log func(string)) (*vmMoveResult, error) {
	a := t.a
	if m.Source == vmSourceHyperV {
		return nil, errors.New("a plan run against another Hyper-V host moves VMware VMs only")
	}
	hv, err := t.remoteHV(plan)
	if err != nil {
		return nil, err
	}
	node, copyLocal, vmLocal, copyAdmin, err := t.remotePlace(plan, vm, hv.info, cutover)
	if err != nil {
		return nil, err
	}
	if cutover {
		if _, _, _, err := t.remoteDraft(plan, inventory, m, hv.info, vmLocal, t.remoteDisks(m, copyLocal), nil); err != nil {
			return nil, fmt.Errorf("%v; nothing was shut down", err)
		}
	}
	need := uint64(0)
	if !vm.Seeded && m.UsedBytes > 0 {
		need = uint64(float64(m.UsedBytes)*1.05) + 1<<30
	}
	vm.CopyFolder = copyLocal
	a.mu.Lock()
	connect := vmConnectOptions{Server: a.vmware.server, User: a.vmware.account, Password: a.vmware.password, Thumbprint: a.vmware.thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
	a.mu.Unlock()
	result, err := a.hvRemoteRound(ctx, hv, m, node, copyLocal, copyAdmin, cutover, plan.Settings.Quiesce && !cutover, plan.Settings.EjectMedia, need, false, vmReceiverLink{}, nil, copyLocal+" on "+node, connect, throttle, progress, log)
	connect.Password = ""
	if result != nil {
		result.Host = node
		if result.ShutDown {
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
	}
	return result, err
}

func (t *mbHostTarget) remoteCreate(ctx context.Context, plan *mbPlan, vm *mbVM, inventory *vmInventory, m vmMachine, result *vmMoveResult, log func(string)) (string, []string, error) {
	a := t.a
	hv, err := t.remoteHV(plan)
	if err != nil {
		return "", nil, err
	}
	node, _, vmLocal, _, err := t.remotePlace(plan, vm, hv.info, true)
	if err != nil {
		return "", nil, err
	}
	_, networks, options, err := t.remoteDraft(plan, inventory, m, hv.info, vmLocal, result.Disks, log)
	if err != nil {
		return "", nil, err
	}
	if err := a.hvRemoteCreateVM(ctx, hv, m, node, networks, options, result, log); err != nil {
		return "", nil, err
	}
	notes := []string{}
	if result.HyperV != nil {
		result.HyperV.Notes = "Moved by the migration plan " + plan.Name + " in Server Inventory and Migration Manager."
		if result.HyperV.ClusterResult != "" {
			notes = append(notes, result.HyperV.ClusterResult)
		}
	}
	a.mu.Lock()
	connect := vmConnectOptions{Server: a.vmware.server, User: a.vmware.account, Password: a.vmware.password, Thumbprint: a.vmware.thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
	a.mu.Unlock()
	notes = append(notes, "Created on "+node+".")
	notes = append(notes, a.afterCutover(ctx, m, result.HyperVID, node, plan.Settings.StartVMs, plan.Settings.DisconnectSource, plan.Settings.FixUp, connect,
		filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(plan.Server)), "Move"), plan.Name, log)...)
	return result.HyperVID, notes, nil
}

// remoteTestPlan is the test VM's plan: differencing children under the
// copy folder, adapters on the test switch or disconnected, no VLAN or MAC.
func (t *mbHostTarget) remoteTestPlan(plan *mbPlan, vm *mbVM, m vmMachine, info *hvRemoteInfo, children []string, testRoot string) (*vmHyperVPlan, error) {
	switches := map[string]string{}
	for _, nic := range m.NICs {
		switches[nic.Network] = plan.Settings.TestSwitch
	}
	hyperV, err := vmHyperVPlanFor(m, nil, vmHyperVOptions{Folder: testRoot, Disks: children, Switches: switches, Start: true})
	if err != nil {
		return nil, err
	}
	hyperV.Name = mbTestName(vm)
	hyperV.Remote = true
	hyperV.Notes = "Test cutover from the migration plan " + plan.Name + ". Ending the test in Server Inventory and Migration Manager removes this VM and its test disks; the copies are not changed."
	for index := range hyperV.Adapters {
		hyperV.Adapters[index].VLAN, hyperV.Adapters[index].MAC = 0, ""
	}
	if plan.Settings.TestSwitch != "" {
		if err := hvRemoteCheckSwitches(info, hyperV); err != nil {
			return nil, err
		}
	}
	return hyperV, nil
}

func (t *mbHostTarget) remoteStartTest(ctx context.Context, plan *mbPlan, vm *mbVM, m vmMachine, log func(string)) (*mbTest, error) {
	hv, err := t.remoteHV(plan)
	if err != nil {
		return nil, err
	}
	node, err := hvRemoteNodeFor(hv.info, plan.Settings.Node)
	if err != nil {
		return nil, err
	}
	name := mbTestName(vm)
	if hvRemoteNameUsed(hv.info, name) {
		return nil, errors.New("a VM named " + name + " already exists on the Hyper-V host; nothing was changed")
	}
	folder := strings.TrimRight(vm.CopyFolder, `\`)
	testRoot := folder + `\Test cutovers`
	copies, children := []string{}, []string{}
	for index, disk := range vm.Disks {
		cut := strings.LastIndex(disk, `\`)
		if cut < 0 || !strings.EqualFold(disk[:cut], folder) {
			return nil, fmt.Errorf("%s is not in the VM's copy folder", disk)
		}
		copies = append(copies, disk[cut+1:])
		children = append(children, fmt.Sprintf(`%s\%s\test-%d-%s`, testRoot, name, index, disk[cut+1:]))
	}
	hyperV, err := t.remoteTestPlan(plan, vm, m, hv.info, children, testRoot)
	if err != nil {
		return nil, err
	}
	test := &mbTest{Name: name, Folder: folder, Copies: copies, Children: children, Started: time.Now().UTC()}
	log("Making the test VM " + name + " on differencing disks on " + node + ".")
	var created struct {
		Lines []string `json:"lines"`
	}
	if err := t.a.runHVRemote(ctx, node, hv.user, hv.password, "Create", hvRemoteTestScript(hyperV, folder, copies, children), &created); err != nil {
		return test, errors.New("the test VM was not made: " + err.Error() + ". End the test to remove what was made")
	}
	id, err := hvRemoteCreatedID(created.Lines, hyperV)
	if err != nil {
		return test, errors.New(err.Error() + ". End the test to remove what was made")
	}
	test.VMID = id
	return test, nil
}

func (t *mbHostTarget) remoteEndTest(ctx context.Context, plan *mbPlan, vm *mbVM, log func(string)) error {
	hv, err := t.remoteHV(plan)
	if err != nil {
		return err
	}
	node, err := hvRemoteNodeFor(hv.info, plan.Settings.Node)
	if err != nil {
		return err
	}
	test := vm.Test
	log("Removing the test VM " + test.Name + " and its test disks on " + node + ".")
	var ended struct {
		Lines []string `json:"lines"`
	}
	if err := t.a.runHVRemote(ctx, node, hv.user, hv.password, "Create", hvRemoteEndTestScript(test), &ended); err != nil {
		return errors.New("the test VM was not removed: " + err.Error())
	}
	return nil
}

// hvRemoteTestScript makes the test disks and the test VM on the node.
func hvRemoteTestScript(hyperV *vmHyperVPlan, folder string, copies, children []string) string {
	return replFailoverScript(hyperV, folder, copies, children)
}

// hvRemoteEndTestScript removes the test VM by its recorded ID and name and
// its test disks; with no recorded ID it removes no VM (never by name).
func hvRemoteEndTestScript(test *mbTest) string {
	script := ""
	if test.VMID == "" {
		script = fmt.Sprintf("Import-Module Hyper-V\nif (Hyper-V\\Get-VM -Name %s -ErrorAction SilentlyContinue) { throw ('A VM named ' + %s + ' exists but this application did not record its ID; remove it in Hyper-V Manager, then end the test again.') }\n", vmPSQuote(test.Name), vmPSQuote(test.Name))
	}
	script += replFailoverEndScript(replFailover{VMID: test.VMID, Name: test.Name, Copies: test.Copies, Children: test.Children}, test.Folder)
	if test.VMID == "" {
		script = strings.Replace(script, "$vm = Hyper-V\\Get-VM -Id ''", "$vm = $null", 1)
	}
	return script
}
