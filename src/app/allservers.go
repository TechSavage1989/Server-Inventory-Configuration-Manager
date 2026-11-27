package main

// All servers (Migration and replication): one list of every server and VM
// this console knows about, whatever moves it: agent replication jobs,
// Migration plans, Move VMs copies, agents with no job yet and VMs of the
// open inventory that nothing moves. Each row gets one state from a fixed
// set so the tiles and filters mean the same thing for every method, and
// says which bulk actions it accepts. Bulk actions run one server after
// another through each method's own rules (asRunner); nothing here changes
// a source or target by itself.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Row states, the same for every method.
const (
	asNotConfigured = "notconfigured" // in the inventory, nothing moves it
	asAgent         = "agent"         // agent installed, no job yet
	asUnreachable   = "unreachable"   // the agent does not answer
	asSeeding       = "seeding"       // the first full copy is not finished
	asAtCutover     = "atcutover"     // copied only at its cutover, while shut down
	asInSync        = "insync"        // a whole copy exists and is current
	asSyncing       = "syncing"       // a whole copy exists and a round is bringing it up to date
	asBehind        = "behind"        // a whole copy exists but is older than its limit
	asPaused        = "paused"
	asTesting       = "testing" // a test VM runs from the copies
	asCuttingOver   = "cuttingover"
	asCutOver       = "cutover"
	asFailed        = "failed"
	asRunning       = "running"  // a one-off task (conversion, restore, file shares) runs
	asFinished      = "finished" // a one-off task finished
)

// Methods.
const (
	asKindJob   = "job"   // agent replication job (Replicate servers)
	asKindAgent = "agent" // agent with no job
	asKindPlan  = "plan"  // VM in a Migration plan
	asKindMove  = "move"  // VM copied by Move VMs
	asKindVM    = "vm"    // inventory VM nothing moves yet
	asKindTask  = "task"  // a one-off task of this computer (conversion, restore, file shares)
)

// Bulk actions.
const (
	asActInstall   = "install"
	asActCreateJob = "createjob"
	asActSync      = "sync"
	asActPause     = "pause"
	asActResume    = "resume"
	asActTest      = "test"
	asActEndTest   = "endtest"
	asActWave      = "wave"
	asActCutover   = "cutover"
	asActCancelCut = "cancelcutover"
)

var asStateText = map[string]string{
	asNotConfigured: "Not configured",
	asAgent:         "Agent installed, no job",
	asUnreachable:   "Agent not answering",
	asSeeding:       "Initial copy",
	asAtCutover:     "Copies at cutover",
	asInSync:        "In sync, ready to cut over",
	asSyncing:       "Syncing",
	asBehind:        "Behind",
	asPaused:        "Paused",
	asTesting:       "Testing",
	asCuttingOver:   "Cutting over",
	asCutOver:       "Cut over",
	asFailed:        "Failed",
	asRunning:       "Running",
	asFinished:      "Finished",
}

var asMethodText = map[string]string{
	asKindJob:   "Agent replication",
	asKindAgent: "Agent replication",
	asKindPlan:  "Migration plan",
	asKindMove:  "Move VMs",
	asKindVM:    "Not configured",
}

type asRow struct {
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	Method    string `json:"method"`
	Name      string `json:"name"`
	Detail    string `json:"detail,omitempty"`
	State     string `json:"state"`
	StateText string `json:"stateText"`
	// Reason says why the row is in its state and what to do next.
	Reason string `json:"reason,omitempty"`
	// Percent is the running copy's progress, -1 when nothing runs.
	Percent int `json:"percent"`
	// BytesRead lets the page measure speed when BytesPerSecond is 0.
	BytesRead      int64     `json:"bytesRead,omitempty"`
	BytesPerSecond int64     `json:"bytesPerSecond,omitempty"`
	SecondsLeft    int64     `json:"secondsLeft,omitempty"`
	LastSync       time.Time `json:"lastSync,omitempty"`
	NextSync       time.Time `json:"nextSync,omitempty"`
	// LagSeconds is the age of the newest whole copy (-1 when none).
	LagSeconds int64     `json:"lagSeconds"`
	LimitMins  int       `json:"limitMinutes,omitempty"`
	Overdue    bool      `json:"overdue,omitempty"` // the last whole copy is older than its limit
	Wave       int       `json:"wave"`
	CutoverAt  time.Time `json:"cutoverAt,omitempty"`
	// Host is the name or address an agent would be installed on.
	Host    string   `json:"host,omitempty"`
	OS      string   `json:"os,omitempty"`
	Agent   string   `json:"agent,omitempty"`
	Job     string   `json:"job,omitempty"`
	Plan    string   `json:"plan,omitempty"`
	VM      string   `json:"vm,omitempty"`
	Actions []string `json:"actions"`
	// Tested is true once a test VM of this VM was ended (plan rows).
	Tested bool `json:"tested,omitempty"`
	// Consoles are the VMs of this row whose console can be opened in the
	// application (rule 68): the source VM, and the new or test Hyper-V VM.
	Consoles []asConsole `json:"consoles,omitempty"`
}

