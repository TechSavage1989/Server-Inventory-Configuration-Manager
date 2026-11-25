package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The disk copy engine copies a source disk into a dynamic VHDX in fixed
// chunks and records each chunk's SHA-256 in a state file beside the target.
// A stopped copy resumes where it stopped; a repeat run over a finished copy
// is a delta pass that rewrites only the chunks whose hash changed, so a
// final pass after the source VM is shut down is short. Zero chunks are not
// written, which keeps the VHDX sparse.

const (
	vmCopyChunkSize    = 4 << 20
	vmCopyStateVersion = 1
	vmCopyZeroHash     = "zero"
	vmCopyReadRetries  = 4
)

type vmCopyState struct {
	Version   int      `json:"version"`
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Size      int64    `json:"size"`
	ChunkSize int64    `json:"chunkSize"`
	Hashes    []string `json:"hashes"`
	Passes    int      `json:"passes"`
	// ChangeID is the vSphere changed-block tracking ID the copy matches,
	// so the next round asks only for blocks changed since then.
	ChangeID string    `json:"changeId,omitempty"`
	Complete bool      `json:"complete"`
	Verified bool      `json:"verified"`
	Updated  time.Time `json:"updated"`
}

type vmCopyProgress struct {
	Pass         int   `json:"pass"`
	Chunks       int   `json:"chunks"`
	Done         int   `json:"done"`
	BytesRead    int64 `json:"bytesRead"`
	BytesWritten int64 `json:"bytesWritten"`
	Changed      int   `json:"changed"`
	Zero         int   `json:"zero"`
	// BytesSent is what crossed the network after compression, when the
	// sink sends over a network.
	BytesSent int64 `json:"bytesSent,omitempty"`
}

// vmSentCounter is a sink that counts the bytes it sent over the network.
type vmSentCounter interface {
	SentBytes() int64
}

type vmCopyOptions struct {
	ChunkSize int64
	// Verify reads the finished VHDX back and compares every chunk hash.
	Verify   bool
	Progress func(vmCopyProgress)
	// Ranges limits a delta pass to these byte ranges (from changed block
	// tracking); nil means read the whole disk.
	Ranges []vmCopyRange
	// ZeroOutside says that, on a first pass with Ranges, everything outside
	// the ranges is known to be unallocated (zero) on the source.
	ZeroOutside bool
	// ChangeID is recorded when the pass completes.
	ChangeID string
	// retryDelay is shortened by tests.
	retryDelay time.Duration
}

type vmCopyRange struct {
	Start  int64 `json:"start"`
	Length int64 `json:"length"`
}

type vmCopyResult struct {
	State    vmCopyState    `json:"state"`
	Progress vmCopyProgress `json:"progress"`
	Resumed  bool           `json:"resumed"`
}

func vmCopyStatePath(target string) string { return target + ".copy.json" }

func loadVMCopyState(target string) (*vmCopyState, error) {
	raw, err := os.ReadFile(vmCopyStatePath(target))
	if err != nil {
		return nil, err
	}
	state := &vmCopyState{}
	if err := json.Unmarshal(raw, state); err != nil {
		return nil, fmt.Errorf("the copy record %s is damaged: %w", vmCopyStatePath(target), err)
	}
	if state.Version != vmCopyStateVersion || state.ChunkSize <= 0 || int64(len(state.Hashes)) != (state.Size+state.ChunkSize-1)/state.ChunkSize {
		return nil, fmt.Errorf("the copy record %s does not match this version", vmCopyStatePath(target))
	}
	return state, nil
}

func saveVMCopyState(state *vmCopyState) error {
	state.Updated = time.Now().UTC()
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	name := vmCopyStatePath(state.Target)
	temporary := name + ".tmp"
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, name)
}

// vmCopySink is where copied chunks go: a VHDX on this computer, or one on
// a receiver across the network. The sink keeps the authoritative copy
// record; the engine works on the copy Open returns.
type vmCopySink interface {
	// Open creates or resumes the target for this source.
	Open(source string, size, chunkSize int64) (state *vmCopyState, resumed bool, err error)
	// Write stores one chunk whose hash changed. A zero chunk overwrites
	// earlier data only where some was written. It returns bytes written.
	Write(index int, data []byte, hash string) (int64, error)
	// Checkpoint saves progress so a stopped copy resumes here.
	Checkpoint(state *vmCopyState) error
	// Finish flushes, optionally verifies every chunk and saves the record.
	Finish(ctx context.Context, state *vmCopyState, verify bool) (*vmCopyState, error)
	Close() error
}

