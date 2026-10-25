//go:build windows

package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	appName           = "Server Inventory Configuration Manager"
	appVersion        = "3.2.1"
	collectorVersion  = "3.2.1"
	maximumCollectors = 49

	mbOK              = 0x00000000
	mbIconError       = 0x00000010
	mbIconInformation = 0x00000040
	mbTopMost         = 0x00040000
	swShowNormal      = 1
	swRestore         = 9

	bifReturnOnlyFSDirs     = 0x00000001
	bifEditBox              = 0x00000010
	bifNewDialogStyle       = 0x00000040
	bifShareable            = 0x00008000
	coinitApartmentThreaded = 0x2

	createNoWindow = 0x08000000

	wmSetIcon      = 0x0080
	iconSmall      = 0
	iconBig        = 1
	imageIcon      = 1
	lrLoadFromFile = 0x0010
)

//go:embed Export-ServerInventory.ps1 Run-Inventory.ps1 index.html favicon.ico app-icon.png
var embeddedPayloads embed.FS

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	shell32                  = syscall.NewLazyDLL("shell32.dll")
	ole32                    = syscall.NewLazyDLL("ole32.dll")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procEnumWindows          = user32.NewProc("EnumWindows")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetClassNameW        = user32.NewProc("GetClassNameW")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop     = user32.NewProc("BringWindowToTop")
	procShowWindow           = user32.NewProc("ShowWindow")
	procIsIconic             = user32.NewProc("IsIconic")
	procLoadImageW           = user32.NewProc("LoadImageW")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procIsUserAnAdmin        = shell32.NewProc("IsUserAnAdmin")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procCoUninitialize       = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

type browseInfo struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

type collectionOptions struct {
	OutputRoot          string   `json:"outputRoot"`
	InventoryCategories []string `json:"inventoryCategories"`
	ExportCategories    []string `json:"exportCategories"`
	EventLogDays        int      `json:"eventLogDays"`
	IncludeSensitive    bool     `json:"includeSensitive"`
}

type logEntry struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type stateResponse struct {
	AppVersion              string              `json:"appVersion"`
	CollectorVersion        string              `json:"collectorVersion"`
	Update                  updateStateResponse `json:"update"`
	BuildIdentity           string              `json:"buildIdentity"`
	ComputerName            string              `json:"computerName"`
	IsAdministrator         bool                `json:"isAdministrator"`
	PowerShellPath          string              `json:"powerShellPath"`
	PowerShellVersion       string              `json:"powerShellVersion"`
	DefaultOutputRoot       string              `json:"defaultOutputRoot"`
	Status                  string              `json:"status"`
	Phase                   string              `json:"phase"`
	Message                 string              `json:"message"`
	CurrentStep             string              `json:"currentStep"`
	Progress                int                 `json:"progress"`
	CompletedSteps          int                 `json:"completedSteps"`
	TotalSteps              int                 `json:"totalSteps"`
	WarningCount            int                 `json:"warningCount"`
	ErrorCount              int                 `json:"errorCount"`
	StartedAtUnixMs         int64               `json:"startedAtUnixMs"`
	EndedAtUnixMs           int64               `json:"endedAtUnixMs"`
	Running                 bool                `json:"running"`
	RunID                   string              `json:"runId"`
	OutputRoot              string              `json:"outputRoot"`
	PackagePath             string              `json:"packagePath"`
	ZipPath                 string              `json:"zipPath"`
	DocxReportPath          string              `json:"docxReportPath"`
	HtmlReportPath          string              `json:"htmlReportPath"`
	SummaryTextPath         string              `json:"summaryTextPath"`
	LauncherLogPath         string              `json:"launcherLogPath"`
	DiagnosticPath          string              `json:"diagnosticPath"`
	LoadedReportAvailable   bool                `json:"loadedReportAvailable"`
	LoadedReportPath        string              `json:"loadedReportPath"`
	LoadedReportPackage     string              `json:"loadedReportPackage"`
	LoadedReportDocxPath    string              `json:"loadedReportDocxPath"`
	LoadedReportSummaryPath string              `json:"loadedReportSummaryPath"`
	LoadedReportName        string              `json:"loadedReportName"`
	LoadedReportSerial      uint64              `json:"loadedReportSerial"`
	Logs                    []logEntry          `json:"logs"`
	NextLogIndex            int                 `json:"nextLogIndex"`
}

type runResult struct {
	Status            string `json:"Status"`
	Message           string `json:"Message"`
	RunID             string `json:"RunId"`
	PowerShellVersion string `json:"PowerShellVersion"`
	OutputRoot        string `json:"OutputRoot"`
	PackageName       string `json:"PackageName"`
	PackagePath       string `json:"PackagePath"`
	ZipPath           string `json:"ZipPath"`
	DocxReportPath    string `json:"DocxReportPath"`
	HtmlReportPath    string `json:"HtmlReportPath"`
	SummaryTextPath   string `json:"SummaryTextPath"`
	CompletedAt       string `json:"CompletedAt"`
}

type completionMarker struct {
	Status          string `json:"Status"`
	ComputerName    string `json:"ComputerName"`
	ScriptVersion   string `json:"ScriptVersion"`
	RunID           string `json:"RunId"`
	PackagePath     string `json:"PackagePath"`
	ZipPath         string `json:"ZipPath"`
	DocxReportPath  string `json:"DocxReportPath"`
	HtmlReportPath  string `json:"HtmlReportPath"`
	SummaryTextPath string `json:"SummaryTextPath"`
}

type internalState struct {
	Status          string
	Phase           string
	Message         string
	CurrentStep     string
	Progress        int
	CompletedSteps  int
	WarningCount    int
	ErrorCount      int
	StartedAt       time.Time
	EndedAt         time.Time
	Running         bool
	RunID           string
	OutputRoot      string
	PackagePath     string
	ZipPath         string
	DocxReportPath  string
	HtmlReportPath  string
	SummaryTextPath string
	LauncherLogPath string
	DiagnosticPath  string
	CancelRequested bool
}

type application struct {
	mu                      sync.Mutex
	update                  updateRuntime
	installing              bool
	criticalOperations      int
	uiReady                 bool
	token                   string
	computerName            string
	admin                   bool
	powerShellPath          string
	powerShellVersion       string
	defaultOutputRoot       string
	state                   internalState
	logs                    []logEntry
	nextLogIndex            int
	completedCollectors     map[string]bool
	cmd                     *exec.Cmd
	launcherLog             *os.File
	temporaryDirectory      string
	server                  *http.Server
	shutdown                chan struct{}
	shutdownOnce            sync.Once
	lastClientActivity      time.Time
	browserCmd              *exec.Cmd
	browserProfileDirectory string
	loadedReportPath        string
	loadedReportPackage     string
	loadedReportDocxPath    string
	loadedReportSummaryPath string
	loadedReportName        string
	loadedReportSerial      uint64
}

func utf16Ptr(value string) *uint16 {
	pointer, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		panic(err)
	}
	return pointer
}

func messageBox(text, caption string, flags uintptr) int {
	result, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(caption))),
		flags|mbTopMost,
	)
	return int(result)
}

func isAdministrator() bool {
	result, _, _ := procIsUserAnAdmin.Call()
	return result != 0
}

