package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// VMware migration planning: the inventory read from vCenter or a standalone
// ESXi host (vmware_collect.go), the per-VM readiness checks for Hyper-V and
// Scale Computing HyperCore, and the migration plan (target mapping, sizing
// and waves). Nothing here changes the VMware environment. This file has no
// Windows dependency so the rules are unit tested on any platform.

const (
	vmTargetHyperV = "hyperv"
	vmTargetScale  = "scale"

	vmSeverityBlocker = "blocker"
	vmSeverityWarning = "warning"
	vmSeverityInfo    = "info"

	vmStatusReady   = "ready"
	vmStatusReview  = "review"
	vmStatusBlocked = "blocked"
)

var vmTargets = []string{vmTargetHyperV, vmTargetScale}

func vmTargetLabel(target string) string {
	switch target {
	case vmTargetHyperV:
		return "Hyper-V"
	case vmTargetScale:
		return "Scale Computing HyperCore"
	}
	return target
}

type vmInventory struct {
	Schema       int           `json:"schema"`
	Server       string        `json:"server"`
	ServerType   string        `json:"serverType"`
	Product      string        `json:"product"`
	APIVersion   string        `json:"apiVersion"`
	CollectedAt  time.Time     `json:"collectedAt"`
	CollectedBy  string        `json:"collectedBy"`
	Datacenters  []string      `json:"datacenters"`
	Clusters     []vmCluster   `json:"clusters"`
	Hosts        []vmHost      `json:"hosts"`
	Datastores   []vmDatastore `json:"datastores"`
	Networks     []vmNetwork   `json:"networks"`
	VMs          []vmMachine   `json:"vms"`
	Warnings     []string      `json:"warnings"`
	AppVersion   string        `json:"appVersion"`
	SourceFolder string        `json:"-"`
}

