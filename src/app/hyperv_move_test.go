package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHyperVHost stands in for Move-HyperVVM.ps1. The VM's disks are real
// VHDX files; the checkpoint freezes them by handing out their paths.
type fakeHyperVHost struct {
	state          hypervVMState
	created        []string
	removed        []string
	shutdowns      int
	shutdownResult string
	removeErr      error
	duringCopy     func()
}

func (h *fakeHyperVHost) State(ctx context.Context, vmID string) (*hypervVMState, error) {
	copied := h.state
	return &copied, nil
}

func (h *fakeHyperVHost) Checkpoint(ctx context.Context, vmID string) (*hypervCheckpoint, error) {
	id := "cp-" + string(rune('a'+len(h.created)))
	h.created = append(h.created, id)
	frozen := []hypervDiskRef{}
	// Reversed on purpose: the round must put them back in VM order.
	for index := len(h.state.Disks) - 1; index >= 0; index-- {
		frozen = append(frozen, h.state.Disks[index])
	}
	return &hypervCheckpoint{ID: id, Disks: frozen}, nil
}

func (h *fakeHyperVHost) RemoveCheckpoint(ctx context.Context, vmID, id string) error {
	h.removed = append(h.removed, id)
	return h.removeErr
}

func (h *fakeHyperVHost) Shutdown(ctx context.Context, vmID string) (string, error) {
	h.shutdowns++
	if h.shutdownResult == "" {
		h.state.PowerState = "poweredOff"
		return "poweredOff", nil
	}
	return h.shutdownResult, nil
}

