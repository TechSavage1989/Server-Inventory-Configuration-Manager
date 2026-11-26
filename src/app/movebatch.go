package main

// Migration plans: many VMs moved together, Nutanix Move style (feature
// reference only). A plan seeds every VM from a start time, keeps each one in
// sync on an interval with a limit on how many copy at once and a bandwidth
// schedule, runs test cutovers on differencing disks, and cuts VMs over now or
// at a scheduled time, in wave order, each VM on its own so one failure never
// stops the others. Copies and cutovers reuse Move VMs' rounds (vmMoveRound
// for VMware, hypervMoveRound for Hyper-V sources), so every rule of a single
// move still applies: our own snapshot or checkpoint only, the guest shut
// down only through VMware Tools or the Hyper-V shutdown component, a
// verified final round, and the new VM created only when that round
// succeeded.
//
// The scheduling and state rules here are untagged so they are tested on
// any platform; movebatch_windows.go supplies the Hyper-V host target and
// the HTTP handlers.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// VM states in a plan.
const (
	mbWaiting = "waiting" // not copied yet
	mbCopying = "copying" // a round is running
	mbReady   = "ready"   // in sync: the last round finished
	mbTesting = "testing" // a test VM runs on differencing disks
	mbCutting = "cutting" // the cutover is running
	mbDone    = "done"    // cut over: the new VM exists
	mbFailed  = "failed"  // the last round or cutover failed
)

const (
	mbMaxPlans       = 20
	mbMaxVMs         = 200
	mbMaxConcurrency = 4
	// mbSweepEvery is how often the runner looks for work.
	mbSweepEvery = 20 * time.Second
)

type mbSettings struct {
	// StartAt is when the first copies may begin; zero is now.
	StartAt time.Time `json:"startAt"`
	// SyncMinutes is how long after a VM's last round the next one starts.
	SyncMinutes int `json:"syncMinutes"`
	// Concurrency is how many VMs of this plan copy at once.
	Concurrency int                `json:"concurrency"`
	Throttle    vmThrottleSchedule `json:"throttle"`
	// CopyRoot holds one folder of copied disks per VM; empty is the
	// host's virtual hard disk folder.
	CopyRoot string `json:"copyRoot"`
	// VMFolder is where Hyper-V keeps the new VMs; empty is the default.
	VMFolder string `json:"vmFolder"`
	KeepMAC  bool   `json:"keepMac"`
	StartVMs bool   `json:"startVms"`
	Cluster  bool   `json:"cluster"`
	Quiesce  bool   `json:"quiesce"`
	// TestSwitch connects test VMs to this switch; empty leaves their
	// adapters disconnected.
	TestSwitch string `json:"testSwitch"`
	// DisconnectSource sets the old VMware VM's adapters to not connect
	// after a successful cutover (recorded first, so it can be undone).
	DisconnectSource bool `json:"disconnectSource"`
}

type mbTest struct {
	Name     string    `json:"name"`
	VMID     string    `json:"vmId"`
	Folder   string    `json:"folder"`
	Copies   []string  `json:"copies"`
	Children []string  `json:"children"`
	Started  time.Time `json:"started"`
}

type mbVM struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Wave  int    `json:"wave"`
	State string `json:"state"`
	// Seeded is true once a round has copied every disk.
	Seeded      bool      `json:"seeded"`
	Rounds      int       `json:"rounds"`
	LastAttempt time.Time `json:"lastAttempt,omitempty"`
	LastSync    time.Time `json:"lastSync,omitempty"`
	LastMode    string    `json:"lastMode,omitempty"`
	LastRead    int64     `json:"lastRead,omitempty"`
	LastWritten int64     `json:"lastWritten,omitempty"`
	LastSeconds int64     `json:"lastSeconds,omitempty"`
	Failures    int       `json:"failures"`
	Error       string    `json:"error,omitempty"`
	CopyFolder  string    `json:"copyFolder"`
	Disks       []string  `json:"disks,omitempty"`
	CutoverAt   time.Time `json:"cutoverAt,omitempty"`
	Test        *mbTest   `json:"test,omitempty"`
	HyperVID    string    `json:"hyperVId,omitempty"`
	CutOver     time.Time `json:"cutOver,omitempty"`
	// Notes are results worth keeping (cluster role, source adapters).
	Notes []string `json:"notes,omitempty"`
	// SyncNow asks for a round at the next sweep.
	SyncNow bool `json:"syncNow,omitempty"`
}

