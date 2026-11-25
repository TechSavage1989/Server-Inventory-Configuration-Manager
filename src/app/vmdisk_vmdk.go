package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// VMDK as a converter source and target. Read: descriptors with flat,
// zero and hosted sparse extents (monolithicSparse, twoGbMaxExtentSparse,
// streamOptimized with compressed grains) and hosted snapshot chains named
// by parentFileNameHint. ESXi snapshot deltas (VMFSSPARSE, SESPARSE) are
// refused: consolidating snapshots in vSphere is the safe way to merge them.
// Written: monolithicSparse (one file) and the ESXi flat layout (a
// descriptor plus -flat.vmdk).

const (
	vmdkGrainSectors  = 128
	vmdkGTEsPerGT     = 512
	vmdkGDAtEnd       = 0xFFFFFFFFFFFFFFFF
	vmdkFlagNewline   = 1
	vmdkFlagRedundant = 2
	vmdkFlagZeroGrain = 4
	vmdkFlagCompress  = 1 << 16
	vmdkFlagMarkers   = 1 << 17
	vmdkSparseMaxSize = 2040 << 30
	vmdkDescriptorLen = 20
)

// vmdkSparse is one hosted sparse extent file.
type vmdkSparse struct {
	file        *os.File
	path        string
	capacity    int64 // bytes
	grain       int64 // bytes
	gtes        int64
	flags       uint32
	gd          []uint32
	rgd         []uint32
	gtCache     map[uint32][]uint32
	descriptor  string
	writable    bool
	end         int64 // bytes
	lastGrainAt int64
	lastGrain   []byte
}

func vmdkReadHeader(file *os.File, at int64) ([]byte, error) {
	header := make([]byte, 512)
	if _, err := file.ReadAt(header, at); err != nil {
		return nil, err
	}
	if string(header[:4]) != "KDMV" {
		return nil, errors.New("this is not a hosted sparse VMDK")
	}
	return header, nil
}

func openVMDKSparse(file *os.File, path string, writable bool) (*vmdkSparse, error) {
	header, err := vmdkReadHeader(file, 0)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	s := &vmdkSparse{file: file, path: path, writable: writable, gtCache: map[uint32][]uint32{}, lastGrainAt: -1}
	descriptorAt, descriptorSize := int64(le.Uint64(header[28:])), int64(le.Uint64(header[36:]))
	if descriptorAt > 0 && descriptorSize > 0 && descriptorSize <= 128 {
		text := make([]byte, descriptorSize*512)
		if _, err := file.ReadAt(text, descriptorAt*512); err == nil {
			if end := bytes.IndexByte(text, 0); end >= 0 {
				text = text[:end]
			}
			s.descriptor = string(text)
		}
	}
	if le.Uint64(header[56:]) == vmdkGDAtEnd {
		// streamOptimized: the real header is the footer before the
		// end-of-stream marker.
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if header, err = vmdkReadHeader(file, info.Size()-1024); err != nil {
			return nil, errors.New("this stream-optimized VMDK has no footer; it may be incomplete")
		}
	}
	version := le.Uint32(header[4:])
	if version < 1 || version > 3 {
		return nil, fmt.Errorf("sparse VMDK version %d is not supported", version)
	}
	s.flags = le.Uint32(header[8:])
	s.capacity = int64(le.Uint64(header[12:])) * 512
	s.grain = int64(le.Uint64(header[20:])) * 512
	s.gtes = int64(le.Uint32(header[44:]))
	if s.grain < 4096 || s.grain > 1<<20 || s.grain&(s.grain-1) != 0 || s.gtes <= 0 || s.gtes > 4096 || s.capacity <= 0 {
		return nil, errors.New("the sparse VMDK header has unsupported values")
	}
	if s.flags&vmdkFlagCompress != 0 && le.Uint16(header[77:]) != 1 {
		return nil, errors.New("the sparse VMDK uses an unknown compression method")
	}
	count := (s.capacity + s.grain*s.gtes - 1) / (s.grain * s.gtes)
	read := func(sector int64) ([]uint32, error) {
		raw := make([]byte, count*4)
		if _, err := file.ReadAt(raw, sector*512); err != nil {
			return nil, err
		}
		table := make([]uint32, count)
		for i := range table {
			table[i] = le.Uint32(raw[i*4:])
		}
		return table, nil
	}
	if s.gd, err = read(int64(le.Uint64(header[56:]))); err != nil {
		return nil, fmt.Errorf("reading the grain directory: %w", err)
	}
	if writable && s.flags&vmdkFlagRedundant != 0 {
		if s.rgd, err = read(int64(le.Uint64(header[48:]))); err != nil {
			return nil, fmt.Errorf("reading the redundant grain directory: %w", err)
		}
	}
	return s, nil
}

