package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// VMware migration planning: the inventory read from vCenter or a standalone
// ESXi host (vmware_collect.go), the per-VM readiness checks for every
// migration target (profiles in vmware_targets.go), and the migration plan (target mapping, sizing
// and waves). Nothing here changes the VMware environment. This file has no
// Windows dependency so the rules are unit tested on any platform.

const (
	vmSeverityBlocker = "blocker"
	vmSeverityWarning = "warning"
	vmSeverityInfo    = "info"

	vmStatusReady   = "ready"
	vmStatusReview  = "review"
	vmStatusBlocked = "blocked"
	// vmStatusSkip marks VMware's own machines (vCenter, vCLS agents,
	// appliances, nested ESXi), which are retired with VMware, not moved.
	vmStatusSkip = "skip"

	// vmToolsGuestManaged is the ToolsVersion vSphere reports when the guest
	// manages its own tools (open-vm-tools packaged by the operating system).
	vmToolsGuestManaged = "2147483647"
)

var (
	vmBackupSnapshotPattern = regexp.MustCompile(`(?i)acronis|veeam|commvault|nakivo|cohesity|rubrik|unitrends|altaro|vranger|arcserve|datto|backup|vdp-`)
	vmDomainControllerName  = regexp.MustCompile(`(?i)(^|[-_. ])(dc|adc|addc|pdc|bdc|ads|ad)[0-9]{0,3}($|[-_. ])|domaincontroller`)
)

// vmInfrastructureRole names the part of VMware a VM is, or returns "" for
// an ordinary workload.
func vmInfrastructureRole(vm vmMachine) string {
	name := strings.ToLower(vm.Name)
	osText := strings.ToLower(vm.GuestOS + " " + vm.GuestOSRunning + " " + vm.GuestID)
	switch {
	case strings.HasPrefix(name, "vcls-") || strings.HasPrefix(name, "vcls (") || name == "vcls":
		return "a vSphere Cluster Services (vCLS) agent VM"
	case strings.Contains(name, "vcenter") || strings.Contains(name, "vcsa"):
		if strings.Contains(osText, "photon") || strings.Contains(osText, "vmkernel") || strings.Contains(osText, "linux") {
			return "the vCenter Server appliance"
		}
	case strings.Contains(osText, "photon"):
		return "a VMware appliance (Photon OS)"
	case strings.Contains(strings.ToLower(vm.GuestID), "vmkernel"):
		return "a nested ESXi host"
	}
	return ""
}

// vmToolsVersionText turns vSphere's tools version into readable text.
func vmToolsVersionText(version string) string {
	if version == vmToolsGuestManaged {
		return "guest-managed (open-vm-tools)"
	}
	return version
}

type vmInventory struct {
	Schema      int           `json:"schema"`
	Server      string        `json:"server"`
	ServerType  string        `json:"serverType"`
	Product     string        `json:"product"`
	APIVersion  string        `json:"apiVersion"`
	CollectedAt time.Time     `json:"collectedAt"`
	CollectedBy string        `json:"collectedBy"`
	Datacenters []string      `json:"datacenters"`
	Clusters    []vmCluster   `json:"clusters"`
	Hosts       []vmHost      `json:"hosts"`
	Datastores  []vmDatastore `json:"datastores"`
	Networks    []vmNetwork   `json:"networks"`
	VMs         []vmMachine   `json:"vms"`
	// Full discovery (vmware_discovery.go); empty in inventories saved by
	// 3.2.29 and earlier.
	Build         string                `json:"build,omitempty"`
	InstanceUUID  string                `json:"instanceUuid,omitempty"`
	OSType        string                `json:"osType,omitempty"`
	Licenses      []vmLicense           `json:"licenses,omitempty"`
	ResourcePools []vmResourcePool      `json:"resourcePools,omitempty"`
	Switches      []vmDistributedSwitch `json:"switches,omitempty"`
	Warnings      []string              `json:"warnings"`
	AppVersion    string                `json:"appVersion"`
	SourceFolder  string                `json:"-"`
}

type vmCluster struct {
	Name             string `json:"name"`
	Datacenter       string `json:"datacenter"`
	Hosts            int    `json:"hosts"`
	HA               bool   `json:"ha"`
	DRS              bool   `json:"drs"`
	CPUCores         int    `json:"cpuCores"`
	MemoryBytes      int64  `json:"memoryBytes"`
	EVCMode          string `json:"evcMode,omitempty"`
	VSAN             bool   `json:"vsan,omitempty"`
	HAAdmission      bool   `json:"haAdmissionControl,omitempty"`
	HAHostMonitoring string `json:"haHostMonitoring,omitempty"`
	DRSBehavior      string `json:"drsBehavior,omitempty"`
}

type vmHost struct {
	Name            string `json:"name"`
	Datacenter      string `json:"datacenter"`
	Cluster         string `json:"cluster"`
	Vendor          string `json:"vendor"`
	Model           string `json:"model"`
	CPUModel        string `json:"cpuModel"`
	Sockets         int    `json:"sockets"`
	Cores           int    `json:"cores"`
	Threads         int    `json:"threads"`
	CPUMhz          int    `json:"cpuMhz"`
	MemoryBytes     int64  `json:"memoryBytes"`
	Version         string `json:"version"`
	Build           string `json:"build"`
	ConnectionState string `json:"connectionState"`
	PowerState      string `json:"powerState"`
	Maintenance     bool   `json:"maintenance"`
	VMCount         int    `json:"vmCount"`
	// Full discovery (vmware_discovery.go).
	License *vmLicenseRef  `json:"license,omitempty"`
	Details *vmHostDetails `json:"details,omitempty"`
	ref     string
}

type vmDatastore struct {
	Name             string `json:"name"`
	Datacenter       string `json:"datacenter"`
	Type             string `json:"type"`
	CapacityBytes    int64  `json:"capacityBytes"`
	FreeBytes        int64  `json:"freeBytes"`
	ProvisionedBytes int64  `json:"provisionedBytes"`
	Accessible       bool   `json:"accessible"`
	Shared           bool   `json:"shared"`
	VMCount          int    `json:"vmCount"`
	// Hosts that mount the datastore and its URL (3.2.31 and later).
	Hosts []string `json:"hosts,omitempty"`
	URL   string   `json:"url,omitempty"`
}

type vmNetwork struct {
	Name       string `json:"name"`
	Datacenter string `json:"datacenter"`
	Kind       string `json:"kind"`
	Switch     string `json:"switch"`
	VLAN       string `json:"vlan"`
	VMCount    int    `json:"vmCount"`
}

