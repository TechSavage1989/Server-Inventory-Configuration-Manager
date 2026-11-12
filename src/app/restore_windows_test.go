//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func restoreTestScan() *restoreScan {
	return &restoreScan{
		PackageName: "SRV01_ServerInventory_20261101_101010_abcd1234",
		Components: []restoreScanComponent{
			{ID: "firewall", Title: "Windows Firewall", Available: true, Items: []restoreScanItem{{ID: "firewall", Available: true}}},
			{ID: "registry", Title: "Registry settings", Available: true, Items: []restoreScanItem{
				{ID: `registry:01-System\Registry-Configuration\SMB-Server-Parameters.reg`, Available: true},
				{ID: `registry:01-System\Registry-Configuration\Windows-Version-Registry.reg`, Available: false, BlockedReason: "never"},
			}},
			{ID: "dhcp", Title: "DHCP Server", Available: false, Items: []restoreScanItem{{ID: "dhcp", Available: false}}},
		},
	}
}

func TestRestoreSelectionOnlyAcceptsAvailableScannedItems(t *testing.T) {
	scan := restoreTestScan()
	selections, total, err := validateRestoreSelection(scan, []restoreSelection{
		{ID: "firewall", Items: []string{"firewall", "firewall"}},
		{ID: "registry", Items: []string{`registry:01-System\Registry-Configuration\SMB-Server-Parameters.reg`}},
	})
	if err != nil || total != 2 || len(selections) != 2 || len(selections[0].Items) != 1 {
		t.Fatalf("valid selection rejected: %v %d %#v", err, total, selections)
	}
	for name, bad := range map[string][]restoreSelection{
		"blocked item":      {{ID: "registry", Items: []string{`registry:01-System\Registry-Configuration\Windows-Version-Registry.reg`}}},
		"blocked component": {{ID: "dhcp", Items: []string{"dhcp"}}},
		"unknown item":      {{ID: "firewall", Items: []string{`registry:..\..\evil.reg`}}},
		"unknown component": {{ID: "everything", Items: []string{"x"}}},
		"duplicate":         {{ID: "firewall", Items: []string{"firewall"}}, {ID: "firewall", Items: []string{"firewall"}}},
		"empty":             {{ID: "firewall", Items: nil}},
		"nothing":           nil,
	} {
		if _, _, err := validateRestoreSelection(scan, bad); err == nil {
			t.Fatal("selection accepted:", name)
		}
	}
	if _, _, err := validateRestoreSelection(nil, []restoreSelection{{ID: "firewall", Items: []string{"firewall"}}}); err == nil {
		t.Fatal("selection accepted without a scan")
	}
}

func TestRestoreEventsUpdateOnlyPlannedItems(t *testing.T) {
	a := &application{}
	a.restore.status = "running"
	for _, line := range []string{
		`@@RESTORE@@{"event":"plan","items":[{"component":"registry","id":"r1","title":"SMB"},{"component":"firewall","id":"firewall","title":"Firewall"}]}`,
		`2026-11-12 10:00:00 [INFO] Starting: Restore registry - SMB`,
		`@@RESTORE@@{"event":"item","component":"registry","id":"r1","status":"restoring","message":"Saving"}`,
		`@@RESTORE@@{"event":"item","component":"registry","id":"r1","status":"succeeded","message":"Restored."}`,
		`@@RESTORE@@{"event":"item","component":"registry","id":"r1","status":"pwned","message":"x"}`,
		`@@RESTORE@@{"event":"item","component":"dhcp","id":"dhcp","status":"succeeded"}`,
		`@@RESTORE@@not json`,
		`@@RESTORE@@{"event":"done","status":"succeeded"}`,
	} {
		a.applyRestoreLineLocked(line)
	}
	if len(a.restore.items) != 2 || a.restore.items[0].Status != "succeeded" || a.restore.items[1].Status != "pending" {
		t.Fatalf("unexpected items: %#v", a.restore.items)
	}
	if a.restore.finalEvent != "succeeded" || len(a.restore.logs) != 1 || !strings.Contains(a.restore.logs[0].Text, "Starting") {
		t.Fatalf("unexpected events/logs: %q %#v", a.restore.finalEvent, a.restore.logs)
	}
	a.finishRestore("canceled", "")
	if a.restore.items[1].Status != "skipped" {
		t.Fatal("unfinished item was not closed out:", a.restore.items[1])
	}
	for i := 0; i < restoreLogLimit+50; i++ {
		a.applyRestoreLineLocked("line")
	}
	if len(a.restore.logs) != restoreLogLimit {
		t.Fatal("restore log is not bounded:", len(a.restore.logs))
	}
}

