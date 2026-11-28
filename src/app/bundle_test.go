package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestBundlePassphraseRules(t *testing.T) {
	cases := []struct {
		passphrase, confirm string
		ok                  bool
	}{
		{"correct horse battery", "correct horse battery", true},
		{"short", "short", false},
		{"twelve chars", "twelve charz", false},
		{"line\nbreak in it", "line\nbreak in it", false},
		{strings.Repeat("x", bundlePassphraseMax+1), strings.Repeat("x", bundlePassphraseMax+1), false},
		{"ééééééééééé1", "ééééééééééé1", true},
	}
	for _, c := range cases {
		err := checkBundlePassphrase(c.passphrase, c.confirm)
		if (err == nil) != c.ok {
			t.Errorf("checkBundlePassphrase(%q) = %v, want ok %v", c.passphrase, err, c.ok)
		}
	}
	if limit, err := normalizeBundleLimit(0); err != nil || limit != bundleContentLimitDefault {
		t.Fatalf("default limit = %d, %v", limit, err)
	}
	for _, bad := range []int{-1, bundleContentLimitMaxGB + 1} {
		if _, err := normalizeBundleLimit(bad); err == nil {
			t.Errorf("limit %d accepted", bad)
		}
	}
}

func writeTestZip(t *testing.T, path string, names map[string]string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range names {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAppendFolderToZipKeepsEntriesAndMatchesSeparator(t *testing.T) {
	for _, separator := range []string{"/", "\\"} {
		root := t.TempDir()
		pkg := filepath.Join(root, "Inventory_SRV01")
		bundle := filepath.Join(pkg, migrationBundleFolder, "Content", "1")
		if err := os.MkdirAll(bundle, 0755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(pkg, migrationBundleFolder, bundleSettingsFileName), []byte(`{"schema":1}`), 0644)
		_ = os.WriteFile(filepath.Join(bundle, "index.html"), []byte("<p>shop</p>"), 0644)
		zipPath := pkg + ".zip"
		writeTestZip(t, zipPath, map[string]string{"Inventory_SRV01" + separator + "Report.html": "report"})
		_ = os.WriteFile(zipPath+".sha256.txt", []byte("OLD *Inventory_SRV01.zip\r\n"), 0644)

		hash, err := appendFolderToZip(zipPath, root, filepath.Join("Inventory_SRV01", migrationBundleFolder))
		if err != nil {
			t.Fatalf("separator %q: %v", separator, err)
		}
		if !regexp.MustCompile(`^[0-9A-F]{64}$`).MatchString(hash) {
			t.Fatalf("hash %q is not upper-case SHA-256", hash)
		}
		if again, _ := fileSHA256Upper(zipPath); again != hash {
			t.Fatalf("returned hash %s does not match the file %s", hash, again)
		}
		reader, err := zip.OpenReader(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, file := range reader.File {
			names[file.Name] = true
		}
		reader.Close()
		want := []string{
			"Inventory_SRV01" + separator + "Report.html",
			strings.Join([]string{"Inventory_SRV01", migrationBundleFolder, bundleSettingsFileName}, separator),
			strings.Join([]string{"Inventory_SRV01", migrationBundleFolder, "Content", "1", "index.html"}, separator),
		}
		for _, name := range want {
			if !names[name] {
				t.Errorf("separator %q: %s missing from %v", separator, name, names)
			}
		}
		if _, err := appendFolderToZip(zipPath, root, filepath.Join("Inventory_SRV01", migrationBundleFolder)); err == nil {
			t.Errorf("separator %q: a second bundle was added", separator)
		}
		if line := zipChecksumLine(hash, zipPath); line != hash+" *Inventory_SRV01.zip" {
			t.Errorf("checksum line %q", line)
		}
	}
}

func TestAppendFolderToZipLeavesTheZipAloneOnFailure(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "p.zip")
	writeTestZip(t, zipPath, map[string]string{"p/a.txt": "a"})
	before, _ := os.ReadFile(zipPath)
	if _, err := appendFolderToZip(zipPath, root, "missing-folder"); err == nil {
		t.Fatal("a missing bundle folder was accepted")
	}
	after, _ := os.ReadFile(zipPath)
	if !bytes.Equal(before, after) {
		t.Fatal("the ZIP changed after a failure")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("temporary files were left: %v", entries)
	}
}

// The secret envelope is written by the bundle script and read by the restore
// engine; the two copies must stay identical or bundles stop opening.
func TestSecretEnvelopeIsIdenticalInBothEngines(t *testing.T) {
	block := regexp.MustCompile(`(?s)# BEGIN SIMM SECRET ENVELOPE.*?# END SIMM SECRET ENVELOPE`)
	var copies []string
	for _, name := range []string{bundleEngineName, "Restore-ServerConfiguration.ps1"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		found := block.FindAllString(strings.ReplaceAll(string(data), "\r\n", "\n"), -1)
		if len(found) != 1 {
			t.Fatalf("%s has %d envelope blocks", name, len(found))
		}
		copies = append(copies, found[0])
	}
	if copies[0] != copies[1] {
		t.Fatal("the secret envelope differs between Export-MigrationBundle.ps1 and Restore-ServerConfiguration.ps1")
	}
	for _, want := range []string{"Rfc2898DeriveBytes", "HMACSHA256", "200000"} {
		if !strings.Contains(copies[0], want) {
			t.Errorf("envelope lacks %s", want)
		}
	}
}

// The bundle engine must stay free of what endpoint protection flags.
func TestBundleEngineAvoidsSuspiciousBehaviour(t *testing.T) {
	for _, name := range []string{bundleEngineName, "Restore-ServerConfiguration.ps1"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(data))
		for _, banned := range []string{"vssadmin", "wmic ", "-encodedcommand", "invoke-expression", "downloadstring", "mimikatz", "lsadump"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s contains %q", name, banned)
			}
		}
	}
}

func TestBundleSummaryText(t *testing.T) {
	text := bundleSummary{Features: 4, LocalUsers: 2, LocalGroups: 1, Shares: 3, IISSites: 2, IISPasswords: 1, WebContentFolders: 2, CertificatesInBundle: 2, CertificatesLeftOut: 1}.text()
	for _, want := range []string{"4 role(s)", "2 local user(s)", "3 file share(s)", "2 IIS site(s) with 1 password(s) and 2 website folder(s)", "2 certificate(s) with private keys", "1 certificate(s) could not be carried"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary %q lacks %q", text, want)
		}
	}
}
