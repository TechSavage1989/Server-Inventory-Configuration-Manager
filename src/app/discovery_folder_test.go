package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoveryWritesOneFolderPerServerAndKeepsHistoryInside(t *testing.T) {
	root := t.TempDir()
	first := vmDiscoveryFolder(root, "VMware_vc01.example.com", "vc01.example.com", "", "")
	if first != filepath.Join(root, "VMware_vc01.example.com") {
		t.Fatalf("folder = %s", first)
	}
	if again := vmDiscoveryFolder(root, "VMware_vc01.example.com", "vc01.example.com", "", ""); again != first {
		t.Fatalf("a second read of the same server went to %s", again)
	}
	chosen := filepath.Join(root, "Chosen", "VMware_vc01_20261101_090000")
	if got := vmDiscoveryFolder(root, "VMware_vc01.example.com", "VC01.example.com", "vc01.example.com", chosen); got != chosen {
		t.Fatalf("the open folder of the same server was not reused: %s", got)
	}
	if got := vmDiscoveryFolder(root, "VMware_vc02.example.com", "vc02.example.com", "vc01.example.com", chosen); got != filepath.Join(root, "VMware_vc02.example.com") {
		t.Fatalf("another server reused the open folder: %s", got)
	}

	if err := vmKeepDiscoveryHistory(first); err != nil {
		t.Fatalf("history of a folder that does not exist yet: %v", err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("history made the folder before anything was read")
	}
	if err := os.MkdirAll(first, 0700); err != nil {
		t.Fatal(err)
	}
	inventory := filepath.Join(first, vmInventoryFile)
	base := time.Date(2026, 11, 27, 9, 0, 0, 0, time.Local)
	for i := 0; i < vmDiscoveryHistoryKeep+3; i++ {
		if err := os.WriteFile(inventory, []byte(fmt.Sprintf(`{"read":%d}`, i)), 0600); err != nil {
			t.Fatal(err)
		}
		when := base.Add(time.Duration(i) * time.Minute)
		os.Chtimes(inventory, when, when)
		if err := vmKeepDiscoveryHistory(first); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(first, "History"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != vmDiscoveryHistoryKeep {
		t.Fatalf("history holds %d copies, want %d", len(entries), vmDiscoveryHistoryKeep)
	}
	newest, err := os.ReadFile(filepath.Join(first, "History", "VMware-Inventory_"+base.Add(time.Duration(vmDiscoveryHistoryKeep+2)*time.Minute).Format("20060102_150405")+".json"))
	if err != nil || string(newest) != fmt.Sprintf(`{"read":%d}`, vmDiscoveryHistoryKeep+2) {
		t.Fatalf("newest copy = %q, %v", newest, err)
	}
	if _, err := os.Stat(filepath.Join(first, "History", "VMware-Inventory_"+base.Format("20060102_150405")+".json")); !os.IsNotExist(err) {
		t.Fatal("the oldest copy was kept")
	}
	all, _ := os.ReadDir(root)
	if len(all) != 1 {
		t.Fatalf("the output folder holds %d entries, want only the one discovery folder", len(all))
	}
}
