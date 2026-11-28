package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// VMware PowerCLI also has Get-VM, New-VM, Start-VM, Stop-VM, Get-VMHost and
// more. On a Hyper-V host where it is installed, an unqualified Hyper-V call
// can run PowerCLI's command, which answers "You are not currently connected
// to any servers" (MATCHC-DC02's fix-up waited on that for 15 minutes,
// 2026-11-28). Every Hyper-V call the application writes names the module.
func TestHyperVCommandsNameTheirModule(t *testing.T) {
	command := regexp.MustCompile(`(?i)(^|[^\\\w-])((Add|Checkpoint|Compare|Connect|Convert|Copy|Disable|Disconnect|Dismount|Enable|Export|Get|Import|Measure|Merge|Mount|Move|New|Optimize|Remove|Rename|Repair|Resize|Restart|Restore|Resume|Save|Set|Start|Stop|Suspend|Test|Update)-(VM|VHD)\w*)\b`)
	// Prose that names a command for the operator, and the protected
	// collector (rule 16) and the engine that defines its own Get-VM.
	prose := []string{"run Set-VMHost -EnableEnhancedSession", "(Set-VMNetworkAdapterVlan -Trunk)", "New-VM would fail at the cutover",
		// The PowerShell Direct guard itself (TestPowerShellDirectUsesHyperVsGetVM).
		"Get-Command Get-VM -ErrorAction SilentlyContinue", "function:global:Get-VM -Value"}
	skipFiles := map[string]bool{"Export-ServerInventory.ps1": true, "Move-HyperVVM.ps1": true}
	files, _ := filepath.Glob("*.ps1")
	goFiles, _ := filepath.Glob("*.go")
	files = append(files, goFiles...)
	checked := 0
	for _, file := range files {
		if skipFiles[file] || strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			allowed := false
			for _, text := range prose {
				if strings.Contains(line, text) {
					allowed = true
				}
			}
			for _, match := range command.FindAllStringSubmatch(line, -1) {
				name := match[2]
				lower := strings.ToLower(name)
				if strings.Contains(lower, "-vmware") || lower == "get-vmguid" || lower == "get-vhddetails" || allowed {
					continue
				}
				// Existence checks ask the Hyper-V module by name.
				if strings.Contains(line, "Get-Command -Module Hyper-V -Name "+name) {
					continue
				}
				t.Errorf("%s:%d calls %s without Hyper-V\\: %s", file, number+1, name, trimmed)
			}
			if strings.Contains(line, `Hyper-V\`) {
				checked++
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d qualified Hyper-V calls found; the scan is not reading the scripts", checked)
	}
}

// PowerShell Direct itself looks the VM up with an unqualified Get-VM, so
// every script that uses it imports Hyper-V first and puts a Get-VM in front
// that calls Hyper-V's when another module's would win.
func TestPowerShellDirectUsesHyperVsGetVM(t *testing.T) {
	for _, file := range []string{"Invoke-GuestRemote.ps1", "Test-RolePreflight.ps1", "Connect-HyperVConsole.ps1"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "Import-Module Hyper-V -ErrorAction Stop") || !strings.Contains(text, `function:global:Get-VM -Value { Hyper-V\Get-VM @args }`) {
			t.Errorf("%s uses PowerShell Direct without making Get-VM Hyper-V's", file)
		}
	}
	// The relay on a remote host gets the same guard.
	data, _ := os.ReadFile("Invoke-GuestRemote.ps1")
	if !strings.Contains(string(data), "[scriptblock]::Create($UseHyperV)") {
		t.Error("the hostdirect relay does not make Get-VM Hyper-V's on the host")
	}
}
