package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"strconv"
	"testing"

	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// Replication rounds into a raw-disk receiver: the seed and the changed-block
// round go over the network onto the new VM's disk, and the receiver's own
// record decides the round.
func TestReplicationRoundsCopyOntoAReceiverDisk(t *testing.T) {
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
	changed := []types.DiskChangeExtent{}
	allocated := []types.DiskChangeExtent{{Start: 0, Length: 3 << 20}, {Start: vhdxBlockSize - (1 << 20), Length: 2 << 20}, {Start: size - 4096, Length: 4096}}
	original := vmChangedAreas
	defer func() { vmChangedAreas = original }()
	vmChangedAreas = func(ctx context.Context, client *vim25.Client, vm, snapshot types.ManagedObjectReference, key int32, offset int64, changeID string) (types.DiskChangeInfo, error) {
		areas := changed
		if changeID == "*" {
			areas = allocated
		}
		return types.DiskChangeInfo{StartOffset: offset, Length: size - offset, ChangedArea: areas}, nil
	}
	simVM := simulator.Map.Get(ref).(*simulator.VirtualMachine)
	changeIDs := 0
	setChangeID := func() {
		changeIDs++
		for _, device := range simVM.Config.Hardware.Device {
			if disk, ok := device.(*types.VirtualDisk); ok {
				disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo).ChangeId = "52 cc/" + strconv.Itoa(changeIDs)
			}
		}
	}
	setChangeID()

	// The new VM's blank disk, rounded up to a whole GiB as a platform would.
	target := &vmMemoryDevice{data: make([]byte, size+(4<<20)), id: "virtio-0"}
	receiver, err := startVMReceiverWith("127.0.0.1:0", vmReceiverConfig{Records: t.TempDir(), Disks: []vmReceiverDisk{{Name: "disk-0", Size: int64(len(target.data)), open: func() (vmBlockDevice, error) { return target, nil }}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	link := vmReceiverLink{Address: "127.0.0.1:" + strconv.Itoa(receiver.Port()), Thumbprint: receiver.Thumbprint(), Code: receiver.Code()}
	disks, err := vmReceiverListDisks(ctx, link.Address, link.Thumbprint, link.Code)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := vmMatchReceiverDisks([]int64{size}, disks)
	if err != nil || mapping[0] != "disk-0" {
		t.Fatalf("mapping %v %v", mapping, err)
	}
	options := vmReplicaOptions{Datacenter: "ha-datacenter", EnableCBT: true, Verify: true, RecordFolder: t.TempDir(), NewSink: link.sinkFactory(ctx, mapping)}

	first, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Disks[0].Mode != "seed" || !first.Disks[0].Verified || first.Disks[0].BytesRead > 4*(4<<20) || first.Disks[0].Target != "disk-0 on 127.0.0.1:"+strconv.Itoa(receiver.Port()) {
		t.Fatalf("first round %+v", first.Disks[0])
	}
	if !bytes.Equal(target.data[:size], data) {
		t.Fatal("the receiver disk differs from the VM disk after the seed")
	}

	rand.Read(data[60<<20 : 60<<20+512])
	os.WriteFile(flatPath, data, 0600)
	changed = []types.DiskChangeExtent{{Start: 60 << 20, Length: 512}}
	setChangeID()
	second, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disks[0].Mode != "changed" || second.Disks[0].BytesRead != 4<<20 || second.Disks[0].Changed != 1 {
		t.Fatalf("second round %+v", second.Disks[0])
	}
	if !bytes.Equal(target.data[:size], data) {
		t.Fatal("the receiver disk differs from the VM disk after the changed-block round")
	}
}

func TestReceiverDisksAreMatchedBySize(t *testing.T) {
	disks := []vmReceiverDisk{{Name: "disk-2", Size: 50 << 30}, {Name: "disk-1", Size: 101 << 30}, {Name: "disk-3", Size: 41 << 30}}
	mapping, err := vmMatchReceiverDisks([]int64{40 << 30, 100 << 30, 50 << 30}, disks)
	if err != nil || mapping[0] != "disk-3" || mapping[1] != "disk-1" || mapping[2] != "disk-2" {
		t.Fatalf("mapping %v %v", mapping, err)
	}
	if _, err := vmMatchReceiverDisks([]int64{40 << 30, 200 << 30}, disks); err == nil {
		t.Fatal("a source disk larger than every receiver disk was matched")
	}
	if _, err := vmMatchReceiverDisks([]int64{40 << 30, 40 << 30, 40 << 30, 40 << 30}, disks); err == nil {
		t.Fatal("more source disks than receiver disks were matched")
	}
}
