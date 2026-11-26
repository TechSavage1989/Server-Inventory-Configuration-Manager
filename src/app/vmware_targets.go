package main

// Migration targets. Each profile describes what a VM needs to move from
// VMware to one destination: the drivers its guest must have before cutover,
// what the destination supports (firmware, Secure Boot, TPM, shared disks,
// device passthrough), how capacity is planned, and the usual migration tool.
// assessVM (vmware_model.go) and the runbook (vmware_plan.go) read these
// profiles so each target's rules stay in one place. No Windows dependency.

const (
	vmTargetHyperV   = "hyperv"
	vmTargetScale    = "scale"
	vmTargetNutanix  = "nutanix"
	vmTargetProxmox  = "proxmox"
	vmTargetAzure    = "azure"
	vmTargetAWS      = "aws"
	vmTargetPhysical = "physical"

	vmKindCluster  = "cluster"
	vmKindCloud    = "cloud"
	vmKindPhysical = "physical"
)

type vmTargetProfile struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Short string `json:"short"`
	// Kind decides how capacity is planned: a cluster of nodes, cloud
	// instances (one per VM, limited by subscription quota) or one physical
	// server per VM.
	Kind string `json:"kind"`
	// Tool is the usual way to copy the VM across.
	Tool string `json:"tool"`
	// Family groups targets that share a hypervisor's drivers: hyperv
	// (Hyper-V and Azure), kvm (HyperCore, AHV, Proxmox), aws, physical.
	Family string `json:"family"`
	// StorageLabel names what a datastore maps to on the target.
	StorageLabel string `json:"storageLabel"`
	NetworkLabel string `json:"networkLabel"`
	// WindowsDrivers is what a Windows guest must have installed before
	// cutover to boot and reach the network; empty when built in.
	WindowsDrivers string `json:"windowsDrivers"`
	LinuxDrivers   string `json:"linuxDrivers"`
	SecureBoot     string `json:"-"`
	VTPM           string `json:"-"`
	SharedDisks    string `json:"-"`
	Passthrough    string `json:"-"`
	USB            string `json:"-"`
	Availability   string `json:"-"`
	CreateStep     string `json:"-"`
}

