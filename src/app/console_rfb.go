package main

// VM console (rule 68): an RFB (VNC) client for the consoles VMware
// (WebMKS), Proxmox VE and Nutanix Prism serve. It decodes into the session's
// framebuffer, which the console window shows; nothing here writes to disk.
// Encodings: Raw, CopyRect, RRE, Hextile, ZRLE, with the Cursor and
// DesktopSize pseudo-encodings. Security: None or VNC authentication.

import (
	"bufio"
	"compress/zlib"
	"crypto/des"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	rfbEncRaw         = 0
	rfbEncCopyRect    = 1
	rfbEncRRE         = 2
	rfbEncHextile     = 5
	rfbEncZRLE        = 16
	rfbEncCursor      = -239
	rfbEncDesktopSize = -223
)

// rfbClient is one RFB connection. Reads run on the Run goroutine; the input
// methods may be called from any goroutine.
type rfbClient struct {
	conn     io.ReadWriteCloser
	r        *bufio.Reader
	writeMu  sync.Mutex
	frame    *consoleFrame
	name     string
	minor    int
	zrle     *rfbZlibStream
	onCursor func(width, height, hotX, hotY int, rgba []byte)
	onClip   func(text string)
	onBell   func()
}

// rfbHandshake negotiates the version and security, then reads ServerInit
// and asks for 32-bit little-endian pixels.
func rfbHandshake(conn io.ReadWriteCloser, password string, frame *consoleFrame) (*rfbClient, error) {
	c := &rfbClient{conn: conn, r: bufio.NewReaderSize(conn, 256<<10), frame: frame}
	version := make([]byte, 12)
	if _, err := io.ReadFull(c.r, version); err != nil {
		return nil, fmt.Errorf("the console did not start a VNC session: %w", err)
	}
	var major, minor int
	if _, err := fmt.Sscanf(string(version), "RFB %03d.%03d\n", &major, &minor); err != nil || major != 3 {
		return nil, fmt.Errorf("the console answered with an unknown protocol %q", strings.TrimSpace(string(version)))
	}
	switch {
	case minor >= 8:
		minor = 8
	case minor == 7:
	default:
		minor = 3
	}
	c.minor = minor
	if _, err := fmt.Fprintf(conn, "RFB 003.%03d\n", minor); err != nil {
		return nil, err
	}
	var security uint32
	if minor == 3 {
		value, err := c.u32()
		if err != nil {
			return nil, err
		}
		security = value
		if security == 0 {
			return nil, c.reason("the console refused the connection")
		}
	} else {
		count, err := c.u8()
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, c.reason("the console refused the connection")
		}
		types := make([]byte, count)
		if _, err := io.ReadFull(c.r, types); err != nil {
			return nil, err
		}
		for _, t := range types {
			if t == 1 && security == 0 {
				security = 1
			}
			if t == 2 && (password != "" || security == 0) {
				security = 2
			}
		}
		if security == 1 && password != "" {
			for _, t := range types {
				if t == 2 {
					security = 2
				}
			}
		}
		if security == 0 {
			return nil, fmt.Errorf("the console asks for a sign-in this application does not support (types %v)", types)
		}
		if _, err := conn.Write([]byte{byte(security)}); err != nil {
			return nil, err
		}
	}
	switch security {
	case 1:
	case 2:
		challenge := make([]byte, 16)
		if _, err := io.ReadFull(c.r, challenge); err != nil {
			return nil, err
		}
		answer, err := rfbVNCAuth(password, challenge)
		if err != nil {
			return nil, err
		}
		if _, err := conn.Write(answer); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("the console asks for a sign-in this application does not support (type %d)", security)
	}
	if security == 2 || minor == 8 {
		result, err := c.u32()
		if err != nil {
			return nil, err
		}
		if result != 0 {
			if minor == 8 {
				return nil, c.reason("the console refused the sign-in")
			}
			return nil, errors.New("the console refused the sign-in")
		}
	}
	if _, err := conn.Write([]byte{1}); err != nil { // shared session
		return nil, err
	}
	head := make([]byte, 24)
	if _, err := io.ReadFull(c.r, head); err != nil {
		return nil, err
	}
	width, height := int(binary.BigEndian.Uint16(head[0:])), int(binary.BigEndian.Uint16(head[2:]))
	nameLength := binary.BigEndian.Uint32(head[20:])
	if nameLength > 4096 {
		return nil, errors.New("the console sent an unexpected desktop name")
	}
	name := make([]byte, nameLength)
	if _, err := io.ReadFull(c.r, name); err != nil {
		return nil, err
	}
	c.name = string(name)
	if width <= 0 || height <= 0 || width > 8192 || height > 8192 {
		return nil, fmt.Errorf("the console reported a screen of %dx%d", width, height)
	}
	frame.Resize(width, height)
	// SetPixelFormat: 32 bpp, depth 24, little endian, true colour, shifts 16/8/0.
	format := []byte{0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
	encodings := []int32{rfbEncZRLE, rfbEncHextile, rfbEncCopyRect, rfbEncRRE, rfbEncRaw, rfbEncCursor, rfbEncDesktopSize}
	set := make([]byte, 4+4*len(encodings))
	set[0] = 2
	binary.BigEndian.PutUint16(set[2:], uint16(len(encodings)))
	for i, e := range encodings {
		binary.BigEndian.PutUint32(set[4+4*i:], uint32(e))
	}
	if err := c.write(append(format, set...)); err != nil {
		return nil, err
	}
	if err := c.requestUpdate(false); err != nil {
		return nil, err
	}
	return c, nil
}

