package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// hvBuildTestCluster is what Read existing reports for a cluster built
// from the first converted host: HV-HOST01 with SET switch "SETswitch"
// (two 10 Gbps ports, jumbo), host adapters LM (VLAN 30) and CSV, iSCSI with
// MPIO through two portals, and the cluster HVCLU with one node.
func hvBuildTestCluster() hvBuildExistingRead {
	text := `{"computer":"HV-HOST01","dnsName":"HV-HOST01.example.com","build":20348,"domain":"example.com","partOfDomain":true,"hyperV":true,
	"switches":[{"name":"SETswitch","type":"External","embedded":true,"adapters":["NIC1","NIC2"],"speedsMb":[10000,10000],"loadBalancing":"Dynamic","managementOs":true,"jumbo":true},
	            {"name":"Lab","type":"Private","adapters":[],"speedsMb":null}],
	"hostNics":[{"name":"SETswitch","switch":"SETswitch","vlan":"","ips":["192.0.2.31/24"],"gateway":true},
	            {"name":"LM","switch":"SETswitch","vlan":"30","ips":"10.0.30.31/24","gateway":false},
	            {"name":"CSV","switch":"SETswitch","vlan":"31","ips":["10.0.31.31/24"],"gateway":false}],
	"storage":{"initiator":"iqn.1991-05.com.microsoft:hv-host01","portals":["10.0.40.5:3260","10.0.41.5:3260"],"targets":["iqn.array:lun"],"mpio":true,"fc":false,"arrays":[],"adapters":[{"adapter":"NIC5","ip":"10.0.40.31/24"}]},
	"host":{"migrationEnabled":true,"auth":"Kerberos","max":4,"maxStorage":2,"performance":"SMB","vmPath":"C:\\ClusterStorage\\Volume1\\VMs","vhdPath":"C:\\ClusterStorage\\Volume1\\VMs","migrationNetworks":[]},
	"cluster":"HVCLU","warnings":[]}`
	var template hvBuildTemplate
	if err := json.Unmarshal([]byte(text), &template); err != nil {
		panic(err)
	}
	cluster := &hvBuildClusterFacts{Name: "HVCLU", Nodes: []hvBuildMember{{Name: "HV-HOST01", State: "Up"}}, Addresses: hvStrings{"192.0.2.40"}, WitnessType: "File Share Witness", Witness: "File Share Witness", WitnessPath: `\\fs01\witness`,
		Networks: []hvBuildClusterNetwork{
			{Name: "Management", Address: "192.0.2.0", Mask: "255.255.255.0", Role: 3},
			{Name: "LM", Address: "10.0.30.0", Mask: "255.255.255.0", Role: 1, Migration: true},
			{Name: "CSV", Address: "10.0.31.0", Mask: "255.255.255.0", Role: 1},
			{Name: "iSCSI", Address: "10.0.40.0", Mask: "255.255.255.0", Role: 0},
		},
		CSVs: []hvBuildClusterCSV{{Name: "Cluster Disk 1", Path: `C:\ClusterStorage\Volume1`, SizeBytes: 2 << 40, FreeBytes: 1 << 40}}}
	return hvBuildExistingRead{Kind: "join", Names: []string{"HVCLU"}, ReadFrom: "HV-HOST01", Template: &template, Cluster: cluster}
}

