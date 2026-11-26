package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hvBuildTestInventory is a synthetic two-host VMware cluster: vSwitch0 with
// management and VM port groups, vSwitch1 with vMotion, vSwitch2 with only
// iSCSI VMkernel ports, a software iSCSI adapter with two portals, and two
// shared VMFS datastores.
func hvBuildTestInventory() *vmInventory {
	host := func(name, mgmt string) vmHost {
		return vmHost{Name: name, Datacenter: "DC1", Cluster: "Prod", Cores: 32, MemoryBytes: 512 << 30, Details: &vmHostDetails{
			DNSServers: []string{"192.0.2.10"}, NTPServers: []string{"time.example.com"},
			Switches: []vmHostSwitch{
				{Name: "vSwitch0", Kind: "Standard", MTU: 1500, Uplinks: []string{"vmnic0", "vmnic1"}, Portgroups: []string{"Management Network", "VM Network", "Servers-20"}},
				{Name: "vSwitch1", Kind: "Standard", MTU: 9000, Uplinks: []string{"vmnic2", "vmnic3"}, Portgroups: []string{"vMotion"}},
				{Name: "vSwitch2", Kind: "Standard", MTU: 9000, Uplinks: []string{"vmnic4", "vmnic5"}, Portgroups: []string{"iSCSI-A", "iSCSI-B"}},
			},
			VMkernel: []vmHostVMkernel{
				{Device: "vmk0", Portgroup: "Management Network", IP: mgmt, Services: []string{"management"}},
				{Device: "vmk1", Portgroup: "vMotion", IP: "10.0.30.11", Services: []string{"vmotion"}, VLAN: "30"},
				{Device: "vmk2", Portgroup: "iSCSI-A", IP: "10.0.40.11", Services: []string{}},
			},
			StorageAdapters: []vmHostAdapter{{Device: "vmhba64", Kind: "iSCSI (software)", Identifier: "iqn.1998-01.com.vmware:" + name, SendTargets: []string{"10.0.40.5:3260", "10.0.41.5:3260"}}},
			LUNs:            []vmHostLUN{{CanonicalName: "naa.1", Type: "disk", Vendor: "EXAMPLE", Model: "Array LUN", CapacityBytes: 2 << 40}},
		}}
	}
	return &vmInventory{
		Server: "vcenter.example.com", Datacenters: []string{"DC1"},
		Clusters: []vmCluster{{Name: "Prod", Datacenter: "DC1", Hosts: 2, HA: true, DRS: true, EVCMode: "intel-broadwell"}},
		Hosts:    []vmHost{host("esx02.example.com", "192.0.2.22"), host("esx01.example.com", "192.0.2.21")},
		Datastores: []vmDatastore{
			{Name: "SAN-01", Datacenter: "DC1", Type: "VMFS", CapacityBytes: 4 << 40, FreeBytes: 1 << 40, Shared: true, VMCount: 12, Hosts: []string{"esx01.example.com", "esx02.example.com"}},
			{Name: "Other", Datacenter: "DC1", Type: "VMFS", CapacityBytes: 1 << 40, FreeBytes: 1 << 39, Shared: true, Hosts: []string{"esx09.example.com"}},
			{Name: "NFS-ISO", Datacenter: "DC1", Type: "NFS", CapacityBytes: 1 << 40, FreeBytes: 1 << 40, Shared: true, Hosts: []string{"esx01.example.com"}},
		},
		Networks: []vmNetwork{
			{Name: "VM Network", Datacenter: "DC1", Kind: "Standard", Switch: "vSwitch0", VLAN: "0", VMCount: 4},
			{Name: "Servers-20", Datacenter: "DC1", Kind: "Standard", Switch: "vSwitch0", VLAN: "20", VMCount: 9},
		},
	}
}

