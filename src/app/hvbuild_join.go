package main

// Build Hyper-V hosts with Hyper-V that already exists (2026-11-27, owner:
// "add VMware hosts to an already existing, or previously created Hyper-V
// cluster, or generate a cluster from a VMware environment, adding it to and
// configuring existing Hyper-V hosts for it").
//
// Read existing (Build-HyperVHost.ps1 -Mode ReadExisting) reads one Hyper-V
// host, or a failover cluster through one of its nodes, and changes nothing.
// hvBuildAdopt then makes the plan match it: its switches (names, adapters,
// load balancing, jumbo frames), its host adapters (names, switches, VLANs),
// its storage (iSCSI portals, MPIO, arrays) and its live migration settings.
// For a cluster the plan joins it (Cluster.Join): the cluster step only adds
// the plan's nodes to it and refuses to create one, and steps that would
// change the cluster itself (quorum, cluster networks) are left out. For a
// standalone host the host becomes a node of a new cluster; its steps then
// list nothing to change.
//
// A node can wait (Later): an ESXi host still running VMs in a rolling
// conversion. Steps, reads and the cluster leave waiting nodes out until the
// operator marks the node ready, so one plan covers every host and each host
// joins as it is converted.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// hvStrings reads a JSON string list that PowerShell may have written as a
// single string or null.
type hvStrings []string

func (s *hvStrings) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*s = list
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single == "" {
			*s = hvStrings{}
		} else {
			*s = hvStrings{single}
		}
		return nil
	}
	*s = hvStrings{}
	return nil
}

// hvInts reads a JSON number list that PowerShell may have written as a
// single number or null.
type hvInts []int

func (s *hvInts) UnmarshalJSON(data []byte) error {
	var list []int
	if err := json.Unmarshal(data, &list); err == nil {
		*s = list
		return nil
	}
	var single int
	if err := json.Unmarshal(data, &single); err == nil {
		*s = hvInts{single}
		return nil
	}
	*s = hvInts{}
	return nil
}

// hvBuildTemplateSwitch is a virtual switch as an existing host has it.
type hvBuildTemplateSwitch struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Embedded      bool      `json:"embedded"`
	Team          bool      `json:"team"`
	Adapters      hvStrings `json:"adapters"`
	SpeedsMb      hvInts    `json:"speedsMb"`
	LoadBalancing string    `json:"loadBalancing"`
	ManagementOS  bool      `json:"managementOs"`
	Jumbo         bool      `json:"jumbo"`
}

// hvBuildTemplateNIC is a host network adapter (vEthernet) of an existing
// host.
type hvBuildTemplateNIC struct {
	Name    string    `json:"name"`
	Switch  string    `json:"switch"`
	VLAN    string    `json:"vlan"`
	IPs     hvStrings `json:"ips"`
	Gateway bool      `json:"gateway"`
}

type hvBuildTemplateStorage struct {
	Initiator string    `json:"initiator"`
	Portals   hvStrings `json:"portals"`
	Targets   hvStrings `json:"targets"`
	MPIO      bool      `json:"mpio"`
	FC        bool      `json:"fc"`
	Arrays    []struct {
		Vendor  string `json:"vendor"`
		Product string `json:"product"`
	} `json:"arrays"`
	Adapters []struct {
		Adapter string `json:"adapter"`
		IP      string `json:"ip"`
	} `json:"adapters"`
}

type hvBuildTemplateHost struct {
	MigrationEnabled  bool      `json:"migrationEnabled"`
	Auth              string    `json:"auth"`
	Max               int       `json:"max"`
	MaxStorage        int       `json:"maxStorage"`
	Performance       string    `json:"performance"`
	VMPath            string    `json:"vmPath"`
	VHDPath           string    `json:"vhdPath"`
	UseAnyNetwork     bool      `json:"useAnyNetwork"`
	MigrationNetworks hvStrings `json:"migrationNetworks"`
}

