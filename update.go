//go:build windows

package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	applicationID              = "Server-Inventory-Configuration-Manager"
	stableExecutableName       = "Server-Inventory-Configuration-Manager.exe"
	repositoryURL              = "https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager"
	updateFeedURL              = "https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json"
	updateCheckInterval        = 5 * time.Minute
	updateFocusInterval        = 2 * time.Minute
	manualUpdateCooldown       = 30 * time.Second
	maximumUpdateBytes   int64 = 64 * 1024 * 1024
	binaryIdentityPrefix       = "SICM_BINARY_ID|Server-Inventory-Configuration-Manager|"
)

var binaryIdentityMarker = binaryIdentityPrefix + appVersion + "|"

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procGetExitCodeProcess       = kernel32.NewProc("GetExitCodeProcess")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procMoveFileExW              = kernel32.NewProc("MoveFileExW")
	procPostMessageWUpdate       = user32.NewProc("PostMessageW")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	wmClose                        = 0x0010
	moveFileReplaceExisting        = 0x00000001
	moveFileWriteThrough           = 0x00000008
)

type updateAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type updateManifest struct {
	SchemaVersion int         `json:"schema_version"`
	AppID         string      `json:"app_id"`
	Version       string      `json:"version"`
	PublishedAt   string      `json:"published_at"`
	ReleaseURL    string      `json:"release_url"`
	Notes         []string    `json:"notes"`
	ReadMore      bool        `json:"read_more"`
	Asset         updateAsset `json:"asset"`
}

type updateCache struct {
	ETag            string          `json:"etag"`
	Manifest        *updateManifest `json:"manifest,omitempty"`
	FailureCount    int             `json:"failure_count"`
	BackoffUntilUTC string          `json:"backoff_until_utc,omitempty"`
	LastCheckedUTC  string          `json:"last_checked_utc,omitempty"`
}

type updateStateResponse struct {
	Status           string   `json:"status"`
	Title            string   `json:"title"`
	Message          string   `json:"message"`
	InstalledVersion string   `json:"installedVersion"`
	LatestVersion    string   `json:"latestVersion"`
	Available        bool     `json:"available"`
	Checking         bool     `json:"checking"`
	Downloading      bool     `json:"downloading"`
	Downloaded       bool     `json:"downloaded"`
	Progress         int      `json:"progress"`
	Notes            []string `json:"notes"`
	ReadMore         bool     `json:"readMore"`
	ReleaseURL       string   `json:"releaseUrl"`
	CanCheck         bool     `json:"canCheck"`
	CanDownload      bool     `json:"canDownload"`
	CanInstall       bool     `json:"canInstall"`
	LastCheckUnixMs  int64    `json:"lastCheckUnixMs"`
	Notice           string   `json:"notice,omitempty"`
}

type updateRuntime struct {
	mu sync.Mutex

	appDataRoot      string
	settingsDir      string
	settingsPath     string
	logsDir          string
	cacheDir         string
	downloadsDir     string
	readyDir         string
	rollbackDir      string
	healthDir        string
	updateLogPath    string
	cachePath        string
	readyPath        string
	rollbackPath     string
	rollbackMetaPath string
	healthMarkerPath string

	currentManifest  *updateManifest
	state            updateStateResponse
	checkInFlight    bool
	downloadInFlight bool
	lastAttempt      time.Time
	lastManual       time.Time
	httpClient       *http.Client
	schedulerOnce    sync.Once
	readyVersion     string
	readySHA256      string
}

type healthMarker struct {
	AppID                  string `json:"app_id"`
	Version                string `json:"version"`
	PID                    int    `json:"pid"`
	ExecutablePath         string `json:"executable_path"`
	HealthToken            string `json:"health_token,omitempty"`
	StartedAtUTC           string `json:"started_at_utc"`
	VisibleWindowConfirmed bool   `json:"visible_window_confirmed"`
	UIReady                bool   `json:"ui_ready"`
}

type rollbackMetadata struct {
	Version      string `json:"version"`
	OriginalPath string `json:"original_path"`
	CreatedAtUTC string `json:"created_at_utc"`
	SHA256       string `json:"sha256"`
}

