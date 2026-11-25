package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// vmMemoryDevice is a block device in memory that counts writes.
type vmMemoryDevice struct {
	data     []byte
	id       string
	writes   int
	written  int64
	closed   bool
	sector   int64
	failRead bool
}

func (d *vmMemoryDevice) ReadAt(p []byte, off int64) (int, error) {
	if d.failRead {
		return 0, errors.New("read failed")
	}
	return copy(p, d.data[off:]), nil
}

func (d *vmMemoryDevice) WriteAt(p []byte, off int64) (int, error) {
	d.writes++
	d.written += int64(len(p))
	return copy(d.data[off:], p), nil
}
func (d *vmMemoryDevice) Size() int64 { return int64(len(d.data)) }
func (d *vmMemoryDevice) SectorSize() int64 {
	if d.sector == 0 {
		return 512
	}
	return d.sector
}
func (d *vmMemoryDevice) Identity() string { return d.id }
func (d *vmMemoryDevice) Describe() string { return "test disk " + d.id }
func (d *vmMemoryDevice) Flush() error     { return nil }
func (d *vmMemoryDevice) Close() error     { d.closed = true; return nil }

func TestRawSinkCopiesOntoABlankDiskResumesAndSendsOnlyChanges(t *testing.T) {
	records := t.TempDir()
	size := int64(40 << 20)
	data := vmTestDiskBytes(size)
	source := &vmBytesSource{data: data, name: "[DS01] app01/app01.vmdk"}
	// The target is larger than the source, as hypervisors round up.
	device := &vmMemoryDevice{data: make([]byte, size+(8<<20)), id: "serial-A"}
	ctx := context.Background()

	stopCtx, cancel := context.WithCancel(ctx)
	if _, err := vmCopyTo(stopCtx, &vmCancelAfter{vmCopySource: source, reads: 3, cancel: cancel}, newVMRawSink(device, records, false), vmCopyOptions{ChunkSize: 1 << 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the copy to stop, got %v", err)
	}
	result, err := vmCopyTo(ctx, source, newVMRawSink(device, records, false), vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Resumed || !result.State.Verified || !result.State.Complete {
		t.Fatalf("result %+v", result.State)
	}
	if !bytes.Equal(device.data[:size], data) {
		t.Fatal("the raw disk differs from the source")
	}
	// The blank disk's zero areas were never written.
	zeroChunks := 0
	for index := int64(0); index < size; index += 1 << 20 {
		if vmIsZero(data[index : index+(1<<20)]) {
			zeroChunks++
		}
	}
	if zeroChunks == 0 || device.written > size-int64(zeroChunks)*(1<<20) {
		t.Fatalf("wrote %d bytes with %d zero chunks of %d", device.written, zeroChunks, size)
	}

	// A delta pass writes only the changed chunk, including one that became zero.
	rand.Read(data[20<<20 : 20<<20+100])
	var nonZero int64 = -1
	for index := int64(0); index < size; index += 1 << 20 {
		if !vmIsZero(data[index:index+(1<<20)]) && index != 20<<20 {
			nonZero = index
			break
		}
	}
	copy(data[nonZero:nonZero+(1<<20)], make([]byte, 1<<20))
	device.written = 0
	result, err = vmCopyTo(ctx, source, newVMRawSink(device, records, false), vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Progress.Changed != 2 || device.written != 2<<20 {
		t.Fatalf("delta pass changed %d, wrote %d", result.Progress.Changed, device.written)
	}
	if !bytes.Equal(device.data[:size], data) {
		t.Fatal("the delta pass left the raw disk different")
	}
}

func TestRawSinkRefusesDisksThatHoldDataOrAnotherCopy(t *testing.T) {
	records := t.TempDir()
	size := int64(36 << 20)
	data := vmTestDiskBytes(size)
	source := &vmBytesSource{data: data, name: "[DS01] app01/app01.vmdk"}
	ctx := context.Background()

	used := &vmMemoryDevice{data: make([]byte, size), id: "serial-used"}
	copy(used.data[510:], []byte{0x55, 0xAA})
	if _, err := vmCopyTo(ctx, source, newVMRawSink(used, records, false), vmCopyOptions{ChunkSize: 1 << 20}); err == nil || !strings.Contains(err.Error(), "already holds data") {
		t.Fatalf("a disk with a partition table was written: %v", err)
	}
	if used.writes != 0 {
		t.Fatal("the refused disk was written")
	}
	tail := &vmMemoryDevice{data: make([]byte, size), id: "serial-tail"}
	copy(tail.data[size-512:], []byte("EFI PART"))
	if _, err := vmCopyTo(ctx, source, newVMRawSink(tail, records, false), vmCopyOptions{ChunkSize: 1 << 20}); err == nil {
		t.Fatal("a disk with a GPT backup header was written")
	}

	small := &vmMemoryDevice{data: make([]byte, size-(1<<20)), id: "serial-small"}
	if _, err := vmCopyTo(ctx, source, newVMRawSink(small, records, false), vmCopyOptions{ChunkSize: 1 << 20}); err == nil || !strings.Contains(err.Error(), "at least as large") {
		t.Fatalf("a smaller disk was accepted: %v", err)
	}
	fourK := &vmMemoryDevice{data: make([]byte, size), id: "serial-4k", sector: 4096}
	odd := &vmBytesSource{data: data[:size-512], name: "odd"}
	if _, err := vmCopyTo(ctx, odd, newVMRawSink(fourK, records, false), vmCopyOptions{ChunkSize: 1 << 20}); err == nil || !strings.Contains(err.Error(), "sectors") {
		t.Fatalf("a source that is not whole 4K sectors was accepted: %v", err)
	}
	if _, err := vmCopyTo(ctx, source, newVMRawSink(used, "", true), vmCopyOptions{}); err == nil {
		t.Fatal("a copy without a records folder was accepted")
	}

	// With overwrite allowed, every chunk is written, zeros included, and the
	// old end of a larger disk is cleared.
	bigger := &vmMemoryDevice{data: make([]byte, size+(4<<20)), id: "serial-bigger"}
	copy(bigger.data[510:], []byte{0x55, 0xAA})
	copy(bigger.data[len(bigger.data)-512:], []byte("EFI PART"))
	result, err := vmCopyTo(ctx, source, newVMRawSink(bigger, records, true), vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.State.Verified || !bytes.Equal(bigger.data[:size], data) || !vmIsZero(bigger.data[len(bigger.data)-512:]) {
		t.Fatal("the overwritten disk does not hold the source")
	}
	if bigger.written < size {
		t.Fatalf("overwrite wrote only %d of %d bytes", bigger.written, size)
	}

	// The recorded disk refuses a different source.
	other := &vmBytesSource{data: data, name: "[DS01] db01/db01.vmdk"}
	if _, err := vmCopyTo(ctx, other, newVMRawSink(bigger, records, false), vmCopyOptions{ChunkSize: 1 << 20}); err == nil || !strings.Contains(err.Error(), "holds a copy of") {
		t.Fatalf("a different source was copied over the recorded disk: %v", err)
	}
	// Records of different disks never collide, even with odd identities.
	if vmRawRecordPath(records, "a/b") == vmRawRecordPath(records, "a\\b") || strings.ContainsAny(filepath.Base(vmRawRecordPath(records, `..\..\x:y`)), `\/:`) {
		t.Fatal("record names collide or escape the folder")
	}
}

func TestRawSinkWritesIntoAnImageFile(t *testing.T) {
	folder := t.TempDir()
	size := int64(36 << 20)
	data := vmTestDiskBytes(size)
	image := filepath.Join(folder, "disk.raw")
	if err := os.WriteFile(image, make([]byte, size), 0600); err != nil {
		t.Fatal(err)
	}
	device, err := openVMImageDevice(image)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vmCopyTo(context.Background(), &vmBytesSource{data: data, name: "src"}, newVMRawSink(device, filepath.Join(folder, "records"), false), vmCopyOptions{ChunkSize: 1 << 20, Verify: true}); err != nil {
		t.Fatal(err)
	}
	device.Close()
	got, _ := os.ReadFile(image)
	if !bytes.Equal(got, data) {
		t.Fatal("the image differs from the source")
	}
	if _, err := openVMImageDevice(folder); err == nil {
		t.Fatal("a folder was opened as a disk image")
	}
	if _, err := openVMImageDevice(filepath.Join(folder, "missing.raw")); err == nil {
		t.Fatal("a missing image was created")
	}
}

func TestReceiverWritesOnlyItsListedRawDisks(t *testing.T) {
	size := int64(36 << 20)
	data := vmTestDiskBytes(size)
	target := &vmMemoryDevice{data: make([]byte, size), id: "serial-R"}
	opened := 0
	disks := []vmReceiverDisk{{Name: "disk-1", Label: "VirtIO disk", Size: size, SectorSize: 512, open: func() (vmBlockDevice, error) {
		opened++
		target.closed = false
		return target, nil
	}}}
	receiver, err := startVMReceiverWith("127.0.0.1:0", vmReceiverConfig{Disks: disks, Records: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	address := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	ctx := context.Background()

	listed, err := vmReceiverListDisks(ctx, address, receiver.Thumbprint(), receiver.Code())
	if err != nil || len(listed) != 1 || listed[0].Name != "disk-1" || listed[0].Size != size {
		t.Fatalf("listed %+v, %v", listed, err)
	}
	if _, err := vmReceiverListDisks(ctx, address, receiver.Thumbprint(), "AAAAA-BBBBB-CCCCC-DDDDD"); err == nil {
		t.Fatal("a wrong pairing code listed the disks")
	}
	source := &vmBytesSource{data: data, name: "[DS01] app01/app01.vmdk"}
	sink, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "disk-1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := vmCopyTo(ctx, source, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.State.Verified || !bytes.Equal(target.data, data) || !target.closed || opened != 1 {
		t.Fatalf("verified %v, closed %v, opened %d", result.State.Verified, target.closed, opened)
	}
	// A repeat pass resumes from the record and writes nothing.
	target.written = 0
	sink, _ = newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "disk-1")
	result, err = vmCopyTo(ctx, source, sink, vmCopyOptions{ChunkSize: 1 << 20})
	if err != nil || !result.Resumed || target.written != 0 {
		t.Fatalf("repeat pass: %v, resumed %v, wrote %d", err, result != nil && result.Resumed, target.written)
	}
	// Names it was not started with, and VHDX files, are refused.
	for _, name := range []string{"disk-2", "app01.vhdx"} {
		sink, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vmCopyTo(ctx, source, sink, vmCopyOptions{ChunkSize: 1 << 20}); err == nil || !strings.Contains(err.Error(), "listed disks") {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
	if _, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), `..\disk-1`); err == nil {
		t.Fatal("a path was accepted as a disk name")
	}
	if _, err := startVMReceiverWith("127.0.0.1:0", vmReceiverConfig{Disks: disks}, nil); err == nil {
		t.Fatal("a raw-disk receiver started without a records folder")
	}
	if _, err := startVMReceiverWith("127.0.0.1:0", vmReceiverConfig{Disks: []vmReceiverDisk{{Name: "C:", open: disks[0].open}}, Records: t.TempDir()}, nil); err == nil {
		t.Fatal("a badly named disk was accepted")
	}
}
