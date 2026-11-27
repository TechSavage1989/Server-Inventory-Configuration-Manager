package main

// Build Hyper-V hosts and failover clusters from the recorded VMware
// environment. The plan starts from what vCenter or ESXi reported (hosts per
// cluster, virtual switches and port groups with their VLANs, VMkernel ports,
// iSCSI portals, Fibre Channel adapters and datastores) and the operator fills
// in what VMware cannot know: the Windows node names, which physical adapters
// form each switch, addresses, the cluster name and the disks to use.
//
// Every step is a PowerShell script generated here with every value quoted
// by vmPSQuote. A step first lists exactly what it would change on each
// computer (Preview); Apply runs the same script with the SHA-256 of that list
// and the script refuses to change anything when the list it computes again
// differs from what was approved. Each change is made only when it is missing,
// so a step can be run again at any time and then lists nothing. Nothing is
// ever removed: an existing switch, adapter address, disk with partitions or
// cluster is left as it is and reported.
//
// This file is untagged so plans, validation and the generated scripts are
// tested on any platform; hvbuild_windows.go runs them through
// Build-HyperVHost.ps1.

import (
	"errors"
	"fmt"
	"math"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const hvBuildSchema = 1

type hvBuildNodeNIC struct {
	Adapter string `json:"adapter"`
	// IP is an IPv4 address with its prefix length, such as 10.0.20.11/24.
	IP string `json:"ip"`
}

// hvBuildNode is one Windows Server that becomes a Hyper-V host.
type hvBuildNode struct {
	Name string `json:"name"`
	// ESXi is the VMware host this node replaces, for reference.
	ESXi string `json:"esxi"`
	// Switches maps each switch name to this node's physical adapters.
	Switches map[string][]string `json:"switches"`
	// HostIPs maps each host network adapter (LiveMigration, Cluster...) to
	// this node's address on it.
	HostIPs map[string]string `json:"hostIps"`
	// Storage lists the physical adapters dedicated to iSCSI on this node.
	Storage []hvBuildNodeNIC `json:"storage"`
	// SourceNICs lists, per switch, the physical adapters the same server
	// used for that switch under ESXi (a rolling conversion reuses the
	// hardware). Read nodes matches them to Windows adapters by MAC address
	// so the switch adapters are ticked for the operator to check.
	SourceNICs map[string][]hvBuildSourceNIC `json:"sourceNics,omitempty"`
	// ManagementIP is the address the host had under ESXi, for reference.
	ManagementIP string `json:"managementIp,omitempty"`
	// Later leaves the node out of reads and steps until it is ready: an
	// ESXi host still running VMs in a rolling conversion.
	Later bool `json:"later,omitempty"`
	// ExistingHost marks a node that already ran Hyper-V when the plan was
	// matched to it.
	ExistingHost bool `json:"existingHost,omitempty"`
	// SourceISCSI are the iSCSI VMkernel addresses the same server had
	// under ESXi (a rolling conversion), for its iSCSI adapters.
	SourceISCSI []string `json:"sourceIscsi,omitempty"`
	// Suggested lists the fields the page filled in by itself ("switch:X",
	// "ip:X", "storage"); a field the operator changes leaves the list.
	Suggested []string `json:"suggested,omitempty"`
}

// hvBuildSourceNIC is one physical adapter as ESXi reported it.
type hvBuildSourceNIC struct {
	Device  string `json:"device"`
	MAC     string `json:"mac"`
	SpeedMb int    `json:"speedMb"`
}

// hvBuildSwitch is a Hyper-V virtual switch: a Switch Embedded Teaming (SET)
// switch on Windows Server 2016 and later, an LBFO team plus switch on
// Windows Server 2012 R2.
type hvBuildSwitch struct {
	Name string `json:"name"`
	// From lists the VMware switches it replaces.
	From []string `json:"from"`
	// ManagementOS keeps a host network adapter on the switch for the
	// node's own traffic (its management address moves onto it).
	ManagementOS   bool   `json:"managementOs"`
	ManagementVLAN string `json:"managementVlan"`
	// LoadBalancing is HyperVPort (the SET default) or Dynamic.
	LoadBalancing string `json:"loadBalancing"`
	// MTU 9000 turns on jumbo frames on the members and the switch's host
	// adapters.
	MTU     int `json:"mtu"`
	Uplinks int `json:"uplinks"`
	// UplinkSpeedsMb are the link speeds of the adapters the switch has on
	// the VMware hosts (or the existing Hyper-V host), so Read nodes can
	// tick adapters of the same speed and count.
	UplinkSpeedsMb []int `json:"uplinkSpeedsMb,omitempty"`
	// SourceVLAN is the VLAN VMware tagged the management network with,
	// for reference (the management VLAN is set only when Windows uses it).
	SourceVLAN string `json:"sourceVlan,omitempty"`
	// Existing marks a switch read from an existing Hyper-V host.
	Existing bool `json:"existing,omitempty"`
}

// hvBuildHostNIC is a host network adapter on a switch for one kind of
// traffic, with an address per node.
type hvBuildHostNIC struct {
	Name   string `json:"name"`
	Switch string `json:"switch"`
	VLAN   string `json:"vlan"`
	// Role is livemigration, cluster or storage.
	Role string `json:"role"`
	// From is the VMkernel port it replaces.
	From string `json:"from"`
}

// hvBuildNetwork maps a VMware port group to a Hyper-V switch and VLAN for
// the VMs that move.
type hvBuildNetwork struct {
	Portgroup  string `json:"portgroup"`
	SourceVLAN string `json:"sourceVlan"`
	Switch     string `json:"switch"`
	VLAN       string `json:"vlan"`
	VMs        int    `json:"vms"`
}

type hvBuildDisk struct {
	// UniqueID is the disk's UniqueId as Get-Disk shows it on the first
	// node; it is the same on every node for a shared LUN.
	UniqueID string `json:"uniqueId"`
	Label    string `json:"label"`
	// FileSystem is ReFS or NTFS.
	FileSystem string `json:"fileSystem"`
	// Use is csv, witness or volume (a standalone host's data volume).
	Use       string `json:"use"`
	SizeBytes uint64 `json:"sizeBytes"`
	Datastore string `json:"datastore"`
}

type hvBuildStorage struct {
	// Kind is none, iscsi or fc.
	Kind string `json:"kind"`
	MPIO bool   `json:"mpio"`
	// Portals are iSCSI discovery portals, address:port.
	Portals []string `json:"portals"`
	// TargetFilter connects only targets whose IQN contains it.
	TargetFilter string `json:"targetFilter"`
	Jumbo        bool   `json:"jumbo"`
	// Arrays are vendor/product pairs Microsoft MPIO should claim on Fibre
	// Channel, as the LUNs reported them to ESXi.
	Arrays []hvBuildArray `json:"arrays"`
	Disks  []hvBuildDisk  `json:"disks"`
}

type hvBuildArray struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
}

