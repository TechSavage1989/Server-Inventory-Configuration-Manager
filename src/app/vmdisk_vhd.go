package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf16"
)

// VHD (Virtual Hard Disk 1.0): fixed, dynamic and differencing disks are
// read; fixed and dynamic disks are written. All fields are big-endian.

const (
	vhdFooterSize    = 512
	vhdHeaderSize    = 1024
	vhdBlockSize     = 2 << 20
	vhdMaxSize       = 2040 << 30
	vhdTypeFixed     = 2
	vhdTypeDynamic   = 3
	vhdTypeDiffering = 4
	vhdUnused        = 0xFFFFFFFF
	vhdNoOffset      = 0xFFFFFFFFFFFFFFFF
)

var vhdEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

type vhdImage struct {
	file       *os.File
	path       string
	fileSize   int64
	size       int64
	diskType   uint32
	footer     []byte
	blockSize  int64
	bitmapSize int64
	bat        []uint32
	batOffset  int64
	end        int64 // where the footer of a dynamic disk sits
	parent     vmDiskImage
	writable   bool
	uniqueID   []byte
}

func vhdChecksum(buffer []byte, field int) uint32 {
	var sum uint32
	for i, b := range buffer {
		if i >= field && i < field+4 {
			continue
		}
		sum += uint32(b)
	}
	return ^sum
}

func vhdValidFooter(footer []byte) bool {
	return len(footer) >= vhdFooterSize && string(footer[:8]) == "conectix" && binary.BigEndian.Uint32(footer[64:]) == vhdChecksum(footer[:vhdFooterSize], 64)
}

