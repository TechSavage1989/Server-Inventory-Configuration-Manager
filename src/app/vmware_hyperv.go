package main

// Hyper-V VM creation for a copied VMware VM: the plan maps the VMware VM's
// firmware, CPUs, memory, disks, adapters and VLANs onto a Hyper-V VM that
// uses the copied VHDX files, and is turned into one PowerShell script run on
// the Hyper-V host. The script refuses to replace an existing VM, never
// deletes anything and leaves the new VM off unless asked to start it.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type vmHyperVAdapter struct {
	Label  string `json:"label"`
	Switch string `json:"switch"`
	MAC    string `json:"mac,omitempty"`
	VLAN   int    `json:"vlan,omitempty"`
}

type vmHyperVPlan struct {
	Name               string            `json:"name"`
	Generation         int               `json:"generation"`
	CPUs               int               `json:"cpus"`
	MemoryBytes        int64             `json:"memoryBytes"`
	SecureBoot         bool              `json:"secureBoot"`
	SecureBootTemplate string            `json:"secureBootTemplate,omitempty"`
	TPM                bool              `json:"tpm"`
	Path               string            `json:"path"`
	Disks              []string          `json:"disks"`
	Adapters           []vmHyperVAdapter `json:"adapters"`
	Start              bool              `json:"start"`
	Notes              string            `json:"notes,omitempty"`
	// ClusterRole adds the new VM to its failover cluster as a highly
	// available role; ClusterResult is the script's report of that step.
	ClusterRole   bool     `json:"clusterRole,omitempty"`
	ClusterResult string   `json:"clusterResult,omitempty"`
	Warnings      []string `json:"warnings"`
	// Remote writes the script's reports as pipeline output, so they come
	// back through PowerShell remoting (Invoke-Command) to the admin
	// computer; [Console]::Out on a remote host reaches nobody.
	Remote bool `json:"remote,omitempty"`
}

type vmHyperVOptions struct {
	// Folder is where Hyper-V keeps the VM's configuration; empty uses the
	// host's default.
	Folder string
	// Disks are the copied VHDX files in the VMware disk order.
	Disks []string
	// Switches maps VMware network names to Hyper-V virtual switch names.
	Switches map[string]string
	// KeepMAC gives each adapter the VMware MAC address.
	KeepMAC bool
	Start   bool
	// ClusterRole makes the new VM highly available in this host's cluster.
	ClusterRole bool
}

// The script reports the cluster step on a line of its own, so a VM that was
// created is never reported as failed because the cluster refused it.
const (
	vmClusterRoleAdded  = "CLUSTER-ROLE-ADDED"
	vmClusterRoleFailed = "CLUSTER-ROLE-FAILED"
)

var vmMACPattern = regexp.MustCompile(`^[0-9A-Fa-f]{2}([:-]?[0-9A-Fa-f]{2}){5}$`)

