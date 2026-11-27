package main

// Validation of a Hyper-V build plan, the ordered steps and the PowerShell
// each step runs (see hvbuild.go).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	hvBuildMaxNodes = 16
	// hvBuildHere is the step target meaning this computer (the admin
	// computer running the application).
	hvBuildHere = ""
)

// Where a step runs.
const (
	hvWhereEach    = "each"    // every node, through PowerShell remoting
	hvWhereFirst   = "first"   // the first node only
	hvWhereHere    = "here"    // this computer, with the signed-in account
	hvWhereRestart = "restart" // nodes that wait for a restart
)

type hvBuildStepInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Where  string `json:"where"`
	// Missing says what the plan still needs before the step can run.
	Missing string `json:"missing"`
}

// hvBuildCheckIPv4CIDR checks "10.0.0.11/24".
func hvBuildCheckIPv4CIDR(value string) (string, error) {
	value = strings.TrimSpace(value)
	ip, network, err := net.ParseCIDR(value)
	if err != nil || ip.To4() == nil {
		return "", fmt.Errorf("%q is not an IPv4 address with a prefix length, such as 10.0.20.11/24", value)
	}
	ones, _ := network.Mask.Size()
	if ones < 8 || ones > 30 || ip.Equal(network.IP) {
		return "", fmt.Errorf("%q is not a usable host address", value)
	}
	return ip.To4().String() + "/" + strconv.Itoa(ones), nil
}

func hvBuildCheckIPv4(value string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return "", fmt.Errorf("%q is not an IPv4 address", value)
	}
	return ip.To4().String(), nil
}

// hvBuildCheckPortal checks address:port (port defaults to 3260).
func hvBuildCheckPortal(value string) (string, error) {
	value = strings.TrimSpace(value)
	host, port := value, "3260"
	if index := strings.LastIndex(value, ":"); index > 0 {
		host, port = value[:index], value[index+1:]
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", fmt.Errorf("the iSCSI portal %q has no valid port", value)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return "", fmt.Errorf("type the iSCSI portal %q as an IPv4 address or a DNS name", value)
		}
		host = ip.To4().String()
	} else if !hvRemoteNodePattern.MatchString(host) {
		return "", fmt.Errorf("the iSCSI portal %q is not an address or DNS name", value)
	}
	return host + ":" + strconv.Itoa(number), nil
}

func hvBuildSplitPortal(portal string) (string, int) {
	index := strings.LastIndex(portal, ":")
	number, _ := strconv.Atoi(portal[index+1:])
	return portal[:index], number
}

func hvBuildCheckVLAN(value, what string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if hvBuildVLAN(value) == "" {
		return "", fmt.Errorf("%s: VLAN %q must be a number from 1 to 4094", what, value)
	}
	return hvBuildVLAN(value), nil
}

// hvBuildCheckPlan checks every value of a plan that is being saved. Missing
// values are allowed while the plan is drafted (hvBuildSteps says what each
// step still needs); values that are present must be valid.
func hvBuildCheckPlan(plan hvBuildPlan) (hvBuildPlan, error) {
	plan.Schema = hvBuildSchema
	plan.Name = strings.TrimSpace(plan.Name)
	if !hvBuildNamePattern.MatchString(plan.Name) {
		return plan, errors.New("give the plan a name (letters, digits, spaces, . _ - and parentheses)")
	}
	if len(plan.Nodes) == 0 || len(plan.Nodes) > hvBuildMaxNodes {
		return plan, fmt.Errorf("a plan has 1 to %d nodes", hvBuildMaxNodes)
	}
	switchNames := map[string]bool{}
	for index := range plan.Switches {
		sw := &plan.Switches[index]
		sw.Name = strings.TrimSpace(sw.Name)
		if !hvBuildNamePattern.MatchString(sw.Name) {
			return plan, fmt.Errorf("switch name %q is not valid", sw.Name)
		}
		if switchNames[strings.ToLower(sw.Name)] {
			return plan, fmt.Errorf("two switches are named %s", sw.Name)
		}
		switchNames[strings.ToLower(sw.Name)] = true
		var err error
		if sw.ManagementVLAN, err = hvBuildCheckVLAN(sw.ManagementVLAN, sw.Name+" management"); err != nil {
			return plan, err
		}
		if sw.ManagementVLAN != "" && !sw.ManagementOS {
			sw.ManagementVLAN = ""
		}
		if sw.SourceVLAN, err = hvBuildCheckVLAN(sw.SourceVLAN, sw.Name); err != nil {
			sw.SourceVLAN = ""
		}
		speeds := []int{}
		for _, speed := range sw.UplinkSpeedsMb {
			if speed > 0 && speed <= 1000000 && len(speeds) < 16 {
				speeds = append(speeds, speed)
			}
		}
		sw.UplinkSpeedsMb = speeds
		if sw.LoadBalancing != "Dynamic" {
			sw.LoadBalancing = "HyperVPort"
		}
		if sw.MTU != 9000 {
			sw.MTU = 1500
		}
	}
	if len(plan.Switches) == 0 {
		return plan, errors.New("a Hyper-V host needs at least one virtual switch")
	}
	nicNames := map[string]bool{}
	for index := range plan.HostNICs {
		nic := &plan.HostNICs[index]
		nic.Name = strings.TrimSpace(nic.Name)
		if !hvBuildNamePattern.MatchString(nic.Name) {
			return plan, fmt.Errorf("host adapter name %q is not valid", nic.Name)
		}
		if nicNames[strings.ToLower(nic.Name)] || switchNames[strings.ToLower(nic.Name)] {
			return plan, fmt.Errorf("the name %s is used twice; each switch and host adapter needs its own name", nic.Name)
		}
		nicNames[strings.ToLower(nic.Name)] = true
		if !switchNames[strings.ToLower(nic.Switch)] {
			return plan, fmt.Errorf("host adapter %s is on a switch the plan does not have", nic.Name)
		}
		if nic.Role != "livemigration" && nic.Role != "cluster" && nic.Role != "storage" {
			return plan, fmt.Errorf("host adapter %s needs a role: live migration, cluster or storage", nic.Name)
		}
		var err error
		if nic.VLAN, err = hvBuildCheckVLAN(nic.VLAN, nic.Name); err != nil {
			return plan, err
		}
	}
	nodeNames := map[string]bool{}
	for index := range plan.Nodes {
		node := &plan.Nodes[index]
		node.Name = strings.TrimSpace(node.Name)
		if node.Name != "" {
			if !hvRemoteNodePattern.MatchString(node.Name) {
				return plan, fmt.Errorf("node name %q is not a valid computer or DNS name", node.Name)
			}
			short := strings.ToLower(strings.SplitN(node.Name, ".", 2)[0])
			if nodeNames[short] {
				return plan, fmt.Errorf("node %s is listed twice", node.Name)
			}
			nodeNames[short] = true
		}
		used := map[string]string{}
		clean := map[string][]string{}
		for name, adapters := range node.Switches {
			if !switchNames[strings.ToLower(name)] {
				continue
			}
			list := []string{}
			for _, adapter := range adapters {
				adapter = strings.TrimSpace(adapter)
				if adapter == "" {
					continue
				}
				if !hvBuildAdapterPattern.MatchString(adapter) {
					return plan, fmt.Errorf("adapter name %q on %s is not valid", adapter, hvBuildNodeLabel(*node, index))
				}
				if owner, ok := used[strings.ToLower(adapter)]; ok {
					return plan, fmt.Errorf("adapter %s on %s is chosen for both %s and %s", adapter, hvBuildNodeLabel(*node, index), owner, name)
				}
				used[strings.ToLower(adapter)] = name
				list = append(list, adapter)
			}
			clean[name] = list
		}
		node.Switches = clean
		ips := map[string]string{}
		for name, value := range node.HostIPs {
			if !nicNames[strings.ToLower(name)] || strings.TrimSpace(value) == "" {
				continue
			}
			ip, err := hvBuildCheckIPv4CIDR(value)
			if err != nil {
				return plan, fmt.Errorf("%s on %s: %v", name, hvBuildNodeLabel(*node, index), err)
			}
			ips[name] = ip
		}
		node.HostIPs = ips
		nics := map[string][]hvBuildSourceNIC{}
		for name, list := range node.SourceNICs {
			if !switchNames[strings.ToLower(name)] {
				continue
			}
			clean := []hvBuildSourceNIC{}
			for _, nic := range list {
				if len(clean) < 32 && hvBuildMACPattern.MatchString(nic.MAC) && len(nic.Device) <= 64 && hvBuildAdapterPattern.MatchString(vmNonEmpty(nic.Device, "x")) {
					nic.MAC = hvBuildNormalMAC(nic.MAC)
					clean = append(clean, nic)
				}
			}
			nics[name] = clean
		}
		node.SourceNICs = nics
		if net.ParseIP(node.ManagementIP) == nil {
			node.ManagementIP = ""
		}
		storage := []hvBuildNodeNIC{}
		for _, nic := range node.Storage {
			nic.Adapter = strings.TrimSpace(nic.Adapter)
			if nic.Adapter == "" {
				continue
			}
			if !hvBuildAdapterPattern.MatchString(nic.Adapter) {
				return plan, fmt.Errorf("storage adapter name %q is not valid", nic.Adapter)
			}
			if owner, ok := used[strings.ToLower(nic.Adapter)]; ok {
				return plan, fmt.Errorf("adapter %s on %s is chosen for both %s and iSCSI storage", nic.Adapter, hvBuildNodeLabel(*node, index), owner)
			}
			used[strings.ToLower(nic.Adapter)] = "iSCSI storage"
			if strings.TrimSpace(nic.IP) != "" {
				ip, err := hvBuildCheckIPv4CIDR(nic.IP)
				if err != nil {
					return plan, fmt.Errorf("storage adapter %s on %s: %v", nic.Adapter, hvBuildNodeLabel(*node, index), err)
				}
				nic.IP = ip
			}
			storage = append(storage, nic)
		}
		node.Storage = storage
		node.Suggested = hvBuildCleanSuggested(node.Suggested)
		iscsi := []string{}
		for _, ip := range node.SourceISCSI {
			if clean, err := hvBuildCheckIPv4CIDR(ip); err == nil && len(iscsi) < 8 {
				iscsi = append(iscsi, clean)
			}
		}
		node.SourceISCSI = iscsi
	}
	for index := range plan.Networks {
		network := &plan.Networks[index]
		if network.Switch != "" && !switchNames[strings.ToLower(network.Switch)] {
			return plan, fmt.Errorf("port group %s goes to a switch the plan does not have", network.Portgroup)
		}
		var err error
		if network.VLAN, err = hvBuildCheckVLAN(network.VLAN, network.Portgroup); err != nil {
			return plan, err
		}
	}
	storage := &plan.Storage
	switch storage.Kind {
	case "iscsi", "fc":
	default:
		storage.Kind = "none"
	}
	portals := []string{}
	for _, portal := range storage.Portals {
		if strings.TrimSpace(portal) == "" {
			continue
		}
		clean, err := hvBuildCheckPortal(portal)
		if err != nil {
			return plan, err
		}
		portals = hvBuildAddUnique(portals, clean)
	}
	storage.Portals = portals
	storage.TargetFilter = strings.TrimSpace(storage.TargetFilter)
	if !hvBuildFilterPattern.MatchString(storage.TargetFilter) {
		return plan, errors.New("the target name filter may hold only letters, digits and : . _ -")
	}
	for _, array := range storage.Arrays {
		if !hvBuildArrayPattern.MatchString(array.Vendor) || !hvBuildArrayPattern.MatchString(array.Product) {
			return plan, fmt.Errorf("array %q %q: vendor and product are up to 16 letters, digits, spaces and . _ -", array.Vendor, array.Product)
		}
	}
	labels := map[string]bool{}
	ids := map[string]bool{}
	witnessDisks := 0
	for index := range storage.Disks {
		disk := &storage.Disks[index]
		disk.UniqueID = strings.TrimSpace(disk.UniqueID)
		disk.Label = strings.TrimSpace(disk.Label)
		if !hvBuildDiskIDPattern.MatchString(disk.UniqueID) {
			return plan, errors.New("choose each disk from the first node's disk list")
		}
		if ids[strings.ToLower(disk.UniqueID)] {
			return plan, errors.New("a disk is chosen twice")
		}
		ids[strings.ToLower(disk.UniqueID)] = true
		if !hvBuildNamePattern.MatchString(disk.Label) || len(disk.Label) > 32 {
			return plan, fmt.Errorf("disk label %q is not valid (up to 32 letters, digits, spaces and . _ -)", disk.Label)
		}
		if labels[strings.ToLower(disk.Label)] {
			return plan, fmt.Errorf("two disks are labelled %s", disk.Label)
		}
		labels[strings.ToLower(disk.Label)] = true
		if disk.FileSystem != "NTFS" {
			disk.FileSystem = "ReFS"
		}
		switch disk.Use {
		case "csv", "volume":
		case "witness":
			witnessDisks++
			disk.FileSystem = "NTFS"
		default:
			disk.Use = "csv"
		}
		if !plan.Cluster.Enabled && disk.Use != "volume" {
			disk.Use = "volume"
		}
	}
	if witnessDisks > 1 {
		return plan, errors.New("choose one witness disk")
	}
	cluster := &plan.Cluster
	if len(plan.Nodes) > 1 || cluster.Join {
		cluster.Enabled = true
	}
	cluster.Name = strings.TrimSpace(cluster.Name)
	if cluster.Join && cluster.Name == "" {
		return plan, errors.New("type the name of the cluster the nodes join, or read it in step 1")
	}
	if cluster.Name != "" {
		if !hvBuildClusterPattern.MatchString(cluster.Name) || strings.Trim(cluster.Name, "0123456789") == "" {
			return plan, errors.New("the cluster name is its computer name in Active Directory: up to 15 letters, digits and hyphens, not only digits")
		}
	}
	addresses := []string{}
	for _, address := range cluster.Addresses {
		if strings.TrimSpace(address) == "" {
			continue
		}
		clean, err := hvBuildCheckIPv4(address)
		if err != nil {
			return plan, fmt.Errorf("cluster address: %v", err)
		}
		addresses = hvBuildAddUnique(addresses, clean)
	}
	cluster.Addresses = addresses
	switch cluster.Witness {
	case "disk", "fileshare", "cloud", "none":
	default:
		cluster.Witness = "fileshare"
	}
	cluster.WitnessShare = strings.TrimSpace(cluster.WitnessShare)
	if cluster.WitnessShare != "" && !hvBuildSharePattern.MatchString(cluster.WitnessShare) {
		return plan, errors.New("type the witness share as \\\\server\\share")
	}
	cluster.CloudAccount = strings.ToLower(strings.TrimSpace(cluster.CloudAccount))
	if cluster.CloudAccount != "" && !hvBuildAccountPattern.MatchString(cluster.CloudAccount) {
		return plan, errors.New("the Azure storage account name is 3 to 24 lowercase letters and digits")
	}
	settings := &plan.Settings
	if settings.MigrationAuth != "CredSSP" {
		settings.MigrationAuth = "Kerberos"
	}
	if settings.MaxMigrations < 1 || settings.MaxMigrations > 16 {
		settings.MaxMigrations = 2
	}
	if settings.MaxStorageMigrations < 1 || settings.MaxStorageMigrations > 16 {
		settings.MaxStorageMigrations = 2
	}
	switch settings.Performance {
	case "SMB", "TCPIP":
	default:
		settings.Performance = "Compression"
	}
	for _, path := range []*string{&settings.VMPath, &settings.VHDPath} {
		if strings.TrimSpace(*path) == "" {
			*path = ""
			continue
		}
		clean, err := hvRemoteCheckLocalPath(*path)
		if err != nil {
			return plan, fmt.Errorf("default folder: %v", err)
		}
		*path = clean
	}
	if plan.Notes == nil {
		plan.Notes = []string{}
	}
	plan.Suggested = hvBuildCleanSuggested(plan.Suggested)
	if existing := plan.Existing; existing != nil {
		members := []hvBuildMember{}
		for _, member := range existing.Members {
			if hvRemoteNodePattern.MatchString(member.Name) && len(members) < 64 {
				members = append(members, member)
			}
		}
		existing.Members = members
		if existing.Kind != "join" {
			existing.Kind = "takein"
		}
		ips := map[string]string{}
		for name, ip := range existing.HostIPs {
			if clean, err := hvBuildCheckIPv4CIDR(ip); err == nil && nicNames[strings.ToLower(name)] {
				ips[name] = clean
			}
		}
		existing.HostIPs = ips
	}
	plan.UpdatedAt = time.Now().UTC()
	return plan, nil
}

