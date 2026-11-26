package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// vmRollingTestInventory is three ESXi hosts of 256 GB and 32 cores on a
// shared SAN datastore, with 18 VMs of 16 GB spread over them, a vCenter
// appliance and a VM on a datastore only esx03 has.
func vmRollingTestInventory() *vmInventory {
	inventory := hvBuildTestInventory()
	inventory.Hosts = nil
	for index := 1; index <= 3; index++ {
		name := fmt.Sprintf("esx0%d.example.com", index)
		host := vmHost{Name: name, Datacenter: "DC1", Cluster: "Prod", Cores: 32, MemoryBytes: 256 << 30, Details: &vmHostDetails{
			ManagementIP: fmt.Sprintf("192.0.2.2%d", index), DefaultGateway: "192.0.2.1",
			Switches: []vmHostSwitch{
				{Name: "vSwitch0", Kind: "Standard", MTU: 1500, Uplinks: []string{"vmnic0", "vmnic1"}, Portgroups: []string{"Management Network", "VM Network", "Servers-20"}},
				{Name: "vSwitch1", Kind: "Standard", MTU: 9000, Uplinks: []string{"vmnic2", "vmnic3"}, Portgroups: []string{"vMotion"}},
			},
			PhysicalNICs: []vmHostNIC{
				{Device: "vmnic0", MAC: fmt.Sprintf("00:50:56:aa:0%d:00", index), SpeedMb: 10000, Switch: "vSwitch0"},
				{Device: "vmnic1", MAC: fmt.Sprintf("00:50:56:aa:0%d:01", index), SpeedMb: 10000, Switch: "vSwitch0"},
				{Device: "vmnic2", MAC: fmt.Sprintf("00:50:56:aa:0%d:02", index), SpeedMb: 25000, Switch: "vSwitch1"},
				{Device: "vmnic3", MAC: fmt.Sprintf("00:50:56:aa:0%d:03", index), SpeedMb: 25000, Switch: "vSwitch1"},
			},
			VMkernel: []vmHostVMkernel{
				{Device: "vmk0", Portgroup: "Management Network", IP: fmt.Sprintf("192.0.2.2%d", index), Mask: "255.255.255.0", Services: []string{"management"}},
				{Device: "vmk1", Portgroup: "vMotion", IP: fmt.Sprintf("10.0.30.1%d", index), Mask: "255.255.255.0", Services: []string{"vmotion"}, VLAN: "30"},
			},
		}}
		inventory.Hosts = append(inventory.Hosts, host)
	}
	inventory.Datastores = []vmDatastore{
		{Name: "SAN-01", Datacenter: "DC1", Type: "VMFS", CapacityBytes: 8 << 40, FreeBytes: 4 << 40, Shared: true, Hosts: []string{"esx01.example.com", "esx02.example.com", "esx03.example.com"}},
		{Name: "esx03-local", Datacenter: "DC1", Type: "VMFS", CapacityBytes: 1 << 40, FreeBytes: 1 << 39, Hosts: []string{"esx03.example.com"}},
		{Name: "NFS-ISO", Datacenter: "DC1", Type: "NFS", CapacityBytes: 1 << 40, FreeBytes: 1 << 40, Shared: true, Hosts: []string{"esx01.example.com"}},
	}
	inventory.VMs = nil
	for index := 0; index < 18; index++ {
		host := fmt.Sprintf("esx0%d.example.com", index%3+1)
		vm := vmMachine{Name: fmt.Sprintf("app%02d", index), ID: fmt.Sprintf("vm-%d", index), Host: host, Cluster: "Prod", PowerState: "poweredOn", CPUs: 4, MemoryMB: 16 << 10, UsedBytes: 100 << 30, Disks: []vmDisk{{Label: "Hard disk 1", Datastore: "SAN-01"}}}
		if index == 2 {
			vm.Disks[0].Datastore = "esx03-local"
		}
		inventory.VMs = append(inventory.VMs, vm)
	}
	inventory.VMs = append(inventory.VMs, vmMachine{Name: "vcenter", ID: "vm-vc", Host: "esx01.example.com", PowerState: "poweredOn", GuestOS: "Other 3.x Linux (Photon)", CPUs: 2, MemoryMB: 14 << 10})
	return inventory
}

