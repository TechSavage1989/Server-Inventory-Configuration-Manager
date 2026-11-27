package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type rbFakeOps struct {
	calls     []string
	stopErr   error
	state     string
	stateErr  error
	reconnect error
	start     error
	agent     error
}

func (f *rbFakeOps) StopNew(ctx context.Context, rec *rbRecord, turnOff bool, log func(string)) ([]rbAdapter, error) {
	f.calls = append(f.calls, "stop")
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	return []rbAdapter{{Name: "Network Adapter", Switch: "LAN"}}, nil
}
func (f *rbFakeOps) NewState(ctx context.Context, rec *rbRecord) (string, error) {
	f.calls = append(f.calls, "state")
	return f.state, f.stateErr
}
func (f *rbFakeOps) ReconnectSource(ctx context.Context, rec *rbRecord, log func(string)) (int, error) {
	f.calls = append(f.calls, "reconnect")
	return 2, f.reconnect
}
func (f *rbFakeOps) StartSource(ctx context.Context, rec *rbRecord, log func(string)) error {
	f.calls = append(f.calls, "start")
	return f.start
}
func (f *rbFakeOps) RollbackAgent(ctx context.Context, rec *rbRecord, log func(string)) error {
	f.calls = append(f.calls, "agent")
	return f.agent
}

func rbSample() rbRecord {
	vm := vmMachine{Name: "APP01", ID: "vm-42", GuestOS: "Microsoft Windows Server 2019", Host: "esx01.example.com"}
	return rbNewRecord(rbMethodMove, rbSource{Kind: "vmware", Server: "vcenter.example.com", VMID: vm.ID, Name: vm.Name, AdapterRecord: `C:\records\APP01-adapters.json`}, rbNew{Kind: "hyperv", ID: "6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f", Name: vm.Name}, &vm)
}

func TestRollbackTurnsTheNewVMOffBeforeTheOldOneStarts(t *testing.T) {
	ops := &rbFakeOps{state: "off"}
	options := rbOptions{TurnOff: true, Reconnect: true, Start: true}
	if why := rbCheck(rbSample(), options); why != "" {
		t.Fatal(why)
	}
	ended := rbRun(context.Background(), rbSample(), options, ops, nil)
	if strings.Join(ops.calls, ",") != "stop,state,reconnect,start" {
		t.Fatalf("wrong order: %v", ops.calls)
	}
	if ended.State != rbStateDone || len(ended.New.Adapters) != 1 {
		t.Fatalf("%+v", ended)
	}
	for _, step := range ended.Steps {
		if step.State != rbStepOK {
			t.Fatalf("step %s is %s", step.Key, step.State)
		}
	}
}

func TestRollbackNeverStartsTheOldVMUnlessTheNewOneIsOff(t *testing.T) {
	for name, ops := range map[string]*rbFakeOps{
		"stop fails":   {stopErr: errors.New("it is still running"), state: "running"},
		"still on":     {state: "running"},
		"state failed": {stateErr: errors.New("host unreachable")},
		"saved state":  {state: "saved"},
	} {
		ended := rbRun(context.Background(), rbSample(), rbOptions{TurnOff: true, Reconnect: true, Start: true}, ops, nil)
		for _, call := range ops.calls {
			if call == "start" || call == "reconnect" {
				t.Fatalf("%s: the old VM was touched: %v", name, ops.calls)
			}
		}
		if ended.State != rbStateFailed {
			t.Fatalf("%s: %s", name, ended.State)
		}
	}
}

func TestRollbackStopsWhenTheNetworkCannotBePutBack(t *testing.T) {
	ops := &rbFakeOps{state: "off", reconnect: errors.New("vSphere refused")}
	ended := rbRun(context.Background(), rbSample(), rbOptions{TurnOff: true, Reconnect: true, Start: true}, ops, nil)
	if ended.State != rbStatePartial || strings.Contains(strings.Join(ops.calls, ","), "start") {
		t.Fatalf("the old VM must not start without its network: %v %s", ops.calls, ended.State)
	}
	ops = &rbFakeOps{state: "off", start: errors.New("is no longer in vcenter.example.com")}
	ended = rbRun(context.Background(), rbSample(), rbOptions{TurnOff: true, Reconnect: true, Start: true}, ops, nil)
	if ended.State != rbStatePartial || len(ended.Manual) == 0 {
		t.Fatalf("a failed start must give steps: %+v", ended)
	}
}

