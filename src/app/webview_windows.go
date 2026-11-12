//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
)

// The built-in window shows the same local interface in Microsoft Edge
// WebView2 when a private Edge/Chrome guest window cannot open, for example on
// company-managed PCs where policy turns off guest browsing. WebView2 is an
// embedded component with its own app-owned data folder: it never opens a
// personal browser profile and never changes browser policies. The WebView2
// client DLL is located through the installed runtime's registration and loaded
// normally; the in-memory loader is not built (it needs the
// native_webview2loader build tag).

const (
	webViewClassName      = "ServerInventoryConfigurationManagerWindow"
	webViewMinimumVersion = "100.0.0.0"
	webViewStartTimeout   = 45 * time.Second

	wvWMDestroy       = 0x0002
	wvWMMove          = 0x0003
	wvWMSize          = 0x0005
	wvWMActivate      = 0x0006
	wvWMGetMinMaxInfo = 0x0024
	wvWMMoving        = 0x0216
	wvWMDPIChanged    = 0x02E0

	wvClassStyle       = 0x0001 | 0x0002 // CS_VREDRAW | CS_HREDRAW
	wvOverlappedWindow = 0x00CF0000
	wvIDCArrow         = 32512
	wvSPIGetWorkArea   = 0x0030
	wvSWPNoZOrderMove  = 0x0004 | 0x0010 // SWP_NOZORDER | SWP_NOACTIVATE
	wvMinTrackWidth    = 600
	wvMinTrackHeight   = 420
)

var (
	wvRegisterClassExW     = user32.NewProc("RegisterClassExW")
	wvCreateWindowExW      = user32.NewProc("CreateWindowExW")
	wvDefWindowProcW       = user32.NewProc("DefWindowProcW")
	wvDestroyWindow        = user32.NewProc("DestroyWindow")
	wvGetMessageW          = user32.NewProc("GetMessageW")
	wvTranslateMessage     = user32.NewProc("TranslateMessage")
	wvDispatchMessageW     = user32.NewProc("DispatchMessageW")
	wvPostQuitMessage      = user32.NewProc("PostQuitMessage")
	wvSetWindowTextW       = user32.NewProc("SetWindowTextW")
	wvLoadCursorW          = user32.NewProc("LoadCursorW")
	wvSystemParametersInfo = user32.NewProc("SystemParametersInfoW")
	wvSetWindowPos         = user32.NewProc("SetWindowPos")
	wvGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")

	webViewHostMu  sync.Mutex
	webViewActive  *webViewHost
	webViewWndProc = syscall.NewCallback(webViewWindowProc)
)

type wvWndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type wvMsg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	PtX, PtY int32
	Private  uint32
}

type wvPoint struct{ X, Y int32 }

type wvMinMaxInfo struct {
	Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize wvPoint
}

type webViewHost struct {
	app      *application
	hwnd     uintptr
	chromium *edge.Chromium
	ready    bool // read and written only on the window's thread
	result   chan error
	done     chan struct{}
}

// webViewRuntimeUnavailable explains why the built-in window cannot be used,
// or returns "" when a suitable WebView2 Runtime is installed.
func webViewRuntimeUnavailable() string {
	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil || strings.TrimSpace(version) == "" {
		return "the Microsoft Edge WebView2 Runtime is not installed"
	}
	if cmp, err := webviewloader.CompareBrowserVersions(version, webViewMinimumVersion); err == nil && cmp < 0 {
		return "the installed Microsoft Edge WebView2 Runtime " + version + " is too old (version 100 or later is needed)"
	}
	return ""
}

// webViewTitle accepts a page title for the native window only when it still
// names the application, so window discovery and dialog owners keep working.
func webViewTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 200 || strings.ContainsAny(title, "\r\n\x00") || !strings.Contains(strings.ToLower(title), strings.ToLower(appName)) {
		return ""
	}
	return title
}

