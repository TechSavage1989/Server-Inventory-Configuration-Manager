package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vmware/govmomi/license"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Full VMware discovery: everything vSphere reports about each host
// (hardware, BIOS, network, storage, time, services, software packages,
// alarms, certificate), licensing, cluster settings, resource pools and
// distributed switches. Like the rest of the inventory, it only reads
// properties and calls read-only query methods (FetchSoftwarePackages,
// HostImageConfigGetAcceptance, QueryAssignedLicenses); nothing is changed.

type vmLicenseRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type vmLicense struct {
	Name       string   `json:"name"`
	Edition    string   `json:"edition"`
	Key        string   `json:"key"`
	Product    string   `json:"product"`
	Version    string   `json:"version"`
	Total      int      `json:"total"`
	Used       int      `json:"used"`
	Unit       string   `json:"unit"`
	Expires    string   `json:"expires"`
	AssignedTo []string `json:"assignedTo"`
}

type vmResourcePool struct {
	Name              string `json:"name"`
	Datacenter        string `json:"datacenter"`
	Owner             string `json:"owner"`
	Parent            string `json:"parent"`
	VApp              bool   `json:"vApp"`
	CPUReservationMHz int64  `json:"cpuReservationMhz"`
	CPULimitMHz       int64  `json:"cpuLimitMhz"`
	MemReservationMB  int64  `json:"memReservationMb"`
	MemLimitMB        int64  `json:"memLimitMb"`
	VMCount           int    `json:"vmCount"`
}

type vmDistributedSwitch struct {
	Name       string   `json:"name"`
	Datacenter string   `json:"datacenter"`
	Version    string   `json:"version"`
	MTU        int      `json:"mtu"`
	Ports      int      `json:"ports"`
	Uplinks    int      `json:"uplinks"`
	Hosts      []string `json:"hosts"`
	Discovery  string   `json:"discovery"`
}

type vmHostNIC struct {
	Device   string `json:"device"`
	Driver   string `json:"driver"`
	Firmware string `json:"firmware"`
	MAC      string `json:"mac"`
	PCI      string `json:"pci"`
	SpeedMb  int    `json:"speedMb"`
	Duplex   bool   `json:"duplex"`
	Switch   string `json:"switch"`
}

type vmHostSwitch struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	MTU        int      `json:"mtu"`
	Ports      int      `json:"ports"`
	Uplinks    []string `json:"uplinks"`
	Portgroups []string `json:"portgroups"`
}

type vmHostVMkernel struct {
	Device    string   `json:"device"`
	Portgroup string   `json:"portgroup"`
	IP        string   `json:"ip"`
	Mask      string   `json:"mask"`
	DHCP      bool     `json:"dhcp"`
	MAC       string   `json:"mac"`
	MTU       int      `json:"mtu"`
	Stack     string   `json:"stack"`
	Services  []string `json:"services"`
}

type vmHostAdapter struct {
	Device     string `json:"device"`
	Kind       string `json:"kind"`
	Model      string `json:"model"`
	Driver     string `json:"driver"`
	Status     string `json:"status"`
	Identifier string `json:"identifier"`
	PCI        string `json:"pci"`
}

type vmHostLUN struct {
	CanonicalName string `json:"canonicalName"`
	DisplayName   string `json:"displayName"`
	Type          string `json:"type"`
	Vendor        string `json:"vendor"`
	Model         string `json:"model"`
	CapacityBytes int64  `json:"capacityBytes"`
	SSD           bool   `json:"ssd"`
	Local         bool   `json:"local"`
	State         string `json:"state"`
}

type vmHostService struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Running bool   `json:"running"`
	Policy  string `json:"policy"`
}

type vmHostPackage struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Vendor     string `json:"vendor"`
	Acceptance string `json:"acceptance"`
	Created    string `json:"created"`
}

