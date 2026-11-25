package main

// Moving a VMware VM to HyperCore, Nutanix AHV, Proxmox VE or a physical
// server: the replication rounds send each disk to a receiver running on
// the new machine (booted from receiver media), matched to the receiver's
// disks by size.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// vmReceiverLink is how to reach one receiver. The code stays in memory.
type vmReceiverLink struct {
	Address    string `json:"address"`
	Thumbprint string `json:"thumbprint"`
	Code       string `json:"-"`
}

// sinkFactory sends source disk index i to the receiver disk mapping[i].
func (l vmReceiverLink) sinkFactory(ctx context.Context, mapping []string) func(int, int64) (vmCopySink, string, error) {
	return func(index int, capacity int64) (vmCopySink, string, error) {
		if index < 0 || index >= len(mapping) || mapping[index] == "" {
			return nil, "", fmt.Errorf("disk %d has no receiver disk; check the receiver again", index+1)
		}
		sink, err := newVMRemoteSink(ctx, l.Address, l.Thumbprint, l.Code, mapping[index])
		if err != nil {
			return nil, "", err
		}
		return sink, mapping[index] + " on " + l.Address, nil
	}
}

// vmMatchReceiverDisks gives each source disk (by capacity) the smallest
// receiver disk that holds it, largest source first.
func vmMatchReceiverDisks(sources []int64, disks []vmReceiverDisk) ([]string, error) {
	if len(sources) == 0 {
		return nil, errors.New("the VM has no disks to copy")
	}
	if len(sources) > len(disks) {
		return nil, fmt.Errorf("the VM has %d disks but the receiver offers %d; give the new machine a blank disk for each", len(sources), len(disks))
	}
	order := make([]int, len(sources))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool { return sources[order[i]] > sources[order[j]] })
	used := map[string]bool{}
	mapping := make([]string, len(sources))
	for _, index := range order {
		best := -1
		for candidate, disk := range disks {
			if used[disk.Name] || disk.Size < sources[index] {
				continue
			}
			if best < 0 || disk.Size < disks[best].Size {
				best = candidate
			}
		}
		if best < 0 {
			return nil, fmt.Errorf("disk %d (%s) is larger than every free receiver disk", index+1, vmFormatBytes(sources[index]))
		}
		used[disks[best].Name] = true
		mapping[index] = disks[best].Name
	}
	return mapping, nil
}

// vmTargetCreateInput is what the operator chose for the new VM; anything
// left empty follows the VMware VM.
type vmTargetCreateInput struct {
	VMID       string              `json:"vmId"`
	Confirm    string              `json:"confirm"`
	Name       string              `json:"name"`
	CPUs       int                 `json:"cpus"`
	MemoryMiB  int64               `json:"memoryMiB"`
	Firmware   string              `json:"firmware"`
	SecureBoot *bool               `json:"secureBoot"`
	TPM        *bool               `json:"tpm"`
	Bus        string              `json:"bus"`
	NICModel   string              `json:"nicModel"`
	Storage    string              `json:"storage"`
	KeepMAC    bool                `json:"keepMac"`
	Networks   []vmTargetNICChoice `json:"networks"`
}

type vmTargetNICChoice struct {
	Network string `json:"network"`
	VLAN    int    `json:"vlan"`
}

// vmTargetSpecFor builds the new VM from the VMware VM: same CPUs, memory,
// firmware, Secure Boot and TPM, one disk per VMware disk (rounded up by the
// platform) and one adapter per VMware adapter, unless the operator chose
// otherwise.
func vmTargetSpecFor(vm vmMachine, input vmTargetCreateInput, iso string) (vmTargetVMSpec, error) {
	spec := vmTargetVMSpec{
		Name: vm.Name, CPUs: vm.CPUs, MemoryMiB: vm.MemoryMB, Firmware: "bios", SecureBoot: vm.SecureBoot, TPM: vm.VTPM,
		Windows:     regexp.MustCompile(`(?i)windows`).MatchString(vm.GuestID + " " + vm.GuestOS + " " + vm.GuestOSRunning),
		GuestOS:     strings.TrimSpace(vmNonEmpty(vm.GuestOSRunning, vm.GuestOS)),
		Description: "Moved from VMware (" + vm.Name + ") by Server Inventory and Migration Manager",
		Storage:     input.Storage, ISO: iso,
	}
	if strings.EqualFold(vm.Firmware, "efi") {
		spec.Firmware = "uefi"
	}
	if name := strings.TrimSpace(input.Name); name != "" {
		spec.Name = name
	}
	if input.CPUs > 0 {
		spec.CPUs = input.CPUs
	}
	if input.MemoryMiB > 0 {
		spec.MemoryMiB = input.MemoryMiB
	}
	if input.Firmware != "" {
		spec.Firmware = input.Firmware
	}
	if input.SecureBoot != nil {
		spec.SecureBoot = *input.SecureBoot
	}
	if input.TPM != nil {
		spec.TPM = *input.TPM
	}
	if spec.Firmware == "bios" {
		spec.SecureBoot, spec.TPM = false, false
	}
	for _, disk := range vm.Disks {
		if disk.RDM != "" {
			return spec, fmt.Errorf("%s is a raw device mapping, which is not copied", disk.Label)
		}
		spec.Disks = append(spec.Disks, vmTargetDisk{Size: disk.CapacityBytes, Bus: input.Bus})
	}
	for index, nic := range vm.NICs {
		choice := vmTargetNICChoice{}
		if index < len(input.Networks) {
			choice = input.Networks[index]
		}
		if strings.TrimSpace(choice.Network) == "" {
			return spec, fmt.Errorf("choose the network for %s (%s)", nic.Label, nic.Network)
		}
		item := vmTargetNIC{Network: strings.TrimSpace(choice.Network), VLAN: choice.VLAN, Model: input.NICModel}
		if input.KeepMAC {
			item.MAC = nic.MAC
		}
		spec.NICs = append(spec.NICs, item)
	}
	return spec, vmValidateTargetSpec(&spec)
}

