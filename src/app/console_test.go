package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/des"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// fakeRFBServer plays an RFB 3.8 server on conn. It answers the handshake
// (security None, or VNC authentication with password), sends one update
// made by draw, then records every client message until the connection ends.
type fakeRFBServer struct {
	mu       sync.Mutex
	keys     []string
	pointers []string
	clips    []string
	encs     []int32
	updates  int
}

func (f *fakeRFBServer) serve(t *testing.T, conn io.ReadWriteCloser, password string, width, height int, draw func(w io.Writer)) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	io.WriteString(conn, "RFB 003.008\n")
	version := make([]byte, 12)
	if _, err := io.ReadFull(r, version); err != nil {
		return
	}
	if password == "" {
		conn.Write([]byte{1, 1})
		if b, _ := r.ReadByte(); b != 1 {
			t.Errorf("client chose security %d", b)
			return
		}
	} else {
		conn.Write([]byte{1, 2})
		if b, _ := r.ReadByte(); b != 2 {
			t.Errorf("client chose security %d", b)
			return
		}
		challenge := []byte("0123456789abcdef")
		conn.Write(challenge)
		answer := make([]byte, 16)
		io.ReadFull(r, answer)
		want, _ := rfbVNCAuth(password, challenge)
		if !bytes.Equal(answer, want) {
			conn.Write([]byte{0, 0, 0, 1, 0, 0, 0, 4})
			io.WriteString(conn, "nope")
			return
		}
	}
	conn.Write([]byte{0, 0, 0, 0})
	if b, _ := r.ReadByte(); b != 1 {
		t.Errorf("client did not ask for a shared session")
	}
	init := make([]byte, 24)
	binary.BigEndian.PutUint16(init[0:], uint16(width))
	binary.BigEndian.PutUint16(init[2:], uint16(height))
	name := "fake"
	binary.BigEndian.PutUint32(init[20:], uint32(len(name)))
	conn.Write(append(init, name...))
	sentUpdate := false
	for {
		kind, err := r.ReadByte()
		if err != nil {
			return
		}
		switch kind {
		case 0:
			r.Discard(19)
		case 2:
			r.Discard(1)
			var n [2]byte
			io.ReadFull(r, n[:])
			count := int(binary.BigEndian.Uint16(n[:]))
			f.mu.Lock()
			for i := 0; i < count; i++ {
				var e [4]byte
				io.ReadFull(r, e[:])
				f.encs = append(f.encs, int32(binary.BigEndian.Uint32(e[:])))
			}
			f.mu.Unlock()
		case 3:
			r.Discard(9)
			f.mu.Lock()
			f.updates++
			f.mu.Unlock()
			if !sentUpdate && draw != nil {
				sentUpdate = true
				var buffer bytes.Buffer
				draw(&buffer)
				conn.Write(buffer.Bytes())
			}
		case 4:
			var m [7]byte
			io.ReadFull(r, m[:])
			f.mu.Lock()
			f.keys = append(f.keys, fmt.Sprintf("%d:%x", m[0], binary.BigEndian.Uint32(m[3:])))
			f.mu.Unlock()
		case 5:
			var m [5]byte
			io.ReadFull(r, m[:])
			f.mu.Lock()
			f.pointers = append(f.pointers, fmt.Sprintf("%d,%d,%d", binary.BigEndian.Uint16(m[1:]), binary.BigEndian.Uint16(m[3:]), m[0]))
			f.mu.Unlock()
		case 6:
			var m [7]byte
			io.ReadFull(r, m[:])
			text := make([]byte, binary.BigEndian.Uint32(m[3:]))
			io.ReadFull(r, text)
			f.mu.Lock()
			f.clips = append(f.clips, string(text))
			f.mu.Unlock()
		default:
			t.Errorf("unexpected client message %d", kind)
			return
		}
	}
}

func rect(w io.Writer, x, y, width, height int, encoding int32) {
	var head [12]byte
	binary.BigEndian.PutUint16(head[0:], uint16(x))
	binary.BigEndian.PutUint16(head[2:], uint16(y))
	binary.BigEndian.PutUint16(head[4:], uint16(width))
	binary.BigEndian.PutUint16(head[6:], uint16(height))
	binary.BigEndian.PutUint32(head[8:], uint32(encoding))
	w.Write(head[:])
}

func bgrx(color uint32) []byte {
	return []byte{byte(color), byte(color >> 8), byte(color >> 16), 0}
}