func relaunchElevated() error {
	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}
	workingDirectory := filepath.Dir(executablePath)
	result, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("runas"))),
		uintptr(unsafe.Pointer(utf16Ptr(executablePath))),
		0,
		uintptr(unsafe.Pointer(utf16Ptr(workingDirectory))),
		swShowNormal,
	)
	if result <= 32 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return fmt.Errorf("elevation request failed: %w", callErr)
		}
		return fmt.Errorf("elevation was canceled or failed with ShellExecute result %d", result)
	}
	return nil
}

func chooseFolder(title string) (string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	_, _, _ = procCoInitializeEx.Call(0, coinitApartmentThreaded)
	defer procCoUninitialize.Call()

	owner := findApplicationWindow()
	bringWindowToFront(owner)

	displayName := make([]uint16, 260)
	info := browseInfo{
		hwndOwner:      owner,
		pszDisplayName: &displayName[0],
		lpszTitle:      utf16Ptr(title),
		ulFlags:        bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle | bifShareable,
	}

	pidl, _, callErr := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return "", false, callErr
		}
		return "", false, nil
	}
	defer procCoTaskMemFree.Call(pidl)

	pathBuffer := make([]uint16, 32768)
	result, _, callErr := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuffer[0])))
	if result == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return "", false, callErr
		}
		return "", false, errors.New("Windows could not resolve the selected folder")
	}
	selectedPath := strings.TrimSpace(syscall.UTF16ToString(pathBuffer))
	if selectedPath == "" {
		return "", false, errors.New("the selected folder path was empty")
	}
	return selectedPath, true, nil
}

func shellOpen(target string) error {
	result, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("open"))),
		uintptr(unsafe.Pointer(utf16Ptr(target))),
		0,
		0,
		swShowNormal,
	)
	if result <= 32 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return fmt.Errorf("Windows could not open the requested item, ShellExecute result %d", result)
	}
	return nil
}

func shellReveal(target string) error {
	target = filepath.Clean(strings.TrimSpace(target))
	owner := findApplicationWindow()
	bringWindowToFront(owner)

	params := `/select,"` + target + `"`
	result, _, callErr := procShellExecuteW.Call(
		owner,
		uintptr(unsafe.Pointer(utf16Ptr("open"))),
		uintptr(unsafe.Pointer(utf16Ptr("explorer.exe"))),
		uintptr(unsafe.Pointer(utf16Ptr(params))),
		0,
		swShowNormal,
	)
	if result <= 32 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return fmt.Errorf("Windows Explorer could not reveal the requested item, ShellExecute result %d", result)
	}
	go bringExplorerToFront(target)
	return nil
}

func shellOpenFolder(target string) error {
	target = filepath.Clean(strings.TrimSpace(target))
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("the requested path is not a folder: %s", target)
	}

	owner := findApplicationWindow()
	bringWindowToFront(owner)
	params := `"` + target + `"`
	result, _, callErr := procShellExecuteW.Call(
		owner,
		uintptr(unsafe.Pointer(utf16Ptr("open"))),
		uintptr(unsafe.Pointer(utf16Ptr("explorer.exe"))),
		uintptr(unsafe.Pointer(utf16Ptr(params))),
		0,
		swShowNormal,
	)
	if result <= 32 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return fmt.Errorf("Windows Explorer could not open the requested folder, ShellExecute result %d", result)
	}
	go bringExplorerToFront(target)
	return nil
}

func terminateProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	command := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = command.Run()
}

func windowText(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, int(length)+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return syscall.UTF16ToString(buffer)
}

func windowClass(hwnd uintptr) string {
	buffer := make([]uint16, 256)
	procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return syscall.UTF16ToString(buffer)
}

func findApplicationWindow() uintptr {
	var found uintptr
	callback := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		title := strings.ToLower(windowText(hwnd))
		if !strings.Contains(title, "server inventory") || !strings.Contains(title, "configuration manager") {
			return 1
		}
		found = hwnd
		return 0
	})
	procEnumWindows.Call(callback, 0)
	return found
}

func bringWindowToFront(hwnd uintptr) {
	if hwnd == 0 {
		return
	}

	// Preserve the application's current window state. Calling SW_RESTORE on a
	// maximized window changes it back to its normal size, which made Open Folder
	// and Load Previous Report unexpectedly un-maximize the exporter. Only restore
	// a window when Windows reports that it is actually minimized.
	isMinimized, _, _ := procIsIconic.Call(hwnd)
	if isMinimized != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}

	procBringWindowToTop.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
}

func bringExplorerToFront(target string) {
	target = filepath.Clean(strings.TrimSpace(target))
	expectedTitle := strings.ToLower(filepath.Base(target))
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		expectedTitle = strings.ToLower(filepath.Base(filepath.Dir(target)))
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var fallback uintptr
		var matched uintptr
		callback := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
			visible, _, _ := procIsWindowVisible.Call(hwnd)
			if visible == 0 {
				return 1
			}
			className := strings.ToLower(windowClass(hwnd))
			if className != "cabinetwclass" && className != "explorewclass" {
				return 1
			}
			if fallback == 0 {
				fallback = hwnd
			}
			title := strings.ToLower(windowText(hwnd))
			if expectedTitle != "" && strings.Contains(title, expectedTitle) {
				matched = hwnd
				return 0
			}
			return 1
		})
		procEnumWindows.Call(callback, 0)
		if matched != 0 {
			bringWindowToFront(matched)
			return
		}
		if fallback != 0 && expectedTitle == "" {
			bringWindowToFront(fallback)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func applyBrowserWindowIcon(iconPath string) {
	if strings.TrimSpace(iconPath) == "" {
		return
	}
	iconPtr, err := syscall.UTF16PtrFromString(iconPath)
	if err != nil {
		return
	}
	bigIcon, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(iconPtr)), imageIcon, 64, 64, lrLoadFromFile)
	smallIcon, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(iconPtr)), imageIcon, 32, 32, lrLoadFromFile)
	if bigIcon == 0 && smallIcon == 0 {
		return
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		matched := false
		callback := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
			visible, _, _ := procIsWindowVisible.Call(hwnd)
			if visible == 0 {
				return 1
			}
			title := strings.ToLower(windowText(hwnd))
			if !strings.Contains(title, "server inventory") || !strings.Contains(title, "configuration manager") {
				return 1
			}
			className := strings.ToLower(windowClass(hwnd))
			if !strings.Contains(className, "chrome_widgetwin") {
				return 1
			}
			if bigIcon != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, iconBig, bigIcon)
			}
			if smallIcon != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, smallIcon)
			}
			matched = true
			return 1
		})
		procEnumWindows.Call(callback, 0)
		if matched {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func openInterfaceURL(url, profile string) (*exec.Cmd, string, error) {
	programFiles := []string{
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramFiles"),
	}
	relativeCandidates := []string{
		filepath.Join("Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join("Google", "Chrome", "Application", "chrome.exe"),
	}
	for _, root := range programFiles {
		if strings.TrimSpace(root) == "" {
			continue
		}
		for _, relative := range relativeCandidates {
			candidate := filepath.Join(root, relative)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				arguments := []string{"--app=" + url, "--new-window", "--no-first-run", "--no-default-browser-check", "--user-data-dir=" + profile}
				command := exec.Command(candidate, arguments...)
				if err := command.Start(); err == nil {
					return command, profile, nil
				}
			}
		}
	}
	return nil, "", shellOpen(url)
}

func powershellPath() string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "powershell.exe"
}