type mbPlan struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Server   string     `json:"server"`
	Created  time.Time  `json:"created"`
	Paused   bool       `json:"paused"`
	Settings mbSettings `json:"settings"`
	VMs      []*mbVM    `json:"vms"`
}

func mbValidateSettings(s *mbSettings) error {
	if s.SyncMinutes == 0 {
		s.SyncMinutes = 60
	}
	if s.SyncMinutes < 15 || s.SyncMinutes > 1440 {
		return errors.New("sync every 15 to 1440 minutes")
	}
	if s.Concurrency == 0 {
		s.Concurrency = 2
	}
	if s.Concurrency < 1 || s.Concurrency > mbMaxConcurrency {
		return fmt.Errorf("copy 1 to %d VMs at once", mbMaxConcurrency)
	}
	if err := s.Throttle.Validate(); err != nil {
		return err
	}
	s.CopyRoot, s.VMFolder, s.TestSwitch = strings.TrimSpace(s.CopyRoot), strings.TrimSpace(s.VMFolder), strings.TrimSpace(s.TestSwitch)
	if s.CopyRoot != "" && !filepath.IsAbs(s.CopyRoot) && !strings.HasPrefix(s.CopyRoot, `\\`) {
		return errors.New("choose a full path for the copied disks, or leave it empty for the host's default")
	}
	if s.VMFolder != "" && !filepath.IsAbs(s.VMFolder) && !strings.HasPrefix(s.VMFolder, `\\`) {
		return errors.New("choose a full path for the new VMs, or leave it empty for the host's default")
	}
	if len(s.TestSwitch) > 100 {
		return errors.New("the test switch name is too long")
	}
	return nil
}

// mbNextSync is when the VM's next round is due; zero means not scheduled.
func mbNextSync(plan *mbPlan, vm *mbVM) time.Time {
	switch vm.State {
	case mbDone, mbTesting, mbCutting, mbCopying:
		return time.Time{}
	}
	if vm.SyncNow || vm.LastAttempt.IsZero() {
		return plan.Settings.StartAt
	}
	interval := time.Duration(plan.Settings.SyncMinutes) * time.Minute
	if vm.Failures > 0 {
		// Retry a failed round sooner: 5, 10, 20 minutes..., never later
		// than the plan's interval.
		backoff := 5 * time.Minute << uint(mbMin(vm.Failures-1, 8))
		if backoff < interval {
			interval = backoff
		}
	}
	return vm.LastAttempt.Add(interval)
}

func mbMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type mbWork struct {
	Plan    *mbPlan
	VM      *mbVM
	Cutover bool
}

// mbDue picks the work for one sweep: due cutovers first, then due copies
// by wave and name, at most each plan's concurrency and limit in all.
func mbDue(plans []*mbPlan, now time.Time, limit int) []mbWork {
	work := []mbWork{}
	for _, plan := range plans {
		if plan.Paused {
			continue
		}
		cutovers, copies := []mbWork{}, []mbWork{}
		busy := 0
		for _, vm := range plan.VMs {
			switch vm.State {
			case mbCopying, mbCutting:
				busy++
				continue
			case mbDone, mbTesting:
				continue
			}
			if !vm.CutoverAt.IsZero() && !now.Before(vm.CutoverAt) && vm.Seeded {
				cutovers = append(cutovers, mbWork{Plan: plan, VM: vm, Cutover: true})
				continue
			}
			if next := mbNextSync(plan, vm); !now.Before(next) {
				copies = append(copies, mbWork{Plan: plan, VM: vm})
			}
		}
		order := func(list []mbWork) {
			sort.SliceStable(list, func(i, j int) bool {
				a, b := list[i].VM, list[j].VM
				if a.Wave != b.Wave {
					return a.Wave < b.Wave
				}
				if a.Seeded != b.Seeded {
					return !a.Seeded
				}
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			})
		}
		order(cutovers)
		order(copies)
		room := plan.Settings.Concurrency - busy
		for _, item := range append(cutovers, copies...) {
			if room <= 0 || len(work) >= limit {
				break
			}
			work = append(work, item)
			room--
		}
	}
	return work
}

