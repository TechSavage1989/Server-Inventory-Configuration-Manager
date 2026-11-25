package main

// Send a disk image to a host (the Disk converter's "Send to a host"): the
// image is converted (or used as it is when the platform takes its format),
// uploaded, and a new VM is made from it. The new VM is left off unless
// Start is ticked. Nothing on the platform is replaced: an image or VM name
// that is already used stops the send before anything is uploaded.
//
//   - Proxmox VE, Nutanix AHV, Scale HyperCore: vmTargetDiskUploader, then
//     vmTargetCreate with the uploaded disk (vmtarget_import.go).
//   - VMware ESXi or vCenter: a stream-optimized VMDK imported through an
//     OVF lease (vmconvert_vsphere.go).
//   - Hyper-V on this computer: a VHDX in the chosen folder and the VM made
//     by vmHyperVPlan's script.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// vmConvertSendPlatforms are the destinations, in the order the converter
// lists them.
var vmConvertSendPlatforms = []vmTargetChoice{
	{ID: vmTargetVSphere, Name: "VMware ESXi or vCenter", Detail: "Imported as a new VM through vSphere (stream-optimized VMDK)."},
	{ID: vmTargetHyperV, Name: "Hyper-V on this computer", Detail: "A VHDX in the folder you choose, attached to a new VM."},
	{ID: vmTargetProxmox, Name: "Proxmox VE", Detail: "Uploaded to a storage with the Import content type, then imported into a new VM."},
	{ID: vmTargetNutanix, Name: "Nutanix AHV", Detail: "Uploaded as a disk image in Prism Central, then cloned into a new VM."},
	{ID: vmTargetScale, Name: "Scale HyperCore", Detail: "Uploaded as a virtual disk (HyperCore 9.2 or later), then attached to a new VM."},
}

func vmConvertSendKnown(platform string) bool {
	for _, item := range vmConvertSendPlatforms {
		if item.ID == platform {
			return true
		}
	}
	return false
}

// vmSendSpec is what the operator chose for the new VM.
type vmSendSpec struct {
	Platform   string `json:"platform"`
	Name       string `json:"name"`
	CPUs       int    `json:"cpus"`
	MemoryMiB  int64  `json:"memoryMiB"`
	Firmware   string `json:"firmware"`
	SecureBoot bool   `json:"secureBoot"`
	TPM        bool   `json:"tpm"`
	Windows    bool   `json:"windows"`
	// GuestOS is the vSphere guest ID, or a Windows version name for the
	// Proxmox VE OS type.
	GuestOS string `json:"guestOs"`
	// Storage is the VM's storage (Proxmox VE storage, vSphere datastore).
	Storage string `json:"storage"`
	// ImportStorage is the Proxmox VE file storage the image is uploaded to.
	ImportStorage string `json:"importStorage"`
	Network       string `json:"network"`
	VLAN          int    `json:"vlan"`
	// Bus is the KVM disk bus; NIC the adapter model on every platform.
	Bus        string `json:"bus"`
	NIC        string `json:"nic"`
	Datacenter string `json:"datacenter"`
	Compute    string `json:"compute"`
	Controller string `json:"controller"`
	Thick      bool   `json:"thick"`
	// Folder holds the converted file: the staging copy for an upload, or
	// the VHDX a Hyper-V VM keeps using.
	Folder string `json:"folder"`
	Start  bool   `json:"start"`
}

type vmSendResult struct {
	Platform string `json:"platform"`
	VMID     string `json:"vmId"`
	VMName   string `json:"vmName"`
	// File is the converted file in the folder (empty when the image was
	// uploaded as it is).
	File     string `json:"file,omitempty"`
	Uploaded string `json:"uploaded,omitempty"`
	Started  bool   `json:"started"`
}

// vmSendHooks reports what the send is doing: Step names the stage and its
// progress (done of total, in bytes or chunks), Log adds a line.
type vmSendHooks struct {
	Step func(stage string, done, total int64)
	Log  func(string)
}

