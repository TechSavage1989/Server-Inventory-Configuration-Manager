package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTransferBoardShowsProgressSpeedAndTimeLeft(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	board := newVMTransferBoard()
	board.now = func() time.Time { return now }
	board.Begin(vmTransfer{ID: "send-0", Kind: "send", Source: "SRV, disk 0", Target: "srv-disk0.vhdx", Peer: "192.0.2.40:44380", Size: 100 << 20})
	board.Progress("send-0", vmCopyProgress{Chunks: 100, Done: 10, BytesRead: 10 << 20})
	now = now.Add(10 * time.Second)
	board.Progress("send-0", vmCopyProgress{Chunks: 100, Done: 30, BytesRead: 30 << 20, BytesSent: 12 << 20})
	list := board.List()
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
	got := list[0]
	if got.State != "running" || got.Percent != 30 || got.Rate != 2<<20 || got.ETASeconds != 35 || got.BytesSent != 12<<20 || got.Peer != "192.0.2.40:44380" {
		t.Fatalf("transfer %+v", got)
	}
	// A copy that stops moving shows no stale speed.
	now = now.Add(time.Minute)
	if got := board.List()[0]; got.Rate != 0 || got.ETASeconds != 0 {
		t.Fatalf("stalled transfer %+v", got)
	}

	board.Report(vmTransfer{ID: "move-vm|Hard disk 1", Kind: "move", Source: "APP01"}, vmCopyProgress{Chunks: 4, Done: 1})
	board.Report(vmTransfer{ID: "move-vm|Hard disk 2", Kind: "move", Source: "APP01"}, vmCopyProgress{Chunks: 4, Done: 4})
	board.EndRunning("move-vm|", nil, false, true)
	board.End("send-0", errors.New("receiver went away"), false, false)
	for _, item := range board.List() {
		switch {
		case strings.HasPrefix(item.ID, "move-") && (item.State != "done" || item.Percent != 100 || !item.Verified):
			t.Fatalf("finished move %+v", item)
		case item.ID == "send-0" && (item.State != "failed" || item.Error != "receiver went away" || item.Rate != 0):
			t.Fatalf("failed send %+v", item)
		}
	}
	// Finished transfers stay for an hour, then go.
	now = now.Add(vmTransferKeep + time.Minute)
	if list := board.List(); len(list) != 0 {
		t.Fatalf("old transfers kept: %+v", list)
	}
}

func TestReceiverListsConnectedSendersAndFinishedCopies(t *testing.T) {
	receiver, err := startVMReceiver("127.0.0.1:0", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	address := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	data := vmTestDiskBytes(48 << 20)
	source := &vmBytesSource{data: data, name: "SRV01 disk 0"}
	sink, err := newVMRemoteSink(context.Background(), address, receiver.Thumbprint(), receiver.Code(), "srv01-disk0.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	watch := &vmWatchSource{vmCopySource: source, after: 6, look: receiver.Transfers}
	if _, err = vmCopyTo(context.Background(), watch, sink, vmCopyOptions{ChunkSize: 1 << 20, Verify: true}); err != nil {
		t.Fatal(err)
	}
	during := watch.seen
	if len(during) != 1 || during[0].State != "running" || during[0].Peer != "127.0.0.1" || during[0].Target != "srv01-disk0.vhdx" || during[0].Source != "SRV01 disk 0" || during[0].Chunks != 48 || during[0].Done < 3 {
		t.Fatalf("while receiving %+v", during)
	}
	after := receiver.Transfers()
	if len(after) != 1 || after[0].State != "done" || !after[0].Verified || after[0].Percent != 100 || after[0].BytesWritten == 0 {
		t.Fatalf("after receiving %+v", after)
	}
	for _, item := range append(during, after...) {
		if strings.Contains(item.Source+item.Target+item.Label+item.Error, receiver.Code()) {
			t.Fatal("the pairing code reached the transfer list")
		}
	}
}

// vmWatchSource calls look once, after a number of reads, while the copy is
// still running.
type vmWatchSource struct {
	vmCopySource
	after, reads int
	look         func() []vmTransfer
	seen         []vmTransfer
}

func (w *vmWatchSource) ReadAt(p []byte, off int64) (int, error) {
	w.reads++
	if w.reads == w.after {
		w.seen = w.look()
	}
	return w.vmCopySource.ReadAt(p, off)
}
