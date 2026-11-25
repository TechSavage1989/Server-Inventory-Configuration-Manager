package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCompositeSourceReadsVolumesFromTheirShadowCopies(t *testing.T) {
	size := int64(1 << 20)
	live := bytes.Repeat([]byte{'L'}, int(size))
	shadowA := bytes.Repeat([]byte{'A'}, 256<<10)
	shadowB := bytes.Repeat([]byte{'B'}, 128<<10)
	source, err := newVMCompositeSource("host disk 0", bytes.NewReader(live), size, []vmOverlay{
		{Start: 640 << 10, Length: 128 << 10, Reader: bytes.NewReader(shadowB), Name: "D:"},
		{Start: 64 << 10, Length: 256 << 10, Reader: bytes.NewReader(shadowA), Name: "C:"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), live...)
	copy(want[64<<10:], shadowA)
	copy(want[640<<10:], shadowB)
	// Read in odd-sized pieces that straddle every boundary.
	got := make([]byte, size)
	for offset := 0; offset < len(got); {
		n := 100000
		if offset+n > len(got) {
			n = len(got) - offset
		}
		if read, err := source.ReadAt(got[offset:offset+n], int64(offset)); read != n || (err != nil && err != io.EOF) {
			t.Fatalf("read at %d: %d %v", offset, read, err)
		}
		offset += n
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the composite disk does not take each volume from its overlay")
	}
	if n, err := source.ReadAt(make([]byte, 10), size-5); n != 5 || err != io.EOF {
		t.Fatalf("reading past the end gave %d %v", n, err)
	}

	for name, overlays := range map[string][]vmOverlay{
		"outside":  {{Start: size - 512, Length: 1024, Reader: bytes.NewReader(nil), Name: "X:"}},
		"overlaps": {{Start: 0, Length: 4096, Reader: bytes.NewReader(nil)}, {Start: 2048, Length: 4096, Reader: bytes.NewReader(nil)}},
		"aligned":  {{Start: 100, Length: 4096, Reader: bytes.NewReader(nil)}},
	} {
		if _, err := newVMCompositeSource("d", bytes.NewReader(live), size, overlays, nil); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	short, _ := newVMCompositeSource("d", bytes.NewReader(live), size, []vmOverlay{{Start: 0, Length: 256 << 10, Reader: bytes.NewReader(make([]byte, 1024))}}, nil)
	if _, err := short.ReadAt(make([]byte, 256<<10), 0); err != io.ErrUnexpectedEOF {
		t.Fatalf("a short shadow read was not reported: %v", err)
	}
	// A shadow copy that ends one sector before its partition: that sector
	// comes from the disk.
	tail, _ := newVMCompositeSource("d", bytes.NewReader(live), size, []vmOverlay{{Start: 4096, Length: 8192, Reader: bytes.NewReader(bytes.Repeat([]byte{'S'}, 8192-512))}}, nil)
	buffer := make([]byte, 16384)
	if n, err := tail.ReadAt(buffer, 0); n != len(buffer) || err != nil {
		t.Fatalf("the volume's last sector was not read from the disk: %d %v", n, err)
	}
	if buffer[4095] != 'L' || buffer[4096] != 'S' || buffer[4096+8192-513] != 'S' || buffer[4096+8192-512] != 'L' {
		t.Fatal("the tail fallback read the wrong bytes")
	}
}
