package main

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestConsoleDecodeTextReadsWhatWindowsWrites(t *testing.T) {
	utf16le := []byte{0xFF, 0xFE, 'P', 0, '@', 0, 's', 0, 's', 0, 0xAC, 0x20, '\r', 0, '\n', 0}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"utf8", []byte("admin\r\nS3cret!\r\n"), "admin\nS3cret!"},
		{"utf8 bom", append([]byte{0xEF, 0xBB, 0xBF}, []byte("caf\u00e9")...), "caf\u00e9"},
		{"utf16 le", utf16le, "P@ss\u20ac"},
		{"utf16 be", []byte{0xFE, 0xFF, 0, 'O', 0, 'K'}, "OK"},
		{"latin1", []byte{'c', 'a', 'f', 0xE9}, "caf\u00e9"},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		got, err := consoleDecodeText(c.data)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := consoleDecodeText([]byte{'M', 'Z', 0, 0, 3}); err == nil {
		t.Fatal("a binary file was read as text")
	}
}

func TestConsoleCheckOCRImageAcceptsOnlySensiblePNGs(t *testing.T) {
	picture := func(w, h int) []byte {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	if err := consoleCheckOCRImage(picture(640, 120)); err != nil {
		t.Fatalf("a normal picture was refused: %v", err)
	}
	for name, data := range map[string][]byte{
		"empty":    nil,
		"not png":  []byte("GIF89a not a png at all"),
		"tiny":     picture(4, 4),
		"too wide": picture(consoleOCRMaxSide+1, 10),
	} {
		if err := consoleCheckOCRImage(data); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := consoleCheckGuestTextPath(""); err == nil {
		t.Error("an empty path was accepted")
	}
	if err := consoleCheckGuestTextPath("C:\\a\nb"); err == nil {
		t.Error("a path with a line break was accepted")
	}
	if name := consoleTempName(".png"); !strings.HasPrefix(name, "console-") || !strings.HasSuffix(name, ".png") || len(name) != len("console-")+24+4 {
		t.Errorf("unexpected temporary name %q", name)
	}
}
