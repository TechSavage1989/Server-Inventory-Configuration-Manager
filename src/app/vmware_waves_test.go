package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func vmWaveTestInventory() *vmInventory {
	on := func(name, os string) vmMachine {
		return vmMachine{Name: name, ID: "vm-" + strings.ToLower(name), PowerState: "poweredOn", GuestOSRunning: os}
	}
	win := "Microsoft Windows Server 2022 (64-bit)"
	return &vmInventory{Server: "vcenter.example.com", VMs: []vmMachine{
		on("CORP-DC01", win), on("CORP-DC02", win), on("SQL01", win), on("ERP-DB01", win),
		on("ERP-APP01", win), on("ERP-WEB01", win), on("CRM-APP01", win), on("FILE01", win), on("PRINT01", win),
		on("DEV-WEB01", win), {Name: "OLDBOX", ID: "vm-oldbox", PowerState: "poweredOff", GuestOS: win},
		{Name: "vCLS-1234", ID: "vm-vcls", PowerState: "poweredOn", GuestOS: "Other 3.x or later Linux (64-bit)"},
		on("VEEAM-PROXY01", win), {Name: "TPL", ID: "vm-tpl", Template: true},
	}}
}

func TestSuggestWavesGroupsByRoleAndRisk(t *testing.T) {
	inventory := vmWaveTestInventory()
	plan, size := suggestWavesWith(defaultVMPlan(inventory), inventory, 0, false)
	if size != 2 {
		t.Fatalf("auto size %d, want 2", size)
	}
	wave := func(id string) int { return plan.entry(id).Wave }
	if wave("vm-dev-web01") != 1 || wave("vm-oldbox") != 1 {
		t.Fatalf("pilot: %+v", plan.VMs)
	}
	for _, id := range []string{"vm-vcls", "vm-veeam-proxy01", "vm-tpl"} {
		if e := plan.entry(id); !e.Exclude || e.Why == "" {
			t.Fatalf("%s should be excluded with a reason: %+v", id, e)
		}
	}
	dc1, dc2 := wave("vm-corp-dc01"), wave("vm-corp-dc02")
	if dc1 == dc2 || dc1 != 2 {
		t.Fatalf("domain controllers %d and %d", dc1, dc2)
	}
	counts := map[int]int{}
	for _, item := range plan.VMs {
		if item.Wave > 0 {
			counts[item.Wave]++
		}
	}
	if counts[dc1] != 1 || counts[dc2] != 1 || counts[wave("vm-sql01")] != 1 || counts[wave("vm-erp-db01")] != 1 {
		t.Fatalf("domain controllers and databases must move alone: %v %+v", counts, plan.VMs)
	}
	if wave("vm-erp-app01") != wave("vm-erp-web01") || wave("vm-erp-app01") == 0 {
		t.Fatalf("ERP servers should move together: %+v", plan.VMs)
	}
	for _, item := range plan.VMs {
		if item.Wave > dc2 {
			t.Fatalf("the second domain controller should move last: %+v", plan.VMs)
		}
	}
	if len(plan.Waves) != len(counts) {
		t.Fatalf("every wave needs a reason: %+v", plan.Waves)
	}
	for _, info := range plan.Waves {
		if info.Title == "" || info.Reason == "" {
			t.Fatalf("wave without a reason: %+v", info)
		}
	}
	if !strings.Contains(vmWaveSummaryText(plan), "pilot (2)") {
		t.Fatalf("summary %q", vmWaveSummaryText(plan))
	}
}

func TestSuggestWavesKeepsWhatTheOperatorSet(t *testing.T) {
	inventory := vmWaveTestInventory()
	plan := suggestWaves(defaultVMPlan(inventory), inventory, 10)
	for index := range plan.VMs {
		switch plan.VMs[index].ID {
		case "vm-file01":
			plan.VMs[index].Wave, plan.VMs[index].Manual = 20, true
		case "vm-veeam-proxy01":
			plan.VMs[index].Exclude, plan.VMs[index].Wave, plan.VMs[index].Manual = false, 20, true
		}
	}
	for index := range plan.Waves {
		if plan.Waves[index].Wave == 1 {
			plan.Waves[index].Title, plan.Waves[index].Window, plan.Waves[index].Edited = "Friday pilot", "2026-12-04T22:00", true
		}
	}
	plan.Waves = append(plan.Waves, vmWaveInfo{Wave: 20, Title: "Saturday", Edited: true})
	validated, err := validateVMPlan(plan, inventory)
	if err != nil {
		t.Fatal(err)
	}
	again := suggestWaves(validated, inventory, 3)
	if e := again.entry("vm-file01"); e.Wave != 20 || !e.Manual {
		t.Fatalf("hand-set wave lost: %+v", e)
	}
	if e := again.entry("vm-veeam-proxy01"); e.Exclude || e.Wave != 20 {
		t.Fatalf("hand-included VM excluded again: %+v", e)
	}
	found := map[int]vmWaveInfo{}
	for _, info := range again.Waves {
		found[info.Wave] = info
	}
	if found[1].Title != "Friday pilot" || found[1].Window != "2026-12-04T22:00" || found[20].Title != "Saturday" {
		t.Fatalf("edited waves lost: %+v", again.Waves)
	}
	reset, _ := suggestWavesWith(again, inventory, 3, true)
	if e := reset.entry("vm-veeam-proxy01"); !e.Exclude || e.Manual {
		t.Fatalf("reset should replace hand-set choices: %+v", e)
	}
}

