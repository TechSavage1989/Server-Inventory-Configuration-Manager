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

// TestServerMoveReadsAndChecksAServerThroughRemoting runs the live move's
// runner over WinRM to localhost (the runner is both the old and the new
// server): Read builds the package with the bundle engine inside the
// session, Scan checks it with the restore engine inside the session, a
// wrong passphrase is told apart, the passphrase never reaches the output
// or a file, StopSites refuses without IIS and Clean removes the package.
func TestServerMoveReadsAndChecksAServerThroughRemoting(t *testing.T) {
	folder := t.TempDir()
	runner := filepath.Join(folder, smRunnerName)
	bundle := filepath.Join(folder, bundleEngineName)
	restore := filepath.Join(folder, restoreEngineName)
	for name, path := range map[string]string{smRunnerName: runner, bundleEngineName: bundle, restoreEngineName: restore} {
		if _, err := writeEmbeddedFile(name, path); err != nil {
			t.Fatal(err)
		}
	}
	passphrase, err := smNewPassphrase()
	if err != nil {
		t.Fatal(err)
	}
	work := "SIMMTESTOLD_TO_SIMMTESTNEW"
	pkg := smPackageName("SIMMTESTOLD", time.Now())
	run := func(mode, engine, secret string, plan any, extra ...string) (smRunnerReply, string) {
		result := filepath.Join(folder, mode+".json")
		_ = os.Remove(result)
		arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", runner, "-Target", "localhost", "-Mode", mode, "-ResultPath", result, "-WorkName", work, "-InputFromStdin"}
		if engine != "" {
			arguments = append(arguments, "-EnginePath", engine, "-PackageName", pkg)
		}
		if plan != nil {
			data, _ := json.Marshal(plan)
			planPath := filepath.Join(folder, mode+"-plan.json")
			if err := os.WriteFile(planPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			arguments = append(arguments, "-PlanPath", planPath)
		}
		arguments = append(arguments, extra...)
		command := exec.Command(powershellPath(), arguments...)
		command.Stdin = strings.NewReader(string(smRunnerInput(smAccount{}, secret, nil)))
		output, _ := command.CombinedOutput()
		var reply smRunnerReply
		data, err := os.ReadFile(result)
		if err == nil {
			if err := json.Unmarshal(stripUTF8BOM(data), &reply); err != nil {
				t.Fatalf("%s result: %v\n%s", mode, err, data)
			}
		}
		if strings.Contains(string(output), passphrase) || strings.Contains(string(data), passphrase) {
			t.Fatalf("the passphrase reached the output or the result of %s", mode)
		}
		return reply, string(output)
	}
	defer run("Clean", "", "", nil)

	// Discover only lists (rule 76): nothing is in the package afterwards.
	reply, output := run("Discover", bundle, "", nil)
	if reply.ExitCode != 0 {
		t.Fatalf("Discover failed (%d):\n%s", reply.ExitCode, output)
	}
	found, err := smParseDiscovery(reply)
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if len(found.Features) < 2 || found.Features[0].DisplayName == "" {
		t.Fatalf("discovery listed features %+v\n%s", found.Features, output)
	}
	if entries, _ := os.ReadDir(filepath.Join(os.Getenv("ProgramData"), "Server Inventory and Migration Manager", "Server-Move", work, "Package")); len(entries) != 0 {
		t.Fatalf("Discover wrote into the package folder: %v", entries)
	}
	// Read carries only what was picked: one feature, no IIS.
	selection, _, err := smCleanSelection(smSelection{Features: []string{found.Features[0].Name}}, found, 1)
	if err != nil {
		t.Fatal(err)
	}
	reply, output = run("Read", bundle, passphrase, map[string]any{"selection": selection}, "-WebContentLimitGB", "1")
	if reply.ExitCode != 0 {
		t.Fatalf("Read failed (%d):\n%s", reply.ExitCode, output)
	}
	read, err := smParseRead(reply)
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if read.Features == 0 || read.SizeBytes == 0 || !strings.HasSuffix(reply.Package, `\`+pkg) {
		t.Fatalf("read %+v, package %q\n%s", read, reply.Package, output)
	}
	packagePath := reply.Package
	if read.Features != 1 || read.IISSites != 0 {
		t.Fatalf("the read carried %d feature(s) and %d IIS site(s), want only the one picked\n%s", read.Features, read.IISSites, output)
	}
	if _, err := os.Stat(filepath.Join(packagePath, `08-Role-Specific\IIS`)); err == nil {
		t.Fatal("IIS was not picked but its configuration was saved")
	}
	var written struct {
		AllFeatures []string `json:"allFeatures"`
		Picked      bool     `json:"picked"`
	}
	data, _ := os.ReadFile(filepath.Join(packagePath, `09-Migration-Bundle\Bundle.json`))
	if err := json.Unmarshal(stripUTF8BOM(data), &written); err != nil || !written.Picked || len(written.AllFeatures) != len(found.Features) {
		t.Fatalf("Bundle.json picked %v, allFeatures %d of %d: %v", written.Picked, len(written.AllFeatures), len(found.Features), err)
	}
	for _, name := range []string{"SHA256-Manifest.csv", `00-Summary\Live-Move.txt`, `09-Migration-Bundle\Bundle.json`, `09-Migration-Bundle\Secrets.simmsec`} {
		if _, err := os.Stat(filepath.Join(reply.Package, name)); err != nil {
			t.Fatalf("the package has no %s: %v", name, err)
		}
	}
	filepath.Walk(reply.Package, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if data, _ := os.ReadFile(path); strings.Contains(string(data), passphrase) {
				t.Fatalf("%s holds the passphrase", path)
			}
		}
		return nil
	})
	if !strings.Contains(output, "Reading local users and groups.") {
		t.Fatalf("the engine's lines were not followed live:\n%s", output)
	}

	reply, output = run("Scan", restore, passphrase, map[string]any{"mappings": restoreChanges{}})
	if reply.ExitCode != 0 {
		t.Fatalf("Scan failed (%d):\n%s", reply.ExitCode, output)
	}
	scan, err := parseRestoreScan([]byte(reply.Result))
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if !scan.ManifestVerified || scan.Bundle.Passphrase != "ok" || scan.SourceComputer != "SIMMTESTOLD" || scan.SameComputer || !scan.IsAdministrator {
		t.Fatalf("scan: verified %v, passphrase %q, source %q, same %v, admin %v\n%s", scan.ManifestVerified, scan.Bundle.Passphrase, scan.SourceComputer, scan.SameComputer, scan.IsAdministrator, output)
	}
	reply, output = run("Scan", restore, "wrong-passphrase-for-the-test", map[string]any{"mappings": restoreChanges{}})
	if scan, err := parseRestoreScan([]byte(reply.Result)); err != nil || scan.Bundle.Passphrase != "wrong" {
		t.Fatalf("a wrong passphrase gave %v, %+v\n%s", err, scan, output)
	}

	// Hosted runners may have IIS: stopping then starting the default site
	// proves both ways; without IIS both are refused.
	sites := map[string]any{"sites": []string{"Default Web Site"}}
	reply, output = run("StopSites", "", "", sites)
	if strings.Contains(output, "IIS is not installed") {
		if len(reply.Changed) != 0 {
			t.Fatalf("stopping sites without IIS changed %v:\n%s", reply.Changed, output)
		}
	} else {
		if len(reply.Failed) != 0 || len(reply.Changed) > 1 || len(reply.Changed) == 1 && reply.Changed[0] != "Default Web Site" {
			t.Fatalf("stopping the default site gave changed %v, failed %v:\n%s", reply.Changed, reply.Failed, output)
		}
		stopped := len(reply.Changed)
		reply, output = run("StartSites", "", "", sites)
		if stopped == 1 && len(reply.Changed) != 1 || len(reply.Failed) != 0 {
			t.Fatalf("starting the default site again gave changed %v, failed %v:\n%s", reply.Changed, reply.Failed, output)
		}
	}
	run("Clean", "", "", nil)
	if _, err := os.Stat(packagePath); err == nil {
		t.Fatal("Clean left the package")
	}
}