func queryPowerShellVersion(path string) string {
	command := exec.Command(path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	output, err := command.Output()
	if err != nil {
		return "Available"
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "Available"
	}
	return value
}

func defaultOutputRoot() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		desktop := filepath.Join(home, "Desktop")
		if info, statErr := os.Stat(desktop); statErr == nil && info.IsDir() {
			return desktop
		}
	}
	return `C:\ServerInventory`
}

func randomHex(bytesCount int) (string, error) {
	data := make([]byte, bytesCount)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(data)), nil
}

func safeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "SERVER"
	}
	invalid := `<>:"/\\|?*`
	var builder strings.Builder
	for _, character := range value {
		if character < 32 || strings.ContainsRune(invalid, character) {
			builder.WriteByte('_')
		} else {
			builder.WriteRune(character)
		}
	}
	result := strings.Trim(builder.String(), " .")
	if result == "" {
		return "SERVER"
	}
	return result
}

func testOutputWritable(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("select an output parent directory")
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("the output directory could not be created or accessed: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the output directory could not be inspected: %w", err)
	}
	if !info.IsDir() {
		return errors.New("the selected output path is not a directory")
	}
	testFile, err := os.CreateTemp(path, ".server-inventory-write-test-*.tmp")
	if err != nil {
		return fmt.Errorf("the selected output directory is not writable: %w", err)
	}
	name := testFile.Name()
	if _, err := testFile.WriteString("write test"); err != nil {
		_ = testFile.Close()
		_ = os.Remove(name)
		return fmt.Errorf("the selected output directory failed a write test: %w", err)
	}
	if err := testFile.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("the selected output directory failed to close its write-test file: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("the selected output directory failed to remove its write-test file: %w", err)
	}
	return nil
}

func writeEmbeddedFile(name, destination string) (string, error) {
	data, err := embeddedPayloads.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("could not read embedded %s: %w", name, err)
	}
	expected := sha256.Sum256(data)
	if err := os.WriteFile(destination, data, 0600); err != nil {
		return "", fmt.Errorf("could not write temporary %s: %w", name, err)
	}
	written, err := os.ReadFile(destination)
	if err != nil {
		return "", fmt.Errorf("could not verify temporary %s: %w", name, err)
	}
	actual := sha256.Sum256(written)
	if actual != expected {
		return "", fmt.Errorf("temporary %s failed its SHA-256 integrity check", name)
	}
	return strings.ToUpper(hex.EncodeToString(expected[:])), nil
}

func writeRuntimeFiles(directory string) (collectorPath, wrapperPath, validatorPath, collectorHash, wrapperHash string, err error) {
	collectorPath = filepath.Join(directory, "Export-ServerInventory.ps1")
	wrapperPath = filepath.Join(directory, "Run-Inventory.ps1")
	validatorPath = filepath.Join(directory, "Validate-Payloads.ps1")

	collectorHash, err = writeEmbeddedFile("Export-ServerInventory.ps1", collectorPath)
	if err != nil {
		return "", "", "", "", "", err
	}
	wrapperHash, err = writeEmbeddedFile("Run-Inventory.ps1", wrapperPath)
	if err != nil {
		return "", "", "", "", "", err
	}

	validator := []byte("\xef\xbb\xbf" + `[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$CollectorPath,
    [Parameter(Mandatory = $true)][string]$WrapperPath
)
$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false) } catch { }
if (-not $PSVersionTable -or $PSVersionTable.PSVersion.Major -lt 3) {
    Write-Error ('Windows PowerShell 3.0 or later is required. Detected: ' + $PSVersionTable.PSVersion.ToString())
    exit 10
}
foreach ($scriptPath in @($CollectorPath, $WrapperPath)) {
    $tokens = $null
    $parseErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile($scriptPath, [ref]$tokens, [ref]$parseErrors) | Out-Null
    if ($parseErrors -and $parseErrors.Count -gt 0) {
        foreach ($parseError in $parseErrors) {
            Write-Error ("{0}: line {1}, column {2}: {3}" -f $scriptPath, $parseError.Extent.StartLineNumber, $parseError.Extent.StartColumnNumber, $parseError.Message)
        }
        exit 2
    }
    Write-Output ("Parser validation passed: " + $scriptPath)
}
exit 0
`)
	if err := os.WriteFile(validatorPath, validator, 0600); err != nil {
		return "", "", "", "", "", fmt.Errorf("could not write parser validator: %w", err)
	}
	return collectorPath, wrapperPath, validatorPath, collectorHash, wrapperHash, nil
}

func stripUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
}

func readRunResult(path string) (runResult, error) {
	var result runResult
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(stripUTF8BOM(data), &result); err != nil {
		return result, err
	}
	return result, nil
}

func readZipEntry(entry *zip.File) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func requireNonEmptyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("expected a file but found a directory")
	}
	if info.Size() <= 0 {
		return errors.New("file is empty")
	}
	return nil
}