func TestValidateVMWaves(t *testing.T) {
	for _, bad := range [][]vmWaveInfo{
		{{Wave: 0, Title: "x"}},
		{{Wave: 1}, {Wave: 1}},
		{{Wave: 2, Title: "a\nb"}},
		{{Wave: 3, Window: "next friday"}},
		{{Wave: 4, Notes: strings.Repeat("x", 501)}},
	} {
		if _, err := validateVMWaves(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	list, err := validateVMWaves([]vmWaveInfo{{Wave: 3, Title: " C "}, {Wave: 1, Window: "2026-12-04T22:00"}})
	if err != nil || list[0].Wave != 1 || list[1].Title != "C" {
		t.Fatalf("%v %+v", err, list)
	}
}

func TestVMWaveRoles(t *testing.T) {
	win := "Microsoft Windows Server 2019 (64-bit)"
	for name, want := range map[string]string{
		"CORP-DC01": vmWaveRoleDC, "dc2": vmWaveRoleDC, "APPDC-SERVICE": vmWaveRoleGeneral, "LEAD01": vmWaveRoleGeneral,
		"SQLPROD01": vmWaveRoleDatabase, "erp-db01": vmWaveRoleDatabase, "ORACLE01": vmWaveRoleDatabase,
		"web01": vmWaveRoleApp, "CRM-APP02": vmWaveRoleApp, "UAT-SQL01": vmWaveRolePilot, "devbox-01": vmWaveRoleGeneral, "DEV01": vmWaveRolePilot,
	} {
		role, why := vmWaveRoleOf(vmMachine{Name: name, PowerState: "poweredOn", GuestOSRunning: win})
		if role != want || why == "" {
			t.Errorf("%s -> %s, want %s", name, role, want)
		}
	}
	if role, _ := vmWaveRoleOf(vmMachine{Name: "dc01", PowerState: "poweredOn", GuestOS: "Ubuntu Linux (64-bit)"}); role == vmWaveRoleDC {
		t.Error("a Linux VM is not a domain controller")
	}
	if vmWaveStem("ERP-APP01") != "erp" || vmWaveStem("APP-ERP01") != "erp" {
		t.Errorf("stems %s %s", vmWaveStem("ERP-APP01"), vmWaveStem("APP-ERP01"))
	}
}

func TestPrepareBatchChecksAndRunsEveryVM(t *testing.T) {
	inventory := vmWaveTestInventory()
	steps := vmPrepareSteps{Snapshots: true}
	requests := []vmPrepareBatchRequest{{VMID: "vm-file01", Steps: steps}, {VMID: "vm-print01", Steps: steps}, {VMID: "vm-sql01", Steps: steps}}
	if _, err := vmPlanPrepareBatch(inventory, requests, "2 VMs"); err == nil || !strings.Contains(err.Error(), "3 VMs") {
		t.Fatalf("wrong count accepted: %v", err)
	}
	for _, bad := range [][]vmPrepareBatchRequest{
		{{VMID: "vm-vcls", Steps: steps}},
		{{VMID: "vm-tpl", Steps: steps}},
		{{VMID: "vm-file01"}},
		{{VMID: "vm-missing", Steps: steps}},
		{{VMID: "vm-file01", Steps: steps}, {VMID: "vm-file01", Steps: steps}},
	} {
		if _, err := vmPlanPrepareBatch(inventory, bad, vmPrepareBatchConfirmText(len(bad))); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	items, err := vmPlanPrepareBatch(inventory, requests, " 3  vms ")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var ran []string
	var last []vmPrepareBatchItem
	done, failed, stopped := vmPrepareBatchRun(ctx, items, func(item vmPrepareBatchItem) (vmPrepareResult, error) {
		ran = append(ran, item.VM)
		switch item.ID {
		case "vm-file01":
			return vmPrepareResult{Done: []string{"Deleted 1 snapshot."}}, errors.New("vSphere refused")
		case "vm-print01":
			cancel() // Stop while this VM runs: it finishes, the next is not started.
			panic("boom")
		}
		return vmPrepareResult{}, nil
	}, func(list []vmPrepareBatchItem) { last = list })
	if fmt.Sprint(ran) != "[FILE01 PRINT01]" || done != 0 || failed != 2 || stopped != 1 {
		t.Fatalf("ran %v done %d failed %d stopped %d", ran, done, failed, stopped)
	}
	if last[0].State != "failed" || len(last[0].Done) != 1 || last[1].State != "failed" || !strings.Contains(last[1].Error, "boom") || last[2].State != "stopped" {
		t.Fatalf("states %+v", last)
	}
}