// vmLocalSink writes a VHDX file and its copy record on this computer.
type vmLocalSink struct {
	target string
	disk   *vhdxFile
	state  *vmCopyState
}

func (s *vmLocalSink) Open(source string, size, chunkSize int64) (*vmCopyState, bool, error) {
	target, err := filepath.Abs(s.target)
	if err != nil {
		return nil, false, err
	}
	s.target = target
	state, stateErr := loadVMCopyState(target)
	_, targetErr := os.Stat(target)
	switch {
	case targetErr == nil && stateErr == nil:
		if state.Size != size || state.ChunkSize != chunkSize {
			return nil, false, fmt.Errorf("%s was copied from a %s disk; this source is %s. Choose a new target file", target, vmFormatBytes(state.Size), vmFormatBytes(size))
		}
		if state.Source != source {
			return nil, false, fmt.Errorf("%s was copied from %s, not %s. Choose a new target file", target, state.Source, source)
		}
		disk, err := openVHDX(target, false)
		if err != nil {
			return nil, false, err
		}
		if disk.Size() != size {
			disk.Close()
			return nil, false, fmt.Errorf("%s does not match its copy record", target)
		}
		s.disk, s.state = disk, state
		return vmCopyStateClone(state), true, nil
	case targetErr == nil:
		return nil, false, fmt.Errorf("%s already exists and was not created by this copy; choose a new file name so nothing is overwritten", target)
	case !os.IsNotExist(targetErr):
		return nil, false, targetErr
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return nil, false, err
	}
	disk, err := createVHDX(target, size)
	if err != nil {
		return nil, false, err
	}
	chunks := (size + chunkSize - 1) / chunkSize
	state = &vmCopyState{Version: vmCopyStateVersion, Source: source, Target: target, Size: size, ChunkSize: chunkSize, Hashes: make([]string, chunks)}
	if err := saveVMCopyState(state); err != nil {
		disk.Close()
		os.Remove(target)
		return nil, false, err
	}
	s.disk, s.state = disk, state
	return vmCopyStateClone(state), false, nil
}

func vmCopyStateClone(state *vmCopyState) *vmCopyState {
	clone := *state
	clone.Hashes = append([]string(nil), state.Hashes...)
	return &clone
}

func (s *vmLocalSink) Write(index int, data []byte, hash string) (int64, error) {
	if index < 0 || index >= len(s.state.Hashes) {
		return 0, fmt.Errorf("chunk %d is outside the disk", index)
	}
	offset := int64(index) * s.state.ChunkSize
	want := s.state.ChunkSize
	if want > s.state.Size-offset {
		want = s.state.Size - offset
	}
	if int64(len(data)) != want {
		return 0, fmt.Errorf("chunk %d is %d bytes, not %d", index, len(data), want)
	}
	written := int64(0)
	if hash != vmCopyZeroHash || s.disk.blockOffset(offset/s.disk.blockSize) != 0 {
		if _, err := s.disk.WriteAt(data, offset); err != nil {
			return 0, fmt.Errorf("writing %s: %w", s.target, err)
		}
		written = int64(len(data))
	}
	s.state.Hashes[index] = hash
	return written, nil
}

func (s *vmLocalSink) Checkpoint(state *vmCopyState) error {
	if err := s.disk.Flush(); err != nil {
		return err
	}
	s.state.Passes, s.state.Complete, s.state.Verified = state.Passes, false, false
	return saveVMCopyState(s.state)
}

func (s *vmLocalSink) Finish(ctx context.Context, state *vmCopyState, verify bool) (*vmCopyState, error) {
	if err := s.disk.Flush(); err != nil {
		return nil, err
	}
	for index, hash := range s.state.Hashes {
		if hash == "" || hash != state.Hashes[index] {
			s.Checkpoint(state)
			return nil, errors.New("some chunks were never copied; run a full pass without changed-block ranges first")
		}
	}
	s.state.Passes, s.state.ChangeID, s.state.Complete, s.state.Verified = state.Passes, state.ChangeID, true, false
	if verify {
		if err := vmVerifyCopy(ctx, s.disk, s.state); err != nil {
			saveVMCopyState(s.state)
			return nil, err
		}
		s.state.Verified = true
	}
	err := s.disk.Close()
	s.disk = nil
	if err != nil {
		return nil, err
	}
	if err := saveVMCopyState(s.state); err != nil {
		return nil, err
	}
	return vmCopyStateClone(s.state), nil
}

