package main

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
)

func TestVSphereOVFEscapesValues(t *testing.T) {
	spec := vmVSphereImport{Name: "app01", CPUs: 2, MemoryMiB: 4096, Datastore: "ds", DiskSize: 1 << 30, Firmware: "uefi", SecureBoot: true, Network: `VM <Network> & "x"`}
	if err := vmVSphereValidate(&spec); err != nil {
		t.Fatal(err)
	}
	descriptor, err := vmVSphereOVF(spec, "a<b.vmdk", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vmw:value="efi"`, `efiSecureBootEnabled`, `a&lt;b.vmdk`, `<rasd:ResourceSubType>lsilogicsas</rasd:ResourceSubType>`, `VmxNet3`} {
		if !strings.Contains(descriptor, want) {
			t.Errorf("descriptor lacks %s", want)
		}
	}
	bad := vmVSphereImport{Name: "x", CPUs: 1, MemoryMiB: 512, Datastore: "ds", DiskSize: 1, SecureBoot: true}
	if vmVSphereValidate(&bad) == nil {
		t.Error("Secure Boot with BIOS was accepted")
	}
	bad = vmVSphereImport{Name: "x", CPUs: 1, MemoryMiB: 512, Datastore: "ds", DiskSize: 1, GuestID: "windows'; drop"}
	if vmVSphereValidate(&bad) == nil {
		t.Error("an invalid guest ID was accepted")
	}
}

// The import creates a new VM with the uploaded disk through an OVF lease
// against the vSphere simulator, and refuses a name already in use.
func TestVSphereImportCreatesAVMFromTheConvertedDisk(t *testing.T) {
	model := simulator.VPX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	password, _ := server.URL.User.Password()
	ctx := context.Background()
	session, err := openVSphereSession(ctx, vmConnectOptions{Server: server.URL.Host, User: server.URL.User.Username(), Password: password, insecureForTest: true}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.logout()

	choices, err := vmVSphereReadChoices(ctx, session.client, "")
	if err != nil || len(choices.Datastores) == 0 || len(choices.Networks) == 0 || len(choices.Computes) == 0 {
		t.Fatalf("choices %+v %v", choices, err)
	}

	folder := t.TempDir()
	data := vmConvertTestData(t)
	source, err := openDiskImage(vmWriteTestFile(t, filepath.Join(folder, "source.raw"), data))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	stream := filepath.Join(folder, "disk.vmdk")
	file, _ := os.Create(stream)
	if _, err := vmWriteStreamOptimized(ctx, source, file, "disk.vmdk", nil); err != nil {
		t.Fatal(err)
	}
	file.Close()

	spec := vmVSphereImport{Name: "app01-converted", Datastore: choices.Datastores[0].ID, Network: "VM Network", Compute: choices.Computes[0].ID,
		CPUs: 2, MemoryMiB: 2048, Firmware: "uefi", GuestID: "windows2019srv_64Guest", DiskSize: source.Size(),
		// The simulator only knows the paravirtual name; ESXi takes lsilogicsas too.
		Controller: "pvscsi"}
	id, err := vmVSphereImportDisk(ctx, session.client, spec, stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	finder := find.NewFinder(session.client, true)
	dc, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(dc)
	vm, err := finder.VirtualMachine(ctx, "app01-converted")
	if err != nil || vm.Reference().Value != id {
		t.Fatalf("the new VM was not found: %v", err)
	}
	var machine mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"config"}, &machine); err != nil {
		t.Fatal(err)
	}
	if machine.Config.Hardware.NumCPU != 2 || machine.Config.Hardware.MemoryMB != 2048 {
		t.Fatalf("hardware %+v", machine.Config.Hardware)
	}
	if _, err := vmVSphereImportDisk(ctx, session.client, spec, stream, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing VM name was reused: %v", err)
	}
}
