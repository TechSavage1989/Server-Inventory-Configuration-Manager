package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Fix-ups for a converted Windows disk (Repair-ConvertedDisk.ps1, run by
// vmrepair_windows.go) and the boot firmware a disk image needs, read from
// its partition table. No Windows dependency here.

// vmRepairTarget is a platform the fix-ups can prepare a disk for. Driver
// is the script's driver set: the KVM platforms share VirtIO.
type vmRepairTarget struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Driver string `json:"driver"`
	// VirtIO is true when the platform boots best from VirtIO disks, so
	// the page asks for a VirtIO drivers folder.
	VirtIO bool `json:"virtio"`
}

var vmRepairTargets = []vmRepairTarget{
	{ID: vmTargetHyperV, Label: "Hyper-V", Driver: "hyperv"},
	{ID: vmTargetScale, Label: "Scale Computing HyperCore", Driver: "kvm", VirtIO: true},
	{ID: vmTargetNutanix, Label: "Nutanix AHV", Driver: "kvm", VirtIO: true},
	{ID: vmTargetProxmox, Label: "Proxmox VE (KVM)", Driver: "kvm", VirtIO: true},
	{ID: vmTargetVSphere, Label: "VMware ESXi", Driver: "vmware"},
	{ID: vmTargetPhysical, Label: "Physical server", Driver: "physical"},
}

// vmTargetVSphere is VMware as a destination: the converter can send a disk
// to ESXi or vCenter (it is a source everywhere else).
const vmTargetVSphere = "vsphere"

func vmRepairTargetFor(id string) (vmRepairTarget, bool) {
	for _, target := range vmRepairTargets {
		if target.ID == id {
			return target, true
		}
	}
	return vmRepairTarget{}, false
}

// vmRepairReport is the script's result file. PowerShell writes PascalCase
// names; encoding/json matches them without case.
type vmRepairReport struct {
	OK       bool              `json:"ok"`
	Mode     string            `json:"mode"`
	Image    string            `json:"image"`
	Target   string            `json:"target"`
	Boot     *vmRepairBoot     `json:"boot"`
	Windows  []vmRepairWindows `json:"windows"`
	Findings []vmRepairFinding `json:"findings"`
	Applied  *vmRepairApplied  `json:"applied"`
	Undone   *vmRepairUndone   `json:"undone"`
	Error    string            `json:"error"`
}

type vmRepairBoot struct {
	PartitionStyle  string `json:"partitionStyle"`
	Firmware        string `json:"firmware"`
	SystemPartition bool   `json:"systemPartition"`
}

type vmRepairWindows struct {
	Partition      int               `json:"partition"`
	ProductName    string            `json:"productName"`
	Version        string            `json:"version"`
	Build          int               `json:"build"`
	Edition        string            `json:"edition"`
	Server         bool              `json:"server"`
	Architecture   string            `json:"architecture"`
	OsTag          string            `json:"osTag"`
	ControlSet     string            `json:"controlSet"`
	Drivers        []vmRepairDriver  `json:"drivers"`
	VMwareTools    bool              `json:"vmwareTools"`
	VMwareServices vmFlexibleStrings `json:"vmwareServices"`
}

type vmRepairDriver struct {
	Name     string `json:"name"`
	Present  bool   `json:"present"`
	Start    int    `json:"start"`
	Override bool   `json:"override"`
	File     bool   `json:"file"`
}

type vmRepairFinding struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type vmRepairChange struct {
	Key     string          `json:"key"`
	Name    string          `json:"name"`
	Old     json.RawMessage `json:"old"`
	Existed bool            `json:"existed"`
	New     json.RawMessage `json:"new"`
}

type vmRepairApplied struct {
	Record    string            `json:"record"`
	Drivers   vmFlexibleStrings `json:"drivers"`
	Changes   []vmRepairChange  `json:"changes"`
	Overrides []json.RawMessage `json:"overrides"`
}

type vmRepairUndone struct {
	Restored int               `json:"restored"`
	Drivers  vmFlexibleStrings `json:"drivers"`
}

// vmFlexibleStrings reads a JSON string list that PowerShell may write as
// a single string, null or an empty object.
type vmFlexibleStrings []string

func (v *vmFlexibleStrings) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch {
	case len(data) == 0 || string(data) == "null" || string(data) == "{}":
		*v = nil
		return nil
	case data[0] == '"':
		var single string
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		*v = []string{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*v = list
	return nil
}

const vmRepairReportLimit = 4 << 20

// parseRepairReport reads the script's result. PowerShell writes a list
// with one entry as an object, so lists are normalized first.
func parseRepairReport(data []byte) (*vmRepairReport, error) {
	if len(data) > vmRepairReportLimit {
		return nil, errors.New("the fix-up result is too large")
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var tree interface{}
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("the fix-up result could not be read: %v", err)
	}
	normalized, _ := json.Marshal(vmNormalizeLists(tree))
	var report vmRepairReport
	if err := json.Unmarshal(normalized, &report); err != nil {
		return nil, fmt.Errorf("the fix-up result could not be read: %v", err)
	}
	return &report, nil
}

// vmRepairListKeys are the result's list fields.
var vmRepairListKeys = map[string]bool{"windows": true, "findings": true, "changes": true, "overrides": true, "drivers": true}

// vmNormalizeLists turns a list field that PowerShell wrote as a lone
// object, or as null, into a list.
func vmNormalizeLists(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, item := range typed {
			if vmRepairListKeys[strings.ToLower(key)] {
				switch item.(type) {
				case nil:
					item = []interface{}{}
				case map[string]interface{}:
					item = []interface{}{item}
				}
			}
			typed[key] = vmNormalizeLists(item)
		}
		return typed
	case []interface{}:
		for index, item := range typed {
			typed[index] = vmNormalizeLists(item)
		}
		return typed
	}
	return value
}

