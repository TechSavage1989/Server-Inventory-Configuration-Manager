package main

import (
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
)

// vmSourceMachine describes the computer a copy came from, so the VM
// started from the copies can be built with the same shape: the agent sends
// it with each finished disk and the receiver keeps it in the copy record.
type vmSourceMachine struct {
	Name     string `json:"name,omitempty"`
	CPUs     int    `json:"cpus,omitempty"`
	MemoryMB int64  `json:"memoryMB,omitempty"`
	// Firmware is "uefi" or "bios" ("" when unknown).
	Firmware string `json:"firmware,omitempty"`
	Adapters int    `json:"adapters,omitempty"`
	OS       string `json:"os,omitempty"`
}

var (
	replMachineOnce sync.Once
	replMachineSeen vmSourceMachine
	// replMachineInfo is replaced in tests.
	replMachineInfo = func() vmSourceMachine {
		replMachineOnce.Do(func() { replMachineSeen = replReadMachine() })
		return replMachineSeen
	}
)

// replReadMachine reads this computer's processors, memory, firmware and
// physical network adapters. Each value is left empty when it cannot be
// read, never guessed.
func replReadMachine() vmSourceMachine {
	m := vmSourceMachine{CPUs: runtime.NumCPU(), OS: runtime.GOOS}
	if name, err := os.Hostname(); err == nil {
		m.Name = vmCleanMachineName(name)
	}
	m.MemoryMB = replMemoryMB()
	m.Firmware = replFirmware()
	m.Adapters = replAdapterCount()
	return m
}

func vmCleanMachineName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// replAdapterCount counts the adapters that look like real network cards:
// up, with a hardware address, not loopback, tunnels or virtual switches.
func replAdapterCount() int {
	interfaces, err := net.Interfaces()
	if err != nil {
		return 0
	}
	count := 0
	for _, adapter := range interfaces {
		name := strings.ToLower(adapter.Name)
		if adapter.Flags&net.FlagLoopback != 0 || len(adapter.HardwareAddr) != 6 {
			continue
		}
		skip := false
		for _, prefix := range []string{"docker", "veth", "virbr", "br-", "vnet", "tun", "tap", "isatap", "teredo", "vethernet", "lo"} {
			if strings.HasPrefix(name, prefix) {
				skip = true
			}
		}
		if !skip {
			count++
		}
	}
	return count
}
