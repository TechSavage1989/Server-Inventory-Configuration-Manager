package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const vmTargetTestSecret = "s3cret-Token-Value"

// vmFakePlatform records requests and keeps a little VM state.
type vmFakePlatform struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string][]byte
	state    string
	auth     func(*http.Request) bool
}

func (f *vmFakePlatform) record(r *http.Request) []byte {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	f.requests = append(f.requests, key)
	if f.bodies == nil {
		f.bodies = map[string][]byte{}
	}
	f.bodies[key] = raw
	return raw
}

func (f *vmFakePlatform) saw(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, request := range f.requests {
		if request == key {
			return true
		}
	}
	return false
}

func vmJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

func vmTestConnection(t *testing.T, server *httptest.Server, platform, user string) vmTargetConnection {
	t.Helper()
	sum := sha256.Sum256(server.Certificate().Raw)
	return vmTargetConnection{Platform: platform, Server: strings.TrimPrefix(server.URL, "https://"), User: user, Password: vmTargetTestSecret, Thumbprint: hex.EncodeToString(sum[:])}
}

func vmTestSpec(iso string) vmTargetVMSpec {
	return vmTargetVMSpec{
		Name: "app01", CPUs: 4, MemoryMiB: 8192, Firmware: "uefi", SecureBoot: true, TPM: true, Windows: true, Storage: "local-lvm",
		Disks: []vmTargetDisk{{Size: 40<<30 + 512}, {Size: 100 << 30, Bus: "sata"}},
		NICs:  []vmTargetNIC{{Network: "vmbr0", VLAN: 20, MAC: "00:50:56:aa:bb:cc"}},
		ISO:   iso,
	}
}

func vmFastPlatform(platform vmTargetPlatform) {
	switch p := platform.(type) {
	case *vmProxmox:
		p.http.pollDelay = time.Millisecond
	case *vmNutanix:
		p.http.pollDelay = time.Millisecond
	case *vmHyperCore:
		p.http.pollDelay = time.Millisecond
	}
}

