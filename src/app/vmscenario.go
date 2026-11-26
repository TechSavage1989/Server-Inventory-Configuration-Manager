package main

// Migration scenarios: how the move uses hardware. The scenario is chosen
// at the start of Plan and runbook and the rest of the Migration area reads
// it: New cluster size, Build Hyper-V hosts, Migration plans and the runbook.
//
//   - new: the target runs on other servers; the operator describes them.
//   - rolling: the source hosts themselves become the target, one host at a
//     time. Each stage empties one source host onto the others, takes it
//     out of the source platform, rebuilds it as a target node and moves a
//     wave of VMs onto the target nodes built so far. The new cluster's size
//     comes from the recorded hosts and every stage is checked on both sides
//     with N+1 warnings.
//
// Scenarios are generic: any inventory with hosts (vCenter, ESXi or a
// Hyper-V host or cluster) and any target that runs on the operator's own
// servers (vmKindCluster). Add a scenario to vmScenarios rather than with
// `if scenario ==` branches elsewhere.
//
// Everything here only plans; nothing is changed on any host. The file is
// untagged so it is tested on every platform.

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	vmScenarioNew     = "new"
	vmScenarioRolling = "rolling"

	vmRollingMaxStages = 64
)

type vmScenarioInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// When says when to pick it.
	When string `json:"when"`
	// Kinds are the target kinds it works with.
	Kinds []string `json:"kinds"`
}

var vmScenarios = []vmScenarioInfo{
	{ID: vmScenarioNew, Name: "New hardware", When: "The new platform runs on servers that are not running the source today. Describe them in New cluster size.", Kinds: []string{vmKindCluster, vmKindCloud, vmKindPhysical}},
	{ID: vmScenarioRolling, Name: "Reuse the existing hosts (rolling, one host at a time)", When: "The source hosts themselves become the new platform. Each stage empties one host onto the others, rebuilds it and moves a wave of VMs onto the hosts rebuilt so far. The new cluster's size is filled from the recorded hosts.", Kinds: []string{vmKindCluster}},
}

func vmScenarioFor(id string) (vmScenarioInfo, bool) {
	for _, item := range vmScenarios {
		if item.ID == id {
			return item, true
		}
	}
	return vmScenarioInfo{}, false
}

// vmPlanScenario is the plan's scenario, "new" when none was chosen (plans
// saved by 3.2.35 and earlier).
func vmPlanScenario(plan vmPlan) string {
	if plan.Scenario == vmScenarioRolling {
		return vmScenarioRolling
	}
	return vmScenarioNew
}

// vmRollingPlan is the operator's staged plan. Every field is editable;
// Suggest stages only fills it as a starting point.
type vmRollingPlan struct {
	// SourceCluster limits the plan to one source cluster ("" for every
	// host in the inventory).
	SourceCluster string `json:"sourceCluster"`
	// AutoCapacity keeps New cluster size filled from the source hosts.
	// Typing a value there turns it off.
	AutoCapacity bool             `json:"autoCapacity"`
	Stages       []vmRollingStage `json:"stages"`
}

// vmRollingStage is one step of the rolling conversion.
type vmRollingStage struct {
	// Host is the source host rebuilt in this stage; "" for a stage that
	// only moves VMs.
	Host string `json:"host"`
	// Wave is the wave of VMs moved in this stage (0 for none).
	Wave int `json:"wave"`
	// MoveFirst moves the wave onto the target nodes already built before
	// the host is emptied. The last host needs it: there is no source host
	// left to take its VMs.
	MoveFirst bool   `json:"moveFirst"`
	Notes     string `json:"notes"`
}

// vmRollingHost is a source host as the plan sees it.
type vmRollingHost struct {
	Name         string `json:"name"`
	Cluster      string `json:"cluster"`
	Cores        int    `json:"cores"`
	MemoryBytes  int64  `json:"memoryBytes"`
	VMs          int    `json:"vms"`
	VMMemory     int64  `json:"vmMemoryBytes"`
	NICs         int    `json:"nics"`
	ManagementIP string `json:"managementIp"`
	Model        string `json:"model"`
}

// vmStorageReuse says what happens to a datastore when its hosts are
// rebuilt and whether it can carry on as target storage.
type vmStorageReuse struct {
	Datastore     string `json:"datastore"`
	Type          string `json:"type"`
	CapacityBytes int64  `json:"capacityBytes"`
	UsedBytes     int64  `json:"usedBytes"`
	// Kind is san, local, nfs, vsan, vvol or other.
	Kind string `json:"kind"`
	// LocalTo is the only host a local datastore is on.
	LocalTo string `json:"localTo"`
	// Reusable is true when the LUN can become target storage once empty.
	Reusable bool   `json:"reusable"`
	Reuse    bool   `json:"reuse"`
	Advice   string `json:"advice"`
}

