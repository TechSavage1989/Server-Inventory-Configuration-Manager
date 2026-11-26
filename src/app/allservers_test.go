package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type asFakeAgent struct {
	calls   []string
	disks   []replDiskInfo
	saved   []replJobSpec
	failing bool
}

func (f *asFakeAgent) Hello(ctx context.Context) (replAgentHello, error) {
	return replAgentHello{}, nil
}
func (f *asFakeAgent) Disks(ctx context.Context) ([]replDiskInfo, error) {
	f.calls = append(f.calls, "disks")
	return f.disks, nil
}
func (f *asFakeAgent) Jobs(ctx context.Context) (replAgentJobs, error) { return replAgentJobs{}, nil }
func (f *asFakeAgent) SaveJob(ctx context.Context, spec replJobSpec) (replJobView, error) {
	f.saved = append(f.saved, spec)
	return replJobView{Spec: spec}, nil
}
func (f *asFakeAgent) Action(ctx context.Context, id, action string) error {
	if f.failing {
		return errors.New("the agent refused")
	}
	f.calls = append(f.calls, action+" "+id)
	return nil
}
func (f *asFakeAgent) Schedule(ctx context.Context, id string, at *time.Time) error {
	if at == nil {
		f.calls = append(f.calls, "unschedule "+id)
	} else {
		f.calls = append(f.calls, "schedule "+id+" "+at.UTC().Format("15:04"))
	}
	return nil
}
func (f *asFakeAgent) Log(ctx context.Context, id string) ([]string, error) { return nil, nil }

type asFakeJobs map[string]*asFakeAgent

func (f asFakeJobs) Client(id string) (replAgentAPI, error) {
	if a, ok := f[id]; ok {
		return a, nil
	}
	return nil, errors.New("that agent is not listed")
}

type asFakePlans struct{ calls []string }

func (p *asFakePlans) SetPaused(id string, paused bool) error {
	p.calls = append(p.calls, "paused "+id+" "+map[bool]string{true: "yes", false: "no"}[paused])
	return nil
}
func (p *asFakePlans) SyncNow(id string, ids []string) error {
	p.calls = append(p.calls, "sync "+id+" "+strings.Join(ids, ","))
	return nil
}
func (p *asFakePlans) ScheduleCutover(id string, ids []string, at time.Time) error {
	if at.IsZero() {
		p.calls = append(p.calls, "unschedule "+id+" "+strings.Join(ids, ","))
	} else {
		p.calls = append(p.calls, "cutover "+id+" "+strings.Join(ids, ",")+" "+at.UTC().Format("15:04"))
	}
	return nil
}
func (p *asFakePlans) StartTest(ctx context.Context, id, vmID string) error {
	p.calls = append(p.calls, "test "+id+" "+vmID)
	return nil
}
func (p *asFakePlans) EndTest(ctx context.Context, id, vmID string) error {
	p.calls = append(p.calls, "endtest "+id+" "+vmID)
	return nil
}
func (p *asFakePlans) SetWave(id string, ids []string, wave int) error {
	p.calls = append(p.calls, "wave "+id+" "+strings.Join(ids, ","))
	return nil
}