type asSummary struct {
	Total     int            `json:"total"`
	States    map[string]int `json:"states"`
	Scheduled int            `json:"scheduled"`
	Methods   map[string]int `json:"methods"`
}

// asInput is everything the list is built from.
type asInput struct {
	Agents    []replAgentRow
	Plans     []mbPlanView
	Transfers []vmTransfer
	Inventory *vmInventory
	// Waves are the console's waves for agent jobs (agent|job) and the
	// inventory plan's waves for VMs (VM ID).
	JobWaves map[string]int
	VMWaves  map[string]int
	// Testing lists replication job IDs with a test VM running here.
	Testing map[string]bool
	// Tasks are this computer's one-off tasks, already as rows (kind task,
	// with Method set).
	Tasks []asRow
	Now   time.Time
}

// asTaskRow is a one-off task of this computer: running, or finished or
// failed within the last 12 hours (older ones are left out).
func asTaskRow(key, method, name, detail string, running, failed bool, message string, percent int, bytesRead int64, ended, now time.Time) (asRow, bool) {
	if !running && (ended.IsZero() || now.Sub(ended) > 12*time.Hour) {
		return asRow{}, false
	}
	row := asRow{Key: asKey(asKindTask, key), Kind: asKindTask, Method: method, Name: name, Detail: detail, Reason: message, Percent: -1, LagSeconds: -1, Actions: []string{}, LastSync: ended}
	switch {
	case running:
		row.State, row.Percent, row.BytesRead = asRunning, percent, bytesRead
		if row.Percent < 0 {
			row.Percent = 0
		}
	case failed:
		row.State = asFailed
	default:
		row.State = asFinished
	}
	row.StateText = asStateText[row.State]
	return row, true
}

func asKey(kind string, parts ...string) string { return kind + ":" + strings.Join(parts, "|") }

func asSeeded(status replJobStatus) bool {
	return !status.InitialCopyAt.IsZero() || (status.Rounds > 0 && !status.RecoveryPoint.IsZero())
}

