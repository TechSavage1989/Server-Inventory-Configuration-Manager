package main

// Reading where an NTFS system file lies from its MFT record, for system
// files Windows will not open even on a shadow copy ($LogFile on Windows
// Server 2025, for example). The first 16 MFT records always lie at the
// start of the MFT, so the record is read straight from the volume.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// replMFTRuns returns the cluster runs of every non-resident attribute in
// one MFT record (data, named streams, folder indexes, bitmaps) and the
// record's sequence number. It applies the update sequence fixups first,
// reports a record not in use with errReplMFTUnused, and refuses a record
// that keeps its attributes elsewhere (an attribute list).
func replMFTRuns(record []byte) ([]replRun, uint16, error) {
	return replMFTRecordRuns(record, false)
}

// replMFTOwnRuns is replMFTRuns for one record on its own: a record with an
// attribute list gives the runs it holds itself, since the rest lie in
// other records the scan reads too.
func replMFTOwnRuns(record []byte) ([]replRun, uint16, error) {
	return replMFTRecordRuns(record, true)
}

// replMFTFixed returns a copy of an MFT record with its update sequence
// fixups applied.
func replMFTFixed(record []byte) ([]byte, error) {
	if len(record) < 64 || string(record[:4]) != "FILE" {
		return nil, errors.New("not an MFT record")
	}
	fixed := append([]byte(nil), record...)
	usaOffset, usaCount := int(binary.LittleEndian.Uint16(fixed[4:])), int(binary.LittleEndian.Uint16(fixed[6:]))
	if usaCount < 2 || usaOffset+2*usaCount > len(fixed) || len(fixed)%(usaCount-1) != 0 {
		return nil, errors.New("the MFT record's update sequence is damaged")
	}
	stride := len(fixed) / (usaCount - 1)
	for i := 1; i < usaCount; i++ {
		end := i*stride - 2
		if fixed[end] != fixed[usaOffset] || fixed[end+1] != fixed[usaOffset+1] {
			return nil, errors.New("the MFT record is torn (its update sequence does not match)")
		}
		fixed[end], fixed[end+1] = fixed[usaOffset+2*i], fixed[usaOffset+2*i+1]
	}
	return fixed, nil
}

func replMFTRecordRuns(record []byte, allowList bool) ([]replRun, uint16, error) {
	fixed, err := replMFTFixed(record)
	if err != nil {
		return nil, 0, err
	}
	sequence := binary.LittleEndian.Uint16(fixed[16:])
	if binary.LittleEndian.Uint16(fixed[22:])&1 == 0 {
		return nil, sequence, errReplMFTUnused
	}
	runs := []replRun{}
	at := int(binary.LittleEndian.Uint16(fixed[20:]))
	for at+16 <= len(fixed) {
		kind := binary.LittleEndian.Uint32(fixed[at:])
		if kind == 0xFFFFFFFF {
			break
		}
		length := int(binary.LittleEndian.Uint32(fixed[at+4:]))
		if length < 16 || at+length > len(fixed) {
			return nil, sequence, errors.New("an attribute runs past the end of the MFT record")
		}
		attribute := fixed[at : at+length]
		if kind == 0x20 && !allowList {
			return nil, sequence, errors.New("the file keeps its attributes in other MFT records")
		}
		if attribute[8] != 0 { // non-resident
			if length < 64 {
				return nil, sequence, errors.New("a non-resident attribute is too short")
			}
			pairs := int(binary.LittleEndian.Uint16(attribute[32:]))
			lcn := int64(0)
			for p := pairs; p < length && attribute[p] != 0; {
				header := attribute[p]
				lengthSize, offsetSize := int(header&0x0F), int(header>>4)
				if lengthSize == 0 || lengthSize > 8 || offsetSize > 8 || p+1+lengthSize+offsetSize > length {
					return nil, sequence, errors.New("a run list is damaged")
				}
				count := int64(0)
				for i := lengthSize - 1; i >= 0; i-- {
					count = count<<8 | int64(attribute[p+1+i])
				}
				if offsetSize > 0 {
					delta := int64(int8(attribute[p+lengthSize+offsetSize]))
					for i := offsetSize - 2; i >= 0; i-- {
						delta = delta<<8 | int64(attribute[p+1+lengthSize+i])
					}
					lcn += delta
					if lcn < 0 || count <= 0 {
						return nil, sequence, errors.New("a run points outside the volume")
					}
					runs = append(runs, replRun{Start: lcn, Count: count})
				}
				p += 1 + lengthSize + offsetSize
			}
		}
		at += length
	}
	return runs, sequence, nil
}