func (h vmSendHooks) step(stage string, done, total int64) {
	if h.Step != nil {
		h.Step(stage, done, total)
	}
}

func (h vmSendHooks) log(format string, args ...interface{}) {
	if h.Log != nil {
		h.Log(fmt.Sprintf(format, args...))
	}
}

func vmDiskFormatExtension(format string) string {
	for _, item := range vmDiskFormatNames {
		if item.ID == format {
			return item.Extension
		}
	}
	return ".img"
}

// vmSendCheck checks the parts every destination needs.
func vmSendCheck(spec *vmSendSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if !vmTargetNamePattern.MatchString(spec.Name) {
		return errors.New("the VM name must be 1 to 63 letters, digits, dots, dashes or underscores")
	}
	if spec.Folder == "" || !filepath.IsAbs(spec.Folder) {
		return errors.New("choose the folder for the converted file")
	}
	if info, err := os.Stat(spec.Folder); err != nil || !info.IsDir() {
		return fmt.Errorf("the folder %s does not exist", spec.Folder)
	}
	if spec.Firmware == "" {
		spec.Firmware = "bios"
	}
	return nil
}

// vmSendConvert converts the image into folder/name+extension, or continues
// or repeats that conversion, and reports its progress as a step.
func vmSendConvert(ctx context.Context, image vmDiskImage, target, format string, hooks vmSendHooks) error {
	hooks.log("Converting %s to %s in %s.", filepath.Base(image.Info().Path), vmDiskFormatTitle(format), filepath.Dir(target))
	stage := "Converting to " + vmDiskFormatTitle(format)
	result, err := vmConvertImage(ctx, image, target, format, vmConvertOptions{Progress: func(progress vmCopyProgress) {
		hooks.step(stage, int64(progress.Done), int64(progress.Chunks))
	}})
	if err != nil {
		return err
	}
	hooks.log("%s", convertDoneMessage(result))
	return nil
}

// vmSendUploadable reports whether the image can be uploaded as it is: one
// file (no parent chain, no separate extents) in a format the platform takes.
func vmSendUploadable(info vmDiskInfo, formats []string) bool {
	if len(info.Chain) > 1 {
		return false
	}
	switch info.Format {
	case vmDiskFormatQCOW2, vmDiskFormatVHDX, vmDiskFormatVHD, vmDiskFormatRaw:
	default:
		return false
	}
	for _, format := range formats {
		if format == info.Format {
			return true
		}
	}
	return false
}

type vmProgressReader struct {
	reader io.Reader
	done   int64
	report func(int64)
}

func (r *vmProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.done += int64(n)
	r.report(r.done)
	return n, err
}