// rfbVNCAuth encrypts the challenge with DES, the key being the password's
// first eight bytes with each byte's bits reversed (RFC 6143 7.2.2).
func rfbVNCAuth(password string, challenge []byte) ([]byte, error) {
	key := make([]byte, 8)
	copy(key, password)
	for i, b := range key {
		var r byte
		for bit := 0; bit < 8; bit++ {
			if b&(1<<uint(bit)) != 0 {
				r |= 1 << uint(7-bit)
			}
		}
		key[i] = r
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	answer := make([]byte, 16)
	block.Encrypt(answer[0:8], challenge[0:8])
	block.Encrypt(answer[8:16], challenge[8:16])
	return answer, nil
}

func (c *rfbClient) reason(prefix string) error {
	length, err := c.u32()
	if err != nil || length > 4096 {
		return errors.New(prefix)
	}
	text := make([]byte, length)
	if _, err := io.ReadFull(c.r, text); err != nil {
		return errors.New(prefix)
	}
	return fmt.Errorf("%s: %s", prefix, strings.TrimSpace(string(text)))
}

func (c *rfbClient) u8() (byte, error) { return c.r.ReadByte() }

func (c *rfbClient) u16() (int, error) {
	var b [2]byte
	if _, err := io.ReadFull(c.r, b[:]); err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(b[:])), nil
}