func hvBuildNodeLabel(node hvBuildNode, index int) string {
	if node.Name != "" {
		return node.Name
	}
	return fmt.Sprintf("node %d", index+1)
}

// hvBuildRevision identifies a plan's content: an approval is only valid for
// the revision it was previewed with.
func hvBuildRevision(plan hvBuildPlan) string {
	plan.UpdatedAt = time.Time{}
	data, _ := json.Marshal(plan)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hvBuildChangeHash is the SHA-256 of a step's change list, as the step
// script computes it: the lines joined with a line feed, UTF-8.
func hvBuildChangeHash(changes []string) string {
	sum := sha256.Sum256([]byte(strings.Join(changes, "\n")))
	return hex.EncodeToString(sum[:])
}

func hvBuildRoleNames() map[string]string {
	return map[string]string{"livemigration": "live migration", "cluster": "cluster", "storage": "storage"}
}

// hvBuildSteps lists the plan's steps in the order they run.
func hvBuildSteps(full hvBuildPlan) []hvBuildStepInfo {
	steps := []hvBuildStepInfo{}
	add := func(id, title, detail, where, missing string) {
		steps = append(steps, hvBuildStepInfo{ID: id, Title: title, Detail: detail, Where: where, Missing: missing})
	}
	// Steps work on the nodes that are ready; waiting nodes join later.
	plan := hvBuildActive(full)
	nodesMissing := ""
	if len(plan.Nodes) == 0 {
		nodesMissing = "Every node still waits to be converted: mark the next one ready in step 2."
	}
	for index, node := range plan.Nodes {
		if node.Name == "" {
			nodesMissing = fmt.Sprintf("Type the name of node %d.", index+1)
			break
		}
	}
	join := plan.Cluster.Join
	waiting := len(full.Nodes) - len(plan.Nodes)
	clustered := plan.Cluster.Enabled
	if clustered {
		add("localtools", "Cluster tools on this computer", "The Failover Cluster Module for Windows PowerShell, which the cluster steps use from here.", hvWhereHere, "")
	}
	roles := "Hyper-V and its PowerShell module"
	if clustered {
		roles += ", Failover Clustering"
	}
	if plan.Storage.MPIO && plan.Storage.Kind != "none" {
		roles += ", Multipath I/O"
	}
	add("roles", "Install roles", roles+".", hvWhereEach, nodesMissing)
	add("restart", "Restart nodes that need it", "Only nodes that wait for a restart after installing roles; one at a time.", hvWhereRestart, nodesMissing)
	networkMissing := nodesMissing
	if networkMissing == "" {
		for index, node := range plan.Nodes {
			for _, sw := range plan.Switches {
				if len(node.Switches[sw.Name]) == 0 {
					networkMissing = fmt.Sprintf("Choose the physical adapters for %s on %s.", sw.Name, hvBuildNodeLabel(node, index))
					break
				}
			}
			for _, nic := range plan.HostNICs {
				if networkMissing == "" && node.HostIPs[nic.Name] == "" {
					networkMissing = fmt.Sprintf("Type %s's address on %s.", hvBuildNodeLabel(node, index), nic.Name)
				}
			}
			if networkMissing != "" {
				break
			}
		}
	}
	add("network", "Virtual switches and host adapters", hvBuildNetworkSummary(plan), hvWhereEach, networkMissing)
	if plan.Storage.Kind != "none" {
		missing := nodesMissing
		if missing == "" && plan.Storage.Kind == "iscsi" && len(plan.Storage.Portals) == 0 {
			missing = "Type the array's iSCSI portal addresses."
		}
		if missing == "" && plan.Storage.Kind == "iscsi" {
			for index, node := range plan.Nodes {
				for _, nic := range node.Storage {
					if nic.IP == "" {
						missing = fmt.Sprintf("Type the address of storage adapter %s on %s.", nic.Adapter, hvBuildNodeLabel(node, index))
					}
				}
			}
		}
		detail := "Fibre Channel: Microsoft MPIO claims the array's disks."
		if plan.Storage.Kind == "iscsi" {
			detail = "iSCSI: the initiator service, the portals " + strings.Join(plan.Storage.Portals, ", ") + " and persistent sessions"
			if plan.Storage.MPIO {
				detail += " with multipath"
			}
			detail += "."
		}
		add("storage", "Connect storage", detail, hvWhereEach, missing)
	}
	if len(plan.Storage.Disks) > 0 {
		add("disks", "Prepare disks", fmt.Sprintf("Initialize and format %d empty disk(s) on the first node. Disks that hold partitions are never changed.", len(plan.Storage.Disks)), hvWhereFirst, nodesMissing)
	}
	if clustered {
		clusterMissing := nodesMissing
		if clusterMissing == "" && !join && len(full.Nodes) < 2 {
			clusterMissing = "A failover cluster needs at least two nodes."
		}
		validateDetail := "Runs Microsoft's cluster validation and saves its report. Changes nothing."
		if members := hvBuildMembers(plan); join && len(members) > 0 {
			validateDetail = "Runs Microsoft's cluster validation on " + strings.Join(members, ", ") + " and the new nodes, without the Storage tests, and saves its report. Changes nothing."
		}
		add("validate", "Validate the cluster", validateDetail, hvWhereHere, clusterMissing)
		if clusterMissing == "" && plan.Cluster.Name == "" {
			clusterMissing = "Type the cluster name."
		}
		if clusterMissing == "" && !join && len(plan.Cluster.Addresses) == 0 {
			clusterMissing = "Type the cluster's IP address."
		}
		if join {
			add("cluster", "Join the cluster", "Adds "+strings.Join(hvBuildNodeNames(plan), ", ")+" to "+vmNonEmpty(plan.Cluster.Name, "the cluster")+" without storage. Never creates a cluster or changes its quorum, networks or volumes.", hvWhereHere, clusterMissing)
		} else {
			detail := "Creates " + vmNonEmpty(plan.Cluster.Name, "the cluster") + " without storage, or adds missing nodes to it."
			if waiting > 0 {
				detail += fmt.Sprintf(" The %d node(s) still waiting join it when they are ready.", waiting)
			}
			add("cluster", "Create the cluster", detail, hvWhereHere, clusterMissing)
		}
		csvCount := 0
		for _, disk := range plan.Storage.Disks {
			if disk.Use == "csv" || disk.Use == "witness" {
				csvCount++
			}
		}
		if csvCount > 0 {
			add("csv", "Cluster Shared Volumes", "Adds the prepared disks to the cluster and makes them Cluster Shared Volumes.", hvWhereHere, clusterMissing)
		}
		quorumMissing := clusterMissing
		if quorumMissing == "" {
			switch plan.Cluster.Witness {
			case "disk":
				if !hvBuildHasWitnessDisk(plan) {
					quorumMissing = "Choose a small disk as the witness disk, or another witness."
				}
			case "fileshare":
				if plan.Cluster.WitnessShare == "" {
					quorumMissing = "Type the witness share."
				}
			case "cloud":
				if plan.Cluster.CloudAccount == "" {
					quorumMissing = "Type the Azure storage account for the cloud witness."
				}
			}
		}
		if !join {
			add("quorum", "Quorum witness", hvBuildWitnessText(plan.Cluster), hvWhereHere, quorumMissing)
			add("clusternetworks", "Cluster networks", "Names the cluster networks after the host adapters, sets their roles and keeps live migration on its own network.", hvWhereHere, clusterMissing)
		}
	}
	add("hostsettings", "Live migration and default folders", fmt.Sprintf("%s authentication, %d at a time, %s.", plan.Settings.MigrationAuth, plan.Settings.MaxMigrations, plan.Settings.Performance), hvWhereEach, nodesMissing)
	return steps
}

func hvBuildHasWitnessDisk(plan hvBuildPlan) bool {
	for _, disk := range plan.Storage.Disks {
		if disk.Use == "witness" {
			return true
		}
	}
	return false
}

func hvBuildWitnessText(cluster hvBuildCluster) string {
	switch cluster.Witness {
	case "disk":
		return "A disk witness on the shared storage."
	case "fileshare":
		return "A file share witness at " + vmNonEmpty(cluster.WitnessShare, "a share") + "."
	case "cloud":
		return "A cloud witness in Azure storage account " + vmNonEmpty(cluster.CloudAccount, "(not set)") + "."
	}
	return "No witness (node majority only, not recommended)."
}

func hvBuildNetworkSummary(plan hvBuildPlan) string {
	parts := []string{}
	for _, sw := range plan.Switches {
		parts = append(parts, sw.Name)
	}
	text := "Switches " + strings.Join(parts, ", ")
	if len(plan.HostNICs) > 0 {
		names := []string{}
		for _, nic := range plan.HostNICs {
			names = append(names, nic.Name)
		}
		text += "; host adapters " + strings.Join(names, ", ")
	}
	return text + "."
}

// hvBuildStepByID returns a step of the plan.
func hvBuildStepByID(plan hvBuildPlan, id string) (hvBuildStepInfo, bool) {
	for _, step := range hvBuildSteps(plan) {
		if step.ID == id {
			return step, true
		}
	}
	return hvBuildStepInfo{}, false
}

// hvBuildStepComputers lists where a step runs: node names, or hvBuildHere.
func hvBuildStepComputers(plan hvBuildPlan, step hvBuildStepInfo) []string {
	plan = hvBuildActive(plan)
	if len(plan.Nodes) == 0 && step.Where != hvWhereHere {
		return []string{}
	}
	switch step.Where {
	case hvWhereHere:
		return []string{hvBuildHere}
	case hvWhereFirst:
		return []string{plan.Nodes[0].Name}
	}
	names := []string{}
	for _, node := range plan.Nodes {
		names = append(names, node.Name)
	}
	return names
}

func hvBuildNodeByName(plan hvBuildPlan, name string) (hvBuildNode, bool) {
	for _, node := range plan.Nodes {
		if strings.EqualFold(node.Name, name) {
			return node, true
		}
	}
	return hvBuildNode{}, false
}

func psList(values []string) string {
	quoted := []string{}
	for _, value := range values {
		quoted = append(quoted, vmPSQuote(value))
	}
	return "@(" + strings.Join(quoted, ", ") + ")"
}

func psBool(value bool) string {
	if value {
		return "$true"
	}
	return "$false"
}

// hvBuildPrelude starts every step script. Plan-Change records a change and
// the script block that makes it; Note records a fact or something left as
// it is; Block records why the step cannot run on this computer.
const hvBuildPrelude = `param([bool]$Apply = $false, [string]$Approved = '', $Secrets = $null)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
$plan = New-Object System.Collections.ArrayList
$notes = New-Object System.Collections.ArrayList
$blocks = New-Object System.Collections.ArrayList
$state = @{ restart = $false; report = ''; warnings = @() }
function Plan-Change { param([string]$Text, [scriptblock]$Do, $Argument = $null) [void]$plan.Add(@{ text = $Text; do = $Do; arg = $Argument }) }
function Note { param([string]$Text) [void]$notes.Add($Text) }
function Block { param([string]$Text) [void]$blocks.Add($Text) }
function Test-RestartPending {
    foreach ($key in @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending', 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired')) {
        if (Test-Path -LiteralPath $key) { return $true }
    }
    return $false
}
function Get-Build { return [int]([Environment]::OSVersion.Version.Build) }
function Test-HasAddress { param([string]$Alias, [string]$Address)
    return [bool](Get-NetIPAddress -InterfaceAlias $Alias -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -eq $Address })
}
function Set-StaticAddress { param($x)
    Set-NetIPInterface -InterfaceAlias $x.alias -Dhcp Disabled
    New-NetIPAddress -InterfaceAlias $x.alias -IPAddress $x.ip -PrefixLength $x.prefix | Out-Null
    Set-DnsClient -InterfaceAlias $x.alias -RegisterThisConnectionsAddress $false
}
function Get-JumboValue { param([string]$Name)
    $property = Get-NetAdapterAdvancedProperty -Name $Name -RegistryKeyword '*JumboPacket' -ErrorAction SilentlyContinue
    if (-not $property) { return -1 }
    return [int]([string]$property.RegistryValue)
}
`

// hvBuildEpilogue checks the approval and makes the changes in order,
// stopping at the first failure, then writes the result as one JSON line.
const hvBuildEpilogue = `
$texts = @($plan | ForEach-Object { [string]$_.text })
$sha = [System.Security.Cryptography.SHA256]::Create()
$hash = (($sha.ComputeHash([System.Text.Encoding]::UTF8.GetBytes(($texts -join [string][char]10))) | ForEach-Object { $_.ToString('x2') }) -join '')
$result = @{ computer = [string]$env:COMPUTERNAME; changes = $texts; notes = @($notes); blocks = @($blocks); hash = $hash; applied = @(); refused = ''; error = ''; restart = $false; report = ''; warnings = @() }
if ($Apply) {
    if ($blocks.Count -gt 0) {
        $result.refused = 'Nothing was changed: fix what is listed first.'
    } elseif ($hash -ne $Approved) {
        $result.refused = 'Nothing was changed: what this step would change is no longer what you approved. Preview it again.'
    } else {
        $applied = New-Object System.Collections.ArrayList
        foreach ($item in $plan) {
            try {
                & $item.do $item.arg | Out-Null
                [void]$applied.Add($item.text)
            } catch {
                $result.error = $item.text + ': ' + $_.Exception.Message
                break
            }
        }
        $result.applied = @($applied)
    }
}
$result.restart = [bool]($state.restart -or (Test-RestartPending))
$result.report = [string]$state.report
$result.warnings = @($state.warnings | ForEach-Object { [string]$_ })
ConvertTo-Json -InputObject $result -Depth 5 -Compress
`

// hvBuildScript returns the script of one step for one computer (a node
// name, or hvBuildHere).
func hvBuildScript(plan hvBuildPlan, stepID, computer string) (string, error) {
	step, ok := hvBuildStepByID(plan, stepID)
	if !ok {
		return "", errors.New("that step is not part of this plan")
	}
	if step.Missing != "" {
		return "", errors.New(step.Missing)
	}
	plan = hvBuildActive(plan)
	var body string
	switch stepID {
	case "localtools":
		body = hvBuildLocalToolsScript()
	case "roles":
		body = hvBuildRolesScript(plan)
	case "restart":
		body = hvBuildRestartScript()
	case "network":
		node, found := hvBuildNodeByName(plan, computer)
		if !found {
			return "", fmt.Errorf("%s is not a node of this plan", computer)
		}
		body = hvBuildNetworkScript(plan, node)
	case "storage":
		node, found := hvBuildNodeByName(plan, computer)
		if !found {
			return "", fmt.Errorf("%s is not a node of this plan", computer)
		}
		body = hvBuildStorageScript(plan, node)
	case "disks":
		body = hvBuildDisksScript(plan)
	case "validate":
		body = hvBuildValidateScript(plan)
	case "cluster":
		body = hvBuildClusterScript(plan)
	case "csv":
		body = hvBuildCSVScript(plan)
	case "quorum":
		body = hvBuildQuorumScript(plan)
	case "clusternetworks":
		body = hvBuildClusterNetworksScript(plan)
	case "hostsettings":
		body = hvBuildHostSettingsScript(plan)
	default:
		return "", errors.New("that step is not part of this plan")
	}
	return hvBuildPrelude + body + hvBuildEpilogue, nil
}

func hvBuildLocalToolsScript() string {
	return `
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
Note ('The cluster steps run on this computer (' + $env:COMPUTERNAME + ') with your Windows account ' + [Security.Principal.WindowsIdentity]::GetCurrent().Name + ', which must be an administrator on every node and allowed to create the cluster''s computer object in Active Directory (or use a prestaged one).')
if (Get-Module -ListAvailable -Name FailoverClusters) {
    Note 'The Failover Cluster Module for Windows PowerShell is installed here.'
} elseif (Get-Command Install-WindowsFeature -ErrorAction SilentlyContinue) {
    if (-not $admin) { Block 'Run the application as administrator to install the cluster module on this computer.' }
    Plan-Change 'Install the Windows feature Failover Cluster Module for Windows PowerShell (RSAT-Clustering-PowerShell) on this computer' { Install-WindowsFeature -Name RSAT-Clustering-PowerShell | Out-Null }
} elseif (Get-Command Add-WindowsCapability -ErrorAction SilentlyContinue) {
    if (-not $admin) { Block 'Run the application as administrator to add the cluster tools on this computer.' }
    Plan-Change 'Add the optional feature RSAT: Failover Clustering Tools on this computer (Windows downloads it from Windows Update or your update server)' { Add-WindowsCapability -Online -Name 'Rsat.FailoverCluster.Management.Tools~~~~0.0.1.0' | Out-Null }
} else {
    Block 'This computer cannot install the Failover Cluster Module for Windows PowerShell. Run the cluster steps from Windows Server 2012 R2 or later, or Windows 10 1809 or later.'
}
$system = Get-WmiObject -Class Win32_ComputerSystem
if (-not $system.PartOfDomain) { Block 'This computer is not in a domain. Run the cluster steps from a computer in the nodes'' domain.' }
`
}

func hvBuildRolesScript(plan hvBuildPlan) string {
	features := []string{"Hyper-V", "Hyper-V-PowerShell"}
	if plan.Cluster.Enabled {
		features = append(features, "Failover-Clustering", "RSAT-Clustering-PowerShell")
	}
	if plan.Storage.MPIO && plan.Storage.Kind != "none" {
		features = append(features, "Multipath-IO")
	}
	return `
if (-not (Get-Command Get-WindowsFeature -ErrorAction SilentlyContinue)) { Block 'This is not Windows Server: Hyper-V hosts must run Windows Server 2012 R2 or later (Install-WindowsFeature is missing).' }
else {
    $os = Get-WmiObject -Class Win32_OperatingSystem
    if ([int]$os.BuildNumber -lt 9600) { Block ($os.Caption + ' is older than Windows Server 2012 R2.') }
    $system = Get-WmiObject -Class Win32_ComputerSystem
    $cpu = @(Get-WmiObject -Class Win32_Processor)[0]
    if (-not $system.HypervisorPresent -and $cpu -and -not $cpu.VirtualizationFirmwareEnabled) { Block 'Hardware virtualization is turned off in this server''s firmware (BIOS/UEFI). Turn on Intel VT-x or AMD-V and restart.' }
` + func() string {
		if plan.Cluster.Enabled {
			return "    if (-not $system.PartOfDomain) { Block 'This node is not joined to a domain. Join every node to the same Active Directory domain before creating a failover cluster.' }\n"
		}
		return ""
	}() + `    foreach ($name in ` + psList(features) + `) {
        $feature = Get-WindowsFeature -Name $name
        if (-not $feature) { Note ($name + ' is not available on this edition.'); continue }
        if ([string]$feature.InstallState -eq 'InstallPending') { Note ($feature.DisplayName + ' is installed and waits for a restart.'); $state.restart = $true }
        elseif ($feature.Installed) { Note ($feature.DisplayName + ' is installed.') }
        else {
            Plan-Change ('Install the Windows feature ' + $feature.DisplayName + ' (' + $name + ')') { param($n) $r = Install-WindowsFeature -Name $n; if ([string]$r.RestartNeeded -eq 'Yes') { $state.restart = $true } } $name
        }
    }
}
`
}

func hvBuildRestartScript() string {
	return `
if (Test-RestartPending) {
    Plan-Change ('Restart ' + $env:COMPUTERNAME + ' (it waits for a restart to finish installing)') { $state.restart = $true }
} else {
    $pending = @(Get-WindowsFeature -ErrorAction SilentlyContinue | Where-Object { [string]$_.InstallState -eq 'InstallPending' })
    if ($pending.Count -gt 0) { Plan-Change ('Restart ' + $env:COMPUTERNAME + ' to finish installing ' + (($pending | ForEach-Object { $_.DisplayName }) -join ', ')) { $state.restart = $true } }
    else { Note 'No restart is needed.' }
}
`
}

func hvBuildNetworkScript(plan hvBuildPlan, node hvBuildNode) string {
	var s strings.Builder
	s.WriteString(`
if (-not (Get-Command New-VMSwitch -ErrorAction SilentlyContinue)) { Block 'Install roles first: the Hyper-V module is missing (and restart the node).' }
else {
$build = Get-Build
`)
	for _, sw := range plan.Switches {
		adapters := node.Switches[sw.Name]
		fmt.Fprintf(&s, `
$swName = %s
$wanted = %s
$existing = Get-VMSwitch -Name $swName -ErrorAction SilentlyContinue
if ($existing) {
    $have = @($existing.NetAdapterInterfaceDescriptions) + @($existing.NetAdapterInterfaceDescription) | Where-Object { $_ }
    $want = @($wanted | ForEach-Object { $a = Get-NetAdapter -Name $_ -ErrorAction SilentlyContinue; if ($a) { $a.InterfaceDescription } })
    $missing = @($want | Where-Object { $have -notcontains $_ })
    if ($missing.Count -gt 0) { Note ('Switch ' + $swName + ' already exists with other adapters (' + ($have -join ', ') + '); it is left as it is.') }
    else { Note ('Switch ' + $swName + ' already exists on ' + ($wanted -join ', ') + '.') }
} else {
    $ok = $true
    foreach ($adapterName in $wanted) {
        $adapter = Get-NetAdapter -Name $adapterName -Physical -ErrorAction SilentlyContinue
        if (-not $adapter) { Block ('There is no physical adapter named ' + $adapterName + '.'); $ok = $false; continue }
        $binding = Get-NetAdapterBinding -Name $adapterName -ComponentID vms_pp -ErrorAction SilentlyContinue
        if ($binding -and $binding.Enabled) { Block ($adapterName + ' already belongs to another virtual switch.'); $ok = $false }
        if (Get-NetLbfoTeamMember -Name $adapterName -ErrorAction SilentlyContinue) { Block ($adapterName + ' is a member of a NIC team.'); $ok = $false }
        if ([string]$adapter.Status -ne 'Up') { Note ($adapterName + ' is ' + [string]$adapter.Status + '.') }
    }
    if ($ok) {
        $x = @{ name = $swName; adapters = $wanted; mgmt = %s; lb = %s }
        if ($build -ge 14393) {
            Plan-Change ('Create the Switch Embedded Teaming switch ' + $swName + ' on ' + ($wanted -join ', ') + ' (load balancing ' + $x.lb + $(if ($x.mgmt) { ', the host keeps a network adapter on it' } else { '' }) + ')') {
                param($x)
                New-VMSwitch -Name $x.name -NetAdapterName $x.adapters -EnableEmbeddedTeaming $true -AllowManagementOS $x.mgmt -MinimumBandwidthMode Weight | Out-Null
                Set-VMSwitchTeam -Name $x.name -LoadBalancingAlgorithm $x.lb
            } $x
        } elseif ($wanted.Count -gt 1) {
            Plan-Change ('Create the NIC team Team-' + $swName + ' on ' + ($wanted -join ', ') + ' (switch independent, dynamic) and the switch ' + $swName + ' on it' + $(if ($x.mgmt) { ', the host keeps a network adapter on it' } else { '' })) {
                param($x)
                New-NetLbfoTeam -Name ('Team-' + $x.name) -TeamMembers $x.adapters -TeamingMode SwitchIndependent -LoadBalancingAlgorithm Dynamic -Confirm:$false | Out-Null
                New-VMSwitch -Name $x.name -NetAdapterName ('Team-' + $x.name) -AllowManagementOS $x.mgmt -MinimumBandwidthMode Weight | Out-Null
            } $x
        } else {
            Plan-Change ('Create the switch ' + $swName + ' on ' + $wanted[0] + $(if ($x.mgmt) { ', the host keeps a network adapter on it' } else { '' })) {
                param($x)
                New-VMSwitch -Name $x.name -NetAdapterName $x.adapters[0] -AllowManagementOS $x.mgmt -MinimumBandwidthMode Weight | Out-Null
            } $x
        }
        if ($x.mgmt) {
            $holds = @(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $wanted -contains $_.InterfaceAlias -and $_.PrefixOrigin -ne 'WellKnown' })
            if ($holds.Count -gt 0) { Note ('Hyper-V moves this node''s address ' + (($holds | ForEach-Object { $_.IPAddress }) -join ', ') + ' to vEthernet (' + $swName + '); the connection pauses for a few seconds.') }
        }
    }
}
`, vmPSQuote(sw.Name), psList(adapters), psBool(sw.ManagementOS), vmPSQuote(sw.LoadBalancing))
		if sw.ManagementOS && sw.ManagementVLAN != "" {
			fmt.Fprintf(&s, `
$vlan = Get-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName %[1]s -ErrorAction SilentlyContinue
if (-not $vlan -or [int]$vlan.AccessVlanId -ne %[2]s) {
    Plan-Change ('Tag the host''s management adapter on ' + %[1]s + ' with VLAN ' + %[2]s + ' (the switch ports must be trunks that carry it)') { param($x) Set-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName $x.name -Access -VlanId $x.vlan } @{ name = %[1]s; vlan = %[2]s }
}
`, vmPSQuote(sw.Name), sw.ManagementVLAN)
		}
		if sw.MTU == 9000 {
			fmt.Fprintf(&s, `
foreach ($adapterName in %s) {
    $value = Get-JumboValue $adapterName
    if ($value -ge 0 -and $value -lt 9000) { Plan-Change ('Turn on jumbo frames (9014) on ' + $adapterName) { param($n) Set-NetAdapterAdvancedProperty -Name $n -RegistryKeyword '*JumboPacket' -RegistryValue 9014 } $adapterName }
    elseif ($value -lt 0) { Note ($adapterName + ' has no jumbo frame setting.') }
}
`, psList(adapters))
		}
	}
	for _, nic := range plan.HostNICs {
		ip := node.HostIPs[nic.Name]
		address, prefix := ip, "24"
		if slash := strings.Index(ip, "/"); slash > 0 {
			address, prefix = ip[:slash], ip[slash+1:]
		}
		jumbo := false
		for _, sw := range plan.Switches {
			if strings.EqualFold(sw.Name, nic.Switch) && sw.MTU == 9000 {
				jumbo = true
			}
		}
		fmt.Fprintf(&s, `
$nicName = %[1]s
$alias = 'vEthernet (' + $nicName + ')'
$vnic = Get-VMNetworkAdapter -ManagementOS -Name $nicName -ErrorAction SilentlyContinue
if (-not $vnic) {
    Plan-Change ('Add the host network adapter ' + $nicName + ' (' + %[3]s + ') on switch ' + %[2]s) { param($x) Add-VMNetworkAdapter -ManagementOS -Name $x.name -SwitchName $x.switch } @{ name = $nicName; switch = %[2]s }
} elseif ($vnic.SwitchName -ne %[2]s) {
    Note ('Host adapter ' + $nicName + ' already exists on switch ' + $vnic.SwitchName + '; it is left as it is.')
}
`, vmPSQuote(nic.Name), vmPSQuote(nic.Switch), vmPSQuote(hvBuildRoleNames()[nic.Role]))
		if nic.VLAN != "" {
			fmt.Fprintf(&s, `
$vlan = $null
if ($vnic) { $vlan = Get-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName $nicName -ErrorAction SilentlyContinue }
if (-not $vlan -or [int]$vlan.AccessVlanId -ne %[1]s) { Plan-Change ('Put ' + $nicName + ' on VLAN %[1]s') { param($x) Set-VMNetworkAdapterVlan -ManagementOS -VMNetworkAdapterName $x -Access -VlanId %[1]s } $nicName }
`, nic.VLAN)
		}
		fmt.Fprintf(&s, `
if (-not (Test-HasAddress $alias %[1]s)) { Plan-Change ('Give ' + $alias + ' the address ' + %[1]s + '/%[2]s (not registered in DNS)') { param($x) Set-StaticAddress $x } @{ alias = $alias; ip = %[1]s; prefix = %[2]s } }
`, vmPSQuote(address), prefix)
		if jumbo {
			s.WriteString(`
$value = -1
if ($vnic) { $value = Get-JumboValue $alias }
if ($value -lt 9000) { Plan-Change ('Turn on jumbo frames (9014) on ' + $alias) { param($n) Set-NetAdapterAdvancedProperty -Name $n -RegistryKeyword '*JumboPacket' -RegistryValue 9014 } $alias }
`)
		}
	}
	s.WriteString("}\n")
	return s.String()
}

func hvBuildStorageScript(plan hvBuildPlan, node hvBuildNode) string {
	var s strings.Builder
	storage := plan.Storage
	if storage.Kind == "iscsi" {
		s.WriteString(`
$service = Get-WmiObject -Class Win32_Service -Filter "Name='MSiSCSI'"
if (-not $service) { Block 'The Microsoft iSCSI Initiator service is missing.' }
elseif ($service.StartMode -ne 'Auto' -or $service.State -ne 'Running') {
    Plan-Change 'Start the Microsoft iSCSI Initiator service and start it automatically' { Set-Service -Name MSiSCSI -StartupType Automatic; Start-Service -Name MSiSCSI }
}
$iqn = @(Get-InitiatorPort -ErrorAction SilentlyContinue | Where-Object { [string]$_.ConnectionType -eq 'iSCSI' } | ForEach-Object { $_.NodeAddress })
if ($iqn.Count -gt 0) { Note ('This node''s iSCSI initiator name is ' + ($iqn -join ', ') + '. The array must give it access to the LUNs.') }
$initiators = New-Object System.Collections.ArrayList
`)
		for _, nic := range node.Storage {
			address, prefix := nic.IP, "24"
			if slash := strings.Index(nic.IP, "/"); slash > 0 {
				address, prefix = nic.IP[:slash], nic.IP[slash+1:]
			}
			fmt.Fprintf(&s, `
$adapterName = %[1]s
$adapter = Get-NetAdapter -Name $adapterName -Physical -ErrorAction SilentlyContinue
if (-not $adapter) { Block ('There is no physical adapter named ' + $adapterName + '.') }
else {
    $binding = Get-NetAdapterBinding -Name $adapterName -ComponentID vms_pp -ErrorAction SilentlyContinue
    if ($binding -and $binding.Enabled) { Block ($adapterName + ' belongs to a virtual switch; iSCSI needs its own adapters.') }
    if (-not (Test-HasAddress $adapterName %[2]s)) { Plan-Change ('Give ' + $adapterName + ' the address ' + %[2]s + '/%[3]s for iSCSI (not registered in DNS)') { param($x) Set-StaticAddress $x } @{ alias = $adapterName; ip = %[2]s; prefix = %[3]s } }
`, vmPSQuote(nic.Adapter), vmPSQuote(address), prefix)
			if storage.Jumbo {
				s.WriteString(`    $value = Get-JumboValue $adapterName
    if ($value -ge 0 -and $value -lt 9000) { Plan-Change ('Turn on jumbo frames (9014) on ' + $adapterName) { param($n) Set-NetAdapterAdvancedProperty -Name $n -RegistryKeyword '*JumboPacket' -RegistryValue 9014 } $adapterName }
`)
			}
			fmt.Fprintf(&s, "    [void]$initiators.Add(%s)\n}\n", vmPSQuote(address))
		}
		portals := []string{}
		for _, portal := range storage.Portals {
			host, port := hvBuildSplitPortal(portal)
			portals = append(portals, fmt.Sprintf("@{ address = %s; port = %d }", vmPSQuote(host), port))
		}
		fmt.Fprintf(&s, `
$portals = @(%[1]s)
$filter = %[2]s
$multipath = %[3]s
$known = @()
$running = $service -and $service.State -eq 'Running'
if ($running) { $known = @(Get-IscsiTargetPortal -ErrorAction SilentlyContinue) }
$allKnown = $true
foreach ($portal in $portals) {
    if (-not ($known | Where-Object { $_.TargetPortalAddress -eq $portal.address -and [int]$_.TargetPortalPortNumber -eq $portal.port })) {
        $allKnown = $false
        Plan-Change ('Add the iSCSI target portal ' + $portal.address + ':' + $portal.port) { param($p) New-IscsiTargetPortal -TargetPortalAddress $p.address -TargetPortalPortNumber $p.port | Out-Null } $portal
    }
}
$paths = @($initiators)
if ($paths.Count -eq 0) { $paths = @('') }
$connect = {
    param($x)
    $existing = @(Get-IscsiTarget -NodeAddress $x.target -ErrorAction SilentlyContinue | Get-IscsiConnection -ErrorAction SilentlyContinue)
    if ($x.initiator -and ($existing | Where-Object { $_.InitiatorAddress -eq $x.initiator })) { return }
    if (-not $x.initiator -and $existing.Count -gt 0) { return }
    $arguments = @{ NodeAddress = $x.target; IsPersistent = $true; IsMultipathEnabled = $x.multipath }
    if ($x.initiator) { $arguments.InitiatorPortalAddress = $x.initiator }
    Connect-IscsiTarget @arguments | Out-Null
}
if ($running -and $allKnown) {
    $targets = @(Get-IscsiTarget -ErrorAction SilentlyContinue | Where-Object { -not $filter -or $_.NodeAddress -like ('*' + $filter + '*') })
    if ($targets.Count -eq 0) { Note 'The portals list no targets for this node yet: give its initiator name access to the LUNs on the array, then preview again.' }
    foreach ($target in $targets) {
        $connections = @(Get-IscsiConnection -IscsiTarget $target -ErrorAction SilentlyContinue)
        foreach ($path in $paths) {
            $have = $false
            if ($path) { $have = [bool]($connections | Where-Object { $_.InitiatorAddress -eq $path }) } else { $have = $connections.Count -gt 0 }
            if ($have) { continue }
            $text = 'Connect to iSCSI target ' + $target.NodeAddress
            if ($path) { $text += ' from ' + $path }
            $text += ' (persistent'
            if ($multipath) { $text += ', multipath' }
            Plan-Change ($text + ')') $connect @{ target = $target.NodeAddress; initiator = $path; multipath = $multipath }
        }
    }
} else {
    $text = 'Connect to every target the portals list'
    if ($filter) { $text += ' whose name contains ' + $filter }
    if ($initiators.Count -gt 0) { $text += ', once from each of ' + ($initiators -join ', ') }
    $text += ' (persistent'
    if ($multipath) { $text += ', multipath' }
    Plan-Change ($text + ')') {
        param($x)
        Update-IscsiTarget -ErrorAction SilentlyContinue | Out-Null
        foreach ($target in @(Get-IscsiTarget | Where-Object { -not $x.filter -or $_.NodeAddress -like ('*' + $x.filter + '*') })) {
            foreach ($path in $x.paths) { & $x.connect @{ target = $target.NodeAddress; initiator = $path; multipath = $x.multipath } }
        }
    } @{ filter = $filter; paths = $paths; multipath = $multipath; connect = $connect }
}
`, strings.Join(portals, "; "), vmPSQuote(storage.TargetFilter), psBool(storage.MPIO))
	} else {
		s.WriteString(`
$wwpn = @(Get-InitiatorPort -ErrorAction SilentlyContinue | Where-Object { [string]$_.ConnectionType -eq 'Fibre Channel' } | ForEach-Object { $_.PortAddress })
if ($wwpn.Count -gt 0) { Note ('This node''s Fibre Channel ports (WWPN) are ' + ($wwpn -join ', ') + '. Zone them to the array and present the LUNs to them.') }
else { Note 'No Fibre Channel ports were found on this node.' }
`)
	}
	if storage.MPIO {
		s.WriteString(`
if (-not (Get-Command Get-MSDSMAutomaticClaimSettings -ErrorAction SilentlyContinue)) { Block 'Install roles first: Multipath I/O is not installed (and restart the node).' }
else {
`)
		if storage.Kind == "iscsi" {
			s.WriteString(`    $claim = Get-MSDSMAutomaticClaimSettings
    if (-not $claim['iSCSI']) { Plan-Change 'Let Microsoft MPIO claim iSCSI disks (takes effect after a restart)' { Enable-MSDSMAutomaticClaim -BusType iSCSI -Confirm:$false | Out-Null; $state.restart = $true } }
`)
		}
		for _, array := range storage.Arrays {
			fmt.Fprintf(&s, `    if (-not (Get-MSDSMSupportedHW | Where-Object { $_.VendorId.Trim() -eq %[1]s -and $_.ProductId.Trim() -eq %[2]s })) { Plan-Change ('Let Microsoft MPIO claim ' + %[1]s + ' ' + %[2]s + ' disks (takes effect after a restart)') { param($x) New-MSDSMSupportedHW -VendorId $x.vendor -ProductId $x.product | Out-Null; $state.restart = $true } @{ vendor = %[1]s; product = %[2]s } }
`, vmPSQuote(array.Vendor), vmPSQuote(array.Product))
		}
		s.WriteString(`    if ([string](Get-MSDSMGlobalDefaultLoadBalancePolicy) -ne 'RR') { Plan-Change 'Set the MPIO default load balancing policy to round robin' { Set-MSDSMGlobalDefaultLoadBalancePolicy -Policy RR } }
}
`)
	}
	return s.String()
}

func hvBuildDisksScript(plan hvBuildPlan) string {
	var s strings.Builder
	s.WriteString(`
$disks = @(Get-Disk)
`)
	for _, disk := range plan.Storage.Disks {
		letter := "$false"
		if disk.Use == "volume" {
			letter = "$true"
		}
		fmt.Fprintf(&s, `
$x = @{ id = %[1]s; label = %[2]s; fs = %[3]s; letter = %[4]s }
$disk = $disks | Where-Object { $_.UniqueId -eq $x.id } | Select-Object -First 1
if (-not $disk) { Block ('Disk ' + $x.label + ' (' + $x.id + ') is not visible on this node: connect storage first, then read the nodes again.') }
elseif ($disk.IsBoot -or $disk.IsSystem) { Block ('Disk ' + $disk.Number + ' holds Windows; it is never changed.') }
elseif ($disk.IsClustered) { Note ('Disk ' + $x.label + ' is already a cluster disk.') }
elseif ([string]$disk.PartitionStyle -eq 'RAW') {
    $size = [math]::Round($disk.Size / 1GB)
    Plan-Change ('Initialize disk ' + $disk.Number + ' (' + $disk.FriendlyName + ', ' + $size + ' GB) as GPT and format it ' + $x.fs + ' with the label ' + $x.label + $(if ($x.letter) { ' and a drive letter' } else { '' })) {
        param($x)
        $d = Get-Disk | Where-Object { $_.UniqueId -eq $x.id } | Select-Object -First 1
        if ([string]$d.PartitionStyle -ne 'RAW') { throw 'The disk is no longer empty; it was not changed.' }
        if ($d.IsOffline) { Set-Disk -Number $d.Number -IsOffline $false }
        if ($d.IsReadOnly) { Set-Disk -Number $d.Number -IsReadOnly $false }
        Initialize-Disk -Number $d.Number -PartitionStyle GPT
        if ($x.letter) { $p = New-Partition -DiskNumber $d.Number -UseMaximumSize -AssignDriveLetter } else { $p = New-Partition -DiskNumber $d.Number -UseMaximumSize }
        Format-Volume -Partition $p -FileSystem $x.fs -NewFileSystemLabel $x.label -AllocationUnitSize 65536 -Confirm:$false | Out-Null
    } $x
} else {
    $labelled = @(Get-Partition -DiskNumber $disk.Number -ErrorAction SilentlyContinue | Get-Volume -ErrorAction SilentlyContinue | Where-Object { $_.FileSystemLabel -eq $x.label })
    if ($labelled.Count -gt 0) { Note ('Disk ' + $disk.Number + ' is already formatted as ' + $x.label + '.') }
    else { Block ('Disk ' + $disk.Number + ' (' + $disk.FriendlyName + ') already holds partitions and is never changed. Choose an empty disk.') }
}
`, vmPSQuote(disk.UniqueID), vmPSQuote(disk.Label), vmPSQuote(disk.FileSystem), letter)
	}
	return s.String()
}

// hvBuildClusterHeader starts every cluster step: it runs on this computer
// with the cluster module.
func hvBuildClusterHeader() string {
	return `
if (-not (Get-Module -ListAvailable -Name FailoverClusters)) { Block 'Run Cluster tools on this computer first: the Failover Cluster Module for Windows PowerShell is missing here.' }
else {
Import-Module FailoverClusters
Note ('Runs on this computer (' + $env:COMPUTERNAME + ') with your Windows account ' + [Security.Principal.WindowsIdentity]::GetCurrent().Name + '.')
`
}

func hvBuildNodeNames(plan hvBuildPlan) []string {
	names := []string{}
	for _, node := range plan.Nodes {
		names = append(names, node.Name)
	}
	return names
}

func hvBuildValidateScript(plan hvBuildPlan) string {
	return hvBuildClusterHeader() + fmt.Sprintf(`
$nodes = %[1]s
$include = @('Inventory', 'Network', 'System Configuration', 'Hyper-V Configuration')
$exists = $null
try { $exists = Get-Cluster -Name %[2]s -ErrorAction Stop } catch { }
$withStorage = %[3]s
if ($withStorage -and $exists) { Note 'The cluster already exists, so the Storage tests are left out: they would take its disks offline.'; $withStorage = $false }
if ($withStorage) { $include += 'Storage' }
$text = 'Run cluster validation on ' + ($nodes -join ', ') + ' (' + ($include -join ', ') + ')'
if ($withStorage) { $text += '. The Storage tests take the shared disks offline briefly' }
Plan-Change $text {
    param($x)
    $name = Join-Path $env:TEMP ('Cluster-Validation-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
    $warnings = @()
    $report = Test-Cluster -Node $x.nodes -Include $x.include -ReportName $name -WarningVariable warnings -WarningAction SilentlyContinue
    $state.report = [string]$report.FullName
    $state.warnings = @($warnings | ForEach-Object { [string]$_ })
} @{ nodes = $nodes; include = $include }
}
`, psList(append(hvBuildMembers(plan), hvBuildNodeNames(plan)...)), vmPSQuote(vmNonEmpty(plan.Cluster.Name, "-")), psBool(plan.Cluster.StorageTests && !plan.Cluster.Join))
}

func hvBuildClusterScript(plan hvBuildPlan) string {
	return hvBuildClusterHeader() + fmt.Sprintf(`
$clusterName = %[1]s
$nodes = %[2]s
$addresses = %[3]s
$short = { param($n) ([string]$n).Split('.')[0].ToLower() }
$exists = $null
try { $exists = Get-Cluster -Name $clusterName -ErrorAction Stop } catch { }
if (-not $exists -and -not %[4]s) { Note ('Your account must be allowed to create the computer object ' + $clusterName + ' in Active Directory, or it must be prestaged and disabled.') }
if ($exists) {
    $members = @(Get-ClusterNode -Cluster $clusterName | ForEach-Object { & $short $_.Name })
    Note ('Cluster ' + $clusterName + ' exists with nodes ' + ($members -join ', ') + '.')
    foreach ($node in $nodes) {
        if ($members -contains (& $short $node)) { continue }
        $other = $null
        try { $other = Get-Cluster -Name $node -ErrorAction Stop } catch { }
        if ($other) { Block ($node + ' is already in cluster ' + $other.Name + '.'); continue }
        Plan-Change ('Add node ' + $node + ' to cluster ' + $clusterName + ' without adding storage') { param($x) Add-ClusterNode -Cluster $x.cluster -Name $x.node -NoStorage | Out-Null } @{ cluster = $clusterName; node = $node }
    }
} elseif (%[4]s) {
    Block ('Cluster ' + $clusterName + ' was not found from this computer. Nothing is created: check that this computer is in the cluster''s domain and read the cluster again in step 1.')
} else {
    foreach ($node in $nodes) {
        $other = $null
        try { $other = Get-Cluster -Name $node -ErrorAction Stop } catch { }
        if ($other) { Block ($node + ' is already in cluster ' + $other.Name + '.') }
    }
    Plan-Change ('Create the failover cluster ' + $clusterName + ' with ' + ($nodes -join ', ') + ' at ' + ($addresses -join ', ') + ', without storage') {
        param($x)
        New-Cluster -Name $x.cluster -Node $x.nodes -StaticAddress $x.addresses -NoStorage | Out-Null
    } @{ cluster = $clusterName; nodes = $nodes; addresses = $addresses }
}
}
`, vmPSQuote(plan.Cluster.Name), psList(hvBuildNodeNames(plan)), psList(plan.Cluster.Addresses), psBool(plan.Cluster.Join))
}

func hvBuildCSVScript(plan hvBuildPlan) string {
	var s strings.Builder
	s.WriteString(hvBuildClusterHeader())
	fmt.Fprintf(&s, `
$clusterName = %s
$firstNode = %s
$cim = $null
try { $cim = New-CimSession -ComputerName $firstNode -ErrorAction Stop } catch { Block ('Could not read the disks of ' + $firstNode + ': ' + $_.Exception.Message) }
$available = @()
try { $available = @(Get-ClusterAvailableDisk -Cluster $clusterName -ErrorAction Stop) } catch { Block ('Could not read cluster ' + $clusterName + ': ' + $_.Exception.Message) }
$resources = @(Get-ClusterResource -Cluster $clusterName -ErrorAction SilentlyContinue)
$shared = @(Get-ClusterSharedVolume -Cluster $clusterName -ErrorAction SilentlyContinue | ForEach-Object { $_.Name })
$plain = { param($v) ([string]$v).Trim('{', '}').ToLower() }
`, vmPSQuote(plan.Cluster.Name), vmPSQuote(plan.Nodes[0].Name))
	for _, disk := range plan.Storage.Disks {
		if disk.Use != "csv" && disk.Use != "witness" {
			continue
		}
		fmt.Fprintf(&s, `
$x = @{ cluster = $clusterName; id = %[1]s; label = %[2]s; csv = %[3]s }
if ($cim) {
    $resource = $resources | Where-Object { $_.Name -eq $x.label -and [string]$_.ResourceType -eq 'Physical Disk' }
    if (-not $resource) {
        $disk = Get-Disk -CimSession $cim | Where-Object { $_.UniqueId -eq $x.id } | Select-Object -First 1
        if (-not $disk) { Block ('Disk ' + $x.label + ' is not visible on ' + $firstNode + '.') }
        elseif ([string]$disk.PartitionStyle -eq 'RAW') { Block ('Disk ' + $x.label + ' is not prepared yet: run Prepare disks first.') }
        else {
            $match = @($available | Where-Object { (& $plain $_.Id) -eq (& $plain $disk.Guid) -or ($disk.Signature -and (& $plain $_.Id) -eq ([string]$disk.Signature).ToLower()) })
            if ($match.Count -eq 0) { $match = @($available | Where-Object { [uint64]$_.Size -eq [uint64]$disk.Size }) }
            if ($match.Count -ne 1) { Block ('The cluster does not offer disk ' + $x.label + ' (' + [math]::Round($disk.Size / 1GB) + ' GB) as an available disk. It must be visible to every node.') }
            else {
                $x.guid = $match[0].Id
                Plan-Change ('Add disk ' + $x.label + ' (' + [math]::Round($disk.Size / 1GB) + ' GB) to cluster ' + $x.cluster + ' as ' + $x.label) {
                    param($x)
                    $added = Get-ClusterAvailableDisk -Cluster $x.cluster | Where-Object { $_.Id -eq $x.guid } | Add-ClusterDisk
                    $added.Name = $x.label
                } $x
                if ($x.csv) { Plan-Change ('Make ' + $x.label + ' a Cluster Shared Volume') { param($x) Add-ClusterSharedVolume -Cluster $x.cluster -Name $x.label | Out-Null } $x }
            }
        }
    } elseif ($x.csv -and $shared -notcontains $x.label) {
        Plan-Change ('Make cluster disk ' + $x.label + ' a Cluster Shared Volume') { param($x) Add-ClusterSharedVolume -Cluster $x.cluster -Name $x.label | Out-Null } $x
    } elseif ($x.csv) {
        $path = @(Get-ClusterSharedVolume -Cluster $clusterName -Name $x.label | ForEach-Object { $_.SharedVolumeInfo } | ForEach-Object { $_.FriendlyVolumeName })
        Note ($x.label + ' is a Cluster Shared Volume at ' + ($path -join ', ') + '.')
    } else {
        Note ($x.label + ' is a cluster disk.')
    }
}
`, vmPSQuote(disk.UniqueID), vmPSQuote(disk.Label), psBool(disk.Use == "csv"))
	}
	s.WriteString("if ($cim) { Remove-CimSession -CimSession $cim }\n}\n")
	return s.String()
}

func hvBuildQuorumScript(plan hvBuildPlan) string {
	var s strings.Builder
	s.WriteString(hvBuildClusterHeader())
	fmt.Fprintf(&s, `
$clusterName = %s
$quorum = $null
try { $quorum = Get-ClusterQuorum -Cluster $clusterName -ErrorAction Stop } catch { Block ('Could not read cluster ' + $clusterName + ': ' + $_.Exception.Message) }
$current = ''
if ($quorum -and $quorum.QuorumResource) { $current = [string]$quorum.QuorumResource.Name }
`, vmPSQuote(plan.Cluster.Name))
	switch plan.Cluster.Witness {
	case "disk":
		label := ""
		for _, disk := range plan.Storage.Disks {
			if disk.Use == "witness" {
				label = disk.Label
			}
		}
		fmt.Fprintf(&s, `
if ($quorum) {
    $resource = Get-ClusterResource -Cluster $clusterName -Name %[1]s -ErrorAction SilentlyContinue
    if (-not $resource) { Block 'Run Cluster Shared Volumes first: the witness disk is not a cluster disk yet.' }
    elseif ($current -eq %[1]s) { Note ('The disk witness is ' + %[1]s + '.') }
    else { Plan-Change ('Use cluster disk ' + %[1]s + ' as the disk witness') { param($x) Set-ClusterQuorum -Cluster $x.cluster -DiskWitness $x.disk | Out-Null } @{ cluster = $clusterName; disk = %[1]s } }
}
`, vmPSQuote(label))
	case "fileshare":
		fmt.Fprintf(&s, `
if ($quorum) {
    $path = ''
    $witness = Get-ClusterResource -Cluster $clusterName -ErrorAction SilentlyContinue | Where-Object { [string]$_.ResourceType -eq 'File Share Witness' } | Select-Object -First 1
    if ($witness) { $path = [string]($witness | Get-ClusterParameter -Name SharePath).Value }
    if ($path -eq %[1]s) { Note ('The file share witness is ' + $path + '.') }
    else { Plan-Change ('Use the file share witness ' + %[1]s + ' (the cluster''s computer account ' + $clusterName + '$ needs Full Control of the share and folder)') { param($x) Set-ClusterQuorum -Cluster $x.cluster -FileShareWitness $x.path | Out-Null } @{ cluster = $clusterName; path = %[1]s } }
}
`, vmPSQuote(plan.Cluster.WitnessShare))
	case "cloud":
		fmt.Fprintf(&s, `
if ($quorum) {
    $account = ''
    $witness = Get-ClusterResource -Cluster $clusterName -ErrorAction SilentlyContinue | Where-Object { [string]$_.ResourceType -eq 'Cloud Witness' } | Select-Object -First 1
    if ($witness) { $account = [string]($witness | Get-ClusterParameter -Name AccountName).Value }
    if ($account -eq %[1]s) { Note ('The cloud witness uses storage account ' + $account + '.') }
    elseif (-not $Secrets -or -not $Secrets.cloudKey) { Block 'Type the storage account''s access key (it is used for this step only and never saved).' }
    elseif ((Get-Build) -lt 14393) { Block 'A cloud witness needs Windows Server 2016 or later.' }
    else { Plan-Change ('Use a cloud witness in Azure storage account ' + %[1]s) { param($x) Set-ClusterQuorum -Cluster $x.cluster -CloudWitness -AccountName $x.account -AccessKey $Secrets.cloudKey | Out-Null } @{ cluster = $clusterName; account = %[1]s } }
}
`, vmPSQuote(plan.Cluster.CloudAccount))
	default:
		s.WriteString(`
if ($quorum) {
    if (-not $current) { Note 'The cluster uses node majority with no witness.' }
    else { Plan-Change ('Remove the witness ' + $current + ' from the quorum (node majority only, not recommended)') { param($x) Set-ClusterQuorum -Cluster $x -NoWitness | Out-Null } $clusterName }
}
`)
	}
	s.WriteString("}\n")
	return s.String()
}

// hvBuildSubnet returns the network of an address/prefix as text.
func hvBuildSubnet(cidr string) (string, string) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", ""
	}
	return network.IP.String(), net.IP(network.Mask).String()
}

