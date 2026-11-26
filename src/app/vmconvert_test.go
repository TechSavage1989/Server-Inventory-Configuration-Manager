package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// vmConvertTestData is a disk with data at the start, across a 32 MB
// boundary, a long zero gap, a lone sector and the very end, and a size
// that is not a whole number of megabytes.
func vmConvertTestData(t *testing.T) []byte {
	t.Helper()
	size := 70<<20 + 64<<10 + 1024
	data := make([]byte, size)
	rand.Read(data[:2<<20])
	rand.Read(data[32<<20-(512<<10) : 32<<20+(512<<10)])
	rand.Read(data[50<<20+4096 : 50<<20+4608])
	rand.Read(data[size-8192:])
	return data
}

func vmWriteTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func vmReadImage(t *testing.T, name string) ([]byte, vmDiskInfo) {
	t.Helper()
	image, err := openDiskImage(name)
	if err != nil {
		t.Fatalf("opening %s: %v", filepath.Base(name), err)
	}
	defer image.Close()
	out := make([]byte, image.Size())
	// Read in odd-sized pieces so block, grain and cluster edges are
	// crossed in the middle of a read.
	for offset := int64(0); offset < image.Size(); {
		n := int64(3<<20 + 4096 + 512)
		if offset+n > image.Size() {
			n = image.Size() - offset
		}
		if _, err := image.ReadAt(out[offset:offset+n], offset); err != nil {
			t.Fatalf("reading %s at %d: %v", filepath.Base(name), offset, err)
		}
		offset += n
	}
	return out, image.Info()
}

func vmAssertSameDisk(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d bytes, want %d", label, len(got), len(want))
	}
	if !bytes.Equal(got, want) {
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: first difference at byte %d", label, i)
			}
		}
	}
}

// Every output format round-trips through its own reader, and a repeat pass
// after the source changes rewrites only the changed chunks.
func TestConvertEveryOutputFormatRoundTripsAndUpdates(t *testing.T) {
	data := vmConvertTestData(t)
	for _, format := range vmDiskFormatNames {
		format := format
		t.Run(format.ID, func(t *testing.T) {
			folder := t.TempDir()
			source := vmWriteTestFile(t, filepath.Join(folder, "source.img"), data)
			target := filepath.Join(folder, "out"+format.Extension)
			result, err := vmConvertDisk(context.Background(), source, target, format.ID, vmConvertOptions{Verify: true})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Verified || result.Passes != 1 || result.Resumed {
				t.Fatalf("unexpected result %+v", result)
			}
			got, info := vmReadImage(t, target)
			vmAssertSameDisk(t, format.ID, got[:len(data)], data)
			if info.VirtualSize < int64(len(data)) {
				t.Fatalf("virtual size %d", info.VirtualSize)
			}

			changed := append([]byte(nil), data...)
			rand.Read(changed[40<<20 : 40<<20+4096])
			vmZeroFill(changed[:2<<20]) // data removed from the source
			vmWriteTestFile(t, source, changed)
			second, err := vmConvertDisk(context.Background(), source, target, format.ID, vmConvertOptions{Verify: true})
			if err != nil {
				t.Fatal(err)
			}
			if !second.Resumed || second.Passes != 2 || second.Progress.Changed != 2 {
				t.Fatalf("delta pass: %+v", second.Progress)
			}
			got, _ = vmReadImage(t, target)
			vmAssertSameDisk(t, format.ID+" after delta", got[:len(changed)], changed)
		})
	}
}

