package main

// Uploading a converted disk image to a target platform, so the Disk
// converter can make a VM straight from it (vmconvert_send.go). Each upload
// uses a name not already on the platform and never replaces an image.
//
//   - Proxmox VE: POST /nodes/{node}/storage/{storage}/upload with content
//     "import" (a file storage with the Import content type), then qm create
//     with import-from, which copies the image onto the VM's storage.
//   - Nutanix AHV: a DISK_IMAGE in Prism Central (v3 images API) that the VM
//     disk is cloned from (data_source_reference).
//   - Scale HyperCore 9.2 and later: PUT /VirtualDisk/upload, then
//     /VirtualDisk/{uuid}/attach to the new VM, as Scale Computing's Ansible
//     collection does (modules virtual_disk and virtual_disk_attach).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// vmTargetDiskUploader is a platform that takes disk images.
type vmTargetDiskUploader interface {
	// DiskFormats are the converter formats (vmDiskFormat IDs) the
	// platform imports, preferred first.
	DiskFormats() []string
	// UploadDisk stores the image under name and returns the reference a
	// vmTargetDisk's Import takes.
	UploadDisk(ctx context.Context, name string, data io.Reader, size int64) (string, error)
}

var vmTargetImageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}$`)

// vmTargetImageName checks an upload file name and its extension.
func vmTargetImageName(name string, extensions ...string) error {
	if !vmTargetImageNamePattern.MatchString(name) {
		return errors.New("the image name must be letters, digits, dots, dashes or underscores")
	}
	lower := strings.ToLower(name)
	for _, extension := range extensions {
		if strings.HasSuffix(lower, extension) {
			return nil
		}
	}
	return fmt.Errorf("the image name must end with %s", strings.Join(extensions, ", "))
}

// Proxmox VE.

func (p *vmProxmox) DiskFormats() []string {
	return []string{vmDiskFormatQCOW2, vmDiskFormatRaw, vmDiskFormatVMDK}
}

// UploadDisk takes name as storage/file.qcow2, where storage is a file
// storage with the Import content type.
func (p *vmProxmox) UploadDisk(ctx context.Context, name string, data io.Reader, size int64) (string, error) {
	if err := p.needNode(); err != nil {
		return "", err
	}
	storage, file, ok := strings.Cut(name, "/")
	if !ok || storage == "" {
		return "", errors.New("name the image as storage/file.qcow2")
	}
	if err := vmTargetImageName(file, ".qcow2", ".raw", ".vmdk", ".img"); err != nil {
		return "", err
	}
	var existing []struct {
		VolID string `json:"volid"`
	}
	if err := p.get(ctx, "/nodes/"+url.PathEscape(p.node)+"/storage/"+url.PathEscape(storage)+"/content", url.Values{"content": {"import"}}, &existing); err != nil {
		return "", fmt.Errorf("%w (the storage %s must be a file storage with the Import content type, on Proxmox VE 8.4 or later)", err, storage)
	}
	volume := storage + ":import/" + file
	for _, item := range existing {
		if item.VolID == volume {
			return "", fmt.Errorf("%s already exists on %s; choose a new file name so nothing is overwritten", file, storage)
		}
	}
	var prefix bytes.Buffer
	form := multipart.NewWriter(&prefix)
	form.WriteField("content", "import")
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

// Nutanix AHV.

func (n *vmNutanix) DiskFormats() []string {
	return []string{vmDiskFormatQCOW2, vmDiskFormatVHDX, vmDiskFormatVMDK, vmDiskFormatVHD, vmDiskFormatRaw}
}

func (n *vmNutanix) UploadDisk(ctx context.Context, name string, data io.Reader, size int64) (string, error) {
	if err := vmTargetImageName(name, ".qcow2", ".vhdx", ".vmdk", ".vhd", ".img", ".raw"); err != nil {
		return "", err
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
		"spec":     map[string]interface{}{"name": name, "description": "Converted by Server Inventory and Migration Manager", "resources": map[string]interface{}{"image_type": "DISK_IMAGE"}},
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

// Scale HyperCore.

func (c *vmHyperCore) DiskFormats() []string {
	return []string{vmDiskFormatQCOW2, vmDiskFormatVHDX, vmDiskFormatVMDK, vmDiskFormatVHD, vmDiskFormatRaw}
}

func (c *vmHyperCore) UploadDisk(ctx context.Context, name string, data io.Reader, size int64) (string, error) {
	if err := vmTargetImageName(name, ".qcow2", ".vhdx", ".vmdk", ".vhd", ".img"); err != nil {
		return "", err
	}
	version, err := c.clusterVersion(ctx)
	if err != nil {
		return "", err
	}
	if vmHyperCoreVersionNumber(version) < 9_002 {
		return "", fmt.Errorf("HyperCore %s cannot take uploaded disks; update the cluster to 9.2 or later, or use the receiver copy", version)
	}
	var existing []struct {
		Name string `json:"name"`
	}
	if err := c.http.do(ctx, http.MethodGet, "/VirtualDisk", nil, nil, "", 0, &existing); err != nil {
		return "", err
	}
	for _, disk := range existing {
		if strings.EqualFold(disk.Name, name) || strings.EqualFold(path.Base(disk.Name), name) {
			return "", fmt.Errorf("a virtual disk named %s already exists; choose a new name so nothing is overwritten", name)
		}
	}
	var created vmHyperCoreCreated
	query := url.Values{"filename": {name}, "filesize": {fmt.Sprint(size)}}
	if err := c.http.do(ctx, http.MethodPut, "/VirtualDisk/upload", query, io.LimitReader(data, size), "application/octet-stream", size, &created); err != nil {
		return "", err
	}
	if created.CreatedUUID == "" {
		return "", errors.New("HyperCore did not return the uploaded disk's ID")
	}
	if created.TaskTag != "" {
		if err := c.waitTask(ctx, created.TaskTag); err != nil {
			return "", err
		}
	}
	return created.CreatedUUID, nil
}

// attachDisk copies an uploaded virtual disk onto the VM in the given slot.
func (c *vmHyperCore) attachDisk(ctx context.Context, diskUUID, vmUUID string, disk vmTargetDisk, slot int) error {
	body := map[string]interface{}{"template": map[string]interface{}{
		"virDomainUUID": vmUUID, "type": vmHyperCoreDiskType(disk.Bus), "capacity": vmCeilDiv(disk.Size, 1<<20) << 20,
		"slot": slot, "cacheMode": "WRITETHROUGH",
	}}
	var answer vmHyperCoreCreated
	if err := c.http.do(ctx, http.MethodPost, "/VirtualDisk/"+url.PathEscape(diskUUID)+"/attach", nil, body, "", 0, &answer); err != nil {
		return err
	}
	return c.waitTask(ctx, answer.TaskTag)
}