func hvBuildClusterNetworksScript(plan hvBuildPlan) string {
	var s strings.Builder
	s.WriteString(hvBuildClusterHeader())
	wanted := []string{}
	first := plan.Nodes[0]
	for _, nic := range plan.HostNICs {
		address, mask := hvBuildSubnet(first.HostIPs[nic.Name])
		if address == "" {
			continue
		}
		role := 1
		name := nic.Name
		if nic.Role == "storage" {
			role = 0
		}
		wanted = append(wanted, fmt.Sprintf("@{ address = %s; mask = %s; name = %s; role = %d; migration = %s }", vmPSQuote(address), vmPSQuote(mask), vmPSQuote(name), role, psBool(nic.Role == "livemigration")))
	}
	for _, nic := range first.Storage {
		address, mask := hvBuildSubnet(nic.IP)
		if address == "" {
			continue
		}
		wanted = append(wanted, fmt.Sprintf("@{ address = %s; mask = %s; name = %s; role = 0; migration = $false }", vmPSQuote(address), vmPSQuote(mask), vmPSQuote("iSCSI-"+hvBuildCleanName(nic.Adapter))))
	}
	fmt.Fprintf(&s, `
$clusterName = %[1]s
$wanted = @(%[2]s)
$networks = @()
try { $networks = @(Get-ClusterNetwork -Cluster $clusterName -ErrorAction Stop) } catch { Block ('Could not read cluster ' + $clusterName + ': ' + $_.Exception.Message) }
$roleText = @{ 0 = 'none (no cluster traffic)'; 1 = 'cluster only'; 3 = 'cluster and client' }
$migrationIds = @()
$usedNames = @($networks | ForEach-Object { $_.Name })
foreach ($want in $wanted) {
    $network = $networks | Where-Object { $_.Address -eq $want.address -and $_.AddressMask -eq $want.mask } | Select-Object -First 1
    if (-not $network) { Note ('No cluster network uses ' + $want.address + '/' + $want.mask + ' (for ' + $want.name + ') yet.'); continue }
    if ($want.migration) { $migrationIds += [string]$network.Id }
    $x = @{ cluster = $clusterName; address = $want.address; mask = $want.mask; name = $want.name; role = $want.role }
    if ($network.Name -ne $want.name) {
        if ($usedNames -contains $want.name) { Note ('Another cluster network is already named ' + $want.name + '; ' + $network.Name + ' keeps its name.') }
        else { Plan-Change ('Rename cluster network ' + $network.Name + ' (' + $want.address + ') to ' + $want.name) { param($x) $n = Get-ClusterNetwork -Cluster $x.cluster | Where-Object { $_.Address -eq $x.address -and $_.AddressMask -eq $x.mask }; $n.Name = $x.name } $x }
    }
    if ([int]$network.Role -ne $want.role) {
        Plan-Change ('Set the role of cluster network ' + $want.name + ' to ' + $roleText[[int]$want.role]) { param($x) $n = Get-ClusterNetwork -Cluster $x.cluster | Where-Object { $_.Address -eq $x.address -and $_.AddressMask -eq $x.mask }; $n.Role = $x.role } $x
    }
}
if ($networks.Count -gt 0 -and $migrationIds.Count -gt 0) {
    $exclude = @($networks | Where-Object { $migrationIds -notcontains [string]$_.Id } | ForEach-Object { [string]$_.Id } | Sort-Object)
    $type = Get-ClusterResourceType -Cluster $clusterName -Name 'Virtual Machine' -ErrorAction SilentlyContinue
    if (-not $type) { Block 'The cluster has no Virtual Machine resource type: install Hyper-V on every node first.' }
    else {
        $current = @(([string]($type | Get-ClusterParameter -Name MigrationExcludeNetworks).Value).Split(';') | Where-Object { $_ } | Sort-Object)
        if (($current -join ';') -ne ($exclude -join ';')) {
            $names = @($networks | Where-Object { $migrationIds -contains [string]$_.Id } | ForEach-Object { $_.Name })
            Plan-Change ('Live migrate only over ' + ($names -join ', ') + ' (the other cluster networks are excluded)') { param($x) Get-ClusterResourceType -Cluster $x.cluster -Name 'Virtual Machine' | Set-ClusterParameter -Name MigrationExcludeNetworks -Value ($x.exclude -join ';') } @{ cluster = $clusterName; exclude = $exclude }
        } else { Note 'Live migration already uses only its own network.' }
    }
} elseif ($migrationIds.Count -eq 0) { Note 'The plan has no live migration adapter, so live migration may use any cluster network.' }
}
`, vmPSQuote(plan.Cluster.Name), strings.Join(wanted, "; "))
	return s.String()
}