type vmDisk struct {
	Label         string `json:"label"`
	Controller    string `json:"controller"`
	Bus           int    `json:"bus"`
	Unit          int    `json:"unit"`
	CapacityBytes int64  `json:"capacityBytes"`
	Datastore     string `json:"datastore"`
	File          string `json:"file"`
	Provisioning  string `json:"provisioning"`
	Mode          string `json:"mode"`
	RDM           string `json:"rdm"`
	Sharing       string `json:"sharing"`
	BusSharing    string `json:"busSharing"`
	BootCandidate bool   `json:"bootCandidate"`
	// Encrypted is true when this disk itself is encrypted with vSphere VM
	// Encryption. A VM with a virtual TPM has encrypted home files but can
	// have plain disks, which copy as they are.
	Encrypted      bool   `json:"encrypted,omitempty"`
	ControllerType string `json:"-"`
	// Hyper-V source: the path this computer opens the disk through (an
	// administrative share for a remote host), its parent and extension.
	LocalPath  string `json:"localPath,omitempty"`
	ParentPath string `json:"parentPath,omitempty"`
	Format     string `json:"format,omitempty"`
}

type vmNIC struct {
	Label       string   `json:"label"`
	AdapterType string   `json:"adapterType"`
	Network     string   `json:"network"`
	MAC         string   `json:"mac"`
	ManualMAC   bool     `json:"manualMac"`
	Connected   bool     `json:"connected"`
	IPAddresses []string `json:"ipAddresses"`
	// Static is true when VMware Tools reports DHCP off or a manually set
	// address on this adapter. Unknown (no Tools data) is false.
	Static     bool `json:"static"`
	GuestKnown bool `json:"guestKnown"`
	// What VMware Tools reports about the adapter's settings inside the
	// guest, so they can be put back after a move without running anything
	// in the old VM (vmGuestCaptureFromInventory). Empty when Tools did not
	// answer or the inventory was saved by an older version.
	MACInGuest      string   `json:"macInGuest,omitempty"`
	StaticAddresses []string `json:"staticAddresses,omitempty"`
	DHCP            bool     `json:"dhcp,omitempty"`
	Gateways        []string `json:"gateways,omitempty"`
	DNSServers      []string `json:"dnsServers,omitempty"`
	DNSSuffix       string   `json:"dnsSuffix,omitempty"`
}

type vmSnapshot struct {
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Quiesced bool      `json:"quiesced"`
	Depth    int       `json:"depth"`
}

type vmGuestDisk struct {
	Path          string `json:"path"`
	CapacityBytes int64  `json:"capacityBytes"`
	FreeBytes     int64  `json:"freeBytes"`
}

type vmMachine struct {
	Name            string `json:"name"`
	ID              string `json:"id"`
	InstanceUUID    string `json:"instanceUuid"`
	Datacenter      string `json:"datacenter"`
	Cluster         string `json:"cluster"`
	Host            string `json:"host"`
	Folder          string `json:"folder"`
	Template        bool   `json:"template"`
	PowerState      string `json:"powerState"`
	GuestID         string `json:"guestId"`
	GuestOS         string `json:"guestOs"`
	GuestOSRunning  string `json:"guestOsRunning"`
	GuestHostName   string `json:"guestHostName"`
	IPAddress       string `json:"ipAddress"`
	CPUs            int    `json:"cpus"`
	CoresPerSocket  int    `json:"coresPerSocket"`
	MemoryMB        int64  `json:"memoryMb"`
	HardwareVersion string `json:"hardwareVersion"`
	Firmware        string `json:"firmware"`
	SecureBoot      bool   `json:"secureBoot"`
	VTPM            bool   `json:"vtpm"`
	Encrypted       bool   `json:"encrypted"`
	// EncryptionRead is true when the inventory recorded which disks are
	// encrypted (vmDisk.Encrypted); older saved inventories lack it.
	EncryptionRead      bool         `json:"encryptionRead,omitempty"`
	FaultTolerance      bool         `json:"faultTolerance"`
	ChangeTracking      bool         `json:"changeTracking"`
	ToolsStatus         string       `json:"toolsStatus"`
	ToolsRunning        bool         `json:"toolsRunning"`
	ToolsVersion        string       `json:"toolsVersion"`
	ProvisionedBytes    int64        `json:"provisionedBytes"`
	UsedBytes           int64        `json:"usedBytes"`
	Disks               []vmDisk     `json:"disks"`
	NICs                []vmNIC      `json:"nics"`
	Snapshots           []vmSnapshot `json:"snapshots"`
	ConsolidationNeeded bool         `json:"consolidationNeeded"`
	ConnectedMedia      []string     `json:"connectedMedia"`
	// MediaImages are the image files CD/DVD and floppy drives name,
	// connected or not (a missing ISO cannot connect, vmware_media.go).
	MediaImages   []string      `json:"mediaImages,omitempty"`
	Passthrough   []string      `json:"passthrough"`
	USBDevices    int           `json:"usbDevices"`
	SerialPorts   int           `json:"serialPorts"`
	ParallelPorts int           `json:"parallelPorts"`
	GuestDisks    []vmGuestDisk `json:"guestDisks"`
	Readiness     []vmReadiness `json:"readiness"`
	// Hyper-V source (hyperv_source.go); empty for VMware.
	Source              string `json:"source,omitempty"`
	Generation          int    `json:"generation,omitempty"`
	ConfigVersion       string `json:"configVersion,omitempty"`
	IntegrationServices string `json:"integrationServices,omitempty"`
	IntegrationVersion  string `json:"integrationVersion,omitempty"`
	DynamicMemory       bool   `json:"dynamicMemory,omitempty"`
	// Preflight is the last optional domain controller and SQL Server
	// check (preflight.go); nil when it was never run.
	Preflight *vmPreflight `json:"preflight,omitempty"`
}

type vmFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Action   string `json:"action"`
}

type vmReadiness struct {
	Target     string      `json:"target"`
	Status     string      `json:"status"`
	Generation int         `json:"generation,omitempty"`
	Findings   []vmFinding `json:"findings"`
}

func (vm vmMachine) readinessFor(target string) *vmReadiness {
	for index := range vm.Readiness {
		if vm.Readiness[index].Target == target {
			return &vm.Readiness[index]
		}
	}
	return nil
}

// osClass groups guest operating systems by how they move to the targets.
type vmOSClass struct {
	Family    string // windows, linux, bsd, other, unknown
	Name      string
	Supported string // current, legacy, obsolete, unsupported, unknown
	Bits64    bool
	Server    bool
}

