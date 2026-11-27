package main

// Optional domain controller and SQL Server checks for readiness
// (2026-11-26, owner: "SQL and DC preflight checks as part of the readiness
// checking, as an optional function"). Test-RolePreflight.ps1 reads the
// facts from inside the server over PowerShell remoting (or PowerShell
// Direct on this Hyper-V host) with nothing installed there; this file
// turns them into readiness findings, report facts and wave roles. It is
// untagged so go test covers it on any platform.

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	preflightCheckDC  = "dc"
	preflightCheckSQL = "sql"
	// preflightMaxVMs is the most VMs one run checks.
	preflightMaxVMs = 100
	// Backups older than these count as missing.
	preflightFullBackupHours = 7 * 24
	preflightLogBackupHours  = 24
	// Inbound replication older than this counts as stale.
	preflightReplStaleHours = 24
)

// preflightStrings reads a JSON string or array of strings: PowerShell
// writes a one-element array as a plain value in some versions.
type preflightStrings []string

func (s *preflightStrings) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*s = list
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		if one != "" {
			*s = []string{one}
		} else {
			*s = nil
		}
		return nil
	}
	if string(data) == "null" {
		*s = nil
		return nil
	}
	return fmt.Errorf("not a string list: %s", data)
}

// vmPreflight is one VM's last check, kept with the VM in the inventory so
// readiness and the reports use it.
type vmPreflight struct {
	CheckedAt time.Time `json:"checkedAt"`
	// Computer is the name or address the check reached; Via is winrm or
	// direct.
	Computer string   `json:"computer"`
	Via      string   `json:"via"`
	Checks   []string `json:"checks"`
	// Error is set when the server could not be reached or the checks did
	// not run; the facts are then empty.
	Error string `json:"error,omitempty"`
	// ErrorSummary is Error in one line (preflightErrorSummary).
	ErrorSummary string            `json:"errorSummary,omitempty"`
	Facts        *vmPreflightFacts `json:"facts,omitempty"`
	// Summary is preflightFacts at the time of the check, for the page.
	Summary [][2]string `json:"summary,omitempty"`
}

type vmPreflightFacts struct {
	Computer  string          `json:"computer"`
	OS        string          `json:"os"`
	OSVersion string          `json:"osVersion"`
	Domain    string          `json:"domain"`
	DC        *vmPreflightDC  `json:"dc,omitempty"`
	SQL       *vmPreflightSQL `json:"sql,omitempty"`
}

type vmPreflightDC struct {
	Checked          bool             `json:"checked"`
	IsDC             bool             `json:"isDC"`
	DomainName       string           `json:"domainName,omitempty"`
	Forest           string           `json:"forest,omitempty"`
	Site             string           `json:"site,omitempty"`
	GlobalCatalog    bool             `json:"globalCatalog,omitempty"`
	Roles            preflightStrings `json:"roles,omitempty"`
	SysvolReady      bool             `json:"sysvolReady,omitempty"`
	SysvolRepl       string           `json:"sysvolReplication,omitempty"`
	DFSRState        *int             `json:"dfsrState,omitempty"`
	DsaNotWritable   int              `json:"dsaNotWritable,omitempty"`
	USNRollbackEvent bool             `json:"usnRollbackEvent,omitempty"`
	ReplLinks        int              `json:"replLinks,omitempty"`
	ReplPartners     int              `json:"replPartners,omitempty"`
	ReplFailing      preflightStrings `json:"replFailing,omitempty"`
	ReplLargestHours int              `json:"replLargestHours,omitempty"`
	TimeSource       string           `json:"timeSource,omitempty"`
	DcdiagPassed     preflightStrings `json:"dcdiagPassed,omitempty"`
	DcdiagFailed     preflightStrings `json:"dcdiagFailed,omitempty"`
	DcdiagText       preflightStrings `json:"dcdiagText,omitempty"`
	Errors           preflightStrings `json:"errors,omitempty"`
}

