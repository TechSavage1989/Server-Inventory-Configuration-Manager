//go:build windows

package main

// /api/servers: the All servers tool's list (GET) and its bulk actions
// (POST). See allservers.go for the rules.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type asRuntime struct {
	waves *asWaveStore
	// The running (or last) bulk action.
	running bool
	action  string
	total   int
	results []asBulkResult
	started time.Time
	ended   time.Time
	cancel  context.CancelFunc
}

func (a *application) asWavesLocked() *asWaveStore {
	if a.servers.waves == nil {
		a.servers.waves = newASWaveStore(filepath.Join(a.update.appDataRoot, "Replication", "waves.json"))
	}
	return a.servers.waves
}

// asTestingJobs lists replication job IDs that a test VM on this host runs
// from (the copies' job, from their records).
func (a *application) asTestingJobs(console *replConsole) map[string]bool {
	out := map[string]bool{}
	list, err := a.replFailoverStore().Load()
	if err != nil || len(list) == 0 {
		return out
	}
	testCopies := map[string]bool{}
	for _, f := range list {
		if f.Mode == "test" {
			for _, c := range f.Copies {
				testCopies[strings.ToLower(c)] = true
			}
		}
	}
	if len(testCopies) == 0 || console == nil {
		return out
	}
	rows, _ := console.Rows()
	for _, agent := range rows {
		for _, job := range agent.Live.Jobs {
			for _, d := range job.Spec.Disks {
				if testCopies[strings.ToLower(d.Target)] {
					out[job.Spec.ID] = true
				}
			}
		}
	}
	return out
}

// asRows builds the current list.
func (a *application) asRows() ([]asRow, asSummary, *replConsole, *mbRunner) {
	runner, _ := a.mbRunner()
	a.mu.Lock()
	console := a.replConsoleLocked()
	waves := a.asWavesLocked()
	inventory := a.vmware.inventory
	vmWaves := map[string]int{}
	if a.vmware.plan != nil {
		for _, p := range a.vmware.plan.VMs {
			vmWaves[p.ID] = p.Wave
		}
	}
	board := a.transfers
	tasks := a.asTasksLocked(time.Now())
	a.mu.Unlock()
	in := asInput{Inventory: inventory, JobWaves: waves.All(), VMWaves: vmWaves, Now: time.Now()}
	if console != nil {
		in.Agents, _ = console.Rows()
	}
	if runner != nil {
		in.Plans, _, _ = runner.View()
	}
	if board != nil {
		in.Transfers = board.List()
	}
	in.Testing = a.asTestingJobs(console)
	in.Tasks = tasks
	rows, summary := asBuild(in)
	return rows, summary, console, runner
}

func (a *application) asJobView(console *replConsole, agentID, jobID string) (replJobView, bool) {
	rows, _ := console.Rows()
	for _, agent := range rows {
		if agent.ID != agentID {
			continue
		}
		for _, job := range agent.Live.Jobs {
			if job.Spec.ID == jobID {
				return job, true
			}
		}
	}
	return replJobView{}, false
}