func (a *application) initializeUpdater() error {
	root := os.Getenv("LOCALAPPDATA")
	if strings.TrimSpace(root) == "" {
		return errors.New("LOCALAPPDATA is unavailable")
	}
	a.update.appDataRoot = filepath.Join(root, "Server Inventory Configuration Manager")
	a.update.settingsDir = filepath.Join(a.update.appDataRoot, "Settings")
	a.update.logsDir = filepath.Join(a.update.appDataRoot, "Logs")
	updateRoot := filepath.Join(a.update.appDataRoot, "Updates")
	a.update.cacheDir = filepath.Join(updateRoot, "Cache")
	a.update.downloadsDir = filepath.Join(updateRoot, "Downloads")
	a.update.readyDir = filepath.Join(updateRoot, "Ready")
	a.update.rollbackDir = filepath.Join(a.update.appDataRoot, "Rollback")
	a.update.healthDir = filepath.Join(a.update.appDataRoot, "Health")
	for _, dir := range []string{a.update.appDataRoot, a.update.settingsDir, a.update.logsDir, a.update.cacheDir, a.update.downloadsDir, a.update.readyDir, a.update.rollbackDir, a.update.healthDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("could not create application data directory %q: %w", dir, err)
		}
	}
	a.update.updateLogPath = filepath.Join(a.update.logsDir, "Updates.log")
	a.update.settingsPath = filepath.Join(a.update.settingsDir, "settings.json")
	a.update.cachePath = filepath.Join(a.update.cacheDir, "update-cache.json")
	a.update.readyPath = filepath.Join(a.update.readyDir, stableExecutableName)
	a.update.rollbackPath = filepath.Join(a.update.rollbackDir, "previous.exe")
	a.update.rollbackMetaPath = filepath.Join(a.update.rollbackDir, "previous.json")
	a.update.healthMarkerPath = filepath.Join(a.update.healthDir, "current.json")
	a.update.state = updateStateResponse{
		Status:           "idle",
		Title:            "Update status",
		Message:          "Installed " + appVersion + ". Update checks begin after startup.",
		InstalledVersion: appVersion,
		CanCheck:         true,
	}
	if cache, err := a.readUpdateCache(); err == nil && validateManifest(cache.Manifest) == nil {
		if when, err := time.Parse(time.RFC3339, cache.LastCheckedUTC); err == nil {
			a.update.lastAttempt = when
		}
		a.applyManifestState(cache.Manifest, false)
		if cmp, _ := compareVersions(appVersion, cache.Manifest.Version); cmp < 0 {
			if verifyUpdateFile(a.update.readyPath, *cache.Manifest) == nil {
				a.update.readyVersion = cache.Manifest.Version
				a.update.readySHA256 = strings.ToLower(cache.Manifest.Asset.SHA256)
				a.update.state.Downloaded = true
				a.update.state.CanInstall = true
				a.update.state.Status = "ready"
				a.update.state.Title = "Update ready"
				a.update.state.Message = fmt.Sprintf("Installed %s. Version %s is downloaded and verified.", appVersion, cache.Manifest.Version)
			}
		}
	}
	a.cleanupStaleUpdateFiles()
	if data, err := os.ReadFile(filepath.Join(a.update.healthDir, "last-update.json")); err == nil {
		var result map[string]string
		if json.Unmarshal(data, &result) == nil && (result["status"] == "rollback" || result["status"] == "failed") {
			a.update.state.Notice = result["message"]
		}
	}
	return nil
}

func (a *application) updateSnapshot() updateStateResponse {
	a.update.mu.Lock()
	defer a.update.mu.Unlock()
	state := a.update.state
	state.Notes = append([]string(nil), state.Notes...)
	return state
}

func (a *application) logUpdate(format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05.000") + "  " + fmt.Sprintf(format, args...) + "\r\n"
	_ = os.MkdirAll(a.update.logsDir, 0o755)
	file, err := os.OpenFile(a.update.updateLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = file.WriteString(line)
		_ = file.Close()
	}
}

// Only recognized application-owned download names are removed. Unknown files remain untouched.
func (a *application) cleanupStaleUpdateFiles() {
	entries, _ := os.ReadDir(a.update.downloadsDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := parseVersion(entry.Name()); err != nil {
			continue
		}
		dir := filepath.Join(a.update.downloadsDir, entry.Name())
		_ = os.Remove(filepath.Join(dir, stableExecutableName+".download"))
		_ = os.Remove(dir) // removes empty directories only
	}
	if info, err := os.Stat(a.update.readyPath); err == nil && !info.IsDir() {
		if validateDownloadedExecutable(a.update.readyPath, appVersion) == nil {
			_ = os.Remove(a.update.readyPath)
		}
	}
}

type applicationSettings struct {
	OutputRoot string `json:"output_root"`
}

func (a *application) loadSavedOutputRoot() string {
	data, err := os.ReadFile(a.update.settingsPath)
	if err != nil {
		return ""
	}
	var settings applicationSettings
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	value := strings.TrimSpace(settings.OutputRoot)
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func (a *application) saveApplicationSettings(outputRoot string) {
	value := strings.TrimSpace(outputRoot)
	if value == "" || !filepath.IsAbs(value) {
		return
	}
	settings := applicationSettings{OutputRoot: filepath.Clean(value)}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return
	}
	temp := a.update.settingsPath + ".tmp"
	if os.WriteFile(temp, data, 0o644) == nil {
		_ = atomicReplaceFile(temp, a.update.settingsPath)
	}
}

func (a *application) readUpdateCache() (*updateCache, error) {
	data, err := os.ReadFile(a.update.cachePath)
	if err != nil {
		return nil, err
	}
	var cache updateCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if cache.FailureCount < 0 {
		cache.FailureCount = 0
	}
	if cache.FailureCount > 8 {
		cache.FailureCount = 8
	}
	return &cache, nil
}

func (a *application) writeUpdateCache(cache *updateCache) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	temp := a.update.cachePath + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err == nil {
		_ = atomicReplaceFile(temp, a.update.cachePath)
	}
}

func parseVersion(value string) ([3]int, error) {
	var result [3]int
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 {
		return result, fmt.Errorf("invalid semantic version %q", value)
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') || strings.Trim(part, "0123456789") != "" {
			return result, fmt.Errorf("invalid semantic version %q", value)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 65535 {
			return result, fmt.Errorf("invalid semantic version %q", value)
		}
		result[i] = n
	}
	return result, nil
}

func compareVersions(left, right string) (int, error) {
	l, err := parseVersion(left)
	if err != nil {
		return 0, err
	}
	r, err := parseVersion(right)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if l[i] < r[i] {
			return -1, nil
		}
		if l[i] > r[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func isAllowedUpdateHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	allowed := []string{"github.com", "release-assets.githubusercontent.com", "github-releases.githubusercontent.com", "objects.githubusercontent.com"}
	for _, item := range allowed {
		if host == item {
			return true
		}
	}
	return false
}

func validateHTTPSURL(rawURL string) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Port() != "" && req.URL.Port() != "443") || req.URL.Fragment != "" {
		return errors.New("update URLs must use HTTPS")
	}
	if !isAllowedUpdateHost(req.URL.Hostname()) {
		return fmt.Errorf("update host %q is not allowlisted", req.URL.Hostname())
	}
	return nil
}

