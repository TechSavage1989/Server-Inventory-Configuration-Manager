package main

// Replication rounds copy a running VMware VM's disks into VHDX files with
// the copy engine. Each round takes a snapshot so the base disks stop
// changing, copies them (the first round only the allocated areas, later
// rounds only the blocks vSphere's changed block tracking reports since the
// previous round), then removes that snapshot. The final round runs after
// the guest has been shut down, so the copy matches the VM exactly.
//
// The only vSphere changes are: turning on changed block tracking when the
// operator allows it, and creating and removing this application's own
// snapshot (vmReplicaSnapshotName). A VM with any other snapshot is refused,
// because its base disk would not be its current state.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

const vmReplicaSnapshotName = "Server Inventory and Migration Manager replication"

type vmReplicaOptions struct {
	Datacenter string
	// Folder receives one VHDX per disk plus its copy record.
	Folder string
	// EnableCBT lets the round turn on changed block tracking when it is off.
	EnableCBT bool
	// Quiesce asks VMware Tools to quiesce the guest file systems for the
	// snapshot (application-consistent on Windows through VSS).
	Quiesce bool
	Verify  bool
	// RecordFolder receives a JSON record before the first vSphere change.
	RecordFolder string
	Progress     func(disk string, progress vmCopyProgress)
	Log          func(string)
	// NewSink, when set, sends each disk somewhere other than a VHDX in
	// Folder (for example a receiver's raw disk). It returns the sink and a
	// label for the target; the sink's own copy record decides the mode.
	NewSink func(index int, capacity int64) (vmCopySink, string, error)
	// Throttle paces what is read from the datastore; nil reads at full
	// speed. One throttle shared by several VMs limits them together.
	Throttle *vmThrottle
}

// vmThrottledSource paces reads through a throttle.
type vmThrottledSource struct {
	vmCopySource
	ctx      context.Context
	throttle *vmThrottle
}

func (s *vmThrottledSource) ReadAt(p []byte, offset int64) (int, error) {
	if err := s.throttle.Wait(s.ctx, int64(len(p))); err != nil {
		return 0, err
	}
	return s.vmCopySource.ReadAt(p, offset)
}

// vmThrottleSource wraps source when a throttle is set.
func vmThrottleSource(ctx context.Context, source vmCopySource, throttle *vmThrottle) vmCopySource {
	if throttle == nil {
		return source
	}
	return &vmThrottledSource{vmCopySource: source, ctx: ctx, throttle: throttle}
}

// vmOpenedSink is a sink already opened to read its copy record; the engine's
// own Open returns that record.
type vmOpenedSink struct {
	vmCopySink
	state   *vmCopyState
	resumed bool
}

func (s *vmOpenedSink) Open(source string, size, chunkSize int64) (*vmCopyState, bool, error) {
	if s.state.Source != source || s.state.Size != size || s.state.ChunkSize != chunkSize {
		return nil, false, errors.New("the copy target changed between reading its record and copying")
	}
	return vmCopyStateClone(s.state), s.resumed, nil
}