func TestHVBuildJoinsAnExistingClusterAndMatchesItsSetup(t *testing.T) {
	inventory := vmRollingTestInventory()
	inventory.Hosts[0].ConnectionState = "connected"
	inventory.Hosts[1].ConnectionState = "connected"
	drafted, err := hvBuildSuggestFor(inventory, "Prod", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	read := hvBuildTestCluster()
	// The first ESXi host was converted to HV-HOST01: rename one drafted
	// node after it, so the plan must leave it out.
	drafted.Nodes[2].Name = "hv-host01"
	plan, err := hvBuildAdopt(&drafted, read)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Cluster.Join || !plan.Cluster.Enabled || plan.Cluster.Name != "HVCLU" || strings.Join(plan.Cluster.Addresses, ",") != "192.0.2.40" {
		t.Fatalf("cluster = %+v", plan.Cluster)
	}
	if len(plan.Nodes) != 2 {
		t.Fatalf("the converted host must be left out: %+v", plan.Nodes)
	}
	for _, node := range plan.Nodes {
		if !node.Later {
			t.Fatalf("an ESXi host vSphere still reaches must wait: %+v", node)
		}
		if _, ok := node.Switches["SETswitch"]; !ok || len(node.Switches) != 1 {
			t.Fatalf("node switches must be the cluster's: %+v", node.Switches)
		}
		if len(node.SourceNICs["SETswitch"]) == 0 {
			t.Fatalf("the ESXi adapters must follow the switch they map to: %+v", node.SourceNICs)
		}
	}
	if len(plan.Switches) != 1 || plan.Switches[0].Name != "SETswitch" || plan.Switches[0].MTU != 9000 || plan.Switches[0].LoadBalancing != "Dynamic" || len(plan.Switches[0].UplinkSpeedsMb) != 2 || len(plan.Switches[0].From) != 2 {
		t.Fatalf("switches = %+v (the private switch is not built; both VMware switches map to the only external one)", plan.Switches)
	}
	if len(plan.HostNICs) != 2 || plan.HostNICs[0].Name != "LM" || plan.HostNICs[0].Role != "livemigration" || plan.HostNICs[0].VLAN != "30" || plan.HostNICs[1].Name != "CSV" || plan.HostNICs[1].Role != "cluster" {
		t.Fatalf("host adapters = %+v (the management adapter is not one)", plan.HostNICs)
	}
	if plan.Nodes[0].HostIPs["LM"] == "" {
		t.Fatalf("the vMotion address must move to the cluster's live migration adapter: %+v", plan.Nodes[0].HostIPs)
	}
	if plan.Storage.Kind != "iscsi" || !plan.Storage.MPIO || len(plan.Storage.Portals) != 2 || len(plan.Storage.Disks) != 0 {
		t.Fatalf("storage = %+v", plan.Storage)
	}
	if plan.Settings.MaxMigrations != 4 || plan.Settings.Performance != "SMB" || plan.Settings.VMPath != `C:\ClusterStorage\Volume1\VMs` {
		t.Fatalf("settings = %+v", plan.Settings)
	}
	for _, network := range plan.Networks {
		if network.Switch != "SETswitch" {
			t.Fatalf("VM network %s goes to %s", network.Portgroup, network.Switch)
		}
	}
	if plan.Existing == nil || len(plan.Existing.Members) != 1 || plan.Existing.Witness != `File share witness \\fs01\witness` || plan.Existing.HostIPs["LM"] != "10.0.30.31/24" {
		t.Fatalf("existing = %+v", plan.Existing)
	}
	notes := strings.Join(plan.Notes, " ")
	if !strings.Contains(notes, "already a node of cluster HVCLU") || !strings.Contains(notes, "private switch Lab") {
		t.Fatalf("notes = %s", notes)
	}
	checked, err := hvBuildCheckPlan(plan)
	if err != nil {
		t.Fatal(err)
	}

	// While every node waits, no step can run.
	for _, step := range hvBuildSteps(checked) {
		if step.Missing == "" && step.Where != hvWhereHere {
			t.Fatalf("step %s can run with every node waiting", step.ID)
		}
	}
	// Mark the first ready: the cluster is joined, never created, and the
	// quorum and cluster networks are left alone.
	checked.Nodes[0].Name = "HV-HOST02.example.com"
	checked.Nodes[0].Later = false
	checked.Nodes[0].Switches["SETswitch"] = []string{"NIC1", "NIC2"}
	checked.Nodes[0].HostIPs["CSV"] = "10.0.31.32/24"
	checked, err = hvBuildCheckPlan(checked)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, step := range hvBuildSteps(checked) {
		ids = append(ids, step.ID)
		if step.Missing != "" {
			t.Fatalf("step %s: %s", step.ID, step.Missing)
		}
		for _, computer := range hvBuildStepComputers(checked, step) {
			if strings.Contains(strings.ToLower(computer), "esx") {
				t.Fatalf("step %s runs on the waiting node %s", step.ID, computer)
			}
			if _, err := hvBuildScript(checked, step.ID, computer); err != nil {
				t.Fatalf("%s: %v", step.ID, err)
			}
		}
	}
	if strings.Join(ids, ",") != "localtools,roles,restart,network,storage,validate,cluster,hostsettings" {
		t.Fatalf("steps = %v", ids)
	}
	cluster, _ := hvBuildScript(checked, "cluster", "")
	if !strings.Contains(cluster, "Nothing is created") || !strings.Contains(cluster, "@('HV-HOST02.example.com')") || !strings.Contains(cluster, "elseif ($true)") {
		t.Fatalf("the join script must refuse to create a cluster:\n%s", cluster)
	}
	validate, _ := hvBuildScript(checked, "validate", "")
	if !strings.Contains(validate, "@('HV-HOST01', 'HV-HOST02.example.com')") || !strings.Contains(validate, "$withStorage = $false") {
		t.Fatalf("validation must include the members and leave out the Storage tests:\n%s", validate)
	}
	network, _ := hvBuildScript(checked, "network", "HV-HOST02.example.com")
	if !strings.Contains(network, "'SETswitch'") || !strings.Contains(network, "'10.0.30.") || !strings.Contains(network, "-VlanId 30") {
		t.Fatalf("the network script does not match the cluster:\n%s", network)
	}
	// A join without a cluster name is refused.
	broken := checked
	broken.Cluster.Name = ""
	if _, err := hvBuildCheckPlan(broken); err == nil {
		t.Fatal("a join without a cluster name was accepted")
	}
}

func TestHVBuildAdoptRefusesTheWrongKind(t *testing.T) {
	read := hvBuildTestCluster()
	read.Kind = "takein"
	if _, err := hvBuildAdopt(nil, read); err == nil || !strings.Contains(err.Error(), "Add hosts to an existing") {
		t.Fatalf("taking in a clustered host: %v", err)
	}
	read = hvBuildTestCluster()
	read.Cluster = nil
	read.Template.Cluster = ""
	if _, err := hvBuildAdopt(nil, read); err == nil || !strings.Contains(err.Error(), "not in a failover cluster") {
		t.Fatalf("joining a standalone host: %v", err)
	}
	read = hvBuildTestCluster()
	read.Template.HyperV = false
	if _, err := hvBuildAdopt(nil, read); err == nil {
		t.Fatal("a computer without Hyper-V was matched")
	}
}

func TestHVBuildTakesInStandaloneHostsForANewCluster(t *testing.T) {
	read := hvBuildTestCluster()
	read.Kind = "takein"
	read.Cluster = nil
	read.Template.Cluster = ""
	read.Names = []string{"HV-HOST01.example.com", "HV-HOST09.example.com"}
	drafted, err := hvBuildSuggest(hvBuildTestInventory(), "Prod")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := hvBuildAdopt(&drafted, read)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cluster.Join || !plan.Cluster.Enabled || len(plan.Nodes) != 4 {
		t.Fatalf("cluster %+v nodes %+v", plan.Cluster, plan.Nodes)
	}
	host := plan.Nodes[0]
	if host.Name != "HV-HOST01.example.com" || !host.ExistingHost || strings.Join(host.Switches["SETswitch"], ",") != "NIC1,NIC2" || host.HostIPs["LM"] != "10.0.30.31/24" || len(host.Storage) != 1 || host.Storage[0].Adapter != "NIC5" {
		t.Fatalf("the existing host must keep its own setup: %+v", host)
	}
	if !plan.Nodes[1].ExistingHost || plan.Nodes[1].Name != "HV-HOST09.example.com" {
		t.Fatalf("the second host typed is a node: %+v", plan.Nodes[1])
	}
	checked, err := hvBuildCheckPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, step := range hvBuildSteps(checked) {
		ids = append(ids, step.ID)
	}
	if !strings.Contains(strings.Join(ids, ","), "quorum,clusternetworks") {
		t.Fatalf("a new cluster keeps its quorum and network steps: %v", ids)
	}
	// Without a VMware inventory the host alone makes the plan.
	alone, err := hvBuildAdopt(nil, read)
	if err != nil || len(alone.Nodes) != 2 || alone.Name == "" || len(alone.Switches) != 1 {
		t.Fatalf("plan from the host alone: %+v %v", alone, err)
	}
}

func TestHVBuildCreatesAOneNodeClusterWhileOthersWait(t *testing.T) {
	plan := hvBuildTestPlan(t)
	plan.Nodes[1].Later = true
	checked, err := hvBuildCheckPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := hvBuildStepByID(checked, "cluster")
	if !ok || step.Missing != "" || !strings.Contains(step.Detail, "still waiting join it") {
		t.Fatalf("cluster step = %+v", step)
	}
	script, _ := hvBuildScript(checked, "cluster", "")
	if strings.Contains(script, "HV2.example.com") {
		t.Fatal("the waiting node is in the cluster script")
	}
	if computers := hvBuildStepComputers(checked, hvBuildStepInfo{Where: hvWhereEach}); len(computers) != 1 || computers[0] != "HV1.example.com" {
		t.Fatalf("computers = %v", computers)
	}
}

func TestHVBuildDraftsKeepUplinkSpeedsAndISCSIAddresses(t *testing.T) {
	inventory := vmRollingTestInventory()
	plan, err := hvBuildSuggestFor(inventory, "Prod", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Switches[0].UplinkSpeedsMb) == 0 {
		t.Fatalf("uplink speeds missing: %+v", plan.Switches[0])
	}
	checked, err := hvBuildCheckPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(checked.Switches[0].UplinkSpeedsMb) == 0 {
		t.Fatal("checking the plan dropped the uplink speeds")
	}
	checked.Nodes[0].Suggested = []string{"switch:SET-vSwitch0", "switch:SET-vSwitch0", "bad\x00key"}
	again, err := hvBuildCheckPlan(checked)
	if err != nil || strings.Join(again.Nodes[0].Suggested, ",") != "switch:SET-vSwitch0" {
		t.Fatalf("suggested keys = %v %v", again.Nodes[0].Suggested, err)
	}
}