type vmHostDetails struct {
	SerialNumber      string           `json:"serialNumber"`
	AssetTag          string           `json:"assetTag"`
	UUID              string           `json:"uuid"`
	BIOSVendor        string           `json:"biosVendor"`
	BIOSVersion       string           `json:"biosVersion"`
	BIOSDate          string           `json:"biosDate"`
	Firmware          string           `json:"firmware"`
	Hyperthreading    string           `json:"hyperthreading"`
	CPUUsagePercent   int              `json:"cpuUsagePercent"`
	MemUsagePercent   int              `json:"memUsagePercent"`
	BootTime          string           `json:"bootTime"`
	OverallStatus     string           `json:"overallStatus"`
	LockdownMode      string           `json:"lockdownMode"`
	ManagementIP      string           `json:"managementIp"`
	HostName          string           `json:"hostName"`
	Domain            string           `json:"domain"`
	DNSServers        []string         `json:"dnsServers"`
	SearchDomains     []string         `json:"searchDomains"`
	DefaultGateway    string           `json:"defaultGateway"`
	TimeZone          string           `json:"timeZone"`
	NTPServers        []string         `json:"ntpServers"`
	NTPRunning        bool             `json:"ntpRunning"`
	CertificateExpiry string           `json:"certificateExpiry"`
	CertificateIssuer string           `json:"certificateIssuer"`
	AcceptanceLevel   string           `json:"acceptanceLevel"`
	PhysicalNICs      []vmHostNIC      `json:"physicalNics"`
	Switches          []vmHostSwitch   `json:"switches"`
	VMkernel          []vmHostVMkernel `json:"vmkernel"`
	StorageAdapters   []vmHostAdapter  `json:"storageAdapters"`
	LUNs              []vmHostLUN      `json:"luns"`
	Services          []vmHostService  `json:"services"`
	Packages          []vmHostPackage  `json:"packages"`
	Alarms            []string         `json:"alarms"`
	ConfigIssues      []string         `json:"configIssues"`
}

// vmHostProperties are the HostSystem properties the inventory reads.
var vmHostProperties = []string{"name", "summary", "parent", "vm", "hardware", "config", "runtime", "triggeredAlarmState", "configIssue", "configManager"}

// vmMaskLicenseKey keeps the first and last group of a license key, which
// identifies it in vSphere's own lists, and hides the rest, so reports can
// be shared without handing out usable keys.
func vmMaskLicenseKey(key string) string {
	groups := strings.Split(key, "-")
	if len(groups) < 3 {
		if len(key) <= 5 {
			return key
		}
		return strings.Repeat("*", len(key)-5) + key[len(key)-5:]
	}
	for i := 1; i < len(groups)-1; i++ {
		groups[i] = strings.Repeat("*", len(groups[i]))
	}
	return strings.Join(groups, "-")
}

func vmLicenseProperty(info types.LicenseManagerLicenseInfo, key string) any {
	for _, property := range info.Properties {
		if property.Key == key {
			return property.Value
		}
	}
	return nil
}

func vmLicenseFrom(info types.LicenseManagerLicenseInfo) vmLicense {
	item := vmLicense{Name: info.Name, Edition: info.EditionKey, Key: vmMaskLicenseKey(info.LicenseKey), Total: int(info.Total), Used: int(info.Used), Unit: info.CostUnit, AssignedTo: []string{}}
	if value, ok := vmLicenseProperty(info, "ProductName").(string); ok {
		item.Product = value
	}
	if value, ok := vmLicenseProperty(info, "ProductVersion").(string); ok {
		item.Version = value
	}
	switch value := vmLicenseProperty(info, "expirationDate").(type) {
	case time.Time:
		item.Expires = value.Format("2006-01-02")
	case *time.Time:
		if value != nil {
			item.Expires = value.Format("2006-01-02")
		}
	}
	if item.Expires == "" && item.Total == 0 && strings.Contains(strings.ToLower(info.EditionKey), "eval") {
		item.Expires = "Evaluation"
	}
	return item
}