// vmHyperVPlanFor builds the Hyper-V VM for a VMware VM.
func vmHyperVPlanFor(vm vmMachine, networks []vmNetwork, options vmHyperVOptions) (*vmHyperVPlan, error) {
	if strings.TrimSpace(vm.Name) == "" {
		return nil, errors.New("the VM has no name")
	}
	if len(options.Disks) == 0 {
		return nil, errors.New("there are no copied disks to attach")
	}
	if len(vm.Disks) > 0 && len(options.Disks) != len(vm.Disks) {
		return nil, fmt.Errorf("the VM has %d disks but %d copies were given", len(vm.Disks), len(options.Disks))
	}
	plan := &vmHyperVPlan{Name: vm.Name, Generation: 1, CPUs: vm.CPUs, Path: options.Folder, Start: options.Start, ClusterRole: options.ClusterRole}
	if strings.EqualFold(vm.Firmware, "efi") {
		plan.Generation = 2
		plan.SecureBoot = vm.SecureBoot
		if plan.SecureBoot {
			plan.SecureBootTemplate = "MicrosoftWindows"
			if !strings.Contains(strings.ToLower(vm.GuestID+" "+vm.GuestOS), "windows") {
				plan.SecureBootTemplate = "MicrosoftUEFICertificateAuthority"
			}
		}
		plan.TPM = vm.VTPM
	} else if vm.VTPM {
		plan.Warnings = append(plan.Warnings, "The VM has a virtual TPM but BIOS firmware; Hyper-V offers a TPM only on generation 2, so none is added.")
	}
	if plan.CPUs < 1 {
		plan.CPUs = 1
	}
	memoryMB := vm.MemoryMB
	if memoryMB < 32 {
		memoryMB = 32
	}
	memoryMB += memoryMB % 2 // Hyper-V needs a multiple of 2 MB
	plan.MemoryBytes = memoryMB << 20

	// The boot disk goes first: the VMware boot candidate, else disk order.
	disks := append([]string(nil), options.Disks...)
	for index, disk := range vm.Disks {
		if disk.BootCandidate && index > 0 && index < len(disks) {
			boot := disks[index]
			copy(disks[1:index+1], disks[:index])
			disks[0] = boot
			break
		}
	}
	for _, disk := range disks {
		if !strings.HasSuffix(strings.ToLower(disk), ".vhdx") {
			return nil, fmt.Errorf("%s is not a VHDX file", disk)
		}
	}
	plan.Disks = disks
	if plan.Generation == 1 && len(disks) > 4 {
		plan.Warnings = append(plan.Warnings, "Generation 1: the boot disk is on IDE and the other disks on SCSI.")
	}

	vlans := map[string]int{}
	for _, network := range networks {
		if id, err := strconv.Atoi(strings.TrimSpace(network.VLAN)); err == nil && id > 0 && id < 4095 {
			if _, seen := vlans[network.Name]; !seen {
				vlans[network.Name] = id
			}
		}
	}
	for _, nic := range vm.NICs {
		adapter := vmHyperVAdapter{Label: nic.Label, Switch: options.Switches[nic.Network], VLAN: vlans[nic.Network]}
		if adapter.Switch == "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s (%s) has no Hyper-V switch chosen; it is added disconnected.", nic.Label, nic.Network))
		}
		if options.KeepMAC && vmMACPattern.MatchString(nic.MAC) {
			adapter.MAC = strings.ToUpper(strings.NewReplacer(":", "", "-", "").Replace(nic.MAC))
		}
		plan.Adapters = append(plan.Adapters, adapter)
	}
	return plan, nil
}