func asHostOf(address string) string {
	host := strings.TrimSpace(address)
	if index := strings.LastIndex(host, ":"); index > 0 && !strings.HasSuffix(host, "]") {
		host = host[:index]
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

func asShortName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if index := strings.Index(name, "."); index > 0 {
		name = name[:index]
	}
	return name
}

// asJobRow is one agent replication job.
func asJobRow(agent replAgentRow, job replJobView, in asInput) asRow {
	name := agent.Name
	if name == "" {
		name = agent.Address
	}
	st := job.Status
	row := asRow{Key: asKey(asKindJob, agent.ID, job.Spec.ID), Kind: asKindJob, Name: name, Detail: "Job " + job.Spec.Name + " on " + agent.Address,
		Agent: agent.ID, Job: job.Spec.ID, Host: asHostOf(agent.Address), OS: agent.Live.Platform, Percent: -1, LagSeconds: -1,
		LimitMins: job.Spec.RPOMinutes, Wave: in.JobWaves[agent.ID+"|"+job.Spec.ID], NextSync: st.NextRun, LastSync: st.RecoveryPoint}
	if job.Spec.CutoverAt != nil {
		row.CutoverAt = *job.Spec.CutoverAt
	}
	seeded := asSeeded(st)
	if !st.RecoveryPoint.IsZero() {
		row.LagSeconds = job.RPOSecond
	}
	if st.State == replStateSyncing || st.State == replStateCutover {
		chunks, done := 0, 0
		for _, d := range st.Disks {
			chunks += d.Chunks
			done += d.Done
			row.BytesRead += d.Read
		}
		row.Percent = 0
		if chunks > 0 {
			row.Percent = done * 100 / chunks
		}
	}
	limit := job.Spec.RPOMinutes
	if limit <= 0 {
		limit = 15
	}
	switch {
	case st.State == replStateCutDone:
		row.State, row.Reason = asCutOver, "The final round finished. The new machine can take over; nothing more runs for this job."
	case st.State == replStateCutover:
		row.State, row.Reason = asCuttingOver, "Services are stopped and the final round is running."
	case !agent.Live.Reachable:
		row.State, row.Reason = asUnreachable, "The agent on "+name+" does not answer, so no rounds run. "+agent.Live.Hint
	case in.Testing[job.Spec.ID] || st.State == replStateHeld:
		row.State, row.Reason = asTesting, "A test VM runs from the copies, so rounds are held. End the test to carry on replicating."
	case st.State == replStatePaused:
		row.State, row.Reason = asPaused, "Paused by an operator. Resume to carry on."
	case st.State == replStateError:
		row.State = asFailed
		row.Reason = "The last round failed"
		if st.LastError != "" {
			row.Reason += ": " + st.LastError
		}
		row.Reason += ". It retries on its own; check that the receiver is running and reachable from " + name + "."
	case !seeded:
		row.State, row.Reason = asSeeding, "The first full copy is running. It cannot be tested or cut over until it finishes."
		if st.State == replStateNeverRun || (st.State == replStateIdle && st.Rounds == 0) {
			row.Reason = "Waiting for the first round to start."
		}
	case st.State == replStateSyncing:
		row.State, row.Reason = asSyncing, "A round is copying the changes since the last recovery point. A whole copy exists, so it can be tested or cut over."
		if job.RPOSecond > int64(limit)*60 {
			row.Overdue = true
			row.Reason += fmt.Sprintf(" The recovery point is older than the %d-minute RPO; this round brings it up to date.", limit)
		}
	case job.RPOSecond > int64(limit)*60:
		row.State, row.Reason, row.Overdue = asBehind, fmt.Sprintf("The newest copy is older than the %d-minute RPO. Rounds may be too slow for the changes: raise the bandwidth limit or Sync now.", limit), true
	default:
		row.State, row.Reason = asInSync, "A whole copy exists and stays current. Test it, then cut over."
	}
	row.StateText = asStateText[row.State]
	acts := []string{}
	switch row.State {
	case asCutOver, asCuttingOver:
	case asUnreachable:
		acts = append(acts, asActWave)
	default:
		if row.State != asTesting && row.State != asAtCutover {
			acts = append(acts, asActSync)
		}
		if st.State == replStatePaused {
			acts = append(acts, asActResume)
		} else if row.State != asTesting {
			acts = append(acts, asActPause)
		}
		if seeded && row.State != asTesting {
			acts = append(acts, asActTest, asActCutover)
		}
		if in.Testing[job.Spec.ID] {
			acts = append(acts, asActEndTest)
		}
		acts = append(acts, asActWave)
	}
	if !row.CutoverAt.IsZero() && row.State != asCutOver {
		acts = append(acts, asActCancelCut)
	}
	row.Actions = acts
	return row
}

// asPlanRow is one VM of a Migration plan.
func asPlanRow(plan mbPlanView, vm mbVMView, now time.Time) asRow {
	row := asRow{Key: asKey(asKindPlan, plan.ID, vm.ID), Kind: asKindPlan, Name: vm.Name, Detail: "Plan " + plan.Name, Plan: plan.ID, VM: vm.ID, Tested: !vm.TestedAt.IsZero(),
		Percent: -1, LagSeconds: -1, Wave: vm.Wave, CutoverAt: vm.CutoverAt, NextSync: vm.NextSync, LastSync: vm.LastSync, LimitMins: plan.Settings.SyncMinutes}
	if vm.Seeded && !vm.LastSync.IsZero() {
		row.LagSeconds = int64(now.Sub(vm.LastSync).Seconds())
	}
	if vm.Live != nil {
		row.Percent, row.BytesRead, row.BytesPerSecond, row.SecondsLeft = vm.Live.Percent, vm.Live.BytesRead, vm.Live.BytesPerSecond, vm.Live.SecondsLeft
	}
	behind := plan.Settings.SyncMinutes > 0 && row.LagSeconds > int64(plan.Settings.SyncMinutes)*60*2+600
	switch {
	case vm.State == mbDone:
		row.State, row.Reason = asCutOver, "Cut over: the Hyper-V VM was created from the final copy."
	case vm.State == mbCutting:
		row.State, row.Reason = asCuttingOver, "The guest is shut down and the final copy is running."
	case vm.State == mbTesting || vm.Test != nil:
		row.State, row.Reason = asTesting, "A test VM runs on differencing disks; syncs wait until the test ends."
	case plan.Paused:
		row.State, row.Reason = asPaused, "The plan "+plan.Name+" is paused. Resume it to carry on."
	case vm.State == mbFailed:
		row.State = asFailed
		row.Reason = "The last round failed"
		if vm.Error != "" {
			row.Reason += ": " + vm.Error
		}
		row.Reason += ". It is retried on its own."
	case !vm.Seeded && mbOffline(&vm.mbVM):
		row.State, row.Reason = asAtCutover, "Copied at its cutover, while shut down: vSphere cannot read it while it runs. Schedule or start the cutover when downtime suits."
	case !vm.Seeded:
		row.State, row.Reason = asSeeding, "The first full copy has not finished yet."
		if plan.Blocked != "" {
			row.Reason += " " + plan.Blocked
		}
	case vm.State == mbCopying:
		row.State, row.Reason = asSyncing, "A sync is copying the changes since the last one. A whole copy exists, so it can be cut over."
		if behind {
			row.Overdue = true
			row.Reason += " The last sync is older than twice the plan's interval; this one brings it up to date."
		}
	case behind:
		row.State, row.Reason, row.Overdue = asBehind, "The last sync is older than twice the plan's interval.", true
		if plan.Blocked != "" {
			row.Reason += " " + plan.Blocked
		}
	default:
		row.State, row.Reason = asInSync, "Seeded and kept in sync. Test it, then cut over."
	}
	row.StateText = asStateText[row.State]
	acts := []string{}
	if row.State != asCutOver && row.State != asCuttingOver {
		if row.State != asTesting && row.State != asAtCutover {
			acts = append(acts, asActSync)
		}
		if plan.Paused {
			acts = append(acts, asActResume)
		} else {
			acts = append(acts, asActPause)
		}
		if vm.Test == nil && vm.State == mbReady {
			acts = append(acts, asActTest)
		}
		if vm.Test != nil {
			acts = append(acts, asActEndTest)
		}
		if mbCanCutOver(&vm.mbVM) == "" {
			acts = append(acts, asActCutover)
		}
		acts = append(acts, asActWave)
	}
	if !vm.CutoverAt.IsZero() && vm.State != mbDone {
		acts = append(acts, asActCancelCut)
	}
	row.Actions = acts
	return row
}

// asVMSkipped is a VM no method moves (templates and system VMs).
func asVMSkipped(vm vmMachine) bool {
	if vm.Template {
		return true
	}
	for _, r := range vm.Readiness {
		if r.Status == vmStatusSkip {
			return true
		}
	}
	return false
}

// asBuild makes the list and its summary.
func asBuild(in asInput) ([]asRow, asSummary) {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	rows := []asRow{}
	known := map[string]bool{} // lower-case names and addresses already listed
	note := func(names ...string) {
		for _, n := range names {
			if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
				known[n] = true
				known[asShortName(n)] = true
			}
		}
	}
	for _, agent := range in.Agents {
		note(agent.Name, agent.Live.Computer, asHostOf(agent.Address))
		name := agent.Name
		if name == "" {
			name = agent.Address
		}
		if len(agent.Live.Jobs) == 0 {
			row := asRow{Key: asKey(asKindAgent, agent.ID), Kind: asKindAgent, Name: name, Detail: agent.Address, Agent: agent.ID, Host: asHostOf(agent.Address), OS: agent.Live.Platform, Percent: -1, LagSeconds: -1, Actions: []string{}}
			if agent.Live.Reachable {
				row.State, row.Reason, row.Actions = asAgent, "The agent answers. Create a replication job to start copying.", []string{asActCreateJob}
			} else {
				row.State, row.Reason = asUnreachable, "The agent does not answer. "+agent.Live.Hint
			}
			row.StateText = asStateText[row.State]
			rows = append(rows, row)
			continue
		}
		for _, job := range agent.Live.Jobs {
			rows = append(rows, asJobRow(agent, job, in))
		}
	}
	inPlan := map[string]bool{}
	for _, plan := range in.Plans {
		for _, vm := range plan.VMs {
			inPlan[vm.ID] = true
			note(vm.Name)
			rows = append(rows, asPlanRow(plan, vm, in.Now))
		}
	}
	// Move VMs copies, one row per VM (a transfer per disk).
	type moveAgg struct {
		row            asRow
		chunks, done   int
		running, fails int
	}
	moves := map[string]*moveAgg{}
	order := []string{}
	for _, t := range in.Transfers {
		if t.Kind != "move" || !strings.HasPrefix(t.ID, "move-") {
			continue
		}
		id := strings.TrimPrefix(t.ID, "move-")
		if bar := strings.Index(id, "|"); bar >= 0 {
			id = id[:bar]
		}
		if inPlan[id] {
			continue
		}
		name := t.Source
		if paren := strings.LastIndex(name, " ("); paren > 0 {
			name = name[:paren]
		}
		agg := moves[id]
		if agg == nil {
			agg = &moveAgg{row: asRow{Key: asKey(asKindMove, id), Kind: asKindMove, Name: name, Detail: "To " + t.Target, VM: id, Percent: -1, LagSeconds: -1, Actions: []string{}}}
			moves[id] = agg
			order = append(order, id)
		}
		agg.chunks += t.Chunks
		agg.done += t.Done
		agg.row.BytesRead += t.BytesRead
		agg.row.BytesPerSecond += int64(t.Rate)
		if t.ETASeconds > agg.row.SecondsLeft {
			agg.row.SecondsLeft = t.ETASeconds
		}
		switch t.State {
		case "running":
			agg.running++
		case "failed":
			agg.fails++
			if agg.row.Reason == "" && t.Error != "" {
				agg.row.Reason = t.Error
			}
		}
		if t.Finished.After(agg.row.LastSync) {
			agg.row.LastSync = t.Finished
		}
	}
	for _, id := range order {
		agg := moves[id]
		row := agg.row
		note(row.Name)
		switch {
		case agg.running > 0:
			row.State, row.StateText = asSeeding, "Copying"
			row.Percent = 0
			if agg.chunks > 0 {
				row.Percent = agg.done * 100 / agg.chunks
			}
			row.Reason = "Move VMs is copying this VM. Open it in Move VMs to cut over when the copy finishes."
		case agg.fails > 0:
			row.State, row.StateText = asFailed, asStateText[asFailed]
			row.Reason = "The last Move VMs copy failed: " + strings.TrimSuffix(row.Reason, ".") + ". Open it in Move VMs and copy again."
		default:
			row.State, row.StateText = asInSync, "Copied"
			row.Reason = "The last Move VMs copy finished. Copy again for a delta, or cut over in Move VMs."
			if !row.LastSync.IsZero() {
				row.LagSeconds = int64(in.Now.Sub(row.LastSync).Seconds())
			}
		}
		rows = append(rows, row)
	}
	if in.Inventory != nil {
		for _, vm := range in.Inventory.VMs {
			if inPlan[vm.ID] || moves[vm.ID] != nil || asVMSkipped(vm) {
				continue
			}
			if known[strings.ToLower(vm.Name)] || known[asShortName(vm.Name)] || (vm.GuestHostName != "" && (known[strings.ToLower(vm.GuestHostName)] || known[asShortName(vm.GuestHostName)])) || (vm.IPAddress != "" && known[strings.ToLower(vm.IPAddress)]) {
				continue
			}
			host := vm.GuestHostName
			if host == "" {
				host = vm.IPAddress
			}
			row := asRow{Key: asKey(asKindVM, vm.ID), Kind: asKindVM, Name: vm.Name, Detail: in.Inventory.Server, VM: vm.ID, Host: host, OS: vm.GuestOS,
				State: asNotConfigured, StateText: asStateText[asNotConfigured], Percent: -1, LagSeconds: -1, Wave: in.VMWaves[vm.ID], Actions: []string{}}
			row.Reason = "Nothing moves this VM yet. Add it to a Migration plan (nothing installed in it), or install the agent to replicate it."
			if host != "" && strings.EqualFold(vm.PowerState, "poweredOn") {
				row.Actions = append(row.Actions, asActInstall)
			} else if host == "" {
				row.Reason += " Its name in the guest is unknown (VMware Tools not running), so type it when installing the agent."
			}
			rows = append(rows, row)
		}
	}
	for i := range rows {
		rows[i].Method = asMethodText[rows[i].Kind]
		rows[i].Consoles = asConsolesFor(rows[i], in)
	}
	rows = append(rows, in.Tasks...)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Wave != rows[j].Wave {
			if rows[i].Wave == 0 || rows[j].Wave == 0 {
				return rows[j].Wave == 0
			}
			return rows[i].Wave < rows[j].Wave
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	summary := asSummary{States: map[string]int{}, Methods: map[string]int{}}
	for state := range asStateText {
		summary.States[state] = 0
	}
	for _, row := range rows {
		summary.Total++
		summary.States[row.State]++
		summary.Methods[row.Kind]++
		if !row.CutoverAt.IsZero() && row.State != asCutOver {
			summary.Scheduled++
		}
	}
	return rows, summary
}

// asWaveStore keeps the console's waves for agent replication jobs (plan
// VMs keep theirs in the plan).
type asWaveStore struct {
	mu    sync.Mutex
	path  string
	waves map[string]int
}

func newASWaveStore(path string) *asWaveStore {
	s := &asWaveStore{path: path, waves: map[string]int{}}
	if raw, err := os.ReadFile(path); err == nil {
		json.Unmarshal(raw, &s.waves)
	}
	if s.waves == nil {
		s.waves = map[string]int{}
	}
	return s
}

func (s *asWaveStore) All() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.waves {
		out[k] = v
	}
	return out
}