func (s *vmLocalSink) Close() error {
	if s.disk == nil {
		return nil
	}
	if s.state != nil {
		saveVMCopyState(s.state)
	}
	err := s.disk.Close()
	s.disk = nil
	return err
}

// vmCopyDisk copies source into the VHDX at target, creating it or resuming
// and updating an earlier copy of the same source.
func vmCopyDisk(ctx context.Context, source vmCopySource, target string, options vmCopyOptions) (*vmCopyResult, error) {
	return vmCopyTo(ctx, source, &vmLocalSink{target: target}, options)
}

// vmCopyTo copies source into a sink in hashed chunks.
func vmCopyTo(ctx context.Context, source vmCopySource, sink vmCopySink, options vmCopyOptions) (*vmCopyResult, error) {
	if options.ChunkSize <= 0 {
		options.ChunkSize = vmCopyChunkSize
	}
	if options.ChunkSize%vhdxLogicalSector != 0 {
		return nil, errors.New("the chunk size must be a whole number of sectors")
	}
	if options.retryDelay == 0 {
		options.retryDelay = 2 * time.Second
	}
	size := source.Size()
	if size <= 0 {
		return nil, errors.New("the source disk is empty")
	}
	if size%vhdxLogicalSector != 0 {
		return nil, fmt.Errorf("the source is %d bytes, not a whole number of 512-byte sectors", size)
	}
	state, resumed, err := sink.Open(source.Describe(), size, options.ChunkSize)
	if err != nil {
		return nil, err
	}
	defer sink.Close()
	result := &vmCopyResult{Resumed: resumed}

	state.Passes++
	state.Complete, state.Verified = false, false
	progress := vmCopyProgress{Pass: state.Passes, Chunks: len(state.Hashes)}
	selected := vmCopySelected(state, options.Ranges)
	if options.ZeroOutside && options.Ranges != nil {
		inRanges := vmCopyInRanges(state, options.Ranges)
		for index, hash := range state.Hashes {
			if hash == "" && !inRanges[index] {
				// Never allocated on the source: the new VHDX already
				// reads zeros here, so record it without sending anything.
				if _, err := sink.Write(index, make([]byte, vmChunkLength(state, index)), vmCopyZeroHash); err != nil {
					return nil, err
				}
				state.Hashes[index] = vmCopyZeroHash
			}
		}
		selected = vmCopySelected(state, options.Ranges)
	}
	buffer := make([]byte, options.ChunkSize)
	lastSave := time.Now()
	sentBase := int64(0)
	counter, counts := sink.(vmSentCounter)
	if counts {
		sentBase = counter.SentBytes()
	}
	report := func() {
		if counts {
			progress.BytesSent = counter.SentBytes() - sentBase
		}
		if options.Progress != nil {
			options.Progress(progress)
		}
	}
	for index := range state.Hashes {
		if !selected[index] {
			progress.Done++
			continue
		}
		if err := ctx.Err(); err != nil {
			sink.Checkpoint(state)
			return nil, err
		}
		offset := int64(index) * options.ChunkSize
		chunk := buffer[:vmChunkLength(state, index)]
		if err := vmReadFull(ctx, source, chunk, offset, options.retryDelay); err != nil {
			sink.Checkpoint(state)
			return nil, fmt.Errorf("reading the source at %s: %w", vmFormatBytes(offset), err)
		}
		progress.BytesRead += int64(len(chunk))
		hash := vmCopyZeroHash
		if !vmIsZero(chunk) {
			sum := sha256.Sum256(chunk)
			hash = hex.EncodeToString(sum[:])
		} else {
			progress.Zero++
		}
		previous := state.Hashes[index]
		if hash != previous {
			written, err := sink.Write(index, chunk, hash)
			if err != nil {
				sink.Checkpoint(state)
				return nil, err
			}
			progress.BytesWritten += written
			if previous != "" {
				progress.Changed++
			}
			state.Hashes[index] = hash
		}
		progress.Done++
		if time.Since(lastSave) > 5*time.Second {
			if err := sink.Checkpoint(state); err != nil {
				return nil, err
			}
			lastSave = time.Now()
			report()
		}
	}
	for _, hash := range state.Hashes {
		if hash == "" {
			sink.Checkpoint(state)
			return nil, errors.New("some chunks were never copied; run a full pass without changed-block ranges first")
		}
	}
	state.Complete = true
	state.ChangeID = options.ChangeID
	report()
	final, err := sink.Finish(ctx, state, options.Verify)
	if err != nil {
		return nil, err
	}
	result.State, result.Progress = *final, progress
	return result, nil
}

