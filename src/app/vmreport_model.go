package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The discovery and readiness report model (rule 44): one view of a VMware
// or Hyper-V inventory that the interactive HTML report (vmreport_html.go),
// the Word report (vmreport_docx.go) and the PDF report (vmreport_pdf.go)
// all draw from, so the three always say the same thing. Untagged and free
// of Windows calls so `go test ./...` covers it everywhere.

const (
	vmReportDocx = "Discovery-and-Readiness-Report.docx"
	vmReportPDF  = "Discovery-and-Readiness-Report.pdf"
)

// vmReportWords are the words that differ between a VMware and a Hyper-V
// source, so a Hyper-V report never talks about ESXi or VMware Tools.
type vmReportWords struct {
	Platform  string // VMware or Hyper-V
	Host      string // ESXi host or Hyper-V host
	Hosts     string
	Version   string // ESXi or Windows
	Tools     string // VMware Tools or Integration services
	Datastore string
	Storage   string
	Network   string
	Networks  string
}

func vmReportWordsFor(inventory *vmInventory) vmReportWords {
	if inventory.ServerType == hypervServerType {
		return vmReportWords{Platform: "Hyper-V", Host: "Hyper-V host", Hosts: "Hyper-V hosts", Version: "Windows", Tools: "Integration services", Datastore: "Storage location", Storage: "Storage", Network: "Virtual switch", Networks: "Virtual switches"}
	}
	return vmReportWords{Platform: "VMware", Host: "ESXi host", Hosts: "ESXi hosts", Version: "ESXi", Tools: "VMware Tools", Datastore: "Datastore", Storage: "Datastores", Network: "Network", Networks: "Networks"}
}

// vmReportTile is one summary figure. Href is the page it opens; Status and
// Flag (optional) filter the VM list when it is opened.
type vmReportTile struct {
	Value  string
	Label  string
	Detail string
	Href   string
	Tone   string // ready, review, blocked, skip, info or ""
	Status string
	Flag   string
	Target string
}

// vmReportTarget is how many VMs are ready for one target.
type vmReportTarget struct {
	ID, Label, Short             string
	Ready, Review, Blocked, Skip int
	Total                        int
}

// Percent returns a status's share of the target's VMs, for bar widths.
func (t vmReportTarget) Percent(status string) float64 {
	if t.Total == 0 {
		return 0
	}
	value := map[string]int{vmStatusReady: t.Ready, vmStatusReview: t.Review, vmStatusBlocked: t.Blocked, vmStatusSkip: t.Skip}[status]
	return float64(value) * 100 / float64(t.Total)
}

// vmReportIssueVM is one VM an issue applies to, with its own wording when
// that differs from the issue's.
type vmReportIssueVM struct {
	Name    string
	Anchor  string
	Message string
}

// vmReportIssue is one kind of finding for one target, with every VM it
// applies to and how to fix it.
type vmReportIssue struct {
	Anchor   string
	Target   string
	Code     string
	Severity string
	Message  string
	Action   string
	VMs      []vmReportIssueVM
	// Varies is true when VMs have their own wording of the issue.
	Varies bool
}

// vmReportIssueTitles name issues whose wording carries details of each VM.
var vmReportIssueTitles = map[string]string{
	"encrypted":        "Disks encrypted with vSphere VM Encryption",
	"os-unsupported":   "Guest operating system not supported on the target",
	"os-obsolete":      "Guest operating system out of vendor support",
	"os-legacy":        "Guest operating system past mainstream support",
	"virtio-drivers":   "Windows needs VirtIO drivers",
	"gen1-boot-disk":   "Boot disk a Generation 1 VM cannot boot from",
	"gen1-cpu":         "More vCPUs than a Generation 1 VM supports",
	"nic-count":        "More network adapters than the target supports",
	"azure-os-disk":    "Boot disk larger than Azure allows",
	"azure-data-disk":  "Data disk larger than Azure allows",
	"vtpm":             "Virtual TPM",
	"shared-disk":      "Shared or multi-writer disks (guest cluster)",
	"rdm-virtual":      "Virtual-mode raw device mappings",
	"independent-disk": "Independent disks are excluded from snapshots",
	"snapshots":        "Snapshots or checkpoints to remove",
	"serial":           "Serial or parallel ports",
	"media":            "Connected CD/DVD media",
	"static-ip":        "Static IP configuration to carry over",
	"hardware-old":     "Very old virtual hardware version",
	"hardware-drivers": "Windows needs the new server's storage driver",
	// Optional domain controller and SQL Server checks (preflight.go).
	"preflight-failed":     "Domain controller and SQL Server checks could not reach the server",
	"dc-usn-rollback":      "Domain controller quarantined after a USN rollback",
	"dc-replication":       "Domain controller replication failing",
	"dc-replication-stale": "Domain controller replication out of date",
	"dc-sysvol":            "SYSVOL not healthy",
	"dc-dcdiag":            "dcdiag tests failing",
	"dc-time":              "Domain controller time source",
	"sql-vss-writer":       "SQL Server VSS Writer not running",
	"sql-fci":              "SQL Server failover cluster instance",
	"sql-ag":               "SQL Server availability group replica",
	"sql-query":            "SQL Server could not be queried",
	"sql-tde":              "Transparent Data Encryption certificates to back up",
	"sql-backup":           "Databases without a recent full backup",
	"sql-log-backup":       "Full recovery databases without log backups",
}

