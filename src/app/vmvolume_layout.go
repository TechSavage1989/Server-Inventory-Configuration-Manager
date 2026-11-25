package main

// Volume layouts for dynamic disks. A simple volume lies in one place on
// one disk, but a spanned volume continues from one disk to the next, a
// striped volume interleaves fixed-size stripes across its disks, and a
// mirrored volume keeps a full copy on each of two disks. The agent reads
// every volume from one shadow copy, so for each disk it needs to know
// which volume bytes belong at which disk offsets. The layout is found from
// the volume's disk extents and Windows' own logical-to-physical mapping
// (IOCTL_VOLUME_LOGICAL_TO_PHYSICAL), never guessed: an arrangement that
// does not match one of these shapes exactly (RAID-5, for example) is
// refused.

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
)

type vmDiskExtent struct {
	Disk   int
	Start  int64
	Length int64
}

type vmPhysicalOffset struct {
	Disk   int
	Offset int64
}

// vmLinearSegment maps a run of volume bytes to one disk.
type vmLinearSegment struct {
	VolumeStart int64
	Length      int64
	Disk        int
	DiskStart   int64
}

type vmVolumeLayout struct {
	Kind     string // simple, spanned, mirrored or striped
	Length   int64
	Segments []vmLinearSegment
	// Striped volumes: Columns are the extents in stripe order.
	StripeSize int64
	Columns    []vmDiskExtent
}

// vmVolumePart is one disk range of a volume and how it maps to volume
// offsets: Map returns the volume offset of a disk offset inside the part
// and how many bytes follow contiguously.
type vmVolumePart struct {
	DiskStart int64
	Length    int64
	Map       func(diskOffset int64) (volumeOffset, run int64)
}

// Disks lists the disks a layout uses.
func (l vmVolumeLayout) Disks() []int {
	seen := map[int]bool{}
	list := []int{}
	add := func(disk int) {
		if !seen[disk] {
			seen[disk] = true
			list = append(list, disk)
		}
	}
	for _, segment := range l.Segments {
		add(segment.Disk)
	}
	for _, column := range l.Columns {
		add(column.Disk)
	}
	sort.Ints(list)
	return list
}

// Parts lists where the volume lies on one disk.
func (l vmVolumeLayout) Parts(disk int) []vmVolumePart {
	parts := []vmVolumePart{}
	for _, segment := range l.Segments {
		if segment.Disk != disk {
			continue
		}
		s := segment
		parts = append(parts, vmVolumePart{DiskStart: s.DiskStart, Length: s.Length, Map: func(diskOffset int64) (int64, int64) {
			delta := diskOffset - s.DiskStart
			return s.VolumeStart + delta, s.Length - delta
		}})
	}
	if l.Kind == "striped" {
		n := int64(len(l.Columns))
		for index, column := range l.Columns {
			if column.Disk != disk {
				continue
			}
			c, i := column, int64(index)
			stripe := l.StripeSize
			usable := l.Length / n / stripe * stripe // whole stripes in each column
			parts = append(parts, vmVolumePart{DiskStart: c.Start, Length: usable, Map: func(diskOffset int64) (int64, int64) {
				delta := diskOffset - c.Start
				row, within := delta/stripe, delta%stripe
				return (row*n+i)*stripe + within, stripe - within
			}})
		}
	}
	sort.Slice(parts, func(a, b int) bool { return parts[a].DiskStart < parts[b].DiskStart })
	return parts
}

