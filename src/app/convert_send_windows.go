//go:build windows

package main

// The Disk converter's "Send to a host" (vmconvert_send.go): connect to a
// destination, then convert, upload and create a VM there. The password or
// token secret stays in this process's memory (convertSendRuntime) until
// Forget or exit; it never reaches the page, logs, files or settings.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type convertSendRuntime struct {
	platform   string
	connection vmTargetConnection
	client     vmTargetPlatform
	// vsphere holds the vSphere login (Password stays here).
	vsphere     vmConnectOptions
	connected   bool
	version     string
	inventory   *vmTargetInventory
	vsphereList *vmVSphereChoices
	switches    []vmTargetChoice
	certificate *vmCertificateError
}

type convertSendState struct {
	Platforms   []vmTargetChoice    `json:"platforms"`
	Platform    string              `json:"platform"`
	Server      string              `json:"server"`
	User        string              `json:"user"`
	Node        string              `json:"node"`
	Connected   bool                `json:"connected"`
	Version     string              `json:"version"`
	Inventory   *vmTargetInventory  `json:"inventory,omitempty"`
	VSphere     *vmVSphereChoices   `json:"vsphere,omitempty"`
	Switches    []vmTargetChoice    `json:"switches,omitempty"`
	Certificate *vmCertificateError `json:"certificate,omitempty"`
	HyperVHere  bool                `json:"hyperVHere"`
	Stage       string              `json:"stage"`
	Done        int64               `json:"done"`
	Total       int64               `json:"total"`
	Result      *vmSendResult       `json:"result,omitempty"`
}

// Caller holds a.mu.
func (a *application) convertSendStateLocked() convertSendState {
	held := a.convertSend
	state := convertSendState{
		Platforms: vmConvertSendPlatforms, Platform: held.platform, Connected: held.connected, Version: held.version,
		Inventory: held.inventory, VSphere: held.vsphereList, Switches: held.switches, Certificate: held.certificate,
		HyperVHere: vmHyperVHere(), Stage: a.convert.sendStage, Done: a.convert.sendDone, Total: a.convert.sendTotal, Result: a.convert.sendResult,
	}
	if held.platform == vmTargetVSphere {
		state.Server, state.User = held.vsphere.Server, held.vsphere.User
	} else {
		state.Server, state.User, state.Node = held.connection.Server, held.connection.User, held.connection.Node
	}
	return state
}

func convertSendFail(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]any{"ok": false, "error": message})
}