// drawAllEncodings sends one update using every encoding the client reads,
// then the guest's clipboard.
func drawAllEncodings(w io.Writer) {
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	w.Write([]byte{0, 0, 0, 10})
	// TightPNG fill 2x2 at (24,0); the TPIXEL is the pixel without padding.
	rect(w, 24, 0, 2, 2, rfbEncTightPNG)
	w.Write([]byte{0x80, 0x56, 0x34, 0x12})
	// TightPNG PNG 2x1 at (24,4): red, blue; a two-byte compact length.
	pic := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	pic.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	pic.Set(1, 0, color.NRGBA{0, 0, 255, 255})
	var pngData bytes.Buffer
	png.Encode(&pngData, pic)
	rect(w, 24, 4, 2, 1, rfbEncTightPNG)
	w.Write(append([]byte{0xA0}, tightLength(pngData.Len())...))
	w.Write(pngData.Bytes())
	// TightPNG JPEG 8x8 at (16,16), one grey.
	grey := image.NewGray(image.Rect(0, 0, 8, 8))
	for i := range grey.Pix {
		grey.Pix[i] = 0x80
	}
	var jpegData bytes.Buffer
	jpeg.Encode(&jpegData, grey, &jpeg.Options{Quality: 95})
	rect(w, 16, 16, 8, 8, rfbEncTightPNG)
	w.Write(append([]byte{0x90}, tightLength(jpegData.Len())...))
	w.Write(jpegData.Bytes())
	// Raw 2x1 at (0,0): red, green.
	rect(w, 0, 0, 2, 1, rfbEncRaw)
	w.Write(append(bgrx(0xff0000), bgrx(0x00ff00)...))
	// RRE at (0,2) 4x2: blue background, one white 1x1 at (1,1).
	rect(w, 0, 2, 4, 2, rfbEncRRE)
	w.Write([]byte{0, 0, 0, 1})
	w.Write(bgrx(0x0000ff))
	w.Write(bgrx(0xffffff))
	w.Write([]byte{0, 1, 0, 1, 0, 1, 0, 1})
	// Hextile 4x4 at (8,0): background yellow, one coloured subrect.
	rect(w, 8, 0, 4, 4, rfbEncHextile)
	w.Write([]byte{2 | 8 | 16})
	w.Write(bgrx(0xffff00))
	w.Write([]byte{1})
	w.Write(bgrx(0x112233))
	w.Write([]byte{0x11, 0x11}) // at (1,1), 2x2
	// ZRLE 2x1 at (0,6): raw CPIXELs, then a second rectangle on the same stream.
	zw.Write([]byte{0, 0x56, 0x34, 0x12, 0x9a, 0x78, 0x56})
	zw.Flush()
	first := append([]byte(nil), zbuf.Bytes()...)
	zbuf.Reset()
	rect(w, 0, 6, 2, 1, rfbEncZRLE)
	binary.Write(w, binary.BigEndian, uint32(len(first)))
	w.Write(first)
	zw.Write([]byte{1, 0xcc, 0xbb, 0xaa})
	zw.Flush()
	second := append([]byte(nil), zbuf.Bytes()...)
	rect(w, 4, 6, 3, 2, rfbEncZRLE)
	binary.Write(w, binary.BigEndian, uint32(len(second)))
	w.Write(second)
	// CopyRect: (0,0) 2x1 copied to (20,20).
	rect(w, 20, 20, 2, 1, rfbEncCopyRect)
	w.Write([]byte{0, 0, 0, 0})
	// Cursor 2x1, hotspot (1,0): left pixel shown, right masked out.
	rect(w, 1, 0, 2, 1, rfbEncCursor)
	w.Write(append(bgrx(0x010203), bgrx(0x040506)...))
	w.Write([]byte{0x80})
	// Guest clipboard.
	w.Write([]byte{3, 0, 0, 0, 0, 0, 0, 5})
	io.WriteString(w, "hello")
}

// tightLength writes Tight's compact length.
func tightLength(n int) []byte {
	out := []byte{byte(n & 0x7f)}
	if n > 0x7f {
		out[0] |= 0x80
		out = append(out, byte(n>>7&0x7f))
		if n > 0x3fff {
			out[1] |= 0x80
			out = append(out, byte(n>>14))
		}
	}
	return out
}

func pixel(f *consoleFrame, x, y int) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pix[y*f.width+x]
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRFBClientDecodesEveryEncodingAndSendsInput(t *testing.T) {
	for _, password := range []string{"", "s3cret"} {
		client, server := net.Pipe()
		fake := &fakeRFBServer{}
		go fake.serve(t, server, password, 32, 24, drawAllEncodings)
		frame := newConsoleFrame()
		backend, err := newConsoleRFBBackend(client, password, frame, nil)
		if err != nil {
			t.Fatal(err)
		}
		var clip string
		var cursor []byte
		var mu sync.Mutex
		backend.client.onClip = func(text string) { mu.Lock(); clip = text; mu.Unlock() }
		backend.client.onCursor = func(w, h, hx, hy int, rgba []byte) { mu.Lock(); cursor = rgba; mu.Unlock() }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- backend.Run(ctx) }()
		waitFor(t, "the clipboard", func() bool { mu.Lock(); defer mu.Unlock(); return clip == "hello" })
		checks := map[[2]int]uint32{
			{0, 0}: 0xff0000, {1, 0}: 0x00ff00, {0, 2}: 0x0000ff, {1, 3}: 0xffffff, {3, 3}: 0x0000ff,
			{8, 0}: 0xffff00, {9, 1}: 0x112233, {10, 2}: 0x112233, {11, 3}: 0xffff00,
			{0, 6}: 0x123456, {1, 6}: 0x56789a, {4, 6}: 0xaabbcc, {6, 7}: 0xaabbcc,
			{20, 20}: 0xff0000, {21, 20}: 0x00ff00,
			{24, 0}: 0x123456, {25, 1}: 0x123456, {24, 4}: 0xff0000, {25, 4}: 0x0000ff,
		}
		for at, want := range checks {
			if got := pixel(frame, at[0], at[1]); got != want {
				t.Errorf("password %q: pixel %v is %06x, want %06x", password, at, got, want)
			}
		}
		if got := pixel(frame, 19, 19); got>>16 < 0x7c || got>>16 > 0x84 || got&0xff < 0x7c || got&0xff > 0x84 {
			t.Errorf("password %q: JPEG pixel is %06x, want about 808080", password, got)
		}
		mu.Lock()
		if len(cursor) != 8 || cursor[0] != 1 || cursor[2] != 3 || cursor[3] != 255 || cursor[7] != 0 {
			t.Errorf("cursor %v", cursor)
		}
		mu.Unlock()
		if err := backend.TypeText(ctx, "aB!\r\n", 0); err != nil {
			t.Fatal(err)
		}
		backend.Pointer(5, 6, 1)
		backend.Pointer(500, -3, 0)
		backend.Clipboard("x€y")
		backend.Special("ctrlaltdel")
		waitFor(t, "the keys", func() bool {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			return len(fake.keys) >= 26 && len(fake.clips) == 1
		})
		fake.mu.Lock()
		got := strings.Join(fake.keys, " ")
		want := "0:ffe1 0:ffe2 0:ffe3 0:ffe4 0:ffe9 0:ffea 0:ffeb 0:ffec 1:61 0:61 1:ffe1 1:42 0:42 0:ffe1 1:ffe1 1:21 0:21 0:ffe1 1:ff0d 0:ff0d 1:ffe3 1:ffe9 1:ffff 0:ffff 0:ffe9 0:ffe3"
		if got != want {
			t.Errorf("keys\n got %s\nwant %s", got, want)
		}
		if strings.Join(fake.pointers, " ") != "5,6,1 31,0,0" {
			t.Errorf("pointers %v", fake.pointers)
		}
		if fake.clips[0] != "x?y" {
			t.Errorf("clipboard %q", fake.clips[0])
		}
		if len(fake.encs) < 2 || fake.encs[0] != rfbEncTightPNG || fake.encs[1] != rfbEncZRLE {
			t.Errorf("encodings %v", fake.encs)
		}
		fake.mu.Unlock()
		cancel()
		backend.Close()
		<-done
	}
}