func (c *rfbClient) u32() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(c.r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func (c *rfbClient) write(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.conn.Write(data)
	return err
}

func (c *rfbClient) requestUpdate(incremental bool) error {
	width, height := c.frame.Size()
	message := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if incremental {
		message[1] = 1
	}
	binary.BigEndian.PutUint16(message[6:], uint16(width))
	binary.BigEndian.PutUint16(message[8:], uint16(height))
	return c.write(message)
}

// Key sends one X11 keysym press or release.
func (c *rfbClient) Key(keysym uint32, down bool) error {
	message := []byte{4, 0, 0, 0, 0, 0, 0, 0}
	if down {
		message[1] = 1
	}
	binary.BigEndian.PutUint32(message[4:], keysym)
	return c.write(message)
}

// Pointer sends the pointer position and button mask (bit 0 left, 1 middle,
// 2 right, 3 and 4 the wheel).
func (c *rfbClient) Pointer(x, y int, buttons byte) error {
	width, height := c.frame.Size()
	x, y = cnClamp(x, 0, width-1), cnClamp(y, 0, height-1)
	message := []byte{5, buttons, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(message[2:], uint16(x))
	binary.BigEndian.PutUint16(message[4:], uint16(y))
	return c.write(message)
}

// Clipboard sends text as the client's clipboard (Latin-1, as RFB defines).
// Few hypervisors pass it to the guest; typing is the reliable way.
func (c *rfbClient) Clipboard(text string) error {
	latin := make([]byte, 0, len(text))
	for _, r := range text {
		if r == '\r' {
			continue
		}
		if r > 255 {
			r = '?'
		}
		latin = append(latin, byte(r))
	}
	message := make([]byte, 8, 8+len(latin))
	message[0] = 6
	binary.BigEndian.PutUint32(message[4:], uint32(len(latin)))
	return c.write(append(message, latin...))
}

func (c *rfbClient) Close() error {
	if c.zrle != nil {
		c.zrle.close()
	}
	return c.conn.Close()
}

// Run reads server messages until the connection ends.
func (c *rfbClient) Run() error {
	for {
		kind, err := c.u8()
		if err != nil {
			return err
		}
		switch kind {
		case 0:
			if err := c.readUpdate(); err != nil {
				return err
			}
			if err := c.requestUpdate(true); err != nil {
				return err
			}
		case 1:
			head := make([]byte, 5)
			if _, err := io.ReadFull(c.r, head); err != nil {
				return err
			}
			count := int(binary.BigEndian.Uint16(head[3:]))
			if _, err := c.r.Discard(count * 6); err != nil {
				return err
			}
		case 2:
			if c.onBell != nil {
				c.onBell()
			}
		case 3:
			if _, err := c.r.Discard(3); err != nil {
				return err
			}
			length, err := c.u32()
			if err != nil {
				return err
			}
			if int32(length) < 0 || length > 10<<20 {
				return errors.New("the console sent clipboard text this application does not read")
			}
			text := make([]byte, length)
			if _, err := io.ReadFull(c.r, text); err != nil {
				return err
			}
			if c.onClip != nil {
				runes := make([]rune, len(text))
				for i, b := range text {
					runes[i] = rune(b)
				}
				c.onClip(string(runes))
			}
		default:
			return fmt.Errorf("the console sent message type %d, which this application does not read", kind)
		}
	}
}

func (c *rfbClient) readUpdate() error {
	if _, err := c.r.Discard(1); err != nil {
		return err
	}
	count, err := c.u16()
	if err != nil {
		return err
	}
	head := make([]byte, 12)
	for i := 0; i < count; i++ {
		if _, err := io.ReadFull(c.r, head); err != nil {
			return err
		}
		x, y := int(binary.BigEndian.Uint16(head[0:])), int(binary.BigEndian.Uint16(head[2:]))
		w, h := int(binary.BigEndian.Uint16(head[4:])), int(binary.BigEndian.Uint16(head[6:]))
		encoding := int32(binary.BigEndian.Uint32(head[8:]))
		if encoding == -224 { // LastRect
			return nil
		}
		if err := c.readRect(x, y, w, h, encoding); err != nil {
			return err
		}
	}
	return nil
}

func (c *rfbClient) readRect(x, y, w, h int, encoding int32) error {
	switch encoding {
	case rfbEncRaw:
		data := make([]byte, w*h*4)
		if _, err := io.ReadFull(c.r, data); err != nil {
			return err
		}
		c.frame.SetBGRX(x, y, w, h, data)
	case rfbEncCopyRect:
		sx, err := c.u16()
		if err != nil {
			return err
		}
		sy, err := c.u16()
		if err != nil {
			return err
		}
		c.frame.Copy(sx, sy, x, y, w, h)
	case rfbEncRRE:
		count, err := c.u32()
		if err != nil {
			return err
		}
		if count > 1<<20 {
			return errors.New("the console sent too many rectangles")
		}
		background, err := c.pixel()
		if err != nil {
			return err
		}
		c.frame.Fill(x, y, w, h, background)
		sub := make([]byte, 12)
		for i := uint32(0); i < count; i++ {
			if _, err := io.ReadFull(c.r, sub); err != nil {
				return err
			}
			color := rfbColor(sub[0:4])
			c.frame.Fill(x+int(binary.BigEndian.Uint16(sub[4:])), y+int(binary.BigEndian.Uint16(sub[6:])), int(binary.BigEndian.Uint16(sub[8:])), int(binary.BigEndian.Uint16(sub[10:])), color)
		}
	case rfbEncHextile:
		return c.readHextile(x, y, w, h)
	case rfbEncZRLE:
		return c.readZRLE(x, y, w, h)
	case rfbEncCursor:
		pixels := make([]byte, w*h*4)
		if _, err := io.ReadFull(c.r, pixels); err != nil {
			return err
		}
		stride := (w + 7) / 8
		mask := make([]byte, stride*h)
		if _, err := io.ReadFull(c.r, mask); err != nil {
			return err
		}
		if c.onCursor != nil && w > 0 && h > 0 && w <= 256 && h <= 256 {
			rgba := make([]byte, w*h*4)
			for row := 0; row < h; row++ {
				for col := 0; col < w; col++ {
					i := (row*w + col) * 4
					rgba[i], rgba[i+1], rgba[i+2] = pixels[i+2], pixels[i+1], pixels[i]
					if mask[row*stride+col/8]&(0x80>>uint(col%8)) != 0 {
						rgba[i+3] = 255
					}
				}
			}
			c.onCursor(w, h, x, y, rgba)
		}
	case rfbEncDesktopSize:
		if w <= 0 || h <= 0 || w > 8192 || h > 8192 {
			return fmt.Errorf("the console reported a screen of %dx%d", w, h)
		}
		c.frame.Resize(w, h)
	default:
		return fmt.Errorf("the console used encoding %d, which this application did not ask for", encoding)
	}
	return nil
}

func (c *rfbClient) pixel() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(c.r, b[:]); err != nil {
		return 0, err
	}
	return rfbColor(b[:]), nil
}

// rfbColor turns a little-endian BGRX pixel into 0xRRGGBB.
func rfbColor(b []byte) uint32 {
	return uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0])
}

