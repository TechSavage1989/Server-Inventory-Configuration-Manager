package main

// VM console (rule 63): Hyper-V. Hyper-V's own Virtual Machine Connection
// is RDP to port 2179, which a browser page cannot speak, so the console
// window shows the VM through Hyper-V's WMI classes instead, the same way on
// this host and on a remote host: the screen from
// Msvm_VirtualSystemManagementService.GetVirtualSystemThumbnailImage at the
// guest's resolution, keys through Msvm_Keyboard (PressKey, ReleaseKey,
// TypeText, TypeCtrlAltDel) and the pointer through Msvm_SyntheticMouse. It
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
}

type consoleHVFrame struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Data  string `json:"data"`
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
	link     consoleHVLink
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
}

func newConsoleHyperVBackend(link consoleHVLink, frame *consoleFrame) (*consoleHyperVBackend, consoleHVInfo, error) {
	b := &consoleHyperVBackend{link: link, frame: frame, interval: 250 * time.Millisecond}
	info, err := b.info()
	if err != nil {
		link.close()
		return nil, info, err
	}
	if info.State != 2 {
		link.close()
		return nil, info, errors.New("the VM is not running, so it has no screen to show. Start it in Hyper-V first")
	}
	b.mouse = info.Mouse
	return b, info, nil
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
		}
		b.mu.Lock()
		width, height := consoleHVFrameSize(b.guestW, b.guestH)
		b.mu.Unlock()
		started := time.Now()
		var answer consoleHVFrame
		err := b.link.call(map[string]any{"op": "frame", "w": width, "h": height}, &answer)
		if err == nil && !answer.OK {
			err = errors.New(firstNonEmpty(answer.Error, "Hyper-V did not send the screen"))
		}
		if err == nil {
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
				}
			}
		}
		if err != nil {
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
		// Ask again sooner when the host answers fast, later when it is slow.
		wait := b.interval
		if took := time.Since(started); took > wait {
			wait = took
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
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

func (b *consoleHyperVBackend) simple(request map[string]any) error {
	var answer consoleHVAnswer
	if err := b.link.call(request, &answer); err != nil {
		return err
	}
	if !answer.OK {
		return errors.New(firstNonEmpty(answer.Error, "Hyper-V refused the request"))
	}
	return nil
}

// Pointer scales from the shown frame to the guest's screen and sends button
// changes one by one (1 left, 2 right, 3 middle in Msvm_SyntheticMouse).
func (b *consoleHyperVBackend) Pointer(x, y int, buttons byte) error {
	if !b.mouse {
		return nil
	}
	b.mu.Lock()
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
		request["wheel"] = map[bool]int{true: 1, false: -1}[buttons&8 != 0]
	}
	return b.simple(request)
}

// TypeText sends ASCII runs through Msvm_Keyboard.TypeText (which handles
// Shift itself) and line ends, tabs and other characters as keys.
func (b *consoleHyperVBackend) TypeText(ctx context.Context, text string, delay time.Duration) error {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var run strings.Builder
	flush := func() error {
		if run.Len() == 0 {
			return nil
		}
		chunk := run.String()
		run.Reset()
		for len(chunk) > 0 {
			part := chunk
			if len(part) > 256 {
				part = part[:256]
			}
			chunk = chunk[len(part):]
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := b.simple(map[string]any{"op": "type", "text": part}); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range text {
		if r >= 0x20 && r <= 0x7e {
			run.WriteRune(r)
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		var vk int
		switch r {
		case '\n', '\r':
			vk = 0x0D
		case '\t':
			vk = 0x09
		case '\b':
			vk = 0x08
		default:
			continue // Msvm_Keyboard types ASCII only
		}
		if err := b.simple(map[string]any{"op": "key", "vk": vk, "down": true}); err != nil {
			return err
		}
		if err := b.simple(map[string]any{"op": "key", "vk": vk, "down": false}); err != nil {
			return err
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return flush()
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
	return b.link.close()
}

// consoleHyperVCaps says what the WMI console offers on this VM.
func consoleHyperVCaps(info consoleHVInfo, local bool) consoleCaps {
	caps := consoleCaps{
		Display:     "snapshots",
		Keyboard:    true,
		Mouse:       info.Mouse,
		TypeText:    true,
		ClipFromWhy: "Hyper-V's basic console has no clipboard. Use Enhanced session for copy and paste both ways once Windows in the VM is running.",
		Native:      []string{"enhanced"},
		Notes: []string{
			"Shown through Hyper-V's own screen and keyboard interfaces (WMI), so it works before the VM has a network. The picture refreshes a few times a second.",
		},
		KeyboardNote: "Paste as typing sends plain text (ASCII) through Hyper-V's keyboard, as Virtual Machine Connection's Type clipboard text does.",
	}
	if !info.Mouse {
		caps.MouseWhy = "The VM has no synthetic mouse yet (Hyper-V integration services are not running in it, or it is generation 1 before Windows starts). Use the keyboard."
	}
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
		"negotiate security layer:i:1",
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
