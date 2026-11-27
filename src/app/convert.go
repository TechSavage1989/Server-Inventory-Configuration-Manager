//go:build windows

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// The Disk converter tab: inspect any VMDK, VHD, VHDX, qcow2 or raw image
// (vmdisk_*.go) and convert it to another format (vmconvert.go). A
// conversion is a critical operation: updates cannot install and nothing
// else can start until it finishes or is stopped. Source images are only
// ever read; an existing file is never overwritten except by repeating the
// same conversion, which rewrites only the chunks that changed.

type convertRuntime struct {
	running         bool
	status          string
	message         string
	source          string
	target          string
	format          string
	info            *vmDiskInfo
	progress        vmCopyProgress
	result          *vmConvertResult
	cancel          context.CancelFunc
	cancelRequested bool
	serial          uint64
	startedAt       time.Time
	endedAt         time.Time
	// task is what is running or ran last: convert or repair.
	task         string
	repairTarget string
	repair       *vmRepairReport
	log          []string
	// A send to a host: its stage, progress and result.
	sendStage  string
	sendDone   int64
	sendTotal  int64
	sendResult *vmSendResult
}

type convertFormat struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Extension string `json:"extension"`
	Use       string `json:"use"`
	Limit     int64  `json:"limit"`
}

type convertStateResponse struct {
	Running         bool             `json:"running"`
	Status          string           `json:"status"`
	Message         string           `json:"message"`
	Source          string           `json:"source"`
	Target          string           `json:"target"`
	Format          string           `json:"format"`
	Info            *vmDiskInfo      `json:"info"`
	Progress        vmCopyProgress   `json:"progress"`
	Result          *vmConvertResult `json:"result"`
	CancelRequested bool             `json:"cancelRequested"`
	Serial          uint64           `json:"serial"`
	Formats         []convertFormat  `json:"formats"`
	StartedAtUnixMs int64            `json:"startedAtUnixMs"`
	EndedAtUnixMs   int64            `json:"endedAtUnixMs"`
	Task            string           `json:"task"`
	RepairTarget    string           `json:"repairTarget"`
	Repair          *vmRepairReport  `json:"repair"`
	RepairTargets   []vmRepairTarget `json:"repairTargets"`
	Log             []string         `json:"log"`
	Send            convertSendState `json:"send"`
}

// Caller holds a.mu.
func (a *application) convertBusyLocked() bool { return a.convert.running }

// Caller holds a.mu.
func (a *application) convertSnapshotLocked() convertStateResponse {
	millis := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixNano() / int64(time.Millisecond)
	}
	status := a.convert.status
	if status == "" {
		status = "idle"
	}
	formats := []convertFormat{}
	for _, item := range vmDiskFormatNames {
		formats = append(formats, convertFormat{ID: item.ID, Name: item.Name, Extension: item.Extension, Use: item.Use, Limit: vmDiskFormatLimit(item.ID)})
	}
	return convertStateResponse{
		Running: a.convert.running, Status: status, Message: a.convert.message,
		Source: a.convert.source, Target: a.convert.target, Format: a.convert.format,
		Info: a.convert.info, Progress: a.convert.progress, Result: a.convert.result,
		CancelRequested: a.convert.cancelRequested && a.convert.running, Serial: a.convert.serial,
		Formats: formats, StartedAtUnixMs: millis(a.convert.startedAt), EndedAtUnixMs: millis(a.convert.endedAt),
		Task: vmNonEmpty(a.convert.task, "convert"), RepairTarget: a.convert.repairTarget, Repair: a.convert.repair,
		RepairTargets: vmRepairTargets, Log: append([]string{}, a.convert.log...),
		Send: a.convertSendStateLocked(),
	}
}

// Native file picker (comdlg32).

var procGetOpenFileNameW = syscall.NewLazyDLL("comdlg32.dll").NewProc("GetOpenFileNameW")