// vmReadLicenses reads the license inventory and what each license is
// assigned to. Standalone ESXi has no assignment manager, so a missing one
// is not an error.
func vmReadLicenses(ctx context.Context, client *vim25.Client, inventory *vmInventory) {
	manager := license.NewManager(client)
	list, err := manager.List(ctx)
	if err != nil {
		inventory.Warnings = append(inventory.Warnings, "Licenses could not be read: "+err.Error())
		return
	}
	byKey := map[string]int{}
	for _, info := range list {
		byKey[info.LicenseKey] = len(inventory.Licenses)
		inventory.Licenses = append(inventory.Licenses, vmLicenseFrom(info))
	}
	hostsByRef := map[string]int{}
	for i, host := range inventory.Hosts {
		hostsByRef[host.ref] = i
	}
	assignments, err := manager.AssignmentManager(ctx)
	if err != nil || assignments == nil {
		return
	}
	assigned, err := assignments.QueryAssigned(ctx, "")
	if err != nil {
		return
	}
	for _, assignment := range assigned {
		key := assignment.AssignedLicense.LicenseKey
		index, known := byKey[key]
		if !known {
			index = len(inventory.Licenses)
			byKey[key] = index
			inventory.Licenses = append(inventory.Licenses, vmLicenseFrom(assignment.AssignedLicense))
		}
		name := vmNonEmpty(assignment.EntityDisplayName, assignment.EntityId)
		if name == "" {
			continue
		}
		inventory.Licenses[index].AssignedTo = append(inventory.Licenses[index].AssignedTo, name)
		if hostIndex, ok := hostsByRef[assignment.EntityId]; ok {
			inventory.Hosts[hostIndex].License = &vmLicenseRef{Name: inventory.Licenses[index].Name, Key: inventory.Licenses[index].Key}
		}
	}
	for i := range inventory.Licenses {
		sort.Strings(inventory.Licenses[i].AssignedTo)
	}
}

