package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vmSendFake is an in-memory platform that takes uploaded disks.
type vmSendFake struct {
	formats  []string
	existing map[string]bool
	uploads  map[string][]byte
	created  []vmTargetVMSpec
	started  []string
}

func (f *vmSendFake) Inventory(context.Context) (*vmTargetInventory, error) {
	return &vmTargetInventory{Platform: vmTargetProxmox, Version: "8.4"}, nil
}
func (f *vmSendFake) UploadISO(context.Context, string, io.Reader, int64) (string, error) {
	return "", errors.New("not used")
}
func (f *vmSendFake) FindVM(_ context.Context, name string) (*vmTargetVM, error) {
	if f.existing[name] {
		return &vmTargetVM{ID: "100", Name: name}, nil
	}
	return nil, nil
}
func (f *vmSendFake) CreateVM(_ context.Context, spec vmTargetVMSpec) (*vmTargetVM, error) {
	f.created = append(f.created, spec)
	return &vmTargetVM{ID: "105", Name: spec.Name, State: "stopped"}, nil
}
func (f *vmSendFake) Start(_ context.Context, id string) error {
	f.started = append(f.started, id)
	return nil
}
func (f *vmSendFake) PowerOff(context.Context, string) error        { return nil }
func (f *vmSendFake) BootFromDisk(context.Context, string) error    { return nil }
func (f *vmSendFake) State(context.Context, string) (string, error) { return "stopped", nil }
func (f *vmSendFake) DiskFormats() []string                         { return f.formats }
func (f *vmSendFake) UploadDisk(_ context.Context, name string, data io.Reader, size int64) (string, error) {
	if f.uploads == nil {
		f.uploads = map[string][]byte{}
	}
	if _, used := f.uploads[name]; used {
		return "", errors.New("already exists")
	}
	raw, err := io.ReadAll(io.LimitReader(data, size))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) != size {
		return "", errors.New("short upload")
	}
	f.uploads[name] = raw
	return "ref:" + name, nil
}

func vmSendSource(t *testing.T) (vmDiskImage, []byte, string) {
	t.Helper()
	folder := t.TempDir()
	data := vmConvertTestData(t)
	path := vmWriteTestFile(t, filepath.Join(folder, "source.raw"), data[:len(data)-len(data)%512])
	image, err := openDiskImage(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { image.Close() })
	return image, data[:len(data)-len(data)%512], folder
}

