package main

import (
	"bytes"
	"strings"
	"testing"
)

// fakeMapper answers like IOCTL_VOLUME_LOGICAL_TO_PHYSICAL for a known layout.
func fakeMapper(layout vmVolumeLayout) func(int64) ([]vmPhysicalOffset, error) {
	return func(offset int64) ([]vmPhysicalOffset, error) {
		out := []vmPhysicalOffset{}
		for _, s := range layout.Segments {
			if offset >= s.VolumeStart && offset < s.VolumeStart+s.Length {
				out = append(out, vmPhysicalOffset{Disk: s.Disk, Offset: s.DiskStart + offset - s.VolumeStart})
			}
		}
		if layout.Kind == "striped" {
			n := int64(len(layout.Columns))
			index := offset / layout.StripeSize
			column := layout.Columns[index%n]
			out = append(out, vmPhysicalOffset{Disk: column.Disk, Offset: column.Start + index/n*layout.StripeSize + offset%layout.StripeSize})
		}
		return out, nil
	}
}

func TestVolumeLayoutDetectsSimpleSpannedMirroredAndStriped(t *testing.T) {
	mb := int64(1 << 20)
	cases := []struct {
		name    string
		truth   vmVolumeLayout
		extents []vmDiskExtent
	}{
		{"simple", vmVolumeLayout{Kind: "simple", Length: 300 * mb, Segments: []vmLinearSegment{{0, 300 * mb, 2, 16 * mb}}},
			[]vmDiskExtent{{2, 16 * mb, 300 * mb}}},
		{"spanned", vmVolumeLayout{Kind: "spanned", Length: 500 * mb, Segments: []vmLinearSegment{{0, 200 * mb, 1, mb}, {200 * mb, 120 * mb, 3, 64 * mb}, {320 * mb, 180 * mb, 1, 400 * mb}}},
			[]vmDiskExtent{{1, mb, 200 * mb}, {3, 64 * mb, 120 * mb}, {1, 400 * mb, 180 * mb}}},
		{"mirrored", vmVolumeLayout{Kind: "mirrored", Length: 256 * mb, Segments: []vmLinearSegment{{0, 256 * mb, 1, mb}, {0, 256 * mb, 2, 8 * mb}}},
			[]vmDiskExtent{{1, mb, 256 * mb}, {2, 8 * mb, 256 * mb}}},
		{"striped", vmVolumeLayout{Kind: "striped", Length: 3 * 100 * mb, StripeSize: 64 << 10, Columns: []vmDiskExtent{{4, mb, 100 * mb}, {5, 2 * mb, 100 * mb}, {6, mb, 100 * mb}}},
			[]vmDiskExtent{{4, mb, 100 * mb}, {5, 2 * mb, 100 * mb}, {6, mb, 100 * mb}}},
	}
	for _, c := range cases {
		got, err := vmDetectVolumeLayout(c.truth.Length, c.extents, fakeMapper(c.truth))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.Kind != c.truth.Kind {
			t.Fatalf("%s: detected %s", c.name, got.Kind)
		}
		// Every volume offset maps back through Parts to the same place.
		for _, disk := range got.Disks() {
			for _, part := range got.Parts(disk) {
				for _, delta := range []int64{0, 4096, part.Length / 3 / 512 * 512, part.Length - 512} {
					volume, run := part.Map(part.DiskStart + delta)
					if run <= 0 {
						t.Fatalf("%s: empty run", c.name)
					}
					physical, _ := fakeMapper(c.truth)(volume)
					found := false
					for _, p := range physical {
						if p.Disk == disk && p.Offset == part.DiskStart+delta {
							found = true
						}
					}
					if !found {
						t.Fatalf("%s: disk %d offset %d maps to volume %d, which Windows puts at %v", c.name, disk, part.DiskStart+delta, volume, physical)
					}
				}
			}
		}
	}
}

func TestVolumeLayoutRefusesRAID5AndIrregularStripes(t *testing.T) {
	mb := int64(1 << 20)
	extents := []vmDiskExtent{{1, mb, 100 * mb}, {2, mb, 100 * mb}, {3, mb, 100 * mb}}
	three := func(offset int64) ([]vmPhysicalOffset, error) {
		return []vmPhysicalOffset{{1, mb + offset}, {2, mb + offset}, {3, mb + offset}}, nil
	}
	if _, err := vmDetectVolumeLayout(100*mb, extents, three); err == nil || !strings.Contains(err.Error(), "RAID-5") {
		t.Fatalf("three copies: %v", err)
	}
	truth := vmVolumeLayout{Kind: "striped", Length: 300 * mb, StripeSize: 64 << 10, Columns: extents}
	good := fakeMapper(truth)
	odd := func(offset int64) ([]vmPhysicalOffset, error) {
		p, err := good(offset)
		if offset > 150*mb && offset/(64<<10)%3 == 1 {
			p[0].Offset += 4096
		}
		return p, err
	}
	if _, err := vmDetectVolumeLayout(300*mb, extents, odd); err == nil {
		t.Fatal("an irregular stripe layout was accepted")
	}
}

// Disks read through mapped overlays hold the shadow copy's bytes exactly
// where the layout puts them, and the live disk everywhere else.
func TestCompositeSourcesReadStripedAndSpannedVolumesFromOneShadow(t *testing.T) {
	mb := int64(1 << 20)
	for _, truth := range []vmVolumeLayout{
		{Kind: "striped", Length: 2 * 8 * mb, StripeSize: 64 << 10, Columns: []vmDiskExtent{{0, mb, 8 * mb}, {1, 2 * mb, 8 * mb}}},
		{Kind: "spanned", Length: 12 * mb, Segments: []vmLinearSegment{{0, 5 * mb, 1, 3 * mb}, {5 * mb, 7 * mb, 0, mb}}},
	} {
		volume := make([]byte, truth.Length)
		for i := range volume {
			volume[i] = byte(i*7 + i/4099)
		}
		live := [][]byte{bytes.Repeat([]byte{0xEE}, int(16*mb)), bytes.Repeat([]byte{0xDD}, int(16*mb))}
		want := [][]byte{append([]byte(nil), live[0]...), append([]byte(nil), live[1]...)}
		for offset := int64(0); offset < truth.Length; offset += 512 {
			physical, _ := fakeMapper(truth)(offset)
			for _, p := range physical {
				copy(want[p.Disk][p.Offset:p.Offset+512], volume[offset:offset+512])
			}
		}
		for disk := 0; disk < 2; disk++ {
			overlays := []vmOverlay{}
			for _, part := range truth.Parts(disk) {
				overlays = append(overlays, vmOverlay{Start: part.DiskStart, Length: part.Length, Reader: bytes.NewReader(volume), Name: "D:", Map: part.Map, VolumeLength: truth.Length})
			}
			source, err := newVMCompositeSource("disk", bytes.NewReader(live[disk]), int64(len(live[disk])), overlays, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(live[disk]))
			// Odd-sized reads cross stripe and part edges.
			for offset := 0; offset < len(got); {
				n := 300<<10 + 512
				if offset+n > len(got) {
					n = len(got) - offset
				}
				if _, err := source.ReadAt(got[offset:offset+n], int64(offset)); err != nil {
					t.Fatal(err)
				}
				offset += n
			}
			if !bytes.Equal(got, want[disk]) {
				t.Fatalf("%s disk %d differs", truth.Kind, disk)
			}
		}
	}
}
