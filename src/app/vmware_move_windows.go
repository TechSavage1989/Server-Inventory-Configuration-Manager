//go:build windows

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Caller holds a.mu.
func (a *application) vmwareCopyRootLocked() string {
	// On a Hyper-V host the copies are the new VM's disks, so they go where
	// the host keeps virtual hard disks unless the operator chooses a folder.
	if vmHyperVHere() {
		if known, vhd, _ := vmHostDefaultFolders(); known && vhd != "" {
			return vhd
		}
	}
	root := a.defaultOutputRoot
	if strings.TrimSpace(root) == "" {
		root = defaultOutputRoot()
	}
	return filepath.Join(root, "VMware-Copies")
}

var vmHyperVIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// vmHyperVHere reports whether this computer can create Hyper-V VMs: the
// Hyper-V role (its management service) and the Hyper-V PowerShell module.
// Move to Hyper-V creates the VM on this computer, so it refuses to start
// anywhere else rather than copy every disk and fail at the cutover.
func vmHyperVHere() bool {
	system := os.Getenv("SystemRoot")
	if system == "" {
		system = `C:\Windows`
	}
	for _, path := range []string{
		filepath.Join(system, "System32", "vmms.exe"),
		filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "Modules", "Hyper-V"),
	} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

// vmCreateHyperVVM runs the plan's script with the Hyper-V module on this
// computer and returns the new VM's ID.
func vmCreateHyperVVM(plan *vmHyperVPlan) (string, error) {
	out, err := vmRunPowerShell(plan.Script())
	if err != nil {
		if strings.Contains(err.Error(), "Hyper-V") && strings.Contains(strings.ToLower(err.Error()), "module") {
			return "", errors.New("the Hyper-V PowerShell module is not installed here; run this on the Hyper-V host (with the Hyper-V role and its management tools)")
		}
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, vmClusterRoleAdded) || strings.HasPrefix(line, vmClusterRoleFailed) {
			plan.ClusterResult = line
		}
	}
	id := strings.TrimSpace(out[strings.LastIndexAny(out, "\r\n")+1:])
	if !vmHyperVIDPattern.MatchString(id) {
		return "", errors.New("Hyper-V did not return the new VM's ID: " + out)
	}
	return id, nil
}

