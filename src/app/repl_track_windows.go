//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	replFsctlQueryUsnJournal      = 0x000900F4
	replFsctlReadUsnJournal       = 0x000900BB
	replFsctlGetVolumeBitmap      = 0x0009006F
	replFsctlGetRetrievalPointers = 0x00090073
	replFsctlGetNtfsVolumeData    = 0x00090064
	replFsctlCreateUsnJournal     = 0x000900E7
	replErrorJournalNotActive     = windows.Errno(1179)
	// Change journal reasons for named streams (alternate data streams).
	replUsnNamedData = 0x00000010 | 0x00000020 | 0x00000040
)

var (
	replKernel32       = windows.NewLazySystemDLL("kernel32.dll")
	replProcOpenFileID = replKernel32.NewProc("OpenFileById")
	replVolumeGUID     = regexp.MustCompile(`\{([0-9A-Fa-f-]{36})\}`)
)

// replMark is a volume's change journal position at one moment.
type replMark struct {
	ID     uint64
	Lowest int64
	Next   int64
}

func replOpenVolume(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

func replQueryJournal(volume windows.Handle) (replMark, error) {
	out := make([]byte, 80)
	var returned uint32
	if err := windows.DeviceIoControl(volume, replFsctlQueryUsnJournal, nil, 0, &out[0], uint32(len(out)), &returned, nil); err != nil {
		return replMark{}, err
	}
	return replMark{ID: binary.LittleEndian.Uint64(out), Next: int64(binary.LittleEndian.Uint64(out[16:])), Lowest: int64(binary.LittleEndian.Uint64(out[24:]))}, nil
}

// replJournalMarks reads the journal position of every simple NTFS volume
// on the disks. Volumes without a journal are left out (read in full).
func replJournalMarks(numbers []int) map[string]replMark {
	marks := map[string]replMark{}
	for _, number := range numbers {
		volumes, err := vmDiskVolumes(number)
		if err != nil {
			continue
		}
		for _, volume := range volumes {
			if volume.FileSystem != "NTFS" || volume.Layout != nil {
				continue
			}
			handle, err := replOpenVolume(strings.TrimSuffix(volume.Name, `\`))
			if err != nil {
				continue
			}
			mark, err := replQueryJournal(handle)
			if errors.Is(err, replErrorJournalNotActive) {
				// A volume without a change journal gets a small one (32 MB),
				// as backup and search software do; tracking needs it.
				in := make([]byte, 16)
				binary.LittleEndian.PutUint64(in, 32<<20)
				binary.LittleEndian.PutUint64(in[8:], 8<<20)
				var returned uint32
				if windows.DeviceIoControl(handle, replFsctlCreateUsnJournal, &in[0], 16, nil, 0, &returned, nil) == nil {
					mark, err = replQueryJournal(handle)
				}
			}
			if err == nil {
				marks[volume.Name] = mark
			}
			windows.CloseHandle(handle)
		}
	}
	return marks
}

// replReadJournal lists the files changed between two journal positions:
// each file reference with the reasons seen, and each parent folder.
func replReadJournal(ctx context.Context, volume windows.Handle, id uint64, from, to int64, limit int) (files map[uint64]uint32, folders map[uint64]bool, err error) {
	files, folders = map[uint64]uint32{}, map[uint64]bool{}
	in := make([]byte, 40)
	out := make([]byte, 256<<10)
	start := from
	for start < to {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		binary.LittleEndian.PutUint64(in[0:], uint64(start))
		binary.LittleEndian.PutUint32(in[8:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(in[12:], 0)
		binary.LittleEndian.PutUint64(in[16:], 0)
		binary.LittleEndian.PutUint64(in[24:], 0)
		binary.LittleEndian.PutUint64(in[32:], id)
		var returned uint32
		if err := windows.DeviceIoControl(volume, replFsctlReadUsnJournal, &in[0], uint32(len(in)), &out[0], uint32(len(out)), &returned, nil); err != nil {
			return nil, nil, fmt.Errorf("reading the change journal: %w", err)
		}
		if returned < 8 {
			break
		}
		next := int64(binary.LittleEndian.Uint64(out))
		done := false
		for at := uint32(8); at+60 <= returned; {
			length := binary.LittleEndian.Uint32(out[at:])
			if length < 60 || at+length > returned {
				return nil, nil, errors.New("the change journal returned a malformed record")
			}
			record := out[at : at+length]
			if major := binary.LittleEndian.Uint16(record[4:]); major != 2 {
				return nil, nil, fmt.Errorf("the change journal uses version %d records", major)
			}
			if usn := int64(binary.LittleEndian.Uint64(record[24:])); usn >= to {
				done = true
				break
			}
			file := binary.LittleEndian.Uint64(record[8:])
			files[file] |= binary.LittleEndian.Uint32(record[40:])
			folders[binary.LittleEndian.Uint64(record[16:])] = true
			if len(files)+len(folders) > limit {
				return nil, nil, fmt.Errorf("more than %d files changed", limit)
			}
			at += length
		}
		if done || next <= start {
			break
		}
		start = next
	}
	return files, folders, nil
}

func replNTFSGeometry(volume windows.Handle) (clusterSize, clusters int64, err error) {
	out := make([]byte, 128)
	var returned uint32
	if err := windows.DeviceIoControl(volume, replFsctlGetNtfsVolumeData, nil, 0, &out[0], uint32(len(out)), &returned, nil); err != nil {
		return 0, 0, err
	}
	clusters = int64(binary.LittleEndian.Uint64(out[16:]))
	clusterSize = int64(binary.LittleEndian.Uint32(out[44:]))
	if clusterSize <= 0 || clusters <= 0 {
		return 0, 0, errors.New("the volume reported no clusters")
	}
	return clusterSize, clusters, nil
}

func replVolumeBitmap(volume windows.Handle, clusters int64) ([]byte, error) {
	in := make([]byte, 8)
	out := make([]byte, 16+(clusters+7)/8+8)
	var returned uint32
	if err := windows.DeviceIoControl(volume, replFsctlGetVolumeBitmap, &in[0], 8, &out[0], uint32(len(out)), &returned, nil); err != nil {
		return nil, fmt.Errorf("reading the volume bitmap: %w", err)
	}
	if size := int64(binary.LittleEndian.Uint64(out[8:])); size != clusters {
		return nil, fmt.Errorf("the volume bitmap covers %d clusters, not %d", size, clusters)
	}
	return append([]byte(nil), out[16:16+(clusters+7)/8]...), nil
}

// replRetrievalRuns lists where a file stream lies on its volume.
func replRetrievalRuns(file windows.Handle) ([]replRun, error) {
	runs := []replRun{}
	in := make([]byte, 8)
	out := make([]byte, 64<<10)
	vcn := int64(0)
	for {
		binary.LittleEndian.PutUint64(in, uint64(vcn))
		var returned uint32
		err := windows.DeviceIoControl(file, replFsctlGetRetrievalPointers, &in[0], 8, &out[0], uint32(len(out)), &returned, nil)
		if errors.Is(err, windows.ERROR_HANDLE_EOF) {
			return runs, nil // resident or empty: kept in the MFT record
		}
		more := errors.Is(err, windows.ERROR_MORE_DATA)
		if err != nil && !more {
			return nil, err
		}
		count := int(binary.LittleEndian.Uint32(out))
		previous := int64(binary.LittleEndian.Uint64(out[8:]))
		for i := 0; i < count && 16+i*16+16 <= int(returned); i++ {
			next := int64(binary.LittleEndian.Uint64(out[16+i*16:]))
			lcn := int64(binary.LittleEndian.Uint64(out[24+i*16:]))
			if lcn >= 0 && next > previous {
				runs = append(runs, replRun{Start: lcn, Count: next - previous})
			}
			previous = next
		}
		if !more || count == 0 {
			return runs, nil
		}
		vcn = previous
	}
}

func replOpenByID(hint windows.Handle, id uint64) (windows.Handle, error) {
	descriptor := make([]byte, 24)
	binary.LittleEndian.PutUint32(descriptor, 24)
	binary.LittleEndian.PutUint64(descriptor[8:], id)
	handle, _, err := replProcOpenFileID.Call(uintptr(hint), uintptr(unsafe.Pointer(&descriptor[0])), 0x80 /* FILE_READ_ATTRIBUTES */, uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE), 0, uintptr(windows.FILE_FLAG_BACKUP_SEMANTICS))
	if windows.Handle(handle) == windows.InvalidHandle {
		return 0, err
	}
	return windows.Handle(handle), nil
}

func replOpenAttributes(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, 0x80, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

// replPathRuns opens a path on the shadow copy and lists its runs.
func replPathRuns(path string) ([]replRun, error) {
	handle, err := replOpenAttributes(path)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	return replRetrievalRuns(handle)
}

func replFinalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0x2 /* VOLUME_NAME_NT */)
	if err != nil {
		return "", err
	}
	if int(n) >= len(buffer) {
		buffer = make([]uint16, n+1)
		if n, err = windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0x2); err != nil {
			return "", err
		}
	}
	return `\\?\GLOBALROOT` + windows.UTF16ToString(buffer[:n]), nil
}

// replStreamNames lists a file's named streams (":name:$DATA").
func replStreamNames(handle windows.Handle) ([]string, error) {
	buffer := make([]byte, 64<<10)
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileStreamInfo, &buffer[0], uint32(len(buffer))); err != nil {
		if errors.Is(err, windows.ERROR_HANDLE_EOF) {
			return nil, nil
		}
		return nil, err
	}
	names := []string{}
	for at := 0; at+24 <= len(buffer); {
		next := int(binary.LittleEndian.Uint32(buffer[at:]))
		length := int(binary.LittleEndian.Uint32(buffer[at+4:]))
		if at+24+length > len(buffer) {
			break
		}
		raw := buffer[at+24 : at+24+length]
		name := make([]uint16, length/2)
		for i := range name {
			name[i] = binary.LittleEndian.Uint16(raw[i*2:])
		}
		if text := windows.UTF16ToString(name); text != "::$DATA" {
			names = append(names, text)
		}
		if next == 0 {
			break
		}
		at += next
	}
	return names, nil
}

// The NTFS metadata files read in full on every tracked round. The first
// four must answer or the volume is read in full.
var replMetaRequired = []string{`$MFT`, `$MFTMirr`, `$LogFile`, `$Bitmap`}

// replMetaRecords are the MFT record numbers of replMetaRequired.
var replMetaRecords = []int64{0, 1, 2, 6}

// replMFTReader reads MFT records straight from a shadowed volume, for
// files Windows will not open even on a shadow copy ($LogFile on Windows
// Server 2025, some files under System Volume Information).
type replMFTReader struct {
	volume      windows.Handle
	clusterSize int64
	recordSize  int64
	validLength int64 // bytes of the MFT in use
	mftRuns     []replRun
}

func newReplMFTReader(device string) (*replMFTReader, error) {
	volume, err := replOpenVolume(device)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 128)
	var returned uint32
	if err := windows.DeviceIoControl(volume, replFsctlGetNtfsVolumeData, nil, 0, &out[0], uint32(len(out)), &returned, nil); err != nil {
		windows.CloseHandle(volume)
		return nil, err
	}
	r := &replMFTReader{volume: volume, clusterSize: int64(binary.LittleEndian.Uint32(out[44:])), recordSize: int64(binary.LittleEndian.Uint32(out[48:])), validLength: int64(binary.LittleEndian.Uint64(out[56:]))}
	mftStart := int64(binary.LittleEndian.Uint64(out[64:]))
	if r.clusterSize <= 0 || r.recordSize < 1024 || r.recordSize > 65536 || mftStart <= 0 {
		r.Close()
		return nil, errors.New("the volume reported an unexpected MFT layout")
	}
	// Record 0 is $MFT itself, at the MFT's first cluster.
	r.mftRuns = []replRun{{Start: mftStart, Count: (r.recordSize + r.clusterSize - 1) / r.clusterSize}}
	runs, _, err := r.Runs(0)
	if err != nil || len(runs) == 0 || runs[0].Start != mftStart {
		r.Close()
		if err == nil {
			err = errors.New("the MFT does not start where the volume says")
		}
		return nil, fmt.Errorf("reading the MFT's own record: %w", err)
	}
	r.mftRuns = runs
	return r, nil
}

func (r *replMFTReader) Close() {
	if r.volume != 0 {
		windows.CloseHandle(r.volume)
		r.volume = 0
	}
}

// Runs returns the runs of every non-resident attribute of one record and
// its sequence number.
func (r *replMFTReader) Runs(record int64) ([]replRun, uint16, error) {
	offset, err := replMFTOffset(r.mftRuns, r.clusterSize, record*r.recordSize)
	if err != nil {
		return nil, 0, err
	}
	const align = 4096
	start := offset / align * align
	buffer := make([]byte, (offset+r.recordSize-start+align-1)/align*align)
	var read uint32
	overlapped := windows.Overlapped{Offset: uint32(start), OffsetHigh: uint32(start >> 32)}
	if err := windows.ReadFile(r.volume, buffer, &read, &overlapped); err != nil {
		return nil, 0, err
	}
	if int64(read) < offset-start+r.recordSize {
		return nil, 0, errors.New("the MFT record could not be read in full")
	}
	return replMFTRuns(buffer[offset-start : offset-start+r.recordSize])
}

// Scan reads the MFT in order and calls each for every record. The MFT's
// data runs come first in its record, so the runs up to the MFT's valid
// length are its data.
func (r *replMFTReader) Scan(ctx context.Context, each func(index int64, record []byte) error) error {
	const piece = 4 << 20
	buffer := make([]byte, piece)
	pending := []byte{}
	index, done := int64(0), int64(0)
	for _, run := range r.mftRuns {
		for cluster := int64(0); cluster < run.Count && done < r.validLength; {
			if err := ctx.Err(); err != nil {
				return err
			}
			count := run.Count - cluster
			if count > piece/r.clusterSize {
				count = piece / r.clusterSize
			}
			size := count * r.clusterSize
			at := (run.Start + cluster) * r.clusterSize
			var read uint32
			overlapped := windows.Overlapped{Offset: uint32(at), OffsetHigh: uint32(at >> 32)}
			if err := windows.ReadFile(r.volume, buffer[:size], &read, &overlapped); err != nil {
				return fmt.Errorf("reading the MFT: %w", err)
			}
			if int64(read) != size {
				return errors.New("the MFT could not be read in full")
			}
			if remaining := r.validLength - done; size > remaining {
				size = remaining
			}
			pending = append(pending, buffer[:size]...)
			done += size
			cluster += count
			used := 0
			for ; int64(len(pending)-used) >= r.recordSize; used += int(r.recordSize) {
				if err := each(index, pending[used:used+int(r.recordSize)]); err != nil {
					return err
				}
				index++
			}
			pending = append(pending[:0], pending[used:]...)
		}
	}
	if done < r.validLength {
		return errors.New("the MFT's runs end before its valid length")
	}
	return nil
}

// replScanLocations reads a shadow copy's MFT and compares where each file
// lies with previous (see replMovedRuns).
func replScanLocations(ctx context.Context, device string, previous []uint64) ([]uint64, replMFTScan, error) {
	reader, err := newReplMFTReader(device)
	if err != nil {
		return nil, replMFTScan{}, err
	}
	defer reader.Close()
	return replMovedRuns(previous, func(each func(int64, []byte) error) error { return reader.Scan(ctx, each) })
}

var replMetaOptional = []string{`$MFT::$BITMAP`, `$Volume`, `$AttrDef`, `$Boot`, `$UpCase`, `$Secure:$SDS`, `$Secure:$SDH:$INDEX_ALLOCATION`, `$Secure:$SII:$INDEX_ALLOCATION`,
	`:$I30:$INDEX_ALLOCATION`, `$Extend:$I30:$INDEX_ALLOCATION`, `$Extend\$UsnJrnl:$J`, `$Extend\$UsnJrnl:$Max`, `$Extend\$Reparse:$R:$INDEX_ALLOCATION`, `$Extend\$ObjId:$O:$INDEX_ALLOCATION`,
	`$Extend\$Quota:$O:$INDEX_ALLOCATION`, `$Extend\$Quota:$Q:$INDEX_ALLOCATION`}

// replTrackedRuns lists what changed on a shadowed volume since the last
// round: clusters allocated or freed, metadata files and the files the
// journal names.
func replTrackedRuns(ctx context.Context, shadowRoot string, shadow windows.Handle, live windows.Handle, previous replVolumeTrack, prevBitmap, bitmap []byte, clusters int64, metadata []replRun) ([]replRun, int, error) {
	runs := replToggled(prevBitmap, bitmap, clusters)
	// Every system file, from the MFT scan: the paths below miss those
	// Windows will not open (Access is denied for $Boot and $Bitmap on
	// Windows Server 2025).
	runs = append(runs, metadata...)
	var mft *replMFTReader
	defer func() {
		if mft != nil {
			mft.Close()
		}
	}()
	// fromRecord finds where a file lies from its MFT record, for files
	// Windows will not open even on a shadow copy.
	fromRecord := func(record int64) ([]replRun, uint16, error) {
		if mft == nil {
			reader, err := newReplMFTReader(strings.TrimSuffix(shadowRoot, `\`))
			if err != nil {
				return nil, 0, err
			}
			mft = reader
		}
		return mft.Runs(record)
	}
	for index, name := range replMetaRequired {
		found, err := replPathRuns(shadowRoot + name)
		if err != nil {
			var recordErr error
			found, _, recordErr = fromRecord(replMetaRecords[index])
			if recordErr != nil {
				return nil, 0, fmt.Errorf("%s: %v; from its MFT record: %w", name, err, recordErr)
			}
		}
		runs = append(runs, found...)
	}
	for _, name := range replMetaOptional {
		if found, err := replPathRuns(shadowRoot + name); err == nil {
			runs = append(runs, found...)
		}
	}
	now, err := replQueryJournal(live)
	if err != nil {
		return nil, 0, err
	}
	if now.ID != previous.JournalID || previous.Mark < now.Lowest {
		return nil, 0, errors.New("the change journal was reset or has wrapped since the last round")
	}
	files, folders, err := replReadJournal(ctx, live, now.ID, previous.Mark, now.Next, replTrackMaxFiles)
	if err != nil {
		return nil, 0, err
	}
	hint, err := replOpenAttributes(shadowRoot)
	if err != nil {
		return nil, 0, err
	}
	defer windows.CloseHandle(hint)
	folders[5|5<<48] = true // the root folder
	add := func(id uint64, reasons uint32, folder bool) error {
		handle, err := replOpenByID(hint, id)
		if err != nil {
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
				return nil // deleted before the shadow copy, or reused
			}
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
				return err
			}
			// Every non-resident attribute of its record: data, named
			// streams and, for a folder, its index.
			found, sequence, recordErr := fromRecord(int64(id & 0xFFFFFFFFFFFF))
			if errors.Is(recordErr, errReplMFTUnused) || (recordErr == nil && sequence != uint16(id>>48)) {
				return nil // deleted before the shadow copy, or reused
			}
			if recordErr != nil {
				return fmt.Errorf("%v; from its MFT record: %w", err, recordErr)
			}
			runs = append(runs, found...)
			return nil
		}
		defer windows.CloseHandle(handle)
		found, err := replRetrievalRuns(handle)
		if err != nil {
			return err
		}
		runs = append(runs, found...)
		if !folder && reasons&replUsnNamedData == 0 {
			return nil
		}
		path, err := replFinalPath(handle)
		if err != nil {
			return err
		}
		if folder {
			if found, err := replPathRuns(path + `:$I30:$INDEX_ALLOCATION`); err == nil {
				runs = append(runs, found...)
			}
			return nil
		}
		streams, err := replStreamNames(handle)
		if err != nil {
			return err
		}
		for _, stream := range streams {
			found, err := replPathRuns(path + stream)
			if err != nil {
				return fmt.Errorf("%s%s: %w", path, stream, err)
			}
			runs = append(runs, found...)
		}
		return nil
	}
	for id, reasons := range files {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if err := add(id, reasons, false); err != nil {
			return nil, 0, fmt.Errorf("changed file %d: %w", id&0xFFFFFFFFFFFF, err)
		}
	}
	for id := range folders {
		if err := add(id, 0, true); err != nil {
			return nil, 0, fmt.Errorf("changed folder %d: %w", id&0xFFFFFFFFFFFF, err)
		}
	}
	return runs, len(files), nil
}

// replVolumeKey names a volume's tracking files by its GUID.
func replVolumeKey(name string) string {
	if match := replVolumeGUID.FindStringSubmatch(name); match != nil {
		return strings.ToLower(match[1])
	}
	return ""
}

type replTrackedVolume struct {
	key       string
	name      string
	label     string
	disk      int
	start     int64
	length    int64
	mark      replMark
	track     replVolumeTrack
	bitmap    []byte
	locations []uint64      // where each MFT record's data lies (replMFTLocation)
	tracked   []vmCopyRange // disk ranges tracking chose, for a check round
	check     bool
	disabled  string
}

// replPlanTrackedRound shows free clusters of each simple NTFS volume as
// zeros and sets the round's changed ranges.
func replPlanTrackedRound(ctx context.Context, trackDir, job string, numbers []int, set *vmAgentDiskSet, marks map[string]replMark, opened *replOpenedDisks, log func(string)) error {
	var store *replTrackStore
	state := replTrackState{Volumes: map[string]replVolumeTrack{}}
	if trackDir != "" {
		var err error
		if store, err = newReplTrackStore(trackDir, job); err != nil {
			return err
		}
		state = store.Load()
	}
	opened.Ranges, opened.Used = map[string][]vmCopyRange{}, nil
	volumes := []*replTrackedVolume{}
	now := time.Now()
	for _, number := range numbers {
		id := strconv.Itoa(number)
		source, _ := set.Sources[number].(*vmCompositeSource)
		if source == nil {
			continue
		}
		handled, changed := []vmCopyRange{}, []vmCopyRange{}
		full := false // read the whole disk: a check or a volume without tracking
		for _, info := range set.Volumes[number] {
			device := set.Shadows[info.Name]
			key := replVolumeKey(info.Name)
			if info.FileSystem != "NTFS" || info.Kind != "simple" || device == "" || key == "" || info.Length <= replTrackTail {
				continue
			}
			volume := &replTrackedVolume{key: key, name: info.Name, label: strings.Join(info.Mounts, ", "), disk: number, start: info.Start, length: info.Length}
			if volume.label == "" {
				volume.label = info.Name
			}
			shadow, err := replOpenVolume(device)
			if err != nil {
				log(fmt.Sprintf("%s: reading it in full (%v).", volume.label, err))
				full = true
				continue
			}
			clusterSize, clusters, err := replNTFSGeometry(shadow)
			var bitmap []byte
			if err == nil {
				bitmap, err = replVolumeBitmap(shadow, clusters)
			}
			windows.CloseHandle(shadow)
			if err == nil && clusterSize*clusters > info.Length {
				err = errors.New("the file system is larger than its partition")
			}
			if err != nil {
				log(fmt.Sprintf("%s: reading it in full (%v).", volume.label, err))
				full = true
				continue
			}
			volume.track = replVolumeTrack{ClusterSize: clusterSize, Clusters: clusters}
			mark, haveMark := marks[info.Name]
			volume.mark = mark
			previous, known := state.Volumes[key]
			reason := ""
			switch {
			case store == nil:
				reason = "no tracking folder"
			case !haveMark:
				reason = "the volume has no change journal"
			case !known:
				reason = "first round"
			case previous.Disabled != "":
				reason = "tracking was turned off: " + previous.Disabled
				volume.disabled = previous.Disabled
			case previous.JournalID != mark.ID || previous.ClusterSize != clusterSize || previous.Clusters != clusters:
				reason = "the volume changed size or its journal was recreated"
			}
			// Where every file lies, to catch data that moved without
			// changing (the journal and bitmap miss such moves).
			var prevLocations []uint64
			if reason == "" {
				if prevLocations, err = store.LoadLocations(key); err != nil {
					reason, prevLocations = "no record yet of where each file lies", nil
				}
			}
			var locations []uint64
			var scan replMFTScan
			err = nil
			if store != nil && volume.disabled == "" {
				locations, scan, err = replScanLocations(ctx, device, prevLocations)
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if reason == "" {
					reason = "reading where files lie: " + err.Error()
				}
			} else {
				volume.locations = locations
				// Shadow copy storage reads as free space: each round's
				// reuses the last one's clusters unseen, and it means
				// nothing on the copy.
				bitmap = replClearRuns(bitmap, clusters, scan.DiffArea)
			}
			if !source.WrapOverlay(info.Start, func(r io.ReaderAt) io.ReaderAt {
				return &vmFreeZeroReader{Reader: r, Bitmap: bitmap, ClusterSize: clusterSize, Clusters: clusters}
			}) {
				log(fmt.Sprintf("%s: reading it in full (its shadow copy is not laid over the disk).", volume.label))
				full = true
				continue
			}
			inUse := replRangeBytes(replVolumeToDisk(info.Start, clusterSize, replAllocatedRuns(bitmap, clusters)))
			volume.bitmap = bitmap
			handled = append(handled, vmCopyRange{Start: info.Start, Length: info.Length})
			var tracked []vmCopyRange
			if reason == "" {
				prevBitmap, err := store.LoadBitmap(key, len(bitmap))
				if err != nil {
					reason = err.Error()
				} else if live, err := replOpenVolume(strings.TrimSuffix(info.Name, `\`)); err != nil {
					reason = err.Error()
				} else {
					runs, files, err := replTrackedRuns(ctx, device+`\`, 0, live, previous, prevBitmap, bitmap, clusters, scan.Metadata)
					windows.CloseHandle(live)
					if err != nil {
						reason = err.Error()
					} else {
						runs = append(runs, scan.Moved...)
						tracked = append(replVolumeToDisk(info.Start, clusterSize, runs), vmCopyRange{Start: info.Start + info.Length - replTrackTail, Length: replTrackTail})
						volume.track.LastCheck = previous.LastCheck
						if now.Sub(previous.LastCheck) >= replTrackCheckEvery {
							volume.check, volume.tracked = true, tracked
							full = true
							log(fmt.Sprintf("%s: reading all %s in use to check change tracking.", volume.label, vmFormatBytes(inUse)))
						} else {
							changed = append(changed, tracked...)
							log(fmt.Sprintf("%s: %d changed files, %s to read of %s in use.", volume.label, files, vmFormatBytes(replRangeBytes(tracked)), vmFormatBytes(inUse)))
						}
					}
				}
			}
			if reason != "" {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				volume.check, full = true, true // a full pass: a fresh starting point
				log(fmt.Sprintf("%s: reading all %s in use (%s).", volume.label, vmFormatBytes(inUse), reason))
			}
			volumes = append(volumes, volume)
		}
		if full {
			opened.Ranges[id] = nil
		} else {
			opened.Ranges[id] = replMergeRanges(append(changed, replOutside(source.Size(), handled)...))
		}
	}
	opened.Commit = func(results map[string]*vmCopyResult) error {
		if store == nil {
			return nil
		}
		for _, volume := range volumes {
			track := volume.track
			track.Disabled = volume.disabled
			if volume.check && volume.tracked != nil {
				result := results[strconv.Itoa(volume.disk)]
				if result == nil {
					continue
				}
				// A chunk of this volume that changed although tracking
				// would not have read it is a miss.
				inVolume := []int{}
				for _, index := range result.ChangedChunks {
					start := int64(index) * result.State.ChunkSize
					if start+result.State.ChunkSize > volume.start && start < volume.start+volume.length {
						inVolume = append(inVolume, index)
					}
				}
				if missed := replMissedChunks(volume.tracked, inVolume, result.State.ChunkSize); len(missed) > 0 {
					track.Disabled = fmt.Sprintf("the check on %s found %d changed chunks tracking did not report (first at %s)", now.UTC().Format(time.RFC3339), len(missed), vmFormatBytes(int64(missed[0])*result.State.ChunkSize))
					log(fmt.Sprintf("%s: %s, so it is read in full from now on.", volume.label, track.Disabled))
				} else {
					log(fmt.Sprintf("%s: the check found no change that tracking missed.", volume.label))
				}
			}
			if volume.check {
				track.LastCheck = now
			}
			if _, ok := marks[volume.name]; !ok {
				delete(state.Volumes, volume.key)
				continue
			}
			track.JournalID, track.Mark = volume.mark.ID, volume.mark.Next
			if err := store.SaveBitmap(volume.key, volume.bitmap); err != nil {
				return err
			}
			// Without this round's locations the next round must not
			// compare with older ones.
			if volume.locations == nil {
				if err := store.RemoveLocations(volume.key); err != nil {
					return err
				}
			} else if err := store.SaveLocations(volume.key, volume.locations); err != nil {
				return err
			}
			state.Volumes[volume.key] = track
		}
		return store.Save(state)
	}
	return nil
}