// mbMissedCutovers cancels scheduled cutovers whose time has come while the
// VM's first copy is still unfinished: running them hours later, when the
// copy ends, could take a server down outside its maintenance window. Each
// such VM gets a note and keeps syncing. It reports whether anything changed.
func mbMissedCutovers(plans []*mbPlan, now time.Time) bool {
	changed := false
	for _, plan := range plans {
		for _, vm := range plan.VMs {
			if vm.CutoverAt.IsZero() || now.Before(vm.CutoverAt) || vm.Seeded || vm.State == mbDone || vm.State == mbCutting {
				continue
			}
			vm.Notes = append(vm.Notes, vm.CutoverAt.Local().Format("2006-01-02 15:04")+": the scheduled cutover did not run because the first copy had not finished. Schedule it again.")
			vm.CutoverAt = time.Time{}
			changed = true
		}
	}
	return changed
}

// mbSummary counts a plan's VMs for the dashboard tiles.
type mbSummary struct {
	Total     int `json:"total"`
	Waiting   int `json:"waiting"`
	Copying   int `json:"copying"`
	Ready     int `json:"ready"`
	Testing   int `json:"testing"`
	Scheduled int `json:"scheduled"`
	Done      int `json:"done"`
	Failed    int `json:"failed"`
}

func mbSummarize(plan *mbPlan) mbSummary {
	s := mbSummary{Total: len(plan.VMs)}
	for _, vm := range plan.VMs {
		switch vm.State {
		case mbWaiting:
			s.Waiting++
		case mbCopying, mbCutting:
			s.Copying++
		case mbReady:
			s.Ready++
		case mbTesting:
			s.Testing++
		case mbDone:
			s.Done++
		case mbFailed:
			s.Failed++
		}
		if !vm.CutoverAt.IsZero() && vm.State != mbDone {
			s.Scheduled++
		}
	}
	return s
}

// mbTarget does the work on the target host.
type mbTarget interface {
	// Check refuses a VM the target cannot take before it joins a plan.
	Check(plan *mbPlan, vm *mbVM) error
	// Round copies the VM's disks; with cutover it shuts the guest down
	// first and verifies.
	Round(ctx context.Context, plan *mbPlan, vm *mbVM, cutover bool, throttle *vmThrottle, progress func(string, vmCopyProgress), log func(string)) (*vmMoveResult, error)
	// Create makes the new VM on the copied disks and returns its ID.
	Create(ctx context.Context, plan *mbPlan, vm *mbVM, result *vmMoveResult, log func(string)) (string, []string, error)
	StartTest(ctx context.Context, plan *mbPlan, vm *mbVM, log func(string)) (*mbTest, error)
	EndTest(ctx context.Context, plan *mbPlan, vm *mbVM, log func(string)) error
}

// mbLive is a running round's progress, kept in memory for the dashboard.
type mbLive struct {
	Started time.Time
	Cutover bool
	Disks   map[string]vmCopyProgress
}

type mbLiveView struct {
	Cutover   bool  `json:"cutover"`
	Percent   int   `json:"percent"`
	BytesRead int64 `json:"bytesRead"`
	// BytesPerSecond and SecondsLeft are 0 until they can be estimated.
	BytesPerSecond int64 `json:"bytesPerSecond"`
	SecondsLeft    int64 `json:"secondsLeft"`
	Elapsed        int64 `json:"elapsed"`
}

func (l *mbLive) view(now time.Time) mbLiveView {
	v := mbLiveView{Cutover: l.Cutover, Elapsed: int64(now.Sub(l.Started).Seconds())}
	chunks, done := 0, 0
	for _, p := range l.Disks {
		chunks += p.Chunks
		done += p.Done
		v.BytesRead += p.BytesRead
	}
	if chunks > 0 {
		v.Percent = done * 100 / chunks
	}
	if v.Elapsed > 0 {
		v.BytesPerSecond = v.BytesRead / v.Elapsed
		if done > 0 && done < chunks {
			v.SecondsLeft = int64(float64(v.Elapsed) * float64(chunks-done) / float64(done))
		}
	}
	return v
}