// vmReportTargetIssues are the issues for one target, worst first.
type vmReportTargetIssues struct {
	Target vmReportTarget
	Issues []vmReportIssue
}

// vmReportVMStatus is a VM's readiness for one target.
type vmReportVMStatus struct {
	Target     string
	Short      string
	Label      string
	Status     string
	Generation int
	Findings   []vmFinding
}

// vmReportVM is a VM with its links and every target's readiness.
type vmReportVM struct {
	*vmRelVM
	Statuses []vmReportVMStatus
	// Flags filter the VM list: on, off, snap, static, tools, encrypted,
	// template.
	Flags []string
	// Checks are the optional domain controller and SQL Server checks as
	// label and value rows (preflight.go); empty when never run.
	Checks [][2]string
}

// Status returns the VM's status for target ("" when not assessed).
func (vm vmReportVM) Status(target string) string {
	for _, status := range vm.Statuses {
		if status.Target == target {
			return status.Status
		}
	}
	return ""
}

type vmReportModel struct {
	Inventory   *vmInventory
	Words       vmReportWords
	Title       string
	Generated   time.Time
	Generator   string
	Summary     vmInventorySummary
	Relations   vmRelations
	Focus       vmReportTarget
	Targets     []vmReportTarget
	Issues      []vmReportTargetIssues
	Tiles       []vmReportTile
	EnvTiles    []vmReportTile
	Watch       []string
	VMs         []vmReportVM
	Templates   []vmReportVM
	Cores       int
	MemoryBytes int64
	// Used and free storage over every datastore.
	StorageCapacity int64
	StorageFree     int64
}

// vmReportFocusTarget is the target the summary tiles count for: the
// operator's plan target when there is one, else Hyper-V.
const vmReportFocusTarget = vmTargetHyperV

