//go:build windows

package main

// The Replication tab's test failover for jobs whose copies are received by
// receiver media in a VM on HyperCore, Nutanix AHV or Proxmox VE. It uses
// the target platform connection held in memory (the same one the VMware
// tab's move panel uses). See repl_targettest.go.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

func (a *application) replTargetTestStore() replTargetTestStore {
	return replTargetTestStore{path: filepath.Join(a.update.appDataRoot, "Replication", "targettests.json")}
}

func (a *application) replTargetTestLog(text string) {
	a.mu.Lock()
	a.repl.ttLog = append(a.repl.ttLog, time.Now().Format("15:04:05")+"  "+text)
	if len(a.repl.ttLog) > 200 {
		a.repl.ttLog = a.repl.ttLog[len(a.repl.ttLog)-200:]
	}
	a.mu.Unlock()
	a.vmwareLog("%s", text)
}

func (a *application) handleReplTargetTest(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	store := a.replTargetTestStore()
	if request.Method == http.MethodGet {
		list, err := store.Load()
		a.mu.Lock()
		t := &a.vmware.target
		state := map[string]any{
			"ok": true, "connected": t.platform != nil, "platform": t.connection.Platform, "server": t.connection.Server,
			"user": t.connection.User, "node": t.connection.Node, "certificate": t.certificate,
			"running": a.repl.ttRunning, "log": append([]string{}, a.repl.ttLog...), "error": a.repl.ttError, "tests": list,
		}
		a.mu.Unlock()
		if err != nil {
			state["error"], state["tests"] = err.Error(), []replTargetTest{}
		}
		writeJSON(response, http.StatusOK, state)
		return
	}
	var input struct {
		Action   string `json:"action"` // start, end, cancel
		Agent    string `json:"agent"`
		Job      string `json:"job"`
		SourceVM string `json:"sourceVM"`
		Name     string `json:"name"`
		ID       string `json:"id"`
		Confirm  string `json:"confirm"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if input.Action == "cancel" {
		a.mu.Lock()
		cancel := a.repl.ttCancel
		a.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
		return
	}
	a.mu.Lock()
	platform, connection := a.vmware.target.platform, a.vmware.target.connection
	a.mu.Unlock()
	if platform == nil {
		a.replFail(response, http.StatusBadRequest, "Connect to the target platform first.")
		return
	}
	if !replTargetTestPlatforms[connection.Platform] {
		a.replFail(response, http.StatusBadRequest, "Test failover here is for HyperCore, Nutanix AHV and Proxmox VE. For Hyper-V, use the Disk copy view on the Hyper-V host.")
		return
	}
	list, err := store.Load()
	if err != nil {
		a.replFail(response, http.StatusInternalServerError, err.Error())
		return
	}
	var run func(ctx context.Context) error
	switch input.Action {
	case "start":
		name := strings.TrimSpace(input.Name)
		if name == "" || !strings.EqualFold(strings.TrimSpace(input.Confirm), name) {
			a.replFail(response, http.StatusBadRequest, "Type the test VM's name to confirm.")
			return
		}
		console := a.replConsoleFor(response)
		if console == nil {
			return
		}
		client, err := console.Client(input.Agent)
		if err != nil {
			a.replFail(response, http.StatusBadRequest, err.Error())
			return
		}
		for _, test := range list {
			if strings.EqualFold(test.Name, name) {
				a.replFail(response, http.StatusConflict, "A test VM named "+test.Name+" is already running; end it first.")
				return
			}
		}
		run = func(ctx context.Context) error {
			test, err := replRunTargetTest(ctx, client, platform, connection, replTargetTestOptions{Agent: input.Agent, Job: input.Job, SourceVM: strings.TrimSpace(input.SourceVM), Name: name, Log: a.replTargetTestLog})
			if err != nil {
				return err
			}
			idBytes := make([]byte, 8)
			rand.Read(idBytes)
			test.ID = hex.EncodeToString(idBytes)
			current, err := store.Load()
			if err == nil {
				err = store.Save(append(current, *test))
			}
			if err != nil {
				a.replTargetTestLog(test.Name + " is running but could not be recorded (" + err.Error() + "); delete it on the platform when the test is done.")
			}
			a.replTargetTestLog(test.Name + " is running from the recovery point of " + test.RecoveryPoint.Format("2006-01-02 15:04:05") + " UTC with its network disconnected. Open its console on the platform to test it.")
			go console.RefreshOne(context.Background(), input.Agent)
			return nil
		}
	case "end":
		index := -1
		for i, test := range list {
			if test.ID == input.ID {
				index = i
			}
		}
		if index < 0 {
			a.replFail(response, http.StatusNotFound, "That test is not in the list.")
			return
		}
		test := list[index]
		if !strings.EqualFold(strings.TrimSpace(input.Confirm), test.Name) {
			a.replFail(response, http.StatusBadRequest, "Type the test VM's name, "+test.Name+", to confirm.")
			return
		}
		if test.Platform != connection.Platform || !strings.EqualFold(test.Server, connection.Server) {
			a.replFail(response, http.StatusBadRequest, "Connect to "+test.Server+" to end this test.")
			return
		}
		run = func(ctx context.Context) error {
			if err := vmTargetTestEnd(ctx, platform, test.VMID, test.Name, a.replTargetTestLog); err != nil {
				return err
			}
			current, err := store.Load()
			if err != nil {
				return err
			}
			kept := []replTargetTest{}
			for _, item := range current {
				if item.ID != test.ID {
					kept = append(kept, item)
				}
			}
			if err := store.Save(kept); err != nil {
				return err
			}
			a.replTargetTestLog("The test " + test.Name + " ended: its VM and disks were deleted. The replica was not touched.")
			return nil
		}
	default:
		a.replFail(response, http.StatusBadRequest, "Choose start or end.")
		return
	}
	a.mu.Lock()
	if a.repl.failing || a.repl.ttRunning || a.closing {
		a.mu.Unlock()
		a.replFail(response, http.StatusConflict, "Another test failover step is running, or the application is closing.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	a.repl.failing, a.repl.ttRunning, a.repl.ttCancel, a.repl.ttError, a.repl.ttLog = true, true, cancel, "", nil
	a.mu.Unlock()
	go func() {
		defer cancel()
		err := run(ctx)
		a.mu.Lock()
		a.repl.failing, a.repl.ttRunning, a.repl.ttCancel = false, false, nil
		if err != nil {
			a.repl.ttError = err.Error()
		}
		a.mu.Unlock()
		if err != nil {
			a.replTargetTestLog("The test failover step failed: " + err.Error())
		}
	}()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}