func vmRollingTestPlan(t *testing.T) (vmPlan, *vmInventory) {
	t.Helper()
	inventory := vmRollingTestInventory()
	plan := defaultVMPlan(inventory)
	plan.Scenario = vmScenarioRolling
	saved, err := validateVMPlan(plan, inventory)
	if err != nil {
		t.Fatal(err)
	}
	return saved, inventory
}

func TestRollingScenarioFillsTheClusterFromTheSourceHosts(t *testing.T) {
	plan, inventory := vmRollingTestPlan(t)
	if plan.Rolling == nil || !plan.Rolling.AutoCapacity || plan.Rolling.SourceCluster != "Prod" {
		t.Fatalf("rolling section not started: %+v", plan.Rolling)
	}
	capacity := plan.Capacity
	if capacity.Nodes != 3 || capacity.CoresPerNode != 32 || capacity.MemoryGBPerNode != 256 || capacity.ReserveNodes != 1 {
		t.Fatalf("capacity not filled from the hosts: %+v", capacity)
	}
	if capacity.UsableStorageTB != 8 {
		t.Fatalf("usable storage should be the reused SAN LUN (8 TB): %v", capacity.UsableStorageTB)
	}
	sizing := sizePlan(plan, inventory)
	if !sizing.Configured || !strings.Contains(strings.Join(sizing.Notes, " "), "Filled from the 3 source hosts") {
		t.Fatalf("sizing should say where the size came from: %+v", sizing)
	}
	// Typing a value turns the automatic size off and keeps the value.
	plan.Rolling.AutoCapacity = false
	plan.Capacity.MemoryGBPerNode = 384
	saved, err := validateVMPlan(plan, inventory)
	if err != nil || saved.Capacity.MemoryGBPerNode != 384 {
		t.Fatalf("typed capacity was not kept: %v %+v", err, saved.Capacity)
	}
	// A new-hardware plan is left as the operator typed it.
	plan.Scenario = vmScenarioNew
	plan.Rolling.AutoCapacity = true
	plan.Capacity.Nodes = 5
	saved, _ = validateVMPlan(plan, inventory)
	if saved.Capacity.Nodes != 5 {
		t.Fatalf("new hardware must not be resized: %+v", saved.Capacity)
	}
}

func TestRollingStorageIsFlaggedForReuse(t *testing.T) {
	plan, inventory := vmRollingTestPlan(t)
	storage := vmStorageReuseFor(plan, inventory, vmRollingHosts(inventory, "Prod"))
	kinds := map[string]vmStorageReuse{}
	for _, item := range storage {
		kinds[item.Datastore] = item
	}
	if !kinds["SAN-01"].Reuse || kinds["SAN-01"].Kind != "san" {
		t.Fatalf("shared VMFS should be reused: %+v", kinds["SAN-01"])
	}
	if kinds["esx03-local"].Reuse || kinds["esx03-local"].LocalTo != "esx03.example.com" {
		t.Fatalf("local VMFS should be flagged as erased with its host: %+v", kinds["esx03-local"])
	}
	if kinds["NFS-ISO"].Reuse || kinds["NFS-ISO"].Kind != "nfs" {
		t.Fatalf("NFS is not reusable for Hyper-V: %+v", kinds["NFS-ISO"])
	}
	off := false
	for index := range plan.Storage {
		if plan.Storage[index].Source == "SAN-01" {
			plan.Storage[index].Reuse = &off
		}
	}
	for _, item := range vmStorageReuseFor(plan, inventory, vmRollingHosts(inventory, "Prod")) {
		if item.Datastore == "SAN-01" && item.Reuse {
			t.Fatal("the operator's choice not to reuse a LUN was ignored")
		}
	}
}

