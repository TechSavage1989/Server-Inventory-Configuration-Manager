package main

// VMware ESXi or vCenter as a Disk converter destination: the converted
// disk, written as a stream-optimized VMDK, is imported through an OVF
// import lease (ImportVApp), which creates a new VM with that disk in one
// step, the way an OVF deployment does. Nothing existing is changed: a VM
// with the same name is refused before anything is sent, and the new VM is
// left powered off unless the operator asked to start it.

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/template"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/ovf"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

type vmVSphereImport struct {
	Name       string `json:"name"`
	Datacenter string `json:"datacenter"`
	// Compute is a cluster or host name; empty uses the datacenter's only
	// compute resource.
	Compute    string `json:"compute"`
	Datastore  string `json:"datastore"`
	Network    string `json:"network"`
	CPUs       int    `json:"cpus"`
	MemoryMiB  int64  `json:"memoryMiB"`
	Firmware   string `json:"firmware"` // bios or uefi
	SecureBoot bool   `json:"secureBoot"`
	GuestID    string `json:"guestId"`
	// Controller is lsilogicsas or pvscsi; NIC is vmxnet3 or e1000e.
	Controller string `json:"controller"`
	NIC        string `json:"nic"`
	Thick      bool   `json:"thick"`
	DiskSize   int64  `json:"diskSize"`
	Start      bool   `json:"start"`
}

var vmVSphereGuestPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// vmVSphereValidate fills defaults and refuses values vSphere would take
// badly.
func vmVSphereValidate(spec *vmVSphereImport) error {
	if !vmTargetNamePattern.MatchString(spec.Name) {
		return errors.New("the VM name must be 1 to 63 letters, digits, dots, dashes or underscores")
	}
	if spec.CPUs < 1 || spec.CPUs > 768 {
		return errors.New("choose between 1 and 768 virtual CPUs")
	}
	if spec.MemoryMiB < 4 || spec.MemoryMiB > 24<<20 {
		return errors.New("choose at least 4 MiB of memory")
	}
	if spec.MemoryMiB%4 != 0 {
		spec.MemoryMiB += 4 - spec.MemoryMiB%4
	}
	switch spec.Firmware {
	case "", "bios":
		spec.Firmware = "bios"
		if spec.SecureBoot {
			return errors.New("Secure Boot needs UEFI firmware")
		}
	case "uefi":
	default:
		return errors.New("firmware must be BIOS or UEFI")
	}
	switch spec.Controller {
	case "":
		spec.Controller = "lsilogicsas"
	case "lsilogicsas", "pvscsi":
	default:
		return errors.New("the disk controller must be LSI Logic SAS or VMware Paravirtual")
	}
	switch spec.NIC {
	case "":
		spec.NIC = "vmxnet3"
	case "vmxnet3", "e1000e":
	default:
		return errors.New("the network adapter must be VMXNET 3 or E1000E")
	}
	if spec.GuestID == "" {
		spec.GuestID = "otherGuest64"
	}
	if !vmVSphereGuestPattern.MatchString(spec.GuestID) {
		return errors.New("the guest OS ID is not valid")
	}
	if spec.Datastore == "" {
		return errors.New("choose the datastore for the new VM")
	}
	if spec.DiskSize <= 0 {
		return errors.New("the disk has no size")
	}
	return nil
}

