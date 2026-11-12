//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows/registry"
)

func TestWebViewTitleOnlyAcceptsApplicationTitles(t *testing.T) {
	for _, accepted := range []string{appName, "42% Collecting \u2014 " + appName, "  Collection complete \u2014 " + appName + "  "} {
		if webViewTitle(accepted) == "" {
			t.Fatal("application title rejected:", accepted)
		}
	}
	for _, rejected := range []string{"", "Untitled", "Evil\r\n" + appName, strings.Repeat("x", 190) + appName} {
		if webViewTitle(rejected) != "" {
			t.Fatal("title that hides the application name accepted:", rejected)
		}
	}
}

func TestBuiltInWindowDefaultBoundsFitSmallAndLargeScreens(t *testing.T) {
	for _, tc := range []struct {
		work                windowRect
		x, y, width, height int32
	}{
		{windowRect{0, 0, 800, 560}, 32, 22, 736, 515},        // 800x600 with taskbar
		{windowRect{0, 0, 1024, 728}, 41, 29, 942, 669},       // 1024x768 with taskbar
		{windowRect{0, 0, 2560, 1400}, 480, 200, 1600, 1000},  // large screen is capped
		{windowRect{0, 40, 1920, 1080}, 160, 82, 1600, 956},   // taskbar at the top
		{windowRect{-1920, 0, 0, 1040}, -1760, 42, 1600, 956}, // monitor left of primary
	} {
		x, y, width, height := centredBounds(tc.work)
		if x != tc.x || y != tc.y || width != tc.width || height != tc.height {
			t.Fatalf("work %v: got %d,%d %dx%d want %d,%d %dx%d", tc.work, x, y, width, height, tc.x, tc.y, tc.width, tc.height)
		}
	}
}

// blockGuestBrowsingForUser sets the same per-user policy a managed PC uses to
// turn off guest browsing in Edge and Chrome, and restores the prior state.
func blockGuestBrowsingForUser(t *testing.T) {
	t.Helper()
	for _, path := range []string{`Software\Policies\Microsoft\Edge`, `Software\Policies\Google\Chrome`} {
		key, existed, err := registry.CreateKey(registry.CURRENT_USER, path, registry.QUERY_VALUE|registry.SET_VALUE)
		if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			t.Skip("setting a browser policy needs an elevated test session")
		}
		if err != nil {
			t.Fatal(err)
		}
		prior, _, priorErr := key.GetIntegerValue("BrowserGuestModeEnabled")
		err = key.SetDWordValue("BrowserGuestModeEnabled", 0)
		key.Close()
		if err != nil {
			t.Fatal(err)
		}
		path, existed, prior, hadPrior := path, existed, prior, priorErr == nil
		t.Cleanup(func() {
			if k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE); err == nil {
				if hadPrior {
					_ = k.SetDWordValue("BrowserGuestModeEnabled", uint32(prior))
				} else {
					_ = k.DeleteValue("BrowserGuestModeEnabled")
				}
				k.Close()
			}
			if !existed {
				_ = registry.DeleteKey(registry.CURRENT_USER, path)
			}
		})
	}
	if guestPolicyBlock(`C:\Edge\msedge.exe`) == "" || guestPolicyBlock(`C:\Chrome\chrome.exe`) == "" {
		t.Fatal("the guest-browsing policy is not visible to the application")
	}
}

func builtInWindowOf(pid int) uintptr {
	var found uintptr
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var owner uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if int(owner) == pid && windowClass(hwnd) == webViewClassName {
			found = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return found
}

// Runs the built application where policy blocks every guest browser window
// and requires the built-in window to render the interface, confirm a healthy
// start without any browser profile, and end the application when closed.
func TestExecutableUsesBuiltInWindowWhenGuestBrowsingIsBlocked(t *testing.T) {
	if reason := webViewRuntimeUnavailable(); reason != "" {
		t.Skip(reason)
	}
	version, _ := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	t.Logf("WebView2 Runtime %s", version)
	blockGuestBrowsingForUser(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	target := filepath.Join(t.TempDir(), stableExecutableName)
	if err := os.WriteFile(target, fixture(t), 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), appName)
	command := exec.Command(target)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			terminateProcessTree(command.Process.Pid)
		}
	})
	wait := func(name string, timeout time.Duration, fn func() bool) {
		t.Helper()
		for until := time.Now().Add(timeout); time.Now().Before(until); time.Sleep(200 * time.Millisecond) {
			if fn() {
				return
			}
			select {
			case err := <-done:
				t.Fatalf("application exited before %s: %v; log: %s", name, err, readUpdateLog(root))
			default:
			}
		}
		t.Fatalf("timed out waiting for %s; log: %s", name, readUpdateLog(root))
	}
	var hwnd uintptr
	wait("a healthy start in the built-in window", 90*time.Second, func() bool {
		data, err := os.ReadFile(filepath.Join(root, "Health", "current.json"))
		if err != nil {
			return false
		}
		var marker healthMarker
		if json.Unmarshal(data, &marker) != nil || marker.PID != command.Process.Pid || !marker.UIReady || !marker.VisibleWindowConfirmed {
			return false
		}
		hwnd = builtInWindowOf(command.Process.Pid)
		return hwnd != 0
	})
	if title := windowText(hwnd); !strings.Contains(title, appName) {
		t.Fatal("built-in window title does not name the application:", title)
	}
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		t.Fatal("built-in window is not visible")
	}
	runtimeDir := filepath.Join(root, "Runtime")
	if profiles, _ := filepath.Glob(filepath.Join(runtimeDir, "Browser-*")); len(profiles) != 0 {
		t.Fatal("a browser profile was created even though guest browsing is blocked:", profiles)
	}
	if info, err := os.Stat(filepath.Join(runtimeDir, "WebView2")); err != nil || !info.IsDir() {
		t.Fatal("the app-owned WebView2 data folder was not used", err)
	}
	if log := readUpdateLog(root); !strings.Contains(log, "Opened the interface in the built-in window") || !strings.Contains(log, "BrowserGuestModeEnabled=0") {
		t.Fatal("the fallback and its policy reason were not recorded:", log)
	}
	procPostMessageWUpdate.Call(hwnd, wmClose, 0, 0)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("closing the built-in window did not end the application")
	}
}

func readUpdateLog(root string) string {
	data, _ := os.ReadFile(filepath.Join(root, "Logs", "Updates.log"))
	return string(data)
}