type vmReplicaDiskResult struct {
	Label        string `json:"label"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	Mode         string `json:"mode"`
	BytesRead    int64  `json:"bytesRead"`
	BytesWritten int64  `json:"bytesWritten"`
	Changed      int    `json:"changed"`
	ChangeID     string `json:"changeId"`
	Verified     bool   `json:"verified"`
}

type vmReplicaResult struct {
	VM         string                `json:"vm"`
	PowerState string                `json:"powerState"`
	EnabledCBT bool                  `json:"enabledCbt"`
	Disks      []vmReplicaDiskResult `json:"disks"`
	Started    time.Time             `json:"started"`
	Finished   time.Time             `json:"finished"`
}

// vmChangedAreas is QueryChangedDiskAreas; tests replace it because the
// vSphere simulator does not implement it.
var vmChangedAreas = func(ctx context.Context, client *vim25.Client, vm, snapshot types.ManagedObjectReference, deviceKey int32, offset int64, changeID string) (types.DiskChangeInfo, error) {
	response, err := methods.QueryChangedDiskAreas(ctx, client, &types.QueryChangedDiskAreas{This: vm, Snapshot: &snapshot, DeviceKey: deviceKey, StartOffset: offset, ChangeId: changeID})
	if err != nil {
		return types.DiskChangeInfo{}, err
	}
	return response.Returnval, nil
}

// vmChangedRanges collects every changed area of a disk, asking again until
// vSphere has covered the whole capacity.
func vmChangedRanges(ctx context.Context, client *vim25.Client, vm, snapshot types.ManagedObjectReference, deviceKey int32, capacity int64, changeID string) ([]vmCopyRange, error) {
	ranges := []vmCopyRange{}
	offset := int64(0)
	for offset < capacity {
		info, err := vmChangedAreas(ctx, client, vm, snapshot, deviceKey, offset, changeID)
		if err != nil {
			return nil, err
		}
		for _, area := range info.ChangedArea {
			ranges = append(ranges, vmCopyRange{Start: area.Start, Length: area.Length})
		}
		next := info.StartOffset + info.Length
		if info.Length <= 0 || next <= offset {
			return nil, errors.New("vSphere returned no progress for the changed areas of this disk")
		}
		offset = next
	}
	return ranges, nil
}

var vmUnsafeFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func vmReplicaFileName(vm string, index int) string {
	name := strings.Trim(vmUnsafeFileName.ReplaceAllString(vm, "-"), "-.")
	if name == "" {
		name = "vm"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return fmt.Sprintf("%s-disk%d.vhdx", name, index)
}

type vmReplicaDisk struct {
	key      int32
	label    string
	file     string
	changeID string
	capacity int64
}

func vmReplicaDisks(devices []types.BaseVirtualDevice) ([]vmReplicaDisk, error) {
	disks := []vmReplicaDisk{}
	for _, device := range devices {
		disk, ok := device.(*types.VirtualDisk)
		if !ok {
			continue
		}
		label := fmt.Sprintf("disk %d", disk.Key)
		if info := disk.DeviceInfo; info != nil {
			label = info.GetDescription().Label
		}
		backing, ok := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)
		if !ok {
			return nil, fmt.Errorf("%s is not a flat VMDK (%T); raw device mappings and sparse disks are not copied by this step", label, disk.Backing)
		}
		if backing.KeyId != nil {
			return nil, fmt.Errorf("%s is encrypted; decrypt the VM with Prepare source first", label)
		}
		if backing.Parent != nil {
			return nil, fmt.Errorf("%s still has a snapshot chain; remove snapshots with Prepare source first", label)
		}
		capacity := disk.CapacityInBytes
		if capacity == 0 {
			capacity = disk.CapacityInKB * 1024
		}
		disks = append(disks, vmReplicaDisk{key: disk.Key, label: label, file: backing.FileName, changeID: backing.ChangeId, capacity: capacity})
	}
	if len(disks) == 0 {
		return nil, errors.New("the VM has no virtual disks")
	}
	return disks, nil
}

func vmFindSnapshots(tree []types.VirtualMachineSnapshotTree, ours *[]types.ManagedObjectReference) (others int) {
	for _, node := range tree {
		if node.Name == vmReplicaSnapshotName {
			*ours = append(*ours, node.Snapshot)
		} else {
			others++
		}
		others += vmFindSnapshots(node.ChildSnapshotList, ours)
	}
	return others
}

func vmReplicaRecord(folder string, machine mo.VirtualMachine, action string) error {
	if folder == "" {
		return nil
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	record := map[string]interface{}{
		"vm":                    machine.Name,
		"action":                action,
		"time":                  time.Now().UTC(),
		"powerState":            machine.Runtime.PowerState,
		"changeTrackingEnabled": machine.Config.ChangeTrackingEnabled != nil && *machine.Config.ChangeTrackingEnabled,
	}
	raw, _ := json.MarshalIndent(record, "", "  ")
	name := filepath.Join(folder, vmUnsafeFileName.ReplaceAllString(machine.Name, "-")+"-"+time.Now().UTC().Format("20060102-150405")+"-replication.json")
	return os.WriteFile(name, raw, 0600)
}

// vmReplicateRound runs one replication round for a VM.
func vmReplicateRound(ctx context.Context, client *vim25.Client, ref types.ManagedObjectReference, options vmReplicaOptions) (*vmReplicaResult, error) {
	log := options.Log
	if log == nil {
		log = func(string) {}
	}
	if options.Folder == "" && options.NewSink == nil {
		return nil, errors.New("choose a folder for the copied disks")
	}
	collector := property.DefaultCollector(client)
	var machine mo.VirtualMachine
	if err := collector.RetrieveOne(ctx, ref, []string{"name", "config", "runtime.powerState", "snapshot"}, &machine); err != nil {
		return nil, err
	}
	if machine.Config == nil {
		return nil, errors.New("vSphere did not return this VM's configuration")
	}
	result := &vmReplicaResult{VM: machine.Name, PowerState: string(machine.Runtime.PowerState), Started: time.Now().UTC()}
	if machine.Config.Template {
		return nil, errors.New("templates are not replicated")
	}
	if _, err := vmReplicaDisks(machine.Config.Hardware.Device); err != nil {
		return nil, err
	}
	vmObject := object.NewVirtualMachine(client, ref)

	var leftovers []types.ManagedObjectReference
	if machine.Snapshot != nil {
		if others := vmFindSnapshots(machine.Snapshot.RootSnapshotList, &leftovers); others > 0 {
			return nil, fmt.Errorf("%s has %d snapshot(s) not made by this application; remove them with Prepare source first so the copy is the VM's current state", machine.Name, others)
		}
	}
	if len(leftovers) > 0 {
		if err := vmReplicaRecord(options.RecordFolder, machine, "remove a replication snapshot left by an earlier round"); err != nil {
			return nil, err
		}
		for _, snapshot := range leftovers {
			log("Removing the replication snapshot left by an earlier round.")
			if err := vmRemoveReplicaSnapshot(ctx, client, snapshot); err != nil {
				return nil, err
			}
		}
	}

	cbt := machine.Config.ChangeTrackingEnabled != nil && *machine.Config.ChangeTrackingEnabled
	if !cbt && options.EnableCBT {
		if err := vmReplicaRecord(options.RecordFolder, machine, "turn on changed block tracking"); err != nil {
			return nil, err
		}
		log("Turning on changed block tracking.")
		task, err := vmObject.Reconfigure(ctx, types.VirtualMachineConfigSpec{ChangeTrackingEnabled: types.NewBool(true)})
		if err == nil {
			err = task.Wait(ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("could not turn on changed block tracking: %w", err)
		}
		cbt, result.EnabledCBT = true, true
	}

	if err := vmReplicaRecord(options.RecordFolder, machine, "create and remove the replication snapshot"); err != nil {
		return nil, err
	}
	log("Taking the replication snapshot.")
	task, err := vmObject.CreateSnapshot(ctx, vmReplicaSnapshotName, "Taken while copying this VM's disks; removed when the copy round ends.", false, options.Quiesce && machine.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn)
	if err != nil {
		return nil, err
	}
	info, err := task.WaitForResult(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not take the replication snapshot: %w", err)
	}
	snapshot, ok := info.Result.(types.ManagedObjectReference)
	if !ok {
		return nil, errors.New("vSphere did not return the new snapshot")
	}
	defer func() {
		// Always remove our snapshot, even when the copy failed or was
		// stopped, with a context that the stop does not cancel.
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		log("Removing the replication snapshot.")
		if err := vmRemoveReplicaSnapshot(cleanup, client, snapshot); err != nil {
			log("Could not remove the replication snapshot: " + err.Error() + ". Remove \"" + vmReplicaSnapshotName + "\" in vSphere.")
		}
	}()

	var frozen mo.VirtualMachineSnapshot
	if err := collector.RetrieveOne(ctx, snapshot, []string{"config"}, &frozen); err != nil {
		return nil, err
	}
	disks, err := vmReplicaDisks(frozen.Config.Hardware.Device)
	if err != nil {
		return nil, err
	}
	if options.NewSink == nil {
		if err := os.MkdirAll(options.Folder, 0700); err != nil {
			return nil, err
		}
	}
	for index, disk := range disks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target := filepath.Join(options.Folder, vmReplicaFileName(machine.Name, index))
		item := vmReplicaDiskResult{Label: disk.label, Source: disk.file, Target: target, ChangeID: disk.changeID}
		copyOptions := vmCopyOptions{Verify: options.Verify, ChangeID: disk.changeID}
		if options.Progress != nil {
			label := disk.label
			copyOptions.Progress = func(p vmCopyProgress) { options.Progress(label, p) }
		}
		var sink vmCopySink
		var source vmCopySource
		previous, stateErr := (*vmCopyState)(nil), error(nil)
		if options.NewSink != nil {
			// Open the source and the sink first: the sink's record (on the
			// receiver) says which kind of round this disk needs.
			if source, err = openDatastoreVMDK(ctx, client, options.Datacenter, disk.file); err != nil {
				return nil, fmt.Errorf("%s: %w", disk.label, err)
			}
			base, label, err := options.NewSink(index, disk.capacity)
			if err != nil {
				source.Close()
				return nil, fmt.Errorf("%s: %w", disk.label, err)
			}
			state, resumed, err := base.Open(source.Describe(), source.Size(), vmCopyChunkSize)
			if err != nil {
				source.Close()
				base.Close()
				return nil, fmt.Errorf("%s: %w", disk.label, err)
			}
			sink, item.Target = &vmOpenedSink{vmCopySink: base, state: state, resumed: resumed}, label
			previous, stateErr = state, nil
			if !resumed {
				stateErr = os.ErrNotExist
			}
		} else {
			previous, stateErr = loadVMCopyState(target)
		}
		switch {
		case !cbt || disk.changeID == "":
			item.Mode = "full"
		case stateErr == nil && previous.Complete && previous.ChangeID != "" && previous.Source == disk.file:
			ranges, err := vmChangedRanges(ctx, client, ref, snapshot, disk.key, disk.capacity, previous.ChangeID)
			if err != nil {
				log(disk.label + ": changed block tracking could not answer (" + err.Error() + "); reading the whole disk.")
				item.Mode = "full"
			} else {
				item.Mode, copyOptions.Ranges = "changed", ranges
			}
		case stateErr != nil:
			ranges, err := vmChangedRanges(ctx, client, ref, snapshot, disk.key, disk.capacity, "*")
			if err != nil {
				log(disk.label + ": could not list the allocated areas (" + err.Error() + "); reading the whole disk.")
				item.Mode = "full"
			} else {
				item.Mode, copyOptions.Ranges, copyOptions.ZeroOutside = "seed", ranges, true
			}
		default:
			item.Mode = "full"
		}
		log(fmt.Sprintf("%s: %s copy of %s to %s.", disk.label, item.Mode, disk.file, item.Target))
		if source == nil {
			if source, err = openDatastoreVMDK(ctx, client, options.Datacenter, disk.file); err != nil {
				return nil, fmt.Errorf("%s: %w", disk.label, err)
			}
			sink = &vmLocalSink{target: target}
		}
		if source.Size() != disk.capacity {
			source.Close()
			sink.Close()
			return nil, fmt.Errorf("%s: the VMDK is %d bytes but vSphere reports %d", disk.label, source.Size(), disk.capacity)
		}
		copied, err := vmCopyTo(ctx, vmThrottleSource(ctx, source, options.Throttle), sink, copyOptions)
		source.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", disk.label, err)
		}
		item.BytesRead, item.BytesWritten, item.Changed, item.Verified = copied.Progress.BytesRead, copied.Progress.BytesWritten, copied.Progress.Changed, copied.State.Verified
		result.Disks = append(result.Disks, item)
	}
	result.Finished = time.Now().UTC()
	return result, nil
}

func vmRemoveReplicaSnapshot(ctx context.Context, client *vim25.Client, snapshot types.ManagedObjectReference) error {
	consolidate := true
	response, err := methods.RemoveSnapshot_Task(ctx, client, &types.RemoveSnapshot_Task{This: snapshot, RemoveChildren: false, Consolidate: &consolidate})
	if err != nil {
		return err
	}
	return object.NewTask(client, response.Returnval).Wait(ctx)
}
