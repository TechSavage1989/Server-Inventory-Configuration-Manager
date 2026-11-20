//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrateTestDiscovery() *migrateDiscovery {
	return &migrateDiscovery{Source: "FS-OLD-07", SourceFqdn: "fs-old-07.example.com", Shares: []migrateSourceShare{
		{Name: "Finance", Path: `E:\Shares\Finance`, RemotePath: `\\FS-OLD-07\E$\Shares\Finance`},
		{Name: "Projects", Path: `E:\Shares\Projects`, RemotePath: `\\FS-OLD-07\E$\Shares\Projects`},
		{Name: "Mounted", Path: `\\?\Volume{x}`, RemotePath: ""},
	}}
}

func TestMigratePlanValidatesSharesNamesAndFolders(t *testing.T) {
	discovery := migrateTestDiscovery()
	plan, err := validateMigratePlan(discovery, []migratePlanInput{
		{Name: "finance", DestName: "Finance", DestPath: `D:\Shares\Finance`},
		{Name: "Projects", DestName: "Projects2", DestPath: `D:\Shares\Projects`},
	}, []migrateShareState{{Name: "Finance", DestName: "Finance", DestPath: `d:\shares\finance`, PreviouslySynced: true, CreatedShare: true}})
	if err != nil || len(plan) != 2 || plan[0].Name != "Finance" || !plan[0].PreviouslySynced || !plan[0].CreatedShare || plan[1].PreviouslySynced {
		t.Fatalf("valid plan rejected or saved progress lost: %v %#v", err, plan)
	}
	moved, _ := validateMigratePlan(discovery, []migratePlanInput{{Name: "Finance", DestName: "Finance", DestPath: `D:\Other\Finance`}}, plan)
	if moved[0].PreviouslySynced {
		t.Fatal("a changed destination kept the earlier copy's progress")
	}
	for name, bad := range map[string][]migratePlanInput{
		"unknown share":   {{Name: "Payroll", DestName: "Payroll", DestPath: `D:\Shares\Payroll`}},
		"not copyable":    {{Name: "Mounted", DestName: "Mounted", DestPath: `D:\Shares\Mounted`}},
		"drive root":      {{Name: "Finance", DestName: "Finance", DestPath: `D:\`}},
		"network folder":  {{Name: "Finance", DestName: "Finance", DestPath: `\\other\share`}},
		"relative folder": {{Name: "Finance", DestName: "Finance", DestPath: `Shares\Finance`}},
		"bad share name":  {{Name: "Finance", DestName: `Fin/ance`, DestPath: `D:\Shares\Finance`}},
		"hidden name":     {{Name: "Finance", DestName: `Finance$`, DestPath: `D:\Shares\Finance`}},
		"duplicate name":  {{Name: "Finance", DestName: "Data", DestPath: `D:\Shares\A`}, {Name: "Projects", DestName: "data", DestPath: `D:\Shares\B`}},
		"nested folders":  {{Name: "Finance", DestName: "Finance", DestPath: `D:\Shares`}, {Name: "Projects", DestName: "Projects", DestPath: `D:\Shares\Projects`}},
		"nothing":         nil,
	} {
		if _, err := validateMigratePlan(discovery, bad, nil); err == nil {
			t.Fatal("plan accepted:", name)
		}
	}
	if _, err := validateMigratePlan(nil, []migratePlanInput{{Name: "Finance", DestName: "Finance", DestPath: `D:\Shares\Finance`}}, nil); err == nil {
		t.Fatal("plan accepted without discovery")
	}
}

func TestMigrateEventsUpdateOnlyPlannedShares(t *testing.T) {
	a := &application{}
	a.migrate.plan = []migrateShareState{{Name: "Finance"}, {Name: "Projects"}}
	for _, line := range []string{
		`2026-11-12 10:00:00 [INFO] Running: robocopy`,
		`@@MIGRATE@@{"event":"share","share":"Finance","status":"copying","message":"Copying changes."}`,
		`@@MIGRATE@@{"event":"progress","share":"Finance","processed":50,"expected":100,"percent":50}`,
		`@@MIGRATE@@{"event":"share","share":"Projects","status":"pwned"}`,
		`@@MIGRATE@@{"event":"share","share":"Payroll","status":"succeeded"}`,
		`@@MIGRATE@@not json`,
		`@@MIGRATE@@{"event":"error","message":"Access is denied."}`,
	} {
		a.applyMigrateLineLocked(line)
	}
	finance, projects := a.migrate.plan[0], a.migrate.plan[1]
	if finance.Status != "copying" || finance.Percent != 50 || finance.Processed != 50 || projects.Status != "" {
		t.Fatalf("unexpected share state: %#v %#v", finance, projects)
	}
	if a.migrate.message != "Access is denied." || len(a.migrate.logs) != 1 {
		t.Fatalf("unexpected message/logs: %q %#v", a.migrate.message, a.migrate.logs)
	}
}

func TestMigrateStepsRequireCheckCopyAndConfirmation(t *testing.T) {
	a := &application{token: "migrate-test", computerName: "SRV-DEMO-01", admin: true, shutdown: make(chan struct{})}
	a.migrate.source = "FS-OLD-07"
	a.migrate.discovery = migrateTestDiscovery()
	a.migrate.plan = []migrateShareState{{Name: "Finance", DestName: "Finance", DestPath: `D:\Shares\Finance`}}
	call := func(handler func(http.ResponseWriter, *http.Request), body map[string]any) (int, string) {
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodPost, "/?token=migrate-test", bytes.NewReader(data)))
		return recorder.Code, recorder.Body.String()
	}
	if code, text := call(a.handleMigrateSync, map[string]any{}); code != http.StatusConflict || !strings.Contains(text, "check") {
		t.Fatal("copy started before the check:", code, text)
	}
	a.migrate.prechecked = true
	a.migrate.plan[0].Precheck = &migratePrecheck{OK: false, Problems: []string{"no space"}}
	if code, _ := call(a.handleMigrateSync, map[string]any{}); code != http.StatusConflict {
		t.Fatal("copy started with a failed check")
	}
	a.migrate.plan[0].Precheck.OK = true
	if code, text := call(a.handleMigrateCutover, map[string]any{"confirmComputerName": "SRV-OTHER"}); code != http.StatusBadRequest || !strings.Contains(text, "SRV-DEMO-01") {
		t.Fatal("cutover accepted without this server's name:", code, text)
	}
	if code, text := call(a.handleMigrateCutover, map[string]any{"confirmComputerName": "srv-demo-01"}); code != http.StatusConflict || !strings.Contains(text, "Copy every") {
		t.Fatal("cutover started before a copy:", code, text)
	}
	if code, _ := call(a.handleMigrateAlias, map[string]any{"confirmOldName": "FS-OLD-07"}); code != http.StatusConflict {
		t.Fatal("alias started without a passing name check")
	}
	if code, _ := call(a.handleMigrateSourceReadOnly, map[string]any{"confirmSourceName": "SRV-DEMO-01"}); code != http.StatusBadRequest {
		t.Fatal("old shares made read-only without the old server's name")
	}
	a.state.Running = true
	a.migrate.plan[0].PreviouslySynced = true
	if code, text := call(a.handleMigrateSync, map[string]any{}); code != http.StatusConflict || !strings.Contains(text, "collection") {
		t.Fatal("copy started during a collection:", code, text)
	}
	a.state.Running = false
	a.restore.status = "running"
	if code, _ := call(a.handleMigrateSync, map[string]any{}); code != http.StatusConflict {
		t.Fatal("copy started during a restore")
	}
	a.restore.status = ""
	a.admin = false
	if code, _ := call(a.handleMigrateSync, map[string]any{}); code != http.StatusForbidden {
		t.Fatal("copy started without administrator rights")
	}
	if a.criticalOperations != 0 {
		t.Fatal("refused steps leaked critical operations:", a.criticalOperations)
	}
}

func TestCollectionRestoreAndCloseWaitForMigration(t *testing.T) {
	a := &application{token: "migrate-test", computerName: "SRV-DEMO-01", admin: true, shutdown: make(chan struct{})}
	a.migrate.action = "sync"
	recorder := httptest.NewRecorder()
	a.handleShutdown(recorder, httptest.NewRequest(http.MethodPost, "/api/shutdown?token=migrate-test", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "migration") {
		t.Fatal("close accepted during a migration step:", recorder.Code, recorder.Body.String())
	}
	data, _ := json.Marshal(map[string]any{"outputRoot": t.TempDir(), "inventoryCategories": []string{"Hardware"}, "eventLogDays": 30})
	recorder = httptest.NewRecorder()
	a.handleStart(recorder, httptest.NewRequest(http.MethodPost, "/api/start?token=migrate-test", bytes.NewReader(data)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "migration") {
		t.Fatal("collection accepted during a migration step:", recorder.Code, recorder.Body.String())
	}
	a.restore = restoreRuntime{status: "ready", packagePath: t.TempDir(), scan: restoreTestScan()}
	data, _ = json.Marshal(map[string]any{"packagePath": a.restore.packagePath, "confirmComputerName": "SRV-DEMO-01", "components": []map[string]any{{"id": "firewall", "items": []string{"firewall"}}}})
	recorder = httptest.NewRecorder()
	a.handleRestoreStart(recorder, httptest.NewRequest(http.MethodPost, "/api/restore/start?token=migrate-test", bytes.NewReader(data)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "migration") {
		t.Fatal("restore accepted during a migration step:", recorder.Code, recorder.Body.String())
	}
}

func TestMigrateConnectAsAccountStaysInMemory(t *testing.T) {
	const secret = "Synthetic-Pa55word!"
	current := migrateRuntime{source: "FS-OLD-07", account: `EXAMPLE\admin`, password: secret}
	for _, test := range []struct {
		source, user, password, wantAccount, wantSecret string
		ok                                              bool
	}{
		{"FS-OLD-07", "", "", "", "", true},
		{"FS-OLD-07", `  EXAMPLE\admin `, "", `EXAMPLE\admin`, secret, true},
		{"FS-OLD-07", `example\ADMIN`, "", `EXAMPLE\admin`, secret, true},
		{"FS-OLD-07", "admin@example.test", "new", "admin@example.test", "new", true},
		{"FS-OLD-07", "Backup Operator", "x", "Backup Operator", "x", true},
		{"FS-OTHER", `EXAMPLE\admin`, "", "", "", false},
		{"FS-OLD-07", `EXAMPLE\other`, "", "", "", false},
		{"FS-OLD-07", `bad"name`, "x", "", "", false},
		{"FS-OLD-07", "admin\r\nInjected", "x", "", "", false},
		{"FS-OLD-07", `EXAMPLE\admin`, "a\x00b", "", "", false},
		{"FS-OLD-07", strings.Repeat("a", 257), "x", "", "", false},
	} {
		account, password, err := resolveMigrateAccount(current, test.source, test.user, test.password)
		if (err == nil) != test.ok || account != test.wantAccount || password != test.wantSecret {
			t.Fatalf("%q/%q: got %q %q %v", test.source, test.user, account, password, err)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Fatal("an error message contains the password")
		}
	}

	a := &application{token: "migrate-test", computerName: "SRV-DEMO-01", admin: true, shutdown: make(chan struct{})}
	a.update.appDataRoot = t.TempDir()
	a.migrate = current
	a.migrate.plan = []migrateShareState{{Name: "Finance", DestName: "Finance", DestPath: `D:\Shares\Finance`}}
	for mode, want := range map[string]bool{"Discover": true, "Precheck": true, "Sync": true, "Cutover": true, "SourceReadOnly": true, "SourceRestore": true, "DfsReport": false, "AliasCheck": false, "Alias": false} {
		input := a.migrateCredentialInputLocked(mode)
		if (input != nil) != want {
			t.Fatalf("%s: credential input %v, want %v", mode, input != nil, want)
		}
		if input != nil {
			var line map[string]string
			if !bytes.HasSuffix(input, []byte("\n")) || json.Unmarshal(input, &line) != nil || line["user"] != `EXAMPLE\admin` || line["password"] != secret || bytes.Count(input, []byte("\n")) != 1 {
				t.Fatalf("%s: unexpected input line %q", mode, input)
			}
		}
	}
	a.migrateSaveLocked()
	saved, err := os.ReadFile(filepath.Join(a.migrateWorkPath("FS-OLD-07"), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(a.migrateSnapshotLocked())
	for name, data := range map[string][]byte{"state.json": saved, "state response": snapshot} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("%s contains the password", name)
		}
	}
	if !bytes.Contains(snapshot, []byte(`"account":"EXAMPLE\\admin"`)) || bytes.Contains(saved, []byte("admin")) {
		t.Fatalf("the state response names the account and state.json does not: %s / %s", snapshot, saved)
	}
	a.applyMigrateLineLocked(migrateEventPrefix + `{"event":"accessDenied","message":""}`)
	if !a.migrate.accessDenied {
		t.Fatal("accessDenied event was not recorded")
	}

	call := func(body map[string]any) (int, string) {
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		a.handleMigrateDiscover(recorder, httptest.NewRequest(http.MethodPost, "/?token=migrate-test", bytes.NewReader(data)))
		return recorder.Code, recorder.Body.String()
	}
	if code, text := call(map[string]any{"source": "FS-OTHER", "user": `EXAMPLE\admin`}); code != http.StatusBadRequest || !strings.Contains(text, `password for EXAMPLE\\admin`) {
		t.Fatal("a held password was reused for another server:", code, text)
	}
	if code, text := call(map[string]any{"source": "FS-OLD-07", "user": "bad|name", "password": secret}); code != http.StatusBadRequest || strings.Contains(text, secret) {
		t.Fatal("an invalid user name was accepted:", code, text)
	}
	if a.migrate.action != "" || a.migrate.account != `EXAMPLE\admin` {
		t.Fatal("a refused Connect changed the migration")
	}
	a.migrate.action = ""
	recorder := httptest.NewRecorder()
	a.handleMigrateReset(recorder, httptest.NewRequest(http.MethodPost, "/?token=migrate-test", nil))
	if a.migrate.account != "" || a.migrate.password != "" {
		t.Fatal("Start over kept the account")
	}
}

func TestMigrateRefusesWindowsProgramAndProfileFolders(t *testing.T) {
	discovery := migrateTestDiscovery()
	for _, folder := range []string{os.Getenv("SystemRoot") + `\Shares\Finance`, os.Getenv("ProgramFiles") + `\Finance`, os.Getenv("ProgramData") + `\Finance`, filepath.Dir(os.Getenv("PUBLIC")) + `\Someone\Finance`} {
		if _, err := validateMigratePlan(discovery, []migratePlanInput{{Name: "Finance", DestName: "Finance", DestPath: folder}}, nil); err == nil || !strings.Contains(err.Error(), "Choose a folder for shared data") {
			t.Fatalf("%s was accepted: %v", folder, err)
		}
	}
	for _, folder := range []string{`D:\Shares\Finance`, `C:\Data\Finance`, `E:\Program Files Old\Finance`} {
		if _, err := validateMigratePlan(discovery, []migratePlanInput{{Name: "Finance", DestName: "Finance", DestPath: folder}}, nil); err != nil {
			t.Fatalf("%s was refused: %v", folder, err)
		}
	}
	a := &application{token: "migrate-test", computerName: "SRV-DEMO-01", admin: true, shutdown: make(chan struct{})}
	a.migrate.action = "sync"
	recorder := httptest.NewRecorder()
	a.handleMigrateBrowse(recorder, httptest.NewRequest(http.MethodPost, "/?token=migrate-test", strings.NewReader(`{"share":"Finance"}`)))
	if recorder.Code != http.StatusConflict {
		t.Fatal("the folder picker opened during a migration step:", recorder.Code, recorder.Body.String())
	}
}
