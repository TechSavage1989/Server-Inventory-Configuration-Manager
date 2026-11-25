package main

// Scale Computing HyperCore through its REST API (https://cluster/rest/v1)
// with basic authentication. The new VM gets the requested disks, the
// receiver ISO in an IDE CD-ROM booted first, and NVRAM (and a virtual TPM)
// for UEFI; cutover powers it off and sets its first disk as the only boot
// device. HyperCore storage is one pool per cluster, and networks are VLANs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const (
	vmHyperCoreBIOSMachine    = "scale-7.2"
	vmHyperCoreUEFIMachine    = "scale-uefi-9.2"
	vmHyperCoreUEFITPMMachine = "scale-uefi-tpm-9.3"
	vmHyperCoreNVRAMBytes     = 540672
)

type vmHyperCore struct {
	http *vmTargetHTTP
}

func newVMHyperCore(connection vmTargetConnection) (*vmHyperCore, error) {
	if connection.User == "" || connection.Password == "" {
		return nil, errors.New("enter the HyperCore user name and password")
	}
	h, err := newVMTargetHTTP(connection, "", "/rest/v1")
	if err != nil {
		return nil, err
	}
	user, password := connection.User, connection.Password
	h.authorize = func(request *http.Request) { request.SetBasicAuth(user, password) }
	return &vmHyperCore{http: h}, nil
}

type vmHyperCoreBlockDevice struct {
	UUID     string `json:"uuid,omitempty"`
	Type     string `json:"type"`
	Capacity int64  `json:"capacity,omitempty"`
	Path     string `json:"path,omitempty"`
	Slot     int    `json:"slot"`
}

type vmHyperCoreNetDevice struct {
	Type       string `json:"type"`
	VLAN       int    `json:"vlan"`
	MacAddress string `json:"macAddress,omitempty"`
	Connected  bool   `json:"connected"`
}

type vmHyperCoreDomain struct {
	UUID        string                   `json:"uuid"`
	Name        string                   `json:"name"`
	State       string                   `json:"state"`
	BlockDevs   []vmHyperCoreBlockDevice `json:"blockDevs"`
	NetDevs     []vmHyperCoreNetDevice   `json:"netDevs"`
	BootDevices []string                 `json:"bootDevices"`
}

type vmHyperCoreCreated struct {
	TaskTag     string `json:"taskTag"`
	CreatedUUID string `json:"createdUUID"`
}

func (c *vmHyperCore) waitTask(ctx context.Context, tag string) error {
	if tag == "" {
		return nil
	}
	for {
		var tasks []struct {
			State     string `json:"state"`
			Formatted string `json:"formattedMessage"`
		}
		if err := c.http.do(ctx, http.MethodGet, "/TaskTag/"+tag, nil, nil, "", 0, &tasks); err != nil {
			return err
		}
		if len(tasks) > 0 {
			switch tasks[0].State {
			case "COMPLETE":
				return nil
			case "ERROR":
				return fmt.Errorf("the HyperCore task failed: %s", tasks[0].Formatted)
			}
		}
		if err := c.http.sleep(ctx); err != nil {
			return err
		}
	}
}

func (c *vmHyperCore) domains(ctx context.Context) ([]vmHyperCoreDomain, error) {
	var list []vmHyperCoreDomain
	err := c.http.do(ctx, http.MethodGet, "/VirDomain", nil, nil, "", 0, &list)
	return list, err
}

func (c *vmHyperCore) domain(ctx context.Context, id string) (*vmHyperCoreDomain, error) {
	var list []vmHyperCoreDomain
	if err := c.http.do(ctx, http.MethodGet, "/VirDomain/"+id, nil, nil, "", 0, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("HyperCore has no VM %s", id)
	}
	return &list[0], nil
}

func (c *vmHyperCore) Inventory(ctx context.Context) (*vmTargetInventory, error) {
	inventory := &vmTargetInventory{Platform: vmTargetScale}
	var clusters []struct {
		ClusterName string `json:"clusterName"`
		IcosVersion string `json:"icosVersion"`
	}
	if err := c.http.do(ctx, http.MethodGet, "/Cluster", nil, nil, "", 0, &clusters); err != nil {
		return nil, err
	}
	if len(clusters) > 0 {
		inventory.Version = "HyperCore " + clusters[0].IcosVersion
		inventory.Nodes = append(inventory.Nodes, vmTargetChoice{ID: clusters[0].ClusterName, Name: clusters[0].ClusterName, Detail: inventory.Version})
	}
	domains, err := c.domains(ctx)
	if err != nil {
		return nil, err
	}
	// HyperCore has no VLAN list; offer the VLANs its VMs already use.
	vlans := map[int]int{}
	for _, domain := range domains {
		for _, nic := range domain.NetDevs {
			vlans[nic.VLAN]++
		}
	}
	ids := []int{}
	for vlan := range vlans {
		ids = append(ids, vlan)
	}
	sort.Ints(ids)
	for _, vlan := range ids {
		inventory.Networks = append(inventory.Networks, vmTargetChoice{ID: fmt.Sprint(vlan), Name: fmt.Sprintf("VLAN %d", vlan), Detail: fmt.Sprintf("used by %d adapters", vlans[vlan])})
	}
	inventory.Storages = []vmTargetChoice{{ID: "", Name: "HyperCore storage", Detail: "one pool per cluster"}}
	return inventory, nil
}