// startWebViewHost opens the interface in the built-in window and returns once
// the page is navigating, or with the reason the window is unavailable.
func startWebViewHost(a *application, address, dataDir, iconPath string) (*webViewHost, error) {
	if reason := webViewRuntimeUnavailable(); reason != "" {
		return nil, errors.New(reason)
	}
	h := &webViewHost{app: a, result: make(chan error, 1), done: make(chan struct{})}
	webViewHostMu.Lock()
	if webViewActive != nil {
		webViewHostMu.Unlock()
		return nil, errors.New("the built-in window is already open")
	}
	webViewActive = h
	webViewHostMu.Unlock()
	// The WebView2 component logs through the standard logger; keep that out
	// of the GUI process's invisible stderr.
	log.SetOutput(io.Discard)
	go h.run(address, dataDir, iconPath)
	select {
	case err := <-h.result:
		if err != nil {
			return nil, err
		}
		return h, nil
	case <-time.After(webViewStartTimeout):
		return nil, fmt.Errorf("the built-in window did not start within %d seconds", int(webViewStartTimeout/time.Second))
	}
}

func (h *webViewHost) run(address, dataDir, iconPath string) {
	// This thread owns the window and its message loop for the life of the
	// process; it is never unlocked.
	runtime.LockOSThread()
	if procSetThreadDPIContext.Find() == nil {
		if prior, _, _ := procSetThreadDPIContext.Call(^uintptr(3)); prior == 0 { // PER_MONITOR_AWARE_V2
			procSetThreadDPIContext.Call(^uintptr(2)) // PER_MONITOR_AWARE
		}
	}
	instance, _, _ := wvGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString(webViewClassName)
	title, _ := syscall.UTF16PtrFromString(appName)
	var bigIcon, smallIcon uintptr
	if iconPtr, err := syscall.UTF16PtrFromString(iconPath); err == nil && iconPath != "" {
		bigIcon, _, _ = procLoadImageW.Call(0, uintptr(unsafe.Pointer(iconPtr)), imageIcon, 64, 64, lrLoadFromFile)
		smallIcon, _, _ = procLoadImageW.Call(0, uintptr(unsafe.Pointer(iconPtr)), imageIcon, 32, 32, lrLoadFromFile)
	}
	cursor, _, _ := wvLoadCursorW.Call(0, wvIDCArrow)
	class := wvWndClassEx{Size: uint32(unsafe.Sizeof(wvWndClassEx{})), Style: wvClassStyle, WndProc: webViewWndProc,
		Instance: instance, Icon: bigIcon, Cursor: cursor, ClassName: className, IconSm: smallIcon}
	wvRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))

	x, y, width, height := defaultWebViewBounds()
	hwnd, _, createErr := wvCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wvOverlappedWindow, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, instance, 0)
	if hwnd == 0 {
		h.result <- fmt.Errorf("the built-in window could not be created: %v", createErr)
		close(h.done)
		return
	}
	h.hwnd = hwnd
	// Reopen where the user left the window; otherwise show the centred default.
	if saved, ok := loadWindowGeometry(h.app.windowGeometryPath()); !ok || !restoreWindowGeometry(hwnd, saved) {
		procShowWindow.Call(hwnd, swShowNormal)
	}

	chromium := edge.NewChromium()
	chromium.DataPath = dataDir
	chromium.SetErrorCallback(h.onError)
	chromium.MessageCallback = h.onMessage
	chromium.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)
	h.chromium = chromium
	if !chromium.Embed(hwnd) {
		h.result <- errors.New("the built-in window could not load Microsoft Edge WebView2")
		wvDestroyWindow.Call(hwnd)
		close(h.done)
		return
	}
	if settings, err := chromium.GetSettings(); err == nil {
		_ = settings.PutAreDevToolsEnabled(false)
		_ = settings.PutAreDefaultContextMenusEnabled(true) // keeps right-click Copy
		_ = settings.PutIsStatusBarEnabled(false)
		_ = settings.PutAreDefaultScriptDialogsEnabled(true)
		_ = settings.PutIsZoomControlEnabled(true)
	}
	h.ready = true
	chromium.Resize()
	chromium.Navigate(address)
	h.result <- nil

	var message wvMsg
	for {
		r, _, _ := wvGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		wvTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		wvDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
	close(h.done)
	h.app.handleInterfaceClosed()
}

// onError receives WebView2 component errors. The component exits the process
// as soon as this returns, so report the failure, shut the application down
// cleanly and park this window thread until the process ends.
func (h *webViewHost) onError(err error) {
	h.app.logUpdate("Built-in window error: %v", err)
	select {
	case h.result <- fmt.Errorf("Microsoft Edge WebView2 reported an error: %v", err):
	default:
		go h.app.handleInterfaceClosed()
	}
	select {}
}

