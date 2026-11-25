package main

// Prepare source: the only steps that change vSphere, each opt-in, confirmed
// per VM with its typed name and run in a fixed order so a VM can be copied
// off VMware:
//
//  1. delete all snapshots (consolidating them into the disks),
//  2. shut the guest down through VMware Tools (never a hard power-off),
//  3. remove the virtual TPM,
//  4. decrypt the VM and its disks (vSphere VM Encryption).
//
// The TPM goes before decryption because vSphere keeps a VM with a virtual
// TPM encrypted. Before anything changes, the VM's current settings are saved
// so the operator can see what was there. No Windows dependency, so the
// steps are tested against VMware's simulator.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

type vmPrepareSteps struct {
	Snapshots bool `json:"snapshots"`
	Shutdown  bool `json:"shutdown"`
	RemoveTPM bool `json:"removeTpm"`
	Decrypt   bool `json:"decrypt"`
}

func vmPrepareStepNames(s vmPrepareSteps) []string {
	names := []string{}
	for _, step := range []struct {
		on   bool
		name string
	}{{s.Snapshots, "delete snapshots"}, {s.Shutdown, "shut down"}, {s.RemoveTPM, "remove the virtual TPM"}, {s.Decrypt, "decrypt"}} {
		if step.on {
			names = append(names, step.name)
		}
	}
	return names
}

func (s vmPrepareSteps) any() bool { return s.Snapshots || s.Shutdown || s.RemoveTPM || s.Decrypt }

type vmPrepareResult struct {
	VM         string   `json:"vm"`
	Done       []string `json:"done"`
	Skipped    []string `json:"skipped"`
	PowerState string   `json:"powerState"`
	Snapshots  int      `json:"snapshots"`
	VTPM       bool     `json:"vtpm"`
	Encrypted  bool     `json:"encrypted"`
	RecordPath string   `json:"recordPath"`
}

// vmPrepareShutdownTimeout is how long the guest gets to shut down.
var vmPrepareShutdownTimeout = 10 * time.Minute

var vmPrepareProperties = []string{"name", "config", "runtime.powerState", "guest.toolsRunningStatus", "snapshot"}

func vmPrepareLoad(ctx context.Context, collector *property.Collector, ref types.ManagedObjectReference) (mo.VirtualMachine, error) {
	var machine mo.VirtualMachine
	if err := collector.RetrieveOne(ctx, ref, vmPrepareProperties, &machine); err != nil {
		return machine, err
	}
	if machine.Config == nil {
		return machine, errors.New("vSphere did not return this VM's configuration")
	}
	return machine, nil
}

func vmMachineTPM(machine mo.VirtualMachine) *types.VirtualTPM {
	if machine.Config == nil {
		return nil
	}
	for _, device := range machine.Config.Hardware.Device {
		if tpm, ok := device.(*types.VirtualTPM); ok {
			return tpm
		}
	}
	return nil
}

func vmDiskKey(backing types.BaseVirtualDeviceBackingInfo) *types.CryptoKeyId {
	switch b := backing.(type) {
	case *types.VirtualDiskFlatVer2BackingInfo:
		return b.KeyId
	case *types.VirtualDiskSeSparseBackingInfo:
		return b.KeyId
	case *types.VirtualDiskSparseVer2BackingInfo:
		return b.KeyId
	}
	return nil
}

func vmMachineEncrypted(machine mo.VirtualMachine) bool {
	if machine.Config == nil {
		return false
	}
	if machine.Config.KeyId != nil {
		return true
	}
	for _, device := range machine.Config.Hardware.Device {
		if disk, ok := device.(*types.VirtualDisk); ok && vmDiskKey(disk.Backing) != nil {
			return true
		}
	}
	return false
}

func vmSnapshotCount(tree []types.VirtualMachineSnapshotTree) int {
	count := 0
	for _, node := range tree {
		count += 1 + vmSnapshotCount(node.ChildSnapshotList)
	}
	return count
}

