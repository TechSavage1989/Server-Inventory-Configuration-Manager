//go:build windows

package main

import (
	"strings"
	"testing"
)

// A migration plan copy leaves Discovery reads of the same server and
// collections running, and refuses only what would change its VMs or the
// session it uses (rule 65).
func TestPlanCopyKeepsDiscoveryReadsAndCollectionsAvailable(t *testing.T) {
	a := &application{computerName: "HV01"}
	a.vmware.server = "vc01.example.com"
	a.plans.copying = &mbClaim{Plans: []string{"Wave 1"}, Servers: []string{"vc01.example.com"}, VMs: 4}
	same := func() string { return "" }
	if code, text := a.startVMwareActionWith("connect", same); code != 0 {
		t.Fatal("a read of the same server was refused:", text)
	}
	a.finishVMwareAction("connected", "")
	switchAway := func() string { return a.vmSessionSwitchRefusalLocked("Reading vc02.example.com") }
	if code, text := a.startVMwareActionWith("connect", switchAway); code == 0 || !strings.Contains(text, "Wave 1") || !strings.Contains(text, "read vc01.example.com again") {
		t.Fatal("a read of another server was not refused with the reason:", code, text)
	}
	for _, action := range []string{"move", "prepare", "hvconnect"} {
		if code, text := a.startVMwareAction(action); code == 0 || !strings.Contains(text, "Migration plan \"Wave 1\" is copying 4 VMs") {
			t.Fatal(action+" ran during a plan copy:", code, text)
		}
	}
	if a.vmwareBlocksCollectionLocked() {
		t.Fatal("a plan copy blocks a collection")
	}
	if !a.vmwareBusyLocked() || !strings.Contains(a.vmwareBusyTextLocked(), "Wave 1") {
		t.Fatal("restore and the other steps no longer wait for a plan copy")
	}
	a.state.Running = true
	if code, text := a.startVMwareActionWith("hyperv", same); code != 0 {
		t.Fatal("a Discovery read was refused during a collection:", text)
	}
	if a.vmwareBlocksCollectionLocked() {
		t.Fatal("a Discovery read blocks a collection")
	}
	if why := a.planCopyRefusalLocked(); !strings.Contains(why, "Discovery read") {
		t.Fatal("a plan copy started during a Hyper-V read:", why)
	}
	a.finishVMwareAction("connected", "")
	if code, _ := a.startVMwareAction("export"); code == 0 {
		t.Fatal("a non-read step ran during a collection")
	}
	if a.criticalOperations != 0 {
		t.Fatal("leaked critical operations:", a.criticalOperations)
	}
}