func validateManifest(manifest *updateManifest) error {
	if manifest == nil {
		return errors.New("update manifest is empty")
	}
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported update manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.AppID != applicationID {
		return fmt.Errorf("update manifest application identity %q does not match %q", manifest.AppID, applicationID)
	}
	if _, err := parseVersion(manifest.Version); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.ReleaseURL) == "" {
		return errors.New("update manifest release URL is missing")
	}
	if manifest.ReleaseURL != repositoryURL+"/releases/tag/v"+manifest.Version {
		return errors.New("release URL does not match the expected repository/version")
	}
	if err := validateHTTPSURL(manifest.ReleaseURL); err != nil {
		return fmt.Errorf("release URL validation failed: %w", err)
	}
	if manifest.Asset.Name != stableExecutableName {
		return fmt.Errorf("unexpected update asset %q", manifest.Asset.Name)
	}
	if manifest.Asset.URL != repositoryURL+"/releases/download/v"+manifest.Version+"/"+stableExecutableName {
		return errors.New("asset URL does not match the expected repository/version")
	}
	if err := validateHTTPSURL(manifest.Asset.URL); err != nil {
		return fmt.Errorf("asset URL validation failed: %w", err)
	}
	hash := strings.ToLower(strings.TrimSpace(manifest.Asset.SHA256))
	if len(hash) != 64 {
		return errors.New("update asset SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return errors.New("update asset SHA-256 is invalid")
	}
	if manifest.Asset.Size <= 0 || manifest.Asset.Size > maximumUpdateBytes {
		return fmt.Errorf("update asset size %d is outside the allowed range", manifest.Asset.Size)
	}
	return nil
}

func newUpdateHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return errors.New("too many update redirects")
			}
			if validateHTTPSURL(req.URL.String()) != nil {
				return fmt.Errorf("redirected to untrusted update URL %q", req.URL.String())
			}
			return nil
		},
	}
}

func parseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil && seconds > 0 && seconds < int64((1<<63-1)/time.Second) {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if duration := time.Until(when); duration > 0 {
			return duration
		}
	}
	return 0
}

func (a *application) updateClient() *http.Client {
	if a.update.httpClient != nil {
		return a.update.httpClient
	}
	return newUpdateHTTPClient()
}

func (a *application) beginUpdateCheck(manual bool, focus bool) error {
	a.mu.Lock()
	if a.installing || (!manual && !a.uiReady) {
		a.mu.Unlock()
		return nil
	}
	a.update.mu.Lock()
	a.mu.Unlock()
	now := time.Now().UTC()
	defer a.update.mu.Unlock()
	if a.update.checkInFlight || a.update.downloadInFlight {
		return nil
	}
	if manual && !a.update.lastManual.IsZero() && now.Sub(a.update.lastManual) < manualUpdateCooldown {
		return nil
	}
	cache, _ := a.readUpdateCache()
	// Manual actions must honor server Retry-After and shared-IP rate limits too.
	if cache != nil {
		if until, err := time.Parse(time.RFC3339, cache.BackoffUntilUTC); err == nil && now.Before(until) {
			a.update.state.Status = "cooldown"
			a.update.state.Title = "Update checks paused"
			a.update.state.Message = fmt.Sprintf("Installed %s. Check unavailable; retry after %s.", appVersion, until.Local().Format("15:04:05"))
			return nil
		}
	}
	if !manual {
		minimum := updateCheckInterval
		if focus {
			minimum = updateFocusInterval
		}
		if !a.update.lastAttempt.IsZero() && now.Sub(a.update.lastAttempt) < minimum {
			return nil
		}
	}
	etag := ""
	if cache != nil && validateManifest(cache.Manifest) == nil {
		etag = cache.ETag
	}
	a.update.checkInFlight = true
	a.update.lastAttempt = now
	if manual {
		a.update.lastManual = now
	}
	a.update.state.Checking = true
	a.update.state.Status = "checking"
	a.update.state.Title = "Checking for updates"
	a.update.state.Message = "Installed " + appVersion
	a.update.state.CanCheck = false
	go a.performUpdateCheck(etag)
	return nil
}

