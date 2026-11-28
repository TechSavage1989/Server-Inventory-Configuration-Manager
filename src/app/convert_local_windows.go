//go:build windows

package main

// P2V in the Disk converter: a disk of this computer (\\.\PhysicalDriveN) is
// a source like any image file. Converting or sending it reads every NTFS and
// ReFS volume from a new Volume Shadow Copy (openVMAgentDisk), so the
// computer keeps running and the copy is consistent per volume; the shadow
// copies are deleted when the step ends. Repeating the conversion into the
// same file copies only what changed. The disk is only ever read.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var vmLocalDiskPattern = regexp.MustCompile(`(?i)^\\\\\.\\PhysicalDrive(\d{1,2})$`)

// vmLocalDiskNumber returns the disk number of a \\.\PhysicalDriveN path.
func vmLocalDiskNumber(path string) (int, bool) {
	match := vmLocalDiskPattern.FindStringSubmatch(strings.TrimSpace(path))
	if match == nil {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil
}

// vmLocalDiskImage is a disk of this computer read as a raw image.
type vmLocalDiskImage struct {
	vmCopySource
	info vmDiskInfo
	// used holds the disk's data: NTFS clusters in use and everything
	// outside its NTFS volumes (vmSkipFreeSpace); nil reads everything.
	used []vmCopyRange
}

func (d *vmLocalDiskImage) Info() vmDiskInfo         { return d.info }
func (d *vmLocalDiskImage) Allocated() []vmCopyRange { return d.used }

func vmLocalDiskInfo(number int, size int64) vmDiskInfo {
	host, _ := os.Hostname()
	info := vmDiskInfo{
		Path: fmt.Sprintf(`\\.\PhysicalDrive%d`, number), Format: vmDiskFormatRaw,
		FormatName: fmt.Sprintf("Disk %d of this computer", number), Variant: host,
		VirtualSize: size, FileSize: size, DataBytes: size,
	}
	volumes, err := vmDiskVolumes(number)
	if err == nil {
		for _, volume := range volumes {
			where := strings.Join(volume.Mounts, ", ")
			if where == "" {
				where = "a volume without a drive letter"
			}
			kind := volume.FileSystem
			if kind == "" {
				kind = "no file system"
			}
			shadow := "read from a new shadow copy"
			if kind != "NTFS" && kind != "ReFS" {
				shadow = "read live (no shadow copy for this file system)"
			}
			info.Notes = append(info.Notes, fmt.Sprintf("%s (%s, %s): %s.", where, kind, vmFormatBytes(volume.Length), shadow))
		}
	}
	info.Notes = append(info.Notes, "The computer keeps running. Only data in use is read: free space on NTFS volumes is skipped and stays empty in the new file. Converting again into the same file copies only what changed since.")
	return info
}

// openConvertSource opens an image file, or a disk of this computer with its
// volumes read from shadow copies.
func openConvertSource(path string, log func(string)) (vmDiskImage, error) {
	number, local := vmLocalDiskNumber(path)
	if !local {
		return openDiskImage(path)
	}
	if !isAdministrator() {
		return nil, fmt.Errorf("copying a disk of this computer needs the application to run as administrator")
	}
	set, err := openVMAgentDisks([]int{number}, false, log)
	if err != nil {
		return nil, err
	}
	// Free NTFS clusters read as zeros without touching the disk, and the
	// first pass reads only the data in use.
	used := vmSkipFreeSpace(context.Background(), set, []int{number}, log)[number]
	source := set.Sources[number].(*vmCompositeSource)
	source.closers = append(set.shared, source.closers...) // closed last
	return &vmLocalDiskImage{vmCopySource: source, info: vmLocalDiskInfo(number, source.Size()), used: used}, nil
}

// inspectLocalDisk reads a disk of this computer's size and partition table
// without taking shadow copies.
func inspectLocalDisk(number int) (vmDiskInfo, vmBootInfo, error) {
	device, err := vmOpenDevice(fmt.Sprintf(`\\.\PhysicalDrive%d`, number))
	if err != nil {
		if !isAdministrator() {
			return vmDiskInfo{}, vmBootInfo{}, fmt.Errorf("reading a disk of this computer needs the application to run as administrator")
		}
		return vmDiskInfo{}, vmBootInfo{}, err
	}
	defer device.Close()
	size, err := vmDeviceLength(device)
	if err != nil {
		return vmDiskInfo{}, vmBootInfo{}, err
	}
	return vmLocalDiskInfo(number, size), vmDetectBoot(device, size), nil
}

// vmTargetOnLocalDisk reports whether a file path lies on a volume of the
// disk being copied: the copy would then hold a partial copy of itself.
func vmTargetOnLocalDisk(target string, number int) bool {
	volumes, err := vmDiskVolumes(number)
	if err != nil {
		return false
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	lower := strings.ToLower(absolute)
	for _, volume := range volumes {
		for _, mount := range volume.Mounts {
			mount = strings.ToLower(strings.TrimRight(mount, `\`)) + `\`
			if strings.HasPrefix(lower, mount) {
				return true
			}
		}
	}
	return false
}

// vmCheckLocalTarget refuses an output on the disk being copied.
func vmCheckLocalTarget(source, target string) error {
	number, local := vmLocalDiskNumber(source)
	if local && vmTargetOnLocalDisk(target, number) {
		return fmt.Errorf("%s is on disk %d, the disk being copied; choose a folder on another disk or a network share", target, number)
	}
	return nil
}

// handleConvertLocalDisks lists this computer's disks for P2V.
func (a *application) handleConvertLocalDisks(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	disks, err := vmListAgentDisks()
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The disks could not be listed: " + err.Error() + "."})
		return
	}
	host, _ := os.Hostname()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "computer": host, "disks": disks})
}
