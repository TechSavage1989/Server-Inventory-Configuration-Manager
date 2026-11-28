//go:build windows

package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var procGetSaveFileNameW = syscall.NewLazyDLL("comdlg32.dll").NewProc("GetSaveFileNameW")

const ofnOverwritePrompt = 0x00000002

// chooseSaveFile asks where to save a file, with Windows' own Save as box.
func chooseSaveFile(title, filter, initialDir, name, defExt string) (string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	_, _, _ = procCoInitializeEx.Call(0, coinitApartmentThreaded)
	defer procCoUninitialize.Call()
	owner := findApplicationWindow()
	bringWindowToFront(owner)
	buffer := make([]uint16, 32768)
	copy(buffer, utf16.Encode([]rune(name)))
	filterUnits := utf16.Encode([]rune(filter))
	dialog := openFileNameW{
		owner:   owner,
		filter:  &filterUnits[0],
		file:    &buffer[0],
		maxFile: uint32(len(buffer)),
		title:   utf16Ptr(title),
		defExt:  utf16Ptr(defExt),
		flags:   ofnExplorer | ofnPathMustExist | ofnHideReadOnly | ofnNoChangeDir | ofnDontAddToRecent | ofnOverwritePrompt,
	}
	dialog.structSize = uint32(unsafe.Sizeof(dialog))
	if initialDir != "" {
		dialog.initialDir = utf16Ptr(initialDir)
	}
	result, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&dialog)))
	if result == 0 {
		return "", false, nil
	}
	selected := strings.TrimSpace(syscall.UTF16ToString(buffer))
	if selected == "" {
		return "", false, errors.New("the chosen file name was empty")
	}
	return selected, true, nil
}

// handleVMLog answers GET with one VM's log (vm=<name>) or the whole VMware
// log (no vm), and POST {vm, save:true} saves a copy where the operator
// chooses.
func (a *application) handleVMLog(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	logs := a.vmLogFiles()
	if logs == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "Logs are not kept yet."})
		return
	}
	vm := ""
	if request.Method == http.MethodPost {
		var input struct {
			VM string `json:"vm"`
		}
		if !decodeVMwareRequest(response, request, &input) {
			return
		}
		vm = strings.TrimSpace(input.VM)
	} else {
		vm = strings.TrimSpace(request.URL.Query().Get("vm"))
	}
	path, label := logs.SharedPath(), "VMware"
	if vm != "" {
		path, label = logs.Path(vm), vmLogFileName(vm)
	}
	text, err := logs.Read(path)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if request.Method != http.MethodPost {
		_, statErr := os.Stat(path)
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "vm": vm, "text": text, "path": path, "folder": filepath.Dir(path), "exists": statErr == nil})
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()
	home, _ := os.UserHomeDir()
	selected, ok, err := chooseSaveFile("Save the log as", "Log files (*.log;*.txt)\x00*.log;*.txt\x00All files (*.*)\x00*.*\x00\x00", filepath.Join(home, "Documents"), label+".log", "log")
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !ok {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "cancelled": true})
		return
	}
	if err := os.WriteFile(selected, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0600); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The log could not be saved: " + err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "path": selected})
}
