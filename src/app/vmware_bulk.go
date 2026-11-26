package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Bulk Prepare on VMware (2026-11-26, user-requested: apply the prepare steps
// to many VMs at once with one confirmation). The batch is checked as a whole
// before anything changes, confirmed by typing the number of VMs ("5 VMs"),
// and then runs prepareVSphereVM for one VM after another, so every rule of a
// single prepare (record first, typed-name equivalent, guest shutdown only,
// system VMs refused) still applies to each. One VM failing never stops the
// rest; Stop takes effect between VMs only, never inside one.

const vmPrepareBatchMax = 200

type vmPrepareBatchRequest struct {
	VMID  string         `json:"vmId"`
	Steps vmPrepareSteps `json:"steps"`
}

type vmPrepareBatchItem struct {
	ID    string         `json:"id"`
	VM    string         `json:"vm"`
	Steps vmPrepareSteps `json:"steps"`
	State string         `json:"state"` // waiting, running, done, failed, stopped
	// Step is what the running VM is doing now and Percent vSphere's
	// progress of a decrypt (-1 when there is none), for live progress.
	Step      string     `json:"step,omitempty"`
	Percent   int        `json:"percent"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Done      []string   `json:"done"`
	Skipped   []string   `json:"skipped"`
	Error     string     `json:"error,omitempty"`
}

// vmPrepareBatchConfirmText is what the operator types to confirm a batch.
func vmPrepareBatchConfirmText(count int) string {
	if count == 1 {
		return "1 VM"
	}
	return fmt.Sprintf("%d VMs", count)
}

// vmPlanPrepareBatch checks a requested batch against the inventory and
// returns the items to run, in the order given.
func vmPlanPrepareBatch(inventory *vmInventory, requests []vmPrepareBatchRequest, confirm string) ([]vmPrepareBatchItem, error) {
	if inventory == nil {
		return nil, errors.New("Read the inventory from vCenter or ESXi in this session first.")
	}
	if len(requests) == 0 {
		return nil, errors.New("Select at least one VM.")
	}
	if len(requests) > vmPrepareBatchMax {
		return nil, fmt.Errorf("Prepare at most %d VMs at a time.", vmPrepareBatchMax)
	}
	byID := map[string]vmMachine{}
	for _, vm := range inventory.VMs {
		byID[vm.ID] = vm
	}
	seen := map[string]bool{}
	items := []vmPrepareBatchItem{}
	for _, request := range requests {
		vm, ok := byID[request.VMID]
		if !ok {
			return nil, errors.New("A selected VM is not in the inventory. Read the inventory again.")
		}
		if seen[vm.ID] {
			return nil, fmt.Errorf("%s is selected twice.", vm.Name)
		}
		seen[vm.ID] = true
		if !request.Steps.any() {
			return nil, fmt.Errorf("No step is chosen for %s. Leave it out of the batch instead.", vm.Name)
		}
		if vm.Template {
			return nil, fmt.Errorf("%s is a template. Templates are not prepared.", vm.Name)
		}
		if vm.Source == "hyperv" {
			return nil, fmt.Errorf("%s is a Hyper-V VM. Prepare on VMware is for VMware VMs only.", vm.Name)
		}
		if role := vmInfrastructureRole(vm); role != "" {
			return nil, fmt.Errorf("%s is %s, which is not migrated. It is never changed.", vm.Name, role)
		}
		items = append(items, vmPrepareBatchItem{ID: vm.ID, VM: vm.Name, Steps: request.Steps, State: "waiting", Percent: -1, Done: []string{}, Skipped: []string{}})
	}
	expected := vmPrepareBatchConfirmText(len(items))
	if !strings.EqualFold(strings.Join(strings.Fields(confirm), " "), expected) {
		return nil, fmt.Errorf("Type %q to confirm the batch.", expected)
	}
	return items, nil
}

// vmPrepareBatchRun runs the batch one VM at a time. prepare changes one VM;
// update is called with a copy of the list after every change. A cancelled
// ctx stops the VMs not yet started.
func vmPrepareBatchRun(ctx context.Context, items []vmPrepareBatchItem, prepare func(item vmPrepareBatchItem) (vmPrepareResult, error), update func([]vmPrepareBatchItem)) (done, failed, stopped int) {
	snapshot := func() {
		list := make([]vmPrepareBatchItem, len(items))
		copy(list, items)
		update(list)
	}
	for index := range items {
		if ctx.Err() != nil {
			items[index].State = "stopped"
			stopped++
			continue
		}
		started := time.Now()
		items[index].State, items[index].StartedAt = "running", &started
		snapshot()
		result, err := func() (result vmPrepareResult, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("Preparing stopped unexpectedly: %v", recovered)
				}
			}()
			return prepare(items[index])
		}()
		ended := time.Now()
		items[index].EndedAt, items[index].Step, items[index].Percent = &ended, "", -1
		items[index].Done = append([]string{}, result.Done...)
		items[index].Skipped = append([]string{}, result.Skipped...)
		if err != nil {
			items[index].State, items[index].Error = "failed", err.Error()
			failed++
		} else {
			items[index].State = "done"
			done++
		}
		snapshot()
	}
	snapshot()
	return done, failed, stopped
}

// vmApplyPrepareResult puts what a prepare changed into the inventory and
// checks readiness again, so a decrypted VM is no longer blocked until the
// next read. It reports whether the inventory was there to change.
func vmApplyPrepareResult(inventory *vmInventory, id string, result vmPrepareResult, now time.Time) bool {
	if inventory == nil {
		return false
	}
	for index := range inventory.VMs {
		vm := &inventory.VMs[index]
		if vm.ID != id || result.PowerState == "" {
			continue
		}
		vm.PowerState, vm.VTPM, vm.Encrypted = result.PowerState, result.VTPM, result.Encrypted
		if !result.Encrypted {
			// Decrypted (or never encrypted): no disk is encrypted any more.
			for disk := range vm.Disks {
				vm.Disks[disk].Encrypted = false
			}
			vm.EncryptionRead = true
		}
		if result.Snapshots == 0 {
			vm.Snapshots, vm.ConsolidationNeeded = nil, false
		}
		if result.PowerState == "poweredOff" {
			vm.ToolsRunning = false
		}
		vm.Readiness = nil
		for _, target := range vmTargets {
			vm.Readiness = append(vm.Readiness, assessVM(*vm, target, now))
		}
	}
	return true
}
