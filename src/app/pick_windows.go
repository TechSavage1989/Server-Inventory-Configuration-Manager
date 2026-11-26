//go:build windows

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	bffmInitialized   = 1
	bffmSetSelectionW = 0x0400 + 103 // WM_USER + 103
)

// folderPickerCallback selects the starting folder (lpData, a UTF-16 path)
// when the folder picker opens. It is made once: Windows callbacks made with
// syscall.NewCallback are never freed.
var folderPickerCallback = syscall.NewCallback(func(hwnd, msg, lParam, lpData uintptr) uintptr {
	if msg == bffmInitialized && lpData != 0 {
		procSendMessageW.Call(hwnd, bffmSetSelectionW, 1, lpData)
	}
	return 0
})

// pickExistingFolder walks up from path to the nearest folder that exists,
// so the picker opens as close as possible to what the field holds.
func pickExistingFolder(path string, file bool) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) {
		return ""
	}
	path = filepath.Clean(path)
	if file {
		path = filepath.Dir(path)
	}
	for i := 0; i < 32; i++ {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
	return ""
}

// pickReachable checks the host's administrative share with this computer's
// account, giving up after 15 seconds so a host that does not answer never
// leaves a picker waiting.
func pickReachable(root string) bool {
	done := make(chan bool, 1)
	go func() {
		info, err := os.Stat(root + `\`)
		done <- err == nil && info.IsDir()
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(15 * time.Second):
		return false
	}
}

// handlePick opens the native folder or file picker for a path field
// (POST /api/pick). kind is folder, file or remote (a folder on a Hyper-V
// host, through its administrative share). It only returns a path.
func (a *application) handlePick(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Kind    string `json:"kind"`
		Filter  string `json:"filter"`
		Title   string `json:"title"`
		Current string `json:"current"`
		Host    string `json:"host"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()
	current := strings.ReplaceAll(input.Current, "\x00", "")
	var selected string
	var ok bool
	var err error
	switch input.Kind {
	case "folder":
		selected, ok, err = chooseFolderAt(pickTitle(input.Title, "Choose a folder on this computer."), pickExistingFolder(current, false))
	case "file":
		selected, ok, err = chooseFile(pickTitle(input.Title, "Choose a file on this computer"), pickFilter(input.Filter), pickExistingFolder(current, true))
	case "remote":
		host, hostErr := pickHost(input.Host)
		if hostErr != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Browse needs the host: " + hostErr.Error() + "."})
			return
		}
		root, start := pickRemoteStart(host, current)
		if !pickReachable(root) {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "This computer could not open " + root + " with your Windows account, so its folders cannot be browsed from here. Pick a volume above, or type the path on " + host + "."})
			return
		}
		if !pickReachable(start) {
			start = root
		}
		title := pickTitle(input.Title, "Choose a folder on "+host+".") + " Showing " + host + "'s drives through its administrative shares."
		selected, ok, err = chooseFolderAt(pickTitle(title, title), start)
		if err == nil && ok {
			selected, err = pickRemoteLocal(host, selected)
			if err != nil {
				writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + "."})
				return
			}
		}
	default:
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Unknown picker."})
		return
	}
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": ok, "path": selected})
}
