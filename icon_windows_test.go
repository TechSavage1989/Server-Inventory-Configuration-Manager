//go:build windows

package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

func TestIconEndpointsServeApprovedVectorAndRasterAssets(t *testing.T) {
	a := &application{}
	for _, tc := range []struct{ route, source, mime string }{
		{"/app-icon.svg", "assets/icons/server-inventory.svg", "image/svg+xml"},
		{"/favicon.ico", "favicon.ico", "image/x-icon"},
		{"/app-icon.png", "app-icon.png", "image/png"},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", tc.route, nil)
		switch tc.route {
		case "/app-icon.svg":
			a.handleAppIconSVG(response, request)
		case "/favicon.ico":
			a.handleFavicon(response, request)
		default:
			a.handleAppIcon(response, request)
		}
		expected, err := os.ReadFile(tc.source)
		if err != nil || response.Code != 200 || response.Header().Get("Content-Type") != tc.mime || !bytes.Equal(response.Body.Bytes(), expected) {
			t.Fatal("wrong embedded icon response", tc.route, err)
		}
	}
	old, err := os.ReadFile("baseline/v3.0.10/icons/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	active, _ := os.ReadFile("favicon.ico")
	if bytes.Equal(old, active) {
		t.Fatal("active icon still uses the historical icon")
	}
}

func TestCompiledIconResourcesAndNativeShellExtraction(t *testing.T) {
	file, err := pe.NewFile(bytes.NewReader(fixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	section := file.Section(".rsrc")
	if section == nil {
		t.Fatal("no icon resource section")
	}
	data, err := section.Data()
	if err != nil {
		t.Fatal(err)
	}
	resource := func(kind, name uint32) []byte {
		t.Helper()
		var offset uint32
		for level, wanted := range []uint32{kind, name, 1033} {
			if uint64(offset)+16 > uint64(len(data)) {
				t.Fatal("invalid resource directory")
			}
			count := uint32(binary.LittleEndian.Uint16(data[offset+12:])) + uint32(binary.LittleEndian.Uint16(data[offset+14:]))
			found := false
			for i := uint32(0); i < count; i++ {
				entry := offset + 16 + i*8
				if uint64(entry)+8 > uint64(len(data)) {
					t.Fatal("truncated resource entry")
				}
				id, child := binary.LittleEndian.Uint32(data[entry:]), binary.LittleEndian.Uint32(data[entry+4:])
				if id != wanted {
					continue
				}
				if level < 2 && child&0x80000000 == 0 {
					t.Fatal("invalid icon directory")
				}
				offset = child & 0x7fffffff
				found = true
				break
			}
			if !found {
				t.Fatal("missing icon resource", kind, name, wanted)
			}
		}
		if uint64(offset)+16 > uint64(len(data)) {
			t.Fatal("invalid resource leaf")
		}
		rva, size := binary.LittleEndian.Uint32(data[offset:]), binary.LittleEndian.Uint32(data[offset+4:])
		if rva < section.VirtualAddress || uint64(rva-section.VirtualAddress)+uint64(size) > uint64(len(data)) {
			t.Fatal("invalid icon resource range")
		}
		return data[rva-section.VirtualAddress : rva-section.VirtualAddress+size]
	}
	ico, err := os.ReadFile("server_inventory.ico")
	if err != nil {
		t.Fatal(err)
	}
	count := int(binary.LittleEndian.Uint16(ico[4:]))
	group := resource(14, 1)
	if len(group) != 6+14*count || int(binary.LittleEndian.Uint16(group[4:])) != count {
		t.Fatal("wrong compiled icon group")
	}
	for i := 0; i < count; i++ {
		entry := ico[6+i*16 : 6+(i+1)*16]
		g := group[6+i*14 : 6+(i+1)*14]
		if !bytes.Equal(entry[:12], g[:12]) {
			t.Fatal("compiled icon dimensions differ", i)
		}
		size, offset := binary.LittleEndian.Uint32(entry[8:]), binary.LittleEndian.Uint32(entry[12:])
		id := binary.LittleEndian.Uint16(g[12:])
		if !bytes.Equal(resource(3, uint32(id)), ico[offset:offset+size]) {
			t.Fatal("compiled icon pixels differ from SVG-derived asset", i)
		}
	}
	// Exercise Windows' own icon extraction used by Explorer and shortcuts.
	exe, _ := filepath.Abs(filepath.Join("build", stableExecutableName))
	var large, small uintptr
	n, _, _ := shell32.NewProc("ExtractIconExW").Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(exe))), 0, uintptr(unsafe.Pointer(&large)), uintptr(unsafe.Pointer(&small)), 1)
	if large != 0 {
		defer user32.NewProc("DestroyIcon").Call(large)
	}
	if small != 0 {
		defer user32.NewProc("DestroyIcon").Call(small)
	}
	if n != 1 || large == 0 || small == 0 {
		t.Fatal("Windows could not extract both desktop and small executable icons")
	}
	t.Log("All ten SVG-derived icon frames match the compiled executable; native large/small shell extraction passed")
}
