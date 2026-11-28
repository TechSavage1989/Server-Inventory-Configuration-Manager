package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Hyper-V as a migration source (phase 4 step 2). Read-HyperVHost.ps1 reads
// a host through the root\virtualization\v2 CIM namespace and returns the
// report below; hypervInventoryFromReport turns it into the same vmInventory
// a vCenter read produces, so readiness, plans, waves, runbooks and the
// Environment view work unchanged. Nothing on the host is changed. Disks are
// copied with the Disk converter, which reads .avhdx checkpoint chains.

const (
	vmSourceVMware    = ""
	vmSourceHyperV    = "hyperv"
	hypervServerType  = "Hyper-V host"
	hypervReadScript  = "Read-HyperVHost.ps1"
	hypervNetworkKind = "Hyper-V virtual switch"
	hypervReportLimit = 256 << 20
)

type hypervReport struct {
	Host     hypervHostReport     `json:"host"`
	Switches []hypervSwitchReport `json:"switches"`
	VMs      []hypervVMReport     `json:"vms"`
	Warnings []string             `json:"warnings"`
}

type hypervHostReport struct {
	Name        string               `json:"name"`
	Account     string               `json:"account"`
	DNSName     string               `json:"dnsName"`
	Domain      string               `json:"domain"`
	Cluster     string               `json:"cluster"`
	Vendor      string               `json:"vendor"`
	Model       string               `json:"model"`
	CPUModel    string               `json:"cpuModel"`
	CPUMhz      int                  `json:"cpuMhz"`
	Sockets     int                  `json:"sockets"`
	Cores       int                  `json:"cores"`
	Threads     int                  `json:"threads"`
	MemoryBytes int64                `json:"memoryBytes"`
	OSName      string               `json:"osName"`
	OSVersion   string               `json:"osVersion"`
	OSBuild     string               `json:"osBuild"`
	Volumes     []hypervVolumeReport `json:"volumes"`
}

type hypervVolumeReport struct {
	Name          string `json:"name"`
	Label         string `json:"label"`
	FileSystem    string `json:"fileSystem"`
	CapacityBytes int64  `json:"capacityBytes"`
	FreeBytes     int64  `json:"freeBytes"`
}

type hypervSwitchReport struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type hypervVMReport struct {
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	EnabledState       int                    `json:"enabledState"`
	Generation         int                    `json:"generation"`
	Version            string                 `json:"version"`
	SecureBoot         bool                   `json:"secureBoot"`
	TPM                bool                   `json:"tpm"`
	Shielded           bool                   `json:"shielded"`
	CPUs               int                    `json:"cpus"`
	MemoryMB           int64                  `json:"memoryMB"`
	DynamicMemory      bool                   `json:"dynamicMemory"`
	MemoryLimitMB      int64                  `json:"memoryLimitMB"`
	Notes              string                 `json:"notes"`
	ConfigurationRoot  string                 `json:"configurationRoot"`
	GuestOS            string                 `json:"guestOs"`
	GuestOSVersion     string                 `json:"guestOsVersion"`
	GuestHostName      string                 `json:"guestHostName"`
	IntegrationVersion string                 `json:"integrationVersion"`
	Heartbeat          string                 `json:"heartbeat"`
	Checkpoints        []hypervCheckpointInfo `json:"checkpoints"`
	Disks              []hypervDiskReport     `json:"disks"`
	NICs               []hypervNICReport      `json:"nics"`
	Media              []string               `json:"media"`
}

type hypervCheckpointInfo struct {
	Name    string `json:"name"`
	Created string `json:"created"`
	Type    string `json:"type"`
}

type hypervDiskReport struct {
	Path             string `json:"path"`
	Controller       string `json:"controller"`
	ControllerNumber string `json:"controllerNumber"`
	Location         string `json:"location"`
	Shared           bool   `json:"shared"`
	Passthrough      bool   `json:"passthrough"`
	HostDisk         string `json:"hostDisk"`
	Format           string `json:"format"`
	Type             string `json:"type"`
	MaxInternalSize  int64  `json:"maxInternalSize"`
	FileSize         int64  `json:"fileSize"`
	ParentPath       string `json:"parentPath"`
	Error            string `json:"error"`
}

type hypervNICReport struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	StaticMAC bool     `json:"staticMac"`
	Legacy    bool     `json:"legacy"`
	Switch    string   `json:"switch"`
	Connected bool     `json:"connected"`
	VLAN      int      `json:"vlan"`
	Addresses []string `json:"addresses"`
	// Subnets holds each address's mask (dotted for IPv4), in the same order.
	Subnets    []string `json:"subnets,omitempty"`
	DHCP       bool     `json:"dhcp"`
	GuestKnown bool     `json:"guestKnown"`
}