func TestSuggestRollingStagesChecksEveryStage(t *testing.T) {
	plan, inventory := vmRollingTestPlan(t)
	// app02 is on esx03's local datastore; the operator keeps it for the
	// last wave.
	for index := range plan.VMs {
		if plan.VMs[index].Name == "app02" {
			plan.VMs[index].Wave = 3
		}
	}
	plan = suggestRollingStages(plan, inventory, true)
	stages := plan.Rolling.Stages
	if len(stages) != 3 {
		t.Fatalf("one stage per host expected: %+v", stages)
	}
	for index, stage := range stages {
		if stage.Wave != index+1 || stage.Host == "" {
			t.Fatalf("stage %d: %+v", index+1, stage)
		}
	}
	if !stages[2].MoveFirst || stages[0].MoveFirst {
		t.Fatal("only the last host's wave moves before its host is emptied")
	}
	for _, item := range plan.VMs {
		if item.ID == "vm-vc" && !item.Exclude {
			t.Fatal("the vCenter appliance must not be put in a wave")
		}
		if item.ID != "vm-vc" && item.Wave == 0 {
			t.Fatalf("%s was not placed in a stage", item.Name)
		}
	}
	report := analyzeRolling(plan, inventory)
	if report == nil || len(report.Stages) != 3 || len(report.Unstaged) != 0 {
		t.Fatalf("report: %+v", report)
	}
	first := report.Stages[0]
	if len(first.Target.Hosts) != 1 || first.Target.Status == "ok" {
		t.Fatalf("one target node cannot keep a node free: %+v", first.Target)
	}
	if len(first.Source.Hosts) != 2 {
		t.Fatalf("stage 1 leaves two source hosts: %+v", first.Source)
	}
	last := report.Stages[2]
	if len(last.Source.Hosts) != 0 || last.Source.VMs != 1 {
		t.Fatalf("after the last stage only the vCenter appliance is left on the source: %+v", last.Source)
	}
	if !strings.Contains(strings.Join(last.Notes, " "), "vcenter is the vCenter Server appliance") {
		t.Fatalf("the vCenter appliance needs a note: %v", last.Notes)
	}
	foundLocal := false
	for _, stage := range report.Stages {
		if stage.Host == "esx03.example.com" && len(stage.LocalVMs) == 1 && stage.LocalVMs[0] == "app02" {
			foundLocal = true
		}
	}
	if !foundLocal {
		t.Fatal("a VM on storage only its host has must be named in that host's stage")
	}
	if len(report.Stages[1].Steps) == 0 || !strings.Contains(strings.Join(report.Stages[1].Steps, " "), "Build Hyper-V hosts") {
		t.Fatalf("stage steps should name Build Hyper-V hosts: %v", report.Stages[1].Steps)
	}
	if !strings.Contains(strings.Join(report.Stages[1].Steps, " "), "Join it to the failover cluster") {
		t.Fatal("later nodes join the cluster")
	}
}

