package main

import (
	"strings"
	"testing"
	"time"
)

// A report shaped like Read-HyperVHost.ps1's output for a remote host with
// synthetic data.
const hypervFixture = `{"ok":true,"error":"","result":{
 "host":{"name":"hv01","account":"EXAMPLE\\admin","domain":"example.com","cluster":"","vendor":"Contoso","model":"R1","cpuModel":"Xeon","cpuMhz":2400,"sockets":2,"cores":32,"threads":64,"memoryBytes":274877906944,"osName":"Microsoft Windows Server 2019 Datacenter","osVersion":"10.0.17763","osBuild":"17763",
  "volumes":[{"name":"C:","label":"","fileSystem":"NTFS","capacityBytes":107374182400,"freeBytes":53687091200},{"name":"D:","label":"VMs","fileSystem":"ReFS","capacityBytes":2199023255552,"freeBytes":1099511627776},{"name":"C:\\ClusterStorage\\Volume1","label":"","fileSystem":"CSVFS","capacityBytes":1099511627776,"freeBytes":549755813888}]},
 "switches":[{"id":"S1","name":"External"},{"id":"S2","name":"Private"}],
 "vms":[
  {"id":"0A1B2C3D-0000-4000-8000-000000000001","name":"app01","enabledState":2,"generation":2,"version":"9.0","secureBoot":true,"tpm":true,"shielded":false,"cpus":4,"memoryMB":8192,"dynamicMemory":true,
   "guestOs":"Windows Server 2019 Datacenter","guestOsVersion":"10.0.17763","guestHostName":"app01.example.com","integrationVersion":"10.0.17763","heartbeat":"ok",
   "checkpoints":[{"name":"Before patch","created":"2026-11-01T10:00:00Z","type":"Microsoft:Hyper-V:Snapshot:Realized"}],
   "disks":[{"path":"D:\\VMs\\app01\\app01_ABC.avhdx","controller":"SCSI","controllerNumber":"0","location":"0","format":"3","type":"4","maxInternalSize":136365211648,"fileSize":4294967296,"parentPath":"D:\\VMs\\app01\\app01.vhdx"},
            {"path":"C:\\ClusterStorage\\Volume1\\app01-data.vhdx","controller":"SCSI","controllerNumber":"0","location":"1","format":"3","type":"3","maxInternalSize":536870912000,"fileSize":107374182400,"shared":true},
            {"path":"","passthrough":true,"controller":"SCSI","location":"2","hostDisk":"Disk 3"}],
   "nics":[{"name":"Network Adapter","mac":"00155D010203","staticMac":true,"legacy":false,"switch":"External","connected":true,"vlan":20,"addresses":["192.0.2.10","fe80::1"],"dhcp":false,"guestKnown":true}],
   "media":["D:\\ISO\\tools.iso"]},
  {"id":"0A1B2C3D-0000-4000-8000-000000000002","name":"Legacy01","enabledState":3,"generation":1,"version":"5.0","cpus":1,"memoryMB":2048,
   "guestOs":"","heartbeat":"","checkpoints":[],
   "disks":[{"path":"\\\\files\\vms\\legacy01.vhd","controller":"IDE","controllerNumber":"0","location":"0","format":"2","type":"2","maxInternalSize":42949672960,"fileSize":42949673472}],
   "nics":[{"name":"Legacy Network Adapter","mac":"00155D0A0B0C","legacy":true,"switch":"","connected":false,"addresses":[],"dhcp":true}],"media":[]}
 ],"warnings":["sample warning"]}}`

func hypervFixtureInventory(t *testing.T, local bool) *vmInventory {
	t.Helper()
	report, err := parseHypervResult([]byte("\xef\xbb\xbf" + hypervFixture))
	if err != nil {
		t.Fatal(err)
	}
	return hypervInventoryFromReport(*report, local, time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), "test")
}

func hypervFinding(vm vmMachine, target, code string) *vmFinding {
	readiness := vm.readinessFor(target)
	if readiness == nil {
		return nil
	}
	for index := range readiness.Findings {
		if readiness.Findings[index].Code == code {
			return &readiness.Findings[index]
		}
	}
	return nil
}

