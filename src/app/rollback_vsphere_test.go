package main

import (
	"context"
	"crypto/tls"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// A cutover's disconnect is undone exactly by the rollback, and the old VM
// powers on with its adapters connecting again.
func TestRollbackReconnectsAndStartsTheOldVMAgainstSimulator(t *testing.T) {
	model := simulator.VPX()
	model.Machine = 1
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
	defer session.logout()
	finder := find.NewFinder(session.client, true)
	datacenter, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(datacenter)
	machines, err := finder.VirtualMachineList(ctx, "*")
	if err != nil || len(machines) == 0 {
		t.Fatal(err)
	}
	vm := machines[0]
	name, _ := vm.ObjectName(ctx)
	id := vm.Reference().Value
	task, _ := vm.PowerOff(ctx)
	if err := task.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	changed, record, err := vmDisconnectSourceAdaptersRecord(ctx, options, id, name, folder, nil)
	if err != nil || changed == 0 || record == "" {
		t.Fatalf("disconnect: %d %q %v", changed, record, err)
	}
	if _, err := vmReconnectSourceAdapters(ctx, options, id, "other", record, folder, nil); err == nil || !strings.Contains(err.Error(), "now called") {
		t.Fatalf("a renamed VM must be refused: %v", err)
	}
	if _, err := vmReconnectSourceAdapters(ctx, options, "vm-404", name, record, folder, nil); err == nil {
		t.Fatal("a record of another VM must be refused")
	}
	back, err := vmReconnectSourceAdapters(ctx, options, id, name, record, folder, nil)
	if err != nil || back != changed {
		t.Fatalf("reconnect: %d of %d, %v", back, changed, err)
	}
	var machine mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"config.hardware.device", "runtime.powerState"}, &machine); err != nil {
		t.Fatal(err)
	}
	for _, device := range machine.Config.Hardware.Device {
		if card, ok := device.(types.BaseVirtualEthernetCard); ok {
			if c := card.GetVirtualEthernetCard().Connectable; c == nil || !c.StartConnected {
				t.Fatalf("adapter does not connect at power on again: %+v", c)
			}
		}
	}
	if files, _ := filepath.Glob(filepath.Join(folder, "*-rollback-adapters.json")); len(files) != 1 {
		t.Fatalf("the rollback must save the settings it changes: %v", files)
	}
	if again, err := vmReconnectSourceAdapters(ctx, options, id, name, record, folder, nil); err != nil || again != 0 {
		t.Fatalf("a second reconnect must change nothing: %d %v", again, err)
	}
	if err := vmPowerOnSource(ctx, options, id, "other", nil); err == nil {
		t.Fatal("a renamed VM must not be powered on")
	}
	if err := vmPowerOnSource(ctx, options, id, name, nil); err != nil {
		t.Fatal(err)
	}
	if err := vm.Properties(ctx, vm.Reference(), []string{"runtime.powerState"}, &machine); err != nil || machine.Runtime.PowerState != types.VirtualMachinePowerStatePoweredOn {
		t.Fatalf("the old VM is not on: %v %v", machine.Runtime.PowerState, err)
	}
	if err := vmPowerOnSource(ctx, options, id, name, nil); err != nil {
		t.Fatalf("a VM already on is left as it is: %v", err)
	}
	if _, err := vmReconnectSourceAdapters(ctx, options, id, name, record, folder, nil); err == nil || !strings.Contains(err.Error(), "not powered off") {
		t.Fatalf("a running VM's adapters must not be changed: %v", err)
	}
	if err := vmPowerOnSource(ctx, options, "vm-9999", name, nil); err == nil || !strings.Contains(err.Error(), "no longer in") {
		t.Fatalf("a deleted VM must say so: %v", err)
	}
}

// The cutover's MIGRATED name and note are put back exactly by Roll back.
func TestOldVMIsMarkedMigratedAndRollbackPutsItBack(t *testing.T) {
	model := simulator.VPX()
	model.Machine = 1
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
	defer session.logout()
	finder := find.NewFinder(session.client, true)
	datacenter, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(datacenter)
	machines, err := finder.VirtualMachineList(ctx, "*")
	if err != nil || len(machines) == 0 {
		t.Fatal(err)
	}
	vm := machines[0]
	name, _ := vm.ObjectName(ctx)
	id := vm.Reference().Value
	task, _ := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{Annotation: "Owner: finance"})
	if err := task.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	if _, _, err := vmMarkSourceMigrated(ctx, options, id, name, "HV01", folder, nil); err == nil || !strings.Contains(err.Error(), "not powered off") {
		t.Fatalf("a running VM must not be renamed: %v", err)
	}
	task, _ = vm.PowerOff(ctx)
	if err := task.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vmMarkSourceMigrated(ctx, options, id, "other", "HV01", folder, nil); err == nil {
		t.Fatal("a VM with another name must not be renamed")
	}
	newName, record, err := vmMarkSourceMigrated(ctx, options, id, name, "HV01", folder, nil)
	if err != nil || newName != vmMigratedName(name) || record == "" {
		t.Fatalf("mark: %q %q %v", newName, record, err)
	}
	var machine mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"name", "config.annotation"}, &machine); err != nil {
		t.Fatal(err)
	}
	if machine.Name != newName || !strings.HasPrefix(machine.Config.Annotation, "MIGRATED ") || !strings.HasSuffix(machine.Config.Annotation, "Owner: finance") || !strings.Contains(machine.Config.Annotation, "HV01") {
		t.Fatalf("not marked: %q %q", machine.Name, machine.Config.Annotation)
	}
	if _, err := vmUnmarkSource(ctx, options, "vm-404", record, nil); err == nil {
		t.Fatal("a record of another VM must be refused")
	}
	changed, err := vmUnmarkSource(ctx, options, id, record, nil)
	if err != nil || !changed {
		t.Fatalf("unmark: %v %v", changed, err)
	}
	if err := vm.Properties(ctx, vm.Reference(), []string{"name", "config.annotation"}, &machine); err != nil {
		t.Fatal(err)
	}
	if machine.Name != name || machine.Config.Annotation != "Owner: finance" {
		t.Fatalf("not put back: %q %q", machine.Name, machine.Config.Annotation)
	}
	if changed, err := vmUnmarkSource(ctx, options, id, record, nil); err != nil || changed {
		t.Fatalf("a second rollback must change nothing: %v %v", changed, err)
	}
	if err := vmPowerOnSource(ctx, options, id, name, nil); err != nil {
		t.Fatalf("the old VM starts by its own name again: %v", err)
	}
}
