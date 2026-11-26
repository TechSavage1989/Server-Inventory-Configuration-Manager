package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// vmStreamVMDK reads a stream-optimized VMDK (VMware Virtual Disk Format
// 5.0, "Stream-Optimized Compressed Sparse Extents") as it arrives, with no
// random access to the stream: the form vSphere sends a running VM's
// snapshot in through an export lease (ExportSnapshot), where the grain
// directory only comes at the end.
//
// Grains arrive in increasing order, so reads at increasing offsets (which
// is how the copy engine reads) decode the stream once. The last
// vmStreamWindow bytes stay available so a retried read of the same chunk
// works; a read further back opens the stream again with reopen, when set.
type vmStreamVMDK struct {
	name   string
	reopen func() (io.ReadCloser, error)

	mu       sync.Mutex
	body     io.ReadCloser
	r        *bufio.Reader
	at       int64 // bytes consumed from the stream
	capacity int64 // bytes
	grain    int64 // bytes
	// decided is the number of whole grains whose content is known: grains
	// below it either came in the stream or are zero.
	decided int64
	recent  map[int64][]byte
	order   []int64
	// next is the grain marker read ahead of decided: its grain index and
	// compressed bytes; nextGrain is -1 at the end of the stream.
	nextGrain int64
	nextData  []byte
	failed    error
}

// vmStreamWindow is how far behind the furthest read a read may go without
// opening the stream again: a little more than one copy chunk.
const vmStreamWindow = vmCopyChunkSize + 1<<20

func openVMStreamVMDK(name string, body io.ReadCloser, reopen func() (io.ReadCloser, error)) (*vmStreamVMDK, error) {
	s := &vmStreamVMDK{name: name, reopen: reopen}
	if err := s.start(body); err != nil {
		body.Close()
		return nil, err
	}
	return s, nil
}

// start reads the header of a (new) stream and positions it at the first
// marker.
func (s *vmStreamVMDK) start(body io.ReadCloser) error {
	s.body, s.r, s.at = body, bufio.NewReaderSize(body, 1<<20), 0
	s.decided, s.recent, s.order, s.failed = 0, map[int64][]byte{}, nil, nil
	header := make([]byte, 512)
	if err := s.readFull(header); err != nil {
		return fmt.Errorf("%s: reading the stream header: %w", s.name, err)
	}
	if string(header[:4]) != "KDMV" {
		return fmt.Errorf("%s: vSphere did not send a VMDK stream", s.name)
	}
	le := binary.LittleEndian
	flags := le.Uint32(header[8:])
	capacity := int64(le.Uint64(header[12:])) * 512
	grain := int64(le.Uint64(header[20:])) * 512
	overhead := int64(le.Uint64(header[64:])) * 512
	if flags&vmdkFlagCompress == 0 || flags&vmdkFlagMarkers == 0 || le.Uint16(header[77:]) != 1 {
		return fmt.Errorf("%s: the stream is not a compressed, stream-optimized VMDK", s.name)
	}
	if grain < 4096 || grain > 1<<20 || grain&(grain-1) != 0 || capacity <= 0 || overhead < 512 || overhead > 64<<20 {
		return fmt.Errorf("%s: the stream header has unsupported values", s.name)
	}
	if s.capacity != 0 && (capacity != s.capacity || grain != s.grain) {
		return fmt.Errorf("%s: the stream changed size between reads", s.name)
	}
	s.capacity, s.grain = capacity, grain
	if err := s.skip(overhead - s.at); err != nil {
		return fmt.Errorf("%s: reading the stream header: %w", s.name, err)
	}
	return s.advance()
}

func (s *vmStreamVMDK) readFull(p []byte) error {
	n, err := io.ReadFull(s.r, p)
	s.at += int64(n)
	return err
}

func (s *vmStreamVMDK) skip(n int64) error {
	if n < 0 {
		return errors.New("the stream went backwards")
	}
	done, err := s.r.Discard(int(n))
	s.at += int64(done)
	return err
}

