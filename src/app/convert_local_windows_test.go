//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// P2V in the Disk converter: a disk of this computer converts like an image
// file, its NTFS volume read from a shadow copy, and an output on that same
// disk is refused.
func TestConverterCopiesADiskOfThisComputer(t *testing.T) {
	if !isAdministrator() {
		t.Skip("needs administrator rights")
	}
	folder := t.TempDir()
	image := filepath.Join(folder, "computer.vhdx")
	disk, err := createVHDX(image, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	disk.Close()
	quoted := strings.ReplaceAll(image, "'", "''")
	setup := vmPowerShell(t, "$d = Mount-DiskImage -ImagePath '"+quoted+"' -PassThru | Get-Disk; "+
		"Initialize-Disk -Number $d.Number -PartitionStyle GPT; "+
		"$p = New-Partition -DiskNumber $d.Number -UseMaximumSize -AssignDriveLetter; "+
		"Format-Volume -Partition $p -FileSystem NTFS -NewFileSystemLabel P2VTest -Confirm:$false | Out-Null; "+
		"[Console]::Out.Write([string]$d.Number + '|' + $p.DriveLetter)")
	mounted := true
	defer func() {
		if mounted {
			vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
		}
	}()
	parts := strings.Split(setup, "|")
	number, err := strconv.Atoi(parts[0])
	if err != nil || len(parts) != 2 {
		t.Fatalf("unexpected setup output %q", setup)
	}
	letter := parts[1] + `:\`
	if err := os.WriteFile(filepath.Join(letter, "ledger.txt"), []byte("physical to virtual"), 0600); err != nil {
		t.Fatal(err)
	}
	vmPowerShell(t, "Write-VolumeCache -DriveLetter "+parts[1])
	path := `\\.\PhysicalDrive` + parts[0]

	info, boot, err := inspectLocalDisk(number)
	if err != nil {
		t.Fatal(err)
	}
	if info.VirtualSize != 256<<20 || boot.Firmware != "uefi" || len(info.Notes) < 2 || !strings.Contains(info.Notes[0], "shadow copy") {
		t.Fatalf("info %+v boot %+v", info, boot)
	}
	if err := vmCheckLocalTarget(path, filepath.Join(letter, "self.vhdx")); err == nil {
		t.Fatal("an output on the disk being copied was accepted")
	}
	target := filepath.Join(folder, "p2v.vhdx")
	if err := vmCheckLocalTarget(path, target); err != nil {
		t.Fatal(err)
	}
	source, err := openConvertSource(path, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	result, err := vmConvertImage(context.Background(), source, target, vmDiskFormatVHDX, vmConvertOptions{Verify: true})
	source.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatal("the copy was not verified")
	}
	// The copy has the source's disk GUID, so Windows would keep it offline
	// while the source is attached.
	vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
	mounted = false
	copyQuoted := strings.ReplaceAll(target, "'", "''")
	copyLetter := vmPowerShell(t, "$d = Mount-DiskImage -ImagePath '"+copyQuoted+"' -PassThru | Get-Disk; "+
		"$p = Get-Partition -DiskNumber $d.Number | Where-Object Type -eq 'Basic'; "+
		"if (-not $p.DriveLetter) { $p | Add-PartitionAccessPath -AssignDriveLetter; $p = Get-Partition -DiskNumber $d.Number | Where-Object Type -eq 'Basic' }; "+
		"[Console]::Out.Write($p.DriveLetter)")
	defer vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+copyQuoted+"' | Out-Null")
	raw, err := os.ReadFile(copyLetter + `:\ledger.txt`)
	if err != nil || string(raw) != "physical to virtual" {
		t.Fatalf("the converted disk holds %q (%v)", raw, err)
	}
}
