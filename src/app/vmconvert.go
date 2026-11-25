package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The disk converter: any readable image (vmdisk_*.go) to any written
// format, through the copy engine so a conversion is chunk-hashed, resumes
// after a stop, repeats as a delta pass that rewrites only changed chunks,
// and can verify the result by reading it back. Empty areas of a sparse
// source are never read on the first pass.

// vmDiskWriter is an output image being written.
type vmDiskWriter interface {
	io.ReaderAt
	// WriteAt stores guest bytes. Zero data is not stored where nothing
	// was written before, so outputs stay sparse.
	WriteAt(p []byte, off int64) (int, error)
	Size() int64
	Flush() error
	Close() error
}

// createDiskWriter makes a new output image; it never replaces a file.
func createDiskWriter(format, path string, size int64) (vmDiskWriter, error) {
	switch format {
	case vmDiskFormatVHDX:
		return createVHDX(path, size)
	case vmDiskFormatVHD:
		return createVHD(path, size, false)
	case vmDiskFormatVHDFixed:
		// Never marked sparse: Windows and Hyper-V refuse to mount a sparse
		// VHD ("must not be sparse").
		return createVHD(path, size, true)
	case vmDiskFormatQCOW2:
		return createQCOW2(path, size)
	case vmDiskFormatVMDK:
		return createVMDKSparse(path, size)
	case vmDiskFormatVMDKFlat:
		return createVMDKFlat(path, size)
	case vmDiskFormatRaw:
		return createRawWriter(path, size)
	}
	return nil, fmt.Errorf("the output format %q is not supported", format)
}

// openDiskWriter reopens an output this converter created.
func openDiskWriter(format, path string) (vmDiskWriter, error) {
	switch format {
	case vmDiskFormatVHDX:
		return openVHDX(path, false)
	case vmDiskFormatVHD, vmDiskFormatVHDFixed:
		writer, err := openVHDWriter(path)
		if err == nil && (writer.diskType == vhdTypeFixed) != (format == vmDiskFormatVHDFixed) {
			writer.Close()
			return nil, fmt.Errorf("%s is a %s VHD, not %s", path, map[bool]string{true: "fixed", false: "dynamic"}[writer.diskType == vhdTypeFixed], vmDiskFormatTitle(format))
		}
		return writer, err
	case vmDiskFormatQCOW2:
		return openQCOW2Writer(path)
	case vmDiskFormatVMDK:
		return openVMDKSparseWriter(path)
	case vmDiskFormatVMDKFlat:
		return openVMDKFlatWriter(path)
	case vmDiskFormatRaw:
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		return openRawWriter(path, info.Size())
	}
	return nil, fmt.Errorf("the output format %q is not supported", format)
}

// Raw output.

type vmRawWriter struct {
	file *os.File
	size int64
}

func createRawWriter(path string, size int64) (*vmRawWriter, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	vmMarkSparse(file)
	if err := file.Truncate(size); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	return &vmRawWriter{file: file, size: size}, nil
}

func openRawWriter(path string, size int64) (*vmRawWriter, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() != size {
		file.Close()
		if err == nil {
			err = fmt.Errorf("%s is %d bytes, not the %d its copy record expects", path, info.Size(), size)
		}
		return nil, err
	}
	return &vmRawWriter{file: file, size: size}, nil
}

func (w *vmRawWriter) ReadAt(p []byte, off int64) (int, error) { return w.file.ReadAt(p, off) }
func (w *vmRawWriter) Size() int64                             { return w.size }
func (w *vmRawWriter) Flush() error                            { return w.file.Sync() }
func (w *vmRawWriter) Close() error                            { return w.file.Close() }

func (w *vmRawWriter) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > w.size {
		return 0, fmt.Errorf("write at %d+%d is outside the %d-byte disk", off, len(p), w.size)
	}
	return vmWriteKeepingSparse(w.file, p, off)
}

// vmImageSink writes a conversion's output image and keeps its copy record
// (<target>.copy.json) beside it, like vmLocalSink does for VHDX copies.
type vmImageSink struct {
	target string
	format string
	disk   vmDiskWriter
	state  *vmCopyState
}

