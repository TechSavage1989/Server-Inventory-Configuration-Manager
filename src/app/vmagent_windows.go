//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	vmIoctlDiskGetLengthInfo         = 0x0007405C
	vmIoctlVolumeGetVolumeDiskExtent = 0x00560000
)

type vmVolumeOnDisk struct {
	Name       string // \\?\Volume{GUID}\
	FileSystem string
	Mounts     []string
	Start      int64
	Length     int64
}

func vmOpenDevice(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return os.NewFile(uintptr(handle), path), nil
}

func vmDeviceLength(file *os.File) (int64, error) {
	var length int64
	var returned uint32
	err := windows.DeviceIoControl(windows.Handle(file.Fd()), vmIoctlDiskGetLengthInfo, nil, 0, (*byte)(unsafe.Pointer(&length)), 8, &returned, nil)
	return length, err
}

// vmDiskVolumes lists the volumes that lie on one physical disk.
func vmDiskVolumes(diskNumber int) ([]vmVolumeOnDisk, error) {
	buffer := make([]uint16, windows.MAX_PATH+1)
	find, err := windows.FindFirstVolume(&buffer[0], uint32(len(buffer)))
	if err != nil {
		return nil, err
	}
	defer windows.FindVolumeClose(find)
	volumes := []vmVolumeOnDisk{}
	for {
		name := windows.UTF16ToString(buffer)
		if volume, ok, err := vmVolumeExtent(name, diskNumber); err != nil {
			return nil, err
		} else if ok {
			volumes = append(volumes, volume)
		}
		if err := windows.FindNextVolume(find, &buffer[0], uint32(len(buffer))); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, err
		}
	}
	return volumes, nil
}

