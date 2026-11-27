package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

func TestProductionSettingsDefaultsValidationAndFile(t *testing.T) {
	d := defaultPGSettings()
	if err := validatePGSettings(&d); err != nil {
		t.Fatalf("the defaults must be valid: %v", err)
	}
	if d.ReservePercent != 10 || d.ReserveGB != 50 || d.MaxCopies != 4 || d.MaxPerDatastore != 2 {
		t.Fatalf("defaults %+v", d)
	}
	bad := []func(*pgSettings){
		func(s *pgSettings) { s.ReservePercent = 60 },
		func(s *pgSettings) { s.StopPercent = 20 }, // above the reserve
		func(s *pgSettings) { s.StopGB = 80 },
		func(s *pgSettings) { s.MaxCopies = 0 },
		func(s *pgSettings) { s.MaxCopies = 9 },
		func(s *pgSettings) { s.MaxPerTarget = 9 },
		func(s *pgSettings) { s.Bandwidth.LimitMbps = -1 },
		func(s *pgSettings) { s.GrowthPercent = 101 },
	}
	for i, change := range bad {
		s := defaultPGSettings()
		change(&s)
		if validatePGSettings(&s) == nil {
			t.Fatalf("bad setting %d was accepted", i)
		}
	}
	dir := t.TempDir()
	if got := loadPGSettings(dir); got.ReserveGB != 50 || got.MaxCopies != 4 {
		t.Fatalf("a missing file must give the defaults, got %+v", got)
	}
	s := defaultPGSettings()
	s.ReserveGB, s.MaxCopies, s.Bandwidth = 200, 2, vmThrottleSchedule{LimitMbps: 500, BusinessMbps: 100, BusinessStart: 8, BusinessEnd: 18}
	if err := savePGSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	if got := loadPGSettings(dir); got.ReserveGB != 200 || got.MaxCopies != 2 || got.Bandwidth.BusinessMbps != 100 {
		t.Fatalf("saved settings read back as %+v", got)
	}
	os.WriteFile(pgSettingsPath(dir), []byte(`{"schema":1,"reservePercent":99}`), 0600)
	if got := loadPGSettings(dir); got.ReservePercent != 10 || got.ReserveGB != 50 {
		t.Fatal("a damaged file must give the defaults")
	}
}

func TestProductionReserveStopAndGrowth(t *testing.T) {
	s := defaultPGSettings()
	tb := int64(1) << 40
	if got := s.Reserve(tb); got != tb/100*10 {
		t.Fatalf("10%% of 1 TB is the reserve, got %d", got)
	}
	if got := s.Reserve(200 * pgGiB); got != 50*pgGiB {
		t.Fatalf("50 GB beats 10%% of 200 GB, got %d", got)
	}
	if got := s.Reserve(60 * pgGiB); got != 30*pgGiB {
		t.Fatalf("a small volume keeps half of itself, not more than it holds, got %d", got)
	}
	if got := s.StopLevel(tb); got != tb/100*5 {
		t.Fatalf("stop level %d", got)
	}
	if got := s.Growth(10 * pgGiB); got != 4*pgGiB {
		t.Fatalf("a small VM's growth is at least 4 GB, got %d", got)
	}
	if got := s.Growth(500 * pgGiB); got != 50*pgGiB {
		t.Fatalf("growth is 10%% of the disks, got %d", got)
	}
	if s.Growth(0) != 0 {
		t.Fatal("no disks there, no growth")
	}
}

