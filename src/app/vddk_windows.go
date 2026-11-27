//go:build windows

package main

// VixDiskLib on Windows, loaded without cgo from the folder the operator
// chose (vddk.go explains why it is not shipped). Every DLL in that folder
// must pass WinVerifyTrust with a VMware or Broadcom signer before any is
// loaded. Structures follow include/vixDiskLib.h of VDDK 8.0.3, which packs
// them to 4 bytes on Windows; VixDiskLib_AllocateConnectParams (VDDK 6.7
// and later) allocates the connection parameters so their size is VDDK's.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	vddkCredUID          = 1
	vddkFlagOpenReadOnly = 1 << 2
	vddkTransport        = "nbdssl"

	// Offsets in VixDiskLibConnectParams (pack 4, 64-bit pointers).
	vddkParamVMXSpec    = 0
	vddkParamServerName = 8
	vddkParamThumbPrint = 16
	vddkParamCredType   = 28
	vddkParamUserName   = 32
	vddkParamPassword   = 40
	vddkParamPort       = 56
	vddkParamSpecType   = 112
	vddkParamSize       = 116

	// VixDiskLibInfo.capacity (sectors) follows two 12-byte geometries.
	vddkInfoCapacity = 24
)

// vddkSigners are the organisations whose signature the VDDK DLLs must carry
// (Broadcom from VDDK 8.0.3, VMware before).
var vddkSigners = []string{"Broadcom Inc", "Broadcom Inc.", "VMware, Inc.", "VMware, Inc"}

type vddkLibrary struct {
	folder string
	procs  map[string]*windows.Proc
	// mu serialises connect, open, close and disconnect, which VixDiskLib
	// documents as not thread safe; reads of different disks may overlap.
	mu sync.Mutex
}

var vddkLoaded struct {
	sync.Mutex
	lib *vddkLibrary
	err error
}

var vddkProcNames = []string{
	"VixDiskLib_InitEx", "VixDiskLib_AllocateConnectParams", "VixDiskLib_FreeConnectParams",
	"VixDiskLib_ConnectEx", "VixDiskLib_Disconnect", "VixDiskLib_Open", "VixDiskLib_Close",
	"VixDiskLib_GetInfo", "VixDiskLib_FreeInfo", "VixDiskLib_Read", "VixDiskLib_GetTransportMode",
	"VixDiskLib_GetErrorText", "VixDiskLib_FreeErrorText",
}

// vddkCheck reads the folder without loading anything: the DLL is there,
// every DLL is signed by VMware or Broadcom and the version is 7.0.3 or
// later.
func vddkCheck(folder string) (vddkStatus, string, error) {
	status := vddkStatus{Folder: folder}
	bin, err := vddkBinFolder(folder)
	if err != nil {
		return status, "", err
	}
	entries, err := os.ReadDir(bin)
	if err != nil {
		return status, "", err
	}
	var dlls []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".dll") {
			dlls = append(dlls, entry.Name())
		}
	}
	sort.Strings(dlls)
	for _, name := range dlls {
		signer, err := vddkFileSigner(filepath.Join(bin, name))
		if err != nil {
			status.Problems = append(status.Problems, name+": "+err.Error())
			continue
		}
		if !vddkTrustedSigner(signer) {
			status.Problems = append(status.Problems, name+" is signed by "+signer+", not VMware or Broadcom")
			continue
		}
		if strings.EqualFold(name, "vixDiskLib.dll") {
			status.Signer = signer
		}
	}
	major, minor, patch, err := vddkLibraryVersion(filepath.Join(bin, "vixDiskLib.dll"))
	if err != nil {
		status.Problems = append(status.Problems, "vixDiskLib.dll has no version: "+err.Error())
	} else {
		status.Version = fmt.Sprintf("%d.%d.%d", major, minor, patch)
		if !vddkSupportedVersion(major, minor, patch) {
			status.Problems = append(status.Problems, "VDDK "+status.Version+" is older than 7.0.3; download VDDK 7.0.3, 8 or 9")
		}
	}
	if len(status.Problems) > 0 {
		return status, bin, errors.New(strings.Join(status.Problems, "; "))
	}
	status.Ready = true
	return status, bin, nil
}

func vddkTrustedSigner(signer string) bool {
	for _, name := range vddkSigners {
		if strings.EqualFold(strings.TrimSpace(signer), name) {
			return true
		}
	}
	return false
}