// hvBuildTemplate is what Read existing found on one Hyper-V host.
type hvBuildTemplate struct {
	Computer     string                  `json:"computer"`
	DNSName      string                  `json:"dnsName"`
	Caption      string                  `json:"caption"`
	Build        int                     `json:"build"`
	Domain       string                  `json:"domain"`
	PartOfDomain bool                    `json:"partOfDomain"`
	HyperV       bool                    `json:"hyperV"`
	Switches     []hvBuildTemplateSwitch `json:"switches"`
	HostNICs     []hvBuildTemplateNIC    `json:"hostNics"`
	Storage      hvBuildTemplateStorage  `json:"storage"`
	Host         *hvBuildTemplateHost    `json:"host"`
	Cluster      string                  `json:"cluster"`
	Warnings     hvStrings               `json:"warnings"`
}

type hvBuildClusterNetwork struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Mask    string `json:"mask"`
	Role    int    `json:"role"`
	// Migration is true when live migration may use the network (it is not
	// in the cluster's MigrationExcludeNetworks).
	Migration bool `json:"migration"`
}

type hvBuildClusterCSV struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes uint64 `json:"sizeBytes"`
	FreeBytes uint64 `json:"freeBytes"`
}

type hvBuildMember struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// hvBuildClusterFacts is what Read existing found about a failover cluster.
type hvBuildClusterFacts struct {
	Name             string                  `json:"name"`
	Domain           string                  `json:"domain"`
	Nodes            []hvBuildMember         `json:"nodes"`
	Addresses        hvStrings               `json:"addresses"`
	WitnessType      string                  `json:"witnessType"`
	Witness          string                  `json:"witness"`
	WitnessPath      string                  `json:"witnessPath"`
	Networks         []hvBuildClusterNetwork `json:"networks"`
	MigrationExclude hvStrings               `json:"migrationExclude"`
	CSVs             []hvBuildClusterCSV     `json:"csvs"`
	VMs              int                     `json:"vms"`
}

// hvBuildExistingRead is one Read existing result.
type hvBuildExistingRead struct {
	// Kind is join (an existing cluster) or takein (standalone hosts that
	// become a new cluster).
	Kind string `json:"kind"`
	// Names are the names the operator typed; the first was read.
	Names    []string             `json:"names"`
	ReadFrom string               `json:"readFrom"`
	Template *hvBuildTemplate     `json:"template"`
	Cluster  *hvBuildClusterFacts `json:"cluster"`
	At       time.Time            `json:"at"`
}

// hvBuildExisting is the plan's record of the Hyper-V it was matched to,
// shown in the plan and used for validation (Members).
type hvBuildExisting struct {
	Kind     string              `json:"kind"`
	Name     string              `json:"name"`
	ReadFrom string              `json:"readFrom"`
	Members  []hvBuildMember     `json:"members"`
	Witness  string              `json:"witness"`
	CSVs     []hvBuildClusterCSV `json:"csvs"`
	// HostIPs are the read node's addresses on each host adapter, so
	// addresses for new nodes can be suggested in the same subnets.
	HostIPs map[string]string `json:"hostIps,omitempty"`
	ReadAt  time.Time         `json:"readAt"`
}

// hvBuildCleanSuggested keeps the keys of values the page filled in by
// itself (a re-read may change them; a value the operator set is never in
// the list, so it is never replaced).
func hvBuildCleanSuggested(keys []string) []string {
	clean := []string{}
	seen := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 200 || seen[key] || len(clean) >= 128 || strings.ContainsAny(key, "\x00\r\n") {
			continue
		}
		seen[key] = true
		clean = append(clean, key)
	}
	return clean
}

// hvBuildActive is the plan without its waiting nodes: what steps, reads
// and scripts work on.
func hvBuildActive(plan hvBuildPlan) hvBuildPlan {
	nodes := []hvBuildNode{}
	for _, node := range plan.Nodes {
		if !node.Later {
			nodes = append(nodes, node)
		}
	}
	plan.Nodes = nodes
	return plan
}

// hvBuildMembers lists the existing cluster's member names.
func hvBuildMembers(plan hvBuildPlan) []string {
	names := []string{}
	if plan.Existing == nil || !plan.Cluster.Join {
		return names
	}
	for _, member := range plan.Existing.Members {
		if member.Name != "" {
			names = append(names, member.Name)
		}
	}
	return names
}

func hvBuildShort(name string) string {
	return strings.ToLower(strings.SplitN(strings.TrimSpace(name), ".", 2)[0])
}

