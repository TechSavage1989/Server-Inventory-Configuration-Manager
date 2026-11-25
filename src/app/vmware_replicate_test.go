package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// replicaTestVM gives the simulator's first VM one small flat disk whose
// files the test controls, and returns the bytes and the local flat path.
func replicaTestVM(t *testing.T, ctx context.Context, client *vim25.Client, size int64) (types.ManagedObjectReference, string, []byte) {
	t.Helper()
	vm := simulator.Map.Any("VirtualMachine").(*simulator.VirtualMachine)
	var disk *types.VirtualDisk
	for _, device := range vm.Config.Hardware.Device {
		if d, ok := device.(*types.VirtualDisk); ok {
			if disk == nil {
				disk = d
			} else {
				d.Backing = nil
			}
		}
	}
	devices := []types.BaseVirtualDevice{}
	for _, device := range vm.Config.Hardware.Device {
		if d, ok := device.(*types.VirtualDisk); ok && d != disk {
			continue
		}
		devices = append(devices, device)
	}
	vm.Config.Hardware.Device = devices
	disk.CapacityInBytes, disk.CapacityInKB = size, size/1024
	backing := disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo)
	datastore := simulator.Map.Any("Datastore").(*simulator.Datastore)
	backing.FileName = "[" + datastore.Name + "] app01/app01.vmdk"
	folder := filepath.Join(datastore.Info.GetDatastoreInfo().Url, "app01")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	data := vmTestDiskBytes(size)
	flat := filepath.Join(folder, "app01-flat.vmdk")
	os.WriteFile(flat, data, 0600)
	os.WriteFile(filepath.Join(folder, "app01.vmdk"), []byte("parentCID=ffffffff\nRW "+strconv.FormatInt(size/512, 10)+" VMFS \"app01-flat.vmdk\"\n"), 0600)
	return vm.Reference(), flat, data
}