func decodeUpdateManifest(data []byte) (*updateManifest, error) {
	// v3.2.0's published manifest uses a UTF-8 BOM. Accept it without weakening JSON validation.
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var parsed updateManifest
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	if err := validateManifest(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func serverRetryDelay(response *http.Response) time.Duration {
	retry := parseRetryAfter(response.Header.Get("Retry-After"))
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusForbidden {
		if seconds, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			if reset := time.Until(time.Unix(seconds, 0)); reset > retry {
				retry = reset
			}
		}
	}
	return retry
}

func (a *application) performUpdateCheck(etag string) {
	resultCache, _ := a.readUpdateCache()
	if resultCache == nil {
		resultCache = &updateCache{}
	}
	request, err := http.NewRequest(http.MethodGet, updateFeedURL, nil)
	if err == nil {
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", applicationID+"/"+appVersion)
		if etag != "" {
			request.Header.Set("If-None-Match", etag)
		}
	}
	var manifest *updateManifest
	var retryAfter time.Duration
	if err == nil {
		response, requestErr := a.updateClient().Do(request)
		if requestErr != nil {
			err = requestErr
		} else {
			defer response.Body.Close()
			retryAfter = serverRetryDelay(response)
			switch response.StatusCode {
			case http.StatusNotModified:
				manifest = resultCache.Manifest
				err = validateManifest(manifest)
				if err == nil && response.Header.Get("ETag") != "" {
					resultCache.ETag = response.Header.Get("ETag")
				}
			case http.StatusOK:
				data, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
				if readErr != nil {
					err = readErr
				} else if len(data) > 1024*1024 {
					err = errors.New("update manifest exceeds 1 MiB")
				} else {
					manifest, err = decodeUpdateManifest(data)
					if err == nil {
						resultCache.Manifest = manifest
						resultCache.ETag = response.Header.Get("ETag")
					}
				}
			default:
				err = fmt.Errorf("update feed returned HTTP %d", response.StatusCode)
			}
		}
	}
	now := time.Now().UTC()
	if err != nil {
		resultCache.FailureCount = minInt(resultCache.FailureCount+1, 8)
		backoff := time.Duration(1<<minInt(resultCache.FailureCount, 6)) * time.Minute
		if retryAfter > backoff {
			backoff = retryAfter
		}
		resultCache.BackoffUntilUTC = now.Add(backoff).Format(time.RFC3339)
		resultCache.LastCheckedUTC = now.Format(time.RFC3339)
		a.writeUpdateCache(resultCache)
		a.update.mu.Lock()
		a.update.checkInFlight = false
		a.update.state.Checking = false
		a.update.state.Status = "error"
		a.update.state.Title = "Update check unavailable"
		var netErr net.Error
		if errors.As(err, &netErr) {
			a.update.state.Status = "offline"
			a.update.state.Title = "Offline or connection unavailable"
		}
		a.update.state.Message = "Installed " + appVersion + ". " + err.Error()
		a.update.state.CanCheck = true
		a.update.state.LastCheckUnixMs = now.UnixMilli()
		a.update.mu.Unlock()
		a.logUpdate("Update check failed: %v; next attempt after %s", err, resultCache.BackoffUntilUTC)
		return
	}
	resultCache.FailureCount = 0
	resultCache.BackoffUntilUTC = ""
	resultCache.LastCheckedUTC = now.Format(time.RFC3339)
	a.writeUpdateCache(resultCache)
	// Retain single-flight ownership until the successful result is fully applied.
	a.applyManifestState(manifest, true)
	a.update.mu.Lock()
	a.update.checkInFlight = false
	a.update.state.Checking = false
	a.update.state.CanCheck = true
	a.update.state.LastCheckUnixMs = now.UnixMilli()
	a.update.mu.Unlock()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *application) applyManifestState(manifest *updateManifest, logResult bool) {
	if err := validateManifest(manifest); err != nil {
		a.update.mu.Lock()
		a.update.state.Status = "error"
		a.update.state.Title = "Update check unavailable"
		a.update.state.Message = err.Error()
		a.update.state.CanCheck = true
		a.update.mu.Unlock()
		return
	}
	cmp, err := compareVersions(appVersion, manifest.Version)
	if err != nil {
		return
	}
	a.update.mu.Lock()
	a.update.currentManifest = manifest
	a.update.state.LatestVersion = manifest.Version
	a.update.state.ReleaseURL = manifest.ReleaseURL
	a.update.state.Available = cmp < 0
	if cmp < 0 {
		a.update.state.Notes = append([]string(nil), manifest.Notes...)
		a.update.state.ReadMore = manifest.ReadMore
	} else {
		a.update.state.Notes = nil
		a.update.state.ReadMore = false
	}
	a.update.state.CanDownload = cmp < 0 && !a.update.downloadInFlight
	a.update.state.CanInstall = cmp < 0 && a.update.readyVersion == manifest.Version && existingFile(a.update.readyPath) != ""
	a.update.state.Downloaded = a.update.state.CanInstall
	a.update.state.Progress = 0
	if cmp < 0 {
		a.update.state.Status = "available"
		a.update.state.Title = "Update available"
		a.update.state.Message = fmt.Sprintf("Installed %s. Latest %s.", appVersion, manifest.Version)
	} else {
		a.update.state.Status = "current"
		a.update.state.Title = "Up to date"
		a.update.state.Message = "Installed " + appVersion + "."
		if !logResult {
			a.update.state.Status = "idle"
			a.update.state.Title = "Update status not yet verified"
			a.update.state.Message = "Installed " + appVersion + ". Waiting for a successful online check."
		}
		a.update.state.CanDownload = false
		a.update.state.CanInstall = false
		a.update.state.Downloaded = false
	}
	a.update.mu.Unlock()
	if logResult {
		a.logUpdate("Update check completed. Installed=%s latest=%s available=%t", appVersion, manifest.Version, cmp < 0)
	}
}

func (a *application) beginUpdateDownload() error {
	a.mu.Lock()
	if a.installing {
		a.mu.Unlock()
		return errors.New("An update is already being installed")
	}
	a.update.mu.Lock()
	a.mu.Unlock()
	if a.update.downloadInFlight || a.update.checkInFlight {
		a.update.mu.Unlock()
		return nil
	}
	manifest := a.update.currentManifest
	if manifest == nil {
		a.update.mu.Unlock()
		return errors.New("no update manifest is loaded")
	}
	if err := validateManifest(manifest); err != nil {
		a.update.mu.Unlock()
		return err
	}
	cmp, err := compareVersions(appVersion, manifest.Version)
	if err != nil || cmp >= 0 {
		a.update.mu.Unlock()
		return errors.New("no newer update is available")
	}
	a.update.downloadInFlight = true
	a.update.state.Downloading = true
	a.update.state.CanDownload = false
	a.update.state.CanInstall = false
	a.update.state.Status = "downloading"
	a.update.state.Title = "Downloading " + manifest.Version
	a.update.state.Message = "The update will not install or restart until you choose Install and restart."
	a.update.state.Progress = 0
	a.update.mu.Unlock()
	go a.performUpdateDownload(*manifest)
	return nil
}

func (a *application) performUpdateDownload(manifest updateManifest) {
	versionDir := filepath.Join(a.update.downloadsDir, manifest.Version)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		a.finishUpdateDownloadError(err)
		return
	}
	downloadPath := filepath.Join(versionDir, stableExecutableName+".download")
	defer func() { _ = os.Remove(downloadPath); _ = os.Remove(versionDir) }()
	client := a.updateClient()
	downloadClient := *client
	downloadClient.Timeout = 5 * time.Minute
	client = &downloadClient
	request, _ := http.NewRequest(http.MethodGet, manifest.Asset.URL, nil)
	request.Header.Set("User-Agent", applicationID+"/"+appVersion)
	response, err := client.Do(request)
	if err != nil {
		a.finishUpdateDownloadError(err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		a.finishUpdateDownloadError(fmt.Errorf("update download returned HTTP %d %s", response.StatusCode, response.Status))
		return
	}
	if response.ContentLength > maximumUpdateBytes {
		a.finishUpdateDownloadError(errors.New("update download exceeded the maximum allowed size"))
		return
	}
	output, err := os.Create(downloadPath)
	if err != nil {
		a.finishUpdateDownloadError(err)
		return
	}
	hasher := sha256.New()
	var written int64
	buffer := make([]byte, 128*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			written += int64(n)
			if written > maximumUpdateBytes {
				_ = output.Close()
				_ = os.Remove(downloadPath)
				a.finishUpdateDownloadError(errors.New("update download exceeded the maximum allowed size"))
				return
			}
			if _, err := output.Write(buffer[:n]); err != nil {
				_ = output.Close()
				a.finishUpdateDownloadError(err)
				return
			}
			_, _ = hasher.Write(buffer[:n])
			progress := 0
			if manifest.Asset.Size > 0 {
				progress = int((written * 100) / manifest.Asset.Size)
				if progress > 100 {
					progress = 100
				}
			}
			a.update.mu.Lock()
			a.update.state.Progress = progress
			a.update.mu.Unlock()
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				_ = output.Close()
				a.finishUpdateDownloadError(readErr)
				return
			}
			break
		}
	}
	if err := output.Close(); err != nil {
		a.finishUpdateDownloadError(err)
		return
	}
	if written != manifest.Asset.Size {
		a.finishUpdateDownloadError(fmt.Errorf("downloaded size %d did not match expected size %d", written, manifest.Asset.Size))
		return
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualHash, manifest.Asset.SHA256) {
		a.finishUpdateDownloadError(errors.New("downloaded SHA-256 did not match the trusted manifest"))
		return
	}
	if err := validateDownloadedExecutable(downloadPath, manifest.Version); err != nil {
		a.finishUpdateDownloadError(err)
		return
	}
	_ = os.Remove(a.update.readyPath)
	if err := copyFile(downloadPath, a.update.readyPath); err != nil {
		a.finishUpdateDownloadError(err)
		return
	}
	if err := verifyUpdateFile(a.update.readyPath, manifest); err != nil {
		a.finishUpdateDownloadError(err)
		return
	}
	a.update.mu.Lock()
	a.update.downloadInFlight = false
	a.update.readyVersion = manifest.Version
	a.update.readySHA256 = strings.ToLower(manifest.Asset.SHA256)
	a.update.state.Downloading = false
	a.update.state.Downloaded = true
	a.update.state.CanInstall = true
	a.update.state.CanDownload = true
	a.update.state.Progress = 100
	a.update.state.Status = "ready"
	a.update.state.Title = "Update ready"
	a.update.state.Message = manifest.Version + " is downloaded and verified."
	a.update.mu.Unlock()
	a.logUpdate("Update %s downloaded and verified. SHA-256=%s", manifest.Version, actualHash)
}

