package main

// Proxmox VE through its REST API (https://host:8006/api2/json) with an API
// token. The new VM is a q35 machine with the requested disks on one
// storage, the receiver ISO in ide2, and boot order CD first; cutover ejects
// the ISO and boots the first disk.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type vmProxmox struct {
	http *vmTargetHTTP
	node string
}

func newVMProxmox(connection vmTargetConnection) (*vmProxmox, error) {
	if !strings.Contains(connection.User, "!") || connection.Password == "" {
		return nil, errors.New("enter a Proxmox VE API token ID (user@realm!token) and its secret")
	}
	h, err := newVMTargetHTTP(connection, "8006", "/api2/json")
	if err != nil {
		return nil, err
	}
	token := "PVEAPIToken=" + connection.User + "=" + connection.Password
	h.authorize = func(request *http.Request) { request.Header.Set("Authorization", token) }
	return &vmProxmox{http: h, node: connection.Node}, nil
}

type vmProxmoxAnswer struct {
	Data interface{} `json:"data"`
}

func (p *vmProxmox) get(ctx context.Context, path string, query url.Values, out interface{}) error {
	return p.http.do(ctx, http.MethodGet, path, query, nil, "", 0, &vmProxmoxAnswer{Data: out})
}

// form sends form parameters and returns the task ID (UPID) or data string.
func (p *vmProxmox) form(ctx context.Context, method, path string, values url.Values) (string, error) {
	var data string
	answer := vmProxmoxAnswer{Data: &data}
	err := p.http.do(ctx, method, path, nil, []byte(values.Encode()), "application/x-www-form-urlencoded", 0, &answer)
	return data, err
}

func (p *vmProxmox) wait(ctx context.Context, upid string) error {
	if !strings.HasPrefix(upid, "UPID:") {
		return nil
	}
	for {
		var status struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := p.get(ctx, "/nodes/"+url.PathEscape(p.node)+"/tasks/"+url.PathEscape(upid)+"/status", nil, &status); err != nil {
			return err
		}
		if status.Status == "stopped" {
			if status.ExitStatus != "OK" {
				return fmt.Errorf("the Proxmox VE task failed: %s", status.ExitStatus)
			}
			return nil
		}
		if err := p.http.sleep(ctx); err != nil {
			return err
		}
	}
}

func (p *vmProxmox) Inventory(ctx context.Context) (*vmTargetInventory, error) {
	inventory := &vmTargetInventory{Platform: vmTargetProxmox}
	var version struct {
		Version string `json:"version"`
	}
	if err := p.get(ctx, "/version", nil, &version); err != nil {
		return nil, err
	}
	inventory.Version = "Proxmox VE " + version.Version
	var nodes []struct {
		Node   string `json:"node"`
		Status string `json:"status"`
	}
	if err := p.get(ctx, "/nodes", nil, &nodes); err != nil {
		return nil, err
	}
	for _, node := range nodes {
		inventory.Nodes = append(inventory.Nodes, vmTargetChoice{ID: node.Node, Name: node.Node, Detail: node.Status})
	}
	if p.node == "" && len(nodes) > 0 {
		p.node = nodes[0].Node
	}
	if p.node == "" {
		return inventory, nil
	}
	var storages []struct {
		Storage string  `json:"storage"`
		Type    string  `json:"type"`
		Content string  `json:"content"`
		Avail   float64 `json:"avail"`
	}
	if err := p.get(ctx, "/nodes/"+url.PathEscape(p.node)+"/storage", url.Values{"enabled": {"1"}}, &storages); err != nil {
		return nil, err
	}
	for _, storage := range storages {
		inventory.Storages = append(inventory.Storages, vmTargetChoice{ID: storage.Storage, Name: storage.Storage, Detail: fmt.Sprintf("%s, %s, %s free", storage.Type, storage.Content, vmFormatBytes(int64(storage.Avail)))})
	}
	var bridges []struct {
		Iface string `json:"iface"`
		Type  string `json:"type"`
	}
	if err := p.get(ctx, "/nodes/"+url.PathEscape(p.node)+"/network", url.Values{"type": {"any_bridge"}}, &bridges); err != nil {
		return nil, err
	}
	for _, bridge := range bridges {
		inventory.Networks = append(inventory.Networks, vmTargetChoice{ID: bridge.Iface, Name: bridge.Iface, Detail: bridge.Type})
	}
	return inventory, nil
}

func (p *vmProxmox) needNode() error {
	if p.node == "" {
		return errors.New("choose the Proxmox VE node")
	}
	return nil
}

