package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
)

func vmTestDiskBytes(size int64) []byte {
	data := make([]byte, size)
	// Data at the start, a zero gap, data across a 32 MB block boundary and
	// at the very end.
	rand.Read(data[:3<<20])
	rand.Read(data[vhdxBlockSize-(1<<20) : vhdxBlockSize+(1<<20)])
	rand.Read(data[size-4096:])
	return data
}

type vmBytesSource struct {
	data  []byte
	name  string
	fails int
}

func (s *vmBytesSource) ReadAt(p []byte, off int64) (int, error) {
	if s.fails > 0 {
		s.fails--
		return 0, errors.New("simulated network drop")
	}
	if off >= int64(len(s.data)) {
		return 0, io.EOF
	}
	n := copy(p, s.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (s *vmBytesSource) Size() int64      { return int64(len(s.data)) }
func (s *vmBytesSource) Describe() string { return s.name }
func (s *vmBytesSource) Close() error     { return nil }

func vmReadVHDX(t *testing.T, name string) []byte {
	t.Helper()
	disk, err := openVHDX(name, true)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	out := make([]byte, disk.Size())
	if _, err := disk.ReadAt(out, 0); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVHDXStructureFollowsTheSpecification(t *testing.T) {
	name := filepath.Join(t.TempDir(), "disk.vhdx")
	disk, err := createVHDX(name, 100<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:8]) != "vhdxfile" {
		t.Fatal("missing file identifier")
	}
	checksum := func(at, length int) bool {
		block := append([]byte(nil), raw[at:at+length]...)
		stored := binary.LittleEndian.Uint32(block[4:])
		binary.LittleEndian.PutUint32(block[4:], 0)
		return crc32.Checksum(block, crc32.MakeTable(crc32.Castagnoli)) == stored
	}
	for _, at := range []int{64 << 10, 128 << 10} {
		if string(raw[at:at+4]) != "head" || !checksum(at, 4096) {
			t.Fatalf("header at %d is invalid", at)
		}
		if version := binary.LittleEndian.Uint16(raw[at+66:]); version != 1 {
			t.Fatalf("header version %d", version)
		}
	}
	for _, at := range []int{192 << 10, 256 << 10} {
		if string(raw[at:at+4]) != "regi" || !checksum(at, 64<<10) {
			t.Fatalf("region table at %d is invalid", at)
		}
	}
	// The GUID encoding must match how Windows stores the BAT region GUID.
	if got := vhdxGUID("2DC27766-F623-4200-9D64-115E9BFD4A08"); !bytes.Equal(got[:], []byte{0x66, 0x77, 0xC2, 0x2D, 0x23, 0xF6, 0x00, 0x42, 0x9D, 0x64, 0x11, 0x5E, 0x9B, 0xFD, 0x4A, 0x08}) {
		t.Fatalf("GUID bytes %x", got)
	}
	if string(raw[2<<20:2<<20+8]) != "metadata" {
		t.Fatal("missing metadata table")
	}
	if len(raw) != 4<<20 {
		t.Fatalf("an empty disk should be 4 MB of structures, got %d", len(raw))
	}
	// Re-opening keeps the newest header and the disk size.
	again, err := openVHDX(name, true)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Size() != 100<<20 || again.AllocatedBytes() != 0 {
		t.Fatalf("size %d allocated %d", again.Size(), again.AllocatedBytes())
	}
}

func TestVHDXRoundTripKeepsZeroRunsSparse(t *testing.T) {
	size := int64(200 << 20)
	data := vmTestDiskBytes(size)
	name := filepath.Join(t.TempDir(), "disk.vhdx")
	disk, err := createVHDX(name, size)
	if err != nil {
		t.Fatal(err)
	}
	for offset := int64(0); offset < size; offset += 3 << 20 {
		end := offset + 3<<20
		if end > size {
			end = size
		}
		if _, err := disk.WriteAt(data[offset:end], offset); err != nil {
			t.Fatal(err)
		}
	}
	// Blocks 0, 1 (boundary) and the last one hold data; the rest stay empty.
	if allocated := disk.AllocatedBytes(); allocated != 3*vhdxBlockSize {
		t.Fatalf("allocated %d bytes, want %d", allocated, 3*vhdxBlockSize)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vmReadVHDX(t, name), data) {
		t.Fatal("VHDX contents differ from what was written")
	}
	if _, err := createVHDX(name, size); err == nil {
		t.Fatal("an existing file was overwritten")
	}
	if _, err := createVHDX(name+"2", 1000); err == nil {
		t.Fatal("a size that is not whole sectors was accepted")
	}
}

func TestCopyEngineResumesAndRewritesOnlyChangedChunks(t *testing.T) {
	size := int64(80 << 20)
	data := vmTestDiskBytes(size)
	source := &vmBytesSource{data: data, name: "[DS01] app01/app01.vmdk"}
	target := filepath.Join(t.TempDir(), "copies", "app01.vhdx")

	ctx, cancel := context.WithCancel(context.Background())
	_, err := vmCopyDisk(ctx, &vmCancelAfter{vmCopySource: source, reads: 5, cancel: cancel}, target, vmCopyOptions{ChunkSize: 1 << 20})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the copy to stop, got %v", err)
	}
	state, err := loadVMCopyState(target)
	if err != nil {
		t.Fatal(err)
	}
	copied := 0
	for _, hash := range state.Hashes {
		if hash != "" {
			copied++
		}
	}
	if copied == 0 || copied >= len(state.Hashes) {
		t.Fatalf("the stopped copy recorded %d of %d chunks", copied, len(state.Hashes))
	}

	result, err := vmCopyDisk(context.Background(), source, target, vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Resumed || !result.State.Complete || !result.State.Verified || result.State.Passes != 2 {
		t.Fatalf("unexpected result %+v", result.State)
	}
	if !bytes.Equal(vmReadVHDX(t, target), data) {
		t.Fatal("the resumed copy differs from the source")
	}

	// Change two chunks, zero one that held data: the delta pass rewrites 3.
	rand.Read(data[10<<20 : 10<<20+100])
	rand.Read(data[size-1:])
	for i := 0; i < 1<<20; i++ {
		data[i] = 0
	}
	var last vmCopyProgress
	result, err = vmCopyDisk(context.Background(), source, target, vmCopyOptions{ChunkSize: 1 << 20, Verify: true, Progress: func(p vmCopyProgress) { last = p }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Progress.Changed != 3 || result.Progress.BytesWritten != 3<<20 || last.Done != last.Chunks {
		t.Fatalf("delta pass changed %d chunks and wrote %d bytes", result.Progress.Changed, result.Progress.BytesWritten)
	}
	if !bytes.Equal(vmReadVHDX(t, target), data) {
		t.Fatal("the delta pass left the copy different from the source")
	}

	// A changed-block pass reads only the given ranges.
	rand.Read(data[40<<20 : 40<<20+10])
	result, err = vmCopyDisk(context.Background(), source, target, vmCopyOptions{ChunkSize: 1 << 20, Ranges: []vmCopyRange{{Start: 40 << 20, Length: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Progress.BytesRead != 1<<20 || result.Progress.Changed != 1 {
		t.Fatalf("changed-block pass read %d bytes, changed %d", result.Progress.BytesRead, result.Progress.Changed)
	}
	if !bytes.Equal(vmReadVHDX(t, target), data) {
		t.Fatal("the changed-block pass left the copy different from the source")
	}

	// A different source or an unknown file is never overwritten.
	other := &vmBytesSource{data: data, name: "[DS01] other/other.vmdk"}
	if _, err := vmCopyDisk(context.Background(), other, target, vmCopyOptions{ChunkSize: 1 << 20}); err == nil {
		t.Fatal("a copy of another disk was written into this target")
	}
	stranger := filepath.Join(t.TempDir(), "existing.vhdx")
	os.WriteFile(stranger, []byte("keep me"), 0600)
	if _, err := vmCopyDisk(context.Background(), source, stranger, vmCopyOptions{ChunkSize: 1 << 20}); err == nil {
		t.Fatal("an existing file without a copy record was overwritten")
	}
	if raw, _ := os.ReadFile(stranger); string(raw) != "keep me" {
		t.Fatal("the existing file was changed")
	}
}

type vmCancelAfter struct {
	vmCopySource
	reads  int
	cancel func()
}

func (s *vmCancelAfter) ReadAt(p []byte, off int64) (int, error) {
	s.reads--
	if s.reads == 0 {
		s.cancel()
	}
	return s.vmCopySource.ReadAt(p, off)
}

func TestCopyEngineRetriesReadsAndDetectsCorruption(t *testing.T) {
	data := vmTestDiskBytes(40 << 20)
	source := &vmBytesSource{data: data, name: "disk", fails: 2}
	target := filepath.Join(t.TempDir(), "disk.vhdx")
	if _, err := vmCopyDisk(context.Background(), source, target, vmCopyOptions{ChunkSize: 1 << 20, retryDelay: time.Millisecond}); err != nil {
		t.Fatalf("two dropped reads were not retried: %v", err)
	}
	// Damage a byte inside the VHDX payload: verification must catch it.
	disk, err := openVHDX(target, true)
	if err != nil {
		t.Fatal(err)
	}
	at := disk.blockOffset(0)
	disk.Close()
	file, _ := os.OpenFile(target, os.O_RDWR, 0)
	file.WriteAt([]byte{data[100] ^ 0xff}, at+100)
	file.Close()
	disk, _ = openVHDX(target, true)
	state, _ := loadVMCopyState(target)
	err = vmVerifyCopy(context.Background(), disk, state)
	disk.Close()
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("corruption was not detected: %v", err)
	}
	source.fails = 10
	if _, err := vmCopyDisk(context.Background(), source, target, vmCopyOptions{ChunkSize: 1 << 20, retryDelay: time.Millisecond}); err == nil {
		t.Fatal("a source that keeps failing was reported as copied")
	}
}

func TestVMDKDescriptorsAreParsedAndUnsafeFormatsRefused(t *testing.T) {
	descriptor, err := parseVMDKDescriptor(`# Disk DescriptorFile
version=1
CID=fffffffe
parentCID=ffffffff
createType="vmfs"

# Extent description
RW 20480 VMFS "app01-flat.vmdk"
RW 2048 ZERO

ddb.adapterType = "lsilogic"
`)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.CreateType != "vmfs" || len(descriptor.Extents) != 2 || descriptor.Extents[0].File != "app01-flat.vmdk" || descriptor.Extents[0].Sectors != 20480 {
		t.Fatalf("parsed %+v", descriptor)
	}
	if err := vmdkFlatExtents(descriptor); err != nil {
		t.Fatal(err)
	}
	refusals := map[string]string{
		`parentCID=12345678` + "\n" + `RW 100 VMFSSPARSE "app01-000001-delta.vmdk"`: "snapshot",
		`RW 100 SESPARSE "app01-000001-sesparse.vmdk"`:                              "disk converter",
		`RW 100 VMFSRDM "app01-rdm.vmdk"`:                                           "raw device mapping",
		`RW 100 FLAT "../../etc/passwd"`:                                            "not a file beside",
		`RW 100 FLAT "C:\Windows\x.vmdk"`:                                           "not a file beside",
	}
	for text, want := range refusals {
		parsed, err := parseVMDKDescriptor(text)
		if err == nil {
			err = vmdkFlatExtents(parsed)
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: got %v, want %q", text, err, want)
		}
	}
	for _, bad := range []string{"[DS01]", "DS01 vm/vm.vmdk", "[DS01] ../x.vmdk", "[DS01] vm/../../x", "[] x.vmdk"} {
		if _, _, err := vmSplitDatastorePath(bad); err == nil {
			t.Fatalf("%q was accepted as a datastore path", bad)
		}
	}
	if ds, file, err := vmSplitDatastorePath("[DS 01] app01/app01.vmdk"); err != nil || ds != "DS 01" || file != "app01/app01.vmdk" {
		t.Fatalf("got %q %q %v", ds, file, err)
	}
}

func TestLocalVMDKWithFlatAndZeroExtentsCopiesExactly(t *testing.T) {
	folder := t.TempDir()
	flat := vmTestDiskBytes(40 << 20)
	os.WriteFile(filepath.Join(folder, "app01-flat.vmdk"), flat, 0600)
	os.WriteFile(filepath.Join(folder, "app01.vmdk"), []byte("parentCID=ffffffff\nRW 81920 VMFS \"app01-flat.vmdk\"\nRW 2048 ZERO\n"), 0600)
	source, err := openLocalVMDK(filepath.Join(folder, "app01.vmdk"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Size() != 41<<20 {
		t.Fatalf("size %d", source.Size())
	}
	target := filepath.Join(folder, "app01.vhdx")
	if _, err := vmCopyDisk(context.Background(), source, target, vmCopyOptions{Verify: true}); err != nil {
		t.Fatal(err)
	}
	got := vmReadVHDX(t, target)
	if !bytes.Equal(got[:40<<20], flat) || !vmIsZero(got[40<<20:]) {
		t.Fatal("the VHDX does not match the VMDK extents")
	}
	os.WriteFile(filepath.Join(folder, "short.vmdk"), []byte("RW 999999 VMFS \"app01-flat.vmdk\"\n"), 0600)
	if _, err := openLocalVMDK(filepath.Join(folder, "short.vmdk")); err == nil || !strings.Contains(err.Error(), "smaller") {
		t.Fatalf("a descriptor larger than its extent was accepted: %v", err)
	}
}

// The datastore source reads over the same HTTPS /folder interface vCenter
// and ESXi provide, here from the vSphere simulator.
func TestDatastoreVMDKIsReadWithRangeRequests(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	server := model.Service.NewServer()
	defer server.Close()
	ctx := context.Background()
	client, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewManager(client).Login(ctx, server.URL.User); err != nil {
		t.Fatal(err)
	}
	datastore := simulator.Map.Any("Datastore").(*simulator.Datastore)
	folder := filepath.Join(datastore.Info.GetDatastoreInfo().Url, "app01")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	flat := vmTestDiskBytes(40 << 20)
	os.WriteFile(filepath.Join(folder, "app01-flat.vmdk"), flat, 0600)
	os.WriteFile(filepath.Join(folder, "app01.vmdk"), []byte("parentCID=ffffffff\nRW 81920 VMFS \"app01-flat.vmdk\"\n"), 0600)

	path := "[" + datastore.Name + "] app01/app01.vmdk"
	source, err := openDatastoreVMDK(ctx, client, "ha-datacenter", path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(t.TempDir(), "app01.vhdx")
	if _, err := vmCopyDisk(ctx, source, target, vmCopyOptions{Verify: true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vmReadVHDX(t, target), flat) {
		t.Fatal("the VHDX differs from the datastore disk")
	}
	if _, err := openDatastoreVMDK(ctx, client, "ha-datacenter", "["+datastore.Name+"] app01/missing.vmdk"); err == nil {
		t.Fatal("a missing disk was opened")
	}
}
