package main

import (
	"strings"
	"testing"
)

func TestHyperVPlanMapsFirmwareDisksAdaptersAndVLANs(t *testing.T) {
	vm := vmMachine{Name: "APP01", Firmware: "efi", SecureBoot: true, VTPM: true, GuestID: "windows2019srvNext_64Guest", CPUs: 4, MemoryMB: 8191,
		Disks: []vmDisk{{Label: "Hard disk 1"}, {Label: "Hard disk 2", BootCandidate: true}},
		NICs:  []vmNIC{{Label: "Network adapter 1", Network: "VM-Prod", MAC: "00:50:56:aa:bb:cc"}, {Label: "Network adapter 2", Network: "Backup"}}}
	networks := []vmNetwork{{Name: "VM-Prod", VLAN: "120"}, {Name: "Backup", VLAN: "trunk"}}
	plan, err := vmHyperVPlanFor(vm, networks, vmHyperVOptions{Folder: `D:\Hyper-V`, Disks: []string{`D:\Copies\APP01-disk0.vhdx`, `D:\Copies\APP01-disk1.vhdx`}, Switches: map[string]string{"VM-Prod": "External"}, KeepMAC: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Generation != 2 || !plan.SecureBoot || plan.SecureBootTemplate != "MicrosoftWindows" || !plan.TPM || plan.CPUs != 4 || plan.MemoryBytes != 8192<<20 {
		t.Fatalf("plan %+v", plan)
	}
	if plan.Disks[0] != `D:\Copies\APP01-disk1.vhdx` {
		t.Fatalf("the boot disk is not first: %v", plan.Disks)
	}
	if plan.Adapters[0].Switch != "External" || plan.Adapters[0].MAC != "005056AABBCC" || plan.Adapters[0].VLAN != 120 || plan.Adapters[1].Switch != "" || plan.Adapters[1].VLAN != 0 {
		t.Fatalf("adapters %+v", plan.Adapters)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "disconnected") {
		t.Fatalf("warnings %v", plan.Warnings)
	}
	script := plan.Script()
	for _, want := range []string{"-Generation 2", "-MemoryStartupBytes 8589934592", "already exists", "-SecureBootTemplate 'MicrosoftWindows'", "Enable-VMTPM", "-StaticMacAddress '005056AABBCC'", "-VlanId 120", "-SwitchName 'External'"} {
		if !strings.Contains(script, want) {
			t.Fatalf("the script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "Start-VM") || strings.Contains(script, "Remove-VM ") || strings.Contains(script, "Remove-Item") {
		t.Fatal("the script starts the VM or deletes something without being asked")
	}

	linux := vmMachine{Name: "web", Firmware: "bios", VTPM: true, GuestID: "ubuntu64Guest", MemoryMB: 1}
	plan, err = vmHyperVPlanFor(linux, nil, vmHyperVOptions{Folder: `D:\VMs`, Disks: []string{`D:\a.vhdx`}, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Generation != 1 || plan.TPM || plan.CPUs != 1 || plan.MemoryBytes != 32<<20 || !strings.Contains(plan.Script(), "-ControllerType IDE") || !strings.Contains(plan.Script(), "Start-VM") {
		t.Fatalf("BIOS plan %+v", plan)
	}
	linux.Firmware, linux.SecureBoot = "efi", true
	plan, _ = vmHyperVPlanFor(linux, nil, vmHyperVOptions{Folder: `D:\VMs`, Disks: []string{`D:\a.vhdx`}})
	if plan.SecureBootTemplate != "MicrosoftUEFICertificateAuthority" {
		t.Fatalf("Linux secure boot template %q", plan.SecureBootTemplate)
	}

	for name, options := range map[string]vmHyperVOptions{
		"no copied disks": {Folder: `D:\VMs`},
		"2 disks but 1":   {Folder: `D:\VMs`, Disks: []string{`D:\a.vhdx`}},
		"not a VHDX":      {Folder: `D:\VMs`, Disks: []string{`D:\a.vmdk`, `D:\b.vhdx`}},
	} {
		if _, err := vmHyperVPlanFor(vmMachine{Name: "x", Disks: []vmDisk{{}, {}}}, nil, options); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestHyperVScriptQuotesEveryValue(t *testing.T) {
	hostile := "x'; Remove-Item C:\\ -Recurse; '\u2019$(calc)"
	plan, err := vmHyperVPlanFor(vmMachine{Name: hostile, NICs: []vmNIC{{Network: "n"}}}, nil, vmHyperVOptions{Folder: hostile, Disks: []string{hostile + ".vhdx"}, Switches: map[string]string{"n": hostile}})
	if err != nil {
		t.Fatal(err)
	}
	quoted := vmPSQuote(hostile)
	if quoted != "'x''; Remove-Item C:\\ -Recurse; ''\u2019\u2019$(calc)'" {
		t.Fatalf("quoted %q", quoted)
	}
	script := plan.Script()
	if strings.Count(script, quoted) < 3 || !strings.Contains(script, vmPSQuote(hostile+".vhdx")) {
		t.Fatalf("not every value went through quoting:\n%s", script)
	}
	if strings.Contains(strings.ReplaceAll(strings.ReplaceAll(script, vmPSQuote(hostile+".vhdx"), ""), quoted, ""), "Remove-Item") {
		t.Fatal("a value escaped its quotes")
	}
}
