package main

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// replTestMFTRecord builds a 1024-byte MFT record with a non-resident
// unnamed $DATA attribute holding the given mapping pairs, with update
// sequence fixups applied as NTFS writes them.
func replTestMFTRecord(pairs []byte, extra ...[]byte) []byte {
	record := make([]byte, 1024)
	copy(record, "FILE")
	binary.LittleEndian.PutUint16(record[4:], 48) // update sequence at 48
	binary.LittleEndian.PutUint16(record[6:], 3)  // usn + 2 sectors
	binary.LittleEndian.PutUint16(record[20:], 56)
	binary.LittleEndian.PutUint16(record[22:], 1) // in use
	at := 56
	for _, attribute := range extra {
		copy(record[at:], attribute)
		at += len(attribute)
	}
	data := make([]byte, 64+(len(pairs)+8)/8*8)
	binary.LittleEndian.PutUint32(data, 0x80)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)))
	data[8] = 1
	binary.LittleEndian.PutUint16(data[32:], 64)
	copy(data[64:], pairs)
	copy(record[at:], data)
	at += len(data)
	binary.LittleEndian.PutUint32(record[at:], 0xFFFFFFFF)
	// Fixups: the last two bytes of each 512-byte stride move to the array.
	usn := []byte{0x07, 0x00}
	copy(record[48:], usn)
	for i := 1; i <= 2; i++ {
		end := i*512 - 2
		record[48+2*i], record[48+2*i+1] = record[end], record[end+1]
		record[end], record[end+1] = usn[0], usn[1]
	}
	return record
}

func TestMFTRecordRunsAreDecoded(t *testing.T) {
	// 0x30000 clusters at LCN 0x2000, then a sparse run, then 16 clusters
	// 0x100 clusters before the first run's start (a negative offset).
	pairs := []byte{0x23, 0x00, 0x00, 0x03, 0x00, 0x20, 0x01, 0x08, 0x21, 0x10, 0x00, 0xFF, 0x00}
	runs, _, err := replMFTRuns(replTestMFTRecord(pairs))
	if err != nil {
		t.Fatal(err)
	}
	want := []replRun{{Start: 0x2000, Count: 0x30000}, {Start: 0x1F00, Count: 0x10}}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("runs %+v, want %+v", runs, want)
	}
	torn := replTestMFTRecord(pairs)
	torn[510] ^= 1
	if _, _, err := replMFTRuns(torn); err == nil {
		t.Fatal("a torn record was accepted")
	}
	list := make([]byte, 24)
	binary.LittleEndian.PutUint32(list, 0x20)
	binary.LittleEndian.PutUint32(list[4:], 24)
	if _, _, err := replMFTRuns(replTestMFTRecord(pairs, list)); err == nil {
		t.Fatal("a record with an attribute list was accepted")
	}
	if _, _, err := replMFTRuns(make([]byte, 1024)); err == nil {
		t.Fatal("a blank record was accepted")
	}
}

func TestMFTOffsetsFollowTheMFTRuns(t *testing.T) {
	runs := []replRun{{Start: 100, Count: 4}, {Start: 900, Count: 2}}
	for offset, want := range map[int64]int64{0: 100 * 4096, 4096*3 + 1024: 103*4096 + 1024, 4096 * 4: 900 * 4096, 4096*5 + 4095: 901*4096 + 4095} {
		got, err := replMFTOffset(runs, 4096, offset)
		if err != nil || got != want {
			t.Errorf("offset %d: %d %v, want %d", offset, got, err, want)
		}
	}
	if _, err := replMFTOffset(runs, 4096, 4096*6); err == nil {
		t.Fatal("an offset past the MFT was accepted")
	}
}

