//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// Shell icons must outlive a disposable browser session. A content-addressed
// filename also gives Explorer a fresh icon key when the approved artwork changes.
func persistentShellIcon(root string) (string, error) {
	data, err := embeddedPayloads.ReadFile("favicon.ico")
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, "Icons")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, fmt.Sprintf("application-%x.ico", sha256.Sum256(data)))
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return path, nil
	}
	file, err := os.CreateTemp(directory, ".icon-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = atomicReplaceFile(file.Name(), path)
	}
	return path, err
}

type shellGUID struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}

var (
	shellLinkClass        = shellGUID{0x00021401, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	shellLinkIID          = shellGUID{0x000214f9, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	persistFileIID        = shellGUID{0x0000010b, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	desktopFolderID       = shellGUID{0xb4bfcc3a, 0xdb2c, 0x424c, [8]byte{0xb0, 0x29, 0x7f, 0xe9, 0x9a, 0x87, 0xc6, 0x41}}
	publicDesktopFolderID = shellGUID{0xc4aa340d, 0xf20f, 0x4863, [8]byte{0xaf, 0xef, 0xf8, 0x7e, 0xf2, 0xe6, 0xba, 0x25}}
)

type shellCOM struct{ vtable *[21]uintptr }

func (object *shellCOM) call(method int, arguments ...uintptr) error {
	arguments = append([]uintptr{uintptr(unsafe.Pointer(object))}, arguments...)
	result, _, _ := syscall.SyscallN(object.vtable[method], arguments...)
	runtime.KeepAlive(object)
	if int32(result) < 0 {
		return fmt.Errorf("Windows shell method %d failed (0x%08x)", method, uint32(result))
	}
	return nil
}

// Every COM operation runs on the same initialized STA thread. Resolve is never
// called: it can display dialogs or search for a missing/unrelated application.
func withShellLink(action func(link, persist *shellCOM) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if int32(hr) < 0 {
		return fmt.Errorf("Windows shell initialization failed (0x%08x)", uint32(hr))
	}
	defer procCoUninitialize.Call()
	var link, persist *shellCOM
	hr, _, _ = ole32.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&shellLinkClass)), 0, 1, uintptr(unsafe.Pointer(&shellLinkIID)), uintptr(unsafe.Pointer(&link)))
	if int32(hr) < 0 || link == nil {
		return fmt.Errorf("Windows shortcut interface failed (0x%08x)", uint32(hr))
	}
	defer link.call(2)
	if err := link.call(0, uintptr(unsafe.Pointer(&persistFileIID)), uintptr(unsafe.Pointer(&persist))); err != nil {
		return err
	}
	defer persist.call(2)
	return action(link, persist)
}

func shellKnownFolder(id *shellGUID) (string, error) {
	var value *uint16
	hr, _, _ := shell32.NewProc("SHGetKnownFolderPath").Call(uintptr(unsafe.Pointer(id)), 0, 0, uintptr(unsafe.Pointer(&value)))
	if value != nil {
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(value)))
	}
	if int32(hr) < 0 || value == nil {
		return "", fmt.Errorf("Windows desktop folder unavailable (0x%08x)", uint32(hr))
	}
	// Windows paths are limited to 32767 UTF-16 code units.
	return syscall.UTF16ToString(unsafe.Slice(value, 32768)), nil
}

func notifyShellIcon(path string) {
	value, err := syscall.UTF16PtrFromString(path)
	if err == nil {
		// SHCNE_UPDATEITEM, SHCNF_PATHW | SHCNF_FLUSHNOWAIT. Notify only
		// the changed item; never delete global caches or restart Explorer.
		shell32.NewProc("SHChangeNotify").Call(0x2000, 0x0005|0x3000, uintptr(unsafe.Pointer(value)), 0)
	}
}

func sameShellPath(left, right string) bool {
	return left != "" && right != "" && strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func repairShellShortcut(shortcut, executable, icon string) (bool, error) {
	changed := false
	err := withShellLink(func(link, persist *shellCOM) error {
		file, err := syscall.UTF16PtrFromString(shortcut)
		if err != nil {
			return err
		}
		// STGM_READWRITE. GetPath uses SLGP_RAWPATH without Resolve/UI/network search.
		if err := persist.call(5, uintptr(unsafe.Pointer(file)), 2); err != nil {
			return err
		}
		var target, previous [32768]uint16
		if err := link.call(3, uintptr(unsafe.Pointer(&target[0])), uintptr(len(target)), 0, 4); err != nil {
			return err
		}
		if !sameShellPath(syscall.UTF16ToString(target[:]), executable) {
			return nil
		}
		var index int32
		if err := link.call(16, uintptr(unsafe.Pointer(&previous[0])), uintptr(len(previous)), uintptr(unsafe.Pointer(&index))); err != nil {
			return err
		}
		if sameShellPath(syscall.UTF16ToString(previous[:]), icon) && index == 0 {
			return nil
		}
		value, err := syscall.UTF16PtrFromString(icon)
		if err != nil {
			return err
		}
		if err := link.call(17, uintptr(unsafe.Pointer(value)), 0); err != nil {
			return err
		}
		// Save the loaded object in place. Arguments, target, working directory,
		// description, hotkey, show state and other link properties stay intact.
		if err := persist.call(6, uintptr(unsafe.Pointer(file)), 1); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if changed {
		notifyShellIcon(shortcut)
	}
	return changed, err
}

type shellShortcutDirectory struct{ path, icon string }

func refreshShellIcons(executable, icon string, directories []shellShortcutDirectory, report func(string, ...any)) {
	notifyShellIcon(executable)
	notifyShellIcon(icon)
	seen := make(map[string]bool)
	for _, directory := range directories {
		key := strings.ToLower(filepath.Clean(directory.path))
		if directory.path == "" || seen[key] {
			continue
		}
		seen[key] = true
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".lnk") {
				continue
			}
			path := filepath.Join(directory.path, entry.Name())
			changed, err := repairShellShortcut(path, executable, directory.icon)
			if err != nil {
				report("Shortcut icon could not be checked: %s: %v", path, err)
			}
			if changed {
				report("Shortcut icon refreshed: %s", path)
			}
		}
	}
	report("Explorer icon refresh completed for %s", executable)
}

func (a *application) refreshDesktopIcons() {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	icon, err := persistentShellIcon(a.update.appDataRoot)
	if err != nil {
		a.logUpdate("Persistent shell icon could not be prepared: %v", err)
		return
	}
	var directories []shellShortcutDirectory
	if desktop, err := shellKnownFolder(&desktopFolderID); err == nil {
		directories = append(directories, shellShortcutDirectory{desktop, icon})
	}
	// Shared/portable shortcuts must keep an icon readable by every user.
	if desktop, err := shellKnownFolder(&publicDesktopFolderID); err == nil {
		directories = append(directories, shellShortcutDirectory{desktop, executable})
	}
	directories = append(directories, shellShortcutDirectory{filepath.Dir(executable), executable})
	refreshShellIcons(executable, icon, directories, a.logUpdate)
}