func (s *asWaveStore) Set(keys []string, wave int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if wave == 0 {
			delete(s.waves, k)
		} else {
			s.waves[k] = wave
		}
	}
	raw, err := json.MarshalIndent(s.waves, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return replWriteFileAtomic(s.path, raw)
}

// asJobTemplate is what every job made by Create replication jobs shares.
type asJobTemplate struct {
	// Local sends every job to this computer's receiver (started and
	// paired here, in Folder); otherwise to Receiver.
	Local                bool               `json:"local"`
	Folder               string             `json:"folder"`
	Receiver             replReceiverLink   `json:"receiver"`
	IntervalMinutes      int                `json:"intervalMinutes"`
	RPOMinutes           int                `json:"rpoMinutes"`
	Throttle             vmThrottleSchedule `json:"throttle"`
	Compress             bool               `json:"compress"`
	AllowLive            bool               `json:"allowLive"`
	StopServices         []string           `json:"stopServices"`
	ShutdownAfterCutover bool               `json:"shutdownAfterCutover"`
	FullReadAtCutover    bool               `json:"fullReadAtCutover"`
}

type asBulkRequest struct {
	Action         string        `json:"action"`
	Keys           []string      `json:"keys"`
	At             time.Time     `json:"at"`
	StaggerMinutes int           `json:"staggerMinutes"`
	Wave           int           `json:"wave"`
	Template       asJobTemplate `json:"template"`
	TestSwitch     string        `json:"testSwitch"`
	Confirm        string        `json:"confirm"`
}