func (c *vmHyperCore) UploadISO(ctx context.Context, name string, data io.Reader, size int64) (string, error) {
	if !strings.HasSuffix(strings.ToLower(name), ".iso") || strings.ContainsAny(name, `/\"`) {
		return "", errors.New("name the ISO file.iso")
	}
	var existing []struct {
		Name string `json:"name"`
	}
	if err := c.http.do(ctx, http.MethodGet, "/ISO", nil, nil, "", 0, &existing); err != nil {
		return "", err
	}
	for _, iso := range existing {
		if strings.EqualFold(iso.Name, name) {
			return "", fmt.Errorf("an ISO named %s already exists; choose a new name so nothing is overwritten", name)
		}
	}
	var created vmHyperCoreCreated
	if err := c.http.do(ctx, http.MethodPost, "/ISO", nil, map[string]interface{}{"name": name, "size": size, "readyForInsert": false}, "", 0, &created); err != nil {
		return "", err
	}
	if created.CreatedUUID == "" {
		return "", errors.New("HyperCore did not return the new ISO's ID")
	}
	if err := c.waitTask(ctx, created.TaskTag); err != nil {
		return "", err
	}
	if err := c.http.do(ctx, http.MethodPut, "/ISO/"+created.CreatedUUID+"/data", nil, io.LimitReader(data, size), "application/octet-stream", size, nil); err != nil {
		return "", err
	}
	var isos []struct {
		Path string `json:"path"`
	}
	if err := c.http.do(ctx, http.MethodGet, "/ISO/"+created.CreatedUUID, nil, nil, "", 0, &isos); err != nil {
		return "", err
	}
	if len(isos) == 0 || isos[0].Path == "" {
		return "", errors.New("HyperCore did not report where the ISO was stored")
	}
	return isos[0].Path, nil
}

func (c *vmHyperCore) FindVM(ctx context.Context, name string) (*vmTargetVM, error) {
	domains, err := c.domains(ctx)
	if err != nil {
		return nil, err
	}
	for _, domain := range domains {
		if strings.EqualFold(domain.Name, name) {
			return &vmTargetVM{ID: domain.UUID, Name: domain.Name, State: vmHyperCoreState(domain.State)}, nil
		}
	}
	return nil, nil
}

func vmHyperCoreState(state string) string {
	switch state {
	case "RUNNING", "BLOCKED":
		return "running"
	case "SHUTOFF", "SHUTDOWN", "CRASHED":
		return "stopped"
	}
	return strings.ToLower(state)
}

func vmHyperCoreDiskType(bus string) string {
	switch bus {
	case "ide", "sata":
		return "IDE_DISK"
	case "scsi":
		return "SCSI_DISK"
	}
	return "VIRTIO_DISK"
}

