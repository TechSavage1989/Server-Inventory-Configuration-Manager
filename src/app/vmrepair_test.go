package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func vmTestMBR(active bool) []byte {
	disk := make([]byte, 1<<20)
	disk[446+4] = 0x07
	if active {
		disk[446] = 0x80
	}
	binary.LittleEndian.PutUint32(disk[446+8:], 2048)
	binary.LittleEndian.PutUint32(disk[446+12:], 1000)
	disk[510], disk[511] = 0x55, 0xAA
	return disk
}

func vmTestGPT(sector int, esp bool) []byte {
	disk := make([]byte, 1<<20)
	disk[446+4] = 0xEE
	disk[510], disk[511] = 0x55, 0xAA
	header := disk[sector:]
	copy(header, "EFI PART")
	binary.LittleEndian.PutUint64(header[72:], 2)
	binary.LittleEndian.PutUint32(header[80:], 128)
	binary.LittleEndian.PutUint32(header[84:], 128)
	entries := disk[2*sector:]
	basic := []byte{0xa2, 0xa0, 0xd0, 0xeb, 0xe5, 0xb9, 0x33, 0x44, 0x87, 0xc0, 0x68, 0xb6, 0xb7, 0x26, 0x99, 0xc7}
	copy(entries[0:], basic)
	if esp {
		copy(entries[128:], vmGPTEspType[:])
	}
	return disk
}

func TestDetectBootReadsTheDisksPartitionTable(t *testing.T) {
	cases := []struct {
		name     string
		disk     []byte
		style    string
		firmware string
		system   bool
		count    int
	}{
		{"active MBR", vmTestMBR(true), "MBR", "bios", true, 1},
		{"MBR without an active partition", vmTestMBR(false), "MBR", "bios", false, 1},
		{"GPT with an EFI system partition", vmTestGPT(512, true), "GPT", "uefi", true, 2},
		{"GPT on 4K sectors", vmTestGPT(4096, true), "GPT", "uefi", true, 2},
		{"GPT without an EFI system partition", vmTestGPT(512, false), "GPT", "uefi", false, 1},
		{"blank disk", make([]byte, 1<<20), "none", "unknown", false, 0},
	}
	for _, item := range cases {
		info := vmDetectBoot(bytes.NewReader(item.disk), int64(len(item.disk)))
		if info.PartitionStyle != item.style || info.Firmware != item.firmware || info.SystemPartition != item.system || info.Partitions != item.count {
			t.Errorf("%s: got %+v", item.name, info)
		}
		if !item.system && info.Note == "" {
			t.Errorf("%s: expected a note explaining why it may not start", item.name)
		}
	}
	if info := vmDetectBoot(bytes.NewReader(make([]byte, 100)), 100); info.Firmware != "unknown" {
		t.Errorf("a tiny disk was read as %+v", info)
	}
}

// PowerShell writes a one-entry list as an object and an empty one as null;
// both must still read as lists.
func TestParseRepairReportAcceptsPowerShellShapes(t *testing.T) {
	data := []byte("\xEF\xBB\xBF" + `{"Ok":true,"Mode":"Apply","Image":"D:\\x.vhdx","Target":"kvm",
		"Boot":{"PartitionStyle":"GPT","Firmware":"uefi","SystemPartition":true},
		"Windows":{"Partition":3,"ProductName":"Windows Server 2019 Datacenter","Build":17763,"Server":true,"OsTag":"2k19",
			"Drivers":{"Name":"storahci","Present":true,"Start":3,"Override":true,"File":true},"VMwareTools":true,"VMwareServices":"VMTools"},
		"Findings":{"Severity":"warning","Message":"No VirtIO storage driver is installed."},
		"Applied":{"Record":"C:\\r.json","Drivers":"oem3.inf","Changes":{"Key":"ControlSet001\\Services\\storahci","Name":"Start","Old":3,"Existed":true,"New":0},"Overrides":null},
		"Undone":null,"Error":""}`)
	report, err := parseRepairReport(data)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Boot == nil || report.Boot.Firmware != "uefi" || len(report.Windows) != 1 || len(report.Findings) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	windows := report.Windows[0]
	if windows.Build != 17763 || windows.OsTag != "2k19" || len(windows.Drivers) != 1 || windows.Drivers[0].Start != 3 || len(windows.VMwareServices) != 1 {
		t.Fatalf("unexpected Windows entry: %+v", windows)
	}
	if report.Applied == nil || len(report.Applied.Changes) != 1 || len(report.Applied.Drivers) != 1 || report.Applied.Drivers[0] != "oem3.inf" || len(report.Applied.Overrides) != 0 {
		t.Fatalf("unexpected applied block: %+v", report.Applied)
	}
	if message := vmRepairDoneMessageText("Apply", report); message != "Fixed up: 1 driver package added and 1 setting changed. Undo puts them back." {
		t.Fatalf("unexpected message %q", message)
	}
	if _, err := parseRepairReport([]byte("not json")); err == nil {
		t.Fatal("expected an error for a broken result")
	}
}

func TestRepairTargetsCoverEveryDestination(t *testing.T) {
	for _, id := range []string{vmTargetHyperV, vmTargetScale, vmTargetNutanix, vmTargetProxmox, vmTargetVSphere, vmTargetPhysical} {
		target, ok := vmRepairTargetFor(id)
		if !ok {
			t.Fatalf("no fix-up target for %s", id)
		}
		if target.VirtIO != (target.Driver == "kvm") {
			t.Errorf("%s: VirtIO flag does not match its driver set", id)
		}
	}
	if _, ok := vmRepairTargetFor("azure"); ok {
		t.Error("Azure is on hold and must not be offered")
	}
}
