package main

// A guest capture made from outside the VM. VMware Tools reports each
// adapter's MAC address, addresses, whether DHCP is on, its default gateways
// and DNS servers to vCenter, so the settings a move must put back are known
// without running anything inside the old VM. The capture has the same shape
// as the one Migrate-VmGuest.ps1 records inside Windows (Schema 1), so the
// guest engine's Status and ApplyNetwork use it unchanged after the cutover.

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// guestCaptureSourceVCenter marks a capture built from the vCenter inventory.
// The guest engine prefers a capture recorded inside Windows, which also
// holds services, disks and activation.
const guestCaptureSourceVCenter = "vCenter"

var guestTargetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()\-]{0,79}$`)

type guestCaptureAddress struct {
	Address      string `json:"Address"`
	PrefixLength int    `json:"PrefixLength"`
	Family       string `json:"Family"`
	Origin       string `json:"Origin"`
}

type guestCaptureGateway struct {
	NextHop string `json:"NextHop"`
	Metric  int    `json:"Metric"`
	Family  string `json:"Family"`
}

type guestCaptureAdapter struct {
	Name           string                `json:"Name"`
	Description    string                `json:"Description"`
	MacAddress     string                `json:"MacAddress"`
	InterfaceIndex int                   `json:"InterfaceIndex"`
	Status         string                `json:"Status"`
	Dhcp           bool                  `json:"Dhcp"`
	Static         bool                  `json:"Static"`
	Addresses      []guestCaptureAddress `json:"Addresses"`
	Gateways       []guestCaptureGateway `json:"Gateways"`
	DnsServers     []string              `json:"DnsServers"`
	DnsSuffix      string                `json:"DnsSuffix"`
	RegisterInDns  bool                  `json:"RegisterInDns"`
	UseSuffixInDns bool                  `json:"UseSuffixInDns"`
	WinsPrimary    string                `json:"WinsPrimary"`
	WinsSecondary  string                `json:"WinsSecondary"`
	NetbiosOption  int                   `json:"NetbiosOption"`
	VlanId         string                `json:"VlanId"`
	JumboPacket    string                `json:"JumboPacket"`
}

type guestCaptureFinding struct {
	Severity string `json:"Severity"`
	Message  string `json:"Message"`
}

type guestCapture struct {
	Schema        int                   `json:"Schema"`
	EngineVersion int                   `json:"EngineVersion"`
	Source        string                `json:"Source"`
	CapturedAt    string                `json:"CapturedAt"`
	VMName        string                `json:"VMName"`
	State         guestCaptureState     `json:"State"`
	Findings      []guestCaptureFinding `json:"Findings"`
	TargetChecks  []any                 `json:"TargetChecks"`
}

type guestCaptureState struct {
	ComputerName        string                `json:"ComputerName"`
	Domain              string                `json:"Domain"`
	OperatingSystem     string                `json:"OperatingSystem"`
	Platform            map[string]string     `json:"Platform"`
	Firmware            string                `json:"Firmware"`
	SecureBoot          bool                  `json:"SecureBoot"`
	Tpm                 bool                  `json:"Tpm"`
	BitLockerOn         []string              `json:"BitLockerOn"`
	Adapters            []guestCaptureAdapter `json:"Adapters"`
	PersistentRoutes    []any                 `json:"PersistentRoutes"`
	Disks               []any                 `json:"Disks"`
	RunningAutoServices []string              `json:"RunningAutoServices"`
}

// guestMACFromVMware turns 00:50:56:aa:bb:cc into 00-50-56-AA-BB-CC, the
// form Windows and the guest engine use.
func guestMACFromVMware(mac string) string {
	hardware, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil || len(hardware) != 6 {
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(hardware.String(), ":", "-"))
}

// vmGuestCaptureFromInventory builds a capture for one VM from what VMware
// Tools reported to vCenter. It fails when Tools reported no adapter
// settings, so the operator knows to record them another way.
func vmGuestCaptureFromInventory(vm vmMachine, now time.Time) (*guestCapture, error) {
	if vm.Source == vmSourceHyperV {
		return nil, errors.New("this VM is on Hyper-V; record its settings with PowerShell Direct or inside the VM")
	}
	computer := strings.ToUpper(strings.SplitN(strings.TrimSpace(vm.GuestHostName), ".", 2)[0])
	domain := ""
	if parts := strings.SplitN(strings.TrimSpace(vm.GuestHostName), ".", 2); len(parts) == 2 {
		domain = parts[1]
	}
	capture := &guestCapture{
		Schema: 1, EngineVersion: 1, Source: guestCaptureSourceVCenter, CapturedAt: now.UTC().Format(time.RFC3339), VMName: vm.Name,
		State: guestCaptureState{
			ComputerName: vmNonEmpty(computer, strings.ToUpper(vm.Name)), Domain: domain, OperatingSystem: vmNonEmpty(vm.GuestOSRunning, vm.GuestOS),
			Platform: map[string]string{"Name": "VMware"}, SecureBoot: vm.SecureBoot, Tpm: vm.VTPM,
			BitLockerOn: []string{}, Adapters: []guestCaptureAdapter{}, PersistentRoutes: []any{}, Disks: []any{}, RunningAutoServices: []string{},
		},
		Findings: []guestCaptureFinding{}, TargetChecks: []any{},
	}
	switch strings.ToLower(vm.Firmware) {
	case "efi":
		capture.State.Firmware = "UEFI"
	case "bios":
		capture.State.Firmware = "BIOS"
	default:
		capture.State.Firmware = "Unknown"
	}
	known, static := 0, 0
	for index, nic := range vm.NICs {
		if !nic.GuestKnown {
			continue
		}
		known++
		mac := guestMACFromVMware(vmNonEmpty(nic.MACInGuest, nic.MAC))
		if mac == "" {
			continue
		}
		adapter := guestCaptureAdapter{
			Name: vmNonEmpty(nic.Label, "Network adapter "+strconv.Itoa(index+1)), Description: vmNonEmpty(nic.AdapterType, "VMware adapter"),
			MacAddress: mac, Status: "Up", Dhcp: nic.DHCP, RegisterInDns: true,
			Addresses: []guestCaptureAddress{}, Gateways: []guestCaptureGateway{}, DnsServers: []string{}, DnsSuffix: nic.DNSSuffix,
		}
		for _, text := range nic.StaticAddresses {
			ip, network, err := net.ParseCIDR(text)
			if err != nil {
				continue
			}
			prefix, _ := network.Mask.Size()
			family := "IPv4"
			if ip.To4() == nil {
				family = "IPv6"
			}
			adapter.Addresses = append(adapter.Addresses, guestCaptureAddress{Address: ip.String(), PrefixLength: prefix, Family: family, Origin: "Manual"})
		}
		for _, hop := range nic.Gateways {
			ip := net.ParseIP(hop)
			if ip == nil {
				continue
			}
			family := "IPv4"
			if ip.To4() == nil {
				family = "IPv6"
			}
			adapter.Gateways = append(adapter.Gateways, guestCaptureGateway{NextHop: ip.String(), Metric: 256, Family: family})
		}
		for _, server := range nic.DNSServers {
			if net.ParseIP(server) != nil {
				adapter.DnsServers = append(adapter.DnsServers, server)
			}
		}
		adapter.Static = len(adapter.Addresses) > 0
		if adapter.Static {
			static++
		}
		capture.State.Adapters = append(capture.State.Adapters, adapter)
	}
	if known == 0 {
		return nil, errors.New("VMware Tools did not report the network settings of " + vm.Name + " (Tools not running, or the inventory was read before this version). Start the VM with VMware Tools running and read vCenter again, or record the settings over the network or inside the VM")
	}
	if static > 0 {
		capture.Findings = append(capture.Findings, guestCaptureFinding{Severity: "info", Message: "Recorded static IP settings for " + strconv.Itoa(static) + " adapter(s) from vCenter (VMware Tools). After cutover, use Put back IP settings on the new adapters."})
	} else {
		capture.Findings = append(capture.Findings, guestCaptureFinding{Severity: "info", Message: "VMware Tools reports no static IP settings: every adapter uses DHCP, so nothing needs putting back."})
	}
	capture.Findings = append(capture.Findings, guestCaptureFinding{Severity: "info", Message: "Recorded from vCenter without signing in to the VM. Services, disks, BitLocker and activation are not in this recording; the fix-up reads them on the new VM."})
	if vm.VTPM {
		capture.Findings = append(capture.Findings, guestCaptureFinding{Severity: "warning", Message: "The VM has a virtual TPM. If BitLocker is on inside it, save the recovery keys and suspend BitLocker before the cutover, or have the keys from Active Directory or Intune ready."})
	}
	return capture, nil
}

// guestTarget says where the guest engine runs: on this computer, or in
// another Windows computer reached through PowerShell remoting, so nothing is
// copied into or installed in it.
//
//	local      this computer (the original behaviour)
//	direct     a VM on this Hyper-V host, through PowerShell Direct (no network)
//	hostdirect a VM on the remote Hyper-V host or cluster node this console is
//	           connected to (Move VMs), PowerShell Direct run on that host
//	winrm      a computer over the network (WinRM), for example the VMware VM
//	           before the move
type guestTarget struct {
	Mode     string `json:"mode"`
	VM       string `json:"vm"`
	Computer string `json:"computer"`
	Host     string `json:"host"`
	User     string `json:"user"`
	password string
}

var guestTargetModes = map[string]string{
	"local":      "This computer",
	"direct":     "A VM on this Hyper-V host (PowerShell Direct)",
	"hostdirect": "A VM on the connected Hyper-V host (PowerShell Direct)",
	"winrm":      "A computer over the network (PowerShell remoting)",
}

var guestComputerPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-.]{0,252}[A-Za-z0-9])?$`)

