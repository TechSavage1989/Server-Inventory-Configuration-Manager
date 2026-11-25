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
	vmFsctlAllowExtendedDasdIO       = 0x00090083
	vmIoctlVolumeLogicalToPhysical   = 0x00560020
)

type vmVolumeOnDisk struct {
	Name       string // \\?\Volume{GUID}\
	FileSystem string
	Mounts     []string
	Start      int64
	Length     int64
	// Layout is set for a volume with more than one extent (a dynamic
	// spanned, striped or mirrored volume, or an extended simple one).
	Layout *vmVolumeLayout
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
	out := make([]byte, 8+24*32)
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(device.Fd()), vmIoctlVolumeGetVolumeDiskExtent, nil, 0, &out[0], uint32(len(out)), &returned, nil); err != nil {
		return volume, false, nil // CD/DVD and virtual volumes have no disk extents
	}
	count := int(binary.LittleEndian.Uint32(out))
	extents := []vmDiskExtent{}
	onDisk := false
	for i := 0; i < count && 8+i*24+24 <= len(out); i++ {
		raw := out[8+i*24:]
		extent := vmDiskExtent{Disk: int(binary.LittleEndian.Uint32(raw)), Start: int64(binary.LittleEndian.Uint64(raw[8:])), Length: int64(binary.LittleEndian.Uint64(raw[16:]))}
		extents = append(extents, extent)
		if extent.Disk == diskNumber {
			onDisk = true
		}
	}
	if !onDisk {
		return volume, false, nil
	}
	if count > len(extents) {
		return volume, false, fmt.Errorf("the volume %s has more than %d pieces", name, len(extents))
	}
	if count == 1 {
		volume.Start, volume.Length = extents[0].Start, extents[0].Length
	} else {
		length, err := vmDeviceLength(device)
		if err != nil {
			return volume, false, fmt.Errorf("the volume %s: %w", name, err)
		}
		handle := windows.Handle(device.Fd())
		layout, err := vmDetectVolumeLayout(length, extents, func(offset int64) ([]vmPhysicalOffset, error) {
			return vmLogicalToPhysical(handle, offset)
		})
		if err != nil {
			return volume, false, fmt.Errorf("the volume %s spans several places: %w", name, err)
		}
		volume.Layout = &layout
		parts := layout.Parts(diskNumber)
		if len(parts) > 0 {
			volume.Start = parts[0].DiskStart
			for _, part := range parts {
				volume.Length += part.Length
			}
		}
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

// vmLogicalToPhysical asks Windows where one volume offset lies on disk.
func vmLogicalToPhysical(volume windows.Handle, offset int64) ([]vmPhysicalOffset, error) {
	in := make([]byte, 8)
	binary.LittleEndian.PutUint64(in, uint64(offset))
	out := make([]byte, 8+16*8)
	var returned uint32
	if err := windows.DeviceIoControl(volume, vmIoctlVolumeLogicalToPhysical, &in[0], 8, &out[0], uint32(len(out)), &returned, nil); err != nil {
		return nil, err
	}
	count := int(binary.LittleEndian.Uint32(out))
	list := []vmPhysicalOffset{}
	for i := 0; i < count && 8+i*16+16 <= len(out); i++ {
		raw := out[8+i*16:]
		list = append(list, vmPhysicalOffset{Disk: int(binary.LittleEndian.Uint32(raw)), Offset: int64(binary.LittleEndian.Uint64(raw[8:]))})
	}
	return list, nil
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
	Kind       string   `json:"kind,omitempty"`
}

// openVMAgentDisk opens physical disk diskNumber of this computer with every
// NTFS and ReFS volume on it read from a new shadow copy. Close the source to
// delete the shadow copies. allowLive copies volumes without a shadow copy
// when Volume Shadow Copy is unavailable; that copy may be inconsistent.
func openVMAgentDisk(diskNumber int, allowLive bool, log func(string)) (vmCopySource, []vmAgentVolume, error) {
	set, err := openVMAgentDisks([]int{diskNumber}, allowLive, log)
	if err != nil {
		return nil, nil, err
	}
	source := set.Sources[diskNumber].(*vmCompositeSource)
	source.closers = append(set.shared, source.closers...) // closed last
	return source, set.Volumes[diskNumber], nil
}

// vmAgentDiskSet is several disks of this computer opened together: a
// volume on more than one of them (spanned, striped or mirrored) is read
// from one shadow copy, so every disk holds the same moment of it.
type vmAgentDiskSet struct {
	Sources map[int]vmCopySource
	Volumes map[int][]vmAgentVolume
	// Shadows maps each shadowed volume's name to its shadow copy device.
	Shadows map[string]string
	shared  []io.Closer
}

// Close closes every disk, then deletes the shadow copies.
func (set *vmAgentDiskSet) Close() error {
	var first error
	for _, source := range set.Sources {
		if closer, ok := source.(io.Closer); ok {
			if err := closer.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	for i := len(set.shared) - 1; i >= 0; i-- {
		if err := set.shared[i].Close(); err != nil && first == nil {
			first = err
		}
	}
	set.shared = nil
	return first
}

func openVMAgentDisks(numbers []int, allowLive bool, log func(string)) (*vmAgentDiskSet, error) {
	if log == nil {
		log = func(string) {}
	}
	set := &vmAgentDiskSet{Sources: map[int]vmCopySource{}, Volumes: map[int][]vmAgentVolume{}, Shadows: map[string]string{}}
	fail := func(err error) (*vmAgentDiskSet, error) {
		set.Close()
		return nil, err
	}
	inJob := map[int]bool{}
	for _, number := range numbers {
		inJob[number] = true
	}
	type shadowed struct {
		reader io.ReaderAt
		length int64
	}
	shadows := map[string]*shadowed{} // volume name -> its shadow copy, or nil when copied live
	shadowOf := func(volume vmVolumeOnDisk) (*shadowed, error) {
		if found, ok := shadows[volume.Name]; ok {
			return found, nil
		}
		id, device, err := vmCreateShadow(volume.Name)
		if err != nil {
			if !allowLive {
				return nil, err
			}
			log(err.Error() + " Copying it live.")
			shadows[volume.Name] = nil
			return nil, nil
		}
		set.shared = append(set.shared, vmShadowCloser{id})
		shadow, err := vmOpenDevice(device)
		if err != nil {
			return nil, err
		}
		set.shared = append(set.shared, shadow)
		// Lets reads reach the volume's last sectors past the file
		// system's size; a short tail still refused comes from the disk.
		var returned uint32
		windows.DeviceIoControl(windows.Handle(shadow.Fd()), vmFsctlAllowExtendedDasdIO, nil, 0, nil, 0, &returned, nil)
		length, _ := vmDeviceLength(shadow)
		found := &shadowed{reader: shadow, length: length}
		shadows[volume.Name] = found
		set.Shadows[volume.Name] = device
		log(fmt.Sprintf("Shadow copy of %s taken.", strings.Join(append([]string{volume.Name}, volume.Mounts...), " ")))
		return found, nil
	}
	hostName, _ := os.Hostname()
	for _, diskNumber := range numbers {
		path := fmt.Sprintf(`\\.\PhysicalDrive%d`, diskNumber)
		disk, err := vmOpenDevice(path)
		if err != nil {
			return fail(err)
		}
		size, err := vmDeviceLength(disk)
		if err != nil {
			disk.Close()
			return fail(fmt.Errorf("%s: %w", path, err))
		}
		volumes, err := vmDiskVolumes(diskNumber)
		if err != nil {
			disk.Close()
			return fail(err)
		}
		overlays := []vmOverlay{}
		report := []vmAgentVolume{}
		for _, volume := range volumes {
			item := vmAgentVolume{Name: volume.Name, Mounts: volume.Mounts, FileSystem: volume.FileSystem, Start: volume.Start, Length: volume.Length, Kind: "simple"}
			label := strings.Join(volume.Mounts, ", ")
			if label == "" {
				label = volume.Name
			}
			if volume.Layout != nil {
				item.Kind = volume.Layout.Kind
				for _, other := range volume.Layout.Disks() {
					if !inJob[other] && volume.Layout.Kind != "mirrored" {
						disk.Close()
						return fail(fmt.Errorf("the %s volume %s also lies on disk %d; add that disk to the copy so the volume is complete", volume.Layout.Kind, label, other))
					}
				}
			}
			if volume.FileSystem == "NTFS" || volume.FileSystem == "ReFS" {
				shadow, err := shadowOf(volume)
				if err != nil {
					disk.Close()
					return fail(err)
				}
				if shadow != nil {
					item.Shadowed = true
					if volume.Layout == nil {
						length := volume.Length
						if shadow.length > 0 && shadow.length < length {
							length = shadow.length / vhdxLogicalSector * vhdxLogicalSector
						}
						overlays = append(overlays, vmOverlay{Start: volume.Start, Length: length, Reader: shadow.reader, Name: label})
					} else {
						for _, part := range volume.Layout.Parts(diskNumber) {
							overlays = append(overlays, vmOverlay{Start: part.DiskStart, Length: part.Length, Reader: shadow.reader, Name: label, Map: part.Map, VolumeLength: volume.Layout.Length})
						}
					}
				}
			}
			report = append(report, item)
		}
		source, err := newVMCompositeSource(fmt.Sprintf("%s disk %d", hostName, diskNumber), disk, size, overlays, []io.Closer{disk})
		if err != nil {
			disk.Close()
			return fail(err)
		}
		set.Sources[diskNumber] = source
		set.Volumes[diskNumber] = report
	}
	return set, nil
}