// hvBuildTestPlan is the suggested plan with everything the operator types.
func hvBuildTestPlan(t *testing.T) hvBuildPlan {
	t.Helper()
	plan, err := hvBuildSuggest(hvBuildTestInventory(), "Prod")
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Nodes {
		number := string(rune('1' + index))
		plan.Nodes[index].Name = "HV" + number + ".example.com"
		plan.Nodes[index].Switches = map[string][]string{"SET-vSwitch0": {"SLOT 1 Port 1", "SLOT 1 Port 2"}, "SET-vSwitch1": {"SLOT 2 Port 1", "SLOT 2 Port 2"}}
		plan.Nodes[index].HostIPs = map[string]string{"LiveMigration": "10.0.30.2" + number + "/24", "Cluster": "10.0.31.2" + number + "/24"}
		plan.Nodes[index].Storage = []hvBuildNodeNIC{{Adapter: "SLOT 3 Port 1", IP: "10.0.40.2" + number + "/24"}, {Adapter: "SLOT 3 Port 2", IP: "10.0.41.2" + number + "/24"}}
	}
	plan.Cluster.Name = "HVCLU01"
	plan.Cluster.Addresses = []string{"192.0.2.30"}
	plan.Storage.Disks = []hvBuildDisk{
		{UniqueID: "60002AC0000000000000000000012345", Label: "CSV-SAN-01", FileSystem: "ReFS", Use: "csv", Datastore: "SAN-01"},
		{UniqueID: "60002AC0000000000000000000012346", Label: "Witness", Use: "witness"},
	}
	plan.Settings.VHDPath = `C:\ClusterStorage\Volume1\Hyper-V`
	checked, err := hvBuildCheckPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	return checked
}

func TestHVBuildSuggestsSwitchesStorageAndClusterFromVMware(t *testing.T) {
	plan, err := hvBuildSuggest(hvBuildTestInventory(), "Prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 2 || plan.Nodes[0].ESXi != "esx01.example.com" || plan.Nodes[0].Name != "" {
		t.Fatalf("nodes = %+v", plan.Nodes)
	}
	if len(plan.Switches) != 2 || plan.Switches[0].Name != "SET-vSwitch0" || !plan.Switches[0].ManagementOS || plan.Switches[1].Name != "SET-vSwitch1" || plan.Switches[1].MTU != 9000 || plan.Switches[1].ManagementOS {
		t.Fatalf("switches = %+v (vSwitch2 carries only iSCSI and must not become a SET switch)", plan.Switches)
	}
	if len(plan.HostNICs) != 2 || plan.HostNICs[0].Name != "LiveMigration" || plan.HostNICs[0].Switch != "SET-vSwitch1" || plan.HostNICs[0].VLAN != "30" || plan.HostNICs[1].Role != "cluster" {
		t.Fatalf("host adapters = %+v", plan.HostNICs)
	}
	if len(plan.Networks) != 2 || plan.Networks[1].Portgroup != "Servers-20" || plan.Networks[1].Switch != "SET-vSwitch0" || plan.Networks[1].VLAN != "20" || plan.Networks[0].VLAN != "" {
		t.Fatalf("networks = %+v", plan.Networks)
	}
	if plan.Storage.Kind != "iscsi" || !plan.Storage.MPIO || !plan.Storage.Jumbo || strings.Join(plan.Storage.Portals, ",") != "10.0.40.5:3260,10.0.41.5:3260" {
		t.Fatalf("storage = %+v", plan.Storage)
	}
	if len(plan.Sizing) != 2 || plan.Sizing[1].Datastore != "SAN-01" || plan.Sizing[1].Label != "CSV-SAN-01" || plan.Sizing[1].SuggestedBytes != 3840<<30 || plan.Sizing[0].Note == "" {
		t.Fatalf("sizing = %+v (Other is not on these hosts; SAN-01 holds 3 TiB, +25%% is 3.75 TiB)", plan.Sizing)
	}
	if !plan.Cluster.Enabled || plan.Cluster.Witness != "disk" || plan.Name != "Prod" {
		t.Fatalf("cluster = %+v name %q", plan.Cluster, plan.Name)
	}
	notes := strings.Join(plan.Notes, " ")
	for _, want := range []string{"intel-broadwell", "VM load balancing", "192.0.2.10", "initiator name"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes do not mention %q: %s", want, notes)
		}
	}
	if _, err := hvBuildSuggest(hvBuildTestInventory(), "Nope"); err == nil {
		t.Fatal("a cluster that does not exist was planned")
	}
	if _, err := hvBuildSuggest(nil, ""); err == nil {
		t.Fatal("a plan was made without an inventory")
	}
}

