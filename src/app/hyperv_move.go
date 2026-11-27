package main

// Copying a Hyper-V VM's disks while it runs (phase 4 step 3). Each round
// takes this application's own checkpoint, which freezes the disks the VM
// was writing to (Hyper-V sends new writes to a new .avhdx), copies those
// frozen disks with the disk readers (which merge any older checkpoint
// chain), and removes the checkpoint again, which merges the .avhdx back.
// The checkpoint is removed even when the copy fails or is stopped. Repeat
// rounds are delta passes: every chunk is read and hashed, and only chunks
// that changed are written or sent. Cutover shuts the guest down through
// the Hyper-V shutdown integration service (never a forced power-off) and
// copies the disks as they are, with no checkpoint, then verifies the copy.
// The source VM is left off and otherwise unchanged, so going back is
// starting it again.
//
// Hyper-V's own change tracking (RCT, Windows Server 2016 and later) makes
// repeat rounds read only what changed: when the host offers it
// (hypervTracker), each running round takes a recovery checkpoint instead of
// a standard one and, once every disk is copied, keeps it as a reference
// point (Hyper-V merges the checkpoint as it does so). The next round asks
// Hyper-V for the areas written since that reference point and reads only
// those; the cutover does the same with the VM off. The reference point
// replaces the previous one this application kept, and the last one is
// removed after a successful cutover. Anything RCT cannot answer is read in
// full as before, and the log says which way each disk was read.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hypervCheckpointName names the only checkpoint this application creates.
const hypervCheckpointName = "Server Inventory and Migration Manager copy"

// hypervDiskRef is one virtual hard disk of a VM: Key identifies the drive
// across the VM and its checkpoints, Path is the file Hyper-V names and
// Reach is the path this computer opens it through.
type hypervDiskRef struct {
	Key   string `json:"key"`
	Path  string `json:"path"`
	Reach string `json:"reach"`
}

type hypervVMState struct {
	Name        string          `json:"name"`
	PowerState  string          `json:"powerState"`
	Checkpoints []string        `json:"checkpoints"`
	Disks       []hypervDiskRef `json:"disks"`
}

type hypervCheckpoint struct {
	ID    string          `json:"id"`
	Disks []hypervDiskRef `json:"disks"`
	// Recovery is true for a recovery checkpoint, which can become a
	// change tracking reference point; Note says why it is not one.
	Recovery bool   `json:"recovery"`
	Note     string `json:"note"`
}

// hypervReferencePoint is a reference point kept from this application's
// recovery checkpoint, with each disk's RCT ID. Unmatched lists the disk
// identifiers Hyper-V gave that matched none of the VM's disks.
type hypervReferencePoint struct {
	ID        string          `json:"id"`
	Disks     []hypervRCTDisk `json:"disks"`
	Unmatched []string        `json:"unmatched"`
}

type hypervRCTDisk struct {
	Key string `json:"key"`
	RCT string `json:"rct"`
}

// hypervTracker is implemented by hosts that offer Hyper-V resilient change
// tracking. A host without it copies with standard checkpoints and reads
// every round in full.
type hypervTracker interface {
	// TrackingCheckpoint creates this application's checkpoint as a
	// recovery checkpoint when the VM allows it, otherwise as Checkpoint
	// does (Recovery false, with the reason in Note).
	TrackingCheckpoint(ctx context.Context, vmID string) (*hypervCheckpoint, error)
	// KeepReferencePoint turns this application's recovery checkpoint into
	// a reference point (the checkpoint is merged and gone afterwards) and
	// then removes the reference point previous, when given.
	KeepReferencePoint(ctx context.Context, vmID, checkpointID, previous string) (*hypervReferencePoint, error)
	// DiskChanges lists the areas of the virtual hard disk at path (a host
	// path) written since the RCT ID limit.
	DiskChanges(ctx context.Context, vmID, path, limit string, size int64) ([]vmCopyRange, error)
	// RemoveReferencePoint removes the VM's reference point with this ID.
	RemoveReferencePoint(ctx context.Context, vmID, id string) error
}

// hypervRCTRecord is saved on this computer after each round that kept a
// reference point. Checkpoint is the checkpoint whose frozen disks the
// copies hold; a copy record whose ChangeID names it (hypervRCTTag) can be
// brought up to date with the RCT IDs in Disks.
type hypervRCTRecord struct {
	VMID           string            `json:"vmId"`
	Checkpoint     string            `json:"checkpoint"`
	ReferencePoint string            `json:"referencePoint"`
	Disks          map[string]string `json:"disks"`
	Saved          time.Time         `json:"saved"`
}