// vmPSQuote returns a PowerShell single-quoted string literal. Every quote
// character PowerShell accepts as a single quote is doubled, so no value can
// end the literal.
func vmPSQuote(value string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range value {
		switch r {
		case '\'', '‘', '’', '‚', '‛':
			b.WriteRune(r)
			b.WriteRune(r)
		case 0:
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// Script returns the PowerShell that creates the VM.
func (p *vmHyperVPlan) Script() string {
	var s strings.Builder
	line := func(format string, args ...interface{}) { fmt.Fprintf(&s, format+"\n", args...) }
	line("$ErrorActionPreference = 'Stop'")
	line("Import-Module Hyper-V")
	line("$name = %s", vmPSQuote(p.Name))
	line("if (Get-VM -Name $name -ErrorAction SilentlyContinue) { throw ('A Hyper-V VM named ' + $name + ' already exists; nothing was changed.') }")
	disks := make([]string, len(p.Disks))
	for i, disk := range p.Disks {
		disks[i] = vmPSQuote(disk)
	}
	line("$disks = @(%s)", strings.Join(disks, ", "))
	line("foreach ($disk in $disks) { if (-not (Test-Path -LiteralPath $disk -PathType Leaf)) { throw ('The copied disk ' + $disk + ' is missing.') } }")
	if p.Path != "" {
		line("$vm = New-VM -Name $name -Generation %d -MemoryStartupBytes %d -Path %s -NoVHD", p.Generation, p.MemoryBytes, vmPSQuote(p.Path))
	} else {
		line("$vm = New-VM -Name $name -Generation %d -MemoryStartupBytes %d -NoVHD", p.Generation, p.MemoryBytes)
	}
	line("try {")
	line("  Set-VMProcessor -VM $vm -Count %d", p.CPUs)
	line("  Set-VMMemory -VM $vm -DynamicMemoryEnabled $false")
	line("  Get-VMNetworkAdapter -VM $vm | Remove-VMNetworkAdapter")
	if p.Generation == 1 {
		line("  Add-VMHardDiskDrive -VM $vm -ControllerType IDE -ControllerNumber 0 -ControllerLocation 0 -Path $disks[0]")
		if len(p.Disks) > 1 {
			line("  for ($i = 1; $i -lt $disks.Count; $i++) { Add-VMHardDiskDrive -VM $vm -ControllerType SCSI -Path $disks[$i] }")
		}
	} else {
		line("  foreach ($disk in $disks) { Add-VMHardDiskDrive -VM $vm -ControllerType SCSI -Path $disk }")
		line("  Set-VMFirmware -VM $vm -FirstBootDevice (Get-VMHardDiskDrive -VM $vm | Select-Object -First 1)")
		if p.SecureBoot {
			line("  Set-VMFirmware -VM $vm -EnableSecureBoot On -SecureBootTemplate %s", vmPSQuote(p.SecureBootTemplate))
		} else {
			line("  Set-VMFirmware -VM $vm -EnableSecureBoot Off")
		}
		if p.TPM {
			line("  Set-VMKeyProtector -VM $vm -NewLocalKeyProtector")
			line("  Enable-VMTPM -VM $vm")
		}
	}
	for index, adapter := range p.Adapters {
		name := fmt.Sprintf("Network Adapter %d", index+1)
		if adapter.Switch != "" {
			line("  $nic = Add-VMNetworkAdapter -VM $vm -Name %s -SwitchName %s -Passthru", vmPSQuote(name), vmPSQuote(adapter.Switch))
		} else {
			line("  $nic = Add-VMNetworkAdapter -VM $vm -Name %s -Passthru", vmPSQuote(name))
		}
		if adapter.MAC != "" {
			line("  Set-VMNetworkAdapter -VMNetworkAdapter $nic -StaticMacAddress %s", vmPSQuote(adapter.MAC))
		}
		if adapter.VLAN > 0 {
			line("  Set-VMNetworkAdapterVlan -VMNetworkAdapter $nic -Access -VlanId %d", adapter.VLAN)
		}
	}
	notes := p.Notes
	if notes == "" {
		notes = "Moved from VMware by Server Inventory and Migration Manager."
	}
	line("  Set-VM -VM $vm -Notes %s", vmPSQuote(notes))
	line("} catch {")
	line("  $problem = $_")
	line("  # The new VM is left in place, without its disks deleted, so the cause can be checked.")
	line("  throw ('Creating the VM stopped: ' + $problem.Exception.Message + ' The partly created VM ' + $name + ' was left for review; its copied disks were not changed.')")
	line("}")
	report, final := "[Console]::Out.WriteLine(%s)", "[Console]::Out.Write($vm.Id.ToString())"
	if p.Remote {
		report, final = "Write-Output (%s)", "Write-Output $vm.Id.ToString()"
	}
	if p.ClusterRole {
		line("try { Import-Module FailoverClusters; Add-ClusterVirtualMachineRole -VMName $name -ErrorAction Stop | Out-Null; "+report+" } catch { "+report+" }", "'"+vmClusterRoleAdded+"'", "'"+vmClusterRoleFailed+" ' + $_.Exception.Message")
	}
	if p.Start {
		line("Start-VM -VM $vm")
	}
	line(final)
	return s.String()
}
