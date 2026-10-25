//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testApp(t *testing.T) *application {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	a := &application{shutdown: make(chan struct{}), token: "test-token", uiReady: true}
	if err := a.initializeUpdater(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(a.shutdown) })
	return a
}
func testManifest(version string) updateManifest {
	return updateManifest{SchemaVersion: 1, AppID: applicationID, Version: version, ReleaseURL: repositoryURL + "/releases/tag/v" + version, Asset: updateAsset{Name: stableExecutableName, URL: repositoryURL + "/releases/download/v" + version + "/" + stableExecutableName, SHA256: strings.Repeat("a", 64), Size: 2048}}
}
func response(code int, body []byte, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}
func waitCheck(t *testing.T, a *application) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		a.update.mu.Lock()
		busy := a.update.checkInFlight
		a.update.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("update check did not finish")
}
func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("build", stableExecutableName))
	if err != nil {
		t.Fatal("Build the GUI executable before tests: ", err)
	}
	return data
}
func setTransport(a *application, f roundTripFunc) {
	a.update.httpClient = &http.Client{Transport: f, CheckRedirect: newUpdateHTTPClient().CheckRedirect}
}

func TestManifestEncodingNegativeControl(t *testing.T) {
	m := testManifest(appVersion)
	body, _ := json.Marshal(m)
	body = append([]byte{0xef, 0xbb, 0xbf}, body...)
	var old updateManifest
	if json.Unmarshal(body, &old) == nil {
		t.Fatal("negative control no longer reproduces the v3.2.0 BOM failure")
	}
	if _, err := decodeUpdateManifest(body); err != nil {
		t.Fatal(err)
	}
}
func TestCachedSameVersionIsNeverCurrentBeforeNetworkSuccess(t *testing.T) {
	a := testApp(t)
	m := testManifest(appVersion)
	a.applyManifestState(&m, false)
	if a.updateSnapshot().Status == "current" {
		t.Fatal("cached state incorrectly claims up to date")
	}
	setTransport(a, func(r *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("offline")}
	})
	_ = a.beginUpdateCheck(true, false)
	waitCheck(t, a)
	if state := a.updateSnapshot(); state.Status != "offline" || strings.Contains(state.Title, "Up to date") {
		t.Fatalf("bad offline state: %+v", state)
	}
}
func TestConditionalRequestsAndETagPoisoning(t *testing.T) {
	a := testApp(t)
	m := testManifest(appVersion)
	body, _ := json.Marshal(m)
	var count int32
	setTransport(a, func(r *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&count, 1)
		if n == 1 {
			return response(200, body, http.Header{"Etag": []string{"good"}}), nil
		}
		if r.Header.Get("If-None-Match") != "good" {
			t.Error("cached ETag missing")
		}
		if n == 2 {
			return response(304, nil, nil), nil
		}
		return response(500, []byte("failure"), http.Header{"Etag": []string{"poison"}}), nil
	})
	for i := 0; i < 3; i++ {
		a.update.lastManual = time.Time{}
		_ = a.beginUpdateCheck(true, false)
		waitCheck(t, a)
	}
	cache, err := a.readUpdateCache()
	if err != nil || cache.ETag != "good" {
		t.Fatalf("failed response poisoned ETag: %+v %v", cache, err)
	}
	if a.updateSnapshot().Status == "current" {
		t.Fatal("HTTP failure claims current")
	}
}
func TestRateLimitBackoffIncludesManualChecks(t *testing.T) {
	for _, status := range []int{403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a := testApp(t)
			var requests int32
			setTransport(a, func(r *http.Request) (*http.Response, error) {
				atomic.AddInt32(&requests, 1)
				return response(status, nil, http.Header{"Retry-After": []string{"600"}, "X-Ratelimit-Reset": []string{time.Now().Add(15 * time.Minute).Format("invalid")}}), nil
			})
			_ = a.beginUpdateCheck(true, false)
			waitCheck(t, a)
			cache, _ := a.readUpdateCache()
			until, _ := time.Parse(time.RFC3339, cache.BackoffUntilUTC)
			if time.Until(until) < 599*time.Second {
				t.Fatal("Retry-After was shortened")
			}
			a.update.lastManual = time.Time{}
			_ = a.beginUpdateCheck(true, false)
			_ = a.beginUpdateCheck(false, true)
			if atomic.LoadInt32(&requests) != 1 {
				t.Fatal("manual or focus check bypassed backoff")
			}
		})
	}
	r := response(403, nil, http.Header{"X-Ratelimit-Reset": []string{time.Now().Add(20 * time.Minute).Format("invalid")}})
	r.Header.Set("X-RateLimit-Reset", fmtUnix(time.Now().Add(20*time.Minute)))
	if serverRetryDelay(r) < 19*time.Minute {
		t.Fatal("GitHub shared-IP reset ignored")
	}
}
func fmtUnix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
func TestCheckSingleFlightAndCooldown(t *testing.T) {
	a := testApp(t)
	gate := make(chan struct{})
	entered := make(chan struct{})
	var requests int32
	m := testManifest(appVersion)
	body, _ := json.Marshal(m)
	setTransport(a, func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		close(entered)
		<-gate
		return response(200, body, nil), nil
	})
	_ = a.beginUpdateCheck(true, false)
	<-entered
	for i := 0; i < 20; i++ {
		_ = a.beginUpdateCheck(true, false)
		_ = a.beginUpdateCheck(false, true)
	}
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatal("overlapping update checks")
	}
	close(gate)
	waitCheck(t, a)
	_ = a.beginUpdateCheck(true, false)
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatal("manual cooldown ignored")
	}
}
func TestNativeSchedulerAndFocusGate(t *testing.T) {
	a := testApp(t)
	m := testManifest(appVersion)
	body, _ := json.Marshal(m)
	calls := make(chan struct{}, 5)
	setTransport(a, func(r *http.Request) (*http.Response, error) {
		calls <- struct{}{}
		return response(200, body, nil), nil
	})
	ticks := make(chan time.Time)
	go a.runUpdateScheduler(ticks)
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("startup did not check")
	}
	waitCheck(t, a)
	a.update.mu.Lock()
	a.update.lastAttempt = time.Now().Add(-updateCheckInterval - time.Second)
	a.update.mu.Unlock()
	ticks <- time.Now()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("five-minute tick not bound")
	}
	waitCheck(t, a)
	a.update.mu.Lock()
	a.update.lastAttempt = time.Now().Add(-updateFocusInterval - time.Second)
	a.update.mu.Unlock()
	_ = a.beginUpdateCheck(false, true)
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("focus check not bound")
	}
	waitCheck(t, a)
	_ = a.beginUpdateCheck(false, true)
	select {
	case <-calls:
		t.Fatal("focus cooldown ignored")
	case <-time.After(20 * time.Millisecond):
	}
	if updateCheckInterval != 5*time.Minute || updateFocusInterval != 2*time.Minute {
		t.Fatal("production intervals changed")
	}
}
func TestDownloadStagingAndRejections(t *testing.T) {
	bytesGood := fixture(t)
	for _, kind := range []string{"valid", "wrong-hash", "wrong-version", "wrong-application", "truncated", "http-error"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			m := testManifest(appVersion)
			payload := append([]byte(nil), bytesGood...)
			switch kind {
			case "wrong-version":
				m.Version = "99.0.0"
			case "wrong-application":
				payload = bytes.ReplaceAll(payload, []byte("SICM_BINARY_ID|Server-Inventory-Configuration-Manager|"), []byte("XXXX_BINARY_ID|Server-Inventory-Configuration-Manager|"))
			case "truncated":
				payload = payload[:4096]
			}
			hash := sha256.Sum256(payload)
			m.Asset.SHA256 = hex.EncodeToString(hash[:])
			m.Asset.Size = int64(len(payload))
			if kind == "wrong-hash" {
				m.Asset.SHA256 = strings.Repeat("0", 64)
			}
			setTransport(a, func(r *http.Request) (*http.Response, error) {
				if kind == "http-error" {
					return response(500, nil, nil), nil
				}
				return response(200, payload, nil), nil
			})
			a.update.currentManifest = &m
			a.performUpdateDownload(m)
			state := a.updateSnapshot()
			if kind == "valid" {
				if !state.CanInstall || state.Progress != 100 {
					t.Fatalf("valid download not ready: %+v", state)
				}
				if err := verifyUpdateFile(a.update.readyPath, m); err != nil {
					t.Fatal(err)
				}
			} else if state.CanInstall || state.Status != "error" {
				t.Fatalf("%s was accepted: %+v", kind, state)
			}
			entries, _ := os.ReadDir(a.update.downloadsDir)
			if len(entries) != 0 {
				t.Fatal("partial downloads not cleaned")
			}
		})
	}
}
func TestBinaryVersionBoundaryAndMalformedPE(t *testing.T) {
	data := fixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "candidate.exe")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateDownloadedExecutable(path, appVersion); err != nil {
		t.Fatal("valid release executable rejected: ", err)
	}
	for _, v := range []string{"3.2.0", "3.2.10", "3.2.11"} {
		if validateDownloadedExecutable(path, v) == nil {
			t.Fatal("wrong exact version accepted", v)
		}
	}
	bad := append([]byte(nil), data...)
	peOffset := int(binary.LittleEndian.Uint32(bad[0x3c:]))
	binary.LittleEndian.PutUint16(bad[peOffset+22:], binary.LittleEndian.Uint16(bad[peOffset+22:])|0x2000)
	_ = os.WriteFile(path, bad, 0600)
	if validateDownloadedExecutable(path, appVersion) == nil {
		t.Fatal("DLL accepted as application")
	}
}
func TestInstallBlockedDuringCriticalOperation(t *testing.T) {
	a := testApp(t)
	a.state.Running = true
	if a.installReadyUpdate() == nil {
		t.Fatal("install accepted during collection")
	}
	a.state.Running = false
	if !a.beginCriticalOperation() {
		t.Fatal("cannot begin report operation")
	}
	if a.installReadyUpdate() == nil {
		t.Fatal("install accepted during report operation")
	}
	a.endCriticalOperation()
	a.installing = true
	if a.beginCriticalOperation() {
		t.Fatal("report operation accepted during installation")
	}
}
func TestSettingsAndOwnedCleanup(t *testing.T) {
	a := testApp(t)
	output := filepath.Join(t.TempDir(), "Reports")
	a.saveApplicationSettings(output)
	if a.loadSavedOutputRoot() != output {
		t.Fatal("settings did not survive serialization")
	}
	unknown := filepath.Join(a.update.downloadsDir, "keep-my-data")
	_ = os.MkdirAll(unknown, 0700)
	_ = os.WriteFile(filepath.Join(unknown, "keep.txt"), []byte("user"), 0600)
	owned := filepath.Join(a.update.downloadsDir, "3.2.0")
	_ = os.MkdirAll(owned, 0700)
	_ = os.WriteFile(filepath.Join(owned, stableExecutableName+".download"), []byte("partial"), 0600)
	a.cleanupStaleUpdateFiles()
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unknown files deleted")
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("owned partial remains")
	}
}

