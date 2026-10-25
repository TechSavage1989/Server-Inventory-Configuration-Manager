//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

var (
	procGetWindowPlacement     = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement     = user32.NewProc("SetWindowPlacement")
	procIsWindowGeometry       = user32.NewProc("IsWindow")
	procMonitorFromRect        = user32.NewProc("MonitorFromRect")
	procGetMonitorInfoGeometry = user32.NewProc("GetMonitorInfoW")
	procSetThreadDPIContext    = user32.NewProc("SetThreadDpiAwarenessContext")
)

type windowPoint struct{ X, Y int32 }
type windowRect struct{ Left, Top, Right, Bottom int32 }
type nativeWindowPlacement struct {
	Length, Flags, ShowCmd   uint32
	MinPosition, MaxPosition windowPoint
	NormalPosition           windowRect
}
type windowMonitorInfo struct {
	Size          uint32
	Monitor, Work windowRect
	Flags         uint32
}

// Bounds are WINDOWPLACEMENT workspace coordinates. Use only placement APIs
// for these values; mixing them with screen-coordinate SetWindowPos causes drift.
type windowGeometry struct {
	Schema    int        `json:"schema_version"`
	Bounds    windowRect `json:"normal_bounds"`
	Maximized bool       `json:"maximized"`
}
type windowGeometryRuntime struct {
	mu               sync.Mutex
	started, stopped bool
	hwnd             uintptr
	pid              uint32
	stop, done       chan struct{}
	last             windowGeometry
	hasLast          bool
	restoreUntil     time.Time
}

func validWindowGeometry(g windowGeometry) bool {
	w, h := int64(g.Bounds.Right)-int64(g.Bounds.Left), int64(g.Bounds.Bottom)-int64(g.Bounds.Top)
	return g.Schema == 1 && w >= 200 && h >= 160 && w <= 32768 && h <= 32768 &&
		g.Bounds.Left >= -1000000 && g.Bounds.Top >= -1000000 && g.Bounds.Right <= 1000000 && g.Bounds.Bottom <= 1000000
}
func loadWindowGeometry(path string) (windowGeometry, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 16384 {
		return windowGeometry{}, false
	}
	data, err := os.ReadFile(path)
	var g windowGeometry
	if err != nil || json.Unmarshal(data, &g) != nil || !validWindowGeometry(g) {
		return windowGeometry{}, false
	}
	return g, true
}
func saveWindowGeometry(path string, g windowGeometry) error {
	if !validWindowGeometry(g) {
		return errors.New("invalid window geometry")
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err = os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	defer os.Remove(temp)
	return atomicReplaceFile(temp, path)
}

// Restrict DPI awareness to this locked thread. Do not change the process-wide
// DPI mode used by existing dialogs. Older Windows keeps its original context.
func withWindowDPIContext(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if procSetThreadDPIContext.Find() == nil {
		prior, _, _ := procSetThreadDPIContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
		if prior == 0 {
			prior, _, _ = procSetThreadDPIContext.Call(^uintptr(2))
		}
		if prior != 0 {
			defer procSetThreadDPIContext.Call(prior)
		}
	}
	fn()
}
func getNativeWindowPlacement(hwnd uintptr) (nativeWindowPlacement, bool) {
	p := nativeWindowPlacement{Length: uint32(unsafe.Sizeof(nativeWindowPlacement{}))}
	ok, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	return p, ok != 0
}
func captureWindowGeometry(hwnd uintptr) (windowGeometry, bool) {
	var g windowGeometry
	var valid bool
	withWindowDPIContext(func() {
		p, ok := getNativeWindowPlacement(hwnd)
		// GetWindowPlacement does not report the prior maximized flag when
		// minimized. Keep the last non-minimized state instead of overwriting it.
		if !ok || p.ShowCmd == 2 {
			return
		}
		g = windowGeometry{Schema: 1, Bounds: p.NormalPosition, Maximized: p.ShowCmd == 3}
		valid = validWindowGeometry(g)
	})
	return g, valid
}
func fitWindowBounds(bounds, work windowRect) windowRect {
	w, h := bounds.Right-bounds.Left, bounds.Bottom-bounds.Top
	if w > work.Right-work.Left {
		w = work.Right - work.Left
	}
	if h > work.Bottom-work.Top {
		h = work.Bottom - work.Top
	}
	x, y := bounds.Left, bounds.Top
	if x+w > work.Right {
		x = work.Right - w
	}
	if y+h > work.Bottom {
		y = work.Bottom - h
	}
	if x < work.Left {
		x = work.Left
	}
	if y < work.Top {
		y = work.Top
	}
	return windowRect{x, y, x + w, y + h}
}
func restoreWindowGeometry(hwnd uintptr, g windowGeometry) bool {
	if !validWindowGeometry(g) {
		return false
	}
	var restored bool
	withWindowDPIContext(func() {
		bounds := g.Bounds
		monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&bounds)), 2) // nearest
		info := windowMonitorInfo{Size: uint32(unsafe.Sizeof(windowMonitorInfo{}))}
		ok, _, _ := procGetMonitorInfoGeometry.Call(monitor, uintptr(unsafe.Pointer(&info)))
		if ok != 0 {
			// Express this monitor's usable screen area in placement workspace
			// coordinates, including a taskbar at the top or left.
			dx, dy := info.Work.Left-info.Monitor.Left, info.Work.Top-info.Monitor.Top
			work := windowRect{info.Work.Left - dx, info.Work.Top - dy, info.Work.Right - dx, info.Work.Bottom - dy}
			bounds = fitWindowBounds(bounds, work)
		}
		p := nativeWindowPlacement{Length: uint32(unsafe.Sizeof(nativeWindowPlacement{})), Flags: 4, ShowCmd: 1,
			MinPosition: windowPoint{-1, -1}, MaxPosition: windowPoint{-1, -1}, NormalPosition: bounds}
		if g.Maximized {
			p.ShowCmd = 3
		}
		ok, _, _ = procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&p)))
		restored = ok != 0
	})
	return restored
}
func (a *application) windowGeometryPath() string {
	return filepath.Join(a.update.settingsDir, "window.json")
}