func (c *rfbClient) readHextile(x, y, w, h int) error {
	var background, foreground uint32
	for ty := y; ty < y+h; ty += 16 {
		th := cnMin(16, y+h-ty)
		for tx := x; tx < x+w; tx += 16 {
			tw := cnMin(16, x+w-tx)
			sub, err := c.u8()
			if err != nil {
				return err
			}
			if sub&1 != 0 {
				data := make([]byte, tw*th*4)
				if _, err := io.ReadFull(c.r, data); err != nil {
					return err
				}
				c.frame.SetBGRX(tx, ty, tw, th, data)
				continue
			}
			if sub&2 != 0 {
				if background, err = c.pixel(); err != nil {
					return err
				}
			}
			c.frame.Fill(tx, ty, tw, th, background)
			if sub&4 != 0 {
				if foreground, err = c.pixel(); err != nil {
					return err
				}
			}
			if sub&8 == 0 {
				continue
			}
			count, err := c.u8()
			if err != nil {
				return err
			}
			for i := 0; i < int(count); i++ {
				color := foreground
				if sub&16 != 0 {
					if color, err = c.pixel(); err != nil {
						return err
					}
				}
				xy, err := c.u8()
				if err != nil {
					return err
				}
				wh, err := c.u8()
				if err != nil {
					return err
				}
				c.frame.Fill(tx+int(xy>>4), ty+int(xy&15), int(wh>>4)+1, int(wh&15)+1, color)
			}
		}
	}
	return nil
}

// rfbZlibStream is ZRLE's one zlib stream across all rectangles: each
// rectangle's bytes are fed in, and a goroutine inflates them into a pipe the
// decoder reads exactly as far as the rectangle needs.
type rfbZlibStream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []byte
	closed bool
	out    *io.PipeReader
}

func newRFBZlibStream() *rfbZlibStream {
	s := &rfbZlibStream{}
	s.cond = sync.NewCond(&s.mu)
	reader, writer := io.Pipe()
	s.out = reader
	go func() {
		zr, err := zlib.NewReader(s)
		if err != nil {
			writer.CloseWithError(err)
			return
		}
		_, err = io.Copy(writer, zr)
		if err == nil {
			err = io.EOF
		}
		writer.CloseWithError(err)
	}()
	return s
}

func (s *rfbZlibStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.queue) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.queue)
	s.queue = s.queue[n:]
	return n, nil
}