func validateCompletedOutput(packagePath, zipPath, runID string) error {
	docxReportPath := filepath.Join(packagePath, "OPEN-FIRST - Server Inventory Report.docx")
	htmlReportPath := filepath.Join(packagePath, "OPEN-FIRST - Server Inventory Report.html")
	summaryTextPath := filepath.Join(packagePath, "OPEN-FIRST - Server Inventory Summary.txt")
	required := []string{
		filepath.Join(packagePath, "00-Summary", "READ-FIRST-Server-Summary.txt"),
		filepath.Join(packagePath, "00-Summary", "Collector-Results.csv"),
		filepath.Join(packagePath, "00-Summary", "COLLECTION-COMPLETED.json"),
		filepath.Join(packagePath, "00-Summary", "EXECUTABLE-VALIDATION-SUCCEEDED.txt"),
		filepath.Join(packagePath, "Logs", "Collection.log"),
		filepath.Join(packagePath, "SHA256-Manifest.csv"),
		docxReportPath,
		htmlReportPath,
		summaryTextPath,
		zipPath,
		zipPath + ".sha256.txt",
	}
	var problems []string
	for _, path := range required {
		if err := requireNonEmptyFile(path); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("required output validation failed:\n%s", strings.Join(problems, "\n"))
	}

	docxArchive, err := zip.OpenReader(docxReportPath)
	if err != nil {
		return fmt.Errorf("could not open the consolidated Word report as an Open XML package: %w", err)
	}
	docxParts := map[string]*zip.File{}
	for _, entry := range docxArchive.File {
		docxParts[filepath.ToSlash(entry.Name)] = entry
	}
	requiredDocxParts := []string{
		"[Content_Types].xml",
		"word/document.xml",
		"word/styles.xml",
		"word/settings.xml",
		"word/_rels/document.xml.rels",
	}
	for _, partName := range requiredDocxParts {
		if docxParts[partName] == nil {
			_ = docxArchive.Close()
			return fmt.Errorf("the consolidated Word report is missing required Open XML part %q", partName)
		}
	}
	documentData, err := readZipEntry(docxParts["word/document.xml"])
	if err != nil {
		_ = docxArchive.Close()
		return fmt.Errorf("could not read the Word report document body: %w", err)
	}
	if !bytes.Contains(documentData, []byte("Server Inventory and Configuration Report")) {
		_ = docxArchive.Close()
		return errors.New("the Word report document body did not contain the expected report title")
	}
	relationshipData, err := readZipEntry(docxParts["word/_rels/document.xml.rels"])
	if err != nil {
		_ = docxArchive.Close()
		return fmt.Errorf("could not read the Word report document relationships: %w", err)
	}
	if !bytes.Contains(relationshipData, []byte("/relationships/styles")) || !bytes.Contains(relationshipData, []byte("/relationships/settings")) {
		_ = docxArchive.Close()
		return errors.New("the Word report is missing required document relationships for styles or settings")
	}
	if err := docxArchive.Close(); err != nil {
		return fmt.Errorf("could not close the consolidated Word report package: %w", err)
	}

	markerPath := filepath.Join(packagePath, "00-Summary", "COLLECTION-COMPLETED.json")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return fmt.Errorf("could not read the completion marker: %w", err)
	}
	var marker completionMarker
	if err := json.Unmarshal(stripUTF8BOM(data), &marker); err != nil {
		return fmt.Errorf("could not parse the completion marker: %w", err)
	}
	if marker.Status != "Completed" {
		return fmt.Errorf("completion marker status was %q instead of %q", marker.Status, "Completed")
	}
	if !strings.EqualFold(marker.RunID, runID) {
		return fmt.Errorf("completion marker Run ID %q did not match expected Run ID %q", marker.RunID, runID)
	}
	if !strings.EqualFold(marker.DocxReportPath, docxReportPath) {
		return fmt.Errorf("completion marker Word report path %q did not match expected path %q", marker.DocxReportPath, docxReportPath)
	}
	if !strings.EqualFold(marker.HtmlReportPath, htmlReportPath) {
		return fmt.Errorf("completion marker HTML report path %q did not match expected path %q", marker.HtmlReportPath, htmlReportPath)
	}
	if !strings.EqualFold(marker.SummaryTextPath, summaryTextPath) {
		return fmt.Errorf("completion marker summary path %q did not match expected path %q", marker.SummaryTextPath, summaryTextPath)
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func existingFile(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() && info.Size() > 0 {
		return path
	}
	return ""
}

func locateInventoryReport(folder string) (htmlPath, docxPath, summaryPath, packagePath, reportName string, err error) {
	folder = filepath.Clean(strings.TrimSpace(folder))
	info, statErr := os.Stat(folder)
	if statErr != nil {
		err = fmt.Errorf("the selected report folder could not be accessed: %w", statErr)
		return
	}
	if !info.IsDir() {
		err = errors.New("the selected report location is not a folder")
		return
	}

	htmlCandidates := []string{
		filepath.Join(folder, "OPEN-FIRST - Server Inventory Report.html"),
		filepath.Join(folder, "OPEN-FIRST - Server Rebuild Report.html"),
	}
	for _, candidate := range htmlCandidates {
		if existingFile(candidate) != "" {
			htmlPath = candidate
			break
		}
	}
	if htmlPath == "" {
		matches, _ := filepath.Glob(filepath.Join(folder, "OPEN-FIRST*.html"))
		for _, candidate := range matches {
			if existingFile(candidate) != "" {
				htmlPath = candidate
				break
			}
		}
	}
	if htmlPath == "" {
		err = errors.New("no consolidated HTML inventory report was found in the selected folder")
		return
	}

	docxCandidates := []string{
		filepath.Join(folder, "OPEN-FIRST - Server Inventory Report.docx"),
		filepath.Join(folder, "OPEN-FIRST - Server Rebuild Report.docx"),
	}
	for _, candidate := range docxCandidates {
		if existingFile(candidate) != "" {
			docxPath = candidate
			break
		}
	}
	summaryCandidates := []string{
		filepath.Join(folder, "OPEN-FIRST - Server Inventory Summary.txt"),
		filepath.Join(folder, "OPEN-FIRST - Server Rebuild Summary.txt"),
	}
	for _, candidate := range summaryCandidates {
		if existingFile(candidate) != "" {
			summaryPath = candidate
			break
		}
	}
	packagePath = folder
	reportName = filepath.Base(folder)
	return
}

func (a *application) setLoadedReport(htmlPath, packagePath, docxPath, summaryPath, reportName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loadedReportPath = htmlPath
	a.loadedReportPackage = packagePath
	a.loadedReportDocxPath = docxPath
	a.loadedReportSummaryPath = summaryPath
	a.loadedReportName = reportName
	a.loadedReportSerial++
}

func newApplication() (*application, error) {
	token, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	computerName := strings.TrimSpace(os.Getenv("COMPUTERNAME"))
	if computerName == "" {
		computerName = "This server"
	}
	psPath := powershellPath()
	app := &application{
		token:               token,
		computerName:        computerName,
		admin:               isAdministrator(),
		powerShellPath:      psPath,
		powerShellVersion:   queryPowerShellVersion(psPath),
		defaultOutputRoot:   defaultOutputRoot(),
		completedCollectors: make(map[string]bool),
		shutdown:            make(chan struct{}),
		lastClientActivity:  time.Now(),
	}
	app.state = internalState{
		Status: "idle",
		Phase:  "Ready", Message: "Choose an output directory and review the collection scope.",
		CurrentStep: "No active collector.",
		Progress:    0,
	}
	if err := app.initializeUpdater(); err != nil {
		return nil, fmt.Errorf("update system initialization failed: %w", err)
	}
	if savedOutputRoot := app.loadSavedOutputRoot(); savedOutputRoot != "" {
		app.defaultOutputRoot = savedOutputRoot
	}
	return app, nil
}

func (a *application) setLastClientActivity() {
	a.mu.Lock()
	a.lastClientActivity = time.Now()
	a.mu.Unlock()
}

func (a *application) appendLog(text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return
	}
	a.mu.Lock()
	entry := logEntry{Index: a.nextLogIndex, Text: text}
	a.nextLogIndex++
	a.logs = append(a.logs, entry)
	if a.launcherLog != nil {
		_, _ = a.launcherLog.WriteString(text + "\r\n")
	}
	a.parseLineLocked(text)
	a.mu.Unlock()
}

func (a *application) logLauncher(format string, values ...any) {
	message := fmt.Sprintf(format, values...)
	a.appendLog(fmt.Sprintf("%s [GUI] %s", time.Now().Format("2006-01-02 15:04:05"), message))
}