var vmVSphereOVFTemplate = template.Must(template.New("ovf").Funcs(template.FuncMap{"x": func(value interface{}) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(fmt.Sprint(value)))
	return b.String()
}}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1" xmlns:rasd="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_ResourceAllocationSettingData" xmlns:vmw="http://www.vmware.com/schema/ovf" xmlns:vssd="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_VirtualSystemSettingData">
  <References><File ovf:href="{{x .File}}" ovf:id="file1" ovf:size="{{.FileSize}}"/></References>
  <DiskSection><Info>Virtual disks</Info><Disk ovf:capacity="{{.Spec.DiskSize}}" ovf:capacityAllocationUnits="byte" ovf:diskId="vmdisk1" ovf:fileRef="file1" ovf:format="http://www.vmware.com/interfaces/specifications/vmdk.html#streamOptimized"/></DiskSection>
  {{if .Spec.Network}}<NetworkSection><Info>Networks</Info><Network ovf:name="network1"><Description>The VM network</Description></Network></NetworkSection>{{end}}
  <VirtualSystem ovf:id="{{x .Spec.Name}}">
    <Info>A virtual machine</Info>
    <Name>{{x .Spec.Name}}</Name>
    <OperatingSystemSection ovf:id="1" vmw:osType="{{x .Spec.GuestID}}"><Info>Guest operating system</Info></OperatingSystemSection>
    <VirtualHardwareSection>
      <Info>Virtual hardware</Info>
      <System><vssd:ElementName>Virtual Hardware Family</vssd:ElementName><vssd:InstanceID>0</vssd:InstanceID><vssd:VirtualSystemIdentifier>{{x .Spec.Name}}</vssd:VirtualSystemIdentifier><vssd:VirtualSystemType>vmx-13</vssd:VirtualSystemType></System>
      <Item><rasd:AllocationUnits>hertz * 10^6</rasd:AllocationUnits><rasd:Description>Number of Virtual CPUs</rasd:Description><rasd:ElementName>{{.Spec.CPUs}} virtual CPU(s)</rasd:ElementName><rasd:InstanceID>1</rasd:InstanceID><rasd:ResourceType>3</rasd:ResourceType><rasd:VirtualQuantity>{{.Spec.CPUs}}</rasd:VirtualQuantity></Item>
      <Item><rasd:AllocationUnits>byte * 2^20</rasd:AllocationUnits><rasd:Description>Memory Size</rasd:Description><rasd:ElementName>{{.Spec.MemoryMiB}}MB of memory</rasd:ElementName><rasd:InstanceID>2</rasd:InstanceID><rasd:ResourceType>4</rasd:ResourceType><rasd:VirtualQuantity>{{.Spec.MemoryMiB}}</rasd:VirtualQuantity></Item>
      <Item><rasd:Address>0</rasd:Address><rasd:Description>SCSI Controller</rasd:Description><rasd:ElementName>SCSI Controller 0</rasd:ElementName><rasd:InstanceID>3</rasd:InstanceID><rasd:ResourceSubType>{{if eq .Spec.Controller "pvscsi"}}VirtualSCSI{{else}}lsilogicsas{{end}}</rasd:ResourceSubType><rasd:ResourceType>6</rasd:ResourceType></Item>
      <Item><rasd:AddressOnParent>0</rasd:AddressOnParent><rasd:ElementName>Hard Disk 1</rasd:ElementName><rasd:HostResource>ovf:/disk/vmdisk1</rasd:HostResource><rasd:InstanceID>4</rasd:InstanceID><rasd:Parent>3</rasd:Parent><rasd:ResourceType>17</rasd:ResourceType></Item>
      {{if .Spec.Network}}<Item><rasd:AddressOnParent>7</rasd:AddressOnParent><rasd:AutomaticAllocation>true</rasd:AutomaticAllocation><rasd:Connection>network1</rasd:Connection><rasd:ElementName>Network adapter 1</rasd:ElementName><rasd:InstanceID>5</rasd:InstanceID><rasd:ResourceSubType>{{if eq .Spec.NIC "e1000e"}}E1000e{{else}}VmxNet3{{end}}</rasd:ResourceSubType><rasd:ResourceType>10</rasd:ResourceType></Item>{{end}}
      {{if eq .Spec.Firmware "uefi"}}<vmw:Config ovf:required="false" vmw:key="firmware" vmw:value="efi"/>{{end}}
      {{if .Spec.SecureBoot}}<vmw:Config ovf:required="false" vmw:key="bootOptions.efiSecureBootEnabled" vmw:value="true"/>{{end}}
    </VirtualHardwareSection>
    <AnnotationSection><Info>Notes</Info><Annotation>Converted by Server Inventory and Migration Manager.</Annotation></AnnotationSection>
  </VirtualSystem>
