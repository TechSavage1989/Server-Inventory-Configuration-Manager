package main

// Nutanix AHV through the Prism Central v3 REST API
// (https://prism-central:9440/api/nutanix/v3) with basic authentication.
// The new VM gets its disks on the cluster's default container (or the
// chosen one), the receiver ISO image as a CD-ROM, and boot order CD first;
// cutover powers it off, sets the boot order to disk only and powers it on.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type vmNutanix struct {
	http    *vmTargetHTTP
	cluster string
}

func newVMNutanix(connection vmTargetConnection) (*vmNutanix, error) {
	if connection.User == "" || connection.Password == "" {
		return nil, errors.New("enter the Prism Central user name and password")
	}
	h, err := newVMTargetHTTP(connection, "9440", "/api/nutanix/v3")
	if err != nil {
		return nil, err
	}
	user, password := connection.User, connection.Password
	h.authorize = func(request *http.Request) { request.SetBasicAuth(user, password) }
	return &vmNutanix{http: h, cluster: connection.Node}, nil
}

type vmNutanixEntity struct {
	Metadata map[string]interface{} `json:"metadata"`
	Spec     map[string]interface{} `json:"spec"`
	Status   map[string]interface{} `json:"status"`
}

func (n *vmNutanix) list(ctx context.Context, kind, filter string) ([]vmNutanixEntity, error) {
	body := map[string]interface{}{"kind": kind, "length": 500}
	if filter != "" {
		body["filter"] = filter
	}
	var answer struct {
		Entities []vmNutanixEntity `json:"entities"`
	}
	err := n.http.do(ctx, http.MethodPost, "/"+kind+"s/list", nil, body, "", 0, &answer)
	return answer.Entities, err
}

func vmNutanixString(value map[string]interface{}, path ...string) string {
	var current interface{} = value
	for _, key := range path {
		object, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current = object[key]
	}
	if text, ok := current.(string); ok {
		return text
	}
	return ""
}

func (n *vmNutanix) waitTask(ctx context.Context, task string) error {
	if task == "" {
		return nil
	}
	for {
		var status struct {
			Status       string        `json:"status"`
			ErrorDetail  string        `json:"error_detail"`
			ProgressMsgs []interface{} `json:"progress_message"`
		}
		if err := n.http.do(ctx, http.MethodGet, "/tasks/"+task, nil, nil, "", 0, &status); err != nil {
			return err
		}
		switch status.Status {
		case "SUCCEEDED":
			return nil
		case "FAILED", "ABORTED":
			return fmt.Errorf("the Nutanix task failed: %s", status.ErrorDetail)
		}
		if err := n.http.sleep(ctx); err != nil {
			return err
		}
	}
}

func vmNutanixTask(answer map[string]interface{}) string {
	status, _ := answer["status"].(map[string]interface{})
	return vmNutanixString(status, "execution_context", "task_uuid")
}

func (n *vmNutanix) clusterUUID(ctx context.Context) (string, error) {
	clusters, err := n.list(ctx, "cluster", "")
	if err != nil {
		return "", err
	}
	for _, cluster := range clusters {
		name := vmNutanixString(cluster.Status, "name")
		if name == "Unnamed" || strings.Contains(strings.ToUpper(strings.Join(vmNutanixServices(cluster), ",")), "PRISM_CENTRAL") {
			continue
		}
		if n.cluster == "" || strings.EqualFold(name, n.cluster) {
			return vmNutanixString(cluster.Metadata, "uuid"), nil
		}
	}
	if n.cluster == "" {
		return "", errors.New("Prism Central lists no AHV cluster")
	}
	return "", fmt.Errorf("the cluster %s was not found in Prism Central", n.cluster)
}

func vmNutanixServices(cluster vmNutanixEntity) []string {
	services := []string{}
	resources, _ := cluster.Status["resources"].(map[string]interface{})
	config, _ := resources["config"].(map[string]interface{})
	list, _ := config["service_list"].([]interface{})
	for _, item := range list {
		services = append(services, fmt.Sprint(item))
	}
	return services
}