func (a *application) parseLineLocked(line string) {
	upper := strings.ToUpper(line)
	if strings.Contains(upper, "[WARN]") {
		a.state.WarningCount++
	}
	if strings.Contains(upper, "[ERROR]") {
		a.state.ErrorCount++
	}

	if marker := strings.Index(line, "Starting: "); marker >= 0 {
		name := strings.TrimSpace(line[marker+len("Starting: "):])
		if name != "" {
			a.state.CurrentStep = name
			if strings.EqualFold(name, "Consolidated inventory report generation") {
				a.state.Phase = "Generating consolidated reports"
				if a.state.Progress < 91 {
					a.state.Progress = 91
				}
			} else {
				a.state.Phase = "Collecting server configuration"
				if a.state.Progress < 8 {
					a.state.Progress = 8
				}
			}
			a.state.Message = "The collector is processing " + name + "."
		}
	}

	completeMarker := "Completed: "
	failedMarker := "Failed: "
	collectorName := ""
	if marker := strings.Index(line, completeMarker); marker >= 0 {
		collectorName = strings.TrimSpace(line[marker+len(completeMarker):])
	} else if marker := strings.Index(line, failedMarker); marker >= 0 {
		collectorName = strings.TrimSpace(line[marker+len(failedMarker):])
		if dot := strings.Index(collectorName, ". "); dot >= 0 {
			collectorName = collectorName[:dot]
		}
	}
	if collectorName != "" && !a.completedCollectors[collectorName] {
		a.completedCollectors[collectorName] = true
		a.state.CompletedSteps++
		progress := 8 + (a.state.CompletedSteps * 82 / maximumCollectors)
		if progress > 92 {
			progress = 92
		}
		if progress > a.state.Progress {
			a.state.Progress = progress
		}
	}

	if strings.Contains(upper, "VALIDATING THE EMBEDDED COLLECTOR") || strings.Contains(upper, "PARSER VALIDATION") {
		a.state.Phase = "Validating embedded scripts"
		a.state.Message = "The embedded PowerShell collector and execution wrapper are being checked."
		if a.state.Progress < 4 {
			a.state.Progress = 4
		}
	}
	if strings.Contains(upper, "STARTING INVENTORY COLLECTION") {
		a.state.Phase = "Starting collection"
		a.state.Message = "The PowerShell inventory collector is starting."
		if a.state.Progress < 7 {
			a.state.Progress = 7
		}
	}
	if strings.Contains(upper, "COLLECTOR RETURNED. VALIDATING REQUIRED OUTPUT") {
		a.state.Phase = "Validating generated output"
		a.state.Message = "The consolidated reports and supporting package are being validated."
		if a.state.Progress < 95 {
			a.state.Progress = 95
		}
	}
	if strings.Contains(upper, "FOUND AND VALIDATED") {
		a.state.Phase = "Final validation"
		a.state.Message = "PowerShell validation passed. The GUI is performing an independent final check."
		if a.state.Progress < 97 {
			a.state.Progress = 97
		}
	}
}

func (a *application) snapshot(since int) stateResponse {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastClientActivity = time.Now()
	if since < 0 {
		since = 0
	}
	logs := make([]logEntry, 0)
	for _, entry := range a.logs {
		if entry.Index >= since {
			logs = append(logs, entry)
		}
	}
	startedMs := int64(0)
	endedMs := int64(0)
	if !a.state.StartedAt.IsZero() {
		startedMs = a.state.StartedAt.UnixNano() / int64(time.Millisecond)
	}
	if !a.state.EndedAt.IsZero() {
		endedMs = a.state.EndedAt.UnixNano() / int64(time.Millisecond)
	}
	return stateResponse{
		AppVersion:              appVersion,
		CollectorVersion:        collectorVersion,
		Update:                  a.updateSnapshot(),
		BuildIdentity:           binaryIdentityMarker,
		ComputerName:            a.computerName,
		IsAdministrator:         a.admin,
		PowerShellPath:          a.powerShellPath,
		PowerShellVersion:       a.powerShellVersion,
		DefaultOutputRoot:       a.defaultOutputRoot,
		Status:                  a.state.Status,
		Phase:                   a.state.Phase,
		Message:                 a.state.Message,
		CurrentStep:             a.state.CurrentStep,
		Progress:                a.state.Progress,
		CompletedSteps:          a.state.CompletedSteps,
		TotalSteps:              maximumCollectors,
		WarningCount:            a.state.WarningCount,
		ErrorCount:              a.state.ErrorCount,
		StartedAtUnixMs:         startedMs,
		EndedAtUnixMs:           endedMs,
		Running:                 a.state.Running,
		RunID:                   a.state.RunID,
		OutputRoot:              a.state.OutputRoot,
		PackagePath:             a.state.PackagePath,
		ZipPath:                 a.state.ZipPath,
		DocxReportPath:          a.state.DocxReportPath,
		HtmlReportPath:          a.state.HtmlReportPath,
		SummaryTextPath:         a.state.SummaryTextPath,
		LauncherLogPath:         a.state.LauncherLogPath,
		DiagnosticPath:          a.state.DiagnosticPath,
		LoadedReportAvailable:   strings.TrimSpace(a.loadedReportPath) != "",
		LoadedReportPath:        a.loadedReportPath,
		LoadedReportPackage:     a.loadedReportPackage,
		LoadedReportDocxPath:    a.loadedReportDocxPath,
		LoadedReportSummaryPath: a.loadedReportSummaryPath,
		LoadedReportName:        a.loadedReportName,
		LoadedReportSerial:      a.loadedReportSerial,
		Logs:                    logs,
		NextLogIndex:            a.nextLogIndex,
	}
}

func (a *application) authorize(request *http.Request) bool {
	token := request.URL.Query().Get("token")
	if token == "" {
		token = request.Header.Get("X-App-Token")
	}
	return token != "" && strings.EqualFold(token, a.token)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func (a *application) requireAuthorization(response http.ResponseWriter, request *http.Request) bool {
	if !a.authorize(request) {
		writeJSON(response, http.StatusForbidden, map[string]any{"ok": false, "error": "The local application token was missing or invalid."})
		return false
	}
	a.setLastClientActivity()
	return true
}

func (a *application) handleIndex(response http.ResponseWriter, request *http.Request) {
	if !a.authorize(request) {
		http.Error(response, "This local application link is invalid or expired.", http.StatusForbidden)
		return
	}
	a.setLastClientActivity()
	data, err := embeddedPayloads.ReadFile("index.html")
	if err != nil {
		http.Error(response, "The embedded interface could not be loaded.", http.StatusInternalServerError)
		return
	}
	html := string(data)
	html = strings.ReplaceAll(html, "__TOKEN__", a.token)
	html = strings.ReplaceAll(html, "__APP_VERSION__", appVersion)
	html = strings.ReplaceAll(html, "__COLLECTOR_VERSION__", collectorVersion)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src data:; frame-src 'self'")
	_, _ = io.WriteString(response, html)
}

func (a *application) handleState(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	since, _ := strconv.Atoi(request.URL.Query().Get("since"))
	writeJSON(response, http.StatusOK, a.snapshot(since))
}

func (a *application) handleBrowse(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()

	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	a.mu.Lock()
	running := a.state.Running
	a.mu.Unlock()
	if running {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The output directory cannot be changed while a collection is running."})
		return
	}
	selectedPath, selected, err := chooseFolder("Select the parent directory for the server inventory package.")
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !selected {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "path": ""})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "path": selectedPath})
}

func (a *application) handleBrowseReport(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if !a.beginCriticalOperation() {
		writeJSON(response, http.StatusConflict, map[string]any{"error": "An update is being installed."})
		return
	}
	defer a.endCriticalOperation()

	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	selectedPath, selected, err := chooseFolder("Select a server inventory package folder containing an HTML report.")
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !selected {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "selected": false})
		return
	}
	htmlPath, docxPath, summaryPath, packagePath, reportName, err := locateInventoryReport(selectedPath)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.setLoadedReport(htmlPath, packagePath, docxPath, summaryPath, reportName)
	writeJSON(response, http.StatusOK, map[string]any{
		"ok":          true,
		"selected":    true,
		"reportName":  reportName,
		"reportPath":  htmlPath,
		"packagePath": packagePath,
		"docxPath":    docxPath,
		"summaryPath": summaryPath,
	})
}

