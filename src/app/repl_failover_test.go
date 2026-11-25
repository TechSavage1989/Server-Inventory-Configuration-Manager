package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A test failover holds the copies: a round meets the hold, the job waits
// without failing or alerting, and after the hold is released the next round
// catches up. Every copy records the round it came from.
func TestReplicationWaitsWhileTheCopiesAreHeldForATestFailover(t *testing.T) {
	agent, platform, clock, receiver, folder := replTestSetup(t)
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	copies, err := replListCopies(folder, receiver.Busy, nil)
	if err != nil || len(copies) != 2 {
		t.Fatalf("copies %+v %v", copies, err)
	}
	if err := replCheckCopies(copies); err != nil {
		t.Fatal(err)
	}
	if copies[0].Job != id || !copies[0].RecoveryPoint.Equal(view.Status.RecoveryPoint) {
		t.Fatalf("the copy does not name its round: %+v", copies[0])
	}
	names := []string{"APP01-disk0.vhdx", "APP01-disk1.vhdx"}
	if err := receiver.Hold(names, "test failover APP01-TEST"); err != nil {
		t.Fatal(err)
	}
	platform.disks["0"][3<<20] ^= 0xff
	for i := 0; i < 4; i++ {
		clock.Add(16 * time.Minute)
		agent.Tick()
		view = replWaitIdle(t, agent, id)
	}
	if view.Status.State != replStateHeld || view.Status.Failures != 0 || view.Status.Rounds != 1 {
		t.Fatalf("held: %+v", view.Status)
	}
	for _, alert := range agent.Alerts() {
		if !alert.Cleared {
			t.Fatalf("a held job raised an alert: %+v", alert)
		}
	}
	if !strings.Contains(view.Status.LastError, "test failover APP01-TEST") {
		t.Fatal("the hold reason is not shown")
	}
	receiver.Release(names)
	clock.Add(3 * time.Minute)
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateIdle || view.Status.Rounds != 2 || view.Status.Disks[0].Changed != 1 {
		t.Fatalf("after the test: %+v", view.Status)
	}
	if !bytes.Equal(vmReadVHDX(t, filepath.Join(folder, "APP01-disk0.vhdx")), platform.disks["0"]) {
		t.Fatal("the copy did not catch up after the test")
	}
}

func TestFailoverRefusesMixedUnfinishedOrBusyCopies(t *testing.T) {
	good := replCopyInfo{Name: "a.vhdx", Complete: true, Round: "j@2026-11-25T12:00:00Z"}
	for _, c := range []struct {
		other replCopyInfo
		want  string
	}{
		{replCopyInfo{Name: "b.vhdx", Complete: true, Round: "j@2026-11-25T12:05:00Z"}, "different replication rounds"},
		{replCopyInfo{Name: "b.vhdx", Complete: false, Round: good.Round}, "not a finished copy"},
		{replCopyInfo{Name: "b.vhdx", Complete: true, Round: good.Round, Busy: true}, "being received"},
		{replCopyInfo{Name: "b.vhdx", Complete: true, Round: good.Round, Held: "test failover X"}, "already in use"},
	} {
		if err := replCheckCopies([]replCopyInfo{good, c.other}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.other, err)
		}
	}
	if err := replCheckCopies([]replCopyInfo{good, {Name: "b.vhdx", Complete: true, Round: good.Round}}); err != nil {
		t.Fatal(err)
	}
}

