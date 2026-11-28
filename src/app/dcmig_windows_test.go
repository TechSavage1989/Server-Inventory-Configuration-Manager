//go:build windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Invoke-DcMigration.ps1 reaches servers through PowerShell remoting. The
// GitHub runner answers WinRM on localhost and is not a domain controller,
// so it plays the new server: ReadNew reads it, ReadOld refuses it as an old
// DC, a computer that does not exist fails, and a typed account never
// reaches the output or the result.
func TestDCMigrationScriptReadsTheRunnerThroughRemoting(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, dcScriptName)
	if err := os.WriteFile(script, dcScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	const secret = "Synthetic-DC-Secret-8f2a"
	run := func(mode string, input map[string]any) (dcEngineResult, string) {
		result := filepath.Join(folder, "result-"+mode+".json")
		_ = os.Remove(result)
		line, _ := json.Marshal(input)
		command := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Mode", mode, "-ResultPath", result)
		command.Stdin = strings.NewReader(string(line) + "\n")
		output, _ := command.CombinedOutput()
		data, err := os.ReadFile(result)
		if err != nil {
			t.Fatalf("no result for %s: %v\n%s", mode, err, output)
		}
		if strings.Contains(string(output), secret) || strings.Contains(string(data), secret) {
			t.Fatal("a password reached the output or the result")
		}
		var parsed dcEngineResult
		if err := json.Unmarshal(stripUTF8BOMBytes(data), &parsed); err != nil {
			t.Fatalf("%v\n%s", err, data)
		}
		return parsed, string(output)
	}
	if res, output := run("ReadNew", map[string]any{"target": "simm-no-such-host.invalid", "user": `EXAMPLE\tester`, "password": secret, "dsrm": secret}); res.OK {
		t.Fatalf("a computer that does not exist answered:\n%s", output)
	}
	res, output := run("ReadNew", map[string]any{"target": "localhost"})
	if !res.OK {
		if strings.Contains(output+res.Error, "WinRM") {
			t.Skipf("WinRM does not answer on this runner:\n%s", output)
		}
		t.Fatalf("ReadNew failed: %s\n%s", res.Error, output)
	}
	if res.New == nil || !strings.EqualFold(res.New.Name, os.Getenv("COMPUTERNAME")) || res.New.OS == "" || res.New.IsDC {
		t.Fatalf("new = %+v", res.New)
	}
	if res.New.FreeGB <= 0 {
		t.Fatalf("free space was not read: %+v", res.New)
	}
	if res, output := run("ReadOld", map[string]any{"source": "localhost"}); res.OK && res.Old != nil && res.Old.Domain != "" {
		t.Fatalf("the runner was read as a domain controller:\n%s", output)
	}
}
