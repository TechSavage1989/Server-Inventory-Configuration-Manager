package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// vmWriteStreamOptimized writes a disk image as a stream-optimized VMDK
// (VMware Virtual Disk Format 5.0, "Stream-Optimized Compressed Sparse
// Extents"): one sequential pass with deflated 64 KB grains, grain tables
// after each run of grains, then the grain directory, a footer and the
// end-of-stream marker. Zero grains are left out. This is the format vSphere
// imports through an OVF lease. It returns the bytes written.
func vmWriteStreamOptimized(ctx context.Context, source vmDiskImage, out io.Writer, name string, progress func(done, total int64)) (int64, error) {
	size := source.Size()
	if size <= 0 || size%512 != 0 {
		return 0, fmt.Errorf("a VMDK must be a whole number of 512-byte sectors, not %d bytes", size)
	}
	const grain = vmdkGrainSectors * 512
	sectors := size / 512
	grains := (size + grain - 1) / grain
	tables := (grains + vmdkGTEsPerGT - 1) / vmdkGTEsPerGT
	le := binary.LittleEndian
	w := &vmCountingWriter{w: out}
	pad := func() error {
		if rest := w.n % 512; rest != 0 {
			_, err := w.Write(make([]byte, 512-rest))
			return err
		}
		return nil
	}
	marker := func(value uint64, kind uint32) error {
		sector := make([]byte, 512)
		le.PutUint64(sector[0:], value)
		le.PutUint32(sector[12:], kind)
		_, err := w.Write(sector)
		return err
	}

	descriptor := strings.Replace(vmdkNewDescriptor("streamOptimized", "SPARSE", name, size), `ddb.virtualHWVersion = "4"`, `ddb.virtualHWVersion = "4"`+"\n"+`ddb.toolsVersion = "0"`, 1)
	descriptorSectors := int64(len(descriptor)+511) / 512
	header := func(gdAt uint64) []byte {
		h := make([]byte, 512)
		copy(h, "KDMV")
		le.PutUint32(h[4:], 3)
		le.PutUint32(h[8:], vmdkFlagNewline|vmdkFlagCompress|vmdkFlagMarkers)
		le.PutUint64(h[12:], uint64(sectors))
		le.PutUint64(h[20:], vmdkGrainSectors)
		le.PutUint64(h[28:], 1)
		le.PutUint64(h[36:], uint64(descriptorSectors))
		le.PutUint32(h[44:], vmdkGTEsPerGT)
		le.PutUint64(h[56:], gdAt)
		le.PutUint64(h[64:], uint64((1+descriptorSectors+vmdkGrainSectors-1)/vmdkGrainSectors*vmdkGrainSectors))
		h[73], h[74], h[75], h[76] = '\n', ' ', '\r', '\n'
		le.PutUint16(h[77:], 1)
		return h
	}
	if _, err := w.Write(header(vmdkGDAtEnd)); err != nil {
		return w.n, err
	}
	if _, err := w.Write([]byte(descriptor)); err != nil {
		return w.n, err
	}
	if err := pad(); err != nil {
		return w.n, err
	}
	// Pad up to the overhead so the first grain is grain-aligned, as
	// VMware's own writers do.
	for w.n < int64(le.Uint64(header(0)[64:]))*512 {
		if _, err := w.Write(make([]byte, 512)); err != nil {
			return w.n, err
		}
	}

	allocated := source.Allocated()
	holds := func(start, length int64) bool {
		if allocated == nil {
			return true
		}
		for _, item := range allocated {
			if item.Start < start+length && start < item.Start+item.Length {
				return true
			}
		}
		return false
	}
	directory := make([]uint32, tables)
	table := make([]uint32, vmdkGTEsPerGT)
	buffer := make([]byte, grain)
	var compressed bytes.Buffer
	var done int64
	for tableIndex := int64(0); tableIndex < tables; tableIndex++ {
		for i := range table {
			table[i] = 0
		}
		used := false
		for entry := int64(0); entry < vmdkGTEsPerGT; entry++ {
			index := tableIndex*vmdkGTEsPerGT + entry
			if index >= grains {
				break
			}
			if err := ctx.Err(); err != nil {
				return w.n, err
			}
			offset := index * grain
			length := int64(grain)
			if offset+length > size {
				length = size - offset
			}
			if !holds(offset, length) {
				done += length
				continue
			}
			data := buffer[:grain]
			vmZeroFill(data)
			if err := vmReadFull(ctx, source, data[:length], offset, 0); err != nil {
				return w.n, fmt.Errorf("reading %s at %d: %w", source.Describe(), offset, err)
			}
			done += length
			if progress != nil {
				progress(done, size)
			}
			if vmAllZero(data) {
				continue
			}
			compressed.Reset()
			z, _ := zlib.NewWriterLevel(&compressed, zlib.BestSpeed)
			z.Write(data)
			if err := z.Close(); err != nil {
				return w.n, err
			}
			table[entry] = uint32(w.n / 512)
			head := make([]byte, 12)
			le.PutUint64(head[0:], uint64(offset/512))
			le.PutUint32(head[8:], uint32(compressed.Len()))
			if _, err := w.Write(head); err != nil {
				return w.n, err
			}
			if _, err := w.Write(compressed.Bytes()); err != nil {
				return w.n, err
			}
			if err := pad(); err != nil {
				return w.n, err
			}
			used = true
		}
		if !used {
			continue
		}
		tableSectors := uint64(vmdkGTEsPerGT * 4 / 512)
		if err := marker(tableSectors, 1); err != nil {
			return w.n, err
		}
		directory[tableIndex] = uint32(w.n / 512)
		raw := make([]byte, vmdkGTEsPerGT*4)
		for i, value := range table {
			le.PutUint32(raw[i*4:], value)
		}
		if _, err := w.Write(raw); err != nil {
			return w.n, err
		}
	}
	directorySectors := (tables*4 + 511) / 512
	if err := marker(uint64(directorySectors), 2); err != nil {
		return w.n, err
	}
	gdAt := uint64(w.n / 512)
	raw := make([]byte, directorySectors*512)
	for i, value := range directory {
		le.PutUint32(raw[i*4:], value)
	}
	if _, err := w.Write(raw); err != nil {
		return w.n, err
	}
	if err := marker(1, 3); err != nil {
		return w.n, err
	}
	if _, err := w.Write(header(gdAt)); err != nil {
		return w.n, err
	}
	if _, err := w.Write(make([]byte, 512)); err != nil {
		return w.n, err
	}
	if progress != nil {
		progress(size, size)
	}
	return w.n, nil
}

type vmCountingWriter struct {
	w io.Writer
	n int64
}

func (c *vmCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err == nil && n < len(p) {
		err = errors.New("short write")
	}
	return n, err
}
