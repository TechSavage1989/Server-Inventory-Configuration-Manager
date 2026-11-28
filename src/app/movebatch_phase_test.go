package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMigratedNameFitsVSphereAndIsNotDoubled(t *testing.T) {
	if got := vmMigratedName("MATCHC-DC02"); got != "MIGRATED - DO NOT POWER ON - MATCHC-DC02" {
		t.Fatal(got)
	}
	if got := vmMigratedName(vmMigratedName("APP01")); got != vmMigratedName("APP01") {
		t.Fatalf("doubled: %q", got)
	}
	long := strings.Repeat("é", 90)
	if got := vmMigratedName(long); len([]rune(got)) != vmNameLimit || !strings.HasPrefix(got, vmMigratedPrefix) {
		t.Fatalf("not cut to the limit: %d", len([]rune(got)))
	}
	if _, err := vmMarkPlan("APP01", "APP01", "poweredOn", "", "HV01", time.Now()); err == nil {
		t.Fatal("a running VM must be refused")
	}
	if _, err := vmMarkPlan("APP02", "APP01", "poweredOff", "", "HV01", time.Now()); err == nil {
		t.Fatal("another name must be refused")
	}
	record, err := vmMarkPlan("APP01", "APP01", "poweredOff", "kept note", "HV01", time.Now())
	if err != nil || !strings.HasSuffix(record.NewAnnotation, "\n\nkept note") || record.Annotation != "kept note" {
		t.Fatalf("%+v %v", record, err)
	}
}

// After the final copy reaches 100% a cutover says it verifies, then that it
// creates the new VM, instead of a frozen 100%.
func TestCutoverShowsItsPhaseAfterTheCopy(t *testing.T) {
	now := time.Now()
	live := &mbLive{Started: now.Add(-time.Minute), Cutover: true, Disks: map[string]vmCopyProgress{}}
	if v := live.view(now); v.Phase != "shutdown" {
		t.Fatalf("before any disk: %+v", v)
	}
	live.Disks["disk0"] = vmCopyProgress{Chunks: 100, Done: 40}
	if v := live.view(now); v.Phase != "copy" || v.Percent != 40 {
		t.Fatalf("copying: %+v", v)
	}
	live.Disks["disk0"] = vmCopyProgress{Chunks: 100, Done: 100, VerifyTotal: 100, VerifyDone: 25}
	live.Disks["disk1"] = vmCopyProgress{Chunks: 100, Done: 100}
	if v := live.view(now); v.Phase != "verify" || v.VerifyPercent != 12 || v.SecondsLeft != 0 {
		t.Fatalf("verifying: %+v", v)
	}
	live.Creating = true
	if v := live.view(now); v.Phase != "create" {
		t.Fatalf("creating: %+v", v)
	}
	if got := asCutoverReason(&mbLiveView{Phase: "verify", VerifyPercent: 50}); !strings.Contains(got, "50%") {
		t.Fatal(got)
	}
	sync := &mbLive{Started: now, Disks: map[string]vmCopyProgress{"d": {Chunks: 10, Done: 10}}}
	if v := sync.view(now); v.Phase != "copy" {
		t.Fatalf("a sync without verify stays copy: %+v", v)
	}
}

// Verify reports how far it has read the copy back, ending at every chunk.
func TestVerifyReportsItsProgress(t *testing.T) {
	data := vmTestDiskBytes(40 << 20)
	sink := &vmLocalSink{target: t.TempDir() + "/v.vhdx"}
	var last vmCopyProgress
	activity := 0
	result, err := vmCopyTo(context.Background(), &vmBytesSource{data: data, name: "disk"}, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true,
		Progress: func(p vmCopyProgress) { last = p },
		Activity: func(what string) {
			if what == "verifying" {
				activity++
			}
		}})
	if err != nil || !result.State.Verified {
		t.Fatalf("%v %+v", err, result)
	}
	if last.VerifyTotal != 40 || last.VerifyDone != 40 || activity == 0 {
		t.Fatalf("verify progress %+v, activity %d", last, activity)
	}
}

func TestFixUpWaitKeepsWhatTheVMAnswered(t *testing.T) {
	reason, ok := guestWaitReason("2026-11-28 03:52:10 [INFO] Waiting for Windows in the VM to start (The credential is invalid.).")
	if !ok || reason != "The credential is invalid." {
		t.Fatalf("%q %v", reason, ok)
	}
	if _, ok := guestWaitReason("2026-11-28 03:52:10 [INFO] The VM answers."); ok {
		t.Fatal("not a waiting line")
	}
	board := &guestFixBoard{}
	board.Set(guestFixStatus{VM: "DC02", State: guestFixWaiting})
	board.Tried("dc02", reason)
	if got := board.Get("DC02"); got.LastTry != reason || got.LastTryAt.IsZero() {
		t.Fatalf("%+v", got)
	}
	board.Update("DC02", guestFixRunning, "running")
	board.Tried("DC02", "later")
	if got := board.Get("DC02"); got.LastTry != reason {
		t.Fatal("only a waiting fix-up keeps answers")
	}
}
