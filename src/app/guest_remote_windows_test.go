//go:build windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Invoke-GuestRemote.ps1 runs the real guest engine inside "another"
// computer through PowerShell remoting. GitHub runners answer WinRM on
// localhost (TestConnectHyperVTargetScriptRemoting relies on it too), so the
// runner plays the guest: nothing is copied to it but data, the engine's
// result comes back, a recording comes back to the console, a vCenter
// recording never replaces a fuller one made inside Windows, and the
// account never reaches the output.
func TestGuestRemoteRunsTheEngineThroughPowerShellRemoting(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, guestRemoteScriptName)
	engine := filepath.Join(folder, guestEngineName)
	if err := os.WriteFile(script, guestRemoteScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeEmbeddedFile(guestEngineName, engine); err != nil {
		t.Fatal(err)
	}
	const secret = "Synthetic-Guest-Secret-91d0"
	run := func(computer, mode, capture, export string, account bool) (map[string]any, string) {
		result := filepath.Join(folder, mode+"-"+computer+".json")
		_ = os.Remove(result)
		arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Transport", "winrm", "-Mode", mode, "-EnginePath", engine, "-ResultPath", result, "-ComputerName", computer, "-CredentialFromInput"}
		if capture != "" {
			arguments = append(arguments, "-CapturePath", capture)
		}
		if export != "" {
			arguments = append(arguments, "-ExportPath", export)
		}
		command := exec.Command(powershellPath(), arguments...)
		if account {
			command.Stdin = strings.NewReader(`{"user":"EXAMPLE\\tester","password":"` + secret + `"}` + "\n")
		} else {
			command.Stdin = strings.NewReader("{}\n")
		}
		output, _ := command.CombinedOutput()
		data, err := os.ReadFile(result)
		if err != nil {
			t.Fatalf("no result for %s on %s: %v\n%s", mode, computer, err, output)
		}
		if strings.Contains(string(output), secret) || strings.Contains(string(data), secret) {
			t.Fatal("the password reached the output or the result")
		}
		var envelope map[string]any
		if err := json.Unmarshal(stripUTF8BOM(data), &envelope); err != nil {
			t.Fatalf("result: %v\n%s", err, data)
		}
		return envelope, string(output)
	}
	if envelope, output := run("simm-no-such-host.invalid", "Status", "", "", true); envelope["ok"] == true {
		t.Fatalf("a computer that does not exist answered:\n%s", output)
	}
	export := filepath.Join(folder, "Console")
	envelope, output := run("localhost", "Capture", "", export, false)
	if envelope["ok"] != true {
		if strings.Contains(output, "WinRM") {
			t.Skipf("WinRM does not answer on this runner:\n%s", output)
		}
		t.Fatalf("Capture through remoting failed: %v\n%s", envelope["error"], output)
	}
	kept, err := os.ReadFile(filepath.Join(export, "Guest-Capture.json"))
	if err != nil || !strings.Contains(string(kept), `"Adapters"`) {
		t.Fatalf("the recording did not come back to the console: %v\n%s", err, output)
	}
	var recorded struct {
		State struct {
			ComputerName string `json:"ComputerName"`
		} `json:"State"`
	}
	if json.Unmarshal(stripUTF8BOM(kept), &recorded) != nil || !strings.EqualFold(recorded.State.ComputerName, os.Getenv("COMPUTERNAME")) {
		t.Fatalf("recorded computer = %q", recorded.State.ComputerName)
	}
	// An older recording from vCenter must not replace the one Windows made.
	vcenter, _ := json.Marshal(map[string]any{"Schema": 1, "Source": "vCenter", "CapturedAt": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "State": map[string]any{"ComputerName": "OTHER", "Adapters": []any{}}})
	vcenterPath := filepath.Join(folder, "vcenter.json")
	if err := os.WriteFile(vcenterPath, vcenter, 0600); err != nil {
		t.Fatal(err)
	}
	envelope, output = run("localhost", "Status", vcenterPath, "", false)
	if envelope["ok"] != true {
		t.Fatalf("Status through remoting failed: %v\n%s", envelope["error"], output)
	}
	result, _ := envelope["result"].(map[string]any)
	capture, _ := result["Capture"].(map[string]any)
	if capture == nil || capture["Source"] == "vCenter" {
		t.Fatalf("Status used the wrong recording: %v\n%s", capture, output)
	}
	if strings.Contains(output, "Used the recording kept by the console") {
		t.Fatalf("the console's vCenter recording replaced the guest's own:\n%s", output)
	}
	// Nothing but data is left behind in the guest.
	temp, _ := filepath.Glob(filepath.Join(os.TempDir(), "SIMM-Guest-*"))
	if len(temp) != 0 {
		t.Fatalf("per-run folders were left behind: %v", temp)
	}
}