func TestStartupRequiresRenderedUI(t *testing.T) {
	a := testApp(t)
	a.uiReady = false
	var requests int32
	m := testManifest(appVersion)
	body, _ := json.Marshal(m)
	setTransport(a, func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return response(200, body, nil), nil
	})
	_ = a.beginUpdateCheck(false, true)
	if atomic.LoadInt32(&requests) != 0 {
		t.Fatal("focus event started network work before the UI rendered")
	}
	a.uiReady = true
	_ = a.beginUpdateCheck(false, false)
	waitCheck(t, a)
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatal("startup check did not run after the UI rendered")
	}
}
func TestManifestHostPathAndSizeRestrictions(t *testing.T) {
	for _, kind := range []string{"http", "wrong-host", "wrong-repository", "oversize", "credentials", "version-prefix"} {
		m := testManifest("3.2.2")
		switch kind {
		case "http":
			m.Asset.URL = strings.Replace(m.Asset.URL, "https:", "http:", 1)
		case "wrong-host":
			m.Asset.URL = strings.Replace(m.Asset.URL, "github.com", "evil.example", 1)
		case "wrong-repository":
			m.Asset.URL = strings.Replace(m.Asset.URL, "TechSavage1989", "someone-else", 1)
		case "oversize":
			m.Asset.Size = maximumUpdateBytes + 1
		case "credentials":
			m.Asset.URL = strings.Replace(m.Asset.URL, "https://", "https://user:secret@", 1)
		case "version-prefix":
			m.Version = "3.2.20"
		}
		if validateManifest(&m) == nil {
			t.Fatal("unsafe manifest accepted", kind)
		}
	}
	req, _ := http.NewRequest("GET", "https://evil.example/update.exe", nil)
	if newUpdateHTTPClient().CheckRedirect(req, nil) == nil {
		t.Fatal("redirect to unallowlisted host accepted")
	}
	a := testApp(t)
	m := testManifest(appVersion)
	setTransport(a, func(r *http.Request) (*http.Response, error) {
		res := response(200, nil, nil)
		res.ContentLength = maximumUpdateBytes + 1
		return res, nil
	})
	a.performUpdateDownload(m)
	if a.updateSnapshot().CanInstall {
		t.Fatal("oversized download accepted")
	}
}

