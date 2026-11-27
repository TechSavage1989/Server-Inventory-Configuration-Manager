package main

// VM console (rule 68, 2026-11-27, owner: "a console access option ... to
// console into the vmware, hyper-v, etc source and target machines directly
// from the app", with keyboard commands, clipboard that pastes into login
// screens and file transfer where possible). Each platform's own console
// stream (VNC for VMware WebMKS, Proxmox VE, Nutanix and HyperCore; the
// Hyper-V video and keyboard WMI classes for Hyper-V) is decoded here into one
// framebuffer and shown in the application's own console window, which works
// the same way for every platform. Nothing is installed in the guest.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// consoleFrame is a session's screen as 0xRRGGBB pixels. Each viewer has its
// own dirty rectangle so two windows on one console both stay current.
type consoleFrame struct {
	mu      sync.Mutex
	width   int
	height  int
	pix     []uint32
	viewers map[int]*consoleViewer
	nextID  int
}

type consoleViewer struct {
	dirty  image.Rectangle
	resize bool
	notify chan struct{}
}

func newConsoleFrame() *consoleFrame {
	return &consoleFrame{viewers: map[int]*consoleViewer{}}
}

func (f *consoleFrame) Size() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.width, f.height
}

func (f *consoleFrame) Resize(width, height int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if width == f.width && height == f.height && f.pix != nil {
		return
	}
	next := make([]uint32, width*height)
	for y := 0; y < cnMin(height, f.height); y++ {
		copy(next[y*width:y*width+cnMin(width, f.width)], f.pix[y*f.width:])
	}
	f.width, f.height, f.pix = width, height, next
	for _, v := range f.viewers {
		v.resize = true
		v.dirty = image.Rect(0, 0, width, height)
		v.signal()
	}
}

func (v *consoleViewer) signal() {
	select {
	case v.notify <- struct{}{}:
	default:
	}
}

// markLocked adds a changed rectangle for every viewer.
func (f *consoleFrame) markLocked(r image.Rectangle) {
	r = r.Intersect(image.Rect(0, 0, f.width, f.height))
	if r.Empty() {
		return
	}
	for _, v := range f.viewers {
		if v.dirty.Empty() {
			v.dirty = r
		} else {
			v.dirty = v.dirty.Union(r)
		}
		v.signal()
	}
}

func (f *consoleFrame) clipLocked(x, y, w, h int) (int, int, int, int, bool) {
	if x < 0 || y < 0 || w <= 0 || h <= 0 || x >= f.width || y >= f.height {
		return 0, 0, 0, 0, false
	}
	return x, y, cnMin(w, f.width-x), cnMin(h, f.height-y), true
}

// SetBGRX writes little-endian 32-bit pixels (the format the RFB client asks for).
func (f *consoleFrame) SetBGRX(x, y, w, h int, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cx, cy, cw, ch, ok := f.clipLocked(x, y, w, h)
	if !ok {
		return
	}
	for row := 0; row < ch; row++ {
		src := ((cy-y+row)*w + (cx - x)) * 4
		dst := (cy+row)*f.width + cx
		for col := 0; col < cw; col++ {
			i := src + col*4
			f.pix[dst+col] = uint32(data[i+2])<<16 | uint32(data[i+1])<<8 | uint32(data[i])
		}
	}
	f.markLocked(image.Rect(cx, cy, cx+cw, cy+ch))
}

func (f *consoleFrame) SetPixels(x, y, w, h int, pixels []uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cx, cy, cw, ch, ok := f.clipLocked(x, y, w, h)
	if !ok {
		return
	}
	for row := 0; row < ch; row++ {
		copy(f.pix[(cy+row)*f.width+cx:(cy+row)*f.width+cx+cw], pixels[(cy-y+row)*w+(cx-x):])
	}
	f.markLocked(image.Rect(cx, cy, cx+cw, cy+ch))
}

func (f *consoleFrame) Fill(x, y, w, h int, color uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cx, cy, cw, ch, ok := f.clipLocked(x, y, w, h)
	if !ok {
		return
	}
	for row := cy; row < cy+ch; row++ {
		line := f.pix[row*f.width+cx : row*f.width+cx+cw]
		for i := range line {
			line[i] = color
		}
	}
	f.markLocked(image.Rect(cx, cy, cx+cw, cy+ch))
}

