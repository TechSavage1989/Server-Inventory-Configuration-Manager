//go:build windows

package main

// Move VMs from an admin computer to a remote Hyper-V host or failover
// cluster (see hyperv_remote.go). The Hyper-V account stays in memory
// (a.hvRemote) until Forget or exit, reaches PowerShell only as one JSON line
// on standard input and never reaches logs, files or the state response.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed Connect-HyperVTarget.ps1
var hvRemoteScriptData []byte

const hvRemoteScriptName = "Connect-HyperVTarget.ps1"

type hvRemoteRuntime struct {
	host     string
	user     string
	password string
	info     *hvRemoteInfo
	// receiver is the receiver on a node the operator started, with its
	// pairing code, kept for repeat copies.
	receiver vmReceiverLink
	message  string
	failed   bool
	serial   uint64
}

type hvRemoteStateResponse struct {
	Connected bool          `json:"connected"`
	Host      string        `json:"host"`
	User      string        `json:"user"`
	Info      *hvRemoteInfo `json:"info"`
	Receiver  string        `json:"receiver"`
	Message   string        `json:"message"`
	Failed    bool          `json:"failed"`
	Serial    uint64        `json:"serial"`
}

// Caller holds a.mu.
func (a *application) hvRemoteStateLocked() *hvRemoteStateResponse {
	r := &a.hvRemote
	return &hvRemoteStateResponse{Connected: r.info != nil, Host: r.host, User: r.user, Info: r.info, Receiver: r.receiver.Address, Message: r.message, Failed: r.failed, Serial: r.serial}
}

func (a *application) handleHVRemote(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/hvremote/") {
	case "connect":
		a.handleHVRemoteConnect(response, request)
	case "forget":
		if !decodeVMwareRequest(response, request, nil) {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.vmware.action == "hvconnect" || a.vmware.action == "hvmove" {
			writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the current step to finish."})
			return
		}
		a.hvRemote = hvRemoteRuntime{serial: a.hvRemote.serial + 1}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "move":
		a.handleHVRemoteMove(response, request)
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "Not found."})
	}
}

// runHVRemote runs Connect-HyperVTarget.ps1 against one host. It runs with
// the RemoteSigned execution policy for this process only (the released
// script is Authenticode signed), never Bypass, and without hiding a console
// window behind a flag: the process has none (CREATE_NO_WINDOW is applied by
// runEngineProcess for every engine).
func (a *application) runHVRemote(ctx context.Context, host, user, password, mode, scriptBody string, into interface{}) error {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "HyperVTarget-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, hvRemoteScriptName)
	if err := os.WriteFile(script, hvRemoteScriptData, 0600); err != nil {
		return err
	}
	resultPath := filepath.Join(directory, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-ComputerName", host, "-Mode", mode, "-ResultPath", resultPath}
	if scriptBody != "" {
		body := filepath.Join(directory, "Create-VM.ps1")
		if err := os.WriteFile(body, []byte(scriptBody), 0600); err != nil {
			return err
		}
		arguments = append(arguments, "-ScriptPath", body)
	}
	if user != "" {
		arguments = append(arguments, "-CredentialFromInput")
	}
	command := exec.CommandContext(ctx, a.powerShellPath, arguments...)
	if user != "" {
		line, _ := json.Marshal(map[string]string{"user": user, "password": password})
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
			return fmt.Errorf("PowerShell remoting to %s stopped: %v", host, waitErr)
		}
		return fmt.Errorf("PowerShell remoting to %s wrote no result", host)
	}
	return hvRemoteParseResult(data, into)
}

// hvRemoteRead reads the host and marks whether it can receive a VM.
func (a *application) hvRemoteRead(ctx context.Context, host, user, password string) (*hvRemoteInfo, error) {
	info := &hvRemoteInfo{}
	if err := a.runHVRemote(ctx, host, user, password, "Read", "", info); err != nil {
		return nil, err
	}
	info.Host = host
	info.Supported, info.Support = hvRemoteSupport(info)
	return info, nil
}