func TestRollingStagesAreEditableAndChecked(t *testing.T) {
	plan, inventory := vmRollingTestPlan(t)
	plan = suggestRollingStages(plan, inventory, true)
	// Swap the first two stages, add a move-only stage and move every VM
	// into wave 1: the first stage then cannot hold them.
	stages := plan.Rolling.Stages
	stages[0], stages[1] = stages[1], stages[0]
	plan.Rolling.Stages = append(stages, vmRollingStage{Wave: 4, Notes: "Stragglers"})
	for index := range plan.VMs {
		if !plan.VMs[index].Exclude {
			plan.VMs[index].Wave = 1
		}
	}
	saved, err := validateVMPlan(plan, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Rolling.Stages[3].Notes != "Stragglers" || saved.Rolling.Stages[0].Wave != 2 {
		t.Fatalf("edited stages were not kept: %+v", saved.Rolling.Stages)
	}
	report := analyzeRolling(saved, inventory)
	if report.Status != "over" {
		t.Fatalf("eighteen 16 GB VMs cannot fit on one 256 GB node: %+v", report.Stages)
	}
	if report.Stages[3].Host != "" || !report.Stages[3].MoveFirst {
		t.Fatal("a stage without a host only moves VMs")
	}
	// Refusals.
	bad := saved
	bad.Rolling = &vmRollingPlan{Stages: []vmRollingStage{{Host: "esx01.example.com"}, {Host: "esx01.example.com"}}}
	if _, err := validateVMPlan(bad, inventory); err == nil || !strings.Contains(err.Error(), "both rebuild") {
		t.Fatalf("a host rebuilt twice must be refused: %v", err)
	}
	bad.Rolling = &vmRollingPlan{Stages: []vmRollingStage{{Host: "esx99"}}}
	if _, err := validateVMPlan(bad, inventory); err == nil {
		t.Fatal("an unknown host must be refused")
	}
	bad.Rolling = &vmRollingPlan{SourceCluster: "Nope"}
	if _, err := validateVMPlan(bad, inventory); err == nil {
		t.Fatal("an unknown cluster must be refused")
	}
	bad.Rolling = nil
	bad.Scenario = "sideways"
	if _, err := validateVMPlan(bad, inventory); err == nil {
		t.Fatal("an unknown scenario must be refused")
	}
	// Keep waves: a VM placed by the operator stays where it is.
	plan, _ = vmRollingTestPlan(t)
	for index := range plan.VMs {
		if plan.VMs[index].Name == "app00" {
			plan.VMs[index].Wave = 3
		}
	}
	plan = suggestRollingStages(plan, inventory, true)
	if plan.entry("vm-0").Wave != 3 {
		t.Fatal("Suggest stages with Keep my waves moved a VM the operator placed")
	}
}

func TestRollingNeedsAnOnPremisesTarget(t *testing.T) {
	plan, inventory := vmRollingTestPlan(t)
	plan.Target = vmTargetAzure
	report := analyzeRolling(plan, inventory)
	if report.Status != "over" || !strings.Contains(strings.Join(report.Notes, " "), "New hardware") {
		t.Fatalf("cloud targets cannot reuse hosts: %+v", report.Notes)
	}
	plan.Target = vmTargetProxmox
	plan = suggestRollingStages(plan, inventory, false)
	report = analyzeRolling(plan, inventory)
	if !strings.Contains(strings.Join(report.Stages[0].Steps, " "), "Proxmox VE") {
		t.Fatalf("rebuild steps follow the target: %v", report.Stages[0].Steps)
	}
}

func TestRollingRunbookHasTheStages(t *testing.T) {
	plan, inventory := vmRollingTestPlan(t)
	plan = suggestRollingStages(plan, inventory, false)
	folder := t.TempDir()
	runbook, _, err := writeVMwarePlanOutputs(folder, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	page := readTestFile(t, runbook)
	for _, want := range []string{"Rolling conversion: 3 stages", "Stage 1: rebuild", "Capacity at this stage", "Storage when the hosts are rebuilt", `data-step="stage-0|`} {
		if !strings.Contains(page, want) {
			t.Fatalf("runbook lacks %q", want)
		}
	}
	docx := readDocxText(t, folder+"/"+vmRunbookDocx)
	if !strings.Contains(docx, "Rolling conversion: 3 stages") || !strings.Contains(docx, "Stage 3: rebuild") {
		t.Fatal("the Word runbook lacks the stages")
	}
}

func TestHVBuildReusesTheESXiHostsHardware(t *testing.T) {
	inventory := vmRollingTestInventory()
	plan, err := hvBuildSuggestFor(inventory, "Prod", true, []string{"esx03.example.com", "esx01.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Reuse || len(plan.Nodes) != 3 || plan.Nodes[0].ESXi != "esx03.example.com" || plan.Nodes[1].ESXi != "esx01.example.com" {
		t.Fatalf("nodes not in stage order: %+v", plan.Nodes)
	}
	node := plan.Nodes[0]
	if node.Name != "esx03" || node.ManagementIP != "192.0.2.23" {
		t.Fatalf("node not filled from its host: %+v", node)
	}
	if node.HostIPs["LiveMigration"] != "10.0.30.13/24" {
		t.Fatalf("live migration should keep the vMotion address: %v", node.HostIPs)
	}
	nics := node.SourceNICs["SET-vSwitch1"]
	if len(nics) != 2 || nics[0].MAC != "00-50-56-AA-03-02" {
		t.Fatalf("switch adapters should be listed by MAC: %+v", node.SourceNICs)
	}
	checked, err := hvBuildCheckPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(checked.Nodes[0].SourceNICs["SET-vSwitch1"]) != 2 {
		t.Fatal("checking the plan dropped the adapter hints")
	}
	if !strings.Contains(strings.Join(plan.Notes, " "), "management address 192.0.2.23") {
		t.Fatalf("notes should give the management address: %v", plan.Notes)
	}
	// Without reuse nothing is copied from the hosts.
	fresh, _ := hvBuildSuggest(inventory, "Prod")
	if fresh.Nodes[0].Name != "" || len(fresh.Nodes[0].HostIPs) != 0 || fresh.Reuse {
		t.Fatalf("new hardware must not take the ESXi hosts' names or addresses: %+v", fresh.Nodes[0])
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readDocxText(t *testing.T, path string) string {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			reader, _ := file.Open()
			body, _ := io.ReadAll(reader)
			reader.Close()
			return string(body)
		}
	}
	t.Fatal("no word/document.xml")
	return ""
}