type hvBuildCluster struct {
	Enabled bool `json:"enabled"`
	// Join adds the nodes to the existing cluster Name and never creates
	// one; quorum and cluster networks are left as the cluster has them.
	Join      bool     `json:"join,omitempty"`
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
	// Witness is disk, fileshare, cloud or none.
	Witness      string `json:"witness"`
	WitnessShare string `json:"witnessShare"`
	CloudAccount string `json:"cloudAccount"`
	// StorageTests includes the Storage tests in validation (they take the
	// shared disks offline briefly; only on a new cluster).
	StorageTests bool `json:"storageTests"`
}

type hvBuildSettings struct {
	// MigrationAuth is Kerberos or CredSSP.
	MigrationAuth        string `json:"migrationAuth"`
	MaxMigrations        int    `json:"maxMigrations"`
	MaxStorageMigrations int    `json:"maxStorageMigrations"`
	// Performance is Compression, SMB or TCPIP.
	Performance string `json:"performance"`
	VMPath      string `json:"vmPath"`
	VHDPath     string `json:"vhdPath"`
}

// hvBuildSizing is a datastore and the Cluster Shared Volume it needs.
type hvBuildSizing struct {
	Datastore     string `json:"datastore"`
	Type          string `json:"type"`
	CapacityBytes int64  `json:"capacityBytes"`
	UsedBytes     int64  `json:"usedBytes"`
	// SuggestedBytes is the used space plus 25%, rounded up to a whole
	// 64 GiB, never less than 256 GiB.
	SuggestedBytes int64  `json:"suggestedBytes"`
	Label          string `json:"label"`
	VMs            int    `json:"vms"`
	Note           string `json:"note"`
}