func TestRFBClientReportsARefusedPassword(t *testing.T) {
	client, server := net.Pipe()
	fake := &fakeRFBServer{}
	go fake.serve(t, server, "right", 8, 8, nil)
	_, err := newConsoleRFBBackend(client, "wrong", newConsoleFrame(), nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("a wrong password must be refused with the server's reason: %v", err)
	}
}

func TestVNCAuthMatchesTheReversedBitDESKey(t *testing.T) {
	challenge := []byte("ABCDEFGHIJKLMNOP")
	answer, err := rfbVNCAuth("pass", challenge)
	if err != nil {
		t.Fatal(err)
	}
	// "pass" with each byte's bits reversed, padded with zeros.
	block, _ := des.NewCipher([]byte{0x0e, 0x86, 0xce, 0xce, 0, 0, 0, 0})
	want := make([]byte, 16)
	block.Encrypt(want[:8], challenge[:8])
	block.Encrypt(want[8:], challenge[8:])
	if !bytes.Equal(answer, want) {
		t.Fatalf("answer %x, want %x", answer, want)
	}
}

// fakeWebSocketServer upgrades one connection and relays it to serve.
func fakeWebSocketServer(t *testing.T, check func(r *http.Request) error, serve func(conn io.ReadWriteCloser)) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := check(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		hijacker := w.(http.Hijacker)
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + consoleWSGUID))
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: binary\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
		rw.Flush()
		serve(&serverWS{conn: conn, r: rw.Reader})
	}))
}

// serverWS is the server end: unmasked frames out, masked frames in.
type serverWS struct {
	conn    net.Conn
	r       *bufio.Reader
	pending []byte
}

func (s *serverWS) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		var head [2]byte
		if _, err := io.ReadFull(s.r, head[:]); err != nil {
			return 0, err
		}
		if head[1]&0x80 == 0 {
			return 0, errors.New("client frame not masked")
		}
		length := int(head[1] & 0x7f)
		if length == 126 {
			var ext [2]byte
			io.ReadFull(s.r, ext[:])
			length = int(binary.BigEndian.Uint16(ext[:]))
		} else if length == 127 {
			var ext [8]byte
			io.ReadFull(s.r, ext[:])
			length = int(binary.BigEndian.Uint64(ext[:]))
		}
		var mask [4]byte
		io.ReadFull(s.r, mask[:])
		payload := make([]byte, length)
		if _, err := io.ReadFull(s.r, payload); err != nil {
			return 0, err
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		switch head[0] & 0x0f {
		case 8:
			return 0, io.EOF
		case 0xA: // the client's answer to our ping
			continue
		}
		s.pending = payload
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *serverWS) Write(p []byte) (int, error) {
	// Split into two frames, the second a ping in between, to test reassembly.
	frame := func(op byte, payload []byte) []byte {
		out := []byte{0x80 | op}
		switch {
		case len(payload) < 126:
			out = append(out, byte(len(payload)))
		case len(payload) <= 0xffff:
			out = append(out, 126, byte(len(payload)>>8), byte(len(payload)))
		default:
			var ext [8]byte
			binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
			out = append(append(out, 127), ext[:]...)
		}
		return append(out, payload...)
	}
	half := len(p) / 2
	data := append(frame(2, p[:half]), frame(9, []byte("ping"))...)
	data = append(data, frame(2, p[half:])...)
	_, err := s.conn.Write(data)
	return len(p), err
}

func (s *serverWS) Close() error { return s.conn.Close() }

