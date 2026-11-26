package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// vmReportDemoInventory is a fictional estate (example.com names, 192.0.2.x
// and 198.51.100.x addresses) with every kind of object and finding, for the
// report tests and the sample reports.
func vmReportDemoInventory() *vmInventory {
	now := vmTestNow
	inventory := &vmInventory{Schema: 1, Server: "vcenter.example.com", ServerType: "vCenter Server", Product: "VMware vCenter Server 8.0.2", APIVersion: "8.0.2.0", Build: "22617221", InstanceUUID: "5f1c2a90-0000-4c1e-9d7a-000000000001", OSType: "linux-x64", CollectedAt: now, CollectedBy: "EXAMPLE\\migration", AppVersion: "3.2.36", Datacenters: []string{"Head Office", "Branch"}}
	inventory.Clusters = []vmCluster{
		{Name: "Production", Datacenter: "Head Office", Hosts: 3, HA: true, HAAdmission: true, HAHostMonitoring: "enabled", DRS: true, DRSBehavior: "fullyAutomated", CPUCores: 96, MemoryBytes: 1536 << 30, EVCMode: "intel-cascadelake"},
		{Name: "Branch-Cluster", Datacenter: "Branch", Hosts: 1, HA: false, DRS: false, CPUCores: 16, MemoryBytes: 256 << 30},
	}
	details := func(serial, ip string, cpu, mem int) *vmHostDetails {
		return &vmHostDetails{SerialNumber: serial, AssetTag: "AT-" + serial[len(serial)-4:], UUID: "4c4c4544-0000-1010-8000-" + serial, BIOSVendor: "Example Systems", BIOSVersion: "2.19.1", BIOSDate: "2025-06-12", Firmware: "UEFI", Hyperthreading: "Active", CPUUsagePercent: cpu, MemUsagePercent: mem, BootTime: "2026-10-02 06:14", OverallStatus: "green", LockdownMode: "lockdownDisabled", ManagementIP: ip, HostName: "esx", Domain: "example.com", DNSServers: []string{"192.0.2.53", "192.0.2.54"}, SearchDomains: []string{"example.com"}, DefaultGateway: "192.0.2.1", TimeZone: "UTC", NTPServers: []string{"pool.example.com"}, NTPRunning: true, CertificateExpiry: "2027-09-01", CertificateIssuer: "VMCA", AcceptanceLevel: "partner",
			PhysicalNICs:    []vmHostNIC{{Device: "vmnic0", Driver: "i40en", Firmware: "9.20", MAC: "00:1b:21:00:00:01", PCI: "0000:18:00.0", SpeedMb: 25000, Duplex: true, Switch: "dvs-prod"}, {Device: "vmnic1", Driver: "i40en", Firmware: "9.20", MAC: "00:1b:21:00:00:02", PCI: "0000:18:00.1", SpeedMb: 25000, Duplex: true, Switch: "dvs-prod"}, {Device: "vmnic2", Driver: "ntg3", MAC: "00:1b:21:00:00:03", PCI: "0000:04:00.0", Switch: "vSwitch0"}},
			Switches:        []vmHostSwitch{{Name: "vSwitch0", Kind: "Standard", MTU: 1500, Ports: 128, Uplinks: []string{"vmnic2"}, Portgroups: []string{"Management Network", "VM Network"}}, {Name: "dvs-prod", Kind: "Distributed", MTU: 9000, Ports: 512, Uplinks: []string{"vmnic0", "vmnic1"}, Portgroups: []string{"Servers-120", "DMZ-200", "vMotion"}}},
			VMkernel:        []vmHostVMkernel{{Device: "vmk0", Portgroup: "Management Network", IP: ip, Mask: "255.255.255.0", MTU: 1500, Stack: "defaultTcpipStack", Services: []string{"management"}}, {Device: "vmk1", Portgroup: "vMotion", IP: strings.Replace(ip, "192.0.2", "198.51.100", 1), Mask: "255.255.255.0", MTU: 9000, Stack: "vmotion", Services: []string{"vmotion"}}},
			StorageAdapters: []vmHostAdapter{{Device: "vmhba0", Kind: "Block SCSI", Model: "Example RAID 940", Driver: "lsi_mr3", Status: "online", PCI: "0000:3b:00.0"}, {Device: "vmhba64", Kind: "iSCSI", Model: "iSCSI Software Adapter", Driver: "iscsi_vmk", Status: "online", Identifier: "iqn.1998-01.com.vmware:esx-" + serial, SendTargets: []string{"192.0.2.200:3260"}}},
			LUNs:            []vmHostLUN{{CanonicalName: "naa.600a0980000000000000000000000001", DisplayName: "SAN LUN 01", Type: "disk", Vendor: "EXAMPLE", Model: "Array 9000", CapacityBytes: 8 << 40, State: "ok"}, {CanonicalName: "naa.600a0980000000000000000000000002", DisplayName: "SAN LUN 02", Type: "disk", Vendor: "EXAMPLE", Model: "Array 9000", CapacityBytes: 4 << 40, SSD: true, State: "ok"}, {CanonicalName: "mpx.vmhba0:C0:T0:L0", DisplayName: "Local boot disk", Type: "disk", Vendor: "EXAMPLE", Model: "M.2 Boot", CapacityBytes: 240 << 30, SSD: true, Local: true, State: "ok"}},
			Services:        []vmHostService{{Key: "TSM-SSH", Label: "SSH", Policy: "off"}, {Key: "ntpd", Label: "NTP Daemon", Running: true, Policy: "on"}, {Key: "vpxa", Label: "VMware vCenter Agent", Running: true, Policy: "on"}, {Key: "sfcbd-watchdog", Label: "CIM Server", Running: true, Policy: "on"}},
			Packages:        []vmHostPackage{{Name: "esx-base", Version: "8.0.2-0.0.22380479", Vendor: "VMware", Acceptance: "VMwareCertified", Created: "2023-09-21"}, {Name: "i40en", Version: "2.5.1.0-1OEM.800.1.0.20613240", Vendor: "INT", Acceptance: "VMwareCertified", Created: "2024-02-10"}, {Name: "lsi-mr3", Version: "7.727.02.00-1OEM", Vendor: "BCM", Acceptance: "VMwareCertified", Created: "2024-01-05"}}}
	}
	host := func(name, dc, cluster, serial, ip string, cpu, mem int) vmHost {
		h := vmHost{Name: name, Datacenter: dc, Cluster: cluster, Vendor: "Example Systems", Model: "ES-R750", CPUModel: "Intel(R) Xeon(R) Gold 6338 CPU @ 2.00GHz", Sockets: 2, Cores: 32, Threads: 64, CPUMhz: 2000, MemoryBytes: 512 << 30, Version: "8.0.2", Build: "22380479", ConnectionState: "connected", PowerState: "poweredOn", License: &vmLicenseRef{Name: "vSphere 8 Enterprise Plus", Key: "AB12C-*****-*****-*****-9XY7Z"}, Details: details(serial, ip, cpu, mem)}
		h.Details.HostName = strings.SplitN(name, ".", 2)[0]
		return h
	}
	inventory.Hosts = []vmHost{
		host("esx01.example.com", "Head Office", "Production", "SN0001", "192.0.2.11", 38, 71),
		host("esx02.example.com", "Head Office", "Production", "SN0002", "192.0.2.12", 44, 79),
		host("esx03.example.com", "Head Office", "Production", "SN0003", "192.0.2.13", 21, 52),
		host("esx-br01.example.com", "Branch", "Branch-Cluster", "SN0101", "192.0.2.111", 12, 40),
	}
	inventory.Hosts[2].Maintenance = true
	inventory.Hosts[3].Cores, inventory.Hosts[3].Threads, inventory.Hosts[3].MemoryBytes, inventory.Hosts[3].Sockets = 16, 32, 256<<30, 1
	inventory.Hosts[1].Details.Alarms = []string{"Host memory usage"}
	inventory.Datastores = []vmDatastore{
		{Name: "SAN-PROD-01", Datacenter: "Head Office", Type: "VMFS", CapacityBytes: 8 << 40, FreeBytes: 2 << 40, ProvisionedBytes: 9 << 40, Accessible: true, Shared: true, Hosts: []string{"esx01.example.com", "esx02.example.com", "esx03.example.com"}, URL: "ds:///vmfs/volumes/6510a2b1-prod01/"},
		{Name: "SAN-PROD-02", Datacenter: "Head Office", Type: "VMFS", CapacityBytes: 4 << 40, FreeBytes: 400 << 30, ProvisionedBytes: 4 << 40, Accessible: true, Shared: true, Hosts: []string{"esx01.example.com", "esx02.example.com", "esx03.example.com"}, URL: "ds:///vmfs/volumes/6510a2b1-prod02/"},
		{Name: "NFS-ISO", Datacenter: "Head Office", Type: "NFS", CapacityBytes: 2 << 40, FreeBytes: 1600 << 30, ProvisionedBytes: 400 << 30, Accessible: true, Shared: true, Hosts: []string{"esx01.example.com", "esx02.example.com"}},
		{Name: "BR-LOCAL-01", Datacenter: "Branch", Type: "VMFS", CapacityBytes: 2 << 40, FreeBytes: 900 << 30, ProvisionedBytes: 1300 << 30, Accessible: true, Hosts: []string{"esx-br01.example.com"}},
	}
	inventory.Networks = []vmNetwork{
		{Name: "Servers-120", Datacenter: "Head Office", Kind: "Distributed", Switch: "dvs-prod", VLAN: "120"},
		{Name: "DMZ-200", Datacenter: "Head Office", Kind: "Distributed", Switch: "dvs-prod", VLAN: "200"},
		{Name: "VM Network", Datacenter: "Head Office", Kind: "Standard", Switch: "vSwitch0", VLAN: "None"},
		{Name: "Branch-LAN", Datacenter: "Branch", Kind: "Standard", Switch: "vSwitch0", VLAN: "10"},
	}
	inventory.Licenses = []vmLicense{
		{Name: "vSphere 8 Enterprise Plus", Edition: "esx.enterprisePlus.cpuPackageCoreLimited", Key: "AB12C-*****-*****-*****-9XY7Z", Product: "VMware ESX Server", Version: "8.*", Total: 8, Used: 7, Unit: "cpuPackage", AssignedTo: []string{"esx01.example.com", "esx02.example.com", "esx03.example.com", "esx-br01.example.com"}},
		{Name: "vCenter Server 8 Standard", Edition: "vc.standard.instance", Key: "CD34E-*****-*****-*****-1QW2E", Product: "VMware VirtualCenter Server", Version: "8.*", Total: 1, Used: 1, Unit: "server", Expires: "2027-01-31", AssignedTo: []string{"vcenter.example.com"}},
	}
	inventory.Switches = []vmDistributedSwitch{{Name: "dvs-prod", Datacenter: "Head Office", Version: "8.0.0", MTU: 9000, Ports: 512, Uplinks: 2, Discovery: "lldp", Hosts: []string{"esx01.example.com", "esx02.example.com", "esx03.example.com"}}}
	inventory.ResourcePools = []vmResourcePool{{Name: "Tier-1", Datacenter: "Head Office", Owner: "Production", Parent: "Resources", CPUReservationMHz: 8000, CPULimitMHz: -1, MemReservationMB: 65536, MemLimitMB: -1, VMCount: 4}}

	disk := func(label string, gb int64, ds string) vmDisk {
		return vmDisk{Label: label, CapacityBytes: gb << 30, Provisioning: "thin", Mode: "persistent", Sharing: "sharingNone", Controller: "pvscsi", BusSharing: "noSharing", Datastore: ds, File: "[" + ds + "] " + label}
	}
	nic := func(network, mac, ip string, static bool) vmNIC {
		n := vmNIC{Label: "Network adapter 1", AdapterType: "vmxnet3", Network: network, MAC: mac, Connected: true, GuestKnown: ip != "", Static: static}
		if ip != "" {
			n.IPAddresses = []string{ip}
		}
		if static {
			n.StaticAddresses, n.Gateways, n.DNSServers = n.IPAddresses, []string{"192.0.2.1"}, []string{"192.0.2.53"}
		}
		return n
	}
	type spec struct {
		name, host, guestID, os string
		on                      bool
		cpus                    int
		memGB                   int64
		diskGB                  int64
		ds, net, ip             string
		static                  bool
		firmware                string
		tools                   string
	}
	specs := []spec{
		{"DC01", "esx01", "windows2019srvNext_64Guest", "Microsoft Windows Server 2022 (64-bit)", true, 2, 8, 80, "SAN-PROD-01", "Servers-120", "192.0.2.21", true, "efi", "guestToolsCurrent"},
		{"DC02", "esx02", "windows2019srvNext_64Guest", "Microsoft Windows Server 2022 (64-bit)", true, 2, 8, 80, "SAN-PROD-01", "Servers-120", "192.0.2.22", true, "efi", "guestToolsCurrent"},
		{"FS01", "esx01", "windows2019srv_64Guest", "Microsoft Windows Server 2019 (64-bit)", true, 4, 16, 1200, "SAN-PROD-01", "Servers-120", "192.0.2.30", true, "efi", "guestToolsCurrent"},
		{"SQL01", "esx02", "windows9Server64Guest", "Microsoft Windows Server 2016 (64-bit)", true, 8, 64, 900, "SAN-PROD-02", "Servers-120", "192.0.2.40", true, "bios", "guestToolsNeedUpgrade"},
		{"APP01", "esx01", "windows2019srvNext_64Guest", "Microsoft Windows Server 2022 (64-bit)", true, 4, 16, 120, "SAN-PROD-01", "Servers-120", "192.0.2.50", false, "efi", "guestToolsCurrent"},
		{"APP02", "esx03", "windows2019srvNext_64Guest", "Microsoft Windows Server 2022 (64-bit)", true, 4, 16, 120, "SAN-PROD-02", "Servers-120", "192.0.2.51", false, "efi", "guestToolsCurrent"},
		{"WEB01", "esx02", "ubuntu64Guest", "Ubuntu Linux (64-bit)", true, 2, 4, 40, "SAN-PROD-01", "DMZ-200", "198.51.100.10", true, "efi", "guestToolsUnmanaged"},
		{"WEB02", "esx03", "ubuntu64Guest", "Ubuntu Linux (64-bit)", true, 2, 4, 40, "SAN-PROD-02", "DMZ-200", "198.51.100.11", true, "efi", "guestToolsUnmanaged"},
		{"PRINT01", "esx01", "windows9Server64Guest", "Microsoft Windows Server 2016 (64-bit)", false, 2, 4, 60, "SAN-PROD-01", "VM Network", "", false, "bios", "guestToolsCurrent"},
		{"LEGACY-ERP", "esx02", "winNetEnterpriseGuest", "Microsoft Windows Server 2003, Enterprise Edition (32-bit)", true, 1, 2, 40, "SAN-PROD-02", "VM Network", "192.0.2.90", true, "bios", "guestToolsNotInstalled"},
		{"MON01", "esx03", "rhel9_64Guest", "Red Hat Enterprise Linux 9 (64-bit)", true, 2, 8, 100, "SAN-PROD-01", "Servers-120", "192.0.2.60", false, "efi", "guestToolsUnmanaged"},
		{"BR-FS01", "esx-br01", "windows2019srv_64Guest", "Microsoft Windows Server 2019 (64-bit)", true, 2, 8, 500, "BR-LOCAL-01", "Branch-LAN", "192.0.2.130", true, "efi", "guestToolsCurrent"},
		{"BR-APP01", "esx-br01", "windows2019srvNext_64Guest", "Microsoft Windows Server 2022 (64-bit)", true, 2, 8, 100, "BR-LOCAL-01", "Branch-LAN", "192.0.2.131", false, "efi", "guestToolsCurrent"},
		{"vCLS-a1b2c3", "esx01", "other4xLinux64Guest", "Other 4.x or later Linux (64-bit)", true, 1, 0, 2, "SAN-PROD-01", "", "", false, "bios", "guestToolsUnmanaged"},
		{"vcenter", "esx02", "vmwarePhoton64Guest", "VMware Photon OS (64-bit)", true, 4, 19, 600, "SAN-PROD-02", "Servers-120", "192.0.2.5", true, "efi", "guestToolsUnmanaged"},
	}
	for index, s := range specs {
		hostName := s.host + ".example.com"
		cluster, dc := "Production", "Head Office"
		if strings.HasPrefix(s.host, "esx-br") {
			cluster, dc = "Branch-Cluster", "Branch"
		}
		power := "poweredOff"
		if s.on {
			power = "poweredOn"
		}
		mem := s.memGB << 10
		if mem == 0 {
			mem = 128
		}
		vm := vmMachine{Name: s.name, ID: fmt.Sprintf("vm-%d", 1001+index), Datacenter: dc, Cluster: cluster, Host: hostName, Folder: "Servers", PowerState: power, GuestID: s.guestID, GuestOS: s.os, CPUs: s.cpus, CoresPerSocket: 1, MemoryMB: mem, HardwareVersion: "vmx-19", Firmware: s.firmware, SecureBoot: s.firmware == "efi" && strings.HasPrefix(s.name, "DC"), ToolsStatus: s.tools, ToolsRunning: s.on, ProvisionedBytes: s.diskGB << 30, UsedBytes: s.diskGB << 29, ChangeTracking: index%3 == 0}
		if s.on {
			vm.GuestOSRunning = s.os
			vm.IPAddress = s.ip
			vm.GuestHostName = strings.ToLower(s.name) + ".example.com"
		}
		vm.Disks = []vmDisk{disk("Hard disk 1", s.diskGB, s.ds)}
		vm.Disks[0].BootCandidate = true
		if s.net != "" {
			vm.NICs = []vmNIC{nic(s.net, fmt.Sprintf("00:50:56:9a:00:%02x", index+1), s.ip, s.static)}
		}
		if s.on && s.ip != "" {
			vm.GuestDisks = []vmGuestDisk{{Path: "C:\\", CapacityBytes: s.diskGB << 30, FreeBytes: s.diskGB << 28}}
		}
		inventory.VMs = append(inventory.VMs, vm)
	}
	sql := &inventory.VMs[3]
	sql.Disks = append(sql.Disks, vmDisk{Label: "Hard disk 2", CapacityBytes: 500 << 30, Provisioning: "raw device mapping", RDM: "physical", Mode: "independent_persistent", Controller: "lsilogic-sas", Bus: 1, BusSharing: "physicalSharing"})
	sql.Snapshots = []vmSnapshot{{Name: "before CU update", Created: now.Add(-21 * 24 * time.Hour)}}
	inventory.VMs[2].Snapshots = []vmSnapshot{{Name: "pre-patch", Created: now.Add(-3 * 24 * time.Hour), Quiesced: true}}
	inventory.VMs[4].VTPM = true
	inventory.VMs[4].Encrypted = true
	inventory.VMs[4].EncryptionRead = true
	inventory.VMs[6].ConnectedMedia = []string{"[NFS-ISO] ubuntu-24.04.iso"}
	inventory.VMs = append(inventory.VMs, vmMachine{Name: "TPL-WS2022", ID: "vm-2001", Datacenter: "Head Office", Cluster: "Production", Host: "esx03.example.com", Template: true, PowerState: "poweredOff", GuestOS: "Microsoft Windows Server 2022 (64-bit)", CPUs: 2, MemoryMB: 4096, ProvisionedBytes: 60 << 30, Disks: []vmDisk{disk("Hard disk 1", 60, "SAN-PROD-01")}})
	for i := range inventory.Hosts {
		count := 0
		for _, vm := range inventory.VMs {
			if vm.Host == inventory.Hosts[i].Name {
				count++
			}
		}
		inventory.Hosts[i].VMCount = count
	}
	for i := range inventory.Datastores {
		count := 0
		for _, vm := range inventory.VMs {
			for _, d := range vm.Disks {
				if d.Datastore == inventory.Datastores[i].Name {
					count++
					break
				}
			}
		}
		inventory.Datastores[i].VMCount = count
	}
	inventory.Warnings = []string{"esx-br01.example.com: software packages (VIBs) could not be listed: the host did not answer in time."}
	assessInventory(inventory, now)
	return inventory
}