func vmReportBuild(inventory *vmInventory, now time.Time) *vmReportModel {
	model := &vmReportModel{Inventory: inventory, Words: vmReportWordsFor(inventory), Generated: now, Generator: vmReportGeneratorLine, Summary: summarizeInventory(inventory), Relations: vmRelationsFor(inventory), Watch: vmDiscoveryWatch(inventory, now)}
	model.Title = model.Words.Platform + " discovery and readiness"
	for _, host := range inventory.Hosts {
		model.Cores += host.Cores
		model.MemoryBytes += host.MemoryBytes
	}
	for _, datastore := range inventory.Datastores {
		model.StorageCapacity += datastore.CapacityBytes
		model.StorageFree += datastore.FreeBytes
	}

	targets := map[string]*vmReportTarget{}
	for _, profile := range vmTargetProfiles {
		model.Targets = append(model.Targets, vmReportTarget{ID: profile.ID, Label: profile.Label, Short: profile.Short})
	}
	for index := range model.Targets {
		targets[model.Targets[index].ID] = &model.Targets[index]
	}

	type issueKey struct{ target, code, severity string }
	issues := map[issueKey]*vmReportIssue{}
	messageCounts := map[issueKey]map[string]int{}
	order := []issueKey{}
	snapshots, static, off := 0, 0, 0
	for _, rel := range model.Relations.VMs {
		vm := rel.VM
		item := vmReportVM{vmRelVM: rel, Checks: preflightFacts(vm.Preflight)}
		if strings.EqualFold(vm.PowerState, "poweredOn") {
			item.Flags = append(item.Flags, "on")
		} else {
			item.Flags = append(item.Flags, "off")
		}
		if len(vm.Snapshots) > 0 {
			item.Flags = append(item.Flags, "snap")
		}
		if len(vmStaticNICs(vm)) > 0 {
			item.Flags = append(item.Flags, "static")
		}
		if vm.Encrypted {
			item.Flags = append(item.Flags, "encrypted")
		}
		for _, profile := range vmTargetProfiles {
			status := vmReportVMStatus{Target: profile.ID, Short: profile.Short, Label: profile.Label}
			if readiness := vm.readinessFor(profile.ID); readiness != nil {
				status.Status, status.Generation, status.Findings = readiness.Status, readiness.Generation, readiness.Findings
			}
			item.Statuses = append(item.Statuses, status)
		}
		if vm.Template {
			item.Flags = append(item.Flags, "template")
			model.Templates = append(model.Templates, item)
			continue
		}
		model.VMs = append(model.VMs, item)
		if len(vm.Snapshots) > 0 {
			snapshots++
		}
		if len(vmStaticNICs(vm)) > 0 {
			static++
		}
		if !strings.EqualFold(vm.PowerState, "poweredOn") {
			off++
		}
		for _, status := range item.Statuses {
			target := targets[status.Target]
			if target == nil || status.Status == "" {
				continue
			}
			target.Total++
			switch status.Status {
			case vmStatusReady:
				target.Ready++
			case vmStatusReview:
				target.Review++
			case vmStatusBlocked:
				target.Blocked++
			case vmStatusSkip:
				target.Skip++
			}
			for _, finding := range status.Findings {
				if finding.Severity == vmSeverityInfo {
					continue
				}
				key := issueKey{status.Target, finding.Code, finding.Severity}
				issue := issues[key]
				if issue == nil {
					issue = &vmReportIssue{Target: status.Target, Code: finding.Code, Severity: finding.Severity, Action: finding.Action}
					issues[key] = issue
					messageCounts[key] = map[string]int{}
					order = append(order, key)
				}
				messageCounts[key][finding.Message]++
				issue.VMs = append(issue.VMs, vmReportIssueVM{Name: vm.Name, Anchor: rel.Anchor, Message: finding.Message})
			}
		}
	}
	// An issue's title is the wording every VM shares; when the wording
	// names VM-specific details (disks, addresses, counts) the title is the
	// kind of issue and each VM keeps its own wording.
	for _, key := range order {
		issue := issues[key]
		title := ""
		if len(messageCounts[key]) == 1 {
			title = issue.VMs[0].Message
		} else if label := vmReportIssueTitles[key.code]; label != "" {
			title = label
		} else if head, _, found := strings.Cut(issue.VMs[0].Message, ":"); found && len(head) >= 12 {
			title = head
		} else {
			best := 0
			for _, vm := range issue.VMs {
				if count := messageCounts[key][vm.Message]; count > best {
					title, best = vm.Message, count
				}
			}
		}
		issue.Message = title
		for index := range issue.VMs {
			if issue.VMs[index].Message == title {
				issue.VMs[index].Message = ""
			} else {
				issue.Varies = true
			}
		}
	}
	for _, target := range model.Targets {
		group := vmReportTargetIssues{Target: target}
		for _, key := range order {
			if key.target == target.ID {
				group.Issues = append(group.Issues, *issues[key])
			}
		}
		sort.SliceStable(group.Issues, func(i, j int) bool {
			a, b := group.Issues[i], group.Issues[j]
			if vmSeverityRank(a.Severity) != vmSeverityRank(b.Severity) {
				return vmSeverityRank(a.Severity) > vmSeverityRank(b.Severity)
			}
			return len(a.VMs) > len(b.VMs)
		})
		for index := range group.Issues {
			group.Issues[index].Anchor = "issue-" + target.ID + "-" + strconv.Itoa(index+1)
		}
		model.Issues = append(model.Issues, group)
	}
	model.Focus = *targets[vmReportFocusTarget]

	focus := model.Focus
	model.Tiles = []vmReportTile{
		{Value: strconv.Itoa(len(model.VMs)), Label: "Virtual machines", Detail: fmt.Sprintf("%d on, %d off", len(model.VMs)-off, off), Href: "#vms"},
		{Value: strconv.Itoa(focus.Ready), Label: "Ready for " + focus.Short, Detail: "Nothing to fix first", Href: "#vms", Tone: vmStatusReady, Status: vmStatusReady, Target: focus.ID},
		{Value: strconv.Itoa(focus.Review), Label: "Review first", Detail: "Warnings to check", Href: "#vms", Tone: vmStatusReview, Status: vmStatusReview, Target: focus.ID},
		{Value: strconv.Itoa(focus.Blocked), Label: "Blocked", Detail: "Must be fixed to move", Href: "#vms", Tone: vmStatusBlocked, Status: vmStatusBlocked, Target: focus.ID},
		{Value: strconv.Itoa(focus.Skip), Label: "Not moved", Detail: model.Words.Platform + " system VMs", Href: "#vms", Tone: vmStatusSkip, Status: vmStatusSkip, Target: focus.ID},
		{Value: strconv.Itoa(snapshots), Label: "VMs with snapshots", Detail: "Remove before moving", Href: "#vms", Tone: map[bool]string{true: vmStatusReview}[snapshots > 0], Flag: "snap"},
		{Value: strconv.Itoa(static), Label: "VMs with static IPs", Detail: "Addresses to carry over", Href: "#vms", Flag: "static"},
	}
	model.EnvTiles = []vmReportTile{
		{Value: strconv.Itoa(len(inventory.Hosts)), Label: model.Words.Hosts, Detail: fmt.Sprintf("%d cluster(s)", len(inventory.Clusters)), Href: "#hosts"},
		{Value: strconv.Itoa(model.Cores), Label: "Physical cores", Detail: vmFormatBytes(model.MemoryBytes) + " memory", Href: "#hosts"},
		{Value: strconv.Itoa(model.Summary.VCPUs), Label: "vCPUs allocated", Detail: vmFormatBytes(model.Summary.MemoryBytes) + " VM memory", Href: "#vms"},
		{Value: strconv.Itoa(len(inventory.Datastores)), Label: model.Words.Storage, Detail: vmFormatBytes(model.StorageCapacity-model.StorageFree) + " used of " + vmFormatBytes(model.StorageCapacity), Href: "#datastores"},
		{Value: strconv.Itoa(len(inventory.Networks)), Label: model.Words.Networks, Href: "#networks"},
	}
	if inventory.ServerType != hypervServerType {
		model.EnvTiles = append(model.EnvTiles, vmReportTile{Value: strconv.Itoa(len(inventory.Licenses)), Label: "Licenses", Href: "#licenses"})
	}
	if len(model.Watch) > 0 {
		model.EnvTiles = append(model.EnvTiles, vmReportTile{Value: strconv.Itoa(len(model.Watch)), Label: "Need attention", Detail: "Licenses, alarms, hosts", Href: "#attention", Tone: vmStatusReview})
	}
	return model
}

