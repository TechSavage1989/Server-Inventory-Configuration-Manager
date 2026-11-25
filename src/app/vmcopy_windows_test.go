//go:build windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func vmPowerShell(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; "+script).CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// Windows itself must accept the VHDX the copy engine writes: mount it
// read-only and compare the disk Windows presents with the source bytes.
func TestWindowsMountsCopiedVHDXWithIdenticalContents(t *testing.T) {
	folder := t.TempDir()
	data := vmTestDiskBytes(200 << 20)
	source := &vmBytesSource{data: data, name: "synthetic-source"}
	target := filepath.Join(folder, "copy.vhdx")
	if _, err := vmCopyDisk(context.Background(), source, target, vmCopyOptions{Verify: true}); err != nil {
		t.Fatal(err)
	}
	quoted := strings.ReplaceAll(target, "'", "''")
	number := vmPowerShell(t, "$image = Mount-DiskImage -ImagePath '"+quoted+"' -Access ReadOnly -StorageType VHDX -NoDriveLetter -PassThru; ($image | Get-Disk).Number")
	defer vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
	if _, err := strconv.Atoi(number); err != nil {
		t.Fatalf("Windows did not report a disk number: %q", number)
	}
	device, err := os.Open(`\\.\PhysicalDrive` + number)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	got := make([]byte, len(data))
	for offset := 0; offset < len(got); offset += 1 << 20 {
		if _, err := device.ReadAt(got[offset:offset+1<<20], int64(offset)); err != nil {
			t.Fatalf("reading the mounted disk at %d: %v", offset, err)
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatal("the disk Windows mounted differs from the copied source")
	}
	size := vmPowerShell(t, "(Get-Disk -Number "+number+").Size")
	if size != strconv.Itoa(len(data)) {
		t.Fatalf("Windows reports %s bytes, want %d", size, len(data))
	}
}

// And the reader must open a VHDX that Windows created.
func TestWindowsCreatedVHDXOpensReadOnly(t *testing.T) {
	folder := t.TempDir()
	target := filepath.Join(folder, "windows.vhdx")
	script := filepath.Join(folder, "create.txt")
	if err := os.WriteFile(script, []byte("create vdisk file=\""+target+"\" maximum=64 type=expandable\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("diskpart.exe", "/s", script).CombinedOutput(); err != nil {
		t.Fatalf("diskpart failed: %v\n%s", err, out)
	}
	disk, err := openVHDX(target, true)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	if disk.Size() != 64<<20 {
		t.Fatalf("size %d", disk.Size())
	}
	buffer := make([]byte, 1<<20)
	if _, err := disk.ReadAt(buffer, 0); err != nil || !vmIsZero(buffer) {
		t.Fatalf("a new Windows disk did not read as zeros: %v", err)
	}
}