func TestProductionGuardHoldsAndStaggersSnapshots(t *testing.T) {
	g := newPGGuard(defaultPGSettings())
	tb := int64(1) << 40
	store := func(free int64) pgVolume {
		return pgVolume{Key: "datastore:ds1", Label: "DS-PROD-01", Free: free, Capacity: tb, VMBytes: 400 * pgGiB}
	}
	// 1 TB, 200 GB free: the reserve is ~102 GB and each VM may grow 40 GB.
	first, err := g.Admit("APP01", []pgVolume{store(200 * pgGiB)})
	if err != nil || first == nil {
		t.Fatalf("the first copy must start: %v", err)
	}
	second, err := g.Admit("APP02", []pgVolume{store(200 * pgGiB)})
	if err != nil {
		t.Fatalf("the second copy fits too: %v", err)
	}
	// The third would leave 200-40-40-40 = 80 GB, under the reserve.
	_, err = g.Admit("APP03", []pgVolume{store(200 * pgGiB)})
	hold, ok := pgIsHold(err)
	if !ok || hold.Stopped {
		t.Fatalf("the third copy must wait, got %v", err)
	}
	for _, want := range []string{"DS-PROD-01", "2 running copies", "kept free for production", "tries again"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the reason %q does not say %q", err, want)
		}
	}
	view := g.View()
	if len(view.Holds) != 1 || view.Holds[0].Owner != "APP03" || view.Holds[0].Kind != "space" || len(view.Active) != 2 {
		t.Fatalf("view %+v", view)
	}
	// Once one copy ends, the waiting one starts and its hold clears.
	first.Release()
	third, err := g.Admit("APP03", []pgVolume{store(200 * pgGiB)})
	if err != nil {
		t.Fatalf("after a release the copy must start: %v", err)
	}
	if len(g.View().Holds) != 0 {
		t.Fatal("the hold was not cleared")
	}
	second.Release()
	third.Release()
	// The destination volume is checked for its reserve but grows by the
	// copy, not a snapshot.
	if _, err := g.Admit("APP04", []pgVolume{{Key: "target:c", Label: `C:\ClusterStorage\Volume1`, Free: 60 * pgGiB, Capacity: tb, Target: true}}); err == nil || !strings.Contains(err.Error(), "destination volume") {
		t.Fatalf("a full destination must hold the copy, got %v", err)
	}
	// A nil guard lets everything through.
	var none *pgGuard
	if claim, err := none.Admit("x", []pgVolume{store(0)}); claim != nil || err != nil {
		t.Fatal("a nil guard must not hold")
	}
}

func TestProductionGuardStopsARoundAtTheStopLevel(t *testing.T) {
	g := newPGGuard(defaultPGSettings())
	g.every = 5 * time.Millisecond
	tb := int64(1) << 40
	claim, err := g.Admit("SQL01", []pgVolume{{Key: "datastore:ds1", Label: "DS-SQL", Free: 400 * pgGiB, Capacity: tb, VMBytes: 100 * pgGiB}})
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	free := int64(400) * pgGiB
	reads := make(chan struct{}, 100)
	logs := []string{}
	claim.Watch(ctx, cancel, func(context.Context) ([]pgVolume, error) {
		reads <- struct{}{}
		return []pgVolume{{Key: "datastore:ds1", Label: "DS-SQL", Free: free, Capacity: tb}}, nil
	}, func(s string) { logs = append(logs, s) })
	<-reads
	select {
	case <-ctx.Done():
		t.Fatal("the round was stopped while space was plentiful")
	default:
	}
	free = 30 * pgGiB // under 5% of 1 TB
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the round was not stopped at the stop level")
	}
	err = claim.Wrap(context.Canceled)
	hold, ok := pgIsHold(err)
	if !ok || !hold.Stopped || !strings.Contains(err.Error(), "snapshot was removed") {
		t.Fatalf("a stopped round must be a hold, got %v", err)
	}
	if v := g.View(); len(v.Holds) != 1 || v.Holds[0].Kind != "stopped" {
		t.Fatalf("holds %+v", v.Holds)
	}
	if claim.Wrap(nil) != nil {
		t.Fatal("a round that finished is not turned into a hold")
	}
}