func TestSendConvertsUploadsAndCreatesTheVM(t *testing.T) {
	image, want, _ := vmSendSource(t)
	out := t.TempDir()
	fake := &vmSendFake{formats: []string{vmDiskFormatQCOW2}}
	var stages []string
	hooks := vmSendHooks{Step: func(stage string, done, total int64) {
		if len(stages) == 0 || stages[len(stages)-1] != stage {
			stages = append(stages, stage)
		}
	}}
	spec := vmSendSpec{Platform: vmTargetProxmox, Name: "app01", CPUs: 2, MemoryMiB: 4096, Firmware: "uefi", Windows: true,
		Storage: "local-lvm", ImportStorage: "local", Network: "vmbr0", Folder: out, Start: true}
	result, err := vmSendToPlatform(context.Background(), image, fake, spec, hooks)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, ok := fake.uploads["local/app01-disk.qcow2"]
	if !ok {
		t.Fatalf("uploads %v", fake.uploads)
	}
	// The upload is the converted qcow2, which reads back as the source.
	copied := filepath.Join(t.TempDir(), "uploaded.qcow2")
	vmWriteTestFile(t, copied, uploaded)
	got, info := vmReadImage(t, copied)
	vmAssertSameDisk(t, "uploaded", got, want)
	if info.Format != vmDiskFormatQCOW2 || result.File != filepath.Join(out, "app01.qcow2") {
		t.Fatalf("format %s file %s", info.Format, result.File)
	}
	if len(fake.created) != 1 || fake.created[0].Disks[0].Import != "ref:local/app01-disk.qcow2" || fake.created[0].ISO != "" ||
		fake.created[0].Disks[0].Size != int64(len(want)) || fake.created[0].NICs[0].Network != "vmbr0" {
		t.Fatalf("created %+v", fake.created)
	}
	if !result.Started || len(fake.started) != 1 || result.VMID != "105" {
		t.Fatalf("result %+v started %v", result, fake.started)
	}
	if strings.Join(stages, "|") != "Converting to qcow2|Uploading|Creating the VM" {
		t.Fatalf("stages %v", stages)
	}

	// The same send again stops at the name check, before any upload.
	fake.existing = map[string]bool{"app01": true}
	if _, err := vmSendToPlatform(context.Background(), image, fake, spec, vmSendHooks{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing VM name was not refused: %v", err)
	}
	if len(fake.uploads) != 1 || len(fake.created) != 1 {
		t.Fatal("a refused send uploaded or created something")
	}
}

func TestSendUploadsAnImageThePlatformTakesAsItIs(t *testing.T) {
	image, want, _ := vmSendSource(t)
	fake := &vmSendFake{formats: []string{vmDiskFormatQCOW2, vmDiskFormatRaw}}
	spec := vmSendSpec{Platform: vmTargetNutanix, Name: "db01", CPUs: 1, MemoryMiB: 1024, Folder: t.TempDir()}
	result, err := vmSendToPlatform(context.Background(), image, fake, spec, vmSendHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if result.File != "" || string(fake.uploads["db01-disk.img"]) != string(want) || result.Started {
		t.Fatalf("result %+v uploads %d", result, len(fake.uploads))
	}
	entries, _ := os.ReadDir(spec.Folder)
	if len(entries) != 0 {
		t.Fatal("an image uploaded as it is was copied into the folder")
	}
}

func TestSendChecksTheSpecBeforeAnyWork(t *testing.T) {
	image, _, _ := vmSendSource(t)
	fake := &vmSendFake{formats: []string{vmDiskFormatQCOW2}}
	folder := t.TempDir()
	for label, spec := range map[string]vmSendSpec{
		"name":           {Platform: vmTargetProxmox, Name: "bad name", CPUs: 1, MemoryMiB: 1024, ImportStorage: "local", Folder: folder},
		"folder":         {Platform: vmTargetProxmox, Name: "a", CPUs: 1, MemoryMiB: 1024, ImportStorage: "local", Folder: filepath.Join(folder, "missing")},
		"cpus":           {Platform: vmTargetProxmox, Name: "a", CPUs: 0, MemoryMiB: 1024, ImportStorage: "local", Folder: folder},
		"import storage": {Platform: vmTargetProxmox, Name: "a", CPUs: 1, MemoryMiB: 1024, Folder: folder},
		"secure boot":    {Platform: vmTargetProxmox, Name: "a", CPUs: 1, MemoryMiB: 1024, ImportStorage: "local", Folder: folder, SecureBoot: true},
	} {
		if _, err := vmSendToPlatform(context.Background(), image, fake, spec, vmSendHooks{}); err == nil {
			t.Errorf("%s: a bad spec was accepted", label)
		}
	}
	entries, _ := os.ReadDir(folder)
	if len(fake.uploads) != 0 || len(fake.created) != 0 || len(entries) != 0 {
		t.Fatal("a refused spec converted, uploaded or created something")
	}
}

func TestSendToHyperVConvertsToVHDXAndPlansTheVM(t *testing.T) {
	image, want, _ := vmSendSource(t)
	folder := t.TempDir()
	var plan *vmHyperVPlan
	create := func(p *vmHyperVPlan) (string, error) {
		plan = p
		return "4f6a3f0e-6a2b-4e0e-9a51-2b6d5f0c1a11", nil
	}
	spec := vmSendSpec{Platform: vmTargetHyperV, Name: "web01", CPUs: 4, MemoryMiB: 8192, Firmware: "uefi", SecureBoot: true, Windows: true, Network: "External", VLAN: 20, Folder: folder}
	result, err := vmSendToHyperV(context.Background(), image, create, spec, vmSendHooks{})
	if err != nil {
		t.Fatal(err)
	}
	got, info := vmReadImage(t, filepath.Join(folder, "web01.vhdx"))
	vmAssertSameDisk(t, "vhdx", got, want)
	if info.Format != vmDiskFormatVHDX || plan == nil || plan.Generation != 2 || plan.SecureBootTemplate != "MicrosoftWindows" ||
		plan.MemoryBytes != 8192<<20 || plan.Disks[0] != result.File || plan.Adapters[0].Switch != "External" || plan.Adapters[0].VLAN != 20 || plan.Start {
		t.Fatalf("plan %+v", plan)
	}
	script := plan.Script()
	if !strings.Contains(script, "New-VM -Name $name -Generation 2") || strings.Contains(script, "Start-VM") {
		t.Fatal("the Hyper-V script does not match the plan")
	}

	spec.Firmware, spec.Name = "bios", "web02"
	if _, err := vmSendToHyperV(context.Background(), image, create, spec, vmSendHooks{}); err == nil {
		t.Fatal("Secure Boot with BIOS firmware was accepted")
	}
}

func TestSendToVSphereWritesAStreamOptimizedDisk(t *testing.T) {
	image, want, _ := vmSendSource(t)
	folder := t.TempDir()
	var imported vmVSphereImport
	var readBack []byte
	importDisk := func(_ context.Context, spec vmVSphereImport, path string, _ func(string)) (string, error) {
		imported = spec
		got, _ := vmReadImage(t, path)
		readBack = got
		return "vm-42", nil
	}
	spec := vmSendSpec{Platform: vmTargetVSphere, Name: "file01", CPUs: 2, MemoryMiB: 2048, Storage: "datastore1", Network: "VM Network", GuestOS: "windows2019srv_64Guest", Folder: folder}
	result, err := vmSendToVSphere(context.Background(), image, importDisk, spec, vmSendHooks{})
	if err != nil {
		t.Fatal(err)
	}
	vmAssertSameDisk(t, "stream", readBack, want)
	if result.VMID != "vm-42" || imported.DiskSize != int64(len(want)) || imported.Datastore != "datastore1" || imported.Controller != "lsilogicsas" {
		t.Fatalf("result %+v import %+v", result, imported)
	}
	// The stream file is never overwritten.
	if _, err := vmSendToVSphere(context.Background(), image, importDisk, spec, vmSendHooks{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing stream file was overwritten: %v", err)
	}
}
