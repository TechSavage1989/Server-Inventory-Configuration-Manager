//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func vmDiskPart(t *testing.T, folder, script string, allowFail bool) string {
	t.Helper()
	path := filepath.Join(folder, "diskpart-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".txt")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("diskpart.exe", "/s", path).CombinedOutput()
	if err != nil && !allowFail {
		t.Fatalf("diskpart failed: %v\n%s\n%s", err, script, out)
	}
	return string(out)
}

// A striped dynamic volume across two disks is read from one shadow copy:
// the detected layout matches Windows, a write made after the shadow copy
// stays out of both copies, and the two copied disks come back online as a
// working striped volume holding the file as it was at the shadow copy.
func TestAgentCopiesAStripedDynamicVolumeFromOneShadowCopy(t *testing.T) {
	if !isAdministrator() {
		t.Skip("dynamic disks need administrator rights")
	}
	folder := t.TempDir()
	images := []string{filepath.Join(folder, "stripe-a.vhdx"), filepath.Join(folder, "stripe-b.vhdx")}
	numbers := []int{}
	for _, image := range images {
		disk, err := createVHDX(image, 256<<20)
		if err != nil {
			t.Fatal(err)
		}
		disk.Close()
		quoted := strings.ReplaceAll(image, "'", "''")
		number := vmPowerShell(t, "$d = Mount-DiskImage -ImagePath '"+quoted+"' -PassThru | Get-Disk; Initialize-Disk -Number $d.Number -PartitionStyle GPT; [Console]::Out.Write($d.Number)")
		n, err := strconv.Atoi(number)
		if err != nil {
			t.Fatalf("disk number %q", number)
		}
		numbers = append(numbers, n)
		defer vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' -ErrorAction SilentlyContinue | Out-Null")
	}
	a, b := strconv.Itoa(numbers[0]), strconv.Itoa(numbers[1])
	if out := vmDiskPart(t, folder, "select disk "+a+"\nconvert dynamic\n", true); !strings.Contains(out, "successfully converted") {
		t.Skip("this Windows does not convert virtual disks to dynamic disks (Windows Server 2025 and later): ", out)
	}
	vmDiskPart(t, folder, "select disk "+b+"\nconvert dynamic\ncreate volume stripe size=160 disk="+a+","+b+"\nformat fs=ntfs quick label=StripeTest\nassign\n", false)
	volumePath := vmPowerShell(t, "[Console]::Out.Write((Get-Volume -FileSystemLabel StripeTest).Path)")
	if !strings.HasPrefix(volumePath, `\\?\Volume{`) {
		t.Fatalf("volume path %q", volumePath)
	}
	if err := os.WriteFile(volumePath+"ledger.txt", []byte("before the shadow copy"), 0600); err != nil {
		t.Fatal(err)
	}
	countShadows := func() string { return vmPowerShell(t, "[Console]::Out.Write(@(Get-WmiObject Win32_ShadowCopy).Count)") }
	before := countShadows()
	set, err := openVMAgentDisks(numbers, false, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range numbers {
		found := false
		for _, volume := range set.Volumes[number] {
			if volume.Kind == "striped" && volume.Shadowed {
				found = true
			}
		}
		if !found {
			set.Close()
			t.Fatalf("disk %d: no shadowed striped volume in %+v", number, set.Volumes[number])
		}
	}
	if err := os.WriteFile(volumePath+"ledger.txt", []byte("after the shadow copy, must not be copied"), 0600); err != nil {
		set.Close()
		t.Fatal(err)
	}
	copies := []string{filepath.Join(folder, "copy-a.vhdx"), filepath.Join(folder, "copy-b.vhdx")}
	for i, number := range numbers {
		if _, err := vmCopyDisk(context.Background(), set.Sources[number], copies[i], vmCopyOptions{Verify: true}); err != nil {
			set.Close()
			t.Fatal(err)
		}
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	if after := countShadows(); after != before {
		t.Fatalf("shadow copies before %s, after %s", before, after)
	}
	// Remove the originals and bring the copies online in their place.
	for _, image := range images {
		vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+strings.ReplaceAll(image, "'", "''")+"' | Out-Null")
	}
	copyNumbers := []string{}
	for _, image := range copies {
		quoted := strings.ReplaceAll(image, "'", "''")
		copyNumbers = append(copyNumbers, vmPowerShell(t, "[Console]::Out.Write((Mount-DiskImage -ImagePath '"+quoted+"' -PassThru | Get-Disk).Number)"))
		defer vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' -ErrorAction SilentlyContinue | Out-Null")
	}
	script := "rescan\n"
	for _, number := range copyNumbers {
		script += "select disk " + number + "\nonline disk noerr\nimport noerr\n"
	}
	var raw []byte
	deadline := time.Now().Add(60 * time.Second)
	for {
		vmDiskPart(t, folder, script, true)
		path := strings.TrimSpace(vmPowerShell(t, "[Console]::Out.Write((Get-Volume -FileSystemLabel StripeTest -ErrorAction SilentlyContinue | Select-Object -First 1).Path)"))
		if path != "" {
			raw, err = os.ReadFile(path + "ledger.txt")
			if err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copied striped volume did not come online (%v):\n%s", err, vmDiskPart(t, folder, "list disk\nlist volume\n", true))
		}
		time.Sleep(3 * time.Second)
	}
	if string(raw) != "before the shadow copy" {
		t.Fatalf("the copied striped volume holds %q", raw)
	}
}