// vmStageCheck is the capacity check of one side at one point of a stage.
type vmStageCheck struct {
	// Status is ok, tight or over.
	Status      string   `json:"status"`
	Hosts       []string `json:"hosts"`
	VMs         int      `json:"vms"`
	MemoryBytes int64    `json:"memoryBytes"`
	Capacity    int64    `json:"capacityBytes"`
	VCPUs       int      `json:"vcpus"`
	Cores       int      `json:"cores"`
	Notes       []string `json:"notes"`
}

type vmRollingStageReport struct {
	Number    int      `json:"number"`
	Host      string   `json:"host"`
	Wave      int      `json:"wave"`
	MoveFirst bool     `json:"moveFirst"`
	WaveVMs   []string `json:"waveVms"`
	// Source is what the source hosts left must hold once this stage's
	// host is empty; Target is what the target nodes hold after the wave.
	Source vmStageCheck `json:"source"`
	Target vmStageCheck `json:"target"`
	// LocalVMs are VMs with disks on storage only the stage's host has.
	LocalVMs []string `json:"localVms"`
	Steps    []string `json:"steps"`
	Status   string   `json:"status"`
	Notes    []string `json:"notes"`
}

type vmRollingReport struct {
	Hosts   []vmRollingHost        `json:"hosts"`
	Storage []vmStorageReuse       `json:"storage"`
	Stages  []vmRollingStageReport `json:"stages"`
	Status  string                 `json:"status"`
	Notes   []string               `json:"notes"`
	// Unstaged are moving VMs in scope that no stage moves.
	Unstaged []string `json:"unstaged"`
	// Words names the source platform's parts for the page.
	Source string `json:"source"`
}

// vmRollingSourceWords are the source platform's words for the steps.
type vmRollingSourceWords struct {
	platform, host, manager, evacuate, remove string
}

func vmRollingWords(inventory *vmInventory) vmRollingSourceWords {
	if inventory != nil && inventory.ServerType == hypervServerType {
		return vmRollingSourceWords{
			platform: "Hyper-V", host: "Hyper-V host", manager: "the failover cluster",
			evacuate: "Drain the node (Failover Cluster Manager, Pause, Drain roles, or Suspend-ClusterNode -Drain) so its VMs live migrate to the other nodes, and move any VM that is not clustered by hand.",
			remove:   "Evict the node from the failover cluster and remove its storage connections.",
		}
	}
	return vmRollingSourceWords{
		platform: "VMware", host: "ESXi host", manager: "vCenter",
		evacuate: "Put the host in maintenance mode so DRS moves its running VMs to the other ESXi hosts with vMotion (with DRS off or partially automated, vMotion them by hand), and move or register powered-off VMs and templates elsewhere.",
		remove:   "Disconnect the host and remove it from vCenter (and from any distributed switch). Unpresent or unmount nothing the other hosts still use.",
	}
}

// vmRollingRebuildSteps describe rebuilding a host as a node of the target.
func vmRollingRebuildSteps(target, host string, nodes int) []string {
	switch target {
	case vmTargetHyperV:
		steps := []string{
			"Install Windows Server (2016 or later for Switch Embedded Teaming) on " + host + ", join it to the domain and give it the management address the plan lists for it.",
			"In Build Hyper-V hosts (Migration and replication), Read nodes, then Preview and Apply the steps for this node: roles, the switches on the same physical adapters (matched by MAC address), host adapters, storage and MPIO.",
		}
		if nodes <= 1 {
			steps = append(steps, "Create the failover cluster on this first node in Build Hyper-V hosts, or run it standalone until the next node is ready.")
		} else {
			steps = append(steps, "Join it to the failover cluster in Build Hyper-V hosts (Cluster) and add its Cluster Shared Volumes; run cluster validation again.")
		}
		return steps
	case vmTargetScale:
		return []string{"Reinstall " + host + " as a Scale Computing HyperCore node with Scale Computing's installer (HyperCore runs on supported hardware only; check with Scale first) and add it to the HyperCore cluster."}
	case vmTargetNutanix:
		return []string{"Image " + host + " with AHV using Nutanix Foundation (on a hardware model Nutanix supports) and add it to the Nutanix cluster (Expand Cluster in Prism)."}
	case vmTargetProxmox:
		return []string{"Install Proxmox VE on " + host + ", give it the same management address and join it to the Proxmox cluster (Datacenter, Cluster, Join)."}
	}
	profile, _ := vmTargetProfileFor(target)
	return []string{"Rebuild " + host + " as a " + vmNonEmpty(profile.Short, "target") + " node and add it to the new cluster."}
}

// vmRollingHosts are the source hosts in the plan's scope, by name.
func vmRollingHosts(inventory *vmInventory, cluster string) []vmHost {
	hosts := []vmHost{}
	if inventory == nil {
		return hosts
	}
	for _, host := range inventory.Hosts {
		if cluster == "" || strings.EqualFold(host.Cluster, cluster) {
			hosts = append(hosts, host)
		}
	}
	sort.Slice(hosts, func(i, j int) bool { return strings.ToLower(hosts[i].Name) < strings.ToLower(hosts[j].Name) })
	return hosts
}

