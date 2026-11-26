package main

// Reading where an NTFS system file lies from its MFT record, for system
// files Windows will not open even on a shadow copy ($LogFile on Windows
// Server 2025, for example). The first 16 MFT records always lie at the
// start of the MFT, so the record is read straight from the volume.

import (
	"encoding/binary"
	"errors"
	"fmt"
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

func replMFTRecordRuns(record []byte, allowList bool) ([]replRun, uint16, error) {
	runs, sequence, _, _, err := replMFTParse(record, allowList)
	return runs, sequence, err
}

// replMFTExtend is the MFT record of the $Extend folder, which holds the
// change journal ($UsnJrnl), $Quota, $ObjId, $Reparse and the transaction
// metadata.
const replMFTExtend = 11

// replMFTParse is replMFTRecordRuns plus who the record belongs to: parent
// is the folder a file name in the record lies in (replMFTExtend when any
// name does), base the record this one extends (zero for a base record).
func replMFTParse(record []byte, allowList bool) ([]replRun, uint16, int64, int64, error) {
	parent, base := int64(0), int64(0)
	if len(record) < 64 || string(record[:4]) != "FILE" {
		return nil, 0, 0, 0, errors.New("not an MFT record")
	}
	fixed := append([]byte(nil), record...)
	usaOffset, usaCount := int(binary.LittleEndian.Uint16(fixed[4:])), int(binary.LittleEndian.Uint16(fixed[6:]))
	if usaCount < 2 || usaOffset+2*usaCount > len(fixed) || len(fixed)%(usaCount-1) != 0 {
		return nil, 0, 0, 0, errors.New("the MFT record's update sequence is damaged")
	}
	stride := len(fixed) / (usaCount - 1)
	for i := 1; i < usaCount; i++ {
		end := i*stride - 2
		if fixed[end] != fixed[usaOffset] || fixed[end+1] != fixed[usaOffset+1] {
			return nil, 0, 0, 0, errors.New("the MFT record is torn (its update sequence does not match)")
		}
		fixed[end], fixed[end+1] = fixed[usaOffset+2*i], fixed[usaOffset+2*i+1]
	}
	sequence := binary.LittleEndian.Uint16(fixed[16:])
	if binary.LittleEndian.Uint16(fixed[22:])&1 == 0 {
		return nil, sequence, 0, 0, errReplMFTUnused
	}
	base = int64(binary.LittleEndian.Uint64(fixed[32:]) & 0xFFFFFFFFFFFF)
	runs := []replRun{}
	at := int(binary.LittleEndian.Uint16(fixed[20:]))
	for at+16 <= len(fixed) {
		kind := binary.LittleEndian.Uint32(fixed[at:])
		if kind == 0xFFFFFFFF {
			break
		}
		length := int(binary.LittleEndian.Uint32(fixed[at+4:]))
		if length < 16 || at+length > len(fixed) {
			return nil, sequence, 0, 0, errors.New("an attribute runs past the end of the MFT record")
		}
		attribute := fixed[at : at+length]
		if kind == 0x20 && !allowList {
			return nil, sequence, 0, 0, errors.New("the file keeps its attributes in other MFT records")
		}
		if kind == 0x30 && attribute[8] == 0 && length >= 24 { // a resident file name
			if content := int(binary.LittleEndian.Uint16(attribute[20:])); content+8 <= length {
				if folder := int64(binary.LittleEndian.Uint64(attribute[content:]) & 0xFFFFFFFFFFFF); parent != replMFTExtend {
					parent = folder
				}
			}
		}
		if attribute[8] != 0 { // non-resident
			if length < 64 {
				return nil, sequence, 0, 0, errors.New("a non-resident attribute is too short")
			}
			pairs := int(binary.LittleEndian.Uint16(attribute[32:]))
			lcn := int64(0)
			for p := pairs; p < length && attribute[p] != 0; {
				header := attribute[p]
				lengthSize, offsetSize := int(header&0x0F), int(header>>4)
				if lengthSize == 0 || lengthSize > 8 || offsetSize > 8 || p+1+lengthSize+offsetSize > length {
					return nil, sequence, 0, 0, errors.New("a run list is damaged")
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
						return nil, sequence, 0, 0, errors.New("a run points outside the volume")
					}
					runs = append(runs, replRun{Start: lcn, Count: count})
				}
				p += 1 + lengthSize + offsetSize
			}
		}
		at += length
	}
	return runs, sequence, parent, base, nil
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
	location, runs, _, _, err := replMFTLocationOwner(record)
	return location, runs, err
}

func replMFTLocationOwner(record []byte) (uint64, []replRun, int64, int64, error) {
	if len(record) < 4 || string(record[:4]) != "FILE" {
		return 0, nil, 0, 0, nil // never used
	}
	runs, sequence, parent, base, err := replMFTParse(record, true)
	if errors.Is(err, errReplMFTUnused) {
		return 0, nil, 0, 0, nil
	}
	if err != nil {
		return 0, nil, 0, 0, err
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
	return hash, runs, parent, base, nil
}

// replMovedRuns compares every record's location with the last round's and
// returns the runs of each record whose location changed, plus every run of
// the files in $Extend (and of the records that extend them) whether they
// moved or not: Windows writes the change journal and its other metadata
// there in place, which neither the journal nor the volume bitmap shows,
// and will not always open those files by path, even on a shadow copy.
// records is the MFT read in order, recordSize bytes each.
func replMovedRuns(previous []uint64, read func(each func(index int64, record []byte) error) error) ([]uint64, []replRun, error) {
	locations := []uint64{}
	moved := []replRun{}
	system := map[int64]bool{}     // base records of files in $Extend
	later := map[int64][]replRun{} // runs of extension records met before their base
	err := read(func(index int64, record []byte) error {
		location, runs, parent, base, err := replMFTLocationOwner(record)
		if err != nil {
			return fmt.Errorf("MFT record %d: %w", index, err)
		}
		for int64(len(locations)) < index {
			locations = append(locations, 0)
		}
		locations = append(locations, location)
		if previous == nil || location == 0 {
			return nil
		}
		changed := index >= int64(len(previous)) || previous[index] != location
		switch {
		case base == 0 && parent == replMFTExtend:
			system[index] = true
			moved = append(moved, runs...)
			moved = append(moved, later[index]...)
		case base != 0 && base < index && system[base]:
			moved = append(moved, runs...)
		case changed:
			moved = append(moved, runs...)
		case base > index:
			later[base] = append(later[base], runs...)
		}
		if base == 0 {
			delete(later, index)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return locations, moved, nil
}
