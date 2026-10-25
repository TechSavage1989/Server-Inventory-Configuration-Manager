//go:build windows

package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	procNtQueryInformationProcess = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQueryInformationProcess")
	procTerminateOwnedProcess     = kernel32.NewProc("TerminateProcess")
	procGetExtendedTCPTable       = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")
)

type browserProcess struct {
	PID, ParentPID uint32
	Name           string
	Args           []string
}
type browserOwner struct {
	AppID      string `json:"app_id"`
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
}

func browserRuntimeDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Server Inventory Configuration Manager", "Runtime")
}
func ownedBrowserProfile(profile string) bool {
	if !filepath.IsAbs(profile) || !strings.EqualFold(filepath.Dir(filepath.Clean(profile)), filepath.Clean(browserRuntimeDir())) {
		return false
	}
	name := filepath.Base(profile)
	if !strings.HasPrefix(name, "Browser-") || len(name) != len("Browser-")+48 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(name, "Browser-"))
	return err == nil
}
func browserArguments(address, profile string) []string {
	return []string{"--app=" + address, "--new-window", "--guest", "--disable-extensions", "--disable-sync", "--disable-default-apps", "--disable-background-mode", "--no-first-run", "--no-default-browser-check", "--user-data-dir=" + profile}
}
func argumentValue(args []string, name string) string {
	for i, arg := range args {
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=")
		}
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func matchesBrowserProfile(args []string, profile string) bool {
	value := argumentValue(args, "--user-data-dir")
	return value != "" && strings.EqualFold(filepath.Clean(value), filepath.Clean(profile))
}

// Query only process command-line metadata. Never inspect browser profile data,
// inject code, or read user browsing/password memory.
func processArguments(pid uint32) ([]string, error) {
	handle, _, err := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return nil, err
	}
	defer procCloseHandle.Call(handle)
	var size uint32
	procNtQueryInformationProcess.Call(handle, 60, 0, 0, uintptr(unsafe.Pointer(&size)))
	if size < 16 || size > 128*1024 {
		return nil, errors.New("process command line unavailable")
	}
	buf := make([]byte, size)
	status, _, _ := procNtQueryInformationProcess.Call(handle, 60, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), uintptr(unsafe.Pointer(&size)))
	if status != 0 {
		return nil, errors.New("process command line query failed")
	}
	length := int(binary.LittleEndian.Uint16(buf[:2]))
	base := uintptr(unsafe.Pointer(&buf[0]))
	pointer := uintptr(binary.LittleEndian.Uint64(buf[8:16]))
	if length == 0 || length%2 != 0 || pointer < base || uint64(pointer-base)+uint64(length) > uint64(len(buf)) {
		return nil, errors.New("invalid process command line")
	}
	data := buf[int(pointer-base) : int(pointer-base)+length]
	units := make([]uint16, length/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	line := string(utf16.Decode(units))
	p, e := syscall.UTF16PtrFromString(line)
	if e != nil {
		return nil, e
	}
	var count int32
	argv, e := syscall.CommandLineToArgv(p, &count)
	if e != nil {
		return nil, e
	}
	defer syscall.LocalFree(syscall.Handle(uintptr(unsafe.Pointer(argv))))
	if count < 1 || count > 8192 {
		return nil, errors.New("invalid process argument count")
	}
	result := make([]string, count)
	for i := range result {
		result[i] = syscall.UTF16ToString(argv[i][:])
	}
	return result, nil
}
func browserProcesses() ([]browserProcess, error) {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(snapshot)
	entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	var result []browserProcess
	for err = syscall.Process32First(snapshot, &entry); err == nil; err = syscall.Process32Next(snapshot, &entry) {
		name := strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:]))
		if name != "msedge.exe" && name != "chrome.exe" {
			continue
		}
		args, e := processArguments(entry.ProcessID)
		if e != nil {
			continue
		}
		result = append(result, browserProcess{PID: entry.ProcessID, ParentPID: entry.ParentProcessID, Name: name, Args: args})
	}
	return result, nil
}
func scopedBrowserProcesses(profile string) ([]browserProcess, error) {
	if !ownedBrowserProfile(profile) {
		return nil, errors.New("refusing a browser profile outside the application's runtime directory")
	}
	all, err := browserProcesses()
	if err != nil {
		return nil, err
	}
	var result []browserProcess
	for _, p := range all {
		if matchesBrowserProfile(p.Args, profile) {
			result = append(result, p)
		}
	}
	return result, nil
}
func scopedBrowserWindows(profile string) []uintptr {
	processes, err := scopedBrowserProcesses(profile)
	if err != nil {
		return nil
	}
	pids := map[uint32]bool{}
	for _, p := range processes {
		pids[p.PID] = true
	}
	var windows []uintptr
	callback := syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible != 0 && pids[pid] && strings.Contains(strings.ToLower(windowClass(hwnd)), "chrome_widgetwin") {
			windows = append(windows, hwnd)
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return windows
}
func closeScopedBrowser(profile string) error {
	if !ownedBrowserProfile(profile) {
		return errors.New("browser session ownership could not be verified")
	}
	for _, window := range scopedBrowserWindows(profile) {
		procPostMessageWUpdate.Call(window, wmClose, 0, 0)
	}
	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		p, err := scopedBrowserProcesses(profile)
		if err != nil {
			return err
		}
		if len(p) == 0 {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	processes, err := scopedBrowserProcesses(profile)
	if err != nil {
		return err
	}
	for _, p := range processes {
		// Revalidate on the same live PID immediately before requesting termination.
		args, e := processArguments(p.PID)
		if e != nil || !matchesBrowserProfile(args, profile) {
			continue
		}
		handle, _, _ := procOpenProcess.Call(0x0001|processQueryLimitedInformation, 0, uintptr(p.PID))
		if handle == 0 {
			continue
		}
		procTerminateOwnedProcess.Call(handle, 0)
		procCloseHandle.Call(handle)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p, err := scopedBrowserProcesses(profile)
		if err != nil {
			return err
		}
		if len(p) == 0 {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("the application browser session did not close")
}
func writeBrowserOwner(profile string) error {
	if !ownedBrowserProfile(profile) {
		return errors.New("invalid application browser profile")
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	executable, _ := os.Executable()
	data, _ := json.Marshal(browserOwner{AppID: applicationID, PID: os.Getpid(), Executable: executable})
	return os.WriteFile(filepath.Join(profile, "sicm-owner.json"), data, 0600)
}
func tcpListenerPID(port int) uint32 {
	var size uint32
	procGetExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, 2, 3, 0)
	if size < 4 || size > 16*1024*1024 {
		return 0
	}
	buf := make([]byte, size)
	code, _, _ := procGetExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 2, 3, 0)
	if code != 0 {
		return 0
	}
	count := int(binary.LittleEndian.Uint32(buf))
	if count > (len(buf)-4)/24 {
		return 0
	}
	for i := 0; i < count; i++ {
		row := buf[4+i*24 : 4+(i+1)*24]
		if int(binary.BigEndian.Uint16(row[8:10])) == port {
			return binary.LittleEndian.Uint32(row[20:24])
		}
	}
	return 0
}
func profileFromApplicationURL(address string) (string, int, bool) {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/" {
		return "", 0, false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	token := u.Query().Get("token")
	if len(token) != 48 {
		return "", 0, false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", 0, false
	}
	profile := filepath.Join(browserRuntimeDir(), "Browser-"+token)
	return profile, port, ownedBrowserProfile(profile)
}
func previousBrowserProfile(parentPID int, target string) string {
	if parentPID <= 0 {
		return ""
	}
	dirs, _ := os.ReadDir(browserRuntimeDir())
	for _, entry := range dirs {
		profile := filepath.Join(browserRuntimeDir(), entry.Name())
		if !entry.IsDir() || !ownedBrowserProfile(profile) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(profile, "sicm-owner.json"))
		if err != nil {
			continue
		}
		var owner browserOwner
		if json.Unmarshal(data, &owner) == nil && owner.AppID == applicationID && owner.PID == parentPID && strings.EqualFold(filepath.Clean(owner.Executable), filepath.Clean(target)) {
			return profile
		}
	}
	// Compatibility with v3.2.0-v3.2.3, which did not write a browser owner record.
	data, err := os.ReadFile(filepath.Join(browserRuntimeDir(), "application-url.txt"))
	if err != nil || len(data) > 8192 {
		return ""
	}
	profile, port, ok := profileFromApplicationURL(string(data))
	if ok && tcpListenerPID(port) == uint32(parentPID) {
		return profile
	}
	return ""
}
func closeOrphanedApplicationBrowsers() {
	all, err := browserProcesses()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range all {
		profile := argumentValue(p.Args, "--user-data-dir")
		if !ownedBrowserProfile(profile) || seen[profile] {
			continue
		}
		address := argumentValue(p.Args, "--app")
		expected, port, ok := profileFromApplicationURL(address)
		if !ok || !strings.EqualFold(filepath.Clean(profile), expected) || tcpListenerPID(port) != 0 {
			continue
		}
		// The exact app-owned profile, token-bound app URL, and dead loopback backend
		// must all match. Personal browser profiles and active app sessions are excluded.
		seen[profile] = true
		_ = closeScopedBrowser(profile)
	}
}
func guestModeAllowed(browser string) bool {
	vendor := "Microsoft\\Edge"
	if strings.EqualFold(filepath.Base(browser), "chrome.exe") {
		vendor = "Google\\Chrome"
	}
	for _, root := range []syscall.Handle{syscall.HKEY_LOCAL_MACHINE, syscall.HKEY_CURRENT_USER} {
		var key syscall.Handle
		if syscall.RegOpenKeyEx(root, syscall.StringToUTF16Ptr("Software\\Policies\\"+vendor), 0, syscall.KEY_READ, &key) != nil {
			continue
		}
		values := map[string]uint32{}
		for _, name := range []string{"BrowserGuestModeEnabled", "BrowserSignin", "ForceBrowserSignin"} {
			var kind uint32
			var data [4]byte
			size := uint32(len(data))
			err := syscall.RegQueryValueEx(key, syscall.StringToUTF16Ptr(name), nil, &kind, &data[0], &size)
			if err == nil && kind == syscall.REG_DWORD && size == 4 {
				values[name] = binary.LittleEndian.Uint32(data[:])
			}
		}
		syscall.RegCloseKey(key)
		if browserPolicyDisallowsGuest(values) {
			return false
		}
	}
	return true
}
func browserPolicyDisallowsGuest(values map[string]uint32) bool {
	guest, configured := values["BrowserGuestModeEnabled"]
	return (configured && guest == 0) || values["BrowserSignin"] == 2 || values["ForceBrowserSignin"] == 1
}
func browserCandidates() []string {
	var result []string
	for _, root := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		for _, relative := range []string{filepath.Join("Microsoft", "Edge", "Application", "msedge.exe"), filepath.Join("Google", "Chrome", "Application", "chrome.exe")} {
			result = append(result, filepath.Join(root, relative))
		}
	}
	return result
}
func (a *application) closeApplicationBrowser() error {
	a.mu.Lock()
	profile := a.browserProfileDirectory
	a.mu.Unlock()
	if profile == "" {
		return nil
	}
	if err := closeScopedBrowser(profile); err != nil {
		return fmt.Errorf("could not close the application window: %w", err)
	}
	_ = os.RemoveAll(profile)
	return nil
}
