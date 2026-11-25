//go:build !windows

package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func replMemoryMB() int64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil {
				return kb >> 10
			}
		}
	}
	return 0
}

// replFirmware: Linux exposes /sys/firmware/efi only when started by UEFI.
func replFirmware() string {
	if _, err := os.Stat("/sys/firmware/efi"); err == nil {
		return "uefi"
	}
	if _, err := os.Stat("/sys/firmware"); err == nil {
		return "bios"
	}
	return ""
}