// vmSendToPlatform uploads the image to Proxmox VE, Nutanix or HyperCore and
// creates the VM from it.
func vmSendToPlatform(ctx context.Context, image vmDiskImage, platform vmTargetPlatform, spec vmSendSpec, hooks vmSendHooks) (*vmSendResult, error) {
	if err := vmSendCheck(&spec); err != nil {
		return nil, err
	}
	uploader, ok := platform.(vmTargetDiskUploader)
	if !ok {
		return nil, errors.New("this platform does not take uploaded disks")
	}
	vmSpec := vmTargetVMSpec{
		Name: spec.Name, Description: "Converted from " + filepath.Base(image.Info().Path) + " by Server Inventory and Migration Manager.",
		CPUs: spec.CPUs, MemoryMiB: spec.MemoryMiB, Firmware: spec.Firmware, SecureBoot: spec.SecureBoot, TPM: spec.TPM,
		Windows: spec.Windows, GuestOS: spec.GuestOS, Storage: spec.Storage,
		Disks: []vmTargetDisk{{Size: image.Size(), Bus: spec.Bus, Import: "pending"}},
	}
	if spec.Network != "" {
		vmSpec.NICs = []vmTargetNIC{{Network: spec.Network, VLAN: spec.VLAN, Model: spec.NIC}}
	}
	// Check everything the platform will check before the long steps.
	if err := vmValidateTargetSpec(&vmSpec); err != nil {
		return nil, err
	}
	existing, err := platform.FindVM(ctx, spec.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("a VM named %s already exists (%s); choose another name. Nothing was changed", spec.Name, existing.ID)
	}
	if spec.Platform == vmTargetProxmox && spec.ImportStorage == "" {
		return nil, errors.New("choose the Proxmox VE storage the image is uploaded to (a file storage with the Import content type)")
	}

	result := &vmSendResult{Platform: spec.Platform, VMName: spec.Name}
	info := image.Info()
	formats := uploader.DiskFormats()
	file, format := info.Path, info.Format
	if !vmSendUploadable(info, formats) {
		format = formats[0]
		file = filepath.Join(spec.Folder, spec.Name+vmDiskFormatExtension(format))
		if err := vmSendConvert(ctx, image, file, format, hooks); err != nil {
			return nil, err
		}
		result.File = file
	} else {
		hooks.log("%s is already %s; uploading it as it is.", filepath.Base(file), vmDiskFormatTitle(format))
	}

	opened, err := os.Open(file)
	if err != nil {
		return result, err
	}
	defer opened.Close()
	stat, err := opened.Stat()
	if err != nil {
		return result, err
	}
	extension := strings.ToLower(filepath.Ext(file))
	if format == vmDiskFormatRaw {
		extension = ".img"
		if spec.Platform == vmTargetProxmox {
			extension = ".raw"
		}
	}
	name := spec.Name + "-disk" + extension
	if spec.Platform == vmTargetProxmox {
		name = spec.ImportStorage + "/" + name
	}
	hooks.log("Uploading %s (%s) as %s.", filepath.Base(file), vmFormatBytes(stat.Size()), name)
	reader := &vmProgressReader{reader: opened, report: func(done int64) { hooks.step("Uploading", done, stat.Size()) }}
	reference, err := uploader.UploadDisk(ctx, name, reader, stat.Size())
	if err != nil {
		return result, fmt.Errorf("uploading the disk: %w", err)
	}
	result.Uploaded = reference
	hooks.log("Uploaded as %s.", reference)

	hooks.step("Creating the VM", 0, 0)
	vmSpec.Disks[0].Import = reference
	vm, err := vmTargetCreate(ctx, platform, vmSpec, hooks.Log)
	if err != nil {
		return result, err
	}
	result.VMID = vm.ID
	hooks.log("Created %s (%s). It is off.", spec.Name, vm.ID)
	if spec.Start {
		if err := platform.Start(ctx, vm.ID); err != nil {
			return result, fmt.Errorf("%s was created but did not start: %w", spec.Name, err)
		}
		result.Started = true
		hooks.log("Started %s.", spec.Name)
	}
	return result, nil
}

// vmVSphereSender is the part of a vSphere session the send uses, so tests
// can run against the simulator.
type vmVSphereSender func(ctx context.Context, spec vmVSphereImport, streamPath string, progress func(string)) (string, error)

