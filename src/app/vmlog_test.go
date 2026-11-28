package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVMLogsKeepEachVMsLinesAndRotate(t *testing.T) {
	logs := newVMLogFiles(t.TempDir())
	logs.Write("MATCHC-DC02", "2026-11-28 03:40:00 Taking the replication snapshot.")
	logs.Write("MATCHC-DC02", "[INFO] Waiting for Windows in the VM to start (x).")
	logs.Write("APP01", "Copy started.")
	logs.Shared("Plan: MATCHC-DC02: cut over")
	text, err := logs.Read(logs.Path("MATCHC-DC02"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "2026-11-28 03:40:00 ") || !strings.Contains(lines[1], "[INFO] Waiting") || lines[1][4] != '-' {
		t.Fatalf("lines %q", lines)
	}
	if other, _ := logs.Read(logs.Path("APP01")); strings.Contains(other, "MATCHC") {
		t.Fatal("one VM's lines reached another VM's log")
	}
	if shared, _ := logs.Read(logs.SharedPath()); !strings.Contains(shared, "cut over") {
		t.Fatalf("shared %q", shared)
	}
	// A name never reaches outside the folder.
	for _, name := range []string{`..\..\evil`, "a/b", "C:x", "   ", "...", "con:"} {
		path := logs.Path(name)
		if filepath.Dir(path) != logs.Folder() {
			t.Fatalf("%q gives %s", name, path)
		}
	}
	// Rotation keeps one older file and reading joins them.
	big := strings.Repeat("x", 1<<20)
	for i := 0; i < 6; i++ {
		logs.Write("BIG", big)
	}
	logs.Write("BIG", "last line")
	if _, err := os.Stat(strings.TrimSuffix(logs.Path("BIG"), ".log") + ".1.log"); err != nil {
		t.Fatal("no rotated file")
	}
	if info, _ := os.Stat(logs.Path("BIG")); info.Size() > vmLogRotateBytes {
		t.Fatalf("current file %d bytes", info.Size())
	}
	got, _ := logs.Read(logs.Path("BIG"))
	if !strings.HasSuffix(strings.TrimSpace(got), "last line") || len(got) > vmLogReadLimit {
		t.Fatalf("read %d bytes", len(got))
	}
	if vmLogStamp("hello", time.Date(2026, 11, 28, 1, 2, 3, 0, time.Local)) != "2026-11-28 01:02:03 hello" {
		t.Fatal("stamp")
	}
}

func TestPlanLinesAboutAVMReachItsOwnLog(t *testing.T) {
	var got [][2]string
	r := &mbRunner{log: func(string) {}, logVM: func(vm, text string) { got = append(got, [2]string{vm, text}) }}
	r.logFor("APP01", "cut over; the new VM was created.")
	if len(got) != 1 || got[0][0] != "APP01" || got[0][1] != "cut over; the new VM was created." {
		t.Fatalf("got %v", got)
	}
}
