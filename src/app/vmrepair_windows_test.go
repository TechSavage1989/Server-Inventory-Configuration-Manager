//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vmBuildFakeWindowsDisk makes a GPT VHDX with an EFI system partition and
// an NTFS partition that holds a minimal Windows layout: SYSTEM and SOFTWARE
// hives built from a scratch registry key, and driver files. Nothing on
// this computer's own Windows is read or changed except the scratch key,
// which is deleted.
func vmBuildFakeWindowsDisk(t *testing.T, folder string) string {
	t.Helper()
	image := filepath.Join(folder, "guest.vhdx")
	vmDiskpart(t, folder, "create vdisk file=\""+image+"\" maximum=1600 type=expandable")
	mount := filepath.Join(folder, "build-mount")
	if err := os.MkdirAll(mount, 0700); err != nil {
		t.Fatal(err)
	}
	quoted := strings.ReplaceAll(image, "'", "''")
	mountQuoted := strings.ReplaceAll(mount, "'", "''")
	scratch := `HKCU:\Software\SIMRepairTest` + strings.ReplaceAll(filepath.Base(folder), " ", "")
	vmPowerShell(t, `
$disk = Mount-DiskImage -ImagePath '`+quoted+`' -NoDriveLetter -PassThru | Get-Disk
Initialize-Disk -Number $disk.Number -PartitionStyle GPT
$esp = New-Partition -DiskNumber $disk.Number -Size 100MB -GptType '{c12a7328-f81f-11d2-ba4b-00a0c93ec93b}'
Format-Volume -Partition $esp -FileSystem FAT32 -Confirm:$false | Out-Null
$data = New-Partition -DiskNumber $disk.Number -UseMaximumSize
Format-Volume -Partition $data -FileSystem NTFS -NewFileSystemLabel Windows -Confirm:$false | Out-Null
Add-PartitionAccessPath -DiskNumber $disk.Number -PartitionNumber $data.PartitionNumber -AccessPath '`+mountQuoted+`\'
$root = '`+mountQuoted+`'
foreach ($dir in @('Windows\System32\config', 'Windows\System32\drivers', 'Windows\SysWOW64')) { New-Item -ItemType Directory -Path (Join-Path $root $dir) -Force | Out-Null }
foreach ($driver in @('storahci', 'intelide', 'vmbus', 'storvsc', 'pciide', 'atapi')) { Set-Content -LiteralPath (Join-Path $root ('Windows\System32\drivers\' + $driver + '.sys')) -Value 'x' }
$scratch = '`+scratch+`'
Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
$sys = $scratch + '\Sys'
New-Item -Path ($sys + '\Select') -Force | Out-Null
New-ItemProperty -LiteralPath ($sys + '\Select') -Name Current -PropertyType DWord -Value 1 | Out-Null
function Add-Service($name, $type, $start, $image) {
  $key = $sys + '\ControlSet001\Services\' + $name
  New-Item -Path $key -Force | Out-Null
  New-ItemProperty -LiteralPath $key -Name Type -PropertyType DWord -Value $type | Out-Null
  New-ItemProperty -LiteralPath $key -Name Start -PropertyType DWord -Value $start | Out-Null
  if ($image) { New-ItemProperty -LiteralPath $key -Name ImagePath -PropertyType ExpandString -Value $image | Out-Null }
}
Add-Service 'storahci' 1 3 'System32\drivers\storahci.sys'
New-Item -Path ($sys + '\ControlSet001\Services\storahci\StartOverride') -Force | Out-Null
New-ItemProperty -LiteralPath ($sys + '\ControlSet001\Services\storahci\StartOverride') -Name '0' -PropertyType DWord -Value 3 | Out-Null
Add-Service 'intelide' 1 3 'System32\drivers\intelide.sys'
Add-Service 'pciide' 1 3 'System32\drivers\pciide.sys'
Add-Service 'vmbus' 1 0 'System32\drivers\vmbus.sys'
Add-Service 'storvsc' 1 3 'System32\drivers\storvsc.sys'
Add-Service 'atapi' 1 3 'System32\drivers\atapi.sys'
Add-Service 'VMTools' 16 2 '"C:\Program Files\VMware\VMware Tools\vmtoolsd.exe"'
$soft = $scratch + '\Soft'
$cv = $soft + '\Microsoft\Windows NT\CurrentVersion'
New-Item -Path $cv -Force | Out-Null
New-ItemProperty -LiteralPath $cv -Name ProductName -Value 'Windows Server 2019 Datacenter' | Out-Null
New-ItemProperty -LiteralPath $cv -Name CurrentBuildNumber -Value '17763' | Out-Null
New-ItemProperty -LiteralPath $cv -Name InstallationType -Value 'Server' | Out-Null
New-ItemProperty -LiteralPath $cv -Name EditionID -Value 'ServerDatacenter' | Out-Null
New-ItemProperty -LiteralPath $cv -Name CurrentMajorVersionNumber -PropertyType DWord -Value 10 | Out-Null
New-ItemProperty -LiteralPath $cv -Name CurrentMinorVersionNumber -PropertyType DWord -Value 0 | Out-Null
$native = $scratch.Replace('HKCU:\', 'HKCU\')
& reg.exe add ($native + '\Soft\Microsoft\Windows\CurrentVersion\Uninstall\VMwareToolsTest') /v DisplayName /t REG_SZ /d 'VMware Tools' /f | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'reg add Uninstall failed' }
& reg.exe query ($native + '\Soft\Microsoft\Windows\CurrentVersion\Uninstall\VMwareToolsTest') /v DisplayName | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'the Uninstall test key was not created' }
& reg.exe save ($native + '\Sys') (Join-Path $root 'Windows\System32\config\SYSTEM') /y | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'reg save SYSTEM failed' }
& reg.exe save ($native + '\Soft') (Join-Path $root 'Windows\System32\config\SOFTWARE') /y | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'reg save SOFTWARE failed' }
Remove-Item -LiteralPath $scratch -Recurse -Force
Remove-PartitionAccessPath -DiskNumber $disk.Number -PartitionNumber $data.PartitionNumber -AccessPath '`+mountQuoted+`\'
Dismount-DiskImage -ImagePath '`+quoted+`' | Out-Null
`)
	return image
}