func TestRollbackDomainControllersNeedTheirOwnTick(t *testing.T) {
	vm := vmMachine{Name: "DC01", ID: "vm-7", GuestOS: "Microsoft Windows Server 2022"}
	rec := rbNewRecord(rbMethodMove, rbSource{Kind: "vmware", VMID: vm.ID, Name: vm.Name}, rbNew{Kind: "hyperv", ID: "6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f"}, &vm)
	if !rec.DC {
		t.Fatal("DC01 is named like a domain controller")
	}
	if why := rbCheck(rec, rbOptions{Start: true}); !strings.Contains(why, "domain controller") {
		t.Fatalf("a domain controller needs the tick: %q", why)
	}
	if why := rbCheck(rec, rbOptions{Start: true, DCConfirmed: true}); why != "" {
		t.Fatal(why)
	}
	checked := vmMachine{Name: "CORE-INFRA", ID: "vm-8", Preflight: &vmPreflight{Facts: &vmPreflightFacts{DC: &vmPreflightDC{IsDC: true}}}}
	if ok, why := rbDomainController(checked); !ok || !strings.Contains(why, "check") {
		t.Fatalf("a check that found a domain controller wins: %v %q", ok, why)
	}
	if ok, _ := rbDomainController(vmMachine{Name: "APP01", GuestOS: "Microsoft Windows Server 2019"}); ok {
		t.Fatal("APP01 is not a domain controller")
	}
}

func TestRollbackOfAnAgentJobGivesStepsWhenTheServerIsOff(t *testing.T) {
	rec := rbNewRecord(rbMethodCopies, rbSource{Kind: "agent", Name: "FILE01", Job: "job1", Services: []string{"LanmanServer"}}, rbNew{Kind: "hyperv", ID: "6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f", Name: "FILE01"}, nil)
	ops := &rbFakeOps{state: "off", agent: errRBAgentUnreachable}
	ended := rbRun(context.Background(), rec, rbOptions{TurnOff: true, Start: true}, ops, nil)
	if ended.State != rbStatePartial || len(ended.Manual) == 0 || !strings.Contains(strings.Join(ended.Manual, " "), "LanmanServer") {
		t.Fatalf("%+v", ended)
	}
	if strings.Contains(strings.Join(ops.calls, ","), "reconnect") {
		t.Fatal("an agent job has no vSphere adapters to put back")
	}
	ops = &rbFakeOps{state: "off"}
	if ended = rbRun(context.Background(), rec, rbOptions{TurnOff: true, Start: true}, ops, nil); ended.State != rbStateDone {
		t.Fatalf("%+v", ended)
	}
}

func TestRollbackRefusesWhatItCannotFindSafely(t *testing.T) {
	rec := rbSample()
	rec.New.ID = ""
	if why := rbCheck(rec, rbOptions{}); why == "" {
		t.Fatal("a record without the new VM's ID must be refused")
	}
	rec = rbSample()
	rec.New.ID = "not-a-guid"
	if why := rbCheck(rec, rbOptions{}); why == "" {
		t.Fatal("a Hyper-V ID must be a GUID")
	}
	rec = rbSample()
	rec.State = rbStateDone
	if why := rbCheck(rec, rbOptions{}); !strings.Contains(why, "already") {
		t.Fatal(why)
	}
}

func TestRollbackHyperVScriptsChangeOnlyTheNewVM(t *testing.T) {
	script := rbHyperVStopScript("6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f", "O'Brien APP", false)
	for _, forbidden := range []string{"Remove-VM", "Remove-Item", "Remove-VMHardDiskDrive", "Remove-VMNetworkAdapter", "Get-VM -Name", "-TurnOff"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("the stop script must not contain %s:\n%s", forbidden, script)
		}
	}
	if !strings.Contains(script, "'O''Brien APP'") || !strings.Contains(script, "Disconnect-VMNetworkAdapter") || !strings.Contains(script, "-AutomaticStartAction Nothing") || !strings.Contains(script, rbHyperVShutdownService) {
		t.Fatalf("%s", script)
	}
	if !strings.Contains(rbHyperVStopScript("6f1c7a3e-1b2c-4d5e-8f90-0a1b2c3d4e5f", "APP01", true), "Stop-VM -VM $vm -TurnOff -Force") {
		t.Fatal("turning off must be offered when allowed")
	}
	adapters, action, state := rbParseHyperV([]string{"Asking Windows to shut down.", "RB-ADAPTER\tNetwork Adapter\tLAN", "RB-ADAPTER\tNetwork Adapter 2\t", "RB-STARTACTION StartIfRunning", "RB-STATE Off\r"})
	if len(adapters) != 2 || adapters[0].Switch != "LAN" || action != "StartIfRunning" || state != "off" {
		t.Fatalf("%v %q %q", adapters, action, state)
	}
}