// handleConvertConnect reads the destination's choices (read-only) and keeps
// the account for the send.
func (a *application) handleConvertConnect(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Platform   string `json:"platform"`
		Server     string `json:"server"`
		User       string `json:"user"`
		Password   string `json:"password"`
		Node       string `json:"node"`
		Thumbprint string `json:"thumbprint"`
		Datacenter string `json:"datacenter"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if !vmConvertSendKnown(input.Platform) {
		convertSendFail(response, http.StatusBadRequest, "Choose where to send the disk.")
		return
	}
	a.mu.Lock()
	busy := a.convertBusyLocked()
	held := a.convertSend
	a.mu.Unlock()
	if busy {
		convertSendFail(response, http.StatusConflict, "Wait for the Disk converter step to finish first.")
		return
	}
	server, user := strings.TrimSpace(input.Server), strings.TrimSpace(input.User)
	heldServer, heldUser, heldPassword, heldThumbprint := held.connection.Server, held.connection.User, held.connection.Password, held.connection.Thumbprint
	if held.platform == vmTargetVSphere {
		heldServer, heldUser, heldPassword, heldThumbprint = held.vsphere.Server, held.vsphere.User, held.vsphere.Password, held.vsphere.Thumbprint
	}
	sameServer := held.platform == input.Platform && strings.EqualFold(heldServer, server)
	if input.Password == "" && sameServer && heldUser == user {
		input.Password = heldPassword
	}
	if input.Thumbprint == "" && sameServer {
		input.Thumbprint = heldThumbprint
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	next := convertSendRuntime{platform: input.Platform}
	var err error
	switch input.Platform {
	case vmTargetHyperV:
		if !vmHyperVHere() {
			convertSendFail(response, http.StatusBadRequest, "This computer is not a Hyper-V host. Run the application on the Hyper-V host to send a disk there, or convert to VHDX and copy it.")
			return
		}
		next.switches, err = vmHyperVSwitches()
		next.version = "Hyper-V on this computer"
	case vmTargetVSphere:
		if server == "" || user == "" || input.Password == "" {
			convertSendFail(response, http.StatusBadRequest, "Type the vCenter or ESXi server, user name and password.")
			return
		}
		next.vsphere = vmConnectOptions{Server: server, User: user, Password: input.Password, Thumbprint: input.Thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
		var session *vmSession
		session, err = openVSphereSession(ctx, next.vsphere, nil)
		if err == nil {
			next.vsphereList, err = vmVSphereReadChoices(ctx, session.client, strings.TrimSpace(input.Datacenter))
			session.logout()
			if next.vsphereList != nil {
				next.version = next.vsphereList.Product
			}
		}
	default:
		if server == "" || user == "" || input.Password == "" {
			convertSendFail(response, http.StatusBadRequest, "Type the server, user name (or API token ID) and password (or token secret).")
			return
		}
		next.connection = vmTargetConnection{Platform: input.Platform, Server: server, User: user, Password: input.Password, Node: strings.TrimSpace(input.Node), Thumbprint: input.Thumbprint}
		next.client, err = newVMTargetPlatform(next.connection)
		if err == nil {
			next.inventory, err = next.client.Inventory(ctx)
			if err == nil {
				next.version = next.inventory.Version
				if next.connection.Platform == vmTargetProxmox && next.connection.Node == "" && len(next.inventory.Nodes) > 0 {
					next.connection.Node = next.inventory.Nodes[0].ID
					next.client, err = newVMTargetPlatform(next.connection)
				}
			}
		}
	}
	input.Password = ""
	var certificate *vmCertificateError
	if errors.As(err, &certificate) {
		a.mu.Lock()
		a.convertSend = convertSendRuntime{platform: input.Platform, certificate: certificate}
		a.convertSend.connection.Server, a.convertSend.vsphere.Server = server, server
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": certificate.Error(), "certificate": certificate})
		return
	}
	if err != nil {
		convertSendFail(response, http.StatusBadGateway, err.Error())
		return
	}
	next.connected = true
	a.mu.Lock()
	a.convertSend = next
	state := a.convertSendStateLocked()
	a.appendConvertLogLocked(time.Now().Format("2006-01-02 15:04:05") + " Connected to " + vmNonEmpty(state.Server, "this computer") + " (" + next.version + ").")
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "send": state})
}

func (a *application) handleConvertForget(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if !decodeVMwareRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.convertBusyLocked() && a.convert.task == "send" {
		convertSendFail(response, http.StatusConflict, "Stop the send first.")
		return
	}
	a.convertSend = convertSendRuntime{}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

// vmHyperVSwitches lists this computer's Hyper-V virtual switches.
func vmHyperVSwitches() ([]vmTargetChoice, error) {
	out, err := vmRunPowerShell("Import-Module Hyper-V; Get-VMSwitch | ForEach-Object { [Console]::Out.WriteLine($_.Name + [char]9 + [string]$_.SwitchType) }")
	if err != nil {
		return nil, err
	}
	switches := []vmTargetChoice{}
	for _, line := range strings.Split(out, "\n") {
		name, kind, _ := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if strings.TrimSpace(name) != "" {
			switches = append(switches, vmTargetChoice{ID: name, Name: name, Detail: kind})
		}
	}
	return switches, nil
}

// handleConvertSend converts, uploads and creates the VM. The VM name must
// be typed again as the confirmation.
func (a *application) handleConvertSend(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Source  string     `json:"source"`
		Confirm string     `json:"confirm"`
		Spec    vmSendSpec `json:"spec"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	spec := input.Spec
	spec.Name = strings.TrimSpace(spec.Name)
	source := strings.TrimSpace(input.Source)
	switch {
	case source == "" || !filepath.IsAbs(source):
		convertSendFail(response, http.StatusBadRequest, "Choose the disk image to send.")
		return
	case spec.Name == "" || strings.TrimSpace(input.Confirm) != spec.Name:
		convertSendFail(response, http.StatusBadRequest, "Type the new VM's name again to confirm.")
		return
	}
	a.mu.Lock()
	held := a.convertSend
	a.mu.Unlock()
	if !held.connected || held.platform != spec.Platform {
		convertSendFail(response, http.StatusBadRequest, "Connect to the destination first.")
		return
	}
	if held.platform == vmTargetHyperV && !vmHyperVHere() {
		convertSendFail(response, http.StatusBadRequest, "This computer is not a Hyper-V host.")
		return
	}
	if err := vmSendCheck(&spec); err != nil {
		convertSendFail(response, http.StatusBadRequest, strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
		return
	}
	if err := vmCheckLocalTarget(source, filepath.Join(spec.Folder, "x")); err != nil {
		convertSendFail(response, http.StatusBadRequest, err.Error())
		return
	}
	image, err := openConvertSource(source, a.convertLog)
	if err != nil {
		convertSendFail(response, http.StatusBadRequest, err.Error())
		return
	}
	if status, message := a.beginConvertTask("send", spec.Platform); status != 0 {
		image.Close()
		convertSendFail(response, status, message)
		return
	}
	info := image.Info()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.convert.cancel, a.convert.source, a.convert.info = cancel, info.Path, &info
	a.convert.message = "Sending " + filepath.Base(info.Path) + " to " + vmSendWhere(spec.Platform) + " as " + spec.Name + "."
	a.convert.sendStage = "Starting"
	a.appendConvertLogLocked(time.Now().Format("2006-01-02 15:04:05") + " " + a.convert.message)
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})

	go func() {
		defer a.endCriticalOperation()
		defer image.Close()
		defer cancel()
		hooks := vmSendHooks{
			Step: func(stage string, done, total int64) {
				a.mu.Lock()
				a.convert.sendStage, a.convert.sendDone, a.convert.sendTotal = stage, done, total
				a.mu.Unlock()
			},
			Log: a.convertLog,
		}
		var result *vmSendResult
		var err error
		switch held.platform {
		case vmTargetHyperV:
			result, err = vmSendToHyperV(ctx, image, vmCreateHyperVVM, spec, hooks)
		case vmTargetVSphere:
			options := held.vsphere
			result, err = vmSendToVSphere(ctx, image, func(ctx context.Context, importSpec vmVSphereImport, path string, progress func(string)) (string, error) {
				// A new login: the VMDK may have taken hours to write.
				session, err := openVSphereSession(ctx, options, progress)
				if err != nil {
					return "", err
				}
				defer session.logout()
				return vmVSphereImportDisk(ctx, session.client, importSpec, path, progress)
			}, spec, hooks)
		default:
			result, err = vmSendToPlatform(ctx, image, held.client, spec, hooks)
		}
		a.mu.Lock()
		a.convert.sendResult = result
		a.mu.Unlock()
		switch {
		case err == nil:
			a.finishConvertTask("success", vmSendDoneMessage(result), nil)
		case errors.Is(err, context.Canceled):
			a.finishConvertTask("canceled", "Stopped before the VM was created. If the upload had started, delete the partly uploaded image on the destination before sending again; the conversion itself continues where it stopped.", nil)
		default:
			a.finishConvertTask("failed", err.Error(), nil)
		}
	}()
}

func vmSendWhere(platform string) string {
	for _, item := range vmConvertSendPlatforms {
		if item.ID == platform {
			return item.Name
		}
	}
	return platform
}
