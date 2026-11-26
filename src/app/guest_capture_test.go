package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func guestTestVM() vmMachine {
	return vmMachine{
		Name: "APP01", GuestHostName: "app01.corp.example.com", GuestOSRunning: "Microsoft Windows Server 2022 (64-bit)", Firmware: "efi", VTPM: true,
		NICs: []vmNIC{
			{Label: "Network adapter 1", AdapterType: "vmxnet3", MAC: "00:50:56:aa:bb:01", GuestKnown: true, MACInGuest: "00:50:56:aa:bb:01",
				StaticAddresses: []string{"192.0.2.10/24", "2001:db8::10/64"}, Gateways: []string{"192.0.2.1"}, DNSServers: []string{"192.0.2.53", "not-an-ip"}, DNSSuffix: "corp.example.com"},
			{Label: "Network adapter 2", AdapterType: "vmxnet3", MAC: "00:50:56:aa:bb:02", GuestKnown: true, DHCP: true},
			{Label: "Network adapter 3", MAC: "00:50:56:aa:bb:03"},
		},
	}
}

// A VMware VM's IP settings are recorded from what VMware Tools reported to
// vCenter, in the guest engine's own capture shape, without running anything
// inside the VM.
func TestGuestCaptureFromInventoryNeedsNothingInTheVM(t *testing.T) {
	capture, err := vmGuestCaptureFromInventory(guestTestVM(), time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if capture.Schema != 1 || capture.Source != guestCaptureSourceVCenter || capture.State.ComputerName != "APP01" || capture.State.Domain != "corp.example.com" || capture.State.Firmware != "UEFI" || !capture.State.Tpm {
		t.Fatalf("capture header = %+v", capture)
	}
	if len(capture.State.Adapters) != 2 {
		t.Fatalf("adapters = %d, want the two Tools reported", len(capture.State.Adapters))
	}
	first, second := capture.State.Adapters[0], capture.State.Adapters[1]
	if first.MacAddress != "00-50-56-AA-BB-01" || !first.Static || first.Dhcp || len(first.Addresses) != 2 {
		t.Fatalf("first adapter = %+v", first)
	}
	if first.Addresses[0].Address != "192.0.2.10" || first.Addresses[0].PrefixLength != 24 || first.Addresses[0].Origin != "Manual" || first.Addresses[1].Family != "IPv6" {
		t.Fatalf("addresses = %+v", first.Addresses)
	}
	if len(first.Gateways) != 1 || first.Gateways[0].NextHop != "192.0.2.1" || len(first.DnsServers) != 1 || first.DnsSuffix != "corp.example.com" || !first.RegisterInDns {
		t.Fatalf("gateway and DNS = %+v", first)
	}
	if second.Static || !second.Dhcp || len(second.Addresses) != 0 {
		t.Fatalf("DHCP adapter = %+v", second)
	}
	// The engine reads these exact property names (Migrate-VmGuest.ps1).
	raw, _ := json.Marshal(capture)
	for _, name := range []string{`"State"`, `"Adapters"`, `"MacAddress"`, `"Static"`, `"Origin":"Manual"`, `"Gateways"`, `"NextHop"`, `"DnsServers"`, `"RunningAutoServices":[]`, `"CapturedAt"`} {
		if !strings.Contains(string(raw), name) {
			t.Errorf("capture JSON lacks %s", name)
		}
	}
	engine, err := os.ReadFile("Migrate-VmGuest.ps1")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"$capture.State.Adapters", "$_.MacAddress", "$_.Static", "$_.Origin -eq 'Manual'", "$Config.Gateways", "$Config.DnsServers"} {
		if !strings.Contains(string(engine), name) {
			t.Errorf("the guest engine no longer reads %s", name)
		}
	}
}

func TestGuestCaptureFromInventoryRefusesWithoutTools(t *testing.T) {
	vm := guestTestVM()
	for index := range vm.NICs {
		vm.NICs[index].GuestKnown = false
	}
	if _, err := vmGuestCaptureFromInventory(vm, time.Now()); err == nil || !strings.Contains(err.Error(), "VMware Tools") {
		t.Fatalf("err = %v", err)
	}
	vm = guestTestVM()
	vm.Source = vmSourceHyperV
	if _, err := vmGuestCaptureFromInventory(vm, time.Now()); err == nil {
		t.Fatal("a Hyper-V VM was recorded from vCenter")
	}
}

func TestGuestTargetValidation(t *testing.T) {
	cases := []struct {
		target guestTarget
		ok     bool
	}{
		{guestTarget{Mode: "local", VM: "x", password: "y"}, true},
		{guestTarget{Mode: "direct", VM: "APP01", User: `.\Administrator`, password: "p"}, true},
		{guestTarget{Mode: "direct", VM: "APP01"}, false},
		{guestTarget{Mode: "direct", VM: "bad;name", User: "a", password: "p"}, false},
		{guestTarget{Mode: "hostdirect", VM: "APP01", Host: "hv-node1", User: "a", password: "p"}, true},
		{guestTarget{Mode: "hostdirect", VM: "APP01", User: "a", password: "p"}, false},
		{guestTarget{Mode: "winrm", Computer: "app01.corp.example.com"}, true},
		{guestTarget{Mode: "winrm", Computer: "192.0.2.10", User: "a", password: "p", VM: "APP01"}, true},
		{guestTarget{Mode: "winrm", Computer: "app01", User: "a"}, false},
		{guestTarget{Mode: "winrm", Computer: "app01; rm"}, false},
		{guestTarget{Mode: "winrm", Computer: "app01", User: "a\nb", password: "p"}, false},
		{guestTarget{Mode: "vmware-tools", VM: "APP01"}, false},
	}
	for _, c := range cases {
		got, err := validateGuestTarget(c.target)
		if (err == nil) != c.ok {
			t.Errorf("%+v: err = %v", c.target, err)
		}
		if err == nil && got.Mode == "local" && (got.VM != "" || got.password != "") {
			t.Errorf("local target kept %+v", got)
		}
	}
	if key := guestCaptureKey("192.0.2.10"); key != "192.0.2.10" {
		t.Errorf("IP key = %q", key)
	}
	if key := guestCaptureKey("app01.corp.example.com"); key != "APP01" {
		t.Errorf("DNS key = %q", key)
	}
}