var vmReportScriptTag = regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)

func TestReportModelCountsAndGroupsIssues(t *testing.T) {
	inventory := vmReportDemoInventory()
	model := vmReportBuild(inventory, vmTestNow)
	if len(model.VMs) != 15 || len(model.Templates) != 1 {
		t.Fatalf("%d VMs, %d templates", len(model.VMs), len(model.Templates))
	}
	for _, target := range model.Targets {
		if target.Ready+target.Review+target.Blocked+target.Skip != len(model.VMs) || target.Total != len(model.VMs) {
			t.Fatalf("%s counts do not add up to every VM: %+v", target.ID, target)
		}
	}
	if model.Focus.ID != vmTargetHyperV || model.Focus.Skip < 2 {
		t.Fatalf("focus: %+v", model.Focus)
	}
	issues := model.IssuesFor(vmTargetHyperV)
	if len(issues) == 0 || issues[0].Severity != vmSeverityBlocker {
		t.Fatalf("issues are not worst first: %+v", issues)
	}
	seen := map[string]bool{}
	for _, issue := range issues {
		if seen[issue.Code+issue.Severity] {
			t.Fatalf("issue %s listed twice", issue.Code)
		}
		seen[issue.Code+issue.Severity] = true
		if issue.Severity == vmSeverityInfo || len(issue.VMs) == 0 || issue.Message == "" {
			t.Fatalf("bad issue: %+v", issue)
		}
	}
	static := 0
	for _, issue := range issues {
		if issue.Code == "static-ip" {
			static = len(issue.VMs)
		}
	}
	// Windows VMs' static IPs are put back by the Hyper-V cutover (rule 45)
	// and are information; the Linux web servers still need it by hand.
	if static < 2 {
		t.Fatalf("static IP issue groups %d VMs", static)
	}
	for _, tile := range model.Tiles {
		if tile.Href == "" || tile.Value == "" {
			t.Fatalf("tile without a link or value: %+v", tile)
		}
	}
	hyperv := inventory
	hyperv.ServerType = hypervServerType
	if words := vmReportBuild(hyperv, vmTestNow).Words; words.Platform != "Hyper-V" || words.Tools == "VMware Tools" {
		t.Fatalf("Hyper-V words: %+v", words)
	}
}

