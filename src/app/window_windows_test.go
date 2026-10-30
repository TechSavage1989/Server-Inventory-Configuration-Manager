//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowGeometryRejectsCorruptionAndRecoversOffScreenBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Settings", "window.json")
	want := windowGeometry{Schema: 1, Bounds: windowRect{-1500, 80, -600, 680}, Maximized: true}
	if err := saveWindowGeometry(path, want); err != nil {
		t.Fatal(err)
	}
	if got, ok := loadWindowGeometry(path); !ok || got != want {
		t.Fatal("negative monitor coordinates were lost", got)
	}
	for _, malformed := range []string{`{"schema_version":99}`, `{"schema_version":1,"normal_bounds":{"Left":1,"Top":2,"Right":2147483647,"Bottom":900}}`, `{"schema_version":1,"normal_bounds":{"Left":0,"Top":0,"Right":-10,"Bottom":1000}}`, `{broken`} {
		if err := os.WriteFile(path, []byte(malformed), 0600); err != nil {
			t.Fatal(err)
		}
		if _, ok := loadWindowGeometry(path); ok {
			t.Fatal("malformed placement was accepted", malformed)
		}
	}
	for _, tc := range []struct{ bounds, work, expected windowRect }{
		{windowRect{50000, 50000, 50900, 50600}, windowRect{0, 0, 1280, 720}, windowRect{380, 120, 1280, 720}},
		{windowRect{-1800, 80, -900, 680}, windowRect{-1920, 0, 0, 1040}, windowRect{-1800, 80, -900, 680}},
		{windowRect{-20, -30, 4000, 3000}, windowRect{0, 0, 1280, 720}, windowRect{0, 0, 1280, 720}},
	} {
		if got := fitWindowBounds(tc.bounds, tc.work); got != tc.expected {
			t.Fatal("unusable restored bounds", got, tc.expected)
		}
	}
}