func TestRollbackRecordsSurviveARestart(t *testing.T) {
	store := &rbStore{path: filepath.Join(t.TempDir(), "Rollback", "rollbacks.json")}
	first, second := rbSample(), rbSample()
	second.Name, second.Source.Name, second.CutoverAt = "WEB01", "WEB01", time.Now().UTC().Add(time.Minute)
	if err := store.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(first.ID, func(r *rbRecord) error { r.State = rbStateRunning; return nil }); err != nil {
		t.Fatal(err)
	}
	store.RecoverInterrupted()
	list, err := store.List()
	if err != nil || len(list) != 2 || list[0].Name != "WEB01" {
		t.Fatalf("%v %v", list, err)
	}
	if got := rbLatestFor(list, "app01"); got == nil || got.State != rbStateFailed {
		t.Fatalf("an interrupted rollback must show failed: %+v", got)
	}
}

func TestRollbackTellsTheWindowAndWhatIsLost(t *testing.T) {
	rec := rbSample()
	rec.CutoverAt = time.Now().Add(-3 * time.Hour)
	if text := rbDataWarning(rec, time.Now()); !strings.Contains(text, "3 hours") {
		t.Fatal(text)
	}
	rec.RollingHost = "esx01.example.com"
	if text := rbWindow(rec); !strings.Contains(text, "esx01.example.com") || !strings.Contains(text, "rebuilt") {
		t.Fatal(text)
	}
	ids := map[string]bool{}
	for _, guide := range rbGuides() {
		if guide.ID == "" || len(guide.Steps) == 0 || ids[guide.ID] {
			t.Fatalf("guide %+v", guide)
		}
		ids[guide.ID] = true
	}
	for _, want := range []string{"movevms", "platform", "replicate", "rolling", "p2v", "v2p", "convert", "shares", "restore"} {
		if !ids[want] {
			t.Fatalf("every way of moving needs its rollback: %s is missing", want)
		}
	}
}

func TestRollingStageSaysWhereRollbackEnds(t *testing.T) {
	inventory := &vmInventory{
		Hosts:      []vmHost{{Name: "esx01"}, {Name: "esx02"}},
		Datastores: []vmDatastore{{Name: "local01", Hosts: []string{"esx01"}}, {Name: "shared", Hosts: []string{"esx01", "esx02"}}},
	}
	moved := []vmMachine{
		{Name: "ON-HOST", Host: "esx01", Disks: []vmDisk{{Datastore: "shared"}}},
		{Name: "LOCAL", Host: "esx02", Disks: []vmDisk{{Datastore: "local01"}}},
		{Name: "SAFE", Host: "esx02", Disks: []vmDisk{{Datastore: "shared"}}},
	}
	got := strings.Join(vmRollbackEndsAt(inventory, moved, "esx01", nil), ",")
	if got != "ON-HOST,LOCAL" {
		t.Fatalf("got %s", got)
	}
	if got := strings.Join(vmRollbackEndsAt(inventory, moved, "esx01", map[string]bool{"shared": true}), ","); got != "ON-HOST,LOCAL,SAFE" {
		t.Fatalf("a reused LUN ends rollback too: %s", got)
	}
}

func TestReplicationJobRollbackStartsTheStoppedServicesAndPauses(t *testing.T) {
	agent, platform, _, receiver, _ := replTestSetup(t)
	spec := replTestSpec(receiver)
	spec.StopServices = []string{"Spooler"}
	view, err := agent.SaveJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	if err := agent.Action(id, "rollback"); err == nil {
		t.Fatal("a job that is not cut over has nothing to roll back")
	}
	agent.Tick()
	replWaitIdle(t, agent, id)
	if err := agent.Action(id, "finish-by-hand"); err != nil {
		t.Fatal(err)
	}
	if err := agent.Action(id, "rollback"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		platform.mu.Lock()
		started := strings.Join(platform.started, ",")
		platform.mu.Unlock()
		if started == "Spooler" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	platform.mu.Lock()
	started := strings.Join(platform.started, ",")
	platform.mu.Unlock()
	if started != "Spooler" {
		t.Fatalf("the stopped services must start again: %q", started)
	}
	for _, job := range agent.Jobs() {
		if job.Spec.ID == id && (job.Status.State != replStatePaused || !job.Status.RescanNext) {
			t.Fatalf("a rolled back job is paused and rescans first: %+v", job.Status)
		}
	}
}