// advance reads markers until the next grain marker (or the end of the
// stream) and holds it in next.
func (s *vmStreamVMDK) advance() error {
	le := binary.LittleEndian
	for {
		marker := make([]byte, 12)
		if err := s.readFull(marker); err != nil {
			return fmt.Errorf("the stream ended before its end marker: %w", err)
		}
		value, size := le.Uint64(marker[0:]), int64(le.Uint32(marker[8:]))
		if size > 0 {
			if size > 2*s.grain+4096 {
				return errors.New("a compressed grain has an invalid length")
			}
			data := make([]byte, size)
			if err := s.readFull(data); err != nil {
				return fmt.Errorf("reading a grain: %w", err)
			}
			if rest := s.at % 512; rest != 0 {
				if err := s.skip(512 - rest); err != nil {
					return err
				}
			}
			index := int64(value) * 512 / s.grain
			if int64(value)*512%s.grain != 0 || int64(value)*512 >= s.capacity {
				return fmt.Errorf("a grain marker points at sector %d, which is not a grain of this disk", value)
			}
			if index < s.decided {
				return errors.New("the stream's grains are not in disk order")
			}
			s.nextGrain, s.nextData = index, data
			return nil
		}
		rest := make([]byte, 500)
		if err := s.readFull(rest); err != nil {
			return fmt.Errorf("reading a metadata marker: %w", err)
		}
		kind := le.Uint32(rest[0:])
		if kind == 0 { // end of stream
			s.nextGrain, s.nextData = -1, nil
			return nil
		}
		if kind > 3 || value > uint64(64<<20)/512 {
			return fmt.Errorf("the stream has an unknown marker (%d)", kind)
		}
		// Grain tables, the grain directory and the footer: not needed when
		// reading in order.
		if err := s.skip(int64(value) * 512); err != nil {
			return fmt.Errorf("reading metadata: %w", err)
		}
	}
}

func (s *vmStreamVMDK) remember(index int64, data []byte) {
	s.recent[index] = data
	s.order = append(s.order, index)
	for len(s.order) > 0 && (index-s.order[0])*s.grain > vmStreamWindow {
		delete(s.recent, s.order[0])
		s.order = s.order[1:]
	}
}

// grainAt returns grain index's bytes (nil for zeros).
func (s *vmStreamVMDK) grainAt(index int64) ([]byte, error) {
	if index < s.decided {
		if (s.decided-index)*s.grain > vmStreamWindow {
			if s.reopen == nil {
				return nil, errors.New("the stream cannot be read backwards")
			}
			s.body.Close()
			body, err := s.reopen()
			if err != nil {
				return nil, err
			}
			if err := s.start(body); err != nil {
				return nil, err
			}
		} else {
			return s.recent[index], nil
		}
	}
	for s.decided <= index {
		if s.nextGrain < 0 || s.nextGrain > s.decided {
			// No grain in the stream here: zeros.
			s.decided++
			continue
		}
		reader, err := zlib.NewReader(bytes.NewReader(s.nextData))
		if err != nil {
			return nil, fmt.Errorf("a compressed grain could not be read: %w", err)
		}
		out := make([]byte, s.grain)
		if _, err := io.ReadFull(reader, out); err != nil && err != io.ErrUnexpectedEOF {
			reader.Close()
			return nil, fmt.Errorf("a compressed grain could not be expanded: %w", err)
		}
		reader.Close()
		s.remember(s.nextGrain, out)
		s.decided = s.nextGrain + 1
		if err := s.advance(); err != nil {
			return nil, err
		}
	}
	return s.recent[index], nil
}

func (s *vmStreamVMDK) Size() int64      { return s.capacity }
func (s *vmStreamVMDK) Describe() string { return s.name }

func (s *vmStreamVMDK) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.body == nil {
		return nil
	}
	err := s.body.Close()
	s.body = nil
	return err
}

func (s *vmStreamVMDK) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.body == nil {
		return 0, errors.New("the stream is closed")
	}
	if off >= s.capacity {
		return 0, io.EOF
	}
	if s.failed != nil {
		// A broken stream is opened again from the start, when it can be.
		if s.reopen == nil {
			return 0, s.failed
		}
		s.body.Close()
		body, err := s.reopen()
		if err != nil {
			return 0, err
		}
		if err := s.start(body); err != nil {
			s.failed = err
			return 0, err
		}
	}
	total := 0
	for len(p) > 0 && off < s.capacity {
		index, inside := off/s.grain, off%s.grain
		n := s.grain - inside
		if n > int64(len(p)) {
			n = int64(len(p))
		}
		if n > s.capacity-off {
			n = s.capacity - off
		}
		data, err := s.grainAt(index)
		if err != nil {
			s.failed = fmt.Errorf("%s: %w", s.name, err)
			return total, s.failed
		}
		if data == nil {
			vmZeroFill(p[:n])
		} else {
			copy(p[:n], data[inside:inside+n])
		}
		p, off, total = p[n:], off+n, total+int(n)
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}
