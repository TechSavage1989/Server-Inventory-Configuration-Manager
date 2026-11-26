//go:build windows

package main

// The Hyper-V host's own storage, so copies land where the host keeps its VMs
// rather than in the output folder: the host's default virtual hard disk and
// VM folders and, on a failover cluster node, its Cluster Shared Volumes, each
// with its free space. Read only on a Hyper-V host, and nothing is changed.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

type vmStorageVolume struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Free  uint64 `json:"freeBytes"`
	Total uint64 `json:"totalBytes"`
	CSV   bool   `json:"csv"`
}

type vmHostStorage struct {
	// Known is false until the host's default folders have been read.
	Known     bool              `json:"known"`
	VHDPath   string            `json:"vhdPath"`
	VMPath    string            `json:"vmPath"`
	Clustered bool              `json:"clustered"`
	Volumes   []vmStorageVolume `json:"volumes"`
	// Switches are the host's virtual switches, for matching VMware
	// networks the plan does not map (vmAutoSwitches).
	Switches []string `json:"switches"`
}

var (
	vmHostDefaultsOnce sync.Once
	vmHostDefaultsMu   sync.Mutex
	vmHostDefaults     struct {
		known    bool
		vhd, vm  string
		switches []string
	}
)

// vmReadHostDefaults reads the host's default folders through WMI, without
// the Hyper-V module, once per run.
func vmReadHostDefaults() {
	out, err := vmRunPowerShell("$s = Get-WmiObject -Namespace 'root\\virtualization\\v2' -Class Msvm_VirtualSystemManagementServiceSettingData | Select-Object -First 1; [Console]::Out.WriteLine([string]$s.DefaultVirtualHardDiskPath); [Console]::Out.WriteLine([string]$s.DefaultExternalDataRoot); foreach ($w in @(Get-WmiObject -Namespace 'root\\virtualization\\v2' -Class Msvm_VirtualEthernetSwitch)) { [Console]::Out.WriteLine('SWITCH ' + [string]$w.ElementName) }")
	vmHostDefaultsMu.Lock()
	defer vmHostDefaultsMu.Unlock()
	vmHostDefaults.known = true
	if err != nil {
		return
	}
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	clean := func(index int) string {
		if index < len(lines) {
			if path := strings.TrimSpace(lines[index]); filepath.IsAbs(path) {
				return filepath.Clean(path)
			}
		}
		return ""
	}
	vmHostDefaults.vhd, vmHostDefaults.vm = clean(0), clean(1)
	vmHostDefaults.switches = nil
	for _, line := range lines {
		if name := strings.TrimSpace(strings.TrimPrefix(line, "SWITCH ")); strings.HasPrefix(line, "SWITCH ") && name != "" {
			vmHostDefaults.switches = append(vmHostDefaults.switches, name)
		}
	}
}

// vmHostSwitches lists this Hyper-V host's virtual switches (read with the
// default folders).
func vmHostSwitches() []string {
	vmHostDefaultsOnce.Do(func() { go vmReadHostDefaults() })
	vmHostDefaultsMu.Lock()
	defer vmHostDefaultsMu.Unlock()
	return append([]string(nil), vmHostDefaults.switches...)
}

func vmHostDefaultFolders() (known bool, vhd, vm string) {
	vmHostDefaultsOnce.Do(func() { go vmReadHostDefaults() })
	vmHostDefaultsMu.Lock()
	defer vmHostDefaultsMu.Unlock()
	return vmHostDefaults.known, vmHostDefaults.vhd, vmHostDefaults.vm
}

// vmClusterStorageRoot is where Windows mounts Cluster Shared Volumes.
func vmClusterStorageRoot() string {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	return drive + `\ClusterStorage`
}

// vmOnClusterStorage reports whether path is inside a Cluster Shared Volume.
func vmOnClusterStorage(path string) bool {
	root := strings.ToLower(vmClusterStorageRoot()) + `\`
	clean := strings.ToLower(filepath.Clean(path))
	return strings.HasPrefix(clean, root) && len(clean) > len(root)
}

// vmFreeSpace returns the free and total bytes of the volume holding path,
// walking up to the nearest folder that exists.
func vmFreeSpace(path string) (free, total uint64, ok bool) {
	path = filepath.Clean(path)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, 0, false
		}
		path = parent
	}
	pointer, err := windows.UTF16PtrFromString(strings.TrimRight(path, `\`) + `\`)
	if err != nil {
		return 0, 0, false
	}
	var available, size, all uint64
	if windows.GetDiskFreeSpaceEx(pointer, &available, &size, &all) != nil {
		return 0, 0, false
	}
	return available, size, true
}

// vmHostStorageNow describes this Hyper-V host's storage, or nil when this
// computer is not a Hyper-V host.
func vmHostStorageNow() *vmHostStorage {
	if !vmHyperVHere() {
		return nil
	}
	known, vhd, vm := vmHostDefaultFolders()
	storage := &vmHostStorage{Known: known, VHDPath: vhd, VMPath: vm, Switches: vmHostSwitches()}
	if entries, err := os.ReadDir(vmClusterStorageRoot()); err == nil {
		for _, entry := range entries {
			path := filepath.Join(vmClusterStorageRoot(), entry.Name())
			if free, total, ok := vmFreeSpace(path); ok {
				storage.Clustered = true
				storage.Volumes = append(storage.Volumes, vmStorageVolume{Path: path, Label: "Cluster Shared Volume", Free: free, Total: total, CSV: true})
			}
		}
	}
	if vhd != "" {
		if free, total, ok := vmFreeSpace(vhd); ok {
			storage.Volumes = append(storage.Volumes, vmStorageVolume{Path: vhd, Label: "This host's default for virtual hard disks", Free: free, Total: total})
		}
	}
	return storage
}

// vmHostDefaultAllowed lets copies go into the host's own default virtual
// hard disk folder even where it sits under ProgramData (the Hyper-V default).
func vmHostDefaultAllowed(path string) bool {
	_, vhd, _ := vmHostDefaultFolders()
	if vhd == "" {
		return false
	}
	clean, root := strings.ToLower(filepath.Clean(path)), strings.ToLower(vhd)
	return clean == root || strings.HasPrefix(clean, root+`\`)
}

// vmFolderBytes adds up the files directly in folder (the copies so far).
func vmFolderBytes(folder string) uint64 {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return 0
	}
	var total uint64
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			total += uint64(info.Size())
		}
	}
	return total
}
