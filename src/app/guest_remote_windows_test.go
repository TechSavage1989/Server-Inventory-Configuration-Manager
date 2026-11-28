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

// A Hyper-V host with VMware PowerCLI installed: PowerCLI's Get-VM sits
// earlier on PSModulePath than Hyper-V's. PowerShell Direct must never reach
// it (MATCHC-DC02, 2026-11-28: every try answered "You are not currently
// connected to any servers"). The runner has no Hyper-V role, so the run
// fails, but with Hyper-V's own error, never the fake PowerCLI's.
func TestPowerShellDirectNeverUsesPowerCLIsGetVM(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, guestRemoteScriptName)
	engine := filepath.Join(folder, guestEngineName)
	if err := os.WriteFile(script, guestRemoteScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeEmbeddedFile(guestEngineName, engine); err != nil {
		t.Fatal(err)
	}
	const marker = "FAKE-POWERCLI: You are not currently connected to any servers."
	modules := filepath.Join(folder, "Modules")
	fake := filepath.Join(modules, "VMware.VimAutomation.Core")
	if err := os.MkdirAll(fake, 0700); err != nil {
		t.Fatal(err)
	}
	module := "function Get-VM { throw '" + marker + "' }\nfunction Get-VMHost { throw '" + marker + "' }\nExport-ModuleMember -Function Get-VM, Get-VMHost\n"
	if err := os.WriteFile(filepath.Join(fake, "VMware.VimAutomation.Core.psm1"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "@{ RootModule = 'VMware.VimAutomation.Core.psm1'; ModuleVersion = '13.0.0'; GUID = 'f3a9b1c2-0d4e-4f5a-9b6c-7d8e9f0a1b2c'; FunctionsToExport = @('Get-VM', 'Get-VMHost') }\n"
	if err := os.WriteFile(filepath.Join(fake, "VMware.VimAutomation.Core.psd1"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	// The fake really does shadow Get-VM without the guard.
	probe := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "try { Get-VM -Id ([guid]::NewGuid()) } catch { $_.Exception.Message }")
	probe.Env = append(os.Environ(), "PSModulePath="+modules+";"+os.Getenv("PSModulePath"))
	shadow, _ := probe.CombinedOutput()
	if !strings.Contains(string(shadow), "FAKE-POWERCLI") {
		t.Logf("the fake module did not shadow Get-VM here (Hyper-V module first?):\n%s", shadow)
	}
	for _, transport := range []string{"direct", "hostdirect"} {
		result := filepath.Join(folder, transport+".json")
		arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Transport", transport, "-Mode", "Status", "-EnginePath", engine, "-ResultPath", result, "-VMName", "SIMM-NO-SUCH-VM", "-VMId", "6f1d2c3b-4a59-4e7f-8a1b-2c3d4e5f6a7b", "-CredentialFromInput"}
		if transport == "hostdirect" {
			arguments = append(arguments, "-HostName", "localhost")
		}
		command := exec.Command(powershellPath(), arguments...)
		command.Env = append(os.Environ(), "PSModulePath="+modules+";"+os.Getenv("PSModulePath"))
		command.Stdin = strings.NewReader(`{"user":"EXAMPLE\\tester","password":"Synthetic-Pw-1"}` + "\n")
		output, _ := command.CombinedOutput()
		data, _ := os.ReadFile(result)
		if strings.Contains(string(output), "FAKE-POWERCLI") || strings.Contains(string(data), "FAKE-POWERCLI") {
			t.Fatalf("%s: PowerShell Direct reached PowerCLI's Get-VM:\n%s\n%s", transport, output, data)
		}
		if len(data) == 0 {
			t.Fatalf("%s: no result:\n%s", transport, output)
		}
	}
}
