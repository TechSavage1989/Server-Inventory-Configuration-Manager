package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The discovery report's files: the interactive report (vmreport_html.go)
// opening at the hosts, and a CSV of every host detail table. The report
// itself holds everything read about the VMware environment (vCenter,
// licenses, clusters, every host in full, distributed switches, resource
// pools) and how VMs, hosts, datastores and networks relate
// (vmware_relations.go).

const vmDiscoveryReport = "VMware-Discovery-Report.html"

// writeVMwareDiscoveryOutputs writes the discovery report and its CSV files.
func writeVMwareDiscoveryOutputs(folder string, inventory *vmInventory, model *vmReportModel) (string, error) {
	page, err := vmReportHTML(model, "hosts")
	if err != nil {
		return "", err
	}
	nicRows, switchRows, vmkRows, adapterRows, lunRows, serviceRows, packageRows := [][]string{}, [][]string{}, [][]string{}, [][]string{}, [][]string{}, [][]string{}, [][]string{}
	for _, host := range inventory.Hosts {
		details := host.Details
		if details == nil {
			continue
		}
		for _, nic := range details.PhysicalNICs {
			nicRows = append(nicRows, []string{host.Name, nic.Device, strconv.Itoa(nic.SpeedMb), strconv.FormatBool(nic.Duplex), nic.MAC, nic.Driver, nic.Firmware, nic.PCI, nic.Switch})
		}
		for _, item := range details.Switches {
			switchRows = append(switchRows, []string{host.Name, item.Name, item.Kind, strconv.Itoa(item.MTU), strconv.Itoa(item.Ports), strings.Join(item.Uplinks, "; "), strings.Join(item.Portgroups, "; ")})
		}
		for _, item := range details.VMkernel {
			vmkRows = append(vmkRows, []string{host.Name, item.Device, item.Portgroup, item.IP, item.Mask, strconv.FormatBool(item.DHCP), item.MAC, strconv.Itoa(item.MTU), item.Stack, strings.Join(item.Services, "; ")})
		}
		for _, item := range details.StorageAdapters {
			adapterRows = append(adapterRows, []string{host.Name, item.Device, item.Kind, item.Model, item.Driver, item.Identifier, item.Status, item.PCI})
		}
		for _, item := range details.LUNs {
			lunRows = append(lunRows, []string{host.Name, item.CanonicalName, item.DisplayName, item.Type, item.Vendor, item.Model, gib(item.CapacityBytes), strconv.FormatBool(item.SSD), strconv.FormatBool(item.Local), item.State})
		}
		for _, item := range details.Services {
			serviceRows = append(serviceRows, []string{host.Name, item.Key, item.Label, strconv.FormatBool(item.Running), item.Policy})
		}
		for _, item := range details.Packages {
			packageRows = append(packageRows, []string{host.Name, item.Name, item.Version, item.Vendor, item.Acceptance, item.Created})
		}
	}
	licenseRows, poolRows, dvsRows := [][]string{}, [][]string{}, [][]string{}
	for _, item := range inventory.Licenses {
		licenseRows = append(licenseRows, []string{item.Name, item.Edition, item.Key, item.Product, item.Version, strconv.Itoa(item.Used), strconv.Itoa(item.Total), item.Unit, item.Expires, strings.Join(item.AssignedTo, "; ")})
	}
	for _, item := range inventory.ResourcePools {
		poolRows = append(poolRows, []string{item.Name, strconv.FormatBool(item.VApp), item.Datacenter, item.Owner, item.Parent, strconv.FormatInt(item.CPUReservationMHz, 10), strconv.FormatInt(item.CPULimitMHz, 10), strconv.FormatInt(item.MemReservationMB, 10), strconv.FormatInt(item.MemLimitMB, 10), strconv.Itoa(item.VMCount)})
	}
	for _, item := range inventory.Switches {
		dvsRows = append(dvsRows, []string{item.Name, item.Datacenter, item.Version, strconv.Itoa(item.MTU), strconv.Itoa(item.Uplinks), item.Discovery, strings.Join(item.Hosts, "; ")})
	}
	files := []struct {
		name   string
		header []string
		rows   [][]string
	}{
		{"Host-Physical-NICs.csv", []string{"Host", "Device", "SpeedMb", "FullDuplex", "MAC", "Driver", "Firmware", "PCI", "Switch"}, nicRows},
		{"Host-Virtual-Switches.csv", []string{"Host", "Switch", "Kind", "MTU", "Ports", "Uplinks", "PortGroups"}, switchRows},
		{"Host-VMkernel-Ports.csv", []string{"Host", "Device", "PortGroup", "IPAddress", "SubnetMask", "DHCP", "MAC", "MTU", "TcpIpStack", "Services"}, vmkRows},
		{"Host-Storage-Adapters.csv", []string{"Host", "Device", "Kind", "Model", "Driver", "Identifier", "Status", "PCI"}, adapterRows},
		{"Host-Storage-Devices.csv", []string{"Host", "CanonicalName", "DisplayName", "Type", "Vendor", "Model", "CapacityGB", "SSD", "Local", "State"}, lunRows},
		{"Host-Services.csv", []string{"Host", "Key", "Service", "Running", "StartPolicy"}, serviceRows},
		{"Host-Software-Packages.csv", []string{"Host", "Package", "Version", "Vendor", "AcceptanceLevel", "Created"}, packageRows},
		{"Licenses.csv", []string{"License", "Edition", "Key", "Product", "Version", "Used", "Total", "Unit", "Expires", "AssignedTo"}, licenseRows},
		{"Resource-Pools.csv", []string{"Pool", "vApp", "Datacenter", "ClusterOrHost", "ParentPool", "CPUReservationMHz", "CPULimitMHz", "MemoryReservationMB", "MemoryLimitMB", "VMs"}, poolRows},
		{"Distributed-Switches.csv", []string{"Switch", "Datacenter", "Version", "MTU", "Uplinks", "DiscoveryProtocol", "Hosts"}, dvsRows},
	}
	for _, file := range files {
		if err := writeCSVFile(filepath.Join(folder, file.name), file.header, file.rows); err != nil {
			return "", err
		}
	}
	report := filepath.Join(folder, vmDiscoveryReport)
	return report, os.WriteFile(report, page, 0600)
}