func openVHDImage(file *os.File, path string, fileSize int64, depth int) (*vhdImage, error) {
	v, err := loadVHD(file, path, fileSize, false)
	if err != nil {
		return nil, err
	}
	if v.diskType == vhdTypeDiffering {
		if err := v.openParent(depth); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func loadVHD(file *os.File, path string, fileSize int64, writable bool) (*vhdImage, error) {
	footer := make([]byte, vhdFooterSize)
	if _, err := file.ReadAt(footer, fileSize-vhdFooterSize); err != nil || !vhdValidFooter(footer) {
		// The copy at the start of a dynamic disk is used when the end was
		// cut short.
		if _, err := file.ReadAt(footer, 0); err != nil || !vhdValidFooter(footer) {
			return nil, errors.New("the VHD footer is missing or damaged")
		}
	}
	v := &vhdImage{file: file, path: path, fileSize: fileSize, footer: footer, writable: writable}
	v.size = int64(binary.BigEndian.Uint64(footer[48:]))
	v.diskType = binary.BigEndian.Uint32(footer[60:])
	v.uniqueID = append([]byte(nil), footer[68:84]...)
	if v.size <= 0 || v.size%vhdxLogicalSector != 0 {
		return nil, fmt.Errorf("the VHD says its disk is %d bytes, which is not a whole number of sectors", v.size)
	}
	switch v.diskType {
	case vhdTypeFixed:
		if fileSize < v.size+vhdFooterSize-1 {
			return nil, errors.New("the fixed VHD is shorter than its disk size; it may be incomplete")
		}
		v.end = v.size
		return v, nil
	case vhdTypeDynamic, vhdTypeDiffering:
	default:
		return nil, fmt.Errorf("the VHD disk type %d is not supported", v.diskType)
	}
	headerAt := int64(binary.BigEndian.Uint64(footer[16:]))
	header := make([]byte, vhdHeaderSize)
	if _, err := file.ReadAt(header, headerAt); err != nil || string(header[:8]) != "cxsparse" || binary.BigEndian.Uint32(header[36:]) != vhdChecksum(header, 36) {
		return nil, errors.New("the VHD dynamic disk header is missing or damaged")
	}
	v.batOffset = int64(binary.BigEndian.Uint64(header[16:]))
	entries := int64(binary.BigEndian.Uint32(header[28:]))
	v.blockSize = int64(binary.BigEndian.Uint32(header[32:]))
	if v.blockSize < vhdxLogicalSector || v.blockSize%vhdxLogicalSector != 0 || v.blockSize > 256<<20 {
		return nil, fmt.Errorf("the VHD block size %d is not supported", v.blockSize)
	}
	if entries*v.blockSize < v.size || entries > 1<<24 {
		return nil, errors.New("the VHD block table is too small for its disk size")
	}
	v.bitmapSize = (v.blockSize/vhdxLogicalSector/8 + vhdxLogicalSector - 1) / vhdxLogicalSector * vhdxLogicalSector
	raw := make([]byte, entries*4)
	if _, err := file.ReadAt(raw, v.batOffset); err != nil {
		return nil, fmt.Errorf("reading the VHD block table: %w", err)
	}
	v.bat = make([]uint32, entries)
	for i := range v.bat {
		v.bat[i] = binary.BigEndian.Uint32(raw[i*4:])
	}
	v.end = fileSize - vhdFooterSize
	if v.diskType == vhdTypeDiffering {
		v.footer = append(footer, header...)
	}
	return v, nil
}

func (v *vhdImage) openParent(depth int) error {
	header := v.footer[vhdFooterSize:]
	parentID := header[40:56]
	nameUnits := make([]uint16, 256)
	for i := range nameUnits {
		nameUnits[i] = binary.BigEndian.Uint16(header[64+i*2:])
	}
	name := string(utf16.Decode(vmTrimUTF16(nameUnits)))
	candidates := []string{}
	var absolute string
	for i := 0; i < 8; i++ {
		entry := header[576+i*24:]
		code := string(entry[:4])
		length := int64(binary.BigEndian.Uint32(entry[8:]))
		offset := int64(binary.BigEndian.Uint64(entry[16:]))
		if (code != "W2ru" && code != "W2ku") || length <= 0 || length > 64<<10 {
			continue
		}
		data := make([]byte, length)
		if _, err := v.file.ReadAt(data, offset); err != nil {
			continue
		}
		units := make([]uint16, length/2)
		for j := range units {
			units[j] = binary.LittleEndian.Uint16(data[j*2:])
		}
		text := string(utf16.Decode(vmTrimUTF16(units)))
		if code == "W2ru" {
			candidates = append(candidates, text)
		} else {
			absolute = text
		}
	}
	candidates = append(candidates, absolute, name)
	parentPath, err := vmResolveParentPath(v.path, candidates...)
	if err != nil {
		return err
	}
	parent, err := openDiskImageDepth(parentPath, "", depth+1)
	if err != nil {
		return fmt.Errorf("opening the parent disk: %w", err)
	}
	if parentVHD, ok := parent.(*vhdImage); ok && !bytes.Equal(parentVHD.uniqueID, parentID) {
		parent.Close()
		return fmt.Errorf("the parent disk %s has changed since this differencing disk was made (its ID does not match)", parentPath)
	}
	v.parent = parent
	return nil
}

func vmTrimUTF16(units []uint16) []uint16 {
	for i, unit := range units {
		if unit == 0 {
			return units[:i]
		}
	}
	return units
}

func (v *vhdImage) Size() int64      { return v.size }
func (v *vhdImage) Describe() string { return v.path }

func (v *vhdImage) Close() error {
	if v.parent != nil {
		v.parent.Close()
		v.parent = nil
	}
	return v.file.Close()
}

func (v *vhdImage) Info() vmDiskInfo {
	info := vmDiskInfo{Path: v.path, Format: vmDiskFormatVHD, FormatName: "VHD", VirtualSize: v.size, FileSize: v.fileSize}
	switch v.diskType {
	case vhdTypeFixed:
		info.Variant, info.DataBytes = "fixed", v.size
	case vhdTypeDynamic:
		info.Variant = "dynamic"
	default:
		info.Variant = "differencing"
	}
	if v.diskType != vhdTypeFixed {
		for _, entry := range v.bat {
			if entry != vhdUnused {
				info.DataBytes += v.blockSize
			}
		}
	}
	return vmChainInfo(info, v.parent)
}

func (v *vhdImage) Allocated() []vmCopyRange {
	if v.diskType == vhdTypeFixed {
		return nil
	}
	own := []vmCopyRange{}
	for block, entry := range v.bat {
		if entry != vhdUnused && int64(block)*v.blockSize < v.size {
			own = append(own, vmCopyRange{Start: int64(block) * v.blockSize, Length: v.blockSize})
		}
	}
	return vmChainAllocated(own, v.parent, v.size)
}

func (v *vhdImage) ReadAt(p []byte, off int64) (int, error) {
	return vmReadLimited(v.size, p, off, v.read)
}

func (v *vhdImage) read(p []byte, off int64) error {
	if v.diskType == vhdTypeFixed {
		n, err := v.file.ReadAt(p, off)
		if n < len(p) {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		return nil
	}
	for len(p) > 0 {
		block, inside := off/v.blockSize, off%v.blockSize
		n := int64(len(p))
		if n > v.blockSize-inside {
			n = v.blockSize - inside
		}
		if err := v.readBlock(p[:n], block, inside); err != nil {
			return err
		}
		p, off = p[n:], off+n
	}
	return nil
}

func (v *vhdImage) readBlock(p []byte, block, inside int64) error {
	entry := v.bat[block]
	virtual := block*v.blockSize + inside
	if entry == vhdUnused {
		_, err := vmParentReader{v.parent}.ReadAt(p, virtual)
		return err
	}
	at := int64(entry)*vhdxLogicalSector + v.bitmapSize
	if v.diskType != vhdTypeDiffering {
		_, err := io.ReadFull(io.NewSectionReader(v.file, at+inside, int64(len(p))), p)
		return err
	}
	// Differencing: each sector comes from this file only when its bit in
	// the block's bitmap is set (most significant bit first).
	bitmap := make([]byte, v.bitmapSize)
	if _, err := v.file.ReadAt(bitmap, int64(entry)*vhdxLogicalSector); err != nil {
		return err
	}
	for len(p) > 0 {
		sector := inside / vhdxLogicalSector
		local := bitmap[sector/8]&(0x80>>uint(sector%8)) != 0
		run := int64(vhdxLogicalSector - inside%vhdxLogicalSector)
		for next := sector + 1; run < int64(len(p)) && next*vhdxLogicalSector < v.blockSize; next++ {
			if (bitmap[next/8]&(0x80>>uint(next%8)) != 0) != local {
				break
			}
			run += vhdxLogicalSector
		}
		if run > int64(len(p)) {
			run = int64(len(p))
		}
		var err error
		if local {
			_, err = io.ReadFull(io.NewSectionReader(v.file, at+inside, run), p[:run])
		} else {
			_, err = vmParentReader{v.parent}.ReadAt(p[:run], block*v.blockSize+inside)
		}
		if err != nil {
			return err
		}
		p, inside = p[run:], inside+run
	}
	return nil
}

// Writing.

// vhdGeometry is the CHS geometry from the VHD specification.
func vhdGeometry(size int64) (cylinders uint16, heads, sectors uint8) {
	total := size / vhdxLogicalSector
	if total > 65535*16*255 {
		total = 65535 * 16 * 255
	}
	var spt, h, cth int64
	if total >= 65535*16*63 {
		spt, h = 255, 16
		cth = total / spt
	} else {
		spt = 17
		cth = total / spt
		h = (cth + 1023) / 1024
		if h < 4 {
			h = 4
		}
		if cth >= h*1024 || h > 16 {
			spt, h = 31, 16
			cth = total / spt
		}
		if cth >= h*1024 {
			spt, h = 63, 16
			cth = total / spt
		}
	}
	return uint16(cth / h), uint8(h), uint8(spt)
}

func vhdNewFooter(size int64, diskType uint32, dataOffset uint64) []byte {
	footer := make([]byte, vhdFooterSize)
	copy(footer, "conectix")
	binary.BigEndian.PutUint32(footer[8:], 2)
	binary.BigEndian.PutUint32(footer[12:], 0x00010000)
	binary.BigEndian.PutUint64(footer[16:], dataOffset)
	binary.BigEndian.PutUint32(footer[24:], uint32(time.Since(vhdEpoch)/time.Second))
	// "win " is what Windows writes; it tells readers such as QEMU to use
	// the exact byte size below rather than the rounded CHS geometry.
	copy(footer[28:], "win ")
	binary.BigEndian.PutUint32(footer[32:], 0x000a0000)
	copy(footer[36:], "Wi2k")
	binary.BigEndian.PutUint64(footer[40:], uint64(size))
	binary.BigEndian.PutUint64(footer[48:], uint64(size))
	cylinders, heads, sectors := vhdGeometry(size)
	binary.BigEndian.PutUint16(footer[56:], cylinders)
	footer[58], footer[59] = heads, sectors
	binary.BigEndian.PutUint32(footer[60:], diskType)
	id := vhdxRandomGUID()
	copy(footer[68:], id[:])
	binary.BigEndian.PutUint32(footer[64:], vhdChecksum(footer, 64))
	return footer
}

func createVHD(path string, size int64, fixed bool) (*vhdImage, error) {
	if size <= 0 || size%vhdxLogicalSector != 0 {
		return nil, fmt.Errorf("a VHD must be a whole number of 512-byte sectors, not %d bytes", size)
	}
	if size > vhdMaxSize {
		return nil, fmt.Errorf("a VHD can hold at most 2040 GB; this disk is %s. Choose VHDX instead", vmFormatBytes(size))
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*vhdImage, error) {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	v := &vhdImage{file: file, path: path, size: size, writable: true}
	if fixed {
		v.diskType, v.footer, v.end = vhdTypeFixed, vhdNewFooter(size, vhdTypeFixed, vhdNoOffset), size
		if err := file.Truncate(size + vhdFooterSize); err != nil {
			return fail(err)
		}
		if _, err := file.WriteAt(v.footer, size); err != nil {
			return fail(err)
		}
		v.fileSize = size + vhdFooterSize
		return v, nil
	}
	v.diskType, v.blockSize, v.bitmapSize = vhdTypeDynamic, vhdBlockSize, vhdxLogicalSector
	entries := (size + vhdBlockSize - 1) / vhdBlockSize
	v.bat = make([]uint32, entries)
	v.batOffset = vhdFooterSize + vhdHeaderSize
	batBytes := (entries*4 + vhdxLogicalSector - 1) / vhdxLogicalSector * vhdxLogicalSector
	v.footer = vhdNewFooter(size, vhdTypeDynamic, vhdFooterSize)
	header := make([]byte, vhdHeaderSize)
	copy(header, "cxsparse")
	binary.BigEndian.PutUint64(header[8:], vhdNoOffset)
	binary.BigEndian.PutUint64(header[16:], uint64(v.batOffset))
	binary.BigEndian.PutUint32(header[24:], 0x00010000)
	binary.BigEndian.PutUint32(header[28:], uint32(entries))
	binary.BigEndian.PutUint32(header[32:], vhdBlockSize)
	binary.BigEndian.PutUint32(header[36:], vhdChecksum(header, 36))
	bat := bytes.Repeat([]byte{0xFF}, int(batBytes))
	for i := range v.bat {
		v.bat[i] = vhdUnused
	}
	v.end = v.batOffset + batBytes
	for _, part := range []struct {
		data []byte
		at   int64
	}{{v.footer, 0}, {header, vhdFooterSize}, {bat, v.batOffset}, {v.footer, v.end}} {
		if _, err := file.WriteAt(part.data, part.at); err != nil {
			return fail(err)
		}
	}
	v.fileSize = v.end + vhdFooterSize
	return v, nil
}

func openVHDWriter(path string) (*vhdImage, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	v, err := loadVHD(file, path, info.Size(), true)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if v.diskType == vhdTypeDiffering {
		file.Close()
		return nil, errors.New("a differencing VHD cannot be a conversion target")
	}
	return v, nil
}

// WriteAt writes virtual disk bytes. On a dynamic disk a block is allocated
// only for non-zero data; zeros over existing data are written as zeros.
func (v *vhdImage) WriteAt(p []byte, off int64) (int, error) {
	if !v.writable {
		return 0, errors.New("the VHD is open read-only")
	}
	if off < 0 || off+int64(len(p)) > v.size {
		return 0, fmt.Errorf("write at %d+%d is outside the %d-byte disk", off, len(p), v.size)
	}
	if v.diskType == vhdTypeFixed {
		return vmWriteKeepingSparse(v.file, p, off)
	}
	total := 0
	for len(p) > 0 {
		block, inside := off/v.blockSize, off%v.blockSize
		n := int64(len(p))
		if n > v.blockSize-inside {
			n = v.blockSize - inside
		}
		if v.bat[block] == vhdUnused {
			if !vmIsZero(p[:n]) {
				if err := v.allocate(block, p[:n], inside); err != nil {
					return total, err
				}
			}
		} else if _, err := v.file.WriteAt(p[:n], int64(v.bat[block])*vhdxLogicalSector+v.bitmapSize+inside); err != nil {
			return total, err
		}
		p, off, total = p[n:], off+n, total+int(n)
	}
	return total, nil
}

func (v *vhdImage) allocate(block int64, data []byte, inside int64) error {
	// The data area starts on a 4 KB boundary, after its 512-byte bitmap.
	dataAt := (v.end + v.bitmapSize + 4095) / 4096 * 4096
	bitmapAt := dataAt - v.bitmapSize
	newEnd := dataAt + v.blockSize
	if err := v.file.Truncate(newEnd); err != nil {
		return err
	}
	bitmap := bytes.Repeat([]byte{0xFF}, int(v.bitmapSize))
	if _, err := v.file.WriteAt(bitmap, bitmapAt); err != nil {
		return err
	}
	if _, err := v.file.WriteAt(data, dataAt+inside); err != nil {
		return err
	}
	if _, err := v.file.WriteAt(v.footer[:vhdFooterSize], newEnd); err != nil {
		return err
	}
	entry := make([]byte, 4)
	binary.BigEndian.PutUint32(entry, uint32(bitmapAt/vhdxLogicalSector))
	if _, err := v.file.WriteAt(entry, v.batOffset+block*4); err != nil {
		return err
	}
	v.bat[block] = uint32(bitmapAt / vhdxLogicalSector)
	v.end = newEnd
	v.fileSize = newEnd + vhdFooterSize
	return nil
}

func (v *vhdImage) Flush() error { return v.file.Sync() }

// vmWriteKeepingSparse writes p into a plain file, skipping zero runs that
// are already zero there so a sparse target stays sparse.
func vmWriteKeepingSparse(file *os.File, p []byte, off int64) (int, error) {
	const step = 64 << 10
	total := 0
	existing := make([]byte, step)
	for len(p) > 0 {
		n := len(p)
		if n > step {
			n = step
		}
		part := p[:n]
		if vmIsZero(part) {
			read, err := file.ReadAt(existing[:n], off)
			if err != nil && err != io.EOF {
				return total, err
			}
			if vmIsZero(existing[:read]) {
				p, off, total = p[n:], off+int64(n), total+n
				continue
			}
		}
		if _, err := file.WriteAt(part, off); err != nil {
			return total, err
		}
		p, off, total = p[n:], off+int64(n), total+n
	}
	return total, nil
}