// IssuesFor returns the issues for one target.
func (m *vmReportModel) IssuesFor(target string) []vmReportIssue {
	for _, group := range m.Issues {
		if group.Target.ID == target {
			return group.Issues
		}
	}
	return nil
}

// vmReportSeverityLabel names a finding's severity for people.
func vmReportSeverityLabel(severity string) string {
	return map[string]string{vmSeverityBlocker: "Blocker", vmSeverityWarning: "Warning", vmSeverityInfo: "Info"}[severity]
}

// vmReportPower names a power state for people.
func vmReportPower(state string) string {
	switch state {
	case "poweredOn":
		return "On"
	case "poweredOff":
		return "Off"
	case "suspended":
		return "Suspended"
	}
	return state
}

// vmReportGuest names a VM's guest operating system.
func vmReportGuest(vm vmMachine) string {
	return vmNonEmpty(vm.GuestOSRunning, vmNonEmpty(vm.GuestOS, vmNonEmpty(vm.GuestID, "Unknown")))
}

// vmReportFirmware describes a VM's firmware, Secure Boot and vTPM.
func vmReportFirmware(vm vmMachine) string {
	parts := []string{}
	switch strings.ToLower(vm.Firmware) {
	case "efi":
		parts = append(parts, "UEFI")
	case "bios":
		parts = append(parts, "BIOS")
	case "":
	default:
		parts = append(parts, vm.Firmware)
	}
	if vm.SecureBoot {
		parts = append(parts, "Secure Boot")
	}
	if vm.VTPM {
		parts = append(parts, "vTPM")
	}
	return strings.Join(parts, ", ")
}

// vmReportTools describes VMware Tools (or integration services) for a VM.
func vmReportTools(vm vmMachine) string {
	if vm.Source == "hyperv" {
		return vmNonEmpty(vm.IntegrationServices, vm.IntegrationVersion)
	}
	labels := map[string]string{"guestToolsCurrent": "Current", "guestToolsNeedUpgrade": "Upgrade available", "guestToolsNotInstalled": "Not installed", "guestToolsUnmanaged": "Guest managed (open-vm-tools)", "guestToolsSupportedOld": "Supported, old", "guestToolsSupportedNew": "Supported", "guestToolsTooOld": "Too old", "guestToolsTooNew": "Too new", "guestToolsBlacklisted": "Blocked version"}
	label := vmNonEmpty(labels[vm.ToolsStatus], vm.ToolsStatus)
	if label != "" && strings.EqualFold(vm.PowerState, "poweredOn") && !vm.ToolsRunning && vm.ToolsStatus != "guestToolsNotInstalled" {
		label += ", not running"
	}
	return label
}

// vmReportPercent is part of whole as a whole-number percentage.
func vmReportPercent(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	return int(part * 100 / whole)
}