func TestReplicationRoundsSeedThenCopyOnlyChangedBlocks(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	server := model.Service.NewServer()
	defer server.Close()
	ctx := context.Background()
	client, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewManager(client).Login(ctx, server.URL.User); err != nil {
		t.Fatal(err)
	}
	size := int64(80 << 20)
	ref, flatPath, data := replicaTestVM(t, ctx, client, size)

	// Changed block tracking, as vSphere would answer it: "*" lists the
	// allocated areas, a change ID lists what changed since it.
	changeIDs := 0
	changed := []types.DiskChangeExtent{}
	allocated := []types.DiskChangeExtent{{Start: 0, Length: 3 << 20}, {Start: vhdxBlockSize - (1 << 20), Length: 2 << 20}, {Start: size - 4096, Length: 4096}}
	var askedWith []string
	original := vmChangedAreas
	defer func() { vmChangedAreas = original }()
	vmChangedAreas = func(ctx context.Context, client *vim25.Client, vm, snapshot types.ManagedObjectReference, key int32, offset int64, changeID string) (types.DiskChangeInfo, error) {
		askedWith = append(askedWith, changeID)
		areas := changed
		if changeID == "*" {
			areas = allocated
		}
		// Answer in two halves so the caller has to ask again.
		half := size / 2
		info := types.DiskChangeInfo{StartOffset: offset, Length: half}
		for _, area := range areas {
			if area.Start >= offset && area.Start < offset+half {
				info.ChangedArea = append(info.ChangedArea, area)
			}
		}
		return info, nil
	}
	simVM := simulator.Map.Get(ref).(*simulator.VirtualMachine)
	setChangeID := func() {
		changeIDs++
		for _, device := range simVM.Config.Hardware.Device {
			if disk, ok := device.(*types.VirtualDisk); ok {
				disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo).ChangeId = "52 aa bb/" + strconv.Itoa(changeIDs)
			}
		}
	}
	setChangeID()

	folder := filepath.Join(t.TempDir(), "replica")
	records := filepath.Join(t.TempDir(), "records")
	options := vmReplicaOptions{Datacenter: "ha-datacenter", Folder: folder, EnableCBT: true, Verify: true, RecordFolder: records}
	first, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.EnabledCBT || len(first.Disks) != 1 || first.Disks[0].Mode != "seed" || !first.Disks[0].Verified {
		t.Fatalf("first round %+v", first)
	}
	// The seed reads only the allocated areas: 3 + 2 + 4 chunks of 4 MB at most.
	if first.Disks[0].BytesRead > 4*(4<<20) {
		t.Fatalf("the seed read %d bytes instead of only the allocated areas", first.Disks[0].BytesRead)
	}
	if !bytes.Equal(vmReadVHDX(t, first.Disks[0].Target), data) {
		t.Fatal("the seeded VHDX differs from the VM disk")
	}
	var machine mo.VirtualMachine
	property.DefaultCollector(client).RetrieveOne(ctx, ref, []string{"config", "snapshot"}, &machine)
	if machine.Config.ChangeTrackingEnabled == nil || !*machine.Config.ChangeTrackingEnabled {
		t.Fatal("changed block tracking was not turned on")
	}
	if machine.Snapshot != nil && len(machine.Snapshot.RootSnapshotList) > 0 {
		t.Fatal("the replication snapshot was left behind")
	}
	if entries, _ := os.ReadDir(records); len(entries) == 0 {
		t.Fatal("no record was saved before vSphere was changed")
	}

	// The guest writes; the next round asks for changes since the seed's ID
	// and copies only that chunk.
	rand.Read(data[50<<20 : 50<<20+512])
	os.WriteFile(flatPath, data, 0600)
	changed = []types.DiskChangeExtent{{Start: 50 << 20, Length: 512}}
	setChangeID()
	second, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disks[0].Mode != "changed" || second.Disks[0].BytesRead != 4<<20 || second.Disks[0].Changed != 1 {
		t.Fatalf("second round %+v", second.Disks[0])
	}
	if askedWith[len(askedWith)-1] != "52 aa bb/1" {
		t.Fatalf("the round asked for changes since %q", askedWith[len(askedWith)-1])
	}
	if !bytes.Equal(vmReadVHDX(t, second.Disks[0].Target), data) {
		t.Fatal("the delta round left the VHDX different from the VM disk")
	}
	state, _ := loadVMCopyState(second.Disks[0].Target)
	if state.ChangeID != "52 aa bb/2" {
		t.Fatalf("the copy record holds change ID %q", state.ChangeID)
	}

	// A replication snapshot left by a stopped round is removed first.
	task, _ := object.NewVirtualMachine(client, ref).CreateSnapshot(ctx, vmReplicaSnapshotName, "", false, false)
	task.Wait(ctx)
	setChangeID()
	changed = nil
	if third, err := vmReplicateRound(ctx, client, ref, options); err != nil || third.Disks[0].BytesRead != 0 {
		t.Fatalf("a leftover replication snapshot was not handled: %v", err)
	}
	machine = mo.VirtualMachine{}
	property.DefaultCollector(client).RetrieveOne(ctx, ref, []string{"snapshot"}, &machine)
	if machine.Snapshot != nil && len(machine.Snapshot.RootSnapshotList) > 0 {
		t.Fatal("the leftover replication snapshot is still there")
	}

	// Someone else's snapshot stops the round before anything changes.
	task, _ = object.NewVirtualMachine(client, ref).CreateSnapshot(ctx, "before patching", "", false, false)
	task.Wait(ctx)
	if _, err := vmReplicateRound(ctx, client, ref, options); err == nil || !strings.Contains(err.Error(), "not made by this application") {
		t.Fatalf("a VM with another snapshot was replicated: %v", err)
	}
}

func TestReplicaDisksRefuseRDMsEncryptionAndChains(t *testing.T) {
	disk := func(backing types.BaseVirtualDeviceBackingInfo) []types.BaseVirtualDevice {
		return []types.BaseVirtualDevice{&types.VirtualDisk{VirtualDevice: types.VirtualDevice{Key: 2000, Backing: backing}, CapacityInBytes: 1 << 30}}
	}
	for name, backing := range map[string]types.BaseVirtualDeviceBackingInfo{
		"not a flat VMDK": &types.VirtualDiskRawDiskMappingVer1BackingInfo{},
		"encrypted":       &types.VirtualDiskFlatVer2BackingInfo{KeyId: &types.CryptoKeyId{KeyId: "k"}},
		"snapshot chain":  &types.VirtualDiskFlatVer2BackingInfo{Parent: &types.VirtualDiskFlatVer2BackingInfo{}},
	} {
		if _, err := vmReplicaDisks(disk(backing)); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := vmReplicaDisks(nil); err == nil {
		t.Fatal("a VM without disks was accepted")
	}
	if got := vmReplicaFileName(`..\Payroll SQL/01:`, 2); got != "Payroll-SQL-01-disk2.vhdx" {
		t.Fatalf("file name %q", got)
	}
}
