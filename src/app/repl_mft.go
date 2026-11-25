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

// replMFTDataRuns returns the cluster runs of the unnamed $DATA attribute in
// one MFT record. It applies the update sequence fixups first, and refuses
// a record that keeps its attributes elsewhere (an attribute list).
func replMFTDataRuns(record []byte) ([]replRun, error) {
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
	if binary.LittleEndian.Uint16(fixed[22:])&1 == 0 {
		return nil, errors.New("the MFT record is not in use")
	}
	runs := []replRun{}
	found := false
	at := int(binary.LittleEndian.Uint16(fixed[20:]))
	for at+16 <= len(fixed) {
		kind := binary.LittleEndian.Uint32(fixed[at:])
		if kind == 0xFFFFFFFF {
			break
		}
		length := int(binary.LittleEndian.Uint32(fixed[at+4:]))
		if length < 16 || at+length > len(fixed) {
			return nil, errors.New("an attribute runs past the end of the MFT record")
		}
		attribute := fixed[at : at+length]
		switch {
		case kind == 0x20:
			return nil, errors.New("the file keeps its attributes in other MFT records")
		case kind == 0x80 && attribute[9] == 0: // unnamed $DATA
			found = true
			if attribute[8] == 0 {
				break // resident: inside the MFT, which is tracked itself
			}
			if length < 64 {
				return nil, errors.New("a $DATA attribute is too short")
			}
			pairs := int(binary.LittleEndian.Uint16(attribute[32:]))
			lcn := int64(0)
			for p := pairs; p < length && attribute[p] != 0; {
				header := attribute[p]
				lengthSize, offsetSize := int(header&0x0F), int(header>>4)
				if lengthSize == 0 || lengthSize > 8 || offsetSize > 8 || p+1+lengthSize+offsetSize > length {
					return nil, errors.New("a $DATA run list is damaged")
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
						return nil, errors.New("a $DATA run points outside the volume")
					}
					runs = append(runs, replRun{Start: lcn, Count: count})
				}
				p += 1 + lengthSize + offsetSize
			}
		}
		at += length
	}
	if !found {
		return nil, fmt.Errorf("the MFT record has no $DATA attribute")
	}
	return runs, nil
}
