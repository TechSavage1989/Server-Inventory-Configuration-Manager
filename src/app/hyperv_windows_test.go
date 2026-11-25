//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Read-HyperVHost.ps1 runs under Windows PowerShell on this computer. GitHub
// runners have no Hyper-V role, so there it must fail with a clear message;
// where Hyper-V is installed the report must convert into an inventory. The
// account given on standard input must never reach the output or result.
func TestReadHyperVHostScriptOnThisComputer(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, hypervReadScript)
	if err := os.WriteFile(script, hypervReadScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(folder, "Result.json")
	const secret = "Synthetic-Secret-9f3b"
	command := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-ResultPath", result, "-CredentialFromInput")
	command.Stdin = strings.NewReader(`{"user":"EXAMPLE\\tester","password":"` + secret + `"}` + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("the script failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("no result: %v\n%s", err, output)
	}
	if strings.Contains(string(output), secret) || strings.Contains(string(data), secret) {
		t.Fatal("the password reached the output or the result")
	}
	if !strings.Contains(string(output), "used only for a remote host") {
		t.Fatalf("the local read should say the account is not used:\n%s", output)
	}
	report, err := parseHypervResult(data)
	if err != nil {
		if !strings.Contains(err.Error(), "Hyper-V is not installed") && !strings.Contains(err.Error(), "Access is denied") {
			t.Fatalf("unexpected failure: %v\n%s", err, output)
		}
		t.Logf("Hyper-V is not available here: %v", err)
		return
	}
	inventory := hypervInventoryFromReport(*report, true, time.Now(), "test")
	if inventory.ServerType != hypervServerType || len(inventory.Hosts) != 1 || inventory.Hosts[0].Cores == 0 {
		t.Fatalf("inventory %+v", inventory)
	}
	t.Logf("read %d VM(s)", len(inventory.VMs))
}

// Move-HyperVVM.ps1 runs under Windows PowerShell on this computer. GitHub
// runners have no Hyper-V role, so State must fail with a clear message and a
// result file; an ID that is not a VM ID is refused before anything is read.
func TestMoveHyperVScriptOnThisComputer(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, hypervMoveScript)
	if err := os.WriteFile(script, hypervMoveScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(mode, vmID string) (map[string]any, string) {
		result := filepath.Join(folder, mode+"-Result.json")
		command := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-Mode", mode, "-VMId", vmID, "-ResultPath", result)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("the script failed: %v\n%s", err, output)
		}
		data, err := os.ReadFile(result)
		if err != nil {
			t.Fatalf("no result: %v\n%s", err, output)
		}
		var envelope map[string]any
		if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &envelope); err != nil {
			t.Fatalf("result %s: %v", data, err)
		}
		return envelope, string(output)
	}
	envelope, output := run("State", "not-a-vm")
	if envelope["ok"] != false || !strings.Contains(fmt.Sprint(envelope["error"]), "not a Hyper-V VM ID") {
		t.Fatalf("a bad ID must be refused: %v\n%s", envelope, output)
	}
	envelope, output = run("State", "00000000-0000-4000-8000-000000000000")
	if envelope["ok"] == true {
		t.Fatalf("a VM that does not exist was read: %v", envelope)
	}
	message := fmt.Sprint(envelope["error"])
	if !strings.Contains(message, "Hyper-V is not installed") && !strings.Contains(message, "was not found") && !strings.Contains(message, "Access is denied") {
		t.Fatalf("unexpected failure: %s\n%s", message, output)
	}
	t.Logf("State: %s", message)
	// Removing a checkpoint that was never named is refused.
	envelope, _ = run("RemoveCheckpoint", "00000000-0000-4000-8000-000000000000")
	if envelope["ok"] == true {
		t.Fatalf("RemoveCheckpoint without an ID succeeded: %v", envelope)
	}
}