// vmSendToVSphere writes a stream-optimized VMDK into the folder and imports
// it as a new VM.
func vmSendToVSphere(ctx context.Context, image vmDiskImage, importDisk vmVSphereSender, spec vmSendSpec, hooks vmSendHooks) (*vmSendResult, error) {
	if err := vmSendCheck(&spec); err != nil {
		return nil, err
	}
	importSpec := vmVSphereImport{
		Name: spec.Name, Datacenter: spec.Datacenter, Compute: spec.Compute, Datastore: spec.Storage, Network: spec.Network,
		CPUs: spec.CPUs, MemoryMiB: spec.MemoryMiB, Firmware: spec.Firmware, SecureBoot: spec.SecureBoot, GuestID: spec.GuestOS,
		Controller: spec.Controller, NIC: spec.NIC, Thick: spec.Thick, DiskSize: image.Size(), Start: spec.Start,
	}
	if err := vmVSphereValidate(&importSpec); err != nil {
		return nil, err
	}
	path := filepath.Join(spec.Folder, spec.Name+"-stream.vmdk")
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%s already exists; nothing was overwritten. Delete it or choose another folder or VM name", path)
		}
		return nil, err
	}
	hooks.log("Writing %s for vSphere.", filepath.Base(path))
	size := image.Size()
	_, err = vmWriteStreamOptimized(ctx, image, out, spec.Name+".vmdk", func(done, total int64) { hooks.step("Writing the VMDK for vSphere", done, total) })
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	result := &vmSendResult{Platform: vmTargetVSphere, VMName: spec.Name, File: path}
	hooks.log("Importing %s (%s disk) into vSphere.", spec.Name, vmFormatBytes(size))
	hooks.step("Importing into vSphere", 0, 0)
	id, err := importDisk(ctx, importSpec, path, func(line string) { hooks.log("%s", line) })
	if err != nil {
		return result, err
	}
	result.VMID, result.Started = id, spec.Start
	hooks.log("Created %s (%s).", spec.Name, id)
	return result, nil
}

// vmSendToHyperV converts the image to a VHDX in the folder and creates the
// VM on this computer through create (vmCreateHyperVVM).
func vmSendToHyperV(ctx context.Context, image vmDiskImage, create func(*vmHyperVPlan) (string, error), spec vmSendSpec, hooks vmSendHooks) (*vmSendResult, error) {
	if err := vmSendCheck(&spec); err != nil {
		return nil, err
	}
	if spec.CPUs < 1 || spec.CPUs > 240 {
		return nil, errors.New("choose between 1 and 240 virtual CPUs")
	}
	if spec.MemoryMiB < 32 {
		return nil, errors.New("choose at least 32 MiB of memory")
	}
	if spec.Firmware == "bios" && (spec.SecureBoot || spec.TPM) {
		return nil, errors.New("Secure Boot and a virtual TPM need UEFI firmware")
	}
	if spec.VLAN < 0 || spec.VLAN > 4094 {
		return nil, errors.New("VLAN must be 0 to 4094")
	}
	file := filepath.Join(spec.Folder, spec.Name+".vhdx")
	if err := vmSendConvert(ctx, image, file, vmDiskFormatVHDX, hooks); err != nil {
		return nil, err
	}
	plan := &vmHyperVPlan{
		Name: spec.Name, Generation: 1, CPUs: spec.CPUs, MemoryBytes: spec.MemoryMiB << 20,
		Disks: []string{file}, Start: spec.Start,
	}
	if spec.Firmware == "uefi" {
		plan.Generation, plan.SecureBoot, plan.TPM = 2, spec.SecureBoot, spec.TPM
		plan.SecureBootTemplate = "MicrosoftUEFICertificateAuthority"
		if spec.Windows {
			plan.SecureBootTemplate = "MicrosoftWindows"
		}
	}
	if spec.Network != "" {
		plan.Adapters = []vmHyperVAdapter{{Label: "Network adapter 1", Switch: spec.Network, VLAN: spec.VLAN}}
	}
	hooks.step("Creating the Hyper-V VM", 0, 0)
	hooks.log("Creating the Hyper-V VM %s (generation %d).", spec.Name, plan.Generation)
	id, err := create(plan)
	result := &vmSendResult{Platform: vmTargetHyperV, VMName: spec.Name, File: file}
	if err != nil {
		return result, err
	}
	result.VMID, result.Started = id, spec.Start
	hooks.log("Created %s (%s).", spec.Name, id)
	return result, nil
}

// vmSendDoneMessage is the converter's one-line result.
func vmSendDoneMessage(result *vmSendResult) string {
	where := vmTargetLabel(result.Platform)
	if result.Platform == vmTargetVSphere {
		where = "vSphere"
	}
	state := "It is off; check its settings, then start it."
	if result.Started {
		state = "It was started."
	}
	return fmt.Sprintf("Created %s on %s. %s", result.VMName, where, state)
}
