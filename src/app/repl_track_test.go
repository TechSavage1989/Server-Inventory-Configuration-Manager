package main

import (
	"reflect"
	"testing"
)

func TestReplTrackBitmapRunsAndRanges(t *testing.T) {
	// clusters 0-2, 9, 16-23 and 30 set, of 31.
	bitmap := []byte{0x07, 0x02, 0xFF, 0x40}
	runs := replAllocatedRuns(bitmap, 31)
	want := []replRun{{0, 3}, {9, 1}, {16, 8}, {30, 1}}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("runs %v", runs)
	}
	previous := []byte{0x03, 0x00, 0x0F, 0x40}
	if got := replNewlyAllocated(previous, bitmap, 31); !reflect.DeepEqual(got, []replRun{{2, 1}, {9, 1}, {20, 4}}) {
		t.Fatalf("new %v", got)
	}
	ranges := replVolumeToDisk(1<<20, 4096, runs)
	if ranges[1] != (vmCopyRange{Start: 1<<20 + 9*4096, Length: 4096}) {
		t.Fatalf("ranges %v", ranges)
	}
	merged := replMergeRanges([]vmCopyRange{{100, 50}, {0, 10}, {10, 5}, {120, 100}, {400, 0}})
	if !reflect.DeepEqual(merged, []vmCopyRange{{0, 15}, {100, 120}}) {
		t.Fatalf("merged %v", merged)
	}
	if out := replOutside(1000, []vmCopyRange{{100, 200}, {250, 100}, {900, 100}}); !reflect.DeepEqual(out, []vmCopyRange{{0, 100}, {350, 550}}) {
		t.Fatalf("outside %v", out)
	}
	chunk := int64(4 << 20)
	tracked := []vmCopyRange{{chunk*2 + 10, 20}, {chunk * 5, chunk}}
	if missed := replMissedChunks(tracked, []int{2, 3, 5, 6}, chunk); !reflect.DeepEqual(missed, []int{3, 6}) {
		t.Fatalf("missed %v", missed)
	}
}

func TestReplTrackStoreKeepsStateAndBitmaps(t *testing.T) {
	store, err := newReplTrackStore(t.TempDir(), "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newReplTrackStore(t.TempDir(), "../x"); err == nil {
		t.Fatal("a path was accepted as a job ID")
	}
	if len(store.Load().Volumes) != 0 {
		t.Fatal("a new store has volumes")
	}
	state := replTrackState{Volumes: map[string]replVolumeTrack{"v1": {JournalID: 7, Mark: 99, ClusterSize: 4096, Clusters: 16}}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBitmap("v1", []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if got := store.Load(); got.Volumes["v1"].Mark != 99 {
		t.Fatalf("loaded %+v", got)
	}
	if _, err := store.LoadBitmap("v1", 3); err == nil {
		t.Fatal("a bitmap of the wrong size was accepted")
	}
	if raw, err := store.LoadBitmap("v1", 2); err != nil || raw[1] != 2 {
		t.Fatal(raw, err)
	}
	store.Forget()
	if len(store.Load().Volumes) != 0 {
		t.Fatal("Forget kept the state")
	}
}