var (
	vmWindowsServerYear = regexp.MustCompile(`(?i)windows\s+server\s+(2003|2008\s*r2|2008|2012\s*r2|2012|2016|2019|2022|2025)`)
	vmWindowsClient     = regexp.MustCompile(`(?i)windows\s+(xp|vista|7|8\.1|8|10|11)\b`)
	vmLinuxPattern      = regexp.MustCompile(`(?i)linux|ubuntu|debian|red\s*hat|rhel|centos|rocky|alma|suse|sles|oracle|photon|fedora|amazon`)
	vmLinuxVersion      = regexp.MustCompile(`(?i)(red hat enterprise linux|rhel|centos|rocky|alma|oracle linux|ubuntu|debian|suse linux enterprise|sles)[^0-9]*([0-9]+)`)
)

// classifyGuestOS uses the running guest's name when VMware Tools reports
// it, otherwise the configured guest name and ID.
func classifyGuestOS(vm vmMachine) vmOSClass {
	name := strings.TrimSpace(vm.GuestOSRunning)
	if name == "" {
		name = strings.TrimSpace(vm.GuestOS)
	}
	id := strings.ToLower(vm.GuestID)
	text := name + " " + id
	class := vmOSClass{Family: "unknown", Name: name, Supported: "unknown", Bits64: strings.Contains(id, "64") || strings.Contains(strings.ToLower(name), "64-bit")}
	if name == "" {
		class.Name = vm.GuestID
	}
	lowerText := strings.ToLower(text)
	switch {
	case strings.Contains(lowerText, "windows") || strings.HasPrefix(id, "win"):
		class.Family = "windows"
		if match := vmWindowsServerYear.FindStringSubmatch(name); match != nil {
			class.Server = true
			year := strings.ToLower(strings.Join(strings.Fields(match[1]), ""))
			switch year {
			case "2016", "2019", "2022", "2025":
				class.Supported = "current"
			case "2012", "2012r2":
				class.Supported = "legacy"
			case "2008", "2008r2":
				class.Supported = "obsolete"
			default:
				class.Supported = "unsupported"
			}
			break
		}
		if match := vmWindowsClient.FindStringSubmatch(name); match != nil {
			switch strings.ToLower(match[1]) {
			case "10", "11":
				class.Supported = "current"
			case "8", "8.1", "7":
				class.Supported = "obsolete"
			default:
				class.Supported = "unsupported"
			}
			break
		}
		// Fall back to VMware guest IDs when the name is missing.
		switch {
		case strings.Contains(id, "windows2019srv"), strings.Contains(id, "windows2022srv"), strings.Contains(id, "windows9server"), strings.Contains(id, "windows9_64"), strings.Contains(id, "windows11"), strings.Contains(id, "windows9guest"):
			class.Supported = "current"
			class.Server = strings.Contains(id, "srv") || strings.Contains(id, "server")
		case strings.Contains(id, "windows8server"):
			class.Supported, class.Server = "legacy", true
		case strings.Contains(id, "windows7server"), strings.Contains(id, "winlonghorn"), strings.Contains(id, "windows7"), strings.Contains(id, "windows8"):
			class.Supported = "obsolete"
			class.Server = strings.Contains(id, "server") || strings.Contains(id, "longhorn")
		case strings.Contains(id, "winnet"), strings.Contains(id, "winxp"), strings.Contains(id, "win2000"), strings.Contains(id, "win98"), strings.Contains(id, "win95"), strings.Contains(id, "winnt"), strings.Contains(id, "winvista"):
			class.Supported = "unsupported"
		}
	case strings.Contains(lowerText, "solaris"), strings.Contains(lowerText, "netware"), strings.Contains(lowerText, "darwin"), strings.Contains(lowerText, "mac os"), strings.Contains(lowerText, "os2"), strings.Contains(lowerText, "dos"), strings.Contains(lowerText, "unixware"), strings.Contains(lowerText, "openserver"), strings.Contains(lowerText, "esxi"), strings.Contains(lowerText, "vmkernel"):
		class.Family, class.Supported = "other", "unsupported"
	case vmLinuxPattern.MatchString(text):
		class.Family = "linux"
		class.Supported = "current"
		if match := vmLinuxVersion.FindStringSubmatch(name); match != nil {
			major := 0
			fmt.Sscanf(match[2], "%d", &major)
			distro := strings.ToLower(match[1])
			switch {
			case strings.Contains(distro, "ubuntu") && major < 16, strings.Contains(distro, "debian") && major < 9, (strings.Contains(distro, "suse") || distro == "sles") && major < 12, !strings.Contains(distro, "ubuntu") && !strings.Contains(distro, "debian") && !strings.Contains(distro, "suse") && distro != "sles" && major < 7:
				class.Supported = "obsolete"
			}
		} else if regexp.MustCompile(`(?i)rhel[56]|centos[56]?_|other(24|26)xlinux|2\.4|2\.6`).MatchString(text) {
			class.Supported = "obsolete"
		} else if strings.Contains(lowerText, "other") {
			class.Supported = "unknown"
		}
	case strings.Contains(lowerText, "freebsd"):
		class.Family, class.Supported = "bsd", "legacy"
	}
	return class
}

func vmFormatBytes(value int64) string {
	if value <= 0 {
		return "0 GB"
	}
	gb := float64(value) / (1 << 30)
	if gb >= 1024 {
		return fmt.Sprintf("%.1f TB", gb/1024)
	}
	if gb >= 10 {
		return fmt.Sprintf("%.0f GB", gb)
	}
	return fmt.Sprintf("%.1f GB", gb)
}

func vmStaticNICs(vm vmMachine) []vmNIC {
	nics := []vmNIC{}
	for _, nic := range vm.NICs {
		if nic.Static {
			nics = append(nics, nic)
		}
	}
	return nics
}

// vmDisksEncrypted is true when a VMware VM's disks are, or may be,
// encrypted with vSphere VM Encryption, so they cannot be copied until the
// VM is decrypted. A VM whose own files only are encrypted (for a virtual
// TPM) has plain disks that copy as they are. Older saved inventories do not
// say which disks are encrypted, so they count as encrypted. Matches
// vmDisksEncrypted in index.html.
func vmDisksEncrypted(vm vmMachine) bool {
	if !vm.Encrypted || vm.Source == "hyperv" {
		return false
	}
	if !vm.EncryptionRead {
		return true
	}
	for _, disk := range vm.Disks {
		if disk.Encrypted {
			return true
		}
	}
	return false
}