func (a *application) finishUpdateDownloadError(err error) {
	a.update.mu.Lock()
	a.update.downloadInFlight = false
	a.update.state.Downloading = false
	a.update.state.Status = "error"
	a.update.state.Title = "Update download failed"
	a.update.state.Message = err.Error()
	a.update.state.CanDownload = a.update.currentManifest != nil
	a.update.state.CanInstall = false
	a.update.mu.Unlock()
	a.logUpdate("Update download failed: %v", err)
}

func verifyUpdateFile(path string, manifest updateManifest) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() != manifest.Asset.Size {
		return errors.New("update size does not match the manifest")
	}
	hash, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(hash, manifest.Asset.SHA256) {
		return errors.New("update SHA-256 does not match the trusted manifest")
	}
	return validateDownloadedExecutable(path, manifest.Version)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func randomToken(bytesCount int) (string, error) {
	data := make([]byte, bytesCount)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func (a *application) installReadyUpdate() error {
	a.mu.Lock()
	running := a.state.Running || a.installing || a.criticalOperations > 0
	if !running {
		a.installing = true
	}
	a.mu.Unlock()
	if running {
		return errors.New("a collection is running; finish or cancel it before installing an update")
	}
	started := false
	defer func() {
		if !started {
			a.mu.Lock()
			a.installing = false
			a.mu.Unlock()
		}
	}()
	a.update.mu.Lock()
	busy := a.update.downloadInFlight || a.update.checkInFlight
	manifest := a.update.currentManifest
	readyVersion := a.update.readyVersion
	readyHash := a.update.readySHA256
	readyPath := a.update.readyPath
	healthPath := a.update.healthMarkerPath
	rollbackPath := a.update.rollbackPath
	rollbackMeta := a.update.rollbackMetaPath
	logPath := a.update.updateLogPath
	a.update.mu.Unlock()
	if busy {
		return errors.New("Wait for the current update check or download to finish")
	}
	if manifest == nil || validateManifest(manifest) != nil || readyVersion == "" || readyVersion != manifest.Version {
		return errors.New("no verified update is ready to install")
	}
	if existingFile(readyPath) == "" {
		return errors.New("the verified update file is missing")
	}
	if err := validateDownloadedExecutable(readyPath, readyVersion); err != nil {
		return err
	}
	actual, err := fileSHA256(readyPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, readyHash) {
		return errors.New("the ready update hash changed after verification")
	}
	if err := verifyUpdateFile(readyPath, *manifest); err != nil {
		return err
	}
	currentPath, err := os.Executable()
	if err != nil {
		return err
	}
	currentPath, _ = filepath.Abs(currentPath)
	token, err := randomToken(16)
	if err != nil {
		return err
	}
	args := []string{
		"--update-helper",
		"--parent-pid", strconv.Itoa(os.Getpid()),
		"--target", currentPath,
		"--expected-version", readyVersion,
		"--expected-sha256", strings.ToLower(readyHash),
		"--old-version", appVersion,
		"--health-marker", healthPath,
		"--health-token", token,
		"--rollback", rollbackPath,
		"--rollback-meta", rollbackMeta,
		"--log", logPath,
	}
	command := exec.Command(readyPath, args...)
	command.Dir = filepath.Dir(currentPath)
	a.saveCurrentWindowGeometry()
	if err := command.Start(); err != nil {
		return fmt.Errorf("could not start the update installer: %w", err)
	}
	started = true
	a.update.mu.Lock()
	a.update.state.CanInstall = false
	a.update.state.CanDownload = false
	a.update.state.CanCheck = false
	a.update.state.Status = "installing"
	a.update.state.Title = "Installing update"
	a.update.state.Message = "Installing " + readyVersion + " and restarting..."
	a.update.mu.Unlock()
	a.logUpdate("Started update installer PID=%d target=%s version=%s", command.Process.Pid, currentPath, readyVersion)
	go func() {
		time.Sleep(800 * time.Millisecond)
		a.stopServer()
	}()
	return nil
}

func closeApplicationWindow() {
	hwnd := findApplicationWindow()
	if hwnd != 0 {
		procPostMessageWUpdate.Call(hwnd, wmClose, 0, 0)
	}
}

func processStillRunning(pid int) bool {
	handle, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return false
	}
	defer procCloseHandle.Call(handle)
	var code uint32
	ok, _, _ := procGetExitCodeProcess.Call(handle, uintptr(unsafePointer(&code)))
	if ok == 0 {
		return false
	}
	return code == stillActive
}

