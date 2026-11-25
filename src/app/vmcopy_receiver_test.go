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
	"time"
)

func TestReceiverCopiesOverTheNetworkAndResumes(t *testing.T) {
	folder := t.TempDir()
	receiver, err := startVMReceiver("127.0.0.1:0", folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	address := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	if len(strings.ReplaceAll(receiver.Code(), "-", "")) != 20 || vmNormalizeThumbprint(receiver.Thumbprint()) == "" {
		t.Fatalf("code %q thumbprint %q", receiver.Code(), receiver.Thumbprint())
	}

	size := int64(80 << 20)
	data := vmTestDiskBytes(size)
	source := &vmBytesSource{data: data, name: "[DS01] app01/app01.vmdk"}
	ctx := context.Background()

	// Stop part way, then resume against the same receiver.
	stopCtx, cancel := context.WithCancel(ctx)
	sink, err := newVMRemoteSink(stopCtx, address, receiver.Thumbprint(), strings.ToLower(receiver.Code()), "app01-disk0.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vmCopyTo(stopCtx, &vmCancelAfter{vmCopySource: source, reads: 4, cancel: cancel}, sink, vmCopyOptions{ChunkSize: 1 << 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the copy to stop, got %v", err)
	}
	sink, err = newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "app01-disk0.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	var progress vmCopyProgress
	result, err := vmCopyTo(ctx, source, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true, ChangeID: "52 aa/1", Progress: func(p vmCopyProgress) { progress = p }})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Resumed || !result.State.Verified || result.State.ChangeID != "52 aa/1" || progress.Done != progress.Chunks {
		t.Fatalf("result %+v", result.State)
	}
	target := filepath.Join(folder, "app01-disk0.vhdx")
	if !bytes.Equal(vmReadVHDX(t, target), data) {
		t.Fatal("the received VHDX differs from the source")
	}
	if receiver.ActiveSessions() != 0 {
		t.Fatal("the finished copy left its session open")
	}

	// A delta pass sends only the changed chunk.
	rand.Read(data[30<<20 : 30<<20+64])
	sink, _ = newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "app01-disk0.vhdx")
	result, err = vmCopyTo(ctx, source, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Progress.Changed != 1 || result.Progress.BytesWritten != 1<<20 {
		t.Fatalf("delta pass changed %d, wrote %d", result.Progress.Changed, result.Progress.BytesWritten)
	}
	if !bytes.Equal(vmReadVHDX(t, target), data) {
		t.Fatal("the delta pass left the received VHDX different")
	}
}

func TestReceiverRefusesWrongPeersCodesNamesAndData(t *testing.T) {
	folder := t.TempDir()
	receiver, err := startVMReceiver("127.0.0.1:0", folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	address := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	ctx := context.Background()

	other, _ := startVMReceiver("127.0.0.1:0", t.TempDir(), nil)
	defer other.Stop()
	if _, err := newVMRemoteSink(ctx, address, other.Thumbprint(), receiver.Code(), "a.vhdx"); err == nil || !strings.Contains(err.Error(), "thumbprint") {
		t.Fatalf("a different certificate was accepted: %v", err)
	}
	if _, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), other.Code(), "a.vhdx"); err == nil || !strings.Contains(err.Error(), "pairing code") {
		t.Fatalf("a wrong pairing code was accepted: %v", err)
	}
	for _, name := range []string{"../escape.vhdx", `..\escape.vhdx`, "C:x.vhdx", "disk.vmdk", ".hidden.vhdx", "a/b.vhdx"} {
		if _, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), name); err == nil {
			t.Fatalf("%q was accepted as a disk name", name)
		}
	}

	// A file the receiver did not create is never overwritten.
	os.WriteFile(filepath.Join(folder, "existing.vhdx"), []byte("keep"), 0600)
	sink, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "existing.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	source := &vmBytesSource{data: vmTestDiskBytes(40 << 20), name: "disk"}
	if _, err := vmCopyTo(ctx, source, sink, vmCopyOptions{ChunkSize: 1 << 20}); err == nil || !strings.Contains(err.Error(), "not created by this copy") {
		t.Fatalf("an existing file was opened for writing: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(folder, "existing.vhdx")); string(raw) != "keep" {
		t.Fatal("the existing file changed")
	}

	// A chunk whose bytes do not match its hash is refused, and two senders
	// cannot write the same disk at once.
	sink, _ = newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "b.vhdx")
	if _, _, err := sink.Open("disk", 40<<20, 1<<20); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 1<<20)
	payload[0] = 1
	if _, err := sink.Write(0, payload, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "does not match its hash") {
		t.Fatalf("a corrupted chunk was written: %v", err)
	}
	second, _ := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "b.vhdx")
	if _, _, err := second.Open("disk", 40<<20, 1<<20); err == nil || !strings.Contains(err.Error(), "already being received") {
		t.Fatalf("two senders opened one disk: %v", err)
	}
	sink.Close()
	if _, _, err := second.Open("disk", 40<<20, 1<<20); err != nil {
		t.Fatalf("the disk could not be reopened after the first sender closed it: %v", err)
	}
	second.Close()

	// A receiver that stops saves the record; the copy resumes on a new one.
	receiver.Stop()
	again, err := startVMReceiver("127.0.0.1:0", folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Stop()
	sink, err = newVMRemoteSink(ctx, "127.0.0.1:"+strconv.Itoa(again.Port()), again.Thumbprint(), again.Code(), "b.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	data := vmTestDiskBytes(40 << 20)
	sink.retries, sink.delay = 2, 10*time.Millisecond
	if _, err := vmCopyTo(ctx, &vmBytesSource{data: data, name: "disk"}, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vmReadVHDX(t, filepath.Join(folder, "b.vhdx")), data) {
		t.Fatal("the copy on the restarted receiver differs")
	}
}
