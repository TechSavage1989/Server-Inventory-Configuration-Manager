//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test-RolePreflight.ps1 reads a server through PowerShell remoting. GitHub
// runners answer WinRM on localhost and are neither a domain controller nor
// (usually) a SQL Server, so the runner plays the server: the checks run,
// say what they found, leave nothing behind, and a typed account never
// reaches the output.
func TestRolePreflightScriptReadsAServerThroughRemoting(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, preflightScriptName)
	if err := os.WriteFile(script, preflightScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	const secret = "Synthetic-Preflight-Secret-5c1e"
	run := func(computer, input string) (preflightResult, string) {
		result := filepath.Join(folder, "result-"+computer+".json")
		_ = os.Remove(result)
		command := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Transport", "winrm", "-ComputerName", computer, "-Checks", "dc,sql", "-ResultPath", result, "-CredentialFromInput")
		command.Stdin = strings.NewReader(input + "\n")
		output, _ := command.CombinedOutput()
		data, err := os.ReadFile(result)
		if err != nil {
			t.Fatalf("no result for %s: %v\n%s", computer, err, output)
		}
		if strings.Contains(string(output), secret) || strings.Contains(string(data), secret) {
			t.Fatal("the password reached the output or the result")
		}
		parsed, err := parsePreflightResult(data)
		if err != nil {
			t.Fatalf("%v\n%s", err, data)
		}
		return parsed, string(output)
	}
	if result, output := run("simm-no-such-host.invalid", `{"user":"EXAMPLE\\tester","password":"`+secret+`"}`); result.OK {
		t.Fatalf("a computer that does not exist answered:\n%s", output)
	}
	result, output := run("localhost", "{}")
	if !result.OK {
		if strings.Contains(output, "WinRM") || strings.Contains(result.Error, "WinRM") {
			t.Skipf("WinRM does not answer on this runner:\n%s", output)
		}
		t.Fatalf("the checks failed: %s\n%s", result.Error, output)
	}
	facts := result.Facts
	if !strings.EqualFold(facts.Computer, os.Getenv("COMPUTERNAME")) || facts.OS == "" {
		t.Fatalf("facts = %+v", facts)
	}
	if facts.DC == nil || !facts.DC.Checked || facts.DC.IsDC {
		t.Fatalf("the runner is not a domain controller: %+v", facts.DC)
	}
	if facts.SQL == nil || !facts.SQL.Checked {
		t.Fatalf("SQL Server was not checked: %+v", facts.SQL)
	}
	check := &vmPreflight{Computer: "localhost", Via: "winrm", Facts: facts}
	codes := preflightCodesOf(preflightFindings(check))
	if codes["dc-not-dc"] == "" {
		t.Fatalf("findings = %v", codes)
	}
	if len(facts.SQL.Instances) == 0 && codes["sql-none"] == "" {
		t.Fatalf("findings = %v", codes)
	}
	t.Logf("SQL Server instances on the runner: %d; findings %v", len(facts.SQL.Instances), codes)
}
