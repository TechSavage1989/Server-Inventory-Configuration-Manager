//go:build windows

package main

// Windows physical disks as raw copy targets: \\.\PhysicalDriveN on the
// receiver (the new VM or server booted from receiver media). The disk is
// taken offline for this session only before anything is written, so Windows
// never mounts the partitions the copy creates, and a disk that holds this
// computer's Windows, this program or its copy records is always refused.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const (
	ioctlDiskGetDriveGeometryEx      = 0x000700A0
	ioctlDiskGetLengthInfo           = 0x0007405C
	ioctlDiskGetDiskAttributes       = 0x000700F0
	ioctlDiskSetDiskAttributes       = 0x0007C0F4
	ioctlDiskUpdateProperties        = 0x00070140
	ioctlStorageQueryProperty        = 0x002D1400
	ioctlVolumeGetVolumeDiskExtents  = 0x00560000
	diskAttributeOffline             = 0x1
	diskAttributeReadOnly            = 0x2
	vmMaxPhysicalDisks               = 64
	vmPhysicalDiskPath               = `\\.\PhysicalDrive`
	storageDevicePropertyStandardQry = 0
)

// vmWindowsDiskInfo describes one physical disk for listing and checks.
type vmWindowsDiskInfo struct {
	Number     int    `json:"number"`
	Size       int64  `json:"size"`
	SectorSize int64  `json:"sectorSize"`
	Vendor     string `json:"vendor"`
	Product    string `json:"product"`
	Serial     string `json:"serial"`
	Offline    bool   `json:"offline"`
	Blank      bool   `json:"blank"`
	Protected  string `json:"protected,omitempty"`
}

func (d vmWindowsDiskInfo) Label() string {
	model := strings.TrimSpace(d.Vendor + " " + d.Product)
	if model == "" {
		model = "disk"
	}
	if d.Serial != "" {
		return fmt.Sprintf("Disk %d, %s, serial %s", d.Number, model, d.Serial)
	}
	return fmt.Sprintf("Disk %d, %s", d.Number, model)
}

func (d vmWindowsDiskInfo) Identity() string {
	if d.Serial != "" {
		return fmt.Sprintf("physical|%s|%s|%s|%d", d.Serial, strings.TrimSpace(d.Vendor), strings.TrimSpace(d.Product), d.Size)
	}
	// Virtual disks often have no serial number; the disk number and size
	// then name it, which stays stable while the receiver media runs.
	return fmt.Sprintf("physical|disk%d|%s|%s|%d", d.Number, strings.TrimSpace(d.Vendor), strings.TrimSpace(d.Product), d.Size)
}

func vmDeviceIoControl(handle windows.Handle, code uint32, in []byte, out []byte) (uint32, error) {
	var returned uint32
	var inPointer, outPointer *byte
	if len(in) > 0 {
		inPointer = &in[0]
	}
	if len(out) > 0 {
		outPointer = &out[0]
	}
	err := windows.DeviceIoControl(handle, code, inPointer, uint32(len(in)), outPointer, uint32(len(out)), &returned, nil)
	return returned, err
}

