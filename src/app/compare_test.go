package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeComparePackage writes a minimal synthetic package the way the
// collector does: UTF-8 CSV with a byte order mark and quoted fields.
func writeComparePackage(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("\xef\xbb\xbf"+content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func compareFixtures(t *testing.T) (string, string) {
	base := t.TempDir()
	before := writeComparePackage(t, filepath.Join(base, "APP01-before"), map[string]string{
		"00-Summary/Server-Summary.csv":                         "\"ComputerName\",\"Domain\",\"Manufacturer\",\"Model\",\"OperatingSystem\",\"CollectedAt\"\n\"APP01\",\"example.com\",\"VMware, Inc.\",\"VMware7,1\",\"Microsoft Windows Server 2022 Standard\",\"11/20/2026 09:00\"\n",
		"05-Software-Services-Tasks/Services.csv":               "\"Name\",\"DisplayName\",\"State\",\"StartMode\",\"StartName\",\"PathName\"\n\"VMTools\",\"VMware Tools\",\"Running\",\"Auto\",\"LocalSystem\",\"C:\\Program Files\\VMware\\vmtoolsd.exe\"\n\"AppSvc\",\"Line of business app\",\"Running\",\"Auto\",\"EXAMPLE\\svc-app\",\"D:\\App\\app.exe\"\n\"Spooler\",\"Print Spooler\",\"Running\",\"Auto\",\"LocalSystem\",\"spoolsv.exe\"\n",
		"05-Software-Services-Tasks/Installed-Applications.csv": "\"DisplayName\",\"DisplayVersion\",\"Architecture\"\n\"VMware Tools\",\"12.3.0\",\"x64\"\n\"Contoso App\",\"5.1\",\"x64\"\n",
		"04-Network/IP-Addresses.csv":                           "\"IPAddress\",\"PrefixLength\",\"InterfaceAlias\"\n\"192.0.2.10\",\"24\",\"Ethernet0\"\n\"127.0.0.1\",\"8\",\"Loopback\"\n\"fe80::1\",\"64\",\"Ethernet0\"\n",
		"04-Network/DNS-Client-Server-Addresses.csv":            "\"InterfaceAlias\",\"ServerAddresses\"\n\"Ethernet0\",\"192.0.2.53 192.0.2.54\"\n",
		"04-Network/Routes.csv":                                 "\"DestinationPrefix\",\"NextHop\"\n\"0.0.0.0/0\",\"192.0.2.1\"\n\"192.0.2.0/24\",\"0.0.0.0\"\n",
		"04-Network/TCP-Listening-Endpoints.csv":                "\"Protocol\",\"LocalAddress\",\"LocalPort\",\"ProcessName\"\n\"TCP\",\"0.0.0.0\",\"443\",\"app\"\n\"TCP\",\"0.0.0.0\",\"49670\",\"lsass\"\n",
		"03-Storage/Disks-Readable.csv":                         "\"Number\",\"FriendlyName\",\"PartitionStyle\",\"Status\",\"CapacityBytes\"\n\"0\",\"VMware Virtual disk\",\"GPT\",\"Online\",\"107374182400\"\n\"1\",\"VMware Virtual disk\",\"GPT\",\"Online\",\"53687091200\"\n",
		"07-Shares-Permissions/SMB-Shares.csv":                  "\"Name\",\"Path\"\n\"Data\",\"D:\\Data\"\n",
	})
	after := writeComparePackage(t, filepath.Join(base, "APP01-after"), map[string]string{
		"00-Summary/Server-Summary.csv":                         "\"ComputerName\",\"Domain\",\"Manufacturer\",\"Model\",\"OperatingSystem\",\"CollectedAt\"\n\"APP01\",\"example.com\",\"Microsoft Corporation\",\"Virtual Machine\",\"Microsoft Windows Server 2022 Standard\",\"11/21/2026 09:00\"\n",
		"05-Software-Services-Tasks/Services.csv":               "\"Name\",\"DisplayName\",\"State\",\"StartMode\",\"StartName\",\"PathName\"\n\"AppSvc\",\"Line of business app\",\"Stopped\",\"Auto\",\"EXAMPLE\\svc-app\",\"D:\\App\\app.exe\"\n\"Spooler\",\"Print Spooler\",\"Running\",\"Auto\",\"LocalSystem\",\"spoolsv.exe\"\n\"vmicheartbeat\",\"Hyper-V Heartbeat Service\",\"Running\",\"Manual\",\"LocalSystem\",\"svchost.exe\"\n",
		"05-Software-Services-Tasks/Installed-Applications.csv": "\"DisplayName\",\"DisplayVersion\",\"Architecture\"\n\"Contoso App\",\"5.1\",\"x64\"\n",
		"04-Network/IP-Addresses.csv":                           "\"IPAddress\",\"PrefixLength\",\"InterfaceAlias\"\n\"192.0.2.10\",\"24\",\"Ethernet 2\"\n",
		"04-Network/DNS-Client-Server-Addresses.csv":            "\"InterfaceAlias\",\"ServerAddresses\"\n\"Ethernet 2\",\"192.0.2.53\"\n",
		"04-Network/Routes.csv":                                 "\"DestinationPrefix\",\"NextHop\"\n\"0.0.0.0/0\",\"192.0.2.1\"\n",
		"04-Network/TCP-Listening-Endpoints.csv":                "\"Protocol\",\"LocalAddress\",\"LocalPort\",\"ProcessName\"\n\"TCP\",\"0.0.0.0\",\"49999\",\"lsass\"\n",
		"03-Storage/Disks-Readable.csv":                         "\"Number\",\"FriendlyName\",\"PartitionStyle\",\"Status\",\"CapacityBytes\"\n\"0\",\"Msft Virtual Disk\",\"GPT\",\"Online\",\"107374182400\"\n\"1\",\"Msft Virtual Disk\",\"GPT\",\"Offline\",\"53687091200\"\n",
		"07-Shares-Permissions/SMB-Shares.csv":                  "\"Name\",\"Path\"\n\"Data\",\"D:\\Data\"\n",
	})
	return before, after
}

func TestComparePackagesSeparatesExpectedFromAttention(t *testing.T) {
	before, after := compareFixtures(t)
	result, err := comparePackages(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SameComputer || result.Before.Manufacturer != "VMware, Inc." {
		t.Fatalf("package info: %+v", result.Before)
	}
	attention := map[string]bool{}
	for _, change := range result.Attention {
		attention[change.Label+"|"+change.Kind+"|"+change.Item] = true
	}
	for _, want := range []string{
		"Services|changed|Line of business app (AppSvc)",
		"DNS servers|removed|192.0.2.54",
		"Listening TCP ports|removed|Port 443 (app)",
		"Disks|changed|1 (Msft Virtual Disk)",
	} {
		if !attention[want] {
			t.Errorf("missing attention item %q in %v", want, attention)
		}
	}
	for key := range attention {
		if strings.Contains(key, "VMware") || strings.Contains(key, "Server|") || strings.Contains(key, "IP addresses") || strings.Contains(key, "49") {
			t.Errorf("unexpected attention item %q", key)
		}
	}
	byKey := map[string]compareDatasetResult{}
	for _, d := range result.Datasets {
		byKey[d.Key] = d
	}
	if byKey["services"].Removed != 1 || byKey["services"].Added != 1 || byKey["services"].Expected != 2 {
		t.Fatalf("services: %+v", byKey["services"])
	}
	if byKey["summary"].Changed != 1 || byKey["summary"].Expected != 1 {
		t.Fatalf("hardware change should be expected: %+v", byKey["summary"])
	}
	if byKey["addresses"].Changed != 0 || byKey["addresses"].BeforeCount != 1 {
		t.Fatalf("addresses: %+v", byKey["addresses"])
	}
	if byKey["tcp"].BeforeCount != 1 || byKey["tcp"].AfterCount != 0 {
		t.Fatalf("ephemeral ports should be ignored: %+v", byKey["tcp"])
	}
	if byKey["firewall"].Available || !strings.Contains(byKey["firewall"].Note, "not collected") {
		t.Fatalf("missing dataset should be reported: %+v", byKey["firewall"])
	}
	folder := t.TempDir()
	if err := writeCompareOutputs(folder, result, before, after); err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(result.ReportPath)
	if !strings.Contains(string(html), "Needs attention") || strings.Contains(string(html), "<script") {
		t.Fatal("comparison report content")
	}
	csvText, _ := os.ReadFile(result.CSVPath)
	if !strings.Contains(string(csvText), "Line of business app (AppSvc)") {
		t.Fatal("comparison CSV content")
	}
}

func TestComparePackagesReadsZipAndParentFolder(t *testing.T) {
	before, after := compareFixtures(t)
	zipPath := filepath.Join(t.TempDir(), "APP01-after.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	filepath.Walk(after, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(filepath.Dir(after), path)
		entry, _ := writer.Create(filepath.ToSlash(relative))
		data, _ := os.ReadFile(path)
		entry.Write(data)
		return nil
	})
	writer.Close()
	file.Close()
	result, err := comparePackages(filepath.Dir(before)+string(filepath.Separator)+"APP01-before", zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.AttentionCount == 0 {
		t.Fatal("zip package not compared")
	}
	parent := t.TempDir()
	if err := os.Rename(before, filepath.Join(parent, "only-package")); err != nil {
		t.Fatal(err)
	}
	if _, err := comparePackages(parent, after); err != nil {
		t.Fatalf("a folder holding one package was refused: %v", err)
	}
	if _, err := comparePackages(after, after); err == nil {
		t.Fatal("the same package was accepted twice")
	}
	if _, err := comparePackages(t.TempDir(), after); err == nil {
		t.Fatal("an empty folder was accepted")
	}
}