func vmHostNamed(hosts []vmHost, name string) (vmHost, bool) {
	for _, host := range hosts {
		if strings.EqualFold(host.Name, name) {
			return host, true
		}
	}
	return vmHost{}, false
}

// vmRollingScope are the VMs on the plan's source hosts.
func vmRollingScope(inventory *vmInventory, hosts []vmHost) []vmMachine {
	vms := []vmMachine{}
	for _, vm := range inventory.VMs {
		if _, ok := vmHostNamed(hosts, vm.Host); ok {
			vms = append(vms, vm)
		}
	}
	return vms
}

func vmIsPoweredOn(vm vmMachine) bool {
	return strings.EqualFold(vm.PowerState, "poweredOn") || strings.EqualFold(vm.PowerState, "running")
}

// vmRollingMoving is true when the plan moves the VM (not a template, not
// excluded, not held).
func vmRollingMoving(plan vmPlan, vm vmMachine) bool {
	entry := plan.entry(vm.ID)
	return !vm.Template && !entry.Exclude && !entry.Hold
}

// vmCapacityFromHosts sizes the new cluster from the hosts it is built
// from: one node per host, the smallest host's cores and memory (the plan
// must hold with any node down) and the storage reused.
func vmCapacityFromHosts(current vmCapacity, hosts []vmHost, storage []vmStorageReuse) vmCapacity {
	capacity := current
	if len(hosts) == 0 {
		return capacity
	}
	capacity.Nodes = len(hosts)
	cores, memory := 0, int64(0)
	for index, host := range hosts {
		if index == 0 || host.Cores < cores {
			cores = host.Cores
		}
		if index == 0 || host.MemoryBytes < memory {
			memory = host.MemoryBytes
		}
	}
	capacity.CoresPerNode = cores
	capacity.MemoryGBPerNode = int(memory >> 30)
	reused := int64(0)
	for _, item := range storage {
		if item.Reuse {
			reused += item.CapacityBytes
		}
	}
	if reused > 0 {
		capacity.UsableStorageTB = math.Floor(float64(reused)/(1<<40)*10) / 10
	}
	if capacity.ReserveNodes == 0 && len(hosts) > 1 {
		capacity.ReserveNodes = 1
	}
	if capacity.ReserveNodes >= capacity.Nodes {
		capacity.ReserveNodes = capacity.Nodes - 1
	}
	if capacity.MaxVCPUPerCore == 0 {
		capacity.MaxVCPUPerCore = 4
	}
	return capacity
}

// vmStorageReuseFor classifies each datastore the plan's hosts use.
func vmStorageReuseFor(plan vmPlan, inventory *vmInventory, hosts []vmHost) []vmStorageReuse {
	names := []string{}
	for _, host := range hosts {
		names = append(names, host.Name)
	}
	result := []vmStorageReuse{}
	for _, datastore := range inventory.Datastores {
		if !hvBuildDatastoreUsed(datastore, names) {
			continue
		}
		used := datastore.CapacityBytes - datastore.FreeBytes
		if used < 0 {
			used = 0
		}
		item := vmStorageReuse{Datastore: datastore.Name, Type: datastore.Type, CapacityBytes: datastore.CapacityBytes, UsedBytes: used}
		kind := strings.ToLower(datastore.Type)
		switch {
		case kind == "vmfs" && (datastore.Shared || len(datastore.Hosts) > 1):
			item.Kind, item.Reusable = "san", true
			item.Advice = "Shared LUN: it can become target storage once it is empty. Move its VMs off first (Storage vMotion to another datastore, or migrate them), unmount and detach it on every host, then present it to the new nodes; it is formatted, so nothing on it survives."
		case kind == "vmfs":
			item.Kind = "local"
			if len(datastore.Hosts) == 1 {
				item.LocalTo = datastore.Hosts[0]
			}
			item.Advice = "Local to " + vmNonEmpty(item.LocalTo, "one host") + ": it is erased when that host is rebuilt. Move its VMs to shared storage (Storage vMotion) or migrate them before that stage."
		case strings.HasPrefix(kind, "nfs"):
			item.Kind = "nfs"
			item.Advice = "NFS stays where it is. Hyper-V cannot run VMs from NFS; if the NAS also serves SMB 3, a share there can hold VMs. KVM targets can mount it."
		case kind == "vsan":
			item.Kind = "vsan"
			item.Advice = "vSAN goes away as its hosts are rebuilt. Plan other storage for the target (SAN LUNs, or Storage Spaces Direct on the same disks, not set up here) before the first host leaves."
		case kind == "vvol":
			item.Kind = "vvol"
			item.Advice = "vVols cannot be presented as LUNs. Ask the array for LUNs for the target."
		default:
			item.Kind = "other"
			if datastore.Shared {
				item.Advice = "Shared storage: check with the array whether it can be presented to the new nodes."
			} else {
				item.Advice = "Check whether this storage survives the rebuild."
			}
		}
		item.Reuse = item.Reusable
		for _, mapped := range plan.Storage {
			if strings.EqualFold(mapped.Source, datastore.Name) && mapped.Reuse != nil {
				item.Reuse = *mapped.Reuse && item.Reusable
			}
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Datastore) < strings.ToLower(result[j].Datastore)
	})
	return result
}

