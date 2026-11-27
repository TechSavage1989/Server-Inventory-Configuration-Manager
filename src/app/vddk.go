package main

// VDDK reads (2026-11-27, owner: "id like to explore the vddk library").
// A running VM's base disk is locked by ESXi, so a round's snapshot is read
// either through vSphere's export (which streams the whole disk) or, when the
// operator has pointed the application at a VMware Virtual Disk Development
// Kit (VDDK) folder, through VixDiskLib over NBDSSL, which reads only the
// sectors the copy engine asks for: a changed-block round reads only the
// blocks changed block tracking reports.
//
// VDDK is not shipped: the license in the VDDK download (VMware General
// Terms, 2023-11-22, section 1.3) allows internal use only and no
// sublicensing, so each operator downloads it from Broadcom and chooses its
// folder in Help > Settings. Its DLLs are loaded only after every DLL in the
// folder passes Windows' Authenticode check with a VMware or Broadcom
// signer (vddk_windows.go). Only the NBDSSL transport is used, read only:
// no driver (vstor2) is installed, no VDDK program is started, nothing runs
// in the guest, and VixDiskLib_PrepareForAccess is never called because it
// changes the VM's allowed operations in vCenter.
//
// The order a round tries for each disk is: VDDK (when a folder is set and
// it loads and connects), the datastore /folder read, then the export.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const vddkSectorSize = 512

// vddkConnect is what a round needs to open its snapshot through VDDK.
// Password is held only in memory for the round.
type vddkConnect struct {
	Server     string // vCenter or ESXi host name, as the session used it
	Port       int    // 0 for 443
	User       string
	Password   string
	Thumbprint string // SHA-1 of the server's certificate, AA:BB:... form
	VMRef      string // vm-123
	Snapshot   string // snapshot-45
}

// vddkSession is one VixDiskLib connection to a snapshot of one VM.
type vddkSession interface {
	Open(path string) (vddkDisk, error)
	Close() error
}

// vddkDisk is one disk opened read only.
type vddkDisk interface {
	Capacity() int64 // bytes
	Transport() string
	ReadSectors(start, count uint64, buffer []byte) error
	Close() error
}

// vddkOpenSession is replaced by tests; the Windows build loads VixDiskLib
// from the chosen folder.
var vddkOpenSession = func(ctx context.Context, folder string, params vddkConnect) (vddkSession, error) {
	return vddkPlatformOpen(ctx, folder, params)
}

// vddkSource is a vmCopySource that reads a VDDK disk at any byte offset.
type vddkSource struct {
	name string
	disk vddkDisk
	size int64
	mu   sync.Mutex
}

func newVDDKSource(name string, disk vddkDisk) *vddkSource {
	return &vddkSource{name: name, disk: disk, size: disk.Capacity()}
}

func (s *vddkSource) Size() int64      { return s.size }
func (s *vddkSource) Describe() string { return s.name }
func (s *vddkSource) Close() error     { return s.disk.Close() }

// vddkMaxRead is the most read in one VixDiskLib_Read call.
const vddkMaxRead = 4 << 20

func (s *vddkSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= s.size {
		return 0, io.EOF
	}
	want := len(p)
	short := false
	if int64(want) > s.size-off {
		want, short = int(s.size-off), true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	done := 0
	for done < want {
		pos := off + int64(done)
		first := pos / vddkSectorSize
		skip := int(pos % vddkSectorSize)
		n := want - done
		if n > vddkMaxRead {
			n = vddkMaxRead
		}
		if skip == 0 && n%vddkSectorSize == 0 {
			if err := s.disk.ReadSectors(uint64(first), uint64(n/vddkSectorSize), p[done:done+n]); err != nil {
				return done, err
			}
			done += n
			continue
		}
		// An unaligned edge: read whole sectors into a buffer.
		sectors := (int64(skip) + int64(n) + vddkSectorSize - 1) / vddkSectorSize
		if sectors*vddkSectorSize > vddkMaxRead {
			sectors = vddkMaxRead / vddkSectorSize
		}
		buffer := make([]byte, sectors*vddkSectorSize)
		if err := s.disk.ReadSectors(uint64(first), uint64(sectors), buffer); err != nil {
			return done, err
		}
		copied := copy(p[done:want], buffer[skip:])
		done += copied
	}
	if short {
		return done, io.EOF
	}
	return done, nil
}

// vddkSettings is Settings\vddk.json: only the folder, nothing secret.
type vddkSettings struct {
	Schema int    `json:"schema"`
	Folder string `json:"folder"`
}

func vddkSettingsPath(settingsDir string) string { return filepath.Join(settingsDir, "vddk.json") }

// cleanVDDKFolder accepts an empty folder (VDDK off) or a full drive or UNC
// path without "..".
func cleanVDDKFolder(folder string) (string, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "", nil
	}
	if strings.Contains(folder, "..") {
		return "", errors.New("Choose the VDDK folder by its full path, without \"..\".")
	}
	if !(len(folder) >= 3 && folder[1] == ':' && (folder[2] == '\\' || folder[2] == '/')) && !strings.HasPrefix(folder, `\\`) {
		return "", errors.New("Choose the VDDK folder by its full path, for example C:\\VDDK\\VMware-vix-disklib-8.0.3.")
	}
	return strings.TrimRight(filepath.Clean(folder), `\/`), nil
}