func vmVolumeExtent(name string, diskNumber int) (vmVolumeOnDisk, bool, error) {
	volume := vmVolumeOnDisk{Name: name}
	device, err := vmOpenDevice(strings.TrimSuffix(name, `\`))
	if err != nil {
		return volume, false, nil // removable drive with no media, or no access
	}
	defer device.Close()
	out := make([]byte, 8+24*8)
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(device.Fd()), vmIoctlVolumeGetVolumeDiskExtent, nil, 0, &out[0], uint32(len(out)), &returned, nil); err != nil {
		return volume, false, nil // CD/DVD and virtual volumes have no disk extents
	}
	count := int(binary.LittleEndian.Uint32(out))
	onDisk := 0
	for i := 0; i < count && 8+i*24+24 <= len(out); i++ {
		extent := out[8+i*24:]
		if int(binary.LittleEndian.Uint32(extent)) == diskNumber {
			onDisk++
			volume.Start = int64(binary.LittleEndian.Uint64(extent[8:]))
			volume.Length = int64(binary.LittleEndian.Uint64(extent[16:]))
		}
	}
	if onDisk == 0 {
		return volume, false, nil
	}
	if count != 1 {
		return volume, false, fmt.Errorf("the volume %s spans several disks; spanned and striped volumes are not supported", name)
	}
	fileSystem := make([]uint16, 64)
	if windows.GetVolumeInformation(windows.StringToUTF16Ptr(name), nil, 0, nil, nil, nil, &fileSystem[0], uint32(len(fileSystem))) == nil {
		volume.FileSystem = windows.UTF16ToString(fileSystem)
	}
	paths := make([]uint16, 1024)
	var length uint32
	if windows.GetVolumePathNamesForVolumeName(windows.StringToUTF16Ptr(name), &paths[0], uint32(len(paths)), &length) == nil {
		start := 0
		for i := 0; i < int(length) && i < len(paths); i++ {
			if paths[i] == 0 {
				if i > start {
					volume.Mounts = append(volume.Mounts, windows.UTF16ToString(paths[start:i]))
				}
				start = i + 1
			}
		}
	}
	return volume, true, nil
}

var vmShadowAnswer = regexp.MustCompile(`^(\{[0-9A-Fa-f-]{36}\})\|(\\\\\?\\GLOBALROOT\\Device\\HarddiskVolumeShadowCopy\d+)$`)
var vmShadowID = regexp.MustCompile(`^\{[0-9A-Fa-f-]{36}\}$`)
var vmVolumeName = regexp.MustCompile(`^\\\\\?\\Volume\{[0-9A-Fa-f-]{36}\}\\$`)

func vmRunPowerShell(script string) (string, error) {
	command := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "$ErrorActionPreference='Stop'; "+script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := command.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, text)
	}
	return text, nil
}

// vmCreateShadow takes a Volume Shadow Copy of one volume.
func vmCreateShadow(volume string) (id, device string, err error) {
	if !vmVolumeName.MatchString(volume) {
		return "", "", fmt.Errorf("%q is not a volume name", volume)
	}
	out, err := vmRunPowerShell(`$r = ([wmiclass]'root\cimv2:Win32_ShadowCopy').Create('` + volume + `', 'ClientAccessible'); if ($r.ReturnValue -ne 0) { throw ('Volume Shadow Copy returned ' + $r.ReturnValue) }; $s = Get-WmiObject Win32_ShadowCopy -Filter ("ID='" + $r.ShadowID + "'"); [Console]::Out.Write($r.ShadowID + '|' + $s.DeviceObject)`)
	if err != nil {
		return "", "", fmt.Errorf("could not take a shadow copy of %s: %w", volume, err)
	}
	match := vmShadowAnswer.FindStringSubmatch(out)
	if match == nil {
		return "", "", fmt.Errorf("unexpected shadow copy answer %q", out)
	}
	return match[1], match[2], nil
}

// vmDeleteShadow removes a shadow copy this agent took.
func vmDeleteShadow(id string) error {
	if !vmShadowID.MatchString(id) {
		return fmt.Errorf("%q is not a shadow copy ID", id)
	}
	_, err := vmRunPowerShell(`Get-WmiObject Win32_ShadowCopy -Filter "ID='` + id + `'" | ForEach-Object { $_.Delete() }`)
	return err
}

type vmShadowCloser struct{ id string }

func (c vmShadowCloser) Close() error { return vmDeleteShadow(c.id) }

type vmAgentVolume struct {
	Name       string   `json:"name"`
	Mounts     []string `json:"mounts"`
	FileSystem string   `json:"fileSystem"`
	Start      int64    `json:"start"`
	Length     int64    `json:"length"`
	Shadowed   bool     `json:"shadowed"`
}

// openVMAgentDisk opens physical disk diskNumber of this computer with every
// NTFS and ReFS volume on it read from a new shadow copy. Close the source to
// delete the shadow copies. allowLive copies volumes without a shadow copy
// when Volume Shadow Copy is unavailable; that copy may be inconsistent.
func openVMAgentDisk(diskNumber int, allowLive bool, log func(string)) (vmCopySource, []vmAgentVolume, error) {
	if log == nil {
		log = func(string) {}
	}
	path := fmt.Sprintf(`\\.\PhysicalDrive%d`, diskNumber)
	disk, err := vmOpenDevice(path)
	if err != nil {
		return nil, nil, err
	}
	closers := []io.Closer{disk}
	fail := func(err error) (vmCopySource, []vmAgentVolume, error) {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i].Close()
		}
		return nil, nil, err
	}
	size, err := vmDeviceLength(disk)
	if err != nil {
		return fail(fmt.Errorf("%s: %w", path, err))
	}
	volumes, err := vmDiskVolumes(diskNumber)
	if err != nil {
		return fail(err)
	}
	overlays := []vmOverlay{}
	report := []vmAgentVolume{}
	for _, volume := range volumes {
		item := vmAgentVolume{Name: volume.Name, Mounts: volume.Mounts, FileSystem: volume.FileSystem, Start: volume.Start, Length: volume.Length}
		if volume.FileSystem == "NTFS" || volume.FileSystem == "ReFS" {
			id, device, err := vmCreateShadow(volume.Name)
			if err != nil {
				if !allowLive {
					return fail(err)
				}
				log(err.Error() + " Copying it live.")
			} else {
				closers = append(closers, vmShadowCloser{id})
				shadow, err := vmOpenDevice(device)
				if err != nil {
					return fail(err)
				}
				closers = append(closers, shadow)
				length := volume.Length
				if shadowLength, err := vmDeviceLength(shadow); err == nil && shadowLength > 0 && shadowLength < length {
					length = shadowLength / vhdxLogicalSector * vhdxLogicalSector
				}
				overlays = append(overlays, vmOverlay{Start: volume.Start, Length: length, Reader: shadow, Name: strings.Join(volume.Mounts, ", ")})
				item.Shadowed = true
				log(fmt.Sprintf("Shadow copy of %s taken.", strings.Join(append([]string{volume.Name}, volume.Mounts...), " ")))
			}
		}
		report = append(report, item)
	}
	hostName, _ := os.Hostname()
	source, err := newVMCompositeSource(fmt.Sprintf("%s disk %d", hostName, diskNumber), disk, size, overlays, closers)
	if err != nil {
		return fail(err)
	}
	return source, report, nil
}