type asBulkResult struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

// asSyncText words a plan's answer to Sync now for one row.
func asSyncText(answer mbSyncAnswer) string {
	switch {
	case answer.Queued == 0 && len(answer.Skipped) > 0:
		return "Not synced: " + answer.Skipped[0] + "."
	case answer.Paused:
		return "Syncs once its plan is resumed."
	case answer.Waiting != "":
		return "Asked to sync, but it waits: " + answer.Waiting
	}
	return "Syncs at the plan's next check, within a minute."
}

// asPlans is what bulk actions call on Migration plans (mbRunner).
type asPlans interface {
	SetPaused(id string, paused bool) error
	SyncRequest(id string, ids []string) (mbSyncAnswer, error)
	ScheduleCutover(id string, ids []string, at time.Time) error
	StartTest(ctx context.Context, id, vmID string) error
	EndTest(ctx context.Context, id, vmID string) error
	SetWave(id string, ids []string, wave int) error
}

// asJobs is what bulk actions call on the replication console.
type asJobs interface {
	Client(id string) (replAgentAPI, error)
}

type asRunner struct {
	jobs  asJobs
	plans asPlans
	waves *asWaveStore
	// checkReceiver answers whether the receiver saves VHDX files in a
	// folder (Create replication jobs needs one).
	checkReceiver func(ctx context.Context, link replReceiverLink) (bool, error)
	// testJob and endTestJob start and end a test VM from a job's copies
	// on this Hyper-V host.
	// localReceiver is this computer's receiver as the agent of row reaches
	// it, started in folder when none runs.
	localReceiver func(row asRow, folder string) (replReceiverLink, error)
	testJob       func(ctx context.Context, row asRow, testSwitch string) error
	endTestJob    func(ctx context.Context, row asRow) error
	now           func() time.Time
}