// vmRollingHostList describes the hosts for the page and the runbook.
func vmRollingHostList(inventory *vmInventory, hosts []vmHost) []vmRollingHost {
	list := []vmRollingHost{}
	for _, host := range hosts {
		item := vmRollingHost{Name: host.Name, Cluster: host.Cluster, Cores: host.Cores, MemoryBytes: host.MemoryBytes, Model: strings.TrimSpace(host.Vendor + " " + host.Model)}
		if host.Details != nil {
			item.NICs = len(host.Details.PhysicalNICs)
			item.ManagementIP = host.Details.ManagementIP
		}
		for _, vm := range inventory.VMs {
			if strings.EqualFold(vm.Host, host.Name) && !vm.Template {
				item.VMs++
				if vmIsPoweredOn(vm) {
					item.VMMemory += vm.MemoryMB << 20
				}
			}
		}
		list = append(list, item)
	}
	return list
}

// checkRollingPlan validates the operator's stages against the inventory.
func checkRollingPlan(rolling *vmRollingPlan, inventory *vmInventory) error {
	if rolling == nil {
		return nil
	}
	if len(rolling.SourceCluster) > 256 || strings.ContainsAny(rolling.SourceCluster, "\x00\r\n") {
		return errors.New("The source cluster name is not valid.")
	}
	if rolling.SourceCluster != "" {
		found := false
		for _, host := range inventory.Hosts {
			if strings.EqualFold(host.Cluster, rolling.SourceCluster) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("The inventory has no hosts in cluster %s.", rolling.SourceCluster)
		}
	}
	if len(rolling.Stages) > vmRollingMaxStages {
		return fmt.Errorf("A rolling plan has at most %d stages.", vmRollingMaxStages)
	}
	hosts := vmRollingHosts(inventory, rolling.SourceCluster)
	seen := map[string]int{}
	for index, stage := range rolling.Stages {
		if stage.Host != "" {
			if _, ok := vmHostNamed(hosts, stage.Host); !ok {
				return fmt.Errorf("Stage %d rebuilds %s, which is not a host of this plan. Choose a host from the list.", index+1, stage.Host)
			}
			key := strings.ToLower(stage.Host)
			if previous, ok := seen[key]; ok {
				return fmt.Errorf("Stages %d and %d both rebuild %s. Each host is rebuilt once; use a stage with no host to move more VMs.", previous, index+1, stage.Host)
			}
			seen[key] = index + 1
		}
		if stage.Wave < 0 || stage.Wave > vmPlanMaxWaves {
			return fmt.Errorf("Stage %d: waves are numbered 1 to %d.", index+1, vmPlanMaxWaves)
		}
		if len(stage.Notes) > 500 || strings.ContainsRune(stage.Notes, 0) {
			return fmt.Errorf("Stage %d: notes are limited to 500 characters.", index+1)
		}
	}
	return nil
}

// applyScenario keeps the plan consistent with its scenario: a rolling
// plan always has its section, and with AutoCapacity its New cluster size
// follows the source hosts.
func applyScenario(plan vmPlan, inventory *vmInventory) vmPlan {
	if vmPlanScenario(plan) != vmScenarioRolling {
		return plan
	}
	if plan.Rolling == nil {
		plan.Rolling = &vmRollingPlan{AutoCapacity: true}
		if clusters := vmRollingClusters(inventory); len(clusters) == 1 {
			plan.Rolling.SourceCluster = clusters[0]
		}
	}
	if plan.Rolling.AutoCapacity {
		hosts := vmRollingHosts(inventory, plan.Rolling.SourceCluster)
		plan.Capacity = vmCapacityFromHosts(plan.Capacity, hosts, vmStorageReuseFor(plan, inventory, hosts))
	}
	return plan
}

// vmRollingClusters are the source clusters that have hosts.
func vmRollingClusters(inventory *vmInventory) []string {
	names := []string{}
	if inventory == nil {
		return names
	}
	for _, host := range inventory.Hosts {
		if host.Cluster != "" && !hvBuildContains(names, host.Cluster) {
			names = append(names, host.Cluster)
		}
	}
	sort.Strings(names)
	return names
}

type vmLoad struct {
	vms    int
	memory int64
	vcpus  int
	used   int64
}

func (load *vmLoad) add(vm vmMachine, running bool) {
	load.vms++
	if running {
		load.memory += vm.MemoryMB << 20
		load.vcpus += vm.CPUs
	}
	load.used += vm.UsedBytes
}