func asTestInput(now time.Time) asInput {
	seededAt := now.Add(-2 * time.Hour)
	job := func(id, name, state string, seeded bool, rpo int64) replJobView {
		v := replJobView{Spec: replJobSpec{ID: id, Name: name, RPOMinutes: 15, Disks: []replDiskMap{{Source: "0", Target: name + "-disk0.vhdx"}}}, Status: replJobStatus{State: state}, RPOSecond: rpo}
		if seeded {
			v.Status.InitialCopyAt, v.Status.RecoveryPoint, v.Status.Rounds = seededAt, now.Add(-time.Duration(rpo)*time.Second), 3
		}
		return v
	}
	syncing := job("j3", "FS01", replStateSyncing, false, 0)
	syncing.Status.Disks = []replDiskStatus{{Chunks: 100, Done: 25, Read: 1 << 30}}
	return asInput{
		Now: now,
		Agents: []replAgentRow{
			{ID: "a1", Name: "APP01", Address: "app01.example.com:44390", Live: replAgentLive{Reachable: true, Computer: "APP01", Jobs: []replJobView{job("j1", "APP01", replStateIdle, true, 120)}}},
			{ID: "a2", Name: "SQL01", Address: "192.0.2.20:44390", Live: replAgentLive{Reachable: true, Jobs: []replJobView{job("j2", "SQL01", replStateIdle, true, 3600)}}},
			{ID: "a3", Name: "FS01", Address: "fs01:44390", Live: replAgentLive{Reachable: true, Jobs: []replJobView{syncing}}},
			{ID: "a4", Name: "WEB01", Address: "web01:44390", Live: replAgentLive{Reachable: true}},
			{ID: "a5", Name: "OLD01", Address: "old01:44390", Live: replAgentLive{Reachable: false, Hint: "The server did not answer in time."}},
		},
		Plans: []mbPlanView{{ID: "p1", Name: "Wave 1", Settings: mbSettings{SyncMinutes: 60}, VMs: []mbVMView{
			{mbVM: mbVM{ID: "vm-10", Name: "DC01", Wave: 1, State: mbReady, Seeded: true, LastSync: now.Add(-20 * time.Minute)}},
			{mbVM: mbVM{ID: "vm-11", Name: "PRINT01", Wave: 2, State: mbCopying}, Live: &mbLiveView{Percent: 40, BytesPerSecond: 1 << 20, SecondsLeft: 90}},
			{mbVM: mbVM{ID: "vm-12", Name: "TEST01", Wave: 1, State: mbTesting, Seeded: true, Test: &mbTest{Name: "TEST01-TEST"}}},
		}}},
		Transfers: []vmTransfer{
			{ID: "move-vm-20|Hard disk 1", Kind: "move", Source: "MAIL01 (Hard disk 1)", Target: "Hyper-V", State: "running", Chunks: 10, Done: 5},
			{ID: "move-vm-10|Hard disk 1", Kind: "move", Source: "DC01 (Hard disk 1)", State: "running"},
		},
		Inventory: &vmInventory{Server: "vcenter.example.com", VMs: []vmMachine{
			{ID: "vm-10", Name: "DC01"},
			{ID: "vm-20", Name: "MAIL01"},
			{ID: "vm-30", Name: "app01", GuestHostName: "app01.example.com"},
			{ID: "vm-31", Name: "NEW01", GuestHostName: "new01.example.com", PowerState: "poweredOn"},
			{ID: "vm-32", Name: "OFF01", PowerState: "poweredOff"},
			{ID: "vm-33", Name: "vCLS-1", Readiness: []vmReadiness{{Target: "hyperv", Status: vmStatusSkip}}},
			{ID: "vm-34", Name: "TPL", Template: true},
		}},
		JobWaves: map[string]int{"a2|j2": 3},
		VMWaves:  map[string]int{"vm-31": 2},
		Testing:  map[string]bool{},
	}
}

func asByName(rows []asRow) map[string]asRow {
	out := map[string]asRow{}
	for _, r := range rows {
		out[r.Name] = r
	}
	return out
}

