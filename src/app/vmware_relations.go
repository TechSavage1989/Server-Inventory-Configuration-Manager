package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/vmware/govmomi/vim25/types"
)

// How the VMware inventory fits together: which VMs run on each host, which
// disks live on each datastore and which adapters use each network. The
// discovery report and its CSVs are built from this; the application builds
// the same tree in the browser from the saved inventory.

type vmRelVM struct {
	Anchor     string
	HostAnchor string
	VM         vmMachine
	Disks      []vmRelDisk
	NICs       []vmRelNIC
}

type vmRelDisk struct {
	VM              string
	VMAnchor        string
	Host            string
	DatastoreAnchor string
	Disk            vmDisk
}

type vmRelNIC struct {
	VM            string
	VMAnchor      string
	Host          string
	NetworkAnchor string
	NIC           vmNIC
}

type vmRelHost struct {
	Anchor     string
	Host       vmHost
	VMs        []*vmRelVM
	Datastores []vmRelLink
	PoweredOn  int
	VCPUs      int
	MemoryMB   int64
}

type vmRelLink struct {
	Name   string
	Anchor string
}

type vmRelCluster struct {
	Name       string
	Datacenter string
	Hosts      []*vmRelHost
	VMs        int
}

type vmRelDatastore struct {
	Anchor    string
	Datastore vmDatastore
	Disks     []vmRelDisk
	VMs       int
	DiskBytes int64
	Hosts     []vmRelLink
}

type vmRelNetwork struct {
	Anchor   string
	Network  vmNetwork
	Adapters []vmRelNIC
	VMs      int
}

type vmRelations struct {
	Clusters   []vmRelCluster
	Hosts      []*vmRelHost
	Datastores []*vmRelDatastore
	Networks   []*vmRelNetwork
	VMs        []*vmRelVM
	// Unplaced VMs name a host that is not in the inventory; OtherDisks and
	// OtherNICs use storage or networks that are not in it.
	Unplaced   []*vmRelVM
	OtherDisks []vmRelDisk
	OtherNICs  []vmRelNIC
}

func vmRelKey(datacenter, name string) string { return datacenter + "\x00" + name }