func vmRepairDoneMessageText(mode string, report *vmRepairReport) string {
	switch mode {
	case "Apply":
		count := 0
		drivers := 0
		if report.Applied != nil {
			count, drivers = len(report.Applied.Changes)+len(report.Applied.Overrides), len(report.Applied.Drivers)
		}
		message := "Fixed up: "
		if drivers > 0 {
			message += vmPlural(drivers, "driver package", "driver packages") + " added and "
		}
		return message + vmPlural(count, "setting", "settings") + " changed. Undo puts them back."
	case "Undo":
		restored := 0
		if report.Undone != nil {
			restored = report.Undone.Restored
		}
		return "Undone: " + vmPlural(restored, "setting", "settings") + " put back."
	}
	if len(report.Windows) > 0 {
		return "Found " + report.Windows[0].ProductName + "."
	}
	return "Checked."
}

// vmRepairRecord is the part of a fix-up record Undo needs to find it.
type vmRepairRecord struct {
	Image    string `json:"image"`
	Complete bool   `json:"complete"`
}

func parseRepairRecord(data []byte) (*vmRepairRecord, error) {
	var record vmRepairRecord
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// Boot firmware from the partition table.

// vmBootInfo is what a disk image's partition table says about how it
// boots: GPT disks need UEFI (Hyper-V generation 2), MBR disks BIOS.
type vmBootInfo struct {
	PartitionStyle  string `json:"partitionStyle"` // GPT, MBR or none
	Firmware        string `json:"firmware"`       // uefi, bios or unknown
	SystemPartition bool   `json:"systemPartition"`
	Partitions      int    `json:"partitions"`
	Note            string `json:"note"`
}

var (
	vmGPTEspType   = [16]byte{0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11, 0xba, 0x4b, 0x00, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b}
	vmGPTSignature = []byte("EFI PART")
)

// vmDetectBoot reads the first sectors of a disk image. It tries 512-byte
// sectors, then 4096-byte sectors for a GPT header.
func vmDetectBoot(disk io.ReaderAt, size int64) vmBootInfo {
	mbr := make([]byte, 512)
	if size < 512 {
		return vmBootInfo{PartitionStyle: "none", Firmware: "unknown", Note: "The disk is too small to hold a partition table."}
	}
	if _, err := disk.ReadAt(mbr, 0); err != nil && err != io.EOF {
		return vmBootInfo{PartitionStyle: "none", Firmware: "unknown", Note: "The first sector could not be read: " + err.Error()}
	}
	if mbr[510] != 0x55 || mbr[511] != 0xAA {
		if vmAllZero(mbr) {
			return vmBootInfo{PartitionStyle: "none", Firmware: "unknown", Note: "The disk has no partition table (it is blank or holds a whole-disk file system)."}
		}
		return vmBootInfo{PartitionStyle: "none", Firmware: "unknown", Note: "The disk has no partition table."}
	}
	protective := false
	for entry := 0; entry < 4; entry++ {
		if mbr[446+entry*16+4] == 0xEE {
			protective = true
		}
	}
	if protective {
		for _, sector := range []int64{512, 4096} {
			if info, ok := vmReadGPT(disk, size, sector); ok {
				return info
			}
		}
		return vmBootInfo{PartitionStyle: "GPT", Firmware: "uefi", Note: "The disk has a protective MBR but its GPT header could not be read."}
	}
	info := vmBootInfo{PartitionStyle: "MBR", Firmware: "bios"}
	for entry := 0; entry < 4; entry++ {
		record := mbr[446+entry*16 : 446+entry*16+16]
		if record[4] == 0 {
			continue
		}
		info.Partitions++
		if record[0] == 0x80 {
			info.SystemPartition = true
		}
	}
	if !info.SystemPartition && info.Partitions > 0 {
		info.Note = "No partition is marked active, so BIOS firmware may not start it."
	}
	return info
}

func vmReadGPT(disk io.ReaderAt, size, sector int64) (vmBootInfo, bool) {
	header := make([]byte, 92)
	if sector*2 > size {
		return vmBootInfo{}, false
	}
	if _, err := disk.ReadAt(header, sector); err != nil && err != io.EOF {
		return vmBootInfo{}, false
	}
	if !bytes.Equal(header[:8], vmGPTSignature) {
		return vmBootInfo{}, false
	}
	entriesLBA := int64(binary.LittleEndian.Uint64(header[72:80]))
	count := int64(binary.LittleEndian.Uint32(header[80:84]))
	entrySize := int64(binary.LittleEndian.Uint32(header[84:88]))
	info := vmBootInfo{PartitionStyle: "GPT", Firmware: "uefi"}
	if count <= 0 || count > 1024 || entrySize < 128 || entrySize > 4096 || entriesLBA <= 0 || entriesLBA*sector+count*entrySize > size {
		info.Note = "The GPT partition entries could not be read."
		return info, true
	}
	entries := make([]byte, count*entrySize)
	if _, err := disk.ReadAt(entries, entriesLBA*sector); err != nil && err != io.EOF {
		info.Note = "The GPT partition entries could not be read."
		return info, true
	}
	for index := int64(0); index < count; index++ {
		entry := entries[index*entrySize : index*entrySize+128]
		if vmAllZero(entry[:16]) {
			continue
		}
		info.Partitions++
		if bytes.Equal(entry[:16], vmGPTEspType[:]) {
			info.SystemPartition = true
		}
	}
	if !info.SystemPartition {
		info.Note = "The disk has no EFI system partition, so UEFI firmware may not start it."
	}
	return info, true
}

func vmAllZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
