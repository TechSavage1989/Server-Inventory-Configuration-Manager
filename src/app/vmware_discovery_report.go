package main

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The discovery report: everything read about the VMware environment
// itself (vCenter, licenses, clusters, every host in full, distributed
// switches, resource pools) and how VMs, hosts, datastores and networks
// relate (vmware_relations.go), next to the readiness report that covers the
// VMs. Self-contained HTML without scripts; packages fold with <details>.

const vmDiscoveryReport = "VMware-Discovery-Report.html"

var vmDiscoveryFunctions = template.FuncMap{
	"bytes": vmFormatBytes,
	"join":  strings.Join,
	"date":  vmReportDate,
	"limit": func(value int64, unit string) string {
		if value < 0 {
			return "Unlimited"
		}
		return strconv.FormatInt(value, 10) + " " + unit
	},
	"speed": func(mb int) string {
		switch {
		case mb <= 0:
			return "Down"
		case mb%1000 == 0:
			return strconv.Itoa(mb/1000) + " Gb/s"
		default:
			return strconv.Itoa(mb) + " Mb/s"
		}
	},
	"mb": func(value int64) int64 { return value * 1048576 },
	"percent": func(part, whole int64) string {
		if whole <= 0 {
			return ""
		}
		return strconv.FormatInt(part*100/whole, 10) + "% free"
	},
	"power": func(state string) string {
		switch state {
		case "poweredOn":
			return "On"
		case "poweredOff":
			return "Off"
		case "suspended":
			return "Suspended"
		}
		return state
	},
	"guest": func(vm vmMachine) string {
		if vm.GuestOSRunning != "" {
			return vm.GuestOSRunning
		}
		if vm.GuestOS != "" {
			return vm.GuestOS
		}
		return "Unknown"
	},
	"yesno": func(value bool) string {
		if value {
			return "Yes"
		}
		return "No"
	},
	"running": func(value bool) string {
		if value {
			return "Running"
		}
		return "Stopped"
	},
}

