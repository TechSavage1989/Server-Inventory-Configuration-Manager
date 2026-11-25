//go:build windows

package main

// The Disk copy view's "Start a VM from the copies" panel on the Hyper-V host
// that receives replication: test failover, ending a test, and the
// production start after cutover. See repl_failover.go.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type replSwitch struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (a *application) replFailoverStore() replFailoverStore {
	return replFailoverStore{path: filepath.Join(a.update.appDataRoot, "Replication", "failovers.json")}
}

// replFailoverFolderLocked is the folder the copies are in: the running
// receiver's, else the default receive folder. Caller holds a.mu.
func (a *application) replFailoverFolderLocked() string {
	if r := a.vmReceive.receiver; r != nil && r.Folder() != "" {
		return r.Folder()
	}
	return filepath.Join(a.vmwareCopyRootLocked(), "Received")
}

// replApplyFailoverHolds holds every copy a recorded VM uses on a receiver
// that has just started.
func (a *application) replApplyFailoverHolds(r *vmReceiver) {
	list, err := a.replFailoverStore().Load()
	if err != nil {
		a.vmwareLog("The list of VMs started from copies could not be read, so no copy is held: %v", err)
		return
	}
	for _, f := range list {
		if err := r.Hold(f.Copies, replHoldReason(f)); err != nil {
			a.vmwareLog("%s: %v", f.Name, err)
		}
	}
}

func replHoldReason(f replFailover) string {
	if f.Mode == "test" {
		return "test failover " + f.Name
	}
	return "the production VM " + f.Name + " runs from it"
}