func (n *vmNutanix) Inventory(ctx context.Context) (*vmTargetInventory, error) {
	inventory := &vmTargetInventory{Platform: vmTargetNutanix, Version: "Prism Central (v3 API)"}
	clusters, err := n.list(ctx, "cluster", "")
	if err != nil {
		return nil, err
	}
	for _, cluster := range clusters {
		if strings.Contains(strings.ToUpper(strings.Join(vmNutanixServices(cluster), ",")), "PRISM_CENTRAL") {
			continue
		}
		name := vmNutanixString(cluster.Status, "name")
		resources, _ := cluster.Status["resources"].(map[string]interface{})
		config, _ := resources["config"].(map[string]interface{})
		build, _ := config["build"].(map[string]interface{})
		detail := ""
		if version, _ := build["version"].(string); version != "" {
			detail = "AOS " + version
		}
		inventory.Nodes = append(inventory.Nodes, vmTargetChoice{ID: vmNutanixString(cluster.Metadata, "uuid"), Name: name, Detail: detail})
	}
	subnets, err := n.list(ctx, "subnet", "")
	if err != nil {
		return nil, err
	}
	for _, subnet := range subnets {
		resources, _ := subnet.Status["resources"].(map[string]interface{})
		detail := ""
		if vlan, ok := resources["vlan_id"].(float64); ok {
			detail = fmt.Sprintf("VLAN %d", int(vlan))
		}
		if cluster := vmNutanixString(subnet.Status, "cluster_reference", "name"); cluster != "" {
			detail = strings.TrimSpace(detail + " on " + cluster)
		}
		inventory.Networks = append(inventory.Networks, vmTargetChoice{ID: vmNutanixString(subnet.Metadata, "uuid"), Name: vmNutanixString(subnet.Status, "name"), Detail: detail})
	}
	return inventory, nil
}

func (n *vmNutanix) UploadISO(ctx context.Context, name string, data io.Reader, size int64) (string, error) {
	if !strings.HasSuffix(strings.ToLower(name), ".iso") || strings.ContainsAny(name, `/\"`) {
		return "", errors.New("name the ISO image file.iso")
	}
	existing, err := n.list(ctx, "image", "name=="+name)
	if err != nil {
		return "", err
	}
	for _, image := range existing {
		if vmNutanixString(image.Status, "name") == name || vmNutanixString(image.Spec, "name") == name {
			return "", fmt.Errorf("an image named %s already exists; choose a new name so nothing is overwritten", name)
		}
	}
	var created map[string]interface{}
	body := map[string]interface{}{
		"spec":     map[string]interface{}{"name": name, "description": "Server Inventory and Migration Manager receiver media", "resources": map[string]interface{}{"image_type": "ISO_IMAGE"}},
		"metadata": map[string]interface{}{"kind": "image"},
	}
	if err := n.http.do(ctx, http.MethodPost, "/images", nil, body, "", 0, &created); err != nil {
		return "", err
	}
	metadata, _ := created["metadata"].(map[string]interface{})
	uuid := vmNutanixString(metadata, "uuid")
	if uuid == "" {
		return "", errors.New("Prism Central did not return the new image's ID")
	}
	if err := n.waitTask(ctx, vmNutanixTask(created)); err != nil {
		return "", err
	}
	if err := n.http.do(ctx, http.MethodPut, "/images/"+uuid+"/file", nil, io.LimitReader(data, size), "application/octet-stream", size, nil); err != nil {
		return "", err
	}
	return uuid, nil
}

func (n *vmNutanix) FindVM(ctx context.Context, name string) (*vmTargetVM, error) {
	vms, err := n.list(ctx, "vm", "vm_name=="+name)
	if err != nil {
		return nil, err
	}
	for _, vm := range vms {
		if strings.EqualFold(vmNutanixString(vm.Status, "name"), name) || strings.EqualFold(vmNutanixString(vm.Spec, "name"), name) {
			return &vmTargetVM{ID: vmNutanixString(vm.Metadata, "uuid"), Name: name, State: strings.ToLower(vmNutanixString(vm.Status, "resources", "power_state"))}, nil
		}
	}
	return nil, nil
}

func vmNutanixAdapter(bus string) string {
	switch bus {
	case "sata":
		return "SATA"
	case "ide":
		return "IDE"
	}
	// AHV attaches VirtIO disks as SCSI (virtio-scsi).
	return "SCSI"
}

