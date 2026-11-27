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

// Connect-HyperVConsole.ps1 runs under Windows PowerShell with the
// RemoteSigned policy the console uses (never Bypass). For a VM ID the host
// does not have (or a host without Hyper-V) Console mode must answer with one
// JSON line saying so, a
// VM ID that is not a GUID must be refused before anything is read, and the
// account given on standard input must never reach the output.
func TestHyperVConsoleScriptRefusesUnknownVMsAndKeepsTheAccountPrivate(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, consoleHyperVScriptName)
	if err := os.WriteFile(script, consoleHyperVScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	const secret = "Synthetic-Secret-7d1a"
	run := func(vmID, mode string, extra ...string) (map[string]any, string) {
		arguments := append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-VMId", vmID, "-Mode", mode, "-CredentialFromInput"}, extra...)
		command := exec.Command(powershellPath(), arguments...)
		command.Stdin = strings.NewReader(`{"user":"EXAMPLE\\tester","password":"` + secret + `","direction":"to","local":"C:\\none.txt","guest":"C:\\none.txt"}` + "\n" + `{"op":"quit"}` + "\n")
		output, _ := command.CombinedOutput()
		text := string(output)
		if strings.Contains(text, secret) {
			t.Fatalf("the account reached the output: %s", text)
		}
		var answer map[string]any
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &answer) == nil {
				break
			}
		}
		if answer == nil {
			t.Fatalf("no JSON answer from %s mode: %s", mode, text)
		}
		return answer, text
	}

	answer, text := run("not-a-guid", "Console")
	if answer["ok"] != false || !strings.Contains(answer["error"].(string), "not a GUID") {
		t.Fatalf("a VM ID that is not a GUID was not refused: %s", text)
	}

	// Hosted runners may or may not have the Hyper-V role; either way an
	// unknown VM ID must be refused with the reason.
	answer, text = run("00000000-0000-4000-8000-000000000000", "Console")
	reason, _ := answer["error"].(string)
	if answer["ok"] != false || !(strings.Contains(reason, "Hyper-V is not installed") || strings.Contains(reason, "has no VM with this ID")) {
		t.Fatalf("Console mode did not refuse an unknown VM with the reason: %s", text)
	}

	// Files mode needs PowerShell Direct into that VM, which does not exist; it
	// must fail with a JSON answer and never print the account.
	answer, text = run("00000000-0000-4000-8000-000000000000", "Files")
	if answer["ok"] != false {
		t.Fatalf("Files mode succeeded without Hyper-V: %s", text)
	}
}
