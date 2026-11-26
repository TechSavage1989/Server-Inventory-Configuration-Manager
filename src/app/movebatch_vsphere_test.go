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
	"github.com/vmware/govmomi/vim25/types"
)

func TestDisconnectSourceAdaptersAgainstSimulator(t *testing.T) {
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
	finder := find.NewFinder(session.client, true)
	datacenter, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(datacenter)
	machines, err := finder.VirtualMachineList(ctx, "*")
	if err != nil || len(machines) == 0 {
		t.Fatal(err)
	}
	vm := machines[0]
	name, _ := vm.ObjectName(ctx)
	folder := t.TempDir()
	if _, err := vmDisconnectSourceAdapters(ctx, options, vm.Reference().Value, name, folder, nil); err == nil || !strings.Contains(err.Error(), "not powered off") {
		t.Fatalf("a running VM must be refused: %v", err)
	}
	if _, err := vmDisconnectSourceAdapters(ctx, options, vm.Reference().Value, "other", folder, nil); err == nil || !strings.Contains(err.Error(), "now called") {
		t.Fatalf("a renamed VM must be refused: %v", err)
	}
	task, err := vm.PowerOff(ctx)
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	changed, err := vmDisconnectSourceAdapters(ctx, options, vm.Reference().Value, name, folder, nil)
	if err != nil || changed == 0 {
		t.Fatalf("changed %d: %v", changed, err)
	}
	var machine mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"config.hardware.device"}, &machine); err != nil {
		t.Fatal(err)
	}
	for _, device := range machine.Config.Hardware.Device {
		if card, ok := device.(types.BaseVirtualEthernetCard); ok {
			if c := card.GetVirtualEthernetCard().Connectable; c == nil || c.StartConnected || c.Connected {
				t.Fatalf("adapter still connects: %+v", c)
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(folder, "*-adapters.json"))
	if len(files) != 1 {
		t.Fatalf("expected one record, got %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	if !strings.Contains(string(raw), `"startConnected": true`) {
		t.Fatalf("the record must hold the settings before the change: %s", raw)
	}
	if again, err := vmDisconnectSourceAdapters(ctx, options, vm.Reference().Value, name, folder, nil); err != nil || again != 0 {
		t.Fatalf("a second run must change nothing: %d %v", again, err)
	}
	session.logout()
}