func TestConsoleWebSocketCarriesAnRFBSession(t *testing.T) {
	fake := &fakeRFBServer{}
	server := fakeWebSocketServer(t, func(r *http.Request) error {
		if r.Header.Get("Authorization") != "PVEAPIToken=a@pve!t=secret" || r.URL.Query().Get("vncticket") != "T+1" {
			return errors.New("bad auth")
		}
		return nil
	}, func(conn io.ReadWriteCloser) { fake.serve(t, conn, "T+1", 16, 16, drawAllEncodings) })
	defer server.Close()
	u, _ := url.Parse(server.URL)
	u.Scheme = "wss"
	u.RawQuery = url.Values{"vncticket": {"T+1"}}.Encode()
	header := http.Header{"Authorization": {"PVEAPIToken=a@pve!t=secret"}}
	ws, err := consoleDialWebSocket(context.Background(), u, &tls.Config{InsecureSkipVerify: true}, header, []string{"binary"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	frame := newConsoleFrame()
	backend, err := newConsoleRFBBackend(ws, "T+1", frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go backend.Run(ctx)
	waitFor(t, "the screen", func() bool { return pixel(frame, 9, 1) == 0x112233 })
	backend.Close()

	u.RawQuery = ""
	if _, err := consoleDialWebSocket(context.Background(), u, &tls.Config{InsecureSkipVerify: true}, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a refused upgrade must report the status: %v", err)
	}
	if _, err := consoleDialWebSocket(context.Background(), u, &tls.Config{InsecureSkipVerify: true}, http.Header{"X": {"a\r\nb"}}, nil, nil); err == nil {
		t.Fatal("a header with a line break must be refused")
	}
}

func TestConsoleFrameSendsOnlyChangedTiles(t *testing.T) {
	frame := newConsoleFrame()
	frame.Resize(200, 100)
	id, viewer := frame.subscribe()
	defer frame.unsubscribe(id)
	var sent []uint32
	sentW, sentH := -1, -1
	tiles, resized, w, h := frame.takeTiles(viewer, &sent, &sentW, &sentH)
	// Neighbouring changed tiles travel as one strip per row.
	if !resized || w != 200 || h != 100 || len(tiles) != 2 {
		t.Fatalf("first frame: resized %v %dx%d %d tiles", resized, w, h, len(tiles))
	}
	if tiles[0].W != 200 || tiles[0].H != 64 || tiles[1].Y != 64 || tiles[1].H != 36 {
		t.Fatalf("strips %+v %+v", tiles[0], tiles[1])
	}
	if img, err := png.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(tiles[1].PNG))); err != nil || img.Bounds().Dx() != 200 || img.Bounds().Dy() != 36 {
		t.Fatalf("strip picture: %v", err)
	}
	frame.Fill(70, 10, 2, 2, 0xabcdef)
	frame.Fill(0, 0, 1, 1, 0) // unchanged pixel: no tile
	tiles, resized, _, _ = frame.takeTiles(viewer, &sent, &sentW, &sentH)
	if resized || len(tiles) != 1 || tiles[0].X != 64 || tiles[0].Y != 0 {
		t.Fatalf("changed tiles %+v", tiles)
	}
	if _, err := base64.StdEncoding.DecodeString(tiles[0].PNG); err != nil {
		t.Fatal(err)
	}
	tiles, _, _, _ = frame.takeTiles(viewer, &sent, &sentW, &sentH)
	if len(tiles) != 0 {
		t.Fatalf("nothing changed but %d tiles were sent", len(tiles))
	}
	full := make([]uint32, 200*100)
	full[150*1+199] = 0
	full[99*200+5] = 0x10
	frame.SetFull(200, 100, full)
	tiles, _, _, _ = frame.takeTiles(viewer, &sent, &sentW, &sentH)
	if len(tiles) != 2 { // the earlier fill reverted, and the new pixel
		t.Fatalf("SetFull tiles %d", len(tiles))
	}
	// Two changed tiles apart in one row are two strips; touching ones one.
	frame.Fill(0, 70, 1, 1, 0x111111)
	frame.Fill(130, 70, 1, 1, 0x111111)
	frame.Fill(64, 0, 1, 1, 0x222222)
	frame.Fill(128, 0, 1, 1, 0x222222)
	tiles, _, _, _ = frame.takeTiles(viewer, &sent, &sentW, &sentH)
	if len(tiles) != 3 || tiles[0].X != 64 || tiles[0].W != 128 || tiles[1].X != 0 || tiles[1].W != 64 || tiles[2].X != 128 {
		t.Fatalf("strips %+v", tiles)
	}
	frame.Copy(190, 90, 0, 0, 20, 20) // clipped to the screen, never a panic
	frame.SetBGRX(198, 98, 4, 4, make([]byte, 64))
}

func TestConsoleKeysymMapsToWindowsVirtualKeys(t *testing.T) {
	cases := map[uint32]int{'a': 0x41, 'Z': 0x5A, '7': 0x37, '!': '1', ':': 0xBA, ksReturn: 0x0D, ksF1 + 11: 0x7B, ksControlL: 0xA2, ksSuperL: 0x5B, 0xffb5: 0x65, ksDelete: 0x2E, 0x20ac: 0}
	for keysym, want := range cases {
		if got := consoleKeysymToVK(keysym); got != want {
			t.Errorf("keysym %x: vk %x, want %x", keysym, got, want)
		}
	}
	if ks, shift, ok := consoleRuneKey('€'); !ok || shift || ks != 0x010020ac {
		t.Errorf("euro: %x %v %v", ks, shift, ok)
	}
	if _, shift, _ := consoleRuneKey('"'); !shift {
		t.Error("a double quote needs Shift on a US keyboard")
	}
}

func TestConsoleRGB565AndFrameSize(t *testing.T) {
	pixels, err := consoleRGB565([]byte{0x00, 0xf8, 0xe0, 0x07, 0x1f, 0x00, 0xff, 0xff}, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0xff0000, 0x00ff00, 0x0000ff, 0xffffff}
	for i := range want {
		if pixels[i] != want[i] {
			t.Errorf("pixel %d %06x, want %06x", i, pixels[i], want[i])
		}
	}
	if _, err := consoleRGB565([]byte{1}, 4, 1); err == nil {
		t.Error("short data must be refused")
	}
	if w, h := consoleHVFrameSize(3840, 2160); w != 1920 || h != 1080 {
		t.Errorf("4K scales to %dx%d", w, h)
	}
	if w, h := consoleHVFrameSize(1024, 768); w != 1024 || h != 768 {
		t.Errorf("1024x768 became %dx%d", w, h)
	}
}

func TestEnhancedSessionFileHoldsNoPasswordAndSharesClipboardAndDrives(t *testing.T) {
	text, err := consoleEnhancedRDP("HV01.example.com", "{6F1D2D2E-3A3B-4C4D-8E8F-9A9B0C0D1E1F}")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"full address:s:HV01.example.com", "server port:i:2179", "pcb:s:6F1D2D2E-3A3B-4C4D-8E8F-9A9B0C0D1E1F;EnhancedMode=1", "negotiate security layer:i:0", "redirectclipboard:i:1", "drivestoredirect:s:*"} {
		if !strings.Contains(text, line+"\r\n") {
			t.Errorf("missing %q", line)
		}
	}
	if strings.Contains(strings.ToLower(text), "password") {
		t.Error("the .rdp file must hold no password")
	}
	if _, err := consoleEnhancedRDP("HV01", "not-a-guid"); err == nil {
		t.Error("a VM ID that is not a GUID must be refused")
	}
	if _, err := consoleEnhancedRDP("HV01\r\nalternate shell:s:cmd", "6F1D2D2E-3A3B-4C4D-8E8F-9A9B0C0D1E1F"); err == nil {
		t.Error("a host with a line break must be refused")
	}
}