// vddkFileSigner verifies a DLL's embedded Authenticode signature from its
// own bytes (authenticode.go: file hash, signed attributes, signature and a
// code-signing chain to this computer's trusted roots) and returns the
// signer's organisation. Windows' WinVerifyTrust is asked only when that
// check fails, so a signature Windows accepts in another form still counts.
// 3.2.38 asked WinVerifyTrust alone, and on the owner's Hyper-V host it
// answered TRUST_E_SUBJECT_FORM_UNKNOWN for every genuine Broadcom DLL.
func vddkFileSigner(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	certificate, ownErr := authenticodeVerify(data, nil, time.Now())
	if ownErr == nil {
		return authenticodeOrganisation(certificate), nil
	}
	signer, windowsErr := vddkVerifySignature(path)
	if windowsErr == nil {
		return signer, nil
	}
	return "", fmt.Errorf("%v; Windows: %v", ownErr, windowsErr)
}

// vddkLibraryVersion reads the file version from the DLL's own version
// resource, then asks Windows.
func vddkLibraryVersion(path string) (major, minor, patch uint16, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, err
	}
	major, minor, patch, _, ownErr := peVersion(data)
	if ownErr == nil {
		return major, minor, patch, nil
	}
	major, minor, patch, _, windowsErr := vddkFileVersion(path)
	if windowsErr == nil {
		return major, minor, patch, nil
	}
	return 0, 0, 0, fmt.Errorf("%v; Windows: %v", ownErr, windowsErr)
}

// vddkVerifySignature checks a file's Authenticode signature with Windows
// (no revocation download) and returns the signer's organisation.
func vddkVerifySignature(path string) (string, error) {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	file := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: path16}
	data := &windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(file),
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		ProvFlags:                       windows.WTD_REVOCATION_CHECK_NONE,
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	signer := ""
	if verifyErr == nil {
		signer = vddkSignerName(data.StateData)
	}
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	if verifyErr != nil {
		return "", fmt.Errorf("its signature is not valid (%v)", verifyErr)
	}
	if signer == "" {
		return "", errors.New("its signer could not be read")
	}
	return signer, nil
}

var (
	wintrustDLL                   = windows.NewLazySystemDLL("wintrust.dll")
	procWTHelperProvDataFromState = wintrustDLL.NewProc("WTHelperProvDataFromStateData")
	procWTHelperGetProvSigner     = wintrustDLL.NewProc("WTHelperGetProvSignerFromChain")
)

// vddkSignerName reads the organisation (O=) of the leaf certificate of the
// first signer of a verified file.
func vddkSignerName(state windows.Handle) string {
	provider, _, _ := procWTHelperProvDataFromState.Call(uintptr(state))
	if provider == 0 {
		return ""
	}
	signer, _, _ := procWTHelperGetProvSigner.Call(provider, 0, 0, 0)
	if signer == 0 {
		return ""
	}
	// CRYPT_PROVIDER_SGNR: cbStruct, FILETIME, csCertChain at 12,
	// pasCertChain at 16; CRYPT_PROVIDER_CERT: cbStruct, pCert at 8.
	count := *(*uint32)(vddkAddr(signer + 12))
	chain := *(*uintptr)(vddkAddr(signer + 16))
	if count == 0 || chain == 0 {
		return ""
	}
	cert := *(**windows.CertContext)(vddkAddr(chain + 8))
	if cert == nil {
		return ""
	}
	oid := []byte("2.5.4.10\x00")
	buffer := make([]uint16, 256)
	n := windows.CertGetNameString(cert, windows.CERT_NAME_ATTR_TYPE, 0, unsafe.Pointer(&oid[0]), &buffer[0], uint32(len(buffer)))
	if n <= 1 {
		return ""
	}
	return windows.UTF16ToString(buffer[:n])
}