func hypervTestDisk(t *testing.T, path string, data []byte) {
	t.Helper()
	writer, err := createDiskWriter(vmDiskFormatVHDX, path, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(data); offset += 1 << 20 {
		chunk := data[offset : offset+1<<20]
		if !bytes.Equal(chunk, make([]byte, len(chunk))) {
			if _, err := writer.WriteAt(chunk, int64(offset)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func hypervTestPatch(t *testing.T, path string, data []byte, offset int64) {
	t.Helper()
	writer, err := openDiskWriter(vmDiskFormatVHDX, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt(data, offset); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func hypervReadAll(t *testing.T, path string) []byte {
	t.Helper()
	image, err := openDiskImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	data := make([]byte, image.Size())
	if _, err := image.ReadAt(data, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	return data
}

type hypervMoveFixture struct {
	host   *fakeHyperVHost
	data   [][]byte
	paths  []string
	folder string
}

func newHyperVMoveFixture(t *testing.T) *hypervMoveFixture {
	t.Helper()
	source := t.TempDir()
	f := &hypervMoveFixture{folder: filepath.Join(t.TempDir(), "Copies"), host: &fakeHyperVHost{state: hypervVMState{Name: "APP01", PowerState: "poweredOn", Checkpoints: []string{"Before patching"}}}}
	for index, size := range []int{96 << 20, 12 << 20} {
		data := make([]byte, size)
		rand.Read(data[:3<<20])
		rand.Read(data[size-(1<<20):])
		path := filepath.Join(source, []string{"APP01.vhdx", "APP01-data.vhdx"}[index])
		hypervTestDisk(t, path, data)
		f.data = append(f.data, data)
		f.paths = append(f.paths, path)
		f.host.state.Disks = append(f.host.state.Disks, hypervDiskRef{Key: []string{"83F8638B-8DCA-4152-9EDA-2CA8B33039B4\\0\\0\\D", "83F8638B-8DCA-4152-9EDA-2CA8B33039B4\\0\\1\\D"}[index], Path: `D:\VMs\` + filepath.Base(path), Reach: path})
	}
	return f
}

func (f *hypervMoveFixture) request(cutover bool) hypervMoveRequest {
	return hypervMoveRequest{VMID: "0A1B2C3D-0000-4000-8000-000000000001", Name: "APP01", CopyFolder: f.folder, Cutover: cutover, chunkSize: 1 << 20}
}

func TestHyperVCopyRoundsWhileRunningThenCutOver(t *testing.T) {
	f := newHyperVMoveFixture(t)
	logs := []string{}
	log := func(text string) { logs = append(logs, text) }

	result, err := hypervMoveRound(context.Background(), f.host, f.request(false), log)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.host.created) != 1 || len(f.host.removed) != 1 || f.host.removed[0] != f.host.created[0] {
		t.Fatalf("checkpoints created %v removed %v", f.host.created, f.host.removed)
	}
	if f.host.shutdowns != 0 || result.ShutDown {
		t.Fatal("a copy round must not shut the VM down")
	}
	if len(result.Disks) != 2 || filepath.Base(result.Disks[0]) != "APP01-disk0.vhdx" || filepath.Base(result.Disks[1]) != "APP01-disk1.vhdx" {
		t.Fatalf("targets %v", result.Disks)
	}
	for index := range f.data {
		if !bytes.Equal(hypervReadAll(t, result.Disks[index]), f.data[index]) {
			t.Fatalf("disk %d differs after the first round (disks out of order?)", index)
		}
		if result.Round.Disks[index].Mode != "full" {
			t.Fatalf("disk %d mode %s", index, result.Round.Disks[index].Mode)
		}
	}
	// Only the area that never held data was skipped on the first pass.
	if read := result.Round.Disks[0].BytesRead; read >= int64(len(f.data[0])) {
		t.Fatalf("the first round read %d bytes of a mostly empty disk", read)
	}

	// The guest writes between rounds; the next round is a delta pass.
	patch := make([]byte, 64<<10)
	rand.Read(patch)
	hypervTestPatch(t, f.paths[0], patch, 10<<20)
	copy(f.data[0][10<<20:], patch)
	hypervTestPatch(t, f.paths[0], make([]byte, 1<<20), 0)
	copy(f.data[0][:1<<20], make([]byte, 1<<20))
	result, err = hypervMoveRound(context.Background(), f.host, f.request(false), log)
	if err != nil {
		t.Fatal(err)
	}
	if result.Round.Disks[0].Mode != "delta" || result.Round.Disks[0].Changed != 2 || result.Round.Disks[1].Changed != 0 {
		t.Fatalf("delta round %+v", result.Round.Disks)
	}
	if !bytes.Equal(hypervReadAll(t, result.Disks[0]), f.data[0]) {
		t.Fatal("the delta round did not bring the copy up to date (including data cleared since)")
	}

	// Cutover: shut down through the guest, no checkpoint, verified copy.
	result, err = hypervMoveRound(context.Background(), f.host, f.request(true), log)
	if err != nil {
		t.Fatal(err)
	}
	if f.host.shutdowns != 1 || !result.ShutDown || len(f.host.created) != 2 {
		t.Fatalf("cutover shutdowns %d, checkpoints %v", f.host.shutdowns, f.host.created)
	}
	for _, disk := range result.Round.Disks {
		if !disk.Verified {
			t.Fatalf("cutover copy not verified: %+v", disk)
		}
	}
	if !strings.Contains(strings.Join(logs, "\n"), "never a forced power-off") {
		t.Fatal("the log does not say how the guest was shut down")
	}
}

func TestHyperVCheckpointIsRemovedWhenTheCopyFails(t *testing.T) {
	f := newHyperVMoveFixture(t)
	request := f.request(false)
	request.open = func(string) (vmDiskImage, error) { return nil, errors.New("access denied") }
	if _, err := hypervMoveRound(context.Background(), f.host, request, nil); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("error %v", err)
	}
	if len(f.host.removed) != 1 {
		t.Fatal("the checkpoint was left behind after a failed copy")
	}
	// A stopped round removes it too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hypervMoveRound(ctx, f.host, f.request(false), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v", err)
	}
	if len(f.host.removed) != 2 {
		t.Fatal("the checkpoint was left behind after a stopped copy")
	}
	// A removal that fails is logged so the operator can delete it.
	f.host.removeErr = errors.New("busy")
	logs := []string{}
	if _, err := hypervMoveRound(context.Background(), f.host, f.request(false), func(s string) { logs = append(logs, s) }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "Delete \""+hypervCheckpointName+"\" in Hyper-V Manager") {
		t.Fatalf("logs %v", logs)
	}
}

func TestHyperVMoveRefusals(t *testing.T) {
	f := newHyperVMoveFixture(t)
	request := f.request(false)
	request.Name = "APP02"
	if _, err := hypervMoveRound(context.Background(), f.host, request, nil); err == nil || !strings.Contains(err.Error(), "now called") {
		t.Fatalf("renamed VM: %v", err)
	}
	f.host.state.Checkpoints = append(f.host.state.Checkpoints, hypervCheckpointName)
	if _, err := hypervMoveRound(context.Background(), f.host, f.request(false), nil); err == nil || !strings.Contains(err.Error(), "earlier copy") {
		t.Fatalf("leftover checkpoint: %v", err)
	}
	f.host.state.Checkpoints = nil
	f.host.shutdownResult = "poweredOn"
	if _, err := hypervMoveRound(context.Background(), f.host, f.request(true), nil); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("guest that does not shut down: %v", err)
	}
	if _, err := os.Stat(f.folder); err == nil {
		t.Fatal("something was copied although the guest kept running")
	}
	if len(f.host.created) != 0 {
		t.Fatal("refused rounds must not create checkpoints")
	}
	if _, err := hypervMatchDisks(f.host.state.Disks, f.host.state.Disks[:1]); err == nil {
		t.Fatal("a checkpoint missing a disk was accepted")
	}
}

func TestHyperVOffVMCopiesWithoutACheckpoint(t *testing.T) {
	f := newHyperVMoveFixture(t)
	f.host.state.PowerState = "poweredOff"
	result, err := hypervMoveRound(context.Background(), f.host, f.request(false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.host.created) != 0 || result.Round.PowerState != "poweredOff" {
		t.Fatal("a VM that is off needs no checkpoint")
	}
}

func TestHyperVRoundSendsThroughTheGivenSink(t *testing.T) {
	f := newHyperVMoveFixture(t)
	request := f.request(false)
	folder := t.TempDir()
	sources := []string{}
	request.NewSink = func(index int, capacity int64) (vmCopySink, string, error) {
		target := filepath.Join(folder, "receiver-disk"+string(rune('0'+index))+".vhdx")
		return &recordingSink{vmCopySink: &vmLocalSink{target: target}, sources: &sources}, target, nil
	}
	result, err := hypervMoveRound(context.Background(), f.host, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(result.Disks[0]) != folder || len(sources) != 2 || sources[0] != hypervSourceID(request.VMID, f.host.state.Disks[0].Key) {
		t.Fatalf("targets %v sources %v", result.Disks, sources)
	}
	if !bytes.Equal(hypervReadAll(t, result.Disks[1]), f.data[1]) {
		t.Fatal("the sink's copy differs")
	}
}

type recordingSink struct {
	vmCopySink
	sources *[]string
}

func (s *recordingSink) Open(source string, size, chunkSize int64) (*vmCopyState, bool, error) {
	*s.sources = append(*s.sources, source)
	return s.vmCopySink.Open(source, size, chunkSize)
}

func TestHyperVDisksFollowTheInventoryOrder(t *testing.T) {
	f := newHyperVMoveFixture(t)
	request := f.request(false)
	request.DiskOrder = []string{f.host.state.Disks[1].Path, strings.ToLower(f.host.state.Disks[0].Path)}
	result, err := hypervMoveRound(context.Background(), f.host, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hypervReadAll(t, result.Disks[0]), f.data[1]) || !bytes.Equal(hypervReadAll(t, result.Disks[1]), f.data[0]) {
		t.Fatal("copies are not numbered in inventory order")
	}
	request.DiskOrder = []string{f.host.state.Disks[0].Path}
	if _, err := hypervMoveRound(context.Background(), f.host, request, nil); err == nil || !strings.Contains(err.Error(), "Read the Hyper-V host again") {
		t.Fatalf("a disk added since the read: %v", err)
	}
	request.DiskOrder = []string{f.host.state.Disks[0].Path, `D:\VMs\gone.vhdx`}
	if _, err := hypervMoveRound(context.Background(), f.host, request, nil); err == nil || !strings.Contains(err.Error(), "no longer uses") {
		t.Fatalf("a disk replaced since the read: %v", err)
	}
}

// fakeRCTHost adds Hyper-V change tracking to fakeHyperVHost: the test says
// which areas were written since each reference point.
type fakeRCTHost struct {
	*fakeHyperVHost
	noRecovery bool
	kept       []string
	removedRP  []string
	changes    []vmCopyRange
	changesErr error
	queries    []string
	keepErr    error
	extraIDs   []string
}

func (h *fakeRCTHost) TrackingCheckpoint(ctx context.Context, vmID string) (*hypervCheckpoint, error) {
	checkpoint, err := h.Checkpoint(ctx, vmID)
	if err != nil {
		return nil, err
	}
	if h.noRecovery {
		checkpoint.Note = "configuration version 5.0"
	} else {
		checkpoint.Recovery = true
	}
	return checkpoint, nil
}

func (h *fakeRCTHost) KeepReferencePoint(ctx context.Context, vmID, checkpointID, previous string) (*hypervReferencePoint, error) {
	if h.keepErr != nil {
		return nil, h.keepErr
	}
	id := "rp-" + checkpointID
	h.kept = append(h.kept, id)
	if previous != "" {
		h.removedRP = append(h.removedRP, previous)
	}
	point := &hypervReferencePoint{ID: id, Unmatched: h.extraIDs}
	for _, disk := range h.state.Disks {
		point.Disks = append(point.Disks, hypervRCTDisk{Key: strings.ToUpper(disk.Key), RCT: "rct:" + id + ":" + disk.Key})
	}
	return point, nil
}

func (h *fakeRCTHost) DiskChanges(ctx context.Context, vmID, path, limit string, size int64) ([]vmCopyRange, error) {
	h.queries = append(h.queries, path+"|"+limit)
	if h.changesErr != nil {
		return nil, h.changesErr
	}
	return h.changes, nil
}

func (h *fakeRCTHost) RemoveReferencePoint(ctx context.Context, vmID, id string) error {
	h.removedRP = append(h.removedRP, id)
	return nil
}

func newRCTFixture(t *testing.T) (*hypervMoveFixture, *fakeRCTHost, func(bool) hypervMoveRequest) {
	f := newHyperVMoveFixture(t)
	host := &fakeRCTHost{fakeHyperVHost: f.host}
	track := t.TempDir()
	return f, host, func(cutover bool) hypervMoveRequest {
		request := f.request(cutover)
		request.TrackFolder = track
		return request
	}
}

func TestHyperVChangeTrackingReadsOnlyChangedAreas(t *testing.T) {
	f, host, request := newRCTFixture(t)
	logs := []string{}
	log := func(text string) { logs = append(logs, text) }

	// First round: no reference point yet, so every disk is read; the
	// recovery checkpoint is kept as a reference point, not removed.
	result, err := hypervMoveRound(context.Background(), host, request(false), log)
	if err != nil {
		t.Fatal(err)
	}
	if len(host.kept) != 1 || len(host.removed) != 0 || len(host.queries) != 0 {
		t.Fatalf("kept %v removed %v queries %v", host.kept, host.removed, host.queries)
	}
	record := loadHypervRCTRecord(request(false).TrackFolder, request(false).VMID)
	if record == nil || record.ReferencePoint != host.kept[0] || record.Checkpoint != host.created[0] || len(record.Disks) != 2 {
		t.Fatalf("record %+v", record)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "no change tracking reference point is kept yet") {
		t.Fatalf("the log does not say why the first round reads everything: %v", logs)
	}

	// The guest writes; Hyper-V reports that area. Only it is read, and the
	// copy matches the disk.
	patch := make([]byte, 64<<10)
	rand.Read(patch)
	hypervTestPatch(t, f.paths[0], patch, 40<<20)
	copy(f.data[0][40<<20:], patch)
	host.changes = []vmCopyRange{{Start: 40 << 20, Length: 64 << 10}}
	logs = nil
	result, err = hypervMoveRound(context.Background(), host, request(false), log)
	if err != nil {
		t.Fatal(err)
	}
	if len(host.queries) != 2 || !strings.Contains(host.queries[0], "|rct:"+host.kept[0]+":") {
		t.Fatalf("queries %v", host.queries)
	}
	disk0 := result.Round.Disks[0]
	if disk0.Mode != "changed" || disk0.Read != "rct" || disk0.Tracked == 0 || disk0.BytesRead != 1<<20 || disk0.Changed != 1 {
		t.Fatalf("changed round %+v", disk0)
	}
	if !bytes.Equal(hypervReadAll(t, result.Disks[0]), f.data[0]) {
		t.Fatal("the changed-area round did not bring the copy up to date")
	}
	if len(host.kept) != 2 || len(host.removedRP) != 1 || host.removedRP[0] != host.kept[0] {
		t.Fatalf("the previous reference point was not replaced: kept %v removed %v", host.kept, host.removedRP)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "Hyper-V change tracking (RCT) reports") {
		t.Fatalf("the log does not name the read method: %v", logs)
	}

	// Cutover: VM off, changed areas only, verified, then the last
	// reference point and the record are removed.
	hypervTestPatch(t, f.paths[1], patch, 2<<20)
	copy(f.data[1][2<<20:], patch)
	host.changes = []vmCopyRange{{Start: 2 << 20, Length: 64 << 10}}
	result, err = hypervMoveRound(context.Background(), host, request(true), log)
	if err != nil {
		t.Fatal(err)
	}
	for index, disk := range result.Round.Disks {
		if disk.Mode != "changed" || !disk.Verified || !bytes.Equal(hypervReadAll(t, result.Disks[index]), f.data[index]) {
			t.Fatalf("cutover disk %d %+v", index, disk)
		}
	}
	if last := host.removedRP[len(host.removedRP)-1]; last != host.kept[1] {
		t.Fatalf("the last reference point %s was not removed (%v)", host.kept[1], host.removedRP)
	}
	if loadHypervRCTRecord(request(false).TrackFolder, request(false).VMID) != nil {
		t.Fatal("the record outlived the cutover")
	}
}

func TestHyperVChangeTrackingFallsBackToFullReads(t *testing.T) {
	f, host, request := newRCTFixture(t)
	if _, err := hypervMoveRound(context.Background(), host, request(false), nil); err != nil {
		t.Fatal(err)
	}
	// Hyper-V cannot answer: the disks are read in full and stay correct.
	patch := make([]byte, 64<<10)
	rand.Read(patch)
	hypervTestPatch(t, f.paths[0], patch, 20<<20)
	copy(f.data[0][20<<20:], patch)
	host.changesErr = errors.New("tracking was reset")
	logs := []string{}
	result, err := hypervMoveRound(context.Background(), host, request(false), func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Round.Disks[0].Mode != "delta" || !bytes.Equal(hypervReadAll(t, result.Disks[0]), f.data[0]) {
		t.Fatalf("fallback %+v", result.Round.Disks[0])
	}
	if !strings.Contains(strings.Join(logs, "\n"), "could not answer (tracking was reset)") {
		t.Fatalf("logs %v", logs)
	}
	// An answer outside the disk is not trusted either.
	host.changesErr = nil
	host.changes = []vmCopyRange{{Start: 1 << 40, Length: 1 << 20}}
	if result, err = hypervMoveRound(context.Background(), host, request(false), nil); err != nil || result.Round.Disks[0].Mode != "delta" {
		t.Fatalf("an impossible area was trusted: %v %+v", err, result.Round.Disks)
	}
	// A copy made somewhere else is not followed by RCT.
	other := request(false)
	other.CopyFolder = filepath.Join(t.TempDir(), "Elsewhere")
	host.changes = nil
	if result, err = hypervMoveRound(context.Background(), host, other, nil); err != nil || result.Round.Disks[0].Mode != "full" || result.Round.Disks[0].Read != "file" || result.Round.Disks[0].Why == "" {
		t.Fatalf("a new copy target used change tracking: %v %+v", err, result.Round.Disks)
	}
	// A failed keep leaves the checkpoint to be removed as before.
	host.keepErr = errors.New("busy")
	removed := len(host.removed)
	if _, err := hypervMoveRound(context.Background(), host, request(false), nil); err != nil {
		t.Fatal(err)
	}
	if len(host.removed) != removed+1 {
		t.Fatal("the checkpoint was not removed after it could not be kept")
	}
}

func TestHyperVWithoutRecoveryCheckpointsReadsInFull(t *testing.T) {
	_, host, request := newRCTFixture(t)
	host.noRecovery = true
	logs := []string{}
	for round := 0; round < 2; round++ {
		result, err := hypervMoveRound(context.Background(), host, request(false), func(s string) { logs = append(logs, s) })
		if err != nil {
			t.Fatal(err)
		}
		if result.Round.Disks[0].Mode == "changed" {
			t.Fatal("a standard checkpoint was followed by change tracking")
		}
	}
	if len(host.kept) != 0 || len(host.removed) != 2 || len(host.queries) != 0 {
		t.Fatalf("kept %v removed %v queries %v", host.kept, host.removed, host.queries)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "configuration version 5.0") {
		t.Fatalf("logs %v", logs)
	}
	// Without a record folder the host's tracking is not used at all.
	plain := request(false)
	plain.TrackFolder = ""
	host.noRecovery = false
	if _, err := hypervMoveRound(context.Background(), host, plain, nil); err != nil || len(host.kept) != 0 {
		t.Fatalf("tracking used without a record folder: %v %v", err, host.kept)
	}
}

func TestHyperVOffVMFollowsTheKeptReferencePoint(t *testing.T) {
	f, host, request := newRCTFixture(t)
	if _, err := hypervMoveRound(context.Background(), host, request(false), nil); err != nil {
		t.Fatal(err)
	}
	host.state.PowerState = "poweredOff"
	patch := make([]byte, 4096)
	rand.Read(patch)
	hypervTestPatch(t, f.paths[1], patch, 5<<20)
	copy(f.data[1][5<<20:], patch)
	host.changes = []vmCopyRange{{Start: 5 << 20, Length: 4096}}
	for round := 0; round < 2; round++ {
		result, err := hypervMoveRound(context.Background(), host, request(false), nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Round.Disks[1].Mode != "changed" || !bytes.Equal(hypervReadAll(t, result.Disks[1]), f.data[1]) {
			t.Fatalf("round %d off-VM disk %+v", round, result.Round.Disks[1])
		}
	}
	if len(host.created) != 1 || len(host.kept) != 1 {
		t.Fatalf("an off VM made checkpoints %v or reference points %v", host.created, host.kept)
	}
}
