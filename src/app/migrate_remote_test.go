package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestMigrateTargetNamesTheNewServer(t *testing.T) {
	for _, test := range []struct {
		target, want string
		ok           bool
	}{
		{"", "", true},
		{"  ", "", true},
		{`\\FS-NEW-02`, "FS-NEW-02", true},
		{"fs-new-02.example.test", "fs-new-02.example.test", true},
		{"srv-demo-01", "", true},
		{"SRV-DEMO-01.example.test", "", true},
		{"localhost", "", true},
		{"fs-old-07", "", false},
		{"FS-OLD-07.example.test", "", false},
		{"fs new", "", false},
		{`fs01\share`, "", false},
		{"-bad", "", false},
	} {
		got, err := migrateCleanTarget(test.target, "SRV-DEMO-01", "FS-OLD-07")
		if (err == nil) != test.ok || got != test.want {
			t.Fatalf("%q: got %q %v", test.target, got, err)
		}
	}
}

func TestMigrateWorkFoldersKeepOldMigrationsAndSeparateTargets(t *testing.T) {
	if migrateWorkKey("fs-old-07", "") != "FS-OLD-07" {
		t.Fatal("a move to this computer must keep its old folder name")
	}
	if migrateWorkKey("fs-old-07", "fs-new-02") != "FS-OLD-07_TO_FS-NEW-02" {
		t.Fatal("a move to another server needs its own folder")
	}
	if migrateWorkKey("", "x") != "" {
		t.Fatal("no source, no folder")
	}
	for in, want := range map[string]string{"fs-old-07": "FS-OLD-07", "fs.example.test": "FS.EXAMPLE.TEST", "a b/c": "A_B_C", "": "SERVER"} {
		if got := migrateRemoteWorkName(in); got != want {
			t.Fatalf("%q: %q", in, got)
		}
	}
	if len(migrateRemoteWorkName(strings.Repeat("a", 300))) != 120 {
		t.Fatal("work name not limited")
	}
}

func TestMigrateRemoteFoldersRefuseWindowsLocations(t *testing.T) {
	for _, bad := range []string{`D:`, `D:\`, `C:\Windows\Shares`, `c:\program files\x`, `C:\Program Files (x86)\x`, `C:\ProgramData\Shares`, `C:\Users\Public\x`, `D:\Shares\..\Windows`, `\\server\share\x`, `Shares\x`} {
		if migrateRemoteFolderProblem(bad) == "" {
			t.Fatalf("%s was accepted", bad)
		}
	}
	for _, good := range []string{`D:\Shares\Finance`, `C:\Data\Finance`, `E:\Program Files Old\Finance`, `C:\Users2\x`} {
		if reason := migrateRemoteFolderProblem(good); reason != "" {
			t.Fatalf("%s was refused: %s", good, reason)
		}
	}
}

func TestMigrateConfirmsWithTheNewServersName(t *testing.T) {
	if migrateConfirmName("", "SRV-DEMO-01") != "SRV-DEMO-01" || migrateConfirmName("fs-new-02.example.test", "SRV-DEMO-01") != "FS-NEW-02" {
		t.Fatal("wrong confirmation name")
	}
	for _, test := range []struct {
		typed, target string
		ok            bool
	}{
		{"srv-demo-01", "", true},
		{"FS-NEW-02", "", false},
		{"fs-new-02", "fs-new-02.example.test", true},
		{"FS-NEW-02.EXAMPLE.TEST", "fs-new-02.example.test", true},
		{"SRV-DEMO-01", "fs-new-02.example.test", false},
		{"", "fs-new-02", false},
	} {
		if migrateConfirmMatches(test.typed, test.target, "SRV-DEMO-01") != test.ok {
			t.Fatalf("%q for %q", test.typed, test.target)
		}
	}
}

func TestMigrateAutomaticCopiesFollowTheInterval(t *testing.T) {
	for _, minutes := range []int{0, 15, 60, 1440} {
		if migrateCheckAutoSync(minutes) != nil {
			t.Fatal(minutes, "refused")
		}
	}
	for _, minutes := range []int{-1, 1, 14, 1441} {
		if migrateCheckAutoSync(minutes) == nil {
			t.Fatal(minutes, "accepted")
		}
	}
	start := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	if migrateAutoSyncDue(0, start, start.Add(48*time.Hour)) {
		t.Fatal("off but due")
	}
	if migrateAutoSyncDue(30, start, start.Add(29*time.Minute)) || !migrateAutoSyncDue(30, start, start.Add(30*time.Minute)) {
		t.Fatal("wrong due time")
	}
}

// The remote runner reaches the new server only through PowerShell
// remoting: no program copied, no service, task or WMI process, no CredSSP,
// no encoded command, no Bypass, and accounts only on standard input.
func TestShareMigrationRunnerUsesOnlyPowerShellRemoting(t *testing.T) {
	raw, err := os.ReadFile(migrateRemoteRunnerName)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(raw))
	for _, word := range []string{"bypass", "-encodedcommand", "credssp", "new-service", "sc.exe", "register-scheduledtask", "schtasks", "win32_process", "invoke-wmimethod", "invoke-cimmethod", "copy-item", "-tosession", "start-process", "vssadmin", "wmic", "trustedhosts -value", "set-item wsman"} {
		if strings.Contains(text, word) {
			t.Errorf("%s uses %s", migrateRemoteRunnerName, word)
		}
	}
	for _, word := range []string{"invoke-command", "new-pssession", "[console]::in.readline()", "remove-pssession", "s-1-5-32-544"} {
		if !strings.Contains(text, word) {
			t.Errorf("%s lacks %s", migrateRemoteRunnerName, word)
		}
	}
	engine, err := os.ReadFile("Migrate-FileShares.ps1")
	if err != nil {
		t.Fatal(err)
	}
	// The engine runs as a script block inside the new server: it must never
	// call exit there.
	if !strings.Contains(string(engine), "if ($Embedded) { return $script:ExitCode }") {
		t.Fatal("the engine does not return instead of exiting when embedded")
	}
	for _, line := range strings.Split(string(engine), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "exit ") && trimmed != "exit $script:ExitCode" {
			t.Fatalf("the engine exits outside its main block: %s", trimmed)
		}
	}
}