// vmNutanixVMBody builds the v3 VM create body.
func vmNutanixVMBody(cluster string, spec vmTargetVMSpec) (map[string]interface{}, error) {
	disks := []interface{}{}
	counts := map[string]int{}
	for _, disk := range spec.Disks {
		adapter := vmNutanixAdapter(disk.Bus)
		item := map[string]interface{}{
			"device_properties": map[string]interface{}{"device_type": "DISK", "disk_address": map[string]interface{}{"adapter_type": adapter, "device_index": counts[adapter]}},
			"disk_size_mib":     vmCeilDiv(disk.Size, 1<<20),
		}
		if disk.Import != "" {
			item["data_source_reference"] = map[string]interface{}{"kind": "image", "uuid": disk.Import}
		}
		if spec.Storage != "" {
			item["storage_config"] = map[string]interface{}{"storage_container_reference": map[string]interface{}{"kind": "storage_container", "uuid": spec.Storage}}
		}
		disks = append(disks, item)
		counts[adapter]++
	}
	if spec.ISO != "" {
		cdromAdapter := "SATA"
		if spec.Firmware == "bios" {
			cdromAdapter = "IDE"
		}
		disks = append(disks, map[string]interface{}{
			"device_properties":     map[string]interface{}{"device_type": "CDROM", "disk_address": map[string]interface{}{"adapter_type": cdromAdapter, "device_index": counts[cdromAdapter]}},
			"data_source_reference": map[string]interface{}{"kind": "image", "uuid": spec.ISO},
		})
	}
	nics := []interface{}{}
	for index, nic := range spec.NICs {
		if nic.Network == "" {
			return nil, fmt.Errorf("adapter %d has no subnet", index)
		}
		item := map[string]interface{}{"subnet_reference": map[string]interface{}{"kind": "subnet", "uuid": nic.Network}, "nic_type": "NORMAL_NIC"}
		if nic.MAC != "" {
			item["mac_address"] = strings.ToLower(nic.MAC)
		}
		if nic.Model == "e1000" {
			item["model"] = "E1000"
		}
		nics = append(nics, item)
	}
	// UEFI VMs boot the CD-ROM while their new disks are blank, and the
	// CD-ROM is emptied at cutover; only legacy BIOS takes a boot order.
	boot := map[string]interface{}{}
	switch {
	case spec.SecureBoot:
		boot["boot_type"] = "SECURE_BOOT"
	case spec.Firmware == "uefi":
		boot["boot_type"] = "UEFI"
	default:
		boot["boot_type"] = "LEGACY"
		boot["boot_device_order_list"] = []string{"CDROM", "DISK"}
		if spec.ISO == "" {
			boot["boot_device_order_list"] = []string{"DISK"}
		}
	}
	resources := map[string]interface{}{
		"num_sockets": 1, "num_vcpus_per_socket": spec.CPUs, "memory_size_mib": spec.MemoryMiB,
		"power_state": "OFF", "disk_list": disks, "nic_list": nics, "boot_config": boot,
	}
	if spec.SecureBoot {
		// AHV requires the Q35 machine type for Secure Boot.
		resources["machine_type"] = "Q35"
	}
	if spec.TPM {
		resources["vtpm_config"] = map[string]interface{}{"vtpm_enabled": true}
	}
	body := map[string]interface{}{
		"spec": map[string]interface{}{
			"name": spec.Name, "description": spec.Description, "resources": resources,
			"cluster_reference": map[string]interface{}{"kind": "cluster", "uuid": cluster},
		},
		"metadata": map[string]interface{}{"kind": "vm"},
	}
	return body, nil
}

func (n *vmNutanix) CreateVM(ctx context.Context, spec vmTargetVMSpec) (*vmTargetVM, error) {
	cluster, err := n.clusterUUID(ctx)
	if err != nil {
		return nil, err
	}
	body, err := vmNutanixVMBody(cluster, spec)
	if err != nil {
		return nil, err
	}
	var created map[string]interface{}
	if err := n.http.do(ctx, http.MethodPost, "/vms", nil, body, "", 0, &created); err != nil {
		return nil, err
	}
	metadata, _ := created["metadata"].(map[string]interface{})
	uuid := vmNutanixString(metadata, "uuid")
	if uuid == "" {
		return nil, errors.New("Prism Central did not return the new VM's ID")
	}
	if err := n.waitTask(ctx, vmNutanixTask(created)); err != nil {
		return nil, err
	}
	return &vmTargetVM{ID: uuid, Name: spec.Name, State: "stopped"}, nil
}