func vddkFileVersion(path string) (major, minor, patch, build uint16, err error) {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	block := make([]byte, size)
	if err = windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&block[0])); err != nil {
		return 0, 0, 0, 0, err
	}
	var fixed *windows.VS_FIXEDFILEINFO
	var length uint32
	if err = windows.VerQueryValue(unsafe.Pointer(&block[0]), `\`, unsafe.Pointer(&fixed), &length); err != nil {
		return 0, 0, 0, 0, err
	}
	if fixed == nil || length < uint32(unsafe.Sizeof(*fixed)) {
		return 0, 0, 0, 0, errors.New("no fixed version")
	}
	return uint16(fixed.FileVersionMS >> 16), uint16(fixed.FileVersionMS), uint16(fixed.FileVersionLS >> 16), uint16(fixed.FileVersionLS), nil
}

// vddkLoad loads and initialises VixDiskLib once per process.
func vddkLoad(folder string) (*vddkLibrary, error) {
	vddkLoaded.Lock()
	defer vddkLoaded.Unlock()
	if vddkLoaded.lib != nil {
		if !strings.EqualFold(vddkLoaded.lib.folder, folder) {
			return nil, errors.New("VDDK was loaded from " + vddkLoaded.lib.folder + "; restart the application to use " + folder)
		}
		return vddkLoaded.lib, nil
	}
	if vddkLoaded.err != nil {
		return nil, vddkLoaded.err
	}
	_, bin, err := vddkCheck(folder)
	if err != nil {
		return nil, err
	}
	// Loading the DLL by its full path with the altered search path makes
	// Windows take its dependencies from the same folder.
	handle, err := windows.LoadLibraryEx(filepath.Join(bin, "vixDiskLib.dll"), 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	if err != nil {
		return nil, fmt.Errorf("could not load vixDiskLib.dll: %v", err)
	}
	dll := &windows.DLL{Name: "vixDiskLib.dll", Handle: handle}
	lib := &vddkLibrary{folder: folder, procs: map[string]*windows.Proc{}}
	for _, name := range vddkProcNames {
		proc, err := dll.FindProc(name)
		if err != nil {
			return nil, fmt.Errorf("vixDiskLib.dll has no %s; use VDDK 7.0.3 or later", name)
		}
		lib.procs[name] = proc
	}
	work := filepath.Join(os.Getenv("LOCALAPPDATA"), "Server Inventory Configuration Manager", "VMware", "VDDK")
	temp := filepath.Join(work, "Temp")
	if err := os.MkdirAll(temp, 0o700); err != nil {
		return nil, err
	}
	config := filepath.Join(work, "vixDiskLib.conf")
	// VixDiskLib writes its own log to tmpDirectory; it never contains the
	// password.
	text := "tmpDirectory=\"" + strings.ReplaceAll(temp, `\`, `\\`) + "\"\r\nvixDiskLib.transport.LogLevel=3\r\nvixDiskLib.nfc.LogLevel=1\r\n"
	if err := os.WriteFile(config, []byte(text), 0o600); err != nil {
		return nil, err
	}
	libDir := filepath.Dir(bin)
	if strings.EqualFold(bin, folder) {
		libDir = folder
	}
	libDir8, config8 := append([]byte(libDir), 0), append([]byte(config), 0)
	r, _, _ := lib.procs["VixDiskLib_InitEx"].Call(7, 0, 0, 0, 0, uintptr(unsafe.Pointer(&libDir8[0])), uintptr(unsafe.Pointer(&config8[0])))
	if r != 0 {
		vddkLoaded.err = lib.errorFor(uint64(r))
		return nil, vddkLoaded.err
	}
	vddkLoaded.lib = lib
	vddkFolderState.Lock()
	vddkFolderState.loaded = folder
	vddkFolderState.Unlock()
	return lib, nil
}

func (l *vddkLibrary) errorFor(code uint64) error {
	text := ""
	if ptr, _, _ := l.procs["VixDiskLib_GetErrorText"].Call(uintptr(code), 0); ptr != 0 {
		text = windows.BytePtrToString((*byte)(vddkAddr(ptr)))
		l.procs["VixDiskLib_FreeErrorText"].Call(ptr)
	}
	return vddkErrorText(code, text)
}

// cString copies s into memory Windows owns, so it stays put while
// VixDiskLib holds it; free it with freeCString.
func vddkCString(s string) (uintptr, error) {
	ptr, err := windows.LocalAlloc(windows.LMEM_FIXED|windows.LMEM_ZEROINIT, uint32(len(s)+1))
	if err != nil {
		return 0, err
	}
	copy(unsafe.Slice((*byte)(vddkAddr(ptr)), len(s)+1), s)
	return ptr, nil
}

func vddkFreeCString(ptr uintptr, length int) {
	if ptr == 0 {
		return
	}
	b := unsafe.Slice((*byte)(vddkAddr(ptr)), length)
	for i := range b {
		b[i] = 0
	}
	windows.LocalFree(windows.Handle(ptr))
}

type vddkCStr struct {
	ptr    uintptr
	length int
}

type vddkWindowsSession struct {
	lib     *vddkLibrary
	params  uintptr
	strings []vddkCStr
	conn    uintptr
}

func vddkPlatformOpen(ctx context.Context, folder string, params vddkConnect) (vddkSession, error) {
	lib, err := vddkLoad(folder)
	if err != nil {
		return nil, err
	}
	lib.mu.Lock()
	defer lib.mu.Unlock()
	raw, _, _ := lib.procs["VixDiskLib_AllocateConnectParams"].Call()
	if raw == 0 {
		return nil, errors.New("VixDiskLib could not allocate its connection parameters")
	}
	session := &vddkWindowsSession{lib: lib, params: raw}
	fail := func(err error) (vddkSession, error) {
		session.release()
		return nil, err
	}
	set := func(offset uintptr, value string) error {
		ptr, err := vddkCString(value)
		if err != nil {
			return err
		}
		session.strings = append(session.strings, vddkCStr{ptr, len(value)})
		*(*uintptr)(vddkAddr(raw + offset)) = ptr
		return nil
	}
	for _, field := range []struct {
		offset uintptr
		value  string
	}{
		{vddkParamVMXSpec, "moref=" + params.VMRef},
		{vddkParamServerName, params.Server},
		{vddkParamThumbPrint, params.Thumbprint},
		{vddkParamUserName, params.User},
		{vddkParamPassword, params.Password},
	} {
		if err := set(field.offset, field.value); err != nil {
			return fail(err)
		}
	}
	*(*uint32)(vddkAddr(raw + vddkParamCredType)) = vddkCredUID
	*(*uint32)(vddkAddr(raw + vddkParamPort)) = uint32(params.Port)
	*(*uint32)(vddkAddr(raw + vddkParamSpecType)) = 0 // VIXDISKLIB_SPEC_VMX
	snapshot := append([]byte(params.Snapshot), 0)
	transport := append([]byte(vddkTransport), 0)
	var conn uintptr
	r, _, _ := lib.procs["VixDiskLib_ConnectEx"].Call(raw, 1, uintptr(unsafe.Pointer(&snapshot[0])), uintptr(unsafe.Pointer(&transport[0])), uintptr(unsafe.Pointer(&conn)))
	if r != 0 {
		return fail(lib.errorFor(uint64(r)))
	}
	session.conn = conn
	return session, nil
}

// release frees the parameters (wiping the password); the caller holds mu.
func (s *vddkWindowsSession) release() {
	if s.params != 0 {
		// The strings are ours: clear the fields so VixDiskLib never frees
		// them.
		for _, offset := range []uintptr{vddkParamVMXSpec, vddkParamServerName, vddkParamThumbPrint, vddkParamUserName, vddkParamPassword} {
			*(*uintptr)(vddkAddr(s.params + offset)) = 0
		}
		s.lib.procs["VixDiskLib_FreeConnectParams"].Call(s.params)
		s.params = 0
	}
	for _, str := range s.strings {
		vddkFreeCString(str.ptr, str.length)
	}
	s.strings = nil
}

func (s *vddkWindowsSession) Open(path string) (vddkDisk, error) {
	s.lib.mu.Lock()
	defer s.lib.mu.Unlock()
	if s.conn == 0 {
		return nil, errors.New("the VDDK connection is closed")
	}
	path8 := append([]byte(path), 0)
	var handle uintptr
	r, _, _ := s.lib.procs["VixDiskLib_Open"].Call(s.conn, uintptr(unsafe.Pointer(&path8[0])), vddkFlagOpenReadOnly, uintptr(unsafe.Pointer(&handle)))
	if r != 0 {
		return nil, s.lib.errorFor(uint64(r))
	}
	disk := &vddkWindowsDisk{lib: s.lib, handle: handle}
	var info uintptr
	if r, _, _ := s.lib.procs["VixDiskLib_GetInfo"].Call(handle, uintptr(unsafe.Pointer(&info))); r != 0 || info == 0 {
		s.lib.procs["VixDiskLib_Close"].Call(handle)
		return nil, s.lib.errorFor(uint64(r))
	}
	disk.capacity = int64(*(*uint64)(vddkAddr(info + vddkInfoCapacity))) * vddkSectorSize
	s.lib.procs["VixDiskLib_FreeInfo"].Call(info)
	if mode, _, _ := s.lib.procs["VixDiskLib_GetTransportMode"].Call(handle); mode != 0 {
		disk.transport = windows.BytePtrToString((*byte)(vddkAddr(mode)))
	}
	if disk.transport != "" && !strings.HasPrefix(strings.ToLower(disk.transport), "nbd") {
		// Only the network transports were asked for; anything else is
		// unexpected, so refuse rather than read through it.
		disk.Close()
		return nil, fmt.Errorf("VDDK opened the disk with %s, not NBDSSL", disk.transport)
	}
	return disk, nil
}

func (s *vddkWindowsSession) Close() error {
	s.lib.mu.Lock()
	defer s.lib.mu.Unlock()
	var err error
	if s.conn != 0 {
		if r, _, _ := s.lib.procs["VixDiskLib_Disconnect"].Call(s.conn); r != 0 {
			err = s.lib.errorFor(uint64(r))
		}
		s.conn = 0
	}
	s.release()
	return err
}

type vddkWindowsDisk struct {
	lib       *vddkLibrary
	handle    uintptr
	capacity  int64
	transport string
	once      sync.Once
}

func (d *vddkWindowsDisk) Capacity() int64   { return d.capacity }
func (d *vddkWindowsDisk) Transport() string { return d.transport }

func (d *vddkWindowsDisk) ReadSectors(start, count uint64, buffer []byte) error {
	if uint64(len(buffer)) < count*vddkSectorSize || count == 0 {
		return errors.New("the VDDK read buffer is too small")
	}
	r, _, _ := d.lib.procs["VixDiskLib_Read"].Call(d.handle, uintptr(start), uintptr(count), uintptr(unsafe.Pointer(&buffer[0])))
	if r != 0 {
		return d.lib.errorFor(uint64(r))
	}
	return nil
}

func (d *vddkWindowsDisk) Close() error {
	var err error
	d.once.Do(func() {
		d.lib.mu.Lock()
		defer d.lib.mu.Unlock()
		if r, _, _ := d.lib.procs["VixDiskLib_Close"].Call(d.handle); r != 0 {
			err = d.lib.errorFor(uint64(r))
		}
	})
	return err
}

// vddkAddr turns an address VixDiskLib or Windows returned into a pointer.
// The memory is not Go's, so the garbage collector never moves or frees it.
func vddkAddr(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

// handleVDDK shows (GET) or saves (POST {"folder": ...}) the VDDK folder.
// A folder is saved only when it passes vddkCheck; an empty folder turns
// VDDK reads off.
func (a *application) handleVDDK(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	folder := currentVDDKFolder()
	switch request.Method {
	case http.MethodGet:
	case http.MethodPost:
		var input struct {
			Folder string `json:"folder"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
		if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The VDDK folder could not be read."})
			return
		}
		cleaned, err := cleanVDDKFolder(input.Folder)
		if err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if cleaned != "" {
			if status, _, err := vddkCheck(cleaned); err != nil {
				status.Message = "Not saved: " + err.Error() + "."
				writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": status.Message, "vddk": status})
				return
			}
		}
		a.mu.Lock()
		err = saveVDDKSettings(a.update.settingsDir, vddkSettings{Folder: cleaned})
		a.mu.Unlock()
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The VDDK folder could not be saved. Check access to the application settings folder."})
			return
		}
		setVDDKFolder(cleaned)
		folder = cleaned
	default:
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET or POST is required."})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "vddk": vddkStatusFor(folder)})
}

func vddkStatusFor(folder string) vddkStatus {
	if folder == "" {
		return vddkStatus{Message: "Off. Copies of running VMware VMs read whole disks through vSphere's export when the datastore refuses them."}
	}
	status, _, err := vddkCheck(folder)
	vddkFolderState.Lock()
	loaded := vddkFolderState.loaded
	vddkFolderState.Unlock()
	status.Loaded = loaded != "" && strings.EqualFold(loaded, folder)
	status.Restart = loaded != "" && !status.Loaded
	switch {
	case err != nil:
		status.Message = "VDDK in this folder will not be used: " + err.Error() + "."
	case status.Restart:
		status.Message = "VDDK " + status.Version + " is ready. Restart the application to use it instead of the folder it loaded earlier."
	default:
		status.Message = "VDDK " + status.Version + " (signed by " + status.Signer + ") is ready. Copies of running VMware VMs read only changed blocks through it."
	}
	return status
}
