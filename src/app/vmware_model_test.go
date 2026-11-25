package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var vmTestNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// vmSampleInventory is fictional: example.com names and documentation
// addresses only.
func vmSampleInventory() *vmInventory {
	thin := func(label string, gb int64) vmDisk {
		return vmDisk{Label: label, CapacityBytes: gb << 30, Provisioning: "thin", Mode: "persistent", Sharing: "sharingNone", Controller: "pvscsi", BusSharing: "noSharing", Datastore: "DS-PROD-01"}
	}
	inventory := &vmInventory{Schema: 1, Server: "vcenter.example.com", ServerType: "vCenter Server", Product: "VMware vCenter Server 8.0.2", CollectedAt: vmTestNow, CollectedBy: "EXAMPLE\\admin", AppVersion: "test",
		Clusters:   []vmCluster{{Name: "Cluster-A", Datacenter: "DC1", Hosts: 3, HA: true, DRS: true, CPUCores: 96, MemoryBytes: 1536 << 30}},
		Hosts:      []vmHost{{Name: "esx01.example.com", Datacenter: "DC1", Cluster: "Cluster-A", Sockets: 2, Cores: 32, MemoryBytes: 512 << 30, Version: "8.0.2", VMCount: 4}},
		Datastores: []vmDatastore{{Name: "DS-PROD-01", Type: "VMFS", CapacityBytes: 10 << 40, FreeBytes: 4 << 40}},
		Networks:   []vmNetwork{{Name: "VM Network", Kind: "Standard", VLAN: "None"}, {Name: "Servers-120", Kind: "Distributed", Switch: "dvs-prod", VLAN: "120"}},
	}
	inventory.VMs = []vmMachine{
		{Name: "APP01", ID: "vm-101", Datacenter: "DC1", Cluster: "Cluster-A", PowerState: "poweredOn", GuestID: "windows2019srvNext_64Guest", GuestOSRunning: "Microsoft Windows Server 2022 (64-bit)", CPUs: 4, MemoryMB: 16384, HardwareVersion: "vmx-19", Firmware: "efi", SecureBoot: true, ToolsStatus: "guestToolsCurrent", ToolsRunning: true, ProvisionedBytes: 200 << 30, UsedBytes: 80 << 30,
			Disks: []vmDisk{thin("Hard disk 1", 100), thin("Hard disk 2", 100)},
			NICs:  []vmNIC{{Label: "Network adapter 1", AdapterType: "vmxnet3", Network: "Servers-120", MAC: "00:50:56:00:00:01", Connected: true, IPAddresses: []string{"192.0.2.10/24"}, Static: true, GuestKnown: true}}},
		{Name: "SQL01", ID: "vm-102", PowerState: "poweredOn", GuestOSRunning: "Microsoft Windows Server 2016 (64-bit)", GuestID: "windows9Server64Guest", CPUs: 8, MemoryMB: 65536, HardwareVersion: "vmx-14", Firmware: "bios", ToolsStatus: "guestToolsCurrent", ToolsRunning: true, ProvisionedBytes: 3000 << 30, UsedBytes: 1500 << 30,
			Disks:     []vmDisk{func() vmDisk { d := thin("Hard disk 1", 2100); d.BootCandidate = true; return d }(), {Label: "Hard disk 2", CapacityBytes: 500 << 30, Provisioning: "raw device mapping", RDM: "physical", Mode: "independent_persistent", Controller: "lsilogic-sas", BusSharing: "physicalSharing"}},
			NICs:      []vmNIC{{Label: "Network adapter 1", AdapterType: "vmxnet3", Network: "VM Network", MAC: "00:50:56:00:00:02", ManualMAC: true, Connected: true, IPAddresses: []string{"192.0.2.20/24"}, GuestKnown: true}},
			Snapshots: []vmSnapshot{{Name: "before patch", Created: vmTestNow.Add(-10 * 24 * time.Hour)}}},
		{Name: "WEB-LNX", ID: "vm-103", PowerState: "poweredOff", GuestID: "ubuntu64Guest", GuestOS: "Ubuntu Linux (64-bit)", CPUs: 2, MemoryMB: 4096, HardwareVersion: "vmx-19", Firmware: "efi", ToolsStatus: "guestToolsUnmanaged", ProvisionedBytes: 40 << 30, UsedBytes: 12 << 30, Disks: []vmDisk{thin("Hard disk 1", 40)}, ConnectedMedia: []string{"ubuntu-22.04.iso"}},
		{Name: "OLD-NT", ID: "vm-104", PowerState: "poweredOn", GuestID: "winNetEnterpriseGuest", GuestOS: "Microsoft Windows Server 2003, Enterprise Edition (32-bit)", CPUs: 1, MemoryMB: 2048, HardwareVersion: "vmx-04", Firmware: "bios", ToolsStatus: "guestToolsNotInstalled", ProvisionedBytes: 20 << 30, UsedBytes: 20 << 30, Disks: []vmDisk{thin("Hard disk 1", 20)}, USBDevices: 1, Encrypted: true},
		{Name: "TPL-2022", ID: "vm-105", Template: true, PowerState: "poweredOff", GuestOS: "Microsoft Windows Server 2022 (64-bit)", CPUs: 2, MemoryMB: 4096, ProvisionedBytes: 60 << 30},
	}
	assessInventory(inventory, vmTestNow)
	return inventory
}

