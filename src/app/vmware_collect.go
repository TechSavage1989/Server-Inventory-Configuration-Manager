package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// Read-only vSphere inventory through the vSphere Web Services API (the API
// PowerCLI uses), with VMware's own Go library. The inventory session only
// reads properties: no task is started and nothing is changed. The only
// changes the application makes are the opt-in prepare-source steps in
// vmware_prepare.go.

type vmConnectOptions struct {
	Server     string
	User       string
	Password   string
	Thumbprint string
	Timeout    time.Duration
	UserAgent  string
	AppVersion string
	// insecureForTest is used only by tests against the simulator.
	insecureForTest bool
}

// vmCertificateError means the server's certificate is not trusted by
// Windows and its thumbprint has not been confirmed by the operator.
type vmCertificateError struct {
	Thumbprint string `json:"thumbprint"`
	Subject    string `json:"subject"`
	Issuer     string `json:"issuer"`
	NotAfter   string `json:"notAfter"`
	Reason     string `json:"reason"`
	Mismatch   bool   `json:"mismatch"`
}

func (e *vmCertificateError) Error() string {
	if e.Mismatch {
		return "The server's certificate changed since you trusted it. Check the thumbprint before trusting it again."
	}
	return "Windows does not trust the server's certificate. Check its thumbprint and trust it to continue."
}

var vmServerPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]{0,252})?(:[0-9]{1,5})?$|^\[[0-9A-Fa-f:.]+\](:[0-9]{1,5})?$`)

// vmServerURL turns "vcenter.example.com", "10.0.0.5:8443" or an https URL
// into the SDK endpoint.
func vmServerURL(server string) (*url.URL, error) {
	server = strings.TrimSpace(server)
	if lower := strings.ToLower(server); strings.HasPrefix(lower, "https://") {
		parsed, err := url.Parse(server)
		if err != nil || parsed.Host == "" {
			return nil, errors.New("Type the vCenter or ESXi host name, for example vcenter.example.com.")
		}
		server = parsed.Host
	} else if strings.Contains(server, "://") {
		return nil, errors.New("Only HTTPS connections to vCenter or ESXi are supported.")
	}
	server = strings.TrimSuffix(server, "/")
	if !vmServerPattern.MatchString(server) {
		return nil, errors.New("Type the vCenter or ESXi host name, for example vcenter.example.com.")
	}
	return &url.URL{Scheme: "https", Host: server, Path: "/sdk"}, nil
}

func vmHostPort(u *url.URL) string {
	if _, _, err := net.SplitHostPort(u.Host); err == nil {
		return u.Host
	}
	return net.JoinHostPort(strings.Trim(u.Host, "[]"), "443")
}

func vmNormalizeThumbprint(value string) string {
	cleaned := strings.ToUpper(regexp.MustCompile(`[^0-9A-Fa-f]`).ReplaceAllString(value, ""))
	if len(cleaned) != 64 {
		return ""
	}
	parts := make([]string, 0, 32)
	for index := 0; index < 64; index += 2 {
		parts = append(parts, cleaned[index:index+2])
	}
	return strings.Join(parts, ":")
}

// vmTLSConfig verifies the certificate with the Windows trust store and the
// host name first. A certificate Windows does not trust is accepted only when
// its SHA-256 thumbprint matches the one the operator confirmed.
func vmTLSConfig(hostName, pinned string, captured **vmCertificateError) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // verified below, with the system roots or the confirmed thumbprint
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("the server sent no certificate")
			}
			leaf := state.PeerCertificates[0]
			intermediates := x509.NewCertPool()
			for _, certificate := range state.PeerCertificates[1:] {
				intermediates.AddCert(certificate)
			}
			_, verifyErr := leaf.Verify(x509.VerifyOptions{DNSName: hostName, Intermediates: intermediates})
			if verifyErr == nil {
				return nil
			}
			thumbprint := soap.ThumbprintSHA256(leaf)
			if pinned != "" && thumbprint == pinned {
				return nil
			}
			*captured = &vmCertificateError{Thumbprint: thumbprint, Subject: leaf.Subject.String(), Issuer: leaf.Issuer.String(), NotAfter: leaf.NotAfter.UTC().Format("2006-01-02"), Reason: verifyErr.Error(), Mismatch: pinned != ""}
			return *captured
		},
	}
}

func vmFriendlyConnectError(err error, server string) error {
	if err == nil {
		return nil
	}
	var certificateErr *vmCertificateError
	if errors.As(err, &certificateErr) {
		return certificateErr
	}
	if soap.IsSoapFault(err) {
		switch soap.ToSoapFault(err).VimFault().(type) {
		case types.InvalidLogin, *types.InvalidLogin:
			return errors.New("vCenter or ESXi refused the user name or password.")
		case types.NoPermission, *types.NoPermission:
			return errors.New("The account can sign in but is not allowed to read the inventory. Use an account with at least the Read-only role on the whole inventory.")
		}
	}
	text := err.Error()
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "login failure"):
		return errors.New("vCenter or ESXi refused the user name or password.")
	case strings.Contains(lower, "incorrect user name or password"), strings.Contains(lower, "invalidlogin"), strings.Contains(lower, "cannot complete login"):
		return errors.New("vCenter or ESXi refused the user name or password.")
	case strings.Contains(lower, "no such host"):
		return fmt.Errorf("%s could not be found. Check the name or use its IP address.", server)
	case strings.Contains(lower, "connection refused"), strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "connectex"):
		return fmt.Errorf("%s did not answer on HTTPS. Check the name, the port and the firewall.", server)
	}
	return fmt.Errorf("Could not read %s: %s", server, text)
}

// collectVSphere connects, reads the inventory and logs out.
// vmSession is a logged-in vSphere session; close it with logout.
type vmSession struct {
	client  *vim25.Client
	manager *session.Manager
	host    string
}

func (s *vmSession) logout() {
	logoutContext, logoutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	_ = s.manager.Logout(logoutContext)
	logoutCancel()
}

// openVSphereSession connects and logs in with the operator's account,
// trusting the server's certificate only through Windows or a confirmed
// thumbprint.
func openVSphereSession(ctx context.Context, options vmConnectOptions, progress func(string)) (*vmSession, error) {
	u, err := vmServerURL(options.Server)
	if err != nil {
		return nil, err
	}
	var certificateErr *vmCertificateError
	soapClient := soap.NewClient(u, options.insecureForTest)
	if !options.insecureForTest {
		hostName := u.Hostname()
		soapClient.DefaultTransport().TLSClientConfig = vmTLSConfig(hostName, vmNormalizeThumbprint(options.Thumbprint), &certificateErr)
	}
	soapClient.Timeout = 2 * time.Minute
	soapClient.UserAgent = options.UserAgent
	progress("Connecting to " + u.Host + ".")
	client, err := vim25.NewClient(ctx, soapClient)
	if err != nil {
		if certificateErr != nil {
			return nil, certificateErr
		}
		return nil, vmFriendlyConnectError(err, u.Host)
	}
	manager := session.NewManager(client)
	if err := manager.Login(ctx, url.UserPassword(options.User, options.Password)); err != nil {
		return nil, vmFriendlyConnectError(err, u.Host)
	}
	return &vmSession{client: client, manager: manager, host: u.Host}, nil
}

func collectVSphere(parent context.Context, options vmConnectOptions, progress func(string)) (*vmInventory, error) {
	if progress == nil {
		progress = func(string) {}
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, progress)
	if err != nil {
		return nil, err
	}
	defer vsphere.logout()
	client, u := vsphere.client, struct{ Host string }{vsphere.host}

	about := client.ServiceContent.About
	inventory := &vmInventory{Schema: 1, Server: u.Host, Product: about.FullName, APIVersion: about.ApiVersion, CollectedAt: time.Now(), CollectedBy: options.User, AppVersion: options.AppVersion, Warnings: []string{}}
	inventory.Build, inventory.InstanceUUID, inventory.OSType = about.Build, about.InstanceUuid, about.OsType
	inventory.ServerType = "ESXi host"
	if about.ApiType == "VirtualCenter" {
		inventory.ServerType = "vCenter Server"
	}
	progress("Connected to " + about.FullName + ".")
	if err := readVSphereInventory(ctx, client, inventory, progress); err != nil {
		return nil, vmFriendlyConnectError(err, u.Host)
	}
	assessInventory(inventory, time.Now())
	return inventory, nil
}

func readVSphereInventory(ctx context.Context, client *vim25.Client, inventory *vmInventory, progress func(string)) error {
	views := view.NewManager(client)
	root, err := views.CreateContainerView(ctx, client.ServiceContent.RootFolder, []string{"Datacenter"}, true)
	if err != nil {
		return err
	}
	var datacenters []mo.Datacenter
	err = root.Retrieve(ctx, []string{"Datacenter"}, []string{"name"}, &datacenters)
	_ = root.Destroy(ctx)
	if err != nil {
		return err
	}
	sort.Slice(datacenters, func(i, j int) bool { return datacenters[i].Name < datacenters[j].Name })
	for _, datacenter := range datacenters {
		inventory.Datacenters = append(inventory.Datacenters, datacenter.Name)
		progress("Reading datacenter " + datacenter.Name + ".")
		if err := readVSphereDatacenter(ctx, views, datacenter, inventory, progress); err != nil {
			return err
		}
	}
	progress("Reading licenses.")
	vmReadLicenses(ctx, client, inventory)
	sort.Slice(inventory.VMs, func(i, j int) bool {
		return strings.ToLower(inventory.VMs[i].Name) < strings.ToLower(inventory.VMs[j].Name)
	})
	return nil
}

func readVSphereDatacenter(ctx context.Context, views *view.Manager, datacenter mo.Datacenter, inventory *vmInventory, progress func(string)) error {
	container, err := views.CreateContainerView(ctx, datacenter.Reference(), nil, true)
	if err != nil {
		return err
	}
	defer container.Destroy(ctx)

	var clusters []mo.ClusterComputeResource
	if err := container.Retrieve(ctx, []string{"ClusterComputeResource"}, []string{"name", "summary", "configurationEx", "host"}, &clusters); err != nil {
		return err
	}
	clusterNames := map[string]string{}
	for _, cluster := range clusters {
		clusterNames[cluster.Reference().Value] = cluster.Name
		item := vmCluster{Name: cluster.Name, Datacenter: datacenter.Name, Hosts: len(cluster.Host)}
		if summary := cluster.Summary.GetComputeResourceSummary(); summary != nil {
			item.CPUCores = int(summary.NumCpuCores)
			item.MemoryBytes = summary.TotalMemory
		}
		if config, ok := cluster.ConfigurationEx.(*types.ClusterConfigInfoEx); ok && config != nil {
			item.HA = config.DasConfig.Enabled != nil && *config.DasConfig.Enabled
			item.DRS = config.DrsConfig.Enabled != nil && *config.DrsConfig.Enabled
		}
		vmClusterSettings(cluster, &item)
		inventory.Clusters = append(inventory.Clusters, item)
	}

	var hosts []mo.HostSystem
	if err := container.Retrieve(ctx, []string{"HostSystem"}, vmHostProperties, &hosts); err != nil {
		return err
	}
	hostNames, hostClusters := map[string]string{}, map[string]string{}
	standardVLANs := map[string]map[string]bool{}
	standardSwitches := map[string]string{}
	firstHost := len(inventory.Hosts)
	for _, host := range hosts {
		hostNames[host.Reference().Value] = host.Name
		cluster := ""
		if host.Parent != nil {
			cluster = clusterNames[host.Parent.Value]
		}
		hostClusters[host.Reference().Value] = cluster
		item := vmHost{Name: host.Name, Datacenter: datacenter.Name, Cluster: cluster, VMCount: len(host.Vm)}
		if hardware := host.Summary.Hardware; hardware != nil {
			item.Vendor, item.Model, item.CPUModel = hardware.Vendor, hardware.Model, hardware.CpuModel
			item.Sockets, item.Cores, item.Threads = int(hardware.NumCpuPkgs), int(hardware.NumCpuCores), int(hardware.NumCpuThreads)
			item.CPUMhz, item.MemoryBytes = int(hardware.CpuMhz), hardware.MemorySize
		}
		if product := host.Summary.Config.Product; product != nil {
			item.Version, item.Build = product.Version, product.Build
		}
		item.ConnectionState = string(host.Summary.Runtime.ConnectionState)
		item.PowerState = string(host.Summary.Runtime.PowerState)
		item.Maintenance = host.Summary.Runtime.InMaintenanceMode
		item.ref = host.Reference().Value
		item.Details = vmHostDetailsFrom(host)
		inventory.Hosts = append(inventory.Hosts, item)
		if host.Config != nil && host.Config.Network != nil {
			for _, portgroup := range host.Config.Network.Portgroup {
				name := portgroup.Spec.Name
				if standardVLANs[name] == nil {
					standardVLANs[name] = map[string]bool{}
				}
				standardVLANs[name][vmStandardVLAN(portgroup.Spec.VlanId)] = true
				standardSwitches[name] = portgroup.Spec.VswitchName
			}
		}
	}
	progress(fmt.Sprintf("Read %d host(s) in %s; reading their software and alarms.", len(hosts), datacenter.Name))
	vmReadHostExtras(ctx, views.Client(), hosts, inventory, firstHost)

	var datastores []mo.Datastore
	if err := container.Retrieve(ctx, []string{"Datastore"}, []string{"name", "summary", "vm", "host"}, &datastores); err != nil {
		return err
	}
	datastoreNames := map[string]string{}
	for _, datastore := range datastores {
		datastoreNames[datastore.Reference().Value] = datastore.Name
		summary := datastore.Summary
		item := vmDatastore{Name: datastore.Name, Datacenter: datacenter.Name, Type: summary.Type, CapacityBytes: summary.Capacity, FreeBytes: summary.FreeSpace, Accessible: summary.Accessible, VMCount: len(datastore.Vm)}
		item.ProvisionedBytes = summary.Capacity - summary.FreeSpace + summary.Uncommitted
		if summary.MultipleHostAccess != nil {
			item.Shared = *summary.MultipleHostAccess
		}
		item.URL = summary.Url
		item.Hosts = vmDatastoreHosts(datastore.Host, hostNames)
		inventory.Datastores = append(inventory.Datastores, item)
	}

	switchNames := map[string]string{}
	var switches []mo.DistributedVirtualSwitch
	if err := container.Retrieve(ctx, []string{"DistributedVirtualSwitch"}, []string{"name", "uuid", "config"}, &switches); err != nil && !vmIsMissingType(err) {
		return err
	}
	for _, item := range switches {
		switchNames[item.Uuid] = item.Name
		switchNames[item.Reference().Value] = item.Name
	}
	vmReadSwitches(switches, datacenter.Name, hostNames, inventory)
	owners := map[string]string{}
	for _, cluster := range clusters {
		owners[cluster.Reference().Value] = cluster.Name
	}
	var computeResources []mo.ComputeResource
	if err := container.Retrieve(ctx, []string{"ComputeResource"}, []string{"name"}, &computeResources); err == nil {
		for _, resource := range computeResources {
			owners[resource.Reference().Value] = resource.Name
		}
	}
	vmReadPools(ctx, func(kind string, properties []string, dst any) error {
		return container.Retrieve(ctx, []string{kind}, properties, dst)
	}, datacenter.Name, owners, inventory)
	var portgroups []mo.DistributedVirtualPortgroup
	if err := container.Retrieve(ctx, []string{"DistributedVirtualPortgroup"}, []string{"name", "config", "vm"}, &portgroups); err != nil && !vmIsMissingType(err) {
		return err
	}
	portgroupNames := map[string]string{}
	seenNetworks := map[string]bool{}
	for _, portgroup := range portgroups {
		portgroupNames[portgroup.Config.Key] = portgroup.Name
		seenNetworks[portgroup.Reference().Value] = true
		if portgroup.Config.Uplink != nil && *portgroup.Config.Uplink {
			continue
		}
		item := vmNetwork{Name: portgroup.Name, Datacenter: datacenter.Name, Kind: "Distributed", VLAN: vmDistributedVLAN(portgroup.Config.DefaultPortConfig), VMCount: len(portgroup.Vm)}
		if portgroup.Config.DistributedVirtualSwitch != nil {
			item.Switch = switchNames[portgroup.Config.DistributedVirtualSwitch.Value]
		}
		inventory.Networks = append(inventory.Networks, item)
	}
	var networks []mo.Network
	if err := container.Retrieve(ctx, []string{"Network"}, []string{"name", "vm"}, &networks); err != nil {
		return err
	}
	for _, network := range networks {
		reference := network.Reference()
		if seenNetworks[reference.Value] || reference.Type == "DistributedVirtualPortgroup" {
			continue
		}
		kind := "Standard"
		if reference.Type == "OpaqueNetwork" {
			kind = "NSX"
		}
		vlans := []string{}
		for vlan := range standardVLANs[network.Name] {
			vlans = append(vlans, vlan)
		}
		sort.Strings(vlans)
		item := vmNetwork{Name: network.Name, Datacenter: datacenter.Name, Kind: kind, Switch: standardSwitches[network.Name], VLAN: strings.Join(vlans, ", "), VMCount: len(network.Vm)}
		inventory.Networks = append(inventory.Networks, item)
	}

	var machines []mo.VirtualMachine
	if err := container.Retrieve(ctx, []string{"VirtualMachine"}, []string{"name", "config", "summary", "guest", "snapshot", "runtime", "parent"}, &machines); err != nil {
		return err
	}
	folders := map[string]string{}
	var folderObjects []mo.Folder
	if err := container.Retrieve(ctx, []string{"Folder"}, []string{"name"}, &folderObjects); err == nil {
		for _, folder := range folderObjects {
			folders[folder.Reference().Value] = folder.Name
		}
	}
	for _, machine := range machines {
		vm := vmFromManagedObject(machine, datacenter.Name, hostNames, hostClusters, datastoreNames, portgroupNames, folders)
		if machine.Config == nil {
			inventory.Warnings = append(inventory.Warnings, fmt.Sprintf("%s: its configuration could not be read (the VM may be orphaned or inaccessible).", machine.Name))
		}
		inventory.VMs = append(inventory.VMs, vm)
	}
	progress(fmt.Sprintf("Read %d VM(s) and template(s) in %s.", len(machines), datacenter.Name))
	return nil
}

func vmIsMissingType(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "invalidtype")
}

func vmStandardVLAN(id int32) string {
	switch {
	case id == 0:
		return "None"
	case id == 4095:
		return "Trunk (all)"
	}
	return strconv.Itoa(int(id))
}

func vmDistributedVLAN(setting types.BaseDVPortSetting) string {
	port, ok := setting.(*types.VMwareDVSPortSetting)
	if !ok || port == nil || port.Vlan == nil {
		return ""
	}
	switch vlan := port.Vlan.(type) {
	case *types.VmwareDistributedVirtualSwitchVlanIdSpec:
		if vlan.VlanId == 0 {
			return "None"
		}
		return strconv.Itoa(int(vlan.VlanId))
	case *types.VmwareDistributedVirtualSwitchTrunkVlanSpec:
		ranges := []string{}
		for _, item := range vlan.VlanId {
			if item.Start == item.End {
				ranges = append(ranges, strconv.Itoa(int(item.Start)))
			} else {
				ranges = append(ranges, fmt.Sprintf("%d-%d", item.Start, item.End))
			}
		}
		return "Trunk " + strings.Join(ranges, ",")
	case *types.VmwareDistributedVirtualSwitchPvlanSpec:
		return "Private VLAN " + strconv.Itoa(int(vlan.PvlanId))
	}
	return ""
}

func vmControllerName(device types.BaseVirtualDevice) (string, string) {
	switch controller := device.(type) {
	case *types.ParaVirtualSCSIController:
		return "pvscsi", string(controller.SharedBus)
	case *types.VirtualLsiLogicController:
		return "lsilogic", string(controller.SharedBus)
	case *types.VirtualLsiLogicSASController:
		return "lsilogic-sas", string(controller.SharedBus)
	case *types.VirtualBusLogicController:
		return "buslogic", string(controller.SharedBus)
	case *types.VirtualIDEController:
		return "ide", ""
	case *types.VirtualAHCIController:
		return "sata", ""
	case *types.VirtualNVMEController:
		return "nvme", ""
	}
	return "", ""
}

func vmAdapterType(device types.BaseVirtualDevice) string {
	switch device.(type) {
	case *types.VirtualVmxnet3Vrdma:
		return "vmxnet3-rdma"
	case *types.VirtualVmxnet3:
		return "vmxnet3"
	case *types.VirtualVmxnet2:
		return "vmxnet2"
	case *types.VirtualVmxnet:
		return "vmxnet"
	case *types.VirtualE1000e:
		return "e1000e"
	case *types.VirtualE1000:
		return "e1000"
	case *types.VirtualPCNet32:
		return "pcnet32"
	case *types.VirtualSriovEthernetCard:
		return "sr-iov"
	}
	return "ethernet"
}

var vmDatastorePath = regexp.MustCompile(`^\[([^\]]*)\]\s*(.*)$`)

func vmFromManagedObject(machine mo.VirtualMachine, datacenter string, hostNames, hostClusters, datastoreNames, portgroupNames, folders map[string]string) vmMachine {
	vm := vmMachine{Name: machine.Name, ID: machine.Reference().Value, Datacenter: datacenter}
	vm.PowerState = string(machine.Runtime.PowerState)
	if machine.Runtime.Host != nil {
		vm.Host = hostNames[machine.Runtime.Host.Value]
		vm.Cluster = hostClusters[machine.Runtime.Host.Value]
	}
	if machine.Parent != nil {
		vm.Folder = folders[machine.Parent.Value]
	}
	if machine.Runtime.ConsolidationNeeded != nil {
		vm.ConsolidationNeeded = *machine.Runtime.ConsolidationNeeded
	}
	vm.FaultTolerance = machine.Runtime.FaultToleranceState != "" && machine.Runtime.FaultToleranceState != types.VirtualMachineFaultToleranceStateNotConfigured
	if storage := machine.Summary.Storage; storage != nil {
		vm.UsedBytes = storage.Committed
		vm.ProvisionedBytes = storage.Committed + storage.Uncommitted
	}

	guestNICs := map[int32]types.GuestNicInfo{}
	if guest := machine.Guest; guest != nil {
		vm.GuestOSRunning = guest.GuestFullName
		vm.GuestHostName = guest.HostName
		vm.IPAddress = guest.IpAddress
		vm.ToolsStatus = guest.ToolsVersionStatus2
		vm.ToolsRunning = guest.ToolsRunningStatus == string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)
		vm.ToolsVersion = vmToolsVersionText(guest.ToolsVersion)
		for _, nic := range guest.Net {
			guestNICs[nic.DeviceConfigId] = nic
		}
		for _, disk := range guest.Disk {
			vm.GuestDisks = append(vm.GuestDisks, vmGuestDisk{Path: disk.DiskPath, CapacityBytes: disk.Capacity, FreeBytes: disk.FreeSpace})
		}
	}

	if machine.Snapshot != nil {
		var walk func(items []types.VirtualMachineSnapshotTree, depth int)
		walk = func(items []types.VirtualMachineSnapshotTree, depth int) {
			for _, item := range items {
				vm.Snapshots = append(vm.Snapshots, vmSnapshot{Name: item.Name, Created: item.CreateTime, Quiesced: item.Quiesced, Depth: depth})
				walk(item.ChildSnapshotList, depth+1)
			}
		}
		walk(machine.Snapshot.RootSnapshotList, 0)
	}

	config := machine.Config
	if config == nil {
		vm.GuestOS = machine.Summary.Config.GuestFullName
		vm.Template = machine.Summary.Config.Template
		vm.CPUs = int(machine.Summary.Config.NumCpu)
		vm.MemoryMB = int64(machine.Summary.Config.MemorySizeMB)
		return vm
	}
	vm.InstanceUUID = config.InstanceUuid
	vm.Template = config.Template
	vm.GuestID = config.GuestId
	vm.GuestOS = config.GuestFullName
	vm.HardwareVersion = config.Version
	vm.Firmware = config.Firmware
	if config.BootOptions != nil && config.BootOptions.EfiSecureBootEnabled != nil {
		vm.SecureBoot = *config.BootOptions.EfiSecureBootEnabled
	}
	vm.Encrypted = config.KeyId != nil
	if config.ChangeTrackingEnabled != nil {
		vm.ChangeTracking = *config.ChangeTrackingEnabled
	}
	vm.CPUs = int(config.Hardware.NumCPU)
	vm.CoresPerSocket = int(config.Hardware.NumCoresPerSocket)
	vm.MemoryMB = int64(config.Hardware.MemoryMB)

	controllers := map[int32][2]string{}
	controllerBus := map[int32]int{}
	for _, device := range config.Hardware.Device {
		if name, sharing := vmControllerName(device); name != "" {
			key := device.GetVirtualDevice().Key
			controllers[key] = [2]string{name, sharing}
			if controller, ok := device.(types.BaseVirtualController); ok {
				controllerBus[key] = int(controller.GetVirtualController().BusNumber)
			}
		}
	}
	label := func(device *types.VirtualDevice) string {
		if device.DeviceInfo != nil {
			if info := device.DeviceInfo.GetDescription(); info != nil && info.Label != "" {
				return info.Label
			}
		}
		return fmt.Sprintf("Device %d", device.Key)
	}
	connected := func(device *types.VirtualDevice) bool {
		return device.Connectable != nil && device.Connectable.Connected
	}
	firstDisk := true
	for _, device := range config.Hardware.Device {
		base := device.GetVirtualDevice()
		switch item := device.(type) {
		case *types.VirtualDisk:
			disk := vmDisk{Label: label(base), CapacityBytes: item.CapacityInBytes, Provisioning: "thick"}
			if disk.CapacityBytes == 0 {
				disk.CapacityBytes = item.CapacityInKB * 1024
			}
			if controller, ok := controllers[base.ControllerKey]; ok {
				disk.Controller, disk.BusSharing = controller[0], controller[1]
				disk.Bus = controllerBus[base.ControllerKey]
			}
			if base.UnitNumber != nil {
				disk.Unit = int(*base.UnitNumber)
			}
			var datastoreRef *types.ManagedObjectReference
			switch backing := base.Backing.(type) {
			case *types.VirtualDiskFlatVer2BackingInfo:
				disk.File, disk.Mode, disk.Sharing, datastoreRef = backing.FileName, backing.DiskMode, backing.Sharing, backing.Datastore
				switch {
				case backing.ThinProvisioned != nil && *backing.ThinProvisioned:
					disk.Provisioning = "thin"
				case backing.EagerlyScrub != nil && *backing.EagerlyScrub:
					disk.Provisioning = "thick eager zeroed"
				default:
					disk.Provisioning = "thick lazy zeroed"
				}
			case *types.VirtualDiskRawDiskMappingVer1BackingInfo:
				disk.File, disk.Mode, disk.Sharing, datastoreRef = backing.FileName, backing.DiskMode, backing.Sharing, backing.Datastore
				disk.Provisioning = "raw device mapping"
				disk.RDM = "virtual"
				if backing.CompatibilityMode == string(types.VirtualDiskCompatibilityModePhysicalMode) {
					disk.RDM = "physical"
				}
			case *types.VirtualDiskSeSparseBackingInfo:
				disk.File, disk.Mode, datastoreRef = backing.FileName, backing.DiskMode, backing.Datastore
				disk.Provisioning = "thin (SE sparse)"
			case *types.VirtualDiskSparseVer2BackingInfo:
				disk.File, disk.Mode, datastoreRef = backing.FileName, backing.DiskMode, backing.Datastore
				disk.Provisioning = "sparse"
			case types.BaseVirtualDeviceFileBackingInfo:
				file := backing.GetVirtualDeviceFileBackingInfo()
				disk.File, datastoreRef = file.FileName, file.Datastore
			}
			if datastoreRef != nil {
				disk.Datastore = datastoreNames[datastoreRef.Value]
			}
			if disk.Datastore == "" {
				if match := vmDatastorePath.FindStringSubmatch(disk.File); match != nil {
					disk.Datastore = match[1]
				}
			}
			disk.File = path.Base(strings.ReplaceAll(vmDatastorePath.ReplaceAllString(disk.File, "$2"), `\`, "/"))
			disk.BootCandidate = firstDisk
			firstDisk = false
			vm.Disks = append(vm.Disks, disk)
		case types.BaseVirtualEthernetCard:
			card := item.GetVirtualEthernetCard()
			nic := vmNIC{Label: label(base), AdapterType: vmAdapterType(device), MAC: card.MacAddress, ManualMAC: strings.EqualFold(card.AddressType, string(types.VirtualEthernetCardMacTypeManual)), Connected: connected(base), IPAddresses: []string{}}
			switch backing := base.Backing.(type) {
			case *types.VirtualEthernetCardNetworkBackingInfo:
				nic.Network = backing.DeviceName
			case *types.VirtualEthernetCardDistributedVirtualPortBackingInfo:
				nic.Network = portgroupNames[backing.Port.PortgroupKey]
				if nic.Network == "" {
					nic.Network = backing.Port.PortgroupKey
				}
			case *types.VirtualEthernetCardOpaqueNetworkBackingInfo:
				nic.Network = backing.OpaqueNetworkId
			}
			if guestNIC, ok := guestNICs[base.Key]; ok {
				nic.GuestKnown = true
				if guestNIC.IpConfig != nil {
					for _, address := range guestNIC.IpConfig.IpAddress {
						if strings.HasPrefix(strings.ToLower(address.IpAddress), "fe80:") {
							continue
						}
						nic.IPAddresses = append(nic.IPAddresses, fmt.Sprintf("%s/%d", address.IpAddress, address.PrefixLength))
						if strings.EqualFold(address.Origin, "manual") {
							nic.Static = true
						}
					}
					if dhcp := guestNIC.IpConfig.Dhcp; dhcp != nil && dhcp.Ipv4 != nil && !dhcp.Ipv4.Enable && len(nic.IPAddresses) > 0 {
						nic.Static = true
					}
				} else {
					nic.IPAddresses = append(nic.IPAddresses, guestNIC.IpAddress...)
				}
			}
			vm.NICs = append(vm.NICs, nic)
		case *types.VirtualCdrom:
			if !connected(base) {
				continue
			}
			switch backing := base.Backing.(type) {
			case *types.VirtualCdromIsoBackingInfo:
				vm.ConnectedMedia = append(vm.ConnectedMedia, path.Base(vmDatastorePath.ReplaceAllString(backing.FileName, "$2")))
			default:
				vm.ConnectedMedia = append(vm.ConnectedMedia, label(base)+" (host or client device)")
			}
		case *types.VirtualFloppy:
			if connected(base) {
				vm.ConnectedMedia = append(vm.ConnectedMedia, label(base))
			}
		case *types.VirtualPCIPassthrough:
			name := label(base)
			if _, ok := base.Backing.(*types.VirtualPCIPassthroughVmiopBackingInfo); ok {
				name += " (vGPU)"
			}
			vm.Passthrough = append(vm.Passthrough, name)
		case *types.VirtualUSB:
			vm.USBDevices++
		case *types.VirtualSerialPort:
			vm.SerialPorts++
		case *types.VirtualParallelPort:
			vm.ParallelPorts++
		case *types.VirtualTPM:
			vm.VTPM = true
		}
	}
	return vm
}