// vmDecryptSpec builds the reconfiguration that decrypts the VM home and every
// encrypted disk, applying the datastore's default storage policy.
func vmDecryptSpec(machine mo.VirtualMachine) types.VirtualMachineConfigSpec {
	spec := types.VirtualMachineConfigSpec{
		Crypto:    &types.CryptoSpecDecrypt{},
		VmProfile: []types.BaseVirtualMachineProfileSpec{&types.VirtualMachineDefaultProfileSpec{}},
	}
	for _, device := range machine.Config.Hardware.Device {
		disk, ok := device.(*types.VirtualDisk)
		if !ok || vmDiskKey(disk.Backing) == nil {
			continue
		}
		spec.DeviceChange = append(spec.DeviceChange, &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationEdit,
			Device:    disk,
			Profile:   []types.BaseVirtualMachineProfileSpec{&types.VirtualMachineDefaultProfileSpec{}},
			Backing:   &types.VirtualDeviceConfigSpecBackingSpec{Crypto: &types.CryptoSpecDecrypt{}},
		})
	}
	return spec
}

// vmPrepareRecord saves what the VM looked like before the first change.
func vmPrepareRecord(folder string, machine mo.VirtualMachine) (string, error) {
	if err := os.MkdirAll(folder, 0700); err != nil {
		return "", err
	}
	devices := []string{}
	for _, device := range machine.Config.Hardware.Device {
		description := ""
		if info := device.GetVirtualDevice().DeviceInfo; info != nil {
			description = info.GetDescription().Label
		}
		devices = append(devices, strings.TrimPrefix(fmt.Sprintf("%T", device), "*types.")+": "+description)
	}
	record := map[string]any{
		"vm": machine.Name, "id": machine.Self.Value, "savedAt": time.Now().Format(time.RFC3339),
		"powerState": machine.Runtime.PowerState, "snapshots": vmSnapshotCount(func() []types.VirtualMachineSnapshotTree {
			if machine.Snapshot == nil {
				return nil
			}
			return machine.Snapshot.RootSnapshotList
		}()),
		"vtpm": vmMachineTPM(machine) != nil, "encrypted": vmMachineEncrypted(machine),
		"firmware": machine.Config.Firmware, "guestId": machine.Config.GuestId, "hardwareVersion": machine.Config.Version,
		"cpus": machine.Config.Hardware.NumCPU, "memoryMb": machine.Config.Hardware.MemoryMB, "devices": devices,
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(folder, vmFileName(machine.Name)+"-"+time.Now().Format("20060102-150405")+".json")
	return path, os.WriteFile(path, data, 0600)
}

// vmFileName makes a VM name safe for a Windows file name.
func vmFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || strings.Trim(name, ".") == "" {
		return "VM"
	}
	return name
}