// mbRunner owns the plans of this computer.
type mbRunner struct {
	mu     sync.Mutex
	path   string
	plans  []*mbPlan
	target mbTarget
	// claim takes the VMware action lock for a sweep; it returns false with
	// a reason when another step holds it.
	claim     func() (release func(), reason string)
	log       func(string)
	now       func() time.Time
	live      map[string]*mbLive
	throttles map[string]*vmThrottle
	cancels   map[string]context.CancelFunc
	sweeping  bool
	// blocked says why the last sweep could not start (another step, no
	// vSphere session); shown on the dashboard.
	blocked string
	// ready reports whether the target can run work now (a live session
	// for the plan's server); "" means yes.
	ready func(plan *mbPlan) string
}

func newMBRunner(path string, target mbTarget) *mbRunner {
	return &mbRunner{path: path, target: target, now: time.Now, log: func(string) {}, live: map[string]*mbLive{}, throttles: map[string]*vmThrottle{}, cancels: map[string]context.CancelFunc{},
		claim: func() (func(), string) { return func() {}, "" }, ready: func(*mbPlan) string { return "" }}
}

func mbNewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Load reads the saved plans. A round or cutover that was running when the
// application stopped is shown as failed so it is retried, never as done.
func (r *mbRunner) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		r.plans = []*mbPlan{}
		return nil
	}
	if err != nil {
		return err
	}
	plans := []*mbPlan{}
	if err := json.Unmarshal(raw, &plans); err != nil {
		return fmt.Errorf("%s: %w", r.path, err)
	}
	for _, plan := range plans {
		for _, vm := range plan.VMs {
			switch vm.State {
			case mbCopying:
				vm.State, vm.Error = mbFailed, "The application stopped while this VM was being copied; the next round resumes where it stopped."
			case mbCutting:
				// Never retried by itself: the guest may be shut down.
				vm.State, vm.CutoverAt = mbFailed, time.Time{}
				vm.Error = "The application stopped during the cutover. Check the source VM, then cut over again."
			}
		}
	}
	r.plans = plans
	return nil
}

// Caller holds r.mu.
func (r *mbRunner) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(r.plans, "", "  ")
	if err != nil {
		return err
	}
	return replWriteFileAtomic(r.path, raw)
}

// Caller holds r.mu.
func (r *mbRunner) planLocked(id string) (*mbPlan, error) {
	for _, plan := range r.plans {
		if plan.ID == id {
			return plan, nil
		}
	}
	return nil, errors.New("that migration plan no longer exists")
}

func mbFindVM(plan *mbPlan, id string) *mbVM {
	for _, vm := range plan.VMs {
		if vm.ID == id {
			return vm
		}
	}
	return nil
}

// Create adds a plan of the given VMs.
func (r *mbRunner) Create(name, server string, settings mbSettings, vms []mbVM) (*mbPlan, error) {
	name = strings.TrimSpace(name)
	if !replVMNamePattern.MatchString(name) {
		return nil, errors.New("name the plan with letters, digits, spaces, dots, dashes, underscores or brackets")
	}
	if err := mbValidateSettings(&settings); err != nil {
		return nil, err
	}
	if len(vms) == 0 {
		return nil, errors.New("choose at least one VM for the plan")
	}
	if len(vms) > mbMaxVMs {
		return nil, fmt.Errorf("a plan holds at most %d VMs", mbMaxVMs)
	}
	plan := &mbPlan{ID: mbNewID(), Name: name, Server: server, Created: r.now().UTC(), Settings: settings}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.plans) >= mbMaxPlans {
		return nil, fmt.Errorf("there are already %d plans; delete a finished one first", mbMaxPlans)
	}
	inPlan := map[string]string{}
	for _, other := range r.plans {
		if strings.EqualFold(other.Name, name) {
			return nil, errors.New("a plan with that name already exists")
		}
		if !strings.EqualFold(other.Server, server) {
			continue
		}
		for _, vm := range other.VMs {
			if vm.State != mbDone {
				inPlan[vm.ID] = other.Name
			}
		}
	}
	seen := map[string]bool{}
	for _, item := range vms {
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		if owner, ok := inPlan[item.ID]; ok {
			return nil, fmt.Errorf("%s is already in the plan %s", item.Name, owner)
		}
		vm := &mbVM{ID: item.ID, Name: item.Name, Wave: item.Wave, State: mbWaiting}
		if err := r.target.Check(plan, vm); err != nil {
			return nil, fmt.Errorf("%s: %v", item.Name, err)
		}
		plan.VMs = append(plan.VMs, vm)
	}
	r.plans = append(r.plans, plan)
	if err := r.saveLocked(); err != nil {
		r.plans = r.plans[:len(r.plans)-1]
		return nil, err
	}
	r.log(fmt.Sprintf("Created the migration plan %s with %d VMs.", plan.Name, len(plan.VMs)))
	return plan, nil
}