// ReadByte keeps flate from reading ahead of what it needs.
func (s *rfbZlibStream) ReadByte() (byte, error) {
	var b [1]byte
	if _, err := s.Read(b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func (s *rfbZlibStream) feed(data []byte) {
	s.mu.Lock()
	s.queue = append(s.queue, data...)
	s.mu.Unlock()
	s.cond.Broadcast()
}

func (s *rfbZlibStream) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cond.Broadcast()
	s.out.Close()
}

func (c *rfbClient) readZRLE(x, y, w, h int) error {
	length, err := c.u32()
	if err != nil {
		return err
	}
	if length > 64<<20 {
		return errors.New("the console sent a ZRLE rectangle larger than 64 MB")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(c.r, data); err != nil {
		return err
	}
	if c.zrle == nil {
		c.zrle = newRFBZlibStream()
	}
	c.zrle.feed(data)
	// Read the pipe directly: any buffering would read past this rectangle.
	return rfbDecodeZRLE(c.zrle.out, c.frame, x, y, w, h)
}

// rfbDecodeZRLE decodes the inflated tiles of one rectangle (RFC 6143 7.7.6)
// with 3-byte CPIXELs (b, g, r).
func rfbDecodeZRLE(z io.Reader, frame *consoleFrame, x, y, w, h int) error {
	readFull := func(n int) ([]byte, error) {
		buffer := make([]byte, n)
		_, err := io.ReadFull(z, buffer)
		return buffer, err
	}
	readByte := func() (byte, error) {
		b, err := readFull(1)
		if err != nil {
			return 0, err
		}
		return b[0], nil
	}
	cpixel := func(b []byte) uint32 { return uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0]) }
	for ty := y; ty < y+h; ty += 64 {
		th := cnMin(64, y+h-ty)
		for tx := x; tx < x+w; tx += 64 {
			tw := cnMin(64, x+w-tx)
			sub, err := readByte()
			if err != nil {
				return err
			}
			pixels := make([]uint32, tw*th)
			switch {
			case sub == 0:
				raw, err := readFull(tw * th * 3)
				if err != nil {
					return err
				}
				for i := range pixels {
					pixels[i] = cpixel(raw[i*3:])
				}
			case sub == 1:
				raw, err := readFull(3)
				if err != nil {
					return err
				}
				frame.Fill(tx, ty, tw, th, cpixel(raw))
				continue
			case sub >= 2 && sub <= 16:
				palette := make([]uint32, sub)
				raw, err := readFull(int(sub) * 3)
				if err != nil {
					return err
				}
				for i := range palette {
					palette[i] = cpixel(raw[i*3:])
				}
				bits := 4
				switch {
				case sub == 2:
					bits = 1
				case sub <= 4:
					bits = 2
				}
				rowBytes := (tw*bits + 7) / 8
				packed, err := readFull(rowBytes * th)
				if err != nil {
					return err
				}
				for row := 0; row < th; row++ {
					for col := 0; col < tw; col++ {
						bitIndex := col * bits
						b := packed[row*rowBytes+bitIndex/8]
						shift := 8 - bits - bitIndex%8
						index := int(b>>uint(shift)) & (1<<uint(bits) - 1)
						if index >= len(palette) {
							return errors.New("the console sent a ZRLE palette index out of range")
						}
						pixels[row*tw+col] = palette[index]
					}
				}
			case sub == 128 || sub >= 130:
				var palette []uint32
				if sub >= 130 {
					size := int(sub) - 128
					raw, err := readFull(size * 3)
					if err != nil {
						return err
					}
					palette = make([]uint32, size)
					for i := range palette {
						palette[i] = cpixel(raw[i*3:])
					}
				}
				for i := 0; i < len(pixels); {
					var color uint32
					run := 1
					if palette == nil {
						raw, err := readFull(3)
						if err != nil {
							return err
						}
						color = cpixel(raw)
						run, err = rfbRunLength(readByte)
						if err != nil {
							return err
						}
					} else {
						index, err := readByte()
						if err != nil {
							return err
						}
						if int(index&0x7f) >= len(palette) {
							return errors.New("the console sent a ZRLE palette index out of range")
						}
						color = palette[index&0x7f]
						if index&0x80 != 0 {
							run, err = rfbRunLength(readByte)
							if err != nil {
								return err
							}
						}
					}
					if i+run > len(pixels) {
						return errors.New("the console sent a ZRLE run past the tile")
					}
					for j := 0; j < run; j++ {
						pixels[i+j] = color
					}
					i += run
				}
			default:
				return fmt.Errorf("the console sent an unknown ZRLE tile type %d", sub)
			}
			frame.SetPixels(tx, ty, tw, th, pixels)
		}
	}
	return nil
}

func rfbRunLength(readByte func() (byte, error)) (int, error) {
	run := 1
	for {
		b, err := readByte()
		if err != nil {
			return 0, err
		}
		run += int(b)
		if b != 255 {
			return run, nil
		}
		if run > 1<<24 {
			return 0, errors.New("the console sent a ZRLE run that is too long")
		}
	}
}

func cnMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func cnClamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
