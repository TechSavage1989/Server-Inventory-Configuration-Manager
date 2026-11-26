package main

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type mbFakeTarget struct {
	mu        sync.Mutex
	rounds    []string
	cutovers  []string
	created   []string
	fail      map[string]error
	createErr error
	checkErr  map[string]error
	gate      chan struct{}
	tests     []string
	ended     []string
	throttled int
}

func (f *mbFakeTarget) Check(plan *mbPlan, vm *mbVM) error { return f.checkErr[vm.Name] }

func (f *mbFakeTarget) Round(ctx context.Context, plan *mbPlan, vm *mbVM, cutover bool, throttle *vmThrottle, progress func(string, vmCopyProgress), log func(string)) (*vmMoveResult, error) {
	if f.gate != nil && !cutover {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if throttle != nil {
		f.throttled++
	}
	progress("Hard disk 1", vmCopyProgress{Chunks: 10, Done: 5, BytesRead: 100})
	vm.CopyFolder = `D:\Copies\` + vm.Name
	if cutover {
		f.cutovers = append(f.cutovers, vm.Name)
	} else {
		f.rounds = append(f.rounds, vm.Name)
	}
	if err := f.fail[vm.Name]; err != nil {
		return &vmMoveResult{ShutDown: cutover}, err
	}
	return &vmMoveResult{ShutDown: cutover, Disks: []string{vm.CopyFolder + `\disk0.vhdx`}, Round: &vmReplicaResult{Disks: []vmReplicaDiskResult{{Mode: "changed", BytesRead: 100, BytesWritten: 40}}}}, nil
}

func (f *mbFakeTarget) Create(ctx context.Context, plan *mbPlan, vm *mbVM, result *vmMoveResult, log func(string)) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", nil, f.createErr
	}
	f.created = append(f.created, vm.Name)
	return "11111111-2222-3333-4444-555555555555", nil, nil
}

func (f *mbFakeTarget) StartTest(ctx context.Context, plan *mbPlan, vm *mbVM, log func(string)) (*mbTest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tests = append(f.tests, vm.Name)
	return &mbTest{Name: vm.Name + "-TEST", VMID: "id"}, nil
}

func (f *mbFakeTarget) EndTest(ctx context.Context, plan *mbPlan, vm *mbVM, log func(string)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, vm.Name)
	return nil
}

func mbTestRunner(t *testing.T, target *mbFakeTarget) (*mbRunner, *time.Time) {
	t.Helper()
	runner := newMBRunner(filepath.Join(t.TempDir(), "plans.json"), target)
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	runner.now = func() time.Time { return now }
	if err := runner.Load(); err != nil {
		t.Fatal(err)
	}
	return runner, &now
}

func mbVMs(names ...string) []mbVM {
	list := []mbVM{}
	for i, name := range names {
		list = append(list, mbVM{ID: "vm-" + name, Name: name, Wave: i / 2})
	}
	return list
}

func TestPlanSeedsOnScheduleThenSyncsOnInterval(t *testing.T) {
	target := &mbFakeTarget{}
	runner, now := mbTestRunner(t, target)
	start := now.Add(time.Hour)
	plan, err := runner.Create("Wave 1", "vc.example.com", mbSettings{StartAt: start, SyncMinutes: 30, Concurrency: 2}, mbVMs("app01", "app02", "sql01"))
	if err != nil {
		t.Fatal(err)
	}
	runner.Sweep()
	if len(target.rounds) != 0 {
		t.Fatalf("copied before the start time: %v", target.rounds)
	}
	*now = start
	runner.Sweep()
	if strings.Join(mbSorted(target.rounds), ",") != "app01,app02" {
		t.Fatalf("first sweep should copy two VMs of wave 0, got %v", target.rounds)
	}
	runner.Sweep()
	if len(target.rounds) != 3 || target.rounds[2] != "sql01" {
		t.Fatalf("second sweep should seed sql01, got %v", target.rounds)
	}
	*now = now.Add(10 * time.Minute)
	runner.Sweep()
	if len(target.rounds) != 3 {
		t.Fatalf("synced before the interval: %v", target.rounds)
	}
	*now = start.Add(31 * time.Minute)
	runner.Sweep()
	if len(target.rounds) != 5 {
		t.Fatalf("expected app01 and app02 to sync after 30 minutes, got %v", target.rounds)
	}
	views, _, _ := runner.View()
	if views[0].Summary.Ready != 3 || views[0].Summary.Total != 3 {
		t.Fatalf("summary %+v", views[0].Summary)
	}
	if target.throttled == 0 {
		t.Fatal("copy rounds should get the plan's throttle")
	}
	// The plan survives a restart.
	again := newMBRunner(runner.path, target)
	if err := again.Load(); err != nil || len(again.plans) != 1 || again.plans[0].ID != plan.ID || !again.plans[0].VMs[0].Seeded {
		t.Fatalf("reloaded plans: %v %+v", err, again.plans)
	}
}

func TestPlanRetriesFailuresWithBackoffAndOthersCarryOn(t *testing.T) {
	target := &mbFakeTarget{fail: map[string]error{"app01": errors.New("datastore unreachable")}}
	runner, now := mbTestRunner(t, target)
	if _, err := runner.Create("P", "vc", mbSettings{SyncMinutes: 120, Concurrency: 4}, mbVMs("app01", "app02")); err != nil {
		t.Fatal(err)
	}
	runner.Sweep()
	vm := runner.plans[0].VMs[0]
	if vm.State != mbFailed || vm.Failures != 1 || !strings.Contains(vm.Error, "datastore") {
		t.Fatalf("app01 %+v", vm)
	}
	if runner.plans[0].VMs[1].State != mbReady {
		t.Fatal("one VM failing must not stop the others")
	}
	*now = now.Add(4 * time.Minute)
	runner.Sweep()
	if len(target.rounds) != 2 {
		t.Fatalf("retried too soon: %v", target.rounds)
	}
	*now = now.Add(2 * time.Minute)
	runner.Sweep()
	if len(target.rounds) != 3 {
		t.Fatalf("expected a retry after 5 minutes: %v", target.rounds)
	}
}

func TestPlanCutsOverOnScheduleAndNeverCreatesAfterAFailedFinalRound(t *testing.T) {
	target := &mbFakeTarget{}
	runner, now := mbTestRunner(t, target)
	plan, err := runner.Create("P", "vc", mbSettings{SyncMinutes: 60, Concurrency: 4}, mbVMs("app01", "app02", "app03"))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"vm-app01", "vm-app02"}
	if err := runner.ScheduleCutover(plan.ID, ids, *now); err == nil {
		t.Fatal("a VM that was never copied must not be cut over now")
	}
	runner.Sweep()
	at := now.Add(2 * time.Hour)
	if err := runner.ScheduleCutover(plan.ID, ids, at); err != nil {
		t.Fatal(err)
	}
	target.fail = map[string]error{"app02": errors.New("final round failed")}
	*now = at.Add(-time.Minute)
	runner.Sweep() // regular syncs only
	if len(target.cutovers) != 0 {
		t.Fatal("cut over early")
	}
	*now = at
	runner.Sweep()
	if strings.Join(mbSorted(target.cutovers), ",") != "app01,app02" {
		t.Fatalf("cutovers %v", target.cutovers)
	}
	if strings.Join(target.created, ",") != "app01" {
		t.Fatalf("only app01 may be created, got %v", target.created)
	}
	a1, a2 := runner.plans[0].VMs[0], runner.plans[0].VMs[1]
	if a1.State != mbDone || a1.HyperVID == "" || !a1.CutoverAt.IsZero() {
		t.Fatalf("app01 %+v", a1)
	}
	if a2.State != mbFailed || !a2.CutoverAt.IsZero() || len(a2.Notes) == 0 {
		t.Fatalf("app02 must fail, keep a note that the source was shut down and never retry by itself: %+v", a2)
	}
	*now = now.Add(3 * time.Hour)
	runner.Sweep()
	if len(target.cutovers) != 2 {
		t.Fatalf("a failed cutover was retried by itself: %v", target.cutovers)
	}
	for _, name := range target.rounds[len(target.rounds)-1:] {
		if name == "app01" {
			t.Fatal("a VM that was cut over must never be copied again")
		}
	}
}

func TestPlanCreateFailureAfterShutdownIsReported(t *testing.T) {
	target := &mbFakeTarget{createErr: errors.New("switch missing")}
	runner, _ := mbTestRunner(t, target)
	plan, _ := runner.Create("P", "vc", mbSettings{}, mbVMs("app01"))
	runner.Sweep()
	if err := runner.ScheduleCutover(plan.ID, []string{"vm-app01"}, time.Time{}.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	runner.Sweep()
	vm := runner.plans[0].VMs[0]
	if vm.State != mbFailed || !strings.Contains(vm.Error, "Start the source VM again") {
		t.Fatalf("%+v", vm)
	}
}

func TestPlanTestHoldsSyncsAndBlocksCutover(t *testing.T) {
	target := &mbFakeTarget{}
	runner, now := mbTestRunner(t, target)
	plan, _ := runner.Create("P", "vc", mbSettings{SyncMinutes: 15}, mbVMs("app01"))
	if err := runner.StartTest(context.Background(), plan.ID, "vm-app01"); err == nil {
		t.Fatal("a VM that was never copied cannot be tested")
	}
	runner.Sweep()
	if err := runner.StartTest(context.Background(), plan.ID, "vm-app01"); err != nil {
		t.Fatal(err)
	}
	if err := runner.ScheduleCutover(plan.ID, []string{"vm-app01"}, *now); err == nil {
		t.Fatal("a VM under test must not be scheduled for cutover")
	}
	*now = now.Add(time.Hour)
	runner.Sweep()
	if len(target.rounds) != 1 {
		t.Fatal("syncs must be held while the test VM runs")
	}
	if err := runner.Remove(plan.ID, nil); err == nil {
		t.Fatal("a plan with a running test must not be deleted")
	}
	if err := runner.EndTest(context.Background(), plan.ID, "vm-app01"); err != nil {
		t.Fatal(err)
	}
	runner.Sweep()
	if len(target.rounds) != 2 || len(target.ended) != 1 {
		t.Fatalf("rounds %v ended %v", target.rounds, target.ended)
	}
}

func TestPlanPauseStopsRoundsWithoutCountingFailure(t *testing.T) {
	target := &mbFakeTarget{gate: make(chan struct{})}
	runner, _ := mbTestRunner(t, target)
	plan, _ := runner.Create("P", "vc", mbSettings{}, mbVMs("app01"))
	done := make(chan struct{})
	go func() { runner.Sweep(); close(done) }()
	for !runner.Busy() {
		time.Sleep(time.Millisecond)
	}
	views, sweeping, _ := runner.View()
	if !sweeping || views[0].VMs[0].Live == nil || views[0].VMs[0].State != mbCopying {
		t.Fatalf("running state %+v", views[0].VMs[0])
	}
	if err := runner.SetPaused(plan.ID, true); err != nil {
		t.Fatal(err)
	}
	<-done
	vm := runner.plans[0].VMs[0]
	if vm.State != mbWaiting || vm.Failures != 0 || vm.Error != "" {
		t.Fatalf("pause is not a failure: %+v", vm)
	}
	runner.Sweep()
	if len(target.rounds) != 0 {
		t.Fatal("a paused plan must not copy")
	}
}

func TestPlanRefusesDuplicatesAndBadSettings(t *testing.T) {
	target := &mbFakeTarget{checkErr: map[string]error{"vcls-1": errors.New("not moved")}}
	runner, _ := mbTestRunner(t, target)
	if _, err := runner.Create("P", "vc", mbSettings{SyncMinutes: 5}, mbVMs("a")); err == nil {
		t.Fatal("5-minute syncs must be refused")
	}
	if _, err := runner.Create("P", "vc", mbSettings{Concurrency: 9}, mbVMs("a")); err == nil {
		t.Fatal("concurrency 9 must be refused")
	}
	if _, err := runner.Create("P", "vc", mbSettings{}, mbVMs("vcls-1")); err == nil {
		t.Fatal("the target's refusal must reach the operator")
	}
	if _, err := runner.Create("P", "vc", mbSettings{}, mbVMs("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Create("Q", "vc", mbSettings{}, mbVMs("a")); err == nil {
		t.Fatal("a VM must not be in two plans")
	}
	if _, err := runner.Create("p", "vc", mbSettings{}, mbVMs("b")); err == nil {
		t.Fatal("plan names must be unique")
	}
}

func TestPlanRestartMarksInterruptedWork(t *testing.T) {
	target := &mbFakeTarget{}
	runner, _ := mbTestRunner(t, target)
	runner.Create("P", "vc", mbSettings{}, mbVMs("a", "b"))
	runner.plans[0].VMs[0].State = mbCopying
	runner.plans[0].VMs[1].State, runner.plans[0].VMs[1].CutoverAt = mbCutting, time.Now()
	runner.mu.Lock()
	runner.saveLocked()
	runner.mu.Unlock()
	again := newMBRunner(runner.path, target)
	again.Load()
	a, b := again.plans[0].VMs[0], again.plans[0].VMs[1]
	if a.State != mbFailed || b.State != mbFailed || !b.CutoverAt.IsZero() || !strings.Contains(b.Error, "cutover") {
		t.Fatalf("a %+v b %+v", a, b)
	}
}

func TestPlanLiveViewEstimatesTimeLeft(t *testing.T) {
	start := time.Unix(1000, 0)
	live := &mbLive{Started: start, Disks: map[string]vmCopyProgress{"d1": {Chunks: 100, Done: 25, BytesRead: 1000}, "d2": {Chunks: 100, Done: 25, BytesRead: 1000}}}
	v := live.view(start.Add(100 * time.Second))
	if v.Percent != 25 || v.BytesPerSecond != 20 || v.SecondsLeft != 300 {
		t.Fatalf("%+v", v)
	}
}

func TestThrottledSourcePacesReads(t *testing.T) {
	waited := int64(0)
	throttle := newVMThrottle(vmThrottleSchedule{LimitMbps: 8})
	throttle.until = func(ctx context.Context, d time.Time) error { waited++; return nil }
	source := vmThrottleSource(context.Background(), &vmMemSource{data: make([]byte, 4<<20)}, throttle)
	buf := make([]byte, 2<<20)
	source.ReadAt(buf, 0)
	source.ReadAt(buf, 2<<20)
	if waited == 0 {
		t.Fatal("reads above the limit must wait")
	}
	if vmThrottleSource(context.Background(), &vmMemSource{}, nil).(*vmMemSource) == nil {
		t.Fatal("no throttle must return the source itself")
	}
}

type vmMemSource struct{ data []byte }

func (m *vmMemSource) ReadAt(p []byte, off int64) (int, error) { return copy(p, m.data[off:]), nil }
func (m *vmMemSource) Size() int64                             { return int64(len(m.data)) }
func (m *vmMemSource) Describe() string                        { return "memory" }
func (m *vmMemSource) Close() error                            { return nil }

func mbSorted(list []string) []string {
	out := append([]string(nil), list...)
	sort.Strings(out)
	return out
}

func TestPlanCutoverScheduledBeforeTheFirstCopyIsCancelledIfTheCopyIsLate(t *testing.T) {
	target := &mbFakeTarget{gate: make(chan struct{})}
	runner, now := mbTestRunner(t, target)
	plan, err := runner.Create("P", "vc", mbSettings{SyncMinutes: 60, Concurrency: 4}, mbVMs("app01"))
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(2 * time.Hour)
	if err := runner.ScheduleCutover(plan.ID, []string{"vm-app01"}, at); err != nil {
		t.Fatalf("a future cutover must be accepted before the first copy: %v", err)
	}
	*now = at
	if !mbMissedCutovers(runner.plans, *now) {
		t.Fatal("the missed cutover was not cancelled")
	}
	vm := runner.plans[0].VMs[0]
	if !vm.CutoverAt.IsZero() || len(vm.Notes) == 0 {
		t.Fatalf("expected a cleared schedule and a note: %+v", vm)
	}
	close(target.gate)
	runner.Sweep()
	if len(target.cutovers) != 0 {
		t.Fatalf("a cutover ran after its window: %v", target.cutovers)
	}
	if len(target.rounds) != 1 {
		t.Fatalf("the VM must keep copying: %v", target.rounds)
	}
}

func TestPlanCutoverScheduledBeforeTheFirstCopyRunsOnceSeeded(t *testing.T) {
	target := &mbFakeTarget{}
	runner, now := mbTestRunner(t, target)
	plan, err := runner.Create("P", "vc", mbSettings{SyncMinutes: 60, Concurrency: 4}, mbVMs("app01"))
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(3 * time.Hour)
	if err := runner.ScheduleCutover(plan.ID, []string{"vm-app01"}, at); err != nil {
		t.Fatal(err)
	}
	runner.Sweep() // seeds
	*now = at
	runner.Sweep()
	if strings.Join(target.cutovers, ",") != "app01" || strings.Join(target.created, ",") != "app01" {
		t.Fatalf("cutovers %v created %v", target.cutovers, target.created)
	}
}
