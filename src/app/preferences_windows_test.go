//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCollectionPreferencesRoundTripAndConsent(t *testing.T) {
	dir := t.TempDir()
	a := &application{token: "prefs-test"}
	a.update.settingsPath = filepath.Join(dir, "settings.json")
	original := []byte(`{"output_root":"C:\\Legacy Reports","future_setting":"preserve"}`)
	if err := os.WriteFile(a.update.settingsPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	options := collectionOptions{OutputRoot: `C:\Reports`, InventoryCategories: []string{"Hardware", "NPS"}, ExportCategories: []string{"NPS"}, EventLogDays: 14, IncludeSensitive: true}
	data, _ := json.Marshal(options)
	req := httptest.NewRequest("POST", "/api/preferences", bytes.NewReader(data))
	req.Header.Set("X-App-Token", a.token)
	res := httptest.NewRecorder()
	a.handlePreferences(res, req)
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	// Loading a fresh application simulates process restart; no collector is run.
	reopened := &application{}
	reopened.update.settingsPath = a.update.settingsPath
	reopened.loadCollectionPreferences()
	if reopened.collectionPreferences == nil {
		t.Fatal("saved choices were not loaded")
	}
	saved := reopened.collectionPreferences.Options
	if saved.OutputRoot != options.OutputRoot || saved.EventLogDays != 14 || !reflect.DeepEqual(saved.InventoryCategories, options.InventoryCategories) {
		t.Fatal(saved)
	}
	if saved.IncludeSensitive || len(saved.ExportCategories) != 0 {
		t.Fatal("sensitive consent was restored", saved)
	}
	if !options.IncludeSensitive || len(options.ExportCategories) != 1 {
		t.Fatal("saving mutated the active collection")
	}
	current, _ := os.ReadFile(a.update.settingsPath)
	if !bytes.Equal(current, original) {
		t.Fatal("legacy settings changed")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "collection-*.tmp"))
	if len(files) != 0 {
		t.Fatal("temporary preferences left behind", files)
	}
}

func TestCollectionPreferencesRejectInvalidAndBusyRequests(t *testing.T) {
	a := &application{token: "prefs-test"}
	a.update.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	valid := collectionOptions{OutputRoot: `C:\Reports`, InventoryCategories: []string{"Hardware"}, ExportCategories: []string{}, EventLogDays: 30}
	send := func(method, token string, options collectionOptions) int {
		data, _ := json.Marshal(options)
		req := httptest.NewRequest(method, "/api/preferences", bytes.NewReader(data))
		req.Header.Set("X-App-Token", token)
		res := httptest.NewRecorder()
		a.handlePreferences(res, req)
		return res.Code
	}
	if send("POST", "wrong", valid) != 403 || send("GET", a.token, valid) != 405 {
		t.Fatal("endpoint authorization/method bypass")
	}
	for _, mutate := range []func(*collectionOptions){
		func(o *collectionOptions) { o.OutputRoot = "relative" }, func(o *collectionOptions) { o.InventoryCategories = nil },
		func(o *collectionOptions) { o.InventoryCategories = []string{"Unknown"} }, func(o *collectionOptions) { o.InventoryCategories = []string{"Hardware", "Hardware"} },
		func(o *collectionOptions) { o.ExportCategories = []string{"Hardware"} }, func(o *collectionOptions) { o.ExportCategories = []string{"DHCP"} },
		func(o *collectionOptions) {
			o.InventoryCategories = []string{"NPS"}
			o.ExportCategories = []string{"NPS"}
		},
		func(o *collectionOptions) { o.EventLogDays = 366 }, func(o *collectionOptions) { o.EventLogDays = -1 },
	} {
		options := valid
		mutate(&options)
		if send("POST", a.token, options) != 400 {
			t.Fatal("invalid choices accepted", options)
		}
	}
	a.state.Running = true
	if send("POST", a.token, valid) != 409 {
		t.Fatal("saved during collection")
	}
	a.state.Running = false
	a.installing = true
	if send("POST", a.token, valid) != 409 {
		t.Fatal("saved during install")
	}
	a.installing = false
	a.closing = true
	if send("POST", a.token, valid) != 409 {
		t.Fatal("saved during shutdown")
	}
	a.closing = false
	// Existing start endpoint must reject the same malformed category before any
	// output-directory write or PowerShell invocation.
	invalid := valid
	invalid.InventoryCategories = []string{"not-a-real-module"}
	data, _ := json.Marshal(invalid)
	req := httptest.NewRequest("POST", "/api/start", bytes.NewReader(data))
	req.Header.Set("X-App-Token", a.token)
	res := httptest.NewRecorder()
	a.handleStart(res, req)
	if res.Code != 400 || a.state.Running {
		t.Fatal("invalid scope launched collection")
	}
}

func TestCollectionPreferencesCorruptionAndWriteFailure(t *testing.T) {
	a := &application{}
	a.update.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	for _, data := range []string{"broken JSON", `{"schema":2}`, strings.Repeat("x", 65537), `{"schema":1,"options":{"outputRoot":"C:\\Reports","inventoryCategories":["Hardware"],"eventLogDays":900}}`} {
		if err := os.WriteFile(a.preferencesPath(), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		a.collectionPreferences = nil
		a.loadCollectionPreferences()
		if a.collectionPreferences != nil {
			t.Fatal("corrupt settings restored")
		}
	}
	if err := os.Remove(a.preferencesPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(a.preferencesPath(), 0700); err != nil {
		t.Fatal(err)
	}
	options := collectionOptions{OutputRoot: `C:\Reports`, InventoryCategories: []string{"Hardware"}, EventLogDays: 30}
	if err := a.saveCollectionPreferences(options); err == nil {
		t.Fatal("directory replacement succeeded unexpectedly")
	}
	if a.collectionPreferences != nil {
		t.Fatal("failed write reported as saved")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(a.preferencesPath()), "collection-*.tmp"))
	if len(files) != 0 {
		t.Fatal("failed write left temporary file")
	}
}