func hvBuildHostSettingsScript(plan hvBuildPlan) string {
	var s strings.Builder
	settings := plan.Settings
	subnets := []string{}
	for _, nic := range plan.HostNICs {
		if nic.Role != "livemigration" || len(plan.Nodes) == 0 {
			continue
		}
		address, mask := hvBuildSubnet(plan.Nodes[0].HostIPs[nic.Name])
		if address != "" {
			ones, _ := net.IPMask(net.ParseIP(mask).To4()).Size()
			subnets = append(subnets, fmt.Sprintf("%s/%d", address, ones))
		}
	}
	fmt.Fprintf(&s, `
if (-not (Get-Command Get-VMHost -ErrorAction SilentlyContinue)) { Block 'Install roles first: the Hyper-V module is missing (and restart the node).' }
else {
$vmHost = Get-VMHost
$x = @{ auth = %[1]s; max = %[2]d; storage = %[3]d; performance = %[4]s }
if (-not $vmHost.VirtualMachineMigrationEnabled) { Plan-Change 'Turn on live migration' { Enable-VMMigration } }
if ([string]$vmHost.VirtualMachineMigrationAuthenticationType -ne $x.auth) { Plan-Change ('Use ' + $x.auth + ' authentication for live migration') { param($x) Set-VMHost -VirtualMachineMigrationAuthenticationType $x.auth } $x }
if ([int]$vmHost.MaximumVirtualMachineMigrations -ne $x.max) { Plan-Change ('Allow ' + $x.max + ' simultaneous live migrations') { param($x) Set-VMHost -MaximumVirtualMachineMigrations $x.max } $x }
if ([int]$vmHost.MaximumStorageMigrations -ne $x.storage) { Plan-Change ('Allow ' + $x.storage + ' simultaneous storage migrations') { param($x) Set-VMHost -MaximumStorageMigrations $x.storage } $x }
if ($vmHost.PSObject.Properties['VirtualMachineMigrationPerformanceOption'] -and [string]$vmHost.VirtualMachineMigrationPerformanceOption -ne $x.performance) { Plan-Change ('Use ' + $x.performance + ' for live migration traffic') { param($x) Set-VMHost -VirtualMachineMigrationPerformanceOption $x.performance } $x }
`, vmPSQuote(settings.MigrationAuth), settings.MaxMigrations, settings.MaxStorageMigrations, vmPSQuote(settings.Performance))
	if settings.MigrationAuth == "Kerberos" {
		s.WriteString("Note 'Kerberos live migration outside the cluster needs constrained delegation for each node in Active Directory (Microsoft Virtual System Migration Service and cifs). Moves inside a failover cluster do not.'\n")
	}
	if !plan.Cluster.Enabled && len(subnets) > 0 {
		fmt.Fprintf(&s, `
$subnets = %s
if ($vmHost.UseAnyNetworkForMigration) { Plan-Change 'Live migrate only over the chosen networks' { Set-VMHost -UseAnyNetworkForMigration $false } }
$have = @(Get-VMMigrationNetwork -ErrorAction SilentlyContinue | ForEach-Object { $_.Subnet })
foreach ($subnet in $subnets) { if ($have -notcontains $subnet) { Plan-Change ('Add ' + $subnet + ' as a live migration network') { param($n) Add-VMMigrationNetwork $n } $subnet } }
`, psList(subnets))
	}
	for _, folder := range []struct{ path, property, flag, what string }{
		{settings.VHDPath, "VirtualHardDiskPath", "VirtualHardDiskPath", "virtual hard disk"},
		{settings.VMPath, "VirtualMachinePath", "VirtualMachinePath", "virtual machine"},
	} {
		if folder.path == "" {
			continue
		}
		fmt.Fprintf(&s, `
$folder = %[1]s
if (-not (Test-Path -LiteralPath $folder -PathType Container)) { Plan-Change ('Create the folder ' + $folder) { param($p) New-Item -ItemType Directory -Path $p -Force | Out-Null } $folder }
if ([string]$vmHost.%[2]s -ne $folder) { Plan-Change ('Make ' + $folder + ' the default %[4]s folder') { param($p) Set-VMHost -%[3]s $p } $folder }
`, vmPSQuote(folder.path), folder.property, folder.flag, folder.what)
	}
	s.WriteString("}\n")
	return s.String()
}

