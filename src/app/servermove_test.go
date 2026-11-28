package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestServerMoveNamesAndPaths(t *testing.T) {
	if _, err := smCleanServer("", "old"); err == nil {
		t.Fatal("an empty name was accepted")
	}
	for _, bad := range []string{"web 01", "web01;calc", "-web01", `web01\c$`, "web01/x"} {
		if _, err := smCleanServer(bad, "old"); err == nil {
			t.Fatalf("%q was accepted as a server name", bad)
		}
	}
	if name, err := smCleanServer(`  \\web01.example.com `, "old"); err != nil || name != "web01.example.com" {
		t.Fatalf("got %q, %v", name, err)
	}
	if !smSameServer("web01", "WEB01.example.com") || smSameServer("web01", "web02") || smSameServer("", "") {
		t.Fatal("smSameServer compared names wrongly")
	}
	if got := smShortName("192.0.2.10"); got != "192.0.2.10" {
		t.Fatalf("an address was shortened to %q", got)
	}
	if got := smWorkName("web01.example.com", "Web_02"); got != "WEB01_TO_WEB02" {
		t.Fatalf("work name %q", got)
	}
	at := time.Date(2026, 11, 28, 10, 15, 0, 0, time.UTC)
	if got := smPackageName("web01.example.com", at); got != "WEB01_ServerInventory_20261128_101500" {
		t.Fatalf("package name %q", got)
	}
	if got := smPackageName("bad name!", at); got != "SERVER_ServerInventory_20261128_101500" {
		t.Fatalf("package name %q", got)
	}
	work := `C:\ProgramData\Server Inventory and Migration Manager\Server-Move\WEB01_TO_WEB02\Package\WEB01_ServerInventory_20261128_101500`
	if got, err := smAdminPath("WEB01", work); err != nil || got != `\\WEB01\C$\ProgramData\Server Inventory and Migration Manager\Server-Move\WEB01_TO_WEB02\Package\WEB01_ServerInventory_20261128_101500` {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{`C:\Windows\System32`, `\\other\share\Server-Move\x`, `C:\ProgramData\Server Inventory and Migration Manager\Server-Move\..\..\x`, "relative"} {
		if _, err := smAdminPath("WEB01", bad); err == nil {
			t.Fatalf("%q was accepted as a move folder", bad)
		}
	}
}

func TestServerMoveCopyMirrorsOnlyItsOwnPackage(t *testing.T) {
	args := smRobocopyArgs(`\\OLD\C$\a`, `\\NEW\C$\b`)
	if args[0] != `\\OLD\C$\a` || args[1] != `\\NEW\C$\b` || args[2] != "/MIR" {
		t.Fatalf("robocopy arguments %v", args)
	}
	for _, flag := range args {
		if strings.EqualFold(flag, "/MOV") || strings.EqualFold(flag, "/MOVE") {
			t.Fatalf("robocopy would move files: %v", args)
		}
	}
	if !smRobocopyOK(0) || !smRobocopyOK(3) || smRobocopyOK(8) || smRobocopyOK(-1) {
		t.Fatal("robocopy exit codes read wrongly")
	}
}

func TestServerMovePassphraseAndInput(t *testing.T) {
	a, err := smNewPassphrase()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := smNewPassphrase()
	if len(a) != smPassphraseLength || a == b || len(a) < bundlePassphraseMin {
		t.Fatalf("passphrases %q and %q", a, b)
	}
	line := smRunnerInput(smAccount{User: `EXAMPLE\admin`, Password: "pw"}, a, map[string]string{"account:svc": "x"})
	if line[len(line)-1] != '\n' || strings.Count(string(line), "\n") != 1 {
		t.Fatal("the runner input must be exactly one line")
	}
	var parsed map[string]any
	if err := json.Unmarshal(line, &parsed); err != nil || parsed["passphrase"] != a || parsed["user"] != `EXAMPLE\admin` {
		t.Fatalf("runner input %s: %v", line, err)
	}
	if !strings.Contains(string(smRunnerInput(smAccount{}, "", nil)), `"passwords":{}`) {
		t.Fatal("no passwords must be an empty object")
	}
}

func TestServerMoveReadsTheBundleSummary(t *testing.T) {
	reply := smRunnerReply{ExitCode: 0, SizeBytes: 1234, Result: "\ufeff" + `{"status":"succeeded","iisSites":2,"siteNames":["Default Web Site","Shop","shop",""],"certificatesInBundle":1,"webContentFolders":2}`}
	read, err := smParseRead(reply)
	if err != nil {
		t.Fatal(err)
	}
	if read.IISSites != 2 || read.SizeBytes != 1234 || len(read.SiteNames) != 2 || read.Warnings == nil {
		t.Fatalf("read %+v", read)
	}
	if _, err := smParseRead(smRunnerReply{Result: `{"status":"failed","message":"The bundle passphrase must be at least 12 characters."}`}); err == nil || !strings.Contains(err.Error(), "12 characters") {
		t.Fatalf("a failed read gave %v", err)
	}
	if _, err := smParseRead(smRunnerReply{}); err == nil {
		t.Fatal("an empty read was accepted")
	}
	if smCheckLimit(0) != smDefaultLimitGB || smCheckLimit(5000) != 2048 || smCheckLimit(20) != 20 {
		t.Fatal("limits")
	}
}

// The runner may only reach the servers through PowerShell remoting, and
// the engines it runs there must end with return when embedded.
func TestServerMoveRunnerUsesOnlyPowerShellRemoting(t *testing.T) {
	data, err := os.ReadFile(smRunnerName)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(data))
	for _, forbidden := range []string{"credssp", "-encodedcommand", "win32_process", "register-scheduledtask", "new-service", "sc.exe", "trustedhosts -value", "set-item wsman", "copy-item -tosession", "start-process", "invoke-wmimethod", "invoke-expression"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("%s uses %s", smRunnerName, forbidden)
		}
	}
	if !strings.Contains(text, "invoke-command -session") || !strings.Contains(text, "new-pssession") {
		t.Fatal("the runner must use PowerShell remoting sessions")
	}
	for _, name := range []string{bundleEngineName, "Restore-ServerConfiguration.ps1"} {
		engine, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(engine), "if ($Embedded) { return ") {
			t.Fatalf("%s must return its exit code when embedded", name)
		}
	}
}