func vmFindingCodes(r *vmReadiness) map[string]string {
	codes := map[string]string{}
	for _, f := range r.Findings {
		codes[f.Code] = f.Severity
	}
	return codes
}

func TestReadinessModernWindowsWithStaticIP(t *testing.T) {
	vm := vmSampleInventory().VMs[0]
	hyperV, scale := vm.readinessFor(vmTargetHyperV), vm.readinessFor(vmTargetScale)
	if hyperV.Status != vmStatusReview || hyperV.Generation != 2 {
		t.Fatalf("Hyper-V: %+v", hyperV)
	}
	codes := vmFindingCodes(hyperV)
	if codes["static-ip"] != vmSeverityWarning || codes["tools-remove"] != vmSeverityInfo || codes["generation"] != vmSeverityInfo {
		t.Fatalf("Hyper-V findings: %v", codes)
	}
	if _, ok := codes["virtio-drivers"]; ok {
		t.Fatal("VirtIO drivers flagged for Hyper-V")
	}
	scaleCodes := vmFindingCodes(scale)
	if scaleCodes["virtio-drivers"] != vmSeverityWarning || scaleCodes["secureboot"] != vmSeverityWarning {
		t.Fatalf("HyperCore findings: %v", scaleCodes)
	}
	if hyperV.Findings[0].Severity != vmSeverityWarning {
		t.Fatal("findings are not ordered worst first")
	}
}

func TestReadinessBlocksSharedDisksAndPhysicalRDM(t *testing.T) {
	vm := vmSampleInventory().VMs[1]
	for _, target := range vmTargets {
		r := vm.readinessFor(target)
		codes := vmFindingCodes(r)
		if r.Status != vmStatusBlocked || codes["shared-disk"] != vmSeverityBlocker || codes["rdm-physical"] != vmSeverityBlocker || codes["independent-disk"] != vmSeverityWarning || codes["snapshots"] != vmSeverityWarning {
			t.Fatalf("%s: %s %v", target, r.Status, codes)
		}
	}
	hyperV := vm.readinessFor(vmTargetHyperV)
	if hyperV.Generation != 1 || vmFindingCodes(hyperV)["gen1-boot-disk"] != vmSeverityWarning || vmFindingCodes(hyperV)["manual-mac"] != vmSeverityInfo {
		t.Fatalf("Generation 1 boot disk limit not flagged: %+v", hyperV)
	}
	for _, f := range hyperV.Findings {
		if f.Code == "snapshots" && !strings.Contains(f.Message, "10 day") {
			t.Fatalf("snapshot age missing: %s", f.Message)
		}
	}
}

