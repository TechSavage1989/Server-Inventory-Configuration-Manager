package main

import (
	"context"
	"crypto/tls"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

func TestDecryptSpecCoversEncryptedDisksOnly(t *testing.T) {
	key := &types.CryptoKeyId{KeyId: "k1", ProviderId: &types.KeyProviderId{Id: "kms"}}
	machine := mo.VirtualMachine{Config: &types.VirtualMachineConfigInfo{KeyId: key, Hardware: types.VirtualHardware{Device: []types.BaseVirtualDevice{
		&types.VirtualDisk{VirtualDevice: types.VirtualDevice{Key: 2000, Backing: &types.VirtualDiskFlatVer2BackingInfo{KeyId: key}}},
		&types.VirtualDisk{VirtualDevice: types.VirtualDevice{Key: 2001, Backing: &types.VirtualDiskFlatVer2BackingInfo{}}},
		&types.VirtualTPM{VirtualDevice: types.VirtualDevice{Key: 11000}},
	}}}}
	if !vmMachineEncrypted(machine) || vmMachineTPM(machine) == nil {
		t.Fatal("encryption or TPM not detected")
	}
	spec := vmDecryptSpec(machine)
	if _, ok := spec.Crypto.(*types.CryptoSpecDecrypt); !ok || len(spec.VmProfile) != 1 {
		t.Fatalf("VM home is not decrypted: %+v", spec)
	}
	if len(spec.DeviceChange) != 1 || spec.DeviceChange[0].GetVirtualDeviceConfigSpec().Device.GetVirtualDevice().Key != 2000 {
		t.Fatalf("only the encrypted disk should be edited: %+v", spec.DeviceChange)
	}
	if _, ok := spec.DeviceChange[0].GetVirtualDeviceConfigSpec().Backing.Crypto.(*types.CryptoSpecDecrypt); !ok {
		t.Fatal("disk backing is not decrypted")
	}
}

func TestPrepareVSphereVMAgainstSimulator(t *testing.T) {
	model := simulator.VPX()
	model.Machine = 2
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	user := server.URL.User.Username()
	password, _ := server.URL.User.Password()
	options := vmConnectOptions{Server: server.URL.Host, User: user, Password: password, insecureForTest: true}

	ctx := context.Background()
	session, err := openVSphereSession(ctx, options, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	finder := find.NewFinder(session.client, true)
	datacenter, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(datacenter)
	machines, err := finder.VirtualMachineList(ctx, "*")
	if err != nil || len(machines) == 0 {
		t.Fatal(err)
	}
	vm := machines[0]
	name, _ := vm.ObjectName(ctx)
	// Give it a snapshot and a virtual TPM to remove.
	task, err := vm.CreateSnapshot(ctx, "Acronis_test", "", false, false)
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	task, err = vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{DeviceChange: []types.BaseVirtualDeviceConfigSpec{&types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationAdd, Device: &types.VirtualTPM{VirtualDevice: types.VirtualDevice{Key: -1}}}}})
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	session.logout()
	simulated := simulator.Map.Get(vm.Reference()).(*simulator.VirtualMachine)
	simulated.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)

	folder := t.TempDir()
	if _, err := prepareVSphereVM(ctx, options, vm.Reference().Value, "not-"+name, vmPrepareSteps{Snapshots: true}, folder, nil); err == nil || !strings.Contains(err.Error(), "now called") {
		t.Fatalf("a renamed VM must be refused: %v", err)
	}
	if _, err := prepareVSphereVM(ctx, options, vm.Reference().Value, name, vmPrepareSteps{RemoveTPM: true}, folder, nil); err == nil || !strings.Contains(err.Error(), "powered off") {
		t.Fatalf("TPM removal on a running VM must be refused: %v", err)
	}
	vmPrepareShutdownTimeout = time.Minute
	result, err := prepareVSphereVM(ctx, options, vm.Reference().Value, name, vmPrepareSteps{Snapshots: true, Shutdown: true, RemoveTPM: true, Decrypt: true}, folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshots != 0 || result.VTPM || result.Encrypted || result.PowerState != "poweredOff" {
		t.Fatalf("%+v", result)
	}
	if len(result.Done) != 3 || !strings.Contains(strings.Join(result.Skipped, " "), "not encrypted") {
		t.Fatalf("%+v", result)
	}
	record, err := os.ReadFile(result.RecordPath)
	if err != nil || !strings.Contains(string(record), `"vtpm": true`) || !strings.Contains(string(record), `"snapshots": 1`) {
		t.Fatalf("the settings before the change were not saved: %s %v", record, err)
	}
}