func hypervRCTTag(checkpoint string) string { return "hyperv-rct:" + checkpoint }

func hypervRCTRecordPath(folder, vmID string) string {
	return filepath.Join(folder, strings.ToLower(vmUnsafeFileName.ReplaceAllString(vmID, "-"))+"-change-tracking.json")
}

func loadHypervRCTRecord(folder, vmID string) *hypervRCTRecord {
	if folder == "" {
		return nil
	}
	data, err := os.ReadFile(hypervRCTRecordPath(folder, vmID))
	if err != nil {
		return nil
	}
	var record hypervRCTRecord
	if json.Unmarshal(data, &record) != nil || !strings.EqualFold(record.VMID, vmID) || record.Checkpoint == "" || record.ReferencePoint == "" {
		return nil
	}
	return &record
}

func saveHypervRCTRecord(folder string, record *hypervRCTRecord) error {
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := hypervRCTRecordPath(folder, record.VMID)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// hypervDiskChanges is DiskChanges' answer from the host script: parallel
// lists of byte offsets and lengths.
type hypervDiskChanges struct {
	Offsets []int64 `json:"offsets"`
	Lengths []int64 `json:"lengths"`
}

func (c hypervDiskChanges) ranges() ([]vmCopyRange, error) {
	if len(c.Offsets) != len(c.Lengths) {
		return nil, fmt.Errorf("Hyper-V gave %d offsets but %d lengths", len(c.Offsets), len(c.Lengths))
	}
	ranges := make([]vmCopyRange, 0, len(c.Offsets))
	for i := range c.Offsets {
		ranges = append(ranges, vmCopyRange{Start: c.Offsets[i], Length: c.Lengths[i]})
	}
	return ranges, nil
}

// hypervTracking is one round's view of change tracking for hypervCopyDisk.
type hypervTracking struct {
	tracker hypervTracker
	vmID    string
	record  *hypervRCTRecord
	// tag is recorded as the copies' ChangeID ("" when this round's copies
	// cannot be followed by an RCT round).
	tag string
	// why explains, for the log, why disks are read in full.
	why string
}

// limitFor returns the RCT ID to read this disk's changes from, or "" with
// the reason the disk is read in full.
func (t *hypervTracking) limitFor(disk hypervDiskRef, state *vmCopyState) (string, string) {
	switch {
	case t == nil || t.tracker == nil:
		return "", "this host offers no Hyper-V change tracking"
	case t.why != "":
		return "", t.why
	case t.record == nil:
		return "", "no change tracking reference point is kept yet (this round keeps one)"
	case !strings.HasSuffix(strings.ToLower(disk.Path), ".vhdx"):
		return "", "it is read through " + filepath.Base(strings.ReplaceAll(disk.Path, `\`, "/")) + ", not a .vhdx file Hyper-V tracks on its own (the VM has other checkpoints, or the disk is a VHD or VHD Set)"
	case state == nil || !state.Complete || state.ChangeID != hypervRCTTag(t.record.Checkpoint):
		return "", "its copy was not made from the round the reference point keeps"
	case t.record.Disks[strings.ToLower(disk.Key)] == "":
		return "", "Hyper-V gave no change tracking ID for this disk"
	}
	return t.record.Disks[strings.ToLower(disk.Key)], ""
}

// hypervHost is the host side of a move. hyperv_move_windows.go runs it
// through Move-HyperVVM.ps1; tests use a fake.
type hypervHost interface {
	State(ctx context.Context, vmID string) (*hypervVMState, error)
	// Checkpoint creates this application's checkpoint and returns the
	// disks it froze.
	Checkpoint(ctx context.Context, vmID string) (*hypervCheckpoint, error)
	// RemoveCheckpoint removes the checkpoint with this ID, only when it is
	// this application's.
	RemoveCheckpoint(ctx context.Context, vmID, checkpointID string) error
	// Shutdown asks the guest to shut down and waits for the VM to be off.
	Shutdown(ctx context.Context, vmID string) (string, error)
}

type hypervMoveRequest struct {
	VMID       string
	Name       string
	CopyFolder string
	Cutover    bool
	// DiskOrder lists the disk files in inventory order, so copies are
	// numbered as the inventory (and the new VM) expect.
	DiskOrder []string
	// NewSink sends the disks somewhere other than a VHDX in CopyFolder.
	NewSink func(index int, capacity int64) (vmCopySink, string, error)
	// Progress reports each disk's copy progress (for the live view).
	Progress func(disk string, p vmCopyProgress)
	// Throttle limits how fast the disks are read; nil is no limit.
	Throttle *vmThrottle
	// Space checks the volumes the checkpoint grows on (Volumes gives them
	// for the VM's disk files) and TargetVolume, the copy's destination,
	// before a running VM's checkpoint and watches them while the round
	// runs (prodguard.go).
	Space        *pgGuard
	SpaceOwner   string
	Volumes      func(paths []string) []pgVolume
	TargetVolume func() (pgVolume, bool)
	// TrackFolder keeps the change tracking record between rounds; empty
	// turns Hyper-V change tracking off.
	TrackFolder string
	// open opens a disk image; tests may replace it.
	open      func(path string) (vmDiskImage, error)
	chunkSize int64
}

// hypervDiskSource gives a disk image a stable identity across rounds, so a
// copy record matches the same VM disk whichever file is frozen this time.
type hypervDiskSource struct {
	vmDiskImage
	id string
}

func (s hypervDiskSource) Describe() string { return s.id }

func hypervSourceID(vmID, key string) string {
	return "hyperv:" + strings.ToLower(vmID) + "/" + strings.ToLower(key)
}

// hypervMoveRound runs one copy round, or the cutover, for one VM.
func hypervMoveRound(ctx context.Context, host hypervHost, request hypervMoveRequest, log func(string)) (_ *vmMoveResult, err error) {
	if log == nil {
		log = func(string) {}
	}
	if request.open == nil {
		request.open = openDiskImage
	}
	result := &vmMoveResult{VM: request.Name, Cutover: request.Cutover}
	round := &vmReplicaResult{VM: request.Name, Started: time.Now().UTC()}
	result.Round = round
	state, err := host.State(ctx, request.VMID)
	if err != nil {
		return result, fmt.Errorf("the VM could not be read on the Hyper-V host: %w", err)
	}
	if state.Name != request.Name {
		return result, fmt.Errorf("the VM with this ID is now called %q, not %q. Read the Hyper-V host again", state.Name, request.Name)
	}
	for _, name := range state.Checkpoints {
		if name == hypervCheckpointName {
			return result, errors.New("the VM still has a checkpoint named \"" + hypervCheckpointName + "\" from an earlier copy that could not be removed. Delete it in Hyper-V Manager (this merges it), then copy again")
		}
	}
	if len(state.Disks) == 0 {
		return result, errors.New("the VM has no virtual hard disks to copy")
	}
	if err := hypervOrderDisks(state, request.DiskOrder); err != nil {
		return result, err
	}
	disks := state.Disks
	tracking := &hypervTracking{vmID: request.VMID}
	if tracker, ok := host.(hypervTracker); ok && request.TrackFolder != "" {
		tracking.tracker = tracker
		tracking.record = loadHypervRCTRecord(request.TrackFolder, request.VMID)
	}
	if request.Cutover {
		if state.PowerState != "poweredOff" {
			log("Shutting down " + request.Name + " through the guest (never a forced power-off).")
			power, err := host.Shutdown(ctx, request.VMID)
			if err != nil {
				return result, fmt.Errorf("the guest could not be shut down, so nothing was cut over: %w", err)
			}
			if power != "poweredOff" {
				return result, errors.New("the VM is still running; the cutover stopped before the last copy")
			}
		}
		result.ShutDown = true
		round.PowerState = "poweredOff"
		if state, err = host.State(ctx, request.VMID); err != nil {
			return result, fmt.Errorf("the VM could not be read after shutting down: %w", err)
		}
		if err := hypervOrderDisks(state, request.DiskOrder); err != nil {
			return result, err
		}
		disks = state.Disks
		tracking.offTag()
	} else if state.PowerState != "poweredOff" {
		round.PowerState = state.PowerState
		if request.Space != nil {
			paths := []string{}
			for _, disk := range state.Disks {
				paths = append(paths, disk.Reach)
			}
			read := func(context.Context) ([]pgVolume, error) {
				volumes := []pgVolume{}
				if request.Volumes != nil {
					volumes = request.Volumes(paths)
				}
				if request.TargetVolume != nil {
					if target, ok := request.TargetVolume(); ok {
						volumes = append(volumes, target)
					}
				}
				return volumes, nil
			}
			volumes, _ := read(ctx)
			claim, err := request.Space.Admit(request.SpaceOwner, volumes)
			if err != nil {
				return result, err
			}
			defer claim.Release()
			guarded, cancel := context.WithCancel(ctx)
			defer cancel()
			claim.Watch(guarded, cancel, read, log)
			ctx = guarded
			defer func() { err = claim.Wrap(err) }()
		}
		log("Creating checkpoint \"" + hypervCheckpointName + "\" on " + request.Name + ".")
		var checkpoint *hypervCheckpoint
		if tracking.tracker != nil {
			checkpoint, err = tracking.tracker.TrackingCheckpoint(ctx, request.VMID)
		} else {
			checkpoint, err = host.Checkpoint(ctx, request.VMID)
		}
		if err != nil {
			return result, fmt.Errorf("the checkpoint could not be created: %w", err)
		}
		kept := false
		defer func() {
			if kept {
				return
			}
			// Removed even when the round failed or was stopped.
			removeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if removeErr := host.RemoveCheckpoint(removeCtx, request.VMID, checkpoint.ID); removeErr != nil {
				log("The checkpoint could not be removed: " + removeErr.Error() + ". Delete \"" + hypervCheckpointName + "\" in Hyper-V Manager.")
			} else {
				log("Removed the checkpoint; Hyper-V merges it in the background.")
			}
		}()
		frozen, err := hypervMatchDisks(state.Disks, checkpoint.Disks)
		if err != nil {
			return result, err
		}
		disks = frozen
		if tracking.tracker != nil {
			if checkpoint.Recovery {
				tracking.tag = hypervRCTTag(checkpoint.ID)
			} else {
				tracking.why = "Hyper-V could not make a recovery checkpoint for this VM (" + vmNonEmpty(checkpoint.Note, "no reason given") + "), which change tracking needs; a VM needs configuration version 6.2 or later (Windows Server 2016)"
			}
		}
		if err := hypervCopyDisks(ctx, request, disks, tracking, round, result, log); err != nil {
			return result, err
		}
		if tracking.tag != "" {
			kept = hypervKeepReferencePoint(request, checkpoint, tracking, log)
		}
		round.Finished = time.Now().UTC()
		result.Finished = round.Finished
		return result, nil
	} else {
		round.PowerState = "poweredOff"
		tracking.offTag()
	}

	if err := hypervCopyDisks(ctx, request, disks, tracking, round, result, log); err != nil {
		return result, err
	}
	if request.Cutover && tracking.record != nil {
		// The move is finished: remove the reference point it kept.
		removeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := tracking.tracker.RemoveReferencePoint(removeCtx, request.VMID, tracking.record.ReferencePoint); err != nil {
			log("The change tracking reference point could not be removed: " + err.Error() + ". It holds no data and does not affect the VM.")
		} else {
			log("Removed the change tracking reference point this move kept.")
			os.Remove(hypervRCTRecordPath(request.TrackFolder, request.VMID))
		}
	}
	round.Finished = time.Now().UTC()
	result.Finished = round.Finished
	return result, nil
}

// offTag lets a round of a VM that is off keep following the kept reference
// point: whatever is written after this read was also written after the
// reference point, so changes since it still cover everything the copy lacks.
func (t *hypervTracking) offTag() {
	if t.tracker != nil && t.record != nil {
		t.tag = hypervRCTTag(t.record.Checkpoint)
	}
}

func hypervCopyDisks(ctx context.Context, request hypervMoveRequest, disks []hypervDiskRef, tracking *hypervTracking, round *vmReplicaResult, result *vmMoveResult, log func(string)) error {
	for index, disk := range disks {
		if err := ctx.Err(); err != nil {
			return err
		}
		diskResult, err := hypervCopyDisk(ctx, request, index, disk, tracking, log)
		if diskResult != nil {
			round.Disks = append(round.Disks, *diskResult)
			result.Disks = append(result.Disks, diskResult.Target)
		}
		if err != nil {
			return err
		}
		log(fmt.Sprintf("%s: %s copy finished, %s read, %s written.", diskResult.Label, diskResult.Mode, vmFormatBytes(diskResult.BytesRead), vmFormatBytes(diskResult.BytesWritten)))
	}
	return nil
}

// hypervKeepReferencePoint keeps this round's recovery checkpoint as a
// reference point and saves the record the next round reads. It returns
// true when the checkpoint is gone (merged by Hyper-V as it became one).
func hypervKeepReferencePoint(request hypervMoveRequest, checkpoint *hypervCheckpoint, tracking *hypervTracking, log func(string)) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	previous := ""
	if tracking.record != nil {
		previous = tracking.record.ReferencePoint
	}
	point, err := tracking.tracker.KeepReferencePoint(ctx, request.VMID, checkpoint.ID, previous)
	if err != nil || point == nil || point.ID == "" {
		if err == nil {
			err = errors.New("Hyper-V did not return the reference point")
		}
		log("The checkpoint could not be kept for change tracking (" + err.Error() + "); the next copy reads all used data again.")
		return false
	}
	record := &hypervRCTRecord{VMID: request.VMID, Checkpoint: checkpoint.ID, ReferencePoint: point.ID, Disks: map[string]string{}, Saved: time.Now().UTC()}
	for _, disk := range point.Disks {
		if disk.Key != "" && disk.RCT != "" {
			record.Disks[strings.ToLower(disk.Key)] = disk.RCT
		}
	}
	if len(point.Unmatched) > 0 {
		log("Hyper-V named reference point disks this move does not know (" + strings.Join(point.Unmatched, ", ") + "); those disks are read in full next time.")
	}
	if err := saveHypervRCTRecord(request.TrackFolder, record); err != nil {
		log("The change tracking record could not be saved (" + err.Error() + "); the next copy reads all used data again.")
		return true
	}
	log("Kept the checkpoint as a Hyper-V change tracking reference point (Hyper-V merges it into the disks); the next copy reads only the blocks written after it.")
	return true
}

// hypervOrderDisks puts the VM's disks in inventory order and refuses when
// they no longer match what was read.
func hypervOrderDisks(state *hypervVMState, order []string) error {
	if len(order) == 0 {
		return nil
	}
	if len(order) != len(state.Disks) {
		return fmt.Errorf("the VM now has %d virtual hard disks, not the %d read earlier. Read the Hyper-V host again", len(state.Disks), len(order))
	}
	ordered := make([]hypervDiskRef, 0, len(order))
	used := make([]bool, len(state.Disks))
	for _, path := range order {
		found := false
		for index, disk := range state.Disks {
			if !used[index] && strings.EqualFold(disk.Path, path) {
				ordered, used[index], found = append(ordered, disk), true, true
				break
			}
		}
		if !found {
			return fmt.Errorf("the VM no longer uses %s. Read the Hyper-V host again", path)
		}
	}
	state.Disks = ordered
	return nil
}

// hypervMatchDisks puts the checkpoint's frozen disks in the VM's disk order.
func hypervMatchDisks(current, frozen []hypervDiskRef) ([]hypervDiskRef, error) {
	byKey := map[string]hypervDiskRef{}
	for _, disk := range frozen {
		byKey[strings.ToLower(disk.Key)] = disk
	}
	ordered := make([]hypervDiskRef, 0, len(current))
	for _, disk := range current {
		match, ok := byKey[strings.ToLower(disk.Key)]
		if !ok {
			return nil, fmt.Errorf("the checkpoint has no copy of the disk %s; the VM's disks changed while the checkpoint was made", disk.Path)
		}
		match.Key = disk.Key
		ordered = append(ordered, match)
	}
	return ordered, nil
}

func hypervCopyDisk(ctx context.Context, request hypervMoveRequest, index int, disk hypervDiskRef, tracking *hypervTracking, log func(string)) (*vmReplicaDiskResult, error) {
	path := vmNonEmpty(disk.Reach, disk.Path)
	label := fmt.Sprintf("Disk %d (%s)", index+1, filepath.Base(strings.ReplaceAll(disk.Path, `\`, "/")))
	result := &vmReplicaDiskResult{Label: label, Source: disk.Path}
	image, err := request.open(path)
	if err != nil {
		return result, fmt.Errorf("%s could not be opened: %w", path, err)
	}
	defer image.Close()
	source := hypervDiskSource{vmDiskImage: image, id: hypervSourceID(request.VMID, disk.Key)}
	chunk := request.chunkSize
	if chunk <= 0 {
		chunk = vmCopyChunkSize
	}
	var sink vmCopySink
	if request.NewSink != nil {
		created, target, err := request.NewSink(index, image.Size())
		if err != nil {
			return result, err
		}
		sink, result.Target = created, target
	} else {
		if request.CopyFolder == "" {
			return result, errors.New("choose a folder for the copied disks")
		}
		result.Target = filepath.Join(request.CopyFolder, vmReplicaFileName(request.Name, index))
		sink = &vmLocalSink{target: result.Target}
	}
	defer sink.Close()
	state, resumed, err := sink.Open(source.Describe(), image.Size(), chunk)
	if err != nil {
		return result, err
	}
	wasComplete := state.Complete
	options := vmCopyOptions{ChunkSize: chunk, Verify: request.Cutover}
	if tracking != nil {
		options.ChangeID = tracking.tag
	}
	changed := false
	limit, why := tracking.limitFor(disk, state)
	if limit != "" {
		ranges, err := tracking.tracker.DiskChanges(ctx, tracking.vmID, disk.Path, limit, image.Size())
		if err == nil {
			err = vmCheckRanges(ranges, image.Size())
		}
		if err != nil {
			why = "Hyper-V change tracking could not answer (" + err.Error() + ")"
		} else {
			changed = true
			options.Ranges = vmMergeRanges(ranges)
			if options.Ranges == nil {
				options.Ranges = []vmCopyRange{}
			}
			total := int64(0)
			for _, r := range options.Ranges {
				total += r.Length
			}
			log(fmt.Sprintf("%s: reading only the %d areas (%s) Hyper-V change tracking (RCT) reports written since the last copy.", label, len(options.Ranges), vmFormatBytes(total)))
		}
	}
	if !changed {
		log(fmt.Sprintf("%s: reading all used data and sending only changed chunks, because %s.", label, why))
	}
	if allocated := image.Allocated(); !changed && allocated != nil {
		// Read the areas that hold data now, plus chunks that held data in
		// the copy, so data removed since then is cleared there too.
		ranges := append([]vmCopyRange(nil), allocated...)
		for i, hash := range state.Hashes {
			if hash != "" && hash != vmCopyZeroHash {
				ranges = append(ranges, vmCopyRange{Start: int64(i) * state.ChunkSize, Length: vmChunkLength(state, i)})
			}
		}
		options.Ranges, options.ZeroOutside = vmMergeRanges(ranges), true
		if options.Ranges == nil {
			options.Ranges = []vmCopyRange{}
		}
	}
	last := time.Time{}
	options.Progress = func(p vmCopyProgress) {
		if request.Progress != nil {
			request.Progress(label, p)
		}
		if p.Chunks > 0 && time.Since(last) > 10*time.Second {
			last = time.Now()
			log(fmt.Sprintf("%s: %d%% (%s read, %s written).", label, p.Done*100/p.Chunks, vmFormatBytes(p.BytesRead), vmFormatBytes(p.BytesWritten)))
		}
	}
	copied, err := vmCopyTo(ctx, vmThrottleSource(ctx, source, request.Throttle), &vmOpenedSink{vmCopySink: sink, state: state, resumed: resumed}, options)
	if err != nil {
		return result, err
	}
	result.BytesRead, result.BytesWritten = copied.Progress.BytesRead, copied.Progress.BytesWritten
	result.Changed, result.Verified = copied.Progress.Changed, copied.State.Verified
	switch {
	case changed:
		result.Mode = "changed"
	case resumed && wasComplete:
		result.Mode = "delta"
	case resumed:
		result.Mode = "resumed"
	default:
		result.Mode = "full"
	}
	return result, nil
}

// vmCheckRanges refuses changed areas outside the disk or of no length, so a
// wrong answer is read in full rather than trusted.
func vmCheckRanges(ranges []vmCopyRange, size int64) error {
	for _, r := range ranges {
		if r.Start < 0 || r.Length <= 0 || r.Start > size || r.Length > size-r.Start {
			return fmt.Errorf("an area at %d of %d bytes lies outside the %d-byte disk", r.Start, r.Length, size)
		}
	}
	return nil
}
