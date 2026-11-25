package main

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func vmImportSpec(reference string) vmTargetVMSpec {
	return vmTargetVMSpec{Name: "app01", CPUs: 2, MemoryMiB: 4096, Firmware: "uefi", Windows: true, Storage: "local-lvm",
		Disks: []vmTargetDisk{{Size: 40 << 30, Import: reference}}, NICs: []vmTargetNIC{{Network: "vmbr0"}}}
}

func TestTargetSpecsAreEitherImportedOrBlank(t *testing.T) {
	spec := vmImportSpec("ref")
	if err := vmValidateTargetSpec(&spec); err != nil {
		t.Fatalf("an imported disk without receiver media was refused: %v", err)
	}
	spec.Disks = append(spec.Disks, vmTargetDisk{Size: 1 << 30})
	if err := vmValidateTargetSpec(&spec); err == nil {
		t.Fatal("a mix of imported and blank disks was accepted")
	}
	if err := vmTargetImageName("disk.qcow2", ".qcow2"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../disk.qcow2", "disk.exe", "a b.qcow2", ""} {
		if vmTargetImageName(bad, ".qcow2") == nil {
			t.Errorf("%q was accepted as an image name", bad)
		}
	}
}

func TestProxmoxImportsAnUploadedDisk(t *testing.T) {
	fake := &vmFakePlatform{state: "stopped"}
	var uploaded []byte
	var contentType string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := fake.record(r)
		path := strings.TrimPrefix(r.URL.Path, "/api2/json")
		switch {
		case path == "/nodes/pve1/storage/local/content":
			if r.URL.Query().Get("content") != "import" {
				t.Errorf("listed %q content", r.URL.Query().Get("content"))
			}
			vmJSON(w, map[string]interface{}{"data": []map[string]string{{"volid": "local:import/old.qcow2"}}})
		case path == "/nodes/pve1/storage/local/upload":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			reader := multipart.NewReader(strings.NewReader(string(raw)), params["boundary"])
			for part, err := reader.NextPart(); err == nil; part, err = reader.NextPart() {
				value, _ := io.ReadAll(part)
				switch part.FormName() {
				case "filename":
					uploaded = value
				case "content":
					contentType = string(value)
				}
			}
			vmJSON(w, map[string]string{"data": "UPID:pve1:upload"})
		case strings.HasPrefix(path, "/nodes/pve1/tasks/"):
			vmJSON(w, map[string]interface{}{"data": map[string]string{"status": "stopped", "exitstatus": "OK"}})
		case path == "/cluster/resources":
			vmJSON(w, map[string]interface{}{"data": []interface{}{}})
		case path == "/cluster/nextid":
			vmJSON(w, map[string]interface{}{"data": "105"})
		case path == "/nodes/pve1/qemu" && r.Method == http.MethodPost:
			vmJSON(w, map[string]string{"data": "UPID:pve1:create"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	connection := vmTestConnection(t, server, vmTargetProxmox, "migrate@pve!move")
	connection.Node = "pve1"
	platform, err := newVMTargetPlatform(connection)
	if err != nil {
		t.Fatal(err)
	}
	vmFastPlatform(platform)
	uploader := platform.(vmTargetDiskUploader)
	if uploader.DiskFormats()[0] != vmDiskFormatQCOW2 {
		t.Fatalf("formats %v", uploader.DiskFormats())
	}
	if _, err := uploader.UploadDisk(ctx, "local/old.qcow2", strings.NewReader("x"), 1); err == nil {
		t.Fatal("an existing image was overwritten")
	}
	volume, err := uploader.UploadDisk(ctx, "local/app01.qcow2", strings.NewReader("QCOW-BYTES"), 10)
	if err != nil || volume != "local:import/app01.qcow2" || string(uploaded) != "QCOW-BYTES" || contentType != "import" {
		t.Fatalf("upload %q %v %q %q", volume, err, uploaded, contentType)
	}
	vm, err := vmTargetCreate(ctx, platform, vmImportSpec(volume), nil)
	if err != nil || vm.ID != "105" || vm.State != "stopped" {
		t.Fatalf("create %+v %v", vm, err)
	}
	if fake.saw("POST /api2/json/nodes/pve1/qemu/105/status/start") {
		t.Fatal("a VM made from an uploaded disk was started without being asked")
	}
	values, _ := url.ParseQuery(string(fake.bodies["POST /api2/json/nodes/pve1/qemu"]))
	if values.Get("virtio0") != "local-lvm:0,import-from=local:import/app01.qcow2" || values.Get("boot") != "order=virtio0" || values.Get("ide2") != "" {
		t.Fatalf("create values %v", values)
	}
}

func TestNutanixMakesAVMFromAnUploadedImage(t *testing.T) {
	fake := &vmFakePlatform{}
	var vmBody, imageBody map[string]interface{}
	var uploaded []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := fake.record(r)
		path := strings.TrimPrefix(r.URL.Path, "/api/nutanix/v3")
		task := map[string]interface{}{"execution_context": map[string]string{"task_uuid": "t1"}}
		switch {
		case path == "/clusters/list":
			vmJSON(w, map[string]interface{}{"entities": []map[string]interface{}{{"metadata": map[string]string{"uuid": "c1"}, "status": map[string]interface{}{"name": "ahv01", "resources": map[string]interface{}{"config": map[string]interface{}{"service_list": []string{"AOS"}}}}}}})
		case path == "/images/list", path == "/vms/list":
			vmJSON(w, map[string]interface{}{"entities": []interface{}{}})
		case path == "/images" && r.Method == http.MethodPost:
			json.Unmarshal(raw, &imageBody)
			vmJSON(w, map[string]interface{}{"metadata": map[string]string{"uuid": "img9"}, "status": task})
		case path == "/images/img9/file":
			uploaded = raw
		case path == "/tasks/t1":
			vmJSON(w, map[string]string{"status": "SUCCEEDED"})
		case path == "/vms" && r.Method == http.MethodPost:
			json.Unmarshal(raw, &vmBody)
			vmJSON(w, map[string]interface{}{"metadata": map[string]string{"uuid": "v9"}, "status": task})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	platform, err := newVMTargetPlatform(vmTestConnection(t, server, vmTargetNutanix, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	vmFastPlatform(platform)
	image, err := platform.(vmTargetDiskUploader).UploadDisk(ctx, "app01.qcow2", strings.NewReader("DISK"), 4)
	if err != nil || image != "img9" || string(uploaded) != "DISK" {
		t.Fatalf("upload %q %v", image, err)
	}
	if !strings.Contains(string(mustJSON(imageBody)), `"image_type":"DISK_IMAGE"`) {
		t.Fatalf("image body %v", imageBody)
	}
	spec := vmImportSpec(image)
	spec.Storage, spec.NICs[0].Network, spec.Firmware = "", "s1", "bios"
	if _, err := vmTargetCreate(ctx, platform, spec, nil); err != nil {
		t.Fatal(err)
	}
	raw := string(mustJSON(vmBody))
	for _, want := range []string{`"data_source_reference":{"kind":"image","uuid":"img9"}`, `"boot_device_order_list":["DISK"]`} {
		if !strings.Contains(raw, want) {
			t.Errorf("VM body lacks %s: %s", want, raw)
		}
	}
	if strings.Contains(raw, "CDROM") || fake.saw("PUT /api/nutanix/v3/vms/v9") {
		t.Fatalf("the VM got a CD-ROM or was powered on: %s", raw)
	}
}

func TestHyperCoreAttachesAnUploadedDisk(t *testing.T) {
	fake := &vmFakePlatform{}
	var created, attach map[string]interface{}
	var uploaded []byte
	var query url.Values
	version := "9.4.30"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := fake.record(r)
		path := strings.TrimPrefix(r.URL.Path, "/rest/v1")
		switch {
		case path == "/Cluster":
			vmJSON(w, []map[string]string{{"icosVersion": version}})
		case path == "/VirtualDisk" && r.Method == http.MethodGet:
			vmJSON(w, []map[string]string{{"name": "old.qcow2"}})
		case path == "/VirtualDisk/upload" && r.Method == http.MethodPut:
			uploaded, query = raw, r.URL.Query()
			vmJSON(w, map[string]string{"createdUUID": "vd1", "taskTag": "1"})
		case path == "/VirtualDisk/vd1/attach":
			json.Unmarshal(raw, &attach)
			vmJSON(w, map[string]string{"taskTag": "3"})
		case strings.HasPrefix(path, "/TaskTag/"):
			vmJSON(w, []map[string]string{{"state": "COMPLETE"}})
		case path == "/VirDomain" && r.Method == http.MethodGet:
			vmJSON(w, []interface{}{})
		case path == "/VirDomain" && r.Method == http.MethodPost:
			json.Unmarshal(raw, &created)
			vmJSON(w, map[string]string{"createdUUID": "d9", "taskTag": "2"})
		case path == "/VirDomain/d9" && r.Method == http.MethodGet:
			vmJSON(w, []map[string]interface{}{{"uuid": "d9", "name": "app01", "state": "SHUTOFF", "blockDevs": []map[string]interface{}{{"uuid": "b1", "type": "VIRTIO_DISK", "slot": 0}, {"uuid": "nv", "type": "NVRAM", "slot": -1}}}})
		case path == "/VirDomain/d9" && r.Method == http.MethodPatch:
			vmJSON(w, map[string]string{"taskTag": "4"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	platform, err := newVMTargetPlatform(vmTestConnection(t, server, vmTargetScale, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	vmFastPlatform(platform)
	uploader := platform.(vmTargetDiskUploader)
	if _, err := uploader.UploadDisk(ctx, "old.qcow2", strings.NewReader("x"), 1); err == nil {
		t.Fatal("an existing virtual disk was overwritten")
	}
	disk, err := uploader.UploadDisk(ctx, "app01.qcow2", strings.NewReader("QCOW"), 4)
	if err != nil || disk != "vd1" || string(uploaded) != "QCOW" || query.Get("filename") != "app01.qcow2" || query.Get("filesize") != "4" {
		t.Fatalf("upload %q %v %q %v", disk, err, uploaded, query)
	}
	spec := vmImportSpec(disk)
	spec.NICs[0].Network = "20"
	if _, err := vmTargetCreate(ctx, platform, spec, nil); err != nil {
		t.Fatal(err)
	}
	body := string(mustJSON(created))
	if strings.Contains(body, "IDE_CDROM") || strings.Contains(body, "VIRTIO_DISK") {
		t.Fatalf("the uploaded disk or a CD-ROM was put in the create body: %s", body)
	}
	if !strings.Contains(string(mustJSON(attach)), `"virDomainUUID":"d9"`) || !strings.Contains(string(fake.bodies["PATCH /rest/v1/VirDomain/d9"]), `"b1"`) {
		t.Fatalf("attach %v boot %s", attach, fake.bodies["PATCH /rest/v1/VirDomain/d9"])
	}
	version = "9.1.20"
	platform, _ = newVMTargetPlatform(vmTestConnection(t, server, vmTargetScale, "admin"))
	if _, err := platform.(vmTargetDiskUploader).UploadDisk(ctx, "new.qcow2", strings.NewReader("x"), 1); err == nil || !strings.Contains(err.Error(), "9.2") {
		t.Fatalf("an old cluster was sent a disk: %v", err)
	}
}

func mustJSON(value interface{}) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