func TestProductionCopyCaps(t *testing.T) {
	s := defaultPGSettings()
	s.MaxCopies, s.MaxPerDatastore, s.MaxPerSourceHost, s.MaxPerTarget = 3, 1, 2, 2
	g := newPGGuard(s)
	a := pgSlotKeys{Datastores: []string{"dc\x00DS1"}, SourceHost: "esx01", Target: "hv01"}
	b := pgSlotKeys{Datastores: []string{"dc\x00DS1"}, SourceHost: "esx02", Target: "hv01"}
	c := pgSlotKeys{Datastores: []string{"dc\x00DS2"}, SourceHost: "esx01", Target: "hv01"}
	d := pgSlotKeys{Datastores: []string{"dc\x00DS3"}, SourceHost: "esx03", Target: "hv02"}
	releaseA, why := g.TryAcquire("A", a)
	if why != "" {
		t.Fatal(why)
	}
	if _, why := g.TryAcquire("B", b); !strings.Contains(why, "datastore DS1") {
		t.Fatalf("one copy per datastore: %q", why)
	}
	releaseC, why := g.TryAcquire("C", c)
	if why != "" {
		t.Fatal(why)
	}
	if _, why := g.TryAcquire("C2", pgSlotKeys{SourceHost: "esx01"}); !strings.Contains(why, "host esx01") {
		t.Fatalf("two copies per source host: %q", why)
	}
	if _, why := g.TryAcquire("E", pgSlotKeys{Target: "hv01"}); !strings.Contains(why, "write to hv01") {
		t.Fatalf("two copies per destination: %q", why)
	}
	releaseD, why := g.TryAcquire("D", d)
	if why != "" {
		t.Fatal(why)
	}
	if _, why := g.TryAcquire("F", pgSlotKeys{}); !strings.Contains(why, "at most 3") {
		t.Fatalf("three copies in all: %q", why)
	}
	if h := g.View().Holds; len(h) == 0 {
		t.Fatal("queued copies must be listed")
	}
	// Acquire waits for a slot and takes it when one frees.
	done := make(chan error, 1)
	go func() {
		release, err := g.Acquire(context.Background(), "B", b, nil)
		if err == nil {
			release()
		}
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	releaseA()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Acquire did not take the freed slot")
	}
	releaseC()
	releaseD()
	// Acquire stops with its context.
	releaseX, _ := g.TryAcquire("X", a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Acquire(ctx, "Y", b, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire must stop with its context, got %v", err)
	}
	releaseX()
	// The planner counts the copies it already let through in one sweep.
	p := g.Planner()
	if p.Try(a) != "" || p.Try(b) == "" || p.Try(c) != "" || p.Try(d) != "" || p.Try(pgSlotKeys{}) == "" {
		t.Fatal("the planner must apply the caps to copies starting together")
	}
}

func TestThrottleAllWaitsForEveryLimit(t *testing.T) {
	clock := time.Date(2026, 11, 27, 22, 0, 0, 0, time.UTC)
	waited := time.Duration(0)
	make := func(mbps int) *vmThrottle {
		th := newVMThrottle(vmThrottleSchedule{LimitMbps: mbps})
		th.now = func() time.Time { return clock }
		th.until = func(ctx context.Context, deadline time.Time) error {
			if d := deadline.Sub(clock); d > waited {
				waited = d
			}
			return nil
		}
		return th
	}
	plan, global := make(800), make(80)
	both := vmThrottleAll(plan, nil, global)
	if both == plan || both == global {
		t.Fatal("two throttles need a combined one")
	}
	// 20 MB at 80 Mbps is 2 seconds, less the one-second burst.
	if err := both.Wait(context.Background(), 20*1000*1000); err != nil {
		t.Fatal(err)
	}
	if waited < 900*time.Millisecond {
		t.Fatalf("the lower (global) limit was not applied: waited %v", waited)
	}
	if vmThrottleAll(nil, nil) != nil || vmThrottleAll(plan) != plan {
		t.Fatal("vmThrottleAll must pass a single throttle through")
	}
}

func TestMigrateBandwidthIsTheLowerLimit(t *testing.T) {
	night := time.Date(2026, 11, 27, 23, 0, 0, 0, time.Local)
	day := time.Date(2026, 11, 27, 10, 0, 0, 0, time.Local) // a Friday
	own := vmThrottleSchedule{LimitMbps: 0, BusinessMbps: 200, BusinessStart: 8, BusinessEnd: 18}
	global := vmThrottleSchedule{LimitMbps: 900}
	if got := migrateBandwidthMbps(day, own, global); got != 200 {
		t.Fatalf("day %d", got)
	}
	if got := migrateBandwidthMbps(night, own, global); got != 900 {
		t.Fatalf("night %d", got)
	}
	if got := migrateBandwidthMbps(night, own, vmThrottleSchedule{}); got != 0 {
		t.Fatalf("no limit %d", got)
	}
}

func TestPlanSweepsFollowWaveLimitsCapsAndHolds(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	plan := &mbPlan{ID: "p", Name: "Wave plan", Settings: mbSettings{SyncMinutes: 60, Concurrency: 4, Waves: []mbWaveLimit{{Wave: 1, Concurrency: 1}}}}
	for _, name := range []string{"A1", "A2", "B1", "B2"} {
		wave := 1
		if name[0] == 'B' {
			wave = 2
		}
		plan.VMs = append(plan.VMs, &mbVM{ID: name, Name: name, Wave: wave, State: mbWaiting})
	}
	if err := mbValidateSettings(&plan.Settings); err != nil {
		t.Fatal(err)
	}
	work := mbDue([]*mbPlan{plan}, now, 4, nil)
	names := []string{}
	for _, w := range work {
		names = append(names, w.VM.Name)
	}
	if strings.Join(names, ",") != "A1,B1,B2" {
		t.Fatalf("wave 1 copies one VM at once: %v", names)
	}
	if !strings.Contains(plan.VMs[1].Waiting, "wave 1 copies at most 1 VM") {
		t.Fatalf("the waiting VM must say why: %q", plan.VMs[1].Waiting)
	}
	// The production guard's caps hold copies back too, with the reason.
	work = mbDue([]*mbPlan{plan}, now, 4, func(item mbWork) string {
		if item.VM.Name == "B2" {
			return "Waiting: 2 copies already read datastore DS1"
		}
		return ""
	})
	if len(work) != 2 || plan.VMs[3].Waiting == "" {
		t.Fatalf("work %d, B2 waiting %q", len(work), plan.VMs[3].Waiting)
	}
	// Bad wave limits are refused.
	for _, waves := range [][]mbWaveLimit{{{Wave: 1}, {Wave: 1}}, {{Wave: 1, Concurrency: 5}}, {{Wave: -1}}, {{Wave: 1, Throttle: vmThrottleSchedule{LimitMbps: -1}}}} {
		s := mbSettings{Waves: waves}
		if mbValidateSettings(&s) == nil {
			t.Fatalf("wave limits %+v were accepted", waves)
		}
	}
	// A held VM waits pgRetryAfter, not the sync interval or a failure backoff.
	vm := &mbVM{ID: "H", Name: "H", State: mbReady, Seeded: true, LastAttempt: now, Held: "Waiting for space", HeldAt: now}
	if got := mbNextSync(plan, vm); !got.Equal(now.Add(pgRetryAfter)) {
		t.Fatalf("a held VM is due at %v", got)
	}
}

type pgHoldTarget struct{ mbTarget }

func (pgHoldTarget) Round(ctx context.Context, plan *mbPlan, vm *mbVM, cutover bool, throttle *vmThrottle, progress func(string, vmCopyProgress), log func(string)) (*vmMoveResult, error) {
	return nil, &pgHoldError{Reason: "Waiting for space on datastore DS1"}
}

func TestPlanRoundHeldForSpaceIsNotAFailure(t *testing.T) {
	r := newMBRunner(filepath.Join(t.TempDir(), "plans.json"), pgHoldTarget{})
	plan := &mbPlan{ID: "p", Name: "P", Settings: mbSettings{SyncMinutes: 60, Concurrency: 1}}
	vm := &mbVM{ID: "v", Name: "APP01", State: mbReady, Seeded: true, Failures: 0}
	plan.VMs = []*mbVM{vm}
	r.plans = []*mbPlan{plan}
	vm.State = mbCopying
	r.run(context.Background(), mbWork{Plan: plan, VM: vm})
	if vm.State != mbReady || vm.Failures != 0 || vm.Error != "" || !strings.Contains(vm.Held, "DS1") || vm.HeldAt.IsZero() {
		t.Fatalf("a held round must leave the VM ready with the reason: %+v", vm)
	}
}

type pgDiskPlatform struct {
	replFakePlatform
	disks []replDiskInfo
}

func (p *pgDiskPlatform) ListDisks() ([]replDiskInfo, error) { return p.disks, nil }

func TestAgentRoundWaitsForSpaceOnTheServer(t *testing.T) {
	platform := &pgDiskPlatform{disks: []replDiskInfo{{ID: "0", Volumes: []replVolumeInfo{{Name: `\\?\Volume{1}`, Mounts: []string{`C:\`}, Length: 100 * pgGiB}}}}}
	a := &replAgent{platform: platform, now: time.Now}
	free := uint64(5) * uint64(pgGiB)
	a.spaceFree = func(string) (uint64, uint64, bool) { return free, uint64(100 * pgGiB), true }
	err := a.checkSpace(replJobSpec{}, []string{"0"})
	if !pgHeld(err) || !strings.Contains(err.Error(), "volume C:") || !strings.Contains(err.Error(), "shadow copy") {
		t.Fatalf("5 GB free of 100 GB must hold the round with the default 20 GB reserve: %v", err)
	}
	if err := a.checkSpace(replJobSpec{Space: &replSpace{}}, []string{"0"}); err != nil {
		t.Fatalf("0 and 0 turn the check off: %v", err)
	}
	free = 60 * uint64(pgGiB)
	if err := a.checkSpace(replJobSpec{}, []string{"0"}); err != nil {
		t.Fatalf("enough space: %v", err)
	}
	if err := a.checkSpace(replJobSpec{}, []string{"1"}); err != nil {
		t.Fatalf("other disks are not checked: %v", err)
	}
	if (&replSpace{ReservePercent: 51}).validate() == nil {
		t.Fatal("over 50% is refused")
	}
}

func TestHyperVCheckpointWaitsForSpace(t *testing.T) {
	f := newHyperVMoveFixture(t)
	request := f.request(false)
	s := defaultPGSettings()
	request.Space, request.SpaceOwner = newPGGuard(s), "APP01"
	request.Volumes = func(paths []string) []pgVolume {
		if len(paths) != 2 {
			t.Fatalf("paths %v", paths)
		}
		return []pgVolume{{Key: "volume:d:", Label: `D:\`, Free: 40 * pgGiB, Capacity: 500 * pgGiB, VMBytes: 100 * pgGiB}}
	}
	_, err := hypervMoveRound(context.Background(), f.host, request, nil)
	if !pgHeld(err) {
		t.Fatalf("a full volume must hold the round: %v", err)
	}
	if len(f.host.created) != 0 {
		t.Fatal("a checkpoint was taken although the volume was short of space")
	}
	request.Volumes = func([]string) []pgVolume {
		return []pgVolume{{Key: "volume:d:", Label: `D:\`, Free: 400 * pgGiB, Capacity: 500 * pgGiB, VMBytes: 100 * pgGiB}}
	}
	if _, err := hypervMoveRound(context.Background(), f.host, request, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.host.created) != 1 || len(f.host.removed) != 1 || len(request.Space.View().Active) != 0 {
		t.Fatal("the round must take and remove one checkpoint and release its claim")
	}
}

func TestVMwareRoundWaitsForDatastoreSpaceBeforeItsSnapshot(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	server := model.Service.NewServer()
	defer server.Close()
	ctx := context.Background()
	client, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewManager(client).Login(ctx, server.URL.User); err != nil {
		t.Fatal(err)
	}
	ref, _, _ := replicaTestVM(t, ctx, client, 80<<20)
	volumes, err := vmSnapshotVolumes(ctx, client, ref)
	if err != nil || len(volumes) == 0 || volumes[0].VMBytes != 80<<20 || volumes[0].Capacity <= 0 {
		t.Fatalf("volumes %+v, %v", volumes, err)
	}
	// A growth estimate larger than any datastore holds the round.
	s := defaultPGSettings()
	s.GrowthMinGB = 10000
	guard := newPGGuard(s)
	options := vmReplicaOptions{Datacenter: "ha-datacenter", Folder: filepath.Join(t.TempDir(), "replica"), EnableCBT: true, RecordFolder: t.TempDir(), Space: guard, SpaceOwner: "app01"}
	_, err = vmReplicateRound(ctx, client, ref, options)
	if !pgHeld(err) || !strings.Contains(err.Error(), volumes[0].Label) {
		t.Fatalf("the round must wait for datastore space: %v", err)
	}
	var machine mo.VirtualMachine
	property.DefaultCollector(client).RetrieveOne(ctx, ref, []string{"config", "snapshot"}, &machine)
	if machine.Snapshot != nil && len(machine.Snapshot.RootSnapshotList) > 0 {
		t.Fatal("a snapshot was taken although the datastore was short of space")
	}
	if machine.Config.ChangeTrackingEnabled != nil && *machine.Config.ChangeTrackingEnabled {
		t.Fatal("nothing may change on a VM whose round waits")
	}
	if guard.View().Holds[0].Owner != "app01" {
		t.Fatal("the hold must be listed")
	}
	_ = types.VirtualMachinePowerStatePoweredOn
	if vmDatastoreOfPath("[DS 1] a/b.vmdk") != "DS 1" || vmDatastoreOfPath("b.vmdk") != "" {
		t.Fatal("datastore of path")
	}
}
