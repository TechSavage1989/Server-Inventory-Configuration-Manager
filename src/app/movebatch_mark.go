package main

// After a successful cutover the old VMware VM is renamed and given a note
// (on by default, owner 2026-11-28: "rename the original vm ... to something
// like Migrated do not power on"), so nobody starts it by mistake beside the
// new VM. vSphere has no setting that blocks a power-on, so the name and the
// note are the marker; the adapters are already set to not connect. The VM
// must be powered off and still carry its own name; a JSON record of the old
// name and note is saved before the change, and Roll back puts both back
// (vmUnmarkSource) before it touches anything else.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// vmMigratedPrefix starts the old VM's new name.
const vmMigratedPrefix = "MIGRATED - DO NOT POWER ON - "

// vmMarkAppName names this application in the note (displayName is
// Windows-only).
const vmMarkAppName = "Server Inventory and Migration Manager"

// vmNameLimit is the longest VM name vSphere accepts.
const vmNameLimit = 80

// vmMigratedName is the name the old VM gets: the prefix and its own name,
// cut to vSphere's limit.
func vmMigratedName(name string) string {
	if strings.HasPrefix(name, vmMigratedPrefix) {
		return name
	}
	out := vmMigratedPrefix + name
	for utf8.RuneCountInString(out) > vmNameLimit {
		_, size := utf8.DecodeLastRuneInString(out)
		out = out[:len(out)-size]
	}
	return out
}

// vmMigratedNote is the note put first in the old VM's annotation.
func vmMigratedNote(name, target string, at time.Time) string {
	return fmt.Sprintf("MIGRATED %s by %s: %s now runs as a new VM on %s. Do not power this VM on, or two copies of the server run at once. Roll back in %s renames this VM back and removes this note.",
		at.UTC().Format("2006-01-02 15:04 UTC"), vmMarkAppName, name, target, vmMarkAppName)
}

// vmMarkRecord is the JSON record saved before the old VM is marked.
type vmMarkRecord struct {
	VM            string    `json:"vm"`
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	Time          time.Time `json:"time"`
	Annotation    string    `json:"annotation"`
	NewName       string    `json:"newName"`
	NewAnnotation string    `json:"newAnnotation"`
}

// vmMarkPlan works out the new name and note, or says why the VM is left.
func vmMarkPlan(current, name, powerState, annotation, target string, now time.Time) (vmMarkRecord, error) {
	if current != name {
		return vmMarkRecord{}, fmt.Errorf("the VM with this ID is now called %q, not %q; it was not renamed", current, name)
	}
	if powerState != string(types.VirtualMachinePowerStatePoweredOff) {
		return vmMarkRecord{}, errors.New("the VM is not powered off; it was not renamed")
	}
	note := vmMigratedNote(name, target, now)
	if annotation != "" {
		note += "\n\n" + annotation
	}
	return vmMarkRecord{VM: name, Action: "rename and note after cutover", Time: now.UTC(), Annotation: annotation, NewName: vmMigratedName(name), NewAnnotation: note}, nil
}

// vmMarkSourceMigrated renames the powered-off old VM and puts the note first
// in its annotation. It returns the new name and the record it saved.
func vmMarkSourceMigrated(parent context.Context, options vmConnectOptions, vmID, name, target, recordFolder string, log func(string)) (string, string, error) {
	if log == nil {
		log = func(string) {}
	}
	if recordFolder == "" {
		return "", "", errors.New("no folder for the record; the old VM was not renamed")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, log)
	if err != nil {
		return "", "", err
	}
	defer vsphere.logout()
	machine, err := vmReadSourceVM(ctx, vsphere, vmID, name, []string{"config.annotation"})
	if err != nil {
		return "", "", err
	}
	annotation := ""
	if machine.Config != nil {
		annotation = machine.Config.Annotation
	}
	record, err := vmMarkPlan(machine.Name, name, string(machine.Runtime.PowerState), annotation, target, time.Now())
	if err != nil {
		return "", "", err
	}
	record.ID = vmID
	if err := os.MkdirAll(recordFolder, 0700); err != nil {
		return "", "", err
	}
	raw, _ := json.MarshalIndent(record, "", "  ")
	file := filepath.Join(recordFolder, vmUnsafeFileName.ReplaceAllString(name, "-")+"-"+time.Now().UTC().Format("20060102-150405")+"-name.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		return "", "", err
	}
	log(fmt.Sprintf("Renaming the old VM to %q and adding a note (record: %s).", record.NewName, file))
	task, err := object.NewVirtualMachine(vsphere.client, machine.Reference()).Reconfigure(ctx, types.VirtualMachineConfigSpec{Name: record.NewName, Annotation: record.NewAnnotation})
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		return "", "", fmt.Errorf("the old VM was not renamed: %w", err)
	}
	return record.NewName, file, nil
}

// vmReadMarkRecord reads the record vmMarkSourceMigrated saved.
func vmReadMarkRecord(file, vmID string) (vmMarkRecord, error) {
	var record vmMarkRecord
	raw, err := os.ReadFile(file)
	if err == nil {
		err = json.Unmarshal(raw, &record)
	}
	if err != nil {
		return record, fmt.Errorf("the record of the old name (%s) could not be read: %v", file, err)
	}
	if record.ID != vmID || record.VM == "" || record.NewName == "" {
		return record, errors.New("the record of the old name belongs to another VM")
	}
	return record, nil
}

// vmUnmarkSource renames the old VM back and takes the note out again. A VM
// already called by its own name is left as it is; one called anything else
// than the two names in the record is refused. The note is put back only
// while it is still exactly the one the cutover wrote.
func vmUnmarkSource(parent context.Context, options vmConnectOptions, vmID, recordFile string, log func(string)) (bool, error) {
	if log == nil {
		log = func(string) {}
	}
	if recordFile == "" {
		return false, nil
	}
	record, err := vmReadMarkRecord(recordFile, vmID)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, log)
	if err != nil {
		return false, err
	}
	defer vsphere.logout()
	machine, err := vmReadSourceVM(ctx, vsphere, vmID, record.NewName, []string{"config.annotation"})
	if err != nil {
		if current, again := vmReadSourceVM(ctx, vsphere, vmID, record.VM, nil); again == nil && current != nil {
			log("The old VM is already called " + record.VM + ".")
			return false, nil
		}
		return false, err
	}
	spec := types.VirtualMachineConfigSpec{Name: record.VM}
	if machine.Config != nil && machine.Config.Annotation == record.NewAnnotation {
		spec.Annotation = record.Annotation
		if spec.Annotation == "" {
			// An empty string leaves the note unchanged; a space clears it.
			spec.Annotation = " "
		}
	} else {
		log("The old VM's note was changed after the cutover, so it is left as it is.")
	}
	log(fmt.Sprintf("Renaming the old VM back to %q.", record.VM))
	task, err := object.NewVirtualMachine(vsphere.client, machine.Reference()).Reconfigure(ctx, spec)
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("the old VM was not renamed back: %w", err)
	}
	return true, nil
}
