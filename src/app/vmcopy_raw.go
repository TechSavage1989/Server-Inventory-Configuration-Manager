package main

// Raw disk targets. HyperCore, Nutanix AHV, Proxmox VE and physical servers
// (V2P) receive a copy straight onto a disk: the new VM, or the new server,
// boots receiver media and the receiver writes each chunk onto its own blank
// disk at the same offset as on the source. The copy record cannot live beside
// a disk, so it is kept in a records folder and names the disk by its
// identity (serial number, model and size).
//
// A disk is written only when it is blank (its first and last megabyte read
// as zeros, so it has no partition table), when the records folder holds this
// copy's record for it, or when the operator explicitly allowed overwriting
// it. Without a record, every chunk is written on the first pass.

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
	"regexp"
	"strings"
	"time"
)

const vmRawProbeBytes = 1 << 20

// vmBlockDevice is a disk or raw image the receiver writes into.
type vmBlockDevice interface {
	io.ReaderAt
	io.WriterAt
	// Size is the device's capacity in bytes; it may be larger than the
	// source disk (hypervisors round sizes up).
	Size() int64
	// SectorSize is the device's logical sector size.
	SectorSize() int64
	// Identity names the device stably across restarts.
	Identity() string
	// Describe is a short label for logs and messages.
	Describe() string
	Flush() error
	Close() error
}

// vmRawRecord is the copy record of a raw disk target.
type vmRawRecord struct {
	vmCopyState
	Device string `json:"device"`
	// Blank says the disk read as zeros when the copy started, so a chunk
	// never written still reads as zeros.
	Blank bool `json:"blank"`
	// Overwrote says the operator allowed writing over a disk that held data.
	Overwrote bool `json:"overwrote,omitempty"`
}

var vmRawRecordName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func vmRawRecordPath(folder, identity string) string {
	sum := sha256.Sum256([]byte(identity))
	label := strings.Trim(vmRawRecordName.ReplaceAllString(identity, "-"), "-.")
	if len(label) > 60 {
		label = label[:60]
	}
	return filepath.Join(folder, label+"-"+hex.EncodeToString(sum[:6])+".raw-copy.json")
}

func loadVMRawRecord(path string) (*vmRawRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	record := &vmRawRecord{}
	if err := json.Unmarshal(raw, record); err != nil {
		return nil, fmt.Errorf("the copy record %s is damaged: %w", path, err)
	}
	state := record.vmCopyState
	if state.Version != vmCopyStateVersion || state.ChunkSize <= 0 || int64(len(state.Hashes)) != (state.Size+state.ChunkSize-1)/state.ChunkSize {
		return nil, fmt.Errorf("the copy record %s does not match this version", path)
	}
	return record, nil
}