type openFileNameW struct {
	structSize    uint32
	owner         uintptr
	instance      uintptr
	filter        *uint16
	customFilter  *uint16
	maxCustFilter uint32
	filterIndex   uint32
	file          *uint16
	maxFile       uint32
	fileTitle     *uint16
	maxFileTitle  uint32
	initialDir    *uint16
	title         *uint16
	flags         uint32
	fileOffset    uint16
	fileExtension uint16
	defExt        *uint16
	custData      uintptr
	hook          uintptr
	templateName  *uint16
	reserved      uintptr
	reservedWord  uint32
	flagsEx       uint32
}

const (
	ofnHideReadOnly          = 0x4
	ofnNoChangeDir           = 0x8
	ofnPathMustExist         = 0x800
	ofnFileMustExist         = 0x1000
	ofnExplorer              = 0x80000
	ofnDontAddToRecent       = 0x02000000
	convertFixupDialogFilter = "VHD and VHDX disks (*.vhdx;*.vhd)\x00*.vhdx;*.vhd\x00\x00"
	convertDialogFilter      = "Virtual disks (*.vmdk;*.vhd;*.vhdx;*.avhd;*.avhdx;*.qcow2;*.img;*.raw)\x00*.vmdk;*.vhd;*.vhdx;*.avhd;*.avhdx;*.qcow2;*.qcow;*.img;*.raw;*.bin\x00All files (*.*)\x00*.*\x00\x00"
)

func chooseFile(title, filter, initialDir string) (string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	_, _, _ = procCoInitializeEx.Call(0, coinitApartmentThreaded)
	defer procCoUninitialize.Call()
	owner := findApplicationWindow()
	bringWindowToFront(owner)
	buffer := make([]uint16, 32768)
	filterUnits := utf16.Encode([]rune(filter))
	dialog := openFileNameW{
		owner:   owner,
		filter:  &filterUnits[0],
		file:    &buffer[0],
		maxFile: uint32(len(buffer)),
		title:   utf16Ptr(title),
		flags:   ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly | ofnNoChangeDir | ofnDontAddToRecent,
	}
	dialog.structSize = uint32(unsafe.Sizeof(dialog))
	if initialDir != "" {
		dialog.initialDir = utf16Ptr(initialDir)
	}
	result, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&dialog)))
	if result == 0 {
		return "", false, nil
	}
	selected := strings.TrimSpace(syscall.UTF16ToString(buffer))
	if selected == "" {
		return "", false, errors.New("the selected file path was empty")
	}
	return selected, true, nil
}