func smTestDiscovery(t *testing.T) *smDiscovery {
	t.Helper()
	text := `{"status":"succeeded","computer":"WEB01","features":[{"name":"Web-Server","displayName":"Web Server (IIS)"},{"name":"DHCP","displayName":"DHCP Server"}],
	"users":[{"name":"svc-shop"}],"groups":[{"name":"Shop Editors"},{"name":"Administrators","builtIn":true,"members":2}],"shares":[{"name":"Data","path":"D:\\Data"}],
	"iis":{"installed":true,"sites":[{"name":"Shop"}],"appPools":2,"roots":[{"path":"D:\\Sites\\Shop","sizeBytes":1073741824},{"path":"D:\\Sites\\Big","sizeBytes":12884901888},{"path":"C:\\Windows\\web","reason":"The folder is inside C:\\Windows, so it is never copied."}]},
	"certificates":[{"thumbprint":"AA11","exportable":true},{"thumbprint":"BB22","exportable":false}],
	"dns":{"installed":true,"forwarders":["192.0.2.53"],"zones":[{"name":"partner.example.com","type":"Secondary"}]},"dhcp":{"installed":true,"authorized":true}}`
	d, err := smParseDiscovery(smRunnerReply{Result: "\ufeff" + text})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestServerMoveSelectionKeepsOnlyWhatTheOldServerListed(t *testing.T) {
	d := smTestDiscovery(t)
	if d.Computer != "WEB01" || len(d.Features) != 2 || len(d.IIS.Roots) != 3 || d.Warnings == nil {
		t.Fatalf("discovery = %+v", d)
	}
	picked, size, err := smCleanSelection(smSelection{
		Features: []string{"web-server", "Web-Server", "NotThere"}, IIS: true, WebRoots: []string{`d:\sites\shop`, `C:\Windows\web`},
		Certificates: []string{"aa11", "BB22"}, Users: []string{"SVC-SHOP", "ghost"}, Groups: []string{"administrators"},
		Shares: []string{"data"}, DNSForwarders: true, DNSZones: []string{"partner.example.com", "other.example.com"}, DHCP: true,
	}, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := smSelection{Features: []string{"Web-Server"}, IIS: true, WebRoots: []string{`D:\Sites\Shop`}, Certificates: []string{"AA11"},
		Users: []string{"svc-shop"}, Groups: []string{"Administrators"}, Shares: []string{"Data"}, DNSForwarders: true, DNSZones: []string{"partner.example.com"}, DHCP: true}
	if got, _ := json.Marshal(picked); string(got) != string(smJSON(want)) {
		t.Fatalf("picked = %s", got)
	}
	if size != 1<<30 || picked.Count() != 10 {
		t.Fatalf("size %d count %d", size, picked.Count())
	}
}

func smJSON(v any) []byte { data, _ := json.Marshal(v); return data }

func TestServerMoveSelectionRefusals(t *testing.T) {
	d := smTestDiscovery(t)
	if _, _, err := smCleanSelection(smSelection{IIS: true}, nil, 10); err == nil || !strings.Contains(err.Error(), "Look at the old server first") {
		t.Fatalf("no discovery: %v", err)
	}
	if _, _, err := smCleanSelection(smSelection{Features: []string{"Nope"}}, d, 10); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("empty: %v", err)
	}
	if _, _, err := smCleanSelection(smSelection{WebRoots: []string{`D:\Sites\Shop`}}, d, 10); err == nil || !strings.Contains(err.Error(), "pick IIS too") {
		t.Fatalf("files without IIS: %v", err)
	}
	if _, _, err := smCleanSelection(smSelection{IIS: true, WebRoots: []string{`D:\Sites\Big`}}, d, 10); err == nil || !strings.Contains(err.Error(), "more than the 10 GB limit") {
		t.Fatalf("over the limit: %v", err)
	}
	if _, size, err := smCleanSelection(smSelection{IIS: true, WebRoots: []string{`D:\Sites\Big`}}, d, 20); err != nil || size != 12<<30 {
		t.Fatalf("raised limit: %v %d", err, size)
	}
	if _, err := smParseDiscovery(smRunnerReply{Result: `{"status":"failed","message":"Access is denied."}`}); err == nil || err.Error() != "Access is denied." {
		t.Fatalf("failed list: %v", err)
	}
	empty := smSelection{}
	if got := string(smJSON(empty)); !strings.Contains(got, `"features":null`) {
		t.Fatalf("zero selection json %s", got)
	}
}
