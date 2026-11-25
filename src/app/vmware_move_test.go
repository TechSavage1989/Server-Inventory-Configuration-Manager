package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
)

func TestMoveCopiesWhileRunningThenCutsOverAfterShutdown(t *testing.T) {
	model := simulator.VPX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	password, _ := server.URL.User.Password()
	options := vmConnectOptions{Server: server.URL.Host, User: server.URL.User.Username(), Password: password, insecureForTest: true}
	ctx := context.Background()
	session, err := openVSphereSession(ctx, options, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	size := int64(40 << 20)
	ref, flatPath, data := replicaTestVM(t, ctx, session.client, size)
	session.logout()
	simulated := simulator.Map.Get(ref).(*simulator.VirtualMachine)
	simulated.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)
	name := simulated.Name

	// No changed-block tracking answers: every round is a full hashed pass.
	original := vmChangedAreas
	defer func() { vmChangedAreas = original }()
	vmChangedAreas = func(context.Context, *vim25.Client, types.ManagedObjectReference, types.ManagedObjectReference, int32, int64, string) (types.DiskChangeInfo, error) {
		return types.DiskChangeInfo{}, errors.New("not supported")
	}

	folder := filepath.Join(t.TempDir(), "copies")
	request := vmMoveRequest{VMID: ref.Value, Name: name, Datacenter: "DC0", CopyFolder: folder, Quiesce: true, RecordFolder: t.TempDir()}
	first, err := vmMoveRound(ctx, options, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ShutDown || len(first.Disks) != 1 || simulated.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOn {
		t.Fatalf("a copy round must leave the VM running: %+v", first)
	}

	rand.Read(data[5<<20 : 5<<20+100])
	os.WriteFile(flatPath, data, 0600)
	vmPrepareShutdownTimeout = time.Minute
	request.Cutover = true
	final, err := vmMoveRound(ctx, options, request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !final.ShutDown || simulated.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOff || !final.Round.Disks[0].Verified || final.Round.Disks[0].Changed != 1 {
		t.Fatalf("cutover %+v / %+v", final, final.Round.Disks[0])
	}
	if !bytes.Equal(vmReadVHDX(t, final.Disks[0]), data) {
		t.Fatal("the cutover copy differs from the shut-down VM's disk")
	}

	if _, err := vmMoveRound(ctx, options, vmMoveRequest{VMID: ref.Value, Name: "renamed", Datacenter: "DC0", CopyFolder: folder}, nil); err == nil {
		t.Fatal("a VM whose name no longer matches was copied")
	}
}