var errReplMFTUnused = errors.New("the MFT record is not in use")

// replMFTOffset maps a byte offset inside the MFT to a byte offset on the
// volume through the MFT's own runs (in file order).
func replMFTOffset(mftRuns []replRun, clusterSize, offset int64) (int64, error) {
	cluster := offset / clusterSize
	for _, run := range mftRuns {
		if cluster < run.Count {
			return (run.Start+cluster)*clusterSize + offset%clusterSize, nil
		}
		cluster -= run.Count
	}
	return 0, fmt.Errorf("MFT offset %d is past the end of the MFT", offset)
}

// replMFTLocation sums up where one MFT record's data lies: zero for a
// record not in use, otherwise a hash of its sequence number and runs. A
// file whose clusters move (defragmentation, or Windows moving data into
// space another file freed) keeps its data, so neither the change journal
// nor the volume bitmap shows the move; its record's runs do.
func replMFTLocation(record []byte) (uint64, []replRun, error) {
	if len(record) < 4 || string(record[:4]) != "FILE" {
		return 0, nil, nil // never used
	}
	runs, sequence, err := replMFTOwnRuns(record)
	if errors.Is(err, errReplMFTUnused) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	hash := uint64(14695981039346656037)
	mix := func(v uint64) {
		for i := 0; i < 8; i++ {
			hash ^= v & 0xFF
			hash *= 1099511628211
			v >>= 8
		}
	}
	mix(uint64(sequence) | 1<<16)
	for _, run := range runs {
		mix(uint64(run.Start))
		mix(uint64(run.Count))
	}
	if hash == 0 {
		hash = 1
	}
	return hash, runs, nil
}

// replMFTName is one $FILE_NAME of an MFT record.
type replMFTName struct {
	Parent int64 // the parent folder's record number
	Name   string
}

// replMFTNames returns a record in use's file names and, for an extension
// record (one holding attributes of another file), its base record number
// (zero otherwise).
func replMFTNames(record []byte) ([]replMFTName, int64, error) {
	fixed, err := replMFTFixed(record)
	if err != nil {
		return nil, 0, err
	}
	base := int64(binary.LittleEndian.Uint64(fixed[32:]) & 0xFFFFFFFFFFFF)
	names := []replMFTName{}
	for at := int(binary.LittleEndian.Uint16(fixed[20:])); at+16 <= len(fixed); {
		kind := binary.LittleEndian.Uint32(fixed[at:])
		if kind == 0xFFFFFFFF {
			break
		}
		length := int(binary.LittleEndian.Uint32(fixed[at+4:]))
		if length < 16 || at+length > len(fixed) {
			return nil, base, errors.New("an attribute runs past the end of the MFT record")
		}
		if kind == 0x30 && fixed[at+8] == 0 && length >= 24 {
			content := at + int(binary.LittleEndian.Uint16(fixed[at+20:]))
			if content+66 > at+length {
				return nil, base, errors.New("a file name runs past its attribute")
			}
			count := int(fixed[content+64])
			if content+66+2*count > at+length {
				return nil, base, errors.New("a file name runs past its attribute")
			}
			chars := make([]uint16, count)
			for i := range chars {
				chars[i] = binary.LittleEndian.Uint16(fixed[content+66+2*i:])
			}
			names = append(names, replMFTName{Parent: int64(binary.LittleEndian.Uint64(fixed[content:]) & 0xFFFFFFFFFFFF), Name: string(utf16.Decode(chars))})
		}
		at += length
	}
	return names, base, nil
}

