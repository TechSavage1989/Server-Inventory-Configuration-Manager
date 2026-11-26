package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

func TestMaskLicenseKeyKeepsOnlyFirstAndLastGroup(t *testing.T) {
	cases := map[string]string{
		"AB12C-DE34F-GH56I-JK78L-MN90P": "AB12C-*****-*****-*****-MN90P",
		"00000-00000-00000-00000-00000": "00000-*****-*****-*****-00000",
		"SHORT":                         "SHORT",
		"ABCDEFGHIJ":                    "*****FGHIJ",
	}
	for key, want := range cases {
		if got := vmMaskLicenseKey(key); got != want {
			t.Fatalf("vmMaskLicenseKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestLicenseFromReadsProductAndExpiry(t *testing.T) {
	expires := time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC)
	info := types.LicenseManagerLicenseInfo{LicenseKey: "AB12C-DE34F-GH56I-JK78L-MN90P", EditionKey: "esx.enterprisePlus.cpuPackageCoreLimited", Name: "VMware vSphere 8 Enterprise Plus", Total: 64, Used: 48, CostUnit: "cpuPackage:32core",
		Properties: []types.KeyAnyValue{{Key: "ProductName", Value: "VMware ESX Server"}, {Key: "ProductVersion", Value: "8.*"}, {Key: "expirationDate", Value: expires}}}
	item := vmLicenseFrom(info)
	if item.Key != "AB12C-*****-*****-*****-MN90P" || item.Product != "VMware ESX Server" || item.Version != "8.*" || item.Expires != "2027-03-31" || item.Used != 48 || item.Total != 64 {
		t.Fatalf("unexpected license: %+v", item)
	}
	if strings.Contains(item.Key, "DE34F") {
		t.Fatal("middle of the key leaked")
	}
}

func vmTestCertificate(t *testing.T, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "esx01.example.com"}, Issuer: pkix.Name{CommonName: "Example CA"}, NotBefore: notAfter.AddDate(-1, 0, 0), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestHostDetailsFromReadsHardwareNetworkAndStorage(t *testing.T) {
	ssd, local := true, false
	boot := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	host := mo.HostSystem{
		Summary: types.HostListSummary{OverallStatus: types.ManagedEntityStatusYellow, Hardware: &types.HostHardwareSummary{CpuMhz: 2000, NumCpuCores: 10, MemorySize: 64 << 30},
			QuickStats: types.HostListSummaryQuickStats{OverallCpuUsage: 5000, OverallMemoryUsage: 32 << 10}},
		Hardware: &types.HostHardwareInfo{SystemInfo: types.HostSystemInfo{Uuid: "4c4c4544-0000-0000-0000-000000000001", OtherIdentifyingInfo: []types.HostSystemIdentificationInfo{
			{IdentifierValue: "SVC1234", IdentifierType: &types.ElementDescription{Key: "ServiceTag"}},
			{IdentifierValue: "ASSET-42", IdentifierType: &types.ElementDescription{Key: "AssetTag"}},
		}}, BiosInfo: &types.HostBIOSInfo{Vendor: "Example BIOS Co", BiosVersion: "2.19.1", FirmwareType: "UEFI"}},
		Runtime:       types.HostRuntimeInfo{BootTime: &boot},
		ManagedEntity: mo.ManagedEntity{TriggeredAlarmState: []types.AlarmState{{Alarm: types.ManagedObjectReference{Type: "Alarm", Value: "alarm-7"}, OverallStatus: types.ManagedEntityStatusRed}}},
		Config: &types.HostConfigInfo{
			HyperThread:  &types.HostHyperThreadScheduleInfo{Available: true, Active: true},
			LockdownMode: types.HostLockdownModeLockdownNormal,
			Certificate:  vmTestCertificate(t, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)),
			DateTimeInfo: &types.HostDateTimeInfo{TimeZone: types.HostDateTimeSystemTimeZone{Key: "UTC", Name: "UTC"}, NtpConfig: &types.HostNtpConfig{Server: []string{"ntp1.example.com"}}},
			Service:      &types.HostServiceInfo{Service: []types.HostService{{Key: "ntpd", Label: "NTP Daemon", Running: true, Policy: "on"}, {Key: "TSM-SSH", Label: "SSH", Policy: "off"}}},
			VirtualNicManagerInfo: &types.HostVirtualNicManagerInfo{NetConfig: []types.VirtualNicManagerNetConfig{
				{NicType: "management", CandidateVnic: []types.HostVirtualNic{{Device: "vmk0", Key: "key-vmk0"}}, SelectedVnic: []string{"key-vmk0"}},
				{NicType: "vmotion", CandidateVnic: []types.HostVirtualNic{{Device: "vmk1", Key: "key-vmk1"}}, SelectedVnic: []string{"key-vmk1"}},
			}},
			Network: &types.HostNetworkInfo{
				Pnic:          []types.PhysicalNic{{Key: "key-vim.host.PhysicalNic-vmnic0", Device: "vmnic0", Driver: "i40en", DriverVersion: "2.1", Mac: "00:50:56:00:00:10", LinkSpeed: &types.PhysicalNicLinkInfo{SpeedMb: 10000, Duplex: true}}, {Key: "key-vim.host.PhysicalNic-vmnic1", Device: "vmnic1", Driver: "i40en"}},
				Vswitch:       []types.HostVirtualSwitch{{Name: "vSwitch0", Mtu: 9000, NumPorts: 128, Pnic: []string{"key-vim.host.PhysicalNic-vmnic0"}, Portgroup: []string{"key-vim.host.PortGroup-Management Network"}}},
				Vnic:          []types.HostVirtualNic{{Device: "vmk0", Portgroup: "Management Network", Spec: types.HostVirtualNicSpec{Ip: &types.HostIpConfig{IpAddress: "192.0.2.21", SubnetMask: "255.255.255.0"}, Mtu: 1500}}, {Device: "vmk1", Portgroup: "vMotion", Spec: types.HostVirtualNicSpec{Ip: &types.HostIpConfig{IpAddress: "10.10.0.21", SubnetMask: "255.255.255.0"}, Mtu: 9000}}},
				DnsConfig:     &types.HostDnsConfig{HostName: "esx01", DomainName: "example.com", Address: []string{"192.0.2.53"}},
				IpRouteConfig: &types.HostIpRouteConfig{DefaultGateway: "192.0.2.1"},
			},
			StorageDevice: &types.HostStorageDeviceInfo{
				HostBusAdapter: []types.BaseHostHostBusAdapter{
					&types.HostFibreChannelHba{HostHostBusAdapter: types.HostHostBusAdapter{Device: "vmhba2", Model: "Example FC HBA", Driver: "qlnativefc", Status: "online"}, PortWorldWideName: 0x2100001b32aabbcc},
					&types.HostInternetScsiHba{HostHostBusAdapter: types.HostHostBusAdapter{Device: "vmhba64", Model: "iSCSI Software Adapter", Driver: "iscsi_vmk", Status: "online"}, IsSoftwareBased: true, IScsiName: "iqn.1998-01.com.vmware:esx01"},
				},
				ScsiLun: []types.BaseScsiLun{&types.HostScsiDisk{ScsiLun: types.ScsiLun{CanonicalName: "naa.600a0b80001", DisplayName: "SAN LUN 1", LunType: "disk", Vendor: "EXAMPLE ", Model: "ARRAY ", OperationalState: []string{"ok"}}, Capacity: types.HostDiskDimensionsLba{BlockSize: 512, Block: 2 << 30}, Ssd: &ssd, LocalDisk: &local}},
			},
		},
	}
	details := vmHostDetailsFrom(host)
	if details.SerialNumber != "SVC1234" || details.AssetTag != "ASSET-42" || details.BIOSVersion != "2.19.1" || details.Firmware != "UEFI" || details.Hyperthreading != "Active" {
		t.Fatalf("hardware: %+v", details)
	}
	if details.CPUUsagePercent != 25 || details.MemUsagePercent != 50 || details.BootTime != "2026-10-01 08:00 UTC" || details.OverallStatus != "yellow" {
		t.Fatalf("usage: cpu %d mem %d boot %q status %q", details.CPUUsagePercent, details.MemUsagePercent, details.BootTime, details.OverallStatus)
	}
	if details.CertificateExpiry != "2026-12-01" || details.TimeZone != "UTC" || !details.NTPRunning || len(details.NTPServers) != 1 || details.LockdownMode != "lockdownNormal" {
		t.Fatalf("certificate/time: %+v", details)
	}
	if details.HostName != "esx01" || details.Domain != "example.com" || details.DefaultGateway != "192.0.2.1" || details.ManagementIP != "192.0.2.21" {
		t.Fatalf("dns/route: %+v", details)
	}
	if len(details.PhysicalNICs) != 2 || details.PhysicalNICs[0].Switch != "vSwitch0" || details.PhysicalNICs[0].SpeedMb != 10000 || details.PhysicalNICs[0].Driver != "i40en 2.1" || details.PhysicalNICs[1].SpeedMb != 0 {
		t.Fatalf("pnics: %+v", details.PhysicalNICs)
	}
	if len(details.Switches) != 1 || details.Switches[0].MTU != 9000 || details.Switches[0].Uplinks[0] != "vmnic0" || details.Switches[0].Portgroups[0] != "Management Network" {
		t.Fatalf("switches: %+v", details.Switches)
	}
	if len(details.VMkernel) != 2 || details.VMkernel[0].Services[0] != "management" || details.VMkernel[1].Services[0] != "vmotion" || details.VMkernel[1].MTU != 9000 {
		t.Fatalf("vmkernel: %+v", details.VMkernel)
	}
	if len(details.StorageAdapters) != 2 || details.StorageAdapters[0].Kind != "Fibre Channel" || details.StorageAdapters[0].Identifier != "WWPN 2100001b32aabbcc" || details.StorageAdapters[1].Kind != "iSCSI (software)" || details.StorageAdapters[1].Identifier != "iqn.1998-01.com.vmware:esx01" {
		t.Fatalf("adapters: %+v", details.StorageAdapters)
	}
	if len(details.LUNs) != 1 || details.LUNs[0].CapacityBytes != 1<<40 || !details.LUNs[0].SSD || details.LUNs[0].Local || details.LUNs[0].Vendor != "EXAMPLE" {
		t.Fatalf("luns: %+v", details.LUNs)
	}
	if len(details.Alarms) != 1 || details.Alarms[0] != "red|alarm-7" {
		t.Fatalf("alarms: %+v", details.Alarms)
	}
}

func TestDiscoveryWatchFlagsExpiryAlarmsAndState(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	inventory := &vmInventory{
		Licenses: []vmLicense{{Name: "vSphere Standard", Key: "AAAAA-*****-BBBBB", Expires: "2026-12-15"}, {Name: "vCenter Standard", Key: "CCCCC-*****-DDDDD", Expires: "2026-01-01"}, {Name: "Over", Key: "EEEEE-*****-FFFFF", Total: 4, Used: 6, Unit: "cpuPackage"}, {Name: "Fine", Expires: "2030-01-01"}},
		Hosts: []vmHost{
			{Name: "esx01", ConnectionState: "connected", Details: &vmHostDetails{CertificateExpiry: "2027-01-10", Alarms: []string{"Host memory usage (red)"}, ConfigIssues: []string{"SSH is enabled."}, Services: []vmHostService{{Key: "ntpd"}}}},
			{Name: "esx02", ConnectionState: "notResponding", Maintenance: true},
		},
	}
	notes := strings.Join(vmDiscoveryWatch(inventory, now), "\n")
	for _, want := range []string{"vSphere Standard (AAAAA-*****-BBBBB) expires on 2026-12-15", "vCenter Standard (CCCCC-*****-DDDDD) expired on 2026-01-01", "over-allocated: 6 of 4", "esx01's certificate expires on 2027-01-10", "Host memory usage (red)", "SSH is enabled.", "esx01 has no NTP server", "esx02 is notResponding", "esx02 is in maintenance mode"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("missing %q in:\n%s", want, notes)
		}
	}
	if strings.Contains(notes, "Fine") {
		t.Fatal("a license far from expiry was flagged")
	}
}

