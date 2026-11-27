package main

// CD/DVD and floppy images on a VMware VM.
//
// vSphere's export of a snapshot (vmware_export.go) opens every file the
// snapshot's devices name, so a CD/DVD drive set to an ISO that no longer
// exists makes the whole export fail ("File [ds] ISO/x.iso was not found",
// EPICHyperspaceLocal, 2026-11-26), although only the disks are read. The
// file need not be connected: a missing ISO cannot be connected, so the
// drive shows as disconnected while its backing still names the file.
//
// With the operator's tick (vmReplicaOptions.EjectMedia), a copy round sets
// such drives to a client device, not connected, before it takes its
// snapshot, as "Client Device" in the vSphere Client does. The VM's settings
// are recorded first. Nothing else about the drive changes, and the drive
// stays on the VM.

import (
	"path"
	"strings"

	"github.com/vmware/govmomi/vim25/types"
)

// vmEjectMediaHint ends the error of a round whose export failed while the
// VM had image media and EjectMedia was not ticked. The page looks for it
// to offer the tick (index.html, vmEjectHint).
const vmEjectMediaHint = "Tick Eject CD/DVD and floppy images (New migration's Review, the migration plan's settings or Move VMs), then copy again."

// vmMediaImage is one CD/DVD or floppy drive whose backing names a file.
type vmMediaImage struct {
	Label     string
	File      string
	Connected bool
}

func vmMediaLabel(device *types.VirtualDevice) string {
	if device.DeviceInfo != nil {
		if info := device.DeviceInfo.GetDescription(); info != nil && info.Label != "" {
			return info.Label
		}
	}
	return "CD/DVD drive"
}

// vmImageMedia lists the drives that name an image file, connected or not.
func vmImageMedia(devices []types.BaseVirtualDevice) []vmMediaImage {
	images := []vmMediaImage{}
	for _, device := range devices {
		base := device.GetVirtualDevice()
		file := ""
		switch device.(type) {
		case *types.VirtualCdrom:
			if backing, ok := base.Backing.(*types.VirtualCdromIsoBackingInfo); ok {
				file = backing.FileName
			}
		case *types.VirtualFloppy:
			if backing, ok := base.Backing.(*types.VirtualFloppyImageBackingInfo); ok {
				file = backing.FileName
			}
		}
		if strings.TrimSpace(file) == "" {
			continue
		}
		connected := base.Connectable != nil && base.Connectable.Connected
		images = append(images, vmMediaImage{Label: vmMediaLabel(base), File: file, Connected: connected})
	}
	return images
}

func vmMediaNames(images []vmMediaImage) string {
	names := make([]string, 0, len(images))
	for _, image := range images {
		names = append(names, image.Label+" ("+image.File+")")
	}
	return strings.Join(names, ", ")
}

// vmMediaFileName is the image's file name without its datastore folder.
func vmMediaFileName(file string) string {
	return path.Base(vmDatastorePath.ReplaceAllString(file, "$2"))
}

// vmEjectMediaSpec sets every image-backed drive to a client device that is
// not connected and does not connect at power on.
func vmEjectMediaSpec(devices []types.BaseVirtualDevice) (types.VirtualMachineConfigSpec, int) {
	spec := types.VirtualMachineConfigSpec{}
	for _, device := range devices {
		base := device.GetVirtualDevice()
		var changed types.BaseVirtualDevice
		switch item := device.(type) {
		case *types.VirtualCdrom:
			if _, ok := base.Backing.(*types.VirtualCdromIsoBackingInfo); !ok {
				continue
			}
			copy := *item
			copy.Backing = &types.VirtualCdromRemotePassthroughBackingInfo{
				VirtualDeviceRemoteDeviceBackingInfo: types.VirtualDeviceRemoteDeviceBackingInfo{DeviceName: "", UseAutoDetect: types.NewBool(false)},
				Exclusive:                            false,
			}
			copy.Connectable = vmDisconnectedConnectable(base.Connectable)
			changed = &copy
		case *types.VirtualFloppy:
			if _, ok := base.Backing.(*types.VirtualFloppyImageBackingInfo); !ok {
				continue
			}
			copy := *item
			copy.Backing = &types.VirtualFloppyRemoteDeviceBackingInfo{
				VirtualDeviceRemoteDeviceBackingInfo: types.VirtualDeviceRemoteDeviceBackingInfo{DeviceName: "", UseAutoDetect: types.NewBool(false)},
			}
			copy.Connectable = vmDisconnectedConnectable(base.Connectable)
			changed = &copy
		default:
			continue
		}
		spec.DeviceChange = append(spec.DeviceChange, &types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationEdit, Device: changed})
	}
	return spec, len(spec.DeviceChange)
}

func vmDisconnectedConnectable(current *types.VirtualDeviceConnectInfo) *types.VirtualDeviceConnectInfo {
	next := types.VirtualDeviceConnectInfo{}
	if current != nil {
		next = *current
	}
	next.Connected, next.StartConnected = false, false
	return &next
}
