package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The receiver VM ("source") must never be changed by a test failover: only
// a clone is made, isolated, booted from disk, and deleted at the end.

func TestProxmoxTestFailoverClonesIsolatesAndDeletesOnlyTheClone(t *testing.T) {
	var mu sync.Mutex
	vms := map[string]map[string]string{"150": {"name": "APP01", "status": "running"}}
	configs := map[string]map[string]string{}
	var touched []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=migrate@pve!move="+vmTargetTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			r.Body.Read(raw)
		}
		form, _ := url.ParseQuery(string(raw))
		mu.Lock()
		defer mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api2/json")
		if r.Method != http.MethodGet {
			touched = append(touched, r.Method+" "+path)
		}
		upid := func() { vmJSON(w, map[string]string{"data": "UPID:pve1:task"}) }
		parts := strings.Split(strings.TrimPrefix(path, "/nodes/pve1/qemu/"), "/")
		id := parts[0]
		switch {
		case strings.HasPrefix(path, "/nodes/pve1/tasks/"):
			vmJSON(w, map[string]interface{}{"data": map[string]string{"status": "stopped", "exitstatus": "OK"}})
		case path == "/cluster/resources":
			list := []map[string]interface{}{}
			for vmid, vm := range vms {
				list = append(list, map[string]interface{}{"vmid": json.Number(vmid), "name": vm["name"], "status": vm["status"]})
			}
			vmJSON(w, map[string]interface{}{"data": list})
		case path == "/cluster/nextid":
			vmJSON(w, map[string]interface{}{"data": "205"})
		case len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost:
			if form.Get("full") != "1" || form.Get("newid") != "205" || !strings.Contains(form.Get("description"), "Test failover") {
				t.Errorf("clone request %v", form)
			}
			vms["205"] = map[string]string{"name": form.Get("name"), "status": "stopped"}
			configs["205"] = map[string]string{"net0": "virtio=BC:24:11:00:00:01,bridge=vmbr0,tag=20", "net1": "e1000=BC:24:11:00:00:02,bridge=vmbr1,link_down=0", "ide2": "local:iso/receiver.iso,media=cdrom", "scsi0": "local-lvm:vm-205-disk-0,size=40G", "boot": "order=ide2;scsi0"}
			upid()
		case len(parts) == 2 && parts[1] == "config" && r.Method == http.MethodGet:
			vmJSON(w, map[string]interface{}{"data": configs[id]})
		case len(parts) == 2 && parts[1] == "config":
			for key := range form {
				configs[id][key] = form.Get(key)
			}
			upid()
		case len(parts) == 3 && parts[2] == "start":
			vms[id]["status"] = "running"
			upid()
		case len(parts) == 3 && parts[2] == "stop":
			vms[id]["status"] = "stopped"
			upid()
		case len(parts) == 3 && parts[2] == "current":
			vmJSON(w, map[string]interface{}{"data": map[string]string{"status": vms[id]["status"]}})
		case len(parts) == 1 && r.Method == http.MethodDelete:
			if r.URL.Query().Get("purge") != "1" || r.URL.Query().Get("destroy-unreferenced-disks") != "1" {
				t.Errorf("delete query %v", r.URL.Query())
			}
			delete(vms, id)
			upid()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	connection := vmTestConnection(t, server, vmTargetProxmox, "migrate@pve!move")
	connection.Node = "pve1"
	platform, err := newVMTargetPlatform(connection)
	if err != nil {
		t.Fatal(err)
	}
	vmFastPlatform(platform)
	ctx := context.Background()
	if _, err := vmTargetTestStart(ctx, platform, "150", "APP01", nil, nil); err == nil {
		t.Fatal("a test VM took an existing VM's name")
	}
	clone, err := vmTargetTestStart(ctx, platform, "150", "APP01-TEST", nil, nil)
	if err != nil || clone.ID != "205" {
		t.Fatalf("start %+v %v", clone, err)
	}
	mu.Lock()
	if !strings.Contains(configs["205"]["net0"], "link_down=1") || !strings.Contains(configs["205"]["net1"], "link_down=1") || strings.Contains(configs["205"]["net1"], "link_down=0") {
		t.Errorf("the clone's network is connected: %v", configs["205"])
	}
	if vms["205"]["status"] != "running" || vms["150"]["status"] != "running" {
		t.Errorf("states %v", vms)
	}
	for _, request := range touched {
		if strings.Contains(request, "/qemu/150") && !strings.HasSuffix(request, "/qemu/150/clone") {
			t.Errorf("the receiver VM was changed: %s", request)
		}
	}
	mu.Unlock()
	if err := vmTargetTestEnd(ctx, platform, "150", "APP01-TEST", nil); err == nil {
		t.Fatal("ending deleted a VM that is not the clone")
	}
	if err := vmTargetTestEnd(ctx, platform, clone.ID, clone.Name, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := vms["205"]; ok {
		t.Fatal("the clone was not deleted")
	}
	if vms["150"]["status"] != "running" {
		t.Fatal("the receiver VM was changed")
	}
}

func TestNutanixTestFailoverClonesWithoutAdaptersAndDeletesOnlyTheClone(t *testing.T) {
	var mu sync.Mutex
	type vm struct {
		name, power string
		resources   map[string]interface{}
	}
	vms := map[string]*vm{"v0": {name: "APP01", power: "ON", resources: map[string]interface{}{"nic_list": []interface{}{map[string]interface{}{"subnet_reference": map[string]string{"uuid": "s1"}}}}}}
	var touched []string
	task := map[string]interface{}{"execution_context": map[string]string{"task_uuid": "t1"}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "admin" || password != vmTargetTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api/nutanix/v3")
		if r.Method != http.MethodGet && path != "/vms/list" {
			touched = append(touched, r.Method+" "+path)
		}
		id := strings.Split(strings.TrimPrefix(path, "/vms/"), "/")[0]
		switch {
		case path == "/tasks/t1":
			vmJSON(w, map[string]string{"status": "SUCCEEDED"})
		case path == "/vms/list":
			list := []map[string]interface{}{}
			for uuid, v := range vms {
				if body["filter"] == "vm_name=="+v.name {
					list = append(list, map[string]interface{}{"metadata": map[string]string{"uuid": uuid}, "status": map[string]interface{}{"name": v.name}})
				}
			}
			vmJSON(w, map[string]interface{}{"entities": list})
		case path == "/vms/v0/clone" && r.Method == http.MethodPost:
			uuid, _ := body["metadata"].(map[string]interface{})["uuid"].(string)
			override := body["override_spec"].(map[string]interface{})
			if nics, ok := override["nic_list"].([]interface{}); !ok || len(nics) != 0 {
				t.Errorf("the clone keeps adapters: %v", override)
			}
			vms[uuid] = &vm{name: override["name"].(string), power: "OFF", resources: map[string]interface{}{"nic_list": []interface{}{}, "disk_list": []interface{}{map[string]interface{}{"device_properties": map[string]string{"device_type": "CDROM"}, "data_source_reference": map[string]string{"uuid": "iso"}}}}}
			vmJSON(w, map[string]interface{}{"task_uuid": "t1"})
		case r.Method == http.MethodGet && vms[id] != nil:
			v := vms[id]
			v.resources["power_state"] = v.power
			vmJSON(w, map[string]interface{}{"metadata": map[string]interface{}{"uuid": id, "kind": "vm", "spec_version": 1}, "spec": map[string]interface{}{"name": v.name, "resources": v.resources}, "status": map[string]interface{}{"resources": map[string]string{"power_state": v.power}}})
		case r.Method == http.MethodPut && vms[id] != nil:
			resources := body["spec"].(map[string]interface{})["resources"].(map[string]interface{})
			vms[id].resources = resources
			vms[id].power = resources["power_state"].(string)
			vmJSON(w, map[string]interface{}{"status": task})
		case r.Method == http.MethodDelete && vms[id] != nil:
			delete(vms, id)
			vmJSON(w, map[string]interface{}{"status": task})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	platform, err := newVMTargetPlatform(vmTestConnection(t, server, vmTargetNutanix, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	vmFastPlatform(platform)
	ctx := context.Background()
	clone, err := vmTargetTestStart(ctx, platform, "v0", "APP01-TEST", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	created := vms[clone.ID]
	if created == nil || created.power != "ON" || len(created.resources["nic_list"].([]interface{})) != 0 {
		t.Fatalf("clone %+v", created)
	}
	for _, request := range touched {
		if strings.Contains(request, "/vms/v0") && request != "POST /vms/v0/clone" {
			t.Errorf("the receiver VM was changed: %s", request)
		}
	}
	mu.Unlock()
	if err := vmTargetTestEnd(ctx, platform, clone.ID, clone.Name, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if vms[clone.ID] != nil || vms["v0"] == nil || vms["v0"].power != "ON" {
		t.Fatalf("after the test: %+v", vms)
	}
}

func TestHyperCoreTestFailoverClonesDisconnectsAndDeletesOnlyTheClone(t *testing.T) {
	var mu sync.Mutex
	domains := map[string]*vmHyperCoreDomain{"d1": {UUID: "d1", Name: "APP01", State: "RUNNING"}}
	var touched []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "admin" || password != vmTargetTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body interface{}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/rest/v1")
		if r.Method != http.MethodGet {
			touched = append(touched, r.Method+" "+path)
		}
		task := map[string]string{"taskTag": "tg"}
		switch {
		case path == "/TaskTag/tg":
			vmJSON(w, []map[string]string{{"state": "COMPLETE"}})
		case path == "/VirDomain" && r.Method == http.MethodGet:
			list := []*vmHyperCoreDomain{}
			for _, d := range domains {
				list = append(list, d)
			}
			vmJSON(w, list)
		case path == "/VirDomain/d1/clone":
			template := body.(map[string]interface{})["template"].(map[string]interface{})
			domains["c1"] = &vmHyperCoreDomain{UUID: "c1", Name: template["name"].(string), State: "SHUTOFF",
				BlockDevs: []vmHyperCoreBlockDevice{{UUID: "cd", Type: "IDE_CDROM"}, {UUID: "b1", Type: "VIRTIO_DISK"}},
				NetDevs:   []vmHyperCoreNetDevice{{UUID: "n1", VLAN: 20, Connected: true}, {UUID: "n2", Connected: true}}}
			vmJSON(w, map[string]string{"taskTag": "tg", "createdUUID": "c1"})
		case strings.HasPrefix(path, "/VirDomainNetDevice/") && r.Method == http.MethodPatch:
			for _, d := range domains {
				for i := range d.NetDevs {
					if "/VirDomainNetDevice/"+d.NetDevs[i].UUID == path {
						d.NetDevs[i].Connected = body.(map[string]interface{})["connected"].(bool)
					}
				}
			}
			vmJSON(w, task)
		case path == "/VirDomain/action":
			action := body.([]interface{})[0].(map[string]interface{})
			d := domains[action["virDomainUUID"].(string)]
			d.State = map[string]string{"START": "RUNNING", "STOP": "SHUTOFF"}[action["actionType"].(string)]
			vmJSON(w, task)
		case strings.HasPrefix(path, "/VirDomain/") && domains[strings.TrimPrefix(path, "/VirDomain/")] != nil:
			d := domains[strings.TrimPrefix(path, "/VirDomain/")]
			switch r.Method {
			case http.MethodGet:
				vmJSON(w, []*vmHyperCoreDomain{d})
			case http.MethodPatch:
				for _, item := range body.(map[string]interface{})["bootDevices"].([]interface{}) {
					d.BootDevices = append(d.BootDevices, item.(string))
				}
				vmJSON(w, task)
			case http.MethodDelete:
				delete(domains, d.UUID)
				vmJSON(w, task)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	platform, err := newVMTargetPlatform(vmTestConnection(t, server, vmTargetScale, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	vmFastPlatform(platform)
	ctx := context.Background()
	clone, err := vmTargetTestStart(ctx, platform, "d1", "APP01-TEST", nil, nil)
	if err != nil || clone.ID != "c1" {
		t.Fatalf("start %+v %v", clone, err)
	}
	mu.Lock()
	c := domains["c1"]
	if c.State != "RUNNING" || c.NetDevs[0].Connected || c.NetDevs[1].Connected || len(c.BootDevices) != 1 || c.BootDevices[0] != "b1" {
		t.Fatalf("clone %+v", c)
	}
	for _, request := range touched {
		if strings.Contains(request, "/VirDomain/d1") && request != "POST /VirDomain/d1/clone" {
			t.Errorf("the receiver VM was changed: %s", request)
		}
	}
	mu.Unlock()
	if err := vmTargetTestEnd(ctx, platform, clone.ID, clone.Name, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if domains["c1"] != nil || domains["d1"].State != "RUNNING" {
		t.Fatal("the clone was not deleted, or the receiver VM changed")
	}
}

// replLocalAgent drives an in-process agent as the console would.
type replLocalAgent struct{ agent *replAgent }

func (l replLocalAgent) Hello(ctx context.Context) (replAgentHello, error) {
	return replAgentHello{}, nil
}
func (l replLocalAgent) Disks(ctx context.Context) ([]replDiskInfo, error) { return nil, nil }
func (l replLocalAgent) Jobs(ctx context.Context) (replAgentJobs, error) {
	return replAgentJobs{Jobs: l.agent.Jobs()}, nil
}
func (l replLocalAgent) SaveJob(ctx context.Context, spec replJobSpec) (replJobView, error) {
	return l.agent.SaveJob(spec)
}
func (l replLocalAgent) Action(ctx context.Context, id, action string) error {
	return l.agent.Action(id, action)
}
func (l replLocalAgent) Schedule(ctx context.Context, id string, at *time.Time) error { return nil }
func (l replLocalAgent) Log(ctx context.Context, id string) ([]string, error)         { return nil, nil }
func (l replLocalAgent) CheckReceiver(ctx context.Context, link replReceiverLink) (replReceiverCheck, error) {
	return l.agent.CheckReceiver(ctx, link)
}

// vmMemoryTarget is a platform in memory that records every change.
type vmMemoryTarget struct {
	mu      sync.Mutex
	vms     map[string]*vmTargetVM
	changes []string
	isolate map[string]bool
	onClone func()
}

func (m *vmMemoryTarget) Inventory(ctx context.Context) (*vmTargetInventory, error) { return nil, nil }
func (m *vmMemoryTarget) UploadISO(ctx context.Context, name string, data io.Reader, size int64) (string, error) {
	return "", errors.New("not here")
}
func (m *vmMemoryTarget) CreateVM(ctx context.Context, spec vmTargetVMSpec) (*vmTargetVM, error) {
	return nil, errors.New("not here")
}
func (m *vmMemoryTarget) FindVM(ctx context.Context, name string) (*vmTargetVM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, vm := range m.vms {
		if strings.EqualFold(vm.Name, name) {
			copy := *vm
			return &copy, nil
		}
	}
	return nil, nil
}
func (m *vmMemoryTarget) change(text string) {
	m.mu.Lock()
	m.changes = append(m.changes, text)
	m.mu.Unlock()
}
func (m *vmMemoryTarget) Start(ctx context.Context, id string) error {
	m.change("start " + id)
	m.mu.Lock()
	m.vms[id].State = "running"
	m.mu.Unlock()
	return nil
}
func (m *vmMemoryTarget) PowerOff(ctx context.Context, id string) error {
	m.change("off " + id)
	m.mu.Lock()
	m.vms[id].State = "stopped"
	m.mu.Unlock()
	return nil
}
func (m *vmMemoryTarget) BootFromDisk(ctx context.Context, id string) error {
	m.change("boot " + id)
	return nil
}
func (m *vmMemoryTarget) State(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.vms[id].State, nil
}
func (m *vmMemoryTarget) CloneVM(ctx context.Context, id, name, note string) (*vmTargetVM, error) {
	if m.onClone != nil {
		m.onClone()
	}
	m.change("clone " + id)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vms["t1"] = &vmTargetVM{ID: "t1", Name: name, State: "stopped"}
	return m.vms["t1"], nil
}
func (m *vmMemoryTarget) Isolate(ctx context.Context, id string) error {
	m.change("isolate " + id)
	return nil
}
func (m *vmMemoryTarget) DeleteVM(ctx context.Context, id string) error {
	m.change("delete " + id)
	m.mu.Lock()
	delete(m.vms, id)
	m.mu.Unlock()
	return nil
}

// The console pauses the job at a whole recovery point, clones while the
// job is paused, resumes it once the clone exists, and the clone is
// isolated before it starts.
func TestTargetTestFailoverPausesClonesAndResumes(t *testing.T) {
	agent, platform, clock, receiver, _ := replTestSetup(t)
	spec := replTestSpec(receiver)
	view, err := agent.SaveJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	target := &vmMemoryTarget{vms: map[string]*vmTargetVM{"r1": {ID: "r1", Name: "APP01", State: "running"}}}
	connection := vmTargetConnection{Platform: vmTargetProxmox, Server: "pve.example.com"}
	client := replLocalAgent{agent}
	// VHDX targets belong to the Hyper-V test failover.
	if _, err := replRunTargetTest(context.Background(), client, target, connection, replTargetTestOptions{Job: id, SourceVM: "APP01", Name: "APP01-TEST"}); err == nil || !strings.Contains(err.Error(), "Hyper-V") {
		t.Fatalf("a VHDX job was accepted: %v", err)
	}
	if got := agent.Jobs()[0].Status.State; got == replStatePaused {
		t.Fatal("a refused test paused the job")
	}
	// Give the job receiver-media disk names; the in-memory receiver
	// stores them as VHDX files.
	original := agent.newSink
	agent.newSink = func(ctx context.Context, link replReceiverLink, target string) (replSink, error) {
		return original(ctx, link, "APP01-"+strings.Replace(target, "-", "", 1)+".vhdx")
	}
	spec.ID = id
	spec.Disks = []replDiskMap{{Source: "0", Target: "disk-0"}, {Source: "1", Target: "disk-1"}}
	if _, err := agent.SaveJob(spec); err != nil {
		t.Fatal(err)
	}
	agent.Tick()
	replWaitIdle(t, agent, id)
	// The source changes and a round is running when the test is asked for.
	platform.disks["0"][7<<20] ^= 0xff
	platform.gate = make(chan struct{})
	clock.Add(6 * time.Minute)
	agent.Tick()
	var pausedAtClone bool
	target.onClone = func() {
		view := agent.Jobs()[0]
		pausedAtClone = view.Status.State == replStatePaused && view.Status.Consistent && view.Status.Rounds == 2
	}
	go func() { time.Sleep(200 * time.Millisecond); close(platform.gate) }()
	var lines []string
	test, err := replRunTargetTest(context.Background(), client, target, connection, replTargetTestOptions{Agent: "a1", Job: id, SourceVM: "APP01", Name: "APP01-TEST", Poll: 20 * time.Millisecond, Log: func(s string) { lines = append(lines, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if !pausedAtClone {
		t.Fatal("the clone was made while the copy was not one whole recovery point")
	}
	if test.VMID != "t1" || test.SourceID != "r1" || test.RecoveryPoint.IsZero() || test.JobName != "APP01" {
		t.Fatalf("record %+v", test)
	}
	want := []string{"clone r1", "isolate t1", "boot t1", "start t1"}
	if strings.Join(target.changes, ",") != strings.Join(want, ",") {
		t.Fatalf("platform changes %v", target.changes)
	}
	view = replWaitIdle(t, agent, id)
	if view.Status.State == replStatePaused || view.Status.PauseAfterRound {
		t.Fatalf("replication did not resume: %+v (%v)", view.Status, lines)
	}
	// Ending the test deletes only the clone.
	if err := vmTargetTestEnd(context.Background(), target, test.VMID, test.Name, nil); err != nil {
		t.Fatal(err)
	}
	if target.vms["t1"] != nil || target.vms["r1"] == nil || target.vms["r1"].State != "running" {
		t.Fatalf("after the test %+v", target.vms)
	}
	// A job the operator paused stays paused.
	if err := agent.Action(id, "pause-consistent"); err != nil {
		t.Fatal(err)
	}
	agent.Tick()
	replWaitIdle(t, agent, id)
	if _, err := replRunTargetTest(context.Background(), client, target, connection, replTargetTestOptions{Job: id, SourceVM: "APP01", Name: "APP01-TEST2", Poll: 20 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if view := agent.Jobs()[0]; view.Status.State != replStatePaused {
		t.Fatalf("an operator pause was undone: %+v", view.Status)
	}
	// A clone that fails to start is removed and replication resumes.
	if _, err := replRunTargetTest(context.Background(), client, target, connection, replTargetTestOptions{Job: id, SourceVM: "NOPE", Name: "X", Poll: 20 * time.Millisecond}); err == nil {
		t.Fatal("a missing receiver VM was accepted")
	}
}
