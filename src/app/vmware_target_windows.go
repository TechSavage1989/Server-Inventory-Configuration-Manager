//go:build windows

package main

// The VMware tab's moves to Scale HyperCore, Nutanix AHV, Proxmox VE and
// physical servers: connect to the platform, build and upload receiver media,
// create the new VM, check its receiver, then copy rounds and the cutover.

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

type vmTargetRuntime struct {
	connection  vmTargetConnection
	platform    vmTargetPlatform
	inventory   *vmTargetInventory
	certificate *vmCertificateError
	mediaPath   string
	iso         string
	isoName     string
	receivers   map[string]vmTargetReceiver
	created     map[string]vmTargetRecord
	message     string
	failed      bool
	serial      uint64
}

type vmTargetReceiver struct {
	Link    vmReceiverLink   `json:"link"`
	Disks   []vmReceiverDisk `json:"disks"`
	Mapping []string         `json:"mapping"`
}

type vmTargetStateResponse struct {
	Platform    string                      `json:"platform"`
	Server      string                      `json:"server"`
	User        string                      `json:"user"`
	Node        string                      `json:"node"`
	Connected   bool                        `json:"connected"`
	Inventory   *vmTargetInventory          `json:"inventory"`
	Certificate *vmCertificateError         `json:"certificate"`
	MediaPath   string                      `json:"mediaPath"`
	ISO         string                      `json:"iso"`
	ISOName     string                      `json:"isoName"`
	Receivers   map[string]vmTargetReceiver `json:"receivers"`
	Created     map[string]vmTargetRecord   `json:"created"`
	Message     string                      `json:"message"`
	Failed      bool                        `json:"failed"`
	Serial      uint64                      `json:"serial"`
}

// Caller holds a.mu.
func (a *application) vmTargetStateLocked() *vmTargetStateResponse {
	t := &a.vmware.target
	return &vmTargetStateResponse{
		Platform: t.connection.Platform, Server: t.connection.Server, User: t.connection.User, Node: t.connection.Node,
		Connected: t.platform != nil, Inventory: t.inventory, Certificate: t.certificate, MediaPath: t.mediaPath, ISO: t.iso, ISOName: t.isoName,
		Receivers: t.receivers, Created: t.created, Message: t.message, Failed: t.failed, Serial: t.serial,
	}
}

func (a *application) vmTargetRecordsFolder(server string) string {
	return filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(server)), "Targets")
}

func (a *application) vmTargetDone(message string, failed bool) {
	a.mu.Lock()
	a.vmware.target.message, a.vmware.target.failed = message, failed
	a.vmware.target.serial++
	a.mu.Unlock()
	a.vmwareLog("%s", message)
}

func (a *application) handleVMwareTarget(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/vmware/target/") {
	case "connect":
		a.handleVMTargetConnect(response, request)
	case "forget":
		a.handleVMTargetForget(response, request)
	case "media":
		a.handleVMTargetMedia(response, request)
	case "upload":
		a.handleVMTargetUpload(response, request)
	case "create":
		a.handleVMTargetCreate(response, request)
	case "receiver":
		a.handleVMTargetReceiver(response, request)
	case "move":
		a.handleVMTargetMove(response, request)
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "Not found."})
	}
}

func vmTargetFail(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]any{"ok": false, "error": message})
}