// vmSideCheck compares a load with a set of hosts, keeping reserve hosts
// free when there are enough of them (N+1).
func vmSideCheck(hosts []vmHost, load vmLoad, capacity vmCapacity, reserve int, side string) vmStageCheck {
	check := vmStageCheck{Status: "ok", Hosts: []string{}, Notes: []string{}, VMs: load.vms, MemoryBytes: load.memory, VCPUs: load.vcpus}
	for _, host := range hosts {
		check.Hosts = append(check.Hosts, host.Name)
	}
	raise := func(level string) {
		if level == "over" || check.Status == "ok" {
			check.Status = level
		}
	}
	reservedMemory := int64(capacity.ReservedMemoryGB) << 30
	ratio := capacity.MaxVCPUPerCore
	if ratio <= 0 {
		ratio = 4
	}
	sum := func(list []vmHost) (int64, int) {
		memory, cores := int64(0), 0
		for _, host := range list {
			if free := host.MemoryBytes - reservedMemory; free > 0 {
				memory += free
			}
			cores += host.Cores
		}
		return memory, cores
	}
	if len(hosts) == 0 {
		if load.vms > 0 {
			raise("over")
			check.Notes = append(check.Notes, fmt.Sprintf("No %s host is left, but %s would still be on it.", side, vmPluralWord(load.vms, "VM")))
		}
		return check
	}
	all, allCores := sum(hosts)
	// Hosts with the most memory are the ones a failure costs most.
	sorted := append([]vmHost{}, hosts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].MemoryBytes > sorted[j].MemoryBytes })
	keep := reserve
	if keep > len(hosts)-1 {
		keep = len(hosts) - 1
	}
	if keep < 0 {
		keep = 0
	}
	usable, usableCores := sum(sorted[keep:])
	check.Capacity, check.Cores = usable, usableCores
	if load.vms == 0 {
		check.Notes = append(check.Notes, "Nothing runs here at this point.")
		return check
	}
	if hostsMissing := hostsWithoutSize(hosts); hostsMissing != "" {
		raise("tight")
		check.Notes = append(check.Notes, "The inventory has no memory or cores for "+hostsMissing+"; read it again to check this stage.")
	}
	switch {
	case load.memory > all:
		raise("over")
		check.Notes = append(check.Notes, fmt.Sprintf("The running VMs need %s of memory; %s %s have %s after the host reserve.", vmFormatBytes(load.memory), vmPluralWord(len(hosts), side+" host"), map[bool]string{true: "has", false: "have"}[len(hosts) == 1], vmFormatBytes(all)))
	case keep < reserve && reserve > 0:
		raise("tight")
		check.Notes = append(check.Notes, fmt.Sprintf("Only %s: no host is free for a failure (N+1) at this point.", vmPluralWord(len(hosts), side+" host")))
	case load.memory > usable:
		raise("tight")
		check.Notes = append(check.Notes, fmt.Sprintf("The VMs fit, but not with %s down (N+1): they need %s and %s is left then.", vmPluralWord(keep, "host"), vmFormatBytes(load.memory), vmFormatBytes(usable)))
	case float64(load.memory) > float64(usable)*0.9:
		raise("tight")
		check.Notes = append(check.Notes, fmt.Sprintf("Memory is %.0f%% used with %s kept free.", float64(load.memory)/float64(usable)*100, vmPluralWord(keep, "host")))
	}
	if allCores > 0 {
		perCore := float64(load.vcpus) / float64(allCores)
		if usableCores > 0 {
			perCore = float64(load.vcpus) / float64(usableCores)
		}
		if perCore > ratio {
			raise("tight")
			check.Notes = append(check.Notes, fmt.Sprintf("%.1f vCPUs per core is more than your limit of %.1f.", perCore, ratio))
		}
	}
	if check.Status == "ok" {
		check.Notes = append(check.Notes, fmt.Sprintf("Fits: %s of %s memory in use%s.", vmFormatBytes(load.memory), vmFormatBytes(usable), map[bool]string{true: " with a host kept free (N+1)", false: ""}[keep > 0]))
	}
	return check
}

func hostsWithoutSize(hosts []vmHost) string {
	names := []string{}
	for _, host := range hosts {
		if host.MemoryBytes <= 0 || host.Cores <= 0 {
			names = append(names, host.Name)
		}
	}
	return strings.Join(names, ", ")
}

func vmPluralWord(count int, word string) string {
	if count == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", count, word)
}