func TestReadinessLinuxAndObsoleteGuests(t *testing.T) {
	inventory := vmSampleInventory()
	linux := inventory.VMs[2]
	codes := vmFindingCodes(linux.readinessFor(vmTargetScale))
	if codes["linux-initramfs"] != vmSeverityWarning || codes["media"] != vmSeverityWarning || codes["powered-off"] != vmSeverityInfo {
		t.Fatalf("Linux findings: %v", codes)
	}
	if _, ok := codes["tools-remove"]; ok {
		t.Fatal("open-vm-tools should not be flagged for removal")
	}
	old := inventory.VMs[3]
	for _, target := range vmTargets {
		r := old.readinessFor(target)
		c := vmFindingCodes(r)
		if r.Status != vmStatusBlocked || c["os-unsupported"] != vmSeverityBlocker || c["encrypted"] != vmSeverityBlocker || c["tools-missing"] != vmSeverityWarning || c["usb"] != vmSeverityWarning || c["hardware-old"] != vmSeverityWarning {
			t.Fatalf("%s: %v", target, c)
		}
	}
}

func TestClassifyGuestOS(t *testing.T) {
	cases := []struct {
		name, id, family, support string
	}{
		{"Microsoft Windows Server 2025 (64-bit)", "", "windows", "current"},
		{"Microsoft Windows Server 2012 R2 (64-bit)", "", "windows", "legacy"},
		{"Microsoft Windows Server 2008 R2 (64-bit)", "", "windows", "obsolete"},
		{"Microsoft Windows 11 (64-bit)", "", "windows", "current"},
		{"", "windows8Server64Guest", "windows", "legacy"},
		{"", "winNetStandardGuest", "windows", "unsupported"},
		{"Red Hat Enterprise Linux 9 (64-bit)", "", "linux", "current"},
		{"CentOS 6 (64-bit)", "", "linux", "obsolete"},
		{"Ubuntu Linux (64-bit)", "ubuntu64Guest", "linux", "current"},
		{"Ubuntu 14.04.6 LTS", "", "linux", "obsolete"},
		{"Oracle Solaris 11 (64-bit)", "", "other", "unsupported"},
		{"FreeBSD 13 (64-bit)", "", "bsd", "legacy"},
		{"", "", "unknown", "unknown"},
	}
	for _, c := range cases {
		class := classifyGuestOS(vmMachine{GuestOSRunning: c.name, GuestID: c.id})
		if class.Family != c.family || class.Supported != c.support {
			t.Errorf("%q/%q -> %s/%s, want %s/%s", c.name, c.id, class.Family, class.Supported, c.family, c.support)
		}
	}
}

func TestPlanReconcileValidateAndWaves(t *testing.T) {
	inventory := vmSampleInventory()
	plan := defaultVMPlan(inventory)
	if len(plan.Networks) != 2 || plan.networkFor("Servers-120").TargetVLAN != "120" || plan.networkFor("VM Network").TargetVLAN != "" {
		t.Fatalf("network defaults: %+v", plan.Networks)
	}
	if len(plan.VMs) != 5 || !plan.entry("vm-105").Exclude {
		t.Fatalf("template should start excluded: %+v", plan.VMs)
	}
	plan = suggestWaves(plan, inventory, 10)
	if plan.entry("vm-103").Wave != 1 || plan.entry("vm-101").Wave != 2 || !plan.entry("vm-102").Hold || !plan.entry("vm-104").Hold {
		t.Fatalf("suggested waves: %+v", plan.VMs)
	}
	edited := plan
	edited.Networks = append([]vmNetworkMap{}, plan.Networks...)
	edited.Networks[0].TargetVLAN = "5000"
	if _, err := validateVMPlan(edited, inventory); err == nil {
		t.Fatal("VLAN 5000 accepted")
	}
	edited.Networks[0].TargetVLAN = "120"
	edited.VMs = append(append([]vmPlanVM{}, plan.VMs...), vmPlanVM{ID: "vm-999", Wave: 1})
	if _, err := validateVMPlan(edited, inventory); err == nil {
		t.Fatal("unknown VM accepted")
	}
	edited.VMs = plan.VMs
	edited.Target = "kvm"
	if _, err := validateVMPlan(edited, inventory); err == nil {
		t.Fatal("unknown target accepted")
	}
	edited.Target = vmTargetScale
	saved, err := validateVMPlan(edited, inventory)
	if err != nil || saved.Target != vmTargetScale || saved.UpdatedAt.IsZero() {
		t.Fatalf("valid plan refused: %v", err)
	}
	// A VM that disappears from the inventory is dropped; a new one is added.
	inventory.VMs = append(inventory.VMs[:4:4], vmMachine{Name: "NEW01", ID: "vm-106", PowerState: "poweredOn"})
	reconciled := reconcileVMPlan(saved, inventory)
	if len(reconciled.VMs) != 5 || reconciled.entry("vm-106").Name != "NEW01" || reconciled.entry("vm-101").Wave != 2 {
		t.Fatalf("reconcile: %+v", reconciled.VMs)
	}
}

