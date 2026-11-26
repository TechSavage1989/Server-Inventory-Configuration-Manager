//go:build windows

package main

// The replication target on this computer: when the console runs on the
// Hyper-V host (or other Windows computer) that receives the copies, a job
// can send to "this computer" and the receiver here is started and paired
// for it, so the operator never sets one up by hand. The pairing code goes
// from the receiver to the job inside this process and never reaches the
// page.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type replLocalState struct {
	HyperV        bool   `json:"hyperV"`
	Computer      string `json:"computer"`
	Running       bool   `json:"running"`
	Folder        string `json:"folder"`
	DefaultFolder string `json:"defaultFolder"`
	Port          int    `json:"port"`
	Sessions      int    `json:"sessions"`
	// Addresses are this computer's addresses, so the page can tell which
	// jobs already send here.
	Addresses []string `json:"addresses"`
}

func (a *application) replLocalStateLocked() replLocalState {
	name, _ := os.Hostname()
	s := replLocalState{HyperV: vmHyperVHere(), Computer: name, DefaultFolder: filepath.Join(a.vmwareCopyRootLocked(), "Received"), Port: vmReceiverDefaultPort, Addresses: vmLocalAddresses()}
	if s.Addresses == nil {
		s.Addresses = []string{}
	}
	if r := a.vmReceive.receiver; r != nil {
		s.Running, s.Folder, s.Port, s.Sessions = true, r.Folder(), r.Port(), r.ActiveSessions()
	}
	return s
}

// replLocalAddress is this computer's address on the network that reaches
// host (the route Windows would use), so an agent is never pointed at a
// storage or cluster network it cannot reach.
func replLocalAddress(host string) string {
	if host = asHostOf(host); host != "" {
		if conn, err := net.Dial("udp", net.JoinHostPort(host, strconv.Itoa(replAgentPort))); err == nil {
			defer conn.Close()
			if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && !addr.IP.IsLoopback() && !addr.IP.IsUnspecified() {
				return addr.IP.String()
			}
		}
	}
	name, _ := os.Hostname()
	return name
}

// replLocalReceiver returns the link an agent at agentAddress uses to reach
// this computer's receiver, starting the receiver in folder (or the
// default) when none runs. A running receiver keeps its folder: changing it
// under existing jobs would make their next rounds start full copies.
func (a *application) replLocalReceiver(agentAddress, folder string) (replReceiverLink, error) {
	folder = strings.TrimSpace(folder)
	a.mu.Lock()
	r, closing := a.vmReceive.receiver, a.closing
	if folder == "" {
		folder = filepath.Join(a.vmwareCopyRootLocked(), "Received")
	}
	a.mu.Unlock()
	if closing {
		return replReceiverLink{}, errors.New("the application is closing")
	}
	if r == nil {
		if !filepath.IsAbs(folder) || (migrateProtectedFolder(folder) != "" && !vmHostDefaultAllowed(folder)) {
			return replReceiverLink{}, errors.New("choose a full path for the copies outside Windows, Program Files and profile folders, such as C:\\ClusterStorage\\Volume1\\Replicas")
		}
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return replReceiverLink{}, errors.New("the destination folder could not be created: " + err.Error())
		}
		// The same start as the Receive panel, so the receiver keeps its
		// identity across restarts and starts again when the app opens.
		if status, reason := a.startVMReceiverFromWindow(vmReceiverStart{Folder: folder, Port: vmReceiverDefaultPort, Firewall: isAdministrator(), Discoverable: true}); status != 0 && !strings.Contains(reason, "already running") {
			return replReceiverLink{}, errors.New(strings.TrimSuffix(reason, "."))
		}
		a.vmwareLog("Started the receiver on this computer for replication, saving copies in %s.", folder)
		a.mu.Lock()
		r = a.vmReceive.receiver
		a.mu.Unlock()
		if r == nil {
			return replReceiverLink{}, errors.New("the receiver on this computer stopped")
		}
	} else if !strings.EqualFold(filepath.Clean(folder), filepath.Clean(r.Folder())) && strings.TrimSpace(folder) != "" {
		return replReceiverLink{}, errors.New("the receiver on this computer already saves copies in " + r.Folder() + ": use that folder, or stop it in Receive and send disks while nothing is being received and choose the folder again")
	}
	return replReceiverLink{Address: net.JoinHostPort(replLocalAddress(agentAddress), strconv.Itoa(r.Port())), Thumbprint: r.Thumbprint(), Code: r.Code()}, nil
}

// handleReplLocal answers the job form: this computer's receiver state, and
// the folder picker for the destination folder.
func (a *application) handleReplLocal(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method == http.MethodGet {
		a.mu.Lock()
		state := a.replLocalStateLocked()
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "local": state})
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if input.Action != "browse" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Unknown action."})
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()
	selected, ok, err := chooseFolder("Choose the folder for the replicated disks on this computer, such as a Cluster Shared Volume. Each server's disks are saved there as SERVER-disk0.vhdx and so on.")
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": ok, "path": selected})
}

// replLocalNames is every name and address of this computer, lower case.
func replLocalNames() map[string]bool {
	out := map[string]bool{}
	if name, err := os.Hostname(); err == nil {
		out[strings.ToLower(name)] = true
		out[asShortName(name)] = true
	}
	for _, address := range vmLocalAddresses() {
		out[strings.ToLower(address)] = true
	}
	return out
}

// replKeepLocalReceiver runs after each poll: jobs that send to this
// computer but hold an older certificate (the receiver was given a new
// identity) are pointed at the running receiver.
func (a *application) replKeepLocalReceiver(console *replConsole) {
	rows, _ := console.Rows()
	local := replLocalNames()
	if !replJobsUseLocal(rows, local) {
		return
	}
	a.mu.Lock()
	r, closing := a.vmReceive.receiver, a.closing
	a.mu.Unlock()
	if closing || r == nil {
		return
	}
	for _, item := range replJobsForLocal(rows, local, r.Thumbprint()) {
		client, err := console.Client(item.Agent)
		if err != nil {
			continue
		}
		record, _ := console.Record(item.Agent)
		spec := item.Spec
		spec.Receiver = replReceiverLink{Address: net.JoinHostPort(replLocalAddress(record.Address), strconv.Itoa(r.Port())), Thumbprint: r.Thumbprint(), Code: r.Code()}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := client.SaveJob(ctx, spec); err != nil {
			a.vmwareLog("%s: could not be pointed at this computer's receiver again: %v", spec.Name, err)
		} else {
			a.vmwareLog("%s now sends to this computer's receiver again (it started with a new certificate).", spec.Name)
		}
		cancel()
		console.RefreshOne(context.Background(), item.Agent)
	}
}
