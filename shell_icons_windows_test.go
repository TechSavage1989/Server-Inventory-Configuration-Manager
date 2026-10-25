//go:build windows

package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func createIconTestShortcut(t *testing.T, shortcut, executable, icon string) {
	t.Helper()
	err := withShellLink(func(link, persist *shellCOM) error {
		for _, item := range []struct {
			method int
			value  string
		}{
			{20, executable}, {11, "--preserved-argument \"two words\""},
			{9, filepath.Dir(executable)}, {7, "Preserved shortcut description"}, {17, icon},
		} {
			value, err := syscall.UTF16PtrFromString(item.value)
			if err != nil {
				return err
			}
			// Pass converted pointers directly to the uintptrescapes call,
			// so stack growth/GC cannot invalidate COM input buffers.
			if item.method == 17 {
				if err := link.call(item.method, uintptr(unsafe.Pointer(value)), 0); err != nil {
					return err
				}
			} else {
				if err := link.call(item.method, uintptr(unsafe.Pointer(value))); err != nil {
					return err
				}
			}
		}
		if err := link.call(13, 0x0649); err != nil {
			return err
		}
		if err := link.call(15, 3); err != nil {
			return err
		}
		file, err := syscall.UTF16PtrFromString(shortcut)
		if err != nil {
			return err
		}
		return persist.call(6, uintptr(unsafe.Pointer(file)), 1)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readIconTestShortcut(t *testing.T, shortcut string) map[string]string {
	t.Helper()
	values := make(map[string]string)
	err := withShellLink(func(link, persist *shellCOM) error {
		file, _ := syscall.UTF16PtrFromString(shortcut)
		if err := persist.call(5, uintptr(unsafe.Pointer(file)), 0); err != nil {
			return err
		}
		for _, item := range []struct {
			method int
			name   string
		}{{3, "target"}, {10, "arguments"}, {8, "directory"}, {6, "description"}, {16, "icon"}} {
			var text [32768]uint16
			var index int32
			switch item.method {
			case 3:
				if err := link.call(item.method, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)), 0, 4); err != nil {
					return err
				}
			case 16:
				if err := link.call(item.method, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)), uintptr(unsafe.Pointer(&index))); err != nil {
					return err
				}
			default:
				if err := link.call(item.method, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text))); err != nil {
					return err
				}
			}
			if item.method == 16 && index != 0 {
				t.Fatal("incorrect icon index", index)
			}
			values[item.name] = syscall.UTF16ToString(text[:])
		}
		var show uint32
		var hotkey uint16
		if err := link.call(14, uintptr(unsafe.Pointer(&show))); err != nil {
			return err
		}
		if err := link.call(12, uintptr(unsafe.Pointer(&hotkey))); err != nil {
			return err
		}
		if show != 3 || hotkey != 0x0649 {
			t.Fatal("shortcut show state or hotkey changed", show, hotkey)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func nativeShellIcon(t *testing.T, path string) uintptr {
	t.Helper()
	var info struct {
		Icon        uintptr
		Index       int32
		Attributes  uint32
		DisplayName [260]uint16
		TypeName    [80]uint16
	}
	value, _ := syscall.UTF16PtrFromString(path)
	result, _, err := shell32.NewProc("SHGetFileInfoW").Call(uintptr(unsafe.Pointer(value)), 0, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0x100)
	if result == 0 || info.Icon == 0 {
		t.Fatal("Explorer icon lookup failed", path, err)
	}
	t.Cleanup(func() { user32.NewProc("DestroyIcon").Call(info.Icon) })
	return info.Icon
}

// Render Windows' own returned HICON into a DIB. This checks actual shell
// pixels, rather than equating a nonzero native handle with the approved icon.
func shellIconPixels(t *testing.T, icon uintptr) []byte {
	t.Helper()
	gdi := syscall.NewLazyDLL("gdi32.dll")
	dc, _, _ := gdi.NewProc("CreateCompatibleDC").Call(0)
	if dc == 0 {
		t.Fatal("no icon rendering DC")
	}
	defer gdi.NewProc("DeleteDC").Call(dc)
	var bitmap struct {
		Size                        uint32
		Width, Height               int32
		Planes, Bits                uint16
		Compression, SizeImage      uint32
		XPels, YPels                int32
		ColorsUsed, ColorsImportant uint32
	}
	bitmap.Size = 40
	bitmap.Width = 32
	bitmap.Height = -32
	bitmap.Planes = 1
	bitmap.Bits = 32
	var pixels *byte
	dib, _, err := gdi.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&bitmap)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if dib == 0 || pixels == nil {
		t.Fatal("no icon bitmap", err)
	}
	defer gdi.NewProc("DeleteObject").Call(dib)
	previous, _, _ := gdi.NewProc("SelectObject").Call(dc, dib)
	defer gdi.NewProc("SelectObject").Call(dc, previous)
	buffer := unsafe.Slice(pixels, 32*32*4)
	for i := range buffer {
		buffer[i] = 0
	}
	ok, _, err := user32.NewProc("DrawIconEx").Call(dc, 0, 0, icon, 32, 32, 0, 0, 3)
	if ok == 0 {
		t.Fatal("icon rendering failed", err)
	}
	gdi.NewProc("GdiFlush").Call()
	return append([]byte(nil), buffer...)
}