func TestSizing(t *testing.T) {
	inventory := vmSampleInventory()
	plan := defaultVMPlan(inventory)
	sizing := sizePlan(plan, inventory)
	if sizing.Configured || sizing.VMs != 4 || sizing.VCPUs != 15 {
		t.Fatalf("unconfigured sizing: %+v", sizing)
	}
	plan.Capacity = vmCapacity{Nodes: 3, CoresPerNode: 16, MemoryGBPerNode: 256, UsableStorageTB: 10, ReserveNodes: 1, MaxVCPUPerCore: 4, ReservedMemoryGB: 8}
	sizing = sizePlan(plan, inventory)
	if !sizing.Configured || sizing.Status != "ok" || sizing.Cores != 32 {
		t.Fatalf("sizing: %+v", sizing)
	}
	plan.Capacity.UsableStorageTB = 1.5
	if sizing = sizePlan(plan, inventory); sizing.Status != "over" {
		t.Fatalf("storage over not detected: %+v", sizing)
	}
	plan.Capacity.UsableStorageTB = 10
	plan.Capacity.Nodes, plan.Capacity.ReserveNodes = 1, 1
	if sizing = sizePlan(plan, inventory); sizing.Status != "over" {
		t.Fatalf("all nodes reserved not detected: %+v", sizing)
	}
}

func TestReportsAndRunbookRenderEscapedAndSafe(t *testing.T) {
	inventory := vmSampleInventory()
	inventory.VMs[0].Name = "<script>alert(1)</script>"
	inventory.VMs[2].Name = "=HYPERLINK(\"http://example.com\")"
	folder := t.TempDir()
	report, err := writeVMwareInventoryOutputs(folder, inventory)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(report)
	if strings.Contains(string(html), "<script>alert") || !strings.Contains(string(html), "&lt;script&gt;") {
		t.Fatal("VM name not escaped in the report")
	}
	if strings.Contains(string(html), "<script") || strings.Contains(string(html), "http-equiv=\"refresh\"") {
		t.Fatal("report contains script")
	}
	csvData, _ := os.ReadFile(filepath.Join(folder, "VMs.csv"))
	if !strings.Contains(string(csvData), "'=HYPERLINK") {
		t.Fatal("CSV formula not neutralised")
	}
	for _, name := range []string{vmInventoryFile, "VM-Disks.csv", "VM-Network-Adapters.csv", "Readiness-Findings.csv", "Hosts.csv", "Datastores.csv", "Networks.csv", "Clusters.csv", "VM-Snapshots.csv"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("%s missing", name)
		}
	}
	plan := suggestWaves(defaultVMPlan(inventory), inventory, 10)
	plan.Capacity = vmCapacity{Nodes: 3, CoresPerNode: 16, MemoryGBPerNode: 256, UsableStorageTB: 10, ReserveNodes: 1, MaxVCPUPerCore: 4}
	runbook, planCSV, err := writeVMwarePlanOutputs(folder, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(runbook)
	for _, expected := range []string{"Wave 1", "Wave 2", "On hold (resolve blockers first)", "Generation 2", "guest pre-flight", "Disconnect the old VM"} {
		if !strings.Contains(string(text), expected) {
			t.Fatalf("runbook missing %q", expected)
		}
	}
	if strings.Contains(string(text), "TPL-2022") {
		t.Fatal("excluded template is in the runbook")
	}
	planText, _ := os.ReadFile(planCSV)
	if !strings.Contains(string(planText), "Servers-120 > Servers-120 (VLAN 120)") {
		t.Fatalf("plan CSV network mapping missing: %s", planText)
	}
}