// handleVMwareMove runs a copy round or the cutover for one VM.
func (a *application) handleVMwareMove(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		VMID       string `json:"vmId"`
		Action     string `json:"action"`
		Confirm    string `json:"confirm"`
		CopyFolder string `json:"copyFolder"`
		VMFolder   string `json:"vmFolder"`
		Quiesce    bool   `json:"quiesce"`
		KeepMAC    bool   `json:"keepMac"`
		Start      bool   `json:"start"`
		Cluster    bool   `json:"cluster"`
		// DisconnectSource sets the old VMware VM's adapters to not connect
		// after the cutover; FixUp fixes the new VM up once it starts.
		DisconnectSource bool `json:"disconnectSource"`
		FixUp            bool `json:"fixUp"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if input.Action != "copy" && input.Action != "cutover" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose copy or cut over."})
		return
	}
	a.mu.Lock()
	inventory, server, user, password, thumbprint, plan := a.vmware.inventory, a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint, a.vmware.plan
	copyRoot := a.vmwareCopyRootLocked()
	a.mu.Unlock()
	var target *vmMachine
	if inventory != nil {
		for index := range inventory.VMs {
			if inventory.VMs[index].ID == input.VMID {
				target = &inventory.VMs[index]
			}
		}
	}
	// A Hyper-V VM is copied through checkpoints on its host, with this
	// computer's account; a VMware VM needs the live vSphere session.
	hyperVSource := target != nil && target.Source == vmSourceHyperV && strings.EqualFold(inventory.ServerType, hypervServerType)
	if inventory == nil || (!hyperVSource && (password == "" || !strings.EqualFold(inventory.Server, server))) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Read the inventory from vCenter or ESXi (or a Hyper-V host) in this session first. A saved inventory cannot copy disks."})
		return
	}
	if target == nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "That VM is not in the inventory. Read the inventory again."})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(input.Confirm), target.Name) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the VM's name exactly to confirm: " + target.Name})
		return
	}
	if !vmHyperVHere() {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "This computer is not a Hyper-V host. Run the application on the Hyper-V host that will run the VM: the disks are written and the VM is created on the computer running the move."})
		return
	}
	if role := vmInfrastructureRole(*target); role != "" || target.Template {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "This VM is not moved."})
		return
	}
	copyFolder := strings.TrimSpace(input.CopyFolder)
	if copyFolder == "" {
		copyFolder = filepath.Join(copyRoot, safeName(target.Name))
	}
	if !filepath.IsAbs(copyFolder) || (migrateProtectedFolder(copyFolder) != "" && !vmHostDefaultAllowed(copyFolder)) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose a full path for the copied disks outside Windows, Program Files and profile folders."})
		return
	}
	vmFolder := strings.TrimSpace(input.VMFolder)
	if vmFolder != "" && (!filepath.IsAbs(vmFolder) || (migrateProtectedFolder(vmFolder) != "" && !vmHostDefaultAllowed(vmFolder))) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose a full path for the Hyper-V VM outside Windows, Program Files and profile folders, or leave it empty for the host's default."})
		return
	}
	if input.Cluster && (!vmOnClusterStorage(copyFolder) || !vmOnClusterStorage(vmFolder)) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "A highly available VM must keep its disks and its configuration on a Cluster Shared Volume (" + vmClusterStorageRoot() + "\\...). Choose one for both folders, or untick Make it highly available."})
		return
	}
	// The first copy needs room for the VM's data; later copies rewrite
	// what is already there.
	if input.Action == "copy" && target.UsedBytes > 0 {
		need := uint64(float64(target.UsedBytes)*1.05) + 1<<30
		if have := vmFolderBytes(copyFolder); have < need {
			need -= have
			if free, _, ok := vmFreeSpace(copyFolder); ok && free < need {
				writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "There is not enough free space for " + target.Name + "'s disks in " + copyFolder + ": about " + vmFormatBytes(int64(need)) + " is needed and " + vmFormatBytes(int64(free)) + " is free. Choose a folder on a larger volume, such as a Cluster Shared Volume."})
				return
			}
		}
	}
	vm := *target
	// The plan's network map names the Hyper-V switch for each VMware
	// network, and its new VLAN where one was typed.
	switches, networks := vmMoveNetworkMap(inventory, plan)
	switches, switchNotes := vmAutoSwitches(switches, vm, vmHostSwitches())
	if status, message := a.startVMwareAction("move"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	message := "Copying " + vm.Name + "'s disks to " + copyFolder + "."
	if input.Action == "cutover" {
		message = "Cutting " + vm.Name + " over to Hyper-V."
	}
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "moving"
	a.vmware.message = message
	a.vmware.moveError = ""
	a.mu.Unlock()
	a.moveStatusBegin(vm, input.Action, "create")
	a.vmwareLog("%s", message)
	go func() {
		defer cancel()
		log := func(text string) { a.vmwareLog("%s", text) }
		if input.Action == "cutover" {
			for _, note := range switchNotes {
				log(note)
			}
		}
		records := filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(server)), "Move")
		progress, prefix := a.moveProgress(vm, copyFolder)
		connect := vmConnectOptions{Server: server, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
		result, err := a.runMoveRound(ctx, inventory, vm, connect,
			vmMoveRequest{VMID: vm.ID, Name: vm.Name, Datacenter: vm.Datacenter, CopyFolder: copyFolder, Quiesce: input.Quiesce, Cutover: input.Action == "cutover", RecordFolder: records, Progress: progress, Stage: a.moveStage}, log)
		a.copyTransfers().EndRunning(prefix, err, errors.Is(err, context.Canceled), err == nil && input.Action == "cutover")
		password = ""
		if err == nil && input.Action == "cutover" {
			var hyperV *vmHyperVPlan
			hyperV, err = vmHyperVPlanFor(vm, networks, vmHyperVOptions{Folder: vmFolder, Disks: result.Disks, Switches: switches, KeepMAC: input.KeepMAC, Start: input.Start, ClusterRole: input.Cluster})
			if err == nil {
				result.HyperV = hyperV
				for _, warning := range hyperV.Warnings {
					log(warning)
				}
				a.moveStage("create", "")
				log("Creating the Hyper-V VM " + vm.Name + ".")
				result.HyperVID, err = vmCreateHyperVVM(hyperV)
			}
			if err != nil {
				err = errors.New("the disks are copied and the source VM is shut down, but the Hyper-V VM was not created: " + err.Error() + ". Start the source VM again to go back, or fix the cause and cut over again")
			} else {
				result.Notes = a.afterCutover(context.Background(), vm, result.HyperVID, "", input.Start, input.DisconnectSource, input.FixUp, connect, records, log)
			}
		}
		connect.Password = ""
		if result != nil {
			result.Finished = time.Now().UTC()
		}
		a.mu.Lock()
		a.vmware.move = result
		a.vmware.moveSerial++
		if err != nil {
			a.vmware.moveError = err.Error()
		}
		if result != nil && result.ShutDown && a.vmware.inventory != nil {
			for index := range a.vmware.inventory.VMs {
				if a.vmware.inventory.VMs[index].ID == vm.ID {
					a.vmware.inventory.VMs[index].PowerState, a.vmware.inventory.VMs[index].ToolsRunning = "poweredOff", false
				}
			}
			a.vmware.inventorySerial++
		}
		a.mu.Unlock()
		switch {
		case errors.Is(err, context.Canceled):
			a.vmwareLog("Stopped. The next copy resumes where this one stopped.")
			a.finishVMwareAction("connected", "Copying "+vm.Name+" was stopped.")
		case err != nil:
			a.vmwareLog("%s", err.Error())
			a.finishVMwareAction("connected", "Moving "+vm.Name+" stopped: "+err.Error())
		case input.Action == "cutover":
			started := ""
			if input.Start {
				started = " and started"
			}
			a.finishVMwareAction("connected", vm.Name+" was created on Hyper-V"+started+".")
		default:
			a.finishVMwareAction("connected", "Copied "+vm.Name+"'s disks. Copy again to bring them up to date, then cut over.")
		}
		a.moveStatusEnd(err)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true, "copyFolder": copyFolder})
}
