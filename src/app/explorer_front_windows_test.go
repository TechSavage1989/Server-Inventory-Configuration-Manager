//go:build windows

package main

import (
	"testing"
	"time"
)

func TestChooseExplorerWindowPrefersNewFolderWindow(t *testing.T) {
	known := map[uintptr]bool{1: true, 2: true}
	title := "server01_20261110"
	for _, tc := range []struct {
		name    string
		windows []uintptr
		titles  []string
		elapsed time.Duration
		want    uintptr
	}{
		{"new titled window wins over an older window showing the same folder", []uintptr{1, 3}, []string{"SERVER01_20261110", `C:\Reports\SERVER01_20261110`}, 0, 3},
		{"older matching window is not raised before a new one can appear", []uintptr{1, 2}, []string{"SERVER01_20261110", "Documents"}, 200 * time.Millisecond, 0},
		{"older matching window is raised when Explorer reuses it", []uintptr{1, 2}, []string{"SERVER01_20261110", "Documents"}, time.Second, 1},
		{"untitled new window waits for its title", []uintptr{4, 2}, []string{"", "Documents"}, 300 * time.Millisecond, 0},
		{"new window with a different title is the late fallback", []uintptr{4, 2}, []string{"Berichte", "Documents"}, 2 * time.Second, 4},
		{"unrelated existing windows are never raised", []uintptr{1, 2}, []string{"Downloads", "Documents"}, 5 * time.Second, 0},
	} {
		if got := chooseExplorerWindow(tc.windows, tc.titles, known, title, tc.elapsed); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Opens a real folder through the production path and requires the new
// Explorer window to end up above the window that was in front before.
func TestOpenFolderRaisesExplorerWindow(t *testing.T) {
	dir := t.TempDir()
	before := explorerWindows()
	if err := shellOpenFolder(dir); err != nil {
		t.Fatal(err)
	}
	known := map[uintptr]bool{}
	for _, hwnd := range before {
		known[hwnd] = true
	}
	var opened uintptr
	deadline := time.Now().Add(15 * time.Second)
	for opened == 0 && time.Now().Before(deadline) {
		for _, hwnd := range explorerWindows() {
			if !known[hwnd] {
				opened = hwnd
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if opened == 0 {
		t.Skip("no interactive Explorer window appeared in this session")
	}
	defer procSendMessageW.Call(opened, 0x0010, 0, 0) // WM_CLOSE
	raised := false
	for time.Now().Before(deadline) && !raised {
		foreground, _, _ := procGetForegroundWindow.Call()
		windows := explorerWindows()
		raised = foreground == opened || (len(windows) > 0 && windows[0] == opened)
		time.Sleep(200 * time.Millisecond)
	}
	if !raised {
		t.Fatal("the opened folder window was not brought to the front")
	}
}
