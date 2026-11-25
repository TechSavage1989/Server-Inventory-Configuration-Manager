//go:build windows

package main

import "unsafe"

var (
	replGlobalMemoryStatus  = replKernel32.NewProc("GlobalMemoryStatusEx")
	replGetFirmwareTypeProc = replKernel32.NewProc("GetFirmwareType")
)

func replMemoryMB() int64 {
	var status struct {
		Length               uint32
		MemoryLoad           uint32
		TotalPhys            uint64
		AvailPhys            uint64
		TotalPageFile        uint64
		AvailPageFile        uint64
		TotalVirtual         uint64
		AvailVirtual         uint64
		AvailExtendedVirtual uint64
	}
	status.Length = uint32(unsafe.Sizeof(status))
	if ok, _, _ := replGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return 0
	}
	return int64(status.TotalPhys >> 20)
}

// replFirmware asks Windows how it started: 1 is BIOS, 2 is UEFI.
func replFirmware() string {
	if replGetFirmwareTypeProc.Find() != nil {
		return ""
	}
	var kind uint32
	if ok, _, _ := replGetFirmwareTypeProc.Call(uintptr(unsafe.Pointer(&kind))); ok == 0 {
		return ""
	}
	switch kind {
	case 1:
		return "bios"
	case 2:
		return "uefi"
	}
	return ""
}
