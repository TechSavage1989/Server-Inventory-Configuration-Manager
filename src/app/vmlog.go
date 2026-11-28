package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Logs on disk (2026-11-28, owner: "logs specific to each vm migration
// should be available and accessible, not just logs in the one screen under
// the discovery area"). Every line of the VMware log is kept in
// Logs\VMware.log and every line about one VM (its copy rounds, cutover,
// test, fix-up) also in Logs\VMs\<name>.log under LocalAppData, so they
// survive restarts. Each file is cut over to <name>.1.log at
// vmLogRotateBytes, keeping one older file.
const (
	vmLogRotateBytes = 5 << 20
	vmLogReadLimit   = 2 << 20
)

type vmLogFiles struct {
	mu   sync.Mutex
	root string
}

func newVMLogFiles(root string) *vmLogFiles { return &vmLogFiles{root: root} }

var vmLogUnsafe = regexp.MustCompile(`[^A-Za-z0-9 ._()-]+`)

// vmLogFileName turns a VM name into a file name: anything unusual becomes
// an underscore, so a name never reaches outside the folder.
func vmLogFileName(name string) string {
	name = strings.TrimSpace(vmLogUnsafe.ReplaceAllString(name, "_"))
	name = strings.Trim(name, ". ")
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	if name == "" || strings.Trim(name, "_") == "" {
		name = "VM"
	}
	return name
}

// Folder is where the per-VM logs are.
func (l *vmLogFiles) Folder() string { return filepath.Join(l.root, "VMs") }

// Path is one VM's log file.
func (l *vmLogFiles) Path(vm string) string {
	return filepath.Join(l.Folder(), vmLogFileName(vm)+".log")
}

// SharedPath is the whole VMware log.
func (l *vmLogFiles) SharedPath() string { return filepath.Join(l.root, "VMware.log") }

// vmLogStamp puts the time in front of a line that has none.
func vmLogStamp(line string, now time.Time) string {
	line = strings.TrimRight(line, "\r\n")
	if len(line) >= 19 && line[4] == '-' && line[7] == '-' && line[10] == ' ' && line[13] == ':' {
		return line
	}
	return now.Format("2006-01-02 15:04:05") + " " + line
}

func (l *vmLogFiles) append(path, line string) {
	if l == nil || l.root == "" || strings.TrimSpace(line) == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= vmLogRotateBytes {
		older := strings.TrimSuffix(path, ".log") + ".1.log"
		_ = os.Remove(older)
		_ = os.Rename(path, older)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(vmLogStamp(line, time.Now()) + "\r\n")
	_ = file.Close()
}

// Write adds a line to one VM's log.
func (l *vmLogFiles) Write(vm, line string) {
	if l == nil || strings.TrimSpace(vm) == "" {
		return
	}
	l.append(l.Path(vm), line)
}

// Shared adds a line to the whole VMware log.
func (l *vmLogFiles) Shared(line string) {
	if l == nil {
		return
	}
	l.append(l.SharedPath(), line)
}

// Read returns the end of a log (the older file first when the current one
// is short), at most vmLogReadLimit bytes.
func (l *vmLogFiles) Read(path string) (string, error) {
	if l == nil {
		return "", errors.New("logs are not kept yet")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tail := func(file string, limit int64) []byte {
		handle, err := os.Open(file)
		if err != nil {
			return nil
		}
		defer handle.Close()
		info, err := handle.Stat()
		if err != nil {
			return nil
		}
		start := info.Size() - limit
		if start < 0 {
			start = 0
		}
		if _, err := handle.Seek(start, io.SeekStart); err != nil {
			return nil
		}
		data, _ := io.ReadAll(io.LimitReader(handle, limit))
		if start > 0 {
			if at := strings.IndexByte(string(data), '\n'); at >= 0 {
				data = data[at+1:]
			}
		}
		return data
	}
	current := tail(path, vmLogReadLimit)
	var data []byte
	if left := int64(vmLogReadLimit - len(current)); left > 64<<10 {
		data = append(data, tail(strings.TrimSuffix(path, ".log")+".1.log", left)...)
	}
	data = append(data, current...)
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}
