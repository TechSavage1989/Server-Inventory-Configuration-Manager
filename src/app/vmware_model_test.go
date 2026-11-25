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
		rdm := vmSeverityBlocker
		if target == vmTargetPhysical {
			rdm = vmSeverityWarning // the LUN can be presented to the server itself
		}
		if r.Status != vmStatusBlocked || codes["shared-disk"] != vmSeverityBlocker || codes["rdm-physical"] != rdm || codes["independent-disk"] != vmSeverityWarning || codes["snapshots"] != vmSeverityWarning {
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
		usb := vmSeverityWarning
		if target == vmTargetPhysical {
			usb = vmSeverityInfo // plug the device into the server
		}
		if r.Status != vmStatusBlocked || c["os-unsupported"] != vmSeverityBlocker || c["encrypted"] != vmSeverityBlocker || c["tools-missing"] != vmSeverityWarning || c["usb"] != usb || c["hardware-old"] != vmSeverityWarning {
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
	for _, expected := range []string{"Wave 1", "Wave 2", "On hold (resolve blockers first)", "Generation 2", "Record settings", "Disconnect the old VM"} {
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

func TestReadinessNewTargets(t *testing.T) {
	inventory := vmSampleInventory()
	app := inventory.VMs[0] // Windows Server 2022, EFI, Secure Boot, static IP
	cases := []struct {
		target   string
		want     map[string]string
		absent   []string
		contains map[string]string
	}{
		{vmTargetNutanix, map[string]string{"virtio-drivers": vmSeverityWarning, "secureboot": vmSeverityWarning, "static-ip": vmSeverityWarning, "firmware": vmSeverityInfo}, []string{"aws-drivers", "instance-size"}, map[string]string{"virtio-drivers": "Nutanix VirtIO"}},
		{vmTargetProxmox, map[string]string{"virtio-drivers": vmSeverityWarning, "static-ip": vmSeverityWarning}, []string{"generation"}, map[string]string{"virtio-drivers": "virtio-win"}},
		{vmTargetAzure, map[string]string{"generation": vmSeverityInfo, "static-ip": vmSeverityWarning, "instance-size": vmSeverityInfo, "secureboot": vmSeverityInfo}, []string{"virtio-drivers", "aws-drivers"}, map[string]string{"static-ip": "DHCP"}},
		{vmTargetAWS, map[string]string{"aws-drivers": vmSeverityWarning, "static-ip": vmSeverityWarning, "instance-size": vmSeverityInfo}, []string{"virtio-drivers", "generation"}, map[string]string{"aws-drivers": "ENA"}},
		{vmTargetPhysical, map[string]string{"hardware-drivers": vmSeverityWarning, "server-size": vmSeverityInfo, "firmware": vmSeverityInfo, "static-ip": vmSeverityWarning}, []string{"virtio-drivers", "instance-size"}, map[string]string{"static-ip": "new server"}},
	}
	for _, c := range cases {
		r := app.readinessFor(c.target)
		if r == nil {
			t.Fatalf("%s: no readiness", c.target)
		}
		codes := vmFindingCodes(r)
		for code, severity := range c.want {
			if codes[code] != severity {
				t.Fatalf("%s: %s = %q, want %q (%v)", c.target, code, codes[code], severity, codes)
			}
		}
		for _, code := range c.absent {
			if _, ok := codes[code]; ok {
				t.Fatalf("%s: unexpected %s", c.target, code)
			}
		}
		for code, text := range c.contains {
			found := false
			for _, f := range r.Findings {
				if f.Code == code && strings.Contains(f.Message+" "+f.Action, text) {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: %s does not mention %q", c.target, code, text)
			}
		}
		if r.Status != vmStatusReview {
			t.Fatalf("%s: status %s", c.target, r.Status)
		}
	}
	if g := app.readinessFor(vmTargetAzure).Generation; g != 2 {
		t.Fatalf("Azure generation %d", g)
	}
	// SQL01: BIOS boot disk over 2 TB; physical-mode RDM.
	sql := inventory.VMs[1]
	if c := vmFindingCodes(sql.readinessFor(vmTargetPhysical)); c["firmware-bios"] != vmSeverityWarning {
		t.Fatalf("physical BIOS warning missing: %v", c)
	}
	big := sql
	big.Disks = []vmDisk{{Label: "Hard disk 1", CapacityBytes: 5000 << 30, BootCandidate: true}}
	big.Readiness = nil
	if c := vmFindingCodes(&[]vmReadiness{assessVM(big, vmTargetAzure, vmTestNow)}[0]); c["azure-os-disk"] != vmSeverityBlocker {
		t.Fatalf("Azure 4 TiB OS disk limit not enforced: %v", c)
	}
	// Linux drivers follow the target.
	for target, driver := range map[string]string{vmTargetAWS: "ena", vmTargetNutanix: "virtio_scsi", vmTargetAzure: "hv_netvsc"} {
		found := false
		for _, f := range inventory.VMs[2].readinessFor(target).Findings {
			if f.Code == "linux-initramfs" && strings.Contains(f.Action, driver) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: Linux initramfs action does not name %s", target, driver)
		}
	}
	if r := assessVM(app, "nonsense", vmTestNow); r.Status != vmStatusBlocked {
		t.Fatal("unknown target was not refused")
	}
}

func TestTargetProfilesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range vmTargetProfiles {
		if seen[p.ID] {
			t.Fatalf("duplicate target %s", p.ID)
		}
		seen[p.ID] = true
		if p.Label == "" || p.Short == "" || p.Tool == "" || p.Family == "" || p.LinuxDrivers == "" || p.SecureBoot == "" || p.VTPM == "" || p.SharedDisks == "" || p.Passthrough == "" || p.USB == "" || p.Availability == "" || p.StorageLabel == "" || p.NetworkLabel == "" {
			t.Fatalf("incomplete profile %+v", p)
		}
		if p.Kind != vmKindCluster && p.Kind != vmKindCloud && p.Kind != vmKindPhysical {
			t.Fatalf("%s: kind %q", p.ID, p.Kind)
		}
	}
	for _, id := range []string{"hyperv", "scale", "nutanix", "proxmox", "azure", "aws", "physical"} {
		if !seen[id] {
			t.Fatalf("missing target %s", id)
		}
	}
}

func TestRunbookAndSizingFollowTarget(t *testing.T) {
	inventory := vmSampleInventory()
	for target, expected := range map[string][]string{
		vmTargetNutanix:  {"Nutanix Move", "Nutanix VirtIO drivers", "Create or check the Nutanix AHV VM", "AHV storage container"},
		vmTargetAzure:    {"Azure Migrate", "static private IP reservation", "uses DHCP", "Generation 2"},
		vmTargetAWS:      {"Application Migration Service", "EC2 instance", "ENA"},
		vmTargetPhysical: {"Prepare the server", "storage controller and network drivers", "Start the new server", "Shut down the new server"},
		vmTargetProxmox:  {"ESXi import wizard", "virtio-win"},
	} {
		plan := suggestWaves(defaultVMPlan(inventory), inventory, 10)
		plan.Target = target
		waves := buildRunbook(plan, inventory)
		text := ""
		for _, wave := range waves {
			for _, item := range wave.VMs {
				text += strings.Join(item.Before, "\n") + strings.Join(item.Cutover, "\n") + strings.Join(item.Rollback, "\n") + strings.Join(item.Disks, "\n")
			}
		}
		for _, want := range expected {
			if !strings.Contains(text, want) {
				t.Fatalf("%s runbook missing %q", target, want)
			}
		}
		if strings.Contains(text, "HyperCore") {
			t.Fatalf("%s runbook mentions HyperCore", target)
		}
		sizing := sizePlan(plan, inventory)
		switch target {
		case vmTargetAzure, vmTargetAWS:
			if sizing.Status != "quota" || sizing.Configured {
				t.Fatalf("%s sizing: %+v", target, sizing)
			}
		case vmTargetPhysical:
			if sizing.Status != "perserver" {
				t.Fatalf("physical sizing: %+v", sizing)
			}
		}
	}
	plan := defaultVMPlan(inventory)
	plan.Target = vmTargetNutanix
	if validated, err := validateVMPlan(plan, inventory); err != nil || validated.Target != vmTargetNutanix {
		t.Fatalf("Nutanix plan refused: %v", err)
	}
	plan.Target = "xen"
	if _, err := validateVMPlan(plan, inventory); err == nil {
		t.Fatal("unknown target accepted")
	}
}

func TestRealEstateFixes(t *testing.T) {
	inventory := &vmInventory{Schema: 1, Server: "vc.example.com"}
	inventory.VMs = []vmMachine{
		{Name: "vCenter", ID: "vm-1", PowerState: "poweredOn", GuestID: "vmkernel5Guest", GuestOS: "VMware Photon OS (64-bit)", Firmware: "bios"},
		{Name: "vCLS-5afadc21-641b", ID: "vm-2", PowerState: "poweredOn", GuestID: "other3xLinux64Guest", GuestOS: "Other 3.x or later Linux (64-bit)", ToolsRunning: false},
		{Name: "CORP-DC01", ID: "vm-3", PowerState: "poweredOn", GuestID: "windows2019srv_64Guest", GuestOSRunning: "Microsoft Windows Server 2022 (64-bit)", Firmware: "efi", ToolsRunning: true},
		{Name: "AI01", ID: "vm-4", PowerState: "poweredOn", GuestID: "windows2019srv_64Guest", GuestOSRunning: "Microsoft Windows Server 2022 (64-bit)", Firmware: "efi", ToolsRunning: true,
			Snapshots: []vmSnapshot{{Name: "Acronis_Mon Nov 23 23:53:16 2026", Created: vmTestNow.Add(-24 * time.Hour)}}},
		{Name: "APPDC-SERVICE", ID: "vm-5", PowerState: "poweredOn", GuestID: "windows2019srv_64Guest", GuestOSRunning: "Microsoft Windows Server 2022 (64-bit)", Firmware: "efi", ToolsRunning: true},
	}
	assessInventory(inventory, vmTestNow)
	for _, vm := range inventory.VMs[:2] {
		for _, r := range vm.Readiness {
			if r.Status != vmStatusSkip || len(r.Findings) != 1 || r.Findings[0].Code != "vmware-infrastructure" {
				t.Fatalf("%s %s: %+v", vm.Name, r.Target, r)
			}
		}
	}
	if !strings.Contains(inventory.VMs[0].Readiness[0].Findings[0].Message, "vCenter Server appliance") {
		t.Fatal(inventory.VMs[0].Readiness[0].Findings[0].Message)
	}
	if vmFindingCodes(inventory.VMs[2].readinessFor(vmTargetHyperV))["domain-controller"] != vmSeverityWarning {
		t.Fatal("domain controller not flagged")
	}
	if _, ok := vmFindingCodes(inventory.VMs[4].readinessFor(vmTargetHyperV))["domain-controller"]; ok {
		t.Fatal("APPDC-SERVICE is not a domain controller name")
	}
	for _, f := range inventory.VMs[3].readinessFor(vmTargetHyperV).Findings {
		if f.Code == "snapshots" && (!strings.Contains(f.Message, "backup software") || !strings.Contains(f.Action, "backup job")) {
			t.Fatalf("%+v", f)
		}
	}
	summary := summarizeInventory(inventory)
	if summary.Readiness[0].Skip != 2 {
		t.Fatalf("%+v", summary.Readiness[0])
	}
	plan := reconcileVMPlan(vmPlan{}, inventory)
	if !plan.VMs[0].Exclude || !plan.VMs[1].Exclude || plan.VMs[2].Exclude {
		t.Fatalf("%+v", plan.VMs)
	}
	if vmToolsVersionText("2147483647") != "guest-managed (open-vm-tools)" || vmToolsVersionText("12325") != "12325" {
		t.Fatal("tools version text")
	}
}
