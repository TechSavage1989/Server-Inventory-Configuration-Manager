package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// The owner's 3.3.0-test.3 plan (2026-11-27): MATCHC-DC02's round read all
// 90 GB after earlier rounds had read only changes. A round that stopped
// part way left the copy record unfinished, and the next round read the
// whole disk with no reason given. It now reads what changed since the last
// whole copy, and every full read says why.
func TestReplicaRoundReadsChangesAfterAStoppedRound(t *testing.T) {
	disk := vmReplicaDisk{label: "Hard disk 1", file: "[ds1] app/app.vmdk", changeID: "52 aa/9", capacity: 64 << 20}
	asked := []string{}
	changed := func(id string) ([]vmCopyRange, error) {
		asked = append(asked, id)
		if id == "*" {
			return []vmCopyRange{{Start: 0, Length: 16 << 20}}, nil
		}
		return []vmCopyRange{{Start: 4 << 20, Length: 1 << 20}}, nil
	}
	logs := []string{}
	log := func(s string) { logs = append(logs, s) }
	chunk := int64(4 << 20)
	stopped := &vmCopyState{Source: disk.file, Size: disk.capacity, ChunkSize: chunk, Hashes: make([]string, 16), ChangeID: "52 aa/7", Complete: false}
	mode, ranges, zero, why := vmReplicaPlanDisk(disk, true, stopped, nil, changed, log)
	if mode != "changed" || why != "" || zero || len(ranges) != 1 || asked[0] != "52 aa/7" {
		t.Fatalf("a stopped round's copy was not read as changes since the last whole copy: %s %v %q %v", mode, ranges, why, asked)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "stopped part way") {
		t.Fatalf("the log did not say the last round stopped: %v", logs)
	}

	// No changes at all still limits the read (an empty list, never nil,
	// which would mean every chunk).
	none := func(string) ([]vmCopyRange, error) { return nil, nil }
	if _, ranges, _, _ := vmReplicaPlanDisk(disk, true, stopped, nil, none, log); ranges == nil {
		t.Fatal("a round with no changes would read the whole disk")
	}

	// A seed that stopped before its first change ID: allocated areas plus
	// every chunk that already holds data, and a reason.
	seed := &vmCopyState{Source: disk.file, Size: disk.capacity, ChunkSize: chunk, Hashes: make([]string, 16)}
	seed.Hashes[10] = "abc"
	seed.Hashes[11] = vmCopyZeroHash
	mode, ranges, zero, why = vmReplicaPlanDisk(disk, true, seed, nil, changed, log)
	inRanges := vmCopyInRanges(seed, ranges)
	if mode != "seed" || !zero || why == "" || !inRanges[0] || !inRanges[3] || !inRanges[10] || inRanges[11] || inRanges[12] {
		t.Fatalf("an unfinished seed was not read as used data plus old data: %s %v %v %q", mode, ranges, zero, why)
	}

	// No record: a seed of the allocated areas, nothing to explain.
	if mode, _, zero, why := vmReplicaPlanDisk(disk, true, nil, os.ErrNotExist, changed, log); mode != "seed" || !zero || why != "" {
		t.Fatalf("a first copy was not a seed: %s %v %q", mode, zero, why)
	}

	// Each full read says why.
	for name, c := range map[string]struct {
		cbt      bool
		disk     vmReplicaDisk
		previous *vmCopyState
		changed  func(string) ([]vmCopyRange, error)
		want     string
	}{
		"tracking off":   {false, disk, stopped, changed, "tracking is off"},
		"no change ID":   {true, vmReplicaDisk{label: "d", file: disk.file}, stopped, changed, "no change ID"},
		"moved disk":     {true, disk, &vmCopyState{Source: "[ds2] app/app.vmdk", ChangeID: "x", Complete: true}, changed, "moved or renamed"},
		"tracking reset": {true, disk, stopped, func(string) ([]vmCopyRange, error) { return nil, errors.New("FileFault") }, "reset tracking"},
	} {
		mode, ranges, _, why := vmReplicaPlanDisk(c.disk, c.cbt, c.previous, nil, c.changed, log)
		if mode != "full" || ranges != nil || !strings.Contains(why, c.want) {
			t.Errorf("%s: %s %v %q", name, mode, ranges, why)
		}
	}
}