func TestHyperVReportBecomesAnInventory(t *testing.T) {
	inventory := hypervFixtureInventory(t, false)
	if inventory.ServerType != hypervServerType || inventory.Server != "hv01" || len(inventory.Hosts) != 1 || inventory.Hosts[0].Cores != 32 {
		t.Fatalf("host: %+v", inventory)
	}
	if len(inventory.VMs) != 2 || inventory.VMs[0].Name != "app01" || inventory.VMs[1].Name != "Legacy01" {
		t.Fatalf("VMs not read or sorted: %+v", inventory.VMs)
	}
	app, legacy := inventory.VMs[0], inventory.VMs[1]
	if app.Source != vmSourceHyperV || app.PowerState != "poweredOn" || app.Firmware != "efi" || !app.SecureBoot || !app.VTPM || app.Generation != 2 {
		t.Fatalf("app01: %+v", app)
	}
	if legacy.PowerState != "poweredOff" || legacy.Firmware != "bios" || !legacy.Disks[0].BootCandidate {
		t.Fatalf("Legacy01: %+v", legacy)
	}
	if app.Disks[0].LocalPath != `\\hv01\D$\VMs\app01\app01_ABC.avhdx` || app.Disks[0].Provisioning != "differencing" || app.Disks[0].Datastore != "D:" || app.Disks[0].Format != "avhdx" {
		t.Fatalf("disk 0: %+v", app.Disks[0])
	}
	if app.Disks[1].Datastore != `C:\ClusterStorage\Volume1` || app.Disks[1].Sharing != "sharingMultiWriter" {
		t.Fatalf("disk 1: %+v", app.Disks[1])
	}
	if app.Disks[2].RDM != "physical" || app.Disks[2].LocalPath != "" {
		t.Fatalf("pass-through disk: %+v", app.Disks[2])
	}
	if legacy.Disks[0].LocalPath != `\\files\vms\legacy01.vhd` || legacy.Disks[0].Datastore != `\\files\vms` {
		t.Fatalf("SMB disk: %+v", legacy.Disks[0])
	}
	nic := app.NICs[0]
	if nic.MAC != "00:15:5d:01:02:03" || !nic.ManualMAC || !nic.Static || nic.Network != "External (VLAN 20)" || app.IPAddress != "192.0.2.10" {
		t.Fatalf("NIC: %+v ip %s", nic, app.IPAddress)
	}
	if legacy.NICs[0].Connected || legacy.NICs[0].AdapterType != "Legacy (emulated)" {
		t.Fatalf("legacy NIC: %+v", legacy.NICs[0])
	}
	if len(app.Snapshots) != 1 || app.Snapshots[0].Created.IsZero() {
		t.Fatalf("checkpoints: %+v", app.Snapshots)
	}
	stores := map[string]vmDatastore{}
	for _, store := range inventory.Datastores {
		stores[store.Name] = store
	}
	if stores["D:"].Type != "ReFS" || stores["D:"].VMCount != 1 || stores[`C:\ClusterStorage\Volume1`].Type != "CSV" || stores[`\\files\vms`].Type != "SMB" {
		t.Fatalf("datastores: %+v", inventory.Datastores)
	}
	networks := map[string]vmNetwork{}
	for _, network := range inventory.Networks {
		networks[network.Name] = network
	}
	if _, ok := networks["Private"]; !ok || networks["External (VLAN 20)"].VMCount != 1 || networks["External (VLAN 20)"].VLAN != "20" {
		t.Fatalf("networks: %+v", inventory.Networks)
	}
	if len(inventory.Warnings) != 1 || len(app.Readiness) == 0 {
		t.Fatalf("warnings %v, readiness %d", inventory.Warnings, len(app.Readiness))
	}
}

func TestHyperVLocalHostKeepsPaths(t *testing.T) {
	inventory := hypervFixtureInventory(t, true)
	if got := inventory.VMs[0].Disks[0].LocalPath; got != `D:\VMs\app01\app01_ABC.avhdx` {
		t.Fatalf("local path %q", got)
	}
}

