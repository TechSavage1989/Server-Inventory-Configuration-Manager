package main

import (
	"strings"
	"testing"
	"time"
)

// A result as Test-RolePreflight.ps1 writes it, with the one-element arrays
// PowerShell sometimes writes as plain values.
const preflightSampleDC = `{"ok":true,"facts":{"computer":"DC01","os":"Microsoft Windows Server 2019 Standard","osVersion":"10.0.17763","domain":"example.com",
"dc":{"checked":true,"isDC":true,"domainName":"example.com","forest":"example.com","site":"HQ","globalCatalog":true,"roles":"PDC emulator","sysvolReady":true,"sysvolReplication":"DFSR","dfsrState":4,"dsaNotWritable":0,"usnRollbackEvent":false,
"replLinks":5,"replPartners":1,"replFailing":[],"replLargestHours":0,"timeSource":"time.example.com,0x8","dcdiagPassed":["Advertising","Services","NetLogons","SysVolCheck","Replications","MachineAccount","KccEvent","DFSREvent"],"dcdiagFailed":[],"errors":[]}}}`

func preflightSample(t *testing.T, text string) *vmPreflight {
	t.Helper()
	result, err := parsePreflightResult([]byte("\xEF\xBB\xBF" + text))
	if err != nil {
		t.Fatal(err)
	}
	return &vmPreflight{CheckedAt: vmTestNow, Computer: "dc01.example.com", Via: "winrm", Checks: []string{"dc", "sql"}, Error: result.Error, Facts: result.Facts}
}

func preflightCodesOf(findings []vmFinding) map[string]string {
	codes := map[string]string{}
	for _, f := range findings {
		codes[f.Code] = f.Severity
	}
	return codes
}

func TestPreflightHealthyDomainControllerIsReady(t *testing.T) {
	check := preflightSample(t, preflightSampleDC)
	if len(check.Facts.DC.Roles) != 1 || check.Facts.DC.Roles[0] != "PDC emulator" {
		t.Fatalf("a single role written as a plain value was not read: %v", check.Facts.DC.Roles)
	}
	codes := preflightCodesOf(preflightFindings(check))
	if codes["dc-ok"] != vmSeverityInfo || codes["dc-fsmo"] != vmSeverityInfo || len(codes) != 2 {
		t.Fatalf("findings = %v", codes)
	}
	vm := vmSampleInventory().VMs[0]
	vm.Name, vm.Preflight = "DC01", check
	if r := assessVM(vm, vmTargetHyperV, vmTestNow); vmFindingCodes(&r)["dc-ok"] == "" {
		t.Fatalf("readiness lacks the check: %v", vmFindingCodes(&r))
	}
	if role, why := vmWaveRoleOf(vm); role != vmWaveRoleDC || !strings.Contains(why, "Checked") {
		t.Fatalf("wave role = %s (%s)", role, why)
	}
	facts := preflightFacts(check)
	joined := ""
	for _, row := range facts {
		joined += row[0] + "=" + row[1] + "\n"
	}
	for _, want := range []string{"FSMO roles=PDC emulator", "SYSVOL=DFSR, ready Yes, DFSR normal", "USN rollback=None", "dcdiag=8 passed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("report facts lack %q:\n%s", want, joined)
		}
	}
}