// fakeHVLink answers like Connect-HyperVConsole.ps1.
type fakeHVLink struct {
	mu            sync.Mutex
	requests      []string
	state         int
	noMouse       bool
	enhancedState int
	// same answers same:true after the first picture, as the helper does
	// for an unchanged screen; delay slows every call.
	same   bool
	frames int
	delay  time.Duration
}

func (f *fakeHVLink) call(request map[string]any, answer any) error {
	f.mu.Lock()
	raw, _ := json.Marshal(request)
	f.requests = append(f.requests, string(raw))
	state, mouse, enhanced, delay := f.state, !f.noMouse, f.enhancedState, f.delay
	if request["op"] == "frame" {
		f.frames++
	}
	same := f.same && f.frames > 1
	f.mu.Unlock()
	time.Sleep(delay)
	var reply any
	switch request["op"] {
	case "info":
		reply = map[string]any{"ok": true, "name": "APP01", "state": state, "width": 800, "height": 600, "generation": 2, "mouse": mouse, "host": "HV01", "enhancedState": enhanced}
	case "frame":
		if same {
			reply = map[string]any{"ok": true, "same": true}
			break
		}
		w, h := request["w"].(int), request["h"].(int)
		data := make([]byte, w*h*2)
		data[0], data[1] = 0x00, 0xf8
		reply = map[string]any{"ok": true, "w": w, "h": h, "data": base64.StdEncoding.EncodeToString(data)}
	default:
		reply = map[string]any{"ok": true}
	}
	raw, _ = json.Marshal(reply)
	return json.Unmarshal(raw, answer)
}

func (f *fakeHVLink) close() error { return nil }

func TestHyperVConsoleShowsScreenshotsAndSendsKeyboardAndMouse(t *testing.T) {
	link := &fakeHVLink{state: 3}
	if _, _, err := newConsoleHyperVBackend(link, newConsoleFrame()); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("a VM that is off must be refused: %v", err)
	}
	link = &fakeHVLink{state: 2}
	frame := newConsoleFrame()
	backend, info, err := newConsoleHyperVBackend(link, frame)
	if err != nil {
		t.Fatal(err)
	}
	caps := consoleHyperVCaps(info, true)
	if !caps.FilesTo || !caps.Mouse || caps.Display != "snapshots" {
		t.Fatalf("caps %+v", caps)
	}
	if remote := consoleHyperVCaps(info, false); remote.FilesTo || remote.FilesWhy == "" {
		t.Fatalf("remote caps must explain files: %+v", remote)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go backend.Run(ctx)
	waitFor(t, "the screen", func() bool { w, _ := frame.Size(); return w == 800 && pixel(frame, 0, 0) == 0xff0000 })
	cancel()
	backend.Key('a', true)
	backend.Key(0x20ac, true) // no virtual key: nothing sent
	backend.Pointer(400, 300, 1)
	backend.Pointer(400, 300, 0)
	backend.TypeText(context.Background(), "Ab\r\nc€", 0)
	backend.Special("ctrlaltdel")
	link.mu.Lock()
	defer link.mu.Unlock()
	all := strings.Join(link.requests, "\n")
	for _, want := range []string{`{"down":true,"op":"key","vk":65}`, `"op":"mouse","x":400,"y":300`, `{"buttons":[{"button":1,"down":true}]`, `{"down":true,"op":"key","vk":160}`, `{"down":true,"op":"key","vk":66}`, `{"down":false,"op":"key","vk":66}`, `{"down":true,"op":"key","vk":13}`, `{"down":true,"op":"key","vk":67}`, `{"op":"cad"}`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing request %s in\n%s", want, all)
		}
	}
	if strings.Count(all, `"vk":13`) != 2 {
		t.Errorf("a pasted Windows line end must be one Enter:\n%s", all)
	}
	if strings.Contains(all, `"op":"type"`) {
		t.Errorf("Hyper-V typing must press keys, not call TypeText:\n%s", all)
	}
}

// keyLog records key events with the time the typing had waited before each.
type keyLog struct {
	mu     sync.Mutex
	events []string
	waited time.Duration
}

func (k *keyLog) Run(ctx context.Context) error { <-ctx.Done(); return nil }
func (k *keyLog) Key(keysym uint32, down bool) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.events = append(k.events, fmt.Sprintf("%v:%x@%d", map[bool]int{true: 1, false: 0}[down], keysym, k.waited/time.Millisecond))
	return nil
}
func (k *keyLog) Pointer(x, y int, buttons byte) error { return nil }
func (k *keyLog) Special(name string) error            { return nil }
func (k *keyLog) TypeText(ctx context.Context, text string, d time.Duration) error {
	return consoleTypeKeys(ctx, k, text, d)
}
func (k *keyLog) Close() error { return nil }