func TestHyperVReadinessUsesHyperVFindings(t *testing.T) {
	inventory := hypervFixtureInventory(t, false)
	app, legacy := inventory.VMs[0], inventory.VMs[1]
	for _, readiness := range append(app.Readiness, legacy.Readiness...) {
		for _, finding := range readiness.Findings {
			if strings.HasPrefix(finding.Code, "tools-") || strings.Contains(finding.Message, "vSphere") || strings.Contains(finding.Message, "VMware Tools") {
				t.Fatalf("%s: VMware finding on a Hyper-V VM: %+v", readiness.Target, finding)
			}
		}
	}
	if hypervFinding(app, vmTargetHyperV, "already-hyperv") == nil {
		t.Fatal("no already-on-Hyper-V note")
	}
	if finding := hypervFinding(app, vmTargetScale, "snapshots"); finding == nil || !strings.Contains(finding.Message, "checkpoint") {
		t.Fatalf("checkpoint finding %+v", finding)
	}
	if hypervFinding(app, vmTargetScale, "passthrough-disk") == nil || hypervFinding(app, vmTargetScale, "rdm-physical") != nil {
		t.Fatal("pass-through disk finding missing or worded for VMware")
	}
	if hypervFinding(app, vmTargetScale, "shared-disk") == nil || hypervFinding(app, vmTargetScale, "dynamic-memory") == nil {
		t.Fatal("shared VHDX or Dynamic Memory finding missing")
	}
	if finding := hypervFinding(app, vmTargetScale, "media"); finding == nil || !strings.Contains(finding.Action, "Hyper-V host") {
		t.Fatalf("media finding %+v", finding)
	}
	if readiness := app.readinessFor(vmTargetHyperV); readiness.Generation != 2 {
		t.Fatalf("generation %d", readiness.Generation)
	}
	// A running VM whose integration services do not answer is flagged; a
	// powered-off one is not.
	silent := app
	silent.IntegrationServices = "noContact"
	if hypervFinding(vmMachine{Readiness: []vmReadiness{assessVM(silent, vmTargetScale, time.Now())}}, vmTargetScale, "integration-missing") == nil {
		t.Fatal("no integration services finding")
	}
	if hypervFinding(legacy, vmTargetScale, "integration-missing") != nil {
		t.Fatal("integration finding on a powered-off VM")
	}
	shielded := app
	shielded.Encrypted = true
	result := assessVM(shielded, vmTargetScale, time.Now())
	if result.Status != vmStatusBlocked || hypervFinding(vmMachine{Readiness: []vmReadiness{result}}, vmTargetScale, "shielded") == nil {
		t.Fatalf("shielded VM: %+v", result)
	}
}

func TestHyperVResultErrors(t *testing.T) {
	if _, err := parseHypervResult([]byte(`{"ok":false,"error":"Hyper-V is not installed on hv02 (it has no root\\virtualization\\v2 namespace)."}`)); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("error %v", err)
	}
	if _, err := parseHypervResult([]byte(`not json`)); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestHyperVPathHelpers(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{`D:\VMs\a.vhdx`, `\\hv01\D$\VMs\a.vhdx`},
		{`\\nas\vms\a.vhdx`, `\\nas\vms\a.vhdx`},
		{`relative.vhdx`, `relative.vhdx`},
	} {
		if got := hypervReachablePath(test.path, "hv01", false); got != test.want {
			t.Errorf("%s: %s, want %s", test.path, got, test.want)
		}
	}
	if hypervMAC("00-15-5D-AA-BB-CC") != "00:15:5d:aa:bb:cc" || hypervMAC("bad") != "bad" {
		t.Fatal("MAC formatting")
	}
	if hypervPowerState(32769) != "suspended" || hypervPowerState(42) != "unknown" {
		t.Fatal("power state")
	}
	if got := hypervVolumeFor(`E:\x.vhdx`, nil); got != "E:" {
		t.Fatalf("volume %q", got)
	}
}