func TestRestoreStartRequiresAdministratorConfirmationAndFreshScan(t *testing.T) {
	packagePath := t.TempDir()
	a := &application{token: "restore-test", computerName: "SRV01", shutdown: make(chan struct{})}
	start := func(body map[string]any) (int, string) {
		data, _ := json.Marshal(body)
		request := httptest.NewRequest(http.MethodPost, "/api/restore/start?token=restore-test", bytes.NewReader(data))
		recorder := httptest.NewRecorder()
		a.handleRestoreStart(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	body := map[string]any{"packagePath": packagePath, "confirmComputerName": "srv01", "components": []map[string]any{{"id": "firewall", "items": []string{"firewall"}}}}
	if code, text := start(body); code != http.StatusForbidden || !strings.Contains(text, "administrator") {
		t.Fatal("restore started without administrator rights:", code, text)
	}
	a.admin = true
	wrongName := map[string]any{"packagePath": packagePath, "confirmComputerName": "SRV02", "components": body["components"]}
	if code, text := start(wrongName); code != http.StatusBadRequest || !strings.Contains(text, "SRV01") {
		t.Fatal("restore started without the typed server name:", code, text)
	}
	if code, text := start(body); code != http.StatusConflict || !strings.Contains(text, "Check this package") {
		t.Fatal("restore started without a scan:", code, text)
	}
	a.restore = restoreRuntime{status: "ready", packagePath: filepath.Join(packagePath, "other"), scan: restoreTestScan()}
	if code, _ := start(body); code != http.StatusConflict {
		t.Fatal("restore started from a different package than the one scanned")
	}
	a.restore.packagePath = packagePath
	a.state.Running = true
	if code, text := start(body); code != http.StatusConflict || !strings.Contains(text, "collection") {
		t.Fatal("restore started during a collection:", code, text)
	}
	a.state.Running = false
	a.installing = true
	if code, _ := start(body); code != http.StatusConflict {
		t.Fatal("restore started while an update installs")
	}
	a.installing = false
	if a.criticalOperations != 0 {
		t.Fatal("refused restores leaked critical operations:", a.criticalOperations)
	}
}

func TestCollectionAndCloseAreRefusedDuringRestore(t *testing.T) {
	a := &application{token: "restore-test", shutdown: make(chan struct{})}
	a.restore.status = "running"
	request := httptest.NewRequest(http.MethodPost, "/api/shutdown?token=restore-test", nil)
	recorder := httptest.NewRecorder()
	a.handleShutdown(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "restore") {
		t.Fatal("close accepted during a restore:", recorder.Code, recorder.Body.String())
	}
	output := t.TempDir()
	data, _ := json.Marshal(map[string]any{"outputRoot": output, "inventoryCategories": []string{"Hardware"}, "eventLogDays": 30})
	request = httptest.NewRequest(http.MethodPost, "/api/start?token=restore-test", bytes.NewReader(data))
	recorder = httptest.NewRecorder()
	a.handleStart(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "restore") {
		t.Fatal("collection accepted during a restore:", recorder.Code, recorder.Body.String())
	}
}

// writePackageFile writes a file into a synthetic package and records it for
// the manifest.
func writePackageFile(t *testing.T, root, relative string, data []byte, manifest *strings.Builder) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	manifest.WriteString(`"` + relative + `","` + "0" + `","2026-11-01 10:10:10","` + strings.ToUpper(hex.EncodeToString(sum[:])) + "\"\r\n")
}

func unicodeFile(text string) []byte {
	encoded := utf16.Encode([]rune(text))
	data := []byte{0xFF, 0xFE}
	for _, unit := range encoded {
		data = append(data, byte(unit), byte(unit>>8))
	}
	return data
}

// Runs the embedded engine's scan against a synthetic package and checks that
// verified files are offered, and tampered, unlisted and unsafe files are not.
func TestRestoreScanVerifiesPackageFilesWithRealEngine(t *testing.T) {
	powerShell := powershellPath()
	if _, err := os.Stat(powerShell); err != nil {
		t.Skip("Windows PowerShell is not available")
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := filepath.Join(t.TempDir(), "SRV-OTHER_ServerInventory_20261101_101010_abcd1234")
	var manifest strings.Builder
	manifest.WriteString("\"RelativePath\",\"LengthBytes\",\"LastWriteTime\",\"SHA256\"\r\n")
	writePackageFile(t, root, `00-Summary\Summary.txt`, []byte("synthetic"), &manifest)
	smb := "Windows Registry Editor Version 5.00\r\n\r\n[HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Services\\LanmanServer\\Parameters]\r\n\"Example\"=dword:00000001\r\n"
	writePackageFile(t, root, `01-System\Registry-Configuration\SMB-Server-Parameters.reg`, unicodeFile(smb), &manifest)
	escape := "Windows Registry Editor Version 5.00\r\n\r\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\Example]\r\n[HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Run]\r\n\"x\"=\"y\"\r\n"
	writePackageFile(t, root, `01-System\Registry-Configuration\Escaping.reg`, unicodeFile(escape), &manifest)
	user := "Windows Registry Editor Version 5.00\r\n\r\n[HKEY_CURRENT_USER\\Software\\Example]\r\n"
	writePackageFile(t, root, `06-Accounts-Policy-Security\User.reg`, unicodeFile(user), &manifest)
	writePackageFile(t, root, `06-Accounts-Policy-Security\Windows-Firewall-Policy.wfw`, []byte("firewall-policy"), &manifest)
	writePackageFile(t, root, `06-Accounts-Policy-Security\Local-Security-Policy.inf`, unicodeFile("[Unicode]\r\nUnicode=yes\r\n"), &manifest)
	if err := os.WriteFile(filepath.Join(root, "SHA256-Manifest.csv"), []byte(manifest.String()), 0600); err != nil {
		t.Fatal(err)
	}
	// Tamper with the firewall policy and add an unlisted registry file.
	if err := os.WriteFile(filepath.Join(root, `06-Accounts-Policy-Security\Windows-Firewall-Policy.wfw`), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, `01-System\Registry-Configuration\Unlisted.reg`), unicodeFile(smb), 0600); err != nil {
		t.Fatal(err)
	}

	a := &application{powerShellPath: powerShell}
	a.update.appDataRoot = filepath.Join(os.Getenv("LOCALAPPDATA"), appName)
	scan, err := a.scanRestorePackage(root)
	if err != nil {
		t.Fatal(err, a.restore.logs)
	}
	if !scan.ManifestVerified || scan.SameComputer || scan.SourceComputer != "SRV-OTHER" || scan.CollectedAt != "2026-11-01 10:10:10" || len(scan.Components) != len(restoreComponentIDs) {
		t.Fatalf("unexpected scan identity: %+v", scan)
	}
	components := map[string]restoreScanComponent{}
	for _, component := range scan.Components {
		components[component.ID] = component
	}
	if firewall := components["firewall"]; firewall.Available || !strings.Contains(firewall.BlockedReason, "SHA-256") {
		t.Fatalf("tampered firewall policy was offered: %+v", firewall)
	}
	registry := map[string]restoreScanItem{}
	for _, item := range components["registry"].Items {
		registry[item.ID] = item
	}
	if item := registry[`registry:01-System\Registry-Configuration\SMB-Server-Parameters.reg`]; !item.Available || item.DefaultSelected || !strings.Contains(item.Detail, "LanmanServer") {
		t.Fatalf("verified registry file was not offered: %+v", item)
	}
	if item := registry[`registry:01-System\Registry-Configuration\Unlisted.reg`]; item.Available || !strings.Contains(item.BlockedReason, "not listed") {
		t.Fatalf("unlisted registry file was offered: %+v", item)
	}
	if item := registry[`registry:01-System\Registry-Configuration\Escaping.reg`]; item.Available || !strings.Contains(item.BlockedReason, "outside its own key") {
		t.Fatalf("registry file writing outside its key was offered: %+v", item)
	}
	if item := registry[`registry:06-Accounts-Policy-Security\User.reg`]; item.Available {
		t.Fatalf("non-machine registry file was offered: %+v", item)
	}
	if dhcp := components["dhcp"]; dhcp.Available || dhcp.BlockedReason == "" {
		t.Fatalf("missing DHCP export was offered: %+v", dhcp)
	}
	data, _ := json.Marshal(scan)
	if strings.Contains(string(data), `"data"`) {
		t.Fatal("internal item data leaked into the scan shown to the interface")
	}

	if err := os.Remove(filepath.Join(root, "SHA256-Manifest.csv")); err != nil {
		t.Fatal(err)
	}
	scan, err = a.scanRestorePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range scan.Components {
		if component.Available {
			t.Fatal("a package without a manifest offered a restore:", component.ID)
		}
	}
	if _, err := a.scanRestorePackage(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not an inventory package") {
		t.Fatal("a folder that is not a package was accepted:", err)
	}
}