// Update changes a plan's settings; running rounds keep theirs and the
// throttle changes at once.
func (r *mbRunner) Update(id string, settings mbSettings) error {
	if err := mbValidateSettings(&settings); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, err := r.planLocked(id)
	if err != nil {
		return err
	}
	for _, vm := range plan.VMs {
		if vm.State != mbWaiting && vm.State != mbDone && !strings.EqualFold(settings.CopyRoot, plan.Settings.CopyRoot) {
			return errors.New("the copied disks' folder cannot change once VMs have been copied; make a new plan for another folder")
		}
	}
	plan.Settings = settings
	if t := r.throttles[plan.ID]; t != nil {
		t.SetSchedule(settings.Throttle)
	}
	return r.saveLocked()
}

// SetPaused pauses or resumes a plan. Pausing stops copy rounds (they
// resume where they stopped); a running cutover always finishes.
func (r *mbRunner) SetPaused(id string, paused bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, err := r.planLocked(id)
	if err != nil {
		return err
	}
	plan.Paused = paused
	if cancel := r.cancels[plan.ID]; paused && cancel != nil {
		cancel()
	}
	return r.saveLocked()
}

// SyncNow asks for a round of the given VMs (all when empty) at the next
// sweep, ahead of their interval and the plan's start time.
func (r *mbRunner) SyncNow(id string, ids []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, err := r.planLocked(id)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, v := range ids {
		want[v] = true
	}
	for _, vm := range plan.VMs {
		if (len(want) == 0 || want[vm.ID]) && (vm.State == mbWaiting || vm.State == mbReady || vm.State == mbFailed) {
			vm.SyncNow = true
		}
	}
	return r.saveLocked()
}

// mbCanCutOver says why a VM cannot be scheduled for cutover, or "".
func mbCanCutOver(vm *mbVM) string {
	switch {
	case vm.State == mbDone:
		return vm.Name + " is already cut over"
	case vm.State == mbTesting:
		return vm.Name + " has a test VM running; end the test first"
	case vm.State == mbCutting:
		return vm.Name + " is being cut over"
	case !vm.Seeded:
		return vm.Name + " has not finished its first copy yet"
	}
	return ""
}

// ScheduleCutover sets (or with a zero time clears) the cutover time of the
// given VMs. A time in the past means now.
func (r *mbRunner) ScheduleCutover(id string, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return errors.New("choose the VMs to cut over")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, err := r.planLocked(id)
	if err != nil {
		return err
	}
	chosen := []*mbVM{}
	for _, vmID := range ids {
		vm := mbFindVM(plan, vmID)
		if vm == nil {
			return errors.New("a chosen VM is no longer in the plan")
		}
		if !at.IsZero() {
			// A cutover may be scheduled before the first copy finishes,
			// so a whole migration can be set up at once; if the copy is
			// still not finished when the time comes, the cutover is
			// cancelled (mbMissedCutovers), never run late.
			if why := mbCanCutOver(vm); why != "" && !(vm.State != mbDone && vm.State != mbTesting && vm.State != mbCutting && !vm.Seeded && at.After(r.now())) {
				return errors.New(why)
			}
		}
		chosen = append(chosen, vm)
	}
	for _, vm := range chosen {
		vm.CutoverAt = at.UTC()
		if at.IsZero() {
			vm.CutoverAt = time.Time{}
		}
	}
	if !at.IsZero() {
		r.log(fmt.Sprintf("%s: %d VM(s) scheduled to cut over at %s.", plan.Name, len(chosen), at.Local().Format("2006-01-02 15:04")))
	}
	return r.saveLocked()
}