func (h *webViewHost) onMessage(message string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
	var payload struct {
		Title string `json:"title"`
	}
	if len(message) > 2048 || json.Unmarshal([]byte(message), &payload) != nil {
		return
	}
	if title := webViewTitle(payload.Title); title != "" {
		if ptr, err := syscall.UTF16PtrFromString(title); err == nil {
			wvSetWindowTextW.Call(h.hwnd, uintptr(unsafe.Pointer(ptr)))
		}
	}
}

func (h *webViewHost) alive() bool {
	select {
	case <-h.done:
		return false
	default:
	}
	valid, _, _ := procIsWindowGeometry.Call(h.hwnd)
	return valid != 0
}

// visibleWindow returns the built-in window while it is open and visible.
func (h *webViewHost) visibleWindow() uintptr {
	if !h.alive() {
		return 0
	}
	if visible, _, _ := procIsWindowVisible.Call(h.hwnd); visible == 0 {
		return 0
	}
	return h.hwnd
}

// close asks the window to close and waits for its message loop to end.
func (h *webViewHost) close(timeout time.Duration) {
	if h.alive() {
		procPostMessageWUpdate.Call(h.hwnd, wmClose, 0, 0)
	}
	select {
	case <-h.done:
	case <-time.After(timeout):
	}
}

// defaultWebViewBounds centres a comfortable window in the primary work area,
// leaving a margin on small screens such as 800x600 and 1024x768.
func defaultWebViewBounds() (x, y, width, height int32) {
	work := windowRect{0, 0, 1024, 768}
	wvSystemParametersInfo.Call(wvSPIGetWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0)
	return centredBounds(work)
}

func centredBounds(work windowRect) (x, y, width, height int32) {
	availableWidth, availableHeight := work.Right-work.Left, work.Bottom-work.Top
	width, height = availableWidth*92/100, availableHeight*92/100
	if width > 1600 {
		width = 1600
	}
	if height > 1000 {
		height = 1000
	}
	return work.Left + (availableWidth-width)/2, work.Top + (availableHeight-height)/2, width, height
}

func webViewWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	webViewHostMu.Lock()
	h := webViewActive
	webViewHostMu.Unlock()
	if msg == wvWMGetMinMaxInfo && lParam != 0 {
		info := *(**wvMinMaxInfo)(unsafe.Pointer(&lParam)) // lParam is a MINMAXINFO*
		info.MinTrackSize = wvPoint{wvMinTrackWidth, wvMinTrackHeight}
		return 0
	}
	if h != nil && h.hwnd == hwnd {
		switch msg {
		case wvWMSize:
			if h.ready {
				h.chromium.Resize()
			}
		case wvWMMove, wvWMMoving:
			if h.ready {
				_ = h.chromium.NotifyParentWindowPositionChanged()
			}
		case wvWMActivate:
			if h.ready && wParam&0xFFFF != 0 {
				if controller := h.chromium.GetController(); controller != nil {
					_ = controller.MoveFocus(edge.COREWEBVIEW2_MOVE_FOCUS_REASON_PROGRAMMATIC)
				}
			}
		case wvWMDPIChanged:
			if lParam != 0 {
				r := *(**windowRect)(unsafe.Pointer(&lParam)) // lParam is the suggested RECT*
				wvSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), wvSWPNoZOrderMove)
			}
			return 0
		case wmClose:
			wvDestroyWindow.Call(hwnd)
			return 0
		case wvWMDestroy:
			wvPostQuitMessage.Call(0)
			return 0
		}
	}
	result, _, _ := wvDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return result
}

// handleInterfaceClosed ends the application when its built-in window closes,
// matching the browser window: a running collection is canceled and stopped.
func (a *application) handleInterfaceClosed() {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return
	}
	running := a.state.Running
	activeCommand := a.cmd
	if running {
		a.state.CancelRequested = true
	}
	a.mu.Unlock()
	if running && activeCommand != nil && activeCommand.Process != nil {
		terminateProcessTree(activeCommand.Process.Pid)
	}
	a.stopServer()
}

// builtInWindow returns the open built-in window host, if the interface is
// shown there instead of a browser window.
func (a *application) builtInWindow() *webViewHost {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.webView
}