func asHas(row asRow, action string) bool {
	for _, a := range row.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func TestAllServersListsEveryMethodWithOneStateEach(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	rows, summary := asBuild(asTestInput(now))
	by := asByName(rows)
	want := map[string]string{"APP01": asInSync, "SQL01": asBehind, "FS01": asSeeding, "WEB01": asAgent, "old01:44390": "", "OLD01": asUnreachable,
		"DC01": asInSync, "PRINT01": asSeeding, "TEST01": asTesting, "MAIL01": asSeeding, "NEW01": asNotConfigured, "OFF01": asNotConfigured}
	for name, state := range want {
		if state == "" {
			continue
		}
		row, ok := by[name]
		if !ok {
			t.Fatalf("%s is missing from %+v", name, rows)
		}
		if row.State != state || row.StateText == "" || row.Method == "" {
			t.Fatalf("%s: state %q (%q, %q), want %q", name, row.State, row.StateText, row.Method, state)
		}
	}
	// Covered by an agent, a plan or a move: not listed again from the
	// inventory; system VMs and templates never.
	if len(rows) != 11 {
		t.Fatalf("%d rows: %+v", len(rows), rows)
	}
	for _, name := range []string{"app01", "vCLS-1", "TPL"} {
		if _, ok := by[name]; ok {
			t.Fatalf("%s was listed", name)
		}
	}
	if by["DC01"].Kind != asKindPlan || by["MAIL01"].Kind != asKindMove || by["MAIL01"].Percent != 50 {
		t.Fatalf("plan/move rows: %+v %+v", by["DC01"], by["MAIL01"])
	}
	if by["FS01"].Percent != 25 || by["FS01"].BytesRead != 1<<30 || by["PRINT01"].BytesPerSecond != 1<<20 || by["PRINT01"].SecondsLeft != 90 {
		t.Fatalf("progress: %+v %+v", by["FS01"], by["PRINT01"])
	}
	if by["APP01"].LagSeconds != 120 || by["DC01"].LagSeconds != 1200 || by["FS01"].LagSeconds != -1 {
		t.Fatalf("lag: %d %d %d", by["APP01"].LagSeconds, by["DC01"].LagSeconds, by["FS01"].LagSeconds)
	}
	if by["SQL01"].Wave != 3 || by["NEW01"].Wave != 2 || by["DC01"].Wave != 1 {
		t.Fatal("waves were not carried over")
	}
	// Actions follow the state.
	if !asHas(by["APP01"], asActCutover) || !asHas(by["APP01"], asActTest) || asHas(by["FS01"], asActCutover) || asHas(by["FS01"], asActTest) {
		t.Fatal("cutover/test offered wrongly for agent jobs")
	}
	if !asHas(by["WEB01"], asActCreateJob) || len(by["OLD01"].Actions) != 0 {
		t.Fatalf("agent rows: %+v %+v", by["WEB01"], by["OLD01"])
	}
	if !asHas(by["NEW01"], asActInstall) || asHas(by["OFF01"], asActInstall) {
		t.Fatal("install offered wrongly")
	}
	if !asHas(by["TEST01"], asActEndTest) || asHas(by["TEST01"], asActCutover) || asHas(by["TEST01"], asActSync) || !asHas(by["DC01"], asActTest) {
		t.Fatal("plan test actions")
	}
	if summary.Total != 11 || summary.States[asInSync] != 2 || summary.States[asSeeding] != 3 || summary.States[asNotConfigured] != 2 || summary.States[asCutOver] != 0 {
		t.Fatalf("summary %+v", summary)
	}
	// Waves first, in order; no wave last.
	if rows[0].Wave != 1 || rows[len(rows)-1].Wave != 0 {
		t.Fatalf("order: first %+v last %+v", rows[0], rows[len(rows)-1])
	}
	// A test running from a job's copies.
	in := asTestInput(now)
	in.Testing["j1"] = true
	rows, _ = asBuild(in)
	if app := asByName(rows)["APP01"]; app.State != asTesting || !asHas(app, asActEndTest) || asHas(app, asActCutover) {
		t.Fatalf("testing job %+v", app)
	}
}

func TestAllServersBulkActionsUseEachMethodAndSkipWhatDoesNotApply(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	rows, _ := asBuild(asTestInput(now))
	agents := asFakeJobs{"a1": {}, "a2": {}, "a3": {}, "a4": {disks: []replDiskInfo{{ID: "1", Size: 1 << 30}, {ID: "0", Size: 1 << 34, System: true}}}}
	plans := &asFakePlans{}
	waves := newASWaveStore(filepath.Join(t.TempDir(), "waves.json"))
	runner := &asRunner{jobs: agents, plans: plans, waves: waves, now: func() time.Time { return now }}
	keyOf := map[string]string{}
	for _, r := range rows {
		keyOf[r.Name] = r.Key
	}
	keys := func(names ...string) []string {
		out := []string{}
		for _, n := range names {
			out = append(out, keyOf[n])
		}
		return out
	}

	// Sync: FS01's agent, DC01's plan; TEST01 (testing) is skipped.
	results := runner.Run(context.Background(), asBulkRequest{Action: asActSync, Keys: keys("FS01", "DC01", "TEST01")}, rows, nil)
	if len(results) != 2 || agents["a3"].calls[0] != "sync j3" || plans.calls[0] != "sync p1 vm-10" {
		t.Fatalf("sync: %+v %v %v", results, agents["a3"].calls, plans.calls)
	}

	// Pause a plan once, however many of its VMs are chosen.
	plans.calls = nil
	results = runner.Run(context.Background(), asBulkRequest{Action: asActPause, Keys: keys("DC01", "PRINT01", "APP01")}, rows, nil)
	if len(plans.calls) != 1 || plans.calls[0] != "paused p1 yes" || agents["a1"].calls[0] != "pause j1" || len(results) != 3 {
		t.Fatalf("pause: %+v %v", results, plans.calls)
	}

	// Cutover in waves: DC01 is wave 1, SQL01 is wave 3, APP01 has none
	// and goes with the first group; each later wave 30 minutes on.
	plans.calls = nil
	at := now.Add(2 * time.Hour)
	results = runner.Run(context.Background(), asBulkRequest{Action: asActCutover, Keys: keys("APP01", "DC01", "SQL01", "FS01"), At: at, StaggerMinutes: 30}, rows, nil)
	if len(results) != 3 {
		t.Fatalf("cutover results %+v", results)
	}
	if agents["a1"].calls[1] != "schedule j1 14:00" || plans.calls[0] != "cutover p1 vm-10 14:30" || agents["a2"].calls[0] != "schedule j2 15:00" {
		t.Fatalf("wave times: %v %v %v", agents["a1"].calls, plans.calls, agents["a2"].calls)
	}
	// Now: the first group cuts over at once, the next waits its gap.
	results = runner.Run(context.Background(), asBulkRequest{Action: asActCutover, Keys: keys("APP01", "SQL01"), StaggerMinutes: 60}, rows, nil)
	if agents["a1"].calls[2] != "cutover j1" || agents["a2"].calls[1] != "schedule j2 13:00" {
		t.Fatalf("now: %v %v %+v", agents["a1"].calls, agents["a2"].calls, results)
	}

	// Create replication jobs: every disk, system disk first, the shared
	// receiver, named like the editor names them.
	tmpl := asJobTemplate{Receiver: replReceiverLink{Address: "hv01:44380", Thumbprint: "AB", Code: "ABCDE-FGHIJ-KLMNO-PQRST"}, RPOMinutes: 30, Compress: true}
	runner.checkReceiver = func(ctx context.Context, link replReceiverLink) (bool, error) { return true, nil }
	results = runner.Run(context.Background(), asBulkRequest{Action: asActCreateJob, Keys: keys("WEB01", "APP01"), Template: tmpl}, rows, nil)
	if len(results) != 1 || !results[0].OK || len(agents["a4"].saved) != 1 {
		t.Fatalf("create: %+v", results)
	}
	spec := agents["a4"].saved[0]
	if spec.Name != "WEB01" || spec.RPOMinutes != 30 || !spec.Compress || spec.Receiver.Address != "hv01:44380" || len(spec.Disks) != 2 || spec.Disks[0].Source != "0" || spec.Disks[0].Target != "WEB01-disk0.vhdx" {
		t.Fatalf("spec %+v", spec)
	}
	// A boot-media receiver takes one machine only: every row is refused.
	runner.checkReceiver = func(ctx context.Context, link replReceiverLink) (bool, error) { return false, nil }
	results = runner.Run(context.Background(), asBulkRequest{Action: asActCreateJob, Keys: keys("WEB01"), Template: tmpl}, rows, nil)
	if len(results) != 1 || results[0].OK || len(agents["a4"].saved) != 1 {
		t.Fatalf("raw receiver: %+v", results)
	}

	// To this computer: the receiver here is started and paired for each
	// server, with no receiver details typed and no receiver check.
	agents["a4"].saved = nil
	runner.checkReceiver = func(ctx context.Context, link replReceiverLink) (bool, error) { t.Fatal("a local job checked a remote receiver"); return false, nil }
	runner.localReceiver = func(row asRow, folder string) (replReceiverLink, error) {
		return replReceiverLink{Address: "192.0.2.5:44380", Thumbprint: "LOCAL", Code: "C" + folder}, nil
	}
	results = runner.Run(context.Background(), asBulkRequest{Action: asActCreateJob, Keys: keys("WEB01"), Template: asJobTemplate{Local: true, Folder: `C:\ClusterStorage\Volume1\Replicas`, RPOMinutes: 15}}, rows, nil)
	if len(results) != 1 || !results[0].OK || agents["a4"].saved[0].Receiver.Thumbprint != "LOCAL" || agents["a4"].saved[0].Receiver.Code != `C`+`C:\ClusterStorage\Volume1\Replicas` {
		t.Fatalf("local: %+v %+v", results, agents["a4"].saved)
	}

	// Waves: agent jobs in the console's store, plan VMs in the plan.
	plans.calls = nil
	runner.Run(context.Background(), asBulkRequest{Action: asActWave, Keys: keys("APP01", "FS01", "PRINT01"), Wave: 4}, rows, nil)
	if w := waves.All(); w["a1|j1"] != 4 || w["a3|j3"] != 4 || plans.calls[0] != "wave p1 vm-11" {
		t.Fatalf("waves %v %v", w, plans.calls)
	}
	if again := newASWaveStore(waves.path).All(); again["a1|j1"] != 4 {
		t.Fatal("waves were not saved")
	}

	// Tests: plan VMs through the plan, agent jobs through testJob.
	plans.calls = nil
	tested := []string{}
	runner.testJob = func(ctx context.Context, row asRow, sw string) error { tested = append(tested, row.Job); return nil }
	results = runner.Run(context.Background(), asBulkRequest{Action: asActTest, Keys: keys("DC01", "APP01", "FS01")}, rows, nil)
	if len(results) != 2 || plans.calls[0] != "test p1 vm-10" || len(tested) != 1 || tested[0] != "j1" {
		t.Fatalf("test %+v %v %v", results, plans.calls, tested)
	}

	// One server failing never stops the others; a stopped run stops
	// before the next server.
	agents["a1"].failing = true
	results = runner.Run(context.Background(), asBulkRequest{Action: asActSync, Keys: keys("APP01", "FS01")}, rows, nil)
	if results[0].OK || !results[1].OK {
		t.Fatalf("failure isolation %+v", results)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results = runner.Run(ctx, asBulkRequest{Action: asActSync, Keys: keys("FS01")}, rows, nil)
	if results[0].OK || !strings.Contains(results[0].Text, "stopped") {
		t.Fatalf("stopped %+v", results)
	}
}

func TestAllServersConfirmsChangingActionsByTheServerCount(t *testing.T) {
	if asCheckConfirm(asActCutover, "3", 3) != nil || asCheckConfirm(asActCutover, "2", 3) == nil || asCheckConfirm(asActTest, "", 1) == nil {
		t.Fatal("cutover and test must be confirmed by the count")
	}
	if asCheckConfirm(asActPause, "", 5) != nil {
		t.Fatal("pause needs no confirmation")
	}
	if got := asTargetName("SRV 01", "/dev/sda"); got != "SRV-01-disksda.vhdx" {
		t.Fatalf("target %q", got)
	}
}

func TestAllServersBuildsATestVMFromAJobsCopies(t *testing.T) {
	row := asRow{Name: "APP01"}
	job := replJobView{Spec: replJobSpec{Disks: []replDiskMap{{Source: "0", Target: "APP01-disk0.vhdx"}, {Source: "1", Target: "APP01-disk1.vhdx"}}}}
	copies := []replCopyInfo{
		{Name: "APP01-disk1.vhdx", Complete: true, Round: "j1@x", Generation: 2},
		{Name: "APP01-disk0.vhdx", Complete: true, Round: "j1@x", Generation: 2, Machine: &vmSourceMachine{CPUs: 8, MemoryMB: 16384, Adapters: 2, OS: "windows"}},
	}
	r, err := asFailoverRequest(row, job, copies, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != "test" || r.Name != "APP01-TEST" || r.Copies[0] != "APP01-disk0.vhdx" || r.CPUs != 8 || r.MemoryMB != 16384 || r.Adapters != 2 || r.Generation != 2 || r.SecureBoot != "windows" || r.Switch != "" {
		t.Fatalf("request %+v", r)
	}
	copies[0].Round = "j1@y"
	if _, err := asFailoverRequest(row, job, copies, ""); err == nil {
		t.Fatal("copies from different rounds were accepted")
	}
	if _, err := asFailoverRequest(row, job, copies[1:], ""); err == nil {
		t.Fatal("a missing copy was accepted")
	}
}

func TestMigrationPlanSetsWaves(t *testing.T) {
	runner := newMBRunner(filepath.Join(t.TempDir(), "plans.json"), nil)
	runner.plans = []*mbPlan{{ID: "p1", Name: "P", VMs: []*mbVM{{ID: "v1", Name: "A"}, {ID: "v2", Name: "B"}}}}
	if err := runner.SetWave("p1", []string{"v2"}, 5); err != nil {
		t.Fatal(err)
	}
	if runner.plans[0].VMs[1].Wave != 5 || runner.plans[0].VMs[0].Wave != 0 {
		t.Fatal("wave not set")
	}
	if runner.SetWave("p1", []string{"v1"}, 100) == nil || runner.SetWave("p1", []string{"zz"}, 1) == nil {
		t.Fatal("bad waves were accepted")
	}
}
