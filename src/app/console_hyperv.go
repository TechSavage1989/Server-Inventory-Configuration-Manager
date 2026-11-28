package main

// VM console (rule 68): Hyper-V. Hyper-V's own Virtual Machine Connection
// is RDP to port 2179, which a browser page cannot speak, so the console
// window shows the VM through Hyper-V's WMI classes instead, the same way on
// this host and on a remote host: the screen from
// Msvm_VirtualSystemManagementService.GetVirtualSystemThumbnailImage at the
// guest's resolution, keys through Msvm_Keyboard (PressKey, ReleaseKey,
// TypeCtrlAltDel) and the pointer through Msvm_SyntheticMouse. It
// works before the guest has a network (first boot after a cutover) and
// changes no VM setting. Enhanced session (clipboard and drives through RDP)
// and Virtual Machine Connection open as separate windows when wanted.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// consoleHVLink is the connection to Connect-HyperVConsole.ps1: one JSON
// request, one JSON answer.
type consoleHVLink interface {
	call(request map[string]any, answer any) error
	close() error
}

type consoleHVInfo struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	Name       string `json:"name"`
	State      int    `json:"state"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Generation int    `json:"generation"`
	Mouse      bool   `json:"mouse"`
	Host       string `json:"host"`
	Enhanced   bool   `json:"enhanced"`
	// EnhancedState is Msvm_ComputerSystem.EnhancedSessionModeState: 2 the
	// VM is ready for an enhanced session, 3 the host does not allow it, 6
	// allowed but Windows in the VM is not ready (0 when not reported).
	EnhancedState int `json:"enhancedState"`
	// EnhancedHost is the host's Enhanced Session Mode Policy (nil unknown).
	EnhancedHost *bool `json:"enhancedHost"`
}

type consoleHVFrame struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	// Same is true when the picture has not changed since the last one sent.
	Same bool   `json:"same"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	Data string `json:"data"`
}

type consoleHVAnswer struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// consoleHVMaxWidth keeps a screenshot of a very large guest screen
// manageable over WinRM; the pointer is scaled back.
const (
	consoleHVMaxWidth  = 1920
	consoleHVMaxHeight = 1200
)

type consoleHyperVBackend struct {
	link consoleHVLink
	// input carries keys and the pointer through a second helper with its
	// own connection, so they never wait behind a picture (the same link
	// when the second helper could not start).
	input    consoleHVLink
	inputMu  sync.Mutex
	kick     chan struct{}
	moveMu   sync.Mutex
	move     map[string]any
	moveWake chan struct{}
	done     chan struct{}
	lastIn   time.Time
	frame    *consoleFrame
	mu       sync.Mutex
	guestW   int
	guestH   int
	frameW   int
	frameH   int
	buttons  byte
	mouse    bool
	interval time.Duration
	closed   bool
	onCaps   func(change func(*consoleCaps))
	enhanced string
}

func (b *consoleHyperVBackend) watchCaps(update func(change func(*consoleCaps))) {
	b.mu.Lock()
	b.onCaps = update
	b.mu.Unlock()
}

// noteInfo follows the mouse and enhanced session as the VM starts: a
// Hyper-V VM has no usable synthetic mouse until Windows in it has loaded
// the driver, so the console must not decide once at open (2026-11-28,
// owner: "i can't click things").
func (b *consoleHyperVBackend) noteInfo(info consoleHVInfo) {
	enhanced := consoleHVEnhancedWhy(info)
	b.mu.Lock()
	changed := b.mouse != info.Mouse || b.enhanced != enhanced
	b.mouse, b.enhanced = info.Mouse, enhanced
	update := b.onCaps
	b.mu.Unlock()
	if changed && update != nil {
		update(func(caps *consoleCaps) {
			caps.Mouse, caps.MouseWhy = info.Mouse, consoleHVMouseWhy(info)
			caps.EnhancedWhy = enhanced
		})
	}
}

func newConsoleHyperVBackend(link consoleHVLink, frame *consoleFrame) (*consoleHyperVBackend, consoleHVInfo, error) {
	b := &consoleHyperVBackend{link: link, input: link, frame: frame, interval: consoleHVBusyInterval, kick: make(chan struct{}, 1), moveWake: make(chan struct{}, 1), done: make(chan struct{})}
	info, err := b.info()
	if err != nil {
		link.close()
		return nil, info, err
	}
	if info.State != 2 {
		link.close()
		return nil, info, errors.New("the VM is not running, so it has no screen to show. Start it in Hyper-V first")
	}
	b.mouse, b.enhanced = info.Mouse, consoleHVEnhancedWhy(info)
	go b.sendMoves()
	return b, info, nil
}

