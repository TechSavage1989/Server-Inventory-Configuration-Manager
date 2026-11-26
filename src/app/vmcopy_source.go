package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
)

// Copy sources: something with a fixed size that can be read at any offset.
// Flat VMDK extents are read directly, from a local folder or from a vSphere
// datastore over HTTPS with Range requests; sparse and snapshot formats are
// refused with a clear reason because the disk converter is a later phase.

type vmCopySource interface {
	io.ReaderAt
	Size() int64
	Describe() string
	Close() error
}

// vmFileSource reads a raw disk image or flat extent from a local path.
type vmFileSource struct {
	file *os.File
	size int64
	name string
}

func openVMFileSource(name string) (*vmFileSource, error) {
	file, err := os.Open(name)
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
		return nil, fmt.Errorf("%s is a folder, not a disk image", name)
	}
	return &vmFileSource{file: file, size: info.Size(), name: name}, nil
}

func (s *vmFileSource) ReadAt(p []byte, off int64) (int, error) { return s.file.ReadAt(p, off) }
func (s *vmFileSource) Size() int64                             { return s.size }
func (s *vmFileSource) Describe() string                        { return s.name }
func (s *vmFileSource) Close() error                            { return s.file.Close() }

// vmVHDXSource reads a VHDX's virtual disk, for verifying a finished copy.
type vmVHDXSource struct{ *vhdxFile }

func (s vmVHDXSource) Describe() string { return s.path }

// VMDK descriptors.

type vmdkExtent struct {
	Access  string
	Sectors int64
	Type    string
	File    string
	Offset  int64
}

type vmdkDescriptor struct {
	CreateType string
	ParentCID  string
	Extents    []vmdkExtent
}

var vmdkExtentLine = regexp.MustCompile(`^(RW|RDONLY|NOACCESS)\s+(\d+)\s+(\w+)(?:\s+"([^"]*)"(?:\s+(\d+))?)?`)

// parseVMDKDescriptor reads the text descriptor of a VMDK.
func parseVMDKDescriptor(text string) (*vmdkDescriptor, error) {
	if len(text) > 64<<10 || strings.IndexByte(text, 0) >= 0 {
		return nil, errors.New("this is not a VMDK descriptor; choose the small .vmdk file, not the -flat.vmdk data file")
	}
	descriptor := &vmdkDescriptor{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if match := vmdkExtentLine.FindStringSubmatch(line); match != nil {
			sectors, _ := strconv.ParseInt(match[2], 10, 64)
			offset, _ := strconv.ParseInt(match[5], 10, 64)
			descriptor.Extents = append(descriptor.Extents, vmdkExtent{Access: match[1], Sectors: sectors, Type: strings.ToUpper(match[3]), File: match[4], Offset: offset})
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch strings.TrimSpace(key) {
			case "createType":
				descriptor.CreateType = value
			case "parentCID":
				descriptor.ParentCID = strings.ToLower(value)
			}
		}
	}
	if len(descriptor.Extents) == 0 {
		return nil, errors.New("the VMDK descriptor lists no extents")
	}
	return descriptor, nil
}