func (f *consoleFrame) Copy(sx, sy, x, y, w, h int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, _, _, _, ok := f.clipLocked(sx, sy, w, h); !ok {
		return
	}
	cx, cy, cw, ch, ok := f.clipLocked(x, y, w, h)
	if !ok {
		return
	}
	cw, ch = cnMin(cw, f.width-sx), cnMin(ch, f.height-sy)
	buffer := make([]uint32, cw*ch)
	for row := 0; row < ch; row++ {
		copy(buffer[row*cw:(row+1)*cw], f.pix[(sy+row)*f.width+sx:])
	}
	for row := 0; row < ch; row++ {
		copy(f.pix[(cy+row)*f.width+cx:(cy+row)*f.width+cx+cw], buffer[row*cw:(row+1)*cw])
	}
	f.markLocked(image.Rect(cx, cy, cx+cw, cy+ch))
}

// SetFull replaces the whole screen (Hyper-V screenshots), resizing when the
// size changed and marking only the rows that differ.
func (f *consoleFrame) SetFull(width, height int, pixels []uint32) {
	if width <= 0 || height <= 0 || len(pixels) < width*height {
		return
	}
	f.Resize(width, height)
	f.mu.Lock()
	defer f.mu.Unlock()
	top, bottom := -1, -1
	for row := 0; row < height; row++ {
		src, dst := pixels[row*width:(row+1)*width], f.pix[row*width:(row+1)*width]
		for i := range src {
			if src[i] != dst[i] {
				if top < 0 {
					top = row
				}
				bottom = row
				break
			}
		}
	}
	copy(f.pix, pixels[:width*height])
	if top >= 0 {
		f.markLocked(image.Rect(0, top, width, bottom+1))
	}
}

func (f *consoleFrame) subscribe() (int, *consoleViewer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	v := &consoleViewer{notify: make(chan struct{}, 1), resize: true, dirty: image.Rect(0, 0, f.width, f.height)}
	f.viewers[f.nextID] = v
	v.signal()
	return f.nextID, v
}

func (f *consoleFrame) unsubscribe(id int) {
	f.mu.Lock()
	delete(f.viewers, id)
	f.mu.Unlock()
}

// consoleTile is one changed part of the screen, as a PNG.
type consoleTile struct {
	X   int    `json:"x"`
	Y   int    `json:"y"`
	W   int    `json:"w"`
	H   int    `json:"h"`
	PNG string `json:"png"`
}

const consoleTileSize = 64

// takeTiles returns the viewer's changed tiles that differ from what it was
// last sent (sent is updated), and whether the screen size changed.
func (f *consoleFrame) takeTiles(v *consoleViewer, sent *[]uint32, sentW, sentH *int) (tiles []consoleTile, resized bool, width, height int) {
	f.mu.Lock()
	width, height = f.width, f.height
	dirty := v.dirty
	resized = v.resize || *sentW != width || *sentH != height
	v.dirty, v.resize = image.Rectangle{}, false
	if resized {
		*sent = make([]uint32, width*height)
		for i := range *sent {
			(*sent)[i] = 0xff000000 // never equal to a pixel, so all is sent
		}
		*sentW, *sentH = width, height
		dirty = image.Rect(0, 0, width, height)
	}
	if dirty.Empty() || width == 0 {
		f.mu.Unlock()
		return nil, resized, width, height
	}
	type pending struct {
		x, y, w, h int
		pix        []uint32
	}
	var changed []pending
	x0, y0 := dirty.Min.X/consoleTileSize*consoleTileSize, dirty.Min.Y/consoleTileSize*consoleTileSize
	for ty := y0; ty < dirty.Max.Y; ty += consoleTileSize {
		th := cnMin(consoleTileSize, height-ty)
		for tx := x0; tx < dirty.Max.X; tx += consoleTileSize {
			tw := cnMin(consoleTileSize, width-tx)
			differs := false
			for row := ty; row < ty+th && !differs; row++ {
				a, b := f.pix[row*width+tx:row*width+tx+tw], (*sent)[row*width+tx:row*width+tx+tw]
				for i := range a {
					if a[i] != b[i] {
						differs = true
						break
					}
				}
			}
			if !differs {
				continue
			}
			pix := make([]uint32, tw*th)
			for row := 0; row < th; row++ {
				copy(pix[row*tw:(row+1)*tw], f.pix[(ty+row)*width+tx:])
				copy((*sent)[(ty+row)*width+tx:(ty+row)*width+tx+tw], pix[row*tw:(row+1)*tw])
			}
			changed = append(changed, pending{tx, ty, tw, th, pix})
		}
	}
	f.mu.Unlock()
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	for _, c := range changed {
		img := image.NewNRGBA(image.Rect(0, 0, c.w, c.h))
		for i, p := range c.pix {
			img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = byte(p>>16), byte(p>>8), byte(p), 255
		}
		var buffer bytes.Buffer
		if err := encoder.Encode(&buffer, img); err != nil {
			continue
		}
		tiles = append(tiles, consoleTile{X: c.x, Y: c.y, W: c.w, H: c.h, PNG: base64.StdEncoding.EncodeToString(buffer.Bytes())})
	}
	return tiles, resized, width, height
}