// Frame pacing: a picture every consoleHVBusyInterval while the screen
// changes or input arrived in the last consoleHVBusyFor, otherwise every
// consoleHVIdleInterval. Input asks for a picture at once, no sooner than
// consoleHVMinFrameGap after the last one.
const (
	consoleHVBusyInterval = 100 * time.Millisecond
	consoleHVIdleInterval = 400 * time.Millisecond
	consoleHVBusyFor      = 3 * time.Second
	consoleHVMinFrameGap  = 40 * time.Millisecond
)

// useInputLink sends keys and the pointer through a second helper.
func (b *consoleHyperVBackend) useInputLink(link consoleHVLink) {
	b.inputMu.Lock()
	b.input = link
	b.inputMu.Unlock()
}

// sendInput sends one input request in order with the others and asks for
// a new picture, so the result of a key or click shows at once.
func (b *consoleHyperVBackend) sendInput(request map[string]any) error {
	b.inputMu.Lock()
	var answer consoleHVAnswer
	err := b.input.call(request, &answer)
	b.inputMu.Unlock()
	b.mu.Lock()
	b.lastIn = time.Now()
	b.mu.Unlock()
	select {
	case b.kick <- struct{}{}:
	default:
	}
	if err != nil {
		return err
	}
	if !answer.OK {
		return errors.New(firstNonEmpty(answer.Error, "Hyper-V refused the request"))
	}
	return nil
}

// sendMoves sends the newest pointer position whenever one is waiting:
// moves arriving while one is sent replace each other instead of queueing,
// which made the pointer trail far behind the mouse.
func (b *consoleHyperVBackend) sendMoves() {
	for {
		select {
		case <-b.done:
			return
		case <-b.moveWake:
		}
		b.moveMu.Lock()
		request := b.move
		b.move = nil
		b.moveMu.Unlock()
		if request != nil && !b.isClosed() {
			_ = b.sendInput(request)
		}
	}
}

func (b *consoleHyperVBackend) info() (consoleHVInfo, error) {
	var info consoleHVInfo
	if err := b.link.call(map[string]any{"op": "info"}, &info); err != nil {
		return info, err
	}
	if !info.OK {
		return info, errors.New(firstNonEmpty(info.Error, "Hyper-V did not answer"))
	}
	b.mu.Lock()
	b.guestW, b.guestH = info.Width, info.Height
	b.mu.Unlock()
	return info, nil
}

// consoleHVFrameSize fits the guest screen in the maximum, keeping its shape.
func consoleHVFrameSize(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 1024, 768
	}
	if width <= consoleHVMaxWidth && height <= consoleHVMaxHeight {
		return width, height
	}
	scale := float64(consoleHVMaxWidth) / float64(width)
	if s := float64(consoleHVMaxHeight) / float64(height); s < scale {
		scale = s
	}
	return int(float64(width) * scale), int(float64(height) * scale)
}

// consoleRGB565 turns Hyper-V's thumbnail bytes (little-endian RGB565) into
// 0xRRGGBB pixels.
func consoleRGB565(data []byte, width, height int) ([]uint32, error) {
	if len(data) < width*height*2 {
		return nil, fmt.Errorf("Hyper-V sent %d bytes for a %dx%d screen", len(data), width, height)
	}
	pixels := make([]uint32, width*height)
	for i := range pixels {
		v := uint32(data[i*2]) | uint32(data[i*2+1])<<8
		r, g, bl := (v>>11)&0x1f, (v>>5)&0x3f, v&0x1f
		pixels[i] = (r<<3|r>>2)<<16 | (g<<2|g>>4)<<8 | (bl<<3 | bl>>2)
	}
	return pixels, nil
}