func TestTypingLetsGoOfModifiersAndHoldsEveryKey(t *testing.T) {
	log := &keyLog{}
	saved := consoleSleep
	consoleSleep = func(ctx context.Context, d time.Duration) error {
		log.mu.Lock()
		log.waited += d
		log.mu.Unlock()
		return ctx.Err()
	}
	defer func() { consoleSleep = saved }()
	if err := consoleTypeKeys(context.Background(), log, "hU1", 0); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(log.events, " ")
	// Every modifier is released first, then 250 ms pass before "h"
	// (Ctrl+H lost the first letter); each key is held 12 ms and 8 ms pass
	// between characters and around Shift.
	want := "0:ffe1@0 0:ffe2@0 0:ffe3@0 0:ffe4@0 0:ffe9@0 0:ffea@0 0:ffeb@0 0:ffec@0 " +
		"1:68@250 0:68@262 1:ffe1@270 1:55@278 0:55@290 0:ffe1@298 1:31@306 0:31@318"
	if got != want {
		t.Errorf("keys\n got %s\nwant %s", got, want)
	}
	if p := consoleTypePaceFor(60*time.Millisecond, consoleTypeMinGap); p.Hold != 40*time.Millisecond || p.Gap != 60*time.Millisecond {
		t.Errorf("careful pace %+v", p)
	}
	if p := consoleTypePaceFor(4*time.Millisecond, consoleHVMinGap); p.Hold != 12*time.Millisecond || p.Gap != 20*time.Millisecond {
		t.Errorf("fast Hyper-V pace %+v", p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := consoleTypeKeys(ctx, &keyLog{}, "abc", 0); err == nil {
		t.Error("a stopped typing must end with the context's error")
	}
}

func TestHyperVConsoleFollowsTheMouseAndEnhancedSessionAsTheVMStarts(t *testing.T) {
	link := &fakeHVLink{state: 2, noMouse: true, enhancedState: 6}
	backend, info, err := newConsoleHyperVBackend(link, newConsoleFrame())
	if err != nil {
		t.Fatal(err)
	}
	caps := consoleHyperVCaps(info, true)
	if caps.Mouse || caps.MouseWhy == "" || !strings.Contains(caps.EnhancedWhy, "not ready") {
		t.Fatalf("a VM still starting has no mouse and no enhanced session yet: %+v", caps)
	}
	backend.Pointer(10, 10, 1)
	link.mu.Lock()
	for _, r := range link.requests {
		if strings.Contains(r, `"op":"mouse"`) {
			t.Errorf("no mouse request may be sent before the mouse exists: %s", r)
		}
	}
	link.noMouse, link.enhancedState = false, 2
	link.mu.Unlock()
	m := newConsoleManager()
	s, err := m.start(consoleTarget{Platform: "hyperv", ID: "6F1D2D2E-3A3B-4C4D-8E8F-9A9B0C0D1E1F", Name: "APP01"}, "this Hyper-V host", caps, newConsoleFrame(), backend)
	if err != nil {
		t.Fatal(err)
	}
	defer m.closeAll()
	info, _ = backend.info()
	backend.noteInfo(info)
	if v := s.view(); !v.Caps.Mouse || v.Caps.MouseWhy != "" || v.Caps.EnhancedWhy != "" {
		t.Fatalf("the mouse and enhanced session must turn on once Windows has started: %+v", v.Caps)
	}
	backend.Pointer(10, 10, 1)
	link.mu.Lock()
	clicked := false
	for _, r := range link.requests {
		clicked = clicked || strings.Contains(r, `"op":"mouse"`) && strings.Contains(r, `"button":1`)
	}
	link.mu.Unlock()
	if !clicked {
		t.Error("a click must reach Hyper-V once the mouse exists")
	}
	off := false
	if why := consoleHVEnhancedWhy(consoleHVInfo{Host: "HV01", EnhancedHost: &off}); !strings.Contains(why, "turned off on HV01") || !strings.Contains(why, "does not change host settings") {
		t.Errorf("host policy off: %q", why)
	}
}

// fakeBackend records what the console window sends.
type fakeBackend struct {
	mu    sync.Mutex
	keys  []uint32
	typed string
	done  chan struct{}
}

func (f *fakeBackend) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
	case <-f.done:
		return errors.New("EOF")
	}
	return nil
}
func (f *fakeBackend) Key(k uint32, down bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if down {
		f.keys = append(f.keys, k)
	}
	return nil
}
func (f *fakeBackend) Pointer(x, y int, b byte) error { return nil }
func (f *fakeBackend) TypeText(ctx context.Context, text string, d time.Duration) error {
	f.mu.Lock()
	f.typed += text
	f.mu.Unlock()
	return nil
}
func (f *fakeBackend) Special(name string) error {
	return consoleSendCombo(f, consoleSpecialKeys[name])
}
func (f *fakeBackend) Close() error { return nil }

func TestConsoleManagerStreamsTheScreenAndTakesInput(t *testing.T) {
	m := newConsoleManager()
	frame := newConsoleFrame()
	frame.Resize(64, 64)
	backend := &fakeBackend{done: make(chan struct{})}
	s, err := m.start(consoleTarget{Platform: "vmware", ID: "vm-1", Name: "APP01"}, "ESXi host esx1", consoleCaps{Keyboard: true, TypeText: true, Mouse: true}, frame, backend)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", m.handleStream)
	mux.HandleFunc("/input", m.handleInput)
	mux.HandleFunc("/keys", m.handleKeys)
	mux.HandleFunc("/type", m.handleType)
	server := httptest.NewServer(mux)
	defer server.Close()
	post := func(path string, body any) map[string]any {
		raw, _ := json.Marshal(body)
		response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out map[string]any
		json.NewDecoder(response.Body).Decode(&out)
		return out
	}
	response, err := http.Get(server.URL + "/stream?id=" + s.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	events := map[string]int{}
	readEvent := func() string {
		name := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "event: ") {
				name = strings.TrimPrefix(line, "event: ")
			}
			if line == "" && name != "" {
				events[name]++
				return name
			}
		}
	}
	for events["size"] == 0 || events["tiles"] == 0 || events["state"] == 0 {
		readEvent()
	}
	if out := post("/input", map[string]any{"id": s.ID, "events": []map[string]any{{"t": "k", "s": 0x61, "d": true}, {"t": "p", "x": 3, "y": 4, "b": 1}}}); out["ok"] != true {
		t.Fatalf("input %v", out)
	}
	if out := post("/keys", map[string]any{"id": s.ID, "combo": "winr"}); out["ok"] != true {
		t.Fatalf("keys %v", out)
	}
	if out := post("/keys", map[string]any{"id": s.ID, "combo": "rm -rf"}); out["ok"] != false {
		t.Fatalf("an unknown combination must be refused: %v", out)
	}
	if out := post("/type", map[string]any{"id": s.ID, "text": "P@ss w0rd"}); out["ok"] != true {
		t.Fatalf("type %v", out)
	}
	if out := post("/type", map[string]any{"id": s.ID, "text": strings.Repeat("x", consoleTypeLimit+1)}); out["ok"] != false {
		t.Fatal("text over the limit must be refused")
	}
	if out := post("/type", map[string]any{"id": s.ID, "text": "x", "clipboard": true}); out["ok"] != false {
		t.Fatal("a backend without a clipboard must refuse clipboard text")
	}
	waitFor(t, "the typed text", func() bool { backend.mu.Lock(); defer backend.mu.Unlock(); return backend.typed == "P@ss w0rd" })
	backend.mu.Lock()
	if fmt.Sprint(backend.keys) != fmt.Sprint([]uint32{0x61, ksSuperL, 'r'}) {
		t.Errorf("keys %x", backend.keys)
	}
	backend.mu.Unlock()
	s.setClip("from the guest")
	for events["clip"] == 0 {
		readEvent()
	}
	frame.Fill(10, 10, 4, 4, 0x123456)
	before := events["tiles"]
	for events["tiles"] == before {
		readEvent()
	}
	close(backend.done)
	for {
		if readEvent() == "state" && s.view().Status == "failed" {
			break
		}
	}
	if !strings.Contains(s.view().Message, "connection ended") {
		t.Errorf("message %q", s.view().Message)
	}
	response.Body.Close()
	if out := post("/input", map[string]any{"id": s.ID, "events": []map[string]any{}}); out["ok"] != false {
		t.Fatal("a closed console must refuse input")
	}
	if !m.close(s.ID) || m.get(s.ID) != nil {
		t.Fatal("close must forget the session")
	}
}