// update reads the VM, lets change edit its spec and puts it back with the
// spec_version Prism Central requires.
func (n *vmNutanix) update(ctx context.Context, id string, change func(resources map[string]interface{}) error) error {
	var current vmNutanixEntity
	if err := n.http.do(ctx, http.MethodGet, "/vms/"+id, nil, nil, "", 0, &current); err != nil {
		return err
	}
	resources, _ := current.Spec["resources"].(map[string]interface{})
	if resources == nil {
		return errors.New("Prism Central returned the VM without its settings")
	}
	if err := change(resources); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]interface{}{"spec": current.Spec, "metadata": current.Metadata})
	var answer map[string]interface{}
	if err := n.http.do(ctx, http.MethodPut, "/vms/"+id, nil, raw, "", 0, &answer); err != nil {
		return err
	}
	return n.waitTask(ctx, vmNutanixTask(answer))
}

func (n *vmNutanix) power(ctx context.Context, id, state string) error {
	return n.update(ctx, id, func(resources map[string]interface{}) error {
		resources["power_state"] = state
		return nil
	})
}

func (n *vmNutanix) Start(ctx context.Context, id string) error    { return n.power(ctx, id, "ON") }
func (n *vmNutanix) PowerOff(ctx context.Context, id string) error { return n.power(ctx, id, "OFF") }

func (n *vmNutanix) State(ctx context.Context, id string) (string, error) {
	var current vmNutanixEntity
	if err := n.http.do(ctx, http.MethodGet, "/vms/"+id, nil, nil, "", 0, &current); err != nil {
		return "", err
	}
	switch vmNutanixString(current.Status, "resources", "power_state") {
	case "ON":
		return "running", nil
	case "OFF":
		return "stopped", nil
	}
	return strings.ToLower(vmNutanixString(current.Status, "resources", "power_state")), nil
}

func (n *vmNutanix) BootFromDisk(ctx context.Context, id string) error {
	return n.update(ctx, id, func(resources map[string]interface{}) error {
		boot, _ := resources["boot_config"].(map[string]interface{})
		if boot == nil {
			boot = map[string]interface{}{}
			resources["boot_config"] = boot
		}
		if boot["boot_type"] == nil || boot["boot_type"] == "LEGACY" {
			boot["boot_device_order_list"] = []string{"DISK"}
		}
		// Empty the CD-ROM so the receiver media is gone.
		disks, _ := resources["disk_list"].([]interface{})
		for _, item := range disks {
			disk, _ := item.(map[string]interface{})
			if vmNutanixString(disk, "device_properties", "device_type") == "CDROM" {
				delete(disk, "data_source_reference")
			}
		}
		return nil
	})
}

// CloneVM clones through Prism Central v3 with no adapters, so the clone
// starts isolated, and a client-chosen UUID so the clone is known at once.
func (n *vmNutanix) CloneVM(ctx context.Context, id, name, note string) (*vmTargetVM, error) {
	uuid, err := vmNewUUID()
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{"metadata": map[string]interface{}{"uuid": uuid}, "override_spec": map[string]interface{}{"name": name, "nic_list": []interface{}{}}}
	var answer map[string]interface{}
	if err := n.http.do(ctx, http.MethodPost, "/vms/"+id+"/clone", nil, body, "", 0, &answer); err != nil {
		return nil, err
	}
	task, _ := answer["task_uuid"].(string)
	if task == "" {
		task = vmNutanixTask(answer)
	}
	if err := n.waitTask(ctx, task); err != nil {
		return nil, err
	}
	return &vmTargetVM{ID: uuid, Name: name, State: "stopped"}, nil
}

// Isolate removes every adapter (a clone is made without any).
func (n *vmNutanix) Isolate(ctx context.Context, id string) error {
	return n.update(ctx, id, func(resources map[string]interface{}) error {
		resources["nic_list"] = []interface{}{}
		return nil
	})
}

func (n *vmNutanix) DeleteVM(ctx context.Context, id string) error {
	var answer map[string]interface{}
	if err := n.http.do(ctx, http.MethodDelete, "/vms/"+id, nil, nil, "", 0, &answer); err != nil {
		return err
	}
	return n.waitTask(ctx, vmNutanixTask(answer))
}