func TestPreflightUnhealthyDomainControllerIsBlocked(t *testing.T) {
	check := preflightSample(t, preflightSampleDC)
	dc := check.Facts.DC
	dc.DsaNotWritable = 4
	dc.ReplFailing = preflightStrings{"DC02 (DC=example,DC=com): 12 failure(s), last status 8453"}
	state := 5
	dc.DFSRState = &state
	dc.SysvolRepl = "FRS"
	dc.DcdiagFailed = preflightStrings{"Replications"}
	dc.TimeSource = "Local CMOS Clock"
	check.Facts.OSVersion = "6.1.7601"
	codes := preflightCodesOf(preflightFindings(check))
	for code, severity := range map[string]string{"dc-usn-rollback": vmSeverityBlocker, "dc-replication": vmSeverityBlocker, "dc-sysvol": vmSeverityBlocker, "dc-frs": vmSeverityWarning, "dc-dcdiag": vmSeverityWarning, "dc-time": vmSeverityWarning, "dc-vmgenid": vmSeverityWarning} {
		if codes[code] != severity {
			t.Fatalf("%s = %q, want %s (all: %v)", code, codes[code], severity, codes)
		}
	}
	if codes["dc-ok"] != "" {
		t.Fatal("an unhealthy domain controller was called healthy")
	}
	vm := vmSampleInventory().VMs[0]
	vm.Preflight = check
	if r := assessVM(vm, vmTargetScale, vmTestNow); r.Status != vmStatusBlocked {
		t.Fatalf("status = %s", r.Status)
	}
	dc.ReplFailing, dc.DsaNotWritable, dc.DFSRState, dc.ReplLargestHours = nil, 0, nil, 72
	if codes := preflightCodesOf(preflightFindings(check)); codes["dc-replication-stale"] != vmSeverityWarning || codes["dc-sysvol"] != "" {
		t.Fatalf("stale replication: %v", codes)
	}
	dc.TimeSource = "VM IC Time Synchronization Provider"
	if codes := preflightCodesOf(preflightFindings(check)); codes["dc-time"] != vmSeverityWarning {
		t.Fatalf("hypervisor time: %v", codes)
	}
}

func TestPreflightSQLServerFindings(t *testing.T) {
	check := preflightSample(t, `{"ok":true,"facts":{"computer":"SQL01","os":"Windows Server 2022","osVersion":"10.0.20348","sql":{"checked":true,"writerState":"Stopped","writerStart":"Manual","instances":[
{"name":"MSSQLSERVER","serviceState":"Running","serviceAccount":"EXAMPLE\\svc-sql","agentAccount":"EXAMPLE\\svc-agent","version":"16.0.4135.4","edition":"Standard Edition (64-bit)","level":"RTM","hadr":true,"availabilityGroups":"AG1 (SECONDARY)",
"databases":[{"name":"master","recovery":"SIMPLE","state":"ONLINE","encrypted":false,"fullAgeHours":20,"logAgeHours":-1},
{"name":"Sales","recovery":"FULL","state":"ONLINE","encrypted":true,"fullAgeHours":500,"logAgeHours":-1},
{"name":"Old","recovery":"SIMPLE","state":"OFFLINE","encrypted":false,"fullAgeHours":-1,"logAgeHours":-1}]},
{"name":"CLUSTERED","failoverCluster":true,"errors":"The instance could not be queried with this account: Login failed."}]}}}`)
	codes := preflightCodesOf(preflightFindings(check))
	for code, severity := range map[string]string{"sql-vss-writer": vmSeverityWarning, "sql-ag": vmSeverityWarning, "sql-tde": vmSeverityWarning, "sql-backup": vmSeverityWarning, "sql-log-backup": vmSeverityWarning, "sql-offline": vmSeverityInfo, "sql-fci": vmSeverityBlocker, "sql-query": vmSeverityWarning} {
		if codes[code] != severity {
			t.Fatalf("%s = %q, want %s (all: %v)", code, codes[code], severity, codes)
		}
	}
	if codes["sql-ok"] != "" {
		t.Fatal("problems were called a pass")
	}
	for _, finding := range preflightFindings(check) {
		if finding.Code == "sql-backup" && (strings.Contains(finding.Message, "Old") || !strings.Contains(finding.Message, "Sales")) {
			t.Fatalf("backup finding names the wrong databases: %s", finding.Message)
		}
	}
	if !check.sqlServer() {
		t.Fatal("not seen as a SQL Server")
	}
	if role, _ := vmWaveRoleOf(vmMachine{Name: "APP02", PowerState: "poweredOn", GuestOS: "Windows", Preflight: check}); role != vmWaveRoleDatabase {
		t.Fatalf("wave role = %s", role)
	}

	clean := preflightSample(t, `{"ok":true,"facts":{"sql":{"checked":true,"writerState":"Running","instances":[{"name":"MSSQLSERVER","serviceState":"Running","version":"15.0","edition":"Enterprise","databases":[{"name":"App","recovery":"SIMPLE","state":"ONLINE","fullAgeHours":3,"logAgeHours":-1}]}]}}}`)
	if codes := preflightCodesOf(preflightFindings(clean)); codes["sql-ok"] != vmSeverityInfo || len(codes) != 1 {
		t.Fatalf("a healthy SQL Server: %v", codes)
	}
	none := preflightSample(t, `{"ok":true,"facts":{"sql":{"checked":true,"instances":[]},"dc":{"checked":true,"isDC":false}}}`)
	if codes := preflightCodesOf(preflightFindings(none)); codes["sql-none"] == "" || codes["dc-not-dc"] == "" || len(codes) != 2 {
		t.Fatalf("nothing found: %v", codes)
	}
}