// vmConvertTargetFiles lists every file a conversion writes.
func vmConvertTargetFiles(format, target string) []string {
	files := []string{target, vmCopyStatePath(target)}
	if format == vmDiskFormatVMDKFlat {
		files = append(files, vmdkFlatDataPath(target))
	}
	return files
}

func (s *vmImageSink) Open(source string, size, chunkSize int64) (*vmCopyState, bool, error) {
	target, err := filepath.Abs(s.target)
	if err != nil {
		return nil, false, err
	}
	s.target = target
	if size > vmDiskFormatLimit(s.format) {
		return nil, false, fmt.Errorf("%s holds at most %s; this disk is %s. Choose VHDX, qcow2, VMDK (ESXi flat) or raw instead", vmDiskFormatTitle(s.format), vmFormatBytes(vmDiskFormatLimit(s.format)), vmFormatBytes(size))
	}
	state, stateErr := loadVMCopyState(target)
	_, targetErr := os.Stat(target)
	switch {
	case targetErr == nil && stateErr == nil:
		if state.Size != size || state.ChunkSize != chunkSize {
			return nil, false, fmt.Errorf("%s was converted from a %s disk; this source is %s. Choose a new target file", target, vmFormatBytes(state.Size), vmFormatBytes(size))
		}
		if state.Source != source {
			return nil, false, fmt.Errorf("%s was converted from %s, not %s. Choose a new target file", target, state.Source, source)
		}
		disk, err := openDiskWriter(s.format, target)
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
		return nil, false, fmt.Errorf("%s already exists and was not created by this conversion; choose a new file name so nothing is overwritten", target)
	case !os.IsNotExist(targetErr):
		return nil, false, targetErr
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return nil, false, err
	}
	disk, err := createDiskWriter(s.format, target, size)
	if err != nil {
		return nil, false, err
	}
	chunks := (size + chunkSize - 1) / chunkSize
	state = &vmCopyState{Version: vmCopyStateVersion, Source: source, Target: target, Size: size, ChunkSize: chunkSize, Hashes: make([]string, chunks)}
	if err := saveVMCopyState(state); err != nil {
		disk.Close()
		for _, file := range vmConvertTargetFiles(s.format, target) {
			os.Remove(file)
		}
		return nil, false, err
	}
	s.disk, s.state = disk, state
	return vmCopyStateClone(state), false, nil
}

func (s *vmImageSink) Write(index int, data []byte, hash string) (int64, error) {
	if index < 0 || index >= len(s.state.Hashes) {
		return 0, fmt.Errorf("chunk %d is outside the disk", index)
	}
	if int64(len(data)) != vmChunkLength(s.state, index) {
		return 0, fmt.Errorf("chunk %d is %d bytes, not %d", index, len(data), vmChunkLength(s.state, index))
	}
	if _, err := s.disk.WriteAt(data, int64(index)*s.state.ChunkSize); err != nil {
		return 0, fmt.Errorf("writing %s: %w", s.target, err)
	}
	s.state.Hashes[index] = hash
	if hash == vmCopyZeroHash {
		return 0, nil
	}
	return int64(len(data)), nil
}

func (s *vmImageSink) Checkpoint(state *vmCopyState) error {
	if err := s.disk.Flush(); err != nil {
		return err
	}
	s.state.Passes, s.state.Complete, s.state.Verified = state.Passes, false, false
	return saveVMCopyState(s.state)
}

