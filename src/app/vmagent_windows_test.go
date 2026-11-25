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

// The agent copies a live NTFS disk through a shadow copy: data written
// after the shadow copy is taken is not in the copy, the copied disk mounts
// with its files intact, and the shadow copy is deleted afterwards.
func TestAgentCopiesALiveDiskThroughAShadowCopy(t *testing.T) {
	folder := t.TempDir()
	image := filepath.Join(folder, "source.vhdx")
	disk, err := createVHDX(image, 512<<20)
	if err != nil {
		t.Fatal(err)
	}
	disk.Close()
	quoted := strings.ReplaceAll(image, "'", "''")
	setup := vmPowerShell(t, "$d = Mount-DiskImage -ImagePath '"+quoted+"' -PassThru | Get-Disk; "+
		"Initialize-Disk -Number $d.Number -PartitionStyle GPT; "+
		"$p = New-Partition -DiskNumber $d.Number -UseMaximumSize -AssignDriveLetter; "+
		"Format-Volume -Partition $p -FileSystem NTFS -NewFileSystemLabel AgentTest -Confirm:$false | Out-Null; "+
		"[Console]::Out.Write([string]$d.Number + '|' + $p.DriveLetter)")
	mounted := true
	defer func() {
		if mounted {
			vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
		}
	}()
	parts := strings.Split(setup, "|")
	if len(parts) != 2 {
		t.Fatalf("unexpected setup output %q", setup)
	}
	diskNumber, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	letter := parts[1] + `:\`
	if err := os.WriteFile(filepath.Join(letter, "payroll.txt"), []byte("before the shadow copy"), 0600); err != nil {
		t.Fatal(err)
	}
	countShadows := func() string { return vmPowerShell(t, "[Console]::Out.Write(@(Get-WmiObject Win32_ShadowCopy).Count)") }
	before := countShadows()

	source, volumes, err := openVMAgentDisk(diskNumber, false, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	shadowed := 0
	for _, volume := range volumes {
		if volume.Shadowed {
			shadowed++
		}
	}
	if shadowed != 1 {
		t.Fatalf("expected one shadowed NTFS volume, got %+v", volumes)
	}
	// Written after the shadow copy: must not reach the copy.
	if err := os.WriteFile(filepath.Join(letter, "payroll.txt"), []byte("after the shadow copy"), 0600); err != nil {
		t.Fatal(err)
	}
	vmPowerShell(t, "Write-VolumeCache -DriveLetter "+parts[1])
	target := filepath.Join(folder, "copy.vhdx")
	if _, err := vmCopyDisk(context.Background(), source, target, vmCopyOptions{Verify: true}); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if after := countShadows(); after != before {
		t.Fatalf("shadow copies before %s, after %s: the agent's copy was not deleted", before, after)
	}
	vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
	mounted = false

	copyQuoted := strings.ReplaceAll(target, "'", "''")
	copyLetter := vmPowerShell(t, "$d = Mount-DiskImage -ImagePath '"+copyQuoted+"' -PassThru | Get-Disk; "+
		"$p = Get-Partition -DiskNumber $d.Number | Where-Object Type -eq 'Basic'; "+
		"if (-not $p.DriveLetter) { $p | Add-PartitionAccessPath -AssignDriveLetter; $p = Get-Partition -DiskNumber $d.Number | Where-Object Type -eq 'Basic' }; "+
		"[Console]::Out.Write($p.DriveLetter)")
	defer vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+copyQuoted+"' | Out-Null")
	raw, err := os.ReadFile(copyLetter + `:\payroll.txt`)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "before the shadow copy" {
		t.Fatalf("the copy holds %q; it was not read from the shadow copy", raw)
	}
}