func TestProxmoxCreatesBootsAndCutsOver(t *testing.T) {
	fake := &vmFakePlatform{state: "stopped"}
	tasks := 0
	var uploaded []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=migrate@pve!move="+vmTargetTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw := fake.record(r)
		path := strings.TrimPrefix(r.URL.Path, "/api2/json")
		upid := func() { tasks++; vmJSON(w, map[string]string{"data": "UPID:pve1:task"}) }
		switch {
		case path == "/version":
			vmJSON(w, map[string]interface{}{"data": map[string]string{"version": "8.3.0"}})
		case path == "/nodes":
			vmJSON(w, map[string]interface{}{"data": []map[string]string{{"node": "pve1", "status": "online"}}})
		case path == "/nodes/pve1/storage":
			vmJSON(w, map[string]interface{}{"data": []map[string]interface{}{{"storage": "local-lvm", "type": "lvmthin", "content": "images", "avail": 1 << 40}}})
		case path == "/nodes/pve1/network":
			vmJSON(w, map[string]interface{}{"data": []map[string]string{{"iface": "vmbr0", "type": "bridge"}}})
		case path == "/nodes/pve1/storage/local/content":
			vmJSON(w, map[string]interface{}{"data": []map[string]string{{"volid": "local:iso/old.iso"}}})
		case path == "/nodes/pve1/storage/local/upload":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			reader := multipart.NewReader(strings.NewReader(string(raw)), params["boundary"])
			for part, err := reader.NextPart(); err == nil; part, err = reader.NextPart() {
				if part.FormName() == "filename" {
					uploaded, _ = io.ReadAll(part)
				}
			}
			if r.ContentLength != int64(len(raw)) {
				t.Errorf("upload length %d, body %d", r.ContentLength, len(raw))
			}
			upid()
		case strings.HasPrefix(path, "/nodes/pve1/tasks/"):
			vmJSON(w, map[string]interface{}{"data": map[string]string{"status": "stopped", "exitstatus": "OK"}})
		case path == "/cluster/resources":
			vmJSON(w, map[string]interface{}{"data": []map[string]interface{}{{"vmid": 100, "name": "dc01", "status": "running"}}})
		case path == "/cluster/nextid":
			vmJSON(w, map[string]interface{}{"data": "101"})
		case path == "/nodes/pve1/qemu" && r.Method == http.MethodPost:
			upid()
		case path == "/nodes/pve1/qemu/101/status/start":
			fake.state = "running"
			upid()
		case path == "/nodes/pve1/qemu/101/status/stop":
			fake.state = "stopped"
			upid()
		case path == "/nodes/pve1/qemu/101/status/current":
			vmJSON(w, map[string]interface{}{"data": map[string]string{"status": fake.state}})
		case path == "/nodes/pve1/qemu/101/config" && r.Method == http.MethodGet:
			vmJSON(w, map[string]interface{}{"data": map[string]string{"boot": "order=ide2;scsi0"}})
		case path == "/nodes/pve1/qemu/101/config":
			upid()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	connection := vmTestConnection(t, server, vmTargetProxmox, "migrate@pve!move")
	platform, err := newVMTargetPlatform(connection)
	if err != nil {
		t.Fatal(err)
	}
	vmFastPlatform(platform)
	inventory, err := platform.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Version != "Proxmox VE 8.3.0" || len(inventory.Storages) != 1 || inventory.Networks[0].ID != "vmbr0" {
		t.Fatalf("inventory %+v", inventory)
	}
	iso, err := platform.UploadISO(ctx, "local/receiver.iso", strings.NewReader("ISO-BYTES"), 9)
	if err != nil || iso != "local:iso/receiver.iso" || string(uploaded) != "ISO-BYTES" {
		t.Fatalf("upload %q %v %q", iso, err, uploaded)
	}
	if _, err := platform.UploadISO(ctx, "local/old.iso", strings.NewReader("x"), 1); err == nil {
		t.Fatal("an existing ISO was overwritten")
	}

	spec := vmTestSpec(iso)
	spec.Name = "dc01"
	if _, err := vmTargetCreate(ctx, platform, spec, nil); err == nil || !strings.Contains(err.Error(), "already exists") || fake.saw("POST /api2/json/nodes/pve1/qemu") {
		t.Fatalf("an existing VM name was reused: %v", err)
	}
	vm, err := vmTargetCreate(ctx, platform, vmTestSpec(iso), nil)
	if err != nil || vm.ID != "101" || vm.State != "running" {
		t.Fatalf("create %+v %v", vm, err)
	}
	values, _ := url.ParseQuery(string(fake.bodies["POST /api2/json/nodes/pve1/qemu"]))
	want := map[string]string{
		"vmid": "101", "name": "app01", "cores": "4", "memory": "8192", "bios": "ovmf", "machine": "q35", "ostype": "win10",
		"virtio0": "local-lvm:41", "sata0": "local-lvm:100", "ide2": "local:iso/receiver.iso,media=cdrom", "boot": "order=ide2;virtio0",
		"efidisk0": "local-lvm:1,efitype=4m,pre-enrolled-keys=1", "tpmstate0": "local-lvm:1,version=v2.0",
		"net0": "virtio=00:50:56:AA:BB:CC,bridge=vmbr0,tag=20",
	}
	for key, value := range want {
		if values.Get(key) != value {
			t.Errorf("%s = %q, want %q", key, values.Get(key), value)
		}
	}
	if err := vmTargetCutover(ctx, platform, vm.ID, nil); err != nil {
		t.Fatal(err)
	}
	config, _ := url.ParseQuery(string(fake.bodies["POST /api2/json/nodes/pve1/qemu/101/config"]))
	if config.Get("boot") != "order=scsi0" || config.Get("ide2") != "none,media=cdrom" || fake.state != "running" || !fake.saw("POST /api2/json/nodes/pve1/qemu/101/status/stop") {
		t.Fatalf("cutover config %v, state %s", config, fake.state)
	}

	// A wrong token is reported without the secret.
	bad := connection
	bad.Password = "wrong-" + vmTargetTestSecret
	wrong, _ := newVMTargetPlatform(bad)
	if _, err := wrong.Inventory(ctx); err == nil || strings.Contains(err.Error(), vmTargetTestSecret) || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := newVMTargetPlatform(vmTargetConnection{Platform: vmTargetProxmox, Server: "pve", User: "root", Password: "x"}); err == nil {
		t.Fatal("a password was accepted where an API token is needed")
	}
}

func TestNutanixCreatesBootsAndCutsOver(t *testing.T) {
	fake := &vmFakePlatform{state: "OFF"}
	var vmSpec map[string]interface{}
	var uploaded []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "admin" || password != vmTargetTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw := fake.record(r)
		path := strings.TrimPrefix(r.URL.Path, "/api/nutanix/v3")
		task := map[string]interface{}{"execution_context": map[string]string{"task_uuid": "t1"}}
		switch {
		case path == "/clusters/list":
			vmJSON(w, map[string]interface{}{"entities": []map[string]interface{}{
				{"metadata": map[string]string{"uuid": "pc"}, "status": map[string]interface{}{"name": "pc01", "resources": map[string]interface{}{"config": map[string]interface{}{"service_list": []string{"PRISM_CENTRAL"}}}}},
				{"metadata": map[string]string{"uuid": "c1"}, "status": map[string]interface{}{"name": "ahv01", "resources": map[string]interface{}{"config": map[string]interface{}{"service_list": []string{"AOS"}, "build": map[string]string{"version": "6.8"}}}}},
			}})
		case path == "/subnets/list":
			vmJSON(w, map[string]interface{}{"entities": []map[string]interface{}{{"metadata": map[string]string{"uuid": "s1"}, "status": map[string]interface{}{"name": "Servers", "resources": map[string]interface{}{"vlan_id": 20}}}}})
		case path == "/images/list":
			vmJSON(w, map[string]interface{}{"entities": []interface{}{}})
		case path == "/images" && r.Method == http.MethodPost:
			vmJSON(w, map[string]interface{}{"metadata": map[string]string{"uuid": "img1"}, "status": task})
		case path == "/images/img1/file":
			uploaded = raw
		case path == "/tasks/t1":
			vmJSON(w, map[string]string{"status": "SUCCEEDED"})
		case path == "/vms/list":
			var body map[string]interface{}
			json.Unmarshal(raw, &body)
			if body["filter"] == "vm_name==dc01" {
				vmJSON(w, map[string]interface{}{"entities": []map[string]interface{}{{"metadata": map[string]string{"uuid": "v0"}, "status": map[string]interface{}{"name": "dc01"}}}})
				return
			}
			vmJSON(w, map[string]interface{}{"entities": []interface{}{}})
		case path == "/vms" && r.Method == http.MethodPost:
			json.Unmarshal(raw, &vmSpec)
			vmJSON(w, map[string]interface{}{"metadata": map[string]string{"uuid": "v1"}, "status": task})
		case path == "/vms/v1" && r.Method == http.MethodGet:
			spec := vmSpec["spec"].(map[string]interface{})
			resources := spec["resources"].(map[string]interface{})
			resources["power_state"] = fake.state
			vmJSON(w, map[string]interface{}{"metadata": map[string]interface{}{"uuid": "v1", "kind": "vm", "spec_version": 3}, "spec": spec, "status": map[string]interface{}{"resources": map[string]string{"power_state": fake.state}}})
		case path == "/vms/v1" && r.Method == http.MethodPut:
			var body map[string]interface{}
			json.Unmarshal(raw, &body)
			if body["metadata"].(map[string]interface{})["spec_version"] != float64(3) {
				t.Error("the update did not send the spec version back")
			}
			vmSpec["spec"] = body["spec"]
			fake.state = body["spec"].(map[string]interface{})["resources"].(map[string]interface{})["power_state"].(string)
			vmJSON(w, map[string]interface{}{"status": task})
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
	inventory, err := platform.Inventory(ctx)
	if err != nil || len(inventory.Nodes) != 1 || inventory.Nodes[0].Detail != "AOS 6.8" || inventory.Networks[0].Detail != "VLAN 20" {
		t.Fatalf("inventory %+v %v", inventory, err)
	}
	iso, err := platform.UploadISO(ctx, "receiver.iso", strings.NewReader("ISO"), 3)
	if err != nil || iso != "img1" || string(uploaded) != "ISO" {
		t.Fatalf("upload %q %v", iso, err)
	}
	spec := vmTestSpec(iso)
	spec.Name = "dc01"
	if _, err := vmTargetCreate(ctx, platform, spec, nil); err == nil || fake.saw("POST /api/nutanix/v3/vms") {
		t.Fatalf("an existing VM name was reused: %v", err)
	}
	spec = vmTestSpec(iso)
	spec.NICs[0].Network = "s1"
	spec.Storage = ""
	vm, err := vmTargetCreate(ctx, platform, spec, nil)
	if err != nil || vm.ID != "v1" || fake.state != "ON" {
		t.Fatalf("create %+v %v state %s", vm, err, fake.state)
	}
	raw, _ := json.Marshal(vmSpec)
	for _, want := range []string{`"cluster_reference":{"kind":"cluster","uuid":"c1"}`, `"boot_type":"SECURE_BOOT"`, `"machine_type":"Q35"`, `"vtpm_enabled":true`, `"disk_size_mib":40961`, `"adapter_type":"SATA"`, `"data_source_reference":{"kind":"image","uuid":"img1"}`, `"mac_address":"00:50:56:aa:bb:cc"`, `"memory_size_mib":8192`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("VM body lacks %s: %s", want, raw)
		}
	}
	if err := vmTargetCutover(ctx, platform, vm.ID, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(vmSpec)
	if strings.Contains(string(raw), `"uuid":"img1"`) || fake.state != "ON" {
		t.Fatalf("cutover left the receiver media or the VM off: %s", raw)
	}
}

func TestHyperCoreCreatesBootsAndCutsOver(t *testing.T) {
	fake := &vmFakePlatform{state: "SHUTOFF"}
	var created map[string]interface{}
	boot := []interface{}{}
	var uploaded []byte
	isoReady := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "admin" || password != vmTargetTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw := fake.record(r)
		path := strings.TrimPrefix(r.URL.Path, "/rest/v1")
		switch {
		case path == "/Cluster":
			vmJSON(w, []map[string]string{{"clusterName": "hc3", "icosVersion": "9.4.30"}})
		case path == "/VirDomain" && r.Method == http.MethodGet:
			vmJSON(w, []map[string]interface{}{{"uuid": "d0", "name": "DC01", "state": "RUNNING", "netDevs": []map[string]int{{"vlan": 0}, {"vlan": 20}}}})
		case path == "/ISO" && r.Method == http.MethodGet:
			vmJSON(w, []map[string]string{{"name": "old.iso"}})
		case path == "/ISO" && r.Method == http.MethodPost:
			vmJSON(w, map[string]string{"taskTag": "1", "createdUUID": "iso1"})
		case path == "/ISO/iso1/data":
			uploaded = raw
			vmJSON(w, map[string]string{})
		case path == "/ISO/iso1" && r.Method == http.MethodPatch:
			if string(raw) != `{"readyForInsert":true}` {
				t.Errorf("ISO update %s", raw)
			}
			isoReady = true
			vmJSON(w, map[string]string{"taskTag": "5"})
		case path == "/ISO/iso1":
			vmJSON(w, []map[string]string{{"path": "scribe/iso1"}})
		case strings.HasPrefix(path, "/TaskTag/"):
			vmJSON(w, []map[string]string{{"state": "COMPLETE"}})
		case path == "/VirDomain" && r.Method == http.MethodPost:
			json.Unmarshal(raw, &created)
			vmJSON(w, map[string]string{"taskTag": "2", "createdUUID": "d1"})
		case path == "/VirDomain/d1" && r.Method == http.MethodGet:
			vmJSON(w, []map[string]interface{}{{"uuid": "d1", "name": "app01", "state": fake.state, "blockDevs": []map[string]interface{}{
				{"uuid": "cd", "type": "IDE_CDROM", "slot": 2}, {"uuid": "b1", "type": "IDE_DISK", "slot": 1}, {"uuid": "b0", "type": "VIRTIO_DISK", "slot": 0}, {"uuid": "nv", "type": "NVRAM", "slot": -1},
			}}})
		case path == "/VirDomain/d1" && r.Method == http.MethodPatch:
			var body map[string][]interface{}
			json.Unmarshal(raw, &body)
			boot = body["bootDevices"]
			vmJSON(w, map[string]string{"taskTag": "3"})
		case path == "/VirDomain/action":
			var actions []map[string]string
			json.Unmarshal(raw, &actions)
			if len(actions) != 1 || actions[0]["virDomainUUID"] != "d1" || actions[0]["cause"] != "INTERNAL" {
				t.Errorf("action %s", raw)
			}
			if actions[0]["actionType"] == "START" {
				fake.state = "RUNNING"
			} else {
				fake.state = "SHUTOFF"
			}
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
	inventory, err := platform.Inventory(ctx)
	if err != nil || inventory.Version != "HyperCore 9.4.30" || len(inventory.Networks) != 2 || inventory.Networks[1].ID != "20" {
		t.Fatalf("inventory %+v %v", inventory, err)
	}
	if _, err := platform.UploadISO(ctx, "OLD.iso", strings.NewReader("x"), 1); err == nil {
		t.Fatal("an existing ISO was overwritten")
	}
	iso, err := platform.UploadISO(ctx, "receiver.iso", strings.NewReader("ISO"), 3)
	if err != nil || iso != "scribe/iso1" || string(uploaded) != "ISO" || !isoReady {
		t.Fatalf("upload %q %v", iso, err)
	}
	spec := vmTestSpec(iso)
	spec.Name = "dc01"
	if _, err := vmTargetCreate(ctx, platform, spec, nil); err == nil {
		t.Fatal("an existing VM name was reused")
	}
	spec = vmTestSpec(iso)
	spec.NICs[0].Network = "20"
	vm, err := vmTargetCreate(ctx, platform, spec, nil)
	if err != nil || vm.ID != "d1" || fake.state != "RUNNING" {
		t.Fatalf("create %+v %v", vm, err)
	}
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), "machineType\":") {
		t.Errorf("HyperCore 9.3 and later take machineTypeKeyword, not a machine type: %s", raw)
	}
	for _, want := range []string{`"machineTypeKeyword":"tpm"`, `"cacheMode":"WRITETHROUGH","capacity":42950721536,"slot":0,"type":"VIRTIO_DISK","uuid":"primaryDrive"`, `"path":"scribe/iso1","slot":2,"type":"IDE_CDROM"`, `"type":"NVRAM"`, `"type":"VTPM"`, `"vlan":20`, `"mem":8589934592`, `"numVCPU":4`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("VM body lacks %s: %s", want, raw)
		}
	}
	if len(boot) != 3 || boot[0] != "cd" || boot[1] != "b0" {
		t.Fatalf("boot order before cutover %v", boot)
	}
	if err := vmTargetCutover(ctx, platform, vm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if len(boot) != 1 || boot[0] != "b0" || fake.state != "RUNNING" {
		t.Fatalf("boot order after cutover %v, state %s", boot, fake.state)
	}
}

func TestTargetConnectionsCheckCertificatesAndSpecs(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vmJSON(w, []map[string]string{{"clusterName": "hc3", "icosVersion": "9.4"}})
	}))
	defer server.Close()
	connection := vmTestConnection(t, server, vmTargetScale, "admin")
	connection.Thumbprint = ""
	platform, _ := newVMTargetPlatform(connection)
	_, err := platform.Inventory(context.Background())
	var certificate *vmCertificateError
	if !errors.As(err, &certificate) || certificate.Thumbprint == "" {
		t.Fatalf("an untrusted certificate was accepted or not reported: %v", err)
	}
	connection = vmTestConnection(t, server, vmTargetScale, "admin")
	platform, _ = newVMTargetPlatform(connection)
	if _, err := platform.Inventory(context.Background()); err != nil {
		t.Fatalf("the trusted thumbprint was refused: %v", err)
	}
	for _, platform := range []string{vmTargetAzure, vmTargetHyperV, vmTargetPhysical} {
		if _, err := newVMTargetPlatform(vmTargetConnection{Platform: platform, Server: "x", User: "u", Password: "p"}); err == nil {
			t.Fatalf("%s was offered VM creation", platform)
		}
	}
	if _, err := vmTargetBaseURL("http://pve", "8006", "/"); err == nil {
		t.Fatal("plain HTTP was accepted")
	}
	if u, _ := vmTargetBaseURL("https://pve.example.com/", "8006", "/api2/json"); u.Host != "pve.example.com:8006" {
		t.Fatalf("host %s", u.Host)
	}
	bad := []func(*vmTargetVMSpec){
		func(s *vmTargetVMSpec) { s.Name = "../x" },
		func(s *vmTargetVMSpec) { s.CPUs = 0 },
		func(s *vmTargetVMSpec) { s.MemoryMiB = 1 },
		func(s *vmTargetVMSpec) { s.Firmware = "bios" },
		func(s *vmTargetVMSpec) { s.Disks = nil },
		func(s *vmTargetVMSpec) { s.Disks[0].Bus = "nvme0" },
		func(s *vmTargetVMSpec) { s.NICs[0].MAC = "zz" },
		func(s *vmTargetVMSpec) { s.NICs[0].VLAN = 5000 },
		func(s *vmTargetVMSpec) { s.ISO = "" },
	}
	for index, change := range bad {
		spec := vmTestSpec("iso")
		change(&spec)
		if err := vmValidateTargetSpec(&spec); err == nil {
			t.Errorf("bad spec %d was accepted", index)
		}
	}
}