func vmRunTask(ctx context.Context, task *object.Task, err error) error {
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// prepareVSphereVM runs the chosen steps on one VM, identified by its
// managed object ID, after checking its name still matches.
func prepareVSphereVM(parent context.Context, options vmConnectOptions, vmID, vmName string, steps vmPrepareSteps, recordFolder string, progress func(string)) (vmPrepareResult, error) {
	if progress == nil {
		progress = func(string) {}
	}
	result := vmPrepareResult{VM: vmName, Done: []string{}, Skipped: []string{}}
	if !steps.any() {
		return result, errors.New("Choose at least one step.")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Minute)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, progress)
	if err != nil {
		return result, err
	}
	defer vsphere.logout()
	collector := property.DefaultCollector(vsphere.client)
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmID}
	machine, err := vmPrepareLoad(ctx, collector, ref)
	if err != nil {
		return result, fmt.Errorf("The VM could not be read: %v", vmFriendlyConnectError(err, vsphere.host))
	}
	if machine.Name != vmName {
		return result, fmt.Errorf("The VM with this ID is now called %q, not %q. Read the inventory again.", machine.Name, vmName)
	}
	if machine.Config.Template {
		return result, errors.New("This is a template. Templates are not prepared.")
	}
	if role := vmInfrastructureRole(vmMachine{Name: machine.Name, GuestID: machine.Config.GuestId, GuestOS: machine.Config.GuestFullName}); role != "" {
		return result, errors.New("This is " + role + ", which is not migrated. It is never changed.")
	}
	result.RecordPath, err = vmPrepareRecord(recordFolder, machine)
	if err != nil {
		return result, fmt.Errorf("The VM's current settings could not be saved, so nothing was changed: %v", err)
	}
	vm := object.NewVirtualMachine(vsphere.client, ref)
	reload := func() error {
		machine, err = vmPrepareLoad(ctx, collector, ref)
		return err
	}

	if steps.Snapshots {
		if machine.Snapshot == nil || vmSnapshotCount(machine.Snapshot.RootSnapshotList) == 0 {
			result.Skipped = append(result.Skipped, "There were no snapshots to delete.")
		} else {
			progress(fmt.Sprintf("Deleting %d snapshot(s) of %s and consolidating the disks. This can take a while.", vmSnapshotCount(machine.Snapshot.RootSnapshotList), vmName))
			consolidate := true
			task, taskErr := vm.RemoveAllSnapshot(ctx, &consolidate)
			if err := vmRunTask(ctx, task, taskErr); err != nil {
				return result, fmt.Errorf("Deleting the snapshots failed: %v", err)
			}
			result.Done = append(result.Done, "Deleted the snapshots and consolidated the disks.")
		}
		if err := reload(); err != nil {
			return result, err
		}
	}
	if steps.Shutdown {
		if machine.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOff {
			result.Skipped = append(result.Skipped, "The VM was already powered off.")
		} else {
			if machine.Guest == nil || machine.Guest.ToolsRunningStatus != string(types.VirtualMachineToolsRunningStatusGuestToolsRunning) {
				return result, errors.New("VMware Tools is not running, so the guest cannot be shut down cleanly. Shut it down from inside the guest, then run this again.")
			}
			progress("Shutting down " + vmName + " through VMware Tools.")
			if err := vm.ShutdownGuest(ctx); err != nil {
				return result, fmt.Errorf("The shutdown request failed: %v", err)
			}
			waitCtx, waitCancel := context.WithTimeout(ctx, vmPrepareShutdownTimeout)
			err := vm.WaitForPowerState(waitCtx, types.VirtualMachinePowerStatePoweredOff)
			waitCancel()
			if err != nil {
				return result, fmt.Errorf("The guest did not shut down within %s. Check it from the console; it was not powered off by force.", vmPrepareShutdownTimeout)
			}
			result.Done = append(result.Done, "Shut down the guest.")
		}
		if err := reload(); err != nil {
			return result, err
		}
	}
	if steps.RemoveTPM || steps.Decrypt {
		if machine.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOff {
			return result, errors.New("The VM must be powered off to remove the virtual TPM or decrypt it. Choose Shut down as well, or shut it down first.")
		}
		if steps.Decrypt && machine.Snapshot != nil && vmSnapshotCount(machine.Snapshot.RootSnapshotList) > 0 {
			return result, errors.New("vSphere cannot decrypt a VM that has snapshots. Choose Delete snapshots as well.")
		}
	}
	if steps.RemoveTPM {
		if tpm := vmMachineTPM(machine); tpm == nil {
			result.Skipped = append(result.Skipped, "The VM had no virtual TPM.")
		} else {
			progress("Removing the virtual TPM from " + vmName + ".")
			spec := types.VirtualMachineConfigSpec{DeviceChange: []types.BaseVirtualDeviceConfigSpec{&types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationRemove, Device: tpm}}}
			task, taskErr := vm.Reconfigure(ctx, spec)
			if err := vmRunTask(ctx, task, taskErr); err != nil {
				return result, fmt.Errorf("Removing the virtual TPM failed: %v", err)
			}
			result.Done = append(result.Done, "Removed the virtual TPM.")
		}
		if err := reload(); err != nil {
			return result, err
		}
	}
	if steps.Decrypt {
		if !vmMachineEncrypted(machine) {
			result.Skipped = append(result.Skipped, "The VM was not encrypted.")
		} else {
			if vmMachineTPM(machine) != nil {
				return result, errors.New("vSphere keeps a VM with a virtual TPM encrypted. Choose Remove the virtual TPM as well.")
			}
			progress("Decrypting " + vmName + ". vSphere rewrites every encrypted disk, so this takes about as long as copying them.")
			task, taskErr := vm.Reconfigure(ctx, vmDecryptSpec(machine))
			if err := vmRunTask(ctx, task, taskErr); err != nil {
				return result, fmt.Errorf("Decrypting failed: %v. The key provider must be available and the account needs the Cryptographic operations > Decrypt privilege.", err)
			}
			result.Done = append(result.Done, "Decrypted the VM and its disks.")
		}
		if err := reload(); err != nil {
			return result, err
		}
	}
	result.PowerState = string(machine.Runtime.PowerState)
	if machine.Snapshot != nil {
		result.Snapshots = vmSnapshotCount(machine.Snapshot.RootSnapshotList)
	}
	result.VTPM = vmMachineTPM(machine) != nil
	result.Encrypted = vmMachineEncrypted(machine)
	return result, nil
}
