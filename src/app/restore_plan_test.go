package main

import (
	"strings"
	"testing"
)

func newServerScan() *restoreScan {
	return &restoreScan{
		Components: []restoreScanComponent{
			{ID: "accounts", Title: "Local users and groups", Available: true, Items: []restoreScanItem{
				{ID: "accounts:user:svcweb", Title: "svcweb", Available: true, NeedsSecret: "account:svcweb", SecretLabel: "Password for OLD\\svcweb", SecretOptional: true},
				{ID: "accounts:group:web", Title: "web", Available: true},
			}},
			{ID: "tasks", Title: "Scheduled tasks", Available: true, Items: []restoreScanItem{
				{ID: "tasks:nightly", Title: "Nightly", Available: true, NeedsSecret: "account:svcweb", SecretLabel: "Password for OLD\\svcweb"},
				{ID: "tasks:report", Title: "Report", Available: true, NeedsSecret: "account:svcreport", SecretLabel: "Password for OLD\\svcreport"},
			}},
		},
		Mappings: []restoreMapping{
			{Kind: "ip", From: "10.0.0.5", To: "192.0.2.9", Suggested: "192.0.2.9", Options: []string{"*", "192.0.2.9", "192.0.2.10"}},
			{Kind: "path", From: "E:", To: "D:", Suggested: "D:", Options: []string{"C:", "D:"}},
		},
	}
}

func TestRestoreChangesKeepOnlyOfferedValues(t *testing.T) {
	scan := newServerScan()
	got, err := validateRestoreChanges(scan, restoreChanges{})
	if err != nil || got.IP["10.0.0.5"] != "192.0.2.9" || got.Path["E:"] != "D:" {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	got, err = validateRestoreChanges(scan, restoreChanges{IP: map[string]string{"10.0.0.5": "*"}, Path: map[string]string{"E:": "c:"}})
	if err != nil || got.IP["10.0.0.5"] != "*" || got.Path["E:"] != "C:" {
		t.Fatalf("chosen = %+v, %v", got, err)
	}
	for name, bad := range map[string]restoreChanges{
		"address not offered": {IP: map[string]string{"10.0.0.5": "203.0.113.1"}},
		"drive not offered":   {Path: map[string]string{"E:": "Z:"}},
		"unknown address":     {IP: map[string]string{"10.9.9.9": "192.0.2.9"}},
		"unknown drive":       {Path: map[string]string{"F:": "D:"}},
	} {
		if _, err := validateRestoreChanges(scan, bad); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := validateRestoreChanges(nil, restoreChanges{}); err == nil {
		t.Error("a missing scan was accepted")
	}
}

func TestRestorePasswordsOnlyForSelectedItems(t *testing.T) {
	scan := newServerScan()
	userOnly := []restoreSelection{{ID: "accounts", Items: []string{"accounts:user:svcweb"}}}
	got, err := validateRestorePasswords(scan, userOnly, map[string]string{"account:svcreport": "unused"})
	if err != nil || len(got) != 0 {
		t.Fatalf("an optional password left empty: %v, %v", got, err)
	}
	withTask := []restoreSelection{{ID: "accounts", Items: []string{"accounts:user:svcweb"}}, {ID: "tasks", Items: []string{"tasks:nightly"}}}
	if _, err := validateRestorePasswords(scan, withTask, nil); err == nil || !strings.Contains(err.Error(), "svcweb") {
		t.Fatalf("a task without its password was accepted: %v", err)
	}
	got, err = validateRestorePasswords(scan, withTask, map[string]string{"account:svcweb": "S3cret", "account:svcreport": "not selected"})
	if err != nil || got["account:svcweb"] != "S3cret" || len(got) != 1 {
		t.Fatalf("passwords = %v, %v", got, err)
	}
	if _, err := validateRestorePasswords(scan, withTask, map[string]string{"account:svcweb": "two\nlines"}); err == nil {
		t.Error("a password with a line break was accepted")
	}
	if _, err := validateRestorePasswords(scan, withTask, map[string]string{"account:svcweb": strings.Repeat("x", restorePasswordLimit+1)}); err == nil {
		t.Error("an overlong password was accepted")
	}
}

func TestRestoreSelectionKnowsTheNewComponents(t *testing.T) {
	for _, id := range []string{"features", "accounts", "shares"} {
		found := false
		for _, known := range restoreComponentIDs {
			if known == id {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not a restore component", id)
		}
	}
	scan := newServerScan()
	if _, total, err := validateRestoreSelection(scan, []restoreSelection{{ID: "tasks", Items: []string{"tasks:nightly", "tasks:nightly"}}}); err != nil || total != 1 {
		t.Fatalf("duplicate item = %d, %v", total, err)
	}
}