func (a *application) handleVMTargetConnect(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Platform   string `json:"platform"`
		Server     string `json:"server"`
		User       string `json:"user"`
		Password   string `json:"password"`
		Node       string `json:"node"`
		Thumbprint string `json:"thumbprint"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	held := a.vmware.target.connection
	a.mu.Unlock()
	if input.Password == "" && strings.EqualFold(held.Server, strings.TrimSpace(input.Server)) && held.User == strings.TrimSpace(input.User) && held.Platform == input.Platform {
		input.Password = held.Password
	}
	if input.Thumbprint == "" && strings.EqualFold(held.Server, strings.TrimSpace(input.Server)) {
		input.Thumbprint = held.Thumbprint
	}
	connection := vmTargetConnection{Platform: input.Platform, Server: strings.TrimSpace(input.Server), User: strings.TrimSpace(input.User), Password: input.Password, Node: strings.TrimSpace(input.Node), Thumbprint: input.Thumbprint}
	input.Password = ""
	platform, err := newVMTargetPlatform(connection)
	if err != nil {
		vmTargetFail(response, http.StatusBadRequest, err.Error())
		return
	}
	if status, message := a.startVMwareAction("target-connect"); status != 0 {
		vmTargetFail(response, status, message)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	inventory, err := platform.Inventory(ctx)
	cancel()
	var certificate *vmCertificateError
	if errors.As(err, &certificate) {
		a.mu.Lock()
		a.vmware.target.certificate = certificate
		a.vmware.target.serial++
		a.mu.Unlock()
		a.finishVMwareAction("", "")
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": certificate.Error(), "certificate": certificate})
		return
	}
	if err != nil {
		a.finishVMwareAction("", "")
		vmTargetFail(response, http.StatusBadGateway, err.Error())
		return
	}
	if connection.Platform == vmTargetProxmox && connection.Node == "" && len(inventory.Nodes) > 0 {
		connection.Node = inventory.Nodes[0].ID
	}
	a.mu.Lock()
	a.vmware.target.connection, a.vmware.target.platform, a.vmware.target.inventory, a.vmware.target.certificate = connection, platform, inventory, nil
	a.vmware.target.serial++
	a.mu.Unlock()
	a.vmwareLog("Connected to %s (%s) as %s.", connection.Server, inventory.Version, connection.User)
	a.finishVMwareAction("", "")
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "inventory": inventory})
}

func (a *application) handleVMTargetForget(response http.ResponseWriter, request *http.Request) {
	if !decodeVMwareRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vmwareBusyLocked() {
		vmTargetFail(response, http.StatusConflict, "Wait for the VMware step to finish first.")
		return
	}
	a.vmware.target.connection, a.vmware.target.platform, a.vmware.target.inventory, a.vmware.target.certificate = vmTargetConnection{}, nil, nil, nil
	a.vmware.target.receivers = nil
	a.vmware.target.serial++
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

func (a *application) handleVMTargetMedia(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Target  string   `json:"target"`
		Output  string   `json:"output"`
		Drivers []string `json:"drivers"`
		Disks   string   `json:"disks"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		vmTargetFail(response, http.StatusInternalServerError, err.Error())
		return
	}
	root := a.vmwareOutputFolder("Receiver-Media")
	output := strings.TrimSpace(input.Output)
	if output == "" {
		output = filepath.Join(root, "receiver-"+safeName(input.Target)+".iso")
	}
	if !filepath.IsAbs(output) || migrateProtectedFolder(filepath.Dir(output)) != "" {
		vmTargetFail(response, http.StatusBadRequest, "Choose a full path for the ISO outside Windows, Program Files and profile folders.")
		return
	}
	drivers := []string{}
	for _, folder := range input.Drivers {
		if folder = strings.TrimSpace(folder); folder != "" {
			if !filepath.IsAbs(folder) {
				vmTargetFail(response, http.StatusBadRequest, "Driver folders must be full paths.")
				return
			}
			drivers = append(drivers, folder)
		}
	}
	work := filepath.Join(filepath.Dir(output), "work-"+time.Now().Format("20060102_150405"))
	if status, message := a.startVMwareAction("media"); status != 0 {
		vmTargetFail(response, status, message)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "media"
	a.vmware.message = "Building receiver media."
	a.mu.Unlock()
	go func() {
		defer cancel()
		os.MkdirAll(filepath.Dir(output), 0700)
		result, err := vmBuildReceiverMedia(ctx, a.powerShellPath, executable, vmReceiverMediaOptions{Output: output, Work: work, Drivers: drivers, Target: input.Target, Disks: input.Disks}, func(line string) { a.vmwareLog("%s", line) })
		os.RemoveAll(work)
		switch {
		case err != nil:
			a.vmTargetDone("Building receiver media failed: "+err.Error(), true)
		case !result.OK:
			problem := result.Error
			if problem == "" {
				problem = strings.Join(result.Problems, " ")
			}
			a.vmTargetDone("Receiver media was not built: "+problem, true)
		default:
			a.mu.Lock()
			a.vmware.target.mediaPath = result.Output
			a.mu.Unlock()
			a.vmTargetDone(fmt.Sprintf("Receiver media written to %s (%s, SHA-256 %s).", result.Output, vmFormatBytes(result.Size), result.SHA256), false)
		}
		a.finishVMwareAction("connected", "")
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true, "output": output})
}

