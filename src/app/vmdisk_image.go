package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Disk images for the disk converter: one reader per format (raw, VHD,
// VHDX, VMDK, qcow2), each a vmCopySource so a conversion runs through the
// same resumable, hashed copy engine as a VMware disk copy, and one writer
// per output format behind vmDiskWriter. Readers follow parent chains
// (differencing VHD/VHDX, qcow2 backing files, hosted VMDK snapshots) and
// report which areas hold data, so a conversion skips empty space without
// reading it. Nothing here writes to a source image.

const (
	vmDiskFormatRaw      = "raw"
	vmDiskFormatVHD      = "vhd"
	vmDiskFormatVHDFixed = "vhd-fixed"
	vmDiskFormatVHDX     = "vhdx"
	vmDiskFormatQCOW2    = "qcow2"
	vmDiskFormatVMDK     = "vmdk"
	vmDiskFormatVMDKFlat = "vmdk-flat"

	// vmDiskChainLimit stops a parent chain that loops or is absurdly long.
	vmDiskChainLimit = 64
)

// vmDiskImage is an opened source image: its virtual disk as one readable
// device, plus what the converter shows about it.
type vmDiskImage interface {
	vmCopySource
	Info() vmDiskInfo
	// Allocated lists byte ranges that may hold data, in order; everything
	// else reads as zeros. A nil result means every byte may hold data.
	Allocated() []vmCopyRange
}

type vmDiskInfo struct {
	Path        string   `json:"path"`
	Format      string   `json:"format"`
	FormatName  string   `json:"formatName"`
	Variant     string   `json:"variant,omitempty"`
	VirtualSize int64    `json:"virtualSize"`
	FileSize    int64    `json:"fileSize"`
	DataBytes   int64    `json:"dataBytes"`
	Chain       []string `json:"chain,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

// vmDiskFormatNames are the output formats the converter writes, in the
// order the interface lists them.
var vmDiskFormatNames = []struct {
	ID, Name, Extension, Use string
}{
	{vmDiskFormatVHDX, "VHDX (dynamic)", ".vhdx", "Hyper-V, Scale HyperCore, Nutanix AHV"},
	{vmDiskFormatQCOW2, "qcow2", ".qcow2", "Proxmox VE, Nutanix AHV, Scale HyperCore, KVM"},
	{vmDiskFormatVMDK, "VMDK (monolithic sparse)", ".vmdk", "VMware Workstation, vmkfstools -i on ESXi, Nutanix AHV"},
	{vmDiskFormatVMDKFlat, "VMDK (ESXi flat)", ".vmdk", "ESXi datastores (descriptor plus -flat.vmdk)"},
	{vmDiskFormatVHD, "VHD (dynamic)", ".vhd", "Older Hyper-V, Scale HyperCore, Virtual PC"},
	{vmDiskFormatVHDFixed, "VHD (fixed)", ".vhd", "Azure uploads and tools that need a fixed VHD"},
	{vmDiskFormatRaw, "Raw image", ".img", "dd, physical disks (V2P), Proxmox, any hypervisor"},
}

func vmDiskFormatKnown(format string) bool {
	for _, item := range vmDiskFormatNames {
		if item.ID == format {
			return true
		}
	}
	return false
}

func vmDiskFormatTitle(format string) string {
	for _, item := range vmDiskFormatNames {
		if item.ID == format {
			return item.Name
		}
	}
	return format
}

// vmDiskFormatLimit is the largest virtual disk each output format holds.
func vmDiskFormatLimit(format string) int64 {
	switch format {
	case vmDiskFormatVHD, vmDiskFormatVHDFixed:
		return vhdMaxSize
	case vmDiskFormatVHDX:
		return vhdxMaxSize
	case vmDiskFormatVMDK:
		return vmdkSparseMaxSize
	}
	return 1 << 62
}

// openDiskImage opens any supported image read-only, following its parents.
func openDiskImage(path string) (vmDiskImage, error) {
	return openDiskImageDepth(path, "", 0)
}

// openDiskImageDepth opens one image of a chain. A known format (from a
// qcow2 backing format) is used instead of probing, so a raw backing file
// that happens to start like an image is never misread.
func openDiskImageDepth(path, format string, depth int) (vmDiskImage, error) {
	if depth > vmDiskChainLimit {
		return nil, errors.New("the parent chain is longer than 64 disks or loops back on itself")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.IsDir() {
		file.Close()
		return nil, fmt.Errorf("%s is a folder, not a disk image", absolute)
	}
	if format == "" {
		format, err = vmProbeDiskFormat(file, info.Size(), absolute)
		if err != nil {
			file.Close()
			return nil, err
		}
	}
	var image vmDiskImage
	switch format {
	case vmDiskFormatRaw:
		image, err = newRawImage(file, absolute, info.Size())
	case vmDiskFormatVHD:
		image, err = openVHDImage(file, absolute, info.Size(), depth)
	case vmDiskFormatVHDX:
		file.Close()
		image, err = openVHDXImage(absolute, depth)
	case vmDiskFormatQCOW2:
		image, err = openQCOW2Image(file, absolute, info.Size(), depth)
	case vmDiskFormatVMDK:
		image, err = openVMDKImage(file, absolute, info.Size(), depth)
	default:
		err = fmt.Errorf("the disk format %q is not supported", format)
	}
	if err != nil {
		if format != vmDiskFormatVHDX {
			file.Close()
		}
		return nil, fmt.Errorf("%s: %w", filepath.Base(absolute), err)
	}
	return image, nil
}

// vmProbeDiskFormat recognises an image by its signature.
func vmProbeDiskFormat(file *os.File, size int64, name string) (string, error) {
	head := make([]byte, 1024)
	n, err := file.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	head = head[:n]
	has := func(prefix string) bool { return bytes.HasPrefix(head, []byte(prefix)) }
	switch {
	case has(vhdxSignature):
		return vmDiskFormatVHDX, nil
	case has("QFI\xfb"):
		return vmDiskFormatQCOW2, nil
	case has("KDMV"):
		return vmDiskFormatVMDK, nil
	case has("COWD"):
		return "", errors.New("this is an ESXi VMFS snapshot delta; remove all snapshots (Prepare source) so the disk is consolidated, then convert the base disk")
	case has("conectix"):
		return vmDiskFormatVHD, nil
	}
	if size >= 512 {
		footer := make([]byte, 512)
		if _, err := file.ReadAt(footer, size-512); err == nil && bytes.HasPrefix(footer, []byte("conectix")) {
			return vmDiskFormatVHD, nil
		}
		// Very old fixed VHDs have a 511-byte footer.
		if _, err := file.ReadAt(footer[:511], size-511); err == nil && bytes.HasPrefix(footer, []byte("conectix")) {
			return vmDiskFormatVHD, nil
		}
	}
	if size <= 64<<10 && vmLooksLikeDescriptor(head) {
		return vmDiskFormatVMDK, nil
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".vhdx") || strings.HasSuffix(lower, ".avhdx"):
		return "", errors.New("the file is named like a VHDX but does not start with a VHDX signature; it may be damaged or still being written")
	case strings.HasSuffix(lower, ".vhd") || strings.HasSuffix(lower, ".avhd"):
		return "", errors.New("the file is named like a VHD but has no VHD footer; it may be damaged or incomplete")
	case strings.HasSuffix(lower, ".qcow2"):
		return "", errors.New("the file is named like a qcow2 image but has no qcow2 signature")
	case strings.HasSuffix(lower, ".vmdk") && !strings.HasSuffix(lower, "-flat.vmdk"):
		return "", errors.New("this VMDK is neither a descriptor nor a sparse disk; for a -flat.vmdk choose the small descriptor beside it, or this file as a raw image")
	case strings.HasSuffix(lower, ".iso"):
		return "", errors.New("this is an ISO image, not a virtual disk")
	}
	return vmDiskFormatRaw, nil
}

func vmLooksLikeDescriptor(head []byte) bool {
	text, ok := vmDescriptorText(head)
	if !ok {
		return false
	}
	return strings.Contains(text, "# Disk DescriptorFile") || strings.Contains(text, "createType")
}

// vmDescriptorText returns a VMDK descriptor's text: everything before the
// first zero byte. Some tools pad the descriptor with zeros to a whole
// sector, and QEMU can leave a few bytes of the old text after that zero:
// when it records a new CID it prints it without leading zeros, so the text
// gets shorter, and it writes back the whole old buffer (vmdk_write_cid). So
// after the first zero only zeros may follow, apart from line breaks or a
// copy of the text's own last few bytes right after the zero. Anything else
// is binary data and the file is not a descriptor.
func vmDescriptorText(raw []byte) (string, bool) {
	end := bytes.IndexByte(raw, 0)
	if end < 0 {
		return string(raw), true
	}
	text, rest := raw[:end], raw[end+1:]
	if len(bytes.Trim(rest, "\x00\r\n")) == 0 {
		return string(text), true
	}
	tail := rest
	if zero := bytes.IndexByte(rest, 0); zero >= 0 {
		tail = rest[:zero]
	}
	if len(tail) == 0 || len(tail) > 8 || !bytes.HasSuffix(text, tail) || !vmIsZero(rest[len(tail):]) {
		return "", false
	}
	return string(text), true
}

// vmResolveParentPath finds a parent image named relative to its child or
// by an absolute path, trying each candidate in order.
func vmResolveParentPath(childPath string, candidates ...string) (string, error) {
	folder := filepath.Dir(childPath)
	tried := []string{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(strings.TrimRight(candidate, "\x00"))
		if candidate == "" {
			continue
		}
		candidate = strings.TrimPrefix(candidate, `\\?\`)
		native := strings.ReplaceAll(candidate, `\`, string(filepath.Separator))
		native = strings.ReplaceAll(native, "/", string(filepath.Separator))
		paths := []string{}
		if filepath.IsAbs(native) || vmWindowsAbsolute(candidate) {
			paths = append(paths, native)
		} else {
			paths = append(paths, filepath.Join(folder, native))
		}
		// A chain copied to another folder or computer keeps working when
		// the parent sits beside the child.
		paths = append(paths, filepath.Join(folder, filepath.Base(native)))
		for _, path := range paths {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
			tried = append(tried, path)
		}
	}
	if len(tried) == 0 {
		return "", errors.New("the parent disk is not named in this differencing disk")
	}
	return "", fmt.Errorf("the parent disk was not found (looked for %s); copy the whole chain into one folder", strings.Join(vmUniqueStrings(tried), ", "))
}

func vmWindowsAbsolute(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') || strings.HasPrefix(path, `\\`)
}

func vmUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// vmZeroFill clears p.
func vmZeroFill(p []byte) {
	for i := range p {
		p[i] = 0
	}
}

// vmParentReader reads the parent of a differencing image at virtual
// offsets; a missing parent or the area past a smaller parent reads zeros.
type vmParentReader struct {
	parent vmDiskImage
}

func (r vmParentReader) ReadAt(p []byte, off int64) (int, error) {
	if r.parent == nil {
		vmZeroFill(p)
		return len(p), nil
	}
	size := r.parent.Size()
	n := int64(len(p))
	if off >= size {
		vmZeroFill(p)
		return len(p), nil
	}
	inside := n
	if off+n > size {
		inside = size - off
		vmZeroFill(p[inside:])
	}
	if read, err := r.parent.ReadAt(p[:inside], off); int64(read) < inside {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return read, err
	}
	return len(p), nil
}

// vmReadLimited reads p at off from a disk of the given size, returning EOF
// for any part past the end as io.ReaderAt requires.
func vmReadLimited(size int64, p []byte, off int64, read func(p []byte, off int64) error) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= size {
		return 0, io.EOF
	}
	want := int64(len(p))
	if off+want > size {
		want = size - off
	}
	if err := read(p[:want], off); err != nil {
		return 0, err
	}
	if want < int64(len(p)) {
		return int(want), io.EOF
	}
	return int(want), nil
}

// vmMergeRanges sorts and joins overlapping or touching ranges.
func vmMergeRanges(ranges []vmCopyRange) []vmCopyRange {
	clean := make([]vmCopyRange, 0, len(ranges))
	for _, r := range ranges {
		if r.Length > 0 && r.Start >= 0 {
			clean = append(clean, r)
		}
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].Start < clean[j].Start })
	out := []vmCopyRange{}
	for _, r := range clean {
		if n := len(out); n > 0 && r.Start <= out[n-1].Start+out[n-1].Length {
			if end := r.Start + r.Length; end > out[n-1].Start+out[n-1].Length {
				out[n-1].Length = end - out[n-1].Start
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// vmChainAllocated joins a layer's own ranges with its parent's.
func vmChainAllocated(own []vmCopyRange, parent vmDiskImage, size int64) []vmCopyRange {
	if parent == nil {
		return vmMergeRanges(own)
	}
	inherited := parent.Allocated()
	if inherited == nil {
		return nil
	}
	all := append([]vmCopyRange(nil), own...)
	for _, r := range inherited {
		if r.Start >= size {
			continue
		}
		if r.Start+r.Length > size {
			r.Length = size - r.Start
		}
		all = append(all, r)
	}
	return vmMergeRanges(all)
}

func vmRangesBytes(ranges []vmCopyRange) int64 {
	var total int64
	for _, r := range ranges {
		total += r.Length
	}
	return total
}

func vmChainInfo(info vmDiskInfo, parent vmDiskImage) vmDiskInfo {
	info.Chain = []string{info.Path}
	if parent != nil {
		parentInfo := parent.Info()
		info.Chain = append(info.Chain, parentInfo.Chain...)
		info.Notes = append(info.Notes, fmt.Sprintf("Differencing disk: %d disks in the chain are merged into one", len(info.Chain)))
	}
	return info
}

// Raw images.

type vmRawImage struct {
	file *os.File
	path string
	size int64 // rounded up to whole sectors
	real int64
}

func newRawImage(file *os.File, path string, size int64) (*vmRawImage, error) {
	if size == 0 {
		return nil, errors.New("the file is empty")
	}
	rounded := (size + vhdxLogicalSector - 1) / vhdxLogicalSector * vhdxLogicalSector
	return &vmRawImage{file: file, path: path, size: rounded, real: size}, nil
}

func (r *vmRawImage) Size() int64      { return r.size }
func (r *vmRawImage) Describe() string { return r.path }
func (r *vmRawImage) Close() error     { return r.file.Close() }
func (r *vmRawImage) Allocated() []vmCopyRange {
	return nil
}

func (r *vmRawImage) Info() vmDiskInfo {
	info := vmDiskInfo{Path: r.path, Format: vmDiskFormatRaw, FormatName: "Raw image", VirtualSize: r.size, FileSize: r.real, DataBytes: r.real, Chain: []string{r.path}}
	if r.real != r.size {
		info.Notes = append(info.Notes, "The file is not a whole number of 512-byte sectors; the last sector is padded with zeros")
	}
	return info
}

func (r *vmRawImage) ReadAt(p []byte, off int64) (int, error) {
	return vmReadLimited(r.size, p, off, func(p []byte, off int64) error {
		inside := int64(len(p))
		if off+inside > r.real {
			inside = r.real - off
			if inside < 0 {
				inside = 0
			}
			vmZeroFill(p[inside:])
		}
		if inside == 0 {
			return nil
		}
		n, err := r.file.ReadAt(p[:inside], off)
		if int64(n) == inside {
			return nil
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return err
	})
}

// openReadOnlyFile opens a source image without any write access.
func openReadOnlyFile(path string) (*os.File, error) { return os.Open(path) }
