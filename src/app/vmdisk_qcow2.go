package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// qcow2 (QEMU copy-on-write, versions 2 and 3): read with backing files,
// zero clusters and zlib-compressed clusters; written as version 3 with
// 64 KB clusters, no compression and 16-bit reference counts.
//
// The writer appends clusters and keeps the reference counts implicit while
// a copy runs (every cluster below the end of the data is used exactly
// once), then writes the reference count blocks and table after the data
// on Close. Reopening for a resumed or repeated copy cuts the old count
// area off again, so a stopped copy never leaves a cluster counted twice.

const (
	qcow2Magic          = "QFI\xfb"
	qcow2OffsetMask     = 0x00fffffffffffe00
	qcow2Copied         = uint64(1) << 63
	qcow2Compressed     = uint64(1) << 62
	qcow2ZeroFlag       = uint64(1)
	qcow2WriteBits      = 16
	qcow2HeaderLength   = 112
	qcow2RefcountOrder  = 4
	qcow2L2CacheEntries = 256
	qcow2IncDirty       = 1
	qcow2IncCorrupt     = 2
	qcow2IncDataFile    = 4
	qcow2IncCompression = 8
	qcow2IncExtendedL2  = 16
)

type qcow2Image struct {
	file        *os.File
	path        string
	fileSize    int64
	version     uint32
	clusterBits uint
	cluster     int64
	size        int64
	l1          []uint64
	l1Offset    int64
	l2Cache     map[int64][]uint64
	parent      vmDiskImage
	backing     string
	writable    bool
	end         int64
	lastZipAt   int64
	lastZip     []byte
	header      []byte
}

func openQCOW2Image(file *os.File, path string, fileSize int64, depth int) (*qcow2Image, error) {
	q, err := loadQCOW2(file, path, fileSize)
	if err != nil {
		return nil, err
	}
	if q.backing != "" {
		if err := q.openBacking(depth); err != nil {
			return nil, err
		}
	}
	return q, nil
}

func loadQCOW2(file *os.File, path string, fileSize int64) (*qcow2Image, error) {
	header := make([]byte, 4096)
	n, err := file.ReadAt(header, 0)
	if n < 72 || string(header[:4]) != qcow2Magic {
		if err == nil {
			err = errors.New("not a qcow2 image")
		}
		return nil, err
	}
	q := &qcow2Image{file: file, path: path, fileSize: fileSize, l2Cache: map[int64][]uint64{}, lastZipAt: -1}
	be := binary.BigEndian
	q.version = be.Uint32(header[4:])
	if q.version != 2 && q.version != 3 {
		return nil, fmt.Errorf("qcow2 version %d is not supported", q.version)
	}
	q.clusterBits = uint(be.Uint32(header[20:]))
	if q.clusterBits < 9 || q.clusterBits > 21 {
		return nil, fmt.Errorf("the qcow2 cluster size 2^%d is not valid", q.clusterBits)
	}
	q.cluster = 1 << q.clusterBits
	q.size = int64(be.Uint64(header[24:]))
	if be.Uint32(header[32:]) != 0 {
		return nil, errors.New("this qcow2 image is encrypted; decrypt it with qemu-img convert first")
	}
	headerLength := uint32(72)
	if q.version == 3 {
		incompatible := be.Uint64(header[72:])
		headerLength = be.Uint32(header[100:])
		if headerLength < 104 || int(headerLength) > n {
			return nil, errors.New("the qcow2 header length is invalid")
		}
		switch {
		case incompatible&qcow2IncCorrupt != 0:
			return nil, errors.New("QEMU has marked this qcow2 image as corrupt; repair it with qemu-img check -r all first")
		case incompatible&qcow2IncDataFile != 0:
			return nil, errors.New("this qcow2 image keeps its data in a separate file, which is not supported")
		case incompatible&qcow2IncExtendedL2 != 0:
			return nil, errors.New("this qcow2 image uses extended L2 entries (subclusters), which are not supported")
		case incompatible&qcow2IncCompression != 0 && headerLength > 104 && header[104] != 0:
			return nil, errors.New("this qcow2 image uses zstd compression, which is not supported; convert it with qemu-img first")
		case incompatible&^uint64(qcow2IncDirty|qcow2IncCompression) != 0:
			return nil, errors.New("this qcow2 image uses features this application does not understand")
		}
	}
	if q.size <= 0 {
		return nil, errors.New("the qcow2 image has no disk size")
	}
	if q.size%vhdxLogicalSector != 0 {
		return nil, fmt.Errorf("the qcow2 disk is %d bytes, not a whole number of 512-byte sectors", q.size)
	}
	l1Size := int64(be.Uint32(header[36:]))
	q.l1Offset = int64(be.Uint64(header[40:]))
	perL2 := q.cluster / 8
	if l1Size < (q.size+q.cluster*perL2-1)/(q.cluster*perL2) || l1Size > 1<<25 {
		return nil, errors.New("the qcow2 L1 table is too small for the disk size")
	}
	raw := make([]byte, l1Size*8)
	if _, err := file.ReadAt(raw, q.l1Offset); err != nil {
		return nil, fmt.Errorf("reading the qcow2 L1 table: %w", err)
	}
	q.l1 = make([]uint64, l1Size)
	for i := range q.l1 {
		q.l1[i] = be.Uint64(raw[i*8:])
	}
	if offset, size := int64(be.Uint64(header[8:])), int64(be.Uint32(header[16:])); offset != 0 && size > 0 {
		if size > 1023 {
			return nil, errors.New("the qcow2 backing file name is too long")
		}
		name := make([]byte, size)
		if _, err := file.ReadAt(name, offset); err != nil {
			return nil, err
		}
		q.backing = string(name)
	}
	q.header = header[:headerLength]
	return q, nil
}