// hypervPowerState maps Msvm_ComputerSystem.EnabledState to the vSphere
// names the rest of the application uses.
func hypervPowerState(state int) string {
	switch state {
	case 2, 10: // Enabled, Starting/Resetting
		return "poweredOn"
	case 3: // Disabled
		return "poweredOff"
	case 6, 9, 32768, 32769, 32776, 32777: // Saved, Paused and their transitions
		return "suspended"
	}
	return "unknown"
}

// hypervMAC formats Hyper-V's bare 12-digit MAC with colons.
func hypervMAC(value string) string {
	value = strings.ToLower(strings.NewReplacer("-", "", ":", "").Replace(strings.TrimSpace(value)))
	if len(value) != 12 {
		return value
	}
	parts := make([]string, 6)
	for index := range parts {
		parts[index] = value[index*2 : index*2+2]
	}
	return strings.Join(parts, ":")
}

// hypervVolumeFor names the host volume (or SMB share) that holds a path,
// which stands in for a datastore.
func hypervVolumeFor(path string, volumes []hypervVolumeReport) string {
	clean := strings.TrimRight(strings.ReplaceAll(path, "/", `\`), `\`)
	if strings.HasPrefix(clean, `\\`) {
		parts := strings.SplitN(strings.TrimPrefix(clean, `\\`), `\`, 3)
		if len(parts) >= 2 {
			return `\\` + parts[0] + `\` + parts[1]
		}
		return clean
	}
	best := ""
	for _, volume := range volumes {
		name := strings.TrimRight(volume.Name, `\`)
		if name == "" {
			continue
		}
		lower, prefix := strings.ToLower(clean), strings.ToLower(name)
		if (lower == prefix || strings.HasPrefix(lower, prefix+`\`)) && len(name) > len(best) {
			best = name
		}
	}
	if best != "" {
		return best
	}
	if len(clean) >= 2 && clean[1] == ':' {
		return strings.ToUpper(clean[:2])
	}
	return ""
}

// hypervReachablePath gives the path this computer opens a host's disk
// through: the path itself on the host, the administrative share
// \\host\D$\... from elsewhere, or the SMB path unchanged.
func hypervReachablePath(path, host string, local bool) string {
	path = strings.TrimSpace(path)
	if path == "" || local || strings.HasPrefix(path, `\\`) {
		return path
	}
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		drive := strings.ToUpper(path[:1])
		if drive[0] >= 'A' && drive[0] <= 'Z' {
			return `\\` + host + `\` + drive + `$\` + strings.ReplaceAll(path[3:], "/", `\`)
		}
	}
	return path
}

func hypervDiskProvisioning(disk hypervDiskReport) string {
	switch disk.Type {
	case "2":
		return "fixed"
	case "3":
		return "dynamic"
	case "4":
		return "differencing"
	}
	return ""
}

func hypervDiskLabel(disk hypervDiskReport, index int) string {
	if disk.Controller == "" {
		return fmt.Sprintf("Hard drive %d", index+1)
	}
	label := "Hard drive on " + disk.Controller + " controller"
	if disk.ControllerNumber != "" {
		label += " " + disk.ControllerNumber
	}
	if disk.Location != "" {
		label += " location " + disk.Location
	}
	return label
}

func hypervAtoi(value string) int {
	number := 0
	fmt.Sscanf(strings.TrimSpace(value), "%d", &number)
	return number
}

// hypervInventoryFromReport converts the script's report. local is true
// when the host is the computer the application runs on, so disk paths
// open directly; otherwise they open through administrative shares.
func hypervInventoryFromReport(report hypervReport, local bool, now time.Time, version string) *vmInventory {
	host := report.Host
	hostName := vmNonEmpty(strings.TrimSpace(host.Name), "Hyper-V host")
	inventory := &vmInventory{
		Schema:      1,
		Server:      hostName,
		ServerType:  hypervServerType,
		Product:     vmNonEmpty(host.OSName, "Microsoft Hyper-V"),
		APIVersion:  host.OSVersion,
		Build:       host.OSBuild,
		CollectedAt: now.UTC(),
		CollectedBy: host.Account,
		Datacenters: []string{hostName},
		Clusters:    []vmCluster{},
		Datastores:  []vmDatastore{},
		Networks:    []vmNetwork{},
		VMs:         []vmMachine{},
		Warnings:    append([]string{}, report.Warnings...),
		AppVersion:  version,
	}
	if host.Cluster != "" {
		inventory.Clusters = append(inventory.Clusters, vmCluster{Name: host.Cluster, Datacenter: hostName, Hosts: 1, CPUCores: host.Cores, MemoryBytes: host.MemoryBytes})
		inventory.Warnings = append(inventory.Warnings, "This host is a member of failover cluster "+host.Cluster+". Only its own VMs were read; read each cluster node to see every VM.")
	}
	inventory.Hosts = []vmHost{{
		Name: hostName, Datacenter: hostName, Cluster: host.Cluster, Vendor: host.Vendor, Model: host.Model, CPUModel: host.CPUModel,
		Sockets: host.Sockets, Cores: host.Cores, Threads: host.Threads, CPUMhz: host.CPUMhz, MemoryBytes: host.MemoryBytes,
		Version: vmNonEmpty(host.OSName, "Windows"), Build: host.OSBuild, ConnectionState: "connected", PowerState: "poweredOn", VMCount: len(report.VMs),
	}}

	datastores := map[string]*vmDatastore{}
	datastoreOrder := []string{}
	addDatastore := func(name string) *vmDatastore {
		key := strings.ToLower(name)
		if existing, ok := datastores[key]; ok {
			return existing
		}
		store := &vmDatastore{Name: name, Datacenter: hostName, Type: "SMB", Accessible: true, Shared: true, Hosts: []string{hostName}}
		for _, volume := range host.Volumes {
			if strings.EqualFold(strings.TrimRight(volume.Name, `\`), name) {
				store.Type = vmNonEmpty(volume.FileSystem, "Volume")
				store.CapacityBytes, store.FreeBytes = volume.CapacityBytes, volume.FreeBytes
				store.Shared = strings.Contains(strings.ToLower(name), `\clusterstorage\`)
				if store.Shared {
					store.Type = "CSV"
				}
			}
		}
		datastores[key] = store
		datastoreOrder = append(datastoreOrder, key)
		return store
	}
	networks := map[string]*vmNetwork{}
	networkOrder := []string{}
	addNetwork := func(name string, vlan int) *vmNetwork {
		label := name
		if vlan > 0 {
			label = fmt.Sprintf("%s (VLAN %d)", name, vlan)
		}
		key := strings.ToLower(label)
		if existing, ok := networks[key]; ok {
			return existing
		}
		network := &vmNetwork{Name: label, Datacenter: hostName, Kind: hypervNetworkKind, Switch: name}
		if vlan > 0 {
			network.VLAN = fmt.Sprint(vlan)
		}
		networks[key] = network
		networkOrder = append(networkOrder, key)
		return network
	}
	for _, sw := range report.Switches {
		if strings.TrimSpace(sw.Name) != "" {
			addNetwork(sw.Name, 0)
		}
	}

	for _, source := range report.VMs {
		vm := vmMachine{
			Name: vmNonEmpty(source.Name, source.ID), ID: source.ID, InstanceUUID: source.ID,
			Datacenter: hostName, Cluster: host.Cluster, Host: hostName,
			PowerState: hypervPowerState(source.EnabledState), GuestOS: source.GuestOS, GuestOSRunning: source.GuestOS,
			GuestHostName: source.GuestHostName, CPUs: source.CPUs, CoresPerSocket: 0, MemoryMB: source.MemoryMB,
			SecureBoot: source.SecureBoot, VTPM: source.TPM, Encrypted: source.Shielded,
			Disks: []vmDisk{}, NICs: []vmNIC{}, Snapshots: []vmSnapshot{}, ConnectedMedia: []string{}, Passthrough: []string{}, GuestDisks: []vmGuestDisk{},
			Source: vmSourceHyperV, Generation: source.Generation, ConfigVersion: source.Version,
			IntegrationServices: source.Heartbeat, IntegrationVersion: source.IntegrationVersion, DynamicMemory: source.DynamicMemory,
		}
		if source.Generation >= 2 {
			vm.Firmware = "efi"
		} else {
			vm.Firmware = "bios"
		}
		if source.Version != "" {
			vm.HardwareVersion = "Hyper-V " + source.Version
		}
		if strings.Contains(strings.ToLower(source.GuestOS), "windows") && strings.HasPrefix(source.GuestOSVersion, "6.") {
			vm.GuestID = "windows"
		}
		bootSet := false
		for index, disk := range source.Disks {
			entry := vmDisk{
				Label: hypervDiskLabel(disk, index), Controller: disk.Controller, Bus: hypervAtoi(disk.ControllerNumber), Unit: hypervAtoi(disk.Location),
				CapacityBytes: disk.MaxInternalSize, File: disk.Path, Provisioning: hypervDiskProvisioning(disk), Mode: "persistent", Sharing: "sharingNone",
			}
			if disk.Passthrough {
				entry.RDM = "physical"
				entry.Label += " (pass-through disk " + vmNonEmpty(disk.HostDisk, "of the host") + ")"
			} else {
				entry.Datastore = hypervVolumeFor(disk.Path, host.Volumes)
				entry.LocalPath = hypervReachablePath(disk.Path, hostName, local)
				entry.ParentPath = disk.ParentPath
				entry.Format = strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.ReplaceAll(disk.Path, `\`, "/"))), ".")
			}
			if disk.Shared {
				entry.Sharing = "sharingMultiWriter"
			}
			if disk.Error != "" {
				inventory.Warnings = append(inventory.Warnings, source.Name+": "+disk.Path+": "+disk.Error)
			}
			if !bootSet && ((source.Generation < 2 && strings.EqualFold(disk.Controller, "IDE")) || source.Generation >= 2) {
				entry.BootCandidate, bootSet = true, true
			}
			if entry.Datastore != "" {
				store := addDatastore(entry.Datastore)
				store.ProvisionedBytes += disk.MaxInternalSize
			}
			vm.ProvisionedBytes += disk.MaxInternalSize
			vm.UsedBytes += disk.FileSize
			vm.Disks = append(vm.Disks, entry)
		}
		if !bootSet && len(vm.Disks) > 0 {
			vm.Disks[0].BootCandidate = true
		}
		counted := map[string]bool{}
		for _, disk := range vm.Disks {
			if disk.Datastore != "" && !counted[strings.ToLower(disk.Datastore)] {
				counted[strings.ToLower(disk.Datastore)] = true
				datastores[strings.ToLower(disk.Datastore)].VMCount++
			}
		}
		seenNetworks := map[string]bool{}
		for index, nic := range source.NICs {
			adapter := vmNonEmpty(nic.Name, fmt.Sprintf("Network adapter %d", index+1))
			kind := "Synthetic"
			if nic.Legacy {
				kind = "Legacy (emulated)"
			}
			entry := vmNIC{Label: adapter, AdapterType: kind, MAC: hypervMAC(nic.MAC), ManualMAC: nic.StaticMAC, Connected: nic.Connected && nic.Switch != "",
				IPAddresses: hypervAddressList(nic), Static: nic.GuestKnown && !nic.DHCP, GuestKnown: nic.GuestKnown}
			if nic.Switch != "" {
				network := addNetwork(nic.Switch, nic.VLAN)
				entry.Network = network.Name
				if !seenNetworks[strings.ToLower(network.Name)] {
					seenNetworks[strings.ToLower(network.Name)] = true
					network.VMCount++
				}
			}
			if vm.IPAddress == "" {
				for _, address := range nic.Addresses {
					if !strings.Contains(address, ":") {
						vm.IPAddress = address
						break
					}
				}
			}
			vm.NICs = append(vm.NICs, entry)
		}
		for _, checkpoint := range source.Checkpoints {
			created, _ := time.Parse(time.RFC3339Nano, checkpoint.Created)
			vm.Snapshots = append(vm.Snapshots, vmSnapshot{Name: checkpoint.Name, Created: created})
		}
		sort.SliceStable(vm.Snapshots, func(i, j int) bool { return vm.Snapshots[i].Created.Before(vm.Snapshots[j].Created) })
		for index := range vm.Snapshots {
			vm.Snapshots[index].Depth = index + 1
		}
		vm.ConnectedMedia = append(vm.ConnectedMedia, source.Media...)
		inventory.VMs = append(inventory.VMs, vm)
	}
	sort.SliceStable(inventory.VMs, func(i, j int) bool {
		return strings.ToLower(inventory.VMs[i].Name) < strings.ToLower(inventory.VMs[j].Name)
	})
	for _, key := range datastoreOrder {
		inventory.Datastores = append(inventory.Datastores, *datastores[key])
	}
	for _, key := range networkOrder {
		inventory.Networks = append(inventory.Networks, *networks[key])
	}
	assessInventory(inventory, now)
	return inventory
}

// parseHypervResult reads the script's result envelope.
func parseHypervResult(data []byte) (*hypervReport, error) {
	var envelope struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &envelope); err != nil {
		return nil, fmt.Errorf("the Hyper-V host's report could not be read: %v", err)
	}
	if !envelope.OK {
		return nil, fmt.Errorf("%s", vmNonEmpty(envelope.Error, "The Hyper-V host could not be read."))
	}
	var report hypervReport
	if err := json.Unmarshal(envelope.Result, &report); err != nil {
		return nil, fmt.Errorf("the Hyper-V host's report could not be read: %v", err)
	}
	return &report, nil
}

// hypervAddressList pairs each guest address with its subnet so the
// inventory shows it with its mask, as VMware's does (netmask.go).
func hypervAddressList(nic hypervNICReport) []string {
	out := make([]string, 0, len(nic.Addresses))
	for i, address := range nic.Addresses {
		mask := ""
		if i < len(nic.Subnets) {
			mask = nic.Subnets[i]
		}
		out = append(out, ipCIDRText(address, mask))
	}
	return out
}
