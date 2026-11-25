package main

// The in-guest source agent copies the disks of the computer it runs on (a
// VM on any hypervisor, or a physical server for P2V) to a receiver. A
// running system's disk is not consistent, so each volume that Windows can
// snapshot is read from its Volume Shadow Copy instead, and only the areas
// outside those volumes (partition tables, the EFI and recovery partitions)
// are read from the live disk. Every round takes new shadow copies, so a
// repeat round is a consistent delta: the copy engine re-reads the disk and
// sends only chunks whose hash changed.

import (
	"errors"
	"fmt"
	"io"
	"sort"
)

// vmOverlay replaces a byte range of a disk with another reader (a volume's
// shadow copy, whose offset 0 is the volume's first byte).
type vmOverlay struct {
	Start  int64
	Length int64
	Reader io.ReaderAt
	Name   string
}

// vmCompositeSource reads a base disk with overlays laid over it.
type vmCompositeSource struct {
	base     io.ReaderAt
	size     int64
	name     string
	overlays []vmOverlay
	closers  []io.Closer
}

func newVMCompositeSource(name string, base io.ReaderAt, size int64, overlays []vmOverlay, closers []io.Closer) (*vmCompositeSource, error) {
	sorted := append([]vmOverlay(nil), overlays...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Start < sorted[b].Start })
	end := int64(0)
	for _, overlay := range sorted {
		if overlay.Length <= 0 || overlay.Start < 0 || overlay.Start+overlay.Length > size {
			return nil, fmt.Errorf("the volume %s lies outside the disk", overlay.Name)
		}
		if overlay.Start < end {
			return nil, fmt.Errorf("the volume %s overlaps another volume", overlay.Name)
		}
		if overlay.Start%vhdxLogicalSector != 0 || overlay.Length%vhdxLogicalSector != 0 {
			return nil, fmt.Errorf("the volume %s is not sector aligned", overlay.Name)
		}
		end = overlay.Start + overlay.Length
	}
	return &vmCompositeSource{base: base, size: size, name: name, overlays: sorted, closers: closers}, nil
}

func (s *vmCompositeSource) Size() int64      { return s.size }
func (s *vmCompositeSource) Describe() string { return s.name }

func (s *vmCompositeSource) Close() error {
	var first error
	for i := len(s.closers) - 1; i >= 0; i-- {
		if err := s.closers[i].Close(); err != nil && first == nil {
			first = err
		}
	}
	s.closers = nil
	return first
}

func (s *vmCompositeSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	total := 0
	for len(p) > 0 && off < s.size {
		reader, readerOffset, run := s.base, off, int64(len(p))
		if run > s.size-off {
			run = s.size - off
		}
		for _, overlay := range s.overlays {
			if off >= overlay.Start+overlay.Length {
				continue
			}
			if off >= overlay.Start {
				reader, readerOffset = overlay.Reader, off-overlay.Start
				if limit := overlay.Start + overlay.Length - off; run > limit {
					run = limit
				}
			} else if limit := overlay.Start - off; run > limit {
				run = limit
			}
			break
		}
		n, err := reader.ReadAt(p[:run], readerOffset)
		total += n
		if int64(n) < run {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return total, err
		}
		p, off = p[run:], off+run
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}