type vmPreflightSQL struct {
	Checked     bool                  `json:"checked"`
	Instances   []vmPreflightInstance `json:"instances"`
	WriterState string                `json:"writerState,omitempty"`
	WriterStart string                `json:"writerStart,omitempty"`
	Errors      preflightStrings      `json:"errors,omitempty"`
}

type vmPreflightInstance struct {
	Name               string                `json:"name"`
	ServiceState       string                `json:"serviceState,omitempty"`
	ServiceAccount     string                `json:"serviceAccount,omitempty"`
	AgentState         string                `json:"agentState,omitempty"`
	AgentAccount       string                `json:"agentAccount,omitempty"`
	FailoverCluster    bool                  `json:"failoverCluster,omitempty"`
	Version            string                `json:"version,omitempty"`
	Edition            string                `json:"edition,omitempty"`
	Level              string                `json:"level,omitempty"`
	HADR               bool                  `json:"hadr,omitempty"`
	AvailabilityGroups preflightStrings      `json:"availabilityGroups,omitempty"`
	Databases          []vmPreflightDatabase `json:"databases,omitempty"`
	MoreDatabases      bool                  `json:"moreDatabases,omitempty"`
	Errors             preflightStrings      `json:"errors,omitempty"`
}

type vmPreflightDatabase struct {
	Name         string `json:"name"`
	Recovery     string `json:"recovery"`
	State        string `json:"state"`
	Encrypted    bool   `json:"encrypted"`
	FullAgeHours int    `json:"fullAgeHours"`
	LogAgeHours  int    `json:"logAgeHours"`
}

// preflightResult is what Test-RolePreflight.ps1 writes.
type preflightResult struct {
	OK    bool              `json:"ok"`
	Error string            `json:"error"`
	Facts *vmPreflightFacts `json:"facts"`
}

func parsePreflightResult(data []byte) (preflightResult, error) {
	var result preflightResult
	if err := json.Unmarshal(stripUTF8BOMBytes(data), &result); err != nil {
		return result, fmt.Errorf("the checks returned something that could not be read: %v", err)
	}
	if !result.OK && strings.TrimSpace(result.Error) == "" {
		result.Error = "The checks did not run."
	}
	if result.OK && result.Facts == nil {
		return result, fmt.Errorf("the checks returned no facts")
	}
	return result, nil
}