func TestHVBuildCheckPlanRefusesBadValues(t *testing.T) {
	good := hvBuildTestPlan(t)
	cases := map[string]func(*hvBuildPlan){
		"address":          func(p *hvBuildPlan) { p.Nodes[0].HostIPs["LiveMigration"] = "10.0.30.21" },
		"network address":  func(p *hvBuildPlan) { p.Nodes[0].HostIPs["LiveMigration"] = "10.0.30.0/24" },
		"adapter twice":    func(p *hvBuildPlan) { p.Nodes[0].Switches["SET-vSwitch1"] = []string{"SLOT 1 Port 1"} },
		"adapter storage":  func(p *hvBuildPlan) { p.Nodes[1].Storage[0].Adapter = "SLOT 2 Port 1" },
		"adapter quote":    func(p *hvBuildPlan) { p.Nodes[0].Switches["SET-vSwitch0"] = []string{"NIC'1"} },
		"node name":        func(p *hvBuildPlan) { p.Nodes[0].Name = "HV1;calc" },
		"node twice":       func(p *hvBuildPlan) { p.Nodes[1].Name = "hv1" },
		"cluster name":     func(p *hvBuildPlan) { p.Cluster.Name = "A-very-long-cluster-name" },
		"cluster digits":   func(p *hvBuildPlan) { p.Cluster.Name = "12345" },
		"cluster address":  func(p *hvBuildPlan) { p.Cluster.Addresses = []string{"192.0.2.300"} },
		"share":            func(p *hvBuildPlan) { p.Cluster.WitnessShare = `C:\Witness` },
		"vlan":             func(p *hvBuildPlan) { p.HostNICs[0].VLAN = "5000" },
		"portal":           func(p *hvBuildPlan) { p.Storage.Portals = []string{"10.0.40.5:99999"} },
		"disk twice":       func(p *hvBuildPlan) { p.Storage.Disks[1].UniqueID = p.Storage.Disks[0].UniqueID },
		"label":            func(p *hvBuildPlan) { p.Storage.Disks[0].Label = "CSV'01" },
		"two witnesses":    func(p *hvBuildPlan) { p.Storage.Disks[0].Use = "witness" },
		"folder":           func(p *hvBuildPlan) { p.Settings.VMPath = `C:\Windows\VMs` },
		"switch twice":     func(p *hvBuildPlan) { p.Switches[1].Name = p.Switches[0].Name },
		"nic on no switch": func(p *hvBuildPlan) { p.HostNICs[0].Switch = "Missing" },
		"filter":           func(p *hvBuildPlan) { p.Storage.TargetFilter = "a' + (calc)" },
	}
	for name, change := range cases {
		plan := good
		plan.Nodes = append([]hvBuildNode(nil), good.Nodes...)
		for index := range plan.Nodes {
			node := &plan.Nodes[index]
			switches := map[string][]string{}
			for key, value := range node.Switches {
				switches[key] = append([]string(nil), value...)
			}
			node.Switches = switches
			ips := map[string]string{}
			for key, value := range node.HostIPs {
				ips[key] = value
			}
			node.HostIPs = ips
			node.Storage = append([]hvBuildNodeNIC(nil), node.Storage...)
		}
		plan.HostNICs = append([]hvBuildHostNIC(nil), good.HostNICs...)
		plan.Switches = append([]hvBuildSwitch(nil), good.Switches...)
		plan.Storage.Disks = append([]hvBuildDisk(nil), good.Storage.Disks...)
		change(&plan)
		if _, err := hvBuildCheckPlan(plan); err == nil {
			t.Errorf("%s: a bad value was accepted", name)
		}
	}
}