func vmRunRepair(t *testing.T, folder string, arguments ...string) (*vmRepairReport, string) {
	t.Helper()
	script, err := filepath.Abs(vmRepairScript)
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(folder, "result.json")
	os.Remove(result)
	base := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-ResultPath", result, "-WorkPath", filepath.Join(folder, "work")}
	out, _ := exec.Command(powershellPath(), append(base, arguments...)...).CombinedOutput()
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("the fix-up wrote no result: %v\n%s", err, out)
	}
	report, err := parseRepairReport(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return report, string(out)
}

func vmFindRepairDriver(t *testing.T, report *vmRepairReport, name string) vmRepairDriver {
	t.Helper()
	if len(report.Windows) != 1 {
		t.Fatalf("expected one Windows installation, got %+v", report)
	}
	for _, driver := range report.Windows[0].Drivers {
		if strings.EqualFold(driver.Name, name) {
			return driver
		}
	}
	t.Fatalf("driver %s was not reported: %+v", name, report.Windows[0].Drivers)
	return vmRepairDriver{}
}

func vmImageAttached(t *testing.T, image string) bool {
	return vmPowerShell(t, "(Get-DiskImage -ImagePath '"+strings.ReplaceAll(image, "'", "''")+"').Attached") == "True"
}

// The fix-up script must read a Windows disk, change only the recorded
// values in its SYSTEM hive, detach it every time, and undo exactly what
// it changed.
func TestRepairScriptFixesUpAndUndoesAWindowsDisk(t *testing.T) {
	if !isAdministrator() {
		t.Skip("attaching a VHDX needs administrator rights")
	}
	folder := t.TempDir()
	image := vmBuildFakeWindowsDisk(t, folder)
	records := filepath.Join(folder, "records")

	check, out := vmRunRepair(t, folder, "-Mode", "Check", "-ImagePath", image, "-Target", "kvm")
	if !check.OK {
		t.Fatalf("check failed: %s\n%s", check.Error, out)
	}
	if check.Boot == nil || check.Boot.Firmware != "uefi" || check.Boot.PartitionStyle != "GPT" || !check.Boot.SystemPartition {
		t.Fatalf("boot firmware not read: %+v", check.Boot)
	}
	windows := check.Windows[0]
	if windows.ProductName != "Windows Server 2019 Datacenter" || windows.Build != 17763 || !windows.Server || windows.OsTag != "2k19" || windows.Architecture != "amd64" || !windows.VMwareTools {
		t.Fatalf("Windows not read: %+v\n%s", windows, out)
	}
	if driver := vmFindRepairDriver(t, check, "storahci"); driver.Start != 3 || !driver.Override || !driver.File {
		t.Fatalf("storahci state not read: %+v", driver)
	}
	if driver := vmFindRepairDriver(t, check, "viostor"); driver.Present {
		t.Fatalf("viostor should be missing: %+v", driver)
	}
	warned := false
	for _, finding := range check.Findings {
		if finding.Severity == "warning" && strings.Contains(finding.Message, "VirtIO") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("missing VirtIO driver not reported: %+v", check.Findings)
	}
	if vmImageAttached(t, image) {
		t.Fatal("Check left the disk attached")
	}

	// A drivers folder that is not a Windows image's makes DISM fail; the
	// disk must still be detached and nothing recorded as complete.
	drivers := filepath.Join(folder, "virtio", "viostor", "2k19", "amd64")
	if err := os.MkdirAll(drivers, 0700); err != nil {
		t.Fatal(err)
	}
	failed, out := vmRunRepair(t, folder, "-Mode", "Apply", "-ImagePath", image, "-Target", "kvm", "-RecordPath", records, "-DriverPath", filepath.Join(folder, "virtio"))
	if failed.OK || !strings.Contains(failed.Error, "DISM") {
		t.Fatalf("expected DISM to fail on a synthetic image: %+v\n%s", failed, out)
	}
	if vmImageAttached(t, image) {
		t.Fatal("a failed Apply left the disk attached")
	}

	applied, out := vmRunRepair(t, folder, "-Mode", "Apply", "-ImagePath", image, "-Target", "kvm", "-RecordPath", records, "-DisableVMwareTools")
	if !applied.OK || applied.Applied == nil {
		t.Fatalf("apply failed: %+v\n%s", applied, out)
	}
	if len(applied.Applied.Changes) != 5 || len(applied.Applied.Overrides) != 1 {
		t.Fatalf("expected storahci, intelide, pciide, atapi and VMTools changed and one override removed: %+v\n%s", applied.Applied, out)
	}
	if vmLatestRepairRecord(records, image) != applied.Applied.Record {
		matches, _ := filepath.Glob(filepath.Join(records, "Repair-*.json"))
		var found strings.Builder
		for _, match := range matches {
			data, _ := os.ReadFile(match)
			found.WriteString(match + ": " + string(data) + "\n")
		}
		t.Fatalf("the record %s is not found as the latest complete record for %s:\n%s", applied.Applied.Record, image, found.String())
	}
	after, _ := vmRunRepair(t, folder, "-Mode", "Check", "-ImagePath", image, "-Target", "kvm")
	if driver := vmFindRepairDriver(t, after, "storahci"); driver.Start != 0 || driver.Override {
		t.Fatalf("storahci not set to boot start: %+v", driver)
	}
	if driver := vmFindRepairDriver(t, after, "vmbus"); driver.Start != 0 {
		t.Fatalf("vmbus changed: %+v", driver)
	}

	undone, out := vmRunRepair(t, folder, "-Mode", "Undo", "-ImagePath", image, "-Target", "kvm", "-RecordPath", records, "-UndoRecord", applied.Applied.Record)
	if !undone.OK || undone.Undone == nil || undone.Undone.Restored != 6 {
		t.Fatalf("undo failed: %+v\n%s", undone, out)
	}
	restored, _ := vmRunRepair(t, folder, "-Mode", "Check", "-ImagePath", image, "-Target", "kvm")
	if driver := vmFindRepairDriver(t, restored, "storahci"); driver.Start != 3 || !driver.Override {
		t.Fatalf("storahci not put back: %+v", driver)
	}
	if driver := vmFindRepairDriver(t, restored, "intelide"); driver.Start != 3 {
		t.Fatalf("intelide not put back: %+v", driver)
	}
	if vmImageAttached(t, image) {
		t.Fatal("the disk was left attached")
	}

	// Formats Windows cannot attach are refused before anything happens.
	raw := filepath.Join(folder, "disk.img")
	os.WriteFile(raw, make([]byte, 1<<20), 0600)
	refused, _ := vmRunRepair(t, folder, "-Mode", "Check", "-ImagePath", raw)
	if refused.OK || !strings.Contains(refused.Error, "VHD") {
		t.Fatalf("a raw image was not refused: %+v", refused)
	}
}