// consoleBackend is one platform's console connection.
type consoleBackend interface {
	// Run shows the console in the frame until ctx ends or the connection drops.
	Run(ctx context.Context) error
	Key(keysym uint32, down bool) error
	// Pointer takes frame coordinates and the RFB button mask.
	Pointer(x, y int, buttons byte) error
	// TypeText types text as keystrokes, which works on sign-in screens.
	TypeText(ctx context.Context, text string, delay time.Duration) error
	// Special sends a key combination the browser cannot pass on
	// (consoleSpecialKeys).
	Special(name string) error
	Close() error
}

// consoleClipboardSender is a backend that can also offer text as the
// console client's clipboard (RFB ClientCutText).
type consoleClipboardSender interface {
	Clipboard(text string) error
}

// consoleCaps says what works on this console and why, for the window's
// "What works here" panel. Every false has its reason.
type consoleCaps struct {
	Display      string   `json:"display"`
	Keyboard     bool     `json:"keyboard"`
	Mouse        bool     `json:"mouse"`
	MouseWhy     string   `json:"mouseWhy,omitempty"`
	TypeText     bool     `json:"typeText"`
	TypeWhy      string   `json:"typeWhy,omitempty"`
	ClipToGuest  bool     `json:"clipToGuest"`
	ClipFromWhy  string   `json:"clipFromWhy,omitempty"`
	ClipFrom     bool     `json:"clipFrom"`
	FilesTo      bool     `json:"filesTo"`
	FilesFrom    bool     `json:"filesFrom"`
	FilesWhy     string   `json:"filesWhy,omitempty"`
	FilesHow     string   `json:"filesHow,omitempty"`
	Native       []string `json:"native,omitempty"`
	Notes        []string `json:"notes,omitempty"`
	KeyboardNote string   `json:"keyboardNote,omitempty"`
}

// consoleTarget names a VM and the platform it runs on.
type consoleTarget struct {
	Platform string `json:"platform"` // vmware, hyperv, proxmox, nutanix, scale
	ID       string `json:"id"`
	Name     string `json:"name"`
	// Host is the Hyper-V host ("" for this computer) or the ESXi host shown.
	Host string `json:"host"`
	// Node is the Proxmox VE node when it differs from the connection's.
	Node string `json:"node,omitempty"`
}

var consolePlatforms = map[string]string{
	"vmware":  "VMware",
	"hyperv":  "Hyper-V",
	"proxmox": "Proxmox VE",
	"nutanix": "Nutanix AHV",
	"scale":   "Scale HyperCore",
}