// The remote runner reaches the guest only through Invoke-Command, never a
// service, scheduled task, WMI process, CredSSP or encoded command, and never
// with the Bypass execution policy (endpoint protection, AGENTS rule 35).
func TestGuestRemoteScriptUsesOnlyPowerShellRemoting(t *testing.T) {
	raw, err := os.ReadFile("Invoke-GuestRemote.ps1")
	if err != nil {
		t.Fatal(err)
	}
	code := string(raw)
	for _, word := range []string{"New-Service", "sc.exe", "Register-ScheduledTask", "schtasks", "Win32_Process", "Start-Process", "CredSSP", "EncodedCommand", "Bypass", "Copy-Item", "TrustedHosts -Value", "Set-Item WSMan"} {
		if strings.Contains(code, word) {
			t.Errorf("Invoke-GuestRemote.ps1 uses %s", word)
		}
	}
	for _, word := range []string{"Invoke-Command -VMName", "Invoke-Command -ComputerName", "-Embedded", "[Console]::In.ReadLine()"} {
		if !strings.Contains(code, word) {
			t.Errorf("Invoke-GuestRemote.ps1 lacks %s", word)
		}
	}
	windows, err := os.ReadFile("guest_remote_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(windows), `"Bypass"`) || !strings.Contains(string(windows), `"RemoteSigned"`) {
		t.Error("the remote guest runner must run with RemoteSigned, never Bypass")
	}
	engine, _ := os.ReadFile("Migrate-VmGuest.ps1")
	if !strings.Contains(string(engine), "if (-not $Embedded) { exit $exitCode }") {
		t.Error("the guest engine must not call exit when it runs embedded")
	}
}

// A Windows 10 VM with a virtual TPM has its own files encrypted but plain
// disks, which copy as they are: it must not be Blocked (field report
// 2026-11-26). Encrypted disks stay Blocked with the ways out, and an older
// inventory that does not say which disks are encrypted stays Blocked.
func TestReadinessSeparatesVTPMHomeEncryptionFromEncryptedDisks(t *testing.T) {
	base := vmMachine{Name: "DESK21-CPA01", PowerState: "poweredOn", GuestID: "windows9_64Guest", GuestOS: "Microsoft Windows 10 (64-bit)", CPUs: 2, MemoryMB: 8192, HardwareVersion: "vmx-19", Firmware: "efi", SecureBoot: true, VTPM: true, Encrypted: true, EncryptionRead: true, ToolsStatus: "guestToolsCurrent", ToolsRunning: true,
		Disks: []vmDisk{{Label: "Hard disk 1", CapacityBytes: 100 << 30, Provisioning: "thin", BootCandidate: true}}}
	codes := func(vm vmMachine) (vmReadiness, map[string]string) {
		r := assessVM(vm, vmTargetHyperV, time.Now())
		found := map[string]string{}
		for _, f := range r.Findings {
			found[f.Code] = f.Severity + ": " + f.Message + " " + f.Action
		}
		return r, found
	}
	r, found := codes(base)
	if r.Status == vmStatusBlocked || found["encrypted"] != "" || found["encrypted-home"] != "" {
		t.Fatalf("vTPM-only VM: status %s, findings %v", r.Status, found)
	}
	if !strings.Contains(found["vtpm"], "disks are not") || !strings.Contains(found["vtpm"], "BitLocker") {
		t.Fatalf("vtpm finding = %q", found["vtpm"])
	}
	noTPM := base
	noTPM.VTPM = false
	if _, found := codes(noTPM); !strings.HasPrefix(found["encrypted-home"], vmSeverityWarning) {
		t.Fatalf("home-only encryption without a vTPM = %v", found)
	}
	locked := base
	locked.Disks = []vmDisk{{Label: "Hard disk 1", CapacityBytes: 100 << 30, BootCandidate: true, Encrypted: true}}
	r, found = codes(locked)
	if r.Status != vmStatusBlocked || !strings.Contains(found["encrypted"], "Replicate servers") || !strings.Contains(found["encrypted"], "1 of the VM's 1 disks") {
		t.Fatalf("encrypted disk: status %s, %q", r.Status, found["encrypted"])
	}
	old := base
	old.EncryptionRead = false
	if r, found := codes(old); r.Status != vmStatusBlocked || !strings.Contains(found["encrypted"], "Read vCenter again") {
		t.Fatalf("older inventory: status %s, %q", r.Status, found["encrypted"])
	}
	plan, err := vmHyperVPlanFor(base, nil, vmHyperVOptions{Folder: `C:\ClusterStorage\Volume1\VMs`, Disks: []string{`C:\ClusterStorage\Volume1\VMs\DESK21-CPA01.vhdx`}, ClusterRole: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.TPM || !strings.Contains(strings.Join(plan.Warnings, " "), "Shielded VM local certificates") || !strings.Contains(plan.Script(), "Enable-VMTPM") {
		t.Fatalf("Hyper-V plan TPM = %v, warnings %v", plan.TPM, plan.Warnings)
	}
}