// hvBuildNetworkMap returns the plan's VM networks as the migration plan's
// network map entries (port group to switch and VLAN).
func hvBuildNetworkMap(plan hvBuildPlan) map[string]vmNetworkMap {
	out := map[string]vmNetworkMap{}
	for _, network := range plan.Networks {
		if network.Switch == "" {
			continue
		}
		out[strings.ToLower(network.Portgroup)] = vmNetworkMap{Source: network.Portgroup, SourceVLAN: network.SourceVLAN, TargetName: network.Switch, TargetVLAN: network.VLAN}
	}
	return out
}

// hvBuildApplyNetworkMap writes the plan's VM networks into a migration
// plan's network map, for the port groups the migration plan lists.
func hvBuildApplyNetworkMap(build hvBuildPlan, migration vmPlan) (vmPlan, int) {
	mapping := hvBuildNetworkMap(build)
	changed := 0
	for index := range migration.Networks {
		entry, ok := mapping[strings.ToLower(migration.Networks[index].Source)]
		if !ok {
			continue
		}
		if migration.Networks[index].TargetName != entry.TargetName || migration.Networks[index].TargetVLAN != entry.TargetVLAN {
			migration.Networks[index].TargetName = entry.TargetName
			migration.Networks[index].TargetVLAN = entry.TargetVLAN
			changed++
		}
	}
	return migration, changed
}
