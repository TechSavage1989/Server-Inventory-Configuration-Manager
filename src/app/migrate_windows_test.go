//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