// Remove takes VMs out of a plan, or deletes the plan when ids is empty.
// Nothing on the source or the target is changed; copied disks stay.
func (r *mbRunner) Remove(id string, ids []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	plan, err := r.planLocked(id)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, v := range ids {
		want[v] = true
	}
	keep := []*mbVM{}
	for _, vm := range plan.VMs {
		if len(want) == 0 || want[vm.ID] {
			switch vm.State {
			case mbCopying, mbCutting:
				return errors.New(vm.Name + " is being copied; pause the plan and wait for the round to stop first")
			case mbTesting:
				return errors.New(vm.Name + " has a test VM running; end the test first")
			}
			continue
		}
		keep = append(keep, vm)
	}
	if len(want) == 0 {
		list := []*mbPlan{}
		for _, other := range r.plans {
			if other != plan {
				list = append(list, other)
			}
		}
		r.plans = list
		delete(r.throttles, plan.ID)
		r.log("Deleted the migration plan " + plan.Name + ". Copied disks were left in place.")
	} else {
		plan.VMs = keep
	}
	return r.saveLocked()
}

func (r *mbRunner) throttleLocked(plan *mbPlan) *vmThrottle {
	t := r.throttles[plan.ID]
	if t == nil {
		t = newVMThrottle(plan.Settings.Throttle)
		r.throttles[plan.ID] = t
	}
	return t
}

// Sweep runs the work due now and returns when it has finished. Only one
// sweep runs at a time.
func (r *mbRunner) Sweep() {
	r.mu.Lock()
	if r.sweeping {
		r.mu.Unlock()
		return
	}
	now := r.now()
	if mbMissedCutovers(r.plans, now) {
		r.saveLocked()
	}
	ready := []*mbPlan{}
	r.blocked = ""
	for _, plan := range r.plans {
		if why := r.ready(plan); why != "" {
			if !plan.Paused && len(mbDue([]*mbPlan{plan}, now, mbMaxConcurrency)) > 0 {
				r.blocked = why
			}
			continue
		}
		ready = append(ready, plan)
	}
	work := mbDue(ready, now, mbMaxConcurrency)
	if len(work) == 0 {
		r.mu.Unlock()
		return
	}
	r.sweeping = true
	r.mu.Unlock()
	release, reason := r.claim()
	r.mu.Lock()
	if reason != "" {
		r.sweeping, r.blocked = false, reason
		r.mu.Unlock()
		return
	}
	contexts := map[string]context.Context{}
	for _, item := range work {
		if contexts[item.Plan.ID] == nil {
			contexts[item.Plan.ID], r.cancels[item.Plan.ID] = context.WithCancel(context.Background())
		}
	}
	for _, item := range work {
		item.VM.State = mbCopying
		if item.Cutover {
			item.VM.State = mbCutting
		}
		item.VM.LastAttempt, item.VM.SyncNow, item.VM.Error = now.UTC(), false, ""
		r.live[item.Plan.ID+"|"+item.VM.ID] = &mbLive{Started: now, Cutover: item.Cutover, Disks: map[string]vmCopyProgress{}}
	}
	r.saveLocked()
	r.mu.Unlock()

	var wait sync.WaitGroup
	for _, item := range work {
		wait.Add(1)
		go func(item mbWork) {
			defer wait.Done()
			r.run(contexts[item.Plan.ID], item)
		}(item)
	}
	wait.Wait()
	release()
	r.mu.Lock()
	for id, cancel := range r.cancels {
		cancel()
		delete(r.cancels, id)
	}
	r.sweeping = false
	r.saveLocked()
	r.mu.Unlock()
}