// hvBuildSameNode reports whether a node is the given computer.
func hvBuildSameNode(node hvBuildNode, computer string) bool {
	short := hvBuildShort(computer)
	if short == "" {
		return false
	}
	return hvBuildShort(node.Name) == short || (node.ESXi != "" && net.ParseIP(node.ESXi) == nil && hvBuildShort(node.ESXi) == short)
}

func hvBuildRoleOf(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "migr") || strings.Contains(lower, "vmotion") || lower == "lm" || strings.HasPrefix(lower, "lm-") || strings.HasPrefix(lower, "lm_"):
		return "livemigration"
	case strings.Contains(lower, "iscsi") || strings.Contains(lower, "storage") || strings.Contains(lower, "smb"):
		return "storage"
	}
	return "cluster"
}

// hvBuildIPv4 returns the first IPv4 address with a prefix of a list.
func hvBuildIPv4(values []string) string {
	for _, value := range values {
		if clean, err := hvBuildCheckIPv4CIDR(value); err == nil {
			return clean
		}
	}
	return ""
}

func hvBuildSubnetKey(cidr string) string {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	return network.String()
}

// hvBuildAdopt makes a plan match an existing Hyper-V cluster or host.
// base is a plan drafted from VMware (or nil for none). For kind join the
// read must be a cluster; for takein it must be a host that is not in one.
func hvBuildAdopt(base *hvBuildPlan, read hvBuildExistingRead) (hvBuildPlan, error) {
	t := read.Template
	if t == nil {
		return hvBuildPlan{}, errors.New("read the existing Hyper-V first")
	}
	if !t.HyperV {
		return hvBuildPlan{}, fmt.Errorf("%s does not run Hyper-V (its Hyper-V PowerShell module is missing)", vmNonEmpty(t.Computer, read.ReadFrom))
	}
	join := read.Kind == "join"
	if join && read.Cluster == nil {
		if t.Cluster != "" {
			return hvBuildPlan{}, fmt.Errorf("%s is in cluster %s, but the cluster could not be read: install the Failover Cluster Module for Windows PowerShell on this computer or on that node, then read it again", t.Computer, t.Cluster)
		}
		return hvBuildPlan{}, fmt.Errorf("%s is not in a failover cluster. Choose \"A new cluster with Hyper-V hosts I already have\" to make one with it", t.Computer)
	}
	if !join && (read.Cluster != nil || t.Cluster != "") {
		return hvBuildPlan{}, fmt.Errorf("%s is already in cluster %s. Choose \"Add hosts to an existing Hyper-V cluster\" to add nodes to it", t.Computer, vmNonEmpty(t.Cluster, read.Cluster.Name))
	}
	plan := hvBuildPlan{Schema: hvBuildSchema, Notes: []string{}, Settings: hvBuildSettings{MigrationAuth: "Kerberos", MaxMigrations: 2, MaxStorageMigrations: 2, Performance: "Compression"}}
	if base != nil {
		plan = *base
		if plan.Notes == nil {
			plan.Notes = []string{}
		}
	}
	notes := []string{}

	// Switches: the existing host's external switches, in place of the
	// drafted ones. Each drafted switch maps to one of them.
	switches := []hvBuildSwitch{}
	for _, sw := range t.Switches {
		if !strings.EqualFold(sw.Type, "External") || !hvBuildNamePattern.MatchString(sw.Name) {
			if sw.Name != "" && !strings.EqualFold(sw.Type, "External") {
				notes = append(notes, fmt.Sprintf("%s has the %s switch %s; make it on the new nodes by hand if VMs use it.", t.Computer, strings.ToLower(sw.Type), sw.Name))
			}
			continue
		}
		lb := "HyperVPort"
		if strings.EqualFold(sw.LoadBalancing, "Dynamic") {
			lb = "Dynamic"
		}
		mtu := 1500
		if sw.Jumbo {
			mtu = 9000
		}
		speeds := []int{}
		for _, speed := range sw.SpeedsMb {
			if speed > 0 {
				speeds = append(speeds, speed)
			}
		}
		uplinks := len(sw.Adapters)
		if uplinks == 0 {
			uplinks = len(speeds)
		}
		switches = append(switches, hvBuildSwitch{Name: sw.Name, From: []string{}, ManagementOS: sw.ManagementOS, LoadBalancing: lb, MTU: mtu, Uplinks: uplinks, UplinkSpeedsMb: speeds, Existing: true})
		if !sw.Embedded && t.Build >= 14393 {
			kind := "a single adapter"
			if sw.Team {
				kind = "an LBFO team"
			}
			notes = append(notes, fmt.Sprintf("%s's switch %s is on %s; new nodes on Windows Server 2016 or later get a Switch Embedded Teaming switch of the same name.", t.Computer, sw.Name, kind))
		}
	}
	if len(switches) == 0 {
		return hvBuildPlan{}, fmt.Errorf("%s has no external virtual switch to match", t.Computer)
	}
	switchMap := map[string]string{}
	for _, sw := range plan.Switches {
		target := hvBuildMatchSwitch(sw, switches)
		switchMap[sw.Name] = target
		for index := range switches {
			if switches[index].Name == target {
				for _, from := range sw.From {
					switches[index].From = hvBuildAddUnique(switches[index].From, from)
				}
				if len(switches[index].UplinkSpeedsMb) == 0 {
					switches[index].UplinkSpeedsMb = sw.UplinkSpeedsMb
				}
			}
		}
	}
	for index := range plan.Nodes {
		node := &plan.Nodes[index]
		adapters := map[string][]string{}
		sources := map[string][]hvBuildSourceNIC{}
		for name, list := range node.Switches {
			target := switchMap[name]
			if target == "" {
				continue
			}
			for _, adapter := range list {
				adapters[target] = hvBuildAddUnique(adapters[target], adapter)
			}
		}
		for name, list := range node.SourceNICs {
			if target := switchMap[name]; target != "" {
				sources[target] = append(sources[target], list...)
			}
		}
		for _, sw := range switches {
			if adapters[sw.Name] == nil {
				adapters[sw.Name] = []string{}
			}
		}
		node.Switches = adapters
		if node.SourceNICs != nil {
			node.SourceNICs = sources
		}
	}
	for index := range plan.Networks {
		if target := switchMap[plan.Networks[index].Switch]; target != "" {
			plan.Networks[index].Switch = target
		} else if plan.Networks[index].Switch != "" {
			plan.Networks[index].Switch = switches[0].Name
		}
	}
	plan.Switches = switches

	// Host adapters: the existing host's, but not its management adapter.
	clusterNets := []hvBuildClusterNetwork{}
	if read.Cluster != nil {
		clusterNets = read.Cluster.Networks
	}
	migrationSubnets := map[string]bool{}
	if t.Host != nil {
		for _, subnet := range t.Host.MigrationNetworks {
			if _, network, err := net.ParseCIDR(subnet); err == nil {
				migrationSubnets[network.String()] = true
			}
		}
	}
	nics := []hvBuildHostNIC{}
	templateIPs := map[string]string{}
	for _, nic := range t.HostNICs {
		ip := hvBuildIPv4(nic.IPs)
		if nic.Gateway || strings.EqualFold(nic.Name, nic.Switch) || !hvBuildNamePattern.MatchString(nic.Name) {
			continue
		}
		known := false
		for _, sw := range switches {
			if strings.EqualFold(sw.Name, nic.Switch) {
				known = true
			}
		}
		if !known {
			continue
		}
		role := hvBuildRoleOf(nic.Name)
		subnet := hvBuildSubnetKey(ip)
		if migrationSubnets[subnet] {
			role = "livemigration"
		}
		for _, network := range clusterNets {
			if subnet == "" || hvBuildSubnetKey(network.Address+"/"+strconv.Itoa(hvBuildMaskBits(network.Mask))) != subnet {
				continue
			}
			switch {
			case network.Role == 0:
				role = "storage"
			case network.Migration && role != "storage" && len(clusterNets) > 1 && hvBuildOnlyMigration(clusterNets, network):
				role = "livemigration"
			}
		}
		vlan, err := hvBuildCheckVLAN(nic.VLAN, nic.Name)
		if err != nil {
			vlan = ""
		}
		nics = append(nics, hvBuildHostNIC{Name: nic.Name, Switch: nic.Switch, VLAN: vlan, Role: role})
		templateIPs[nic.Name] = ip
	}
	nicMap := map[string]string{}
	for _, old := range plan.HostNICs {
		for _, nic := range nics {
			if strings.EqualFold(nic.Name, old.Name) {
				nicMap[old.Name] = nic.Name
			}
		}
		if nicMap[old.Name] != "" {
			continue
		}
		for _, nic := range nics {
			taken := false
			for _, used := range nicMap {
				if used == nic.Name {
					taken = true
				}
			}
			if nic.Role == old.Role && !taken {
				nicMap[old.Name] = nic.Name
				break
			}
		}
		if nicMap[old.Name] == "" && len(plan.Nodes) > 0 {
			notes = append(notes, fmt.Sprintf("The existing nodes have no %s adapter like the drafted %s, so it is left out.", hvBuildRoleNames()[old.Role], old.Name))
		}
	}
	for index := range plan.Nodes {
		node := &plan.Nodes[index]
		ips := map[string]string{}
		for name, ip := range node.HostIPs {
			if target := nicMap[name]; target != "" && ip != "" {
				ips[target] = ip
			}
		}
		node.HostIPs = ips
	}
	for index := range nics {
		nics[index].From = ""
		for old, target := range nicMap {
			if target == nics[index].Name {
				for _, drafted := range plan.HostNICs {
					if drafted.Name == old {
						nics[index].From = drafted.From
					}
				}
			}
		}
	}
	plan.HostNICs = nics

	// Storage: what the existing host is connected to.
	storage := t.Storage
	switch {
	case len(storage.Portals) > 0:
		portals := []string{}
		for _, portal := range storage.Portals {
			if clean, err := hvBuildCheckPortal(portal); err == nil {
				portals = hvBuildAddUnique(portals, clean)
			}
		}
		plan.Storage.Kind, plan.Storage.Portals, plan.Storage.MPIO = "iscsi", portals, storage.MPIO
		if !storage.MPIO {
			notes = append(notes, t.Computer+" uses iSCSI without Multipath I/O; the new nodes match it.")
		}
		if storage.Initiator != "" {
			notes = append(notes, "Give each new node's iSCSI initiator name (shown after Read nodes) access to the same LUNs as "+t.Computer+" ("+storage.Initiator+") on the array.")
		}
	case storage.FC:
		plan.Storage.Kind, plan.Storage.MPIO = "fc", storage.MPIO
		arrays := []hvBuildArray{}
		for _, array := range storage.Arrays {
			item := hvBuildArray{Vendor: strings.TrimSpace(array.Vendor), Product: strings.TrimSpace(array.Product)}
			if hvBuildArrayPattern.MatchString(item.Vendor) && hvBuildArrayPattern.MatchString(item.Product) {
				arrays = append(arrays, item)
			}
		}
		if len(arrays) > 0 {
			plan.Storage.Arrays = arrays
		}
		notes = append(notes, "Zone each new node's Fibre Channel ports (WWPNs shown after Read nodes) to the same LUNs as "+t.Computer+".")
	default:
		plan.Storage.Kind, plan.Storage.MPIO, plan.Storage.Portals = "none", false, nil
		notes = append(notes, t.Computer+" uses no iSCSI or Fibre Channel storage; storage is left as it is.")
	}
	if plan.Storage.Portals == nil {
		plan.Storage.Portals = []string{}
	}
	if plan.Storage.Arrays == nil {
		plan.Storage.Arrays = []hvBuildArray{}
	}
	plan.Storage.Disks = []hvBuildDisk{}

	// Live migration and default folders.
	if h := t.Host; h != nil {
		settings := hvBuildSettings{MigrationAuth: "Kerberos", MaxMigrations: h.Max, MaxStorageMigrations: h.MaxStorage, Performance: h.Performance}
		if strings.EqualFold(h.Auth, "CredSSP") {
			settings.MigrationAuth = "CredSSP"
		}
		if settings.MaxMigrations < 1 || settings.MaxMigrations > 16 {
			settings.MaxMigrations = 2
		}
		if settings.MaxStorageMigrations < 1 || settings.MaxStorageMigrations > 16 {
			settings.MaxStorageMigrations = 2
		}
		switch strings.ToUpper(settings.Performance) {
		case "SMB":
			settings.Performance = "SMB"
		case "TCPIP":
			settings.Performance = "TCPIP"
		default:
			settings.Performance = "Compression"
		}
		if clean, err := hvRemoteCheckLocalPath(h.VMPath); err == nil {
			settings.VMPath = clean
		}
		if clean, err := hvRemoteCheckLocalPath(h.VHDPath); err == nil {
			settings.VHDPath = clean
		}
		plan.Settings = settings
	}

	existing := &hvBuildExisting{Kind: read.Kind, Name: t.Computer, ReadFrom: vmNonEmpty(read.ReadFrom, t.Computer), Members: []hvBuildMember{}, CSVs: []hvBuildClusterCSV{}, ReadAt: read.At}
	if join {
		c := read.Cluster
		existing.Name = c.Name
		for _, member := range c.Nodes {
			if hvRemoteNodePattern.MatchString(member.Name) {
				existing.Members = append(existing.Members, member)
			}
		}
		existing.CSVs = append(existing.CSVs, c.CSVs...)
		existing.Witness = hvBuildWitnessFacts(c)
		plan.Cluster.Enabled, plan.Cluster.Join = true, true
		plan.Cluster.Name = c.Name
		plan.Cluster.Addresses = []string{}
		for _, address := range c.Addresses {
			if clean, err := hvBuildCheckIPv4(address); err == nil {
				plan.Cluster.Addresses = hvBuildAddUnique(plan.Cluster.Addresses, clean)
			}
		}
		plan.Cluster.StorageTests = false
		// Nodes already in the cluster are not built again.
		kept := []hvBuildNode{}
		for _, node := range plan.Nodes {
			member := ""
			for _, item := range existing.Members {
				if hvBuildSameNode(node, item.Name) {
					member = item.Name
				}
			}
			if member != "" {
				notes = append(notes, fmt.Sprintf("%s is already a node of cluster %s, so it is left out of the plan.", vmNonEmpty(node.Name, node.ESXi), c.Name))
				continue
			}
			kept = append(kept, node)
		}
		plan.Nodes = kept
		if len(plan.Nodes) == 0 {
			plan.Nodes = []hvBuildNode{{Switches: map[string][]string{}, HostIPs: map[string]string{}, Storage: []hvBuildNodeNIC{}}}
			for _, sw := range plan.Switches {
				plan.Nodes[0].Switches[sw.Name] = []string{}
			}
		}
		if plan.Name == "" || base == nil || plan.Name == hvBuildCleanName(plan.SourceCluster) {
			plan.Name = "Join " + c.Name
		}
		if len(c.CSVs) > 0 {
			names := []string{}
			for _, csv := range c.CSVs {
				names = append(names, csv.Name)
			}
			notes = append(notes, fmt.Sprintf("Cluster %s keeps its Cluster Shared Volumes (%s) and its %s; joining adds nodes only.", c.Name, strings.Join(names, ", "), vmNonEmpty(strings.ToLower(existing.Witness), "quorum")))
		}
	} else {
		// The host becomes the plan's first node, configured as it is.
		host := hvBuildNode{Name: vmNonEmpty(t.DNSName, t.Computer), ExistingHost: true, Switches: map[string][]string{}, HostIPs: map[string]string{}, Storage: []hvBuildNodeNIC{}}
		if len(read.Names) > 0 && hvRemoteNodePattern.MatchString(read.Names[0]) && hvBuildShort(read.Names[0]) == hvBuildShort(t.Computer) {
			host.Name = read.Names[0]
		}
		for _, sw := range t.Switches {
			for _, target := range plan.Switches {
				if target.Name == sw.Name {
					host.Switches[sw.Name] = append([]string{}, sw.Adapters...)
				}
			}
		}
		for _, nic := range plan.HostNICs {
			if ip := templateIPs[nic.Name]; ip != "" {
				host.HostIPs[nic.Name] = ip
			}
		}
		for _, adapter := range storage.Adapters {
			if hvBuildAdapterPattern.MatchString(adapter.Adapter) {
				ip, _ := hvBuildCheckIPv4CIDR(adapter.IP)
				host.Storage = append(host.Storage, hvBuildNodeNIC{Adapter: adapter.Adapter, IP: ip})
			}
		}
		nodes := []hvBuildNode{host}
		extras := []string{}
		if len(read.Names) > 1 {
			extras = read.Names[1:]
		}
		for _, extra := range extras {
			if !hvRemoteNodePattern.MatchString(extra) {
				continue
			}
			node := hvBuildNode{Name: extra, ExistingHost: true, Switches: map[string][]string{}, HostIPs: map[string]string{}, Storage: []hvBuildNodeNIC{}}
			for _, sw := range plan.Switches {
				node.Switches[sw.Name] = []string{}
			}
			nodes = append(nodes, node)
		}
		for _, node := range plan.Nodes {
			taken := false
			for _, item := range nodes {
				if hvBuildSameNode(node, item.Name) {
					taken = true
				}
			}
			if !taken && (node.Name != "" || node.ESXi != "") {
				nodes = append(nodes, node)
			}
		}
		if len(nodes) > hvBuildMaxNodes {
			nodes = nodes[:hvBuildMaxNodes]
		}
		plan.Nodes = nodes
		plan.Cluster.Join = false
		plan.Cluster.Enabled = len(plan.Nodes) > 1
		if plan.Cluster.Witness == "" {
			plan.Cluster.Witness = "fileshare"
		}
		if plan.Name == "" {
			plan.Name = hvBuildCleanName(t.Computer)
		}
		notes = append(notes, fmt.Sprintf("%s is already a Hyper-V host: the plan uses its switches, host adapters, storage and live migration settings, so its own steps list nothing to change, and the other nodes are built to match it.", t.Computer))
	}
	for _, warning := range t.Warnings {
		notes = append(notes, t.Computer+": "+warning)
	}
	existing.HostIPs = map[string]string{}
	for _, nic := range plan.HostNICs {
		if ip := templateIPs[nic.Name]; ip != "" {
			existing.HostIPs[nic.Name] = ip
		}
	}
	plan.Existing = existing
	plan.Notes = append(notes, plan.Notes...)
	return plan, nil
}