func TestPreflightUnreachableServerIsAReviewItem(t *testing.T) {
	result, err := parsePreflightResult([]byte(`{"ok":false,"error":"WinRM cannot complete the operation."}`))
	if err != nil || result.OK {
		t.Fatalf("result = %+v, %v", result, err)
	}
	check := &vmPreflight{CheckedAt: vmTestNow, Computer: "app01", Via: "winrm", Error: result.Error}
	findings := preflightFindings(check)
	if len(findings) != 1 || findings[0].Code != "preflight-failed" || findings[0].Severity != vmSeverityWarning || !strings.Contains(findings[0].Message, "WinRM") {
		t.Fatalf("findings = %+v", findings)
	}
	if _, err := parsePreflightResult([]byte(`not json`)); err == nil {
		t.Fatal("unreadable output was accepted")
	}
	if _, err := parsePreflightResult([]byte(`{"ok":true}`)); err == nil {
		t.Fatal("a result without facts was accepted")
	}
}

func TestPreflightComputerNamesAndRoles(t *testing.T) {
	for _, good := range []string{"dc01", "dc01.example.com", "192.0.2.10", "fe80::1", "SQL-01"} {
		if !validPreflightComputer(good) {
			t.Fatalf("%q refused", good)
		}
	}
	for _, bad := range []string{"", "192.0.2.1-50", "192.0.2.0/24", "a b", "dc01;whoami", "*", "dc01,dc02", "-dc01", "dc01..example", `\\dc01`} {
		if validPreflightComputer(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
	if got := normalizePreflightChecks([]string{"SQL", "dc", "nonsense", "dc"}); strings.Join(got, ",") != "dc,sql" {
		t.Fatalf("checks = %v", got)
	}
	windows := vmMachine{Name: "EXAMPLE-DC01", GuestOS: "Microsoft Windows Server 2022 (64-bit)", GuestHostName: "dc01.example.com", IPAddress: "192.0.2.5"}
	if roles := preflightLikelyRole(windows); strings.Join(roles, ",") != "dc" {
		t.Fatalf("roles = %v", roles)
	}
	if preflightComputerFor(windows) != "dc01.example.com" {
		t.Fatal("the guest DNS name was not used")
	}
	windows.GuestHostName = ""
	if preflightComputerFor(windows) != "192.0.2.5" {
		t.Fatal("the IP address was not used")
	}
	if roles := preflightLikelyRole(vmMachine{Name: "PRD-SQL02", GuestOS: "Microsoft Windows Server 2019 (64-bit)"}); strings.Join(roles, ",") != "sql" {
		t.Fatalf("roles = %v", roles)
	}
	if roles := preflightLikelyRole(vmMachine{Name: "sql-lnx", GuestOS: "Ubuntu Linux (64-bit)"}); len(roles) != 0 {
		t.Fatalf("a Linux VM was offered Windows checks: %v", roles)
	}
}

func TestPreflightResultsSurviveANewReadAndReachTheReports(t *testing.T) {
	inventory := vmSampleInventory()
	check := preflightSample(t, preflightSampleDC)
	check.Facts.DC.ReplFailing = preflightStrings{"DC02 (DC=example,DC=com): 3 failure(s), last status 1722"}
	if applyPreflightResults(inventory, map[string]*vmPreflight{"vm-101": check}, vmTestNow) != 1 {
		t.Fatal("not applied")
	}
	if inventory.VMs[0].readinessFor(vmTargetHyperV).Status != vmStatusBlocked {
		t.Fatal("readiness was not checked again")
	}
	older := *check
	older.CheckedAt = vmTestNow.Add(-time.Hour)
	older.Error = "old"
	if applyPreflightResults(inventory, map[string]*vmPreflight{"vm-101": &older}, vmTestNow) != 0 || inventory.VMs[0].Preflight.Error != "" {
		t.Fatal("an older check replaced a newer one")
	}
	summary := summarizePreflight(inventory)
	if summary.Checked != 1 || summary.DCs != 1 || summary.Issues == 0 {
		t.Fatalf("summary = %+v", summary)
	}
	model := vmReportBuild(inventory, vmTestNow)
	found := false
	for _, vm := range model.VMs {
		if vm.VM.ID == "vm-101" && len(vm.Checks) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("the report model has no checks")
	}
	titled := false
	for _, group := range model.Issues {
		for _, issue := range group.Issues {
			if issue.Code == "dc-replication" {
				titled = true
			}
		}
	}
	if !titled {
		t.Fatal("the replication failure is not an issue in the report")
	}
	page, err := vmReportHTML(model, "overview")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Domain controller and SQL Server checks") || !strings.Contains(string(page), "PDC emulator") {
		t.Fatal("the HTML report lacks the checks")
	}
	for _, code := range preflightCodes {
		if code == "" {
			t.Fatal("empty code")
		}
	}
}

func TestPreflightErrorsAreOneLineWithTheDetailKept(t *testing.T) {
	winrm := "Connecting to remote server DESK19101038.example.org failed with the following error message : The client cannot connect to the destination specified in the request. Verify that the service on the destination is running and is accepting requests. Consult the logs and documentation for the WS-Management service running on the destination, most commonly IIS or WinRM. If the destination is the WinRM service, run the following command on the destination to analyze and configure the WinRM service: \"winrm quickconfig\". For more information, see the about_Remote_Troubleshooting Help topic."
	cases := []struct{ via, err, want string }{
		{"winrm", winrm, "couldn't reach DESK19101038.example.org over WinRM (TCP 5985)."},
		{"winrm", "Connecting to remote server app01 failed with the following error message : Access is denied.", "app01 refused the account (access denied)."},
		{"winrm", "The WinRM client cannot process the request. Default authentication may be used with an IP address under the following conditions: the transport is HTTPS or the destination is in the TrustedHosts list", "couldn't sign in to app01 over WinRM (TCP 5985): use its domain name (Kerberos) rather than an IP address."},
		{"direct", "The operation timed out.", "app01 did not answer in time through PowerShell Direct."},
		{"winrm", "Something odd. With more text.\nSecond line", "Something odd."},
	}
	for _, c := range cases {
		computer := "app01"
		if strings.Contains(c.err, "DESK19101038") {
			computer = "DESK19101038.example.org"
		}
		if got := preflightErrorSummary(computer, c.via, c.err); got != c.want {
			t.Errorf("summary of %.40q = %q, want %q", c.err, got, c.want)
		}
	}
	check := &vmPreflight{CheckedAt: vmTestNow, Computer: "DESK19101038.example.org", Via: "winrm", Error: winrm}
	findings := preflightFindings(check)
	if len(findings) != 1 || strings.Contains(findings[0].Message, "quickconfig") || !strings.Contains(findings[0].Message, "over WinRM (TCP 5985)") || findings[0].Detail != winrm {
		t.Fatalf("findings = %+v", findings)
	}
	if rows := preflightFacts(check); strings.Contains(rows[len(rows)-1][1], "quickconfig") {
		t.Fatalf("report row keeps the raw error: %q", rows[len(rows)-1][1])
	}
}