// assessVM checks one VM against one target. Each finding names what was
// found and what to do about it; the status is the worst severity.
// vmAlreadyMovedPattern matches names operators give a source VM they have
// already moved or retired ("APP01 - MIGRATED! Do not power on").
var vmAlreadyMovedPattern = regexp.MustCompile(`(^|[^a-z])(migrated|decommissioned|decom|retired)([^a-z]|$)|do[ _-]*not[ _-]*(power|turn|start)[ _-]*(it[ _-]*)?on`)

// vmAlreadyMoved is true when the VM's name says it was already migrated or
// retired, so a bulk action never moves it a second time by accident.
func vmAlreadyMoved(vm vmMachine) bool {
	return vmAlreadyMovedPattern.MatchString(strings.ToLower(vm.Name))
}

func assessVM(vm vmMachine, target string, now time.Time) vmReadiness {
	result := vmReadiness{Target: target, Status: vmStatusReady, Findings: []vmFinding{}}
	add := func(severity, code, message, action string) {
		result.Findings = append(result.Findings, vmFinding{Severity: severity, Code: code, Message: message, Action: action})
	}
	profile, known := vmTargetProfileFor(target)
	if !known {
		add(vmSeverityBlocker, "target-unknown", "Unknown migration target "+target+".", "Choose a target from the list.")
		result.Status = vmStatusBlocked
		return result
	}
	hyperV := vm.Source == vmSourceHyperV
	if role := vmInfrastructureRole(vm); role != "" && !hyperV {
		add(vmSeverityInfo, "vmware-infrastructure", "This is "+role+", part of the VMware environment itself.", "It is not migrated: it is retired with VMware. Leave it out of the plan and set up the new platform's own management instead.")
		result.Status = vmStatusSkip
		return result
	}
	if vmAlreadyMoved(vm) {
		add(vmSeverityWarning, "already-moved", "The VM's name says it was already migrated or retired.", "Check it is not still needed. Moving it again would make a second copy beside the one already running. Exclude it from the plan, or acknowledge this if it really must move.")
	}
	os := classifyGuestOS(vm)
	efi := strings.EqualFold(vm.Firmware, "efi")
	if target == vmTargetHyperV || target == vmTargetAzure {
		result.Generation = 1
		if efi {
			result.Generation = 2
		}
	}

	if vm.Template {
		add(vmSeverityInfo, "template", "This is a template, not a running VM.", "Rebuild the template on the target, or convert it to a VM first if it must move as is.")
	}
	if vm.Encrypted && hyperV {
		add(vmSeverityBlocker, "shielded", "The VM is a shielded VM: its state and disks are protected by the Host Guardian Service.", "Unshield it (turn off the shielding policy and BitLocker in the guest) before copying its disks. Other platforms cannot read them.")
	}
	encryptedDisks := 0
	for _, disk := range vm.Disks {
		if disk.Encrypted {
			encryptedDisks++
		}
	}
	homeOnly := vm.Encrypted && !hyperV && vm.EncryptionRead && encryptedDisks == 0
	if vm.Encrypted && !hyperV {
		switch {
		case homeOnly && vm.VTPM:
			// Said once, with the virtual TPM finding below.
		case homeOnly:
			// A virtual TPM needs the VM's home files encrypted, but its disks
			// can stay plain, and plain disks copy as they are.
			add(vmSeverityWarning, "encrypted-home", "Only the VM's own files are encrypted with vSphere VM Encryption (a virtual TPM needs that); its disks are not encrypted, so they copy as they are.", "Nothing to decrypt. The virtual TPM itself does not move: save the BitLocker recovery keys and suspend BitLocker before the cutover (Prepare and fix up, from this computer or inside the VM). "+profile.VTPM)
		case vm.EncryptionRead:
			add(vmSeverityBlocker, "encrypted", fmt.Sprintf("%d of the VM's %d disks are encrypted with vSphere VM Encryption, so no other platform can read them.", encryptedDisks, len(vm.Disks)), "Ways out: (1) Replicate servers: an agent inside Windows reads the disks already decrypted, with nothing to change in vSphere; or (2) decrypt it with Move VMs, Prepare on VMware: save the BitLocker keys and suspend BitLocker, shut it down, remove the virtual TPM and decrypt (the VM storage policy must then be one without encryption). "+profile.VTPM)
		default:
			add(vmSeverityBlocker, "encrypted", "The VM is encrypted with vSphere VM Encryption.", "Read vCenter again: this inventory does not say whether its disks or only its own files (for a virtual TPM) are encrypted. Plain disks copy as they are; encrypted disks need Replicate servers (an agent inside Windows) or decrypting with Move VMs, Prepare on VMware.")
		}
	}
	if hyperV && target == vmTargetHyperV {
		add(vmSeverityInfo, "already-hyperv", "The VM already runs on Hyper-V.", "Move it with Hyper-V live migration, storage migration, or export and import, which keep its configuration. Copy its disks with Convert disk images only to rebuild it on a different host by hand.")
	}

	// Guest operating system support.
	switch os.Supported {
	case "unsupported":
		add(vmSeverityBlocker, "os-unsupported", fmt.Sprintf("%s is not supported on %s.", vmNonEmpty(os.Name, "This guest operating system"), profile.Label), "Upgrade or rebuild the workload on a supported operating system, or keep it on VMware.")
	case "obsolete":
		action := "Plan an operating system upgrade. If it must move as is, test the conversion first."
		if os.Family == "windows" {
			switch profile.Family {
			case "hyperv":
				action = "It can run as a Generation 1 VM but needs Hyper-V Integration Services installed before cutover and is outside Microsoft support. Test the conversion first."
				if target == vmTargetAzure {
					action = "Azure runs only Windows versions it supports; check the Azure supported Windows list and upgrade first if this one is not on it."
				}
			case "kvm":
				action = "Confirm the VirtIO drivers you have support this version, install them before cutover and test the conversion first."
			case "aws":
				action = "Check that AWS Application Migration Service supports this Windows version; older versions need the AWS PV drivers instead of ENA and NVMe. Test the conversion first."
			case "physical":
				action = "Check that the server vendor still publishes storage and network drivers for this Windows version. Without them it cannot boot on the new hardware."
			}
		}
		add(vmSeverityWarning, "os-obsolete", fmt.Sprintf("%s is out of vendor support.", vmNonEmpty(os.Name, "This guest operating system")), action)
	case "legacy":
		if os.Family == "bsd" {
			add(vmSeverityWarning, "os-bsd", "FreeBSD guests have limited support.", "Check the target's supported guest list and test the conversion first.")
		} else {
			add(vmSeverityWarning, "os-legacy", fmt.Sprintf("%s is past mainstream support.", vmNonEmpty(os.Name, "This guest operating system")), "It runs on the target, but plan an upgrade.")
		}
	case "unknown":
		if os.Family == "unknown" {
			add(vmSeverityWarning, "os-unknown", "The guest operating system could not be identified.", "Check the guest operating system and confirm the target supports it.")
		}
	}
	if os.Family == "windows" && !os.Bits64 && os.Supported != "unsupported" && os.Name != "" && (profile.Kind == vmKindCloud) {
		add(vmSeverityWarning, "os-32bit", "The guest looks like a 32-bit Windows.", "Cloud images and migration services expect 64-bit Windows. Confirm the edition, or rebuild it as 64-bit.")
	}

	// Drivers the guest needs before cutover.
	if os.Family == "linux" {
		add(vmSeverityWarning, "linux-initramfs", "Linux boots from an initramfs that may not include the target's storage drivers.", "Before cutover, rebuild the initramfs with "+profile.LinuxDrivers+" (for example dracut -f --add-drivers or update-initramfs). Network interface names usually change, so check static IP configuration after cutover.")
	}
	if os.Family == "windows" && (vmDomainControllerName.MatchString(vm.Name) || vmDomainControllerName.MatchString(strings.SplitN(vm.GuestHostName, ".", 2)[0])) {
		add(vmSeverityWarning, "domain-controller", "The name suggests a domain controller.", "Move a domain controller cold: shut it down, copy it, start only the new copy and never run the old one again (or restore it from an older copy), which would cause USN rollback. Check replication with repadmin /replsummary afterwards. Building a new domain controller and demoting the old one is often simpler.")
	}
	if os.Family == "windows" && profile.WindowsDrivers != "" {
		switch profile.Family {
		case "kvm":
			add(vmSeverityWarning, "virtio-drivers", fmt.Sprintf("Windows needs VirtIO drivers to boot from %s's disk controller and use its network adapter.", profile.Short), "Install "+profile.WindowsDrivers+" in the VM before cutover. The guest pre-flight on this VM shows whether they are present.")
		case "aws":
			add(vmSeverityWarning, "aws-drivers", "Windows needs the AWS ENA and NVMe drivers to boot and reach the network on Nitro instances.", "Use "+profile.WindowsDrivers+". The guest pre-flight on this VM shows whether they are present.")
		case "physical":
			add(vmSeverityWarning, "hardware-drivers", "Windows needs the new server's storage controller driver to boot, and its network driver to reach the network.", "Install "+profile.WindowsDrivers+" before cutover. The guest pre-flight on this VM lists the storage drivers present now.")
		}
	}

	// Firmware, Secure Boot and TPM.
	switch target {
	case vmTargetHyperV:
		if efi && !os.Bits64 && os.Family != "unknown" {
			add(vmSeverityWarning, "efi-32bit", "The VM uses EFI firmware but its guest is 32-bit.", "Generation 2 VMs need a 64-bit guest. Use Generation 1, which may need the boot configuration converted to BIOS.")
		}
		if efi {
			add(vmSeverityInfo, "generation", "EFI firmware: create it as a Generation 2 VM.", "")
		} else {
			add(vmSeverityInfo, "generation", "BIOS firmware: create it as a Generation 1 VM.", "Converting to Generation 2 later needs the boot disk converted to GPT (for example MBR2GPT on Windows).")
			for _, disk := range vm.Disks {
				if disk.BootCandidate && disk.CapacityBytes > 2040*(1<<30) {
					add(vmSeverityWarning, "gen1-boot-disk", fmt.Sprintf("The boot disk %s is %s. A Generation 1 VM boots from IDE, which is limited to 2,040 GB.", disk.Label, vmFormatBytes(disk.CapacityBytes)), "Shrink the boot disk or convert the VM to EFI/GPT before migrating so it can be Generation 2.")
					break
				}
			}
			if vm.CPUs > 64 {
				add(vmSeverityWarning, "gen1-cpu", fmt.Sprintf("%d vCPUs is more than a Generation 1 VM supports (64).", vm.CPUs), "Convert to Generation 2 or reduce the vCPU count.")
			}
			if vm.MemoryMB > 1024*1024 {
				add(vmSeverityWarning, "gen1-memory", "More than 1 TB of memory is more than a Generation 1 VM supports.", "Convert to Generation 2 or reduce memory.")
			}
		}
		if vm.SecureBoot && os.Family == "linux" {
			add(vmSeverityWarning, "secureboot-linux", "Secure Boot is on for a Linux guest.", profile.SecureBoot)
		}
		if len(vm.NICs) > 8 {
			add(vmSeverityWarning, "nic-count", fmt.Sprintf("%d network adapters. Hyper-V supports 8 network adapters per VM.", len(vm.NICs)), "Consolidate adapters or use VLAN trunking in the guest.")
		}
	case vmTargetAzure:
		if efi {
			add(vmSeverityInfo, "generation", "EFI firmware: it moves as a Generation 2 Azure VM.", "Pick a VM size that supports Generation 2.")
		} else {
			add(vmSeverityInfo, "generation", "BIOS firmware: it moves as a Generation 1 Azure VM.", "Trusted Launch (Secure Boot and vTPM) needs Generation 2; convert the boot disk to GPT first if you need it.")
		}
		for _, disk := range vm.Disks {
			if disk.BootCandidate && disk.CapacityBytes > 4096*(1<<30) {
				add(vmSeverityBlocker, "azure-os-disk", fmt.Sprintf("The boot disk %s is %s. Azure OS disks are limited to 4 TiB.", disk.Label, vmFormatBytes(disk.CapacityBytes)), "Move data off the boot disk to a data disk and shrink it before migrating.")
				break
			}
			if !disk.BootCandidate && disk.CapacityBytes > 32*1024*(1<<30) {
				add(vmSeverityBlocker, "azure-data-disk", fmt.Sprintf("Disk %s is %s. Azure managed data disks are limited to 32 TiB.", disk.Label, vmFormatBytes(disk.CapacityBytes)), "Split the data across smaller disks before migrating.")
			}
		}
		if vm.SecureBoot {
			add(vmSeverityInfo, "secureboot", "Secure Boot is on.", profile.SecureBoot)
		}
	default:
		if efi {
			switch profile.Kind {
			case vmKindPhysical:
				add(vmSeverityInfo, "firmware", "EFI firmware: set the server to UEFI boot.", "")
			default:
				add(vmSeverityInfo, "firmware", "EFI firmware: create the VM with UEFI boot on "+profile.Short+".", "")
			}
		} else if profile.Kind == vmKindPhysical {
			add(vmSeverityWarning, "firmware-bios", "BIOS firmware with an MBR boot disk. Many current servers boot only with UEFI.", "Turn on legacy (CSM) boot on the server if it has one, or convert the boot disk to GPT (MBR2GPT on Windows) before the move.")
		}
		if vm.SecureBoot {
			add(vmSeverityWarning, "secureboot", "Secure Boot is on.", profile.SecureBoot)
		}
	}
	if vm.VTPM {
		message := "The VM has a virtual TPM. BitLocker keys, Windows Hello and credentials sealed to it do not move with the disks."
		if homeOnly {
			message += " Its own files are encrypted for the virtual TPM (vSphere VM Encryption), but its disks are not, so they copy as they are with nothing to decrypt."
		}
		action := "Before the cutover, save the BitLocker recovery keys and suspend BitLocker (Prepare and fix up: from this computer over the network, or inside the VM), or have the recovery keys from Active Directory or Intune ready. " + profile.VTPM
		if target == vmTargetHyperV && !hyperV && efi {
			// The cutover gives the generation 2 VM a new virtual TPM, so this
			// is something to know rather than something to fix.
			add(vmSeverityInfo, "vtpm", message+" The cutover gives the Hyper-V VM a new virtual TPM.", "If BitLocker is on inside the VM, Windows asks for the recovery key at the first start: save the keys first (Prepare and fix up) or have them from Active Directory or Intune.")
		} else {
			add(vmSeverityWarning, "vtpm", message, action)
		}
	}

	// Disks.
	sharedDisk, physicalRDM, virtualRDM, independent := []string{}, []string{}, []string{}, []string{}
	for _, disk := range vm.Disks {
		switch {
		case disk.RDM == "physical":
			physicalRDM = append(physicalRDM, disk.Label)
		case disk.RDM == "virtual":
			virtualRDM = append(virtualRDM, disk.Label)
		}
		if strings.EqualFold(disk.Sharing, "sharingMultiWriter") || disk.BusSharing == "physicalSharing" || disk.BusSharing == "virtualSharing" {
			sharedDisk = append(sharedDisk, disk.Label)
		}
		if strings.HasPrefix(disk.Mode, "independent") {
			independent = append(independent, disk.Label)
		}
	}
	if len(sharedDisk) > 0 {
		add(vmSeverityBlocker, "shared-disk", "Shared or multi-writer disks (guest cluster): "+strings.Join(sharedDisk, ", ")+".", profile.SharedDisks)
	}
	if len(physicalRDM) > 0 {
		action := "Copy the data to a virtual disk first, or present the LUN to the guest over iSCSI on the target."
		severity := vmSeverityBlocker
		if profile.Kind == vmKindPhysical {
			action = "Present the same LUN to the physical server over its HBA or iSCSI after cutover, and keep it out of the disk image."
			severity = vmSeverityWarning
		} else if profile.Kind == vmKindCloud {
			action = "Copy the data to a disk in the cloud first; SAN LUNs cannot be presented to cloud VMs."
		}
		if hyperV {
			add(severity, "passthrough-disk", "Pass-through disks: "+strings.Join(physicalRDM, ", ")+". Their data is on a physical disk of the host, not in a virtual disk.", action)
		} else {
			add(severity, "rdm-physical", "Physical-mode raw device mappings: "+strings.Join(physicalRDM, ", ")+". Their data is on a SAN LUN, not in a virtual disk.", action)
		}
	}
	if len(virtualRDM) > 0 {
		add(vmSeverityWarning, "rdm-virtual", "Virtual-mode raw device mappings: "+strings.Join(virtualRDM, ", ")+".", "Convert them to virtual disks (a Storage vMotion that changes the disk format does this) before migrating.")
	}
	if len(independent) > 0 {
		add(vmSeverityWarning, "independent-disk", "Independent disks are excluded from snapshots: "+strings.Join(independent, ", ")+".", "Snapshot-based migration and backup tools skip them. Make them dependent, or copy their data separately.")
	}

	if count := len(vm.Snapshots); count > 0 {
		oldest := vm.Snapshots[0].Created
		for _, snapshot := range vm.Snapshots {
			if !snapshot.Created.IsZero() && (oldest.IsZero() || snapshot.Created.Before(oldest)) {
				oldest = snapshot.Created
			}
		}
		age := ""
		if !oldest.IsZero() {
			days := int(now.Sub(oldest).Hours() / 24)
			if days >= 1 {
				age = fmt.Sprintf(" The oldest is %d day(s) old.", days)
			}
		}
		backup := []string{}
		for _, snapshot := range vm.Snapshots {
			if vmBackupSnapshotPattern.MatchString(snapshot.Name) {
				backup = append(backup, snapshot.Name)
			}
		}
		action := "Delete or consolidate the snapshots before migrating. Conversion tools read the current state, and long snapshot chains slow the copy."
		if len(backup) > 0 {
			age += " Made by backup software: " + strings.Join(backup, ", ") + "."
			action = "Backup software normally deletes its own snapshot when the job ends; a leftover one means a job failed or is still running. Check the backup job, then delete the snapshot in vSphere before migrating."
		}
		if hyperV {
			action = "Delete the checkpoints in Hyper-V Manager before migrating, which merges the .avhdx files into the disks. To copy without deleting them, convert the newest .avhdx with Convert disk images: it reads the whole chain."
			if len(backup) > 0 {
				action = "Backup software normally removes its own checkpoint when the job ends; a leftover one means a job failed or is still running. Check the backup job, then delete the checkpoint before migrating."
			}
			add(vmSeverityWarning, "snapshots", fmt.Sprintf("%d checkpoint(s).%s", count, age), action)
		} else {
			add(vmSeverityWarning, "snapshots", fmt.Sprintf("%d snapshot(s).%s", count, age), action)
		}
	}
	if vm.ConsolidationNeeded {
		add(vmSeverityWarning, "consolidation", "vSphere reports that the VM's disks need consolidation.", "Run Snapshots > Consolidate in vSphere before migrating.")
	}
	if vm.FaultTolerance {
		add(vmSeverityWarning, "fault-tolerance", "vSphere Fault Tolerance is configured.", "Turn off Fault Tolerance before migrating, and plan availability on the target ("+profile.Availability+").")
	}
	if len(vm.Passthrough) > 0 {
		severity := vmSeverityBlocker
		if profile.Kind == vmKindPhysical {
			severity = vmSeverityWarning
		}
		add(severity, "passthrough", "PCI passthrough or vGPU devices: "+strings.Join(vm.Passthrough, ", ")+".", "These devices do not move with the VM. "+profile.Passthrough)
	}
	if vm.USBDevices > 0 {
		severity := vmSeverityWarning
		if profile.Kind == vmKindPhysical {
			severity = vmSeverityInfo
		}
		add(severity, "usb", fmt.Sprintf("%d USB device(s) connected to the VM.", vm.USBDevices), profile.USB)
	}
	if vm.SerialPorts > 0 || vm.ParallelPorts > 0 {
		action := "Physical port mappings do not move. Use a network serial device server or named pipes on the target."
		if profile.Kind == vmKindPhysical {
			action = "Connect the devices to the server's own ports, or a network serial device server."
		} else if profile.Kind == vmKindCloud {
			action = "Cloud VMs have no serial or parallel devices. Use a network serial device server reachable from the cloud, or redesign."
		}
		add(vmSeverityWarning, "serial", fmt.Sprintf("%d serial and %d parallel port(s).", vm.SerialPorts, vm.ParallelPorts), action)
	}
	if vm.Source != "hyperv" {
		if idle := vmIdleMediaImages(vm); len(idle) > 0 {
			add(vmSeverityWarning, "media-image", "A CD/DVD or floppy drive names an image file that is not connected: "+strings.Join(idle, ", ")+". The file may no longer exist, and vSphere's export of a running VM fails when it is missing.", "Tick Eject CD/DVD and floppy images in New migration's Review, the migration plan's settings or Move VMs: the copy then sets the drive to a client device, not connected (the VM's settings are recorded first).")
		}
	}
	if len(vm.ConnectedMedia) > 0 {
		if hyperV {
			add(vmSeverityWarning, "media", "Connected DVD media: "+strings.Join(vm.ConnectedMedia, ", ")+".", "Eject the media before migrating. ISO files on the Hyper-V host do not move with the VM.")
		} else {
			add(vmSeverityWarning, "media", "Connected CD/DVD media: "+strings.Join(vm.ConnectedMedia, ", ")+".", "Disconnect the media before migrating, or tick Eject CD/DVD and floppy images in New migration's Review, the migration plan's settings or Move VMs. ISO files on VMware datastores do not move with the VM.")
		}
	}

	// VMware Tools and guest networking.
	powered := strings.EqualFold(vm.PowerState, "poweredOn")
	switch {
	case hyperV:
		switch {
		case !powered:
		case vm.IntegrationServices == "ok":
		case vm.IntegrationServices == "disabled":
			add(vmSeverityWarning, "integration-off", "The Heartbeat integration service is turned off, so Hyper-V cannot report the guest's details.", "Turn the integration services on, or record the guest's IP configuration inside the VM (the guest pre-flight does this) before cutover.")
		default:
			add(vmSeverityWarning, "integration-missing", "Hyper-V Integration Services are not answering, so the guest's IP settings may be missing.", "Install or update the integration services in the guest, or record its IP configuration inside the VM (the guest pre-flight does this) before cutover.")
		}
		if vm.DynamicMemory && profile.Family != "hyperv" {
			add(vmSeverityInfo, "dynamic-memory", fmt.Sprintf("Dynamic Memory is on; the VM starts with %s.", vmFormatBytes(vm.MemoryMB<<20)), "Most targets give a VM fixed memory. Size it from what the guest actually uses, not its startup memory.")
		}
	case vm.ToolsStatus == "guestToolsNotInstalled":
		add(vmSeverityWarning, "tools-missing", "VMware Tools is not installed, so vSphere cannot report the guest's IP settings.", "Record the guest's IP configuration manually, or run the guest pre-flight inside the VM before cutover.")
	case powered && !vm.ToolsRunning:
		add(vmSeverityWarning, "tools-stopped", "VMware Tools is not running, so guest details may be missing or old.", "Start VMware Tools, or record the guest's IP configuration inside the VM before cutover.")
	case vm.ToolsStatus != "" && vm.ToolsStatus != "guestToolsUnmanaged" && os.Family == "windows":
		add(vmSeverityInfo, "tools-remove", "VMware Tools is installed.", "Remove VMware Tools after cutover (the fix-up on the migrated computer can do this). Its drivers do nothing on the target.")
	}
	if statics := vmStaticNICs(vm); len(statics) > 0 {
		addresses := []string{}
		for _, nic := range statics {
			addresses = append(addresses, strings.Join(nic.IPAddresses, ", "))
		}
		if profile.Kind == vmKindCloud {
			add(vmSeverityWarning, "static-ip", "Static IP configuration on "+vmPlural(len(statics), "adapter", "adapters")+" ("+strings.Join(addresses, "; ")+"). "+profile.Short+" assigns addresses through DHCP, and a static address inside the guest cuts it off the network.", "Reserve the address as a static private IP on the new "+profile.NetworkLabel+" (if it fits the subnet), and leave the guest on DHCP. The migration service usually switches the guest to DHCP; check after cutover. Plan DNS changes for addresses that change.")
		} else if target == vmTargetHyperV && !hyperV && os.Family == "windows" {
			// Move VMs records these from vCenter on every copy and the
			// automatic fix-up puts them back on the new adapters
			// (guest_autofix.go), with the old VM's adapters disconnected.
			add(vmSeverityInfo, "static-ip", "Static IP configuration on "+vmPlural(len(statics), "adapter", "adapters")+" ("+strings.Join(addresses, "; ")+"). Recorded from vCenter on every copy and put back on the new adapters automatically after the cutover.", "Enter the guest administrator account once in Move VMs or the migration plan. The old VM's adapters are set to not connect first, so the address is never used twice.")
		} else {
			add(vmSeverityWarning, "static-ip", "Static IP configuration on "+vmPlural(len(statics), "adapter", "adapters")+" ("+strings.Join(addresses, "; ")+"). The new "+map[bool]string{true: "server", false: "VM"}[profile.Kind == vmKindPhysical]+" gets new network adapters, which start without these settings.", "Run the guest pre-flight inside the VM before cutover, then the fix-up after cutover to put the settings back. Disconnect the old VM's adapters so the address is not used twice.")
		}
	}
	for _, nic := range vm.NICs {
		if nic.ManualMAC {
			action := "Set the same static MAC on the target if licensing or DHCP reservations depend on it."
			if profile.Kind == vmKindCloud {
				action = "Cloud providers assign MAC addresses. Move licensing or reservations that depend on this MAC to another identifier."
			} else if profile.Kind == vmKindPhysical {
				action = "The server's adapters have their own MAC addresses. Update licensing or DHCP reservations that depend on this one."
			}
			add(vmSeverityInfo, "manual-mac", "A manually set MAC address ("+nic.MAC+") on "+nic.Label+".", action)
		}
	}
	if version := vmHardwareVersionNumber(vm.HardwareVersion); version > 0 && version < 7 {
		add(vmSeverityWarning, "hardware-old", "Virtual hardware version "+vm.HardwareVersion+" is very old.", "Upgrade VMware Tools and the virtual hardware, or rebuild, before converting.")
	}

	// Sizing that depends on the destination.
	switch profile.Kind {
	case vmKindCloud:
		add(vmSeverityInfo, "instance-size", fmt.Sprintf("Needs an instance size with at least %d vCPU and %s memory.", vm.CPUs, vmFormatBytes(vm.MemoryMB<<20)), "Right-size from the VM's actual use rather than its allocation; the migration service's assessment can recommend a size.")
	case vmKindPhysical:
		add(vmSeverityInfo, "server-size", fmt.Sprintf("Needs a server with at least %d cores, %s memory and %s of disk.", vm.CPUs, vmFormatBytes(vm.MemoryMB<<20), vmFormatBytes(vm.ProvisionedBytes)), "Windows treats the new hardware as a different computer: plan reactivation and check per-core licensing.")
	}
	if !powered && !vm.Template {
		add(vmSeverityInfo, "powered-off", "The VM is powered off: a good first test migration.", "")
	}
	result.Findings = append(result.Findings, preflightFindings(vm.Preflight)...)

	sort.SliceStable(result.Findings, func(i, j int) bool {
		return vmSeverityRank(result.Findings[i].Severity) > vmSeverityRank(result.Findings[j].Severity)
	})
	for _, finding := range result.Findings {
		if finding.Severity == vmSeverityBlocker {
			result.Status = vmStatusBlocked
			break
		}
		if finding.Severity == vmSeverityWarning {
			result.Status = vmStatusReview
		}
	}
	return result
}