func (b *consoleHyperVBackend) Run(ctx context.Context) error {
	lastInfo := time.Now()
	lastChange := time.Now()
	needFull := true
	failures := 0
	for ctx.Err() == nil {
		if time.Since(lastInfo) > 5*time.Second {
			lastInfo = time.Now()
			info, err := b.info()
			if err != nil {
				return err
			}
			if info.State != 2 {
				return errors.New("the VM is no longer running")
			}
			b.noteInfo(info)
		}
		b.mu.Lock()
		width, height := consoleHVFrameSize(b.guestW, b.guestH)
		b.mu.Unlock()
		started := time.Now()
		var answer consoleHVFrame
		request := map[string]any{"op": "frame", "w": width, "h": height}
		if needFull {
			request["full"] = true
		}
		err := b.link.call(request, &answer)
		if err == nil && !answer.OK {
			err = errors.New(firstNonEmpty(answer.Error, "Hyper-V did not send the screen"))
		}
		changed := false
		if err == nil && answer.Same {
			// Nothing changed since the last picture.
		} else if err == nil {
			changed = true
			var raw []byte
			raw, err = base64.StdEncoding.DecodeString(answer.Data)
			if err == nil {
				var pixels []uint32
				pixels, err = consoleRGB565(raw, answer.W, answer.H)
				if err == nil {
					b.mu.Lock()
					b.frameW, b.frameH = answer.W, answer.H
					b.mu.Unlock()
					b.frame.SetFull(answer.W, answer.H, pixels)
					needFull = false
				}
			}
		}
		if err != nil {
			needFull = true
			if b.isClosed() {
				return nil
			}
			failures++
			if failures >= 5 {
				return err
			}
		} else {
			failures = 0
		}
		if changed {
			lastChange = time.Now()
		}
		b.mu.Lock()
		lastIn := b.lastIn
		b.mu.Unlock()
		wait := consoleHVIdleInterval
		if time.Since(lastChange) < consoleHVBusyFor || time.Since(lastIn) < consoleHVBusyFor {
			wait = b.interval
		}
		// A slow host is asked no faster than it answers.
		if took := time.Since(started); took > wait {
			wait = took
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-timer.C:
		case <-b.kick:
			// Input arrived: show its result now, after a short gap so a
			// burst of keys is one picture.
			if gap := consoleHVMinFrameGap - time.Since(started); gap > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(gap):
				}
			}
		}
		timer.Stop()
	}
	return nil
}

func (b *consoleHyperVBackend) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func (b *consoleHyperVBackend) Key(keysym uint32, down bool) error {
	vk := consoleKeysymToVK(keysym)
	if vk == 0 {
		return nil
	}
	return b.simple(map[string]any{"op": "key", "vk": vk, "down": down})
}

// simple sends a key, typing or button request in order, after any pointer
// move still waiting.
func (b *consoleHyperVBackend) simple(request map[string]any) error {
	b.moveMu.Lock()
	move := b.move
	b.move = nil
	b.moveMu.Unlock()
	if move != nil {
		if err := b.sendInput(move); err != nil {
			return err
		}
	}
	return b.sendInput(request)
}

// Pointer scales from the shown frame to the guest's screen and sends button
// changes one by one (1 left, 2 right, 3 middle in Msvm_SyntheticMouse).
func (b *consoleHyperVBackend) Pointer(x, y int, buttons byte) error {
	b.mu.Lock()
	if !b.mouse {
		b.mu.Unlock()
		return nil
	}
	gx, gy := x, y
	if b.frameW > 0 && b.frameH > 0 && b.guestW > 0 && b.guestH > 0 {
		gx, gy = x*b.guestW/b.frameW, y*b.guestH/b.frameH
	}
	previous := b.buttons
	b.buttons = buttons
	b.mu.Unlock()
	request := map[string]any{"op": "mouse", "x": gx, "y": gy}
	changes := []map[string]any{}
	for _, button := range []struct {
		mask byte
		id   int
	}{{1, 1}, {4, 2}, {2, 3}} {
		if (previous^buttons)&button.mask != 0 {
			changes = append(changes, map[string]any{"button": button.id, "down": buttons&button.mask != 0})
		}
	}
	if len(changes) > 0 {
		request["buttons"] = changes
	}
	if buttons&(8|16) != 0 && previous&(8|16) == 0 {
		request["wheel"] = map[bool]int{true: 120, false: -120}[buttons&8 != 0] // one wheel notch
	}
	if len(changes) == 0 && request["wheel"] == nil {
		// A plain move: keep only the newest one waiting.
		b.moveMu.Lock()
		b.move = request
		b.moveMu.Unlock()
		select {
		case b.moveWake <- struct{}{}:
		default:
		}
		return nil
	}
	b.moveMu.Lock()
	b.move = nil // this request moves the pointer there too
	b.moveMu.Unlock()
	return b.sendInput(request)
}

