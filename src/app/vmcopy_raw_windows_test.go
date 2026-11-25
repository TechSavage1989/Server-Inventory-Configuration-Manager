//go:build windows

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The receiver writes onto a real Windows disk: attach a blank VHDX as a
// disk, copy onto \\.\PhysicalDriveN through the raw sink, detach it and
// compare the VHDX contents with the source.
func TestRawCopyOntoAWindowsDiskMatchesTheSource(t *testing.T) {
	folder := t.TempDir()
	records := filepath.Join(folder, "records")
	image := filepath.Join(folder, "target.vhdx")
	size := int64(72 << 20)
	blank, err := createVHDX(image, size)
	if err != nil {
		t.Fatal(err)
	}
	blank.Close()
	quoted := strings.ReplaceAll(image, "'", "''")
	number := vmPowerShell(t, "$image = Mount-DiskImage -ImagePath '"+quoted+"' -StorageType VHDX -NoDriveLetter -PassThru; ($image | Get-Disk).Number")
	mounted := true
	defer func() {
		if mounted {
			vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
		}
	}()
	disk, err := strconv.Atoi(number)
	if err != nil {
		t.Fatalf("Windows did not report a disk number: %q", number)
	}

	// The listing marks the system disk and finds the new blank one.
	var found, system bool
	for _, info := range vmListPhysicalDisks(records) {
		if info.Number == disk {
			found = info.Blank && info.Size == size && info.Protected == ""
		}
		if strings.Contains(info.Protected, "Windows") {
			system = true
			if _, err := openVMPhysicalDisk(info.Number, records); err == nil {
				t.Fatal("the system disk was opened for writing")
			}
		}
	}
	if !found || !system {
		t.Fatalf("listing: target found %v, system disk protected %v", found, system)
	}

	data := vmTestDiskBytes(size)
	copy(data[510:], []byte{0x55, 0xAA})
	source := &vmBytesSource{data: data, name: "synthetic-source"}
	open := func() vmBlockDevice {
		device, err := openVMPhysicalDisk(disk, records)
		if err != nil {
			t.Fatal(err)
		}
		return device
	}
	device := open()
	if _, err := vmCopyTo(context.Background(), source, newVMRawSink(device, records, false), vmCopyOptions{Verify: true}); err != nil {
		t.Fatal(err)
	}
	device.Close()
	if state := vmPowerShell(t, "(Get-Disk -Number "+number+").IsOffline"); state != "True" {
		t.Fatalf("the target disk was left online: %s", state)
	}

	// A delta pass after a change writes only that chunk.
	copy(data[40<<20:], []byte("changed after the first pass"))
	device = open()
	result, err := vmCopyTo(context.Background(), source, newVMRawSink(device, records, false), vmCopyOptions{Verify: true})
	device.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Resumed || result.Progress.Changed != 1 {
		t.Fatalf("delta pass: resumed %v, changed %d", result.Resumed, result.Progress.Changed)
	}

	// Without its record, the disk now holds data and is refused.
	if err := os.RemoveAll(records); err != nil {
		t.Fatal(err)
	}
	device = open()
	_, err = vmCopyTo(context.Background(), source, newVMRawSink(device, records, false), vmCopyOptions{})
	device.Close()
	if err == nil || !strings.Contains(err.Error(), "already holds data") {
		t.Fatalf("a disk holding data was written without overwrite: %v", err)
	}

	vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
	mounted = false
	if got := vmReadVHDX(t, image); !bytes.Equal(got, data) {
		t.Fatal("the disk Windows wrote differs from the source")
	}
}

func TestReceiverCommandParsesDiskNumbers(t *testing.T) {
	numbers, err := vmParseDiskNumbers(" 2,1,2 ,")
	if err != nil || len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
		t.Fatalf("%v %v", numbers, err)
	}
	for _, bad := range []string{"C", "-1", "64", "1;2"} {
		if _, err := vmParseDiskNumbers(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}