func vmSeverityRank(severity string) int {
	switch severity {
	case vmSeverityBlocker:
		return 3
	case vmSeverityWarning:
		return 2
	case vmSeverityInfo:
		return 1
	}
	return 0
}

func vmHardwareVersionNumber(version string) int {
	number := 0
	fmt.Sscanf(strings.TrimPrefix(strings.ToLower(version), "vmx-"), "%d", &number)
	return number
}

func vmNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func vmPlural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}

// assessInventory fills every VM's readiness for every target.
func assessInventory(inventory *vmInventory, now time.Time) {
	for index := range inventory.VMs {
		inventory.VMs[index].Readiness = nil
		for _, target := range vmTargets {
			inventory.VMs[index].Readiness = append(inventory.VMs[index].Readiness, assessVM(inventory.VMs[index], target, now))
		}
	}
}

type vmReadinessSummary struct {
	Target  string `json:"target"`
	Ready   int    `json:"ready"`
	Review  int    `json:"review"`
	Blocked int    `json:"blocked"`
	Skip    int    `json:"skip"`
}

type vmInventorySummary struct {
	Server           string               `json:"server"`
	ServerType       string               `json:"serverType"`
	Product          string               `json:"product"`
	CollectedAt      string               `json:"collectedAt"`
	Hosts            int                  `json:"hosts"`
	Clusters         int                  `json:"clusters"`
	Datastores       int                  `json:"datastores"`
	Networks         int                  `json:"networks"`
	VMs              int                  `json:"vms"`
	Templates        int                  `json:"templates"`
	PoweredOn        int                  `json:"poweredOn"`
	VCPUs            int                  `json:"vcpus"`
	MemoryBytes      int64                `json:"memoryBytes"`
	ProvisionedBytes int64                `json:"provisionedBytes"`
	UsedBytes        int64                `json:"usedBytes"`
	Snapshots        int                  `json:"snapshots"`
	StaticIPVMs      int                  `json:"staticIpVms"`
	Readiness        []vmReadinessSummary `json:"readiness"`
}

