package main

// VM console (rule 68): getting text out of a VM without anything installed
// in it. Two ways work on top of what a platform's console passes on:
//
//   - Text from a file in the VM: the operator saves the text in the VM (for
//     example Get-Clipboard into a file, which the window can type for them)
//     and the console copies that one file out through the platform's own
//     file transfer (VMware Tools, the QEMU guest agent, PowerShell Direct),
//     reads it here as text and deletes its local copy at once.
//   - Text from the screen: the console crops the part of the screen the
//     operator chose and Windows' own text recognition reads it on this
//     computer (Read-ScreenText.ps1).
//
// Neither keeps the text: it goes back to the page, which shows it and puts
// it on this computer's clipboard. Nothing is logged.

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image/png"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// consoleTextLimit is the largest text file read out of a VM.
	consoleTextLimit = 1 << 20
	// consoleOCRLimit is the largest picture the page may send.
	consoleOCRLimit = 12 << 20
	// consoleOCRMaxSide is Windows text recognition's usual limit
	// (OcrEngine.MaxImageDimension); the script checks the real one.
	consoleOCRMaxSide = 10000
)

// consoleDecodeText turns a text file's bytes into a string: UTF-8 with or
// without a byte order mark, UTF-16 with one (what Windows PowerShell 5.1's
// Out-File and Set-Content -Encoding Unicode write), else Windows-1252 bytes
// as Latin-1. A file that holds NUL bytes without a UTF-16 mark is refused as
// not text.
func consoleDecodeText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		order := binary.ByteOrder(binary.LittleEndian)
		if data[0] == 0xFE {
			order = binary.BigEndian
		}
		data = data[2:]
		if len(data)%2 == 1 {
			data = data[:len(data)-1]
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = order.Uint16(data[2*i:])
		}
		return consoleNormalizeText(string(utf16.Decode(units))), nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", errors.New("the file in the VM is not a text file")
	}
	if utf8.Valid(data) {
		return consoleNormalizeText(string(data)), nil
	}
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return consoleNormalizeText(string(runes)), nil
}

// consoleNormalizeText uses line feeds only and drops the one line break a
// file usually ends with.
func consoleNormalizeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSuffix(text, "\n")
}

// consoleCheckOCRImage accepts only a PNG of a sensible size.
func consoleCheckOCRImage(data []byte) error {
	if len(data) == 0 || len(data) > consoleOCRLimit {
		return errors.New("the picture of the screen is empty or too large")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errors.New("the picture of the screen is not a PNG")
	}
	if config.Width < 8 || config.Height < 8 || config.Width > consoleOCRMaxSide || config.Height > consoleOCRMaxSide {
		return errors.New("choose a larger area of the screen")
	}
	return nil
}

// consoleTempName is a random file name for one text copy or picture.
func consoleTempName(ext string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "console-" + hex.EncodeToString(b[:]) + ext
}

// consoleCheckGuestTextPath refuses paths that cannot be a file in the VM.
func consoleCheckGuestTextPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("type the path of the text file in the VM")
	}
	if len(path) > 1024 || strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("the path in the VM is not valid")
	}
	return nil
}