// unsafePointer is kept tiny and local so the rest of the updater does not rely on unsafe.
func unsafePointer(value *uint32) uintptr {
	return uintptr(unsafe.Pointer(value))
}

func helperLog(path, format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05.000") + "  " + fmt.Sprintf(format, args...) + "\r\n"
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = file.WriteString(line)
		_ = file.Close()
	}
}

func atomicReplaceFile(source, target string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := procMoveFileExW.Call(
		uintptr(unsafe.Pointer(sourcePtr)),
		uintptr(unsafe.Pointer(targetPtr)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if result == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return errors.New("MoveFileExW failed")
	}
	return nil
}

func restoreRollbackExecutable(rollbackPath, target string) error {
	if existingFile(rollbackPath) == "" {
		return errors.New("no rollback executable is available")
	}
	metadataPath := filepath.Join(filepath.Dir(rollbackPath), "previous.json")
	var meta rollbackMetadata
	data, err := os.ReadFile(metadataPath)
	if err != nil || json.Unmarshal(data, &meta) != nil || len(meta.SHA256) != 64 {
		return errors.New("rollback checksum metadata is missing or malformed")
	}
	hash, err := fileSHA256(rollbackPath)
	if err != nil || !strings.EqualFold(hash, meta.SHA256) {
		return errors.New("rollback executable checksum mismatch")
	}
	temp := filepath.Join(filepath.Dir(target), fmt.Sprintf(".%s.rollback-%d.tmp.exe", strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)), os.Getpid()))
	_ = os.Remove(temp)
	if err := copyFile(rollbackPath, temp); err != nil {
		return err
	}
	if err := atomicReplaceFile(temp, target); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func runUpdateHelper(args []string) int {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") && i+1 < len(args) {
			values[args[i]] = args[i+1]
			i++
		}
	}
	parentPID, _ := strconv.Atoi(values["--parent-pid"])
	target := values["--target"]
	expectedVersion := values["--expected-version"]
	expectedHash := strings.ToLower(values["--expected-sha256"])
	oldVersion := values["--old-version"]
	healthMarkerPath := values["--health-marker"]
	healthToken := values["--health-token"]
	rollbackPath := values["--rollback"]
	rollbackMetaPath := values["--rollback-meta"]
	logPath := values["--log"]

	if healthToken == "" || target == "" || expectedVersion == "" || expectedHash == "" || healthMarkerPath == "" || rollbackPath == "" || rollbackMetaPath == "" || logPath == "" {
		return 2
	}
	stagedPath, err := os.Executable()
	if err != nil {
		helperLog(logPath, "Installer could not determine staged path: %v", err)
		return 3
	}
	helperLog(logPath, "Installer started. ParentPID=%d target=%s staged=%s version=%s", parentPID, target, stagedPath, expectedVersion)
	priorProfile := previousBrowserProfile(parentPID, target)
	if priorProfile != "" {
		time.Sleep(150 * time.Millisecond) // allow the install POST response to finish
		if err := closeScopedBrowser(priorProfile); err != nil {
			helperLog(logPath, "Previous browser close: %v", err)
		} else {
			helperLog(logPath, "Previous app browser session closed")
		}
	}
	deadline := time.Now().Add(45 * time.Second)
	for parentPID > 0 && processStillRunning(parentPID) && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	if parentPID > 0 && processStillRunning(parentPID) {
		helperLog(logPath, "Parent process did not exit before timeout.")
		return 4
	}
	if parentPID > 0 {
		closeOrphanedApplicationBrowsers()
	}
	if err := validateDownloadedExecutable(stagedPath, expectedVersion); err != nil {
		helperLog(logPath, "Staged update validation failed: %v", err)
		return 5
	}
	actualHash, err := fileSHA256(stagedPath)
	if err != nil || !strings.EqualFold(actualHash, expectedHash) {
		helperLog(logPath, "Staged update hash validation failed. actual=%s err=%v", actualHash, err)
		return 6
	}
	restartOriginal := true
	defer func() {
		if restartOriginal && parentPID > 0 && existingFile(target) != "" {
			cmd := exec.Command(target)
			cmd.Dir = filepath.Dir(target)
			_ = cmd.Start()
		}
	}()
	targetDir := filepath.Dir(target)
	tempTarget := filepath.Join(targetDir, fmt.Sprintf(".%s.update-%d.tmp.exe", strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)), os.Getpid()))
	_ = os.Remove(tempTarget)
	defer os.Remove(tempTarget)
	if err := copyFile(stagedPath, tempTarget); err != nil {
		helperLog(logPath, "Could not stage update on target filesystem: %v", err)
		return 7
	}
	if err := validateDownloadedExecutable(tempTarget, expectedVersion); err != nil {
		helperLog(logPath, "Target-filesystem staging validation failed: %v", err)
		_ = os.Remove(tempTarget)
		return 8
	}
	copiedHash, hashErr := fileSHA256(tempTarget)
	if hashErr != nil || !strings.EqualFold(copiedHash, expectedHash) {
		_ = os.Remove(tempTarget)
		helperLog(logPath, "Target filesystem hash mismatch")
		return 8
	}
	if err := os.MkdirAll(filepath.Dir(rollbackPath), 0o755); err != nil {
		helperLog(logPath, "Could not create rollback directory: %v", err)
		_ = os.Remove(tempTarget)
		return 9
	}
	if existingFile(target) == "" {
		_ = os.Remove(tempTarget)
		helperLog(logPath, "Original target is missing")
		return 10
	}
	if existingFile(target) != "" {
		oldHash, err := fileSHA256(target)
		if err != nil {
			helperLog(logPath, "Could not read the installed executable for rollback: %v", err)
			writeInstallResult(healthMarkerPath, "failed", oldVersion, "The update could not prepare a rollback copy because the installed executable was locked or unreadable. The installed file was left unchanged.")
			return 10
		}
		rollbackTemp := rollbackPath + ".tmp"
		defer os.Remove(rollbackTemp)
		if err := copyFile(target, rollbackTemp); err != nil {
			helperLog(logPath, "Could not create rollback copy: %v", err)
			_ = os.Remove(tempTarget)
			return 10
		}
		copiedHash, err := fileSHA256(rollbackTemp)
		if err != nil || !strings.EqualFold(copiedHash, oldHash) {
			helperLog(logPath, "Rollback copy checksum mismatch")
			_ = os.Remove(tempTarget)
			return 10
		}
		if err := atomicReplaceFile(rollbackTemp, rollbackPath); err != nil {
			_ = os.Remove(tempTarget)
			helperLog(logPath, "Could not commit rollback copy: %v", err)
			return 10
		}
		meta := rollbackMetadata{Version: oldVersion, OriginalPath: target, CreatedAtUTC: time.Now().UTC().Format(time.RFC3339), SHA256: oldHash}
		if data, err := json.MarshalIndent(meta, "", "  "); err == nil {
			_ = os.WriteFile(rollbackMetaPath, data, 0o644)
		}
	}
	_ = os.Remove(healthMarkerPath)
	if err := atomicReplaceFile(tempTarget, target); err != nil {
		helperLog(logPath, "Could not atomically replace application: %v", err)
		_ = os.Remove(tempTarget)
		return 11
	}
	launchArgs := []string{"--health-token", healthToken}

	restartOriginal = false
	launch := exec.Command(target, launchArgs...)
	launch.Dir = targetDir
	if err := launch.Start(); err != nil {
		helperLog(logPath, "Could not relaunch updated application: %v", err)
		if restoreErr := restoreRollbackExecutable(rollbackPath, target); restoreErr == nil {
			_ = exec.Command(target).Start()
		} else {
			helperLog(logPath, "Rollback restore after launch failure also failed: %v", restoreErr)
		}
		return 12
	}
	helperLog(logPath, "Updated application launched. PID=%d", launch.Process.Pid)
	healthDeadline := time.Now().Add(45 * time.Second)
	healthy := false
	var healthySince time.Time
	for time.Now().Before(healthDeadline) {
		data, err := os.ReadFile(healthMarkerPath)
		if err == nil {
			var marker healthMarker
			if json.Unmarshal(data, &marker) == nil && marker.AppID == applicationID && marker.Version == expectedVersion && marker.HealthToken == healthToken {
				if marker.VisibleWindowConfirmed && marker.UIReady && marker.PID == launch.Process.Pid && strings.EqualFold(filepath.Clean(marker.ExecutablePath), filepath.Clean(target)) && processStillRunning(marker.PID) {
					if healthySince.IsZero() {
						healthySince = time.Now()
					}
					if time.Since(healthySince) < 2*time.Second {
						time.Sleep(250 * time.Millisecond)
						continue
					}
					healthy = true
					helperLog(logPath, "Healthy startup confirmed. VisibleWindowConfirmed=%t", marker.VisibleWindowConfirmed)
					break
				}
			}
		}
		if !processStillRunning(launch.Process.Pid) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if healthy {
		helperLog(logPath, "Update installed successfully.")
		writeInstallResult(healthMarkerPath, "installed", expectedVersion, "Update installed successfully")

		return 0
	}
	helperLog(logPath, "Healthy startup was not confirmed; rolling back.")
	writeInstallResult(healthMarkerPath, "rollback", oldVersion, "The update did not confirm a healthy visible startup. The previous version was restored.")
	terminateProcessTree(launch.Process.Pid)
	time.Sleep(500 * time.Millisecond)
	if err := restoreRollbackExecutable(rollbackPath, target); err != nil {
		helperLog(logPath, "Rollback restore failed: %v", err)
		return 13
	}
	rollbackLaunch := exec.Command(target)
	rollbackLaunch.Dir = targetDir
	if err := rollbackLaunch.Start(); err != nil {
		helperLog(logPath, "Rollback relaunch failed: %v", err)
		return 16
	}
	helperLog(logPath, "Previous version restored and relaunched. PID=%d", rollbackLaunch.Process.Pid)
	return 17
}