func vmChunkLength(state *vmCopyState, index int) int64 {
	offset := int64(index) * state.ChunkSize
	if state.ChunkSize > state.Size-offset {
		return state.Size - offset
	}
	return state.ChunkSize
}

// vmCopyInRanges marks the chunks that overlap the given byte ranges.
func vmCopyInRanges(state *vmCopyState, ranges []vmCopyRange) []bool {
	marked := make([]bool, len(state.Hashes))
	for _, r := range ranges {
		if r.Length <= 0 || r.Start < 0 || r.Start >= state.Size {
			continue
		}
		last := r.Start + r.Length - 1
		if last >= state.Size {
			last = state.Size - 1
		}
		for index := r.Start / state.ChunkSize; index <= last/state.ChunkSize; index++ {
			marked[index] = true
		}
	}
	return marked
}

// vmCopySelected marks the chunks a pass reads: all of them, or the chunks
// that overlap the changed ranges plus any never copied.
func vmCopySelected(state *vmCopyState, ranges []vmCopyRange) []bool {
	selected := vmCopyInRanges(state, ranges)
	for index, hash := range state.Hashes {
		selected[index] = selected[index] || ranges == nil || hash == ""
	}
	return selected
}

func vmReadFull(ctx context.Context, source io.ReaderAt, p []byte, offset int64, delay time.Duration) error {
	var err error
	for attempt := 0; attempt < vmCopyReadRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay * time.Duration(attempt)):
			}
		}
		var n int
		n, err = source.ReadAt(p, offset)
		if n == len(p) {
			return nil
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}

// vmVerifyCopy reads the copy (a VHDX or a raw disk) back and checks every
// chunk against the hash recorded when it was copied.
func vmVerifyCopy(ctx context.Context, disk io.ReaderAt, state *vmCopyState) error {
	buffer := make([]byte, state.ChunkSize)
	for index, want := range state.Hashes {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset := int64(index) * state.ChunkSize
		length := state.ChunkSize
		if length > state.Size-offset {
			length = state.Size - offset
		}
		chunk := buffer[:length]
		if _, err := disk.ReadAt(chunk, offset); err != nil && err != io.EOF {
			return err
		}
		got := vmCopyZeroHash
		if !vmIsZero(chunk) {
			sum := sha256.Sum256(chunk)
			got = hex.EncodeToString(sum[:])
		}
		if got != want {
			return fmt.Errorf("verification failed at %s: the copy does not match the source", vmFormatBytes(offset))
		}
	}
	return nil
}

// vmRescanHashes replaces a copy record's hashes with hashes of what the
// target holds now. It is used when the target may have changed outside the
// copy (a test failover booted it, or a failback copies back onto the
// original disk): the next pass then sends exactly the chunks that differ.
func vmRescanHashes(ctx context.Context, disk io.ReaderAt, state *vmCopyState, progress func(done, total int)) error {
	buffer := make([]byte, state.ChunkSize)
	for index := range state.Hashes {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := buffer[:vmChunkLength(state, index)]
		if _, err := disk.ReadAt(chunk, int64(index)*state.ChunkSize); err != nil && err != io.EOF {
			return fmt.Errorf("reading the target at %s: %w", vmFormatBytes(int64(index)*state.ChunkSize), err)
		}
		hash := vmCopyZeroHash
		if !vmIsZero(chunk) {
			sum := sha256.Sum256(chunk)
			hash = hex.EncodeToString(sum[:])
		}
		state.Hashes[index] = hash
		if progress != nil && (index%64 == 0 || index == len(state.Hashes)-1) {
			progress(index+1, len(state.Hashes))
		}
	}
	state.Complete, state.Verified, state.ChangeID = false, false, ""
	return nil
}

// vmRescanner is a sink whose record can be rebuilt from the target's content.
type vmRescanner interface {
	Rescan(ctx context.Context, progress func(done, total int)) error
}

func (s *vmLocalSink) Rescan(ctx context.Context, progress func(done, total int)) error {
	if s.disk == nil || s.state == nil {
		return errors.New("the copy is not open")
	}
	if err := vmRescanHashes(ctx, s.disk, s.state, progress); err != nil {
		return err
	}
	return saveVMCopyState(s.state)
}
