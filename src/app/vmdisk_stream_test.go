package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A stream-optimized VMDK written from any image must read back to the same
// bytes, both with this application's reader and, where installed, QEMU.
func TestStreamOptimizedVMDKMatchesItsSource(t *testing.T) {
	folder := t.TempDir()
	data := vmConvertTestData(t)
	raw := vmWriteTestFile(t, filepath.Join(folder, "source.raw"), data)
	source, err := openDiskImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(folder, "stream.vmdk")
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	written, err := vmWriteStreamOptimized(context.Background(), source, file, "stream.vmdk", func(done, total int64) { last = done })
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(target)
	if info.Size() != written || written%512 != 0 || last != int64(len(data)) {
		t.Fatalf("wrote %d bytes, file %d, progress %d", written, info.Size(), last)
	}
	if written > int64(len(data))/4 {
		t.Fatalf("the stream-optimized disk is not compressed or sparse: %d bytes", written)
	}
	got, readInfo := vmReadImage(t, target)
	if !bytes.Equal(got, data) {
		t.Fatal("the stream-optimized VMDK does not read back to the source")
	}
	if readInfo.Variant != "streamOptimized" {
		t.Fatalf("read as %q", readInfo.Variant)
	}
	if _, err := exec.LookPath("qemu-img"); err == nil {
		qemu := vmQEMU(t)
		vmRun(t, qemu, "check", "-f", "vmdk", target)
		vmRun(t, qemu, "compare", "-f", "raw", "-F", "vmdk", raw, target)
	}
	// A stop leaves an error, not a half-written success.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := vmWriteStreamOptimized(ctx, source, &bytes.Buffer{}, "x.vmdk", nil); err == nil {
		t.Fatal("a stopped write reported success")
	}
}