</Envelope>`))

// vmVSphereOVF is the descriptor for one VM with one stream-optimized disk.
func vmVSphereOVF(spec vmVSphereImport, file string, fileSize int64) (string, error) {
	var b bytes.Buffer
	err := vmVSphereOVFTemplate.Execute(&b, map[string]interface{}{"Spec": spec, "File": file, "FileSize": fileSize})
	return b.String(), err
}

// vmVSphereChoices lists what the import can use, read-only.
type vmVSphereChoices struct {
	Product     string           `json:"product"`
	Datacenters []string         `json:"datacenters"`
	Computes    []vmTargetChoice `json:"computes"`
	Datastores  []vmTargetChoice `json:"datastores"`
	Networks    []vmTargetChoice `json:"networks"`
}

func vmVSphereReadChoices(ctx context.Context, client *vim25.Client, datacenter string) (*vmVSphereChoices, error) {
	finder := find.NewFinder(client, true)
	choices := &vmVSphereChoices{Product: client.ServiceContent.About.FullName}
	datacenters, err := finder.DatacenterList(ctx, "*")
	if err != nil {
		return nil, err
	}
	for _, item := range datacenters {
		choices.Datacenters = append(choices.Datacenters, item.Name())
	}
	dc, err := vmVSphereDatacenter(ctx, finder, datacenter)
	if err != nil {
		return choices, nil
	}
	finder.SetDatacenter(dc)
	if computes, err := finder.ComputeResourceList(ctx, "*"); err == nil {
		for _, item := range computes {
			choices.Computes = append(choices.Computes, vmTargetChoice{ID: item.Name(), Name: item.Name()})
		}
	}
	if stores, err := finder.DatastoreList(ctx, "*"); err == nil {
		for _, item := range stores {
			choices.Datastores = append(choices.Datastores, vmTargetChoice{ID: item.Name(), Name: item.Name()})
		}
	}
	if networks, err := finder.NetworkList(ctx, "*"); err == nil {
		for _, item := range networks {
			name := item.GetInventoryPath()
			if index := strings.LastIndex(name, "/"); index >= 0 {
				name = name[index+1:]
			}
			choices.Networks = append(choices.Networks, vmTargetChoice{ID: name, Name: name})
		}
	}
	return choices, nil
}

func vmVSphereDatacenter(ctx context.Context, finder *find.Finder, name string) (*object.Datacenter, error) {
	if name == "" {
		return finder.DefaultDatacenter(ctx)
	}
	return finder.Datacenter(ctx, name)
}

// vmVSphereImportDisk creates the VM from the stream-optimized VMDK at
// streamPath and returns its managed object ID.
func vmVSphereImportDisk(ctx context.Context, client *vim25.Client, spec vmVSphereImport, streamPath string, progress func(string)) (string, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if err := vmVSphereValidate(&spec); err != nil {
		return "", err
	}
	finder := find.NewFinder(client, true)
	dc, err := vmVSphereDatacenter(ctx, finder, spec.Datacenter)
	if err != nil {
		return "", fmt.Errorf("the datacenter could not be found: %w", err)
	}
	finder.SetDatacenter(dc)
	if existing, err := finder.VirtualMachineList(ctx, spec.Name); err == nil && len(existing) > 0 {
		return "", fmt.Errorf("a VM named %s already exists in %s; choose another name. Nothing was changed", spec.Name, dc.Name())
	}
	datastore, err := finder.Datastore(ctx, spec.Datastore)
	if err != nil {
		return "", fmt.Errorf("the datastore %s could not be found: %w", spec.Datastore, err)
	}
	var pool *object.ResourcePool
	if spec.Compute != "" {
		compute, err := finder.ComputeResource(ctx, spec.Compute)
		if err != nil {
			return "", fmt.Errorf("the cluster or host %s could not be found: %w", spec.Compute, err)
		}
		if pool, err = compute.ResourcePool(ctx); err != nil {
			return "", err
		}
	} else if pool, err = finder.DefaultResourcePool(ctx); err != nil {
		return "", fmt.Errorf("choose the cluster or host for the new VM: %w", err)
	}
	folders, err := dc.Folders(ctx)
	if err != nil {
		return "", err
	}
	params := types.OvfCreateImportSpecParams{EntityName: spec.Name, DiskProvisioning: string(types.VirtualDiskTypeThin)}
	if spec.Thick {
		params.DiskProvisioning = string(types.VirtualDiskTypeEagerZeroedThick)
	}
	if spec.Network != "" {
		network, err := finder.Network(ctx, spec.Network)
		if err != nil {
			return "", fmt.Errorf("the network %s could not be found: %w", spec.Network, err)
		}
		params.NetworkMapping = []types.OvfNetworkMapping{{Name: "network1", Network: network.Reference()}}
	}
	info, err := os.Stat(streamPath)
	if err != nil {
		return "", err
	}
	descriptor, err := vmVSphereOVF(spec, spec.Name+"-disk1.vmdk", info.Size())
	if err != nil {
		return "", err
	}
	importSpec, err := ovf.NewManager(client).CreateImportSpec(ctx, descriptor, pool, datastore, params)
	if err != nil {
		return "", err
	}
	if importSpec.Error != nil {
		return "", errors.New(importSpec.Error[0].LocalizedMessage)
	}
	for _, warning := range importSpec.Warning {
		progress("vSphere: " + warning.LocalizedMessage)
	}
	progress("Creating " + spec.Name + " on " + datastore.Name() + ".")
	lease, err := pool.ImportVApp(ctx, importSpec.ImportSpec, folders.VmFolder, nil)
	if err != nil {
		return "", err
	}
	leaseInfo, err := lease.Wait(ctx, importSpec.FileItem)
	if err != nil {
		return "", err
	}
	abort := func(cause error) (string, error) {
		_ = lease.Abort(context.Background(), &types.LocalizedMethodFault{LocalizedMessage: cause.Error()})
		return "", cause
	}
	if len(leaseInfo.Items) != 1 {
		return abort(fmt.Errorf("vSphere asked for %d files instead of the one disk", len(leaseInfo.Items)))
	}
	file, err := os.Open(streamPath)
	if err != nil {
		return abort(err)
	}
	defer file.Close()
	updater := lease.StartUpdater(ctx, leaseInfo)
	progress(fmt.Sprintf("Sending the disk (%s).", vmFormatBytes(info.Size())))
	err = lease.Upload(ctx, leaseInfo.Items[0], io.Reader(file), soap.Upload{ContentLength: info.Size()})
	updater.Done()
	if err != nil {
		return abort(fmt.Errorf("sending the disk stopped: %w", err))
	}
	if err := lease.Complete(ctx); err != nil {
		return "", err
	}
	vm := object.NewVirtualMachine(client, leaseInfo.Entity)
	progress("Created " + spec.Name + ".")
	if spec.Start {
		progress("Starting " + spec.Name + ".")
		task, err := vm.PowerOn(ctx)
		if err == nil {
			err = task.Wait(ctx)
		}
		if err != nil {
			return leaseInfo.Entity.Value, fmt.Errorf("%s was created but did not start: %w", spec.Name, err)
		}
	}
	return leaseInfo.Entity.Value, nil
}
