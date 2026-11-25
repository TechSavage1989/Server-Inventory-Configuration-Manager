package main

import (
	"sort"
	"sync"
	"time"
)

// vmTransfer is one disk being copied, as the Disk copy, Move and
// replication views show it: source and target, per-disk progress, bytes,
// throughput, time left and the result. It never holds a pairing code,
// password or key.
type vmTransfer struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"` // send, move, receive
	Label        string    `json:"label"`
	Source       string    `json:"source"`
	Target       string    `json:"target"`
	Peer         string    `json:"peer,omitempty"`
	Size         int64     `json:"size"`
	Pass         int       `json:"pass"`
	Chunks       int       `json:"chunks"`
	Done         int       `json:"done"`
	BytesRead    int64     `json:"bytesRead"`
	BytesWritten int64     `json:"bytesWritten"`
	BytesSent    int64     `json:"bytesSent"`
	Started      time.Time `json:"started"`
	Updated      time.Time `json:"updated"`
	Finished     time.Time `json:"finished,omitempty"`
	State        string    `json:"state"` // running, done, failed, stopped
	Verified     bool      `json:"verified"`
	Error        string    `json:"error,omitempty"`
	// Computed when listed.
	Percent    float64 `json:"percent"`
	Rate       float64 `json:"bytesPerSecond"`
	ETASeconds int64   `json:"etaSeconds"`
}

// vmRateMeter smooths a running total into a rate per second.
type vmRateMeter struct {
	rate  float64
	lastT time.Time
	lastV int64
}

func (m *vmRateMeter) Add(now time.Time, total int64) {
	if m.lastT.IsZero() || total < m.lastV {
		m.lastT, m.lastV = now, total
		return
	}
	elapsed := now.Sub(m.lastT).Seconds()
	if elapsed < 1 {
		return
	}
	sample := float64(total-m.lastV) / elapsed
	if m.rate == 0 {
		m.rate = sample
	} else {
		m.rate = 0.7*m.rate + 0.3*sample
	}
	m.lastT, m.lastV = now, total
}

// Rate is zero once nothing has moved for a while, so a stalled copy does
// not show a stale speed.
func (m *vmRateMeter) Rate(now time.Time) float64 {
	if m.lastT.IsZero() || now.Sub(m.lastT) > 30*time.Second {
		return 0
	}
	return m.rate
}

type vmTransferEntry struct {
	vmTransfer
	bytes  vmRateMeter
	chunks vmRateMeter
}

// vmTransferBoard keeps the transfers of one view: running ones, and
// finished ones for an hour so the result stays visible.
type vmTransferBoard struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*vmTransferEntry
}

const vmTransferKeep = time.Hour
const vmTransferMax = 64

func newVMTransferBoard() *vmTransferBoard {
	return &vmTransferBoard{now: time.Now, entries: map[string]*vmTransferEntry{}}
}

func (b *vmTransferBoard) clock() time.Time {
	if b.now == nil {
		return time.Now()
	}
	return b.now()
}

// Begin starts (or restarts) a transfer.
func (b *vmTransferBoard) Begin(t vmTransfer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	t.Started, t.Updated, t.State, t.Finished, t.Error = now, now, "running", time.Time{}, ""
	b.entries[t.ID] = &vmTransferEntry{vmTransfer: t}
	b.trimLocked(now)
}

// Progress records the copy engine's progress for a transfer.
func (b *vmTransferBoard) Progress(id string, p vmCopyProgress) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entries[id]
	if e == nil {
		return
	}
	now := b.clock()
	e.Pass, e.Chunks, e.Done, e.BytesRead, e.BytesWritten, e.BytesSent, e.Updated = p.Pass, p.Chunks, p.Done, p.BytesRead, p.BytesWritten, p.BytesSent, now
	e.bytes.Add(now, p.BytesRead)
	e.chunks.Add(now, int64(p.Done))
}

// End records how a transfer finished.
func (b *vmTransferBoard) End(id string, err error, stopped, verified bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entries[id]
	if e == nil {
		return
	}
	now := b.clock()
	e.Finished, e.Updated, e.Verified = now, now, verified
	switch {
	case stopped:
		e.State = "stopped"
	case err != nil:
		e.State, e.Error = "failed", err.Error()
	default:
		e.State = "done"
		if e.Chunks > 0 {
			e.Done = e.Chunks
		}
	}
}

func (b *vmTransferBoard) trimLocked(now time.Time) {
	for id, e := range b.entries {
		if e.State != "running" && now.Sub(e.Finished) > vmTransferKeep {
			delete(b.entries, id)
		}
	}
	for len(b.entries) > vmTransferMax {
		oldest := ""
		for id, e := range b.entries {
			if e.State != "running" && (oldest == "" || e.Finished.Before(b.entries[oldest].Finished)) {
				oldest = id
			}
		}
		if oldest == "" {
			return
		}
		delete(b.entries, oldest)
	}
}

// List returns every transfer, running first, newest first, with percent,
// throughput and time left filled in.
func (b *vmTransferBoard) List() []vmTransfer {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	b.trimLocked(now)
	list := make([]vmTransfer, 0, len(b.entries))
	for _, e := range b.entries {
		t := e.vmTransfer
		vmTransferFill(&t, e.bytes.Rate(now), e.chunks.Rate(now))
		list = append(list, t)
	}
	vmSortTransfers(list)
	return list
}

func vmTransferFill(t *vmTransfer, byteRate, chunkRate float64) {
	if t.Chunks > 0 {
		t.Percent = float64(t.Done) * 100 / float64(t.Chunks)
	}
	if t.State != "running" {
		t.Rate, t.ETASeconds = 0, 0
		if t.State == "done" {
			t.Percent = 100
		}
		return
	}
	t.Rate = byteRate
	if remaining := t.Chunks - t.Done; remaining > 0 && chunkRate > 0 {
		t.ETASeconds = int64(float64(remaining)/chunkRate + 0.5)
	}
}

func vmSortTransfers(list []vmTransfer) {
	sort.SliceStable(list, func(i, j int) bool {
		ri, rj := list[i].State == "running", list[j].State == "running"
		if ri != rj {
			return ri
		}
		if !list[i].Started.Equal(list[j].Started) {
			return list[i].Started.After(list[j].Started)
		}
		return list[i].ID < list[j].ID
	})
}

// Report starts the transfer on its first progress report (a move learns
// each disk's size only when its copy starts) and records the progress.
func (b *vmTransferBoard) Report(base vmTransfer, p vmCopyProgress) {
	b.mu.Lock()
	e := b.entries[base.ID]
	fresh := e == nil || e.State != "running"
	b.mu.Unlock()
	if fresh {
		b.Begin(base)
	}
	b.Progress(base.ID, p)
}

// EndRunning ends every running transfer whose ID starts with prefix: a
// round's disks finish together when the round ends.
func (b *vmTransferBoard) EndRunning(prefix string, err error, stopped, verified bool) {
	b.mu.Lock()
	ids := []string{}
	for id, e := range b.entries {
		if e.State == "running" && len(id) >= len(prefix) && id[:len(prefix)] == prefix {
			ids = append(ids, id)
		}
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.End(id, err, stopped, verified)
	}
}