// backingFormat reads the backing format header extension, if any.
func (q *qcow2Image) backingFormat() string {
	be := binary.BigEndian
	start := int64(len(q.header))
	buffer := make([]byte, q.cluster)
	n, _ := q.file.ReadAt(buffer, 0)
	buffer = buffer[:n]
	for at := start; at+8 <= int64(len(buffer)); {
		kind, length := be.Uint32(buffer[at:]), int64(be.Uint32(buffer[at+4:]))
		if kind == 0 || at+8+length > int64(len(buffer)) {
			break
		}
		if kind == 0xE2792ACA {
			return string(buffer[at+8 : at+8+length])
		}
		at += 8 + (length+7)/8*8
	}
	return ""
}

func (q *qcow2Image) openBacking(depth int) error {
	if bytes.Contains([]byte(q.backing), []byte("://")) || bytes.HasPrefix([]byte(q.backing), []byte("json:")) {
		return fmt.Errorf("the backing file %q is not a local file", q.backing)
	}
	format := ""
	switch q.backingFormat() {
	case "raw":
		format = vmDiskFormatRaw
	case "qcow2":
		format = vmDiskFormatQCOW2
	case "vpc":
		format = vmDiskFormatVHD
	case "vhdx":
		format = vmDiskFormatVHDX
	case "vmdk":
		format = vmDiskFormatVMDK
	case "":
	default:
		return fmt.Errorf("the backing file format %q is not supported", q.backingFormat())
	}
	path, err := vmResolveParentPath(q.path, q.backing)
	if err != nil {
		return err
	}
	parent, err := openDiskImageDepth(path, format, depth+1)
	if err != nil {
		return fmt.Errorf("opening the backing file: %w", err)
	}
	q.parent = parent
	return nil
}

func (q *qcow2Image) Size() int64      { return q.size }
func (q *qcow2Image) Describe() string { return q.path }

func (q *qcow2Image) l2Table(index int64) ([]uint64, error) {
	if index >= int64(len(q.l1)) {
		return nil, nil
	}
	offset := int64(q.l1[index] & qcow2OffsetMask)
	if offset == 0 {
		return nil, nil
	}
	if table, ok := q.l2Cache[offset]; ok {
		return table, nil
	}
	raw := make([]byte, q.cluster)
	if _, err := q.file.ReadAt(raw, offset); err != nil {
		return nil, fmt.Errorf("reading a qcow2 L2 table: %w", err)
	}
	table := make([]uint64, q.cluster/8)
	for i := range table {
		table[i] = binary.BigEndian.Uint64(raw[i*8:])
	}
	if len(q.l2Cache) >= qcow2L2CacheEntries {
		q.l2Cache = map[int64][]uint64{}
	}
	q.l2Cache[offset] = table
	return table, nil
}