func vmDurationText(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func vmPercent(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	return int(part * 100 / whole)
}

// vmHostDetailsFrom turns what vSphere reports about one host into the
// discovery record. It has no network access, so it is unit tested directly.
func vmHostDetailsFrom(host mo.HostSystem) *vmHostDetails {
	details := &vmHostDetails{OverallStatus: string(host.Summary.OverallStatus), DNSServers: []string{}, SearchDomains: []string{}, NTPServers: []string{}, PhysicalNICs: []vmHostNIC{}, Switches: []vmHostSwitch{}, VMkernel: []vmHostVMkernel{}, StorageAdapters: []vmHostAdapter{}, LUNs: []vmHostLUN{}, Services: []vmHostService{}, Packages: []vmHostPackage{}, Alarms: []string{}, ConfigIssues: []string{}}
	details.ManagementIP = host.Summary.ManagementServerIp
	if hardware := host.Hardware; hardware != nil {
		info := hardware.SystemInfo
		details.UUID, details.SerialNumber = info.Uuid, info.SerialNumber
		for _, other := range info.OtherIdentifyingInfo {
			description := other.IdentifierType.GetElementDescription()
			if description == nil {
				continue
			}
			switch description.Key {
			case "ServiceTag", "SerialNumberTag", "EnclosureSerialNumberTag":
				if details.SerialNumber == "" && strings.TrimSpace(other.IdentifierValue) != "" {
					details.SerialNumber = strings.TrimSpace(other.IdentifierValue)
				}
			case "AssetTag":
				details.AssetTag = strings.TrimSpace(other.IdentifierValue)
			}
		}
		if bios := hardware.BiosInfo; bios != nil {
			details.BIOSVendor, details.BIOSVersion, details.Firmware = bios.Vendor, bios.BiosVersion, bios.FirmwareType
			if bios.ReleaseDate != nil && !bios.ReleaseDate.IsZero() {
				details.BIOSDate = bios.ReleaseDate.Format("2006-01-02")
			}
		}
	}
	if hardware := host.Summary.Hardware; hardware != nil {
		stats := host.Summary.QuickStats
		details.CPUUsagePercent = vmPercent(int64(stats.OverallCpuUsage), int64(hardware.CpuMhz)*int64(hardware.NumCpuCores))
		details.MemUsagePercent = vmPercent(int64(stats.OverallMemoryUsage)<<20, hardware.MemorySize)
	}
	details.BootTime = vmDurationText(host.Runtime.BootTime)
	if host.Runtime.BootTime == nil && host.Summary.Runtime != nil {
		details.BootTime = vmDurationText(host.Summary.Runtime.BootTime)
	}
	for _, alarm := range host.TriggeredAlarmState {
		details.Alarms = append(details.Alarms, string(alarm.OverallStatus)+"|"+alarm.Alarm.Value)
	}
	for _, issue := range host.ConfigIssue {
		if event := issue.GetEvent(); event != nil && strings.TrimSpace(event.FullFormattedMessage) != "" {
			details.ConfigIssues = append(details.ConfigIssues, strings.TrimSpace(event.FullFormattedMessage))
		}
	}
	config := host.Config
	if config == nil {
		return details
	}
	if config.HyperThread != nil {
		switch {
		case !config.HyperThread.Available:
			details.Hyperthreading = "Not available"
		case config.HyperThread.Active:
			details.Hyperthreading = "Active"
		default:
			details.Hyperthreading = "Available, off"
		}
	}
	details.LockdownMode = string(config.LockdownMode)
	if len(config.Certificate) > 0 {
		data := []byte(config.Certificate)
		if block, _ := pem.Decode(data); block != nil {
			data = block.Bytes
		}
		if certificate, err := x509.ParseCertificate(data); err == nil {
			details.CertificateExpiry = certificate.NotAfter.UTC().Format("2006-01-02")
			details.CertificateIssuer = certificate.Issuer.CommonName
		}
	}
	if time := config.DateTimeInfo; time != nil {
		if time.TimeZone.Name != "" {
			details.TimeZone = time.TimeZone.Name
		} else {
			details.TimeZone = time.TimeZone.Key
		}
		if time.NtpConfig != nil {
			details.NTPServers = append(details.NTPServers, time.NtpConfig.Server...)
		}
	}
	if config.Service != nil {
		for _, service := range config.Service.Service {
			details.Services = append(details.Services, vmHostService{Key: service.Key, Label: service.Label, Running: service.Running, Policy: service.Policy})
			if service.Key == "ntpd" && service.Running {
				details.NTPRunning = true
			}
		}
	}
	vmkernelServices := map[string][]string{}
	if config.VirtualNicManagerInfo != nil {
		for _, netConfig := range config.VirtualNicManagerInfo.NetConfig {
			for _, selected := range netConfig.SelectedVnic {
				for _, candidate := range netConfig.CandidateVnic {
					if candidate.Key == selected {
						vmkernelServices[candidate.Device] = append(vmkernelServices[candidate.Device], netConfig.NicType)
					}
				}
			}
		}
	}
	if network := config.Network; network != nil {
		if dns, ok := network.DnsConfig.(*types.HostDnsConfig); ok && dns != nil {
			details.HostName, details.Domain = dns.HostName, dns.DomainName
			details.DNSServers = append(details.DNSServers, dns.Address...)
			details.SearchDomains = append(details.SearchDomains, dns.SearchDomain...)
		}
		if route := network.IpRouteConfig; route != nil {
			if config := route.GetHostIpRouteConfig(); config != nil {
				details.DefaultGateway = config.DefaultGateway
			}
		}
		uplinkSwitch := map[string]string{}
		for _, item := range network.Vswitch {
			uplinks := []string{}
			for _, key := range item.Pnic {
				device := vmPnicDevice(network.Pnic, key)
				uplinks = append(uplinks, device)
				uplinkSwitch[device] = item.Name
			}
			portgroups := []string{}
			for _, key := range item.Portgroup {
				portgroups = append(portgroups, strings.TrimPrefix(key, "key-vim.host.PortGroup-"))
			}
			details.Switches = append(details.Switches, vmHostSwitch{Name: item.Name, Kind: "Standard", MTU: int(item.Mtu), Ports: int(item.NumPorts), Uplinks: uplinks, Portgroups: portgroups})
		}
		for _, item := range network.ProxySwitch {
			uplinks := []string{}
			for _, key := range item.Pnic {
				device := vmPnicDevice(network.Pnic, key)
				uplinks = append(uplinks, device)
				uplinkSwitch[device] = item.DvsName
			}
			details.Switches = append(details.Switches, vmHostSwitch{Name: item.DvsName, Kind: "Distributed", MTU: int(item.Mtu), Ports: int(item.NumPorts), Uplinks: uplinks, Portgroups: []string{}})
		}
		for _, pnic := range network.Pnic {
			item := vmHostNIC{Device: pnic.Device, Driver: strings.TrimSpace(pnic.Driver + " " + pnic.DriverVersion), Firmware: pnic.FirmwareVersion, MAC: pnic.Mac, PCI: pnic.Pci, Switch: uplinkSwitch[pnic.Device]}
			if pnic.LinkSpeed != nil {
				item.SpeedMb, item.Duplex = int(pnic.LinkSpeed.SpeedMb), pnic.LinkSpeed.Duplex
			}
			details.PhysicalNICs = append(details.PhysicalNICs, item)
		}
		for _, vnic := range network.Vnic {
			item := vmHostVMkernel{Device: vnic.Device, Portgroup: vnic.Portgroup, MAC: vnic.Spec.Mac, MTU: int(vnic.Spec.Mtu), Stack: vnic.Spec.NetStackInstanceKey, Services: vmkernelServices[vnic.Device]}
			if item.Services == nil {
				item.Services = []string{}
			}
			if item.Portgroup == "" && vnic.Spec.DistributedVirtualPort != nil {
				item.Portgroup = "Distributed port " + vnic.Spec.DistributedVirtualPort.PortKey
			}
			if ip := vnic.Spec.Ip; ip != nil {
				item.IP, item.Mask, item.DHCP = ip.IpAddress, ip.SubnetMask, ip.Dhcp
			}
			if details.ManagementIP == "" && vmContains(item.Services, "management") {
				details.ManagementIP = item.IP
			}
			details.VMkernel = append(details.VMkernel, item)
		}
	}
	if storage := config.StorageDevice; storage != nil {
		for _, base := range storage.HostBusAdapter {
			adapter := base.GetHostHostBusAdapter()
			if adapter == nil {
				continue
			}
			item := vmHostAdapter{Device: adapter.Device, Model: adapter.Model, Driver: adapter.Driver, Status: adapter.Status, PCI: adapter.Pci, Kind: vmNonEmpty(adapter.StorageProtocol, "scsi")}
			switch hba := base.(type) {
			case *types.HostFibreChannelHba:
				item.Kind = "Fibre Channel"
				item.Identifier = fmt.Sprintf("WWPN %016x", uint64(hba.PortWorldWideName))
			case *types.HostFibreChannelOverEthernetHba:
				item.Kind = "FCoE"
				item.Identifier = fmt.Sprintf("WWPN %016x", uint64(hba.PortWorldWideName))
			case *types.HostInternetScsiHba:
				item.Kind = "iSCSI"
				if hba.IsSoftwareBased {
					item.Kind = "iSCSI (software)"
				}
				item.Identifier = hba.IScsiName
			case *types.HostParallelScsiHba:
				item.Kind = "SCSI"
			case *types.HostBlockHba:
				item.Kind = "Block (SATA/NVMe/local)"
			case *types.HostSerialAttachedHba:
				item.Kind = "SAS"
			case *types.HostPcieHba, *types.HostTcpHba, *types.HostRdmaHba:
				item.Kind = "NVMe"
			}
			details.StorageAdapters = append(details.StorageAdapters, item)
		}
		for _, base := range storage.ScsiLun {
			lun := base.GetScsiLun()
			if lun == nil {
				continue
			}
			item := vmHostLUN{CanonicalName: lun.CanonicalName, DisplayName: lun.DisplayName, Type: lun.LunType, Vendor: strings.TrimSpace(lun.Vendor), Model: strings.TrimSpace(lun.Model), State: strings.Join(lun.OperationalState, ", ")}
			if disk, ok := base.(*types.HostScsiDisk); ok {
				item.CapacityBytes = disk.Capacity.Block * int64(disk.Capacity.BlockSize)
				item.SSD = disk.Ssd != nil && *disk.Ssd
				item.Local = disk.LocalDisk != nil && *disk.LocalDisk
			}
			details.LUNs = append(details.LUNs, item)
		}
	}
	return details
}

func vmPnicDevice(pnics []types.PhysicalNic, key string) string {
	for _, pnic := range pnics {
		if pnic.Key == key {
			return pnic.Device
		}
	}
	return strings.TrimPrefix(key, "key-vim.host.PhysicalNic-")
}

func vmContains(values []string, value string) bool {
	for _, item := range values {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

// vmReadHostExtras reads each host's software packages and acceptance level
// and resolves triggered alarm names. Failures become one warning each, so
// an ESXi build that does not answer these calls still gives a full
// inventory.
func vmReadHostExtras(ctx context.Context, client *vim25.Client, hosts []mo.HostSystem, inventory *vmInventory, first int) {
	alarmRefs := []types.ManagedObjectReference{}
	seen := map[string]bool{}
	for _, host := range hosts {
		for _, alarm := range host.TriggeredAlarmState {
			if !seen[alarm.Alarm.Value] {
				seen[alarm.Alarm.Value] = true
				alarmRefs = append(alarmRefs, alarm.Alarm)
			}
		}
	}
	alarmNames := map[string]string{}
	if len(alarmRefs) > 0 {
		var alarms []mo.Alarm
		if err := property.DefaultCollector(client).Retrieve(ctx, alarmRefs, []string{"info.name"}, &alarms); err == nil {
			for _, alarm := range alarms {
				if alarm.Info.Name != "" {
					alarmNames[alarm.Reference().Value] = alarm.Info.Name
				}
			}
		}
	}
	packageFailures := []string{}
	for i, host := range hosts {
		item := &inventory.Hosts[first+i]
		if item.Details == nil {
			continue
		}
		for j, alarm := range item.Details.Alarms {
			parts := strings.SplitN(alarm, "|", 2)
			if len(parts) == 2 {
				item.Details.Alarms[j] = vmNonEmpty(alarmNames[parts[1]], "Alarm "+parts[1]) + " (" + parts[0] + ")"
			}
		}
		if host.ConfigManager.ImageConfigManager == nil || !strings.EqualFold(item.ConnectionState, "connected") {
			continue
		}
		manager := *host.ConfigManager.ImageConfigManager
		if acceptance, err := methods.HostImageConfigGetAcceptance(ctx, client, &types.HostImageConfigGetAcceptance{This: manager}); err == nil {
			item.Details.AcceptanceLevel = acceptance.Returnval
		}
		packages, err := methods.FetchSoftwarePackages(ctx, client, &types.FetchSoftwarePackages{This: manager})
		if err != nil {
			packageFailures = append(packageFailures, item.Name)
			continue
		}
		for _, software := range packages.Returnval {
			created := ""
			if software.CreationDate != nil && !software.CreationDate.IsZero() {
				created = software.CreationDate.Format("2006-01-02")
			}
			item.Details.Packages = append(item.Details.Packages, vmHostPackage{Name: software.Name, Version: software.Version, Vendor: software.Vendor, Acceptance: software.AcceptanceLevel, Created: created})
		}
		sort.Slice(item.Details.Packages, func(a, b int) bool { return item.Details.Packages[a].Name < item.Details.Packages[b].Name })
	}
	if len(packageFailures) > 0 {
		inventory.Warnings = append(inventory.Warnings, "Installed software packages (VIBs) could not be listed on "+strings.Join(packageFailures, ", ")+".")
	}
}

// vmClusterSettings adds EVC, vSAN, HA and DRS details to a cluster.
func vmClusterSettings(cluster mo.ClusterComputeResource, item *vmCluster) {
	if summary, ok := cluster.Summary.(*types.ClusterComputeResourceSummary); ok && summary != nil {
		item.EVCMode = summary.CurrentEVCModeKey
	}
	config, ok := cluster.ConfigurationEx.(*types.ClusterConfigInfoEx)
	if !ok || config == nil {
		return
	}
	item.HAAdmission = config.DasConfig.AdmissionControlEnabled != nil && *config.DasConfig.AdmissionControlEnabled
	item.HAHostMonitoring = config.DasConfig.HostMonitoring
	if item.DRS {
		item.DRSBehavior = string(config.DrsConfig.DefaultVmBehavior)
	}
	item.VSAN = config.VsanConfigInfo != nil && config.VsanConfigInfo.Enabled != nil && *config.VsanConfigInfo.Enabled
}

func vmAllocation(info types.ResourceAllocationInfo) (int64, int64) {
	reservation, limit := int64(0), int64(-1)
	if info.Reservation != nil {
		reservation = *info.Reservation
	}
	if info.Limit != nil {
		limit = *info.Limit
	}
	return reservation, limit
}

// vmReadPools reads resource pools and vApps (not the hidden root pool of
// each cluster or host).
func vmReadPools(ctx context.Context, retrieve func(kind string, properties []string, dst any) error, datacenter string, owners map[string]string, inventory *vmInventory) {
	var pools []mo.ResourcePool
	if err := retrieve("ResourcePool", []string{"name", "config", "vm", "parent", "owner"}, &pools); err != nil {
		return
	}
	names := map[string]string{}
	for _, pool := range pools {
		names[pool.Reference().Value] = pool.Name
	}
	// The container view returns vApps with the resource pools, since a
	// vApp is a kind of resource pool.
	for _, pool := range pools {
		vApp := pool.Reference().Type == "VirtualApp"
		if pool.Parent == nil || pool.Parent.Type != "ResourcePool" && pool.Parent.Type != "VirtualApp" {
			continue
		}
		item := vmResourcePool{Name: pool.Name, Datacenter: datacenter, VApp: vApp, VMCount: len(pool.Vm), Parent: names[pool.Parent.Value]}
		if item.Parent == "Resources" {
			item.Parent = ""
		}
		item.Owner = owners[pool.Owner.Value]
		item.CPUReservationMHz, item.CPULimitMHz = vmAllocation(pool.Config.CpuAllocation)
		item.MemReservationMB, item.MemLimitMB = vmAllocation(pool.Config.MemoryAllocation)
		inventory.ResourcePools = append(inventory.ResourcePools, item)
	}
}

// vmReadSwitches reads distributed switches with their version, MTU and
// member hosts.
func vmReadSwitches(switches []mo.DistributedVirtualSwitch, datacenter string, hostNames map[string]string, inventory *vmInventory) {
	for _, item := range switches {
		config := item.Config
		if config == nil {
			continue
		}
		base := config.GetDVSConfigInfo()
		if base == nil {
			continue
		}
		record := vmDistributedSwitch{Name: item.Name, Datacenter: datacenter, Version: base.ProductInfo.Version, Ports: int(base.NumPorts), Uplinks: len(base.UplinkPortgroup), Hosts: []string{}}
		if policy, ok := base.UplinkPortPolicy.(*types.DVSNameArrayUplinkPortPolicy); ok && policy != nil {
			record.Uplinks = len(policy.UplinkPortName)
		}
		if vmware, ok := config.(*types.VMwareDVSConfigInfo); ok {
			record.MTU = int(vmware.MaxMtu)
			if vmware.LinkDiscoveryProtocolConfig != nil {
				record.Discovery = vmware.LinkDiscoveryProtocolConfig.Protocol + " " + vmware.LinkDiscoveryProtocolConfig.Operation
			}
		}
		for _, member := range base.Host {
			if member.Config.Host != nil {
				record.Hosts = append(record.Hosts, vmNonEmpty(hostNames[member.Config.Host.Value], member.Config.Host.Value))
			}
		}
		sort.Strings(record.Hosts)
		inventory.Switches = append(inventory.Switches, record)
	}
}

// vmDiscoveryWatch lists infrastructure facts that need attention: license
// and certificate expiry, alarms and configuration issues, hosts not
// connected or in maintenance.
func vmDiscoveryWatch(inventory *vmInventory, now time.Time) []string {
	notes := []string{}
	soon := now.AddDate(0, 0, 90)
	for _, item := range inventory.Licenses {
		if expires, err := time.Parse("2006-01-02", item.Expires); err == nil {
			if expires.Before(now) {
				notes = append(notes, fmt.Sprintf("License %s (%s) expired on %s.", vmNonEmpty(item.Name, item.Edition), item.Key, item.Expires))
			} else if expires.Before(soon) {
				notes = append(notes, fmt.Sprintf("License %s (%s) expires on %s.", vmNonEmpty(item.Name, item.Edition), item.Key, item.Expires))
			}
		}
		if item.Total > 0 && item.Used > item.Total {
			notes = append(notes, fmt.Sprintf("License %s (%s) is over-allocated: %d of %d %s used.", vmNonEmpty(item.Name, item.Edition), item.Key, item.Used, item.Total, item.Unit))
		}
	}
	for _, host := range inventory.Hosts {
		if !strings.EqualFold(host.ConnectionState, "connected") && host.ConnectionState != "" {
			notes = append(notes, fmt.Sprintf("Host %s is %s.", host.Name, host.ConnectionState))
		}
		if host.Maintenance {
			notes = append(notes, fmt.Sprintf("Host %s is in maintenance mode.", host.Name))
		}
		details := host.Details
		if details == nil {
			continue
		}
		if expires, err := time.Parse("2006-01-02", details.CertificateExpiry); err == nil && expires.Before(soon) {
			notes = append(notes, fmt.Sprintf("Host %s's certificate expires on %s.", host.Name, details.CertificateExpiry))
		}
		for _, alarm := range details.Alarms {
			notes = append(notes, fmt.Sprintf("Host %s alarm: %s.", host.Name, alarm))
		}
		for _, issue := range details.ConfigIssues {
			notes = append(notes, fmt.Sprintf("Host %s: %s", host.Name, issue))
		}
		if len(details.NTPServers) == 0 && len(details.Services) > 0 {
			notes = append(notes, fmt.Sprintf("Host %s has no NTP server.", host.Name))
		}
	}
	return notes
}
