//go:build windows

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBrowserLaunchIsGuestAndExtensionFree(t *testing.T) {
	profile := filepath.Join(browserRuntimeDir(), "Browser-"+strings.Repeat("a", 48))
	args := browserArguments("http://127.0.0.1:12345/?token="+strings.Repeat("a", 48), profile)
	for _, flag := range []string{"--guest", "--disable-extensions", "--disable-sync", "--no-first-run", "--no-default-browser-check"} {
		found := false
		for _, arg := range args {
			if arg == flag {
				found = true
			}
		}
		if !found {
			t.Fatal("Missing private browser flag", flag)
		}
	}
	if !matchesBrowserProfile(args, profile) {
		t.Fatal("private profile did not match")
	}
	if matchesBrowserProfile(args, filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "User Data")) {
		t.Fatal("personal browser profile matched app ownership")
	}
	if ownedBrowserProfile(filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "User Data")) {
		t.Fatal("personal profile treated as application owned")
	}
}
func TestNativeProcessCommandLineMetadata(t *testing.T) {
	args, err := processArguments(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if len(args) == 0 || !strings.EqualFold(filepath.Clean(args[0]), filepath.Clean(exe)) {
		t.Fatalf("unexpected process arguments: %v", args)
	}
}
func TestBrowserPolicyCannotFallbackToAccountSignin(t *testing.T) {
	for _, policy := range []map[string]uint32{{"BrowserGuestModeEnabled": 0}, {"BrowserSignin": 2}, {"ForceBrowserSignin": 1}} {
		if !browserPolicyDisallowsGuest(policy) {
			t.Fatal("blocked guest policy was ignored", policy)
		}
	}
	if browserPolicyDisallowsGuest(map[string]uint32{}) || browserPolicyDisallowsGuest(map[string]uint32{"BrowserGuestModeEnabled": 1, "BrowserSignin": 1}) {
		t.Fatal("available guest session was rejected")
	}
}
func TestLegacyBrowserOwnershipRequiresLiveMatchingBackendPID(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	token := strings.Repeat("c", 48)
	address := server.URL + "/?token=" + token
	profile := filepath.Join(browserRuntimeDir(), "Browser-"+token)
	if err := os.MkdirAll(browserRuntimeDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(browserRuntimeDir(), "application-url.txt"), []byte(address), 0600); err != nil {
		t.Fatal(err)
	}
	if got := previousBrowserProfile(os.Getpid(), "unused legacy target"); got != profile {
		t.Fatal("could not identify the live previous application profile", got)
	}
	if got := previousBrowserProfile(os.Getpid()+1000000, "unused legacy target"); got != "" {
		t.Fatal("accepted a different application's listener PID", got)
	}
	_, port, ok := profileFromApplicationURL(address)
	if !ok || tcpListenerPID(port) != uint32(os.Getpid()) {
		t.Fatal("native listener metadata did not identify the owning PID", strconv.Itoa(port))
	}
	if _, _, ok := profileFromApplicationURL("https://example.com/?token=" + token); ok {
		t.Fatal("non-application address accepted as browser ownership")
	}
}
func TestNativeBrowserClosesModalWithoutLauncherPIDAndPreservesOtherProfile(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><head><title>"+appName+"</title></head><body>Native modal regression<script>setTimeout(function(){if(location.search.indexOf('modal')>=0){alert('Owned app modal regression');}},300);</script></body></html>")
	}))
	defer server.Close()
	first := filepath.Join(browserRuntimeDir(), "Browser-"+strings.Repeat("a", 48))
	second := filepath.Join(browserRuntimeDir(), "Browser-"+strings.Repeat("b", 48))
	one, _, err := openInterfaceURL(server.URL+"/?modal", first)
	if err != nil {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	defer func() { _ = closeScopedBrowser(first); _ = one.Wait() }()
	other, _, err := openInterfaceURL(server.URL+"/", second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeScopedBrowser(second); _ = other.Wait() }()
	wait := func(fn func() bool) {
		t.Helper()
		until := time.Now().Add(25 * time.Second)
		for time.Now().Before(until) {
			if fn() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("native browser condition was not met")
	}
	wait(func() bool { return len(scopedBrowserWindows(first)) > 0 && len(scopedBrowserWindows(second)) > 0 })
	time.Sleep(time.Second)
	// The launcher PID is deliberately unavailable, matching Edge handoff/de-elevation.
	a := &application{browserProfileDirectory: first}
	if a.ownApplicationWindow() == 0 {
		t.Fatal("owned visible app was lost after browser launcher PID handoff")
	}
	if err := a.closeApplicationBrowser(); err != nil {
		t.Fatal(err)
	}
	p, err := scopedBrowserProcesses(first)
	if err != nil || len(p) != 0 || len(scopedBrowserWindows(first)) != 0 {
		t.Fatal("old modal browser window/process remained", err)
	}
	if len(scopedBrowserWindows(second)) == 0 {
		t.Fatal("closing the app also closed a different browser profile")
	}
}