// UploadISO uploads to storage "storage:name.iso"; name carries the storage
// as "storage/file.iso".
func (p *vmProxmox) UploadISO(ctx context.Context, name string, data io.Reader, size int64) (string, error) {
	if err := p.needNode(); err != nil {
		return "", err
	}
	storage, file, ok := strings.Cut(name, "/")
	if !ok || storage == "" || !strings.HasSuffix(strings.ToLower(file), ".iso") || strings.ContainsAny(file, `/\`) {
		return "", errors.New("name the ISO as storage/file.iso")
	}
	var existing []struct {
		VolID string `json:"volid"`
	}
	if err := p.get(ctx, "/nodes/"+url.PathEscape(p.node)+"/storage/"+url.PathEscape(storage)+"/content", url.Values{"content": {"iso"}}, &existing); err != nil {
		return "", err
	}
	volume := storage + ":iso/" + file
	for _, item := range existing {
		if item.VolID == volume {
			return "", fmt.Errorf("%s already exists on %s; choose a new file name so nothing is overwritten", file, storage)
		}
	}
	// The form is sent with its exact length: the file between a prefix
	// (the content field and file header) and the closing boundary.
	var prefix bytes.Buffer
	form := multipart.NewWriter(&prefix)
	form.WriteField("content", "iso")
	if _, err := form.CreateFormFile("filename", file); err != nil {
		return "", err
	}
	head := append([]byte(nil), prefix.Bytes()...)
	prefix.Reset()
	form.Close()
	tail := append([]byte("\r\n"), bytes.TrimPrefix(prefix.Bytes(), []byte("\r\n"))...)
	body := io.MultiReader(bytes.NewReader(head), io.LimitReader(data, size), bytes.NewReader(tail))
	var upid string
	answer := vmProxmoxAnswer{Data: &upid}
	if err := p.http.do(ctx, http.MethodPost, "/nodes/"+url.PathEscape(p.node)+"/storage/"+url.PathEscape(storage)+"/upload", nil, body, form.FormDataContentType(), int64(len(head))+size+int64(len(tail)), &answer); err != nil {
		return "", err
	}
	return volume, p.wait(ctx, upid)
}

func (p *vmProxmox) FindVM(ctx context.Context, name string) (*vmTargetVM, error) {
	var resources []struct {
		VMID   int    `json:"vmid"`
		Name   string `json:"name"`
		Node   string `json:"node"`
		Status string `json:"status"`
	}
	if err := p.get(ctx, "/cluster/resources", url.Values{"type": {"vm"}}, &resources); err != nil {
		return nil, err
	}
	for _, resource := range resources {
		if strings.EqualFold(resource.Name, name) {
			return &vmTargetVM{ID: strconv.Itoa(resource.VMID), Name: resource.Name, State: resource.Status}, nil
		}
	}
	return nil, nil
}

func vmProxmoxBus(bus string) string {
	switch bus {
	case "virtio":
		return "virtio"
	case "sata":
		return "sata"
	case "ide":
		return "ide"
	}
	return "scsi"
}

// vmProxmoxCreateValues builds the qm create parameters.
func vmProxmoxCreateValues(vmid string, spec vmTargetVMSpec) (url.Values, error) {
	if spec.Storage == "" {
		return nil, errors.New("choose the Proxmox VE storage for the disks")
	}
	values := url.Values{}
	values.Set("vmid", vmid)
	values.Set("name", spec.Name)
	values.Set("cores", strconv.Itoa(spec.CPUs))
	values.Set("sockets", "1")
	values.Set("memory", strconv.FormatInt(spec.MemoryMiB, 10))
	values.Set("machine", "q35")
	values.Set("scsihw", "virtio-scsi-single")
	if spec.Windows {
		values.Set("ostype", "win10")
	} else {
		values.Set("ostype", "l26")
	}
	if spec.Description != "" {
		values.Set("description", spec.Description)
	}
	if spec.Firmware == "uefi" {
		values.Set("bios", "ovmf")
		keys := "0"
		if spec.SecureBoot {
			keys = "1"
		}
		values.Set("efidisk0", spec.Storage+":1,efitype=4m,pre-enrolled-keys="+keys)
	} else {
		values.Set("bios", "seabios")
	}
	if spec.TPM {
		values.Set("tpmstate0", spec.Storage+":1,version=v2.0")
	}
	counts := map[string]int{}
	first := ""
	for _, disk := range spec.Disks {
		bus := vmProxmoxBus(disk.Bus)
		slot := fmt.Sprintf("%s%d", bus, counts[bus])
		if bus == "ide" && counts[bus] == 2 {
			// ide2 holds the receiver ISO.
			counts[bus]++
			slot = "ide3"
		}
		counts[bus]++
		// Sizes are whole GiB, rounded up, so the disk is never smaller
		// than the source.
		values.Set(slot, fmt.Sprintf("%s:%d", spec.Storage, vmCeilDiv(disk.Size, 1<<30)))
		if first == "" {
			first = slot
		}
	}
	if counts["ide"] > 3 || counts["sata"] > 6 || counts["virtio"] > 16 || counts["scsi"] > 16 {
		return nil, errors.New("too many disks on one bus for Proxmox VE")
	}
	values.Set("ide2", spec.ISO+",media=cdrom")
	values.Set("boot", "order=ide2;"+first)
	for index, nic := range spec.NICs {
		if nic.Network == "" {
			return nil, fmt.Errorf("adapter %d has no bridge", index)
		}
		model := nic.Model
		if nic.MAC != "" {
			model += "=" + nic.MAC
		}
		value := model + ",bridge=" + nic.Network
		if nic.VLAN > 0 {
			value += ",tag=" + strconv.Itoa(nic.VLAN)
		}
		values.Set(fmt.Sprintf("net%d", index), value)
	}
	return values, nil
}

func (p *vmProxmox) CreateVM(ctx context.Context, spec vmTargetVMSpec) (*vmTargetVM, error) {
	if err := p.needNode(); err != nil {
		return nil, err
	}
	var next interface{}
	if err := p.get(ctx, "/cluster/nextid", nil, &next); err != nil {
		return nil, err
	}
	vmid := strings.Trim(fmt.Sprint(next), `"`)
	if _, err := strconv.Atoi(vmid); err != nil {
		return nil, errors.New("Proxmox VE did not give a free VM ID")
	}
	values, err := vmProxmoxCreateValues(vmid, spec)
	if err != nil {
		return nil, err
	}
	upid, err := p.form(ctx, http.MethodPost, "/nodes/"+url.PathEscape(p.node)+"/qemu", values)
	if err != nil {
		return nil, err
	}
	if err := p.wait(ctx, upid); err != nil {
		return nil, err
	}
	return &vmTargetVM{ID: vmid, Name: spec.Name, State: "stopped"}, nil
}

func (p *vmProxmox) action(ctx context.Context, id, action string) error {
	if err := p.needNode(); err != nil {
		return err
	}
	upid, err := p.form(ctx, http.MethodPost, "/nodes/"+url.PathEscape(p.node)+"/qemu/"+url.PathEscape(id)+"/status/"+action, url.Values{})
	if err != nil {
		return err
	}
	return p.wait(ctx, upid)
}

func (p *vmProxmox) Start(ctx context.Context, id string) error    { return p.action(ctx, id, "start") }
func (p *vmProxmox) PowerOff(ctx context.Context, id string) error { return p.action(ctx, id, "stop") }

func (p *vmProxmox) State(ctx context.Context, id string) (string, error) {
	if err := p.needNode(); err != nil {
		return "", err
	}
	var status struct {
		Status string `json:"status"`
	}
	err := p.get(ctx, "/nodes/"+url.PathEscape(p.node)+"/qemu/"+url.PathEscape(id)+"/status/current", nil, &status)
	return status.Status, err
}

func (p *vmProxmox) BootFromDisk(ctx context.Context, id string) error {
	if err := p.needNode(); err != nil {
		return err
	}
	var config map[string]interface{}
	if err := p.get(ctx, "/nodes/"+url.PathEscape(p.node)+"/qemu/"+url.PathEscape(id)+"/config", nil, &config); err != nil {
		return err
	}
	boot, _ := config["boot"].(string)
	order := strings.TrimPrefix(boot, "order=")
	disks := []string{}
	for _, device := range strings.Split(order, ";") {
		if device != "" && device != "ide2" {
			disks = append(disks, device)
		}
	}
	if len(disks) == 0 {
		return errors.New("the VM has no boot disk in its boot order")
	}
	upid, err := p.form(ctx, http.MethodPost, "/nodes/"+url.PathEscape(p.node)+"/qemu/"+url.PathEscape(id)+"/config", url.Values{"ide2": {"none,media=cdrom"}, "boot": {"order=" + strings.Join(disks, ";")}})
	if err != nil {
		return err
	}
	return p.wait(ctx, upid)
}