func extractHealthToken(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--health-token" {
			return args[i+1]
		}
	}
	return ""
}

func hasArgument(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

func (a *application) writeHealthMarker(token string, visible bool) {
	if strings.TrimSpace(a.update.healthMarkerPath) == "" {
		return
	}
	executable, _ := os.Executable()
	a.mu.Lock()
	uiReady := a.uiReady
	a.mu.Unlock()
	marker := healthMarker{
		AppID:                  applicationID,
		Version:                appVersion,
		PID:                    os.Getpid(),
		ExecutablePath:         executable,
		HealthToken:            token,
		StartedAtUTC:           time.Now().UTC().Format(time.RFC3339),
		VisibleWindowConfirmed: visible,
		UIReady:                uiReady,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return
	}
	temp := a.update.healthMarkerPath + ".tmp"
	if os.WriteFile(temp, data, 0o644) == nil {
		_ = atomicReplaceFile(temp, a.update.healthMarkerPath)
	}
}

func (a *application) ownApplicationWindow() uintptr {
	a.mu.Lock()
	profile := a.browserProfileDirectory
	a.mu.Unlock()
	if profile == "" {
		return 0
	}
	for _, window := range scopedBrowserWindows(profile) {
		if strings.Contains(strings.ToLower(windowText(window)), strings.ToLower(appName)) {
			return window
		}
	}
	return 0
}
func (a *application) closeOwnApplicationWindow() {
	a.mu.Lock()
	profile := a.browserProfileDirectory
	a.mu.Unlock()
	if profile != "" {
		_ = closeScopedBrowser(profile)
	}
}

func (a *application) confirmHealthyWindowAndMark(token string) {
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		ready := a.uiReady
		a.mu.Unlock()
		if hwnd := a.ownApplicationWindow(); ready && hwnd != 0 {
			a.startWindowPersistence(hwnd)
			a.writeHealthMarker(token, true)
			a.logUpdate("Healthy startup confirmed: visible owned window and rendered interface")
			go a.cleanupAfterHealthyUpdate()
			return
		}
		select {
		case <-a.shutdown:
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	a.logUpdate("Healthy startup was not confirmed")
}

func (a *application) handleUIReady(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "POST is required"})
		return
	}
	a.mu.Lock()
	a.uiReady = true
	a.mu.Unlock()
	a.startUpdateScheduler()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