// validateGuestTarget checks what the page sends before anything runs.
func validateGuestTarget(target guestTarget) (guestTarget, error) {
	target.Mode = strings.TrimSpace(target.Mode)
	target.VM, target.Computer, target.Host, target.User = strings.TrimSpace(target.VM), strings.TrimSpace(target.Computer), strings.TrimSpace(target.Host), strings.TrimSpace(target.User)
	if _, ok := guestTargetModes[target.Mode]; !ok {
		return target, errors.New("Choose where to run the checks.")
	}
	if len(target.User) > 256 || strings.ContainsAny(target.User, "\r\n\x00") || len(target.password) > 1024 {
		return target, errors.New("The account name or password is not valid.")
	}
	switch target.Mode {
	case "local":
		return guestTarget{Mode: "local"}, nil
	case "direct", "hostdirect":
		if !guestTargetNamePattern.MatchString(target.VM) {
			return target, errors.New("Type the Hyper-V VM's name.")
		}
		if target.User == "" || target.password == "" {
			return target, errors.New("PowerShell Direct needs an administrator account of the VM (for example .\\Administrator or DOMAIN\\admin) and its password.")
		}
		if target.Mode == "hostdirect" && !guestComputerPattern.MatchString(target.Host) {
			return target, errors.New("Choose the Hyper-V host that runs the VM.")
		}
		target.Computer = ""
	case "winrm":
		if !guestComputerPattern.MatchString(target.Computer) {
			return target, errors.New("Type the computer's DNS name or IP address.")
		}
		if (target.User == "") != (target.password == "") {
			return target, errors.New("Type both the account and its password, or leave both empty to use your own account.")
		}
		// VM is only the VMware VM's name for keeping the recording under
		// it; the connection uses Computer.
		if target.VM != "" && !guestTargetNamePattern.MatchString(target.VM) {
			target.VM = ""
		}
		target.Host = ""
	}
	return target, nil
}

// guestTargetLabel names the target in messages and confirmations.
func guestTargetLabel(target guestTarget, here string) string {
	switch target.Mode {
	case "direct", "hostdirect":
		return target.VM
	case "winrm":
		return target.Computer
	}
	return here
}

// guestCaptureKey names the console's own folder for one target's capture.
func guestCaptureKey(name string) string {
	name = strings.TrimSpace(name)
	if net.ParseIP(name) == nil {
		name = strings.SplitN(name, ".", 2)[0]
	}
	return vmFileName(strings.ToUpper(name))
}
