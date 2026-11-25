//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// vmMountDisk attaches an image with Windows and returns its disk number
// and a function that detaches it.
func vmMountDisk(t *testing.T, image string, writable bool) (string, func()) {
	t.Helper()
	quoted := strings.ReplaceAll(image, "'", "''")
	access := "ReadOnly"
	if writable {
		access = "ReadWrite"
	}
	number := vmPowerShell(t, "$image = Mount-DiskImage -ImagePath '"+quoted+"' -Access "+access+" -NoDriveLetter -PassThru; ($image | Get-Disk).Number")
	if _, err := strconv.Atoi(number); err != nil {
		t.Fatalf("Windows did not report a disk number: %q", number)
	}
	if writable {
		vmPowerShell(t, "Set-Disk -Number "+number+" -IsOffline $false; Set-Disk -Number "+number+" -IsReadOnly $false")
	}
	return number, func() { vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null") }
}

func vmReadPhysical(t *testing.T, number string, size int) []byte {
	t.Helper()
	device, err := os.Open(`\\.\PhysicalDrive` + number)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	got := make([]byte, size)
	for offset := 0; offset < size; offset += 1 << 20 {
		end := offset + 1<<20
		if end > size {
			end = size
		}
		if _, err := device.ReadAt(got[offset:end], int64(offset)); err != nil {
			t.Fatalf("reading the mounted disk at %d: %v", offset, err)
		}
	}
	return got
}

func vmWritePhysical(t *testing.T, number string, data []byte, offset int64) {
	t.Helper()
	device, err := os.OpenFile(`\\.\PhysicalDrive`+number, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if _, err := device.WriteAt(data, offset); err != nil {
		t.Fatalf("writing the mounted disk at %d: %v", offset, err)
	}
}

func vmDiskpart(t *testing.T, folder, command string) {
	t.Helper()
	script := filepath.Join(folder, "diskpart.txt")
	if err := os.WriteFile(script, []byte(command+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("diskpart.exe", "/s", script).CombinedOutput(); err != nil {
		t.Fatalf("diskpart failed: %v\n%s", err, out)
	}
}

// Windows must mount the VHD files the converter writes and see the same
// bytes (VHDX output is covered by the copy engine's mount test).
func TestWindowsMountsConvertedVHDs(t *testing.T) {
	folder := t.TempDir()
	data := make([]byte, 96<<20)
	rand.Read(data[:3<<20])
	rand.Read(data[40<<20 : 44<<20])
	rand.Read(data[len(data)-4096:])
	source := filepath.Join(folder, "source.img")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{vmDiskFormatVHD, vmDiskFormatVHDFixed} {
		target := filepath.Join(folder, format+".vhd")
		if _, err := vmConvertDisk(context.Background(), source, target, format, vmConvertOptions{Verify: true}); err != nil {
			t.Fatal(err)
		}
		number, detach := vmMountDisk(t, target, false)
		got := vmReadPhysical(t, number, len(data))
		size := vmPowerShell(t, "(Get-Disk -Number "+number+").Size")
		detach()
		if !bytes.Equal(got, data) {
			t.Fatalf("%s: the disk Windows mounted differs from the source", format)
		}
		if size != strconv.Itoa(len(data)) {
			t.Fatalf("%s: Windows reports %s bytes, want %d", format, size, len(data))
		}
	}
}

// Differencing disks made by Windows (the checkpoint layout Hyper-V uses)
// read as the parent with the child's writes on top, including writes that
// cover only part of a block, so the sector bitmap is exercised.
func TestWindowsDifferencingChainsReadAsMerged(t *testing.T) {
	for _, extension := range []string{".vhdx", ".vhd"} {
		t.Run(extension, func(t *testing.T) {
			folder := t.TempDir()
			base := make([]byte, 64<<20)
			rand.Read(base[:8<<20])
			rand.Read(base[30<<20 : 36<<20])
			raw := filepath.Join(folder, "base.img")
			if err := os.WriteFile(raw, base, 0600); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Join(folder, "base"+extension)
			format := vmDiskFormatVHDX
			if extension == ".vhd" {
				format = vmDiskFormatVHD
			}
			if _, err := vmConvertDisk(context.Background(), raw, parent, format, vmConvertOptions{}); err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(folder, "child"+extension)
			vmDiskpart(t, folder, `create vdisk file="`+child+`" parent="`+parent+`"`)
			want := append([]byte(nil), base...)
			number, detach := vmMountDisk(t, child, true)
			for _, write := range []struct{ offset, length int64 }{{4096, 4096}, {5 << 20, 1 << 20}, {33<<20 + 512, 3584}, {50 << 20, 2 << 20}} {
				patch := make([]byte, write.length)
				rand.Read(patch)
				vmWritePhysical(t, number, patch, write.offset)
				copy(want[write.offset:], patch)
			}
			detach()
			got, info := vmReadImage(t, child)
			if len(info.Chain) != 2 {
				t.Fatalf("chain %v", info.Chain)
			}
			vmAssertSameDisk(t, "child"+extension, got, want)
			// Converting the chain gives one disk with the merged contents.
			merged := filepath.Join(folder, "merged.qcow2")
			if _, err := vmConvertDisk(context.Background(), child, merged, vmDiskFormatQCOW2, vmConvertOptions{Verify: true}); err != nil {
				t.Fatal(err)
			}
			got, _ = vmReadImage(t, merged)
			vmAssertSameDisk(t, "merged", got, want)
		})
	}
}