// vmTargetRecord is saved per VMware VM so a later session (or a restart)
// knows which VM was created for it. It holds no password, token or code.
type vmTargetRecord struct {
	VMwareID   string     `json:"vmwareId"`
	VMwareName string     `json:"vmwareName"`
	Platform   string     `json:"platform"`
	Server     string     `json:"server"`
	Node       string     `json:"node"`
	VM         vmTargetVM `json:"vm"`
	ISO        string     `json:"iso"`
	Created    time.Time  `json:"created"`
	CutOver    time.Time  `json:"cutOver,omitempty"`
}

func vmTargetRecordPath(folder, vmwareID string) string {
	return filepath.Join(folder, vmRawRecordName.ReplaceAllString(vmwareID, "-")+".json")
}

func saveVMTargetRecord(folder string, record vmTargetRecord) error {
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := vmTargetRecordPath(folder, record.VMwareID)
	if err := os.WriteFile(path+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func loadVMTargetRecord(folder, vmwareID string) (*vmTargetRecord, error) {
	raw, err := os.ReadFile(vmTargetRecordPath(folder, vmwareID))
	if err != nil {
		return nil, err
	}
	record := &vmTargetRecord{}
	if err := json.Unmarshal(raw, record); err != nil {
		return nil, err
	}
	return record, nil
}

// vmTargetUsesReceiver reports whether the target is reached by booting
// receiver media on the new VM or server and copying onto its own disks.
func vmTargetUsesReceiver(target string) bool {
	switch target {
	case vmTargetScale, vmTargetNutanix, vmTargetProxmox, vmTargetPhysical:
		return true
	}
	return false
}

// vmReceiverRunbookSteps are the runbook's preparation steps for a move
// through receiver media, done before the cutover window.
func vmReceiverRunbookSteps(target string, profile vmTargetProfile) []string {
	drivers := "the drivers the new machine needs to see its disks and network"
	if profile.WindowsDrivers != "" {
		drivers = profile.WindowsDrivers
	}
	steps := []string{"Build the receiver media in this application (the VM's details, Move to " + profile.Short + ", step 2) with the Windows ADK and its Windows PE add-on, adding " + drivers + "."}
	if target == vmTargetPhysical {
		steps = append(steps,
			"Write the receiver ISO to a USB stick or mount it as the server's virtual media, and boot the server from it. The receiver takes only blank disks.",
		)
	} else {
		steps = append(steps,
			"Upload the receiver media to "+profile.Short+" and create the VM from the Move panel. It gets the same CPUs, memory, firmware and disks (rounded up) and starts from the receiver media.",
		)
	}
	steps = append(steps,
		"Type the receiver's address, pairing code and certificate thumbprint from its console into the Move panel and check which disk each VMware disk goes to.",
		"Copy while the VMware VM keeps running, as often as needed; each copy after the first sends only changed blocks.",
	)
	if target == vmTargetPhysical {
		steps = append(steps, "At cutover choose Cut over: the guest shuts down through VMware Tools and the last changes are copied and verified. Then stop the receiver, remove the boot media and start the server from its own disk.")
	} else {
		steps = append(steps, "At cutover choose Cut over: the guest shuts down through VMware Tools, the last changes are copied and verified, and the new VM is set to start from its own disk.")
	}
	return steps
}