// vmDiscoveryReportFor returns the discovery report written beside a
// readiness report, if there is one.
func vmDiscoveryReportFor(readinessReport string) string {
	if readinessReport == "" {
		return ""
	}
	path := filepath.Join(filepath.Dir(readinessReport), vmDiscoveryReport)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}

// vmReportBeside returns a report file written beside the readiness
// report, or "" when there is none (inventories read by older versions).
func vmReportBeside(readinessReport, name string) string {
	if readinessReport == "" {
		return ""
	}
	path := filepath.Join(filepath.Dir(readinessReport), name)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}

// vmHostCSVColumns are the discovery columns added to Hosts.csv.
var vmHostCSVColumns = []string{"SerialNumber", "AssetTag", "SystemUUID", "BIOS", "BIOSDate", "Hyperthreading", "BootTime", "OverallStatus", "LockdownMode", "ManagementIP", "DNSName", "DNSServers", "DefaultGateway", "NTPServers", "TimeZone", "CertificateExpires", "AcceptanceLevel", "License", "LicenseKey", "PhysicalNICs", "VMkernelPorts", "StorageAdapters", "Alarms"}

func vmHostCSVValues(host vmHost) []string {
	license, key := "", ""
	if host.License != nil {
		license, key = host.License.Name, host.License.Key
	}
	details := host.Details
	if details == nil {
		values := make([]string, len(vmHostCSVColumns))
		values[17], values[18] = license, key
		return values
	}
	dnsName := details.HostName
	if details.Domain != "" {
		dnsName += "." + details.Domain
	}
	return []string{details.SerialNumber, details.AssetTag, details.UUID, strings.TrimSpace(details.BIOSVendor + " " + details.BIOSVersion), details.BIOSDate, details.Hyperthreading, details.BootTime, details.OverallStatus, details.LockdownMode, details.ManagementIP, dnsName, strings.Join(details.DNSServers, "; "), details.DefaultGateway, strings.Join(details.NTPServers, "; "), details.TimeZone, details.CertificateExpiry, details.AcceptanceLevel, license, key, strconv.Itoa(len(details.PhysicalNICs)), strconv.Itoa(len(details.VMkernel)), strconv.Itoa(len(details.StorageAdapters)), strings.Join(details.Alarms, "; ")}
}
