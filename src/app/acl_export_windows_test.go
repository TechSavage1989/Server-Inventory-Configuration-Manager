//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestACLExportProgressBoundToCurrentRun(t *testing.T) {
	a := &application{}
	a.state.Running = true
	a.state.RunID = "current"
	event := `@@ACL_EXPORT@@{"RunId":"current","State":"Running","ShareIndex":1,"ShareCount":2,"ShareName":"<test>","ElapsedSeconds":7,"TimeoutSeconds":120}`
	a.parseLineLocked(event)
	if a.state.ACLExport.ShareName != "<test>" || a.state.ACLExport.ElapsedSeconds != 7 || a.state.Progress != 0 {
		t.Fatal(a.state)
	}
	a.state.ACLExport.SkipRequested = true
	a.parseLineLocked(strings.Replace(event, `"Running"`, `"TimedOut"`, 1))
	if !a.state.ACLExport.Incomplete || !a.state.ACLExport.SkipRequested {
		t.Fatal("lost partial/skip state")
	}
	a.parseLineLocked(strings.Replace(event, `"Running"`, `"Idle"`, 1))
	if !a.state.ACLExport.Incomplete || !a.state.ACLExport.SkipRequested {
		t.Fatal("Idle cleared incomplete export warning")
	}
	expected := a.state.ACLExport
	for _, bad := range []string{strings.Replace(event, "current", "stale", 1), strings.Replace(event, `"Running"`, `"Unknown"`, 1), strings.Replace(event, `"TimeoutSeconds":120`, `"TimeoutSeconds":0`, 1), `@@ACL_EXPORT@@{broken}`} {
		a.parseLineLocked(bad)
		if a.state.ACLExport != expected {
			t.Fatal("invalid/stale event accepted", bad)
		}
	}
	a.state.Running = false
	a.parseLineLocked(event)
	if a.state.ACLExport != expected {
		t.Fatal("finished collection accepted heartbeat")
	}
	a.state = internalState{Running: true, RunID: "new-run"}
	a.parseLineLocked(strings.Replace(event, "current", "new-run", 1))
	if a.state.ACLExport.Incomplete || a.state.ACLExport.SkipRequested {
		t.Fatal("new run retained stale export flags")
	}
}

func TestSkipACLExportAuthorizationOwnershipAndIdempotence(t *testing.T) {
	dir := t.TempDir()
	a := &application{token: "acl-test", temporaryDirectory: dir, aclControlPath: filepath.Join(dir, "ACL-Control.json")}
	a.state.Running = true
	a.state.RunID = "current"
	a.state.ACLExport = aclExportState{RunID: "current", State: "Running", TimeoutSeconds: 120}
	send := func(method, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/skip-acl-export", bytes.NewBufferString(body))
		req.Header.Set("X-App-Token", token)
		res := httptest.NewRecorder()
		a.handleSkipACLExport(res, req)
		return res
	}
	good := `{"expectedRunId":"current"}`
	for _, c := range []struct {
		method, token, body string
		code                int
	}{{"POST", "wrong", good, 403}, {"GET", a.token, good, 405}, {"POST", a.token, `{"expectedRunId":"stale"}`, 409}, {"POST", a.token, good + `{}`, 400}, {"POST", a.token, `{}`, 409}} {
		if res := send(c.method, c.token, c.body); res.Code != c.code {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	if _, err := os.Stat(a.aclControlPath); !os.IsNotExist(err) {
		t.Fatal("invalid request created control")
	}
	if res := send("POST", a.token, good); res.Code != 202 {
		t.Fatal(res.Code, res.Body.String())
	}
	first, err := os.ReadFile(a.aclControlPath)
	if err != nil {
		t.Fatal(err)
	}
	var control struct {
		RunID         string `json:"RunId"`
		SkipRemaining bool   `json:"SkipRemaining"`
	}
	if json.Unmarshal(first, &control) != nil || control.RunID != "current" || !control.SkipRemaining || !a.state.ACLExport.SkipRequested {
		t.Fatal(string(first))
	}
	if res := send("POST", a.token, good); res.Code != 202 {
		t.Fatal("repeat not idempotent")
	}
	second, _ := os.ReadFile(a.aclControlPath)
	if !bytes.Equal(first, second) {
		t.Fatal("duplicate changed control")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "acl-control-*.tmp"))
	if len(files) != 0 {
		t.Fatal("left temporary control files")
	}
	a.state.ACLExport.SkipRequested = false
	a.aclControlPath = filepath.Join(t.TempDir(), "outside.json")
	if res := send("POST", a.token, good); res.Code != 409 {
		t.Fatal("accepted foreign control path")
	}
	a.aclControlPath = filepath.Join(dir, "ACL-Control.json")
	a.state.CancelRequested = true
	if res := send("POST", a.token, good); res.Code != 409 {
		t.Fatal("accepted skip during cancel")
	}
	a.state.CancelRequested = false
	a.state.ACLExport.State = "Completed"
	if res := send("POST", a.token, good); res.Code != 409 {
		t.Fatal("accepted skip after export ended")
	}
}

func TestACLTimeoutDefaultsAndValidation(t *testing.T) {
	valid := collectionOptions{OutputRoot: `C:\Reports`, InventoryCategories: []string{"Hardware"}, EventLogDays: 30}
	if err := validateCollectionOptions(&valid); err != nil || valid.AclExportTimeoutSeconds != 120 {
		t.Fatal("old clients did not receive bounded default", err, valid)
	}
	for _, timeout := range []int{-1, 1, 29, 3601} {
		o := valid
		o.AclExportTimeoutSeconds = timeout
		if validateCollectionOptions(&o) == nil {
			t.Fatal("invalid timeout", timeout)
		}
	}
	for _, timeout := range []int{30, 120, 3600} {
		o := valid
		o.AclExportTimeoutSeconds = timeout
		if err := validateCollectionOptions(&o); err != nil {
			t.Fatal(err)
		}
	}
	a := &application{}
	a.update.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	valid.AclExportTimeoutSeconds = 240
	if err := a.saveCollectionPreferences(valid); err != nil {
		t.Fatal(err)
	}
	reopened := &application{}
	reopened.update.settingsPath = a.update.settingsPath
	reopened.loadCollectionPreferences()
	if reopened.collectionPreferences == nil || reopened.collectionPreferences.Options.AclExportTimeoutSeconds != 240 {
		t.Fatal("timeout not persisted")
	}
}
