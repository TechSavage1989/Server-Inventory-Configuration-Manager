package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

// A dynamic VHDX file written directly (MS-VHDX), so a copied disk can be
// attached to a Hyper-V VM without converting it again. Only the parts the
// copy engine needs are implemented: a fixed layout, a 32 MiB block size,
// blocks allocated on first non-zero write, no parent disk and an empty log.
// Unallocated blocks read as zeros.

const (
	vhdxSignature        = "vhdxfile"
	vhdxBlockSize        = 32 << 20
	vhdxLogicalSector    = 512
	vhdxPhysicalSector   = 4096
	vhdxMB               = 1 << 20
	vhdxHeader1Offset    = 64 << 10
	vhdxHeader2Offset    = 128 << 10
	vhdxRegion1Offset    = 192 << 10
	vhdxRegion2Offset    = 256 << 10
	vhdxLogOffset        = 1 * vhdxMB
	vhdxLogLength        = 1 * vhdxMB
	vhdxMetadataOffset   = 2 * vhdxMB
	vhdxMetadataLength   = 1 * vhdxMB
	vhdxBATOffset        = 3 * vhdxMB
	vhdxMaxSize          = 64 << 40
	vhdxBlockNotPresent  = 0
	vhdxBlockFullPresent = 6
	vhdxBlockPartPresent = 7
	vhdxCreator          = "Server Inventory and Migration Manager"
)

var (
	vhdxCRC          = crc32.MakeTable(crc32.Castagnoli)
	vhdxRegionBAT    = vhdxGUID("2DC27766-F623-4200-9D64-115E9BFD4A08")
	vhdxRegionMeta   = vhdxGUID("8B7CA206-4790-4B9A-B8FE-575F050F886E")
	vhdxMetaFile     = vhdxGUID("CAA16737-FA36-4D43-B3B6-33F0AA44E76B")
	vhdxMetaSize     = vhdxGUID("2FA54224-CD1B-4876-B211-5DBED83BF4B8")
	vhdxMetaDiskID   = vhdxGUID("BECA12AB-B2E6-4523-93EF-C309E000C746")
	vhdxMetaLogical  = vhdxGUID("8141BF1D-A96F-4709-BA47-F233A8FAAB5F")
	vhdxMetaPhysical = vhdxGUID("CDA348C7-445D-4471-9CC9-E9885251C556")
	vhdxMetaParent   = vhdxGUID("A8D35F2D-B30B-454D-ABF7-D3D84834AB0C")
)

// vhdxGUID encodes a GUID in the mixed-endian layout Windows stores.
func vhdxGUID(text string) [16]byte {
	var out [16]byte
	hex := strings.ReplaceAll(text, "-", "")
	raw := make([]byte, 16)
	for i := 0; i < 16; i++ {
		fmt.Sscanf(hex[i*2:i*2+2], "%02x", &raw[i])
	}
	binary.LittleEndian.PutUint32(out[0:], binary.BigEndian.Uint32(raw[0:]))
	binary.LittleEndian.PutUint16(out[4:], binary.BigEndian.Uint16(raw[4:]))
	binary.LittleEndian.PutUint16(out[6:], binary.BigEndian.Uint16(raw[6:]))
	copy(out[8:], raw[8:])
	return out
}

func vhdxRandomGUID() [16]byte {
	var out [16]byte
	_, _ = rand.Read(out[:])
	out[7] = out[7]&0x0f | 0x40
	out[8] = out[8]&0x3f | 0x80
	return out
}

type vhdxFile struct {
	file       *os.File
	path       string
	size       int64
	blockSize  int64
	chunkRatio int64
	bat        []uint64
	end        int64
	sequence   uint64
	readOnly   bool
	batAt      int64
	// Differencing disks are opened only by the disk converter's reader
	// (vmdisk_vhdx.go), which follows the parent; logical is the logical
	// sector size and dataGUID the current header's data write GUID.
	allowDifferencing bool
	differencing      bool
	logical           int64
	dataGUID          [16]byte
	metaOffset        int64
}

func vhdxBATEntries(size, blockSize int64) (blocks, entries, chunkRatio int64) {
	chunkRatio = (int64(1) << 23) * vhdxLogicalSector / blockSize
	blocks = (size + blockSize - 1) / blockSize
	entries = blocks
	if blocks > 0 {
		entries += (blocks - 1) / chunkRatio
	}
	return
}