func (a *application) handleCurrentReport(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodGet {
		http.Error(response, "GET is required.", http.StatusMethodNotAllowed)
		return
	}
	a.mu.Lock()
	path := a.loadedReportPath
	a.mu.Unlock()
	if existingFile(path) == "" {
		http.Error(response, "No inventory report is currently loaded.", http.StatusNotFound)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(response, "The inventory report could not be opened.", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(response, "The inventory report could not be inspected.", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'")
	response.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	_, _ = io.Copy(response, file)
}

func (a *application) handleStart(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var options collectionOptions
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024))
	if err := decoder.Decode(&options); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The collection settings were invalid: " + err.Error()})
		return
	}
	rawOutputRoot := strings.TrimSpace(options.OutputRoot)
	if rawOutputRoot == "" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Select or enter an output parent directory."})
		return
	}
	options.OutputRoot = filepath.Clean(rawOutputRoot)
	if !filepath.IsAbs(options.OutputRoot) {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The output parent directory must be an absolute local or UNC path."})
		return
	}
	if len(options.InventoryCategories) == 0 {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Select at least one inventory category."})
		return
	}
	if options.EventLogDays == 0 {
		options.EventLogDays = 30
	}
	if options.EventLogDays < 1 || options.EventLogDays > 365 {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Event log days must be between 1 and 365."})
		return
	}
	if err := testOutputWritable(options.OutputRoot); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	a.mu.Lock()
	if a.state.Running || a.installing {
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "A server inventory collection is already running."})
		return
	}
	a.mu.Unlock()

	runID, err := randomHex(16)
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "A secure run identifier could not be generated: " + err.Error()})
		return
	}
	packageName := fmt.Sprintf("%s_ServerInventory_%s_%s", safeName(a.computerName), time.Now().Format("20060102_150405"), runID[:8])
	packagePath := filepath.Join(options.OutputRoot, packageName)
	zipPath := filepath.Join(options.OutputRoot, packageName+".zip")
	launcherLogPath := filepath.Join(options.OutputRoot, packageName+"_GUI-Launcher.log")

	a.mu.Lock()
	if a.state.Running || a.installing {
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "A server inventory collection is already running."})
		return
	}
	a.state = internalState{
		Status:          "running",
		Phase:           "Preparing collection",
		Message:         "The output directory and embedded collector are being prepared.",
		CurrentStep:     "Preflight validation",
		Progress:        2,
		StartedAt:       time.Now(),
		Running:         true,
		RunID:           runID,
		OutputRoot:      options.OutputRoot,
		PackagePath:     packagePath,
		ZipPath:         zipPath,
		DocxReportPath:  filepath.Join(packagePath, "OPEN-FIRST - Server Inventory Report.docx"),
		HtmlReportPath:  filepath.Join(packagePath, "OPEN-FIRST - Server Inventory Report.html"),
		SummaryTextPath: filepath.Join(packagePath, "OPEN-FIRST - Server Inventory Summary.txt"),
		LauncherLogPath: launcherLogPath,
	}
	a.saveApplicationSettings(options.OutputRoot)
	a.logs = nil
	a.nextLogIndex = 0
	a.completedCollectors = make(map[string]bool)
	a.mu.Unlock()

	go a.runCollection(options, packageName, runID)
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true, "runId": runID, "packagePath": packagePath})
}

func (a *application) handleCancel(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	a.mu.Lock()
	if !a.state.Running || a.cmd == nil || a.cmd.Process == nil {
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "No collection process is currently running."})
		return
	}
	pid := a.cmd.Process.Pid
	a.state.CancelRequested = true
	a.state.Phase = "Canceling collection"
	a.state.Message = "Windows is stopping the PowerShell collector and its child processes."
	a.mu.Unlock()

	command := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err := command.Run(); err != nil {
		a.mu.Lock()
		if a.cmd != nil && a.cmd.Process != nil {
			_ = a.cmd.Process.Kill()
		}
		a.mu.Unlock()
	}
	a.logLauncher("Cancellation was requested by the operator.")
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

func (a *application) handleOpen(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var input struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(io.LimitReader(request.Body, 16*1024)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The requested item was invalid."})
		return
	}
	a.mu.Lock()
	state := a.state
	loadedReportPath := a.loadedReportPath
	loadedReportPackage := a.loadedReportPackage
	loadedReportDocxPath := a.loadedReportDocxPath
	loadedReportSummaryPath := a.loadedReportSummaryPath
	a.mu.Unlock()
	var target string
	var urlTarget string
	reveal := false
	switch strings.ToLower(input.Kind) {
	case "folder":
		target = state.PackagePath
	case "docx":
		target = state.DocxReportPath
	case "html":
		target = state.HtmlReportPath
	case "summary":
		target = state.SummaryTextPath
	case "zip":
		target = state.ZipPath
		reveal = true
	case "log":
		target = state.LauncherLogPath
	case "outputroot":
		target = state.OutputRoot
	case "diagnostics":
		target = state.DiagnosticPath
	case "loadedhtml":
		target = loadedReportPath
	case "loadedfolder":
		target = loadedReportPackage
	case "loadeddocx":
		target = loadedReportDocxPath
	case "loadedsummary":
		target = loadedReportSummaryPath
	case "updaterelease":
		a.update.mu.Lock()
		manifest := a.update.currentManifest
		a.update.mu.Unlock()
		if manifest == nil || strings.TrimSpace(manifest.ReleaseURL) == "" {
			writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "No update release page is available."})
			return
		}
		urlTarget = manifest.ReleaseURL
	case "repository":
		urlTarget = repositoryURL
	default:
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The requested output type was not recognized."})
		return
	}
	if strings.TrimSpace(urlTarget) != "" {
		if err := shellOpen(urlTarget); err != nil {
			writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if strings.TrimSpace(target) == "" {
		writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "The requested output path is not available."})
		return
	}
	if _, err := os.Stat(target); err != nil {
		writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "The requested output does not exist: " + err.Error()})
		return
	}
	var err error
	if reveal {
		err = shellReveal(target)
	} else if strings.EqualFold(input.Kind, "folder") || strings.EqualFold(input.Kind, "outputroot") || strings.EqualFold(input.Kind, "diagnostics") || strings.EqualFold(input.Kind, "loadedfolder") {
		err = shellOpenFolder(target)
	} else {
		err = shellOpen(target)
	}
	if err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "Windows could not open the requested output: " + err.Error()})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

func (a *application) handleUpdateCheck(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var payload struct {
		Manual bool   `json:"manual"`
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(request.Body, 8*1024)).Decode(&payload)
	if err := a.beginUpdateCheck(payload.Manual, strings.EqualFold(payload.Reason, "focus")); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleUpdateDownload(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	if err := a.beginUpdateDownload(); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleUpdateInstall(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	if err := a.installReadyUpdate(); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true, "restarting": true})
}

func (a *application) handleReset(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	a.mu.Lock()
	if a.state.Running || a.installing {
		a.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "A running collection cannot be reset."})
		return
	}
	a.state = internalState{
		Status:      "idle",
		Phase:       "Ready",
		Message:     "Choose an output directory and review the collection scope.",
		CurrentStep: "No active collector.",
	}
	a.logs = nil
	a.nextLogIndex = 0
	a.completedCollectors = make(map[string]bool)
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

func (a *application) handleShutdown(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	a.mu.Lock()
	running := a.state.Running
	a.mu.Unlock()
	if running {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The application cannot close while collection is running. Cancel the collection first."})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.stopServer()
	}()
}

func (a *application) closeLauncherLog() {
	a.mu.Lock()
	logFile := a.launcherLog
	a.launcherLog = nil
	a.mu.Unlock()
	if logFile != nil {
		_ = logFile.Sync()
		_ = logFile.Close()
	}
}

