package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestCompressedThrottledSendMatchesAndSendsLess(t *testing.T) {
	folder := t.TempDir()
	receiver, err := startVMReceiver("127.0.0.1:0", folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	address := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	size := int64(40 << 20)
	data := vmTestDiskBytes(size)
	// A compressible region: repeated text, as in logs and databases.
	for i := 8 << 20; i < 12<<20; i++ {
		data[i] = "replication "[i%12]
	}
	ctx := context.Background()
	sink, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "app-disk0.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	sink.Compress = true
	throttle := newVMThrottle(vmThrottleSchedule{LimitMbps: 8})
	start := time.Date(2026, 11, 25, 12, 0, 0, 0, time.Local)
	var clockMu sync.Mutex
	clock := start
	throttle.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	throttle.until = func(_ context.Context, deadline time.Time) error {
		clockMu.Lock()
		defer clockMu.Unlock()
		if deadline.After(clock) {
			clock = deadline
		}
		return nil
	}
	sink.Throttle = throttle
	result, err := vmCopyTo(ctx, &vmBytesSource{data: data, name: "app disk 0"}, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.State.Verified || !bytes.Equal(vmReadVHDX(t, folder+"/app-disk0.vhdx"), data) {
		t.Fatal("the compressed copy differs from the source")
	}
	if result.Progress.BytesSent <= 0 || result.Progress.BytesSent >= result.Progress.BytesWritten {
		t.Fatalf("sent %d of %d written: compression saved nothing", result.Progress.BytesSent, result.Progress.BytesWritten)
	}
	// 8 Mbps is 1 MB/s: everything past the first second of burst waits.
	want := time.Duration(float64(result.Progress.BytesSent-1_000_000) / 1_000_000 * float64(time.Second))
	waited := clock.Sub(start)
	if waited < want-100*time.Millisecond || waited > want+100*time.Millisecond {
		t.Fatalf("throttle waited %v for %d bytes, want about %v", waited, result.Progress.BytesSent, want)
	}
}

func TestThrottleScheduleFollowsBusinessHours(t *testing.T) {
	s := vmThrottleSchedule{LimitMbps: 0, BusinessMbps: 100, BusinessStart: 8, BusinessEnd: 18}
	monday9 := time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)
	if !s.Business(monday9) || s.BytesPerSecond(monday9) != 12_500_000 {
		t.Fatal("Monday 09:00 is business hours at 100 Mbps")
	}
	if s.Business(monday9.Add(10*time.Hour)) || s.BytesPerSecond(monday9.Add(10*time.Hour)) != 0 {
		t.Fatal("Monday 19:00 is not business hours")
	}
	if s.Business(monday9.AddDate(0, 0, -1)) {
		t.Fatal("Sunday is not a business day by default")
	}
	night := vmThrottleSchedule{BusinessMbps: 10, BusinessStart: 22, BusinessEnd: 6, BusinessDays: []int{0, 1, 2, 3, 4, 5, 6}}
	if !night.Business(monday9.Add(-8*time.Hour)) || night.Business(monday9) {
		t.Fatal("a window across midnight is wrong")
	}
	if (vmThrottleSchedule{LimitMbps: -1}).Validate() == nil || (vmThrottleSchedule{BusinessDays: []int{7}}).Validate() == nil {
		t.Fatal("bad schedules were accepted")
	}
}

func TestRescanSendsOnlyWhatDiffersOnADiskThatHoldsData(t *testing.T) {
	// Failback: the original server's disk still holds almost the same
	// data. A rescan lets the copy send only the chunks that differ.
	size := int64(36 << 20)
	original := vmTestDiskBytes(size)
	current := append([]byte(nil), original...)
	rand.Read(current[5<<20 : 5<<20+100])
	rand.Read(current[30<<20 : 30<<20+100])
	target := &vmMemoryDevice{data: append([]byte(nil), original...), id: "serial-F"}
	disks := []vmReceiverDisk{{Name: "disk-0", Label: "original", Size: size, SectorSize: 512, Overwrite: true, open: func() (vmBlockDevice, error) { target.closed = false; return target, nil }}}
	receiver, err := startVMReceiverWith("127.0.0.1:0", vmReceiverConfig{Disks: disks, Records: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	address := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	ctx := context.Background()
	sink, err := newVMRemoteSink(ctx, address, receiver.Thumbprint(), receiver.Code(), "disk-0")
	if err != nil {
		t.Fatal(err)
	}
	sink.Rescan, sink.delay = true, 10*time.Millisecond
	progressed := false
	sink.RescanProgress = func(done, total int) { progressed = progressed || done == total }
	target.written = 0
	result, err := vmCopyTo(ctx, &vmBytesSource{data: current, name: "new server disk 0"}, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if !progressed || !result.State.Verified || !bytes.Equal(target.data, current) {
		t.Fatal("the failback copy does not match")
	}
	if target.written != 2<<20 {
		t.Fatalf("wrote %d bytes; only the 2 changed chunks should travel", target.written)
	}
}