// Machine types by HyperCore version, as Scale's own Ansible collection
// sends them.
func TestHyperCoreMachineTypeFollowsTheClusterVersion(t *testing.T) {
	for _, c := range []struct {
		version, firmware string
		tpm               bool
		want              string
	}{
		{"9.4.30.217736", "bios", false, `"options":{"attachGuestToolsISO":false,"machineTypeKeyword":"bios"}`},
		{"9.3.1", "uefi", false, `"machineTypeKeyword":"uefi"`},
		{"9.3.1", "uefi", true, `"machineTypeKeyword":"tpm"`},
		{"9.2.13.211102", "bios", false, `"machineType":"scale-7.2"`},
		{"9.2.13", "uefi", false, `"machineType":"scale-8.10"`},
		{"9.2.13", "uefi", true, `"machineType":"scale-uefi-tpm-9.2"`},
		{"9.1.14", "uefi", false, `"machineType":"scale-8.10"`},
	} {
		spec := vmTestSpec("scribe/iso")
		spec.Firmware, spec.TPM, spec.SecureBoot = c.firmware, c.tpm, false
		spec.NICs[0].Network = "20"
		body, err := vmHyperCoreDomainBody(spec, c.version)
		if err != nil {
			t.Fatalf("%s %s: %v", c.version, c.firmware, err)
		}
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), c.want) {
			t.Errorf("%s %s tpm=%v: want %s in %s", c.version, c.firmware, c.tpm, c.want, raw)
		}
		if (c.firmware == "uefi") != strings.Contains(string(raw), `"type":"NVRAM"`) {
			t.Errorf("%s %s: NVRAM device wrong in %s", c.version, c.firmware, raw)
		}
	}
	spec := vmTestSpec("scribe/iso")
	spec.TPM = true
	spec.NICs[0].Network = "20"
	if _, err := vmHyperCoreDomainBody(spec, "9.1.14"); err == nil {
		t.Fatal("a virtual TPM was requested from HyperCore 9.1, which has none")
	}
	if _, err := vmHyperCoreDomainBody(spec, "unknown"); err == nil {
		t.Fatal("an unreadable version was accepted")
	}
}

func TestProxmoxOSTypeFollowsTheWindowsVersion(t *testing.T) {
	for guest, want := range map[string]string{
		"Microsoft Windows Server 2019 (64-bit)": "win10",
		"Microsoft Windows Server 2016 or later": "win10",
		"Microsoft Windows Server 2022 (64-bit)": "win11",
		"Microsoft Windows Server 2025 Standard": "win11",
		"Microsoft Windows 11 (64-bit)":          "win11",
	} {
		spec := vmTestSpec("local:iso/r.iso")
		spec.GuestOS = guest
		values, err := vmProxmoxCreateValues("101", spec)
		if err != nil || values.Get("ostype") != want {
			t.Errorf("%s: ostype %q, want %s (%v)", guest, values.Get("ostype"), want, err)
		}
	}
}