func summarizeInventory(inventory *vmInventory) vmInventorySummary {
	summary := vmInventorySummary{Server: inventory.Server, ServerType: inventory.ServerType, Product: inventory.Product, Hosts: len(inventory.Hosts), Clusters: len(inventory.Clusters), Datastores: len(inventory.Datastores), Networks: len(inventory.Networks)}
	if !inventory.CollectedAt.IsZero() {
		summary.CollectedAt = inventory.CollectedAt.Format("2006-01-02 15:04")
	}
	counts := map[string]*vmReadinessSummary{}
	for _, target := range vmTargets {
		counts[target] = &vmReadinessSummary{Target: target}
	}
	for _, vm := range inventory.VMs {
		if vm.Template {
			summary.Templates++
			continue
		}
		summary.VMs++
		if strings.EqualFold(vm.PowerState, "poweredOn") {
			summary.PoweredOn++
		}
		summary.VCPUs += vm.CPUs
		summary.MemoryBytes += vm.MemoryMB << 20
		summary.ProvisionedBytes += vm.ProvisionedBytes
		summary.UsedBytes += vm.UsedBytes
		summary.Snapshots += len(vm.Snapshots)
		if len(vmStaticNICs(vm)) > 0 {
			summary.StaticIPVMs++
		}
		for _, readiness := range vm.Readiness {
			count := counts[readiness.Target]
			if count == nil {
				continue
			}
			switch readiness.Status {
			case vmStatusReady:
				count.Ready++
			case vmStatusReview:
				count.Review++
			case vmStatusBlocked:
				count.Blocked++
			case vmStatusSkip:
				count.Skip++
			}
		}
	}
	for _, target := range vmTargets {
		summary.Readiness = append(summary.Readiness, *counts[target])
	}
	return summary
}

// vmIdleMediaImages lists the image files a VM's drives name but do not
// have connected.
func vmIdleMediaImages(vm vmMachine) []string {
	connected := map[string]bool{}
	for _, name := range vm.ConnectedMedia {
		connected[strings.ToLower(name)] = true
	}
	idle := []string{}
	for _, file := range vm.MediaImages {
		if !connected[strings.ToLower(vmMediaFileName(file))] {
			idle = append(idle, file)
		}
	}
	return idle
}
