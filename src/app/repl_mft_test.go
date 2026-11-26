package main

import (
	"encoding/binary"
	"reflect"
	"sort"
	"testing"
	"unicode/utf16"
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
	if err != nil || len(moved.Moved) != 0 || len(first) != 5 {
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
	if !reflect.DeepEqual(moved.Moved, want) || len(second) != 6 || second[4] != first[4] {
		t.Fatalf("moved %v, locations %v", moved, second)
	}
	// A damaged record in use stops the scan: tracking then reads in full.
	torn := replTestMFTRecord(at(0x10))
	torn[510] ^= 0xFF
	if _, _, err := replMovedRuns(first, mft(torn)); err == nil {
		t.Fatal("a torn record was accepted")
	}
}

// replTestFileName builds a resident $FILE_NAME attribute.
func replTestFileName(parent int64, name string) []byte {
	chars := utf16.Encode([]rune(name))
	content := make([]byte, 66+2*len(chars))
	binary.LittleEndian.PutUint64(content, uint64(parent)|1<<48)
	content[64] = byte(len(chars))
	content[65] = 1
	for i, c := range chars {
		binary.LittleEndian.PutUint16(content[66+2*i:], c)
	}
	attribute := make([]byte, (24+len(content)+7)/8*8)
	binary.LittleEndian.PutUint32(attribute, 0x30)
	binary.LittleEndian.PutUint32(attribute[4:], uint32(len(attribute)))
	binary.LittleEndian.PutUint32(attribute[16:], uint32(len(content)))
	binary.LittleEndian.PutUint16(attribute[20:], 24)
	copy(attribute[24:], content)
	return attribute
}

func replTestSortRuns(runs []replRun) []replRun {
	sort.Slice(runs, func(i, j int) bool { return runs[i].Start < runs[j].Start })
	return runs
}

// The scan finds every NTFS system file, including those Windows will not
// open on a shadow copy ($Boot, $Bitmap) and those under $Extend with no
// fixed record number ($UsnJrnl, $RmMetadata's logs), and the shadow copy
// storage files, whose content changes under a shadow copy.
func TestMFTScanFindsSystemFilesAndShadowCopyStorage(t *testing.T) {
	at := func(lcn byte) []byte { return []byte{0x11, 0x01, lcn, 0} } // 1 cluster at lcn
	records := make([][]byte, 30)
	for i := range records {
		records[i] = make([]byte, 1024) // never used
	}
	records[0] = replTestMFTRecord(at(0x10), replTestFileName(5, "$MFT"))
	records[5] = replTestMFTRecord(at(0x15), replTestFileName(5, "."))        // the root folder: tracked through the journal
	records[7] = replTestMFTRecord(at(0x17), replTestFileName(5, "$Boot"))    // Access is denied on a shadow copy
	records[8] = replTestMFTRecord(at(0x18), replTestFileName(5, "$BadClus")) // never read
	records[11] = replTestMFTRecord(at(0x1B), replTestFileName(5, "$Extend"))
	records[16] = replTestMFTRecord(at(0x20), replTestFileName(5, "$Recycle.Bin"))  // a user folder named $
	records[17] = replTestMFTRecord(at(0x21), replTestFileName(16, "$R0AB12C.txt")) // a user file named $
	records[18] = replTestMFTRecord(at(0x22), replTestFileName(11, "$RmMetadata"))  // under $Extend
	records[19] = replTestMFTRecord(at(0x23), replTestFileName(18, "$TxfLog"))      // under $RmMetadata
	records[20] = replTestMFTRecord(at(0x24), replTestFileName(19, "$TxfLog.blf"))  // its log
	records[21] = replTestMFTRecord(at(0x25), replTestFileName(18, "Ordinary"))     // not a $ file
	records[22] = replTestMFTRecord(at(0x26), replTestFileName(5, "System Volume Information"))
	records[23] = replTestMFTRecord(at(0x30), replTestFileName(22, "{0A1B2C3D-0000-1111-2222-333344445555}{3808876B-C176-4E48-B7AE-04046E6CC752}"))
	records[24] = replTestMFTRecord(at(0x40)) // an extension record of the storage file
	binary.LittleEndian.PutUint64(records[24][32:], 23|1<<48)
	records[25] = replTestMFTRecord(at(0x50)) // an extension record of $UsnJrnl, before its base
	binary.LittleEndian.PutUint64(records[25][32:], 27|1<<48)
	records[26] = replTestMFTRecord(at(0x60)) // an extension record of a user file
	binary.LittleEndian.PutUint64(records[26][32:], 17|1<<48)
	records[27] = replTestMFTRecord(at(0x51), replTestFileName(11, "$UsnJrnl"))
	records[28] = replTestMFTRecord(at(0x70), replTestFileName(22, "tracking.log"))
	_, scan, err := replMovedRuns(nil, func(each func(int64, []byte) error) error {
		for i, record := range records {
			if err := each(int64(i), record); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	one := func(lcns ...int64) []replRun {
		runs := []replRun{}
		for _, lcn := range lcns {
			runs = append(runs, replRun{Start: lcn, Count: 1})
		}
		return runs
	}
	if got, want := replTestSortRuns(scan.Metadata), one(0x10, 0x17, 0x1B, 0x22, 0x23, 0x24, 0x50, 0x51); !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata %v, want %v", got, want)
	}
	if got, want := replTestSortRuns(scan.DiffArea), one(0x30, 0x40); !reflect.DeepEqual(got, want) {
		t.Fatalf("shadow copy storage %v, want %v", got, want)
	}
	if len(scan.Moved) != 0 {
		t.Fatalf("a first scan reported moved runs %v", scan.Moved)
	}
}

func TestClearRunsMarksClustersFree(t *testing.T) {
	bitmap := []byte{0xFF, 0xFF}
	got := replClearRuns(bitmap, 12, []replRun{{Start: 2, Count: 3}, {Start: 10, Count: 10}})
	if !reflect.DeepEqual(got, []byte{0xE3, 0xF3}) || bitmap[0] != 0xFF {
		t.Fatalf("bitmap %08b, original %08b", got, bitmap)
	}
}
