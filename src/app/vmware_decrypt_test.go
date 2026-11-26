package main

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/types"
)

// Decrypting from New migration (2026-11-26, owner: "if decryption is
// required, it should be streamlined into the wizard").

func TestDisksEncryptedSeparatesVirtualTPMFilesFromDisks(t *testing.T) {
	cases := []struct {
		name string
		vm   vmMachine
		want bool
	}{
		{"plain", vmMachine{}, false},
		{"home only for a virtual TPM", vmMachine{Encrypted: true, EncryptionRead: true, VTPM: true, Disks: []vmDisk{{}}}, false},
		{"an encrypted disk", vmMachine{Encrypted: true, EncryptionRead: true, Disks: []vmDisk{{}, {Encrypted: true}}}, true},
		{"older inventory", vmMachine{Encrypted: true, Disks: []vmDisk{{}}}, true},
		{"shielded Hyper-V VM", vmMachine{Encrypted: true, Source: "hyperv"}, false},
	}
	for _, c := range cases {
		if got := vmDisksEncrypted(c.vm); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrepareResultUnblocksADecryptedVM(t *testing.T) {
	vm := vmMachine{ID: "vm-1", Name: "app01", PowerState: "poweredOn", GuestID: "windows2019srv_64Guest", Firmware: "efi", VTPM: true, Encrypted: true, EncryptionRead: true, ToolsRunning: true,
		Disks: []vmDisk{{Label: "Hard disk 1", CapacityBytes: 40 << 30, Encrypted: true, BootCandidate: true}}}
	inventory := &vmInventory{VMs: []vmMachine{vm}}
	before := assessVM(vm, vmTargetHyperV, time.Now())
	if before.Status != vmStatusBlocked {
		t.Fatalf("an encrypted disk must block: %+v", before)
	}
	if vmApplyPrepareResult(nil, "vm-1", vmPrepareResult{}, time.Now()) {
		t.Fatal("no inventory, nothing to change")
	}
	if !vmApplyPrepareResult(inventory, "vm-1", vmPrepareResult{PowerState: "poweredOff", Encrypted: false, VTPM: false}, time.Now()) {
		t.Fatal("the inventory was not changed")
	}
	got := inventory.VMs[0]
	if got.Encrypted || got.Disks[0].Encrypted || !got.EncryptionRead || got.VTPM || got.ToolsRunning {
		t.Fatalf("decryption not reflected: %+v", got)
	}
	for _, r := range got.Readiness {
		if r.Target != vmTargetHyperV {
			continue
		}
		for _, f := range r.Findings {
			if f.Code == "encrypted" || f.Code == "encrypted-home" {
				t.Fatalf("still flagged encrypted after decrypting: %+v", f)
			}
		}
		return
	}
	t.Fatal("readiness was not checked again")
}

func TestPrepareBatchRecordsTimesForLiveProgress(t *testing.T) {
	items := []vmPrepareBatchItem{{ID: "a", VM: "a", State: "waiting", Percent: -1}, {ID: "b", VM: "b", State: "waiting", Percent: -1}}
	var mu sync.Mutex
	seen := []string{}
	done, failed, _ := vmPrepareBatchRun(context.Background(), items, func(item vmPrepareBatchItem) (vmPrepareResult, error) {
		if item.ID == "b" {
			return vmPrepareResult{}, errors.New("key provider offline")
		}
		return vmPrepareResult{Done: []string{"Decrypted the VM and its disks."}}, nil
	}, func(list []vmPrepareBatchItem) {
		mu.Lock()
		defer mu.Unlock()
		for _, i := range list {
			if i.State == "running" && i.StartedAt == nil {
				t.Errorf("%s runs without a start time", i.VM)
			}
			seen = append(seen, i.VM+":"+i.State)
		}
	})
	if done != 1 || failed != 1 {
		t.Fatalf("done %d failed %d", done, failed)
	}
	for _, i := range items {
		if i.StartedAt == nil || i.EndedAt == nil || i.EndedAt.Before(*i.StartedAt) || i.Percent != -1 || i.Step != "" {
			t.Fatalf("times or progress not recorded: %+v", i)
		}
	}
	if items[1].Error != "key provider offline" {
		t.Fatalf("%+v", items[1])
	}
}

func TestPrepareTimeoutAllowsALongDecrypt(t *testing.T) {
	if vmPrepareTimeout(vmPrepareSteps{Snapshots: true}) != 45*time.Minute {
		t.Fatal("ordinary steps keep 45 minutes")
	}
	if vmPrepareTimeout(vmPrepareSteps{Decrypt: true}) < 12*time.Hour {
		t.Fatal("decrypting rewrites every encrypted disk and needs far longer")
	}
}

func TestTaskProgressReportsAndNeverHangs(t *testing.T) {
	model := simulator.VPX()
	model.Machine = 1
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	user := server.URL.User.Username()
	password, _ := server.URL.User.Password()
	ctx := context.Background()
	session, err := openVSphereSession(ctx, vmConnectOptions{Server: server.URL.Host, User: user, Password: password, insecureForTest: true}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer session.logout()
	finder := find.NewFinder(session.client, true)
	datacenter, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(datacenter)
	machines, err := finder.VirtualMachineList(ctx, "*")
	if err != nil || len(machines) == 0 {
		t.Fatal(err)
	}
	reports := []int{}
	task, taskErr := machines[0].Reconfigure(ctx, types.VirtualMachineConfigSpec{Annotation: "progress test"})
	if err := vmRunTaskProgress(ctx, task, taskErr, func(p int) { reports = append(reports, p) }); err != nil {
		t.Fatal(err)
	}
	for _, p := range reports {
		if p < 0 || p > 100 {
			t.Fatalf("percent out of range: %v", reports)
		}
	}
	if err := vmRunTaskProgress(ctx, nil, errors.New("refused"), func(int) {}); err == nil {
		t.Fatal("the task error must come back")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	task, taskErr = machines[0].Reconfigure(ctx, types.VirtualMachineConfigSpec{Annotation: "again"})
	finished := make(chan error, 1)
	go func() { finished <- vmRunTaskProgress(cancelled, task, taskErr, func(int) {}) }()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("a cancelled wait must fail")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("waiting on a cancelled context hangs")
	}
}
