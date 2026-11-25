//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const vmReceiverFirewallRule = "Server Inventory and Migration Manager disk receiver"

// vmReceiverRuntime is this computer's receiver, if one is running.
type vmReceiverRuntime struct {
	receiver *vmReceiver
	firewall bool
	port     int
	started  time.Time
	message  string
}

type vmReceiverState struct {
	Running    bool     `json:"running"`
	Port       int      `json:"port"`
	Code       string   `json:"code,omitempty"`
	Thumbprint string   `json:"thumbprint,omitempty"`
	Folder     string   `json:"folder,omitempty"`
	Addresses  []string `json:"addresses,omitempty"`
	Sessions   int      `json:"sessions"`
	Firewall   bool     `json:"firewall"`
	Message    string   `json:"message,omitempty"`
	DefaultDir string   `json:"defaultFolder"`
}

// Caller holds a.mu.
func (a *application) vmReceiverStateLocked() vmReceiverState {
	state := vmReceiverState{Port: vmReceiverDefaultPort, Message: a.vmReceive.message, DefaultDir: filepath.Join(a.vmwareCopyRootLocked(), "Received")}
	if r := a.vmReceive.receiver; r != nil {
		state.Running, state.Port, state.Code, state.Thumbprint, state.Folder = true, r.Port(), r.Code(), r.Thumbprint(), r.Folder()
		state.Addresses, state.Sessions, state.Firewall = vmLocalAddresses(), r.ActiveSessions(), a.vmReceive.firewall
	}
	return state
}

func vmFirewall(args ...string) error {
	command := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "netsh.exe"), append([]string{"advfirewall", "firewall"}, args...)...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func vmRemoveReceiverFirewallRule() error {
	return vmFirewall("delete", "rule", "name="+vmReceiverFirewallRule)
}

func vmAddReceiverFirewallRule(port int) error {
	_ = vmRemoveReceiverFirewallRule() // only this application's own rule, by its name
	return vmFirewall("add", "rule", "name="+vmReceiverFirewallRule, "dir=in", "action=allow", "protocol=TCP", "localport="+strconv.Itoa(port), "profile=any")
}

// stopVMReceiver stops this computer's receiver and removes the firewall
// rule it added. Senders keep their copy records and resume against a new
// receiver.
func (a *application) stopVMReceiver(message string) {
	a.mu.Lock()
	r, firewall := a.vmReceive.receiver, a.vmReceive.firewall
	a.vmReceive.receiver, a.vmReceive.firewall = nil, false
	a.vmReceive.message = message
	a.mu.Unlock()
	if r == nil {
		return
	}
	r.Stop()
	if firewall {
		if err := vmRemoveReceiverFirewallRule(); err != nil {
			a.vmwareLog("The firewall rule %q could not be removed: %v", vmReceiverFirewallRule, err)
		}
	}
	a.vmwareLog("The disk receiver stopped.")
}