func (a *application) startUpdateScheduler() {
	a.update.schedulerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			a.runUpdateScheduler(ticker.C)
		}()
	})
}

func (a *application) runUpdateScheduler(ticks <-chan time.Time) {
	a.update.mu.Lock()
	a.update.lastAttempt = time.Time{}
	a.update.mu.Unlock()
	// Called only after the authenticated UI has painted; network work runs separately.
	_ = a.beginUpdateCheck(false, false)
	for {
		select {
		case <-ticks:
			_ = a.beginUpdateCheck(false, false)
		case <-a.shutdown:
			return
		}
	}
}

func (a *application) beginCriticalOperation() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.installing {
		return false
	}
	a.criticalOperations++
	return true
}
func (a *application) endCriticalOperation() { a.mu.Lock(); a.criticalOperations--; a.mu.Unlock() }

func writeInstallResult(healthPath, status, version, message string) {
	data, _ := json.Marshal(map[string]string{"status": status, "version": version, "message": message, "time": time.Now().UTC().Format(time.RFC3339)})
	path := filepath.Join(filepath.Dir(healthPath), "last-update.json")
	if os.WriteFile(path+".tmp", data, 0600) == nil {
		_ = atomicReplaceFile(path+".tmp", path)
	}
}

// Windows locks the helper's executable while it runs. Retry owned ready-file
// cleanup after healthy confirmation, letting that helper finish first.
func (a *application) cleanupAfterHealthyUpdate() {
	for i := 0; i < 90; i++ {
		if validateDownloadedExecutable(a.update.readyPath, appVersion) != nil {
			return
		}
		if os.Remove(a.update.readyPath) == nil {
			a.logUpdate("Cleaned the completed update helper executable")
			return
		}
		select {
		case <-a.shutdown:
			return
		case <-time.After(time.Second):
		}
	}
}