func saveVMRawRecord(path string, record *vmRawRecord) error {
	record.Updated = time.Now().UTC()
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// vmDeviceIsBlank reads the first and last megabyte of a device. A disk with
// an MBR or GPT partition table (or a GPT backup header) is not blank.
func vmDeviceIsBlank(device vmBlockDevice) (bool, error) {
	size := device.Size()
	probe := int64(vmRawProbeBytes)
	if probe > size {
		probe = size
	}
	buffer := make([]byte, probe)
	for _, offset := range []int64{0, size - probe} {
		if _, err := device.ReadAt(buffer, offset); err != nil && err != io.EOF {
			return false, fmt.Errorf("reading %s: %w", device.Describe(), err)
		}
		if !vmIsZero(buffer) {
			return false, nil
		}
	}
	return true, nil
}

// vmRawSink writes a copy onto a block device. Overwrite allows a disk that
// is neither blank nor recorded; it is the operator's explicit choice.
type vmRawSink struct {
	device    vmBlockDevice
	records   string
	overwrite bool

	path   string
	record *vmRawRecord
}

func newVMRawSink(device vmBlockDevice, records string, overwrite bool) *vmRawSink {
	return &vmRawSink{device: device, records: records, overwrite: overwrite}
}

func (s *vmRawSink) Open(source string, size, chunkSize int64) (*vmCopyState, bool, error) {
	if s.records == "" {
		return nil, false, errors.New("choose a folder for the copy records")
	}
	sector := s.device.SectorSize()
	if sector <= 0 {
		sector = vhdxLogicalSector
	}
	if size > s.device.Size() {
		return nil, false, fmt.Errorf("%s holds %s; the source disk is %s. Make the target disk at least as large as the source", s.device.Describe(), vmFormatBytes(s.device.Size()), vmFormatBytes(size))
	}
	if size%sector != 0 || chunkSize%sector != 0 {
		return nil, false, fmt.Errorf("%s uses %d-byte sectors; the source disk is not a whole number of them", s.device.Describe(), sector)
	}
	s.path = vmRawRecordPath(s.records, s.device.Identity())
	record, recordErr := loadVMRawRecord(s.path)
	switch {
	case recordErr == nil:
		if record.Device != s.device.Identity() {
			return nil, false, fmt.Errorf("the copy record %s belongs to another disk", s.path)
		}
		if record.Size != size || record.ChunkSize != chunkSize {
			return nil, false, fmt.Errorf("%s was copied from a %s disk; this source is %s. Use another target disk", s.device.Describe(), vmFormatBytes(record.Size), vmFormatBytes(size))
		}
		if record.Source != source {
			return nil, false, fmt.Errorf("%s holds a copy of %s, not %s. Use another target disk", s.device.Describe(), record.Source, source)
		}
		s.record = record
		return vmCopyStateClone(&record.vmCopyState), true, nil
	case !os.IsNotExist(recordErr):
		return nil, false, recordErr
	}
	blank, err := vmDeviceIsBlank(s.device)
	if err != nil {
		return nil, false, err
	}
	if !blank && !s.overwrite {
		return nil, false, fmt.Errorf("%s already holds data (it has a partition table) and no copy record. Choose a blank disk, or allow overwriting this disk if you are sure its contents can be destroyed", s.device.Describe())
	}
	chunks := (size + chunkSize - 1) / chunkSize
	record = &vmRawRecord{
		vmCopyState: vmCopyState{Version: vmCopyStateVersion, Source: source, Target: s.device.Describe(), Size: size, ChunkSize: chunkSize, Hashes: make([]string, chunks)},
		Device:      s.device.Identity(),
		Blank:       blank,
		Overwrote:   !blank,
	}
	if !blank && s.device.Size() > size {
		// The disk is larger than the source: clear its old last megabyte
		// so a stale GPT backup header there cannot be mistaken for ours.
		tail := s.device.Size() - size
		if tail > vmRawProbeBytes {
			tail = vmRawProbeBytes
		}
		if _, err := s.device.WriteAt(make([]byte, tail), s.device.Size()-tail); err != nil {
			return nil, false, fmt.Errorf("clearing the end of %s: %w", s.device.Describe(), err)
		}
	}
	if err := saveVMRawRecord(s.path, record); err != nil {
		return nil, false, err
	}
	s.record = record
	return vmCopyStateClone(&record.vmCopyState), false, nil
}

func (s *vmRawSink) State() *vmCopyState { return &s.record.vmCopyState }

func (s *vmRawSink) Write(index int, data []byte, hash string) (int64, error) {
	state := &s.record.vmCopyState
	if index < 0 || index >= len(state.Hashes) {
		return 0, fmt.Errorf("chunk %d is outside the disk", index)
	}
	if int64(len(data)) != vmChunkLength(state, index) {
		return 0, fmt.Errorf("chunk %d is %d bytes, not %d", index, len(data), vmChunkLength(state, index))
	}
	previous := state.Hashes[index]
	written := int64(0)
	// A zero chunk is skipped only where the disk is known to read zeros:
	// it was blank and nothing has been written there, or zeros were.
	known := previous == vmCopyZeroHash || (previous == "" && s.record.Blank)
	if hash != vmCopyZeroHash || !known {
		if _, err := s.device.WriteAt(data, int64(index)*state.ChunkSize); err != nil {
			return 0, fmt.Errorf("writing %s: %w", s.device.Describe(), err)
		}
		written = int64(len(data))
	}
	state.Hashes[index] = hash
	return written, nil
}

func (s *vmRawSink) Checkpoint(state *vmCopyState) error {
	if err := s.device.Flush(); err != nil {
		return err
	}
	s.record.Passes, s.record.Complete, s.record.Verified = state.Passes, false, false
	return saveVMRawRecord(s.path, s.record)
}

func (s *vmRawSink) Finish(ctx context.Context, state *vmCopyState, verify bool) (*vmCopyState, error) {
	if err := s.device.Flush(); err != nil {
		return nil, err
	}
	for index, hash := range s.record.Hashes {
		if hash == "" || hash != state.Hashes[index] {
			s.Checkpoint(state)
			return nil, errors.New("some chunks were never copied; run a full pass without changed-block ranges first")
		}
	}
	s.record.Passes, s.record.ChangeID, s.record.Round, s.record.Complete, s.record.Verified = state.Passes, state.ChangeID, state.Round, true, false
	s.record.Machine = state.Machine
	if verify {
		if err := vmVerifyCopy(ctx, s.device, &s.record.vmCopyState); err != nil {
			saveVMRawRecord(s.path, s.record)
			return nil, err
		}
		s.record.Verified = true
	}
	if err := saveVMRawRecord(s.path, s.record); err != nil {
		return nil, err
	}
	return vmCopyStateClone(&s.record.vmCopyState), nil
}

// Close saves the record; the device itself is owned by whoever opened it.
func (s *vmRawSink) Close() error {
	if s.record == nil || s.path == "" {
		return nil
	}
	s.device.Flush()
	return saveVMRawRecord(s.path, s.record)
}

// vmImageDevice is a raw disk image file used as a block device, for tests
// and for targets that import raw images. It never creates the file.
type vmImageDevice struct {
	file *os.File
	path string
	size int64
}

func openVMImageDevice(path string) (*vmImageDevice, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("%s is not a disk image file", path)
	}
	absolute, _ := filepath.Abs(path)
	return &vmImageDevice{file: file, path: absolute, size: info.Size()}, nil
}

func (d *vmImageDevice) ReadAt(p []byte, off int64) (int, error)  { return d.file.ReadAt(p, off) }
func (d *vmImageDevice) WriteAt(p []byte, off int64) (int, error) { return d.file.WriteAt(p, off) }
func (d *vmImageDevice) Size() int64                              { return d.size }
func (d *vmImageDevice) SectorSize() int64                        { return vhdxLogicalSector }
func (d *vmImageDevice) Identity() string                         { return "image|" + d.path }
func (d *vmImageDevice) Describe() string                         { return d.path }
func (d *vmImageDevice) Flush() error                             { return d.file.Sync() }
func (d *vmImageDevice) Close() error                             { return d.file.Close() }

func (s *vmRawSink) Rescan(ctx context.Context, progress func(done, total int)) error {
	if s.record == nil {
		return errors.New("the copy is not open")
	}
	if err := vmRescanHashes(ctx, s.device, &s.record.vmCopyState, progress); err != nil {
		return err
	}
	return saveVMRawRecord(s.path, s.record)
}
