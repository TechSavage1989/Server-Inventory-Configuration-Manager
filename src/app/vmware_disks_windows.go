//go:build windows

package main

import (
	"context"
	"encoding/json"
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

const (
	vmReceiverFirewallRule = "Server Inventory and Migration Manager disk receiver"
	// vmDiscoveryFirewallRule lets senders on the network find the
	// receiver (UDP find requests). Added and removed with the TCP rule.
	vmDiscoveryFirewallRule = "Server Inventory and Migration Manager disk receiver discovery"
)

// vmReceiverRuntime is this computer's receiver, if one is running.
type vmReceiverRuntime struct {
	receiver *vmReceiver
	firewall bool
	port     int
	started  time.Time
	message  string
	// last keeps the stopped receiver's transfers so the panel still shows
	// what was received.
	last []vmTransfer
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
	// FreeBytes is the free space where the receiver writes (or would).
	FreeBytes uint64 `json:"freeBytes"`
	// Transfers are the copies being received (with each sender's address)
	// and those finished in the last hour.
	Transfers []vmTransfer `json:"transfers"`
	// Fingerprint is the short thumbprint senders compare by eye.
	Fingerprint  string          `json:"fingerprint,omitempty"`
	Discoverable bool            `json:"discoverable"`
	PairRequests []vmPairRequest `json:"pairRequests"`
}

// Caller holds a.mu.
func (a *application) vmReceiverStateLocked() vmReceiverState {
	state := vmReceiverState{Port: vmReceiverDefaultPort, Message: a.vmReceive.message, DefaultDir: filepath.Join(a.vmwareCopyRootLocked(), "Received")}
	if r := a.vmReceive.receiver; r != nil {
		state.Running, state.Port, state.Code, state.Thumbprint, state.Folder = true, r.Port(), r.Code(), r.Thumbprint(), r.Folder()
		state.Addresses, state.Sessions, state.Firewall = vmLocalAddresses(), r.ActiveSessions(), a.vmReceive.firewall
		state.Transfers = r.Transfers()
		state.Fingerprint, state.Discoverable, state.PairRequests = vmFingerprint(r.Thumbprint()), r.discovery != nil, r.PairRequests()
	} else {
		state.Transfers = a.vmReceive.last
	}
	if state.Transfers == nil {
		state.Transfers = []vmTransfer{}
	}
	if state.PairRequests == nil {
		state.PairRequests = []vmPairRequest{}
	}
	where := state.Folder
	if where == "" {
		where = state.DefaultDir
	}
	if free, _, ok := vmFreeSpace(where); ok {
		state.FreeBytes = free
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

// vmRemoveReceiverFirewallRule removes this application's own two rules,
// by their names, and nothing else.
func vmRemoveReceiverFirewallRule() error {
	err := vmFirewall("delete", "rule", "name="+vmReceiverFirewallRule)
	_ = vmFirewall("delete", "rule", "name="+vmDiscoveryFirewallRule) // absent when discovery was off
	return err
}

func vmAddReceiverFirewallRule(port int, discovery bool) error {
	_ = vmRemoveReceiverFirewallRule()
	if err := vmFirewall("add", "rule", "name="+vmReceiverFirewallRule, "dir=in", "action=allow", "protocol=TCP", "localport="+strconv.Itoa(port), "profile=any"); err != nil {
		return err
	}
	if discovery {
		if err := vmFirewall("add", "rule", "name="+vmDiscoveryFirewallRule, "dir=in", "action=allow", "protocol=UDP", "localport="+strconv.Itoa(vmDiscoveryPort), "profile=any"); err != nil {
			_ = vmRemoveReceiverFirewallRule()
			return err
		}
	}
	return nil
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
	transfers := r.Transfers()
	a.mu.Lock()
	if a.vmReceive.receiver == nil {
		a.vmReceive.last = transfers
	}
	a.mu.Unlock()
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
		// Discoverable answers find requests from senders on the network;
		// nil (older pages) means on.
		Discoverable *bool `json:"discoverable"`
		// NewIdentity makes a new pairing code and certificate instead of
		// the ones kept from the last start; senders then need the new ones.
		NewIdentity bool `json:"newIdentity"`
		// PID names the other copy of the application to close
		// (close-other).
		PID uint32 `json:"pid"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if input.Action == "close-other" {
		port, err := vmReceiverPort(input.Port)
		if err == nil {
			err = vmCloseOtherInstance(port, input.PID)
		}
		if err != nil {
			writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The other copy of the application was not closed: " + err.Error() + "."})
			return
		}
		a.vmwareLog("Closed the other copy of this application (process %d) that held port %d.", input.PID, port)
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if input.Action == "stop" {
		_ = os.Remove(a.vmReceiverStartPath())
		a.stopVMReceiver("Stopped. Start it again to receive more; senders resume where they stopped.")
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if input.Action != "start" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose start or stop."})
		return
	}
	discoverable := input.Discoverable == nil || *input.Discoverable
	if status, reason := a.startVMReceiverFromWindow(vmReceiverStart{Folder: input.Folder, Port: input.Port, Firewall: input.Firewall, Discoverable: discoverable, NewIdentity: input.NewIdentity}); status != 0 {
		answer := map[string]any{"ok": false, "error": reason}
		if port, err := vmReceiverPort(input.Port); err == nil {
			if other, found := vmOtherInstanceOn(port); found {
				// Most often a copy whose window was closed while it was
				// busy: say so and offer to close it.
				answer["error"] = fmt.Sprintf("Port %d is held by another copy of this application (process %d, started %s) that is still running, perhaps without a window. Close it, then start the receiver.", port, other.PID, other.Started.Local().Format("15:04"))
				answer["otherInstance"] = other
			}
		}
		writeJSON(response, status, answer)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

// vmReceiverStart is how the receiver was last started from the window. It
// is saved (no code, no key) so the receiver starts again by itself when the
// application opens, and removed when the operator stops it.
type vmReceiverStart struct {
	Folder       string `json:"folder"`
	Port         int    `json:"port"`
	Firewall     bool   `json:"firewall"`
	Discoverable bool   `json:"discoverable"`
	NewIdentity  bool   `json:"-"`
}

// vmReceiverLogPath is the receiver's log file (no pairing code is ever
// written to it).
func (a *application) vmReceiverLogPath() string {
	return filepath.Join(a.vmwareRoot(), "Receiver", "receiver.log")
}

func (a *application) vmReceiverStartPath() string {
	return filepath.Join(a.vmwareRoot(), "Receiver", "autostart.json")
}

// startVMReceiverFromWindow starts this computer's receiver. It returns 0
// on success, or an HTTP status and the reason.
func (a *application) startVMReceiverFromWindow(options vmReceiverStart) (int, string) {
	port, err := vmReceiverPort(options.Port)
	if err != nil {
		return http.StatusBadRequest, "The port was not accepted: " + err.Error() + "."
	}
	a.mu.Lock()
	running, closing := a.vmReceive.receiver != nil, a.closing
	folder := strings.TrimSpace(options.Folder)
	if folder == "" {
		folder = filepath.Join(a.vmwareCopyRootLocked(), "Received")
	}
	a.mu.Unlock()
	switch {
	case closing:
		return http.StatusConflict, "The application is closing."
	case running:
		return http.StatusConflict, "The receiver is already running. Stop it first to change its folder or port."
	case !filepath.IsAbs(folder) || (migrateProtectedFolder(folder) != "" && !vmHostDefaultAllowed(folder)):
		return http.StatusBadRequest, "Choose a full path for received disks outside Windows, Program Files and profile folders."
	case options.Firewall && !isAdministrator():
		return http.StatusBadRequest, "Opening the firewall port needs the application to run as administrator. Run it as administrator, or open the port yourself and untick that option."
	}
	discoverable := options.Discoverable
	config := vmReceiverConfig{Folder: folder, Pairing: true, Version: appVersion, FreeBytes: func() uint64 {
		free, _, _ := vmFreeSpace(folder)
		return free
	}}
	if discoverable {
		config.Discovery = ":" + strconv.Itoa(vmDiscoveryPort)
	}
	hostName, _ := os.Hostname()
	identity, kept, saveErr, err := vmReceiverIdentityFor(filepath.Join(a.vmwareRoot(), "Receiver", "identity.json"), hostName, options.NewIdentity, replSeal, replUnseal)
	if err != nil {
		return http.StatusBadRequest, "The receiver could not start: " + err.Error()
	}
	config.Identity = identity
	switch {
	case kept:
		a.vmwareLog("The receiver uses the same pairing code and certificate as before, so senders and replication jobs carry on.")
	case saveErr != nil:
		a.vmwareLog("A new pairing code and certificate were made but could not be saved (%v): they last until the receiver stops.", saveErr)
	default:
		a.vmwareLog("A new pairing code and certificate were made and kept for later starts. Senders and replication jobs need the new ones.")
	}
	logPath := a.vmReceiverLogPath()
	vmAppendLogLine(logPath, fmt.Sprintf("Receiver starting on port %d, folder %s.", port, folder))
	r, err := startVMReceiverWith(":"+strconv.Itoa(port), config, func(text string) {
		a.vmwareLog("%s", text)
		vmAppendLogLine(logPath, text)
	})
	if err != nil {
		return http.StatusBadRequest, "The receiver could not start: " + err.Error()
	}
	// Copies a VM was started from stay held, before the firewall opens.
	a.replApplyFailoverHolds(r)
	firewall := false
	if options.Firewall {
		if err := vmAddReceiverFirewallRule(port, r.discovery != nil); err != nil {
			r.Stop()
			return http.StatusBadRequest, "The firewall port could not be opened, so the receiver was stopped: " + err.Error()
		}
		firewall = true
		a.vmwareLog("Allowed TCP port %d in Windows Firewall (rule %q, removed when the receiver stops).", port, vmReceiverFirewallRule)
		if r.discovery != nil {
			a.vmwareLog("Allowed UDP port %d for finding this receiver (rule %q, removed when the receiver stops).", vmDiscoveryPort, vmDiscoveryFirewallRule)
		}
	}
	a.mu.Lock()
	if a.vmReceive.receiver != nil || a.closing {
		a.mu.Unlock()
		r.Stop()
		if firewall {
			vmRemoveReceiverFirewallRule()
		}
		return http.StatusConflict, "The receiver is already running."
	}
	a.vmReceive = vmReceiverRuntime{receiver: r, firewall: firewall, port: port, started: time.Now()}
	a.mu.Unlock()
	if data, err := json.Marshal(vmReceiverStart{Folder: folder, Port: port, Firewall: firewall, Discoverable: discoverable}); err == nil {
		path := a.vmReceiverStartPath()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err == nil {
			_ = os.WriteFile(path, data, 0600)
		}
	}
	return 0, ""
}

// resumeVMReceiver starts the receiver again when the application opens, if
// it was receiving when the application (or the computer) last closed, with
// the same code and certificate, so senders and replication jobs carry on.
func (a *application) resumeVMReceiver() {
	data, err := os.ReadFile(a.vmReceiverStartPath())
	if err != nil {
		return
	}
	var options vmReceiverStart
	if json.Unmarshal(data, &options) != nil {
		return
	}
	if options.Firewall && !isAdministrator() {
		options.Firewall = false
		a.vmwareLog("The receiver starts again without opening the firewall port: run the application as administrator for that.")
	}
	if status, reason := a.startVMReceiverFromWindow(options); status != 0 {
		a.vmwareLog("The receiver that was running when the application closed could not start again: %s", reason)
		return
	}
	a.vmwareLog("The receiver started again as it was when the application closed, with the same code and certificate.")
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
	if vmNormalizeThumbprint(input.Thumbprint) == "" {
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
	board := a.copyTransfers()
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
				id := fmt.Sprintf("send-%d", disk)
				board.Begin(vmTransfer{ID: id, Kind: "send", Label: fmt.Sprintf("Disk %d", disk), Source: fmt.Sprintf("%s, disk %d", host, disk), Target: result.Name, Peer: address, Size: source.Size()})
				copied, err := vmCopyTo(ctx, source, sink, vmCopyOptions{Verify: input.Verify, Progress: func(p vmCopyProgress) {
					board.Progress(id, p)
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
				board.End(id, err, errors.Is(err, context.Canceled), result.Verified)
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

// vmRunPowerShell runs a short script for the console's own steps (Hyper-V,
// failover). The replication agent never calls it (see
// TestAgentCodeStartsNoPowerShellAndDeletesNoShadowCopies).
func vmRunPowerShell(script string) (string, error) {
	command := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "$ErrorActionPreference='Stop'; "+script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := command.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, text)
	}
	return text, nil
}