func TestInteractiveReportIsSelfContainedLinkedAndEscaped(t *testing.T) {
	inventory := vmReportDemoInventory()
	inventory.VMs[0].Name = `<img src=x onerror=alert(1)>`
	inventory.Hosts[0].Name = "<b>esx</b>"
	folder := t.TempDir()
	report, err := writeVMwareInventoryOutputs(folder, inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{report, vmDiscoveryReportFor(report)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		page := string(data)
		scripts := vmReportScriptTag.FindAllStringSubmatch(page, -1)
		if len(scripts) != 1 || scripts[0][1] != vmReportScript {
			t.Fatalf("%s: the report must hold only its own script", filepath.Base(path))
		}
		if !strings.Contains(page, "script-src '"+strings.ReplaceAll(vmReportScriptHash, "+", "&#43;")+"'") || regexp.MustCompile(`script-src[^;"]*unsafe`).MatchString(page) || strings.Contains(page, "'unsafe-eval'") {
			t.Fatal("the script is not pinned by its hash")
		}
		if strings.Contains(page, "<img src=x") || !strings.Contains(page, "&lt;img src=x") || strings.Contains(page, "<b>esx</b>") {
			t.Fatal("names are not escaped")
		}
		for _, banned := range []string{"http://", "https://", "src=\"//", "@import", "url("} {
			if strings.Contains(strings.ReplaceAll(page, "http://www.w3.org", ""), banned) {
				t.Fatalf("the report could make a request: %q", banned)
			}
		}
		// Every in-page link has a target.
		ids := map[string]bool{}
		for _, match := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(page, -1) {
			ids[match[1]] = true
		}
		for _, match := range regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(page, -1) {
			if !ids[match[1]] {
				t.Fatalf("link to #%s has no target", match[1])
			}
		}
		for _, want := range []string{`class="tree" data-view="hosts"`, `data-view="storage"`, `data-view="network"`, `class="tile ready"`, `data-status="blocked"`, `class="issue blocker"`, "Fix:", "Virtual machines on this host", "VM disks on this datastore", "Disks not on a datastore in this inventory", "Licenses", "vmnic0", "Production"} {
			if !strings.Contains(page, want) {
				t.Fatalf("%s is missing %q", filepath.Base(path), want)
			}
		}
	}
	readiness, _ := os.ReadFile(report)
	discovery, _ := os.ReadFile(vmDiscoveryReportFor(report))
	if !strings.Contains(string(readiness), `data-start="overview"`) || !strings.Contains(string(discovery), `data-start="hosts"`) {
		t.Fatal("the two reports should open at the overview and the hosts")
	}
}

func TestWordReportHasContentsLinksAndColours(t *testing.T) {
	model := vmReportBuild(vmReportDemoInventory(), vmTestNow)
	path := filepath.Join(t.TempDir(), vmReportDocx)
	if err := writeVMReportDocx(path, model); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	parts := map[string]string{}
	for _, file := range reader.File {
		handle, _ := file.Open()
		data, _ := io.ReadAll(handle)
		handle.Close()
		parts[file.Name] = string(data)
	}
	document := parts["word/document.xml"]
	for _, part := range []string{"word/styles.xml", "word/settings.xml", "word/footer1.xml", "word/_rels/document.xml.rels", "[Content_Types].xml"} {
		if parts[part] == "" {
			t.Fatalf("%s missing", part)
		}
	}
	bookmarks := map[string]bool{}
	for _, match := range regexp.MustCompile(`<w:bookmarkStart w:id="\d+" w:name="([^"]+)"/>`).FindAllStringSubmatch(document, -1) {
		if bookmarks[match[1]] {
			t.Fatalf("bookmark %s twice", match[1])
		}
		if len(match[1]) > 40 {
			t.Fatalf("bookmark %s is longer than Word allows", match[1])
		}
		bookmarks[match[1]] = true
	}
	links := regexp.MustCompile(`<w:hyperlink w:anchor="([^"]+)"`).FindAllStringSubmatch(document, -1)
	if len(links) < 50 {
		t.Fatalf("only %d internal links", len(links))
	}
	for _, match := range links {
		if !bookmarks[match[1]] {
			t.Fatalf("link to %s has no bookmark", match[1])
		}
	}
	for _, want := range []string{`TOC \o "1-2" \h`, `w:fill="E6F6EE"`, `w:fill="FDE8E8"`, "Readiness by target", "Issues and fixes", "DC01", "esx01.example.com", "PAGE"} {
		if !strings.Contains(document+parts["word/footer1.xml"], want) {
			t.Fatalf("Word report is missing %q", want)
		}
	}
	if strings.Contains(parts["word/settings.xml"], "updateFields") {
		t.Fatal("the Word report must open without asking to update fields")
	}
}

func TestPDFReportHasOutlineLinksAndPages(t *testing.T) {
	model := vmReportBuild(vmReportDemoInventory(), vmTestNow)
	path := filepath.Join(t.TempDir(), vmReportPDF)
	if err := writeVMReportPDF(path, model); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data, []byte("%%EOF")) {
		t.Fatal("not a PDF")
	}
	for _, want := range []string{"/Outlines", "/Annots", "/Link", "/Dest", "/Title"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("PDF is missing %s", want)
		}
	}
	if pages := bytes.Count(data, []byte("/Type /Page\n")) + bytes.Count(data, []byte("/Type /Page ")) + bytes.Count(data, []byte("/Type/Page\n")); pages < 5 {
		t.Fatalf("only %d pages", pages)
	}
}

// TestWriteSampleReports writes the reports for the demo estate to
// SIMM_REPORT_SAMPLES, for screenshots and review. It does nothing unless
// that variable names a folder.
func TestWriteSampleReports(t *testing.T) {
	folder := os.Getenv("SIMM_REPORT_SAMPLES")
	if folder == "" {
		t.Skip("set SIMM_REPORT_SAMPLES to write the sample reports")
	}
	if _, err := writeVMwareInventoryOutputs(folder, vmReportDemoInventory()); err != nil {
		t.Fatal(err)
	}
}
