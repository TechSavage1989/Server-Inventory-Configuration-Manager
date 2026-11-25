package main

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The migration plan: where each VMware network and datastore goes on the
// target, whether the target has room, and which wave each VM moves in.
// It is saved per vCenter/ESXi server so planning can continue later.

type vmNetworkMap struct {
	Source     string `json:"source"`
	SourceVLAN string `json:"sourceVlan"`
	TargetName string `json:"targetName"`
	TargetVLAN string `json:"targetVlan"`
}

type vmStorageMap struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type vmCapacity struct {
	Nodes            int     `json:"nodes"`
	CoresPerNode     int     `json:"coresPerNode"`
	MemoryGBPerNode  int     `json:"memoryGbPerNode"`
	UsableStorageTB  float64 `json:"usableStorageTb"`
	ReserveNodes     int     `json:"reserveNodes"`
	MaxVCPUPerCore   float64 `json:"maxVcpuPerCore"`
	ReservedMemoryGB int     `json:"reservedMemoryGbPerNode"`
}

type vmPlanVM struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Wave    int    `json:"wave"`
	Hold    bool   `json:"hold"`
	Exclude bool   `json:"exclude"`
	Notes   string `json:"notes"`
}

type vmPlan struct {
	Schema    int            `json:"schema"`
	Server    string         `json:"server"`
	Target    string         `json:"target"`
	Networks  []vmNetworkMap `json:"networks"`
	Storage   []vmStorageMap `json:"storage"`
	Capacity  vmCapacity     `json:"capacity"`
	VMs       []vmPlanVM     `json:"vms"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

type vmSizing struct {
	VMs                int      `json:"vms"`
	VCPUs              int      `json:"vcpus"`
	MemoryBytes        int64    `json:"memoryBytes"`
	ProvisionedBytes   int64    `json:"provisionedBytes"`
	UsedBytes          int64    `json:"usedBytes"`
	UsableNodes        int      `json:"usableNodes"`
	Cores              int      `json:"cores"`
	VCPUPerCore        float64  `json:"vcpuPerCore"`
	MemoryPercent      float64  `json:"memoryPercent"`
	StoragePercent     float64  `json:"storagePercent"`
	ProvisionedPercent float64  `json:"provisionedPercent"`
	Status             string   `json:"status"`
	Notes              []string `json:"notes"`
	Configured         bool     `json:"configured"`
}

const (
	vmPlanMaxWaves = 99
)

var vmVLANPattern = regexp.MustCompile(`^(|[0-9]{1,4})$`)

func defaultVMPlan(inventory *vmInventory) vmPlan {
	plan := vmPlan{Schema: 1, Server: inventory.Server, Target: vmTargetHyperV, Capacity: vmCapacity{Nodes: 3, ReserveNodes: 1, MaxVCPUPerCore: 4, ReservedMemoryGB: 8}}
	return reconcileVMPlan(plan, inventory)
}

// reconcileVMPlan keeps the operator's choices for networks, datastores and
// VMs that still exist, adds new ones with defaults and drops the rest.
func reconcileVMPlan(plan vmPlan, inventory *vmInventory) vmPlan {
	plan.Schema = 1
	plan.Server = inventory.Server
	if !vmIsTarget(plan.Target) {
		plan.Target = vmTargetHyperV
	}
	networks := map[string]vmNetworkMap{}
	for _, item := range plan.Networks {
		networks[strings.ToLower(item.Source)] = item
	}
	plan.Networks = []vmNetworkMap{}
	seen := map[string]bool{}
	for _, network := range inventory.Networks {
		key := strings.ToLower(network.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		item, ok := networks[key]
		if !ok {
			item = vmNetworkMap{Source: network.Name, TargetName: network.Name}
			if vmVLANPattern.MatchString(network.VLAN) && network.VLAN != "" {
				item.TargetVLAN = network.VLAN
			}
		}
		item.Source, item.SourceVLAN = network.Name, network.VLAN
		plan.Networks = append(plan.Networks, item)
	}
	storage := map[string]vmStorageMap{}
	for _, item := range plan.Storage {
		storage[strings.ToLower(item.Source)] = item
	}
	plan.Storage = []vmStorageMap{}
	seen = map[string]bool{}
	for _, datastore := range inventory.Datastores {
		key := strings.ToLower(datastore.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		item, ok := storage[key]
		if !ok {
			item = vmStorageMap{Source: datastore.Name}
		}
		item.Source = datastore.Name
		plan.Storage = append(plan.Storage, item)
	}
	vms := map[string]vmPlanVM{}
	for _, item := range plan.VMs {
		vms[item.ID] = item
	}
	plan.VMs = []vmPlanVM{}
	for _, vm := range inventory.VMs {
		item, ok := vms[vm.ID]
		if !ok {
			item = vmPlanVM{ID: vm.ID, Exclude: vm.Template || vmInfrastructureRole(vm) != ""}
		}
		item.Name = vm.Name
		plan.VMs = append(plan.VMs, item)
	}
	return plan
}

// validateVMPlan checks an edited plan against the inventory.
func validateVMPlan(input vmPlan, inventory *vmInventory) (vmPlan, error) {
	if inventory == nil {
		return vmPlan{}, errors.New("Read a VMware inventory first.")
	}
	if !vmIsTarget(input.Target) {
		return vmPlan{}, errors.New("Choose a migration target from the list.")
	}
	for _, item := range input.Networks {
		if len(item.TargetName) > 128 || strings.ContainsAny(item.TargetName, "\x00\r\n") {
			return vmPlan{}, fmt.Errorf("The target network name for %s is not valid.", item.Source)
		}
		vlan := strings.TrimSpace(item.TargetVLAN)
		if !vmVLANPattern.MatchString(vlan) {
			return vmPlan{}, fmt.Errorf("The target VLAN for %s must be a number from 1 to 4094, or empty for none.", item.Source)
		}
		if vlan != "" {
			number := 0
			fmt.Sscanf(vlan, "%d", &number)
			if number < 1 || number > 4094 {
				return vmPlan{}, fmt.Errorf("The target VLAN for %s must be a number from 1 to 4094, or empty for none.", item.Source)
			}
		}
	}
	for _, item := range input.Storage {
		if len(item.Target) > 260 || strings.ContainsAny(item.Target, "\x00\r\n<>\"|?*") {
			return vmPlan{}, fmt.Errorf("The target storage for %s is not valid.", item.Source)
		}
	}
	capacity := input.Capacity
	if capacity.Nodes < 0 || capacity.Nodes > 64 || capacity.CoresPerNode < 0 || capacity.CoresPerNode > 1024 || capacity.MemoryGBPerNode < 0 || capacity.MemoryGBPerNode > 65536 || capacity.UsableStorageTB < 0 || capacity.UsableStorageTB > 100000 || math.IsNaN(capacity.UsableStorageTB) || capacity.ReserveNodes < 0 || capacity.ReserveNodes > 8 || capacity.MaxVCPUPerCore < 0 || capacity.MaxVCPUPerCore > 32 || capacity.ReservedMemoryGB < 0 || capacity.ReservedMemoryGB > 1024 {
		return vmPlan{}, errors.New("Check the target capacity: one or more values are out of range.")
	}
	known := map[string]bool{}
	for _, vm := range inventory.VMs {
		known[vm.ID] = true
	}
	for _, item := range input.VMs {
		if !known[item.ID] {
			return vmPlan{}, errors.New("The plan names a VM that is not in the inventory. Read the inventory again.")
		}
		if item.Wave < 0 || item.Wave > vmPlanMaxWaves {
			return vmPlan{}, fmt.Errorf("Waves are numbered 1 to %d.", vmPlanMaxWaves)
		}
		if len(item.Notes) > 500 || strings.ContainsRune(item.Notes, 0) {
			return vmPlan{}, errors.New("Notes are limited to 500 characters.")
		}
	}
	plan := reconcileVMPlan(input, inventory)
	plan.Capacity = capacity
	plan.UpdatedAt = time.Now()
	return plan, nil
}

// suggestWaves fills waves for VMs that have none: blocked VMs are held,
// templates excluded, powered-off VMs go first as low-risk tests and the
// rest follow in groups of the given size.
func suggestWaves(plan vmPlan, inventory *vmInventory, waveSize int) vmPlan {
	if waveSize <= 0 {
		waveSize = 10
	}
	byID := map[string]vmMachine{}
	for _, vm := range inventory.VMs {
		byID[vm.ID] = vm
	}
	nextWave, inWave := 2, 0
	for index := range plan.VMs {
		item := &plan.VMs[index]
		vm, ok := byID[item.ID]
		if !ok || item.Wave > 0 || item.Exclude || item.Hold {
			continue
		}
		if vm.Template || vmInfrastructureRole(vm) != "" {
			item.Exclude = true
			continue
		}
		if readiness := vm.readinessFor(plan.Target); readiness != nil && readiness.Status == vmStatusBlocked {
			item.Hold = true
			continue
		}
		if !strings.EqualFold(vm.PowerState, "poweredOn") {
			item.Wave = 1
			continue
		}
		item.Wave = nextWave
		inWave++
		if inWave >= waveSize {
			nextWave++
			inWave = 0
		}
		if nextWave > vmPlanMaxWaves {
			nextWave = vmPlanMaxWaves
		}
	}
	return plan
}

func (plan vmPlan) entry(id string) vmPlanVM {
	for _, item := range plan.VMs {
		if item.ID == id {
			return item
		}
	}
	return vmPlanVM{ID: id}
}

func (plan vmPlan) networkFor(source string) vmNetworkMap {
	for _, item := range plan.Networks {
		if strings.EqualFold(item.Source, source) {
			return item
		}
	}
	return vmNetworkMap{Source: source, TargetName: source}
}

func (plan vmPlan) storageFor(source string) string {
	for _, item := range plan.Storage {
		if strings.EqualFold(item.Source, source) {
			return item.Target
		}
	}
	return ""
}

// sizePlan totals the VMs that will move and compares them with the target
// capacity after the reserve nodes (N+1) are set aside.
func sizePlan(plan vmPlan, inventory *vmInventory) vmSizing {
	sizing := vmSizing{Status: "unknown", Notes: []string{}}
	for _, vm := range inventory.VMs {
		entry := plan.entry(vm.ID)
		if vm.Template || entry.Exclude {
			continue
		}
		sizing.VMs++
		sizing.VCPUs += vm.CPUs
		sizing.MemoryBytes += vm.MemoryMB << 20
		sizing.ProvisionedBytes += vm.ProvisionedBytes
		sizing.UsedBytes += vm.UsedBytes
	}
	profile, _ := vmTargetProfileFor(plan.Target)
	switch profile.Kind {
	case vmKindCloud:
		sizing.Status = "quota"
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("%s runs each VM as its own instance, so there is no cluster to fill. Check the subscription or account quota in the target region covers %d vCPUs, and budget for %s of disks (%s if every disk is fully provisioned).", profile.Short, sizing.VCPUs, vmFormatBytes(sizing.UsedBytes), vmFormatBytes(sizing.ProvisionedBytes)))
		sizing.Notes = append(sizing.Notes, "Pick each instance size from the VM's measured use; allocation in VMware is often larger than needed.")
		return sizing
	case vmKindPhysical:
		sizing.Status = "perserver"
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("Each VM moves to its own server. The runbook lists the cores, memory and disk each one needs; together they need %d cores and %s of memory.", sizing.VCPUs, vmFormatBytes(sizing.MemoryBytes)))
		return sizing
	}
	capacity := plan.Capacity
	sizing.UsableNodes = capacity.Nodes - capacity.ReserveNodes
	if capacity.Nodes <= 0 || capacity.CoresPerNode <= 0 || capacity.MemoryGBPerNode <= 0 || capacity.UsableStorageTB <= 0 {
		sizing.Notes = append(sizing.Notes, "Enter the target's nodes, cores, memory and usable storage to check whether the VMs fit.")
		return sizing
	}
	sizing.Configured = true
	if sizing.UsableNodes <= 0 {
		sizing.Status = "over"
		sizing.Notes = append(sizing.Notes, "Every node is held in reserve. Lower the reserve or add nodes.")
		return sizing
	}
	sizing.Cores = sizing.UsableNodes * capacity.CoresPerNode
	sizing.VCPUPerCore = round1(float64(sizing.VCPUs) / float64(sizing.Cores))
	memoryAvailable := float64(sizing.UsableNodes*(capacity.MemoryGBPerNode-capacity.ReservedMemoryGB)) * (1 << 30)
	if memoryAvailable > 0 {
		sizing.MemoryPercent = round1(float64(sizing.MemoryBytes) / memoryAvailable * 100)
	} else {
		sizing.MemoryPercent = 999
	}
	storageAvailable := capacity.UsableStorageTB * (1 << 40)
	sizing.StoragePercent = round1(float64(sizing.UsedBytes) / storageAvailable * 100)
	sizing.ProvisionedPercent = round1(float64(sizing.ProvisionedBytes) / storageAvailable * 100)

	status := "ok"
	raise := func(level string) {
		if level == "over" || status == "ok" {
			status = level
		}
	}
	maxRatio := capacity.MaxVCPUPerCore
	if maxRatio <= 0 {
		maxRatio = 4
	}
	switch {
	case sizing.VCPUPerCore > maxRatio:
		raise("over")
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("%.1f vCPUs per physical core with %d node(s) in reserve is more than your limit of %.1f.", sizing.VCPUPerCore, capacity.ReserveNodes, maxRatio))
	case sizing.VCPUPerCore > maxRatio*0.8:
		raise("tight")
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("%.1f vCPUs per physical core is close to your limit of %.1f.", sizing.VCPUPerCore, maxRatio))
	}
	switch {
	case sizing.MemoryPercent > 100:
		raise("over")
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("The VMs need %.0f%% of the memory left when %d node(s) are in reserve.", sizing.MemoryPercent, capacity.ReserveNodes))
	case sizing.MemoryPercent > 85:
		raise("tight")
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("The VMs use %.0f%% of the memory left when %d node(s) are in reserve, leaving little room to grow.", sizing.MemoryPercent, capacity.ReserveNodes))
	}
	switch {
	case sizing.StoragePercent > 100:
		raise("over")
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("The VMs' used storage is %.0f%% of the usable capacity.", sizing.StoragePercent))
	case sizing.StoragePercent > 80:
		raise("tight")
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("The VMs' used storage is %.0f%% of the usable capacity. Keep at least 20%% free.", sizing.StoragePercent))
	}
	if sizing.ProvisionedPercent > 100 && sizing.StoragePercent <= 100 {
		sizing.Notes = append(sizing.Notes, fmt.Sprintf("Fully provisioned disks would need %.0f%% of the usable capacity, so they must stay thin (dynamically expanding) on the target.", sizing.ProvisionedPercent))
	}
	if status == "ok" {
		sizing.Notes = append(sizing.Notes, "The selected VMs fit with the reserve you chose.")
	}
	sizing.Status = status
	return sizing
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

type vmRunbookVM struct {
	VM        vmMachine
	Entry     vmPlanVM
	Readiness vmReadiness
	Before    []string
	Cutover   []string
	Verify    []string
	Rollback  []string
	NICs      []string
	Disks     []string
}

type vmRunbookWave struct {
	Number int
	Label  string
	VMs    []vmRunbookVM
}

func vmHasFinding(readiness vmReadiness, code string) bool {
	for _, finding := range readiness.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

// buildRunbook turns the plan into per-wave, per-VM checklists that reflect
// each VM's own findings.
func buildRunbook(plan vmPlan, inventory *vmInventory) []vmRunbookWave {
	waves := map[int]*vmRunbookWave{}
	target := vmTargetLabel(plan.Target)
	profile, _ := vmTargetProfileFor(plan.Target)
	for _, vm := range inventory.VMs {
		entry := plan.entry(vm.ID)
		if entry.Exclude || vm.Template {
			continue
		}
		readiness := assessVM(vm, plan.Target, time.Now())
		if existing := vm.readinessFor(plan.Target); existing != nil {
			readiness = *existing
		}
		number := entry.Wave
		if entry.Hold {
			number = -1
		}
		wave := waves[number]
		if wave == nil {
			wave = &vmRunbookWave{Number: number}
			switch {
			case number == -1:
				wave.Label = "On hold (resolve blockers first)"
			case number == 0:
				wave.Label = "Not yet scheduled"
			default:
				wave.Label = fmt.Sprintf("Wave %d", number)
			}
			waves[number] = wave
		}
		item := vmRunbookVM{VM: vm, Entry: entry, Readiness: readiness}
		for _, nic := range vm.NICs {
			mapped := plan.networkFor(nic.Network)
			destination := vmNonEmpty(mapped.TargetName, "(choose a network)")
			if mapped.TargetVLAN != "" {
				destination += ", VLAN " + mapped.TargetVLAN
			}
			address := ""
			if len(nic.IPAddresses) > 0 {
				address = " [" + strings.Join(nic.IPAddresses, ", ")
				if nic.Static {
					address += ", static"
				}
				address += "]"
			}
			item.NICs = append(item.NICs, fmt.Sprintf("%s on %s → %s%s", nic.Label, vmNonEmpty(nic.Network, "no network"), destination, address))
		}
		for _, disk := range vm.Disks {
			destination := plan.storageFor(disk.Datastore)
			if destination == "" {
				destination = "(choose " + profile.StorageLabel + ")"
			}
			item.Disks = append(item.Disks, fmt.Sprintf("%s, %s %s on %s → %s", disk.Label, vmFormatBytes(disk.CapacityBytes), disk.Provisioning, vmNonEmpty(disk.Datastore, "unknown datastore"), destination))
		}

		for _, finding := range readiness.Findings {
			if finding.Severity == vmSeverityBlocker {
				item.Before = append(item.Before, "Resolve: "+finding.Message+" "+finding.Action)
			}
		}
		if vmHasFinding(readiness, "snapshots") || vmHasFinding(readiness, "consolidation") {
			item.Before = append(item.Before, "Delete or consolidate snapshots in vSphere.")
		}
		if vmHasFinding(readiness, "rdm-virtual") || vmHasFinding(readiness, "independent-disk") {
			item.Before = append(item.Before, "Convert raw device mappings and independent disks, or plan to copy their data separately.")
		}
		if vmHasFinding(readiness, "media") {
			item.Before = append(item.Before, "Disconnect CD/DVD media.")
		}
		if vmHasFinding(readiness, "virtio-drivers") || vmHasFinding(readiness, "aws-drivers") || vmHasFinding(readiness, "hardware-drivers") {
			item.Before = append(item.Before, "Install "+profile.WindowsDrivers+" in the VM, then check them with the guest pre-flight.")
		}
		if vmHasFinding(readiness, "firmware-bios") {
			item.Before = append(item.Before, "Confirm the server can boot this BIOS/MBR disk (legacy boot), or convert the boot disk to GPT first.")
		}
		if vmHasFinding(readiness, "linux-initramfs") {
			item.Before = append(item.Before, "Rebuild the initramfs with the target's storage and network drivers.")
		}
		if vmHasFinding(readiness, "vtpm") {
			item.Before = append(item.Before, "Save BitLocker recovery keys and suspend BitLocker.")
		}
		if classifyGuestOS(vm).Family == "windows" {
			item.Before = append(item.Before, "Inside the VMware VM, run Server Inventory and Migration Manager as administrator: a full collection, and Record settings under Inside the VM on the VMware tab.")
		} else if vmHasFinding(readiness, "static-ip") {
			item.Before = append(item.Before, "Record the guest's IP configuration (addresses, gateway, DNS) before cutover.")
		}
		item.Before = append(item.Before, "Confirm a recent, restorable backup of the VM.")
		if vmTargetUsesReceiver(plan.Target) {
			item.Before = append(item.Before, vmReceiverRunbookSteps(plan.Target, profile)...)
		} else if plan.Target == vmTargetHyperV {
			item.Before = append(item.Before, "On the Hyper-V host, run this application as administrator, read the inventory from vCenter or ESXi and choose Copy disks in the VM's details (Move to Hyper-V). Copy as often as needed while the VM runs; each copy after the first sends only changed blocks.")
		}

		item.Cutover = append(item.Cutover, "Tell users the service is going down, then stop the applications in the guest.")
		item.Cutover = append(item.Cutover, "Shut the VM down cleanly from the guest operating system.")
		item.Cutover = append(item.Cutover, fmt.Sprintf("Run the final conversion, replication or image restore to %s with %s.", target, profile.Tool))
		item.Cutover = append(item.Cutover, "Disconnect the old VM's network adapters in vSphere and set it not to start, so it cannot come back with the same addresses. Do not delete it yet.")
		boot := map[bool]string{true: "UEFI", false: "BIOS"}[strings.EqualFold(vm.Firmware, "efi")]
		size := fmt.Sprintf("%d vCPU, %s memory", vm.CPUs, vmFormatBytes(vm.MemoryMB<<20))
		switch plan.Target {
		case vmTargetHyperV:
			generation := 1
			if strings.EqualFold(vm.Firmware, "efi") {
				generation = 2
			}
			item.Cutover = append(item.Cutover, fmt.Sprintf("Create or check the Hyper-V VM: Generation %d, %s, network adapters as listed above.", generation, size))
		case vmTargetAzure:
			item.Cutover = append(item.Cutover, fmt.Sprintf("Check the Azure VM: Generation %d, a size with at least %s, the virtual network and subnet listed above, and a static private IP reservation if the address must stay.", map[bool]int{true: 2, false: 1}[boot == "UEFI"], size))
		case vmTargetAWS:
			item.Cutover = append(item.Cutover, fmt.Sprintf("Check the EC2 instance: %s boot mode, an instance type with at least %s, the subnet listed above, and a secondary private IP if the address must stay.", boot, size))
		case vmTargetPhysical:
			item.Cutover = append(item.Cutover, fmt.Sprintf("Prepare the server: %s boot, at least %d cores, %s memory and %s of disk, storage and network drivers ready, cabled to the networks listed above.", boot, vm.CPUs, vmFormatBytes(vm.MemoryMB<<20), vmFormatBytes(vm.ProvisionedBytes)))
		default:
			item.Cutover = append(item.Cutover, fmt.Sprintf("Create or check the %s VM: %s, %s boot, network adapters and %ss as listed above.", profile.Short, size, boot, profile.NetworkLabel))
		}
		machine := map[bool]string{true: "server", false: "VM"}[profile.Kind == vmKindPhysical]
		item.Cutover = append(item.Cutover, "Start the new "+machine+" and sign in at its console.")
		if classifyGuestOS(vm).Family == "windows" {
			if profile.Kind == vmKindCloud {
				item.Cutover = append(item.Cutover, "Check the guest uses DHCP and has the address you reserved. Then, inside the new VM, run the fix-up under Inside the VM on the VMware tab to remove VMware Tools and hidden old adapters, and bring offline disks online.")
			} else {
				item.Cutover = append(item.Cutover, "Inside the new VM, run the fix-up under Inside the VM on the VMware tab: put back the static IP settings, remove VMware Tools and hidden old adapters, and bring offline disks online.")
			}
		} else if vmHasFinding(readiness, "static-ip") && profile.Kind != vmKindCloud {
			item.Cutover = append(item.Cutover, "Reapply the recorded static IP configuration to the new network interface.")
		}
		item.Verify = append(item.Verify, "Check the VM can reach its gateway, DNS and domain controllers, and that its applications start.")
		if classifyGuestOS(vm).Family == "windows" {
			item.Verify = append(item.Verify, "Run a new collection and compare it with the one taken before cutover (Before and after on the VMware tab).")
			item.Verify = append(item.Verify, "Check Windows activation; a hardware change can require reactivation.")
		}
		item.Verify = append(item.Verify, "Ask the application owner to confirm the service works, then add the VM to backup and monitoring on the target.")
		item.Rollback = append(item.Rollback, "Shut down the new "+machine+".")
		item.Rollback = append(item.Rollback, "Reconnect the old VM's network adapters in vSphere and start it.")
		item.Rollback = append(item.Rollback, "Check DNS records point back to the old VM, and note what failed before trying again.")
		wave.VMs = append(wave.VMs, item)
	}
	numbers := []int{}
	for number := range waves {
		numbers = append(numbers, number)
	}
	// Scheduled waves first, then unscheduled, then on hold.
	sort.Slice(numbers, func(i, j int) bool {
		rank := func(n int) int {
			switch {
			case n > 0:
				return n
			case n == 0:
				return 1000
			}
			return 1001
		}
		return rank(numbers[i]) < rank(numbers[j])
	})
	result := []vmRunbookWave{}
	for _, number := range numbers {
		wave := waves[number]
		sort.SliceStable(wave.VMs, func(i, j int) bool {
			return strings.ToLower(wave.VMs[i].VM.Name) < strings.ToLower(wave.VMs[j].VM.Name)
		})
		result = append(result, *wave)
	}
	return result
}
