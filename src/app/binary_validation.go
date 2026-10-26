//go:build windows

package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"unicode/utf16"
)

func validateDownloadedExecutable(path, expectedVersion string) error {
	if _, err := parseVersion(expectedVersion); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 1024 || info.Size() > maximumUpdateBytes {
		return errors.New("invalid update executable size/type")
	}
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("invalid Windows PE application: %w", err)
	}
	defer file.Close()
	if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || file.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return errors.New("update is not an AMD64 executable application")
	}
	optional, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || optional.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI || optional.AddressOfEntryPoint == 0 || len(file.Sections) == 0 {
		return errors.New("update is not a Windows GUI PE32+ application")
	}
	entryValid := false
	markerFound := false
	marker := []byte(binaryIdentityPrefix + expectedVersion + "|")
	for _, section := range file.Sections {
		if uint64(section.Offset)+uint64(section.Size) > uint64(info.Size()) {
			return errors.New("truncated PE section")
		}
		if optional.AddressOfEntryPoint >= section.VirtualAddress && uint64(optional.AddressOfEntryPoint) < uint64(section.VirtualAddress)+uint64(section.VirtualSize) && section.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE != 0 {
			entryValid = true
		}
		// An appended marker cannot turn an unrelated executable into this application.
		if section.Name == ".rdata" {
			data, readErr := section.Data()
			if readErr != nil {
				return readErr
			}
			markerFound = bytes.Contains(data, marker)
		}
	}
	if !entryValid || !markerFound {
		return errors.New("unexpected application identity/version or invalid PE entry point")
	}
	return validateVersionResource(file, expectedVersion)
}

func utf16Bytes(value string) []byte {
	units := utf16.Encode([]rune(value + "\x00"))
	result := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(result[i*2:], u)
	}
	return result
}

func validateVersionResource(file *pe.File, expectedVersion string) error {
	section := file.Section(".rsrc")
	if section == nil {
		return errors.New("missing Windows application metadata")
	}
	data, err := section.Data()
	if err != nil {
		return err
	}
	offset := uint32(0)
	// Find resource type VERSION (16), identity 1, then its language leaf.
	for level := 0; level < 3; level++ {
		if uint64(offset)+16 > uint64(len(data)) {
			return errors.New("invalid PE resource directory")
		}
		named := binary.LittleEndian.Uint16(data[offset+12:])
		ids := binary.LittleEndian.Uint16(data[offset+14:])
		count := uint32(named) + uint32(ids)
		if count == 0 || uint64(offset)+16+uint64(count)*8 > uint64(len(data)) {
			return errors.New("invalid PE resource entry count")
		}
		found := false
		for i := uint32(0); i < count; i++ {
			entry := offset + 16 + i*8
			id := binary.LittleEndian.Uint32(data[entry:])
			child := binary.LittleEndian.Uint32(data[entry+4:])
			if (level == 0 && id != 16) || (level == 1 && id != 1) {
				continue
			}
			if level < 2 && child&0x80000000 == 0 {
				return errors.New("invalid version metadata directory")
			}
			if level == 2 && child&0x80000000 != 0 {
				return errors.New("invalid version metadata leaf")
			}
			offset = child & 0x7fffffff
			found = true
			break
		}
		if !found {
			return errors.New("missing version metadata resource")
		}
	}
	if uint64(offset)+16 > uint64(len(data)) {
		return errors.New("invalid version metadata descriptor")
	}
	rva := binary.LittleEndian.Uint32(data[offset:])
	size := binary.LittleEndian.Uint32(data[offset+4:])
	if rva < section.VirtualAddress {
		return errors.New("invalid version metadata address")
	}
	start := rva - section.VirtualAddress
	if uint64(start)+uint64(size) > uint64(len(data)) || size < 92 {
		return errors.New("truncated Windows version metadata")
	}
	blob := data[start : start+size]
	if int(binary.LittleEndian.Uint16(blob)) != len(blob) || !bytes.Equal(blob[6:6+len(utf16Bytes("VS_VERSION_INFO"))], utf16Bytes("VS_VERSION_INFO")) {
		return errors.New("invalid Windows version metadata")
	}
	fixed := 40 // 6-byte block header, UTF-16 key and DWORD alignment
	if binary.LittleEndian.Uint32(blob[fixed:]) != 0xfeef04bd {
		return errors.New("invalid fixed version metadata")
	}
	version, _ := parseVersion(expectedVersion)
	ms := uint32(version[0])<<16 | uint32(version[1])
	ls := uint32(version[2]) << 16
	if binary.LittleEndian.Uint32(blob[fixed+8:]) != ms || binary.LittleEndian.Uint32(blob[fixed+12:]) != ls || binary.LittleEndian.Uint32(blob[fixed+16:]) != ms || binary.LittleEndian.Uint32(blob[fixed+20:]) != ls {
		return errors.New("Windows file/product version does not match the manifest")
	}
	for key, value := range map[string]string{"ProductName": "Server Inventory Configuration Manager", "OriginalFilename": stableExecutableName, "FileVersion": expectedVersion, "ProductVersion": expectedVersion} {
		needle := append(utf16Bytes(key), 0, 0)
		// String value begins on a DWORD boundary after its key.
		keyBytes := utf16Bytes(key)
		at := bytes.Index(blob, keyBytes)
		if at < 0 {
			return fmt.Errorf("missing Windows metadata %s", key)
		}
		valueStart := (at + len(keyBytes) + 3) &^ 3
		needle = utf16Bytes(value)
		if valueStart+len(needle) > len(blob) || !bytes.Equal(blob[valueStart:valueStart+len(needle)], needle) {
			return fmt.Errorf("Windows metadata %s does not match", key)
		}
	}
	return nil
}