var vmTargetProfiles = []vmTargetProfile{
	{
		ID: vmTargetHyperV, Label: "Hyper-V", Short: "Hyper-V", Kind: vmKindCluster, Family: "hyperv",
		Tool:         "this application's Move to Hyper-V (in the VM's details, run on the Hyper-V host), or your migration tool (for example System Center VMM or Veeam Instant Recovery)",
		StorageLabel: "Cluster Shared Volume or share", NetworkLabel: "virtual switch",
		LinuxDrivers: "hv_storvsc, hv_netvsc and hv_vmbus",
		SecureBoot:   "On Hyper-V choose the Microsoft UEFI Certificate Authority Secure Boot template for Linux guests, or turn Secure Boot off for this VM.",
		VTPM:         "Move VMs adds a virtual TPM to the new Generation 2 VM by itself (a local key protector). In a failover cluster, give every node the same Shielded VM local certificates, or the VM starts only on the node that created it.",
		SharedDisks:  "Guest clusters need shared storage on the target: use a Shared VHDX or VHD Set on Hyper-V, or redesign the cluster.",
		Passthrough:  "Plan the equivalent on Hyper-V (Discrete Device Assignment or GPU partitioning), or keep the workload on VMware.",
		USB:          "Hyper-V does not pass USB devices to VMs. Use a USB-over-network device (common for license dongles) or redesign.",
		Availability: "a Hyper-V failover cluster",
	},
	{
		ID: vmTargetScale, Label: "Scale Computing HyperCore", Short: "Scale HyperCore", Kind: vmKindCluster, Family: "kvm",
		Tool:         "this application's receiver copy (Move to Scale HyperCore in the VM's details), or Scale Computing's migration tooling (for example SC//Migrate) or a VM import",
		StorageLabel: "HyperCore storage", NetworkLabel: "VLAN",
		WindowsDrivers: "the Scale Computing guest tools (VirtIO storage and network drivers)",
		LinuxDrivers:   "virtio_blk, virtio_scsi and virtio_net",
		SecureBoot:     "Confirm the HyperCore version you are moving to supports Secure Boot for this guest, or turn Secure Boot off for this VM.",
		VTPM:           "Confirm your HyperCore version supports a virtual TPM before relying on one.",
		SharedDisks:    "HyperCore has no shared virtual disks. Redesign the guest cluster (for example in-guest iSCSI or application-level replication).",
		Passthrough:    "Check whether your HyperCore version supports this device, or keep the workload on VMware.",
		USB:            "Check whether your HyperCore version can pass this USB device through, or use a USB-over-network device (common for license dongles).",
		Availability:   "HyperCore HA",
	},
	{
		ID: vmTargetNutanix, Label: "Nutanix AHV", Short: "Nutanix AHV", Kind: vmKindCluster, Family: "kvm",
		Tool:         "this application's receiver copy (Move to Nutanix AHV in the VM's details), or Nutanix Move, which copies the VM from vCenter or ESXi and can install the VirtIO drivers for supported guests",
		StorageLabel: "AHV storage container", NetworkLabel: "AHV subnet",
		WindowsDrivers: "the Nutanix VirtIO drivers (Nutanix Move installs them for supported Windows versions, or install them yourself)",
		LinuxDrivers:   "virtio_blk, virtio_scsi and virtio_net",
		SecureBoot:     "AHV supports UEFI Secure Boot on current AOS versions; create the VM with UEFI and Secure Boot on, or turn Secure Boot off for this VM.",
		VTPM:           "Recent AHV versions support a virtual TPM; confirm your AOS version and add one on the new VM.",
		SharedDisks:    "Use a Nutanix Volume Group attached to every cluster node (directly or over iSCSI) for the shared disks, or redesign the guest cluster.",
		Passthrough:    "AHV can pass through supported GPUs (passthrough or vGPU). Check your hardware and AOS version, or keep the workload on VMware.",
		USB:            "AHV does not pass host USB devices to VMs. Use a USB-over-network device (common for license dongles).",
		Availability:   "AHV high availability",
	},
	{
		ID: vmTargetProxmox, Label: "Proxmox VE (KVM)", Short: "Proxmox VE", Kind: vmKindCluster, Family: "kvm",
		Tool:         "this application's receiver copy (Move to Proxmox VE in the VM's details), or the Proxmox VE ESXi import wizard (Proxmox VE 8.2 or later) or an OVF export and qm importovf",
		StorageLabel: "Proxmox storage", NetworkLabel: "bridge",
		WindowsDrivers: "the virtio-win drivers (VirtIO SCSI, block and network), or start the VM on SATA and e1000 and switch after installing them",
		LinuxDrivers:   "virtio_blk, virtio_scsi and virtio_net",
		SecureBoot:     "Use OVMF (UEFI) with pre-enrolled keys on Proxmox VE to keep Secure Boot, or turn Secure Boot off for this VM.",
		VTPM:           "Proxmox VE supports a virtual TPM (TPM state disk); add one on the new VM.",
		SharedDisks:    "Shared disks need shared storage and careful configuration on Proxmox VE. Test the guest cluster first, or redesign it (for example in-guest iSCSI).",
		Passthrough:    "Proxmox VE supports PCI passthrough with IOMMU enabled on the host. Plan the device on the target host.",
		USB:            "Proxmox VE can pass a host USB device to the VM. Plan which host it runs on, or use a USB-over-network device.",
		Availability:   "Proxmox VE HA",
	},
	{
		ID: vmTargetAzure, Label: "Microsoft Azure", Short: "Azure", Kind: vmKindCloud, Family: "hyperv",
		Tool:         "Azure Migrate (Server Migration), which replicates the VM from vCenter",
		StorageLabel: "managed disk type", NetworkLabel: "virtual network and subnet",
		LinuxDrivers: "hv_storvsc, hv_netvsc and hv_vmbus (plus the Azure Linux agent)",
		SecureBoot:   "Use a Trusted Launch (Generation 2) VM in Azure to keep Secure Boot.",
		VTPM:         "Trusted Launch (Generation 2) VMs in Azure have a virtual TPM; turn it on for the new VM.",
		SharedDisks:  "Azure shared disks (Premium SSD or Ultra Disk) can host a guest cluster. Check the cluster design is supported, or redesign it.",
		Passthrough:  "Physical devices cannot move to Azure. Use a GPU-enabled VM size for GPU workloads, or keep the workload on premises.",
		USB:          "Azure VMs have no USB. Use a USB-over-network device or a licensing alternative.",
		Availability: "availability zones or sets in Azure",
	},
	{
		ID: vmTargetAWS, Label: "Amazon EC2", Short: "AWS EC2", Kind: vmKindCloud, Family: "aws",
		Tool:         "AWS Application Migration Service, which replicates the VM and adds the AWS drivers during conversion",
		StorageLabel: "EBS volume type", NetworkLabel: "VPC subnet",
		WindowsDrivers: "the AWS ENA network and NVMe storage drivers (Application Migration Service adds them during conversion; install them first if you use another tool)",
		LinuxDrivers:   "ena and nvme",
		SecureBoot:     "EC2 supports UEFI Secure Boot on Nitro instances; launch with UEFI boot mode, or turn Secure Boot off for this VM.",
		VTPM:           "EC2 offers NitroTPM on UEFI Nitro instances; turn it on for the new instance.",
		SharedDisks:    "EBS Multi-Attach (io1/io2) is limited and needs a cluster-aware file system. Redesign the guest cluster, for example with Amazon FSx.",
		Passthrough:    "Physical devices cannot move to EC2. Use a GPU instance type for GPU workloads, or keep the workload on premises.",
		USB:            "EC2 instances have no USB. Use a USB-over-network device or a licensing alternative.",
		Availability:   "multiple Availability Zones",
	},
	{
		ID: vmTargetPhysical, Label: "Physical server (V2P)", Short: "Physical server", Kind: vmKindPhysical, Family: "physical",
		Tool:         "this application's receiver copy (Move to a physical server in the VM's details), or a disk imaging tool that restores to different hardware (for example Windows Server Backup bare metal recovery or your backup product's restore to physical)",
		StorageLabel: "server disk or RAID volume", NetworkLabel: "switch port or VLAN",
		WindowsDrivers: "the new server's storage controller and network drivers (from the server vendor), installed in the VM or injected during the restore",
		LinuxDrivers:   "the new server's storage controller and network drivers",
		SecureBoot:     "Turn on Secure Boot in the server's UEFI setup, or turn it off for this VM.",
		VTPM:           "The server's own TPM replaces the virtual TPM; turn it on in the server's firmware.",
		SharedDisks:    "Guest cluster disks need shared storage (a SAN or iSCSI target) presented to the physical servers.",
		Passthrough:    "The device can be installed in the physical server directly. Plan the hardware and its drivers.",
		USB:            "Plug the USB device into the physical server.",
		Availability:   "a physical cluster or a standby server",
	},
}

var vmTargets = func() []string {
	ids := []string{}
	for _, profile := range vmTargetProfiles {
		ids = append(ids, profile.ID)
	}
	return ids
}()

func vmTargetProfileFor(target string) (vmTargetProfile, bool) {
	for _, profile := range vmTargetProfiles {
		if profile.ID == target {
			return profile, true
		}
	}
	return vmTargetProfile{}, false
}

func vmIsTarget(target string) bool {
	_, ok := vmTargetProfileFor(target)
	return ok
}

func vmTargetLabel(target string) string {
	if profile, ok := vmTargetProfileFor(target); ok {
		return profile.Label
	}
	return target
}