// hvBuildOnlyMigration reports whether network is the one network live
// migration may use (every other network is excluded).
func hvBuildOnlyMigration(networks []hvBuildClusterNetwork, network hvBuildClusterNetwork) bool {
	allowed := 0
	for _, item := range networks {
		if item.Migration {
			allowed++
		}
	}
	return allowed == 1 && network.Migration
}

func hvBuildWitnessFacts(c *hvBuildClusterFacts) string {
	switch strings.ToLower(c.WitnessType) {
	case "physical disk":
		return "Disk witness " + c.Witness
	case "file share witness":
		return "File share witness " + vmNonEmpty(c.WitnessPath, c.Witness)
	case "cloud witness":
		return "Cloud witness " + vmNonEmpty(c.WitnessPath, c.Witness)
	case "":
		return "No witness"
	}
	return c.WitnessType + " " + c.Witness
}

// hvBuildMatchSwitch picks the existing switch a drafted switch becomes:
// the same name, the same name without SET-, the only one, the one that
// keeps the management address, or the first.
func hvBuildMatchSwitch(sw hvBuildSwitch, existing []hvBuildSwitch) string {
	plain := func(name string) string {
		name = strings.ToLower(strings.TrimSpace(name))
		return strings.TrimPrefix(name, "set-")
	}
	for _, item := range existing {
		if strings.EqualFold(item.Name, sw.Name) {
			return item.Name
		}
	}
	for _, item := range existing {
		if plain(item.Name) == plain(sw.Name) {
			return item.Name
		}
		for _, from := range sw.From {
			if plain(item.Name) == plain(from) {
				return item.Name
			}
		}
	}
	if len(existing) == 1 {
		return existing[0].Name
	}
	for _, item := range existing {
		if item.ManagementOS == sw.ManagementOS {
			return item.Name
		}
	}
	return existing[0].Name
}

// hvBuildNextNode is the first waiting node, or -1.
func hvBuildNextNode(plan hvBuildPlan) int {
	for index, node := range plan.Nodes {
		if node.Later {
			return index
		}
	}
	return -1
}