func (a *application) startWindowPersistence(hwnd uintptr) {
	w := &a.windowGeometry
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.stopped {
		return
	}
	w.started, w.hwnd = true, hwnd
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&w.pid)))
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	if saved, ok := loadWindowGeometry(a.windowGeometryPath()); ok {
		w.last, w.hasLast = saved, true
		if restoreWindowGeometry(hwnd, saved) {
			// The placement is posted to Edge's thread. Do not persist its old
			// startup bounds while the request is still being applied.
			w.restoreUntil = time.Now().Add(750 * time.Millisecond)
		} else {
			a.logUpdate("Saved window placement could not be restored")
		}
	}
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.saveCurrentWindowGeometry()
			case <-w.stop:
				a.saveCurrentWindowGeometry()
				return
			}
		}
	}()
}
func (a *application) saveCurrentWindowGeometry() {
	w := &a.windowGeometry
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || time.Now().Before(w.restoreUntil) {
		return
	}
	var pid uint32
	valid, _, _ := procIsWindowGeometry.Call(w.hwnd)
	procGetWindowThreadProcessID.Call(w.hwnd, uintptr(unsafe.Pointer(&pid)))
	if valid == 0 || pid != w.pid {
		return
	}
	g, ok := captureWindowGeometry(w.hwnd)
	if !ok || (w.hasLast && g == w.last) {
		return
	}
	if err := saveWindowGeometry(a.windowGeometryPath(), g); err != nil {
		a.logUpdate("Window placement could not be saved: %v", err)
		return
	}
	w.last, w.hasLast = g, true
}
func (a *application) stopWindowPersistence() {
	w := &a.windowGeometry
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	if !w.started {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	done := w.done
	w.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		a.logUpdate("Final window placement save did not finish before shutdown")
	}
}