const vmDiscoveryTemplate = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><title>VMware discovery - {{.Inventory.Server}}</title><style>{{.Style}} details{margin:4px 0 12px}summary{cursor:pointer;font-weight:600}.host{background:#fff;border:1px solid #dbe3ed;border-radius:10px;padding:10px 14px;margin:14px 0}.kv{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:4px 18px;margin:6px 0 10px}.kv div{font-size:12px}.kv b{display:block;color:#52647c;font-weight:600;font-size:11px;text-transform:uppercase;letter-spacing:.03em}.toc{display:flex;flex-wrap:wrap;gap:6px 8px;margin:10px 0 4px}.toc a{background:#fff;border:1px solid #dbe3ed;border-radius:999px;padding:3px 10px;font-size:12px;color:#1d4ed8;text-decoration:none}a{color:#1d4ed8}tr:target,.host:target{outline:2px solid #2563eb;outline-offset:-2px;background:#eef4ff}details.host>summary{font-weight:700}</style></head>
<body><main>
<h1>VMware discovery</h1>
<p class="sub">{{.Inventory.ServerType}} {{.Inventory.Server}} · {{.Inventory.Product}} · read {{date .Inventory.CollectedAt}} by {{.Inventory.CollectedBy}} · {{.Generator}} {{.Inventory.AppVersion}}</p>
<div class="note">Read-only discovery: nothing in VMware was changed. License keys show only their first and last group.</div>
<div class="cards">
<div class="card"><b>{{len .Inventory.Hosts}}</b><span>hosts in {{len .Inventory.Clusters}} cluster(s)</span></div>
<div class="card"><b>{{.Cores}}</b><span>physical cores, {{bytes .Memory}} memory</span></div>
<div class="card"><b>{{len .Inventory.Datastores}}</b><span>datastores</span></div>
<div class="card"><b>{{len .Inventory.Licenses}}</b><span>licenses</span></div>
<div class="card"><b>{{.VMs}}</b><span>VMs and templates</span></div>
<div class="card"><b>{{len .Inventory.Networks}}</b><span>networks</span></div>
</div>
<nav class="toc"><a href="#licenses">Licenses</a><a href="#clusters">Clusters</a><a href="#hosts">Hosts and their VMs</a><a href="#datastores">Datastores and their disks</a><a href="#networks">Networks and their VMs</a><a href="#vms">Virtual machines</a><a href="#switches">Distributed switches</a><a href="#pools">Resource pools</a></nav>
{{if .Watch}}<h2>Needs attention</h2><ul>{{range .Watch}}<li>{{.}}</li>{{end}}</ul>{{end}}
<h2>{{.Inventory.ServerType}}</h2>
<div class="kv"><div><b>Server</b>{{.Inventory.Server}}</div><div><b>Product</b>{{.Inventory.Product}}</div><div><b>Build</b>{{.Inventory.Build}}</div><div><b>API version</b>{{.Inventory.APIVersion}}</div><div><b>Instance UUID</b>{{.Inventory.InstanceUUID}}</div><div><b>Platform</b>{{.Inventory.OSType}}</div><div><b>Datacenters</b>{{join .Inventory.Datacenters ", "}}</div></div>
<h2 id="licenses">Licenses</h2>
<table><tr><th>License</th><th>Edition</th><th>Key</th><th>Product</th><th>Used / total</th><th>Expires</th><th>Assigned to</th></tr>{{range .Inventory.Licenses}}<tr><td>{{.Name}}</td><td>{{.Edition}}</td><td>{{.Key}}</td><td>{{.Product}} {{.Version}}</td><td>{{.Used}} / {{if .Total}}{{.Total}}{{else}}unlimited{{end}} {{.Unit}}</td><td>{{if .Expires}}{{.Expires}}{{else}}Never{{end}}</td><td>{{join .AssignedTo ", "}}</td></tr>{{else}}<tr><td colspan="7">Licenses were not read (inventories saved by older versions do not have them).</td></tr>{{end}}</table>
<h2 id="clusters">Clusters</h2>
<table><tr><th>Cluster</th><th>Datacenter</th><th>Hosts</th><th>Cores</th><th>Memory</th><th>HA</th><th>Admission control</th><th>DRS</th><th>EVC mode</th><th>vSAN</th></tr>{{range .Inventory.Clusters}}<tr><td>{{.Name}}</td><td>{{.Datacenter}}</td><td>{{.Hosts}}</td><td>{{.CPUCores}}</td><td>{{bytes .MemoryBytes}}</td><td>{{yesno .HA}}{{if .HAHostMonitoring}} (host monitoring {{.HAHostMonitoring}}){{end}}</td><td>{{yesno .HAAdmission}}</td><td>{{yesno .DRS}}{{if .DRSBehavior}} ({{.DRSBehavior}}){{end}}</td><td>{{if .EVCMode}}{{.EVCMode}}{{else}}Off{{end}}</td><td>{{yesno .VSAN}}</td></tr>{{else}}<tr><td colspan="10">No clusters (standalone hosts).</td></tr>{{end}}</table>
<h2 id="hosts">Hosts and their VMs</h2>
{{range .Relations.Clusters}}{{if .Hosts}}<h3>{{.Name}} <span class="muted">({{.Datacenter}}, {{len .Hosts}} host(s), {{.VMs}} VM(s))</span></h3>
<table><tr><th>Host</th><th>Model</th><th>Serial</th><th>CPU</th><th>Memory</th><th>ESXi</th><th>License</th><th>State</th><th>VMs</th><th>Allocated</th></tr>{{range .Hosts}}<tr><td><a href="#{{.Anchor}}">{{.Host.Name}}</a></td><td>{{.Host.Vendor}} {{.Host.Model}}</td><td>{{with .Host.Details}}{{.SerialNumber}}{{end}}</td><td>{{.Host.Sockets}} × {{.Host.CPUModel}}, {{.Host.Cores}} cores</td><td>{{bytes .Host.MemoryBytes}}</td><td>{{.Host.Version}} ({{.Host.Build}})</td><td>{{with .Host.License}}{{.Name}}{{end}}</td><td>{{.Host.ConnectionState}}{{if .Host.Maintenance}}, maintenance{{end}}</td><td>{{len .VMs}} ({{.PoweredOn}} on)</td><td>{{.VCPUs}} vCPU, {{bytes (mb .MemoryMB)}}</td></tr>{{end}}</table>{{end}}{{end}}
{{range .Relations.Hosts}}<div class="host" id="{{.Anchor}}"><h3>{{.Host.Name}}</h3>
{{with .Host}}<div class="kv"><div><b>Datacenter / cluster</b>{{.Datacenter}}{{if .Cluster}} / {{.Cluster}}{{end}}</div><div><b>Hardware</b>{{.Vendor}} {{.Model}}</div><div><b>CPU</b>{{.Sockets}} × {{.CPUModel}} ({{.Cores}} cores, {{.Threads}} threads, {{.CPUMhz}} MHz)</div><div><b>Memory</b>{{bytes .MemoryBytes}}</div><div><b>ESXi</b>{{.Version}} build {{.Build}}</div><div><b>State</b>{{.ConnectionState}}, {{.PowerState}}{{if .Maintenance}}, maintenance mode{{end}}</div><div><b>License</b>{{with .License}}{{.Name}} ({{.Key}}){{else}}Not reported{{end}}</div>
{{with .Details}}<div><b>Serial number</b>{{.SerialNumber}}</div><div><b>Asset tag</b>{{.AssetTag}}</div><div><b>System UUID</b>{{.UUID}}</div><div><b>BIOS</b>{{.BIOSVendor}} {{.BIOSVersion}}{{if .BIOSDate}} ({{.BIOSDate}}){{end}}{{if .Firmware}}, {{.Firmware}}{{end}}</div><div><b>Hyperthreading</b>{{.Hyperthreading}}</div><div><b>Usage now</b>CPU {{.CPUUsagePercent}}%, memory {{.MemUsagePercent}}%</div><div><b>Booted</b>{{.BootTime}}</div><div><b>Health</b>{{.OverallStatus}}</div><div><b>Lockdown mode</b>{{.LockdownMode}}</div><div><b>Management IP</b>{{.ManagementIP}}</div><div><b>DNS name</b>{{.HostName}}{{if .Domain}}.{{.Domain}}{{end}}</div><div><b>DNS servers</b>{{join .DNSServers ", "}}</div><div><b>Search domains</b>{{join .SearchDomains ", "}}</div><div><b>Default gateway</b>{{.DefaultGateway}}</div><div><b>Time zone</b>{{.TimeZone}}</div><div><b>NTP</b>{{if .NTPServers}}{{join .NTPServers ", "}}{{else}}None{{end}} ({{running .NTPRunning}})</div><div><b>Certificate</b>{{if .CertificateExpiry}}expires {{.CertificateExpiry}}{{if .CertificateIssuer}}, issued by {{.CertificateIssuer}}{{end}}{{end}}</div><div><b>Software acceptance level</b>{{.AcceptanceLevel}}</div>{{end}}</div>{{end}}
<div class="kv"><div><b>Datastores mounted</b>{{range $i, $d := .Datastores}}{{if $i}}, {{end}}<a href="#{{$d.Anchor}}">{{$d.Name}}</a>{{else}}Not reported{{end}}</div><div><b>VMs on this host</b>{{len .VMs}} ({{.PoweredOn}} powered on)</div><div><b>Allocated to VMs</b>{{.VCPUs}} vCPU, {{bytes (mb .MemoryMB)}}</div></div>
<details open><summary>Virtual machines on this host ({{len .VMs}})</summary><table><tr><th>VM</th><th>Power</th><th>Guest OS</th><th>Size</th><th>Disks and datastores</th><th>Networks</th></tr>{{range .VMs}}<tr><td><a href="#{{.Anchor}}">{{.VM.Name}}</a>{{if .VM.Template}} <span class="chip skip">Template</span>{{end}}</td><td>{{power .VM.PowerState}}</td><td>{{guest .VM}}</td><td>{{.VM.CPUs}} vCPU, {{bytes (mb .VM.MemoryMB)}}</td><td>{{range .Disks}}<div>{{.Disk.Label}} · {{bytes .Disk.CapacityBytes}} on {{if .DatastoreAnchor}}<a href="#{{.DatastoreAnchor}}">{{.Disk.Datastore}}</a>{{else if .Disk.Datastore}}{{.Disk.Datastore}}{{else}}no datastore{{end}}{{if .Disk.RDM}} (RDM){{end}}</div>{{end}}</td><td>{{range .NICs}}<div>{{if .NetworkAnchor}}<a href="#{{.NetworkAnchor}}">{{.NIC.Network}}</a>{{else if .NIC.Network}}{{.NIC.Network}}{{else}}No network{{end}}{{if .NIC.IPAddresses}} <span class="muted">{{join .NIC.IPAddresses ", "}}</span>{{end}}</div>{{end}}</td></tr>{{else}}<tr><td colspan="6">No VMs.</td></tr>{{end}}</table></details>
{{with .Host.Details}}
{{if or .Alarms .ConfigIssues}}<ul>{{range .Alarms}}<li><span class="chip warning">Alarm</span> {{.}}</li>{{end}}{{range .ConfigIssues}}<li><span class="chip warning">Issue</span> {{.}}</li>{{end}}</ul>{{end}}
<details open><summary>Networking ({{len .PhysicalNICs}} physical NICs, {{len .Switches}} switches, {{len .VMkernel}} VMkernel ports)</summary>
<table><tr><th>Physical NIC</th><th>Speed</th><th>MAC</th><th>Driver</th><th>Firmware</th><th>PCI</th><th>Switch</th></tr>{{range .PhysicalNICs}}<tr><td>{{.Device}}</td><td>{{speed .SpeedMb}}{{if and .SpeedMb (not .Duplex)}}, half duplex{{end}}</td><td>{{.MAC}}</td><td>{{.Driver}}</td><td>{{.Firmware}}</td><td>{{.PCI}}</td><td>{{.Switch}}</td></tr>{{end}}</table>
<table><tr><th>Virtual switch</th><th>Kind</th><th>MTU</th><th>Ports</th><th>Uplinks</th><th>Port groups</th></tr>{{range .Switches}}<tr><td>{{.Name}}</td><td>{{.Kind}}</td><td>{{.MTU}}</td><td>{{.Ports}}</td><td>{{join .Uplinks ", "}}</td><td>{{join .Portgroups ", "}}</td></tr>{{end}}</table>
<table><tr><th>VMkernel port</th><th>Port group</th><th>IP address</th><th>Mask</th><th>MTU</th><th>TCP/IP stack</th><th>Services</th></tr>{{range .VMkernel}}<tr><td>{{.Device}}</td><td>{{.Portgroup}}</td><td>{{.IP}}{{if .DHCP}} (DHCP){{end}}</td><td>{{.Mask}}</td><td>{{.MTU}}</td><td>{{.Stack}}</td><td>{{join .Services ", "}}</td></tr>{{end}}</table></details>
<details open><summary>Storage ({{len .StorageAdapters}} adapters, {{len .LUNs}} devices)</summary>
<table><tr><th>Storage adapter</th><th>Kind</th><th>Model</th><th>Driver</th><th>Identifier</th><th>Status</th></tr>{{range .StorageAdapters}}<tr><td>{{.Device}}</td><td>{{.Kind}}</td><td>{{.Model}}</td><td>{{.Driver}}</td><td>{{.Identifier}}</td><td>{{.Status}}</td></tr>{{end}}</table>
<table><tr><th>Storage device</th><th>Type</th><th>Vendor / model</th><th>Capacity</th><th>SSD</th><th>Local</th><th>State</th></tr>{{range .LUNs}}<tr><td>{{.DisplayName}}<div class="muted">{{.CanonicalName}}</div></td><td>{{.Type}}</td><td>{{.Vendor}} {{.Model}}</td><td>{{if .CapacityBytes}}{{bytes .CapacityBytes}}{{end}}</td><td>{{yesno .SSD}}</td><td>{{yesno .Local}}</td><td>{{.State}}</td></tr>{{end}}</table></details>
<details><summary>Services ({{len .Services}})</summary><table><tr><th>Service</th><th>Key</th><th>State</th><th>Start policy</th></tr>{{range .Services}}<tr><td>{{.Label}}</td><td>{{.Key}}</td><td>{{running .Running}}</td><td>{{.Policy}}</td></tr>{{end}}</table></details>
<details><summary>Installed software packages (VIBs) ({{len .Packages}})</summary><table><tr><th>Package</th><th>Version</th><th>Vendor</th><th>Acceptance</th><th>Created</th></tr>{{range .Packages}}<tr><td>{{.Name}}</td><td>{{.Version}}</td><td>{{.Vendor}}</td><td>{{.Acceptance}}</td><td>{{.Created}}</td></tr>{{else}}<tr><td colspan="5">Not listed by this host.</td></tr>{{end}}</table></details>
{{end}}</div>{{end}}
<h2 id="datastores">Datastores and their disks</h2>
<table><tr><th>Datastore</th><th>Type</th><th>Capacity</th><th>Free</th><th>Provisioned</th><th>Shared</th><th>Mounted on</th><th>VMs</th><th>VM disks</th></tr>{{range .Relations.Datastores}}<tr><td><a href="#{{.Anchor}}">{{.Datastore.Name}}</a></td><td>{{.Datastore.Type}}</td><td>{{bytes .Datastore.CapacityBytes}}</td><td>{{bytes .Datastore.FreeBytes}}{{if .Datastore.CapacityBytes}} ({{percent .Datastore.FreeBytes .Datastore.CapacityBytes}}){{end}}</td><td>{{bytes .Datastore.ProvisionedBytes}}</td><td>{{yesno .Datastore.Shared}}</td><td>{{if .Hosts}}{{len .Hosts}} host(s){{end}}</td><td>{{.VMs}}</td><td>{{len .Disks}} ({{bytes .DiskBytes}})</td></tr>{{end}}</table>
{{range .Relations.Datastores}}<div class="host" id="{{.Anchor}}"><h3>{{.Datastore.Name}}</h3>
<div class="kv"><div><b>Type</b>{{.Datastore.Type}}{{if not .Datastore.Accessible}}, not accessible{{end}}</div><div><b>Capacity</b>{{bytes .Datastore.CapacityBytes}}</div><div><b>Free</b>{{bytes .Datastore.FreeBytes}}{{if .Datastore.CapacityBytes}} ({{percent .Datastore.FreeBytes .Datastore.CapacityBytes}}){{end}}</div><div><b>Provisioned</b>{{bytes .Datastore.ProvisionedBytes}}</div><div><b>Shared</b>{{yesno .Datastore.Shared}}</div><div><b>Datacenter</b>{{.Datastore.Datacenter}}</div>{{if .Datastore.URL}}<div><b>URL</b>{{.Datastore.URL}}</div>{{end}}<div><b>Mounted on</b>{{range $i, $h := .Hosts}}{{if $i}}, {{end}}{{if $h.Anchor}}<a href="#{{$h.Anchor}}">{{$h.Name}}</a>{{else}}{{$h.Name}}{{end}}{{else}}Not reported{{end}}</div></div>
<details open><summary>VM disks on this datastore ({{len .Disks}} disks of {{.VMs}} VM(s), {{bytes .DiskBytes}})</summary><table><tr><th>VM</th><th>Host</th><th>Disk</th><th>Size</th><th>Provisioning</th><th>Mode</th><th>File</th></tr>{{range .Disks}}<tr><td><a href="#{{.VMAnchor}}">{{.VM}}</a></td><td>{{.Host}}</td><td>{{.Disk.Label}}{{if .Disk.RDM}} (RDM {{.Disk.RDM}}){{end}}</td><td>{{bytes .Disk.CapacityBytes}}</td><td>{{.Disk.Provisioning}}</td><td>{{.Disk.Mode}}{{if .Disk.Sharing}}{{if ne .Disk.Sharing "sharingNone"}}, {{.Disk.Sharing}}{{end}}{{end}}</td><td>{{.Disk.File}}</td></tr>{{else}}<tr><td colspan="7">No VM disks.</td></tr>{{end}}</table></details>
</div>{{end}}
{{if .Relations.OtherDisks}}<h3>Disks not on a datastore in this inventory</h3><p class="muted">Raw device mappings, and disks on datastores this account could not read.</p><table><tr><th>VM</th><th>Host</th><th>Disk</th><th>Size</th><th>Datastore or device</th><th>File</th></tr>{{range .Relations.OtherDisks}}<tr><td><a href="#{{.VMAnchor}}">{{.VM}}</a></td><td>{{.Host}}</td><td>{{.Disk.Label}}</td><td>{{bytes .Disk.CapacityBytes}}</td><td>{{if .Disk.Datastore}}{{.Disk.Datastore}}{{end}}{{if .Disk.RDM}}{{if .Disk.Datastore}}, {{end}}RDM ({{.Disk.RDM}}){{end}}</td><td>{{.Disk.File}}</td></tr>{{end}}</table>{{end}}
<h2 id="networks">Networks and their VMs</h2>
<table><tr><th>Network</th><th>Kind</th><th>Switch</th><th>VLAN</th><th>VMs</th><th>Adapters</th></tr>{{range .Relations.Networks}}<tr><td><a href="#{{.Anchor}}">{{.Network.Name}}</a></td><td>{{.Network.Kind}}</td><td>{{.Network.Switch}}</td><td>{{.Network.VLAN}}</td><td>{{.VMs}}</td><td>{{len .Adapters}}</td></tr>{{end}}</table>
{{range .Relations.Networks}}<details open class="host" id="{{.Anchor}}"><summary>{{.Network.Name}} <span class="muted">({{if .Network.VLAN}}VLAN {{.Network.VLAN}}, {{end}}{{if .Network.Switch}}{{.Network.Switch}}, {{end}}{{len .Adapters}} adapter(s))</span></summary><table><tr><th>VM</th><th>Host</th><th>Adapter</th><th>Type</th><th>MAC</th><th>Connected</th><th>Guest addresses</th></tr>{{range .Adapters}}<tr><td><a href="#{{.VMAnchor}}">{{.VM}}</a></td><td>{{.Host}}</td><td>{{.NIC.Label}}</td><td>{{.NIC.AdapterType}}</td><td>{{.NIC.MAC}}</td><td>{{yesno .NIC.Connected}}</td><td>{{join .NIC.IPAddresses ", "}}{{if .NIC.Static}} (static){{end}}</td></tr>{{else}}<tr><td colspan="7">No VM adapters.</td></tr>{{end}}</table></details>{{end}}
<h2 id="vms">Virtual machines</h2>
<p class="muted">Each VM's host, disks with their datastores, and networks. Readiness for each migration target is in the readiness report.</p>
<table><tr><th>VM</th><th>Host</th><th>Power</th><th>Guest OS</th><th>Size</th><th>Disks and datastores</th><th>Networks</th></tr>{{range .Relations.VMs}}<tr id="{{.Anchor}}"><td><b>{{.VM.Name}}</b>{{if .VM.Template}} <span class="chip skip">Template</span>{{end}}</td><td>{{if .HostAnchor}}<a href="#{{.HostAnchor}}">{{.VM.Host}}</a>{{else}}{{.VM.Host}}{{end}}</td><td>{{power .VM.PowerState}}</td><td>{{guest .VM}}</td><td>{{.VM.CPUs}} vCPU, {{bytes (mb .VM.MemoryMB)}}</td><td>{{range .Disks}}<div>{{.Disk.Label}} · {{bytes .Disk.CapacityBytes}} on {{if .DatastoreAnchor}}<a href="#{{.DatastoreAnchor}}">{{.Disk.Datastore}}</a>{{else if .Disk.Datastore}}{{.Disk.Datastore}}{{else}}no datastore{{end}}{{if .Disk.RDM}} (RDM){{end}}</div>{{end}}</td><td>{{range .NICs}}<div>{{if .NetworkAnchor}}<a href="#{{.NetworkAnchor}}">{{.NIC.Network}}</a>{{else if .NIC.Network}}{{.NIC.Network}}{{else}}No network{{end}}{{if .NIC.IPAddresses}} <span class="muted">{{join .NIC.IPAddresses ", "}}</span>{{end}}</div>{{end}}</td></tr>{{else}}<tr><td colspan="7">No VMs.</td></tr>{{end}}</table>
<h2 id="switches">Distributed switches</h2>
<table><tr><th>Switch</th><th>Datacenter</th><th>Version</th><th>MTU</th><th>Uplinks</th><th>Discovery protocol</th><th>Hosts</th></tr>{{range .Inventory.Switches}}<tr><td>{{.Name}}</td><td>{{.Datacenter}}</td><td>{{.Version}}</td><td>{{.MTU}}</td><td>{{.Uplinks}}</td><td>{{.Discovery}}</td><td>{{join .Hosts ", "}}</td></tr>{{else}}<tr><td colspan="7">None.</td></tr>{{end}}</table>
<h2 id="pools">Resource pools and vApps</h2>
<table><tr><th>Pool</th><th>Kind</th><th>Cluster or host</th><th>Parent pool</th><th>CPU reservation / limit</th><th>Memory reservation / limit</th><th>VMs</th></tr>{{range .Inventory.ResourcePools}}<tr><td>{{.Name}}</td><td>{{if .VApp}}vApp{{else}}Resource pool{{end}}</td><td>{{.Owner}}</td><td>{{.Parent}}</td><td>{{.CPUReservationMHz}} MHz / {{limit .CPULimitMHz "MHz"}}</td><td>{{.MemReservationMB}} MB / {{limit .MemLimitMB "MB"}}</td><td>{{.VMCount}}</td></tr>{{else}}<tr><td colspan="7">None.</td></tr>{{end}}</table>
{{if .Inventory.Warnings}}<h2>Collection warnings</h2><ul>{{range .Inventory.Warnings}}<li>{{.}}</li>{{end}}</ul>{{end}}
</main></body></html>`

var vmDiscoveryTmpl = template.Must(template.New("discovery").Funcs(vmDiscoveryFunctions).Parse(vmDiscoveryTemplate))

// writeVMwareDiscoveryOutputs writes the discovery report and its CSV files.
func writeVMwareDiscoveryOutputs(folder string, inventory *vmInventory, now time.Time) (string, error) {
	cores, memory := 0, int64(0)
	for _, host := range inventory.Hosts {
		cores += host.Cores
		memory += host.MemoryBytes
	}
	var page bytes.Buffer
	if err := vmDiscoveryTmpl.Execute(&page, map[string]any{"Inventory": inventory, "Watch": vmDiscoveryWatch(inventory, now), "Relations": vmRelationsFor(inventory), "Cores": cores, "Memory": memory, "VMs": len(inventory.VMs), "Style": template.CSS(vmReportStylesheet), "Generator": vmReportGeneratorLine}); err != nil {
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
	return report, os.WriteFile(report, page.Bytes(), 0600)
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