func TestPlanRoundSummarySaysHowTheDisksWereRead(t *testing.T) {
	vm := &mbVM{LastMode: "changed", LastVia: "vddk", LastTracked: 3 << 30, LastRead: 3 << 30, LastWritten: 2 << 30, LastSeconds: 240}
	if got := mbRoundSummary(vm); !strings.HasPrefix(got, "changed blocks through VDDK, 3.0 GB reported by change tracking") {
		t.Fatalf("summary: %s", got)
	}
	vm = &mbVM{LastMode: "full", LastVia: "export", LastRead: 90 << 30, LastWhy: "Hard disk 1: changed block tracking is off"}
	if got := mbRoundSummary(vm); !strings.HasPrefix(got, "full read,") || !strings.HasSuffix(got, "tracking is off") {
		t.Fatalf("summary: %s", got)
	}
	if got := mbViaText("export", "changed"); !strings.Contains(got, "export") {
		t.Fatalf("export: %s", got)
	}
}

// Five-minute syncs: the next round waits mbRoundGap after a round that ran
// longer than the interval, never starting straight after it.
func TestShortSyncIntervalsLeaveAGapAfterALongRound(t *testing.T) {
	plan := &mbPlan{Settings: mbSettings{SyncMinutes: 5}}
	if err := mbValidateSettings(&plan.Settings); err != nil {
		t.Fatalf("5-minute syncs refused: %v", err)
	}
	start := mbDueNow.AddDate(56, 0, 0)
	vm := &mbVM{State: mbReady, Seeded: true, LastAttempt: start, LastEnded: start.Add(30 * 60e9)}
	if next := mbNextSync(plan, vm); !next.Equal(vm.LastEnded.Add(mbRoundGap)) {
		t.Fatalf("next round at %v, want %v", next, vm.LastEnded.Add(mbRoundGap))
	}
	vm.LastEnded = start.Add(60e9)
	if next := mbNextSync(plan, vm); !next.Equal(start.Add(5 * 60e9)) {
		t.Fatalf("next round at %v, want the interval", next)
	}
}

// A test ended by a version that does not keep TestedAt (a stable build
// rewrites the plans file without it) can be recorded by hand.
func TestPlanMarksVMsTestedByHand(t *testing.T) {
	runner, _ := mbTestRunner(t, &mbFakeTarget{})
	plan, err := runner.Create("P", "vc", mbSettings{SyncMinutes: 60, Concurrency: 1}, mbVMs("app01", "app02"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.MarkTested(plan.ID, nil); err == nil {
		t.Fatal("marking no VMs must be refused")
	}
	id := plan.VMs[0].ID
	if err := runner.MarkTested(plan.ID, []string{id}); err != nil {
		t.Fatal(err)
	}
	views, _, _ := runner.View()
	if views[0].VMs[0].TestedAt.IsZero() || !views[0].VMs[1].TestedAt.IsZero() {
		t.Fatalf("only the chosen VM is tested: %+v", views[0].VMs)
	}
}

// Fields a newer version wrote survive a save by a version that does not
// know them (a stable build saving a test-channel plan file).
func TestPlanSaveKeepsFieldsItDoesNotKnow(t *testing.T) {
	old := []byte(`[{"id":"p1","name":"P","futurePlan":1,"settings":{"syncMinutes":60,"futureSetting":"x"},"vms":[{"id":"a","name":"A","error":"old error","futureTestedAt":"2026-11-27T10:00:00Z"},{"id":"b","name":"B"}]},{"id":"gone","futurePlan":2}]`)
	fresh := []byte(`[{"id":"p1","name":"P","settings":{"syncMinutes":30},"vms":[{"id":"a","name":"A"},{"id":"b","name":"B"}]}]`)
	got := strings.Join(strings.Fields(string(mbKeepUnknownFields(fresh, old))), "")
	for _, want := range []string{`"futurePlan":1`, `"futureSetting":"x"`, `"futureTestedAt":"2026-11-27T10:00:00Z"`, `"syncMinutes":30`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "old error") || strings.Contains(got, `"futurePlan":2`) {
		t.Errorf("a field this version cleared, or a deleted plan, came back: %s", got)
	}
	if string(mbKeepUnknownFields(fresh, []byte(`[{"id":"p1","name":"P"}]`))) != string(fresh) {
		t.Error("a file with nothing unknown must be written as it is")
	}
}
