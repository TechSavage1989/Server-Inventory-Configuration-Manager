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

// vmCopyDisk copies source into the VHDX at target, creating it or resuming
// and updating an earlier copy of the same source.
func vmCopyDisk(ctx context.Context, source vmCopySource, target string, options vmCopyOptions) (*vmCopyResult, error) {
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
	target, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	result := &vmCopyResult{}
	state, stateErr := loadVMCopyState(target)
	_, targetErr := os.Stat(target)
	var disk *vhdxFile
	switch {
	case targetErr == nil && stateErr == nil:
		if state.Size != size || state.ChunkSize != options.ChunkSize {
			return nil, fmt.Errorf("%s was copied from a %s disk; this source is %s. Choose a new target file", target, vmFormatBytes(state.Size), vmFormatBytes(size))
		}
		if state.Source != source.Describe() {
			return nil, fmt.Errorf("%s was copied from %s, not %s. Choose a new target file", target, state.Source, source.Describe())
		}
		if disk, err = openVHDX(target, false); err != nil {
			return nil, err
		}
		if disk.Size() != size {
			disk.Close()
			return nil, fmt.Errorf("%s does not match its copy record", target)
		}
		result.Resumed = true
	case targetErr == nil:
		return nil, fmt.Errorf("%s already exists and was not created by this copy; choose a new file name so nothing is overwritten", target)
	case !os.IsNotExist(targetErr):
		return nil, targetErr
	default:
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		if disk, err = createVHDX(target, size); err != nil {
			return nil, err
		}
		chunks := (size + options.ChunkSize - 1) / options.ChunkSize
		state = &vmCopyState{Version: vmCopyStateVersion, Source: source.Describe(), Target: target, Size: size, ChunkSize: options.ChunkSize, Hashes: make([]string, chunks)}
		if err := saveVMCopyState(state); err != nil {
			disk.Close()
			os.Remove(target)
			return nil, err
		}
	}
	defer func() {
		if disk != nil {
			disk.Close()
		}
	}()

	state.Passes++
	state.Complete, state.Verified = false, false
	progress := vmCopyProgress{Pass: state.Passes, Chunks: len(state.Hashes)}
	selected := vmCopySelected(state, options.Ranges)
	if options.ZeroOutside && options.Ranges != nil {
		inRanges := vmCopyInRanges(state, options.Ranges)
		for index, hash := range state.Hashes {
			if hash == "" && !inRanges[index] {
				state.Hashes[index] = vmCopyZeroHash
			}
		}
		selected = vmCopySelected(state, options.Ranges)
	}
	buffer := make([]byte, options.ChunkSize)
	lastSave := time.Now()
	report := func() {
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
			saveVMCopyState(state)
			return nil, err
		}
		offset := int64(index) * options.ChunkSize
		length := options.ChunkSize
		if length > size-offset {
			length = size - offset
		}
		chunk := buffer[:length]
		if err := vmReadFull(ctx, source, chunk, offset, options.retryDelay); err != nil {
			saveVMCopyState(state)
			return nil, fmt.Errorf("reading the source at %s: %w", vmFormatBytes(offset), err)
		}
		progress.BytesRead += length
		hash := vmCopyZeroHash
		if !vmIsZero(chunk) {
			sum := sha256.Sum256(chunk)
			hash = hex.EncodeToString(sum[:])
		} else {
			progress.Zero++
		}
		previous := state.Hashes[index]
		if hash != previous {
			// A chunk that became zero still has to overwrite old data; a
			// never-written zero chunk stays unallocated.
			if hash != vmCopyZeroHash || (previous != "" && disk.blockOffset(offset/disk.blockSize) != 0) {
				if _, err := disk.WriteAt(chunk, offset); err != nil {
					saveVMCopyState(state)
					return nil, fmt.Errorf("writing %s: %w", target, err)
				}
				progress.BytesWritten += length
			}
			if previous != "" {
				progress.Changed++
			}
			state.Hashes[index] = hash
		}
		progress.Done++
		if time.Since(lastSave) > 5*time.Second {
			if err := disk.Flush(); err != nil {
				return nil, err
			}
			if err := saveVMCopyState(state); err != nil {
				return nil, err
			}
			lastSave = time.Now()
			report()
		}
	}
	if err := disk.Flush(); err != nil {
		return nil, err
	}
	for _, hash := range state.Hashes {
		if hash == "" {
			saveVMCopyState(state)
			return nil, errors.New("some chunks were never copied; run a full pass without changed-block ranges first")
		}
	}
	state.Complete = true
	state.ChangeID = options.ChangeID
	report()
	if options.Verify {
		if err := vmVerifyCopy(ctx, disk, state); err != nil {
			saveVMCopyState(state)
			return nil, err
		}
		state.Verified = true
	}
	if err := disk.Close(); err != nil {
		return nil, err
	}
	disk = nil
	if err := saveVMCopyState(state); err != nil {
		return nil, err
	}
	result.State, result.Progress = *state, progress
	return result, nil
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

// vmVerifyCopy reads the VHDX back and checks every chunk against the hash
// recorded when it was copied.
func vmVerifyCopy(ctx context.Context, disk *vhdxFile, state *vmCopyState) error {
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