func vmWorse(a, b string) string {
	rank := map[string]int{"ok": 0, "tight": 1, "over": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// analyzeRolling walks the stages in order and checks both sides at every
// stage: the source hosts left must hold what has not moved yet once the
// stage's host is empty, and the target nodes built so far must hold
// everything moved.
func analyzeRolling(plan vmPlan, inventory *vmInventory) *vmRollingReport {
	if inventory == nil || vmPlanScenario(plan) != vmScenarioRolling || plan.Rolling == nil {
		return nil
	}
	words := vmRollingWords(inventory)
	hosts := vmRollingHosts(inventory, plan.Rolling.SourceCluster)
	report := &vmRollingReport{Hosts: vmRollingHostList(inventory, hosts), Storage: vmStorageReuseFor(plan, inventory, hosts), Stages: []vmRollingStageReport{}, Status: "ok", Notes: []string{}, Unstaged: []string{}, Source: words.platform}
	if len(hosts) == 0 {
		report.Status = "over"
		report.Notes = append(report.Notes, "The inventory has no hosts to reuse. Read vCenter, ESXi or the Hyper-V cluster again in Discovery.")
		return report
	}
	profile, _ := vmTargetProfileFor(plan.Target)
	if profile.Kind != vmKindCluster {
		report.Status = "over"
		report.Notes = append(report.Notes, profile.Short+" does not run on your own servers, so hosts cannot be reused for it. Choose New hardware, or a target such as Hyper-V, HyperCore, Nutanix AHV or Proxmox VE.")
		return report
	}
	scope := vmRollingScope(inventory, hosts)
	onSource := map[string]bool{}
	for _, vm := range scope {
		if !vm.Template {
			onSource[vm.ID] = true
		}
	}
	waveVMs := func(wave int) []vmMachine {
		list := []vmMachine{}
		if wave <= 0 {
			return list
		}
		for _, vm := range scope {
			if onSource[vm.ID] && vmRollingMoving(plan, vm) && plan.entry(vm.ID).Wave == wave {
				list = append(list, vm)
			}
		}
		return list
	}
	sourceHosts := append([]vmHost{}, hosts...)
	targetNodes := []vmHost{}
	moved := vmLoad{}
	removeHost := func(list []vmHost, name string) []vmHost {
		out := []vmHost{}
		for _, host := range list {
			if !strings.EqualFold(host.Name, name) {
				out = append(out, host)
			}
		}
		return out
	}
	sourceLoad := func() vmLoad {
		load := vmLoad{}
		for _, vm := range scope {
			if onSource[vm.ID] {
				load.add(vm, vmIsPoweredOn(vm))
			}
		}
		return load
	}
	reserve := plan.Capacity.ReserveNodes
	for index, stage := range plan.Rolling.Stages {
		item := vmRollingStageReport{Number: index + 1, Host: stage.Host, Wave: stage.Wave, MoveFirst: stage.MoveFirst || stage.Host == "", WaveVMs: []string{}, LocalVMs: []string{}, Steps: []string{}, Status: "ok", Notes: []string{}}
		wave := waveVMs(stage.Wave)
		moveWave := func() {
			for _, vm := range wave {
				item.WaveVMs = append(item.WaveVMs, vm.Name)
				delete(onSource, vm.ID)
				moved.add(vm, true)
			}
			item.Target = vmSideCheck(targetNodes, moved, plan.Capacity, reserve, "target")
			if len(targetNodes) == 0 && len(wave) > 0 {
				item.Target.Status = "over"
				item.Target.Notes = []string{"No target node is built yet when this wave moves. Rebuild a host in an earlier stage, or untick Move first."}
			}
		}
		target := vmTargetLabel(plan.Target)
		waveText := fmt.Sprintf("Move wave %d (%s) to %s with Move VMs or a Migration plan, and cut each one over.", stage.Wave, vmPluralWord(len(wave), "VM"), target)
		if stage.Wave <= 0 {
			waveText = ""
		} else if len(wave) == 0 {
			waveText = fmt.Sprintf("Wave %d has no VMs left on these hosts.", stage.Wave)
		}
		if item.MoveFirst {
			if waveText != "" {
				item.Steps = append(item.Steps, waveText)
			}
			moveWave()
		}
		if stage.Host != "" {
			host, _ := vmHostNamed(hosts, stage.Host)
			if !hvBuildContains(hostNames(sourceHosts), host.Name) {
				item.Notes = append(item.Notes, host.Name+" was already rebuilt.")
			}
			sourceHosts = removeHost(sourceHosts, host.Name)
			item.Source = vmSideCheck(sourceHosts, sourceLoad(), plan.Capacity, 1, strings.ToLower(words.platform))
			for _, vm := range scope {
				if !onSource[vm.ID] || !strings.EqualFold(vm.Host, host.Name) {
					continue
				}
				for _, disk := range vm.Disks {
					if vmDatastoreLocalTo(inventory, disk.Datastore, host.Name) {
						item.LocalVMs = append(item.LocalVMs, vm.Name)
						break
					}
				}
				if role := vmInfrastructureRole(vm); role != "" && len(sourceHosts) == 0 {
					item.Notes = append(item.Notes, vm.Name+" is "+role+"; it has no host left after this stage. Retire it once nothing needs it, or move it in an earlier stage.")
				}
			}
			if len(item.LocalVMs) > 0 {
				item.Source.Status = vmWorse(item.Source.Status, "tight")
				item.Source.Notes = append(item.Source.Notes, "On storage only "+host.Name+" has: "+strings.Join(item.LocalVMs, ", ")+". Storage vMotion them to shared storage, or move them in this stage's wave first.")
			}
			item.Steps = append(item.Steps, fmt.Sprintf("Check the %s hosts left (%s) can take %s's VMs.", words.platform, vmNonEmpty(strings.Join(hostNames(sourceHosts), ", "), "none"), host.Name))
			item.Steps = append(item.Steps, words.evacuate)
			item.Steps = append(item.Steps, words.remove)
			targetNodes = append(targetNodes, host)
			item.Steps = append(item.Steps, vmRollingRebuildSteps(plan.Target, host.Name, len(targetNodes))...)
			for _, store := range report.Storage {
				if store.Reuse && store.Kind == "san" && index == 0 {
					item.Steps = append(item.Steps, "Present the shared LUNs that are already empty to "+host.Name+" (the first target storage). "+store.Datastore+" is reused once its VMs have moved off.")
					break
				}
			}
		}
		if !item.MoveFirst {
			if waveText != "" {
				item.Steps = append(item.Steps, waveText)
			}
			moveWave()
		}
		if stage.Host == "" {
			item.Source = vmSideCheck(sourceHosts, sourceLoad(), plan.Capacity, 1, strings.ToLower(words.platform))
		}
		if stage.Notes != "" {
			item.Notes = append(item.Notes, stage.Notes)
		}
		item.Status = vmWorse(item.Source.Status, item.Target.Status)
		report.Status = vmWorse(report.Status, item.Status)
		report.Stages = append(report.Stages, item)
	}
	for _, vm := range scope {
		if onSource[vm.ID] && vmRollingMoving(plan, vm) {
			report.Unstaged = append(report.Unstaged, vm.Name)
		}
	}
	rebuilt := len(hosts) - len(sourceHosts)
	switch {
	case len(plan.Rolling.Stages) == 0:
		report.Status = "over"
		report.Notes = append(report.Notes, "No stages yet. Suggest stages drafts one per host; change it as you like.")
	case len(report.Unstaged) > 0:
		report.Status = vmWorse(report.Status, "tight")
		report.Notes = append(report.Notes, fmt.Sprintf("%s in no stage's wave: %s. Give them a wave a stage moves.", vmPluralWord(len(report.Unstaged), "VM is"), strings.Join(report.Unstaged, ", ")))
	}
	if rebuilt < len(hosts) && len(plan.Rolling.Stages) > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("%s not rebuilt by any stage: %s.", vmPluralWord(len(sourceHosts), "host is"), strings.Join(hostNames(sourceHosts), ", ")))
	}
	reusable := false
	for _, store := range report.Storage {
		if store.Reuse {
			reusable = true
		}
		if store.Kind == "vsan" {
			report.Notes = append(report.Notes, "vSAN datastore "+store.Datastore+" shrinks with every host that leaves. Move its VMs to other storage before the first stage.")
		}
	}
	if reusable {
		report.Notes = append(report.Notes, "Reused LUNs are formatted for the target, so the first target node needs storage that is already empty: a new LUN, or a datastore emptied with Storage vMotion before stage 1.")
	}
	if report.Status == "ok" {
		report.Notes = append(report.Notes, "Every stage fits on both sides.")
	}
	return report
}

