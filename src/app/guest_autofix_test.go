package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGuestAutoFixPlanPairsEachAdapterWithItsHyperVAdapter(t *testing.T) {
	vm := vmMachine{Name: "APP01", NICs: []vmNIC{
		{Label: "Network adapter 1", Network: "VM Network", MAC: "00:50:56:aa:bb:01"},
		{Label: "Network adapter 2", Network: "Backup", MAC: "00:50:56:aa:bb:02", MACInGuest: "00:50:56:aa:bb:22"},
		{Label: "Network adapter 3", Network: "Broken", MAC: "not-a-mac"},
	}}
	data, err := guestAutoFixPlan(vm, guestAutoFixDefaults())
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Auto     guestAutoFixOptions   `json:"auto"`
		Adapters []guestAutoFixAdapter `json:"adapters"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Auto.Network || !plan.Auto.Tools || !plan.Auto.Activate || !plan.Auto.Restart {
		t.Fatalf("defaults: %+v", plan.Auto)
	}
	// Adapter numbers follow the VMware order even when one is skipped, as
	// vmHyperVPlan.Script names them; the guest MAC wins over the vSphere one.
	want := []guestAutoFixAdapter{{Capture: "00-50-56-AA-BB-01", HostAdapter: "Network Adapter 1"}, {Capture: "00-50-56-AA-BB-22", HostAdapter: "Network Adapter 2"}}
	if len(plan.Adapters) != len(want) || plan.Adapters[0] != want[0] || plan.Adapters[1] != want[1] {
		t.Fatalf("adapters: %+v", plan.Adapters)
	}
	script := (&vmHyperVPlan{Name: "APP01", Generation: 2, CPUs: 1, MemoryBytes: 1 << 30, Disks: []string{`D:\a.vhdx`}, Adapters: []vmHyperVAdapter{{Switch: "LAN"}, {Switch: "LAN"}}}).Script()
	if !strings.Contains(script, "'Network Adapter 1'") || !strings.Contains(script, "'Network Adapter 2'") {
		t.Fatal("the creation script no longer names adapters the way the fix-up expects")
	}
}

func TestGuestAutoFixParseStates(t *testing.T) {
	cases := []struct {
		raw   string
		state string
	}{
		{`{"Computer":"APP01","NetworkOk":true,"Steps":[{"Step":"IP settings","Status":"done","Message":"Put back"},{"Step":"VMware Tools","Status":"skipped","Message":"none"}]}`, guestFixDone},
		{`{"NetworkOk":true,"Steps":[{"Step":"Activation","Status":"warning","Message":"KMS not reached"}]}`, guestFixWarning},
		{`{"NetworkOk":false,"Steps":[{"Step":"IP settings","Status":"failed","Message":"No new adapter could be matched"}]}`, guestFixFailed},
		{`{"NetworkOk":false,"Steps":[]}`, guestFixFailed},
	}
	for _, c := range cases {
		report, state, err := guestAutoFixParse(json.RawMessage(c.raw))
		if err != nil || state != c.state {
			t.Fatalf("%s: %s %v", c.raw, state, err)
		}
		summary := guestFixSummary("APP01", state, report)
		if state == guestFixFailed && !strings.Contains(summary, "Prepare and fix up") {
			t.Fatalf("a failed fix-up must point to the manual tool: %s", summary)
		}
	}
	report, _, _ := guestAutoFixParse(json.RawMessage(cases[2].raw))
	if !strings.Contains(guestFixSummary("APP01", guestFixFailed, report), "No new adapter could be matched") {
		t.Fatal("the failure reason is not in the summary")
	}
	if _, state, err := guestAutoFixParse(json.RawMessage(`[1,2]`)); err == nil || state != guestFixFailed {
		t.Fatal("an unreadable result must fail")
	}
}

func TestGuestAutoFixWanted(t *testing.T) {
	vmware := vmMachine{Name: "APP01"}
	if ok, _ := guestAutoFixWanted(vmware, true, `.\Administrator`); !ok {
		t.Fatal("a started VMware VM with an account is fixed up")
	}
	for _, c := range []struct {
		vm      vmMachine
		started bool
		user    string
		why     string
	}{
		{vmMachine{Name: "HV01", Source: vmSourceHyperV}, true, "x", "Hyper-V"},
		{vmware, false, "x", "left off"},
		{vmware, true, " ", "account"},
	} {
		if ok, why := guestAutoFixWanted(c.vm, c.started, c.user); ok || !strings.Contains(why, c.why) {
			t.Fatalf("%+v: %v %s", c, ok, why)
		}
	}
}

func TestGuestFixBoardKeepsTheLatestPerVM(t *testing.T) {
	var board guestFixBoard
	start := time.Now()
	board.Set(guestFixStatus{VM: "APP01", State: guestFixWaiting, Started: start})
	if !board.Busy() {
		t.Fatal("a waiting fix-up keeps the board busy")
	}
	board.Update("app01", guestFixRunning, "Fixing up")
	if got := board.Get("APP01"); got == nil || got.State != guestFixRunning || !got.Finished.IsZero() {
		t.Fatalf("running: %+v", got)
	}
	board.Update("APP01", guestFixDone, "done")
	if board.Busy() || board.Get("APP01").Finished.IsZero() {
		t.Fatal("a finished fix-up is not busy and has a finish time")
	}
	board.Set(guestFixStatus{VM: "APP02", State: guestFixFailed, Started: start.Add(time.Minute)})
	if list := board.List(); len(list) != 2 || list[0].VM != "APP02" {
		t.Fatalf("newest first: %+v", list)
	}
	for i := 0; i < 250; i++ {
		board.Set(guestFixStatus{VM: "VM" + string(rune('A'+i%26)) + strings.Repeat("x", i/26), Started: start.Add(time.Duration(i+2) * time.Minute)})
	}
	if len(board.List()) > 200 {
		t.Fatal("the board grows without limit")
	}
}

func TestAutoSwitchesFillOnlyClearAnswers(t *testing.T) {
	vm := vmMachine{NICs: []vmNIC{{Label: "a", Network: "LAN"}, {Label: "b", Network: "DMZ"}, {Label: "c", Network: "Mapped"}}}
	mapped := map[string]string{"Mapped": "Trunk"}
	filled, notes := vmAutoSwitches(mapped, vm, []string{"lan", "Trunk"})
	if filled["LAN"] != "lan" || filled["DMZ"] != "" || filled["Mapped"] != "Trunk" || len(notes) != 1 {
		t.Fatalf("same name only: %v %v", filled, notes)
	}
	if mapped["LAN"] != "" {
		t.Fatal("the plan's own map was changed")
	}
	filled, notes = vmAutoSwitches(nil, vm, []string{"External"})
	if filled["LAN"] != "External" || filled["DMZ"] != "External" || filled["Mapped"] != "External" || len(notes) != 3 {
		t.Fatalf("only switch: %v %v", filled, notes)
	}
	if filled, _ = vmAutoSwitches(nil, vm, nil); len(filled) != 0 {
		t.Fatalf("no host switches: %v", filled)
	}
}

func TestAcknowledgedReviewCountsAsReady(t *testing.T) {
	r := &vmReadiness{Target: vmTargetHyperV, Status: vmStatusReview, Findings: []vmFinding{
		{Severity: vmSeverityWarning, Code: "snapshots"}, {Severity: vmSeverityWarning, Code: "media"}, {Severity: vmSeverityInfo, Code: "generation"},
	}}
	if vmAcknowledgedReady(r, vmPlanVM{Acknowledged: []string{"hyperv:snapshots"}}) {
		t.Fatal("one open warning keeps the VM in review")
	}
	if !vmAcknowledgedReady(r, vmPlanVM{Acknowledged: []string{"hyperv:snapshots", "hyperv:media"}}) {
		t.Fatal("every warning acknowledged counts as ready")
	}
	if vmAcknowledgedReady(r, vmPlanVM{Acknowledged: []string{"scale:snapshots", "scale:media"}}) {
		t.Fatal("acknowledgements are per target")
	}
	blocked := &vmReadiness{Target: vmTargetHyperV, Status: vmStatusBlocked, Findings: []vmFinding{{Severity: vmSeverityBlocker, Code: "shared-disk"}}}
	if vmAcknowledgedReady(blocked, vmPlanVM{Acknowledged: []string{"hyperv:shared-disk"}}) {
		t.Fatal("a blocked VM can never be acknowledged as ready")
	}
	inventory := vmSampleInventory()
	plan := defaultVMPlan(inventory)
	plan.Target = vmTargetHyperV
	plan.VMs[0].Acknowledged = []string{"hyperv:snapshots"}
	saved, err := validateVMPlan(plan, inventory)
	if err != nil || len(saved.entry(plan.VMs[0].ID).Acknowledged) != 1 {
		t.Fatalf("acknowledgements are kept: %v %+v", err, saved.entry(plan.VMs[0].ID))
	}
	plan.VMs[0].Acknowledged = []string{"<script>"}
	if _, err := validateVMPlan(plan, inventory); err == nil {
		t.Fatal("an invalid acknowledgement is refused")
	}
}
