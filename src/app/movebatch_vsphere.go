package main

// After a migration plan's cutover, the old VMware VM can have its network
// adapters set to not connect (opt-in, Disconnect the old VM's network), so
// starting it by mistake cannot put a second copy of the server on the
// network with the same name and addresses. The VM must be powered off; a
// JSON record of every adapter's connection settings is saved before the
// change so it can be put back by hand. Nothing else about the VM changes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

type vmAdapterRecord struct {
	Key            int32  `json:"key"`
	Label          string `json:"label"`
	StartConnected bool   `json:"startConnected"`
	Connected      bool   `json:"connected"`
}

// vmDisconnectSourceAdapters sets every adapter of the powered-off VM to not
// connect at power on. It returns how many adapters it changed.
func vmDisconnectSourceAdapters(parent context.Context, options vmConnectOptions, vmID, name, recordFolder string, log func(string)) (int, error) {
	if log == nil {
		log = func(string) {}
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, log)
	if err != nil {
		return 0, err
	}
	defer vsphere.logout()
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmID}
	var machine mo.VirtualMachine
	if err := property.DefaultCollector(vsphere.client).RetrieveOne(ctx, ref, []string{"name", "runtime.powerState", "config.hardware.device"}, &machine); err != nil {
		return 0, fmt.Errorf("the VM could not be read: %v", vmFriendlyConnectError(err, vsphere.host))
	}
	if machine.Name != name {
		return 0, fmt.Errorf("the VM with this ID is now called %q, not %q; its adapters were not changed", machine.Name, name)
	}
	if machine.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOff {
		return 0, errors.New("the VM is not powered off; its adapters were not changed")
	}
	if machine.Config == nil {
		return 0, errors.New("vSphere did not return the VM's devices")
	}
	records := []vmAdapterRecord{}
	changes := []types.BaseVirtualDeviceConfigSpec{}
	for _, device := range machine.Config.Hardware.Device {
		card, ok := device.(types.BaseVirtualEthernetCard)
		if !ok {
			continue
		}
		nic := card.GetVirtualEthernetCard()
		label := fmt.Sprintf("adapter %d", nic.Key)
		if nic.DeviceInfo != nil {
			label = nic.DeviceInfo.GetDescription().Label
		}
		record := vmAdapterRecord{Key: nic.Key, Label: label}
		if nic.Connectable != nil {
			record.StartConnected, record.Connected = nic.Connectable.StartConnected, nic.Connectable.Connected
		}
		records = append(records, record)
		if nic.Connectable != nil && !nic.Connectable.StartConnected && !nic.Connectable.Connected {
			continue
		}
		connectable := types.VirtualDeviceConnectInfo{AllowGuestControl: true}
		if nic.Connectable != nil {
			connectable = *nic.Connectable
		}
		connectable.StartConnected, connectable.Connected = false, false
		nic.Connectable = &connectable
		changes = append(changes, &types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationEdit, Device: device})
	}
	if len(changes) == 0 {
		return 0, nil
	}
	if recordFolder == "" {
		return 0, errors.New("no folder for the adapters' record; nothing was changed")
	}
	if err := os.MkdirAll(recordFolder, 0700); err != nil {
		return 0, err
	}
	raw, _ := json.MarshalIndent(map[string]interface{}{"vm": machine.Name, "id": vmID, "action": "disconnect network adapters after cutover", "time": time.Now().UTC(), "adapters": records}, "", "  ")
	file := filepath.Join(recordFolder, vmUnsafeFileName.ReplaceAllString(machine.Name, "-")+"-"+time.Now().UTC().Format("20060102-150405")+"-adapters.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		return 0, err
	}
	log(fmt.Sprintf("Setting %d network adapter(s) of the old VM to not connect (record: %s).", len(changes), file))
	task, err := object.NewVirtualMachine(vsphere.client, ref).Reconfigure(ctx, types.VirtualMachineConfigSpec{DeviceChange: changes})
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		return 0, fmt.Errorf("the old VM's adapters could not be disconnected: %w", err)
	}
	return len(changes), nil
}