func (t consoleTarget) validate() error {
	if _, ok := consolePlatforms[t.Platform]; !ok {
		return fmt.Errorf("consoles are not available for platform %q", t.Platform)
	}
	if strings.TrimSpace(t.ID) == "" || len(t.ID) > 200 || strings.ContainsAny(t.ID, "\r\n\x00") {
		return errors.New("choose the VM to open")
	}
	if len(t.Name) > 200 || len(t.Host) > 255 || len(t.Node) > 100 {
		return errors.New("the VM's name or host is too long")
	}
	return nil
}

// consoleSession is one open console.
type consoleSession struct {
	ID        string
	Target    consoleTarget
	Where     string
	Caps      consoleCaps
	frame     *consoleFrame
	backend   consoleBackend
	cancel    context.CancelFunc
	started   time.Time
	mu        sync.Mutex
	status    string
	message   string
	cursor    *consoleCursor
	cursorSer uint64
	clip      string
	clipSer   uint64
	typing    bool
	typeCtx   context.CancelFunc
	file      *consoleFileJob
	ended     bool
	changed   chan struct{}
}

type consoleCursor struct {
	PNG  string `json:"png"`
	HotX int    `json:"hotX"`
	HotY int    `json:"hotY"`
}

type consoleFileJob struct {
	Direction string    `json:"direction"`
	Local     string    `json:"local"`
	Guest     string    `json:"guest"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Bytes     int64     `json:"bytes"`
	Started   time.Time `json:"started"`
}

// consoleSessionView is a session as the page sees it (no secrets).
type consoleSessionView struct {
	ID       string          `json:"id"`
	Target   consoleTarget   `json:"target"`
	Platform string          `json:"platformName"`
	Where    string          `json:"where"`
	Caps     consoleCaps     `json:"caps"`
	Status   string          `json:"status"`
	Message  string          `json:"message"`
	Typing   bool            `json:"typing"`
	File     *consoleFileJob `json:"file,omitempty"`
	Started  time.Time       `json:"started"`
}

func (s *consoleSession) view() consoleSessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	var file *consoleFileJob
	if s.file != nil {
		copied := *s.file
		file = &copied
	}
	return consoleSessionView{ID: s.ID, Target: s.Target, Platform: consolePlatforms[s.Target.Platform], Where: s.Where, Caps: s.Caps, Status: s.status, Message: s.message, Typing: s.typing, File: file, Started: s.started}
}

func (s *consoleSession) setStatus(status, message string) {
	s.mu.Lock()
	s.status, s.message = status, message
	s.mu.Unlock()
	s.poke()
}

// poke wakes every stream of this session (the channel is closed and replaced).
func (s *consoleSession) poke() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *consoleSession) changes() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

func (s *consoleSession) setCursor(width, height, hotX, hotY int, rgba []byte) {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	copy(img.Pix, rgba)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		return
	}
	s.mu.Lock()
	s.cursor = &consoleCursor{PNG: base64.StdEncoding.EncodeToString(buffer.Bytes()), HotX: hotX, HotY: hotY}
	s.cursorSer++
	s.mu.Unlock()
	s.poke()
}

func (s *consoleSession) setClip(text string) {
	if len(text) > 1<<20 {
		text = text[:1<<20]
	}
	s.mu.Lock()
	s.clip = text
	s.clipSer++
	s.mu.Unlock()
	s.poke()
}

// consoleManager holds the open consoles of this application window.
type consoleManager struct {
	mu       sync.Mutex
	sessions map[string]*consoleSession
	// typeDelay is the pause between typed characters.
	typeDelay time.Duration
}

const consoleMaxSessions = 8

func newConsoleManager() *consoleManager {
	return &consoleManager{sessions: map[string]*consoleSession{}, typeDelay: 15 * time.Millisecond}
}

func consoleNewID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// start registers a session and runs its backend in the background.
func (m *consoleManager) start(target consoleTarget, where string, caps consoleCaps, frame *consoleFrame, backend consoleBackend) (*consoleSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	open := 0
	for _, s := range m.sessions {
		if !s.isEnded() {
			open++
		}
	}
	if open >= consoleMaxSessions {
		backend.Close()
		return nil, fmt.Errorf("%d consoles are already open; close one first", consoleMaxSessions)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &consoleSession{ID: consoleNewID(), Target: target, Where: where, Caps: caps, frame: frame, backend: backend, cancel: cancel, started: time.Now(), status: "connected", changed: make(chan struct{})}
	m.sessions[s.ID] = s
	go func() {
		err := backend.Run(ctx)
		message := "The console closed."
		status := "closed"
		if err != nil && ctx.Err() == nil {
			message, status = consoleFriendlyError(err), "failed"
		}
		backend.Close()
		s.mu.Lock()
		s.ended = true
		s.mu.Unlock()
		s.setStatus(status, message)
	}()
	return s, nil
}

func (s *consoleSession) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

func (m *consoleManager) get(id string) *consoleSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *consoleManager) close(id string) bool {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s == nil {
		return false
	}
	s.cancel()
	s.backend.Close()
	return true
}

func (m *consoleManager) closeAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.close(id)
	}
}

func (m *consoleManager) views() []consoleSessionView {
	m.mu.Lock()
	list := make([]*consoleSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].started.Before(list[j].started) })
	views := make([]consoleSessionView, 0, len(list))
	for _, s := range list {
		views = append(views, s.view())
	}
	return views
}

// busy reports a typing or file transfer still running, so closing the
// application can wait for it.
func (m *consoleManager) busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.mu.Lock()
		running := s.typing || (s.file != nil && s.file.Status == "running")
		s.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}

func consoleFriendlyError(err error) string {
	text := err.Error()
	if text == "EOF" || strings.Contains(text, "use of closed network connection") {
		return "The console connection ended. The VM may have been turned off or moved to another host; open the console again."
	}
	return "The console connection ended: " + text
}

// ---- Keyboard ----

// X11 keysyms the window and the special-key menu use.
const (
	ksBackSpace = 0xff08
	ksTab       = 0xff09
	ksReturn    = 0xff0d
	ksEscape    = 0xff1b
	ksDelete    = 0xffff
	ksHome      = 0xff50
	ksLeft      = 0xff51
	ksUp        = 0xff52
	ksRight     = 0xff53
	ksDown      = 0xff54
	ksPageUp    = 0xff55
	ksPageDown  = 0xff56
	ksEnd       = 0xff57
	ksInsert    = 0xff63
	ksPrint     = 0xff61
	ksF1        = 0xffbe
	ksShiftL    = 0xffe1
	ksControlL  = 0xffe3
	ksAltL      = 0xffe9
	ksSuperL    = 0xffeb
	ksMenu      = 0xff67
)

// consoleSpecialKeys are the combinations the toolbar sends: keys held in
// order and released in reverse.
var consoleSpecialKeys = map[string][]uint32{
	"ctrlaltdel":   {ksControlL, ksAltL, ksDelete},
	"ctrlaltend":   {ksControlL, ksAltL, ksEnd},
	"ctrlesc":      {ksControlL, ksEscape},
	"ctrlshiftesc": {ksControlL, ksShiftL, ksEscape},
	"alttab":       {ksAltL, ksTab},
	"altf4":        {ksAltL, ksF1 + 3},
	"win":          {ksSuperL},
	"winr":         {ksSuperL, 'r'},
	"wine":         {ksSuperL, 'e'},
	"winl":         {ksSuperL, 'l'},
	"winx":         {ksSuperL, 'x'},
	"print":        {ksPrint},
	"altprint":     {ksAltL, ksPrint},
	"ctrlaltf1":    {ksControlL, ksAltL, ksF1},
	"ctrlaltf2":    {ksControlL, ksAltL, ksF1 + 1},
	"ctrlaltf7":    {ksControlL, ksAltL, ksF1 + 6},
	"ctrlaltbs":    {ksControlL, ksAltL, ksBackSpace},
	"menu":         {ksMenu},
	"esc":          {ksEscape},
	"f8":           {ksF1 + 7},
	"f12":          {ksF1 + 11},
}

func consoleSendCombo(backend consoleBackend, keys []uint32) error {
	for _, k := range keys {
		if err := backend.Key(k, true); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i := len(keys) - 1; i >= 0; i-- {
		if err := backend.Key(keys[i], false); err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// consoleShifted lists the US-layout characters typed with Shift.
const consoleShifted = `~!@#$%^&*()_+{}|:"<>?`

// consoleRuneKey returns the keysym for a typed character and whether a US
// keyboard needs Shift for it. ok is false for characters no keysym covers.
func consoleRuneKey(r rune) (keysym uint32, shift bool, ok bool) {
	switch {
	case r == '\n':
		return ksReturn, false, true
	case r == '\t':
		return ksTab, false, true
	case r == '\b':
		return ksBackSpace, false, true
	case r >= 'A' && r <= 'Z':
		return uint32(r), true, true
	case r >= 0x20 && r <= 0x7e:
		return uint32(r), strings.ContainsRune(consoleShifted, r), true
	case r >= 0xa0 && r <= 0xff:
		return uint32(r), false, true
	case r > 0xff && r <= 0x10ffff:
		return 0x01000000 | uint32(r), false, true
	}
	return 0, false, false
}

const consoleTypeLimit = 20000

// consoleTypeKeys types text through Key, one character at a time, holding
// Shift where a US keyboard needs it. Carriage returns are dropped so a
// pasted Windows line ending is one Enter.
func consoleTypeKeys(ctx context.Context, backend consoleBackend, text string, delay time.Duration) error {
	if !utf8.ValidString(text) {
		return errors.New("the text is not valid UTF-8")
	}
	for _, r := range text {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r == '\r' {
			continue
		}
		keysym, shift, ok := consoleRuneKey(r)
		if !ok {
			continue
		}
		if shift {
			if err := backend.Key(ksShiftL, true); err != nil {
				return err
			}
		}
		if err := backend.Key(keysym, true); err != nil {
			return err
		}
		if err := backend.Key(keysym, false); err != nil {
			return err
		}
		if shift {
			if err := backend.Key(ksShiftL, false); err != nil {
				return err
			}
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil
}

// ---- HTTP ----

// consoleInput is what the window sends: key and pointer events in order.
type consoleInput struct {
	ID     string `json:"id"`
	Events []struct {
		T string `json:"t"` // k (key) or p (pointer)
		S uint32 `json:"s"`
		D bool   `json:"d"`
		X int    `json:"x"`
		Y int    `json:"y"`
		B byte   `json:"b"`
	} `json:"events"`
}

func (m *consoleManager) handleInput(w http.ResponseWriter, r *http.Request) {
	var input consoleInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		consoleJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "The console input was not understood."})
		return
	}
	s := m.get(input.ID)
	if s == nil || s.isEnded() {
		consoleJSON(w, http.StatusGone, map[string]any{"ok": false, "error": "This console is closed."})
		return
	}
	if len(input.Events) > 500 {
		input.Events = input.Events[:500]
	}
	for _, e := range input.Events {
		var err error
		switch e.T {
		case "k":
			err = s.backend.Key(e.S, e.D)
		case "p":
			if s.Caps.Mouse {
				err = s.backend.Pointer(e.X, e.Y, e.B)
			}
		}
		if err != nil {
			consoleJSON(w, http.StatusOK, map[string]any{"ok": false, "error": consoleFriendlyError(err)})
			return
		}
	}
	consoleJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleKeys sends one special key combination.
func (m *consoleManager) handleKeys(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID    string `json:"id"`
		Combo string `json:"combo"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		consoleJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "The key request was not understood."})
		return
	}
	s := m.get(input.ID)
	if s == nil || s.isEnded() {
		consoleJSON(w, http.StatusGone, map[string]any{"ok": false, "error": "This console is closed."})
		return
	}
	if _, ok := consoleSpecialKeys[input.Combo]; !ok {
		consoleJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "That key combination is not offered."})
		return
	}
	if err := s.backend.Special(input.Combo); err != nil {
		consoleJSON(w, http.StatusOK, map[string]any{"ok": false, "error": consoleFriendlyError(err)})
		return
	}
	consoleJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleType types text into the VM in the background; a second request
// with stop:true ends a typing that is still running.
func (m *consoleManager) handleType(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		Stop      bool   `json:"stop"`
		Clipboard bool   `json:"clipboard"`
		DelayMS   int    `json:"delayMs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&input); err != nil {
		consoleJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "The text was not understood."})
		return
	}
	s := m.get(input.ID)
	if s == nil || s.isEnded() {
		consoleJSON(w, http.StatusGone, map[string]any{"ok": false, "error": "This console is closed."})
		return
	}
	if input.Stop {
		s.mu.Lock()
		if s.typeCtx != nil {
			s.typeCtx()
		}
		s.mu.Unlock()
		consoleJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if input.Clipboard {
		sender, ok := s.backend.(consoleClipboardSender)
		if !ok || !s.Caps.ClipToGuest {
			consoleJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "This console cannot pass a clipboard to the VM; use Paste as typing."})
			return
		}
		if err := sender.Clipboard(input.Text); err != nil {
			consoleJSON(w, http.StatusOK, map[string]any{"ok": false, "error": consoleFriendlyError(err)})
			return
		}
		consoleJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if !s.Caps.TypeText {
		consoleJSON(w, http.StatusOK, map[string]any{"ok": false, "error": s.Caps.TypeWhy})
		return
	}
	if input.Text == "" {
		consoleJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Type or paste the text to send."})
		return
	}
	if utf8.RuneCountInString(input.Text) > consoleTypeLimit {
		consoleJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("Paste as typing sends at most %d characters at a time.", consoleTypeLimit)})
		return
	}
	delay := m.typeDelay
	if input.DelayMS > 0 {
		delay = time.Duration(cnClamp(input.DelayMS, 1, 500)) * time.Millisecond
	}
	s.mu.Lock()
	if s.typing {
		s.mu.Unlock()
		consoleJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "The console is still typing the last text. Wait or stop it."})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.typing, s.typeCtx = true, cancel
	s.mu.Unlock()
	s.poke()
	go func() {
		err := s.backend.TypeText(ctx, input.Text, delay)
		cancel()
		s.mu.Lock()
		s.typing, s.typeCtx = false, nil
		if err != nil && !errors.Is(err, context.Canceled) {
			s.message = "Typing stopped: " + err.Error()
		}
		s.mu.Unlock()
		s.poke()
	}()
	consoleJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleStream sends the screen to the window as server-sent events: size,
