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
