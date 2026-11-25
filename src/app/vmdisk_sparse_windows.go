//go:build windows

package main

import (
	"os"
	"syscall"
)

const fsctlSetSparse = 0x000900c4

// vmMarkSparse marks a new output file sparse on NTFS so writing near the
// end of a large raw or fixed image does not first fill it with zeros.
// File systems without sparse files keep working, only slower.
func vmMarkSparse(file *os.File) {
	var returned uint32
	_ = syscall.DeviceIoControl(syscall.Handle(file.Fd()), fsctlSetSparse, nil, 0, nil, 0, &returned, nil)
}