func (a *application) handleVMTargetUpload(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	platform := a.vmware.target.platform
	if input.Path == "" {
		input.Path = a.vmware.target.mediaPath
	}
	a.mu.Unlock()
	if platform == nil {
		vmTargetFail(response, http.StatusBadRequest, "Connect to the platform first.")
		return
	}
	file, err := os.Open(input.Path)
	if err != nil {
		vmTargetFail(response, http.StatusBadRequest, "The ISO could not be opened: "+err.Error())
		return
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(input.Path), ".iso") {
		file.Close()
		vmTargetFail(response, http.StatusBadRequest, "Choose an .iso file.")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = filepath.Base(input.Path)
	}
	if status, message := a.startVMwareAction("upload"); status != 0 {
		file.Close()
		vmTargetFail(response, status, message)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "uploading"
	a.vmware.message = "Uploading " + name + "."
	a.mu.Unlock()
	a.vmwareLog("Uploading %s (%s).", name, vmFormatBytes(info.Size()))
	go func() {
		defer cancel()
		defer file.Close()
		iso, err := platform.UploadISO(ctx, name, file, info.Size())
		if err != nil {
			a.vmTargetDone("Uploading the receiver media failed: "+err.Error(), true)
		} else {
			a.mu.Lock()
			a.vmware.target.iso, a.vmware.target.isoName = iso, name
			a.mu.Unlock()
			a.vmTargetDone("Uploaded the receiver media as "+iso+".", false)
		}
		a.finishVMwareAction("connected", "")
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// vmwareTargetVM finds the inventory VM and checks the typed name.
func (a *application) vmwareTargetVM(response http.ResponseWriter, id, confirm string, needLive bool) (*vmMachine, string, bool) {
	a.mu.Lock()
	inventory, server, password := a.vmware.inventory, a.vmware.server, a.vmware.password
	a.mu.Unlock()
	hyperVSource := inventory != nil && strings.EqualFold(inventory.ServerType, hypervServerType)
	if inventory == nil || (needLive && !hyperVSource && (password == "" || !strings.EqualFold(inventory.Server, server))) {
		vmTargetFail(response, http.StatusBadRequest, "Read the inventory from vCenter or ESXi in this session first.")
		return nil, "", false
	}
	for index := range inventory.VMs {
		vm := inventory.VMs[index]
		if vm.ID != id {
			continue
		}
		if role := vmInfrastructureRole(vm); role != "" || vm.Template {
			vmTargetFail(response, http.StatusBadRequest, "This VM is not moved.")
			return nil, "", false
		}
		if confirm != "-" && !strings.EqualFold(strings.TrimSpace(confirm), vm.Name) {
			vmTargetFail(response, http.StatusBadRequest, "Type the VM's name exactly to confirm: "+vm.Name)
			return nil, "", false
		}
		return &vm, inventory.Server, true
	}
	vmTargetFail(response, http.StatusBadRequest, "That VM is not in the inventory. Read the inventory again.")
	return nil, "", false
}

func (a *application) handleVMTargetCreate(response http.ResponseWriter, request *http.Request) {
	var input vmTargetCreateInput
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	vm, server, ok := a.vmwareTargetVM(response, input.VMID, input.Confirm, false)
	if !ok {
		return
	}
	a.mu.Lock()
	platform, connection, iso := a.vmware.target.platform, a.vmware.target.connection, a.vmware.target.iso
	a.mu.Unlock()
	if platform == nil {
		vmTargetFail(response, http.StatusBadRequest, "Connect to the platform first.")
		return
	}
	spec, err := vmTargetSpecFor(*vm, input, iso)
	if err != nil {
		vmTargetFail(response, http.StatusBadRequest, err.Error())
		return
	}
	if status, message := a.startVMwareAction("target-create"); status != 0 {
		vmTargetFail(response, status, message)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "creating"
	a.vmware.message = "Creating " + spec.Name + " on " + vmTargetLabel(connection.Platform) + "."
	a.mu.Unlock()
	records := a.vmTargetRecordsFolder(server)
	go func() {
		defer cancel()
		created, err := vmTargetCreate(ctx, platform, spec, func(line string) { a.vmwareLog("%s", line) })
		if created != nil {
			record := vmTargetRecord{VMwareID: vm.ID, VMwareName: vm.Name, Platform: connection.Platform, Server: connection.Server, Node: connection.Node, VM: *created, ISO: iso, Created: time.Now().UTC()}
			if saveErr := saveVMTargetRecord(records, record); saveErr != nil {
				a.vmwareLog("The record of the new VM could not be saved: %s", saveErr.Error())
			}
			a.mu.Lock()
			if a.vmware.target.created == nil {
				a.vmware.target.created = map[string]vmTargetRecord{}
			}
			a.vmware.target.created[vm.ID] = record
			a.mu.Unlock()
		}
		if err != nil {
			a.vmTargetDone("Creating the VM failed: "+err.Error(), true)
		} else {
			a.vmTargetDone(spec.Name+" was created on "+vmTargetLabel(connection.Platform)+" and started from the receiver media. Open its console, note the pairing code and thumbprint, and check the receiver.", false)
		}
		a.finishVMwareAction("connected", "")
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleVMTargetReceiver(response http.ResponseWriter, request *http.Request) {
	var input struct {
		VMID       string `json:"vmId"`
		Address    string `json:"address"`
		Code       string `json:"code"`
		Thumbprint string `json:"thumbprint"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	vm, _, ok := a.vmwareTargetVM(response, input.VMID, "-", false)
	if !ok {
		return
	}
	link := vmReceiverLink{Address: strings.TrimSpace(input.Address), Thumbprint: strings.TrimSpace(input.Thumbprint), Code: strings.TrimSpace(input.Code)}
	if link.Code == "" {
		a.mu.Lock()
		if held, found := a.vmware.target.receivers[vm.ID]; found && held.Link.Address == link.Address {
			link.Code = held.Link.Code
		}
		a.mu.Unlock()
	}
	input.Code = ""
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	disks, err := vmReceiverListDisks(ctx, link.Address, link.Thumbprint, link.Code)
	cancel()
	if err != nil {
		vmTargetFail(response, http.StatusBadGateway, err.Error())
		return
	}
	sizes := []int64{}
	for _, disk := range vm.Disks {
		sizes = append(sizes, disk.CapacityBytes)
	}
	mapping, err := vmMatchReceiverDisks(sizes, disks)
	if err != nil {
		vmTargetFail(response, http.StatusConflict, err.Error())
		return
	}
	a.mu.Lock()
	if a.vmware.target.receivers == nil {
		a.vmware.target.receivers = map[string]vmTargetReceiver{}
	}
	a.vmware.target.receivers[vm.ID] = vmTargetReceiver{Link: link, Disks: disks, Mapping: mapping}
	a.vmware.target.serial++
	a.mu.Unlock()
	for index, name := range mapping {
		a.vmwareLog("%s: %s goes to %s on %s.", vm.Name, vm.Disks[index].Label, name, link.Address)
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "disks": disks, "mapping": mapping})
}

func (a *application) handleVMTargetMove(response http.ResponseWriter, request *http.Request) {
	var input struct {
		VMID    string `json:"vmId"`
		Action  string `json:"action"`
		Confirm string `json:"confirm"`
		Quiesce bool   `json:"quiesce"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if input.Action != "copy" && input.Action != "cutover" {
		vmTargetFail(response, http.StatusBadRequest, "Choose copy or cut over.")
		return
	}
	vm, _, ok := a.vmwareTargetVM(response, input.VMID, input.Confirm, true)
	if !ok {
		return
	}
	a.mu.Lock()
	server, user, password, thumbprint := a.vmware.server, a.vmware.account, a.vmware.password, a.vmware.thumbprint
	receiver, haveReceiver := a.vmware.target.receivers[vm.ID]
	created, haveCreated := a.vmware.target.created[vm.ID]
	platform := a.vmware.target.platform
	a.mu.Unlock()
	if !haveReceiver || receiver.Link.Code == "" {
		vmTargetFail(response, http.StatusBadRequest, "Check the receiver first: enter its address, pairing code and thumbprint.")
		return
	}
	if !haveCreated {
		if record, err := loadVMTargetRecord(a.vmTargetRecordsFolder(server), vm.ID); err == nil {
			created, haveCreated = *record, true
		}
	}
	if status, message := a.startVMwareAction("move"); status != 0 {
		vmTargetFail(response, status, message)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	message := "Copying " + vm.Name + "'s disks to " + receiver.Link.Address + "."
	if input.Action == "cutover" {
		message = "Cutting " + vm.Name + " over."
	}
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "moving"
	a.vmware.message = message
	a.vmware.moveError = ""
	a.mu.Unlock()
	a.vmwareLog("%s", message)
	go func() {
		defer cancel()
		log := func(text string) { a.vmwareLog("%s", text) }
		records := filepath.Join(a.vmwareRoot(), safeName(strings.ToLower(server)), "Move")
		a.mu.Lock()
		inventory := a.vmware.inventory
		a.mu.Unlock()
		result, err := a.runMoveRound(ctx, inventory, *vm, vmConnectOptions{Server: server, User: user, Password: password, Thumbprint: thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion},
			vmMoveRequest{VMID: vm.ID, Name: vm.Name, Datacenter: vm.Datacenter, Quiesce: input.Quiesce, Cutover: input.Action == "cutover", RecordFolder: records, NewSink: receiver.Link.sinkFactory(ctx, receiver.Mapping)}, log)
		password = ""
		finished := ""
		if err == nil && input.Action == "cutover" {
			switch {
			case haveCreated && platform != nil && created.Platform == a.vmTargetPlatformID():
				err = vmTargetCutover(ctx, platform, created.VM.ID, log)
				if err == nil {
					created.CutOver = time.Now().UTC()
					saveVMTargetRecord(a.vmTargetRecordsFolder(server), created)
					finished = vm.Name + " is cut over and starting on " + vmTargetLabel(created.Platform) + " from its own disk."
				} else {
					err = errors.New("the last changes are copied and the VMware VM is shut down, but switching the new VM to its disk failed: " + err.Error() + ". Turn the new VM off, remove the receiver media, set it to boot from its first disk and start it, or start the VMware VM again to go back")
				}
			case haveCreated:
				finished = "The last changes are copied. Connect to " + vmTargetLabel(created.Platform) + " again to switch the new VM to its disk, or turn it off, remove the receiver media and start it from its first disk."
			default:
				finished = "The last changes are copied and verified. On the new server: stop the receiver (Ctrl+C), remove the receiver media and restart from its own disk."
			}
		}
		a.mu.Lock()
		a.vmware.move = result
		a.vmware.moveSerial++
		if err != nil {
			a.vmware.moveError = err.Error()
		}
		if haveCreated && created.CutOver.After(time.Time{}) {
			if a.vmware.target.created == nil {
				a.vmware.target.created = map[string]vmTargetRecord{}
			}
			a.vmware.target.created[vm.ID] = created
		}
		if result != nil && result.ShutDown && a.vmware.inventory != nil {
			for index := range a.vmware.inventory.VMs {
				if a.vmware.inventory.VMs[index].ID == vm.ID {
					a.vmware.inventory.VMs[index].PowerState, a.vmware.inventory.VMs[index].ToolsRunning = "poweredOff", false
				}
			}
			a.vmware.inventorySerial++
		}
		a.vmware.target.serial++
		a.mu.Unlock()
		switch {
		case errors.Is(err, context.Canceled):
			a.vmwareLog("Stopped. The next copy resumes where this one stopped.")
			a.finishVMwareAction("connected", "Copying "+vm.Name+" was stopped.")
		case err != nil:
			a.vmwareLog("%s", err.Error())
			a.finishVMwareAction("connected", "Moving "+vm.Name+" stopped: "+err.Error())
		case input.Action == "cutover":
			a.vmwareLog("%s", finished)
			a.finishVMwareAction("connected", finished)
		default:
			a.finishVMwareAction("connected", "Copied "+vm.Name+"'s disks to the receiver. Copy again to bring them up to date, then cut over.")
		}
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) vmTargetPlatformID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.vmware.target.connection.Platform
}
