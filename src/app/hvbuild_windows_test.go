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

// TestBuildHyperVHostEngineOnThisComputer runs Build-HyperVHost.ps1 on the
// runner, which has no Hyper-V role: Read, a preview of the roles step whose
// change hash must match Go's, and applies that must change nothing because
// their approval does not match.
func TestBuildHyperVHostEngineOnThisComputer(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, hvBuildScriptName)
	if err := os.WriteFile(script, hvBuildScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	const secret = "Synthetic-Secret-8d1f"
	run := func(name string, arguments []string, account bool) []byte {
		result := filepath.Join(folder, name+".json")
		full := append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-ResultPath", result}, arguments...)
		if account {
			full = append(full, "-CredentialFromInput")
		}
		command := exec.Command(powershellPath(), full...)
		if account {
			command.Stdin = strings.NewReader(`{"user":"EXAMPLE\\tester","password":"` + secret + `","secrets":{"cloudKey":"` + secret + `"}}` + "\n")
		}
		output, _ := command.CombinedOutput()
		data, err := os.ReadFile(result)
		if err != nil {
			t.Fatalf("%s wrote no result: %v\n%s", name, err, output)
		}
		if strings.Contains(string(output), secret) || strings.Contains(string(data), secret) {
			t.Fatalf("%s: the password reached the output or the result", name)
		}
		return data
	}
	here, _ := os.Hostname()

	facts := &hvBuildFacts{}
	if err := hvRemoteParseResult(run("read", []string{"-Mode", "Read", "-Computers", here}, true), facts); err != nil {
		t.Fatal(err)
	}
	node, ok := facts.Nodes[here]
	if !ok || !node.OK || node.Facts == nil || node.Facts.Computer == "" || node.Facts.Build == 0 || len(node.Facts.Disks) == 0 {
		t.Fatalf("read %+v", facts)
	}
	if facts.Local.Computer == "" || facts.Local.Account == "" {
		t.Fatalf("local facts %+v", facts.Local)
	}
	t.Logf("read %s: %s, %d adapters, %d disks, features %v", node.Facts.Computer, node.Facts.Caption, len(node.Facts.Adapters), len(node.Facts.Disks), node.Facts.Features)

	plan := hvBuildPlan{Name: "Runner build", Nodes: []hvBuildNode{{Name: here}}, Switches: []hvBuildSwitch{{Name: "SET-Test", ManagementOS: true}}, Storage: hvBuildStorage{Kind: "iscsi", MPIO: true, Portals: []string{"192.0.2.5:3260"}}}
	plan, err := hvBuildCheckPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	body, err := hvBuildScript(plan, "roles", here)
	if err != nil {
		t.Fatal(err)
	}
	stepPath := filepath.Join(folder, "Step.ps1")
	if err := os.WriteFile(stepPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Runs []hvBuildRun `json:"runs"`
	}
	if err := hvRemoteParseResult(run("preview", []string{"-Mode", "Run", "-Computers", here, "-StepPath", stepPath}, false), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Runs) != 1 || !preview.Runs[0].OK || preview.Runs[0].Output == nil {
		t.Fatalf("preview = %+v", preview)
	}
	output := preview.Runs[0].Output
	if output.Hash != hvBuildChangeHash(output.Changes) {
		t.Fatalf("PowerShell's change hash %s differs from Go's %s for %q", output.Hash, hvBuildChangeHash(output.Changes), output.Changes)
	}
	if len(output.Applied) != 0 || output.Refused != "" || output.Error != "" {
		t.Fatalf("a preview changed something: %+v", output)
	}
	if !vmHyperVHere() && len(output.Changes)+len(output.Blocks) == 0 {
		t.Fatalf("a computer without Hyper-V lists nothing to install: %+v", output)
	}
	t.Logf("preview: changes %q, blocks %q, notes %q", output.Changes, output.Blocks, output.Notes)

	approved := filepath.Join(folder, "Approved.json")
	data, _ := json.Marshal(map[string]string{here: strings.Repeat("0", 64)})
	if err := os.WriteFile(approved, data, 0600); err != nil {
		t.Fatal(err)
	}
	var refused struct {
		Runs []hvBuildRun `json:"runs"`
	}
	if err := hvRemoteParseResult(run("refused", []string{"-Mode", "Run", "-Computers", here, "-StepPath", stepPath, "-ApprovedPath", approved, "-Apply"}, false), &refused); err != nil {
		t.Fatal(err)
	}
	if len(refused.Runs) != 1 || refused.Runs[0].Output == nil || refused.Runs[0].Output.Refused == "" || len(refused.Runs[0].Output.Applied) != 0 {
		t.Fatalf("an apply with the wrong approval was not refused: %+v", refused.Runs)
	}
	var missing struct {
		Runs []hvBuildRun `json:"runs"`
	}
	if err := hvRemoteParseResult(run("missing", []string{"-Mode", "Run", "-Computers", here, "-StepPath", stepPath, "-Apply"}, false), &missing); err != nil {
		t.Fatal(err)
	}
	if len(missing.Runs) != 1 || missing.Runs[0].Error == "" || missing.Runs[0].Output != nil {
		t.Fatalf("an apply without any approval ran: %+v", missing.Runs)
	}
}

// TestHVBuildScriptsParseInWindowsPowerShell parses every step script of a
// full cluster plan with the Windows PowerShell parser.
func TestHVBuildScriptsParseInWindowsPowerShell(t *testing.T) {
	plan := hvBuildTestPlan(t)
	folder := t.TempDir()
	count := 0
	for _, step := range hvBuildSteps(plan) {
		for index, computer := range hvBuildStepComputers(plan, step) {
			script, err := hvBuildScript(plan, step.ID, computer)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(folder, step.ID+"-"+string(rune('a'+index))+".ps1"), []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			count++
		}
	}
	check := `$bad = 0; foreach ($file in Get-ChildItem -LiteralPath $args[0] -Filter *.ps1) { $tokens = $null; $errors = $null; [void][System.Management.Automation.Language.Parser]::ParseFile($file.FullName, [ref]$tokens, [ref]$errors); foreach ($e in $errors) { $bad++; [Console]::Out.WriteLine($file.Name + ':' + $e.Extent.StartLineNumber + ': ' + $e.Message) } }; [Console]::Out.WriteLine('bad=' + $bad)`
	checker := filepath.Join(t.TempDir(), "Check.ps1")
	if err := os.WriteFile(checker, []byte(check), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", checker, folder).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "bad=0") {
		t.Fatalf("%d scripts, parse errors: %v\n%s", count, err, output)
	}
}