func TestHVBuildStepsRunInOrderAndSayWhatIsMissing(t *testing.T) {
	plan := hvBuildTestPlan(t)
	ids := []string{}
	for _, step := range hvBuildSteps(plan) {
		ids = append(ids, step.ID)
		if step.Missing != "" {
			t.Fatalf("%s is missing %q in a complete plan", step.ID, step.Missing)
		}
	}
	if strings.Join(ids, ",") != "localtools,roles,restart,network,storage,disks,validate,cluster,csv,quorum,clusternetworks,hostsettings" {
		t.Fatalf("steps = %v", ids)
	}
	draft, _ := hvBuildSuggest(hvBuildTestInventory(), "Prod")
	draft.Name = "Draft"
	draft, err := hvBuildCheckPlan(draft)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range hvBuildSteps(draft) {
		if step.ID == "roles" && !strings.Contains(step.Missing, "node 1") {
			t.Fatalf("roles step without node names: %q", step.Missing)
		}
		if _, err := hvBuildScript(draft, step.ID, ""); step.Missing != "" && err == nil {
			t.Fatalf("%s produced a script while it is missing %q", step.ID, step.Missing)
		}
	}
	// One standalone host: no cluster steps, disks become volumes.
	single := hvBuildTestPlan(t)
	single.Nodes = single.Nodes[:1]
	single.Cluster.Enabled = false
	single, err = hvBuildCheckPlan(single)
	if err != nil {
		t.Fatal(err)
	}
	ids = ids[:0]
	for _, step := range hvBuildSteps(single) {
		ids = append(ids, step.ID)
	}
	if strings.Join(ids, ",") != "roles,restart,network,storage,disks,hostsettings" || single.Storage.Disks[0].Use != "volume" {
		t.Fatalf("standalone steps = %v, disk use %q", ids, single.Storage.Disks[0].Use)
	}
	script, err := hvBuildScript(single, "hostsettings", single.Nodes[0].Name)
	if err != nil || !strings.Contains(script, "Add-VMMigrationNetwork") || !strings.Contains(script, "'10.0.30.0/24'") {
		t.Fatalf("a standalone host's live migration network is not set: %v\n%s", err, script)
	}
}