// createVHDX writes an empty dynamic VHDX of the given size.
func createVHDX(path string, size int64) (*vhdxFile, error) {
	if size <= 0 || size%vhdxLogicalSector != 0 {
		return nil, fmt.Errorf("a VHDX must be a whole number of 512-byte sectors, not %d bytes", size)
	}
	if size > vhdxMaxSize {
		return nil, fmt.Errorf("a VHDX can hold at most 64 TB; this disk is %s", vmFormatBytes(size))
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, entries, ratio := vhdxBATEntries(size, vhdxBlockSize)
	batLength := (entries*8 + vhdxMB - 1) / vhdxMB * vhdxMB
	v := &vhdxFile{file: file, path: path, size: size, blockSize: vhdxBlockSize, batAt: vhdxBATOffset, chunkRatio: ratio, bat: make([]uint64, entries), end: vhdxBATOffset + batLength}
	fail := func(err error) (*vhdxFile, error) {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if err := file.Truncate(v.end); err != nil {
		return fail(err)
	}
	identifier := make([]byte, 64<<10)
	copy(identifier, vhdxSignature)
	for i, unit := range utf16.Encode([]rune(vhdxCreator)) {
		binary.LittleEndian.PutUint16(identifier[8+i*2:], unit)
	}
	if _, err := file.WriteAt(identifier, 0); err != nil {
		return fail(err)
	}
	regions := make([]byte, 64<<10)
	copy(regions, "regi")
	binary.LittleEndian.PutUint32(regions[8:], 2)
	putRegion := func(at int, id [16]byte, offset int64, length int64) {
		copy(regions[at:], id[:])
		binary.LittleEndian.PutUint64(regions[at+16:], uint64(offset))
		binary.LittleEndian.PutUint32(regions[at+24:], uint32(length))
		binary.LittleEndian.PutUint32(regions[at+28:], 1)
	}
	putRegion(16, vhdxRegionBAT, vhdxBATOffset, batLength)
	putRegion(48, vhdxRegionMeta, vhdxMetadataOffset, vhdxMetadataLength)
	binary.LittleEndian.PutUint32(regions[4:], crc32.Checksum(regions, vhdxCRC))
	for _, offset := range []int64{vhdxRegion1Offset, vhdxRegion2Offset} {
		if _, err := file.WriteAt(regions, offset); err != nil {
			return fail(err)
		}
	}
	if _, err := file.WriteAt(vhdxMetadata(size), vhdxMetadataOffset); err != nil {
		return fail(err)
	}
	if err := v.writeHeaders(); err != nil {
		return fail(err)
	}
	return v, nil
}

func vhdxMetadata(size int64) []byte {
	meta := make([]byte, 64<<10+4096)
	copy(meta, "metadata")
	diskID := vhdxRandomGUID()
	items := []struct {
		id    [16]byte
		value []byte
		flags uint32
	}{
		{vhdxMetaFile, vhdxUint32s(vhdxBlockSize, 0), 4},
		{vhdxMetaSize, vhdxUint64(uint64(size)), 6},
		{vhdxMetaDiskID, diskID[:], 6},
		{vhdxMetaLogical, vhdxUint32s(vhdxLogicalSector), 6},
		{vhdxMetaPhysical, vhdxUint32s(vhdxPhysicalSector), 6},
	}
	binary.LittleEndian.PutUint16(meta[10:], uint16(len(items)))
	offset := 64 << 10
	for i, item := range items {
		entry := 32 + i*32
		copy(meta[entry:], item.id[:])
		binary.LittleEndian.PutUint32(meta[entry+16:], uint32(offset))
		binary.LittleEndian.PutUint32(meta[entry+20:], uint32(len(item.value)))
		binary.LittleEndian.PutUint32(meta[entry+24:], item.flags)
		copy(meta[offset:], item.value)
		offset += len(item.value)
	}
	return meta
}

func vhdxUint32s(values ...uint32) []byte {
	out := make([]byte, 4*len(values))
	for i, value := range values {
		binary.LittleEndian.PutUint32(out[i*4:], value)
	}
	return out
}

func vhdxUint64(value uint64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, value)
	return out
}

// writeHeaders writes both headers with new sequence numbers and a new data
// write GUID, the older header first so one valid header always remains.
func (v *vhdxFile) writeHeaders() error {
	fileGUID, dataGUID := vhdxRandomGUID(), vhdxRandomGUID()
	for _, offset := range []int64{vhdxHeader2Offset, vhdxHeader1Offset} {
		v.sequence++
		header := make([]byte, 4096)
		copy(header, "head")
		binary.LittleEndian.PutUint64(header[8:], v.sequence)
		copy(header[16:], fileGUID[:])
		copy(header[32:], dataGUID[:])
		binary.LittleEndian.PutUint16(header[66:], 1)
		binary.LittleEndian.PutUint32(header[68:], vhdxLogLength)
		binary.LittleEndian.PutUint64(header[72:], vhdxLogOffset)
		binary.LittleEndian.PutUint32(header[4:], crc32.Checksum(header, vhdxCRC))
		if _, err := v.file.WriteAt(header, offset); err != nil {
			return err
		}
	}
	return v.file.Sync()
}

func vhdxChecked(buffer []byte, signature string) bool {
	if string(buffer[:len(signature)]) != signature {
		return false
	}
	stored := binary.LittleEndian.Uint32(buffer[4:])
	copyBuffer := append([]byte(nil), buffer...)
	binary.LittleEndian.PutUint32(copyBuffer[4:], 0)
	return crc32.Checksum(copyBuffer, vhdxCRC) == stored
}

// openVHDX opens a dynamic VHDX to add to it or read it back. Differencing
// disks and files with a pending log are refused.
func openVHDX(path string, readOnly bool) (*vhdxFile, error) {
	flag := os.O_RDWR
	if readOnly {
		flag = os.O_RDONLY
	}
	file, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	v := &vhdxFile{file: file, path: path, readOnly: readOnly}
	if err := v.load(); err != nil {
		file.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !readOnly {
		if err := v.writeHeaders(); err != nil {
			file.Close()
			return nil, err
		}
	}
	return v, nil
}

func (v *vhdxFile) load() error {
	identifier := make([]byte, 8)
	if _, err := v.file.ReadAt(identifier, 0); err != nil || string(identifier) != vhdxSignature {
		return errors.New("not a VHDX file")
	}
	var current []byte
	for _, offset := range []int64{vhdxHeader1Offset, vhdxHeader2Offset} {
		header := make([]byte, 4096)
		if _, err := v.file.ReadAt(header, offset); err != nil || !vhdxChecked(header, "head") {
			continue
		}
		if sequence := binary.LittleEndian.Uint64(header[8:]); current == nil || sequence > v.sequence {
			current, v.sequence = header, sequence
		}
	}
	if current == nil {
		return errors.New("both VHDX headers are damaged")
	}
	copy(v.dataGUID[:], current[32:48])
	if !bytes.Equal(current[48:64], make([]byte, 16)) {
		return errors.New("the VHDX has unapplied log entries; attach it once in Hyper-V or Windows so they are applied, then try again")
	}
	var regions []byte
	for _, offset := range []int64{vhdxRegion1Offset, vhdxRegion2Offset} {
		table := make([]byte, 64<<10)
		if _, err := v.file.ReadAt(table, offset); err == nil && vhdxChecked(table, "regi") {
			regions = table
			break
		}
	}
	if regions == nil {
		return errors.New("both VHDX region tables are damaged")
	}
	var batOffset, metaOffset int64
	var batLength int64
	for i := 0; i < int(binary.LittleEndian.Uint32(regions[8:])) && 16+i*32+32 <= len(regions); i++ {
		entry := regions[16+i*32:]
		var id [16]byte
		copy(id[:], entry[:16])
		switch id {
		case vhdxRegionBAT:
			batOffset, batLength = int64(binary.LittleEndian.Uint64(entry[16:])), int64(binary.LittleEndian.Uint32(entry[24:]))
		case vhdxRegionMeta:
			metaOffset = int64(binary.LittleEndian.Uint64(entry[16:]))
		default:
			if binary.LittleEndian.Uint32(entry[28:])&1 != 0 {
				return errors.New("the VHDX uses a required region this application does not understand")
			}
		}
	}
	if batOffset == 0 || metaOffset == 0 {
		return errors.New("the VHDX has no block table or metadata")
	}
	meta := make([]byte, 64<<10)
	if _, err := v.file.ReadAt(meta, metaOffset); err != nil || string(meta[:8]) != "metadata" {
		return errors.New("the VHDX metadata is damaged")
	}
	logical := int64(0)
	for i := 0; i < int(binary.LittleEndian.Uint16(meta[10:])); i++ {
		entry := meta[32+i*32:]
		var id [16]byte
		copy(id[:], entry[:16])
		value := make([]byte, binary.LittleEndian.Uint32(entry[20:]))
		if _, err := v.file.ReadAt(value, metaOffset+int64(binary.LittleEndian.Uint32(entry[16:]))); err != nil {
			return err
		}
		switch id {
		case vhdxMetaFile:
			v.blockSize = int64(binary.LittleEndian.Uint32(value))
			if binary.LittleEndian.Uint32(value[4:])&2 != 0 {
				if !v.allowDifferencing {
					return errors.New("differencing VHDX files are not supported here")
				}
				v.differencing = true
			}
		case vhdxMetaSize:
			v.size = int64(binary.LittleEndian.Uint64(value))
		case vhdxMetaLogical:
			logical = int64(binary.LittleEndian.Uint32(value))
		case vhdxMetaParent:
			if !v.allowDifferencing {
				return errors.New("differencing VHDX files are not supported here")
			}
			v.differencing = true
		}
	}
	if v.blockSize < vhdxMB || v.blockSize&(v.blockSize-1) != 0 || v.size <= 0 || (logical != 512 && logical != 4096) {
		return errors.New("the VHDX metadata has unsupported values")
	}
	v.chunkRatio = (int64(1) << 23) * logical / v.blockSize
	v.logical, v.metaOffset = logical, metaOffset
	_, entries, _ := vhdxBATEntries(v.size, v.blockSize)
	if entries*8 > batLength {
		return errors.New("the VHDX block table is too small for its size")
	}
	raw := make([]byte, entries*8)
	if _, err := v.file.ReadAt(raw, batOffset); err != nil {
		return err
	}
	v.bat = make([]uint64, entries)
	for i := range v.bat {
		v.bat[i] = binary.LittleEndian.Uint64(raw[i*8:])
	}
	info, err := v.file.Stat()
	if err != nil {
		return err
	}
	v.end = (info.Size() + vhdxMB - 1) / vhdxMB * vhdxMB
	v.batAt = batOffset
	return nil
}

func (v *vhdxFile) Size() int64 { return v.size }

func (v *vhdxFile) batIndex(block int64) int64 { return block + block/v.chunkRatio }

// blockOffset returns where a payload block's data is, or 0 when it is not
// allocated (it reads as zeros).
func (v *vhdxFile) blockOffset(block int64) int64 {
	entry := v.bat[v.batIndex(block)]
	switch entry & 7 {
	case vhdxBlockFullPresent, vhdxBlockPartPresent:
		return int64(entry>>20) * vhdxMB
	}
	return 0
}

// ReadAt reads virtual disk bytes; unallocated blocks read as zeros.
func (v *vhdxFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= v.size {
		return 0, io.EOF
	}
	total := 0
	for len(p) > 0 && off < v.size {
		block, inside := off/v.blockSize, off%v.blockSize
		n := int64(len(p))
		if n > v.blockSize-inside {
			n = v.blockSize - inside
		}
		if n > v.size-off {
			n = v.size - off
		}
		if at := v.blockOffset(block); at == 0 {
			for i := int64(0); i < n; i++ {
				p[i] = 0
			}
		} else if _, err := v.file.ReadAt(p[:n], at+inside); err != nil {
			return total, err
		}
		p, off, total = p[n:], off+n, total+int(n)
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}

// WriteAt writes virtual disk bytes. A block is allocated only when non-zero
// data is written to it, so zero runs stay sparse.
func (v *vhdxFile) WriteAt(p []byte, off int64) (int, error) {
	if v.readOnly {
		return 0, errors.New("the VHDX is open read-only")
	}
	if off < 0 || off+int64(len(p)) > v.size {
		return 0, fmt.Errorf("write at %d+%d is outside the %d-byte disk", off, len(p), v.size)
	}
	total := 0
	for len(p) > 0 {
		block, inside := off/v.blockSize, off%v.blockSize
		n := int64(len(p))
		if n > v.blockSize-inside {
			n = v.blockSize - inside
		}
		at := v.blockOffset(block)
		if at == 0 {
			if vmIsZero(p[:n]) {
				p, off, total = p[n:], off+n, total+int(n)
				continue
			}
			at = v.end
			if err := v.file.Truncate(at + v.blockSize); err != nil {
				return total, err
			}
			if _, err := v.file.WriteAt(p[:n], at+inside); err != nil {
				return total, err
			}
			entry := uint64(vhdxBlockFullPresent) | uint64(at/vhdxMB)<<20
			index := v.batIndex(block)
			if _, err := v.file.WriteAt(vhdxUint64(entry), v.batAt+index*8); err != nil {
				return total, err
			}
			v.bat[index] = entry
			v.end = at + v.blockSize
		} else if _, err := v.file.WriteAt(p[:n], at+inside); err != nil {
			return total, err
		}
		p, off, total = p[n:], off+n, total+int(n)
	}
	return total, nil
}

// AllocatedBytes is the space the payload blocks take.
func (v *vhdxFile) AllocatedBytes() int64 {
	var total int64
	for block := int64(0); block*v.blockSize < v.size; block++ {
		if v.blockOffset(block) != 0 {
			total += v.blockSize
		}
	}
	return total
}

func (v *vhdxFile) Flush() error { return v.file.Sync() }

func (v *vhdxFile) Close() error {
	if v.readOnly {
		return v.file.Close()
	}
	err := v.writeHeaders()
	if closeErr := v.file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func vmIsZero(p []byte) bool {
	for len(p) >= 8 {
		if binary.LittleEndian.Uint64(p) != 0 {
			return false
		}
		p = p[8:]
	}
	for _, b := range p {
		if b != 0 {
			return false
		}
	}
	return true
}