func (s *vmdkSparse) gt(index int64) ([]uint32, error) {
	if index >= int64(len(s.gd)) || s.gd[index] == 0 {
		return nil, nil
	}
	sector := s.gd[index]
	if table, ok := s.gtCache[sector]; ok {
		return table, nil
	}
	raw := make([]byte, s.gtes*4)
	if _, err := s.file.ReadAt(raw, int64(sector)*512); err != nil {
		return nil, fmt.Errorf("reading a grain table: %w", err)
	}
	table := make([]uint32, s.gtes)
	for i := range table {
		table[i] = binary.LittleEndian.Uint32(raw[i*4:])
	}
	if len(s.gtCache) > 1024 {
		s.gtCache = map[uint32][]uint32{}
	}
	s.gtCache[sector] = table
	return table, nil
}

func (s *vmdkSparse) entry(grain int64) (uint32, error) {
	table, err := s.gt(grain / s.gtes)
	if err != nil || table == nil {
		return 0, err
	}
	return table[grain%s.gtes], nil
}

func (s *vmdkSparse) zeroGrain(entry uint32) bool {
	return entry == 1 && s.flags&vmdkFlagZeroGrain != 0
}

// read fills p from the extent at off; unallocated grains come from parent.
func (s *vmdkSparse) read(p []byte, off int64, parent func(p []byte, off int64) error) error {
	for len(p) > 0 {
		grain, inside := off/s.grain, off%s.grain
		n := s.grain - inside
		if n > int64(len(p)) {
			n = int64(len(p))
		}
		entry, err := s.entry(grain)
		if err != nil {
			return err
		}
		switch {
		case entry == 0:
			if err := parent(p[:n], off); err != nil {
				return err
			}
		case s.zeroGrain(entry):
			vmZeroFill(p[:n])
		case s.flags&vmdkFlagCompress != 0:
			data, err := s.inflate(int64(entry) * 512)
			if err != nil {
				return err
			}
			copy(p[:n], data[inside:])
		default:
			if _, err := io.ReadFull(io.NewSectionReader(s.file, int64(entry)*512+inside, n), p[:n]); err != nil {
				return fmt.Errorf("reading grain %d: %w", grain, err)
			}
		}
		p, off = p[n:], off+n
	}
	return nil
}

func (s *vmdkSparse) inflate(at int64) ([]byte, error) {
	if at == s.lastGrainAt {
		return s.lastGrain, nil
	}
	marker := make([]byte, 12)
	if _, err := s.file.ReadAt(marker, at); err != nil {
		return nil, err
	}
	length := int64(binary.LittleEndian.Uint32(marker[8:]))
	if length <= 0 || length > 2*s.grain+4096 {
		return nil, errors.New("a compressed grain has an invalid length")
	}
	raw := make([]byte, length)
	if _, err := s.file.ReadAt(raw, at+12); err != nil {
		return nil, err
	}
	reader, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("a compressed grain could not be read: %w", err)
	}
	defer reader.Close()
	out := make([]byte, s.grain)
	if _, err := io.ReadFull(reader, out); err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("a compressed grain could not be expanded: %w", err)
	}
	s.lastGrainAt, s.lastGrain = at, out
	return out, nil
}

func (s *vmdkSparse) allocated(start int64) ([]vmCopyRange, int64) {
	ranges := []vmCopyRange{}
	data := int64(0)
	for index := range s.gd {
		table, err := s.gt(int64(index))
		if err != nil {
			return nil, 0
		}
		for i, entry := range table {
			grain := int64(index)*s.gtes + int64(i)
			if entry == 0 || s.zeroGrain(entry) || grain*s.grain >= s.capacity {
				continue
			}
			ranges = append(ranges, vmCopyRange{Start: start + grain*s.grain, Length: s.grain})
			data += s.grain
		}
	}
	return ranges, data
}

// The whole VMDK: one or more extents and an optional parent.

type vmdkPart struct {
	start, length int64
	kind          string // FLAT, ZERO or SPARSE
	reader        io.ReaderAt
	offset        int64
	sparse        *vmdkSparse
}

