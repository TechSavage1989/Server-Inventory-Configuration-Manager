package main

// Automatic fix-up after a cutover (no-touch migration). Move VMs records
// the guest's network settings from vCenter before every copy
// (saveVCenterCapture), so nothing is installed in the old VM. Once the new
// Hyper-V VM has started, the console reaches it through PowerShell Direct
// (on this host, or through the connected remote host or cluster node) with
// the guest administrator account typed once, and runs the guest engine's
// AutoFixup mode: hidden VMware adapters removed, the recorded static IP
// settings put on the new adapters (matched by the MAC addresses the host
// gave them), offline data disks online, VMware Tools removed, Windows asked
// to activate. A failed network step leaves the manual Prepare and fix up
// buttons, with the reason. These rules are untagged so they are tested on
// any platform; guest_autofix_windows.go runs them.

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// guestAutoFixWait is how long the console waits for Windows in a newly
// started VM to answer PowerShell Direct. The first start on new hardware
// installs devices and can restart once.
const guestAutoFixWait = 15 * time.Minute

// Fix-up states shown per VM.
const (
	guestFixWaiting = "waiting" // the VM is starting
	guestFixRunning = "running"
	guestFixDone    = "done"
	guestFixWarning = "warning" // done, with steps to check
	guestFixFailed  = "failed"  // the manual buttons are the way on
	guestFixSkipped = "skipped" // not run (no account, VM not started, ...)
)

// guestAutoFixOptions are the steps the fix-up runs; all are on by default.
type guestAutoFixOptions struct {
	Network  bool `json:"network"`
	Ghosts   bool `json:"ghosts"`
	Disks    bool `json:"disks"`
	Tools    bool `json:"tools"`
	Activate bool `json:"activate"`
	Restart  bool `json:"restart"`
}

func guestAutoFixDefaults() guestAutoFixOptions {
	return guestAutoFixOptions{Network: true, Ghosts: true, Disks: true, Tools: true, Activate: true, Restart: true}
}

// guestAutoFixAdapter pairs a recorded adapter (its MAC inside the old guest)
// with the Hyper-V adapter made for it; the host fills in that adapter's MAC
// once the VM has started.
type guestAutoFixAdapter struct {
	Capture     string `json:"capture"`
	HostAdapter string `json:"hostAdapter"`
}

// guestAutoFixPlan builds the plan file the guest engine reads. Hyper-V
// adapters are created in the VMware adapter order and named "Network
// Adapter N" (vmHyperVPlan.Script), so the Nth VMware adapter's guest MAC
// belongs to "Network Adapter N".
func guestAutoFixPlan(vm vmMachine, options guestAutoFixOptions) ([]byte, error) {
	adapters := []guestAutoFixAdapter{}
	for index, nic := range vm.NICs {
		mac := guestMACFromVMware(vmNonEmpty(nic.MACInGuest, nic.MAC))
		if mac == "" {
			continue
		}
		adapters = append(adapters, guestAutoFixAdapter{Capture: mac, HostAdapter: "Network Adapter " + strconv.Itoa(index+1)})
	}
	return json.Marshal(map[string]any{"auto": options, "adapters": adapters})
}

// guestAutoFixStep is one line of the engine's report.
type guestAutoFixStep struct {
	Step    string `json:"step"`
	Status  string `json:"status"` // done, skipped, warning, failed
	Message string `json:"message"`
}

type guestAutoFixReport struct {
	Computer   string             `json:"Computer"`
	Platform   string             `json:"Platform"`
	NetworkOk  bool               `json:"NetworkOk"`
	Restarting bool               `json:"Restarting"`
	Steps      []guestAutoFixStep `json:"Steps"`
}

// guestAutoFixParse reads the engine's AutoFixup result and says what state
// the VM's fix-up ended in.
func guestAutoFixParse(raw json.RawMessage) (*guestAutoFixReport, string, error) {
	var report guestAutoFixReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, guestFixFailed, errors.New("the fix-up result could not be read")
	}
	state := guestFixDone
	for _, step := range report.Steps {
		switch step.Status {
		case "failed":
			state = guestFixFailed
		case "warning":
			if state == guestFixDone {
				state = guestFixWarning
			}
		}
	}
	if !report.NetworkOk {
		state = guestFixFailed
	}
	return &report, state, nil
}