func vmOpenDiskHandle(number int, write bool) (windows.Handle, error) {
	path, err := windows.UTF16PtrFromString(fmt.Sprintf("%s%d", vmPhysicalDiskPath, number))
	if err != nil {
		return windows.InvalidHandle, err
	}
	access := uint32(windows.GENERIC_READ)
	flags := uint32(0)
	if write {
		access |= windows.GENERIC_WRITE
		flags = windows.FILE_FLAG_WRITE_THROUGH
	}
	return windows.CreateFile(path, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
}

func vmCString(buffer []byte, offset uint32) string {
	if offset == 0 || int(offset) >= len(buffer) {
		return ""
	}
	end := int(offset)
	for end < len(buffer) && buffer[end] != 0 {
		end++
	}
	return strings.TrimSpace(string(buffer[offset:end]))
}

func vmQueryDiskInfo(handle windows.Handle, number int) (vmWindowsDiskInfo, error) {
	info := vmWindowsDiskInfo{Number: number}
	length := make([]byte, 8)
	if _, err := vmDeviceIoControl(handle, ioctlDiskGetLengthInfo, nil, length); err != nil {
		return info, fmt.Errorf("reading the size of disk %d: %w", number, err)
	}
	info.Size = int64(binary.LittleEndian.Uint64(length))
	geometry := make([]byte, 256)
	if _, err := vmDeviceIoControl(handle, ioctlDiskGetDriveGeometryEx, nil, geometry); err == nil {
		info.SectorSize = int64(binary.LittleEndian.Uint32(geometry[20:24]))
	}
	if info.SectorSize <= 0 {
		info.SectorSize = 512
	}
	query := make([]byte, 12) // StorageDeviceProperty, PropertyStandardQuery
	descriptor := make([]byte, 1024)
	if _, err := vmDeviceIoControl(handle, ioctlStorageQueryProperty, query, descriptor); err == nil {
		info.Vendor = vmCString(descriptor, binary.LittleEndian.Uint32(descriptor[12:16]))
		info.Product = vmCString(descriptor, binary.LittleEndian.Uint32(descriptor[16:20]))
		info.Serial = vmCString(descriptor, binary.LittleEndian.Uint32(descriptor[24:28]))
	}
	attributes := make([]byte, 16)
	if _, err := vmDeviceIoControl(handle, ioctlDiskGetDiskAttributes, nil, attributes); err == nil {
		info.Offline = binary.LittleEndian.Uint64(attributes[8:16])&diskAttributeOffline != 0
	}
	return info, nil
}

// vmVolumeDiskNumbers returns the physical disks a path's volume lives on.
func vmVolumeDiskNumbers(path string) ([]int, error) {
	if path == "" {
		return nil, nil
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	volume := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(pointer, &volume[0], uint32(len(volume))); err != nil {
		return nil, err
	}
	root := strings.TrimRight(windows.UTF16ToString(volume), `\`)
	if root == "" {
		return nil, nil
	}
	devicePath := `\\.\` + root
	if strings.HasPrefix(root, `\\?\Volume{`) {
		devicePath = root
	}
	devicePointer, err := windows.UTF16PtrFromString(devicePath)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(devicePointer, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	extents := make([]byte, 8+24*32)
	if _, err := vmDeviceIoControl(handle, ioctlVolumeGetVolumeDiskExtents, nil, extents); err != nil {
		// A RAM disk (WinPE's X:) or network path has no physical extents.
		return nil, nil
	}
	count := int(binary.LittleEndian.Uint32(extents[0:4]))
	numbers := []int{}
	for i := 0; i < count && 8+24*i+4 <= len(extents); i++ {
		numbers = append(numbers, int(binary.LittleEndian.Uint32(extents[8+24*i:])))
	}
	return numbers, nil
}

// vmProtectedDisks names the disks that must never be written: the one
// holding Windows, this program, and the copy records.
func vmProtectedDisks(records string) map[int]string {
	protected := map[int]string{}
	executable, _ := os.Executable()
	for _, item := range []struct{ path, reason string }{
		{os.Getenv("SystemRoot"), "it holds this computer's Windows"},
		{executable, "it holds this program"},
		{records, "it holds the copy records"},
	} {
		numbers, _ := vmVolumeDiskNumbers(item.path)
		for _, number := range numbers {
			if _, seen := protected[number]; !seen {
				protected[number] = item.reason
			}
		}
	}
	return protected
}

// vmListPhysicalDisks lists the disks this computer can see.
func vmListPhysicalDisks(records string) []vmWindowsDiskInfo {
	protected := vmProtectedDisks(records)
	disks := []vmWindowsDiskInfo{}
	for number := 0; number < vmMaxPhysicalDisks; number++ {
		handle, err := vmOpenDiskHandle(number, false)
		if err != nil {
			continue
		}
		info, err := vmQueryDiskInfo(handle, number)
		if err == nil {
			device := &vmWindowsDisk{file: os.NewFile(uintptr(handle), fmt.Sprintf("%s%d", vmPhysicalDiskPath, number)), info: info}
			info.Blank, _ = vmDeviceIsBlank(device)
			info.Protected = protected[number]
			disks = append(disks, info)
			device.file.Close()
		} else {
			windows.CloseHandle(handle)
		}
	}
	return disks
}

// vmWindowsDisk is an open physical disk.
type vmWindowsDisk struct {
	file *os.File
	info vmWindowsDiskInfo
}

// openVMPhysicalDisk opens disk number for writing after checking it is not
// protected, and takes it offline (not persistently) and writable.
func openVMPhysicalDisk(number int, records string) (*vmWindowsDisk, error) {
	if reason, protected := vmProtectedDisks(records)[number]; protected {
		return nil, fmt.Errorf("disk %d cannot be a copy target because %s", number, reason)
	}
	handle, err := vmOpenDiskHandle(number, true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, fmt.Errorf("disk %d needs administrator rights to write: %w", number, err)
		}
		return nil, fmt.Errorf("opening disk %d: %w", number, err)
	}
	info, err := vmQueryDiskInfo(handle, number)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	// SET_DISK_ATTRIBUTES: Version 40, Persist false, Attributes 0 (online
	// cleared to offline below), mask offline and read-only.
	set := make([]byte, 40)
	binary.LittleEndian.PutUint32(set[0:4], 40)
	binary.LittleEndian.PutUint64(set[8:16], diskAttributeOffline)
	binary.LittleEndian.PutUint64(set[16:24], diskAttributeOffline|diskAttributeReadOnly)
	if _, err := vmDeviceIoControl(handle, ioctlDiskSetDiskAttributes, set, nil); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("taking disk %d offline before writing: %w", number, err)
	}
	info.Offline = true
	return &vmWindowsDisk{file: os.NewFile(uintptr(handle), fmt.Sprintf("%s%d", vmPhysicalDiskPath, number)), info: info}, nil
}

func (d *vmWindowsDisk) ReadAt(p []byte, off int64) (int, error)  { return d.file.ReadAt(p, off) }
func (d *vmWindowsDisk) WriteAt(p []byte, off int64) (int, error) { return d.file.WriteAt(p, off) }
func (d *vmWindowsDisk) Size() int64                              { return d.info.Size }
func (d *vmWindowsDisk) SectorSize() int64                        { return d.info.SectorSize }
func (d *vmWindowsDisk) Identity() string                         { return d.info.Identity() }
func (d *vmWindowsDisk) Describe() string                         { return d.info.Label() }
func (d *vmWindowsDisk) Flush() error                             { return windows.FlushFileBuffers(windows.Handle(d.file.Fd())) }

// Close tells Windows the partition table may have changed; the disk stays
// offline until the computer restarts or an administrator brings it online.
func (d *vmWindowsDisk) Close() error {
	if d.file == nil {
		return nil
	}
	vmDeviceIoControl(windows.Handle(d.file.Fd()), ioctlDiskUpdateProperties, nil, nil)
	err := d.file.Close()
	d.file = nil
	return err
}

// vmReceiverPhysicalDisks turns disk numbers into receiver disks named
// disk-N. overwrite lists the numbers the operator allowed to be overwritten.
func vmReceiverPhysicalDisks(numbers []int, overwrite map[int]bool, records string) ([]vmReceiverDisk, error) {
	listed := map[int]vmWindowsDiskInfo{}
	for _, info := range vmListPhysicalDisks(records) {
		listed[info.Number] = info
	}
	disks := []vmReceiverDisk{}
	for _, number := range numbers {
		info, ok := listed[number]
		if !ok {
			return nil, fmt.Errorf("disk %d was not found", number)
		}
		if info.Protected != "" {
			return nil, fmt.Errorf("disk %d cannot be a copy target because %s", number, info.Protected)
		}
		number := number
		disks = append(disks, vmReceiverDisk{
			Name: fmt.Sprintf("disk-%d", number), Label: info.Label(), Size: info.Size, SectorSize: info.SectorSize, Overwrite: overwrite[number],
			open: func() (vmBlockDevice, error) { return openVMPhysicalDisk(number, records) },
		})
	}
	return disks, nil
}

// vmReceiverRecordsFolder is the default home of raw disk copy records.
// WinPE has no LocalAppData, so records then go beside the program.
func vmReceiverRecordsFolder() string {
	if root := os.Getenv("LOCALAPPDATA"); root != "" {
		return filepath.Join(root, appName, "Receiver", "Records")
	}
	executable, _ := os.Executable()
	return filepath.Join(filepath.Dir(executable), "Receiver-Records")
}