// A file whose data moved (defragmentation, or data moved into space another
// file freed) changes neither the change journal nor the bitmap. Its MFT
// record's runs change, and the scan reports the new runs.
func TestMFTScanReportsFilesWhoseDataMoved(t *testing.T) {
	at := func(lcn byte) []byte { return []byte{0x11, 0x10, lcn, 0} } // 16 clusters at lcn
	unused := replTestMFTRecord(at(0x40))
	unused[22] = 0
	mft := func(records ...[]byte) func(func(int64, []byte) error) error {
		return func(each func(int64, []byte) error) error {
			for i, record := range records {
				if err := each(int64(i), record); err != nil {
					return err
				}
			}
			return nil
		}
	}
	list := replTestMFTRecord([]byte{0x11, 0x08, 0x50, 0}, append([]byte{0x20, 0, 0, 0, 0x18, 0, 0, 0}, make([]byte, 16)...))
	first, moved, err := replMovedRuns(nil, mft(replTestMFTRecord(at(0x10)), replTestMFTRecord(at(0x20)), make([]byte, 1024), unused, list))
	if err != nil || len(moved) != 0 || len(first) != 5 {
		t.Fatalf("first scan %v %v %v", first, moved, err)
	}
	if first[0] == 0 || first[1] == 0 || first[2] != 0 || first[3] != 0 || first[4] == 0 {
		t.Fatalf("locations %v: records in use need a location, others none", first)
	}
	// Record 1's data moved into record 0's old clusters and record 0
	// moved on; record 4 (with an attribute list) is unchanged; a new
	// record appeared.
	second, moved, err := replMovedRuns(first, mft(replTestMFTRecord(at(0x30)), replTestMFTRecord(at(0x10)), make([]byte, 1024), unused, list, replTestMFTRecord(at(0x60))))
	if err != nil {
		t.Fatal(err)
	}
	want := []replRun{{Start: 0x30, Count: 16}, {Start: 0x10, Count: 16}, {Start: 0x60, Count: 16}}
	if !reflect.DeepEqual(moved, want) || len(second) != 6 || second[4] != first[4] {
		t.Fatalf("moved %v, locations %v", moved, second)
	}
	// A damaged record in use stops the scan: tracking then reads in full.
	torn := replTestMFTRecord(at(0x10))
	torn[510] ^= 0xFF
	if _, _, err := replMovedRuns(first, mft(torn)); err == nil {
		t.Fatal("a torn record was accepted")
	}
}

// The change journal and the other files in $Extend are written in place,
// so their runs are read every round even when nothing shows a change,
// including runs held by records that extend them.
func TestMovedRunsAlwaysIncludeTheFilesInExtend(t *testing.T) {
	at := func(lcn byte) []byte { return []byte{0x11, 0x10, lcn, 0} }
	name := func(parent uint64) []byte {
		attribute := make([]byte, 40)
		binary.LittleEndian.PutUint32(attribute, 0x30)
		binary.LittleEndian.PutUint32(attribute[4:], 40)
		binary.LittleEndian.PutUint16(attribute[20:], 24)
		binary.LittleEndian.PutUint64(attribute[24:], parent|5<<48)
		return attribute
	}
	extends := func(record []byte, base uint64) []byte {
		binary.LittleEndian.PutUint64(record[32:], base|1<<48)
		return record
	}
	// 0: an ordinary file; 1: extends the journal (met before it);
	// 2: the journal, in $Extend; 3: extends the journal; 4: another
	// ordinary file.
	records := [][]byte{
		replTestMFTRecord(at(0x10), name(5)),
		extends(replTestMFTRecord(at(0x20)), 2),
		replTestMFTRecord(at(0x30), name(replMFTExtend)),
		extends(replTestMFTRecord(at(0x40)), 2),
		replTestMFTRecord(at(0x50)),
	}
	mft := func(each func(int64, []byte) error) error {
		for i, record := range records {
			if err := each(int64(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	first, moved, err := replMovedRuns(nil, mft)
	if err != nil || len(moved) != 0 {
		t.Fatalf("first scan %v %v", moved, err)
	}
	_, moved, err = replMovedRuns(first, mft)
	if err != nil {
		t.Fatal(err)
	}
	want := []replRun{{Start: 0x30, Count: 16}, {Start: 0x20, Count: 16}, {Start: 0x40, Count: 16}}
	if !reflect.DeepEqual(moved, want) {
		t.Fatalf("nothing moved, yet the journal's runs must be read: got %v, want %v", moved, want)
	}
}