// handleVMwareReceiver starts or stops this computer's disk receiver.
func (a *application) handleVMwareReceiver(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Action   string `json:"action"`
		Folder   string `json:"folder"`
		Port     int    `json:"port"`
		Firewall bool   `json:"firewall"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if input.Action == "stop" {
		a.stopVMReceiver("Stopped. Start it again to receive more; senders resume where they stopped.")
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if input.Action != "start" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose start or stop."})
		return
	}
	port, err := vmReceiverPort(input.Port)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The port was not accepted: " + err.Error() + "."})
		return
	}
	a.mu.Lock()
	running, closing := a.vmReceive.receiver != nil, a.closing
	folder := strings.TrimSpace(input.Folder)
	if folder == "" {
		folder = filepath.Join(a.vmwareCopyRootLocked(), "Received")
	}
	a.mu.Unlock()
	switch {
	case closing:
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The application is closing."})
		return
	case running:
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The receiver is already running. Stop it first to change its folder or port."})
		return
	case !filepath.IsAbs(folder) || migrateProtectedFolder(folder) != "":
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose a full path for received disks outside Windows, Program Files and profile folders."})
		return
	case input.Firewall && !isAdministrator():
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Opening the firewall port needs the application to run as administrator. Run it as administrator, or open the port yourself and untick that option."})
		return
	}
	r, err := startVMReceiver(":"+strconv.Itoa(port), folder, func(text string) { a.vmwareLog("%s", text) })
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The receiver could not start: " + err.Error()})
		return
	}
	firewall := false
	if input.Firewall {
		if err := vmAddReceiverFirewallRule(port); err != nil {
			r.Stop()
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The firewall port could not be opened, so the receiver was stopped: " + err.Error()})
			return
		}
		firewall = true
		a.vmwareLog("Allowed TCP port %d in Windows Firewall (rule %q, removed when the receiver stops).", port, vmReceiverFirewallRule)
	}
	a.mu.Lock()
	if a.vmReceive.receiver != nil || a.closing {
		a.mu.Unlock()
		r.Stop()
		if firewall {
			vmRemoveReceiverFirewallRule()
		}
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The receiver is already running."})
		return
	}
	a.vmReceive = vmReceiverRuntime{receiver: r, firewall: firewall, port: port, started: time.Now()}
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

// vmAgentDiskInfo describes one disk of this computer for the Disk copy view.
type vmAgentDiskInfo struct {
	Number  int             `json:"number"`
	Size    int64           `json:"size"`
	System  bool            `json:"system"`
	Volumes []vmAgentVolume `json:"volumes"`
}

// vmListAgentDisks lists the disks of this computer.
func vmListAgentDisks() ([]vmAgentDiskInfo, error) {
	systemDrive := strings.ToUpper(strings.TrimSpace(os.Getenv("SystemDrive")))
	disks := []vmAgentDiskInfo{}
	misses := 0
	for number := 0; number < 64 && misses < 8; number++ {
		device, err := vmOpenDevice(fmt.Sprintf(`\\.\PhysicalDrive%d`, number))
		if err != nil {
			if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return nil, errors.New("reading this computer's disks needs the application to run as administrator")
			}
			misses++
			continue
		}
		size, err := vmDeviceLength(device)
		device.Close()
		if err != nil || size <= 0 {
			continue
		}
		info := vmAgentDiskInfo{Number: number, Size: size}
		volumes, err := vmDiskVolumes(number)
		if err != nil {
			return nil, err
		}
		for _, volume := range volumes {
			item := vmAgentVolume{Name: volume.Name, Mounts: volume.Mounts, FileSystem: volume.FileSystem, Start: volume.Start, Length: volume.Length, Kind: "simple"}
			if volume.Layout != nil {
				item.Kind = volume.Layout.Kind
			}
			info.Volumes = append(info.Volumes, item)
			for _, mount := range volume.Mounts {
				if systemDrive != "" && strings.EqualFold(strings.TrimRight(mount, `\`), systemDrive) {
					info.System = true
				}
			}
		}
		disks = append(disks, info)
	}
	return disks, nil
}

func (a *application) handleVMwareAgentDisks(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	disks, err := vmListAgentDisks()
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The disks could not be listed: " + err.Error() + "."})
		return
	}
	host, _ := os.Hostname()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "computer": host, "disks": disks})
}

// handleVMwareAgentSend sends disks of this computer to a receiver. Each
// volume is read from a new shadow copy, deleted when its disk is done.
func (a *application) handleVMwareAgentSend(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Disks      []int  `json:"disks"`
		Address    string `json:"address"`
		Thumbprint string `json:"thumbprint"`
		Code       string `json:"code"`
		AllowLive  bool   `json:"allowLive"`
		Verify     bool   `json:"verify"`
		// Targets maps a source disk number to a raw receiver's disk-N;
		// a disk not listed is sent as a VHDX file.
		Targets   map[string]string `json:"targets"`
		Compress  bool              `json:"compress"`
		LimitMbps int               `json:"limitMbps"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	fail := func(message string) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": message})
	}
	address, err := vmReceiverAddress(input.Address)
	if err != nil {
		fail("The receiver was not accepted: " + err.Error() + ".")
		return
	}
	if len(vmNormalizeThumbprint(input.Thumbprint)) != 64 {
		fail("Enter the receiver's certificate thumbprint exactly as the receiving computer shows it (64 characters).")
		return
	}
	if len(vmNormalizePairingCode(input.Code)) != 20 {
		fail("Enter the 20-character pairing code the receiving computer shows.")
		return
	}
	seen := map[int]bool{}
	disks := []int{}
	for _, disk := range input.Disks {
		if disk < 0 || disk > 63 || seen[disk] {
			fail("Choose the disks from the list.")
			return
		}
		seen[disk] = true
		disks = append(disks, disk)
	}
	if len(disks) == 0 {
		fail("Choose at least one disk to send.")
		return
	}
	names, err := vmAgentTargetNames(disks, input.Targets, func(disk int) string { host, _ := os.Hostname(); return vmAgentDiskName(host, disk) })
	if err != nil {
		fail(err.Error())
		return
	}
	schedule := vmThrottleSchedule{LimitMbps: input.LimitMbps}
	if err := schedule.Validate(); err != nil {
		fail("The bandwidth limit was not accepted: " + err.Error() + ".")
		return
	}
	throttle := newVMThrottle(schedule)
	if !isAdministrator() {
		fail("Sending this computer's disks needs the application to run as administrator.")
		return
	}
	a.mu.Lock()
	receiving := a.vmReceive.receiver != nil
	a.mu.Unlock()
	if receiving {
		fail("This computer is receiving disks. Stop the receiver before sending its own disks.")
		return
	}
	if status, message := a.startVMwareAction("send"); status != 0 {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	host, _ := os.Hostname()
	a.mu.Lock()
	a.vmware.cancel = cancel
	a.vmware.status = "sending"
	a.vmware.message = fmt.Sprintf("Sending %s to %s.", vmPlural(len(disks), "disk", "disks"), address)
	a.vmware.sendError = ""
	a.mu.Unlock()
	a.vmwareLog("Sending %s of %s to %s.", vmPlural(len(disks), "disk", "disks"), host, address)
	thumbprint, code := input.Thumbprint, input.Code
	go func() {
		defer cancel()
		log := func(text string) { a.vmwareLog("%s", text) }
		results := []vmAgentSendResult{}
		var failure error
		// Every disk is opened at once so a volume that spans disks is read
		// from one shadow copy.
		set, openErr := openVMAgentDisks(disks, input.AllowLive, log)
		if set != nil {
			defer set.Close()
		}
		for _, disk := range disks {
			result := vmAgentSendResult{Disk: disk, Name: names[disk]}
			err := func() error {
				if openErr != nil {
					return openErr
				}
				source, volumes := set.Sources[disk], set.Volumes[disk]
				for _, volume := range volumes {
					if volume.Shadowed {
						result.Shadowed++
					}
				}
				sink, err := newVMRemoteSink(ctx, address, thumbprint, code, result.Name)
				if err != nil {
					return err
				}
				sink.Compress, sink.Throttle = input.Compress, throttle
				last := -1
				copied, err := vmCopyTo(ctx, source, sink, vmCopyOptions{Verify: input.Verify, Progress: func(p vmCopyProgress) {
					if p.Chunks > 0 {
						if percent := int(p.Done * 100 / p.Chunks); percent/10 != last/10 {
							last = percent
							log(fmt.Sprintf("Disk %d: %d%% (%s read, %s sent).", disk, percent, vmFormatBytes(p.BytesRead), vmFormatBytes(p.BytesWritten)))
						}
					}
				}})
				if copied != nil {
					result.BytesRead, result.BytesWritten, result.Resumed = copied.Progress.BytesRead, copied.Progress.BytesWritten, copied.Resumed
					result.Verified, result.Passes = copied.State.Verified, copied.State.Passes
					result.BytesSent = copied.Progress.BytesSent
				}
				return err
			}()
			if err != nil {
				result.Error = err.Error()
				failure = err
			} else {
				log(fmt.Sprintf("Disk %d sent as %s: %s read, %s sent%s.", disk, result.Name, vmFormatBytes(result.BytesRead), vmFormatBytes(result.BytesWritten), map[bool]string{true: ", verified", false: ""}[result.Verified]))
			}
			results = append(results, result)
			if failure != nil {
				break
			}
		}
		a.mu.Lock()
		a.vmware.send = results
		a.vmware.sendSerial++
		if failure != nil {
			a.vmware.sendError = failure.Error()
		}
		a.mu.Unlock()
		switch {
		case errors.Is(failure, context.Canceled):
			a.vmwareLog("Stopped. The next send resumes where this one stopped.")
			a.finishVMwareAction(a.vmwareIdleStatus(), "Sending was stopped.")
		case failure != nil:
			a.vmwareLog("%s", failure.Error())
			a.finishVMwareAction(a.vmwareIdleStatus(), "Sending stopped: "+failure.Error())
		default:
			a.finishVMwareAction(a.vmwareIdleStatus(), fmt.Sprintf("Sent %s to %s. Send again to bring the copies up to date.", vmPlural(len(disks), "disk", "disks"), address))
		}
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

// handleVMwareAgentReceiverDisks asks a receiver what it writes: VHDX files
// in a folder, and/or the raw disks of the new VM or server it runs on.
func (a *application) handleVMwareAgentReceiverDisks(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Address    string `json:"address"`
		Thumbprint string `json:"thumbprint"`
		Code       string `json:"code"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	address, err := vmReceiverAddress(input.Address)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The receiver was not accepted: " + err.Error() + "."})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	folder, disks, err := vmReceiverDescribe(ctx, address, input.Thumbprint, input.Code)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The receiver could not be reached: " + err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "folder": folder, "disks": disks})
}