func (a *application) handleServers(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	if request.Method == http.MethodGet {
		rows, summary, console, _ := a.asRows()
		a.mu.Lock()
		bulk := map[string]any{"running": a.servers.running, "action": a.servers.action, "total": a.servers.total, "results": append([]asBulkResult{}, a.servers.results...)}
		if !a.servers.started.IsZero() {
			bulk["started"] = a.servers.started.UTC()
		}
		if !a.servers.ended.IsZero() {
			bulk["ended"] = a.servers.ended.UTC()
		}
		inventory := ""
		if a.vmware.inventory != nil {
			inventory = a.vmware.inventory.Server
		}
		loadErr := a.repl.loadErr
		a.mu.Unlock()
		a.mu.Lock()
		local := a.replLocalStateLocked()
		a.mu.Unlock()
		state := map[string]any{"ok": true, "local": local, "rows": rows, "summary": summary, "bulk": bulk, "hyperVHere": vmHyperVHere(), "isAdministrator": isAdministrator(), "inventory": inventory, "console": console != nil, "now": time.Now().UTC()}
		if loadErr != "" {
			state["error"] = "The replication console's records could not be opened: " + loadErr
		}
		writeJSON(response, http.StatusOK, state)
		return
	}
	var input asBulkRequest
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if input.Action == "stop" {
		a.mu.Lock()
		if a.servers.cancel != nil {
			a.servers.cancel()
		}
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
		return
	}
	label, known := asActionLabels[input.Action]
	if !known || input.Action == asActInstall {
		fail(http.StatusBadRequest, "Unknown action.")
		return
	}
	if input.Wave < 0 || input.Wave > 99 {
		fail(http.StatusBadRequest, "Choose a wave from 0 to 99.")
		return
	}
	if input.StaggerMinutes < 0 || input.StaggerMinutes > 7*24*60 {
		fail(http.StatusBadRequest, "Choose a gap between waves from 0 minutes to 7 days.")
		return
	}
	if input.Action == asActCreateJob && input.Template.Local {
		input.Template.Receiver = replReceiverLink{}
	}
	if input.Action == asActCreateJob {
		t := &input.Template
		t.Receiver.Address, t.Receiver.Thumbprint, t.Receiver.Code = strings.TrimSpace(t.Receiver.Address), strings.ToUpper(strings.NewReplacer(" ", "", ":", "").Replace(t.Receiver.Thumbprint)), strings.TrimSpace(t.Receiver.Code)
		if !t.Local && (t.Receiver.Address == "" || t.Receiver.Thumbprint == "" || len(strings.NewReplacer("-", "", " ", "").Replace(t.Receiver.Code)) != 20) {
			fail(http.StatusBadRequest, "Type the receiver's address, certificate thumbprint and 20-character pairing code (or find it on the network).")
			return
		}
		if t.RPOMinutes < 1 || t.RPOMinutes > 10080 {
			fail(http.StatusBadRequest, "Type the RPO alert as a number of minutes from 1 to 10080.")
			return
		}
		if t.IntervalMinutes < 0 || t.IntervalMinutes > 1440 {
			fail(http.StatusBadRequest, "Choose how often to replicate.")
			return
		}
		if err := t.Throttle.Validate(); err != nil {
			fail(http.StatusBadRequest, "The bandwidth limit was not accepted: "+err.Error()+".")
			return
		}
	}
	if input.Action == asActCutover && !input.At.IsZero() && input.At.Before(time.Now().Add(-time.Minute)) {
		fail(http.StatusBadRequest, "The cutover time has passed; choose a later one, or Cut over now.")
		return
	}
	rows, _, console, runner := a.asRows()
	chosen := asApplicable(rows, input.Keys, input.Action)
	if len(chosen) == 0 {
		fail(http.StatusBadRequest, "None of the selected servers can take "+strings.ToLower(label)+" now. The row's state says why.")
		return
	}
	if err := asCheckConfirm(input.Action, input.Confirm, len(chosen)); err != nil {
		fail(http.StatusBadRequest, strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
		return
	}
	testing := input.Action == asActTest || input.Action == asActEndTest
	if testing {
		for _, row := range chosen {
			if row.Kind == asKindJob && !vmHyperVHere() {
				fail(http.StatusBadRequest, "Test failovers from replication copies run on the Hyper-V host that receives them; this computer has no Hyper-V role or module. Run this on that host, or use Start VMs from copies there.")
				return
			}
		}
		if !a.beginCriticalOperation() {
			fail(http.StatusConflict, "An update is being installed.")
			return
		}
	}
	a.mu.Lock()
	if a.servers.running || a.closing || (testing && (a.repl.failing || a.plans.testing)) {
		a.mu.Unlock()
		if testing {
			a.endCriticalOperation()
		}
		fail(http.StatusConflict, "Another bulk action or VM step is running, or the application is closing.")
		return
	}
	if testing {
		a.repl.failing, a.plans.testing = true, true
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.servers.running, a.servers.action, a.servers.total, a.servers.results = true, input.Action, len(chosen), []asBulkResult{}
	a.servers.started, a.servers.ended, a.servers.cancel = time.Now(), time.Time{}, cancel
	waves := a.asWavesLocked()
	a.mu.Unlock()
	runnerArg := asPlans(nil)
	if runner != nil {
		runnerArg = runner
	}
	jobsArg := asJobs(nil)
	if console != nil {
		jobsArg = console
	}
	bulk := &asRunner{jobs: jobsArg, plans: runnerArg, waves: waves,
		checkReceiver: func(ctx context.Context, link replReceiverLink) (bool, error) {
			address, err := vmReceiverAddress(link.Address)
			if err != nil {
				return false, errors.New("the receiver was not accepted: " + err.Error())
			}
			callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			folder, _, err := vmReceiverDescribe(callCtx, address, link.Thumbprint, link.Code)
			if err != nil {
				return false, errors.New("the receiver could not be reached from this computer: " + err.Error())
			}
			return folder, nil
		},
		localReceiver: func(row asRow, folder string) (replReceiverLink, error) {
			address := row.Host
			if record, ok := console.Record(row.Agent); ok {
				address = record.Address
			}
			return a.replLocalReceiver(address, folder)
		},
		testJob: func(ctx context.Context, row asRow, testSwitch string) error {
			job, ok := a.asJobView(console, row.Agent, row.Job)
			if !ok {
				return errors.New("the job is no longer listed")
			}
			a.mu.Lock()
			folder, receiver := a.replFailoverFolderLocked(), a.vmReceive.receiver
			a.mu.Unlock()
			store := a.replFailoverStore()
			list, err := store.Load()
			if err != nil {
				return err
			}
			held := map[string]string{}
			for _, f := range list {
				for _, name := range f.Copies {
					held[strings.ToLower(name)] = replHoldReason(f)
				}
			}
			busy := func(name string) bool { return receiver != nil && receiver.Busy(name) }
			copies, err := replListCopies(folder, busy, func(name string) string { return held[strings.ToLower(name)] })
			if err != nil {
				return errors.New("the copies could not be listed: " + err.Error())
			}
			req, err := asFailoverRequest(row, job, copies, testSwitch)
			if err != nil {
				return err
			}
			_, _, err = a.replStartFromCopiesLocked(req, list, store, folder, receiver)
			return err
		},
		endTestJob: func(ctx context.Context, row asRow) error {
			job, ok := a.asJobView(console, row.Agent, row.Job)
			if !ok {
				return errors.New("the job is no longer listed")
			}
			targets := map[string]bool{}
			for _, d := range job.Spec.Disks {
				targets[strings.ToLower(d.Target)] = true
			}
			a.mu.Lock()
			receiver := a.vmReceive.receiver
			a.mu.Unlock()
			store := a.replFailoverStore()
			ended := 0
			for {
				list, err := store.Load()
				if err != nil {
					return err
				}
				index := -1
				for i, f := range list {
					if f.Mode != "test" {
						continue
					}
					for _, c := range f.Copies {
						if targets[strings.ToLower(c)] {
							index = i
						}
					}
				}
				if index < 0 {
					break
				}
				if _, err := a.replEndFailoverLocked(list, index, store, receiver); err != nil {
					return err
				}
				ended++
			}
			if ended == 0 {
				return errors.New("no test VM from this job's copies is recorded on this host")
			}
			return nil
		},
	}
	a.vmwareLog("All servers: %s on %s.", label, vmPlural(len(chosen), "server", "servers"))
	go func() {
		defer cancel()
		if testing {
			defer a.endCriticalOperation()
		}
		results := bulk.Run(ctx, input, rows, func(result asBulkResult) {
			a.mu.Lock()
			a.servers.results = append(a.servers.results, result)
			a.mu.Unlock()
			if !result.OK {
				a.vmwareLog("All servers: %s: %s", result.Name, result.Text)
			}
		})
		failed := 0
		for _, r := range results {
			if !r.OK {
				failed++
			}
		}
		a.mu.Lock()
		a.servers.running, a.servers.ended, a.servers.cancel = false, time.Now(), nil
		if testing {
			a.repl.failing, a.plans.testing = false, false
		}
		a.mu.Unlock()
		a.vmwareLog("All servers: %s finished, %d of %d done.", label, len(results)-failed, len(results))
		if console != nil {
			console.Refresh(context.Background())
		}
		if runner != nil {
			go runner.Sweep()
		}
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true, "count": len(chosen)})
}

// asTasksLocked lists this computer's one-off tasks: a disk conversion (or
// fix-up or send), a restore and a file share migration. Caller holds a.mu.
func (a *application) asTasksLocked(now time.Time) []asRow {
	out := []asRow{}
	failed := func(status string) bool {
		status = strings.ToLower(status)
		return strings.Contains(status, "fail") || strings.Contains(status, "error") || status == "canceled"
	}
	c := a.convert
	if c.status != "" && c.status != "idle" {
		name := filepath.Base(c.source)
		if name == "." || name == "" {
			name = "Disk image"
		}
		method := map[string]string{"repair": "Disk fix-up", "send": "Send to a host"}[c.task]
		if method == "" {
			method = "Disk conversion"
		}
		percent := -1
		if c.progress.Chunks > 0 {
			percent = c.progress.Done * 100 / c.progress.Chunks
		} else if c.sendTotal > 0 {
			percent = int(c.sendDone * 100 / c.sendTotal)
		}
		detail := c.target
		if detail != "" {
			detail = "To " + detail
		}
		if row, ok := asTaskRow("convert", method, name, detail, c.running, failed(c.status), c.message, percent, c.progress.BytesRead, c.endedAt, now); ok {
			out = append(out, row)
		}
	}
	r := a.restore
	if r.status == "running" || (!r.endedAt.IsZero() && r.status != "ready" && r.status != "scanning") {
		name := filepath.Base(r.packagePath)
		if name == "." || name == "" {
			name = "Restore"
		}
		if row, ok := asTaskRow("restore", "Restore", name, "Restore package", r.status == "running", failed(r.status), r.message, -1, 0, r.endedAt, now); ok {
			out = append(out, row)
		}
	}
	m := a.migrate
	if m.action != "" || (!m.endedAt.IsZero() && m.source != "") {
		name := m.source
		if name == "" {
			name = "File shares"
		}
		if row, ok := asTaskRow("shares", "Move file shares", name, "File shares to this server", m.action != "", failed(m.status), m.message, -1, 0, m.endedAt, now); ok {
			out = append(out, row)
		}
	}
	return out
}