func TestOldMarkerPrefixAndOverlayNegativeControls(t *testing.T) {
	data := fixture(t)
	marker := []byte(binaryIdentityPrefix + appVersion + "|")
	// The previous substring check accepts 3.2.10 when checking for 3.2.1.
	mutated := bytes.ReplaceAll(data, marker, []byte(binaryIdentityPrefix+appVersion+"0"))
	oldExpected := []byte(binaryIdentityPrefix + appVersion)
	if !bytes.Contains(mutated, oldExpected) {
		t.Fatal("negative control did not reproduce old prefix acceptance")
	}
	path := filepath.Join(t.TempDir(), "candidate.exe")
	_ = os.WriteFile(path, mutated, 0600)
	if validateDownloadedExecutable(path, appVersion) == nil {
		t.Fatal("version-prefix impostor accepted")
	}
	// A forged overlay marker also passed the previous whole-file substring check.
	mutated = bytes.ReplaceAll(data, marker, bytes.Repeat([]byte{'X'}, len(marker)))
	mutated = append(mutated, oldExpected...)
	if !bytes.Contains(mutated, oldExpected) {
		t.Fatal("overlay negative control is invalid")
	}
	_ = os.WriteFile(path, mutated, 0600)
	if validateDownloadedExecutable(path, appVersion) == nil {
		t.Fatal("overlay identity impostor accepted")
	}
}