type vmdkImage struct {
	path       string
	createType string
	size       int64
	parts      []vmdkPart
	files      []*os.File
	parent     vmDiskImage
	cid        string
	fileSize   int64
}

type vmdkDescriptorExtra struct {
	CID, ParentCID, ParentHint, CreateType string
}

func parseVMDKExtra(text string) vmdkDescriptorExtra {
	extra := vmdkDescriptorExtra{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "CID":
			extra.CID = strings.ToLower(value)
		case "parentCID":
			extra.ParentCID = strings.ToLower(value)
		case "parentFileNameHint":
			extra.ParentHint = value
		case "createType":
			extra.CreateType = value
		}
	}
	return extra
}

func openVMDKImage(file *os.File, path string, fileSize int64, depth int) (*vmdkImage, error) {
	image := &vmdkImage{path: path, fileSize: fileSize}
	head := make([]byte, 4)
	file.ReadAt(head, 0)
	var descriptorText string
	if string(head) == "KDMV" {
		sparse, err := openVMDKSparse(file, path, false)
		if err != nil {
			return nil, err
		}
		image.files = append(image.files, file)
		descriptorText = sparse.descriptor
		image.parts = []vmdkPart{{start: 0, length: sparse.capacity, kind: "SPARSE", sparse: sparse}}
		image.size = sparse.capacity
	} else {
		raw, err := io.ReadAll(io.LimitReader(file, 64<<10))
		if err != nil {
			return nil, err
		}
		file.Close()
		if end := bytes.IndexByte(raw, 0); end >= 0 {
			if !vmIsZero(raw[end:]) {
				return nil, errors.New("this VMDK descriptor contains binary data")
			}
			raw = raw[:end]
		}
		descriptorText = string(raw)
		if err := image.openExtents(descriptorText); err != nil {
			image.Close()
			return nil, err
		}
	}
	extra := parseVMDKExtra(descriptorText)
	image.cid, image.createType = extra.CID, extra.CreateType
	if image.createType == "" {
		image.createType = "monolithicSparse"
	}
	if extra.ParentCID != "" && extra.ParentCID != "ffffffff" {
		if extra.ParentHint == "" {
			image.Close()
			return nil, errors.New("this VMDK is a snapshot delta but does not name its parent disk")
		}
		parentPath, err := vmResolveParentPath(path, extra.ParentHint)
		if err != nil {
			image.Close()
			return nil, err
		}
		parent, err := openDiskImageDepth(parentPath, "", depth+1)
		if err != nil {
			image.Close()
			return nil, fmt.Errorf("opening the parent disk: %w", err)
		}
		if parentVMDK, ok := parent.(*vmdkImage); ok && parentVMDK.cid != "" && parentVMDK.cid != extra.ParentCID {
			parent.Close()
			image.Close()
			return nil, fmt.Errorf("the parent disk %s has changed since this snapshot was taken (content ID %s, expected %s)", parentPath, parentVMDK.cid, extra.ParentCID)
		}
		image.parent = parent
	}
	return image, nil
}