// vmHyperCoreDomainBody builds the VirDomain create body.
func vmHyperCoreDomainBody(spec vmTargetVMSpec) (map[string]interface{}, error) {
	machine := spec.MachineType
	if machine == "" {
		switch {
		case spec.TPM:
			machine = vmHyperCoreUEFITPMMachine
		case spec.Firmware == "uefi":
			machine = vmHyperCoreUEFIMachine
		default:
			machine = vmHyperCoreBIOSMachine
		}
	}
	devices := []vmHyperCoreBlockDevice{}
	for index, disk := range spec.Disks {
		// Whole MiB, rounded up, so the disk is never smaller than the source.
		devices = append(devices, vmHyperCoreBlockDevice{Type: vmHyperCoreDiskType(disk.Bus), Capacity: vmCeilDiv(disk.Size, 1<<20) << 20, Slot: index})
	}
	devices = append(devices, vmHyperCoreBlockDevice{Type: "IDE_CDROM", Path: spec.ISO, Slot: len(spec.Disks)})
	if spec.Firmware == "uefi" {
		devices = append(devices, vmHyperCoreBlockDevice{Type: "NVRAM", Capacity: vmHyperCoreNVRAMBytes, Slot: -1})
	}
	if spec.TPM {
		devices = append(devices, vmHyperCoreBlockDevice{Type: "VTPM", Slot: -1})
	}
	nics := []vmHyperCoreNetDevice{}
	for index, nic := range spec.NICs {
		vlan := nic.VLAN
		if nic.Network != "" {
			if _, err := fmt.Sscanf(nic.Network, "%d", &vlan); err != nil || vlan < 0 || vlan > 4094 {
				return nil, fmt.Errorf("adapter %d: HyperCore networks are VLAN numbers", index)
			}
		}
		kind := "VIRTIO"
		if nic.Model == "e1000" {
			kind = "INTEL_E1000"
		}
		nics = append(nics, vmHyperCoreNetDevice{Type: kind, VLAN: vlan, MacAddress: nic.MAC, Connected: true})
	}
	return map[string]interface{}{
		"dom": map[string]interface{}{
			"name": spec.Name, "description": spec.Description, "mem": spec.MemoryMiB << 20, "numVCPU": spec.CPUs,
			"machineType": machine, "blockDevs": devices, "netDevs": nics, "tags": "migration",
		},
		"options": map[string]interface{}{"attachGuestToolsISO": false},
	}, nil
}

func (c *vmHyperCore) CreateVM(ctx context.Context, spec vmTargetVMSpec) (*vmTargetVM, error) {
	body, err := vmHyperCoreDomainBody(spec)
	if err != nil {
		return nil, err
	}
	var created vmHyperCoreCreated
	if err := c.http.do(ctx, http.MethodPost, "/VirDomain", nil, body, "", 0, &created); err != nil {
		return nil, err
	}
	if created.CreatedUUID == "" {
		return nil, errors.New("HyperCore did not return the new VM's ID")
	}
	if err := c.waitTask(ctx, created.TaskTag); err != nil {
		return nil, err
	}
	// Boot the receiver CD-ROM first while the disks are blank.
	domain, err := c.domain(ctx, created.CreatedUUID)
	if err != nil {
		return nil, err
	}
	order := []string{}
	for _, device := range vmHyperCoreSorted(domain.BlockDevs) {
		if device.Type == "IDE_CDROM" {
			order = append([]string{device.UUID}, order...)
		} else if strings.HasSuffix(device.Type, "_DISK") {
			order = append(order, device.UUID)
		}
	}
	if err := c.setBoot(ctx, created.CreatedUUID, order); err != nil {
		return nil, err
	}
	return &vmTargetVM{ID: created.CreatedUUID, Name: spec.Name, State: "stopped"}, nil
}

func (c *vmHyperCore) setBoot(ctx context.Context, id string, order []string) error {
	var answer vmHyperCoreCreated
	if err := c.http.do(ctx, http.MethodPatch, "/VirDomain/"+id, nil, map[string]interface{}{"bootDevices": order}, "", 0, &answer); err != nil {
		return err
	}
	return c.waitTask(ctx, answer.TaskTag)
}

func (c *vmHyperCore) action(ctx context.Context, id, action string) error {
	var answer vmHyperCoreCreated
	if err := c.http.do(ctx, http.MethodPost, "/VirDomain/action", nil, []map[string]string{{"virDomainUUID": id, "actionType": action}}, "", 0, &answer); err != nil {
		return err
	}
	return c.waitTask(ctx, answer.TaskTag)
}

func (c *vmHyperCore) Start(ctx context.Context, id string) error { return c.action(ctx, id, "START") }
func (c *vmHyperCore) PowerOff(ctx context.Context, id string) error {
	return c.action(ctx, id, "STOP")
}

func (c *vmHyperCore) State(ctx context.Context, id string) (string, error) {
	domain, err := c.domain(ctx, id)
	if err != nil {
		return "", err
	}
	return vmHyperCoreState(domain.State), nil
}

func (c *vmHyperCore) BootFromDisk(ctx context.Context, id string) error {
	domain, err := c.domain(ctx, id)
	if err != nil {
		return err
	}
	for _, device := range vmHyperCoreSorted(domain.BlockDevs) {
		if strings.HasSuffix(device.Type, "_DISK") {
			return c.setBoot(ctx, id, []string{device.UUID})
		}
	}
	return errors.New("the VM has no disk to boot from")
}

func vmHyperCoreSorted(devices []vmHyperCoreBlockDevice) []vmHyperCoreBlockDevice {
	sorted := append([]vmHyperCoreBlockDevice(nil), devices...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Slot < sorted[j].Slot })
	return sorted
}
