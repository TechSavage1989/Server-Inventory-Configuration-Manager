package main

// The vSphere side of Roll back (rollback.go): put the old VM's network
// adapters back from the record the cutover saved, and power the old VM on.
// Both check the VM's ID and name first and change nothing else about it.

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

type vmAdapterRecordFile struct {
	VM       string            `json:"vm"`
	ID       string            `json:"id"`
	Adapters []vmAdapterRecord `json:"adapters"`
}

func vmReadSourceVM(ctx context.Context, vsphere *vmSession, vmID, name string, properties []string) (*mo.VirtualMachine, error) {
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmID}
	var machine mo.VirtualMachine
	if err := property.DefaultCollector(vsphere.client).RetrieveOne(ctx, ref, append([]string{"name", "runtime.powerState"}, properties...), &machine); err != nil {
		if vmIsNotFound(err) {
			return nil, fmt.Errorf("%s is no longer in %s (it was deleted, removed from the inventory or its host was rebuilt)", name, vsphere.host)
		}
		return nil, fmt.Errorf("the old VM could not be read: %v", vmFriendlyConnectError(err, vsphere.host))
	}
	if machine.Name != name {
		return nil, fmt.Errorf("the VM with this ID is now called %q, not %q; it was not changed", machine.Name, name)
	}
	return &machine, nil
}

// vmReconnectSourceAdapters sets each adapter in the record back to how it
// connected at power on before the cutover. The VM must be off. It saves a
// record of the settings it changes first, in saveFolder.
func vmReconnectSourceAdapters(parent context.Context, options vmConnectOptions, vmID, name, recordFile, saveFolder string, log func(string)) (int, error) {
	if log == nil {
		log = func(string) {}
	}
	if recordFile == "" {
		return 0, nil
	}
	raw, err := os.ReadFile(recordFile)
	if err != nil {
		return 0, fmt.Errorf("the record of the adapters (%s) could not be read: %v", recordFile, err)
	}
	var record vmAdapterRecordFile
	if err := json.Unmarshal(raw, &record); err != nil {
		return 0, fmt.Errorf("the record of the adapters (%s) could not be read: %v", recordFile, err)
	}
	if record.ID != "" && record.ID != vmID {
		return 0, errors.New("the record of the adapters belongs to another VM")
	}
	wanted := map[int32]vmAdapterRecord{}
	for _, adapter := range record.Adapters {
		wanted[adapter.Key] = adapter
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, log)
	if err != nil {
		return 0, err
	}
	defer vsphere.logout()
	machine, err := vmReadSourceVM(ctx, vsphere, vmID, name, []string{"config.hardware.device"})
	if err != nil {
		return 0, err
	}
	if machine.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOff {
		return 0, errors.New("the old VM is not powered off; its adapters were not changed")
	}
	if machine.Config == nil {
		return 0, errors.New("vSphere did not return the VM's devices")
	}
	before := []vmAdapterRecord{}
	changes := []types.BaseVirtualDeviceConfigSpec{}
	for _, device := range machine.Config.Hardware.Device {
		card, ok := device.(types.BaseVirtualEthernetCard)
		if !ok {
			continue
		}
		nic := card.GetVirtualEthernetCard()
		want, ok := wanted[nic.Key]
		if !ok {
			continue
		}
		current := types.VirtualDeviceConnectInfo{AllowGuestControl: true}
		if nic.Connectable != nil {
			current = *nic.Connectable
		}
		label := want.Label
		before = append(before, vmAdapterRecord{Key: nic.Key, Label: label, StartConnected: current.StartConnected, Connected: current.Connected})
		if current.StartConnected == want.StartConnected {
			continue
		}
		current.StartConnected = want.StartConnected
		nic.Connectable = &current
		changes = append(changes, &types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationEdit, Device: device})
	}
	if len(before) < len(record.Adapters) {
		log(fmt.Sprintf("%d adapter(s) in the record are no longer on the VM.", len(record.Adapters)-len(before)))
	}
	if len(changes) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(saveFolder, 0700); err != nil {
		return 0, err
	}
	data, _ := json.MarshalIndent(map[string]interface{}{"vm": machine.Name, "id": vmID, "action": "reconnect network adapters for a rollback", "from": recordFile, "time": time.Now().UTC(), "adapters": before}, "", "  ")
	file := filepath.Join(saveFolder, vmUnsafeFileName.ReplaceAllString(machine.Name, "-")+"-"+time.Now().UTC().Format("20060102-150405")+"-rollback-adapters.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		return 0, err
	}
	log(fmt.Sprintf("Setting %d network adapter(s) of the old VM to connect at power on again (record: %s).", len(changes), file))
	task, err := object.NewVirtualMachine(vsphere.client, machine.Reference()).Reconfigure(ctx, types.VirtualMachineConfigSpec{DeviceChange: changes})
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		return 0, fmt.Errorf("the old VM's adapters could not be put back: %w", err)
	}
	return len(changes), nil
}

// vmPowerOnSource powers the old VM on after a rollback and waits until
// vSphere reports it on. A VM already on is left as it is.
func vmPowerOnSource(parent context.Context, options vmConnectOptions, vmID, name string, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, log)
	if err != nil {
		return err
	}
	defer vsphere.logout()
	machine, err := vmReadSourceVM(ctx, vsphere, vmID, name, []string{"runtime.question"})
	if err != nil {
		return err
	}
	if machine.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
		log("The old VM is already on.")
		return nil
	}
	if machine.Runtime.PowerState == types.VirtualMachinePowerStateSuspended {
		log("The old VM is suspended; resuming it.")
	}
	log("Powering the old VM " + name + " on.")
	vm := object.NewVirtualMachine(vsphere.client, machine.Reference())
	task, err := vm.PowerOn(ctx)
	if err != nil {
		return fmt.Errorf("vSphere refused to power it on: %w", err)
	}
	// A pending question (for example "moved or copied") blocks the task.
	done := make(chan error, 1)
	go func() { done <- task.Wait(ctx) }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("vSphere did not power it on: %w", err)
			}
			return nil
		case <-ticker.C:
			var current mo.VirtualMachine
			if property.DefaultCollector(vsphere.client).RetrieveOne(ctx, machine.Reference(), []string{"runtime.question"}, &current) == nil && current.Runtime.Question != nil {
				return fmt.Errorf("vSphere is waiting for an answer before it starts the VM (%s); answer it in the vSphere Client", current.Runtime.Question.Text)
			}
		case <-ctx.Done():
			return errors.New("vSphere did not power it on within ten minutes")
		}
	}
}

// vmIsNotFound says whether vSphere answered that the object does not exist.
func vmIsNotFound(err error) bool {
	var fault types.HasFault
	if errors.As(err, &fault) {
		if _, ok := fault.Fault().(*types.ManagedObjectNotFound); ok {
			return true
		}
	}
	return strings.Contains(err.Error(), "ManagedObjectNotFound") || strings.Contains(err.Error(), "has already been deleted")
}