// Runs the built application and its real guest window, not a substitute UI.
func TestExecutableRemembersWindowBoundsMaximizedAndMinimizedClose(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	target := filepath.Join(t.TempDir(), stableExecutableName)
	if err := os.WriteFile(target, fixture(t), 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), appName)
	settings := filepath.Join(root, "Settings", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	settingsBytes, _ := json.Marshal(applicationSettings{OutputRoot: filepath.Join(t.TempDir(), "Preserved Reports")})
	if err := os.WriteFile(settings, settingsBytes, 0600); err != nil {
		t.Fatal(err)
	}
	geometryPath := filepath.Join(root, "Settings", "window.json")
	var command *exec.Cmd
	var done chan error
	var address, profile string
	var hwnd uintptr
	wait := func(name string, timeout time.Duration, fn func() bool) {
		t.Helper()
		until := time.Now().Add(timeout)
		for time.Now().Before(until) {
			if fn() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("timed out", name)
	}
	closeApp := func(nativeClose bool) {
		t.Helper()
		if nativeClose {
			procPostMessageWUpdate.Call(hwnd, wmClose, 0, 0)
		} else {
			u, _ := url.Parse(address)
			request, _ := http.NewRequest(http.MethodPost, u.Scheme+"://"+u.Host+"/api/shutdown", nil)
			request.Header.Set("X-App-Token", u.Query().Get("token"))
			response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("app shutdown rejected", response.StatusCode)
			}
		}
		select {
		case <-done:
		case <-time.After(42 * time.Second):
			t.Fatal("app did not close")
		}
		command = nil
	}
	t.Cleanup(func() {
		if command != nil && command.Process != nil {
			terminateProcessTree(command.Process.Pid)
		}
		if profile != "" {
			_ = closeScopedBrowser(profile)
		}
	})
	startApp := func() {
		t.Helper()
		_ = os.Remove(filepath.Join(root, "Health", "current.json"))
		command = exec.Command(target)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done = make(chan error, 1)
		current := command
		go func() { done <- current.Wait() }()
		wait("visible healthy real application", 45*time.Second, func() bool {
			data, err := os.ReadFile(filepath.Join(root, "Health", "current.json"))
			if err != nil {
				return false
			}
			var marker healthMarker
			if json.Unmarshal(data, &marker) != nil || marker.PID != current.Process.Pid || !marker.UIReady || !marker.VisibleWindowConfirmed {
				return false
			}
			data, err = os.ReadFile(filepath.Join(root, "Runtime", "application-url.txt"))
			if err != nil {
				return false
			}
			address = string(data)
			var ok bool
			profile, _, ok = profileFromApplicationURL(address)
			if !ok {
				return false
			}
			hwnd = (&application{browserProfileDirectory: profile}).ownApplicationWindow()
			return hwnd != 0
		})
	}
	wanted := windowGeometry{Schema: 1, Bounds: windowRect{87, 63, 987, 663}}
	startApp()
	// Exercise the real registered endpoint and on-disk choices in the same
	// native-close/relaunch flow; this does not run synthetic inventory commands.
	preferences := collectionOptions{OutputRoot: filepath.Join(t.TempDir(), "Saved choices"), InventoryCategories: []string{"Hardware", "EventLogs"}, ExportCategories: []string{}, EventLogDays: 17}
	preferenceBytes, _ := json.Marshal(preferences)
	appURL, _ := url.Parse(address)
	preferenceRequest, _ := http.NewRequest(http.MethodPost, appURL.Scheme+"://"+appURL.Host+"/api/preferences", bytes.NewReader(preferenceBytes))
	preferenceRequest.Header.Set("X-App-Token", appURL.Query().Get("token"))
	preferenceResponse, err := (&http.Client{Timeout: 5 * time.Second}).Do(preferenceRequest)
	if err != nil {
		t.Fatal(err)
	}
	preferenceResponse.Body.Close()
	if preferenceResponse.StatusCode != 200 {
		t.Fatal("real executable could not save choices", preferenceResponse.StatusCode)
	}
	if !restoreWindowGeometry(hwnd, wanted) {
		t.Fatal("could not move and resize the real window")
	}
	wait("automatic window position/size save", 8*time.Second, func() bool { got, ok := loadWindowGeometry(geometryPath); return ok && got == wanted })
	// A native title-bar close destroys the browser before backend shutdown.
	// The live observer must already have retained its last usable placement.
	closeApp(true)
	startApp()
	appURL, _ = url.Parse(address)
	stateRequest, _ := http.NewRequest(http.MethodGet, appURL.Scheme+"://"+appURL.Host+"/api/state", nil)
	stateRequest.Header.Set("X-App-Token", appURL.Query().Get("token"))
	stateResponseBody, err := (&http.Client{Timeout: 5 * time.Second}).Do(stateRequest)
	if err != nil {
		t.Fatal(err)
	}
	var restored stateResponse
	decodeErr := json.NewDecoder(stateResponseBody.Body).Decode(&restored)
	stateResponseBody.Body.Close()
	if decodeErr != nil || restored.CollectionPreferences == nil || restored.CollectionPreferences.Options.EventLogDays != 17 || restored.CollectionPreferences.Options.OutputRoot != preferences.OutputRoot || len(restored.CollectionPreferences.Options.InventoryCategories) != 2 {
		t.Fatal("real executable did not restore collection choices", decodeErr, restored.CollectionPreferences)
	}
	wait("normal position and size restored on reopening", 8*time.Second, func() bool { got, ok := captureWindowGeometry(hwnd); return ok && got == wanted })
	procShowWindow.Call(hwnd, 3)
	wanted.Maximized = true
	wait("maximized state saved", 8*time.Second, func() bool { got, ok := loadWindowGeometry(geometryPath); return ok && got == wanted })
	procShowWindow.Call(hwnd, 6)
	wait("real window minimized", 5*time.Second, func() bool { p, ok := getNativeWindowPlacement(hwnd); return ok && p.ShowCmd == 2 })
	time.Sleep(750 * time.Millisecond)
	if got, ok := loadWindowGeometry(geometryPath); !ok || got != wanted {
		t.Fatal("minimizing overwrote the last usable placement", got)
	}
	closeApp(false)
	startApp()
	wait("reopens maximized after closing while minimized", 8*time.Second, func() bool { got, ok := captureWindowGeometry(hwnd); return ok && got == wanted })
	procShowWindow.Call(hwnd, 9)
	wanted.Maximized = false
	wait("restoring from maximize retains normal bounds", 8*time.Second, func() bool { got, ok := captureWindowGeometry(hwnd); return ok && got == wanted })
	closeApp(false)
	// Simulate a saved monitor that no longer exists without changing the runner's display.
	if err := saveWindowGeometry(geometryPath, windowGeometry{Schema: 1, Bounds: windowRect{50000, 50000, 50900, 50600}}); err != nil {
		t.Fatal(err)
	}
	startApp()
	wait("off-screen saved window recovered", 8*time.Second, func() bool {
		got, ok := captureWindowGeometry(hwnd)
		return ok && got.Bounds.Left < 50000 && got.Bounds.Top < 50000 && !got.Maximized
	})
	closeApp(false)
	after, err := os.ReadFile(settings)
	if err != nil || !bytes.Equal(after, settingsBytes) {
		t.Fatal("window persistence changed existing application settings", err)
	}
	if _, err := os.Stat(geometryPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary placement file was left behind", err)
	}
	t.Log("Actual EXE move/resize, native close/reopen, maximize, minimized close, restored normal bounds, off-screen recovery and settings preservation passed")
}