// Sparse sources are converted without reading their empty areas.
func TestConvertSkipsUnallocatedSourceAreas(t *testing.T) {
	folder := t.TempDir()
	data := vmConvertTestData(t)
	middle := filepath.Join(folder, "middle.qcow2")
	if _, err := vmConvertDisk(context.Background(), vmWriteTestFile(t, filepath.Join(folder, "a.img"), data), middle, vmDiskFormatQCOW2, vmConvertOptions{}); err != nil {
		t.Fatal(err)
	}
	result, err := vmConvertDisk(context.Background(), middle, filepath.Join(folder, "b.vhdx"), vmDiskFormatVHDX, vmConvertOptions{Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Progress.BytesRead >= int64(len(data))/2 {
		t.Fatalf("read %d of %d bytes; the empty areas should be skipped", result.Progress.BytesRead, len(data))
	}
	got, _ := vmReadImage(t, filepath.Join(folder, "b.vhdx"))
	vmAssertSameDisk(t, "via qcow2", got, data)
}

// A stopped conversion resumes where it stopped.
func TestConvertResumesAfterStop(t *testing.T) {
	folder := t.TempDir()
	data := vmConvertTestData(t)
	source := vmWriteTestFile(t, filepath.Join(folder, "source.img"), data)
	for _, format := range []string{vmDiskFormatQCOW2, vmDiskFormatVMDK, vmDiskFormatVHD} {
		target := filepath.Join(folder, "stopped-"+format)
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, err := vmConvertDisk(ctx, source, target, format, vmConvertOptions{chunkSize: 1 << 20, Progress: func(vmCopyProgress) {}})
		if err != nil {
			t.Fatal(err)
		}
		// Second run is a full repeat; now stop a fresh one part way.
		target = filepath.Join(folder, "stopped2-"+format)
		stopping := &vmStopAfterSource{vmDiskImage: vmMustOpen(t, source), after: 20, cancel: cancel}
		_, err = vmConvertImage(ctx, stopping, target, format, vmConvertOptions{chunkSize: 1 << 20})
		stopping.Close()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: expected a stop, got %v", format, err)
		}
		calls = stopping.reads
		result, err := vmConvertDisk(context.Background(), source, target, format, vmConvertOptions{chunkSize: 1 << 20, Verify: true})
		if err != nil {
			t.Fatalf("%s: resume: %v", format, err)
		}
		if !result.Resumed || calls == 0 {
			t.Fatalf("%s: did not resume: %+v", format, result)
		}
		got, _ := vmReadImage(t, target)
		vmAssertSameDisk(t, format+" resumed", got[:len(data)], data)
	}
}

type vmStopAfterSource struct {
	vmDiskImage
	after  int
	reads  int
	cancel context.CancelFunc
}

func (s *vmStopAfterSource) ReadAt(p []byte, off int64) (int, error) {
	s.reads++
	if s.reads == s.after {
		s.cancel()
	}
	return s.vmDiskImage.ReadAt(p, off)
}

func vmMustOpen(t *testing.T, name string) vmDiskImage {
	t.Helper()
	image, err := openDiskImage(name)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

// Nothing is ever overwritten: an unrelated file, a different source, or
// the source itself as the target are all refused.
func TestConvertNeverOverwrites(t *testing.T) {
	folder := t.TempDir()
	data := vmConvertTestData(t)
	source := vmWriteTestFile(t, filepath.Join(folder, "source.img"), data)
	other := vmWriteTestFile(t, filepath.Join(folder, "other.img"), data[:1<<20])
	existing := vmWriteTestFile(t, filepath.Join(folder, "existing.vhdx"), []byte("keep me"))
	if _, err := vmConvertDisk(context.Background(), source, existing, vmDiskFormatVHDX, vmConvertOptions{}); err == nil || !strings.Contains(err.Error(), "not created by this conversion") {
		t.Fatalf("existing file: %v", err)
	}
	if kept, _ := os.ReadFile(existing); string(kept) != "keep me" {
		t.Fatal("an existing file was changed")
	}
	if _, err := vmConvertDisk(context.Background(), source, source, vmDiskFormatRaw, vmConvertOptions{}); err == nil {
		t.Fatal("converting a file onto itself was allowed")
	}
	target := filepath.Join(folder, "out.qcow2")
	if _, err := vmConvertDisk(context.Background(), source, target, vmDiskFormatQCOW2, vmConvertOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := vmConvertDisk(context.Background(), other, target, vmDiskFormatQCOW2, vmConvertOptions{}); err == nil {
		t.Fatal("a different source was written over an earlier conversion")
	}
	flat := filepath.Join(folder, "disk.vmdk")
	vmWriteTestFile(t, vmdkFlatDataPath(flat), []byte("existing flat"))
	if _, err := vmConvertDisk(context.Background(), source, flat, vmDiskFormatVMDKFlat, vmConvertOptions{}); err == nil {
		t.Fatal("an existing -flat.vmdk was overwritten")
	}
	if _, err := os.Stat(flat); !os.IsNotExist(err) {
		t.Fatal("a refused conversion left a descriptor behind")
	}
}

func TestConvertRefusesOversizedOutputs(t *testing.T) {
	if _, err := createVHD(filepath.Join(t.TempDir(), "big.vhd"), 3<<40, false); err == nil || !strings.Contains(err.Error(), "2040 GB") {
		t.Fatalf("VHD over 2040 GB: %v", err)
	}
	if _, err := createVMDKSparse(filepath.Join(t.TempDir(), "big.vmdk"), 3<<40); err == nil {
		t.Fatal("a sparse VMDK over 2040 GB was allowed")
	}
}

func TestProbeRefusesMisnamedAndSnapshotFiles(t *testing.T) {
	folder := t.TempDir()
	for name, content := range map[string]string{
		"fake.vhdx":  strings.Repeat("x", 4096),
		"fake.qcow2": strings.Repeat("x", 4096),
		"delta.vmdk": "COWD" + strings.Repeat("\x00", 4092),
		"disk.iso":   strings.Repeat("\x00", 4096),
	} {
		path := vmWriteTestFile(t, filepath.Join(folder, name), []byte(content))
		if image, err := openDiskImage(path); err == nil {
			image.Close()
			t.Errorf("%s opened as %s", name, image.Info().Format)
		}
	}
	descriptor := "# Disk DescriptorFile\nversion=1\nCID=12345678\nparentCID=ffffffff\ncreateType=\"vmfsSparse\"\nRW 2048 SESPARSE \"x-sesparse.vmdk\"\n"
	path := vmWriteTestFile(t, filepath.Join(folder, "snap.vmdk"), []byte(descriptor))
	if _, err := openDiskImage(path); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("SESPARSE: %v", err)
	}
}

// QEMU pads a descriptor to a whole sector and, for some lengths, leaves its
// final line feed after the first zero byte. TestReadsEveryImageQEMUWrites
// hit this only when the random CID happened to give that length.
func TestDescriptorPaddedByQEMUIsRecognised(t *testing.T) {
	text := "# Disk DescriptorFile\nversion=1\nCID=2d63dd\nparentCID=ffffffff\ncreateType=\"monolithicFlat\"\n"
	padded := []byte(text + "\x00\n")
	padded = append(padded, make([]byte, 512-len(padded))...)
	if !vmLooksLikeDescriptor(padded) {
		t.Fatal("a zero-padded descriptor with a trailing line feed was not recognised")
	}
	if vmLooksLikeDescriptor(append([]byte(text+"\x00"), "KDMV"...)) {
		t.Fatal("data after the padding was accepted as a descriptor")
	}
	// QEMU records a new CID without leading zeros and writes back the whole
	// old buffer, so the old text's last bytes stay after the first zero.
	full := strings.Replace(text, "CID=2d63dd", "CID=12345678", 1) + "ddb.toolsVersion = \"2147483647\"\n"
	for _, cut := range []int{1, 2, 4, 7} {
		shrunk := strings.Replace(full, "CID=12345678", "CID="+"12345678"[cut:], 1)
		left := []byte(shrunk + "\x00" + full[len(full)-cut:])
		left = append(left, make([]byte, 512-len(left))...)
		got, ok := vmDescriptorText(left)
		if !ok || got != shrunk {
			t.Fatalf("cut %d: a descriptor QEMU shortened was refused (%q)", cut, left)
		}
	}
	for _, bad := range []string{"\x00\x01\x02", "\x00KDMV", "\x00\n\x00\x00\x07", "\x00ffff\"\n"} {
		if _, ok := vmDescriptorText([]byte(full + bad)); ok {
			t.Fatalf("%q after the text was accepted", bad)
		}
	}
}

func TestConvertSuggestedName(t *testing.T) {
	cases := map[string]string{
		`disk-flat.vmdk`: "disk.vhdx",
		`server.vhd`:     "server.vhdx",
		`a.b.qcow2`:      "a.b.vhdx",
	}
	for in, want := range cases {
		if got := vmConvertSuggestedName(in, vmDiskFormatVHDX); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

// QEMU cross-checks: qemu-img writes every input variant the reader must
// understand and checks every output the writers make. They run wherever
// qemu-img is installed (the CI disk job installs it).

func vmQEMU(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("qemu-img")
	if err != nil {
		t.Skip("qemu-img is not installed")
	}
	return path
}

func vmRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(name), strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestReadsEveryImageQEMUWrites(t *testing.T) {
	qemu := vmQEMU(t)
	folder := t.TempDir()
	data := vmConvertTestData(t)
	raw := vmWriteTestFile(t, filepath.Join(folder, "source.raw"), data)
	cases := []struct {
		name, format string
		args         []string
	}{
		{"monolithicSparse.vmdk", "vmdk", []string{"-o", "subformat=monolithicSparse"}},
		{"monolithicFlat.vmdk", "vmdk", []string{"-o", "subformat=monolithicFlat"}},
		{"twoGbSparse.vmdk", "vmdk", []string{"-o", "subformat=twoGbMaxExtentSparse"}},
		{"twoGbFlat.vmdk", "vmdk", []string{"-o", "subformat=twoGbMaxExtentFlat"}},
		{"streamOptimized.vmdk", "vmdk", []string{"-o", "subformat=streamOptimized"}},
		{"dynamic.vhd", "vpc", []string{"-o", "subformat=dynamic,force_size=on"}},
		{"fixed.vhd", "vpc", []string{"-o", "subformat=fixed,force_size=on"}},
		{"dynamic.vhdx", "vhdx", []string{"-o", "subformat=dynamic"}},
		{"smallblocks.vhdx", "vhdx", []string{"-o", "subformat=dynamic,block_size=1M"}},
		{"fixed.vhdx", "vhdx", []string{"-o", "subformat=fixed"}},
		{"v2.qcow2", "qcow2", []string{"-o", "compat=0.10"}},
		{"v3.qcow2", "qcow2", []string{"-o", "compat=1.1"}},
		{"4k.qcow2", "qcow2", []string{"-o", "cluster_size=4096"}},
		{"compressed.qcow2", "qcow2", []string{"-c"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			target := filepath.Join(folder, c.name)
			args := append([]string{"convert", "-f", "raw", "-O", c.format}, c.args...)
			vmRun(t, qemu, append(args, raw, target)...)
			got, info := vmReadImage(t, target)
			vmAssertSameDisk(t, c.name, got, data)
			if info.Format == "" || info.VirtualSize != int64(len(data)) {
				t.Fatalf("info %+v", info)
			}
		})
	}
}

func TestReadsQEMUBackingChains(t *testing.T) {
	qemu := vmQEMU(t)
	folder := t.TempDir()
	data := vmConvertTestData(t)
	raw := vmWriteTestFile(t, filepath.Join(folder, "base.raw"), data)
	vmRun(t, qemu, "convert", "-f", "raw", "-O", "qcow2", raw, filepath.Join(folder, "base.qcow2"))
	vmRun(t, qemu, "convert", "-f", "raw", "-O", "vmdk", raw, filepath.Join(folder, "base.vmdk"))
	want := append([]byte(nil), data...)
	patch := make([]byte, 3<<20)
	rand.Read(patch)
	copy(want[10<<20:], patch)
	vmZeroFill(want[32<<20 : 32<<20+65536])
	for _, chain := range []struct{ base, baseFormat, overlay, overlayFormat string }{
		{"base.qcow2", "qcow2", "overlay.qcow2", "qcow2"},
		{"base.raw", "raw", "overlay-on-raw.qcow2", "qcow2"},
		{"base.vmdk", "vmdk", "overlay.vmdk", "vmdk"},
	} {
		overlay := filepath.Join(folder, chain.overlay)
		vmRun(t, qemu, "create", "-f", chain.overlayFormat, "-F", chain.baseFormat, "-b", chain.base, overlay)
		vmRun(t, "qemu-io", "-f", chain.overlayFormat, "-c", "write -P 0 32M 64k", overlay)
		// Write the random patch through qemu-io in 1 MB pieces.
		for i := 0; i < 3; i++ {
			piece := vmWriteTestFile(t, filepath.Join(folder, "piece.bin"), patch[i<<20:(i+1)<<20])
			vmQEMUWriteFile(t, qemu, chain.overlayFormat, overlay, piece, int64(10<<20+i<<20))
		}
		got, info := vmReadImage(t, overlay)
		vmAssertSameDisk(t, chain.overlay, got, want)
		if len(info.Chain) != 2 {
			t.Fatalf("%s chain %v", chain.overlay, info.Chain)
		}
	}
}

// vmQEMUWriteFile writes a file into an image at offset through QEMU.
func vmQEMUWriteFile(t *testing.T, qemu, format, image, file string, offset int64) {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	vmRun(t, "qemu-io", "-f", format, "-c", "write -s "+file+" "+strconv.FormatInt(offset, 10)+" "+strconv.FormatInt(info.Size(), 10), image)
}

func TestQEMUAcceptsEveryOutput(t *testing.T) {
	qemu := vmQEMU(t)
	folder := t.TempDir()
	data := vmConvertTestData(t)
	source := vmWriteTestFile(t, filepath.Join(folder, "source.raw"), data)
	qemuFormat := map[string]string{
		vmDiskFormatVHDX: "vhdx", vmDiskFormatVHD: "vpc", vmDiskFormatVHDFixed: "vpc",
		vmDiskFormatQCOW2: "qcow2", vmDiskFormatVMDK: "vmdk", vmDiskFormatVMDKFlat: "vmdk", vmDiskFormatRaw: "raw",
	}
	for _, format := range vmDiskFormatNames {
		target := filepath.Join(folder, "out-"+format.ID+format.Extension)
		if _, err := vmConvertDisk(context.Background(), source, target, format.ID, vmConvertOptions{}); err != nil {
			t.Fatal(err)
		}
		switch format.ID {
		case vmDiskFormatQCOW2, vmDiskFormatVHDX, vmDiskFormatVMDK:
			vmRun(t, qemu, "check", "-f", qemuFormat[format.ID], target)
		}
		out := vmRun(t, qemu, "compare", "-f", "raw", "-F", qemuFormat[format.ID], source, target)
		if !strings.Contains(out, "identical") {
			t.Fatalf("%s: %s", format.ID, out)
		}
		info := vmRun(t, qemu, "info", "-f", qemuFormat[format.ID], target)
		if !strings.Contains(info, "("+strconv.Itoa(len(data))+" bytes)") {
			t.Fatalf("%s: QEMU reports another size:\n%s", format.ID, info)
		}
	}
	// A delta pass leaves a qcow2 that still checks clean.
	changed := append([]byte(nil), data...)
	rand.Read(changed[60<<20 : 61<<20])
	vmWriteTestFile(t, source, changed)
	target := filepath.Join(folder, "out-qcow2.qcow2")
	if _, err := vmConvertDisk(context.Background(), source, target, vmDiskFormatQCOW2, vmConvertOptions{}); err != nil {
		t.Fatal(err)
	}
	vmRun(t, qemu, "check", "-f", "qcow2", target)
	if out := vmRun(t, qemu, "compare", "-f", "raw", "-F", "qcow2", source, target); !strings.Contains(out, "identical") {
		t.Fatal(out)
	}
}