type vmCluster struct {
	Name        string `json:"name"`
	Datacenter  string `json:"datacenter"`
	Hosts       int    `json:"hosts"`
	HA          bool   `json:"ha"`
	DRS         bool   `json:"drs"`
	CPUCores    int    `json:"cpuCores"`
	MemoryBytes int64  `json:"memoryBytes"`
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
	Label          string `json:"label"`
	Controller     string `json:"controller"`
	Bus            int    `json:"bus"`
	Unit           int    `json:"unit"`
	CapacityBytes  int64  `json:"capacityBytes"`
	Datastore      string `json:"datastore"`
	File           string `json:"file"`
	Provisioning   string `json:"provisioning"`
	Mode           string `json:"mode"`
	RDM            string `json:"rdm"`
	Sharing        string `json:"sharing"`
	BusSharing     string `json:"busSharing"`
	BootCandidate  bool   `json:"bootCandidate"`
	ControllerType string `json:"-"`
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
	Name                string        `json:"name"`
	ID                  string        `json:"id"`
	InstanceUUID        string        `json:"instanceUuid"`
	Datacenter          string        `json:"datacenter"`
	Cluster             string        `json:"cluster"`
	Host                string        `json:"host"`
	Folder              string        `json:"folder"`
	Template            bool          `json:"template"`
	PowerState          string        `json:"powerState"`
	GuestID             string        `json:"guestId"`
	GuestOS             string        `json:"guestOs"`
	GuestOSRunning      string        `json:"guestOsRunning"`
	GuestHostName       string        `json:"guestHostName"`
	IPAddress           string        `json:"ipAddress"`
	CPUs                int           `json:"cpus"`
	CoresPerSocket      int           `json:"coresPerSocket"`
	MemoryMB            int64         `json:"memoryMb"`
	HardwareVersion     string        `json:"hardwareVersion"`
	Firmware            string        `json:"firmware"`
	SecureBoot          bool          `json:"secureBoot"`
	VTPM                bool          `json:"vtpm"`
	Encrypted           bool          `json:"encrypted"`
	FaultTolerance      bool          `json:"faultTolerance"`
	ChangeTracking      bool          `json:"changeTracking"`
	ToolsStatus         string        `json:"toolsStatus"`
	ToolsRunning        bool          `json:"toolsRunning"`
	ToolsVersion        string        `json:"toolsVersion"`
	ProvisionedBytes    int64         `json:"provisionedBytes"`
	UsedBytes           int64         `json:"usedBytes"`
	Disks               []vmDisk      `json:"disks"`
	NICs                []vmNIC       `json:"nics"`
	Snapshots           []vmSnapshot  `json:"snapshots"`
	ConsolidationNeeded bool          `json:"consolidationNeeded"`
	ConnectedMedia      []string      `json:"connectedMedia"`
	Passthrough         []string      `json:"passthrough"`
	USBDevices          int           `json:"usbDevices"`
	SerialPorts         int           `json:"serialPorts"`
	ParallelPorts       int           `json:"parallelPorts"`
	GuestDisks          []vmGuestDisk `json:"guestDisks"`
	Readiness           []vmReadiness `json:"readiness"`
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

// assessVM checks one VM against one target. Each finding names what was
// found and what to do about it; the status is the worst severity.
func assessVM(vm vmMachine, target string, now time.Time) vmReadiness {
	result := vmReadiness{Target: target, Status: vmStatusReady, Findings: []vmFinding{}}
	add := func(severity, code, message, action string) {
		result.Findings = append(result.Findings, vmFinding{Severity: severity, Code: code, Message: message, Action: action})
	}
	os := classifyGuestOS(vm)
	efi := strings.EqualFold(vm.Firmware, "efi")
	if target == vmTargetHyperV {
		result.Generation = 1
		if efi {
			result.Generation = 2
		}
	}

	if vm.Template {
		add(vmSeverityInfo, "template", "This is a template, not a running VM.", "Rebuild the template on the target, or convert it to a VM first if it must move as is.")
	}
	if vm.Encrypted {
		add(vmSeverityBlocker, "encrypted", "The VM is encrypted with vSphere VM Encryption.", "Decrypt the VM in vSphere before exporting or converting it. Its disks cannot be read by other hypervisors.")
	}

	// Guest operating system support.
	switch os.Supported {
	case "unsupported":
		add(vmSeverityBlocker, "os-unsupported", fmt.Sprintf("%s is not supported on %s.", vmNonEmpty(os.Name, "This guest operating system"), vmTargetLabel(target)), "Upgrade or rebuild the workload on a supported operating system, or keep it on VMware.")
	case "obsolete":
		action := "Plan an operating system upgrade. If it must move as is, test the conversion first."
		if os.Family == "windows" && target == vmTargetHyperV {
			action = "It can run as a Generation 1 VM but needs Hyper-V Integration Services installed before cutover and is outside Microsoft support. Test the conversion first."
		} else if os.Family == "windows" && target == vmTargetScale {
			action = "Confirm the VirtIO drivers you have support this version, install them before cutover and test the conversion first."
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

	if os.Family == "linux" {
		drivers := "hv_storvsc, hv_netvsc and hv_vmbus"
		if target == vmTargetScale {
			drivers = "virtio_blk, virtio_scsi and virtio_net"
		}
		add(vmSeverityWarning, "linux-initramfs", "Linux boots from an initramfs that may not include the target's storage drivers.", "Before cutover, rebuild the initramfs with "+drivers+" (for example dracut -f --add-drivers or update-initramfs). Network interface names usually change, so check static IP configuration after cutover.")
	}

	if os.Family == "windows" && target == vmTargetScale {
		add(vmSeverityWarning, "virtio-drivers", "Windows needs VirtIO drivers to boot from HyperCore's disk controller and use its network adapter.", "Install the Scale Computing guest tools (VirtIO drivers) in the VM before cutover. The guest pre-flight on this VM shows whether they are present.")
	}

	if target == vmTargetHyperV {
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
			add(vmSeverityWarning, "secureboot-linux", "Secure Boot is on for a Linux guest.", "On Hyper-V choose the Microsoft UEFI Certificate Authority Secure Boot template, or turn Secure Boot off for this VM.")
		}
		if len(vm.NICs) > 8 {
			add(vmSeverityWarning, "nic-count", fmt.Sprintf("%d network adapters. Hyper-V supports 8 network adapters per VM.", len(vm.NICs)), "Consolidate adapters or use VLAN trunking in the guest.")
		}
	} else {
		if efi {
			add(vmSeverityInfo, "firmware", "EFI firmware: create the VM with UEFI boot on HyperCore.", "")
		}
		if vm.SecureBoot {
			add(vmSeverityWarning, "secureboot", "Secure Boot is on.", "Confirm the HyperCore version you are moving to supports Secure Boot for this guest, or turn Secure Boot off for this VM.")
		}
	}

	if vm.VTPM {
		add(vmSeverityWarning, "vtpm", "The VM has a virtual TPM. BitLocker keys, Windows Hello and credentials sealed to it do not move with the disks.", "Save BitLocker recovery keys and suspend BitLocker before cutover, then add a virtual TPM on the target (Hyper-V Generation 2 supports one; confirm your HyperCore version does).")
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
		action := "Guest clusters need shared storage on the target: use a Shared VHDX or VHD Set on Hyper-V, or redesign the cluster."
		if target == vmTargetScale {
			action = "HyperCore has no shared virtual disks. Redesign the guest cluster (for example in-guest iSCSI or application-level replication)."
		}
		add(vmSeverityBlocker, "shared-disk", "Shared or multi-writer disks (guest cluster): "+strings.Join(sharedDisk, ", ")+".", action)
	}
	if len(physicalRDM) > 0 {
		add(vmSeverityBlocker, "rdm-physical", "Physical-mode raw device mappings: "+strings.Join(physicalRDM, ", ")+". Their data is on a SAN LUN, not in a virtual disk.", "Copy the data to a virtual disk first, or present the LUN to the guest over iSCSI on the target.")
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
		add(vmSeverityWarning, "snapshots", fmt.Sprintf("%d snapshot(s).%s", count, age), "Delete or consolidate the snapshots before migrating. Conversion tools read the current state, and long snapshot chains slow the copy.")
	}
	if vm.ConsolidationNeeded {
		add(vmSeverityWarning, "consolidation", "vSphere reports that the VM's disks need consolidation.", "Run Snapshots > Consolidate in vSphere before migrating.")
	}
	if vm.FaultTolerance {
		add(vmSeverityWarning, "fault-tolerance", "vSphere Fault Tolerance is configured.", "Turn off Fault Tolerance before migrating, and plan the target's availability (Hyper-V failover cluster or HyperCore HA).")
	}
	if len(vm.Passthrough) > 0 {
		add(vmSeverityBlocker, "passthrough", "PCI passthrough or vGPU devices: "+strings.Join(vm.Passthrough, ", ")+".", "These devices do not move. Plan the equivalent on the target (for example Discrete Device Assignment or GPU partitioning on Hyper-V) or keep the workload on VMware.")
	}
	if vm.USBDevices > 0 {
		action := "Hyper-V does not pass USB devices to VMs. Use a USB-over-network device (common for license dongles) or redesign."
		if target == vmTargetScale {
			action = "Check whether your HyperCore version can pass this USB device through, or use a USB-over-network device (common for license dongles)."
		}
		add(vmSeverityWarning, "usb", fmt.Sprintf("%d USB device(s) connected to the VM.", vm.USBDevices), action)
	}
	if vm.SerialPorts > 0 || vm.ParallelPorts > 0 {
		add(vmSeverityWarning, "serial", fmt.Sprintf("%d serial and %d parallel port(s).", vm.SerialPorts, vm.ParallelPorts), "Physical port mappings do not move. Use a network serial device server or named pipes on the target.")
	}
	if len(vm.ConnectedMedia) > 0 {
		add(vmSeverityWarning, "media", "Connected CD/DVD media: "+strings.Join(vm.ConnectedMedia, ", ")+".", "Disconnect the media before migrating. ISO files on VMware datastores do not move with the VM.")
	}

	// VMware Tools and guest networking.
	powered := strings.EqualFold(vm.PowerState, "poweredOn")
	switch {
	case vm.ToolsStatus == "guestToolsNotInstalled":
		add(vmSeverityWarning, "tools-missing", "VMware Tools is not installed, so vSphere cannot report the guest's IP settings.", "Record the guest's IP configuration manually, or run the guest pre-flight inside the VM before cutover.")
	case powered && !vm.ToolsRunning:
		add(vmSeverityWarning, "tools-stopped", "VMware Tools is not running, so guest details may be missing or old.", "Start VMware Tools, or record the guest's IP configuration inside the VM before cutover.")
	case vm.ToolsStatus != "" && vm.ToolsStatus != "guestToolsUnmanaged" && os.Family == "windows":
		add(vmSeverityInfo, "tools-remove", "VMware Tools is installed.", "Remove VMware Tools after cutover (the fix-up on the migrated VM can do this). Its drivers do nothing on the target.")
	}
	if statics := vmStaticNICs(vm); len(statics) > 0 {
		addresses := []string{}
		for _, nic := range statics {
			addresses = append(addresses, strings.Join(nic.IPAddresses, ", "))
		}
		add(vmSeverityWarning, "static-ip", "Static IP configuration on "+vmPlural(len(statics), "adapter", "adapters")+" ("+strings.Join(addresses, "; ")+"). The target VM gets new network adapters, which start without these settings.", "Run the guest pre-flight inside the VM before cutover, then the fix-up after cutover to put the settings back. Disconnect the old VM's adapters so the address is not used twice.")
	}
	for _, nic := range vm.NICs {
		if nic.ManualMAC {
			add(vmSeverityInfo, "manual-mac", "A manually set MAC address ("+nic.MAC+") on "+nic.Label+".", "Set the same static MAC on the target if licensing or DHCP reservations depend on it.")
		}
	}
	if version := vmHardwareVersionNumber(vm.HardwareVersion); version > 0 && version < 7 {
		add(vmSeverityWarning, "hardware-old", "Virtual hardware version "+vm.HardwareVersion+" is very old.", "Upgrade VMware Tools and the virtual hardware, or rebuild, before converting.")
	}
	if !powered && !vm.Template {
		add(vmSeverityInfo, "powered-off", "The VM is powered off: a good first test migration.", "")
	}

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

// assessInventory fills every VM's readiness for both targets.
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
			}
		}
	}
	for _, target := range vmTargets {
		summary.Readiness = append(summary.Readiness, *counts[target])
	}
	return summary
}
