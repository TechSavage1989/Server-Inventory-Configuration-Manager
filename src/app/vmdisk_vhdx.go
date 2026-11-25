package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// VHDX as a converter source: the dynamic and fixed disks the copy engine
// already reads, plus differencing disks (Hyper-V checkpoints, .avhdx),
// whose blocks may be absent (read from the parent) or partly present
// (a sector bitmap says which sectors are in this file).

type vmVHDXImage struct {
	disk   *vhdxFile
	parent vmDiskImage
	bat    []uint64 // the whole table, including sector bitmap entries
	size   int64
}

func openVHDXImage(path string, depth int) (*vmVHDXImage, error) {
	disk, err := openVHDXForRead(path)
	if err != nil {
		return nil, err
	}
	image := &vmVHDXImage{disk: disk, bat: disk.bat, size: disk.size}
	if !disk.differencing {
		return image, nil
	}
	if err := image.loadDifferencing(depth); err != nil {
		disk.Close()
		return nil, err
	}
	return image, nil
}

func openVHDXForRead(path string) (*vhdxFile, error) {
	file, err := openReadOnlyFile(path)
	if err != nil {
		return nil, err
	}
	v := &vhdxFile{file: file, path: path, readOnly: true, allowDifferencing: true}
	if err := v.load(); err != nil {
		file.Close()
		return nil, err
	}
	return v, nil
}

func (m *vmVHDXImage) loadDifferencing(depth int) error {
	disk := m.disk
	blocks := (disk.size + disk.blockSize - 1) / disk.blockSize
	chunks := (blocks + disk.chunkRatio - 1) / disk.chunkRatio
	entries := chunks * (disk.chunkRatio + 1)
	raw := make([]byte, entries*8)
	if _, err := disk.file.ReadAt(raw, disk.batAt); err != nil {
		return fmt.Errorf("reading the block table: %w", err)
	}
	m.bat = make([]uint64, entries)
	for i := range m.bat {
		m.bat[i] = binary.LittleEndian.Uint64(raw[i*8:])
	}
	locator, err := vhdxParentLocator(disk)
	if err != nil {
		return err
	}
	parentPath, err := vmResolveParentPath(disk.path, locator["relative_path"], locator["absolute_win32_path"], locator["volume_path"])
	if err != nil {
		return err
	}
	parent, err := openDiskImageDepth(parentPath, "", depth+1)
	if err != nil {
		return fmt.Errorf("opening the parent disk: %w", err)
	}
	if parentVHDX, ok := parent.(*vmVHDXImage); ok {
		linkage := strings.Trim(locator["parent_linkage"], "{}")
		alternate := strings.Trim(locator["parent_linkage2"], "{}")
		current := vhdxGUIDText(parentVHDX.disk.dataGUID)
		if linkage != "" && !strings.EqualFold(linkage, current) && !strings.EqualFold(alternate, current) {
			parent.Close()
			return fmt.Errorf("the parent disk %s has changed since this differencing disk was made (its data ID does not match); a checkpoint chain must be copied as a whole while the VM is off", parentPath)
		}
	}
	m.parent = parent
	return nil
}

// vhdxParentLocator reads the parent locator's key/value pairs.
func vhdxParentLocator(disk *vhdxFile) (map[string]string, error) {
	table := make([]byte, 64<<10)
	if _, err := disk.file.ReadAt(table, disk.metaOffset); err != nil {
		return nil, err
	}
	for i := 0; i < int(binary.LittleEndian.Uint16(table[10:])) && 32+i*32+32 <= len(table); i++ {
		entry := table[32+i*32:]
		var id [16]byte
		copy(id[:], entry[:16])
		if id != vhdxMetaParent {
			continue
		}
		length := int64(binary.LittleEndian.Uint32(entry[20:]))
		if length < 20 || length > 1<<20 {
			return nil, errors.New("the parent locator is damaged")
		}
		value := make([]byte, length)
		if _, err := disk.file.ReadAt(value, disk.metaOffset+int64(binary.LittleEndian.Uint32(entry[16:]))); err != nil {
			return nil, err
		}
		pairs := map[string]string{}
		count := int(binary.LittleEndian.Uint16(value[18:]))
		text := func(offset uint32, size uint16) string {
			end := int64(offset) + int64(size)
			if size%2 != 0 || end > length {
				return ""
			}
			units := make([]uint16, size/2)
			for j := range units {
				units[j] = binary.LittleEndian.Uint16(value[int(offset)+j*2:])
			}
			return string(utf16.Decode(units))
		}
		for j := 0; j < count && 20+j*12+12 <= len(value); j++ {
			item := value[20+j*12:]
			key := text(binary.LittleEndian.Uint32(item[0:]), binary.LittleEndian.Uint16(item[8:]))
			pairs[key] = text(binary.LittleEndian.Uint32(item[4:]), binary.LittleEndian.Uint16(item[10:]))
		}
		return pairs, nil
	}
	return nil, errors.New("the differencing disk has no parent locator")
}

// vhdxGUIDText formats a GUID stored in Windows' mixed-endian layout.
func vhdxGUIDText(id [16]byte) string {
	return fmt.Sprintf("%08X-%04X-%04X-%X-%X", binary.LittleEndian.Uint32(id[0:]), binary.LittleEndian.Uint16(id[4:]), binary.LittleEndian.Uint16(id[6:]), id[8:10], id[10:16])
}

func (m *vmVHDXImage) Size() int64      { return m.size }
func (m *vmVHDXImage) Describe() string { return m.disk.path }