// run does one VM's round or cutover and records the outcome.
func (r *mbRunner) run(ctx context.Context, item mbWork) {
	plan, vm := item.Plan, item.VM
	key := plan.ID + "|" + vm.ID
	r.mu.Lock()
	throttle := r.throttleLocked(plan)
	snapshot := *plan
	vmCopy := *vm
	r.mu.Unlock()
	log := func(text string) { r.log(vmCopy.Name + ": " + text) }
	progress := func(disk string, p vmCopyProgress) {
		r.mu.Lock()
		if live := r.live[key]; live != nil {
			live.Disks[disk] = p
		}
		r.mu.Unlock()
	}
	started := r.now()
	var result *vmMoveResult
	var err error
	var created string
	var notes []string
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("internal error: %v", p)
			}
		}()
		if item.Cutover {
			// A cutover is never stopped half way by a pause: the guest may
			// already be shut down.
			log("Cutting over.")
			result, err = r.target.Round(context.Background(), &snapshot, &vmCopy, true, nil, progress, log)
			if err == nil {
				created, notes, err = r.target.Create(context.Background(), &snapshot, &vmCopy, result, log)
				if err != nil {
					err = errors.New("the disks are copied and the source VM is shut down, but the new VM was not created: " + err.Error() + ". Start the source VM again to go back, or fix the cause and cut over again")
				}
			}
			return
		}
		result, err = r.target.Round(ctx, &snapshot, &vmCopy, false, throttle, progress, log)
	}()
	finished := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, key)
	if result != nil && result.Round != nil {
		vm.LastRead, vm.LastWritten = 0, 0
		modes := []string{}
		for _, disk := range result.Round.Disks {
			vm.LastRead += disk.BytesRead
			vm.LastWritten += disk.BytesWritten
			modes = append(modes, disk.Mode)
		}
		vm.LastMode = strings.Join(mbUnique(modes), ", ")
		if item.Cutover {
			vm.LastMode = "cutover (" + vm.LastMode + ")"
		}
		vm.LastSeconds = int64(finished.Sub(started).Seconds())
	}
	if vmCopy.CopyFolder != "" {
		vm.CopyFolder = vmCopy.CopyFolder
	}
	switch {
	case err == nil && item.Cutover:
		vm.State, vm.HyperVID, vm.CutOver, vm.CutoverAt, vm.Failures = mbDone, created, finished.UTC(), time.Time{}, 0
		vm.Disks = result.Disks
		vm.Rounds++
		vm.LastSync = finished.UTC()
		vm.Notes = append(vm.Notes, notes...)
		r.log(fmt.Sprintf("%s: cut over; the new VM was created.", vm.Name))
	case err == nil:
		vm.State, vm.Seeded, vm.Failures, vm.LastSync = mbReady, true, 0, finished.UTC()
		vm.Disks = result.Disks
		vm.Rounds++
	case errors.Is(err, context.Canceled):
		// Paused: not a failure; the next round resumes.
		vm.State = mbWaiting
		if vm.Seeded {
			vm.State = mbReady
		}
		vm.LastAttempt = time.Time{}
		if !vm.LastSync.IsZero() {
			vm.LastAttempt = vm.LastSync
		}
		vm.SyncNow = !plan.Paused
		r.log(vm.Name + ": stopped; the next round resumes where this one stopped.")
	default:
		vm.State, vm.Error = mbFailed, err.Error()
		vm.Failures++
		if item.Cutover {
			// Never retried by itself: someone must look at the source.
			vm.CutoverAt = time.Time{}
			if result != nil && result.ShutDown {
				vm.Notes = append(vm.Notes, finished.Local().Format("2006-01-02 15:04")+": the cutover stopped after the source was shut down.")
			}
		}
		r.log(vm.Name + ": " + err.Error())
	}
}