// guestFixStatus is what the page shows for one VM.
type guestFixStatus struct {
	VM       string             `json:"vm"`
	HyperVID string             `json:"hyperVId,omitempty"`
	Host     string             `json:"host,omitempty"`
	Via      string             `json:"via"`
	State    string             `json:"state"`
	Message  string             `json:"message"`
	Steps    []guestAutoFixStep `json:"steps,omitempty"`
	Started  time.Time          `json:"started"`
	Finished time.Time          `json:"finished,omitempty"`
}

// guestFixBoard keeps the latest fix-up of each VM for the page.
type guestFixBoard struct {
	mu    sync.Mutex
	items map[string]*guestFixStatus
}

func (b *guestFixBoard) key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func (b *guestFixBoard) Set(status guestFixStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.items == nil {
		b.items = map[string]*guestFixStatus{}
	}
	copied := status
	copied.Steps = append([]guestAutoFixStep(nil), status.Steps...)
	b.items[b.key(status.VM)] = &copied
	// Keep the board small: the newest 200 VMs.
	if len(b.items) > 200 {
		oldest, when := "", time.Time{}
		for key, item := range b.items {
			if oldest == "" || item.Started.Before(when) {
				oldest, when = key, item.Started
			}
		}
		delete(b.items, oldest)
	}
}

// Update changes one VM's state and message, keeping the rest.
func (b *guestFixBoard) Update(name, state, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if item := b.items[b.key(name)]; item != nil {
		item.State, item.Message = state, message
		if state != guestFixWaiting && state != guestFixRunning {
			item.Finished = time.Now().UTC()
		}
	}
}

func (b *guestFixBoard) Get(name string) *guestFixStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	if item := b.items[b.key(name)]; item != nil {
		copied := *item
		return &copied
	}
	return nil
}

// Busy reports whether any fix-up is waiting or running, so closing and
// update installation wait for it.
func (b *guestFixBoard) Busy() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, item := range b.items {
		if item.State == guestFixWaiting || item.State == guestFixRunning {
			return true
		}
	}
	return false
}

// List returns the board newest first.
func (b *guestFixBoard) List() []guestFixStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := make([]guestFixStatus, 0, len(b.items))
	for _, item := range b.items {
		list = append(list, *item)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Started.After(list[j].Started) })
	return list
}

// guestFixSummary is the one-line result for a finished fix-up.
func guestFixSummary(name, state string, report *guestAutoFixReport) string {
	switch state {
	case guestFixDone:
		message := name + " is fixed up: IP settings, adapters, disks, VMware Tools and activation are done."
		if report != nil && report.Restarting {
			message += " Windows restarts in one minute to finish removing VMware Tools."
		}
		return message
	case guestFixWarning:
		return name + " is fixed up; check the steps marked below."
	}
	reason := "the fix-up failed"
	if report != nil {
		for _, step := range report.Steps {
			if step.Status == "failed" {
				reason = step.Message
				break
			}
		}
	}
	return name + " was not fully fixed up: " + reason + " Use Prepare and fix up for this VM."
}

// guestAutoFixWanted says whether a cutover can be fixed up automatically,
// and why not otherwise.
func guestAutoFixWanted(vm vmMachine, started bool, user string) (bool, string) {
	switch {
	case vm.Source == vmSourceHyperV:
		return false, "The VM came from Hyper-V, so its adapters keep their settings; use Prepare and fix up if anything needs changing."
	case !started:
		return false, "The new VM was left off, so it was not fixed up. Start it, then use Prepare and fix up."
	case strings.TrimSpace(user) == "":
		return false, "No guest administrator account was entered, so the new VM was not fixed up. Enter one in Move VMs and use Fix up now, or use Prepare and fix up."
	}
	return true, ""
}