// consoleHVMinGap is the least time between characters typed into Hyper-V:
// each key is a WMI call, and keys sent faster than a sign-in screen takes
// them are dropped.
const consoleHVMinGap = 20 * time.Millisecond

// consoleHVModifierVKs are released before typing: generic, left and right
// Shift, Ctrl and Alt, and both Windows keys.
var consoleHVModifierVKs = []int{0x10, 0x11, 0x12, 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0x5B, 0x5C}

// TypeText lets go of every modifier (the generic Shift, Ctrl and Alt too,
// which consoleTypeKeys does not name), then types every character as a held
// key press through Msvm_Keyboard.PressKey and ReleaseKey, the same calls the
// keyboard uses, with Shift held where a US keyboard needs it. It used
// Msvm_Keyboard.TypeText, one call per character, until 2026-11-28: on the
// owner's host (MATCHC-DC02, 3.3.3-test.1) neither Paste as typing nor
// Ctrl+V typed anything while keys typed by hand did.
func (b *consoleHyperVBackend) TypeText(ctx context.Context, text string, delay time.Duration) error {
	for _, vk := range consoleHVModifierVKs[:3] {
		_ = b.simple(map[string]any{"op": "key", "vk": vk, "down": false})
	}
	return consoleTypeKeysWith(ctx, b, text, delay, consoleHVMinGap)
}

func (b *consoleHyperVBackend) Special(name string) error {
	if name == "ctrlaltdel" {
		return b.simple(map[string]any{"op": "cad"})
	}
	return consoleSendCombo(b, consoleSpecialKeys[name])
}

func (b *consoleHyperVBackend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	close(b.done)
	b.inputMu.Lock()
	input := b.input
	b.inputMu.Unlock()
	if input != b.link {
		input.close()
	}
	return b.link.close()
}

// consoleHyperVCaps says what the WMI console offers on this VM.
func consoleHyperVCaps(info consoleHVInfo, local bool) consoleCaps {
	caps := consoleCaps{
		Display:     "snapshots",
		Keyboard:    true,
		Mouse:       info.Mouse,
		TypeText:    true,
		ClipFromWhy: "Hyper-V's basic console has no clipboard. Use Enhanced session for copy and paste both ways once Windows in the VM is running, read text from the screen, or (on the Hyper-V host) save the text to a file in the VM and read it here.",
		Native:      []string{"enhanced"},
		Notes: []string{
			"Shown through Hyper-V's own screen and keyboard interfaces (WMI), so it works before the VM has a network. The picture refreshes up to ten times a second, and at once after a key or click; a smooth, full-speed screen needs Enhanced session or Virtual Machine Connection.",
		},
		KeyboardNote: "Paste as typing presses each key through Hyper-V's keyboard, as if typed on a US keyboard, so it works on sign-in screens.",
	}
	caps.MouseWhy = consoleHVMouseWhy(info)
	caps.EnhancedWhy = consoleHVEnhancedWhy(info)
	if local {
		caps.FilesTo, caps.FilesFrom = true, true
		caps.FilesHow = "Through PowerShell Direct from this Hyper-V host (Windows 10 or Windows Server 2016 and later in the VM). Needs a user name and password of Windows in the VM. Nothing is installed in the VM."
		caps.Native = append(caps.Native, "vmconnect")
	} else {
		caps.FilesWhy = "Files are copied through PowerShell Direct, which runs only on the Hyper-V host itself. From this computer, open Enhanced session and use its shared drives."
		caps.Native = append(caps.Native, "vmconnect")
	}
	if info.Generation > 0 {
		caps.Notes = append(caps.Notes, fmt.Sprintf("Generation %d VM on %s.", info.Generation, firstNonEmpty(info.Host, "this host")))
	}
	return caps
}

// consoleHVMouseWhy says why the mouse is off ("" when it works).
func consoleHVMouseWhy(info consoleHVInfo) string {
	if info.Mouse {
		return ""
	}
	return "The VM has no mouse device Hyper-V can move yet. It turns on by itself once Windows in the VM has started; until then use the keyboard."
}

