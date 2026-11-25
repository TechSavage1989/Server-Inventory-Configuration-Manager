package main

// Moving one VMware VM to Hyper-V from the VMware tab, run on the Hyper-V
// host: copy rounds while the VM keeps running, then a cutover that shuts
// the guest down through VMware Tools, copies the last changes and creates
// the Hyper-V VM on the copied disks. The VMware VM is left powered off and
// otherwise unchanged, so moving back is starting it again.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

type vmMoveRequest struct {
	VMID       string
	Name       string
	Datacenter string
	CopyFolder string
	Quiesce    bool
	Cutover    bool
	// RecordFolder receives JSON records before vSphere changes.
	RecordFolder string
}

type vmMoveResult struct {
	VM       string           `json:"vm"`
	Cutover  bool             `json:"cutover"`
	Round    *vmReplicaResult `json:"round"`
	Disks    []string         `json:"disks"`
	ShutDown bool             `json:"shutDown"`
	HyperV   *vmHyperVPlan    `json:"hyperV,omitempty"`
	HyperVID string           `json:"hyperVId,omitempty"`
	Finished time.Time        `json:"finished"`
}

// vmMoveRound runs one copy round, or the cutover round (shut the guest
// down first, then copy what changed), for one VM.
func vmMoveRound(parent context.Context, options vmConnectOptions, request vmMoveRequest, log func(string)) (*vmMoveResult, error) {
	if log == nil {
		log = func(string) {}
	}
	result := &vmMoveResult{VM: request.Name, Cutover: request.Cutover}
	if request.Cutover {
		prepared, err := prepareVSphereVM(parent, options, request.VMID, request.Name, vmPrepareSteps{Shutdown: true}, request.RecordFolder, log)
		for _, line := range prepared.Done {
			log(line)
		}
		if err != nil {
			return result, fmt.Errorf("the guest could not be shut down, so nothing was cut over: %w", err)
		}
		if prepared.PowerState != string(types.VirtualMachinePowerStatePoweredOff) {
			return result, errors.New("the VM is still running; the cutover stopped before the last copy")
		}
		result.ShutDown = true
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	vsphere, err := openVSphereSession(ctx, options, log)
	if err != nil {
		return result, err
	}
	defer vsphere.logout()
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: request.VMID}
	var machine mo.VirtualMachine
	if err := property.DefaultCollector(vsphere.client).RetrieveOne(ctx, ref, []string{"name", "config.guestId", "config.guestFullName"}, &machine); err != nil {
		return result, fmt.Errorf("the VM could not be read: %v", vmFriendlyConnectError(err, vsphere.host))
	}
	if machine.Name != request.Name {
		return result, fmt.Errorf("the VM with this ID is now called %q, not %q. Read the inventory again", machine.Name, request.Name)
	}
	guestID, guestName := "", ""
	if machine.Config != nil {
		guestID, guestName = machine.Config.GuestId, machine.Config.GuestFullName
	}
	if role := vmInfrastructureRole(vmMachine{Name: machine.Name, GuestID: guestID, GuestOS: guestName}); role != "" {
		return result, errors.New("this is " + role + ", which is not migrated")
	}
	round, err := vmReplicateRound(ctx, vsphere.client, ref, vmReplicaOptions{
		Datacenter:   request.Datacenter,
		Folder:       request.CopyFolder,
		EnableCBT:    true,
		Quiesce:      request.Quiesce && !request.Cutover,
		Verify:       request.Cutover,
		RecordFolder: request.RecordFolder,
		Log:          log,
		Progress: func(disk string, p vmCopyProgress) {
			if p.Chunks > 0 {
				log(fmt.Sprintf("%s: %d%% (%s read, %s written).", disk, p.Done*100/p.Chunks, vmFormatBytes(p.BytesRead), vmFormatBytes(p.BytesWritten)))
			}
		},
	})
	result.Round = round
	if err != nil {
		return result, err
	}
	for _, disk := range round.Disks {
		result.Disks = append(result.Disks, disk.Target)
		log(fmt.Sprintf("%s: %s copy finished, %s read, %s written.", disk.Label, disk.Mode, vmFormatBytes(disk.BytesRead), vmFormatBytes(disk.BytesWritten)))
	}
	result.Finished = time.Now().UTC()
	return result, nil
}