// entry returns the L2 entry of a guest cluster, 0 when unallocated.
func (q *qcow2Image) entry(cluster int64) (uint64, error) {
	perL2 := q.cluster / 8
	table, err := q.l2Table(cluster / perL2)
	if err != nil || table == nil {
		return 0, err
	}
	return table[cluster%perL2], nil
}

func (q *qcow2Image) Close() error {
	var err error
	if q.writable {
		err = q.finish()
	}
	if q.parent != nil {
		q.parent.Close()
		q.parent = nil
	}
	if closeErr := q.file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (q *qcow2Image) Info() vmDiskInfo {
	info := vmDiskInfo{Path: q.path, Format: vmDiskFormatQCOW2, FormatName: "qcow2", Variant: fmt.Sprintf("version %d, %s clusters", q.version, vmFormatBytes(q.cluster)), VirtualSize: q.size, FileSize: q.fileSize}
	compressed := false
	q.walk(func(cluster int64, entry uint64) {
		info.DataBytes += q.cluster
		compressed = compressed || entry&qcow2Compressed != 0
	})
	if compressed {
		info.Notes = append(info.Notes, "Some clusters are compressed; they are expanded in the converted disk")
	}
	if q.backing != "" {
		info.Notes = append(info.Notes, "Backing file: "+filepath.Base(q.backing))
	}
	return vmChainInfo(info, q.parent)
}

// walk calls fn for every guest cluster that holds data in this file.
func (q *qcow2Image) walk(fn func(cluster int64, entry uint64)) error {
	perL2 := q.cluster / 8
	total := (q.size + q.cluster - 1) / q.cluster
	for index := range q.l1 {
		table, err := q.l2Table(int64(index))
		if err != nil {
			return err
		}
		for i, entry := range table {
			cluster := int64(index)*perL2 + int64(i)
			if cluster >= total {
				break
			}
			if entry&qcow2Compressed != 0 || (entry&qcow2OffsetMask != 0 && entry&qcow2ZeroFlag == 0) {
				fn(cluster, entry)
			}
		}
	}
	return nil
}

func (q *qcow2Image) Allocated() []vmCopyRange {
	own := []vmCopyRange{}
	if err := q.walk(func(cluster int64, _ uint64) {
		own = append(own, vmCopyRange{Start: cluster * q.cluster, Length: q.cluster})
	}); err != nil {
		return nil
	}
	return vmChainAllocated(own, q.parent, q.size)
}

func (q *qcow2Image) ReadAt(p []byte, off int64) (int, error) {
	return vmReadLimited(q.size, p, off, q.read)
}

func (q *qcow2Image) read(p []byte, off int64) error {
	for len(p) > 0 {
		cluster, inside := off/q.cluster, off%q.cluster
		n := q.cluster - inside
		if n > int64(len(p)) {
			n = int64(len(p))
		}
		entry, err := q.entry(cluster)
		if err != nil {
			return err
		}
		switch {
		case entry&qcow2Compressed != 0:
			data, err := q.decompress(entry)
			if err != nil {
				return err
			}
			copy(p[:n], data[inside:])
		case q.version >= 3 && entry&qcow2ZeroFlag != 0:
			vmZeroFill(p[:n])
		case entry&qcow2OffsetMask != 0:
			at := int64(entry & qcow2OffsetMask)
			if _, err := io.ReadFull(io.NewSectionReader(q.file, at+inside, n), p[:n]); err != nil {
				return fmt.Errorf("reading qcow2 cluster %d: %w", cluster, err)
			}
		default:
			if _, err := (vmParentReader{q.parent}).ReadAt(p[:n], off); err != nil {
				return err
			}
		}
		p, off = p[n:], off+n
	}
	return nil
}

func (q *qcow2Image) decompress(entry uint64) ([]byte, error) {
	shift := 62 - (q.clusterBits - 8)
	offset := int64(entry & (uint64(1)<<shift - 1))
	if offset == q.lastZipAt {
		return q.lastZip, nil
	}
	sectors := int64((entry>>shift)&(uint64(1)<<(q.clusterBits-8)-1)) + 1
	length := sectors*512 - offset%512
	if offset+length > q.fileSize {
		length = q.fileSize - offset
	}
	if length <= 0 || length > 2*q.cluster+512 {
		return nil, errors.New("a compressed qcow2 cluster has an invalid length")
	}
	raw := make([]byte, length)
	if _, err := q.file.ReadAt(raw, offset); err != nil && err != io.EOF {
		return nil, err
	}
	out := make([]byte, q.cluster)
	reader := flate.NewReader(bytes.NewReader(raw))
	defer reader.Close()
	if _, err := io.ReadFull(reader, out); err != nil {
		return nil, fmt.Errorf("a compressed qcow2 cluster could not be expanded: %w", err)
	}
	q.lastZipAt, q.lastZip = offset, out
	return out, nil
}

// Writing.

func createQCOW2(path string, size int64) (*qcow2Image, error) {
	if size <= 0 || size%vhdxLogicalSector != 0 {
		return nil, fmt.Errorf("a qcow2 disk must be a whole number of 512-byte sectors, not %d bytes", size)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*qcow2Image, error) {
		file.Close()
		os.Remove(path)
		return nil, err
	}
	cluster := int64(1) << qcow2WriteBits
	perL2 := cluster / 8
	l1Size := (size + cluster*perL2 - 1) / (cluster * perL2)
	l1Clusters := (l1Size*8 + cluster - 1) / cluster
	q := &qcow2Image{file: file, path: path, version: 3, clusterBits: qcow2WriteBits, cluster: cluster, size: size, l1: make([]uint64, l1Size), l1Offset: cluster, l2Cache: map[int64][]uint64{}, writable: true, lastZipAt: -1}
	q.end = (1 + l1Clusters) * cluster
	header := make([]byte, cluster)
	be := binary.BigEndian
	copy(header, qcow2Magic)
	be.PutUint32(header[4:], 3)
	be.PutUint32(header[20:], qcow2WriteBits)
	be.PutUint64(header[24:], uint64(size))
	be.PutUint32(header[36:], uint32(l1Size))
	be.PutUint64(header[40:], uint64(q.l1Offset))
	be.PutUint64(header[72:], qcow2IncDirty)
	be.PutUint32(header[96:], qcow2RefcountOrder)
	be.PutUint32(header[100:], qcow2HeaderLength)
	// The header extension area after byte 112 is all zeros: its end marker.
	if _, err := file.WriteAt(header, 0); err != nil {
		return fail(err)
	}
	if err := file.Truncate(q.end); err != nil {
		return fail(err)
	}
	q.header = header[:qcow2HeaderLength]
	q.fileSize = q.end
	return q, nil
}

// openQCOW2Writer reopens a qcow2 this application wrote, to resume or
// update a copy.
func openQCOW2Writer(path string) (*qcow2Image, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	q, err := loadQCOW2(file, path, info.Size())
	if err == nil && (q.backing != "" || q.clusterBits != qcow2WriteBits || q.version != 3) {
		err = errors.New("this qcow2 was not written by this application's converter")
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	q.writable = true
	// The data ends after the last cluster any table points to; the old
	// reference counts after it are cut off and rebuilt on Close.
	l1Clusters := (int64(len(q.l1))*8 + q.cluster - 1) / q.cluster
	end := (1 + l1Clusters) * q.cluster
	for index, l1 := range q.l1 {
		offset := int64(l1 & qcow2OffsetMask)
		if offset == 0 {
			continue
		}
		if offset+q.cluster > end {
			end = offset + q.cluster
		}
		table, err := q.l2Table(int64(index))
		if err != nil {
			file.Close()
			return nil, err
		}
		for _, entry := range table {
			if entry&qcow2Compressed != 0 {
				file.Close()
				return nil, errors.New("this qcow2 has compressed clusters and was not written by this converter")
			}
			if at := int64(entry & qcow2OffsetMask); at != 0 && at+q.cluster > end {
				end = at + q.cluster
			}
		}
	}
	if err := q.setDirty(true); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Truncate(end); err != nil {
		file.Close()
		return nil, err
	}
	q.end, q.fileSize = end, end
	return q, nil
}

func (q *qcow2Image) setDirty(dirty bool) error {
	value := make([]byte, 8)
	if dirty {
		binary.BigEndian.PutUint64(value, qcow2IncDirty)
	}
	_, err := q.file.WriteAt(value, 72)
	return err
}

// WriteAt writes guest bytes, allocating a cluster only for non-zero data.
func (q *qcow2Image) WriteAt(p []byte, off int64) (int, error) {
	if !q.writable {
		return 0, errors.New("the qcow2 image is open read-only")
	}
	if off < 0 || off+int64(len(p)) > q.size {
		return 0, fmt.Errorf("write at %d+%d is outside the %d-byte disk", off, len(p), q.size)
	}
	total := 0
	perL2 := q.cluster / 8
	for len(p) > 0 {
		cluster, inside := off/q.cluster, off%q.cluster
		n := q.cluster - inside
		if n > int64(len(p)) {
			n = int64(len(p))
		}
		entry, err := q.entry(cluster)
		if err != nil {
			return total, err
		}
		if at := int64(entry & qcow2OffsetMask); at != 0 {
			if _, err := q.file.WriteAt(p[:n], at+inside); err != nil {
				return total, err
			}
		} else if !vmIsZero(p[:n]) {
			table, err := q.l2Table(cluster / perL2)
			if err != nil {
				return total, err
			}
			if table == nil {
				if table, err = q.newL2(cluster / perL2); err != nil {
					return total, err
				}
			}
			at := q.end
			if err := q.file.Truncate(at + q.cluster); err != nil {
				return total, err
			}
			if _, err := q.file.WriteAt(p[:n], at+inside); err != nil {
				return total, err
			}
			q.end, q.fileSize = at+q.cluster, at+q.cluster
			value := uint64(at) | qcow2Copied
			raw := make([]byte, 8)
			binary.BigEndian.PutUint64(raw, value)
			if _, err := q.file.WriteAt(raw, int64(q.l1[cluster/perL2]&qcow2OffsetMask)+(cluster%perL2)*8); err != nil {
				return total, err
			}
			table[cluster%perL2] = value
		}
		p, off, total = p[n:], off+n, total+int(n)
	}
	return total, nil
}

func (q *qcow2Image) newL2(index int64) ([]uint64, error) {
	at := q.end
	if err := q.file.Truncate(at + q.cluster); err != nil {
		return nil, err
	}
	q.end, q.fileSize = at+q.cluster, at+q.cluster
	value := uint64(at) | qcow2Copied
	raw := make([]byte, 8)
	binary.BigEndian.PutUint64(raw, value)
	if _, err := q.file.WriteAt(raw, q.l1Offset+index*8); err != nil {
		return nil, err
	}
	q.l1[index] = value
	table := make([]uint64, q.cluster/8)
	if len(q.l2Cache) >= qcow2L2CacheEntries {
		q.l2Cache = map[int64][]uint64{}
	}
	q.l2Cache[at] = table
	return table, nil
}

func (q *qcow2Image) Flush() error { return q.file.Sync() }

// finish writes reference counts after the data: every cluster from the
// header to the end of the count table is used exactly once.
func (q *qcow2Image) finish() error {
	data := q.end / q.cluster
	perBlock := q.cluster * 8 / (1 << qcow2RefcountOrder)
	blocks, tableClusters := int64(0), int64(0)
	for {
		total := data + blocks + tableClusters
		needBlocks := (total + perBlock - 1) / perBlock
		needTable := (needBlocks*8 + q.cluster - 1) / q.cluster
		if needBlocks == blocks && needTable == tableClusters {
			break
		}
		blocks, tableClusters = needBlocks, needTable
	}
	total := data + blocks + tableClusters
	if err := q.file.Truncate(total * q.cluster); err != nil {
		return err
	}
	block := make([]byte, q.cluster)
	for b := int64(0); b < blocks; b++ {
		vmZeroFill(block)
		for i := int64(0); i < perBlock; i++ {
			if b*perBlock+i >= total {
				break
			}
			binary.BigEndian.PutUint16(block[i*2:], 1)
		}
		if _, err := q.file.WriteAt(block, (data+b)*q.cluster); err != nil {
			return err
		}
	}
	table := make([]byte, tableClusters*q.cluster)
	for b := int64(0); b < blocks; b++ {
		binary.BigEndian.PutUint64(table[b*8:], uint64((data+b)*q.cluster))
	}
	tableAt := (data + blocks) * q.cluster
	if _, err := q.file.WriteAt(table, tableAt); err != nil {
		return err
	}
	if err := q.file.Sync(); err != nil {
		return err
	}
	fields := make([]byte, 12)
	binary.BigEndian.PutUint64(fields, uint64(tableAt))
	binary.BigEndian.PutUint32(fields[8:], uint32(tableClusters))
	if _, err := q.file.WriteAt(fields, 48); err != nil {
		return err
	}
	if err := q.setDirty(false); err != nil {
		return err
	}
	q.writable = false
	return q.file.Sync()
}