type hvBuildPlan struct {
	Schema int    `json:"schema"`
	Name   string `json:"name"`
	// Source and SourceCluster name the VMware inventory it came from.
	Source        string           `json:"source"`
	SourceCluster string           `json:"sourceCluster"`
	Nodes         []hvBuildNode    `json:"nodes"`
	Switches      []hvBuildSwitch  `json:"switches"`
	HostNICs      []hvBuildHostNIC `json:"hostNics"`
	Networks      []hvBuildNetwork `json:"networks"`
	Storage       hvBuildStorage   `json:"storage"`
	Cluster       hvBuildCluster   `json:"cluster"`
	Settings      hvBuildSettings  `json:"settings"`
	Sizing        []hvBuildSizing  `json:"sizing"`
	Notes         []string         `json:"notes"`
	// Reuse is true when the nodes are the ESXi hosts themselves, rebuilt
	// one at a time (Plan and runbook's rolling scenario).
	Reuse bool `json:"reuse,omitempty"`
	// Suggested lists the plan-wide fields the page filled in by itself
	// ("cluster.name", "cluster.addresses", "witness", "vlan:<switch>",
	// "disks").
	Suggested []string `json:"suggested,omitempty"`
	// Existing is the Hyper-V cluster or host the plan was matched to.
	Existing  *hvBuildExisting `json:"existing,omitempty"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

var (
	hvBuildNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()-]{0,63}$`)
	hvBuildAdapterPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._#()+,-]{0,127}$`)
	hvBuildClusterPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,14}$`)
	hvBuildSharePattern   = regexp.MustCompile(`^\\\\[A-Za-z0-9][A-Za-z0-9.-]{0,252}\\[^\\/:*?"<>|\x00-\x1f]{1,80}(\\[^\\/:*?"<>|\x00-\x1f]{1,80})*$`)
	hvBuildAccountPattern = regexp.MustCompile(`^[a-z0-9]{3,24}$`)
	hvBuildDiskIDPattern  = regexp.MustCompile(`^[^\x00-\x1f"<>|*?]{1,256}$`)
	hvBuildFilterPattern  = regexp.MustCompile(`^[A-Za-z0-9:._-]{0,200}$`)
	hvBuildArrayPattern   = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,16}$`)
)

// hvBuildCleanName turns a VMware name into a Hyper-V name.
func hvBuildCleanName(value string) string {
	value = regexp.MustCompile(`[^A-Za-z0-9._()-]+`).ReplaceAllString(strings.TrimSpace(value), "-")
	value = strings.Trim(value, "-.")
	if len(value) > 48 {
		value = value[:48]
	}
	if value == "" {
		value = "Switch"
	}
	return value
}

// hvBuildVLAN returns a single VLAN ID from a VMware VLAN text, or "".
func hvBuildVLAN(value string) string {
	value = strings.TrimSpace(value)
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > 4094 {
		return ""
	}
	return strconv.Itoa(number)
}

func hvBuildContains(values []string, value string) bool {
	for _, item := range values {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func hvBuildAddUnique(values []string, value string) []string {
	if value == "" || hvBuildContains(values, value) {
		return values
	}
	return append(values, value)
}

// hvBuildSourceHosts are the ESXi hosts of the chosen VMware cluster (all
// hosts when no cluster is named).
func hvBuildSourceHosts(inventory *vmInventory, cluster string) []vmHost {
	hosts := []vmHost{}
	for _, host := range inventory.Hosts {
		if cluster == "" || strings.EqualFold(host.Cluster, cluster) {
			hosts = append(hosts, host)
		}
	}
	sort.Slice(hosts, func(i, j int) bool { return strings.ToLower(hosts[i].Name) < strings.ToLower(hosts[j].Name) })
	return hosts
}

// hvBuildSuggest drafts a plan from the inventory for one VMware cluster.
func hvBuildSuggest(inventory *vmInventory, cluster string) (hvBuildPlan, error) {
	return hvBuildSuggestFor(inventory, cluster, false, nil)
}

// hvBuildSuggestFor drafts the plan. With reuse each node is the ESXi host
// it replaces, rebuilt on the same hardware: it is named after the host,
// keeps the host's vMotion address for live migration and lists the
// physical adapters (by MAC) each switch used, in the order of order (the
// rolling plan's stages) when given.
func hvBuildSuggestFor(inventory *vmInventory, cluster string, reuse bool, order []string) (hvBuildPlan, error) {
	if inventory == nil {
		return hvBuildPlan{}, errors.New("read the VMware inventory first (Discovery, VMware and Hyper-V)")
	}
	hosts := hvBuildSourceHosts(inventory, cluster)
	if len(hosts) == 0 {
		if cluster != "" {
			return hvBuildPlan{}, fmt.Errorf("the inventory has no ESXi hosts in cluster %s", cluster)
		}
		return hvBuildPlan{}, errors.New("the inventory has no ESXi hosts")
	}
	plan := hvBuildPlan{Schema: hvBuildSchema, Source: inventory.Server, SourceCluster: cluster, Notes: []string{}}
	hostNames := []string{}
	for _, host := range hosts {
		hostNames = append(hostNames, host.Name)
	}

	// Switches: every VMware switch that carries VM networks, management
	// or vMotion becomes a SET switch; one carrying only other VMkernel
	// ports (iSCSI or NFS storage) becomes dedicated storage adapters.
	switches := []*hvBuildSourceSwitch{}
	byName := map[string]*hvBuildSourceSwitch{}
	vmNetworkSwitch := map[string]string{}
	for _, network := range inventory.Networks {
		if network.Switch != "" {
			vmNetworkSwitch[strings.ToLower(network.Name)] = network.Switch
		}
	}
	for _, host := range hosts {
		if host.Details == nil {
			continue
		}
		for _, item := range host.Details.Switches {
			entry := byName[strings.ToLower(item.Name)]
			if entry == nil {
				entry = &hvBuildSourceSwitch{name: item.Name, kind: item.Kind}
				byName[strings.ToLower(item.Name)] = entry
				switches = append(switches, entry)
			}
			if item.MTU > entry.mtu {
				entry.mtu = item.MTU
			}
			if len(item.Uplinks) > entry.uplinks {
				entry.uplinks = len(item.Uplinks)
			}
			for _, portgroup := range item.Portgroups {
				entry.portgroups = hvBuildAddUnique(entry.portgroups, portgroup)
			}
			if len(entry.speeds) == 0 {
				for _, nic := range host.Details.PhysicalNICs {
					if strings.EqualFold(nic.Switch, item.Name) && nic.SpeedMb > 0 {
						entry.speeds = append(entry.speeds, nic.SpeedMb)
					}
				}
			}
		}
		for _, vmk := range host.Details.VMkernel {
			sw := hvBuildSwitchOfPortgroup(host.Details, vmk.Portgroup, byName)
			if sw == nil {
				continue
			}
			switch {
			case vmContains(vmk.Services, "management"):
				sw.management = true
				if sw.managementVLAN == "" {
					sw.managementVLAN = vmk.VLAN
				}
			case vmContains(vmk.Services, "vmotion"):
				sw.vmotion = append(sw.vmotion, vmk)
			default:
				sw.other = append(sw.other, vmk)
			}
		}
	}
	firstSET := ""
	for _, sw := range switches {
		carriesVMs := false
		for _, network := range inventory.Networks {
			if strings.EqualFold(network.Switch, sw.name) && network.VMCount >= 0 && hvBuildContains(sw.portgroups, network.Name) {
				carriesVMs = true
			}
		}
		if strings.EqualFold(sw.kind, "Distributed") {
			carriesVMs = true
		}
		if !carriesVMs && !sw.management && len(sw.vmotion) == 0 {
			if len(sw.other) > 0 {
				sw.storageOnly = true
			}
			continue
		}
		name := "SET-" + hvBuildCleanName(sw.name)
		mtu := 1500
		if sw.mtu >= 9000 {
			mtu = 9000
		}
		plan.Switches = append(plan.Switches, hvBuildSwitch{Name: name, From: []string{sw.name}, ManagementOS: sw.management, LoadBalancing: "HyperVPort", MTU: mtu, Uplinks: sw.uplinks, UplinkSpeedsMb: sw.speeds})
		if sw.management && sw.managementVLAN != "" {
			plan.Switches[len(plan.Switches)-1].SourceVLAN = sw.managementVLAN
		}
		if sw.management && sw.managementVLAN != "" {
			plan.Notes = append(plan.Notes, fmt.Sprintf("VMware tags the management network on %s with VLAN %s. Set the management VLAN on %s only if the nodes' switch ports are trunks and Windows already uses that VLAN; otherwise the node loses its connection.", sw.name, sw.managementVLAN, name))
		}
		if firstSET == "" {
			firstSET = name
		}
		if len(sw.vmotion) > 0 && !hvBuildHasRole(plan.HostNICs, "livemigration") {
			plan.HostNICs = append(plan.HostNICs, hvBuildHostNIC{Name: "LiveMigration", Switch: name, VLAN: sw.vmotion[0].VLAN, Role: "livemigration", From: sw.vmotion[0].Portgroup})
		}
	}
	if len(plan.Switches) == 0 {
		plan.Switches = append(plan.Switches, hvBuildSwitch{Name: "SET-Converged", ManagementOS: true, LoadBalancing: "HyperVPort", MTU: 1500, Uplinks: 2})
		firstSET = "SET-Converged"
		plan.Notes = append(plan.Notes, "The inventory has no host network details (read it again with full discovery for switches, VMkernel ports and storage adapters), so one converged SET switch is suggested.")
	}
	if len(hosts) > 1 && !hvBuildHasRole(plan.HostNICs, "cluster") {
		plan.HostNICs = append(plan.HostNICs, hvBuildHostNIC{Name: "Cluster", Switch: firstSET, Role: "cluster"})
	}

	// VM networks: each port group keeps its VLAN on the switch that
	// replaces its VMware switch.
	switchFor := func(vmwareSwitch string) string {
		for _, sw := range plan.Switches {
			if hvBuildContains(sw.From, vmwareSwitch) {
				return sw.Name
			}
		}
		return firstSET
	}
	for _, network := range inventory.Networks {
		if len(network.Datacenter) > 0 && len(hosts) > 0 && !strings.EqualFold(network.Datacenter, hosts[0].Datacenter) {
			continue
		}
		vmSwitch := network.Switch
		if vmSwitch == "" {
			vmSwitch = vmNetworkSwitch[strings.ToLower(network.Name)]
		}
		target := firstSET
		if vmSwitch != "" {
			target = switchFor(vmSwitch)
		}
		plan.Networks = append(plan.Networks, hvBuildNetwork{Portgroup: network.Name, SourceVLAN: network.VLAN, Switch: target, VLAN: hvBuildVLAN(network.VLAN), VMs: network.VMCount})
		if network.VLAN != "" && hvBuildVLAN(network.VLAN) == "" && network.VLAN != "0" {
			plan.Notes = append(plan.Notes, fmt.Sprintf("Port group %s uses VLAN %s (a trunk or range). Hyper-V sets trunks per VM adapter (Set-VMNetworkAdapterVlan -Trunk); set its VLAN by hand.", network.Name, network.VLAN))
		}
	}

	// Storage.
	portals := []string{}
	iscsi, fc := false, false
	arrays := []hvBuildArray{}
	for _, host := range hosts {
		if host.Details == nil {
			continue
		}
		for _, adapter := range host.Details.StorageAdapters {
			kind := strings.ToLower(adapter.Kind)
			switch {
			case strings.Contains(kind, "iscsi"):
				iscsi = true
				for _, target := range adapter.SendTargets {
					portals = hvBuildAddUnique(portals, target)
				}
				for _, target := range adapter.StaticTargets {
					if at := strings.LastIndex(target, "@"); at >= 0 {
						portals = hvBuildAddUnique(portals, target[at+1:])
					}
				}
			case strings.Contains(kind, "fibre") || strings.Contains(kind, "fcoe"):
				fc = true
			}
		}
		for _, lun := range host.Details.LUNs {
			if lun.Local || strings.TrimSpace(lun.Vendor) == "" || !strings.EqualFold(lun.Type, "disk") && lun.Type != "" {
				continue
			}
			array := hvBuildArray{Vendor: strings.TrimSpace(lun.Vendor), Product: strings.TrimSpace(lun.Model)}
			if len(array.Vendor) > 8 {
				array.Vendor = array.Vendor[:8]
			}
			if len(array.Product) > 16 {
				array.Product = array.Product[:16]
			}
			known := false
			for _, existing := range arrays {
				if strings.EqualFold(existing.Vendor, array.Vendor) && strings.EqualFold(existing.Product, array.Product) {
					known = true
				}
			}
			if !known && hvBuildArrayPattern.MatchString(array.Vendor) && hvBuildArrayPattern.MatchString(array.Product) {
				arrays = append(arrays, array)
			}
		}
	}
	switch {
	case iscsi:
		plan.Storage = hvBuildStorage{Kind: "iscsi", MPIO: true, Portals: portals}
		for _, sw := range switches {
			if sw.storageOnly && sw.mtu >= 9000 {
				plan.Storage.Jumbo = true
			}
		}
		if len(portals) == 0 {
			plan.Notes = append(plan.Notes, "ESXi uses iSCSI but the inventory lists no discovery portals; type the array's portal addresses.")
		}
		plan.Notes = append(plan.Notes, "Give each node's iSCSI initiator name (shown after Read nodes) access to the LUNs on the array, the same way the ESXi hosts have it.")
	case fc:
		plan.Storage = hvBuildStorage{Kind: "fc", MPIO: true, Arrays: arrays}
		plan.Notes = append(plan.Notes, "Zone each node's Fibre Channel ports (WWPNs shown after Read nodes) to the array and present the LUNs to them, the same way the ESXi hosts have it.")
	default:
		plan.Storage = hvBuildStorage{Kind: "none"}
	}

	// Datastores to Cluster Shared Volumes.
	for _, datastore := range inventory.Datastores {
		if !hvBuildDatastoreUsed(datastore, hostNames) {
			continue
		}
		used := datastore.CapacityBytes - datastore.FreeBytes
		if used < 0 {
			used = 0
		}
		sizing := hvBuildSizing{Datastore: datastore.Name, Type: datastore.Type, CapacityBytes: datastore.CapacityBytes, UsedBytes: used, SuggestedBytes: hvBuildSuggestedSize(used), Label: "CSV-" + hvBuildCleanName(datastore.Name), VMs: datastore.VMCount}
		switch strings.ToLower(datastore.Type) {
		case "vmfs":
			if !datastore.Shared && len(hosts) > 1 {
				sizing.Note = "Local to one ESXi host; on a cluster its VMs need room on shared storage."
			}
		case "nfs", "nfs41":
			sizing.Note = "NFS: Hyper-V keeps VMs on a Cluster Shared Volume or an SMB 3 share instead."
		case "vsan":
			sizing.Note = "vSAN: plan SAN LUNs or Storage Spaces Direct (not set up here) for these VMs."
		case "vvol":
			sizing.Note = "vVols: ask the array for LUNs of this size for Hyper-V."
		}
		plan.Sizing = append(plan.Sizing, sizing)
	}
	sort.Slice(plan.Sizing, func(i, j int) bool { return plan.Sizing[i].Datastore < plan.Sizing[j].Datastore })

	// Nodes, one per ESXi host, with the switch adapter lists to fill in.
	if reuse && len(order) > 0 {
		hosts = hvBuildOrderHosts(hosts, order)
	}
	for _, host := range hosts {
		node := hvBuildNode{ESXi: host.Name, Switches: map[string][]string{}, HostIPs: map[string]string{}, Storage: []hvBuildNodeNIC{}}
		for _, sw := range plan.Switches {
			node.Switches[sw.Name] = []string{}
		}
		if reuse {
			hvBuildReuseHost(&plan, &node, host)
			// A host vSphere still reaches runs ESXi: it waits until it
			// is rebuilt with Windows.
			node.Later = strings.EqualFold(host.ConnectionState, "connected")
		}
		plan.Nodes = append(plan.Nodes, node)
	}
	if reuse {
		plan.Reuse = true
		plan.Notes = append(plan.Notes, "Rolling conversion: each node is an ESXi host rebuilt on the same hardware, in the order of the stages in Plan and runbook. Nodes are named after their ESXi hosts and keep their vMotion address for live migration; change either if you like. Read nodes ticks each switch's adapters by the MAC addresses ESXi used; check them before Apply.")
	}

	// Cluster.
	sourceCluster := vmCluster{}
	for _, item := range inventory.Clusters {
		if strings.EqualFold(item.Name, cluster) {
			sourceCluster = item
		}
	}
	plan.Cluster = hvBuildCluster{Enabled: len(hosts) > 1, Witness: "fileshare"}
	if plan.Storage.Kind != "none" {
		plan.Cluster.Witness = "disk"
	}
	plan.Cluster.StorageTests = true
	if sourceCluster.EVCMode != "" {
		plan.Notes = append(plan.Notes, fmt.Sprintf("The VMware cluster uses EVC mode %s. Use nodes with the same processor generation, or turn on processor compatibility on VMs that must live migrate between different ones.", sourceCluster.EVCMode))
	}
	if sourceCluster.DRS {
		plan.Notes = append(plan.Notes, "DRS balances the VMware cluster; Hyper-V clusters balance VMs with VM load balancing, which Windows Server 2016 and later turn on by default.")
	}
	if sourceCluster.VSAN {
		plan.Notes = append(plan.Notes, "The VMware cluster uses vSAN. This tool connects SAN storage; Storage Spaces Direct is not set up here.")
	}
	if len(hosts) > 0 && hosts[0].Details != nil {
		details := hosts[0].Details
		if len(details.DNSServers) > 0 {
			plan.Notes = append(plan.Notes, "ESXi uses DNS servers "+strings.Join(details.DNSServers, ", ")+". The Windows nodes must be joined to the domain and use its DNS servers before the cluster is created.")
		}
		if len(details.NTPServers) > 0 {
			plan.Notes = append(plan.Notes, "ESXi keeps time from "+strings.Join(details.NTPServers, ", ")+". Domain-joined nodes take time from the domain.")
		}
	}
	cores, memory := 0, int64(0)
	for _, host := range hosts {
		cores += host.Cores
		memory += host.MemoryBytes
	}
	plan.Notes = append(plan.Notes, fmt.Sprintf("The %d ESXi hosts have %d cores and %s of memory in all. Plan and runbook sizes the new cluster against the VMs.", len(hosts), cores, vmFormatBytes(memory)))
	plan.Settings = hvBuildSettings{MigrationAuth: "Kerberos", MaxMigrations: 2, MaxStorageMigrations: 2, Performance: "Compression"}
	if cluster != "" {
		plan.Name = hvBuildCleanName(cluster)
	} else {
		plan.Name = hvBuildCleanName(hosts[0].Name)
	}
	return plan, nil
}

type hvBuildSourceSwitch struct {
	name, kind     string
	mtu, uplinks   int
	speeds         []int
	portgroups     []string
	management     bool
	managementVLAN string
	vmotion        []vmHostVMkernel
	storageOnly    bool
	other          []vmHostVMkernel
}

// hvBuildSwitchOfPortgroup finds the VMware switch holding a port group on
// one host; a VMkernel port on a distributed switch goes to the host's only
// distributed switch.
func hvBuildSwitchOfPortgroup(details *vmHostDetails, portgroup string, byName map[string]*hvBuildSourceSwitch) *hvBuildSourceSwitch {
	distributed := []string{}
	for _, item := range details.Switches {
		if hvBuildContains(item.Portgroups, portgroup) {
			return byName[strings.ToLower(item.Name)]
		}
		if strings.EqualFold(item.Kind, "Distributed") {
			distributed = append(distributed, item.Name)
		}
	}
	if len(distributed) == 1 {
		return byName[strings.ToLower(distributed[0])]
	}
	return nil
}

func hvBuildHasRole(nics []hvBuildHostNIC, role string) bool {
	for _, nic := range nics {
		if nic.Role == role {
			return true
		}
	}
	return false
}

func hvBuildDatastoreUsed(datastore vmDatastore, hosts []string) bool {
	if len(datastore.Hosts) == 0 {
		return true
	}
	for _, host := range datastore.Hosts {
		if hvBuildContains(hosts, host) {
			return true
		}
	}
	return false
}

func hvBuildSuggestedSize(used int64) int64 {
	const gib = int64(1) << 30
	size := int64(math.Ceil(float64(used)*1.25/float64(64*gib))) * 64 * gib
	if size < 256*gib {
		size = 256 * gib
	}
	return size
}

// hvBuildOrderHosts puts hosts in the given order; hosts not named follow.
func hvBuildOrderHosts(hosts []vmHost, order []string) []vmHost {
	result := []vmHost{}
	used := map[string]bool{}
	for _, name := range order {
		for _, host := range hosts {
			if strings.EqualFold(host.Name, name) && !used[strings.ToLower(host.Name)] {
				result = append(result, host)
				used[strings.ToLower(host.Name)] = true
			}
		}
	}
	for _, host := range hosts {
		if !used[strings.ToLower(host.Name)] {
			result = append(result, host)
		}
	}
	return result
}

// hvBuildShortName is a host's computer name: its DNS name's first label,
// or "" for an IP address or a name Windows cannot use.
func hvBuildShortName(name string) string {
	name = strings.TrimSpace(name)
	if net.ParseIP(name) != nil {
		return ""
	}
	short := strings.SplitN(name, ".", 2)[0]
	if len(short) > 15 || !hvBuildClusterPattern.MatchString(short) {
		return ""
	}
	return short
}

// hvBuildMaskBits turns a dotted IPv4 mask into its prefix length.
func hvBuildMaskBits(mask string) int {
	ip := net.ParseIP(strings.TrimSpace(mask)).To4()
	if ip == nil {
		return 0
	}
	ones, bits := net.IPv4Mask(ip[0], ip[1], ip[2], ip[3]).Size()
	if bits == 0 {
		return 0
	}
	return ones
}

// hvBuildReuseHost fills a node from the ESXi host it replaces.
func hvBuildReuseHost(plan *hvBuildPlan, node *hvBuildNode, host vmHost) {
	node.Name = hvBuildShortName(host.Name)
	if host.Details == nil {
		return
	}
	details := host.Details
	node.ManagementIP = details.ManagementIP
	node.SourceNICs = map[string][]hvBuildSourceNIC{}
	for _, sw := range plan.Switches {
		list := []hvBuildSourceNIC{}
		for _, nic := range details.PhysicalNICs {
			if nic.Switch != "" && hvBuildContains(sw.From, nic.Switch) && hvBuildMACPattern.MatchString(nic.MAC) {
				list = append(list, hvBuildSourceNIC{Device: nic.Device, MAC: hvBuildNormalMAC(nic.MAC), SpeedMb: nic.SpeedMb})
			}
		}
		if len(list) > 0 {
			node.SourceNICs[sw.Name] = list
		}
	}
	for _, nic := range plan.HostNICs {
		if nic.From == "" {
			continue
		}
		for _, vmk := range details.VMkernel {
			if !strings.EqualFold(vmk.Portgroup, nic.From) || vmk.DHCP || net.ParseIP(vmk.IP).To4() == nil {
				continue
			}
			if bits := hvBuildMaskBits(vmk.Mask); bits > 0 && bits < 32 {
				node.HostIPs[nic.Name] = vmk.IP + "/" + strconv.Itoa(bits)
			}
		}
	}
	label := vmNonEmpty(node.Name, host.Name)
	if details.ManagementIP != "" {
		plan.Notes = append(plan.Notes, fmt.Sprintf("%s: give Windows the management address %s that ESXi used%s, and remove %s's old DNS record if it does not update.", label, details.ManagementIP, hvBuildGatewayText(details), host.Name))
	}
	iscsi := []string{}
	for _, vmk := range details.VMkernel {
		if !vmContains(vmk.Services, "management") && !vmContains(vmk.Services, "vmotion") && vmk.IP != "" && strings.Contains(strings.ToLower(vmk.Portgroup), "iscsi") {
			iscsi = append(iscsi, vmk.IP)
			if bits := hvBuildMaskBits(vmk.Mask); bits > 0 && bits < 32 && net.ParseIP(vmk.IP).To4() != nil {
				node.SourceISCSI = append(node.SourceISCSI, vmk.IP+"/"+strconv.Itoa(bits))
			}
		}
	}
	if len(iscsi) > 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf("%s used iSCSI addresses %s under ESXi; they are free for its iSCSI adapters in Storage once the host is rebuilt.", label, strings.Join(iscsi, ", ")))
	}
}

func hvBuildGatewayText(details *vmHostDetails) string {
	if details.DefaultGateway == "" {
		return ""
	}
	return " (gateway " + details.DefaultGateway + ")"
}

var hvBuildMACPattern = regexp.MustCompile(`^[0-9A-Fa-f]{2}([:-][0-9A-Fa-f]{2}){5}$`)

// hvBuildNormalMAC writes a MAC address the way Windows shows it.
func hvBuildNormalMAC(mac string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(mac), ":", "-"))
}