func (m *vmdkImage) openExtents(text string) error {
	descriptor, err := parseVMDKDescriptor(text)
	if err != nil {
		return err
	}
	folder := filepath.Dir(m.path)
	for _, extent := range descriptor.Extents {
		part := vmdkPart{start: m.size, length: extent.Sectors * 512, kind: extent.Type, offset: extent.Offset * 512}
		switch extent.Type {
		case "ZERO":
		case "FLAT", "VMFS", "SPARSE":
			if extent.File == "" || strings.ContainsAny(extent.File, `/\`) || extent.File == "." || extent.File == ".." {
				return fmt.Errorf("the VMDK extent %q is not a file beside the descriptor", extent.File)
			}
			file, err := openReadOnlyFile(filepath.Join(folder, extent.File))
			if err != nil {
				return fmt.Errorf("could not open the extent %s: %w", extent.File, err)
			}
			m.files = append(m.files, file)
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if extent.Type == "SPARSE" {
				sparse, err := openVMDKSparse(file, file.Name(), false)
				if err != nil {
					return fmt.Errorf("%s: %w", extent.File, err)
				}
				part.sparse = sparse
				break
			}
			part.kind, part.reader = "FLAT", file
			if info.Size() < part.offset+part.length {
				return fmt.Errorf("%s is %d bytes, smaller than its descriptor says (%d)", extent.File, info.Size(), part.offset+part.length)
			}
		case "VMFSRDM", "VMFSPASSTHROUGHRAW":
			return errors.New("this is a raw device mapping; copy its LUN contents from inside the guest or with the storage array instead")
		case "VMFSSPARSE", "SESPARSE":
			return errors.New("this disk is an ESXi snapshot delta; remove all snapshots (Prepare source) so the disk is consolidated, then convert the base disk")
		default:
			return fmt.Errorf("the VMDK extent type %s is not supported", extent.Type)
		}
		m.parts = append(m.parts, part)
		m.size += part.length
	}
	if m.size == 0 {
		return errors.New("the VMDK has no data extents")
	}
	return nil
}

func (m *vmdkImage) Size() int64      { return m.size }
func (m *vmdkImage) Describe() string { return m.path }

func (m *vmdkImage) Close() error {
	if m.parent != nil {
		m.parent.Close()
		m.parent = nil
	}
	var first error
	for _, file := range m.files {
		if err := file.Close(); err != nil && first == nil {
			first = err
		}
	}
	m.files = nil
	return first
}

func (m *vmdkImage) Info() vmDiskInfo {
	info := vmDiskInfo{Path: m.path, Format: vmDiskFormatVMDK, FormatName: "VMDK", Variant: m.createType, VirtualSize: m.size, FileSize: m.fileSize}
	for _, part := range m.parts {
		switch part.kind {
		case "FLAT":
			info.DataBytes += part.length
		case "SPARSE":
			_, data := part.sparse.allocated(part.start)
			info.DataBytes += data
			if part.sparse.flags&vmdkFlagCompress != 0 {
				info.Notes = append(info.Notes, "Stream-optimized (compressed) grains are expanded in the converted disk")
			}
		}
	}
	if len(m.parts) > 1 {
		info.Notes = append(info.Notes, fmt.Sprintf("%d extent files are joined into one disk", len(m.parts)))
	}
	info.Notes = vmUniqueStrings(info.Notes)
	return vmChainInfo(info, m.parent)
}

func (m *vmdkImage) Allocated() []vmCopyRange {
	own := []vmCopyRange{}
	for _, part := range m.parts {
		switch part.kind {
		case "FLAT":
			own = append(own, vmCopyRange{Start: part.start, Length: part.length})
		case "SPARSE":
			ranges, _ := part.sparse.allocated(part.start)
			if ranges == nil {
				return nil
			}
			own = append(own, ranges...)
		}
	}
	return vmChainAllocated(own, m.parent, m.size)
}

func (m *vmdkImage) ReadAt(p []byte, off int64) (int, error) {
	return vmReadLimited(m.size, p, off, m.read)
}

func (m *vmdkImage) read(p []byte, off int64) error {
	for _, part := range m.parts {
		if len(p) == 0 {
			break
		}
		if off < part.start || off >= part.start+part.length {
			continue
		}
		inside := off - part.start
		n := int64(len(p))
		if n > part.length-inside {
			n = part.length - inside
		}
		var err error
		switch part.kind {
		case "ZERO":
			vmZeroFill(p[:n])
		case "FLAT":
			_, err = io.ReadFull(io.NewSectionReader(part.reader, part.offset+inside, n), p[:n])
		case "SPARSE":
			start := part.start
			err = part.sparse.read(p[:n], inside, func(q []byte, local int64) error {
				_, err := vmParentReader{m.parent}.ReadAt(q, start+local)
				return err
			})
		}
		if err != nil {
			return err
		}
		p, off = p[n:], off+n
	}
	if len(p) > 0 {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// Writing.

func vmdkCID() string {
	id := vhdxRandomGUID()
	return fmt.Sprintf("%08x", binary.LittleEndian.Uint32(id[:]))
}

func vmdkNewDescriptor(createType, extentType, extentFile string, size int64) string {
	sectors := size / 512
	cylinders := sectors / (255 * 63)
	if cylinders > 65535 {
		cylinders = 65535
	}
	uuid := vhdxRandomGUID()
	var b strings.Builder
	b.WriteString("# Disk DescriptorFile\nversion=1\nencoding=\"UTF-8\"\n")
	b.WriteString("CID=" + vmdkCID() + "\nparentCID=ffffffff\n")
	b.WriteString("createType=\"" + createType + "\"\n\n# Extent description\n")
	b.WriteString("RW " + strconv.FormatInt(sectors, 10) + " " + extentType + " \"" + extentFile + "\"\n\n")
	b.WriteString("# The Disk Data Base\n#DDB\n\n")
	b.WriteString("ddb.adapterType = \"lsilogic\"\n")
	b.WriteString("ddb.geometry.cylinders = \"" + strconv.FormatInt(cylinders, 10) + "\"\n")
	b.WriteString("ddb.geometry.heads = \"255\"\nddb.geometry.sectors = \"63\"\n")
	b.WriteString(fmt.Sprintf("ddb.uuid = \"%x\"\n", uuid[:]))
	b.WriteString("ddb.virtualHWVersion = \"4\"\n")
	return b.String()
}

// createVMDKSparse writes an empty monolithicSparse VMDK with redundant
// grain tables preallocated after the header.
func createVMDKSparse(path string, size int64) (*vmdkSparse, error) {
	if size <= 0 || size%vhdxLogicalSector != 0 {
		return nil, fmt.Errorf("a VMDK must be a whole number of 512-byte sectors, not %d bytes", size)
	}
	if size > vmdkSparseMaxSize {
		return nil, fmt.Errorf("a sparse VMDK holds at most 2040 GB; this disk is %s. Choose VMDK (ESXi flat) instead", vmFormatBytes(size))
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*vmdkSparse, error) {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	sectors := size / 512
	tables := (sectors + vmdkGrainSectors*vmdkGTEsPerGT - 1) / (vmdkGrainSectors * vmdkGTEsPerGT)
	gdSectors := (tables*4 + 511) / 512
	gtSectors := int64(vmdkGTEsPerGT * 4 / 512)
	rgdAt := int64(1 + vmdkDescriptorLen)
	rgtAt := rgdAt + gdSectors
	gdAt := rgtAt + tables*gtSectors
	gtAt := gdAt + gdSectors
	overhead := (gtAt + tables*gtSectors + vmdkGrainSectors - 1) / vmdkGrainSectors * vmdkGrainSectors
	le := binary.LittleEndian
	header := make([]byte, 512)
	copy(header, "KDMV")
	le.PutUint32(header[4:], 1)
	le.PutUint32(header[8:], vmdkFlagNewline|vmdkFlagRedundant)
	le.PutUint64(header[12:], uint64(sectors))
	le.PutUint64(header[20:], vmdkGrainSectors)
	le.PutUint64(header[28:], 1)
	le.PutUint64(header[36:], vmdkDescriptorLen)
	le.PutUint32(header[44:], vmdkGTEsPerGT)
	le.PutUint64(header[48:], uint64(rgdAt))
	le.PutUint64(header[56:], uint64(gdAt))
	le.PutUint64(header[64:], uint64(overhead))
	header[73], header[74], header[75], header[76] = '\n', ' ', '\r', '\n'
	descriptor := vmdkNewDescriptor("monolithicSparse", "SPARSE", filepath.Base(path), size)
	if len(descriptor) > vmdkDescriptorLen*512 {
		return fail(errors.New("the VMDK descriptor is too long"))
	}
	if err := file.Truncate(overhead * 512); err != nil {
		return fail(err)
	}
	if _, err := file.WriteAt(header, 0); err != nil {
		return fail(err)
	}
	if _, err := file.WriteAt([]byte(descriptor), 512); err != nil {
		return fail(err)
	}
	s := &vmdkSparse{file: file, path: path, capacity: size, grain: vmdkGrainSectors * 512, gtes: vmdkGTEsPerGT, flags: vmdkFlagNewline | vmdkFlagRedundant, writable: true, gtCache: map[uint32][]uint32{}, lastGrainAt: -1, end: overhead * 512, descriptor: descriptor}
	s.gd, s.rgd = make([]uint32, tables), make([]uint32, tables)
	directory := func(at, tableAt int64, out []uint32) error {
		raw := make([]byte, gdSectors*512)
		for i := int64(0); i < tables; i++ {
			out[i] = uint32(tableAt + i*gtSectors)
			le.PutUint32(raw[i*4:], out[i])
		}
		_, err := file.WriteAt(raw, at*512)
		return err
	}
	if err := directory(rgdAt, rgtAt, s.rgd); err != nil {
		return fail(err)
	}
	if err := directory(gdAt, gtAt, s.gd); err != nil {
		return fail(err)
	}
	return s, nil
}

func openVMDKSparseWriter(path string) (*vmdkSparse, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	s, err := openVMDKSparse(file, path, true)
	if err == nil && (s.flags&(vmdkFlagCompress|vmdkFlagRedundant) != vmdkFlagRedundant || s.grain != vmdkGrainSectors*512 || s.gtes != vmdkGTEsPerGT) {
		err = errors.New("this VMDK was not written by this application's converter")
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	header, _ := vmdkReadHeader(file, 0)
	s.end = int64(binary.LittleEndian.Uint64(header[64:])) * 512
	for index := range s.gd {
		table, err := s.gt(int64(index))
		if err != nil {
			file.Close()
			return nil, err
		}
		for _, entry := range table {
			if at := int64(entry)*512 + s.grain; entry > 1 && at > s.end {
				s.end = at
			}
		}
	}
	if err := file.Truncate(s.end); err != nil {
		file.Close()
		return nil, err
	}
	return s, nil
}

func (s *vmdkSparse) Size() int64 { return s.capacity }

func (s *vmdkSparse) ReadAt(p []byte, off int64) (int, error) {
	return vmReadLimited(s.capacity, p, off, func(p []byte, off int64) error {
		return s.read(p, off, func(q []byte, _ int64) error { vmZeroFill(q); return nil })
	})
}

// WriteAt writes guest bytes, allocating a grain only for non-zero data.
func (s *vmdkSparse) WriteAt(p []byte, off int64) (int, error) {
	if !s.writable {
		return 0, errors.New("the VMDK is open read-only")
	}
	if off < 0 || off+int64(len(p)) > s.capacity {
		return 0, fmt.Errorf("write at %d+%d is outside the %d-byte disk", off, len(p), s.capacity)
	}
	total := 0
	for len(p) > 0 {
		grain, inside := off/s.grain, off%s.grain
		n := s.grain - inside
		if n > int64(len(p)) {
			n = int64(len(p))
		}
		entry, err := s.entry(grain)
		if err != nil {
			return total, err
		}
		if entry > 1 {
			if _, err := s.file.WriteAt(p[:n], int64(entry)*512+inside); err != nil {
				return total, err
			}
		} else if !vmIsZero(p[:n]) {
			at := s.end
			if at/512 > 0xFFFFFFFF-uint64Sectors(s.grain) {
				return total, errors.New("the sparse VMDK is full")
			}
			if err := s.file.Truncate(at + s.grain); err != nil {
				return total, err
			}
			if _, err := s.file.WriteAt(p[:n], at+inside); err != nil {
				return total, err
			}
			s.end = at + s.grain
			value := make([]byte, 4)
			binary.LittleEndian.PutUint32(value, uint32(at/512))
			index, slot := grain/s.gtes, grain%s.gtes
			for _, directory := range [][]uint32{s.rgd, s.gd} {
				if _, err := s.file.WriteAt(value, int64(directory[index])*512+slot*4); err != nil {
					return total, err
				}
			}
			table, err := s.gt(index)
			if err != nil {
				return total, err
			}
			table[slot] = uint32(at / 512)
		}
		p, off, total = p[n:], off+n, total+int(n)
	}
	return total, nil
}

func uint64Sectors(bytes int64) int64 { return bytes / 512 }

func (s *vmdkSparse) Flush() error { return s.file.Sync() }
func (s *vmdkSparse) Close() error { return s.file.Close() }

// The ESXi flat layout: a text descriptor and a raw -flat.vmdk.

func vmdkFlatDataPath(descriptor string) string {
	return strings.TrimSuffix(strings.TrimSuffix(descriptor, ".vmdk"), ".VMDK") + "-flat.vmdk"
}

func createVMDKFlat(path string, size int64) (*vmRawWriter, error) {
	data := vmdkFlatDataPath(path)
	if _, err := os.Stat(data); err == nil {
		return nil, fmt.Errorf("%s already exists and was not created by this copy; choose a new file name so nothing is overwritten", data)
	}
	descriptor := vmdkNewDescriptor("vmfs", "VMFS", filepath.Base(data), size)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if _, err := file.WriteString(descriptor); err != nil {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return nil, err
	}
	writer, err := createRawWriter(data, size)
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	return writer, nil
}

func openVMDKFlatWriter(path string) (*vmRawWriter, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	descriptor, err := parseVMDKDescriptor(string(text))
	if err != nil {
		return nil, err
	}
	data := vmdkFlatDataPath(path)
	if len(descriptor.Extents) != 1 || descriptor.Extents[0].File != filepath.Base(data) {
		return nil, fmt.Errorf("%s was not written by this application's converter", path)
	}
	return openRawWriter(data, descriptor.Extents[0].Sectors*512)
}
