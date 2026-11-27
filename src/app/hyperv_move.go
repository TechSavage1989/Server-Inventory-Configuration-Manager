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

import (
	"context"
	"errors"
	"fmt"
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
		checkpoint, err := host.Checkpoint(ctx, request.VMID)
		if err != nil {
			return result, fmt.Errorf("the checkpoint could not be created: %w", err)
		}
		defer func() {
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
	} else {
		round.PowerState = "poweredOff"
	}

	for index, disk := range disks {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		diskResult, err := hypervCopyDisk(ctx, request, index, disk, log)
		if diskResult != nil {
			round.Disks = append(round.Disks, *diskResult)
			result.Disks = append(result.Disks, diskResult.Target)
		}
		if err != nil {
			return result, err
		}
		log(fmt.Sprintf("%s: %s copy finished, %s read, %s written.", diskResult.Label, diskResult.Mode, vmFormatBytes(diskResult.BytesRead), vmFormatBytes(diskResult.BytesWritten)))
	}
	round.Finished = time.Now().UTC()
	result.Finished = round.Finished
	return result, nil
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

func hypervCopyDisk(ctx context.Context, request hypervMoveRequest, index int, disk hypervDiskRef, log func(string)) (*vmReplicaDiskResult, error) {
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
	if allocated := image.Allocated(); allocated != nil {
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
	case resumed && wasComplete:
		result.Mode = "delta"
	case resumed:
		result.Mode = "resumed"
	default:
		result.Mode = "full"
	}
	return result, nil
}
