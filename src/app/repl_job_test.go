package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// replFakePlatform serves in-memory disks and records service actions.
type replFakePlatform struct {
	mu       sync.Mutex
	disks    map[string][]byte
	stopped  []string
	started  []string
	stopFail bool
	openFail error
	shutdown int
	snapshot func() time.Time
	gate     chan struct{} // when set, OpenDisks waits for it
	panics   bool          // OpenDisks panics, like an internal error would
}

func (p *replFakePlatform) Name() string { return "test" }
func (p *replFakePlatform) ListDisks() ([]replDiskInfo, error) {
	list := []replDiskInfo{}
	for id, data := range p.disks {
		list = append(list, replDiskInfo{ID: id, Size: int64(len(data))})
	}
	return list, nil
}
func (p *replFakePlatform) OpenDisks(ctx context.Context, job string, sources []string, allowLive bool, log func(string)) (*replOpenedDisks, error) {
	if p.gate != nil {
		<-p.gate
	}
	if p.panics {
		var missing map[string]int
		missing["x"] = 1 // a nil map write, as a real bug would make
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.openFail != nil {
		return nil, p.openFail
	}
	opened := &replOpenedDisks{Sources: map[string]vmCopySource{}, Snapshot: p.snapshot(), Close: func() {}}
	for _, id := range sources {
		data, ok := p.disks[id]
		if !ok {
			return nil, errors.New("no disk " + id)
		}
		opened.Sources[id] = &vmBytesSource{data: append([]byte(nil), data...), name: "test disk " + id}
	}
	return opened, nil
}
func (p *replFakePlatform) StopServices(names []string, log func(string)) error {
	if p.stopFail {
		return errors.New("access denied")
	}
	p.stopped = append(p.stopped, names...)
	return nil
}
func (p *replFakePlatform) StartServices(names []string, log func(string)) error {
	p.started = append(p.started, names...)
	return nil
}
func (p *replFakePlatform) Shutdown(log func(string)) error { p.shutdown++; return nil }

type replTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *replTestClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *replTestClock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func replWaitIdle(t *testing.T, agent *replAgent, id string) replJobView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		agent.mu.Lock()
		job := agent.jobs[id]
		running := job.running || job.status.State == replStateSyncing || job.status.State == replStateCutover
		agent.mu.Unlock()
		if !running {
			for _, view := range agent.Jobs() {
				if view.Spec.ID == id {
					return view
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the job did not finish its round")
	return replJobView{}
}

func replTestSetup(t *testing.T) (*replAgent, *replFakePlatform, *replTestClock, *vmReceiver, string) {
	folder := t.TempDir()
	receiver, err := startVMReceiver("127.0.0.1:0", folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { receiver.Stop() })
	clock := &replTestClock{now: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
	platform := &replFakePlatform{disks: map[string][]byte{"0": vmTestDiskBytes(36 << 20), "1": vmTestDiskBytes(36 << 20)}, snapshot: clock.Now}
	agent, err := newReplAgent(t.TempDir(), platform)
	if err != nil {
		t.Fatal(err)
	}
	agent.now = clock.Now
	return agent, platform, clock, receiver, folder
}

func replTestSpec(receiver *vmReceiver) replJobSpec {
	return replJobSpec{
		Name:            "APP01",
		Disks:           []replDiskMap{{Source: "0", Target: "APP01-disk0.vhdx"}, {Source: "1", Target: "APP01-disk1.vhdx"}},
		Receiver:        replReceiverLink{Address: "127.0.0.1:" + strconv.Itoa(receiver.Port()), Thumbprint: receiver.Thumbprint(), Code: receiver.Code()},
		IntervalMinutes: 5,
		RPOMinutes:      15,
		Compress:        true,
		StopServices:    []string{"MSSQLSERVER", "W3SVC"},
	}
}

func TestReplicationJobRunsRoundsTracksRPOAndAlerts(t *testing.T) {
	agent, platform, clock, receiver, folder := replTestSetup(t)
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	if view.Spec.Receiver.Code != "" || !view.HasCode {
		t.Fatal("the pairing code was shown in the job view")
	}
	id := view.Spec.ID
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.Rounds != 1 || view.Status.State != replStateIdle || !view.Status.RecoveryPoint.Equal(clock.Now()) {
		t.Fatalf("first round: %+v", view.Status)
	}
	for disk, name := range map[string]string{"0": "APP01-disk0.vhdx", "1": "APP01-disk1.vhdx"} {
		if !bytes.Equal(vmReadVHDX(t, filepath.Join(folder, name)), platform.disks[disk]) {
			t.Fatalf("%s differs from disk %s", name, disk)
		}
	}
	// Not due yet: nothing runs.
	clock.Add(4 * time.Minute)
	agent.Tick()
	if view = replWaitIdle(t, agent, id); view.Status.Rounds != 1 {
		t.Fatal("a round ran before the interval")
	}
	// Due: a delta round sends only the changed chunk.
	rand.Read(platform.disks["1"][7<<20 : 7<<20+10])
	clock.Add(time.Minute)
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.Rounds != 2 || view.Status.Disks[1].Changed != 1 || view.Status.Disks[0].Changed != 0 {
		t.Fatalf("delta round: %+v", view.Status.Disks)
	}
	if !bytes.Equal(vmReadVHDX(t, filepath.Join(folder, "APP01-disk1.vhdx")), platform.disks["1"]) {
		t.Fatal("the delta round left the copy different")
	}
	// The source goes away: rounds fail, back off, and alert after three.
	platform.openFail = errors.New("shadow copy failed")
	for i := 0; i < 3; i++ {
		clock.Add(16 * time.Minute)
		agent.Tick()
		view = replWaitIdle(t, agent, id)
	}
	if view.Status.State != replStateError || view.Status.Failures != 3 {
		t.Fatalf("failing: %+v", view.Status)
	}
	kinds := map[string]bool{}
	for _, alert := range agent.Alerts() {
		if !alert.Cleared {
			kinds[alert.Kind] = true
		}
	}
	if !kinds["rpo"] || !kinds["failing"] {
		t.Fatalf("alerts %+v", agent.Alerts())
	}
	if view.RPOSecond < 45*60 {
		t.Fatalf("RPO %d seconds", view.RPOSecond)
	}
	// It recovers: the alerts clear.
	platform.openFail = nil
	clock.Add(20 * time.Minute)
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateIdle || view.Status.Failures != 0 {
		t.Fatalf("recovery: %+v", view.Status)
	}
	for _, alert := range agent.Alerts() {
		if !alert.Cleared {
			t.Fatalf("alert left open: %+v", alert)
		}
	}
	lines, _ := agent.Log(id)
	if !strings.Contains(strings.Join(lines, "\n"), "ALERT:") {
		t.Fatal("alerts are not in the job log")
	}
	// The agent restarts from its records.
	again, err := newReplAgent(agent.dir, platform)
	if err != nil || len(again.Jobs()) != 1 || again.Jobs()[0].Status.Rounds != 3 {
		t.Fatalf("reloaded: %v", err)
	}
	if again.jobs[id].spec.Receiver.Code == "" {
		t.Fatal("the reloaded job lost its pairing code")
	}
}

func TestReplicationCutoverVerifiesAndRollsBackOnFailure(t *testing.T) {
	agent, platform, clock, receiver, folder := replTestSetup(t)
	spec := replTestSpec(receiver)
	spec.ShutdownAfterCutover = false
	view, err := agent.SaveJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	if agent.Action(id, "cutover") == nil {
		t.Fatal("cutover was allowed before the first round")
	}
	agent.Tick()
	replWaitIdle(t, agent, id)

	// A failed cutover starts the services again and keeps replicating.
	platform.openFail = errors.New("volume shadow copy service is not running")
	if err := agent.Action(id, "cutover"); err != nil {
		t.Fatal(err)
	}
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateError || !strings.Contains(view.Status.Message, "rolled back") {
		t.Fatalf("failed cutover: %+v", view.Status)
	}
	if strings.Join(platform.stopped, ",") != "MSSQLSERVER,W3SVC" || strings.Join(platform.started, ",") != "MSSQLSERVER,W3SVC" {
		t.Fatalf("services stopped %v started %v", platform.stopped, platform.started)
	}

	// A scheduled cutover runs at its time and verifies every disk.
	platform.openFail = nil
	platform.stopped, platform.started = nil, nil
	rand.Read(platform.disks["0"][1<<20 : 1<<20+10])
	at := clock.Now().Add(30 * time.Minute)
	if err := agent.Schedule(id, &at); err != nil {
		t.Fatal(err)
	}
	if err := agent.Schedule(id, &time.Time{}); err == nil {
		t.Fatal("a cutover in the past was accepted")
	}
	clock.Add(31 * time.Minute)
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateCutDone || !view.Status.Disks[0].Verified || !view.Status.Disks[1].Verified || view.Spec.CutoverAt != nil {
		t.Fatalf("cutover: %+v", view.Status)
	}
	if len(platform.started) != 0 || len(platform.stopped) != 2 {
		t.Fatal("services were started again after a successful cutover")
	}
	if !bytes.Equal(vmReadVHDX(t, filepath.Join(folder, "APP01-disk0.vhdx")), platform.disks["0"]) {
		t.Fatal("the cutover copy differs from the source")
	}
	// Nothing more runs.
	clock.Add(time.Hour)
	agent.Tick()
	if replWaitIdle(t, agent, id).Status.Rounds != view.Status.Rounds {
		t.Fatal("a round ran after cutover")
	}
	if agent.Action(id, "sync") == nil || agent.Action(id, "pause") == nil {
		t.Fatal("a cut-over job accepted sync or pause")
	}
}

func TestReplicationJobValidation(t *testing.T) {
	agent, _, _, receiver, _ := replTestSetup(t)
	good := replTestSpec(receiver)
	cases := map[string]func(*replJobSpec){
		"no name":       func(s *replJobSpec) { s.Name = " " },
		"no disks":      func(s *replJobSpec) { s.Disks = nil },
		"same target":   func(s *replJobSpec) { s.Disks[1].Target = "app01-DISK0.vhdx" },
		"path target":   func(s *replJobSpec) { s.Disks[0].Target = `..\x.vhdx` },
		"bad source":    func(s *replJobSpec) { s.Disks[0].Source = "0; rm -rf" },
		"no thumbprint": func(s *replJobSpec) { s.Receiver.Thumbprint = "abc" },
		"no code":       func(s *replJobSpec) { s.Receiver.Code = "" },
		"bad interval":  func(s *replJobSpec) { s.IntervalMinutes = -1 },
		"bad service":   func(s *replJobSpec) { s.StopServices = []string{"a\"b"} },
		"bad throttle":  func(s *replJobSpec) { s.Throttle.LimitMbps = -5 },
	}
	for name, change := range cases {
		spec := replTestSpec(receiver)
		spec.Disks = append([]replDiskMap(nil), good.Disks...)
		change(&spec)
		if _, err := agent.SaveJob(spec); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	view, err := agent.SaveJob(good)
	if err != nil {
		t.Fatal(err)
	}
	// Editing without retyping the code keeps it for the same receiver.
	edit := view.Spec
	edit.IntervalMinutes = 30
	if _, err := agent.SaveJob(edit); err != nil {
		t.Fatal(err)
	}
	// A second job may not send to the same receiver disk.
	other := replTestSpec(receiver)
	other.Name = "APP02"
	if _, err := agent.SaveJob(other); err == nil || !strings.Contains(err.Error(), "already sends") {
		t.Fatalf("a second job to the same target: %v", err)
	}
}

func TestReplicationAgentServerNeedsTheKeyAndPinnedCertificate(t *testing.T) {
	agent, _, _, receiver, _ := replTestSetup(t)
	dir := t.TempDir()
	key := replNewAgentKey()
	config := replAgentConfig{Port: 0, KeyHash: replKeyHash(key)}
	if err := saveReplAgentConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	certificate, thumbprint, err := replAgentCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The certificate is reused on restart.
	if _, again, _ := replAgentCertificate(dir); again != thumbprint {
		t.Fatal("the agent certificate changed on restart")
	}
	server, err := startReplAgentServer("127.0.0.1:0", agent, config, certificate, thumbprint, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	address := "127.0.0.1:" + strconv.Itoa(server.Port())
	ctx := context.Background()
	client, _ := newReplAgentClient(address, thumbprint, key)
	hello, err := client.Hello(ctx)
	if err != nil || hello.Version != "9.9.9" || hello.Platform != "test" {
		t.Fatalf("hello %+v %v", hello, err)
	}
	view, err := client.SaveJob(ctx, replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := client.Jobs(ctx)
	if err != nil || len(jobs.Jobs) != 1 || jobs.Jobs[0].Spec.Receiver.Code != "" {
		t.Fatalf("jobs %+v %v", jobs, err)
	}
	if err := client.Action(ctx, view.Spec.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if lines, err := client.Log(ctx, view.Spec.ID); err != nil || !strings.Contains(strings.Join(lines, "\n"), "Paused") {
		t.Fatalf("log %v %v", lines, err)
	}
	wrongKey, _ := newReplAgentClient(address, thumbprint, replNewAgentKey())
	if _, err := wrongKey.Hello(ctx); err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("a wrong key was accepted: %v", err)
	}
	other := strings.Repeat("ab", 32)
	wrongCert, _ := newReplAgentClient(address, other, key)
	if _, err := wrongCert.Hello(ctx); err == nil || !strings.Contains(err.Error(), "thumbprint") {
		t.Fatalf("a different certificate was accepted: %v", err)
	}
}