func replHyperVSwitches() ([]replSwitch, error) {
	out, err := vmRunPowerShell("Import-Module Hyper-V; $s = @(Get-VMSwitch | ForEach-Object { [pscustomobject]@{ name = $_.Name; type = [string]$_.SwitchType } }); [Console]::Out.Write((ConvertTo-Json -InputObject $s -Compress))")
	if err != nil {
		return nil, err
	}
	list := []replSwitch{}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "["):]), &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (a *application) handleReplFailover(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	a.mu.Lock()
	folder, receiver := a.replFailoverFolderLocked(), a.vmReceive.receiver
	a.mu.Unlock()
	busy := func(name string) bool { return receiver != nil && receiver.Busy(name) }
	store := a.replFailoverStore()
	if request.Method == http.MethodGet {
		hyperV := vmHyperVHere()
		state := map[string]any{"ok": true, "hyperV": hyperV, "folder": folder, "receiving": receiver != nil}
		list, err := store.Load()
		if err != nil {
			state["error"] = err.Error()
			list = []replFailover{}
		}
		held := map[string]string{}
		for _, f := range list {
			for _, name := range f.Copies {
				held[strings.ToLower(name)] = replHoldReason(f)
			}
		}
		copies, err := replListCopies(folder, busy, func(name string) string { return held[strings.ToLower(name)] })
		if err != nil {
			state["error"] = "The copies could not be listed: " + err.Error()
			copies = []replCopyInfo{}
		}
		state["copies"], state["failovers"] = copies, list
		if hyperV && request.URL.Query().Get("switches") == "1" {
			if switches, err := replHyperVSwitches(); err == nil {
				state["switches"] = switches
			} else {
				state["switchError"] = err.Error()
			}
		}
		writeJSON(response, http.StatusOK, state)
		return
	}
	var input struct {
		Action  string              `json:"action"`
		Request replFailoverRequest `json:"request"`
		ID      string              `json:"id"`
		Confirm string              `json:"confirm"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	if !vmHyperVHere() {
		fail(http.StatusBadRequest, "Starting a VM from the copies runs on the Hyper-V host that receives them; this computer has no Hyper-V role or module.")
		return
	}
	a.mu.Lock()
	if a.repl.failing || a.closing {
		a.mu.Unlock()
		fail(http.StatusConflict, "Another VM step is running, or the application is closing.")
		return
	}
	a.repl.failing = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.repl.failing = false; a.mu.Unlock() }()
	list, err := store.Load()
	if err != nil {
		fail(http.StatusInternalServerError, err.Error())
		return
	}
	switch input.Action {
	case "start":
		r := input.Request
		if !strings.EqualFold(strings.TrimSpace(input.Confirm), strings.TrimSpace(r.Name)) || strings.TrimSpace(r.Name) == "" {
			fail(http.StatusBadRequest, "Type the new VM's name to confirm.")
			return
		}
		for _, f := range list {
			if strings.EqualFold(f.Name, strings.TrimSpace(r.Name)) {
				fail(http.StatusConflict, "A VM named "+f.Name+" was already started from copies here; end it first.")
				return
			}
		}
		plan, children, err := replFailoverPlan(r, folder, filepath.Join(folder, "Test failovers"))
		if err != nil {
			fail(http.StatusBadRequest, err.Error()+".")
			return
		}
		held := map[string]string{}
		for _, f := range list {
			for _, name := range f.Copies {
				held[strings.ToLower(name)] = replHoldReason(f)
			}
		}
		all, err := replListCopies(folder, busy, func(name string) string { return held[strings.ToLower(name)] })
		if err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		byName := map[string]replCopyInfo{}
		for _, info := range all {
			byName[strings.ToLower(info.Name)] = info
		}
		chosen := []replCopyInfo{}
		for _, name := range r.Copies {
			info, ok := byName[strings.ToLower(name)]
			if !ok {
				fail(http.StatusBadRequest, name+" is not a finished copy in "+folder+".")
				return
			}
			chosen = append(chosen, info)
		}
		if err := replCheckCopies(chosen); err != nil {
			fail(http.StatusConflict, err.Error()+".")
			return
		}
		idBytes := make([]byte, 8)
		rand.Read(idBytes)
		record := replFailover{ID: hex.EncodeToString(idBytes), Mode: r.Mode, Name: plan.Name, Copies: r.Copies, Children: children, Folder: folder, Switch: r.Switch, RecoveryPoint: chosen[0].RecoveryPoint, Started: time.Now().UTC()}
		// Hold first, so no round writes into a copy while the VM is made.
		if receiver != nil {
			if err := receiver.Hold(r.Copies, replHoldReason(record)); err != nil {
				fail(http.StatusConflict, err.Error()+".")
				return
			}
		}
		release := func() {
			if receiver != nil {
				receiver.Release(r.Copies)
			}
		}
		a.vmwareLog("Starting %s from %s (%s).", plan.Name, strings.Join(r.Copies, ", "), map[bool]string{true: "test failover on differencing disks", false: "production"}[r.Mode == "test"])
		out, err := vmRunPowerShell(replFailoverScript(plan, folder, r.Copies, children))
		if err != nil {
			if r.Mode == "test" {
				// Undo what was made; the copies are never touched.
				vmRunPowerShell(replFailoverEndScript(replFailover{Name: plan.Name, VMID: "00000000-0000-0000-0000-000000000000", Copies: r.Copies, Children: children}, folder))
			}
			release()
			a.vmwareLog("%s could not be started: %v", plan.Name, err)
			fail(http.StatusBadRequest, "The VM could not be created: "+err.Error())
			return
		}
		id := strings.TrimSpace(out[strings.LastIndexAny(out, "\r\n")+1:])
		if !vmHyperVIDPattern.MatchString(id) {
			release()
			fail(http.StatusInternalServerError, "Hyper-V did not return the new VM's ID: "+out)
			return
		}
		record.VMID = id
		if err := store.Save(append(list, record)); err != nil {
			a.vmwareLog("%s was created but could not be recorded: %v. End it in Hyper-V Manager.", plan.Name, err)
		}
		a.vmwareLog("%s was created (ID %s).", plan.Name, id)
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "failover": record})
	case "end":
		index := -1
		for i, f := range list {
			if f.ID == input.ID {
				index = i
			}
		}
		if index < 0 {
			fail(http.StatusNotFound, "That VM is not in the list.")
			return
		}
		f := list[index]
		if !strings.EqualFold(strings.TrimSpace(input.Confirm), f.Name) {
			fail(http.StatusBadRequest, "Type the VM's name to confirm.")
			return
		}
		if f.Mode == "test" {
			if _, err := vmRunPowerShell(replFailoverEndScript(f, f.Folder)); err != nil {
				fail(http.StatusBadRequest, "The test could not be ended: "+err.Error())
				return
			}
			a.vmwareLog("The test failover %s ended: its VM and test disks were removed.", f.Name)
		} else {
			// A production VM is never removed here; it is only forgotten
			// once it no longer uses the copies.
			out, err := vmRunPowerShell("Import-Module Hyper-V; if (Get-VM -Id " + vmPSQuote(f.VMID) + " -ErrorAction SilentlyContinue) { [Console]::Out.Write('present') }")
			if err != nil {
				fail(http.StatusBadRequest, err.Error())
				return
			}
			if strings.Contains(out, "present") {
				fail(http.StatusConflict, f.Name+" still exists in Hyper-V. It runs from the copies, so replication into them stays held; remove the VM yourself first if you really want to replicate into them again.")
				return
			}
			a.vmwareLog("%s no longer exists in Hyper-V; its copies are released.", f.Name)
		}
		if receiver != nil {
			receiver.Release(f.Copies)
		}
		list = append(list[:index], list[index+1:]...)
		if err := store.Save(list); err != nil {
			fail(http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	default:
		fail(http.StatusBadRequest, "Choose start or end.")
	}
}