func (a *application) handleConvertBrowse(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Kind    string `json:"kind"`
		Current string `json:"current"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	busy := a.convertBusyLocked()
	a.mu.Unlock()
	if busy {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the conversion to finish, or stop it, first."})
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()
	var selected string
	var ok bool
	var err error
	switch input.Kind {
	case "folder":
		selected, ok, err = chooseFolder("Choose the folder for the converted disk. It needs room for the disk's data.")
	case "drivers":
		selected, ok, err = chooseFolder("Choose the drivers folder: for example the root of the virtio-win ISO, or a folder of the new server's storage and network drivers.")
	case "fixup":
		folder := ""
		if strings.TrimSpace(input.Current) != "" {
			folder = filepath.Dir(input.Current)
		}
		selected, ok, err = chooseFile("Choose the converted Windows disk to fix up", convertFixupDialogFilter, folder)
	default:
		folder := ""
		if strings.TrimSpace(input.Current) != "" {
			folder = filepath.Dir(input.Current)
		}
		selected, ok, err = chooseFile("Choose the virtual disk to convert", convertDialogFilter, folder)
	}
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": ok, "path": selected})
}

func (a *application) handleConvertInspect(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Path   string `json:"path"`
		Format string `json:"format"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	path := strings.TrimSpace(input.Path)
	if path == "" || !filepath.IsAbs(path) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose the disk image with Browse, or type its full path."})
		return
	}
	a.mu.Lock()
	busy := a.convertBusyLocked()
	a.mu.Unlock()
	if busy {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "Wait for the conversion to finish, or stop it, first."})
		return
	}
	var info vmDiskInfo
	var boot vmBootInfo
	if number, local := vmLocalDiskNumber(path); local {
		var err error
		if info, boot, err = inspectLocalDisk(number); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	} else {
		image, err := openDiskImage(path)
		if err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		info = image.Info()
		boot = vmDetectBoot(image, image.Size())
		image.Close()
	}
	format := input.Format
	if !vmDiskFormatKnown(format) {
		format = vmDiskFormatVHDX
	}
	suggested := filepath.Join(filepath.Dir(info.Path), vmConvertSuggestedName(info.Path, format))
	if _, local := vmLocalDiskNumber(path); local {
		host, _ := os.Hostname()
		suggested = vmConvertSuggestedName(host+"-"+strings.TrimPrefix(info.Path, `\\.\`)+".img", format)
	}
	a.mu.Lock()
	a.convert.source, a.convert.info = info.Path, &info
	if !a.convert.running && a.convert.status != "running" {
		a.convert.status, a.convert.message, a.convert.result = "idle", "", nil
	}
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "info": info, "boot": boot, "suggested": suggested})
}

func (a *application) handleConvertStart(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Format string `json:"format"`
		Verify bool   `json:"verify"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	source, target := strings.TrimSpace(input.Source), strings.TrimSpace(input.Target)
	switch {
	case source == "" || !filepath.IsAbs(source):
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose the disk image to convert."})
		return
	case target == "" || !filepath.IsAbs(target):
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type the full path of the converted disk, or choose its folder."})
		return
	case !vmDiskFormatKnown(input.Format):
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Choose an output format."})
		return
	}
	if info, err := os.Stat(filepath.Dir(target)); err != nil || !info.IsDir() {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The folder " + filepath.Dir(target) + " does not exist."})
		return
	}
	if err := vmCheckLocalTarget(source, target); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	image, err := openConvertSource(source, a.convertLog)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	info := image.Info()
	if !a.beginCriticalOperation() {
		image.Close()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "An update is being installed."})
		return
	}
	a.mu.Lock()
	refusal := ""
	switch {
	case a.state.Running:
		refusal = "A collection is running. Wait for it to finish first."
	case a.restoreBusyLocked():
		refusal = "A restore is running. Wait for it to finish first."
	case a.migrateBusyLocked():
		refusal = "A file migration step is running. Wait for it to finish first."
	case a.vmwareBusyLocked():
		refusal = a.vmwareBusyTextLocked() + " Wait for it to finish first."
	case a.convertBusyLocked():
		refusal = "A conversion is already running."
	}
	if refusal != "" {
		a.mu.Unlock()
		image.Close()
		a.endCriticalOperation()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": refusal})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.convert = convertRuntime{
		running: true, status: "running", source: info.Path, target: target, format: input.Format, info: &info,
		message: "Converting " + filepath.Base(info.Path) + " to " + vmDiskFormatTitle(input.Format) + ".",
		cancel:  cancel, serial: a.convert.serial + 1, startedAt: time.Now(), task: "convert",
	}
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})

	go func() {
		defer a.endCriticalOperation()
		defer image.Close()
		result, err := vmConvertImage(ctx, image, target, input.Format, vmConvertOptions{
			Verify: input.Verify,
			Progress: func(progress vmCopyProgress) {
				a.mu.Lock()
				a.convert.progress = progress
				a.mu.Unlock()
			},
		})
		cancel()
		a.mu.Lock()
		defer a.mu.Unlock()
		a.convert.running, a.convert.cancel, a.convert.endedAt = false, nil, time.Now()
		switch {
		case err == nil:
			a.convert.status, a.convert.result, a.convert.progress = "success", result, result.Progress
			a.convert.message = convertDoneMessage(result)
		case errors.Is(err, context.Canceled):
			a.convert.status = "canceled"
			a.convert.message = "Stopped. Convert again with the same output file to continue where it stopped."
		default:
			a.convert.status, a.convert.message = "failed", err.Error()
		}
	}()
}

func (a *application) handleConvertCancel(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if !decodeVMwareRequest(response, request, nil) {
		return
	}
	a.mu.Lock()
	cancel := a.convert.cancel
	if a.convert.running {
		a.convert.cancelRequested = true
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}