func (a *application) handleHVRemoteConnect(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Host     string `json:"host"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	host, user, password := strings.TrimSpace(input.Host), strings.TrimSpace(input.User), input.Password
	input.Password = ""
	if !hypervHostPattern.MatchString(host) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the Hyper-V host's or failover cluster's DNS name."})
		return
	}
	a.mu.Lock()
	if password == "" && user != "" && strings.EqualFold(a.hvRemote.host, host) && strings.EqualFold(a.hvRemote.user, user) {
		password = a.hvRemote.password
	}
	a.mu.Unlock()
	if user != "" && (!vmwareUserPattern.MatchString(user) || password == "" || len(password) > 256 || strings.ContainsRune(password, 0)) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the password for " + user + ", or leave the account empty to use your own Windows account."})
		return
	}
	if status, message := a.startVMwareAction("hvconnect"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	idle := a.vmwareIdleStatus()
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "hvconnecting"
	a.vmware.message = "Connecting to Hyper-V host " + host + "."
	a.mu.Unlock()
	a.vmwareLog("Connecting to Hyper-V host %s through PowerShell remoting.", host)
	go func() {
		defer cancel()
		info, err := a.hvRemoteRead(ctx, host, user, password)
		a.mu.Lock()
		if err == nil {
			a.hvRemote.host, a.hvRemote.user, a.hvRemote.password, a.hvRemote.info = host, user, password, info
			a.hvRemote.message, a.hvRemote.failed = "Connected to "+info.Computer+".", false
		} else {
			if errors.Is(err, context.Canceled) {
				err = errors.New("Stopped. Nothing was read.")
			}
			a.hvRemote.message, a.hvRemote.failed = err.Error(), true
		}
		a.hvRemote.serial++
		a.mu.Unlock()
		password = ""
		if err != nil {
			a.vmwareLog("%s", err.Error())
			a.finishVMwareAction(idle, "Connecting to Hyper-V host "+host+" failed: "+err.Error())
			return
		}
		where := info.Computer
		if info.Cluster != "" {
			where += " in failover cluster " + info.Cluster
		}
		message := "Connected to Hyper-V host " + where + ". " + info.Support
		a.vmwareLog("%s", message)
		a.finishVMwareAction(idle, message)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// vmMoveNetworkMap gives each VMware network its Hyper-V switch and any new
// VLAN from the plan's network map.
func vmMoveNetworkMap(inventory *vmInventory, plan *vmPlan) (map[string]string, []vmNetwork) {
	switches := map[string]string{}
	networks := append([]vmNetwork(nil), inventory.Networks...)
	if plan != nil {
		for _, network := range plan.Networks {
			if strings.TrimSpace(network.TargetName) != "" {
				switches[network.Source] = strings.TrimSpace(network.TargetName)
			}
			if strings.TrimSpace(network.TargetVLAN) != "" {
				networks = append([]vmNetwork{{Name: network.Source, VLAN: strings.TrimSpace(network.TargetVLAN)}}, networks...)
			}
		}
	}
	return switches, networks
}

func (a *application) handleHVRemoteMove(response http.ResponseWriter, request *http.Request) {
	var input struct {
		VMID       string `json:"vmId"`
		Action     string `json:"action"`
		Confirm    string `json:"confirm"`
		Node       string `json:"node"`
		CopyFolder string `json:"copyFolder"`
		VMFolder   string `json:"vmFolder"`
		Quiesce    bool   `json:"quiesce"`
		KeepMAC    bool   `json:"keepMac"`
		Start      bool   `json:"start"`
		Cluster    bool   `json:"cluster"`
		// Path is "share" (write over the node's administrative share) or
		// "receiver" (a receiver the operator started on the node).
		Path       string `json:"path"`
		Address    string `json:"address"`
		Thumbprint string `json:"thumbprint"`
		Code       string `json:"code"`
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	code := input.Code
	input.Code = ""
	if input.Action != "copy" && input.Action != "cutover" {
		fail(http.StatusBadRequest, "Choose copy or cut over.")
		return
	}
	a.mu.Lock()
	inventory, server, user, password, thumbprint, plan := a.vmware.inventory, a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint, a.vmware.plan
	hv := a.hvRemote
	a.mu.Unlock()
	if inventory == nil || password == "" || !strings.EqualFold(inventory.Server, server) {
		fail(http.StatusBadRequest, "Read the inventory from vCenter or ESXi in this session first. A saved inventory cannot copy disks.")
		return
	}
	if hv.info == nil {
		fail(http.StatusBadRequest, "Connect to the Hyper-V host or cluster first.")
		return
	}
	var target *vmMachine
	for index := range inventory.VMs {
		if inventory.VMs[index].ID == input.VMID {
			target = &inventory.VMs[index]
		}
	}
	if target == nil {
		fail(http.StatusBadRequest, "That VM is not in the inventory. Read the inventory again.")
		return
	}
	if target.Source == vmSourceHyperV {
		fail(http.StatusBadRequest, "Moving a Hyper-V VM to another Hyper-V host from here is not supported yet; use Hyper-V's own live migration or run Move VMs on the target host.")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(input.Confirm), target.Name) {
		fail(http.StatusBadRequest, "Type the VM's name exactly to confirm: "+target.Name)
		return
	}
	if role := vmInfrastructureRole(*target); role != "" || target.Template {
		fail(http.StatusBadRequest, "This VM is not moved.")
		return
	}
	if hvRemoteNameUsed(hv.info, target.Name) {
		fail(http.StatusBadRequest, "A VM named "+target.Name+" already exists on "+vmNonEmpty(hv.info.Cluster, hv.info.Computer)+". Rename or remove it there first; nothing was changed.")
		return
	}
	need := uint64(0)
	if target.UsedBytes > 0 {
		need = uint64(float64(target.UsedBytes)*1.05) + 1<<30
	}
	useReceiver := input.Path == "receiver"
	link := vmReceiverLink{}
	if useReceiver {
		link = vmReceiverLink{Address: strings.TrimSpace(input.Address), Thumbprint: vmNormalizeThumbprint(input.Thumbprint), Code: code}
		if link.Code == "" && strings.EqualFold(hv.receiver.Address, link.Address) && strings.EqualFold(hv.receiver.Thumbprint, link.Thumbprint) {
			link.Code = hv.receiver.Code
		}
		if link.Address == "" || link.Thumbprint == "" || link.Code == "" {
			fail(http.StatusBadRequest, "Paste the receiver's address, thumbprint and pairing code from the node.")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		place, err := vmReceiverWhere(ctx, link.Address, link.Thumbprint, link.Code)
		cancel()
		if err != nil {
			fail(http.StatusBadRequest, "The receiver did not answer: "+err.Error())
			return
		}
		if !place.Folder || place.FolderPath == "" {
			fail(http.StatusBadRequest, "That receiver does not write VHDX files to a folder, or is an older version that does not say where. Start it on the node with a folder (Receive and send disks) from this version.")
			return
		}
		node, err := hvRemoteNodeFor(hv.info, input.Node)
		if err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		if !hvRemoteSameComputer(place.Computer, node) {
			fail(http.StatusBadRequest, "That receiver runs on "+place.Computer+", not on "+node+". Start the receiver on the node that will run the VM, or choose "+place.Computer+" as the node.")
			return
		}
		input.CopyFolder = place.FolderPath
	}
	node, copyLocal, vmLocal, copyAdmin, err := hvRemotePlanMove(hv.info, target.Name, hvRemoteMoveChoice{Node: input.Node, CopyFolder: input.CopyFolder, VMFolder: input.VMFolder, Cluster: input.Cluster, Cutover: input.Action == "cutover"})
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	vm := *target
	switches, networks := vmMoveNetworkMap(inventory, plan)
	// The VM is planned now, with the disk paths it will get, so a missing
	// switch is refused before the guest is shut down.
	diskCount := len(vm.Disks)
	if diskCount == 0 {
		diskCount = 1
	}
	receiverNames := hvRemoteReceiverNames(vm.Name, diskCount)
	localDisks := make([]string, diskCount)
	for index := range localDisks {
		if useReceiver {
			localDisks[index] = strings.TrimRight(copyLocal, `\`) + `\` + receiverNames[index]
		} else {
			localDisks[index] = strings.TrimRight(copyLocal, `\`) + `\` + vmReplicaFileName(vm.Name, index)
		}
	}
	options := vmHyperVOptions{Folder: vmLocal, Disks: localDisks, Switches: switches, KeepMAC: input.KeepMAC, Start: input.Start, ClusterRole: input.Cluster}
	draft, err := vmHyperVPlanFor(vm, networks, options)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if err := hvRemoteCheckSwitches(hv.info, draft); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if status, message := a.startVMwareAction("hvmove"); status != 0 {
		fail(status, message)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	shown := copyLocal + " on " + node
	message := "Copying " + vm.Name + "'s disks to " + shown + "."
	if input.Action == "cutover" {
		message = "Cutting " + vm.Name + " over to Hyper-V on " + node + "."
	}
	a.mu.Lock()
	if useReceiver {
		a.hvRemote.receiver = link
	}
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
		result, err := a.hvRemoteMoveRun(ctx, hv, vm, node, copyLocal, copyAdmin, input.Action == "cutover", input.Quiesce && input.Action == "copy", need, useReceiver, link, receiverNames, shown,
			vmConnectOptions{Server: server, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}, log)
		password = ""
		if err == nil && input.Action == "cutover" {
			a.moveStage("create", "")
			err = a.hvRemoteCreate(ctx, hv, vm, node, networks, options, result, log)
		}
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
			a.finishVMwareAction("connected", vm.Name+" was created on Hyper-V host "+node+started+".")
		default:
			a.finishVMwareAction("connected", "Copied "+vm.Name+"'s disks to "+shown+". Copy again to bring them up to date, then cut over.")
		}
		a.moveStatusEnd(err)
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true, "copyFolder": copyLocal, "node": node})
}

// hvRemoteMoveRun runs one copy round (or the cutover round) to the node:
// over its administrative share with the connected account, or through the
// receiver on the node.
func (a *application) hvRemoteMoveRun(ctx context.Context, hv hvRemoteRuntime, vm vmMachine, node, copyLocal, copyAdmin string, cutover, quiesce bool, need uint64, useReceiver bool, link vmReceiverLink, receiverNames []string, shown string, options vmConnectOptions, log func(string)) (*vmMoveResult, error) {
	if cutover {
		// Read the host again before the guest is shut down: a VM made
		// there since connecting, or a node gone down, stops the cutover
		// while the source still runs.
		fresh, err := a.hvRemoteRead(ctx, hv.host, hv.user, hv.password)
		if err != nil {
			return nil, fmt.Errorf("the Hyper-V host could not be read before the cutover, so nothing was shut down: %w", err)
		}
		if hvRemoteNameUsed(fresh, vm.Name) {
			return nil, errors.New("a VM named " + vm.Name + " now exists on the Hyper-V host; nothing was shut down")
		}
		if _, err := hvRemoteNodeFor(fresh, node); err != nil {
			return nil, fmt.Errorf("%v; nothing was shut down", err)
		}
		a.mu.Lock()
		if a.hvRemote.info != nil && strings.EqualFold(a.hvRemote.host, hv.host) {
			a.hvRemote.info = fresh
			a.hvRemote.serial++
		}
		a.mu.Unlock()
	}
	request := vmMoveRequest{VMID: vm.ID, Name: vm.Name, Datacenter: vm.Datacenter, Quiesce: quiesce, Cutover: cutover, RecordFolder: filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(options.Server)), "Move"), Stage: a.moveStage}
	progress, prefix := a.moveProgress(vm, shown)
	request.Progress = progress
	if useReceiver {
		request.NewSink = link.sinkFactory(ctx, receiverNames)
	} else {
		closeIPC, err := replConnectIPC(node, hv.user, hv.password)
		if err != nil {
			return nil, err
		}
		defer closeIPC()
		if err := os.MkdirAll(copyAdmin, 0755); err != nil {
			return nil, fmt.Errorf("the folder %s could not be made on %s through its administrative share: %v. The account needs administrator rights on the host", shown, node, err)
		}
		if !cutover && need > 0 {
			have := vmFolderBytes(copyAdmin)
			if free, _, ok := vmFreeSpace(copyAdmin); ok && have < need && free < need-have {
				return nil, fmt.Errorf("there is not enough free space for %s's disks in %s: about %s is needed and %s is free", vm.Name, shown, vmFormatBytes(int64(need-have)), vmFormatBytes(int64(free)))
			}
		}
		request.CopyFolder = copyAdmin
	}
	if !cutover {
		a.saveVCenterCapture(vm, log)
	}
	result, err := vmMoveRound(ctx, options, request, log)
	a.copyTransfers().EndRunning(prefix, err, errors.Is(err, context.Canceled), err == nil && cutover)
	if err == nil && result != nil {
		// The VM is created from the paths the node sees.
		local := make([]string, 0, len(result.Disks))
		for index, disk := range result.Disks {
			if useReceiver {
				if index >= len(receiverNames) {
					return result, fmt.Errorf("the VM has more disks (%d) than the inventory listed; read the inventory again", len(result.Disks))
				}
				local = append(local, strings.TrimRight(copyLocal, `\`)+`\`+receiverNames[index])
				continue
			}
			path, convertErr := hvRemoteLocalPath(node, disk)
			if convertErr != nil {
				return result, convertErr
			}
			local = append(local, path)
		}
		result.Disks = local
	}
	return result, err
}

// hvRemoteCreate creates the VM on the node with the copied disks.
func (a *application) hvRemoteCreate(ctx context.Context, hv hvRemoteRuntime, vm vmMachine, node string, networks []vmNetwork, options vmHyperVOptions, result *vmMoveResult, log func(string)) error {
	options.Disks = result.Disks
	plan, err := vmHyperVPlanFor(vm, networks, options)
	if err == nil {
		plan.Remote = true
		if plan.TPM && plan.ClusterRole {
			plan.Warnings = append(plan.Warnings, "The VM gets a virtual TPM with a local key protector, which only this node can open: it fails over to another node only after the key protector is moved there or a Host Guardian Service is set up.")
		}
		result.HyperV = plan
		for _, warning := range plan.Warnings {
			log(warning)
		}
		log("Creating the Hyper-V VM " + vm.Name + " on " + node + ".")
		var created struct {
			Lines []string `json:"lines"`
		}
		err = a.runHVRemote(ctx, node, hv.user, hv.password, "Create", plan.Script(), &created)
		if err == nil {
			result.HyperVID, err = hvRemoteCreatedID(created.Lines, plan)
		}
	}
	if err != nil {
		return errors.New("the disks are copied and the source VM is shut down, but the Hyper-V VM was not created: " + err.Error() + ". Start the source VM again to go back, or fix the cause and cut over again")
	}
	return nil
}