func hostNames(hosts []vmHost) []string {
	names := []string{}
	for _, host := range hosts {
		names = append(names, host.Name)
	}
	return names
}

// vmDatastoreLocalTo is true when only host has the datastore.
func vmDatastoreLocalTo(inventory *vmInventory, name, host string) bool {
	for _, datastore := range inventory.Datastores {
		if !strings.EqualFold(datastore.Name, name) {
			continue
		}
		if len(datastore.Hosts) == 1 {
			return strings.EqualFold(datastore.Hosts[0], host)
		}
		return false
	}
	return false
}

// suggestRollingStages drafts one stage per host: the host whose VMs are
// easiest to place goes first, and each stage moves a wave that fits on
// the target nodes built so far (keeping a node free once there are two)
// while leaving the source hosts able to take the next host's VMs. The last
// host's stage moves its wave first. With keepWaves the operator's waves
// are kept and only VMs without one are placed. Every result is editable.
func suggestRollingStages(plan vmPlan, inventory *vmInventory, keepWaves bool) vmPlan {
	plan = applyScenario(plan, inventory)
	if plan.Rolling == nil {
		return plan
	}
	hosts := vmRollingHosts(inventory, plan.Rolling.SourceCluster)
	if len(hosts) == 0 {
		return plan
	}
	list := vmRollingHostList(inventory, hosts)
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].VMMemory != list[j].VMMemory {
			return list[i].VMMemory < list[j].VMMemory
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	order := []vmHost{}
	for _, item := range list {
		host, _ := vmHostNamed(hosts, item.Name)
		order = append(order, host)
	}
	scope := vmRollingScope(inventory, hosts)
	inScope := map[string]vmMachine{}
	for _, vm := range scope {
		inScope[vm.ID] = vm
	}
	for index := range plan.VMs {
		item := &plan.VMs[index]
		vm, ok := inScope[item.ID]
		if !ok || item.Exclude {
			continue
		}
		if vm.Template || vmInfrastructureRole(vm) != "" {
			item.Exclude = true
			item.Wave = 0
			continue
		}
		if readiness := vm.readinessFor(plan.Target); readiness != nil && readiness.Status == vmStatusBlocked && item.Wave == 0 {
			item.Hold = true
		}
		if !keepWaves && !item.Hold {
			item.Wave = 0
		}
	}
	// VMs still to place: powered-off VMs first (low risk), then smaller
	// VMs, so early stages move as many as fit.
	pending := []vmMachine{}
	for _, vm := range scope {
		entry := plan.entry(vm.ID)
		if entry.Wave == 0 && vmRollingMoving(plan, vm) && vmInfrastructureRole(vm) == "" {
			pending = append(pending, vm)
		}
	}
	sort.SliceStable(pending, func(i, j int) bool {
		a, b := vmIsPoweredOn(pending[i]), vmIsPoweredOn(pending[j])
		if a != b {
			return !a
		}
		if pending[i].MemoryMB != pending[j].MemoryMB {
			return pending[i].MemoryMB < pending[j].MemoryMB
		}
		return strings.ToLower(pending[i].Name) < strings.ToLower(pending[j].Name)
	})
	reservedMemory := int64(plan.Capacity.ReservedMemoryGB) << 30
	usedByWave := map[int]int64{}
	for _, vm := range scope {
		entry := plan.entry(vm.ID)
		if entry.Wave > 0 && vmRollingMoving(plan, vm) {
			usedByWave[entry.Wave] += vm.MemoryMB << 20
		}
	}
	setWave := func(id string, wave int) {
		for index := range plan.VMs {
			if plan.VMs[index].ID == id {
				plan.VMs[index].Wave = wave
			}
		}
	}
	stages := []vmRollingStage{}
	movedMemory := int64(0)
	for index, host := range order {
		number := index + 1
		last := index == len(order)-1
		nodes := order[:number]
		if last {
			nodes = order[:index]
		}
		budget := int64(0)
		sorted := append([]vmHost{}, nodes...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].MemoryBytes > sorted[j].MemoryBytes })
		keep := plan.Capacity.ReserveNodes
		if keep > len(sorted)-1 {
			keep = len(sorted) - 1
		}
		if keep < 0 {
			keep = 0
		}
		for _, node := range sorted[keep:] {
			if free := node.MemoryBytes - reservedMemory; free > 0 {
				budget += free
			}
		}
		if keep == 0 {
			budget = budget * 85 / 100
		}
		movedMemory += usedByWave[number]
		remaining := pending[:0:0]
		for _, vm := range pending {
			need := vm.MemoryMB << 20
			if last || movedMemory+need <= budget {
				setWave(vm.ID, number)
				movedMemory += need
				continue
			}
			remaining = append(remaining, vm)
		}
		pending = remaining
		stages = append(stages, vmRollingStage{Host: host.Name, Wave: number, MoveFirst: last && len(order) > 1})
	}
	if len(order) == 1 {
		stages[0].MoveFirst = false
	}
	plan.Rolling.Stages = stages
	return plan
}

// vmRollingRunbook is the staged plan for the runbook: per stage, its
// steps and checks.
type vmRollingRunbookStage struct {
	Title  string
	Status string
	Steps  []string
	Checks []string
	VMs    string
}

func vmRollingRunbookStages(report *vmRollingReport) []vmRollingRunbookStage {
	if report == nil {
		return nil
	}
	result := []vmRollingRunbookStage{}
	for _, stage := range report.Stages {
		title := fmt.Sprintf("Stage %d", stage.Number)
		if stage.Host != "" {
			title += ": rebuild " + stage.Host
		}
		if stage.Wave > 0 {
			title += fmt.Sprintf(", move wave %d", stage.Wave)
		}
		checks := []string{}
		if stage.Host != "" || len(stage.Source.Notes) > 0 {
			for _, note := range stage.Source.Notes {
				checks = append(checks, "Source: "+note)
			}
		}
		for _, note := range stage.Target.Notes {
			checks = append(checks, "Target: "+note)
		}
		checks = append(checks, stage.Notes...)
		result = append(result, vmRollingRunbookStage{Title: title, Status: stage.Status, Steps: stage.Steps, Checks: checks, VMs: strings.Join(stage.WaveVMs, ", ")})
	}
	return result
}