// replDiffAreaSuffix ends the name of every Volume Shadow Copy storage file
// (System Volume Information\{id}{3808876b-...}).
const replDiffAreaSuffix = "{3808876b-c176-4e48-b7ae-04046e6cc752}"

// replMFTScan is what one pass over a shadow copy's MFT finds.
type replMFTScan struct {
	// Moved holds the runs of every record whose location changed.
	Moved []replRun
	// Metadata holds the runs of the NTFS system files: records 0 to 15
	// (but the root folder and $BadClus) and everything under $Extend
	// ($UsnJrnl, $ObjId, $Quota, $Reparse, $RmMetadata and its logs).
	// Windows will not open some of them even on a shadow copy, and none
	// is listed in the change journal, so every round reads them.
	Metadata []replRun
	// DiffArea holds the runs of shadow copy storage files. Their content
	// on a shadow copy is not frozen (shadow copies do not copy their own
	// storage on write) and is meaningless on a copy, so they are read as
	// free space.
	DiffArea []replRun
}

// replMovedRuns compares every record's location with the last round's and
// returns the runs of each record whose location changed, with the runs of
// the system files and shadow copy storage files. records is the MFT read
// in order, recordSize bytes each.
func replMovedRuns(previous []uint64, read func(each func(index int64, record []byte) error) error) ([]uint64, replMFTScan, error) {
	locations := []uint64{}
	scan := replMFTScan{Moved: []replRun{}}
	type candidate struct {
		parent int64
		runs   []replRun
	}
	system := map[int64]*candidate{}  // records named $..., by record
	diffArea := map[int64][]replRun{} // shadow copy storage files
	extensions := map[int64][]replRun{}
	err := read(func(index int64, record []byte) error {
		location, runs, err := replMFTLocation(record)
		if err != nil {
			return fmt.Errorf("MFT record %d: %w", index, err)
		}
		for int64(len(locations)) < index {
			locations = append(locations, 0)
		}
		locations = append(locations, location)
		if previous != nil && location != 0 && (index >= int64(len(previous)) || previous[index] != location) {
			scan.Moved = append(scan.Moved, runs...)
		}
		if location == 0 {
			return nil
		}
		names, base, err := replMFTNames(record)
		if err != nil {
			return fmt.Errorf("MFT record %d: %w", index, err)
		}
		if base != 0 && base != index {
			// Kept while its base record could still matter: one seen
			// already as a system or storage file, or one not read yet.
			_, isSystem := system[base]
			_, isDiff := diffArea[base]
			if isSystem || isDiff || base > index || base < 16 {
				extensions[base] = append(extensions[base], runs...)
			}
			return nil
		}
		if index < 16 {
			system[index] = &candidate{parent: -1, runs: runs}
			return nil
		}
		for _, name := range names {
			if strings.HasSuffix(strings.ToLower(name.Name), replDiffAreaSuffix) {
				diffArea[index] = runs
				return nil
			}
		}
		for _, name := range names {
			if strings.HasPrefix(name.Name, "$") {
				system[index] = &candidate{parent: name.Parent, runs: runs}
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, replMFTScan{}, err
	}
	// A $ file is a system file when its parent is $Extend or another
	// system file under it.
	known := map[int64]bool{}
	var isSystem func(record int64, depth int) bool
	isSystem = func(record int64, depth int) bool {
		if record < 16 {
			return record != 5 && record != 8
		}
		if done, ok := known[record]; ok {
			return done
		}
		found := system[record]
		result := found != nil && depth < 32 && isSystem(found.parent, depth+1)
		known[record] = result
		return result
	}
	for record, found := range system {
		if isSystem(record, 0) {
			scan.Metadata = append(scan.Metadata, found.runs...)
			scan.Metadata = append(scan.Metadata, extensions[record]...)
		}
	}
	for record, runs := range diffArea {
		scan.DiffArea = append(scan.DiffArea, runs...)
		scan.DiffArea = append(scan.DiffArea, extensions[record]...)
	}
	return locations, scan, nil
}