// vmdkFlatExtents checks that every extent can be read as plain bytes.
func vmdkFlatExtents(descriptor *vmdkDescriptor) error {
	if descriptor.ParentCID != "" && descriptor.ParentCID != "ffffffff" {
		return errors.New("this disk is a snapshot delta; remove all snapshots (Prepare source) so the disk is consolidated, then copy it")
	}
	for _, extent := range descriptor.Extents {
		switch extent.Type {
		case "FLAT", "VMFS":
			if extent.File == "" || strings.ContainsAny(extent.File, `/\`) || extent.File == "." || extent.File == ".." {
				return fmt.Errorf("the VMDK extent %q is not a file beside the descriptor", extent.File)
			}
		case "ZERO":
		case "VMFSRDM", "VMFSPASSTHROUGHRAW":
			return errors.New("this is a raw device mapping; copy its LUN contents from inside the guest or with the storage array instead")
		case "SPARSE", "VMFSSPARSE", "SESPARSE":
			return fmt.Errorf("this disk is stored as %s, which needs the disk converter (a later phase); consolidate snapshots first, or convert it to a flat disk", extent.Type)
		default:
			return fmt.Errorf("the VMDK extent type %s is not supported yet", extent.Type)
		}
	}
	return nil
}

// vmExtentSource joins a VMDK's flat extents into one disk.
type vmExtentSource struct {
	name    string
	size    int64
	parts   []vmExtentPart
	closers []io.Closer
}

type vmExtentPart struct {
	start  int64
	length int64
	reader io.ReaderAt
	offset int64
}

func newVMExtentSource(name string, descriptor *vmdkDescriptor, open func(file string) (io.ReaderAt, int64, io.Closer, error)) (*vmExtentSource, error) {
	if err := vmdkFlatExtents(descriptor); err != nil {
		return nil, err
	}
	source := &vmExtentSource{name: name}
	for _, extent := range descriptor.Extents {
		length := extent.Sectors * 512
		part := vmExtentPart{start: source.size, length: length, offset: extent.Offset * 512}
		if extent.Type != "ZERO" {
			reader, available, closer, err := open(extent.File)
			if err != nil {
				source.Close()
				return nil, fmt.Errorf("could not open %s: %w", extent.File, err)
			}
			source.closers = append(source.closers, closer)
			if available < part.offset+length {
				source.Close()
				return nil, fmt.Errorf("%s is %d bytes, smaller than its descriptor says (%d)", extent.File, available, part.offset+length)
			}
			part.reader = reader
		}
		source.parts = append(source.parts, part)
		source.size += length
	}
	return source, nil
}

func (s *vmExtentSource) Size() int64      { return s.size }
func (s *vmExtentSource) Describe() string { return s.name }

func (s *vmExtentSource) Close() error {
	var first error
	for _, closer := range s.closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.closers = nil
	return first
}

func (s *vmExtentSource) ReadAt(p []byte, off int64) (int, error) {
	total := 0
	for _, part := range s.parts {
		if len(p) == 0 {
			break
		}
		if off >= part.start+part.length || off < part.start {
			continue
		}
		inside := off - part.start
		n := int64(len(p))
		if n > part.length-inside {
			n = part.length - inside
		}
		if part.reader == nil {
			for i := int64(0); i < n; i++ {
				p[i] = 0
			}
		} else if read, err := part.reader.ReadAt(p[:n], part.offset+inside); int64(read) < n {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return total + read, err
		}
		p, off, total = p[n:], off+n, total+int(n)
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}

// openLocalVMDK opens a VMDK descriptor and its flat extents from a folder,
// for example a datastore copied to local disk or an exported VM.
func openLocalVMDK(name string) (vmCopySource, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.Size() > 64<<10 {
		if strings.HasSuffix(strings.ToLower(name), "-flat.vmdk") {
			return openVMFileSource(name)
		}
		return nil, errors.New("this VMDK has no text descriptor; it may be a sparse or stream-optimized disk, which needs the disk converter (a later phase)")
	}
	text, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	descriptor, err := parseVMDKDescriptor(string(text))
	if err != nil {
		return nil, err
	}
	folder := filepath.Dir(name)
	return newVMExtentSource(name, descriptor, func(file string) (io.ReaderAt, int64, io.Closer, error) {
		source, err := openVMFileSource(filepath.Join(folder, file))
		if err != nil {
			return nil, 0, nil, err
		}
		return source, source.size, source, nil
	})
}

// Datastore files over HTTPS.

// vmSplitDatastorePath splits "[datastore] folder/disk.vmdk".
func vmSplitDatastorePath(value string) (datastore, file string, err error) {
	value = strings.TrimSpace(value)
	end := strings.Index(value, "]")
	if !strings.HasPrefix(value, "[") || end < 2 {
		return "", "", fmt.Errorf("%q is not a datastore path like [datastore] folder/disk.vmdk", value)
	}
	datastore, file = value[1:end], strings.TrimSpace(value[end+1:])
	clean := path.Clean("/" + file)
	if file == "" || clean != "/"+file || strings.Contains(file, `\`) {
		return "", "", fmt.Errorf("%q is not a plain datastore file path", value)
	}
	return datastore, file, nil
}

// vmDatastoreStatusError is a datastore file the host answered with an HTTP
// error for, such as a disk a running VM holds open.
type vmDatastoreStatusError struct {
	Datastore, File, Status string
	Code                    int
}

func (e *vmDatastoreStatusError) Error() string {
	return fmt.Sprintf("[%s] %s: the server answered %s", e.Datastore, e.File, e.Status)
}

// vmDatastoreFile reads one datastore file with HTTP Range requests through
// the logged-in vSphere session. Only GET and HEAD are ever sent.
//
// A server may answer a ranged GET with 200 and the whole file (HTTP allows
// it, and vCenter's /folder proxy does it, at least for a range that covers
// the whole file). A 200 is never taken as the requested range: at offset 0
// the body starts where the read starts; anywhere else the file is read as
// one stream from the start, skipping forward to each read, and reopened
// only when a read goes backwards. The copy engine reads in increasing
// order, so a server that ignores Range still gives a correct copy.
type vmDatastoreFile struct {
	ctx    context.Context
	client *vim25.Client
	url    *url.URL
	size   int64

	mu      sync.Mutex
	noRange bool
	stream  io.ReadCloser
	pos     int64
}

func vmDatastoreURL(client *vim25.Client, datacenter, datastore, file string) *url.URL {
	base := client.URL()
	return &url.URL{
		Scheme:   base.Scheme,
		Host:     base.Host,
		Path:     "/folder/" + file,
		RawQuery: url.Values{"dcPath": []string{datacenter}, "dsName": []string{datastore}}.Encode(),
	}
}

func openVMDatastoreFile(ctx context.Context, client *vim25.Client, datacenter, datastore, file string) (*vmDatastoreFile, error) {
	f := &vmDatastoreFile{ctx: ctx, client: client, url: vmDatastoreURL(client, datacenter, datastore, file)}
	response, err := client.DownloadRequest(ctx, f.url, &soap.Download{Method: http.MethodHead})
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &vmDatastoreStatusError{Datastore: datastore, File: file, Status: response.Status, Code: response.StatusCode}
	}
	f.size = response.ContentLength
	if f.size < 0 {
		return nil, fmt.Errorf("[%s] %s: the server did not say how large the file is", datastore, file)
	}
	return f, nil
}

// vmContentRangeStart reads the first byte position of a Content-Range
// header ("bytes 100-199/1000").
func vmContentRangeStart(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes ") {
		return 0, false
	}
	value = strings.TrimSpace(value[len("bytes "):])
	dash := strings.Index(value, "-")
	if dash < 1 {
		return 0, false
	}
	start, err := strconv.ParseInt(value[:dash], 10, 64)
	return start, err == nil
}

func (f *vmDatastoreFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= f.size {
		return 0, io.EOF
	}
	want := int64(len(p))
	if want > f.size-off {
		want = f.size - off
	}
	if want == 0 {
		return 0, nil
	}
	var n int
	var err error
	f.mu.Lock()
	if f.noRange {
		n, err = f.readStreamLocked(p[:want], off)
		f.mu.Unlock()
	} else {
		f.mu.Unlock()
		n, err = f.readRange(p[:want], off)
	}
	if err == nil && want < int64(len(p)) {
		err = io.EOF
	}
	return n, err
}

func (f *vmDatastoreFile) readRange(p []byte, off int64) (int, error) {
	response, err := f.client.DownloadRequest(f.ctx, f.url, &soap.Download{
		Method:  http.MethodGet,
		Headers: map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1)},
	})
	if err != nil {
		return 0, err
	}
	switch response.StatusCode {
	case http.StatusPartialContent:
		defer response.Body.Close()
		if start, ok := vmContentRangeStart(response.Header.Get("Content-Range")); ok && start != off {
			return 0, fmt.Errorf("the datastore answered a ranged read at %d with data from %d", off, start)
		}
		return io.ReadFull(response.Body, p)
	case http.StatusOK:
		// The whole file, from its first byte.
		f.mu.Lock()
		defer f.mu.Unlock()
		if off > 0 {
			f.noRange = true
		}
		f.closeStreamLocked()
		f.stream, f.pos = response.Body, 0
		n, err := f.readStreamLocked(p, off)
		if off == 0 {
			// The server may still honour ranges further on; do not hold a
			// stream of the whole file open for it.
			f.closeStreamLocked()
		}
		return n, err
	default:
		response.Body.Close()
		return 0, fmt.Errorf("the datastore answered %s to a ranged read", response.Status)
	}
}

// readStreamLocked reads p at off from the whole-file stream, opening it
// (again) when there is none or the read is behind it. Caller holds f.mu.
func (f *vmDatastoreFile) readStreamLocked(p []byte, off int64) (int, error) {
	if f.stream == nil || off < f.pos {
		f.closeStreamLocked()
		response, err := f.client.DownloadRequest(f.ctx, f.url, &soap.Download{Method: http.MethodGet})
		if err != nil {
			return 0, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return 0, fmt.Errorf("the datastore answered %s to a read of the whole file", response.Status)
		}
		f.stream, f.pos = response.Body, 0
	}
	if skip := off - f.pos; skip > 0 {
		skipped, err := io.CopyN(io.Discard, f.stream, skip)
		f.pos += skipped
		if err != nil {
			f.closeStreamLocked()
			return 0, fmt.Errorf("the datastore file ended early while skipping to %d: %w", off, err)
		}
	}
	n, err := io.ReadFull(f.stream, p)
	f.pos += int64(n)
	if err != nil {
		f.closeStreamLocked()
	}
	return n, err
}

func (f *vmDatastoreFile) closeStreamLocked() {
	if f.stream != nil {
		f.stream.Close()
		f.stream, f.pos = nil, 0
	}
}

// Close ends a whole-file stream, if one is open.
func (f *vmDatastoreFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeStreamLocked()
	return nil
}

func (f *vmDatastoreFile) readAll(limit int64) ([]byte, error) {
	if f.size > limit {
		return nil, fmt.Errorf("the file is larger than %d bytes", limit)
	}
	buffer := make([]byte, f.size)
	n, err := f.ReadAt(buffer, 0)
	if err == io.EOF && int64(n) == f.size {
		err = nil
	}
	return buffer[:n], err
}

// openDatastoreVMDK opens a VM disk ("[datastore] vm/vm.vmdk") straight from
// vCenter or ESXi. The VM should be powered off or have no snapshots so the
// flat extent is not changing while it is read.
func openDatastoreVMDK(ctx context.Context, client *vim25.Client, datacenter, diskPath string) (vmCopySource, error) {
	datastore, file, err := vmSplitDatastorePath(diskPath)
	if err != nil {
		return nil, err
	}
	descriptorFile, err := openVMDatastoreFile(ctx, client, datacenter, datastore, file)
	if err != nil {
		return nil, err
	}
	text, err := descriptorFile.readAll(64 << 10)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", diskPath, err)
	}
	descriptor, err := parseVMDKDescriptor(string(text))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", diskPath, err)
	}
	folder := path.Dir(file)
	return newVMExtentSource(diskPath, descriptor, func(extent string) (io.ReaderAt, int64, io.Closer, error) {
		name := extent
		if folder != "." {
			name = folder + "/" + extent
		}
		data, err := openVMDatastoreFile(ctx, client, datacenter, datastore, name)
		if err != nil {
			return nil, 0, nil, err
		}
		return data, data.size, data, nil
	})
}