func TestConsoleManagerLimitsOpenConsoles(t *testing.T) {
	m := newConsoleManager()
	for i := 0; i < consoleMaxSessions; i++ {
		if _, err := m.start(consoleTarget{Platform: "hyperv", ID: fmt.Sprint(i)}, "", consoleCaps{}, newConsoleFrame(), &fakeBackend{done: make(chan struct{})}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.start(consoleTarget{Platform: "hyperv", ID: "x"}, "", consoleCaps{}, newConsoleFrame(), &fakeBackend{done: make(chan struct{})}); err == nil {
		t.Fatal("a ninth console must be refused")
	}
	m.closeAll()
	if len(m.views()) != 0 {
		t.Fatal("closeAll must close every console")
	}
	if err := (consoleTarget{Platform: "xen", ID: "1"}).validate(); err == nil {
		t.Fatal("an unknown platform must be refused")
	}
}

// TestVMwareConsoleUsesTheWebMKSTicketAndPinnedThumbprint opens a console
// against vcsim with the ticket answered by a fake ESXi WebMKS endpoint.
func TestVMwareConsoleUsesTheWebMKSTicketAndPinnedThumbprint(t *testing.T) {
	model := simulator.VPX()
	model.Machine = 1
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	model.Service.TLS = new(tls.Config)
	server := model.Service.NewServer()
	defer server.Close()
	user := server.URL.User.Username()
	password, _ := server.URL.User.Password()
	options := vmConnectOptions{Server: server.URL.Host, User: user, Password: password, insecureForTest: true}
	ctx := context.Background()
	vsphere, err := openVSphereSession(ctx, options, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	finder := find.NewFinder(vsphere.client, true)
	datacenter, _ := finder.DefaultDatacenter(ctx)
	finder.SetDatacenter(datacenter)
	machines, _ := finder.VirtualMachineList(ctx, "*")
	vm := machines[0]
	name, _ := vm.ObjectName(ctx)
	vsphere.logout()

	fake := &fakeRFBServer{}
	var gotPath string
	esx := fakeWebSocketServer(t, func(r *http.Request) error { gotPath = r.URL.Path; return nil }, func(conn io.ReadWriteCloser) { fake.serve(t, conn, "", 16, 16, drawAllEncodings) })
	defer esx.Close()
	esxURL, _ := url.Parse(esx.URL)
	host, portText, _ := net.SplitHostPort(esxURL.Host)
	var port int
	fmt.Sscan(portText, &port)
	thumbprint := vmThumbprintSHA1(esx.Certificate())
	saved := consoleAcquireTicket
	defer func() { consoleAcquireTicket = saved }()
	consoleAcquireTicket = func(ctx context.Context, client soap.RoundTripper, request *types.AcquireTicket) (*types.VirtualMachineTicket, error) {
		if request.TicketType != "webmks" || request.This.Value != vm.Reference().Value {
			return nil, errors.New("wrong ticket request")
		}
		return &types.VirtualMachineTicket{Ticket: "abc123", Host: host, Port: int32(port), SslThumbprint: thumbprint}, nil
	}
	frame := newConsoleFrame()
	target := consoleTarget{Platform: "vmware", ID: vm.Reference().Value, Name: name}
	backend, caps, where, err := consoleOpenVMware(ctx, options, target, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ticket/abc123" || !strings.Contains(where, "ESXi host") || !caps.Keyboard {
		t.Fatalf("path %q where %q caps %+v", gotPath, where, caps)
	}
	runCtx, cancel := context.WithCancel(ctx)
	go backend.Run(runCtx)
	waitFor(t, "the VMware screen", func() bool { return pixel(frame, 0, 0) == 0xff0000 })
	cancel()
	backend.Close()

	// A ticket whose thumbprint does not match the host is refused.
	consoleAcquireTicket = func(ctx context.Context, client soap.RoundTripper, request *types.AcquireTicket) (*types.VirtualMachineTicket, error) {
		return &types.VirtualMachineTicket{Ticket: "abc123", Host: host, Port: int32(port), SslThumbprint: "00:11:22"}, nil
	}
	if _, _, _, err := consoleOpenVMware(ctx, options, target, newConsoleFrame(), nil); err == nil {
		t.Fatal("a host whose certificate does not match the ticket's thumbprint must be refused")
	}
	target.Name = "renamed"
	if _, _, _, err := consoleOpenVMware(ctx, options, target, newConsoleFrame(), nil); err == nil || !strings.Contains(err.Error(), "now called") {
		t.Fatalf("a renamed VM must be refused: %v", err)
	}
	if url := consoleVMRCURL("vc.example.com:443", "cst-VCT-52+x", "vm-42"); url != "vmrc://clone:cst-VCT-52+x@vc.example.com/?moid=vm-42" {
		t.Fatalf("vmrc URL %s", url)
	}
}

func TestProxmoxConsoleAsksForATicketAndUsesIt(t *testing.T) {
	fake := &fakeRFBServer{}
	var mu sync.Mutex
	var steps []string
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	mux.HandleFunc("/api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"vmid":101,"name":"APP01","node":"pve2","status":"running","type":"qemu"}]}`)
	})
	mux.HandleFunc("/api2/json/nodes/pve2/qemu/101/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		steps = append(steps, "vncproxy websocket="+r.Form.Get("websocket"))
		mu.Unlock()
		io.WriteString(w, `{"data":{"port":"5901","ticket":"PVEVNC:abc"}}`)
	})
	ws := fakeWebSocketServer(t, func(r *http.Request) error { return nil }, func(conn io.ReadWriteCloser) { fake.serve(t, conn, "PVEVNC:abc", 16, 16, drawAllEncodings) })
	defer ws.Close()
	mux.HandleFunc("/api2/json/nodes/pve2/qemu/101/vncwebsocket", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		steps = append(steps, "ws port="+r.URL.Query().Get("port")+" auth="+r.Header.Get("Authorization"))
		mu.Unlock()
		ws.Config.Handler.ServeHTTP(w, r)
	})
	u, _ := url.Parse(server.URL)
	connection := vmTargetConnection{Platform: "proxmox", Server: u.Host, User: "root@pam!console", Password: "secret", client: server.Client()}
	frame := newConsoleFrame()
	dial := func(ctx context.Context, address string) (net.Conn, error) { return net.Dial("tcp", address) }
	// The client in connection is for API calls; the WebSocket verifies with
	// vmTLSConfig, so trust the test certificate's thumbprint.
	connection.Thumbprint = soap.ThumbprintSHA256(server.Certificate())
	backend, caps, where, err := consoleOpenProxmox(context.Background(), connection, consoleTarget{Platform: "proxmox", ID: "101"}, frame, dial)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go backend.Run(ctx)
	waitFor(t, "the Proxmox screen", func() bool { return pixel(frame, 1, 0) == 0x00ff00 })
	backend.Close()
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(steps, "|") != "vncproxy websocket=1|ws port=5901 auth=PVEAPIToken=root@pam!console=secret" {
		t.Fatalf("steps %v", steps)
	}
	if where != "Proxmox VE node pve2" || !caps.FilesTo {
		t.Fatalf("where %q caps %+v", where, caps)
	}
	if _, _, _, err := consoleOpenProxmox(context.Background(), connection, consoleTarget{Platform: "proxmox", ID: "102"}, newConsoleFrame(), dial); err == nil {
		t.Fatal("an unknown VM must be refused")
	}
}

