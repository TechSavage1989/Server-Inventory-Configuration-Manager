package main

// VM console (rule 68): a minimal RFC 6455 WebSocket client, used to reach
// the VNC streams that VMware (WebMKS), Proxmox VE and Nutanix Prism serve
// over HTTPS. It sends binary frames, reads binary and text frames as one
// byte stream, answers pings and never follows redirects.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const consoleWSGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type consoleWebSocket struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
	pending []byte
	closed  atomic.Bool
	once    sync.Once
}

// consoleDialWebSocket opens wss://host/path. dial replaces the TCP dial in
// tests; tlsConfig verifies the server (vmTLSConfig or an exact pin).
func consoleDialWebSocket(ctx context.Context, target *url.URL, tlsConfig *tls.Config, header http.Header, protocols []string, dial func(ctx context.Context, address string) (net.Conn, error)) (*consoleWebSocket, error) {
	if target.Scheme != "wss" && target.Scheme != "ws" {
		return nil, errors.New("the console address is not a WebSocket address")
	}
	host := target.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		if target.Scheme == "wss" {
			host = net.JoinHostPort(strings.Trim(host, "[]"), "443")
		} else {
			host = net.JoinHostPort(strings.Trim(host, "[]"), "80")
		}
	}
	if dial == nil {
		dialer := &net.Dialer{Timeout: 20 * time.Second}
		dial = func(ctx context.Context, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		}
	}
	raw, err := dial(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", host, err)
	}
	conn := raw
	if target.Scheme == "wss" {
		config := tlsConfig.Clone()
		if config.ServerName == "" {
			config.ServerName = strings.Trim(target.Hostname(), "[]")
		}
		tlsConn := tls.Client(raw, config)
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(30 * time.Second)
		}
		_ = tlsConn.SetDeadline(deadline)
		if err := tlsConn.Handshake(); err != nil {
			raw.Close()
			return nil, err
		}
		_ = tlsConn.SetDeadline(time.Time{})
		conn = tlsConn
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := target.RequestURI()
	var request strings.Builder
	fmt.Fprintf(&request, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", path, target.Host, key)
	if len(protocols) > 0 {
		fmt.Fprintf(&request, "Sec-WebSocket-Protocol: %s\r\n", strings.Join(protocols, ", "))
	}
	for name, values := range header {
		for _, value := range values {
			if strings.ContainsAny(name+value, "\r\n") {
				conn.Close()
				return nil, errors.New("a console header held a line break")
			}
			fmt.Fprintf(&request, "%s: %s\r\n", name, value)
		}
	}
	request.WriteString("\r\n")
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.WriteString(conn, request.String()); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("the console server's answer was not understood: %w", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		conn.Close()
		text := strings.TrimSpace(string(body))
		if text != "" {
			return nil, fmt.Errorf("the console server refused the connection (%s): %s", response.Status, text)
		}
		return nil, fmt.Errorf("the console server refused the connection (%s)", response.Status)
	}
	sum := sha1.Sum([]byte(key + consoleWSGUID))
	if response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		conn.Close()
		return nil, errors.New("the console server's WebSocket answer did not match")
	}
	_ = conn.SetDeadline(time.Time{})
	return &consoleWebSocket{conn: conn, reader: reader}, nil
}

// Read returns the payload of binary and text frames as one stream.
func (w *consoleWebSocket) Read(p []byte) (int, error) {
	for len(w.pending) == 0 {
		if w.closed.Load() {
			return 0, io.EOF
		}
		if err := w.readFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, w.pending)
	w.pending = w.pending[n:]
	return n, nil
}

func (w *consoleWebSocket) readFrame() error {
	var head [2]byte
	if _, err := io.ReadFull(w.reader, head[:]); err != nil {
		return err
	}
	opcode := head[0] & 0x0f
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.reader, ext[:]); err != nil {
			return err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.reader, ext[:]); err != nil {
			return err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > 64<<20 {
		return errors.New("the console server sent a frame larger than 64 MB")
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(w.reader, mask[:]); err != nil {
			return err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(w.reader, payload); err != nil {
		return err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	switch opcode {
	case 0x0, 0x1, 0x2:
		w.pending = append(w.pending, payload...)
	case 0x8:
		w.closed.Store(true)
		_ = w.writeFrame(0x8, nil)
		return io.EOF
	case 0x9:
		return w.writeFrame(0xA, payload)
	case 0xA:
	default:
		return fmt.Errorf("the console server sent an unknown frame type %d", opcode)
	}
	return nil
}

func (w *consoleWebSocket) Write(p []byte) (int, error) {
	if err := w.writeFrame(0x2, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// writeFrame sends one masked frame (clients must mask).
func (w *consoleWebSocket) writeFrame(opcode byte, payload []byte) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	frame := make([]byte, 0, len(payload)+14)
	frame = append(frame, 0x80|opcode)
	switch {
	case len(payload) < 126:
		frame = append(frame, 0x80|byte(len(payload)))
	case len(payload) <= 0xffff:
		frame = append(frame, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	default:
		frame = append(frame, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		frame = append(frame, ext[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	frame = append(frame, mask[:]...)
	start := len(frame)
	frame = append(frame, payload...)
	for i := start; i < len(frame); i++ {
		frame[i] ^= mask[(i-start)%4]
	}
	_, err := w.conn.Write(frame)
	return err
}

func (w *consoleWebSocket) Close() error {
	var err error
	w.once.Do(func() {
		if !w.closed.Swap(true) {
			_ = w.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_ = w.writeFrame(0x8, []byte{0x03, 0xe8})
		}
		err = w.conn.Close()
	})
	return err
}