func (s *vmImageSink) Finish(ctx context.Context, state *vmCopyState, verify bool) (*vmCopyState, error) {
	if err := s.disk.Flush(); err != nil {
		return nil, err
	}
	for index, hash := range s.state.Hashes {
		if hash == "" || hash != state.Hashes[index] {
			s.Checkpoint(state)
			return nil, errors.New("some chunks were never copied; run the conversion again")
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

func (s *vmImageSink) Close() error {
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

type vmConvertOptions struct {
	Verify   bool
	Progress func(vmCopyProgress)
	// chunkSize is changed by tests.
	chunkSize int64
}

type vmConvertResult struct {
	Source   vmDiskInfo     `json:"source"`
	Target   string         `json:"target"`
	Format   string         `json:"format"`
	Files    []string       `json:"files"`
	Resumed  bool           `json:"resumed"`
	Passes   int            `json:"passes"`
	Verified bool           `json:"verified"`
	Progress vmCopyProgress `json:"progress"`
}

// vmConvertDisk converts the image at sourcePath into a new image (or the
// earlier conversion of the same source) at target.
func vmConvertDisk(ctx context.Context, sourcePath, target, format string, options vmConvertOptions) (*vmConvertResult, error) {
	if !vmDiskFormatKnown(format) {
		return nil, fmt.Errorf("the output format %q is not supported", format)
	}
	source, err := openDiskImage(sourcePath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return vmConvertImage(ctx, source, target, format, options)
}

func vmConvertImage(ctx context.Context, source vmDiskImage, target, format string, options vmConvertOptions) (*vmConvertResult, error) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	for _, file := range source.Info().Chain {
		for _, written := range vmConvertTargetFiles(format, absolute) {
			if vmSameFile(file, written) {
				return nil, errors.New("the target is part of the source disk chain; choose another file name")
			}
		}
	}
	copyOptions := vmCopyOptions{ChunkSize: options.chunkSize, Verify: options.Verify, Progress: options.Progress}
	if copyOptions.ChunkSize <= 0 {
		copyOptions.ChunkSize = vmCopyChunkSize
	}
	if allocated := source.Allocated(); allocated != nil {
		// Read only the areas that hold data. On a repeat pass, chunks that
		// held data last time are read too, so data removed from the source
		// since then is cleared in the copy.
		ranges := append([]vmCopyRange(nil), allocated...)
		if earlier, err := loadVMCopyState(absolute); err == nil && earlier.Size == source.Size() && earlier.ChunkSize == copyOptions.ChunkSize {
			for index, hash := range earlier.Hashes {
				if hash != "" && hash != vmCopyZeroHash {
					ranges = append(ranges, vmCopyRange{Start: int64(index) * earlier.ChunkSize, Length: vmChunkLength(earlier, index)})
				}
			}
		}
		copyOptions.Ranges, copyOptions.ZeroOutside = vmMergeRanges(ranges), true
		if copyOptions.Ranges == nil {
			copyOptions.Ranges = []vmCopyRange{}
		}
	}
	sink := &vmImageSink{target: absolute, format: format}
	result, err := vmCopyTo(ctx, source, sink, copyOptions)
	if err != nil {
		return nil, err
	}
	files := []string{absolute}
	if format == vmDiskFormatVMDKFlat {
		files = append(files, vmdkFlatDataPath(absolute))
	}
	return &vmConvertResult{Source: source.Info(), Target: absolute, Format: format, Files: files, Resumed: result.Resumed, Passes: result.State.Passes, Verified: result.State.Verified, Progress: result.Progress}, nil
}

func vmSameFile(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	right, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(left, right)
}

// vmConvertSuggestedName is the default output name for a source.
func vmConvertSuggestedName(sourcePath, format string) string {
	base := filepath.Base(sourcePath)
	if extension := filepath.Ext(base); extension != "" && len(extension) <= 7 {
		base = strings.TrimSuffix(base, extension)
	}
	base = strings.TrimSuffix(base, "-flat")
	extension := ".img"
	for _, item := range vmDiskFormatNames {
		if item.ID == format {
			extension = item.Extension
		}
	}
	return base + extension
}

func convertDoneMessage(result *vmConvertResult) string {
	parts := []string{}
	switch {
	case result.Passes > 1 && result.Progress.Changed == 0 && result.Progress.BytesWritten == 0:
		parts = append(parts, "Already up to date: nothing changed since the last pass.")
	case result.Passes > 1:
		parts = append(parts, fmt.Sprintf("Updated (pass %d): %d changed chunks rewritten.", result.Passes, result.Progress.Changed))
	case result.Resumed:
		parts = append(parts, "Converted, continuing where the earlier run stopped.")
	default:
		parts = append(parts, "Converted.")
	}
	if result.Verified {
		parts = append(parts, "Every chunk was read back and matches the source.")
	}
	return strings.Join(parts, " ")
}