// asActionLabels name each bulk action in results and refusals.
var asActionLabels = map[string]string{
	asActInstall: "Install agents", asActCreateJob: "Create replication jobs", asActSync: "Sync now", asActPause: "Pause", asActResume: "Resume",
	asActTest: "Test failover", asActEndTest: "End test", asActWave: "Set wave", asActCutover: "Cut over", asActCancelCut: "Cancel scheduled cutover",
}

// asApplicable returns the rows of keys that accept action, in list order.
func asApplicable(rows []asRow, keys []string, action string) []asRow {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	out := []asRow{}
	for _, row := range rows {
		if !want[row.Key] {
			continue
		}
		for _, a := range row.Actions {
			if a == action {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

var asTargetUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// asTargetName is the VHDX a job made in bulk writes for one disk; it
// matches the Replicate servers editor's default.
func asTargetName(server, disk string) string {
	safe := func(text, fallback string) string {
		text = strings.Trim(asTargetUnsafe.ReplaceAllString(text, "-"), "-.")
		if len(text) > 80 {
			text = text[:80]
		}
		if text == "" {
			return fallback
		}
		return text
	}
	return safe(server, "server") + "-disk" + strings.TrimPrefix(safe(disk, "server"), "dev-") + ".vhdx"
}

// asJobSpec is the job for one server: every disk, to the shared receiver.
func asJobSpec(name string, disks []replDiskInfo, t asJobTemplate) (replJobSpec, error) {
	if len(disks) == 0 {
		return replJobSpec{}, errors.New("the agent listed no disks")
	}
	spec := replJobSpec{Name: name, Receiver: t.Receiver, IntervalMinutes: t.IntervalMinutes, Throttle: t.Throttle, Compress: t.Compress, AllowLive: t.AllowLive,
		RPOMinutes: t.RPOMinutes, StopServices: t.StopServices, ShutdownAfterCutover: t.ShutdownAfterCutover, FullReadAtCutover: t.FullReadAtCutover}
	if spec.RPOMinutes <= 0 {
		spec.RPOMinutes = 15
	}
	ordered := append([]replDiskInfo(nil), disks...)
	// The system disk first, so a VM started from the copies boots from it.
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].System && !ordered[j].System })
	for _, d := range ordered {
		spec.Disks = append(spec.Disks, replDiskMap{Source: d.ID, Target: asTargetName(name, d.ID)})
	}
	return spec, nil
}

// asWaveTimes gives each row its cutover time: the earliest wave at base
// and each later wave staggerMinutes after the one before. Rows without a
// wave go with the first group.
func asWaveTimes(rows []asRow, base time.Time, staggerMinutes int) map[string]time.Time {
	waves := []int{}
	seen := map[int]bool{}
	for _, r := range rows {
		if !seen[r.Wave] {
			seen[r.Wave] = true
			waves = append(waves, r.Wave)
		}
	}
	sort.Ints(waves)
	index := map[int]int{}
	for i, w := range waves {
		index[w] = i
	}
	out := map[string]time.Time{}
	for _, r := range rows {
		out[r.Key] = base.Add(time.Duration(index[r.Wave]*staggerMinutes) * time.Minute)
	}
	return out
}

// Run carries out a bulk action on the applicable rows of keys, one
// server after another, reporting each result as it comes. One server
// failing never stops the others; a cancelled ctx stops before the next.
func (r *asRunner) Run(ctx context.Context, req asBulkRequest, rows []asRow, report func(asBulkResult)) []asBulkResult {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	chosen := asApplicable(rows, req.Keys, req.Action)
	results := []asBulkResult{}
	add := func(row asRow, err error, ok string) {
		res := asBulkResult{Key: row.Key, Name: row.Name, OK: err == nil, Text: ok}
		if err != nil {
			res.Text = strings.TrimSuffix(err.Error(), ".") + "."
		}
		results = append(results, res)
		if report != nil {
			report(res)
		}
	}
	client := func(row asRow) (replAgentAPI, error) {
		if r.jobs == nil {
			return nil, errors.New("the replication console is not available")
		}
		return r.jobs.Client(row.Agent)
	}
	plans := func() error {
		if r.plans == nil {
			return errors.New("Migration plans are not available on this computer")
		}
		return nil
	}
	base := req.At
	if base.IsZero() || base.Before(now()) {
		base = time.Time{}
	}
	var times map[string]time.Time
	if req.Action == asActCutover {
		start := base
		if start.IsZero() {
			start = now()
		}
		times = asWaveTimes(chosen, start, req.StaggerMinutes)
	}
	if req.Action == asActCreateJob && len(chosen) > 0 && !req.Template.Local {
		folder := true
		var err error
		if r.checkReceiver != nil {
			folder, err = r.checkReceiver(ctx, req.Template.Receiver)
		}
		if err == nil && !folder {
			err = errors.New("this receiver writes the disks of one new machine (boot media), so it can take only one server: create that job in Replicate servers, and start a receiver that saves VHDX files (Receive on a Hyper-V host) for many servers")
		}
		if err != nil {
			for _, row := range chosen {
				add(row, err, "")
			}
			return results
		}
	}
	// Pause and resume act on a whole plan once.
	planDone := map[string]bool{}
	waveDone := false
	for _, row := range chosen {
		if ctx.Err() != nil {
			add(row, errors.New("stopped before this server"), "")
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var err error
		text := ""
		switch req.Action {
		case asActSync, asActPause, asActResume:
			if row.Kind == asKindJob {
				var c replAgentAPI
				if c, err = client(row); err == nil {
					err = c.Action(callCtx, row.Job, req.Action)
				}
				text = map[string]string{asActSync: "A round starts now.", asActPause: "Paused.", asActResume: "Resumed."}[req.Action]
			} else if err = plans(); err == nil {
				if req.Action == asActSync {
					var answer mbSyncAnswer
					answer, err = r.plans.SyncRequest(row.Plan, []string{row.VM})
					text = asSyncText(answer)
				} else if planDone[row.Plan] {
					text = "Its plan was already " + map[bool]string{true: "paused", false: "resumed"}[req.Action == asActPause] + "."
				} else {
					planDone[row.Plan] = true
					err = r.plans.SetPaused(row.Plan, req.Action == asActPause)
					text = "Its plan (" + strings.TrimPrefix(row.Detail, "Plan ") + ") is " + map[bool]string{true: "paused", false: "resumed"}[req.Action == asActPause] + ", with every VM in it."
				}
			}
		case asActCutover:
			at := times[row.Key]
			immediate := base.IsZero() && !at.After(now().Add(30*time.Second))
			if row.Kind == asKindJob {
				var c replAgentAPI
				if c, err = client(row); err == nil {
					if immediate {
						err, text = c.Action(callCtx, row.Job, "cutover"), "Cutting over now."
					} else {
						err, text = c.Schedule(callCtx, row.Job, &at), "Cuts over at "+at.Local().Format("2006-01-02 15:04")+"."
					}
				}
			} else if err = plans(); err == nil {
				if immediate {
					at = now()
					text = "Cutting over now."
				} else {
					text = "Cuts over at " + at.Local().Format("2006-01-02 15:04") + "."
				}
				err = r.plans.ScheduleCutover(row.Plan, []string{row.VM}, at)
			}
			if row.Wave > 0 && err == nil {
				text = fmt.Sprintf("Wave %d: %s", row.Wave, text)
			}
		case asActCancelCut:
			if row.Kind == asKindJob {
				var c replAgentAPI
				if c, err = client(row); err == nil {
					err = c.Schedule(callCtx, row.Job, nil)
				}
			} else if err = plans(); err == nil {
				err = r.plans.ScheduleCutover(row.Plan, []string{row.VM}, time.Time{})
			}
			text = "The scheduled cutover is cancelled."
		case asActWave:
			if row.Kind == asKindPlan {
				if err = plans(); err == nil {
					err = r.plans.SetWave(row.Plan, []string{row.VM}, req.Wave)
				}
			} else if !waveDone {
				keys := []string{}
				for _, other := range chosen {
					if other.Kind == asKindJob {
						keys = append(keys, other.Agent+"|"+other.Job)
					}
				}
				waveDone = true
				if r.waves == nil {
					err = errors.New("waves cannot be saved on this computer")
				} else {
					err = r.waves.Set(keys, req.Wave)
				}
			}
			text = "Wave " + fmt.Sprint(req.Wave) + "."
			if req.Wave == 0 {
				text = "No wave."
			}
		case asActTest, asActEndTest:
			cancel()
			callCtx, cancel = context.WithTimeout(ctx, 15*time.Minute)
			if row.Kind == asKindJob {
				if req.Action == asActTest {
					if r.testJob == nil {
						err = errors.New("test failovers from replication copies run on the Hyper-V host that receives them")
					} else {
						err, text = r.testJob(callCtx, row, req.TestSwitch), "The test VM "+row.Name+"-TEST is running from the copies, with "+map[bool]string{true: "its adapters disconnected.", false: "its adapters on " + req.TestSwitch + "."}[req.TestSwitch == ""]
					}
				} else if r.endTestJob == nil {
					err = errors.New("end the test on the Hyper-V host that runs it")
				} else {
					err, text = r.endTestJob(callCtx, row), "The test VM and its test disks are removed; replication carries on."
				}
			} else if err = plans(); err == nil {
				if req.Action == asActTest {
					err, text = r.plans.StartTest(callCtx, row.Plan, row.VM), "The test VM "+row.Name+"-TEST is running on differencing disks."
				} else {
					err, text = r.plans.EndTest(callCtx, row.Plan, row.VM), "The test VM is removed; syncs carry on."
				}
			}
		case asActCreateJob:
			var c replAgentAPI
			if c, err = client(row); err == nil {
				var disks []replDiskInfo
				if disks, err = c.Disks(callCtx); err == nil {
					var spec replJobSpec
					if spec, err = asJobSpec(row.Name, disks, req.Template); err == nil && req.Template.Local {
						if r.localReceiver == nil {
							err = errors.New("this computer cannot receive the copies")
						} else {
							spec.Receiver, err = r.localReceiver(row, req.Template.Folder)
						}
					}
					if err == nil {
						if _, err = c.SaveJob(callCtx, spec); err == nil {
							text = fmt.Sprintf("Job saved: %s to the receiver. The first round starts within a few seconds.", vmPlural(len(spec.Disks), "disk", "disks"))
						}
					}
				}
			}
		default:
			err = errors.New("unknown action")
		}
		cancel()
		add(row, err, text)
	}
	return results
}

// asCheckConfirm checks a changing bulk action's typed confirmation: the
// number of servers it applies to.
func asCheckConfirm(action, typed string, count int) error {
	switch action {
	case asActCutover, asActTest, asActEndTest:
	default:
		return nil
	}
	if strings.TrimSpace(typed) != fmt.Sprint(count) {
		return fmt.Errorf("type %d, the number of servers this %s applies to, to confirm", count, strings.ToLower(asActionLabels[action]))
	}
	return nil
}

// asFailoverRequest is the test VM for a replication job's copies: the
// job's disks in order (system disk first), the source machine's shape
// when it was recorded, adapters disconnected unless a test switch is set.
func asFailoverRequest(row asRow, job replJobView, copies []replCopyInfo, testSwitch string) (replFailoverRequest, error) {
	byName := map[string]replCopyInfo{}
	for _, c := range copies {
		byName[strings.ToLower(c.Name)] = c
	}
	r := replFailoverRequest{Mode: "test", Name: row.Name + "-TEST", Switch: testSwitch, Start: true, CPUs: 2, MemoryMB: 4096}
	chosen := []replCopyInfo{}
	for _, d := range job.Spec.Disks {
		c, ok := byName[strings.ToLower(d.Target)]
		if !ok {
			return r, fmt.Errorf("%s is not among the copies on this host; run the test on the Hyper-V host that receives this job", d.Target)
		}
		chosen = append(chosen, c)
		r.Copies = append(r.Copies, c.Name)
	}
	if err := replCheckCopies(chosen); err != nil {
		return r, err
	}
	r.Generation = chosen[0].Generation
	if r.Generation == 0 {
		return r, fmt.Errorf("%s is not a finished copy yet", chosen[0].Name)
	}
	if m := chosen[0].Machine; m != nil {
		if m.CPUs > 0 {
			r.CPUs = m.CPUs
		}
		if m.MemoryMB > 0 {
			r.MemoryMB = m.MemoryMB
		}
		r.Adapters = m.Adapters
	}
	if r.Generation == 2 {
		r.SecureBoot = "windows"
		if m := chosen[0].Machine; m != nil && strings.EqualFold(m.OS, "linux") {
			r.SecureBoot = "linux"
		}
	} else {
		r.SecureBoot = "off"
	}
	return r, nil
}

// asConsole is one console a row offers, labelled for its button.
type asConsole struct {
	Label  string        `json:"label"`
	Target consoleTarget `json:"target"`
}

// asConsolesFor lists the consoles of a row: the source VM from the open
// inventory (VMware, or Hyper-V on the inventory's host), then for a plan VM
// the Hyper-V VM made at cutover and a running test VM, on the plan's host.
func asConsolesFor(row asRow, in asInput) []asConsole {
	var list []asConsole
	if row.VM != "" && in.Inventory != nil {
		for _, vm := range in.Inventory.VMs {
			if vm.ID != row.VM {
				continue
			}
			target := consoleTarget{Platform: "vmware", ID: vm.ID, Name: vm.Name}
			if vm.Source == "hyperv" {
				target.Platform, target.Host = "hyperv", in.Inventory.Server
			}
			if row.State != asCutOver || vm.Source == "hyperv" {
				list = append(list, asConsole{Label: "Source", Target: target})
			}
			break
		}
	}
	if row.Kind == asKindPlan {
		for _, plan := range in.Plans {
			if plan.ID != row.Plan {
				continue
			}
			host := firstNonEmpty(plan.Settings.Node, plan.Settings.RemoteHost)
			for _, vm := range plan.VMs {
				if vm.ID != row.VM {
					continue
				}
				if vm.HyperVID != "" {
					list = append(list, asConsole{Label: "New VM", Target: consoleTarget{Platform: "hyperv", ID: vm.HyperVID, Name: vm.Name, Host: host}})
				}
				if vm.Test != nil && vm.Test.VMID != "" {
					list = append(list, asConsole{Label: "Test VM", Target: consoleTarget{Platform: "hyperv", ID: vm.Test.VMID, Name: vm.Test.Name, Host: host}})
				}
			}
		}
	}
	return list
}