func stripUTF8BOMBytes(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// preflightComputerPattern accepts a host name, FQDN, IPv4 or IPv6 address:
// one computer, never a range or a list.
var preflightComputerPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]{0,252}[A-Za-z0-9])?$|^[0-9A-Fa-f:]{2,45}$`)

func validPreflightComputer(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || !preflightComputerPattern.MatchString(name) || strings.Contains(name, "..") {
		return false
	}
	// Something made of digits, dots and dashes only must be one address,
	// so a range such as 192.0.2.1-50 is refused.
	if strings.Trim(name, "0123456789.-") == "" || strings.Contains(name, ":") {
		return net.ParseIP(name) != nil
	}
	return true
}

// normalizePreflightChecks keeps the known checks, in a fixed order.
func normalizePreflightChecks(checks []string) []string {
	out := []string{}
	for _, want := range []string{preflightCheckDC, preflightCheckSQL} {
		for _, check := range checks {
			if strings.EqualFold(strings.TrimSpace(check), want) {
				out = append(out, want)
				break
			}
		}
	}
	return out
}

// preflightComputerFor is the name the check reaches by default: the guest's
// DNS name, else its IP address.
func preflightComputerFor(vm vmMachine) string {
	if name := strings.TrimSpace(vm.GuestHostName); validPreflightComputer(name) {
		return name
	}
	if address := strings.TrimSpace(vm.IPAddress); validPreflightComputer(address) {
		return address
	}
	return ""
}

// preflightLikelyRole says whether a VM looks like a domain controller or a
// SQL Server from its name, the way the wave suggestion guesses, so the page
// can tick it by default. The check itself finds the real roles.
func preflightLikelyRole(vm vmMachine) []string {
	if classifyGuestOS(vm).Family != "windows" || vm.Template {
		return nil
	}
	name := strings.ToLower(vm.Name)
	out := []string{}
	if vmWaveDCPattern.MatchString(name) {
		out = append(out, preflightCheckDC)
	}
	if strings.Contains(name, "sql") || vmWaveDatabasePattern.MatchString(name) {
		out = append(out, preflightCheckSQL)
	}
	return out
}

// preflightDomainController and preflightSQLServer say what the last check
// found (false when it found nothing or did not run).
func (p *vmPreflight) domainController() bool {
	return p != nil && p.Facts != nil && p.Facts.DC != nil && p.Facts.DC.IsDC
}

func (p *vmPreflight) sqlServer() bool {
	return p != nil && p.Facts != nil && p.Facts.SQL != nil && len(p.Facts.SQL.Instances) > 0
}

var preflightDFSRStates = map[int]string{0: "uninitialized", 1: "initialized", 2: "initial sync", 3: "auto recovery", 4: "normal", 5: "in error"}

// preflightFindings turns a check into readiness findings. They are the same
// for every target.
func preflightFindings(p *vmPreflight) []vmFinding {
	if p == nil {
		return nil
	}
	findings := []vmFinding{}
	when := p.CheckedAt.Local().Format("2006-01-02 15:04")
	if p.Error != "" || p.Facts == nil {
		findings = append(findings, vmFinding{Severity: vmSeverityWarning, Code: "preflight-failed", Message: "SQL and DC checks: " + preflightErrorSummary(p.Computer, p.Via, p.Error) + " (" + when + ")", Action: "Check the computer name, that PowerShell remoting answers on it (Enable-PSRemoting, TCP 5985) and that the account is an administrator there, then run the checks again. Nothing is installed on the server.", Detail: strings.TrimSpace(p.Error)})
		return findings
	}
	facts := p.Facts
	if dc := facts.DC; dc != nil && dc.Checked {
		findings = append(findings, preflightDCFindings(dc, facts, when)...)
	}
	if sql := facts.SQL; sql != nil && sql.Checked {
		findings = append(findings, preflightSQLFindings(sql, when)...)
	}
	return findings
}

func preflightDCFindings(dc *vmPreflightDC, facts *vmPreflightFacts, when string) []vmFinding {
	findings := []vmFinding{}
	add := func(severity, code, message, action string) {
		findings = append(findings, vmFinding{Severity: severity, Code: code, Message: message, Action: action})
	}
	if !dc.IsDC {
		add(vmSeverityInfo, "dc-not-dc", "Checked "+when+": this server is not a domain controller.", "")
		return findings
	}
	problems := 0
	if dc.DsaNotWritable != 0 || dc.USNRollbackEvent {
		problems++
		add(vmSeverityBlocker, "dc-usn-rollback", "Active Directory on this domain controller is quarantined after a USN rollback (Dsa Not Writable "+strconv.Itoa(dc.DsaNotWritable)+", event 2095 in the last 30 days: "+vmYesNo(dc.USNRollbackEvent)+").", "Do not migrate it. Demote it (forcibly if needed), clean up its metadata and promote a new domain controller instead.")
	}
	if len(dc.ReplFailing) > 0 {
		problems++
		add(vmSeverityBlocker, "dc-replication", fmt.Sprintf("%d of its %d inbound replication links are failing: %s.", len(dc.ReplFailing), dc.ReplLinks, strings.Join(dc.ReplFailing, "; ")), "Fix replication first (repadmin /showrepl and /replsummary on a domain controller, then repadmin /syncall /AdeP once the cause is fixed). A domain controller that does not replicate must not be moved.")
	} else if dc.ReplLargestHours > preflightReplStaleHours {
		problems++
		add(vmSeverityWarning, "dc-replication-stale", fmt.Sprintf("Its oldest successful inbound replication was %d hours ago.", dc.ReplLargestHours), "Check that every replication partner is online and replicating (repadmin /replsummary) before the cutover.")
	}
	sysvolOK := dc.SysvolReady
	if dc.DFSRState != nil && *dc.DFSRState != 4 {
		sysvolOK = false
	}
	if !sysvolOK {
		problems++
		state := ""
		if dc.DFSRState != nil {
			state = " DFSR state: " + vmNonEmpty(preflightDFSRStates[*dc.DFSRState], strconv.Itoa(*dc.DFSRState)) + "."
		}
		add(vmSeverityBlocker, "dc-sysvol", "SYSVOL is not healthy (SysvolReady "+vmYesNo(dc.SysvolReady)+")."+state, "Fix SYSVOL replication before moving it (DFS Replication event log, dcdiag /test:SysVolCheck). Moving a domain controller with SYSVOL out of sync can roll back Group Policy.")
	}
	if dc.SysvolRepl == "FRS" {
		problems++
		add(vmSeverityWarning, "dc-frs", "SYSVOL still replicates with FRS.", "Migrate SYSVOL to DFS Replication (dfsrmig) before the move: FRS is removed from Windows Server 2019 and later, and new domain controllers cannot join a domain that still uses it.")
	}
	if len(dc.DcdiagFailed) > 0 {
		problems++
		add(vmSeverityWarning, "dc-dcdiag", "dcdiag failed: "+strings.Join(dc.DcdiagFailed, ", ")+".", "Run dcdiag on it to read the details. Event tests (KccEvent, DFSREvent) also fail on old errors that are already fixed; check the dates.")
	} else if len(dc.DcdiagPassed) == 0 && len(dc.DcdiagText) > 0 {
		add(vmSeverityInfo, "dc-dcdiag-unread", "dcdiag ran but its output could not be read (it is not in English). Its last lines are kept with the check.", "Read the output in the readiness details or run dcdiag on it.")
	}
	source := strings.ToLower(dc.TimeSource)
	pdc := false
	for _, role := range dc.Roles {
		if role == "PDC emulator" {
			pdc = true
		}
	}
	switch {
	case strings.Contains(source, "local cmos clock") || strings.Contains(source, "free-running"):
		problems++
		add(vmSeverityWarning, "dc-time", "Its time source is "+dc.TimeSource+".", "Point the PDC emulator at a reliable NTP source (w32tm /config /manualpeerlist:... /syncfromflags:manual /reliable:yes /update) and the other domain controllers at the domain hierarchy.")
	case strings.Contains(source, "vm ic time") || strings.Contains(source, "vmware") || strings.Contains(source, "vmictimeprovider"):
		problems++
		add(vmSeverityWarning, "dc-time", "It takes its time from the hypervisor ("+dc.TimeSource+").", "On Hyper-V, turn off the Time synchronization integration service for the new VM after the cutover (or keep the host's time provider only for start-up) so the domain hierarchy keeps the time.")
	}
	if len(dc.Roles) > 0 {
		action := "Keep it in its own wave and check the roles answer from it after the cutover (netdom query fsmo)."
		if pdc {
			action += " As the PDC emulator it is the domain's time source: after the move, check w32tm /query /source and that the Hyper-V Time synchronization integration service does not set its clock."
		}
		add(vmSeverityInfo, "dc-fsmo", "It holds the "+preflightList(dc.Roles)+" role"+map[bool]string{true: "s", false: ""}[len(dc.Roles) > 1]+".", action)
	}
	if version := preflightOSVersion(facts.OSVersion); version > 0 && version < 6.2 {
		add(vmSeverityWarning, "dc-vmgenid", "Windows older than Server 2012 has no VM-Generation ID protection.", "Never start an old copy or a checkpoint of it after the cutover: that causes a USN rollback. Consider promoting a new Windows Server domain controller instead of moving this one.")
	}
	for _, text := range dc.Errors {
		add(vmSeverityInfo, "dc-check-partial", "Part of the check did not run: "+text, "")
	}
	if problems == 0 {
		detail := []string{}
		if dc.ReplPartners > 0 {
			detail = append(detail, fmt.Sprintf("replication with %d partner(s) succeeding", dc.ReplPartners))
		}
		detail = append(detail, strings.Join(strings.Fields("SYSVOL "+dc.SysvolRepl+" healthy"), " "), "no USN rollback")
		if len(dc.DcdiagPassed) > 0 {
			detail = append(detail, fmt.Sprintf("%d dcdiag tests passed", len(dc.DcdiagPassed)))
		}
		add(vmSeverityInfo, "dc-ok", "Domain controller checks passed "+when+": "+strings.Join(detail, ", ")+".", "Move it with the source shut down (a cold cutover) and never start the old VM again once the new one runs.")
	}
	return findings
}

func preflightSQLFindings(sql *vmPreflightSQL, when string) []vmFinding {
	findings := []vmFinding{}
	add := func(severity, code, message, action string) {
		findings = append(findings, vmFinding{Severity: severity, Code: code, Message: message, Action: action})
	}
	if len(sql.Instances) == 0 {
		add(vmSeverityInfo, "sql-none", "Checked "+when+": no SQL Server instance is installed.", "")
		return findings
	}
	problems := 0
	if sql.WriterState != "" && !strings.EqualFold(sql.WriterState, "Running") {
		problems++
		add(vmSeverityWarning, "sql-vss-writer", "The SQL Server VSS Writer service is "+strings.ToLower(sql.WriterState)+".", "Start it and set it to Automatic before copying: quiesced snapshots and the shadow copies of Replicate servers need it for application-consistent databases.")
	}
	for _, instance := range sql.Instances {
		label := "SQL Server instance " + instance.Name
		if instance.FailoverCluster {
			problems++
			add(vmSeverityBlocker, "sql-fci", label+" is a failover cluster instance on shared storage.", "A failover cluster instance cannot move by copying one node's disks. Build the instance on the new platform (a new cluster or a standalone instance) and move the databases with backup and restore, log shipping or an availability group.")
		}
		if len(instance.AvailabilityGroups) > 0 {
			problems++
			add(vmSeverityWarning, "sql-ag", label+" is in availability group "+strings.Join(instance.AvailabilityGroups, ", ")+".", "Move the secondary replicas first, one at a time, and fail over before moving the primary; keep each replica in its own wave and check the cluster and listener afterwards. Windows clusters do not span VMware and Hyper-V hosts without network checks.")
		}
		if instance.ServiceState != "" && !strings.EqualFold(instance.ServiceState, "Running") && !instance.FailoverCluster {
			add(vmSeverityInfo, "sql-stopped", label+" is "+strings.ToLower(instance.ServiceState)+", so its databases were not read.", "Start it and check again if it is in use.")
		}
		for _, text := range instance.Errors {
			problems++
			add(vmSeverityWarning, "sql-query", label+": "+text, "Use an account that is a SQL Server login with the sysadmin role (or VIEW SERVER STATE and read access to msdb) and check again.")
		}
		encrypted, noFull, noLog, offline := []string{}, []string{}, []string{}, []string{}
		for _, database := range instance.Databases {
			system := database.Name == "master" || database.Name == "model" || database.Name == "msdb"
			if database.Encrypted {
				encrypted = append(encrypted, database.Name)
			}
			if !strings.EqualFold(database.State, "ONLINE") {
				offline = append(offline, database.Name+" ("+strings.ToLower(database.State)+")")
				continue
			}
			if database.FullAgeHours < 0 || database.FullAgeHours > preflightFullBackupHours {
				if !system || database.Name == "master" {
					noFull = append(noFull, database.Name)
				}
			}
			if strings.EqualFold(database.Recovery, "FULL") && !system && (database.LogAgeHours < 0 || database.LogAgeHours > preflightLogBackupHours) {
				noLog = append(noLog, database.Name)
			}
		}
		if len(encrypted) > 0 {
			add(vmSeverityWarning, "sql-tde", label+" has Transparent Data Encryption on "+preflightList(encrypted)+".", "Back up the TDE certificate and its private key (and the database master key) before the cutover. The copied disks keep the databases encrypted as they are, but without the certificate backup they cannot be restored anywhere else.")
		}
		if len(noFull) > 0 {
			problems++
			add(vmSeverityWarning, "sql-backup", label+": no full backup in the last 7 days for "+preflightList(noFull)+".", "Take a full backup of each database before the cutover so there is a way back that does not depend on the old VM.")
		}
		if len(noLog) > 0 {
			problems++
			add(vmSeverityWarning, "sql-log-backup", label+": full recovery model with no log backup in the last 24 hours for "+preflightList(noLog)+".", "Back up the transaction logs (or switch to simple recovery if point-in-time restore is not needed); otherwise the logs keep growing and the copy is larger.")
		}
		if len(offline) > 0 {
			add(vmSeverityInfo, "sql-offline", label+" has databases that are not online: "+preflightList(offline)+".", "Check whether they are still needed before the move.")
		}
	}
	if problems == 0 {
		names := []string{}
		databases := 0
		for _, instance := range sql.Instances {
			name := instance.Name
			if instance.Version != "" {
				name += " (" + strings.TrimSpace(instance.Edition+" "+instance.Version) + ")"
			}
			names = append(names, name)
			databases += len(instance.Databases)
		}
		add(vmSeverityInfo, "sql-ok", "SQL Server checks passed "+when+": "+preflightList(names)+", "+vmPlural(databases, "database", "databases")+" with recent backups.", "Copy it with a quiesced snapshot or shadow copy (the SQL Server VSS Writer makes the databases consistent) and check the services start under their accounts after the cutover.")
	}
	return findings
}

func preflightList(items []string) string {
	if len(items) > 8 {
		return strings.Join(items[:8], ", ") + fmt.Sprintf(" and %d more", len(items)-8)
	}
	return strings.Join(items, ", ")
}

func preflightOSVersion(version string) float64 {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0
	}
	value, err := strconv.ParseFloat(parts[0]+"."+parts[1], 64)
	if err != nil {
		return 0
	}
	return value
}

// preflightFacts is the check as label and value rows for the reports.
func preflightFacts(p *vmPreflight) [][2]string {
	if p == nil {
		return nil
	}
	rows := [][2]string{{"Checked", p.CheckedAt.Local().Format("2006-01-02 15:04") + " through " + map[string]string{"direct": "PowerShell Direct", "winrm": "PowerShell remoting"}[p.Via] + " to " + p.Computer}}
	if p.Error != "" || p.Facts == nil {
		return append(rows, [2]string{"Result", "Not reached: " + preflightErrorSummary(p.Computer, p.Via, p.Error)})
	}
	facts := p.Facts
	rows = append(rows, [2]string{"Windows", strings.TrimSpace(facts.OS + " " + facts.OSVersion)})
	if dc := facts.DC; dc != nil && dc.Checked {
		if !dc.IsDC {
			rows = append(rows, [2]string{"Domain controller", "No"})
		} else {
			rows = append(rows, [2]string{"Domain controller", strings.TrimSpace(vmNonEmpty(dc.DomainName, facts.Domain) + map[bool]string{true: ", global catalog", false: ""}[dc.GlobalCatalog] + map[bool]string{true: ", site " + dc.Site, false: ""}[dc.Site != ""])})
			if len(dc.Roles) > 0 {
				rows = append(rows, [2]string{"FSMO roles", strings.Join(dc.Roles, ", ")})
			}
			repl := fmt.Sprintf("%d link(s) from %d partner(s), %d failing", dc.ReplLinks, dc.ReplPartners, len(dc.ReplFailing))
			if dc.ReplLargestHours > 0 {
				repl += fmt.Sprintf(", oldest success %d h ago", dc.ReplLargestHours)
			}
			rows = append(rows, [2]string{"Replication", repl})
			sysvol := vmNonEmpty(dc.SysvolRepl, "unknown") + ", ready " + vmYesNo(dc.SysvolReady)
			if dc.DFSRState != nil {
				sysvol += ", DFSR " + vmNonEmpty(preflightDFSRStates[*dc.DFSRState], strconv.Itoa(*dc.DFSRState))
			}
			rows = append(rows, [2]string{"SYSVOL", sysvol})
			rows = append(rows, [2]string{"USN rollback", map[bool]string{true: "Detected", false: "None"}[dc.DsaNotWritable != 0 || dc.USNRollbackEvent]})
			if dc.TimeSource != "" {
				rows = append(rows, [2]string{"Time source", dc.TimeSource})
			}
			if len(dc.DcdiagPassed)+len(dc.DcdiagFailed) > 0 {
				text := fmt.Sprintf("%d passed", len(dc.DcdiagPassed))
				if len(dc.DcdiagFailed) > 0 {
					text += ", failed: " + strings.Join(dc.DcdiagFailed, ", ")
				}
				rows = append(rows, [2]string{"dcdiag", text})
			}
		}
	}
	if sql := facts.SQL; sql != nil && sql.Checked {
		if len(sql.Instances) == 0 {
			rows = append(rows, [2]string{"SQL Server", "None installed"})
		}
		for _, instance := range sql.Instances {
			parts := []string{}
			if instance.Edition != "" || instance.Version != "" {
				parts = append(parts, strings.TrimSpace(instance.Edition+" "+instance.Version+" "+instance.Level))
			}
			if instance.ServiceState != "" {
				parts = append(parts, strings.ToLower(instance.ServiceState))
			}
			if instance.FailoverCluster {
				parts = append(parts, "failover cluster instance")
			}
			if len(instance.AvailabilityGroups) > 0 {
				parts = append(parts, "availability groups "+strings.Join(instance.AvailabilityGroups, ", "))
			}
			full, simple, encrypted := 0, 0, 0
			for _, database := range instance.Databases {
				if strings.EqualFold(database.Recovery, "FULL") {
					full++
				} else {
					simple++
				}
				if database.Encrypted {
					encrypted++
				}
			}
			if len(instance.Databases) > 0 {
				text := fmt.Sprintf("%d databases (%d full recovery, %d other)", len(instance.Databases), full, simple)
				if encrypted > 0 {
					text += fmt.Sprintf(", %d with TDE", encrypted)
				}
				parts = append(parts, text)
			}
			rows = append(rows, [2]string{"SQL Server " + instance.Name, strings.Join(parts, "; ")})
			accounts := []string{}
			if instance.ServiceAccount != "" {
				accounts = append(accounts, "engine "+instance.ServiceAccount)
			}
			if instance.AgentAccount != "" {
				accounts = append(accounts, "agent "+instance.AgentAccount)
			}
			if len(accounts) > 0 {
				rows = append(rows, [2]string{"Service accounts", strings.Join(accounts, ", ")})
			}
		}
		if sql.WriterState != "" {
			rows = append(rows, [2]string{"SQL VSS Writer", sql.WriterState + ", " + strings.ToLower(sql.WriterStart)})
		}
	}
	return rows
}

// applyPreflightResults puts saved checks on the inventory's VMs (by VM ID)
// and checks their readiness again. A check already on a VM is kept unless
// the saved one is newer.
func applyPreflightResults(inventory *vmInventory, saved map[string]*vmPreflight, now time.Time) int {
	if inventory == nil || len(saved) == 0 {
		return 0
	}
	applied := 0
	for index := range inventory.VMs {
		vm := &inventory.VMs[index]
		check := saved[vm.ID]
		if check == nil || (vm.Preflight != nil && !check.CheckedAt.After(vm.Preflight.CheckedAt)) {
			continue
		}
		if check.Error != "" && check.ErrorSummary == "" {
			check.ErrorSummary = preflightErrorSummary(check.Computer, check.Via, check.Error)
		}
		vm.Preflight = check
		vm.Readiness = nil
		for _, target := range vmTargets {
			vm.Readiness = append(vm.Readiness, assessVM(*vm, target, now))
		}
		applied++
	}
	return applied
}

// preflightSummary counts what the checks found over an inventory.
type preflightSummary struct {
	Checked int `json:"checked"`
	Failed  int `json:"failed"`
	DCs     int `json:"dcs"`
	SQL     int `json:"sql"`
	Issues  int `json:"issues"`
}

func summarizePreflight(inventory *vmInventory) preflightSummary {
	summary := preflightSummary{}
	if inventory == nil {
		return summary
	}
	for _, vm := range inventory.VMs {
		if vm.Preflight == nil {
			continue
		}
		summary.Checked++
		if vm.Preflight.Error != "" {
			summary.Failed++
		}
		if vm.Preflight.domainController() {
			summary.DCs++
		}
		if vm.Preflight.sqlServer() {
			summary.SQL++
		}
		for _, finding := range preflightFindings(vm.Preflight) {
			if finding.Severity != vmSeverityInfo {
				summary.Issues++
			}
		}
	}
	return summary
}

// preflightCodes lists the finding codes the checks can produce, for the
// report's issue titles and the page.
var preflightCodes = func() []string {
	codes := []string{"preflight-failed", "dc-not-dc", "dc-usn-rollback", "dc-replication", "dc-replication-stale", "dc-sysvol", "dc-frs", "dc-dcdiag", "dc-dcdiag-unread", "dc-time", "dc-fsmo", "dc-vmgenid", "dc-check-partial", "dc-ok", "sql-none", "sql-vss-writer", "sql-fci", "sql-ag", "sql-stopped", "sql-query", "sql-tde", "sql-backup", "sql-log-backup", "sql-offline", "sql-ok"}
	sort.Strings(codes)
	return codes
}()

// preflightErrorSummary says in one line why a check could not reach its
// server. Remote errors (WinRM above all) run to many lines; the full text
// stays in the finding's Detail.
func preflightErrorSummary(computer, via, message string) string {
	name := vmNonEmpty(strings.TrimSpace(computer), "the server")
	text := strings.ToLower(message)
	route := "over WinRM (TCP 5985)"
	if via == "direct" {
		route = "through PowerShell Direct"
	}
	has := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(text, part) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.TrimSpace(message) == "":
		return "no facts came back from " + name + "."
	case has("access is denied", "access denied", "logon failure", "user name or password is incorrect", "the user name or password"):
		return name + " refused the account (access denied)."
	case has("kerberos", "trustedhosts", "negotiate authentication"):
		return "couldn't sign in to " + name + " " + route + ": use its domain name (Kerberos) rather than an IP address."
	case has("could not be resolved", "cannot find the computer", "no such host", "name or service not known", "network path was not found", "the computer name is not valid"):
		return name + " was not found: check the name and DNS."
	case has("timed out", "timeout", "did not respond"):
		return name + " did not answer in time " + route + "."
	case has("cannot connect to the destination", "winrm cannot complete", "winrm client cannot", "connecting to remote server", "cannot connect", "refused", "unreachable"):
		return "couldn't reach " + name + " " + route + "."
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(message), "\n", 2)[0])
	if cut := strings.Index(line, ". "); cut > 0 {
		line = line[:cut+1]
	}
	if runes := []rune(line); len(runes) > 140 {
		line = string(runes[:137]) + "..."
	}
	return line
}