// vmDetectVolumeLayout works out a volume's layout from its extents and a
// logical-to-physical query. length is the volume's size in bytes.
func vmDetectVolumeLayout(length int64, extents []vmDiskExtent, mapOffset func(int64) ([]vmPhysicalOffset, error)) (vmVolumeLayout, error) {
	if len(extents) == 0 || length <= 0 {
		return vmVolumeLayout{}, errors.New("the volume has no disk extents")
	}
	at := func(offset int64) ([]vmPhysicalOffset, error) {
		physical, err := mapOffset(offset)
		if err != nil {
			return nil, fmt.Errorf("mapping volume offset %d: %w", offset, err)
		}
		if len(physical) == 0 {
			return nil, fmt.Errorf("volume offset %d has no physical location", offset)
		}
		return physical, nil
	}
	sameRun := func(base []vmPhysicalOffset, delta int64, got []vmPhysicalOffset) bool {
		if len(base) != len(got) {
			return false
		}
		for i := range base {
			if base[i].Disk != got[i].Disk || base[i].Offset+delta != got[i].Offset {
				return false
			}
		}
		return true
	}
	// runLength finds how far the mapping at start stays linear, up to max.
	runLength := func(start, max int64) (int64, error) {
		base, err := at(start)
		if err != nil {
			return 0, err
		}
		good, step := int64(vhdxLogicalSector), int64(vhdxLogicalSector)
		for good+step < max {
			got, err := at(start + good + step)
			if err != nil {
				return 0, err
			}
			if !sameRun(base, good+step, got) {
				break
			}
			good += step
			step *= 2
		}
		// Binary search between good (linear) and good+step (not, or max).
		high := good + step
		if high > max {
			high = max
		}
		for high-good > vhdxLogicalSector {
			mid := (good + high) / 2 / vhdxLogicalSector * vhdxLogicalSector
			got, err := at(start + mid)
			if err != nil {
				return 0, err
			}
			if sameRun(base, mid, got) {
				good = mid
			} else {
				high = mid
			}
		}
		if high == max {
			if got, err := at(start + max - vhdxLogicalSector); err == nil && sameRun(base, max-vhdxLogicalSector, got) {
				return max, nil
			}
		}
		return high, nil
	}
	first, err := at(0)
	if err != nil {
		return vmVolumeLayout{}, err
	}
	layout := vmVolumeLayout{Length: length}
	switch {
	case len(first) == 2 && len(extents) == 2:
		// Mirrored: each disk holds the whole volume.
		run, err := runLength(0, length)
		if err != nil {
			return layout, err
		}
		if run != length {
			return layout, errors.New("the mirrored volume is not laid out in one piece on each disk")
		}
		layout.Kind = "mirrored"
		for _, copy := range first {
			layout.Segments = append(layout.Segments, vmLinearSegment{VolumeStart: 0, Length: length, Disk: copy.Disk, DiskStart: copy.Offset})
		}
		return layout, nil
	case len(first) != 1:
		return layout, errors.New("the volume keeps more than one copy of its data in a way that is not a two-disk mirror (RAID-5 volumes are not supported)")
	}
	run, err := runLength(0, length)
	if err != nil {
		return layout, err
	}
	if run == length {
		layout.Kind = "simple"
		layout.Segments = []vmLinearSegment{{VolumeStart: 0, Length: length, Disk: first[0].Disk, DiskStart: first[0].Offset}}
		if len(extents) > 1 {
			layout.Kind = "spanned" // only one extent is used; still simple in effect
		}
		return layout, nil
	}
	// Striped when the first run is a short stripe and the next stripe
	// starts on another disk; spanned when runs are whole extents.
	isExtentLength := false
	for _, extent := range extents {
		if extent.Disk == first[0].Disk && extent.Start == first[0].Offset && extent.Length == run {
			isExtentLength = true
		}
	}
	if !isExtentLength && len(extents) >= 2 && run <= 16<<20 {
		stripe := run
		n := int64(len(extents))
		columns := []vmDiskExtent{}
		for i := int64(0); i < n; i++ {
			physical, err := at(i * stripe)
			if err != nil {
				return layout, err
			}
			if len(physical) != 1 {
				return layout, errors.New("the striped volume keeps more than one copy of a stripe")
			}
			found := false
			for _, extent := range extents {
				if extent.Disk == physical[0].Disk && extent.Start == physical[0].Offset {
					columns = append(columns, extent)
					found = true
				}
			}
			if !found {
				return layout, errors.New("a stripe of the volume does not start where its disk extent does")
			}
		}
		layout.Kind, layout.StripeSize, layout.Columns = "striped", stripe, columns
		// Check the arithmetic against Windows at stripe edges and at
		// random places, including the last whole stripe.
		random := rand.New(rand.NewSource(length))
		usable := length / n / stripe * stripe * n
		probes := []int64{stripe - vhdxLogicalSector, n * stripe, usable - vhdxLogicalSector}
		for i := 0; i < 48; i++ {
			probes = append(probes, random.Int63n(usable/vhdxLogicalSector)*vhdxLogicalSector)
		}
		for _, offset := range probes {
			physical, err := at(offset)
			if err != nil {
				return layout, err
			}
			row, within := offset/stripe/n, offset%stripe
			column := columns[(offset/stripe)%n]
			if len(physical) != 1 || physical[0].Disk != column.Disk || physical[0].Offset != column.Start+row*stripe+within {
				return layout, errors.New("the striped volume does not follow a regular stripe layout")
			}
		}
		if usable != length {
			return layout, fmt.Errorf("the striped volume is %d bytes, not a whole number of stripes across its disks", length)
		}
		return layout, nil
	}
	// Spanned: walk the volume run by run.
	layout.Kind = "spanned"
	offset := int64(0)
	for offset < length {
		if len(layout.Segments) > 64 {
			return layout, errors.New("the spanned volume has too many pieces")
		}
		physical, err := at(offset)
		if err != nil {
			return layout, err
		}
		if len(physical) != 1 {
			return layout, errors.New("part of the spanned volume keeps more than one copy")
		}
		run, err := runLength(offset, length-offset)
		if err != nil {
			return layout, err
		}
		layout.Segments = append(layout.Segments, vmLinearSegment{VolumeStart: offset, Length: run, Disk: physical[0].Disk, DiskStart: physical[0].Offset})
		offset += run
	}
	return layout, nil
}