func TestFailoverPlansTestDisksAndRemovesOnlyThem(t *testing.T) {
	folder, tests := `D:\Received`, `D:\Received\Test failovers`
	request := replFailoverRequest{Mode: "test", Name: "APP01-TEST", Copies: []string{"APP01-disk0.vhdx", "APP01 o'disk1.vhdx"}, Generation: 2, CPUs: 4, MemoryMB: 8191, SecureBoot: "linux", Start: true}
	plan, children, err := replFailoverPlan(request, folder, tests)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 || plan.Disks[0] != children[0] || plan.MemoryBytes != 8192<<20 || plan.SecureBootTemplate != "MicrosoftUEFICertificateAuthority" {
		t.Fatalf("plan %+v %v", plan, children)
	}
	if plan.Adapters[0].Switch != "" {
		t.Fatal("a test VM without a chosen switch must stay disconnected")
	}
	script := replFailoverScript(plan, folder, request.Copies, children)
	for _, want := range []string{"-Differencing", "-ParentPath " + vmPSQuote(filepath.Join(folder, "APP01 o'disk1.vhdx")), "already exists", "Test failover from replicated copies", "Start-VM"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if strings.Index(script, "New-VHD") > strings.Index(script, "New-VM") {
		t.Fatal("the test disks must exist before the VM")
	}
	end := replFailoverEndScript(replFailover{Name: "APP01-TEST", VMID: "6f1d4c3a-0000-4000-8000-000000000001", Copies: request.Copies, Children: children}, folder)
	for _, want := range []string{"Get-VM -Id", "-ne 'APP01-TEST'", "'Differencing'", "Stop-VM -VM $vm -TurnOff"} {
		if !strings.Contains(end, want) {
			t.Errorf("the end script lacks %q:\n%s", want, end)
		}
	}
	for _, line := range strings.Split(end, "\n") {
		if strings.Contains(line, "Remove-Item") && strings.Contains(line, "Received\\APP01") && !strings.Contains(line, "Test failovers") {
			t.Fatalf("the end script removes a copy: %s", line)
		}
	}
	production := request
	production.Mode = "production"
	plan, children, err = replFailoverPlan(production, folder, tests)
	if err != nil || len(children) != 0 || plan.Disks[0] != filepath.Join(folder, "APP01-disk0.vhdx") {
		t.Fatalf("production %+v %v", plan, err)
	}
	for _, bad := range []replFailoverRequest{
		{Mode: "test", Name: "", Copies: request.Copies, Generation: 2, CPUs: 2, MemoryMB: 2048},
		{Mode: "test", Name: "X", Copies: []string{`..\x.vhdx`}, Generation: 2, CPUs: 2, MemoryMB: 2048},
		{Mode: "test", Name: "X", Copies: []string{"a.vhdx", "A.vhdx"}, Generation: 2, CPUs: 2, MemoryMB: 2048},
		{Mode: "live", Name: "X", Copies: request.Copies, Generation: 2, CPUs: 2, MemoryMB: 2048},
		{Mode: "test", Name: "X';Remove-VM", Copies: request.Copies, Generation: 2, CPUs: 2, MemoryMB: 2048},
	} {
		if _, _, err := replFailoverPlan(bad, folder, tests); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestFailoverDetectsUEFICopies(t *testing.T) {
	folder := t.TempDir()
	for name, gpt := range map[string]bool{"bios.vhdx": false, "uefi.vhdx": true} {
		disk, err := createVHDX(filepath.Join(folder, name), 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		sector := make([]byte, 512)
		if gpt {
			copy(sector, "EFI PART")
		}
		if _, err := disk.WriteAt(sector, 512); err != nil {
			t.Fatal(err)
		}
		disk.Close()
		if err := saveVMCopyState(&vmCopyState{Version: 1, Target: filepath.Join(folder, name), Size: 64 << 20, ChunkSize: 4 << 20, Hashes: make([]string, 16), Complete: true}); err != nil {
			t.Fatal(err)
		}
	}
	copies, err := replListCopies(folder, nil, nil)
	if err != nil || len(copies) != 2 || copies[0].Generation != 1 || copies[1].Generation != 2 {
		t.Fatalf("%+v %v", copies, err)
	}
}

// A failback job compares the receiver's disks with the source in its first
// round.
func TestFailbackJobRescansTheReceiverFirst(t *testing.T) {
	agent, _, _, receiver, _ := replTestSetup(t)
	spec := replTestSpec(receiver)
	spec.RescanFirst = true
	view, err := agent.SaveJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Status.RescanNext {
		t.Fatal("a failback job must rescan the receiver in its first round")
	}
	agent.Tick()
	view = replWaitIdle(t, agent, view.Spec.ID)
	if view.Status.State != replStateIdle || view.Status.RescanNext {
		t.Fatalf("after the first round: %+v", view.Status)
	}
}