func (m *vmVHDXImage) Close() error {
	if m.parent != nil {
		m.parent.Close()
		m.parent = nil
	}
	return m.disk.Close()
}

func (m *vmVHDXImage) state(block int64) (uint64, int64) {
	entry := m.bat[m.disk.batIndex(block)]
	return entry & 7, int64(entry>>20) * vhdxMB
}

func (m *vmVHDXImage) Info() vmDiskInfo {
	info := vmDiskInfo{Path: m.disk.path, Format: vmDiskFormatVHDX, FormatName: "VHDX", VirtualSize: m.size, Variant: "dynamic"}
	if stat, err := m.disk.file.Stat(); err == nil {
		info.FileSize = stat.Size()
	}
	if m.disk.differencing {
		info.Variant = "differencing"
	}
	present := int64(0)
	blocks := (m.size + m.disk.blockSize - 1) / m.disk.blockSize
	for block := int64(0); block < blocks; block++ {
		if state, _ := m.state(block); state == vhdxBlockFullPresent || state == vhdxBlockPartPresent {
			present++
		}
	}
	info.DataBytes = present * m.disk.blockSize
	if present == blocks && !m.disk.differencing {
		info.Variant = "fixed or fully allocated"
	}
	if m.disk.logical == 4096 {
		info.Notes = append(info.Notes, "Logical sector size is 4096 bytes; the converted disk uses 512-byte sectors")
	}
	return vmChainInfo(info, m.parent)
}

func (m *vmVHDXImage) Allocated() []vmCopyRange {
	own := []vmCopyRange{}
	blocks := (m.size + m.disk.blockSize - 1) / m.disk.blockSize
	for block := int64(0); block < blocks; block++ {
		if state, _ := m.state(block); state == vhdxBlockFullPresent || state == vhdxBlockPartPresent {
			own = append(own, vmCopyRange{Start: block * m.disk.blockSize, Length: m.disk.blockSize})
		}
	}
	return vmChainAllocated(own, m.parent, m.size)
}

func (m *vmVHDXImage) ReadAt(p []byte, off int64) (int, error) {
	return vmReadLimited(m.size, p, off, m.read)
}

func (m *vmVHDXImage) read(p []byte, off int64) error {
	blockSize := m.disk.blockSize
	for len(p) > 0 {
		block, inside := off/blockSize, off%blockSize
		n := int64(len(p))
		if n > blockSize-inside {
			n = blockSize - inside
		}
		if err := m.readBlock(p[:n], block, inside); err != nil {
			return err
		}
		p, off = p[n:], off+n
	}
	return nil
}

func (m *vmVHDXImage) readBlock(p []byte, block, inside int64) error {
	state, at := m.state(block)
	virtual := block*m.disk.blockSize + inside
	switch state {
	case vhdxBlockFullPresent:
		_, err := io.ReadFull(io.NewSectionReader(m.disk.file, at+inside, int64(len(p))), p)
		return err
	case vhdxBlockPartPresent:
		if !m.disk.differencing {
			_, err := io.ReadFull(io.NewSectionReader(m.disk.file, at+inside, int64(len(p))), p)
			return err
		}
		return m.readPartial(p, block, inside, at)
	case 2, 3: // zero, unmapped
		vmZeroFill(p)
		return nil
	}
	// Not present or undefined: the parent's data, or zeros without one.
	if m.disk.differencing {
		_, err := vmParentReader{m.parent}.ReadAt(p, virtual)
		return err
	}
	vmZeroFill(p)
	return nil
}

// readPartial reads a partly present block of a differencing disk. Each
// bit of the chunk's sector bitmap (least significant first) says whether
// a logical sector is in this file or in the parent.
func (m *vmVHDXImage) readPartial(p []byte, block, inside, at int64) error {
	disk := m.disk
	chunk := block / disk.chunkRatio
	entry := m.bat[chunk*(disk.chunkRatio+1)+disk.chunkRatio]
	if entry&7 != vhdxBlockFullPresent {
		return fmt.Errorf("block %d is partly present but its sector bitmap is missing", block)
	}
	bitmapAt := int64(entry>>20) * vhdxMB
	sector := disk.logical
	firstSector := (block%disk.chunkRatio)*disk.blockSize/sector + inside/sector
	lastSector := (block%disk.chunkRatio)*disk.blockSize/sector + (inside+int64(len(p))-1)/sector
	bits := make([]byte, lastSector/8-firstSector/8+1)
	if _, err := disk.file.ReadAt(bits, bitmapAt+firstSector/8); err != nil {
		return err
	}
	isLocal := func(s int64) bool {
		index := s - firstSector/8*8
		return bits[index/8]&(1<<uint(index%8)) != 0
	}
	position := inside
	for len(p) > 0 {
		s := (block%disk.chunkRatio)*disk.blockSize/sector + position/sector
		local := isLocal(s)
		run := sector - position%sector
		for next := s + 1; run < int64(len(p)) && next <= lastSector && isLocal(next) == local; next++ {
			run += sector
		}
		if run > int64(len(p)) {
			run = int64(len(p))
		}
		var err error
		if local {
			_, err = io.ReadFull(io.NewSectionReader(disk.file, at+position, run), p[:run])
		} else {
			_, err = vmParentReader{m.parent}.ReadAt(p[:run], block*disk.blockSize+position)
		}
		if err != nil {
			return err
		}
		p, position = p[run:], position+run
	}
	return nil
}