func TestHVBuildScriptsQuoteValuesAndNeverRemoveAnything(t *testing.T) {
	plan := hvBuildTestPlan(t)
	dump := os.Getenv("SIMM_HVBUILD_DUMP")
	for _, step := range hvBuildSteps(plan) {
		for _, computer := range hvBuildStepComputers(plan, step) {
			script, err := hvBuildScript(plan, step.ID, computer)
			if err != nil {
				t.Fatalf("%s on %q: %v", step.ID, computer, err)
			}
			if !strings.HasPrefix(script, "param([bool]$Apply") || !strings.Contains(script, "$hash -ne $Approved") || !strings.HasSuffix(strings.TrimSpace(script), "-Depth 5 -Compress") {
				t.Fatalf("%s is missing the approval check", step.ID)
			}
			if strings.Contains(script, "`") {
				t.Fatalf("%s contains a backtick", step.ID)
			}
			for _, forbidden := range []string{"Remove-VMSwitch", "Remove-NetIPAddress", "Clear-Disk", "Remove-Partition", "Remove-Cluster", "Remove-VMNetworkAdapter", "Remove-IscsiTargetPortal", "Disconnect-IscsiTarget", "Invoke-Expression", "-EncodedCommand", "Start-Process", "Register-ScheduledTask", "New-Service", "CredSSP -Role"} {
				if strings.Contains(script, forbidden) {
					t.Fatalf("%s contains %s", step.ID, forbidden)
				}
			}
			if dump != "" {
				name := step.ID
				if computer != "" {
					name += "-" + strings.SplitN(computer, ".", 2)[0]
				}
				if err := os.WriteFile(filepath.Join(dump, name+".ps1"), []byte(script), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	network, _ := hvBuildScript(plan, "network", "HV2.example.com")
	for _, want := range []string{"'SET-vSwitch0'", "@('SLOT 1 Port 1', 'SLOT 1 Port 2')", "-EnableEmbeddedTeaming $true", "New-NetLbfoTeam", "'10.0.30.22'", "-VlanId 30", "*JumboPacket"} {
		if !strings.Contains(network, want) {
			t.Fatalf("the network script for HV2 lacks %s", want)
		}
	}
	if strings.Contains(network, "10.0.30.21") {
		t.Fatal("HV2's network script holds HV1's address")
	}
	storage, _ := hvBuildScript(plan, "storage", "HV1.example.com")
	for _, want := range []string{"address = '10.0.40.5'; port = 3260", "'10.0.40.21'", "Enable-MSDSMAutomaticClaim -BusType iSCSI", "IsMultipathEnabled"} {
		if !strings.Contains(storage, want) {
			t.Fatalf("the storage script lacks %s", want)
		}
	}
	disks, _ := hvBuildScript(plan, "disks", "HV1.example.com")
	if !strings.Contains(disks, "already holds partitions and is never changed") || !strings.Contains(disks, "IsBoot -or $disk.IsSystem") {
		t.Fatal("the disk script does not protect disks with data")
	}
	cluster, _ := hvBuildScript(plan, "cluster", "")
	if !strings.Contains(cluster, "-NoStorage") || !strings.Contains(cluster, "'HVCLU01'") || !strings.Contains(cluster, "@('192.0.2.30')") {
		t.Fatal("the cluster script is wrong")
	}
	// A value with every PowerShell quote character stays one literal.
	if quoted := vmPSQuote("a'b’c"); quoted != "'a''b’’c'" {
		t.Fatalf("quoted = %s", quoted)
	}
}

func TestHVBuildChangeHashMatchesTheScript(t *testing.T) {
	empty := sha256.Sum256(nil)
	if hvBuildChangeHash(nil) != hex.EncodeToString(empty[:]) {
		t.Fatal("the hash of no changes is wrong")
	}
	two := sha256.Sum256([]byte("Install A\nInstall B"))
	if hvBuildChangeHash([]string{"Install A", "Install B"}) != hex.EncodeToString(two[:]) {
		t.Fatal("changes are not joined with a line feed")
	}
	plan := hvBuildTestPlan(t)
	revision := hvBuildRevision(plan)
	plan.Nodes[0].HostIPs["Cluster"] = "10.0.31.99/24"
	if hvBuildRevision(plan) == revision {
		t.Fatal("a changed plan kept its revision, so an old approval would still count")
	}
}

func TestHVBuildWritesTheNetworkMapForMoveVMs(t *testing.T) {
	build := hvBuildTestPlan(t)
	inventory := hvBuildTestInventory()
	migration := defaultVMPlan(inventory)
	updated, changed := hvBuildApplyNetworkMap(build, migration)
	if changed != 2 {
		t.Fatalf("changed = %d", changed)
	}
	for _, network := range updated.Networks {
		if network.Source == "Servers-20" && (network.TargetName != "SET-vSwitch0" || network.TargetVLAN != "20") {
			t.Fatalf("Servers-20 maps to %+v", network)
		}
	}
	if _, again := hvBuildApplyNetworkMap(build, updated); again != 0 {
		t.Fatal("writing the same map again changed it")
	}
}
