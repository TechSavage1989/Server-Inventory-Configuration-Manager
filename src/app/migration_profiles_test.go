package main

import (
	"os"
	"strings"
	"testing"
)

func TestMigrationProfilesValidateSaveAndReplaceByName(t *testing.T) {
	dir := t.TempDir()
	p := migrationProfile{Name: "  Contoso\x07 ", CopyRoot: `C:\ClusterStorage\Volume1\Copies`, SyncMinutes: 30, Concurrency: 3,
		Throttle:   vmThrottleSchedule{BusinessMbps: 200, BusinessStart: 8, BusinessEnd: 18},
		Options:    migrationProfileOptions{KeepMAC: true, FixUp: true},
		NetworkMap: []vmNetworkMap{{Source: "VM Network", TargetName: "External", TargetVLAN: "20"}, {Source: "Empty"}}}
	list, err := putMigrationProfile(nil, p)
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Name != "Contoso" || len(list[0].NetworkMap) != 1 {
		t.Fatalf("cleaned profile: %+v", list[0])
	}
	if err := saveMigrationProfiles(dir, list); err != nil {
		t.Fatal(err)
	}
	again := loadMigrationProfiles(dir)
	if len(again) != 1 || again[0].SyncMinutes != 30 || !again[0].Options.FixUp || again[0].Throttle.BusinessMbps != 200 {
		t.Fatalf("reloaded: %+v", again)
	}
	p2 := migrationProfile{Name: "contoso", SyncMinutes: 60}
	list, err = putMigrationProfile(again, p2)
	if err != nil || len(list) != 1 || list[0].SyncMinutes != 60 {
		t.Fatalf("same name replaces: %+v %v", list, err)
	}
	list, _ = putMigrationProfile(list, migrationProfile{Name: "Alpha"})
	if list[0].Name != "Alpha" {
		t.Fatalf("sorted by name: %+v", list)
	}
	list, found := deleteMigrationProfile(list, "ALPHA")
	if !found || len(list) != 1 {
		t.Fatalf("delete: %+v", list)
	}
	data, _ := os.ReadFile(migrationProfilesPath(dir))
	for _, word := range []string{"password", "secret", "code"} {
		if strings.Contains(strings.ToLower(string(data)), word) {
			t.Fatalf("a profile file holds %q: %s", word, data)
		}
	}
}

func TestMigrationProfilesRefuseBadValues(t *testing.T) {
	for _, p := range []migrationProfile{
		{Name: ""},
		{Name: "A", CopyRoot: `relative\path`},
		{Name: "A", CopyRoot: `C:\a\..\Windows`},
		{Name: "A", SyncMinutes: 5},
		{Name: "A", Concurrency: 9},
		{Name: "A", Throttle: vmThrottleSchedule{LimitMbps: -1}},
		{Name: "A", Throttle: vmThrottleSchedule{BusinessEnd: 25}},
	} {
		if err := validateMigrationProfile(&p); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
	p := migrationProfile{Name: "B", VMFolder: `\\fs01\vms`}
	if err := validateMigrationProfile(&p); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(migrationProfilesPath(dir), []byte("{not json"), 0o600)
	if got := loadMigrationProfiles(dir); len(got) != 0 {
		t.Fatalf("damaged file: %+v", got)
	}
}