// consoleHVEnhancedWhy says why an enhanced session cannot open yet ("" when
// it can, or when the host did not say).
func consoleHVEnhancedWhy(info consoleHVInfo) string {
	if info.EnhancedHost != nil && !*info.EnhancedHost || info.EnhancedState == 3 {
		host := firstNonEmpty(info.Host, "the Hyper-V host")
		return "Enhanced session is turned off on " + host + ". Turn on Allow enhanced session mode in that host's Hyper-V Settings (Enhanced Session Mode Policy), or run Set-VMHost -EnableEnhancedSessionMode $true there. This application does not change host settings."
	}
	if info.EnhancedState == 6 {
		return "Windows in the VM is not ready for an enhanced session yet. It needs Windows 8.1 or Windows Server 2012 R2 or later, started to its sign-in screen, with the Remote Desktop Services service running. Use this window or Virtual Machine Connection meanwhile."
	}
	return ""
}

// consoleKeysymToVK maps X11 keysyms to Windows virtual-key codes for
// Msvm_Keyboard. Shifted symbols map to their key, since Shift is held.
func consoleKeysymToVK(keysym uint32) int {
	switch {
	case keysym >= 'a' && keysym <= 'z':
		return int(keysym - 'a' + 'A')
	case keysym >= 'A' && keysym <= 'Z', keysym >= '0' && keysym <= '9':
		return int(keysym)
	case keysym >= ksF1 && keysym <= ksF1+23:
		return 0x70 + int(keysym-ksF1)
	case keysym >= 0xffb0 && keysym <= 0xffb9:
		return 0x60 + int(keysym-0xffb0)
	}
	if vk, ok := consoleVKMap[keysym]; ok {
		return vk
	}
	return 0
}

var consoleVKMap = map[uint32]int{
	' ': 0x20, ksBackSpace: 0x08, ksTab: 0x09, ksReturn: 0x0D, 0xff8d: 0x0D, ksEscape: 0x1B,
	ksDelete: 0x2E, ksInsert: 0x2D, ksHome: 0x24, ksEnd: 0x23, ksPageUp: 0x21, ksPageDown: 0x22,
	ksLeft: 0x25, ksUp: 0x26, ksRight: 0x27, ksDown: 0x28, ksPrint: 0x2C, 0xff13: 0x13,
	ksShiftL: 0xA0, 0xffe2: 0xA1, ksControlL: 0xA2, 0xffe4: 0xA3, ksAltL: 0xA4, 0xffea: 0xA5,
	ksSuperL: 0x5B, 0xffec: 0x5C, ksMenu: 0x5D, 0xffe5: 0x14, 0xff7f: 0x90, 0xff14: 0x91,
	0xffaa: 0x6A, 0xffab: 0x6B, 0xffad: 0x6D, 0xffae: 0x6E, 0xffaf: 0x6F,
	';': 0xBA, ':': 0xBA, '=': 0xBB, '+': 0xBB, ',': 0xBC, '<': 0xBC, '-': 0xBD, '_': 0xBD,
	'.': 0xBE, '>': 0xBE, '/': 0xBF, '?': 0xBF, '`': 0xC0, '~': 0xC0, '[': 0xDB, '{': 0xDB,
	'\\': 0xDC, '|': 0xDC, ']': 0xDD, '}': 0xDD, '\'': 0xDE, '"': 0xDE,
	'!': '1', '@': '2', '#': '3', '$': '4', '%': '5', '^': '6', '&': '7', '*': '8', '(': '9', ')': '0',
}

// consoleEnhancedRDP writes the .rdp file that opens a Hyper-V VM's enhanced
// session (VMConnect's RDP on port 2179 with the VM's ID as the
// pre-connection blob) with the clipboard and this computer's drives shared.
// It holds no password; Windows asks for the Hyper-V host's account.
func consoleEnhancedRDP(host, vmID string) (string, error) {
	vmID = strings.Trim(strings.TrimSpace(vmID), "{}")
	if !consoleGUIDPattern(vmID) {
		return "", errors.New("the Hyper-V VM ID is not a GUID")
	}
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "\r\n:;\"") {
		return "", errors.New("the Hyper-V host name is not valid")
	}
	lines := []string{
		"full address:s:" + host,
		"server port:i:2179",
		"pcb:s:" + vmID + ";EnhancedMode=1",
		"negotiate security layer:i:0", // port 2179 does not answer RDP security negotiation
		"authentication level:i:2",
		"enablecredsspsupport:i:1",
		"prompt for credentials on client:i:1",
		"redirectclipboard:i:1",
		"drivestoredirect:s:*",
		"audiomode:i:0",
		"screen mode id:i:1",
		"smart sizing:i:1",
		"dynamic resolution:i:1",
		"autoreconnection enabled:i:1",
	}
	return strings.Join(lines, "\r\n") + "\r\n", nil
}

func consoleGUIDPattern(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}
