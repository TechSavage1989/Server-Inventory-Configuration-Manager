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
	// Map, when set, gives the reader offset for a disk offset inside the
	// overlay and how many bytes follow contiguously (a striped, spanned or
	// mirrored volume's part on this disk). VolumeLength is then the whole
	// volume's length, for the short tail a shadow copy may refuse.
	Map          func(diskOffset int64) (readerOffset, run int64)
	VolumeLength int64
}

// vmOverlayTail is the most of a volume's end that may be read from the
// disk when its shadow copy refuses it.
const vmOverlayTail = 64 << 10

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
		reader, readerOffset, run, overlayEnd, mapped := s.base, off, int64(len(p)), int64(-1), int64(0)
		if run > s.size-off {
			run = s.size - off
		}
		for _, overlay := range s.overlays {
			if off >= overlay.Start+overlay.Length {
				continue
			}
			if off >= overlay.Start {
				reader, readerOffset, overlayEnd = overlay.Reader, off-overlay.Start, overlay.Start+overlay.Length
				if limit := overlay.Start + overlay.Length - off; run > limit {
					run = limit
				}
				if overlay.Map != nil {
					var limit int64
					readerOffset, limit = overlay.Map(off)
					if limit <= 0 {
						return total, fmt.Errorf("the volume %s has no data at disk offset %d", overlay.Name, off)
					}
					if run > limit {
						run = limit
					}
					mapped = overlay.VolumeLength
				}
			} else if limit := overlay.Start - off; run > limit {
				run = limit
			}
			break
		}
		n, err := reader.ReadAt(p[:run], readerOffset)
		atEnd := off+run == overlayEnd
		if mapped > 0 {
			atEnd = mapped-(readerOffset+int64(n)) <= vmOverlayTail
		}
		if overlayEnd >= 0 && int64(n) < run && atEnd && run-int64(n) <= vmOverlayTail &&
			(err == nil || err == io.EOF || err == io.ErrUnexpectedEOF) {
			// A volume device can end a little before its partition: NTFS
			// keeps its backup boot sector in the partition's last sector,
			// past the size the file system reports. Only that short tail
			// comes from the disk; any other short read is an error.
			var more int
			more, err = s.base.ReadAt(p[n:run], off+int64(n))
			n += more
		}
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

// vmFreeZeroReader reads an NTFS volume with every cluster the volume
// bitmap marks free returned as zeros, without reading it. NTFS never reads
// free clusters, so the copy is the same to the file system, free space
// compresses to nothing, and a repeat pass that reads the whole volume
// finds a change only where data or allocation really changed.
type vmFreeZeroReader struct {
	Reader      io.ReaderAt
	Bitmap      []byte
	ClusterSize int64
	Clusters    int64
}

func (z *vmFreeZeroReader) used(cluster int64) bool {
	return z.Bitmap[cluster/8]&(1<<(uint(cluster)%8)) != 0
}

func (z *vmFreeZeroReader) ReadAt(p []byte, off int64) (int, error) {
	total := 0
	end := z.Clusters * z.ClusterSize
	for len(p) > 0 {
		if off >= end {
			n, err := z.Reader.ReadAt(p, off)
			return total + n, err
		}
		cluster := off / z.ClusterSize
		state := z.used(cluster)
		runEnd := (cluster + 1) * z.ClusterSize
		for runEnd < end && runEnd-off < int64(len(p)) && z.used(runEnd/z.ClusterSize) == state {
			runEnd += z.ClusterSize
		}
		run := runEnd - off
		if run > int64(len(p)) {
			run = int64(len(p))
		}
		if state {
			n, err := z.Reader.ReadAt(p[:run], off)
			total += n
			if int64(n) < run {
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				return total, err
			}
		} else {
			for i := range p[:run] {
				p[i] = 0
			}
			total += int(run)
		}
		p, off = p[run:], off+run
	}
	return total, nil
}

// WrapOverlay replaces the reader of the overlay that starts at start.
func (s *vmCompositeSource) WrapOverlay(start int64, wrap func(io.ReaderAt) io.ReaderAt) bool {
	for i := range s.overlays {
		if s.overlays[i].Start == start && s.overlays[i].Map == nil {
			s.overlays[i].Reader = wrap(s.overlays[i].Reader)
			return true
		}
	}
	return false
}