func TestCollectVSphereFullDiscoveryAgainstSimulator(t *testing.T) {
	model := simulator.VPX()
	model.Datacenter, model.Cluster, model.Host, model.Machine, model.Pool, model.App = 1, 1, 1, 1, 1, 1
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	password, _ := server.URL.User.Password()
	inventory, err := collectVSphere(context.Background(), vmConnectOptions{Server: server.URL.Host, User: server.URL.User.Username(), Password: password, insecureForTest: true, Timeout: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Build == "" || inventory.InstanceUUID == "" {
		t.Fatalf("vCenter details missing: build %q uuid %q", inventory.Build, inventory.InstanceUUID)
	}
	for _, host := range inventory.Hosts {
		d := host.Details
		if d == nil || d.UUID == "" || len(d.PhysicalNICs) == 0 || len(d.Switches) == 0 || len(d.VMkernel) == 0 || len(d.StorageAdapters) == 0 || len(d.LUNs) == 0 || len(d.Services) == 0 || len(d.DNSServers) == 0 || d.ManagementIP == "" {
			t.Fatalf("host %s discovery incomplete: %+v", host.Name, d)
		}
	}
	if len(inventory.Licenses) == 0 || !strings.Contains(inventory.Licenses[0].Key, "*****") {
		t.Fatalf("licenses: %+v", inventory.Licenses)
	}
	pools, apps := 0, 0
	for _, pool := range inventory.ResourcePools {
		if pool.Owner == "" {
			t.Fatalf("pool without owner: %+v", pool)
		}
		if pool.VApp {
			apps++
		} else {
			pools++
		}
	}
	if pools != 1 || apps != 1 {
		t.Fatalf("expected one pool and one vApp, got %+v", inventory.ResourcePools)
	}
	if len(inventory.Switches) == 0 {
		t.Fatal("distributed switch missing")
	}
	// vcsim has no image configuration manager, so the VIB list fails and
	// becomes one warning rather than an error.
	if !strings.Contains(strings.Join(inventory.Warnings, " "), "software packages (VIBs)") {
		t.Fatalf("warnings: %v", inventory.Warnings)
	}
	for _, datastore := range inventory.Datastores {
		if len(datastore.Hosts) == 0 || datastore.URL == "" {
			t.Fatalf("datastore %s has no mounting hosts or URL: %+v", datastore.Name, datastore)
		}
	}
	relations := vmRelationsFor(inventory)
	if len(relations.Unplaced) != 0 || len(relations.OtherDisks) != 0 {
		t.Fatalf("simulator VMs not linked: %d unplaced, %d disks elsewhere", len(relations.Unplaced), len(relations.OtherDisks))
	}
	linked, mounted := 0, 0
	for _, host := range relations.Hosts {
		linked += len(host.VMs)
		mounted += len(host.Datastores)
	}
	if mounted == 0 {
		t.Fatal("no host is linked to a datastore")
	}
	if linked != len(inventory.VMs) {
		t.Fatalf("%d of %d VMs linked to hosts", linked, len(inventory.VMs))
	}
	folder := t.TempDir()
	inventory.Hosts[0].Name = "<b>esx</b>"
	report, err := writeVMwareInventoryOutputs(folder, inventory)
	if err != nil {
		t.Fatal(err)
	}
	discovery := vmDiscoveryReportFor(report)
	if discovery == "" {
		t.Fatal("discovery report not written")
	}
	html, _ := os.ReadFile(discovery)
	page := string(html)
	if strings.Count(page, "<script") != 1 || !strings.Contains(page, "<script>"+vmReportScript+"</script>") || strings.Contains(page, "<b>esx</b>") || !strings.Contains(page, "&lt;b&gt;esx&lt;/b&gt;") || !strings.Contains(page, "vmnic0") || !strings.Contains(page, "Evaluation") {
		t.Fatal("discovery report is unsafe or incomplete")
	}
	for _, name := range []string{"Host-Physical-NICs.csv", "Host-Virtual-Switches.csv", "Host-VMkernel-Ports.csv", "Host-Storage-Adapters.csv", "Host-Storage-Devices.csv", "Host-Services.csv", "Host-Software-Packages.csv", "Licenses.csv", "Resource-Pools.csv", "Distributed-Switches.csv"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("%s missing", name)
		}
	}
	hosts, _ := os.ReadFile(filepath.Join(folder, "Hosts.csv"))
	if !strings.Contains(strings.SplitN(string(hosts), "\n", 2)[0], "SerialNumber") {
		t.Fatal("Hosts.csv has no discovery columns")
	}
}

func TestDiscoveryReportHandlesInventoryFromOlderVersions(t *testing.T) {
	inventory := vmSampleInventory()
	for i := range inventory.Hosts {
		inventory.Hosts[i].Details = nil
	}
	inventory.Licenses = nil
	report, err := writeVMwareInventoryOutputs(t.TempDir(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(vmDiscoveryReportFor(report))
	if !strings.Contains(string(html), "Licenses were not read") {
		t.Fatal("older inventory not explained")
	}
}

func TestRelationsLinkVMsToHostsDatastoresAndNetworks(t *testing.T) {
	inventory := vmSampleInventory()
	inventory.Hosts = append(inventory.Hosts, vmHost{Name: "esx02.example.com", Datacenter: "DC1", Cluster: "Cluster-A"}, vmHost{Name: "esx-solo.example.com", Datacenter: "DC1"})
	inventory.Datastores[0].Hosts = []string{"esx01.example.com", "esx02.example.com", "gone.example.com"}
	for i := range inventory.VMs {
		inventory.VMs[i].Host = "esx01.example.com"
	}
	inventory.VMs[1].Host = "esx02.example.com"
	inventory.VMs[2].Host = "missing.example.com"
	inventory.VMs[3].Disks = append(inventory.VMs[3].Disks, vmDisk{Label: "Hard disk 2", CapacityBytes: 1 << 30, Datastore: "local-esx09"})
	relations := vmRelationsFor(inventory)

	if len(relations.Clusters) != 2 || relations.Clusters[0].Name != "Cluster-A" || len(relations.Clusters[0].Hosts) != 2 || relations.Clusters[1].Name != "Standalone hosts" || len(relations.Clusters[1].Hosts) != 1 {
		t.Fatalf("clusters: %+v", relations.Clusters)
	}
	esx01, esx02 := relations.Hosts[0], relations.Hosts[1]
	if len(esx01.VMs) != 3 || len(esx02.VMs) != 1 || esx02.VMs[0].VM.Name != "SQL01" {
		t.Fatalf("host VMs: esx01 %d esx02 %d", len(esx01.VMs), len(esx02.VMs))
	}
	if esx01.VMs[0].VM.Name > esx01.VMs[1].VM.Name {
		t.Fatal("host VMs are not sorted by name")
	}
	if len(esx01.Datastores) != 1 || esx01.Datastores[0].Anchor != relations.Datastores[0].Anchor || len(relations.Hosts[2].Datastores) != 0 {
		t.Fatalf("mounted datastores: %+v", esx01.Datastores)
	}
	if len(relations.Unplaced) != 1 || relations.Unplaced[0].VM.Name != "WEB-LNX" {
		t.Fatalf("unplaced: %+v", relations.Unplaced)
	}
	datastore := relations.Datastores[0]
	if datastore.Hosts[2].Anchor != "" || datastore.Hosts[0].Anchor != esx01.Anchor {
		t.Fatalf("datastore hosts: %+v", datastore.Hosts)
	}
	disks := 0
	for _, vm := range inventory.VMs {
		for _, disk := range vm.Disks {
			if disk.Datastore == "DS-PROD-01" {
				disks++
			}
		}
	}
	if len(datastore.Disks) != disks || datastore.VMs == 0 || datastore.DiskBytes == 0 {
		t.Fatalf("datastore disks: %d of %d, %d VMs", len(datastore.Disks), disks, datastore.VMs)
	}
	// SQL01's physical RDM has no datastore; OLD-NT's second disk is on a
	// datastore that is not in the inventory.
	if len(relations.OtherDisks) != 2 || relations.OtherDisks[0].Disk.RDM == "" || relations.OtherDisks[1].Disk.Datastore != "local-esx09" || relations.OtherDisks[1].DatastoreAnchor != "" {
		t.Fatalf("other disks: %+v", relations.OtherDisks)
	}
	servers := relations.Networks[1]
	if servers.Network.Name != "Servers-120" || len(servers.Adapters) != 1 || servers.Adapters[0].VM != "APP01" || servers.VMs != 1 {
		t.Fatalf("network: %+v", servers)
	}
	app := relations.VMs[0]
	if app.HostAnchor != esx01.Anchor || app.NICs[0].NetworkAnchor != servers.Anchor || app.Disks[0].DatastoreAnchor != datastore.Anchor {
		t.Fatalf("VM links: %+v", app)
	}

	report, err := writeVMwareInventoryOutputs(t.TempDir(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(vmDiscoveryReportFor(report))
	page := string(html)
	for _, want := range []string{`id="` + esx01.Anchor + `"`, `href="#` + datastore.Anchor + `"`, `href="#` + app.Anchor + `"`, `id="` + app.Anchor + `"`, "Virtual machines on this host (3)", "VM disks on this datastore", "Disks not on a datastore in this inventory", "local-esx09", "Networks and their VMs"} {
		if !strings.Contains(page, want) {
			t.Fatalf("discovery report is missing %q", want)
		}
	}
}