func (a *application) markFailure(message string, canceled bool) {
	a.mu.Lock()
	a.state.Running = false
	a.state.EndedAt = time.Now()
	if canceled {
		a.state.Status = "canceled"
		a.state.Phase = "Collection canceled"
		a.state.Message = "The collection was canceled. Partial output and diagnostic files may remain."
	} else {
		a.state.Status = "failed"
		a.state.Phase = "Collection failed"
		a.state.Message = message
		a.state.ErrorCount++
	}
	a.state.CurrentStep = "Review the execution log for details."
	if a.state.Progress < 3 {
		a.state.Progress = 3
	}
	a.cmd = nil
	a.mu.Unlock()
	a.closeLauncherLog()
}

func (a *application) markSuccess(finalLogPath string) {
	a.mu.Lock()
	a.state.Running = false
	a.state.Status = "success"
	a.state.Phase = "Inventory complete"
	a.state.Message = "The consolidated reports and supporting package passed validation."
	a.state.CurrentStep = "Open the engineering report or summary to review the server inventory and configuration information."
	a.state.Progress = 100
	a.state.EndedAt = time.Now()
	a.state.LauncherLogPath = finalLogPath
	a.cmd = nil
	a.loadedReportPath = a.state.HtmlReportPath
	a.loadedReportPackage = a.state.PackagePath
	a.loadedReportDocxPath = a.state.DocxReportPath
	a.loadedReportSummaryPath = a.state.SummaryTextPath
	a.loadedReportName = filepath.Base(a.state.PackagePath)
	a.loadedReportSerial++
	a.mu.Unlock()
}

func (a *application) startAndStream(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}

	a.mu.Lock()
	a.cmd = command
	a.mu.Unlock()

	var readers sync.WaitGroup
	scan := func(reader io.Reader) {
		defer readers.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			a.appendLog(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			a.logLauncher("Output stream error: %v", err)
		}
	}
	readers.Add(2)
	go scan(stdout)
	go scan(stderr)
	waitErr := command.Wait()
	readers.Wait()

	a.mu.Lock()
	if a.cmd == command {
		a.cmd = nil
	}
	a.mu.Unlock()
	return waitErr
}

func (a *application) runCollection(options collectionOptions, packageName, runID string) {
	a.mu.Lock()
	launcherLogPath := a.state.LauncherLogPath
	packagePath := a.state.PackagePath
	zipPath := a.state.ZipPath
	a.mu.Unlock()

	logFile, err := os.OpenFile(launcherLogPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		a.markFailure("The GUI launcher log could not be created: "+err.Error(), false)
		return
	}
	a.mu.Lock()
	a.launcherLog = logFile
	a.mu.Unlock()

	a.logLauncher("Application version: %s", appVersion)
	a.logLauncher("Collector version: %s", collectorVersion)
	a.logLauncher("Run ID: %s", runID)
	a.logLauncher("Output root: %s", options.OutputRoot)
	a.logLauncher("Expected package: %s", packagePath)
	a.logLauncher("Inventory categories: %s", strings.Join(options.InventoryCategories, ";"))
	a.logLauncher("Backup/export categories: %s", strings.Join(options.ExportCategories, ";"))
	a.logLauncher("Event history days when selected: %d", options.EventLogDays)
	a.logLauncher("Sensitive exports allowed: %t", options.IncludeSensitive)

	_ = os.MkdirAll(filepath.Join(a.update.appDataRoot, "Runtime"), 0700)
	temporaryDirectory, err := os.MkdirTemp(filepath.Join(a.update.appDataRoot, "Runtime"), "Collectors-")
	if err != nil {
		a.logLauncher("Temporary directory creation failed: %v", err)
		a.markFailure("A runtime directory could not be created: "+err.Error(), false)
		return
	}
	a.mu.Lock()
	a.temporaryDirectory = temporaryDirectory
	a.state.DiagnosticPath = temporaryDirectory
	a.mu.Unlock()
	a.logLauncher("Runtime diagnostics directory: %s", temporaryDirectory)

	collectorPath, wrapperPath, validatorPath, collectorHash, wrapperHash, err := writeRuntimeFiles(temporaryDirectory)
	if err != nil {
		a.logLauncher("Embedded payload preparation failed: %v", err)
		a.markFailure("The embedded collector could not be prepared: "+err.Error(), false)
		return
	}
	a.logLauncher("Collector SHA-256: %s", collectorHash)
	a.logLauncher("Wrapper SHA-256: %s", wrapperHash)

	a.mu.Lock()
	a.state.Phase = "Validating embedded scripts"
	a.state.Message = "The PowerShell parser is checking the collector and execution wrapper."
	a.state.CurrentStep = "PowerShell parser validation"
	a.state.Progress = 4
	a.mu.Unlock()

	validateCommand := exec.Command(
		a.powerShellPath,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", validatorPath,
		"-CollectorPath", collectorPath,
		"-WrapperPath", wrapperPath,
	)
	if err := a.startAndStream(validateCommand); err != nil {
		a.mu.Lock()
		canceled := a.state.CancelRequested
		a.mu.Unlock()
		if canceled {
			a.markFailure("", true)
		} else {
			a.logLauncher("PowerShell parser validation failed: %v", err)
			a.markFailure("The embedded PowerShell files failed parser validation. Review the launcher log and runtime diagnostics.", false)
		}
		return
	}
	a.mu.Lock()
	validatorCanceled := a.state.CancelRequested
	a.mu.Unlock()
	if validatorCanceled {
		a.markFailure("", true)
		return
	}
	a.logLauncher("Embedded payload syntax validation passed.")

	resultPath := filepath.Join(temporaryDirectory, "Run-Result.json")
	arguments := []string{
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", wrapperPath,
		"-CollectorPath", collectorPath,
		"-OutputRoot", options.OutputRoot,
		"-PackageName", packageName,
		"-RunId", runID,
		"-ResultPath", resultPath,
	}
	arguments = append(arguments,
		"-InventoryCategoryList", strings.Join(options.InventoryCategories, ";"),
		"-ExportCategoryList", strings.Join(options.ExportCategories, ";"),
		"-EventLogDays", strconv.Itoa(options.EventLogDays),
	)
	if options.IncludeSensitive {
		arguments = append(arguments, "-IncludeSensitive")
	}

	a.mu.Lock()
	a.state.Phase = "Starting collection"
	a.state.Message = "The embedded PowerShell collector is starting."
	a.state.CurrentStep = "Collector startup"
	a.state.Progress = 6
	a.mu.Unlock()
	a.logLauncher("Starting PowerShell inventory collection.")

	inventoryCommand := exec.Command(a.powerShellPath, arguments...)
	inventoryCommand.Dir = options.OutputRoot
	if err := a.startAndStream(inventoryCommand); err != nil {
		a.mu.Lock()
		canceled := a.state.CancelRequested
		a.mu.Unlock()
		if canceled {
			a.markFailure("", true)
			return
		}
		resultMessage := ""
		if result, readErr := readRunResult(resultPath); readErr == nil && result.Message != "" {
			resultMessage = result.Message
		}
		a.logLauncher("PowerShell collection failed: %v. %s", err, resultMessage)
		message := "The PowerShell collection process exited with an error."
		if resultMessage != "" {
			message += " " + resultMessage
		}
		a.markFailure(message, false)
		return
	}
	a.mu.Lock()
	inventoryCanceled := a.state.CancelRequested
	a.mu.Unlock()
	if inventoryCanceled {
		a.markFailure("", true)
		return
	}

	result, err := readRunResult(resultPath)
	if err != nil {
		a.logLauncher("The wrapper result file could not be read: %v", err)
		a.markFailure("PowerShell returned without a valid result file. Review the launcher log and runtime diagnostics.", false)
		return
	}
	if result.Status != "Success" || !strings.EqualFold(result.RunID, runID) {
		a.logLauncher("Unexpected wrapper result: status=%q, run ID=%q, message=%q", result.Status, result.RunID, result.Message)
		a.markFailure("The PowerShell wrapper did not report a validated success. "+result.Message, false)
		return
	}

	a.mu.Lock()
	a.state.Phase = "Independent final validation"
	a.state.Message = "The GUI is independently checking the reports, completion marker, archive, and checksum."
	a.state.CurrentStep = "Validating consolidated output"
	a.state.Progress = 98
	a.mu.Unlock()

	if err := validateCompletedOutput(packagePath, zipPath, runID); err != nil {
		a.logLauncher("Independent output validation failed: %v", err)
		a.markFailure("The generated package failed independent validation: "+err.Error(), false)
		return
	}
	a.logLauncher("Consolidated Word report, HTML report, text summary, completion marker, inventory folder, ZIP archive, and checksum were validated.")
	a.logLauncher("Collection completed successfully.")
	a.closeLauncherLog()

	finalLogPath := filepath.Join(packagePath, "Logs", "GUI-Launcher.log")
	if err := copyFile(launcherLogPath, finalLogPath); err == nil {
		_ = os.Remove(launcherLogPath)
	} else {
		finalLogPath = launcherLogPath
	}
	_ = copyFile(wrapperPath, filepath.Join(packagePath, "00-Summary", "GUI-Run-Wrapper.ps1"))

	if err := os.RemoveAll(temporaryDirectory); err == nil {
		a.mu.Lock()
		a.temporaryDirectory = ""
		a.state.DiagnosticPath = ""
		a.mu.Unlock()
	}
	a.markSuccess(finalLogPath)
}