func loadVDDKSettings(settingsDir string) vddkSettings {
	data, err := os.ReadFile(vddkSettingsPath(settingsDir))
	if err != nil || len(data) > 16*1024 {
		return vddkSettings{Schema: 1}
	}
	var saved vddkSettings
	if json.Unmarshal(data, &saved) != nil || saved.Schema != 1 {
		return vddkSettings{Schema: 1}
	}
	if folder, err := cleanVDDKFolder(saved.Folder); err == nil {
		saved.Folder = folder
	} else {
		saved.Folder = ""
	}
	return saved
}

func saveVDDKSettings(settingsDir string, settings vddkSettings) error {
	settings.Schema = 1
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(settingsDir, "vddk-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, vddkSettingsPath(settingsDir))
}

// vddkBinFolder finds the folder holding vixDiskLib.dll: the chosen folder
// itself or its bin subfolder (the layout of the VDDK download).
func vddkBinFolder(folder string) (string, error) {
	for _, candidate := range []string{filepath.Join(folder, "bin"), folder} {
		if info, err := os.Stat(filepath.Join(candidate, "vixDiskLib.dll")); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s has no vixDiskLib.dll (in the folder or its bin folder). Choose the folder you extracted the Windows VDDK download into", folder)
}

// vddkFolderState holds the folder the rounds use; the application sets it
// at start and when the operator saves a new one.
var vddkFolderState struct {
	sync.Mutex
	folder string
	// loaded is the folder VixDiskLib was loaded from in this process; a
	// different folder needs a restart because a DLL cannot be swapped.
	loaded string
}

func setVDDKFolder(folder string) {
	vddkFolderState.Lock()
	vddkFolderState.folder = folder
	vddkFolderState.Unlock()
}

func currentVDDKFolder() string {
	vddkFolderState.Lock()
	defer vddkFolderState.Unlock()
	return vddkFolderState.folder
}

// vddkStatus is what Help > Settings shows about the chosen folder.
type vddkStatus struct {
	Folder   string   `json:"folder"`
	Ready    bool     `json:"ready"`
	Version  string   `json:"version,omitempty"`
	Signer   string   `json:"signer,omitempty"`
	Loaded   bool     `json:"loaded"`
	Restart  bool     `json:"restart,omitempty"`
	Problems []string `json:"problems,omitempty"`
	Message  string   `json:"message"`
}

// vddkSupportedVersion accepts VDDK 7.0.3 and later (7, 8 and 9).
func vddkSupportedVersion(major, minor, patch uint16) bool {
	switch {
	case major >= 8:
		return true
	case major == 7:
		return minor > 0 || patch >= 3
	}
	return false
}

// vddkErrorText turns a VixError code into a sentence when VixDiskLib gives
// none.
func vddkErrorText(code uint64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "VixDiskLib error"
	}
	return fmt.Errorf("%s (VDDK error %d)", text, code&0xFFFF)
}

// vddkConnectFor builds a round's VDDK connection from the session's
// options. VixDiskLib checks the server by its SHA-1 thumbprint, so the
// certificate is read here through the same trust the session used
// (Windows, or the SHA-256 thumbprint the operator confirmed).
func vddkConnectFor(ctx context.Context, options vmConnectOptions) (*vddkConnect, error) {
	u, err := vmServerURL(options.Server)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	port := 443
	if p := u.Port(); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	var certificateErr *vmCertificateError
	config := vmTLSConfig(host, vmNormalizeThumbprint(options.Thumbprint), &certificateErr)
	dialer := &tls.Dialer{Config: config}
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		if certificateErr != nil {
			return nil, certificateErr
		}
		return nil, fmt.Errorf("could not read %s's certificate: %v", host, err)
	}
	defer conn.Close()
	certificates := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certificates) == 0 {
		return nil, fmt.Errorf("%s sent no certificate", host)
	}
	return &vddkConnect{Server: host, Port: port, User: options.User, Password: options.Password, Thumbprint: vmThumbprintSHA1(certificates[0])}, nil
}