// tiles (changed PNG tiles), cursor, clip (the guest's clipboard), state.
func (m *consoleManager) handleStream(w http.ResponseWriter, r *http.Request) {
	s := m.get(r.URL.Query().Get("id"))
	if s == nil {
		http.Error(w, "This console is closed.", http.StatusGone)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is not available.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	viewerID, viewer := s.frame.subscribe()
	defer s.frame.unsubscribe(viewerID)
	send := func(event string, value any) bool {
		raw, err := json.Marshal(value)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	var sent []uint32
	sentW, sentH := -1, -1
	var cursorSer, clipSer uint64
	lastState := ""
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		changes := s.changes()
		tiles, resized, width, height := s.frame.takeTiles(viewer, &sent, &sentW, &sentH)
		if resized && width > 0 {
			if !send("size", map[string]int{"w": width, "h": height}) {
				return
			}
		}
		for start := 0; start < len(tiles); start += 24 {
			end := cnMin(start+24, len(tiles))
			if !send("tiles", tiles[start:end]) {
				return
			}
		}
		s.mu.Lock()
		cursor, cs := s.cursor, s.cursorSer
		clip, cl := s.clip, s.clipSer
		s.mu.Unlock()
		if cs != cursorSer && cursor != nil {
			cursorSer = cs
			if !send("cursor", cursor) {
				return
			}
		}
		if cl != clipSer {
			clipSer = cl
			if !send("clip", map[string]string{"text": clip}) {
				return
			}
		}
		view := s.view()
		state, _ := json.Marshal(view)
		if string(state) != lastState {
			lastState = string(state)
			if !send("state", view) {
				return
			}
			if view.Status == "closed" || view.Status == "failed" {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-viewer.notify:
		case <-changes:
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
		// At most about 25 updates a second.
		time.Sleep(40 * time.Millisecond)
	}
}

func consoleJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