func mbUnique(list []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range list {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// StartTest makes a test VM for one VM in sync. Copy rounds for that VM are
// held while the test runs: they would change the test disks' parents.
func (r *mbRunner) StartTest(ctx context.Context, id, vmID string) error {
	r.mu.Lock()
	plan, err := r.planLocked(id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	vm := mbFindVM(plan, vmID)
	switch {
	case vm == nil:
		r.mu.Unlock()
		return errors.New("that VM is no longer in the plan")
	case vm.State == mbTesting:
		r.mu.Unlock()
		return errors.New(vm.Name + " already has a test VM")
	case vm.State != mbReady:
		r.mu.Unlock()
		return errors.New(vm.Name + " must be in sync (its last round finished) to test it")
	case !vm.CutoverAt.IsZero():
		r.mu.Unlock()
		return errors.New(vm.Name + " is scheduled to cut over; cancel that first")
	}
	vm.State = mbTesting
	snapshot, vmCopy := *plan, *vm
	r.saveLocked()
	r.mu.Unlock()
	test, err := r.target.StartTest(ctx, &snapshot, &vmCopy, func(text string) { r.log(vmCopy.Name + ": " + text) })
	r.mu.Lock()
	defer r.mu.Unlock()
	if test != nil {
		// Recorded even when a later step failed, so End test removes it.
		vm.Test = test
	}
	if err != nil {
		if vm.Test == nil {
			vm.State = mbReady
		}
		r.saveLocked()
		return err
	}
	r.log(fmt.Sprintf("%s: test VM %s started with its network %s.", vm.Name, test.Name, map[bool]string{true: "on " + plan.Settings.TestSwitch, false: "disconnected"}[plan.Settings.TestSwitch != ""]))
	return r.saveLocked()
}

// EndTest removes the test VM and its differencing disks; syncs resume.
func (r *mbRunner) EndTest(ctx context.Context, id, vmID string) error {
	r.mu.Lock()
	plan, err := r.planLocked(id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	vm := mbFindVM(plan, vmID)
	if vm == nil || vm.Test == nil {
		r.mu.Unlock()
		return errors.New("that VM has no test VM")
	}
	snapshot, vmCopy := *plan, *vm
	r.mu.Unlock()
	if err := r.target.EndTest(ctx, &snapshot, &vmCopy, func(text string) { r.log(vmCopy.Name + ": " + text) }); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	vm.Test, vm.State = nil, mbReady
	r.log(vm.Name + ": test ended; the test VM and its test disks were removed and syncs resume.")
	return r.saveLocked()
}

// Busy reports whether a sweep is running (a critical operation).
func (r *mbRunner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sweeping
}

// Stop stops running copy rounds (cutovers finish).
func (r *mbRunner) Stop() {
	r.mu.Lock()
	for _, cancel := range r.cancels {
		cancel()
	}
	r.mu.Unlock()
}

type mbVMView struct {
	mbVM
	NextSync time.Time   `json:"nextSync,omitempty"`
	Live     *mbLiveView `json:"live,omitempty"`
}

type mbPlanView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Server   string     `json:"server"`
	Created  time.Time  `json:"created"`
	Paused   bool       `json:"paused"`
	Settings mbSettings `json:"settings"`
	Throttle string     `json:"throttle"`
	Summary  mbSummary  `json:"summary"`
	// Blocked says why due work cannot run now ("" when it can).
	Blocked string     `json:"blocked,omitempty"`
	VMs     []mbVMView `json:"vms"`
}

// View is the dashboard state.
func (r *mbRunner) View() ([]mbPlanView, bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	views := []mbPlanView{}
	for _, plan := range r.plans {
		view := mbPlanView{ID: plan.ID, Name: plan.Name, Server: plan.Server, Created: plan.Created, Paused: plan.Paused, Settings: plan.Settings, Throttle: plan.Settings.Throttle.Describe(), Summary: mbSummarize(plan), VMs: []mbVMView{}}
		if !plan.Paused {
			view.Blocked = r.ready(plan)
		}
		for _, vm := range plan.VMs {
			item := mbVMView{mbVM: *vm}
			if !plan.Paused {
				item.NextSync = mbNextSync(plan, vm)
				if item.NextSync.IsZero() && (vm.State == mbWaiting || vm.State == mbReady || vm.State == mbFailed) {
					item.NextSync = now
				}
			}
			if live := r.live[plan.ID+"|"+vm.ID]; live != nil {
				v := live.view(now)
				item.Live = &v
			}
			view.VMs = append(view.VMs, item)
		}
		views = append(views, view)
	}
	return views, r.sweeping, r.blocked
}

// Loop sweeps until ctx ends.
func (r *mbRunner) Loop(ctx context.Context) {
	ticker := time.NewTicker(mbSweepEvery)
	defer ticker.Stop()
	for {
		r.Sweep()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