func saveShellIconEvidence(t *testing.T, name string, pixels []byte) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for i := 0; i < len(pixels); i += 4 {
		img.Pix[i] = pixels[i+2]
		img.Pix[i+1] = pixels[i+1]
		img.Pix[i+2] = pixels[i]
		img.Pix[i+3] = pixels[i+3]
	}
	if err := os.MkdirAll("build", 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join("build", "shell-icon-"+name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func TestExplorerAndDesktopShortcutUseApprovedIcon(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "Server-Inventory-Configuration-Exporter-GUI-v3.0.10.exe")
	if err := os.WriteFile(executable, fixture(t), 0700); err != nil {
		t.Fatal(err)
	}
	oldIcon := filepath.Join(root, "old.ico")
	oldBytes, err := os.ReadFile("baseline/v3.0.10/icons/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldIcon, oldBytes, 0600); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(root, "LocalAppData", appName)
	icon, err := persistentShellIcon(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	approved := shellIconPixels(t, nativeShellIcon(t, icon))
	saveShellIconEvidence(t, "expected", approved)
	if !bytes.Equal(approved, shellIconPixels(t, nativeShellIcon(t, executable))) {
		t.Fatal("Explorer EXE icon does not match approved SVG-derived ICO")
	}
	desktop := filepath.Join(root, "Desktop")
	if err := os.MkdirAll(desktop, 0700); err != nil {
		t.Fatal(err)
	}
	shortcut := filepath.Join(desktop, "Server inventory.lnk")
	createIconTestShortcut(t, shortcut, executable, oldIcon)
	before := readIconTestShortcut(t, shortcut)
	t.Logf("Shortcut target=%q executable=%q", before["target"], executable)
	if bytes.Equal(approved, shellIconPixels(t, nativeShellIcon(t, shortcut))) {
		t.Fatal("negative control did not retain the previous desktop icon")
	}
	t.Log("Negative control: actual shortcut retains old artwork despite its target EXE containing the new icon")
	unrelated := filepath.Join(desktop, "Other application.lnk")
	createIconTestShortcut(t, unrelated, filepath.Join(root, "other.exe"), oldIcon)
	unrelatedBytes, _ := os.ReadFile(unrelated)
	malformed := filepath.Join(desktop, "Malformed.lnk")
	if err := os.WriteFile(malformed, []byte("malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	portable := filepath.Join(root, "Portable shortcut.lnk")
	createIconTestShortcut(t, portable, executable, oldIcon)
	refreshShellIcons(executable, icon, []shellShortcutDirectory{{desktop, icon}, {root, executable}}, t.Logf)
	after := readIconTestShortcut(t, shortcut)
	for _, key := range []string{"target", "arguments", "directory", "description"} {
		if before[key] != after[key] {
			t.Fatal("shortcut property changed", key)
		}
	}
	if !sameShellPath(after["icon"], icon) {
		t.Fatal("shortcut still references obsolete icon", after["icon"])
	}
	// SHCNF_FLUSHNOWAIT deliberately queues delivery so a stalled Explorer
	// cannot block normal application startup. Allow that notification to land.
	var actual []byte
	deadline := time.Now().Add(5 * time.Second)
	for {
		actual = shellIconPixels(t, nativeShellIcon(t, shortcut))
		if bytes.Equal(approved, actual) || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	saveShellIconEvidence(t, "shortcut-after", actual)
	if !bytes.Equal(approved, actual) {
		t.Fatal("actual Explorer shortcut pixels do not match approved icon")
	}
	if shared := readIconTestShortcut(t, portable); !sameShellPath(shared["icon"], executable) {
		t.Fatal("shared or portable link incorrectly depends on a per-user icon", shared)
	}
	// Validate the exact shell-pixel probe used by the later published update
	// before a release is allowed to publish.
	probe := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-File", "scripts/Test-ShellIcon.ps1", "-ExecutablePath", executable, "-ShortcutPath", shortcut, "-ExpectedIconPath", icon)
	if output, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("published shell-icon probe failed: %v\n%s", err, output)
	} else {
		t.Logf("Published shell-icon probe: %s", output)
	}
	if b, _ := os.ReadFile(unrelated); !bytes.Equal(b, unrelatedBytes) {
		t.Fatal("unrelated shortcut was changed")
	}
	first, _ := os.ReadFile(shortcut)
	if changed, err := repairShellShortcut(shortcut, executable, icon); err != nil || changed {
		t.Fatal("unchanged shortcut was rewritten", err)
	}
	if second, _ := os.ReadFile(shortcut); !bytes.Equal(first, second) {
		t.Fatal("unchanged shortcut bytes differ")
	}
	// Browser-session cleanup cannot invalidate the persistent shortcut icon.
	if err := os.RemoveAll(filepath.Join(dataRoot, "Runtime")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(icon); err != nil {
		t.Fatal("shortcut icon did not survive browser-session cleanup", err)
	}
	// Repair corruption at the owned content-addressed location, without touching
	// any unrelated file or removing older artwork still used by a shortcut.
	if err := os.WriteFile(icon, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if repaired, err := persistentShellIcon(dataRoot); err != nil || repaired != icon {
		t.Fatal("icon corruption was not repaired", err)
	}
	data, _ := os.ReadFile(icon)
	expected, _ := embeddedPayloads.ReadFile("favicon.ico")
	if !bytes.Equal(data, expected) {
		t.Fatal("persistent icon bytes differ")
	}
	t.Log("Explorer EXE/shortcut pixels match approved artwork; link properties, unrelated links, persistent ICO and idempotence passed")
}
