package main

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// vmSlowSource counts reads and can fail at one offset.
type vmSlowSource struct {
	vmCopySource
	mu      sync.Mutex
	reads   int
	failAt  int64
	delay   time.Duration
	maxRead int64
}

func (s *vmSlowSource) ReadAt(p []byte, off int64) (int, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	s.reads++
	if off > s.maxRead {
		s.maxRead = off
	}
	s.mu.Unlock()
	if s.failAt > 0 && off == s.failAt {
		return 0, errors.New("disk gone")
	}
	return s.vmCopySource.ReadAt(p, off)
}

// vmGatedSink blocks its writes until released, to see how far the reader
// gets ahead.
type vmGatedSink struct {
	vmCopySink
	gate    chan struct{}
	written int
}

func (s *vmGatedSink) Write(index int, data []byte, hash string) (int64, error) {
	<-s.gate
	s.written++
	return s.vmCopySink.Write(index, data, hash)
}

func TestCopyReadsAheadButNeverFarAheadOfTheWriter(t *testing.T) {
	data := vmTestDiskBytes(64 << 20)
	for i := range data {
		if data[i] == 0 {
			data[i] = byte(i) | 1 // no zero chunks: every chunk is written
		}
	}
	source := &vmSlowSource{vmCopySource: &vmBytesSource{data: data, name: "disk"}, maxRead: -1}
	target := t.TempDir() + "/a.vhdx"
	sink := &vmGatedSink{vmCopySink: &vmLocalSink{target: target}, gate: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := vmCopyTo(context.Background(), source, sink, vmCopyOptions{ChunkSize: 1 << 20})
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	source.mu.Lock()
	ahead := source.reads
	source.mu.Unlock()
	// Nothing written yet: the reader holds at most every buffer.
	if ahead == 0 || ahead > vmCopyReadAhead+1 {
		t.Fatalf("read %d chunks before the first write; want 1 to %d", ahead, vmCopyReadAhead+1)
	}
	close(sink.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sink.written != 64 {
		t.Fatalf("wrote %d chunks", sink.written)
	}
	disk, err := openVHDX(target, true)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	got := make([]byte, len(data))
	if _, err := disk.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("the copy differs from the source")
	}
}

func TestCopyStopsAtAReadErrorAndResumesFromIt(t *testing.T) {
	data := vmTestDiskBytes(48 << 20)
	source := &vmSlowSource{vmCopySource: &vmBytesSource{data: data, name: "disk"}, failAt: 10 << 20}
	target := t.TempDir() + "/b.vhdx"
	sink := &vmLocalSink{target: target}
	_, err := vmCopyTo(context.Background(), source, sink, vmCopyOptions{ChunkSize: 1 << 20, retryDelay: time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "reading the source at 10.0 MB") && !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("error %v", err)
	}
	state, err := loadVMCopyState(target)
	if err != nil {
		t.Fatal(err)
	}
	for index, hash := range state.Hashes {
		if index < 10 && hash == "" {
			t.Fatalf("chunk %d before the failure was not recorded", index)
		}
		if index >= 10 && hash != "" {
			t.Fatalf("chunk %d at or after the failure was recorded", index)
		}
	}
	source.failAt = 0
	sink = &vmLocalSink{target: target}
	result, err := vmCopyTo(context.Background(), source, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Resumed || !result.State.Verified {
		t.Fatalf("result %+v", result)
	}
}

func TestCopyStopsPromptlyWhenCancelled(t *testing.T) {
	data := vmTestDiskBytes(64 << 20)
	source := &vmSlowSource{vmCopySource: &vmBytesSource{data: data, name: "disk"}, delay: 5 * time.Millisecond}
	sink := &vmLocalSink{target: t.TempDir() + "/c.vhdx"}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	if _, err := vmCopyTo(ctx, source, sink, vmCopyOptions{ChunkSize: 1 << 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v", err)
	}
	// The reader has stopped: no read happens after vmCopyTo returns.
	source.mu.Lock()
	before := source.reads
	source.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	source.mu.Lock()
	after := source.reads
	source.mu.Unlock()
	if after != before {
		t.Fatalf("the source was read %d more times after the copy returned", after-before)
	}
}

func TestPlatformReceiverSinksCompressWhenTheReceiverSaysItCan(t *testing.T) {
	receiver, err := startVMReceiver("127.0.0.1:0", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	link := vmReceiverLink{Address: "127.0.0.1:" + strconv.Itoa(receiver.Port()), Thumbprint: receiver.Thumbprint(), Code: receiver.Code()}
	created, _, err := link.sinkFactory(context.Background(), []string{"app01-disk0.vhdx"})(0, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	sink := created.(*vmRemoteSink)
	if !sink.Compress {
		t.Fatal("the sink does not compress although the receiver inflates")
	}
	// Compressible data in use: it travels smaller and lands intact.
	data := make([]byte, 32<<20)
	for i := 0; i < 8<<20; i++ {
		data[i] = byte(i % 61)
	}
	result, err := vmCopyTo(context.Background(), &vmBytesSource{data: data, name: "APP01 disk 0"}, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.State.Verified || result.Progress.BytesSent >= int64(8<<20)/2 {
		t.Fatalf("sent %d bytes for 8 MB of data in use (verified %t)", result.Progress.BytesSent, result.State.Verified)
	}
}