func (a *application) stopServer() {
	a.shutdownOnce.Do(func() {
		if a.server != nil {
			contextWithTimeout, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = a.server.Shutdown(contextWithTimeout)
			cancel()
		}
		a.mu.Lock()
		profile := a.browserProfileDirectory
		command := a.browserCmd
		a.browserCmd = nil
		a.mu.Unlock()
		if command != nil && command.Process != nil {
			terminateProcessTree(command.Process.Pid)
		}
		if profile != "" {
			_ = os.RemoveAll(profile)
		}
		close(a.shutdown)
	})
}

func (a *application) idleMonitor() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.mu.Lock()
			running := a.state.Running
			idleFor := time.Since(a.lastClientActivity)
			activeCommand := a.cmd
			if idleFor > 30*time.Second && running {
				a.state.CancelRequested = true
			}
			a.mu.Unlock()
			if idleFor > 30*time.Second {
				if running && activeCommand != nil && activeCommand.Process != nil {
					terminateProcessTree(activeCommand.Process.Pid)
				}
				a.stopServer()
				return
			}
		case <-a.shutdown:
			return
		}
	}
}

func (a *application) handleFavicon(response http.ResponseWriter, request *http.Request) {
	data, err := embeddedPayloads.ReadFile("favicon.ico")
	if err != nil {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", "image/x-icon")
	response.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = response.Write(data)
}

func (a *application) handleAppIcon(response http.ResponseWriter, request *http.Request) {
	data, err := embeddedPayloads.ReadFile("app-icon.png")
	if err != nil {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", "image/png")
	response.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = response.Write(data)
}

func (a *application) serve(listener net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/favicon.ico", a.handleFavicon)
	mux.HandleFunc("/app-icon.png", a.handleAppIcon)
	mux.HandleFunc("/api/state", a.handleState)
	mux.HandleFunc("/api/browse", a.handleBrowse)
	mux.HandleFunc("/api/browse-report", a.handleBrowseReport)
	mux.HandleFunc("/report/current", a.handleCurrentReport)
	mux.HandleFunc("/api/start", a.handleStart)
	mux.HandleFunc("/api/cancel", a.handleCancel)
	mux.HandleFunc("/api/open", a.handleOpen)
	mux.HandleFunc("/api/ui/ready", a.handleUIReady)
	mux.HandleFunc("/api/update/check", a.handleUpdateCheck)
	mux.HandleFunc("/api/update/download", a.handleUpdateDownload)
	mux.HandleFunc("/api/update/install", a.handleUpdateInstall)
	mux.HandleFunc("/api/reset", a.handleReset)
	mux.HandleFunc("/api/shutdown", a.handleShutdown)

	a.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}
	go a.idleMonitor()
	go func() {
		err := a.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			messageBox("The local web interface stopped unexpectedly.\r\n\r\n"+err.Error(), appName, mbOK|mbIconError)
			a.stopServer()
		}
	}()
	return nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--update-helper" {
		os.Exit(runUpdateHelper(os.Args[2:]))
	}
	healthToken := extractHealthToken(os.Args[1:])
	_ = binaryIdentityMarker

	if !isAdministrator() {
		if err := relaunchElevated(); err != nil {
			messageBox("Administrator rights are required.\r\n\r\n"+err.Error(), appName, mbOK|mbIconError)
		}
		return
	}

	app, err := newApplication()
	if err != nil {
		messageBox("The application could not initialize.\r\n\r\n"+err.Error(), appName, mbOK|mbIconError)
		return
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		messageBox("The local web interface could not start.\r\n\r\n"+err.Error(), appName, mbOK|mbIconError)
		return
	}
	if err := app.serve(listener); err != nil {
		_ = listener.Close()
		messageBox("The local web interface could not start.\r\n\r\n"+err.Error(), appName, mbOK|mbIconError)
		return
	}

	address := listener.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", address.Port, app.token)
	runtimeDir := filepath.Join(app.update.appDataRoot, "Runtime")
	_ = os.MkdirAll(runtimeDir, 0o700)
	urlFile := filepath.Join(runtimeDir, "application-url.txt")
	_ = os.WriteFile(urlFile, []byte(url), 0600)
	windowIconPath := filepath.Join(runtimeDir, "application.ico")
	if iconData, iconErr := embeddedPayloads.ReadFile("favicon.ico"); iconErr == nil {
		_ = os.WriteFile(windowIconPath, iconData, 0600)
	}
	browserCmd, browserProfileDirectory, openErr := openInterfaceURL(url, filepath.Join(runtimeDir, "Browser-"+app.token))
	if openErr != nil {
		messageBox("The interface started, but a browser window could not be opened.\r\n\r\nThe local address was written to:\r\n"+urlFile, appName, mbOK|mbIconInformation)
	} else if browserCmd != nil {
		app.mu.Lock()
		app.browserCmd = browserCmd
		app.browserProfileDirectory = browserProfileDirectory
		app.mu.Unlock()
	}

	go applyBrowserWindowIcon(windowIconPath)
	go app.confirmHealthyWindowAndMark(healthToken)
	// The authenticated UI-ready event starts the scheduler after the first paint.

	<-app.shutdown
	_ = os.Remove(urlFile)
	_ = os.Remove(windowIconPath)
}