// vmRelationsFor links every VM to its host, datastores and networks.
func vmRelationsFor(inventory *vmInventory) vmRelations {
	relations := vmRelations{}
	hostsByKey, hostsByName := map[string]*vmRelHost{}, map[string]*vmRelHost{}
	for index, host := range inventory.Hosts {
		item := &vmRelHost{Anchor: "host-" + strconv.Itoa(index+1), Host: host}
		relations.Hosts = append(relations.Hosts, item)
		hostsByKey[vmRelKey(host.Datacenter, host.Name)] = item
		if _, seen := hostsByName[host.Name]; !seen {
			hostsByName[host.Name] = item
		}
	}
	datastores := map[string]*vmRelDatastore{}
	for index, datastore := range inventory.Datastores {
		item := &vmRelDatastore{Anchor: "datastore-" + strconv.Itoa(index+1), Datastore: datastore}
		for _, name := range datastore.Hosts {
			host := hostsByKey[vmRelKey(datastore.Datacenter, name)]
			if host == nil {
				host = hostsByName[name]
			}
			link := vmRelLink{Name: name}
			if host != nil {
				link.Anchor = host.Anchor
				host.Datastores = append(host.Datastores, vmRelLink{Name: datastore.Name, Anchor: item.Anchor})
			}
			item.Hosts = append(item.Hosts, link)
		}
		relations.Datastores = append(relations.Datastores, item)
		datastores[vmRelKey(datastore.Datacenter, datastore.Name)] = item
		if _, seen := datastores[vmRelKey("\x00any", datastore.Name)]; !seen {
			datastores[vmRelKey("\x00any", datastore.Name)] = item
		}
	}
	networks := map[string]*vmRelNetwork{}
	for index, network := range inventory.Networks {
		item := &vmRelNetwork{Anchor: "network-" + strconv.Itoa(index+1), Network: network}
		relations.Networks = append(relations.Networks, item)
		for _, key := range []string{vmRelKey(network.Datacenter, network.Name), vmRelKey("\x00any", network.Name)} {
			if _, seen := networks[key]; !seen {
				networks[key] = item
			}
		}
	}

	datastoreVMs, networkVMs := map[*vmRelDatastore]map[string]bool{}, map[*vmRelNetwork]map[string]bool{}
	for index, vm := range inventory.VMs {
		item := &vmRelVM{Anchor: "vm-" + strconv.Itoa(index+1), VM: vm}
		relations.VMs = append(relations.VMs, item)
		host := hostsByKey[vmRelKey(vm.Datacenter, vm.Host)]
		if host == nil && vm.Host != "" {
			host = hostsByName[vm.Host]
		}
		if host != nil {
			item.HostAnchor = host.Anchor
			host.VMs = append(host.VMs, item)
			if vm.PowerState == "poweredOn" {
				host.PoweredOn++
			}
			host.VCPUs += vm.CPUs
			host.MemoryMB += vm.MemoryMB
		} else {
			relations.Unplaced = append(relations.Unplaced, item)
		}
		for _, disk := range vm.Disks {
			ref := vmRelDisk{VM: vm.Name, VMAnchor: item.Anchor, Host: vm.Host, Disk: disk}
			datastore := datastores[vmRelKey(vm.Datacenter, disk.Datastore)]
			if datastore == nil && disk.Datastore != "" {
				datastore = datastores[vmRelKey("\x00any", disk.Datastore)]
			}
			if datastore == nil {
				relations.OtherDisks = append(relations.OtherDisks, ref)
			} else {
				ref.DatastoreAnchor = datastore.Anchor
				datastore.Disks = append(datastore.Disks, ref)
				datastore.DiskBytes += disk.CapacityBytes
				if datastoreVMs[datastore] == nil {
					datastoreVMs[datastore] = map[string]bool{}
				}
				datastoreVMs[datastore][item.Anchor] = true
			}
			item.Disks = append(item.Disks, ref)
		}
		for _, nic := range vm.NICs {
			ref := vmRelNIC{VM: vm.Name, VMAnchor: item.Anchor, Host: vm.Host, NIC: nic}
			network := networks[vmRelKey(vm.Datacenter, nic.Network)]
			if network == nil && nic.Network != "" {
				network = networks[vmRelKey("\x00any", nic.Network)]
			}
			if network == nil {
				relations.OtherNICs = append(relations.OtherNICs, ref)
			} else {
				ref.NetworkAnchor = network.Anchor
				network.Adapters = append(network.Adapters, ref)
				if networkVMs[network] == nil {
					networkVMs[network] = map[string]bool{}
				}
				networkVMs[network][item.Anchor] = true
			}
			item.NICs = append(item.NICs, ref)
		}
	}
	for datastore, vms := range datastoreVMs {
		datastore.VMs = len(vms)
	}
	for network, vms := range networkVMs {
		network.VMs = len(vms)
	}
	byName := func(list []*vmRelVM) {
		sort.SliceStable(list, func(a, b int) bool { return strings.ToLower(list[a].VM.Name) < strings.ToLower(list[b].VM.Name) })
	}
	for _, host := range relations.Hosts {
		byName(host.VMs)
		sort.SliceStable(host.Datastores, func(a, b int) bool { return host.Datastores[a].Name < host.Datastores[b].Name })
	}
	byName(relations.Unplaced)
	for _, datastore := range relations.Datastores {
		sort.SliceStable(datastore.Disks, func(a, b int) bool {
			return strings.ToLower(datastore.Disks[a].VM) < strings.ToLower(datastore.Disks[b].VM)
		})
	}
	for _, network := range relations.Networks {
		sort.SliceStable(network.Adapters, func(a, b int) bool {
			return strings.ToLower(network.Adapters[a].VM) < strings.ToLower(network.Adapters[b].VM)
		})
	}

	// Clusters in inventory order, then standalone hosts per datacenter.
	clusterIndex := map[string]int{}
	for _, cluster := range inventory.Clusters {
		clusterIndex[vmRelKey(cluster.Datacenter, cluster.Name)] = len(relations.Clusters)
		relations.Clusters = append(relations.Clusters, vmRelCluster{Name: cluster.Name, Datacenter: cluster.Datacenter})
	}
	for _, host := range relations.Hosts {
		key := vmRelKey(host.Host.Datacenter, host.Host.Cluster)
		if host.Host.Cluster == "" {
			key = vmRelKey(host.Host.Datacenter, "\x00standalone")
		}
		index, ok := clusterIndex[key]
		if !ok {
			name := host.Host.Cluster
			if name == "" {
				name = "Standalone hosts"
			}
			index = len(relations.Clusters)
			clusterIndex[key] = index
			relations.Clusters = append(relations.Clusters, vmRelCluster{Name: name, Datacenter: host.Host.Datacenter})
		}
		relations.Clusters[index].Hosts = append(relations.Clusters[index].Hosts, host)
		relations.Clusters[index].VMs += len(host.VMs)
	}
	return relations
}

// vmDatastoreHosts names the hosts that have a datastore mounted.
func vmDatastoreHosts(mounts []types.DatastoreHostMount, hostNames map[string]string) []string {
	names := []string{}
	for _, mount := range mounts {
		if mount.MountInfo.Mounted != nil && !*mount.MountInfo.Mounted {
			continue
		}
		if name := hostNames[mount.Key.Value]; name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