func TestConsoleFileRequestsNeverReplaceFiles(t *testing.T) {
	folder := t.TempDir()
	existing := folder + "/a.txt"
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := consoleOpenLocalTarget(existing); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("an existing local file must not be replaced: %v", err)
	}
	for _, bad := range []consoleFileRequest{
		{Direction: "sideways", Local: existing, Guest: "C:\\a"},
		{Direction: "to", Local: "relative.txt", Guest: "C:\\a"},
		{Direction: "to", Local: existing, Guest: "C:\\a\r\nb"},
		{Direction: "from", Local: existing, Guest: ""},
	} {
		if err := bad.validate(); err == nil {
			t.Errorf("request %+v must be refused", bad)
		}
	}
}

func TestHyperVConsoleSendsInputBesidePicturesAndKeepsOnlyTheNewestMove(t *testing.T) {
	pictures := &fakeHVLink{state: 2, same: true, delay: 30 * time.Millisecond}
	frame := newConsoleFrame()
	backend, _, err := newConsoleHyperVBackend(pictures, frame)
	if err != nil {
		t.Fatal(err)
	}
	input := &fakeHVLink{state: 2, delay: 40 * time.Millisecond}
	backend.useInputLink(input)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go backend.Run(ctx)
	waitFor(t, "the screen", func() bool { w, _ := frame.Size(); return w == 800 && pixel(frame, 0, 0) == 0xff0000 })
	// Moves arriving faster than Hyper-V takes them replace each other.
	for i := 0; i < 30; i++ {
		backend.Pointer(i*10, i*5, 0)
		time.Sleep(2 * time.Millisecond)
	}
	backend.Pointer(300, 150, 1) // a click goes in order, after no stale move
	backend.Key('a', true)
	waitFor(t, "the input", func() bool {
		input.mu.Lock()
		defer input.mu.Unlock()
		return strings.Contains(strings.Join(input.requests, "\n"), `"vk":65`)
	})
	input.mu.Lock()
	moves := 0
	for _, r := range input.requests {
		if strings.Contains(r, `"op":"mouse"`) {
			moves++
		}
	}
	last := input.requests[len(input.requests)-2]
	input.mu.Unlock()
	if moves > 8 {
		t.Errorf("%d pointer requests for 30 moves; waiting moves must replace each other", moves)
	}
	if !strings.Contains(last, `"button":1`) || !strings.Contains(last, `"x":300`) {
		t.Errorf("the click must be sent last at its own position: %s", last)
	}
	pictures.mu.Lock()
	all := strings.Join(pictures.requests, "\n")
	pictures.mu.Unlock()
	if strings.Contains(all, `"op":"mouse"`) || strings.Contains(all, `"op":"key"`) {
		t.Error("input must not travel on the picture link")
	}
	if !strings.Contains(pictures.requests[1], `"full":true`) || strings.Count(all, `"full":true`) != 1 {
		t.Errorf("only the first picture asks for a full answer:\n%s", all)
	}
	backend.Close()
	backend.Pointer(1, 1, 0) // after Close: never a panic
}
