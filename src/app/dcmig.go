package main

// Move a domain controller (2026-11-28, owner: "build a new DC based on an
// existing one, and also handle promotion, fsmo transfer, and demotion of the
// old one"; "can you offer both options?" for taking over the old DC's IP
// address or keeping the new one and repointing). A domain controller is
// never copied or cloned while it runs: the tool builds a new one beside it,
// moves the roles and retires the old one.
//
// Nothing is installed on any server. Invoke-DcMigration.ps1 (signed, run
// with -ExecutionPolicy RemoteSigned) reaches each server with
// Invoke-Command over WinRM and runs the in-box AD DS, DNS, DHCP and
// networking cmdlets there. Everything that decides what runs is here,
// untagged, so go test covers it on any platform:
//   dcmig.go         model, suggested settings from the old DC, checks
//   dcmig_runner.go  plans and their steps, scheduling and rollback

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FSMO roles, as Move-ADDirectoryServerOperationMasterRole names them.
const (
	dcRoleSchema         = "SchemaMaster"
	dcRoleNaming         = "DomainNamingMaster"
	dcRolePDC            = "PDCEmulator"
	dcRoleRID            = "RIDMaster"
	dcRoleInfrastructure = "InfrastructureMaster"
)

var dcRoleOrder = []string{dcRoleSchema, dcRoleNaming, dcRolePDC, dcRoleRID, dcRoleInfrastructure}

var dcRoleNames = map[string]string{
	dcRoleSchema:         "Schema master",
	dcRoleNaming:         "Domain naming master",
	dcRolePDC:            "PDC emulator",
	dcRoleRID:            "RID master",
	dcRoleInfrastructure: "Infrastructure master",
}

// How clients find the new DC after the old one retires.
const (
	dcIPTakeover = "takeover" // the new DC takes the old DC's address after demotion
	dcIPRepoint  = "repoint"  // the new DC keeps its own address; DHCP and servers are repointed
)

var dcIPModeText = map[string]string{
	dcIPTakeover: "Take over the old DC's IP address: pick this when printers, firewalls, VPNs or servers have the old DC's address typed in as their DNS server, so they keep working without being found and changed.",
	dcIPRepoint:  "Keep the new IP and repoint: pick this when clients get DNS from DHCP and few devices have the old address typed in, or the new DC is in another subnet or site.",
}

// Steps, in the order they run.
const (
	dcStepHealth  = "health"  // SQL and DC checks on every DC (read only)
	dcStepJoin    = "join"    // join the new server to the domain (only when it is not)
	dcStepInstall = "install" // add the AD DS (and DNS) roles
	dcStepPromote = "promote" // Install-ADDSDomainController, restart
	dcStepVerify  = "verify"  // advertising, SYSVOL ready, replication working
	dcStepRoles   = "roles"   // move (or seize) the FSMO roles, time source
	dcStepRepoint = "repoint" // DHCP DNS options, DNS client settings, forwarders
	dcStepDemote  = "demote"  // Uninstall-ADDSDomainController on the old DC, or metadata cleanup when it is dead
	dcStepIP      = "ipswap"  // the new DC takes the old DC's address
)

var dcStepOrder = []string{dcStepHealth, dcStepJoin, dcStepInstall, dcStepPromote, dcStepVerify, dcStepRoles, dcStepRepoint, dcStepDemote, dcStepIP}

var dcStepNames = map[string]string{
	dcStepHealth:  "Check the domain",
	dcStepJoin:    "Join the new server to the domain",
	dcStepInstall: "Add the domain controller roles",
	dcStepPromote: "Promote the new server",
	dcStepVerify:  "Wait until the new DC is working",
	dcStepRoles:   "Move the FSMO roles",
	dcStepRepoint: "Repoint DNS and DHCP",
	dcStepDemote:  "Demote the old DC",
	dcStepIP:      "Move the old DC's IP address",
}

// Phases group the steps that run together from one button or schedule.
const (
	dcPhaseBuild   = "build"   // health .. verify
	dcPhaseCutover = "cutover" // roles, repoint
	dcPhaseRetire  = "retire"  // demote, ipswap
	dcPhaseCheck   = "check"   // health only (read only), runnable at any time
)

var dcPhaseSteps = map[string][]string{
	dcPhaseBuild:   {dcStepHealth, dcStepJoin, dcStepInstall, dcStepPromote, dcStepVerify},
	dcPhaseCutover: {dcStepRoles, dcStepRepoint},
	dcPhaseRetire:  {dcStepDemote, dcStepIP},
	dcPhaseCheck:   {dcStepHealth},
}

var dcPhaseNames = map[string]string{
	dcPhaseBuild:   "Build the new DC",
	dcPhaseCutover: "Move the roles",
	dcPhaseRetire:  "Retire the old DC",
	dcPhaseCheck:   "Check the domain",
}

// Step states.
const (
	dcPending = "pending"
	dcRunning = "running"
	dcDone    = "done"
	dcFailed  = "failed"
	dcSkipped = "skipped" // not needed for this plan
)

const (
	dcMaxPlans    = 50
	dcMinFreeGB   = 10
	dcVerifyLimit = 90 * time.Minute
)

// dcAddress is one IPv4 address of an adapter.
type dcAddress struct {
	Interface string   `json:"interface"`
	Index     int      `json:"index"`
	Address   string   `json:"address"`
	Prefix    int      `json:"prefix"`
	Gateway   string   `json:"gateway,omitempty"`
	DNS       []string `json:"dns,omitempty"`
	DHCP      bool     `json:"dhcp,omitempty"`
}

// dcPeer is one domain controller of the domain.
type dcPeer struct {
	Name          string `json:"name"`
	HostName      string `json:"hostName"`
	Site          string `json:"site"`
	Address       string `json:"address"`
	GlobalCatalog bool   `json:"globalCatalog"`
	ReadOnly      bool   `json:"readOnly"`
	OS            string `json:"os"`
}

// dcSourceInfo is what Read found on the old DC.
type dcSourceInfo struct {
	Name          string            `json:"name"`
	HostName      string            `json:"hostName"`
	Domain        string            `json:"domain"`
	NetBIOS       string            `json:"netbios"`
	Forest        string            `json:"forest"`
	DomainMode    string            `json:"domainMode"`
	ForestMode    string            `json:"forestMode"`
	Site          string            `json:"site"`
	Sites         []string          `json:"sites"`
	OS            string            `json:"os"`
	OSVersion     string            `json:"osVersion"`
	GlobalCatalog bool              `json:"globalCatalog"`
	ReadOnly      bool              `json:"readOnly"`
	DNSServer     bool              `json:"dnsServer"`
	DHCPServer    bool              `json:"dhcpServer"`
	Roles         []string          `json:"roles"`
	RoleOwners    map[string]string `json:"roleOwners"`
	Forwarders    []string          `json:"forwarders"`
	Zones         []string          `json:"zones"`
	Addresses     []dcAddress       `json:"addresses"`
	NtpServer     string            `json:"ntpServer"`
	TimeType      string            `json:"timeType"`
	Sysvol        string            `json:"sysvol"` // DFSR or FRS
	DatabasePath  string            `json:"databasePath"`
	LogPath       string            `json:"logPath"`
	SysvolPath    string            `json:"sysvolPath"`
	DCs           []dcPeer          `json:"dcs"`
	DHCPServers   []string          `json:"dhcpServers"` // authorized in AD
	OtherRoles    []string          `json:"otherRoles"`  // Windows roles besides AD DS and DNS (DHCP, AD CS, file server ...)
}

// dcTargetInfo is what Read found on the new server.
type dcTargetInfo struct {
	Name          string      `json:"name"`
	HostName      string      `json:"hostName"`
	OS            string      `json:"os"`
	OSVersion     string      `json:"osVersion"`
	DomainJoined  bool        `json:"domainJoined"`
	Domain        string      `json:"domain"`
	IsDC          bool        `json:"isDC"`
	ADDSInstalled bool        `json:"addsInstalled"`
	DNSInstalled  bool        `json:"dnsInstalled"`
	PendingReboot bool        `json:"pendingReboot"`
	FreeGB        float64     `json:"freeGB"`
	Addresses     []dcAddress `json:"addresses"`
}

// dcSettings are what the build and the cutover use. Every field is filled
// from the old DC with a reason (Why) and can be changed; Suggested lists the
// fields still at the suggestion.
type dcSettings struct {
	Site          string   `json:"site"`
	InstallDNS    bool     `json:"installDns"`
	GlobalCatalog bool     `json:"globalCatalog"`
	ReplicateFrom string   `json:"replicateFrom"`
	DatabasePath  string   `json:"databasePath"`
	LogPath       string   `json:"logPath"`
	SysvolPath    string   `json:"sysvolPath"`
	MoveRoles     []string `json:"moveRoles"`
	NtpServer     string   `json:"ntpServer"` // the new PDC's external time source
	Forwarders    []string `json:"forwarders"`
	IPMode        string   `json:"ipMode"`
	// The old server's new address when the new DC takes over its address.
	OldNewAddress string `json:"oldNewAddress"`
	OldNewPrefix  int    `json:"oldNewPrefix"`
	OldNewGateway string `json:"oldNewGateway"`
	// Repoint DHCP options and the DNS client settings of other DCs.
	Repoint bool `json:"repoint"`
	// OldDead: the old DC no longer works; roles are seized and its
	// metadata cleaned up instead of a demotion.
	OldDead bool `json:"oldDead"`

	Suggested map[string]bool   `json:"suggested,omitempty"`
	Why       map[string]string `json:"why,omitempty"`
}

// dcStep is one step's state in a plan.
type dcStep struct {
	State   string    `json:"state"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`
	Message string    `json:"message,omitempty"`
	Notes   []string  `json:"notes,omitempty"`
}

// dcChange is one setting the plan changed outside the two DCs, with the
// value before, so Roll back can put it back.
type dcChange struct {
	Kind     string   `json:"kind"` // dhcp-server, dhcp-scope, dns-client, time
	Server   string   `json:"server"`
	Key      string   `json:"key"` // scope ID, interface index, or "pdc"/"old"
	Before   []string `json:"before"`
	After    []string `json:"after"`
	Restored bool     `json:"restored,omitempty"`
}

// dcHealth is one DC's check result.
type dcHealth struct {
	DC        string      `json:"dc"`
	CheckedAt time.Time   `json:"checkedAt"`
	Error     string      `json:"error,omitempty"`
	Findings  []vmFinding `json:"findings"`
}

type dcSchedule struct {
	Phase string    `json:"phase"`
	At    time.Time `json:"at"`
}

// dcPlan is one domain controller move.
type dcPlan struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Created  time.Time         `json:"created"`
	Source   string            `json:"source"` // the old DC as typed
	Target   string            `json:"target"` // the new server as typed
	User     string            `json:"user"`   // DOMAIN\user used for the changing steps
	Old      *dcSourceInfo     `json:"old"`
	New      *dcTargetInfo     `json:"new"`
	Settings dcSettings        `json:"settings"`
	Steps    map[string]dcStep `json:"steps"`
	Health   []dcHealth        `json:"health,omitempty"`
	Changes  []dcChange        `json:"changes,omitempty"`
	// RolesMoved lists the roles this plan moved, so Roll back moves exactly
	// those back.
	RolesMoved   []string    `json:"rolesMoved,omitempty"`
	Schedule     *dcSchedule `json:"schedule,omitempty"`
	Wave         int         `json:"wave"`
	RolledBackAt time.Time   `json:"rolledBackAt"`
	Note         string      `json:"note,omitempty"`
}

func (p *dcPlan) step(id string) dcStep {
	if s, ok := p.Steps[id]; ok {
		return s
	}
	return dcStep{State: dcPending}
}

func (p *dcPlan) setStep(id string, s dcStep) {
	if p.Steps == nil {
		p.Steps = map[string]dcStep{}
	}
	p.Steps[id] = s
}

func (p *dcPlan) done(id string) bool {
	st := p.step(id).State
	return st == dcDone || st == dcSkipped
}

// dcNeeded says whether a step applies to this plan at all.
func dcNeeded(p *dcPlan, step string) bool {
	switch step {
	case dcStepJoin:
		return p.New == nil || !p.New.DomainJoined
	case dcStepInstall:
		return p.New == nil || !p.New.ADDSInstalled || (p.Settings.InstallDNS && !p.New.DNSInstalled)
	case dcStepRoles:
		return len(p.Settings.MoveRoles) > 0
	case dcStepRepoint:
		return p.Settings.Repoint && p.Settings.IPMode == dcIPRepoint
	case dcStepIP:
		return p.Settings.IPMode == dcIPTakeover && !p.Settings.OldDead
	}
	return true
}

// dcOldAddress is the old DC's primary IPv4 address.
func (p *dcPlan) dcOldAddress() dcAddress {
	if p.Old != nil {
		for _, a := range p.Old.Addresses {
			if a.Address != "" {
				return a
			}
		}
	}
	return dcAddress{}
}

func (p *dcPlan) dcNewAddress() dcAddress {
	if p.New != nil {
		for _, a := range p.New.Addresses {
			if a.Address != "" {
				return a
			}
		}
	}
	return dcAddress{}
}

var dcNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$`)

// dcCheckComputer accepts a computer name or FQDN, never an address range,
// a path or anything PowerShell would read as more than a name.
func dcCheckComputer(name, what string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("type the %s", what)
	}
	if !dcNameRe.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("the %s must be a computer name such as DC01 or DC01.example.com", what)
	}
	return nil
}

func dcShort(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.Index(name, "."); i > 0 {
		name = name[:i]
	}
	return strings.ToUpper(name)
}

func dcSameComputer(a, b string) bool {
	return a != "" && b != "" && dcShort(a) == dcShort(b)
}

// dcOSVersion reads "10.0.17763" as 17763 (the build), or 6.3 style
// versions as 9600-like builds.
func dcOSBuild(version string) int {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) >= 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil {
			return n
		}
	}
	return 0
}

// dcOSLabel names a Windows Server build.
func dcOSLabel(build int) string {
	switch {
	case build >= 26100:
		return "Windows Server 2025"
	case build >= 20348:
		return "Windows Server 2022"
	case build >= 17763:
		return "Windows Server 2019"
	case build >= 14393:
		return "Windows Server 2016"
	case build >= 9600:
		return "Windows Server 2012 R2"
	case build >= 9200:
		return "Windows Server 2012"
	case build > 0:
		return "Windows Server 2008 R2 or older"
	}
	return "an unknown Windows version"
}

// dcModeLevel ranks a domain or forest functional level ("Windows2016Domain").
func dcModeLevel(mode string) int {
	m := strings.ToLower(mode)
	for _, x := range []struct {
		key   string
		level int
	}{{"2025", 10}, {"2016", 7}, {"2012r2", 6}, {"2012", 5}, {"2008r2", 4}, {"2008", 3}, {"2003", 2}, {"2000", 1}} {
		if strings.Contains(m, x.key) {
			return x.level
		}
	}
	return 0
}

// dcSuggest fills the settings from the old DC, each with its reason. A
// field the operator changed (Suggested false) is kept.
func dcSuggest(prev dcSettings, old *dcSourceInfo, tgt *dcTargetInfo) dcSettings {
	s := prev
	if s.Suggested == nil {
		s.Suggested = map[string]bool{}
		for _, k := range []string{"site", "installDns", "globalCatalog", "replicateFrom", "paths", "moveRoles", "ntpServer", "forwarders", "ipMode", "oldNewAddress", "repoint"} {
			s.Suggested[k] = true
		}
	}
	if s.Why == nil {
		s.Why = map[string]string{}
	}
	if old == nil {
		return s
	}
	fill := func(key string) bool { return s.Suggested[key] }
	if fill("site") {
		s.Site = old.Site
		s.Why["site"] = "The old DC is in site " + old.Site + ", so the new one serves the same clients."
	}
	if fill("installDns") {
		s.InstallDNS = old.DNSServer
		if old.DNSServer {
			s.Why["installDns"] = "The old DC is a DNS server."
		} else {
			s.Why["installDns"] = "The old DC is not a DNS server."
		}
	}
	if fill("globalCatalog") {
		s.GlobalCatalog = true
		if old.GlobalCatalog {
			s.Why["globalCatalog"] = "The old DC is a global catalog."
		} else {
			s.Why["globalCatalog"] = "Every DC should be a global catalog unless the infrastructure master needs otherwise; most domains make all DCs global catalogs."
		}
	}
	if fill("replicateFrom") {
		s.ReplicateFrom = old.HostName
		if s.ReplicateFrom == "" {
			s.ReplicateFrom = old.Name
		}
		s.Why["replicateFrom"] = "The first copy of the directory comes from the old DC, which has passed the checks."
	}
	if fill("paths") {
		s.DatabasePath, s.LogPath, s.SysvolPath = dcDefaultPath(old.DatabasePath, `C:\Windows\NTDS`), dcDefaultPath(old.LogPath, `C:\Windows\NTDS`), dcDefaultPath(old.SysvolPath, `C:\Windows\SYSVOL`)
		s.Why["paths"] = "The same folders as on the old DC."
	}
	if fill("moveRoles") {
		s.MoveRoles = dcSortRoles(old.Roles)
		if len(old.Roles) == 0 {
			s.Why["moveRoles"] = "The old DC holds no FSMO roles."
		} else {
			s.Why["moveRoles"] = "Every FSMO role the old DC holds moves to the new one before it is demoted."
		}
	}
	if fill("ntpServer") {
		s.NtpServer = ""
		if dcHasRole(s.MoveRoles, dcRolePDC) {
			s.NtpServer = strings.TrimSpace(strings.Replace(old.NtpServer, ",0x9", "", -1))
			if strings.EqualFold(old.TimeType, "NTP") && s.NtpServer != "" {
				s.Why["ntpServer"] = "The old PDC emulator takes its time from " + s.NtpServer + "; the new PDC uses the same source."
			} else {
				s.NtpServer = "time.windows.com"
				s.Why["ntpServer"] = "The old PDC emulator has no external time source (" + old.TimeType + "); the new PDC needs one. Change it to your own time server if you have one."
			}
		} else {
			s.Why["ntpServer"] = "Only the PDC emulator takes time from outside the domain; it is not moving."
		}
	}
	if fill("forwarders") {
		s.Forwarders = append([]string{}, old.Forwarders...)
		s.Why["forwarders"] = "The same DNS forwarders as the old DC."
	}
	if fill("ipMode") {
		s.IPMode = dcIPTakeover
		s.Why["ipMode"] = "Devices with the old DC's address typed in keep working."
		if tgt != nil {
			oa, na := (&dcPlan{Old: old}).dcOldAddress(), (&dcPlan{New: tgt}).dcNewAddress()
			if oa.Address != "" && na.Address != "" && !dcSameSubnet(oa.Address, na.Address, oa.Prefix) {
				s.IPMode = dcIPRepoint
				s.Why["ipMode"] = "The new server is in another subnet than the old DC, so it cannot take over the old address."
			}
		}
	}
	if fill("repoint") {
		s.Repoint = true
		s.Why["repoint"] = "DHCP scopes and the other DCs are pointed at the new DC's address."
	}
	if fill("oldNewAddress") && s.OldNewAddress == "" {
		oa := (&dcPlan{Old: old}).dcOldAddress()
		s.OldNewPrefix, s.OldNewGateway = oa.Prefix, oa.Gateway
		s.Why["oldNewAddress"] = "Type a free address in the old DC's subnet for the old server; the new DC takes " + oa.Address + "."
	}
	return s
}

func dcDefaultPath(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func dcHasRole(roles []string, role string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, role) {
			return true
		}
	}
	return false
}

func dcSortRoles(roles []string) []string {
	out := []string{}
	for _, r := range dcRoleOrder {
		if dcHasRole(roles, r) {
			out = append(out, r)
		}
	}
	return out
}

func dcSameSubnet(a, b string, prefix int) bool {
	ia, ib := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if ia == nil || ib == nil || prefix <= 0 || prefix > 32 {
		return false
	}
	mask := net.CIDRMask(prefix, 32)
	return ia.Mask(mask).Equal(ib.Mask(mask))
}

func dcValidIPv4(a string) bool {
	ip := net.ParseIP(strings.TrimSpace(a)).To4()
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast()
}

// dcCheckSettings refuses settings the steps cannot use.
func dcCheckSettings(p *dcPlan) error {
	s := &p.Settings
	if strings.TrimSpace(s.Site) == "" {
		return errors.New("choose the site for the new DC")
	}
	if p.Old != nil && len(p.Old.Sites) > 0 {
		found := false
		for _, site := range p.Old.Sites {
			if strings.EqualFold(site, s.Site) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("site %q does not exist in the forest", s.Site)
		}
	}
	for _, path := range []string{s.DatabasePath, s.LogPath, s.SysvolPath} {
		if !regexp.MustCompile(`^[A-Za-z]:\\[^"'*?<>|]*$`).MatchString(path) || strings.Contains(path, "..") {
			return fmt.Errorf("%q is not a full folder path on the new server", path)
		}
	}
	for _, r := range s.MoveRoles {
		if dcRoleNames[r] == "" {
			return fmt.Errorf("%q is not an FSMO role", r)
		}
	}
	if s.NtpServer != "" && !regexp.MustCompile(`^[A-Za-z0-9.\-, ]{1,250}$`).MatchString(s.NtpServer) {
		return errors.New("the time source must be names or addresses separated by spaces")
	}
	for _, f := range s.Forwarders {
		if !dcValidIPv4(f) && net.ParseIP(strings.TrimSpace(f)) == nil {
			return fmt.Errorf("forwarder %q is not an IP address", f)
		}
	}
	if s.ReplicateFrom != "" {
		if err := dcCheckComputer(s.ReplicateFrom, "DC to replicate from"); err != nil {
			return err
		}
	}
	switch s.IPMode {
	case dcIPTakeover:
		if s.OldDead {
			break
		}
		if !dcValidIPv4(s.OldNewAddress) {
			return errors.New("type a free IPv4 address for the old server, which gives its address to the new DC")
		}
		if s.OldNewPrefix < 8 || s.OldNewPrefix > 30 {
			return errors.New("the old server's new subnet prefix must be between 8 and 30")
		}
		if s.OldNewGateway != "" && !dcValidIPv4(s.OldNewGateway) {
			return errors.New("the old server's new gateway is not an IPv4 address")
		}
		oa, na := p.dcOldAddress(), p.dcNewAddress()
		if s.OldNewAddress == oa.Address || s.OldNewAddress == na.Address {
			return errors.New("the old server's new address must differ from both DCs' current addresses")
		}
		if oa.Address != "" && na.Address != "" && !dcSameSubnet(oa.Address, na.Address, oa.Prefix) {
			return errors.New("the new server is in another subnet than the old DC, so it cannot take over the old DC's address: choose Keep the new IP and repoint")
		}
	case dcIPRepoint:
	default:
		return errors.New("choose how clients find the new DC")
	}
	return nil
}

// dcCheckPlan lists what blocks or needs attention before the build, from
// the two reads and the health checks.
func dcCheckPlan(p *dcPlan) []vmFinding {
	out := []vmFinding{}
	add := func(sev, code, msg, action string) {
		out = append(out, vmFinding{Severity: sev, Code: code, Message: msg, Action: action})
	}
	old, tgt := p.Old, p.New
	if old == nil {
		add(vmSeverityBlocker, "dc-old-unread", "The old DC has not been read.", "Read both servers.")
		return out
	}
	if old.Domain == "" {
		add(vmSeverityBlocker, "dc-old-not-dc", p.Source+" is not a domain controller.", "Type the name of the domain controller you are replacing.")
		return out
	}
	if old.ReadOnly {
		add(vmSeverityBlocker, "dc-rodc", p.Source+" is a read-only domain controller.", "Read-only DCs are not moved by this tool yet: promote a new RODC in Server Manager, then demote the old one.")
	}
	if strings.EqualFold(old.Sysvol, "FRS") {
		add(vmSeverityBlocker, "dc-frs", "SYSVOL still replicates with FRS.", "Migrate SYSVOL to DFSR first (dfsrmig /setglobalstate 1, 2, 3); Windows Server 2019 and later cannot join a domain that uses FRS.")
	}
	if tgt == nil {
		add(vmSeverityBlocker, "dc-new-unread", "The new server has not been read.", "Read both servers.")
		return out
	}
	if dcSameComputer(p.Source, p.Target) || dcSameComputer(old.Name, tgt.Name) {
		add(vmSeverityBlocker, "dc-same", "The old DC and the new server are the same computer.", "Choose a different server for the new DC. A DC is never upgraded or copied in place by this tool.")
	}
	if tgt.IsDC {
		if tgt.Domain != "" && !strings.EqualFold(tgt.Domain, old.Domain) {
			add(vmSeverityBlocker, "dc-new-other-domain", tgt.Name+" is already a domain controller of "+tgt.Domain+".", "Choose a server that is not a domain controller.")
		} else if !p.done(dcStepPromote) {
			add(vmSeverityInfo, "dc-new-already", tgt.Name+" is already a domain controller of "+old.Domain+".", "The build steps are skipped; the tool goes on with the checks, the roles and retiring the old DC.")
		}
	} else if tgt.DomainJoined && !strings.EqualFold(tgt.Domain, old.Domain) {
		add(vmSeverityBlocker, "dc-new-other-domain", tgt.Name+" is joined to "+tgt.Domain+", not "+old.Domain+".", "Remove it from that domain or choose another server.")
	}
	ob, nb := dcOSBuild(old.OSVersion), dcOSBuild(tgt.OSVersion)
	if nb > 0 && nb < 14393 {
		add(vmSeverityBlocker, "dc-new-os", tgt.Name+" runs "+dcOSLabel(nb)+".", "Use Windows Server 2016 or later for a new domain controller.")
	}
	if nb >= 26100 && dcModeLevel(old.DomainMode) > 0 && dcModeLevel(old.DomainMode) < 7 {
		add(vmSeverityBlocker, "dc-level-2025", "Windows Server 2025 needs the domain and forest functional level Windows Server 2016; "+old.Domain+" is at "+old.DomainMode+".", "Raise the functional levels once every DC runs Windows Server 2016 or later, or use Windows Server 2022 for the new DC.")
	}
	if nb > 0 && ob > 0 && nb < ob {
		add(vmSeverityWarning, "dc-new-older", "The new server runs an older Windows ("+dcOSLabel(nb)+") than the old DC ("+dcOSLabel(ob)+").", "That works, but you would normally replace a DC with a newer version.")
	}
	if dcModeLevel(old.DomainMode) > 0 && dcModeLevel(old.DomainMode) < 3 {
		add(vmSeverityBlocker, "dc-level-old", old.Domain+" is at functional level "+old.DomainMode+".", "Windows Server 2016 and later need at least Windows Server 2008; raise the level first.")
	}
	if tgt.PendingReboot {
		add(vmSeverityBlocker, "dc-new-reboot", tgt.Name+" is waiting for a restart.", "Restart it, then read both servers again.")
	}
	if tgt.FreeGB > 0 && tgt.FreeGB < dcMinFreeGB {
		add(vmSeverityBlocker, "dc-new-space", fmt.Sprintf("%s has %.1f GB free on its system drive.", tgt.Name, tgt.FreeGB), fmt.Sprintf("Free at least %d GB before promoting it.", dcMinFreeGB))
	}
	na := p.dcNewAddress()
	if na.DHCP {
		add(vmSeverityBlocker, "dc-new-dhcp", tgt.Name+" gets its address from DHCP.", "Give it a static IPv4 address first; a domain controller needs one.")
	}
	if na.Address != "" && len(na.DNS) > 0 {
		inDomain := false
		for _, d := range na.DNS {
			for _, peer := range old.DCs {
				if peer.Address == d {
					inDomain = true
				}
			}
		}
		if !inDomain {
			add(vmSeverityWarning, "dc-new-dns", tgt.Name+" uses DNS servers ("+strings.Join(na.DNS, ", ")+") that are not domain controllers of "+old.Domain+".", "Point its DNS at an existing DC before promoting it, or the domain may not be found.")
		}
	}
	if old.DHCPServer {
		add(vmSeverityWarning, "dc-old-dhcp", p.Source+" is also a DHCP server.", "Move DHCP first with Restore > Another server (live), or it stops when the old DC is retired.")
	}
	if len(old.OtherRoles) > 0 {
		add(vmSeverityWarning, "dc-old-roles", p.Source+" also runs "+strings.Join(old.OtherRoles, ", ")+".", "Move those to another server before retiring the old DC; demotion leaves them installed, and they stop if the server is turned off.")
	}
	writable := 0
	for _, peer := range old.DCs {
		if dcSameComputer(peer.Name, old.Name) || peer.ReadOnly {
			continue
		}
		writable++
	}
	if writable == 0 {
		add(vmSeverityInfo, "dc-only-dc", p.Source+" is the only writable domain controller of "+old.Domain+".", "The new DC becomes the second; the old one is retired only after the new one has replicated and holds the roles.")
	}
	for _, h := range p.Health {
		if h.Error != "" {
			sev := vmSeverityWarning
			if dcSameComputer(h.DC, old.Name) {
				sev = vmSeverityBlocker
			}
			add(sev, "dc-health-unreachable", "The checks could not reach "+h.DC+": "+preflightErrorSummary(h.DC, "winrm", h.Error), "Check that PowerShell remoting answers on it and the account is a Domain Admin, then check the domain again.")
			continue
		}
		for _, f := range h.Findings {
			if f.Severity == vmSeverityInfo {
				continue
			}
			f.Message = h.DC + ": " + f.Message
			out = append(out, f)
		}
	}
	if !p.done(dcStepHealth) {
		add(vmSeverityBlocker, "dc-health-pending", "The domain has not been checked yet.", "Check the domain: replication, SYSVOL, FSMO owners and time on every DC (read only).")
	}
	if err := dcCheckSettings(p); err != nil {
		add(vmSeverityBlocker, "dc-settings", dcCap(err)+".", "")
	}
	sort.SliceStable(out, func(i, j int) bool { return dcSevRank(out[i].Severity) < dcSevRank(out[j].Severity) })
	return out
}

func dcSevRank(s string) int {
	switch s {
	case vmSeverityBlocker:
		return 0
	case vmSeverityWarning:
		return 1
	}
	return 2
}

func dcBlocked(findings []vmFinding) bool {
	for _, f := range findings {
		if f.Severity == vmSeverityBlocker {
			return true
		}
	}
	return false
}

// dcRetireChecks are the checks made just before the old DC is demoted,
// from a fresh read of the domain (roles, the new DC, global catalogs, DNS).
func dcRetireChecks(p *dcPlan, fresh *dcSourceInfo) []string {
	problems := []string{}
	if fresh == nil {
		return []string{"the domain could not be read again"}
	}
	newName := ""
	if p.New != nil {
		newName = p.New.Name
	}
	var newPeer *dcPeer
	gcOthers, dnsReady := 0, false
	for i := range fresh.DCs {
		peer := &fresh.DCs[i]
		if dcSameComputer(peer.Name, newName) {
			newPeer = peer
		}
		if !dcSameComputer(peer.Name, p.Old.Name) && !peer.ReadOnly && peer.GlobalCatalog {
			gcOthers++
		}
	}
	if newPeer == nil {
		problems = append(problems, newName+" is not listed as a domain controller")
	}
	if gcOthers == 0 {
		problems = append(problems, "no other writable DC is a global catalog")
	}
	for role, owner := range fresh.RoleOwners {
		if dcSameComputer(owner, p.Old.Name) && !p.Settings.OldDead {
			problems = append(problems, p.Old.Name+" still holds the "+dcRoleNames[role]+" role")
		}
	}
	if p.Settings.InstallDNS || !p.Old.DNSServer {
		dnsReady = true
	}
	if !dnsReady {
		problems = append(problems, "the new DC is not a DNS server, so the old DC may be the last one")
	}
	if !p.done(dcStepVerify) {
		problems = append(problems, "the new DC has not finished replicating")
	}
	return problems
}

// dcDHCPReplace returns the DNS server list with the old address replaced
// by the new one, and whether anything changed. The new address is never
// added twice.
func dcReplaceAddress(list []string, from, to string) ([]string, bool) {
	out := []string{}
	changed := false
	seen := map[string]bool{}
	for _, v := range list {
		v = strings.TrimSpace(v)
		if v == from {
			v = to
			changed = true
		}
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out, changed
}

// dcRollbackAllowed says whether Roll back can put things back by itself,
// or why not.
func dcRollbackAllowed(p *dcPlan) (bool, string) {
	if !p.RolledBackAt.IsZero() {
		return false, "This plan was already rolled back."
	}
	if p.step(dcStepDemote).State == dcDone || p.step(dcStepDemote).State == dcRunning {
		return false, "The old DC has been demoted, so it cannot be put back by itself: follow the steps under Roll back to promote it again."
	}
	if p.Settings.OldDead && len(p.RolesMoved) > 0 {
		return false, "The roles were seized from a DC that no longer works; it must never come back online."
	}
	if len(p.RolesMoved) == 0 && !p.done(dcStepPromote) && dcChangesOpen(p) == 0 {
		return false, "Nothing has changed yet."
	}
	return true, ""
}

func dcChangesOpen(p *dcPlan) int {
	n := 0
	for _, c := range p.Changes {
		if !c.Restored {
			n++
		}
	}
	return n
}

// dcRollbackSteps lists what Roll back does, in order.
func dcRollbackSteps(p *dcPlan, removeNew bool) []string {
	steps := []string{}
	for i := len(p.Changes) - 1; i >= 0; i-- {
		c := p.Changes[i]
		if c.Restored {
			continue
		}
		steps = append(steps, dcChangeText(c, true))
	}
	if len(p.RolesMoved) > 0 {
		names := []string{}
		for _, r := range p.RolesMoved {
			names = append(names, dcRoleNames[r])
		}
		steps = append(steps, "Move "+strings.Join(names, ", ")+" back to "+p.Old.Name+".")
	}
	if removeNew && p.done(dcStepPromote) {
		steps = append(steps, "Demote the new DC "+p.New.Name+" (it stays a member server; nothing is deleted).")
	}
	return steps
}

func dcChangeText(c dcChange, undo bool) string {
	before, after := strings.Join(c.Before, ", "), strings.Join(c.After, ", ")
	if undo {
		before, after = after, before
	}
	if before == "" {
		before = "none"
	}
	if after == "" {
		after = "none"
	}
	switch c.Kind {
	case "dhcp-server":
		return fmt.Sprintf("DHCP server %s: DNS servers (server option 6) %s → %s.", c.Server, before, after)
	case "dhcp-scope":
		return fmt.Sprintf("DHCP server %s scope %s: DNS servers %s → %s.", c.Server, c.Key, before, after)
	case "dns-client":
		return fmt.Sprintf("%s adapter %s: DNS servers %s → %s.", c.Server, c.Key, before, after)
	case "time":
		return fmt.Sprintf("%s time source: %s → %s.", c.Server, before, after)
	}
	return fmt.Sprintf("%s %s: %s → %s.", c.Server, c.Key, before, after)
}

// dcRollbackGuide is what to do after the old DC was demoted.
func dcRollbackGuide(p *dcPlan) []string {
	old := "the old server"
	if p.Old != nil {
		old = p.Old.Name
	}
	steps := []string{
		"Leave the new DC running: it holds the roles and a full copy of the directory, so nothing is lost.",
		"If the new DC took over the old address, give " + old + " its old address back only after the new DC has another address (Move the old DC's IP address is not undone by itself).",
		"Promote " + old + " again: in this tool start a new plan with " + old + " as the new server, or use Server Manager's Promote this server to a domain controller.",
		"Move the FSMO roles back with that plan's Move the roles step if you want " + old + " to hold them.",
	}
	if p.Settings.OldDead {
		steps = []string{"A DC whose roles were seized or whose metadata was cleaned up must never be turned on while connected to the network: rebuild it from scratch and promote it as a new DC."}
	}
	return steps
}

// dcCap starts an error with a capital and ends it with a full stop.
func dcCap(err error) string {
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return text
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

// dcStepInput is what Invoke-DcMigration.ps1 needs for a step (no
// passwords: dcmig_windows.go adds those from memory).
func dcStepInput(p *dcPlan, step string) map[string]any {
	in := map[string]any{"step": step, "source": p.Source, "target": p.Target, "settings": p.Settings, "roles": p.Settings.MoveRoles}
	if p.Old != nil {
		if p.Old.HostName != "" {
			in["source"] = p.Old.HostName
		}
		in["domain"] = p.Old.Domain
		in["dhcpServers"] = p.Old.DHCPServers
		clients := []string{}
		for _, peer := range p.Old.DCs {
			if dcSameComputer(peer.Name, p.Old.Name) || (p.New != nil && dcSameComputer(peer.Name, p.New.Name)) {
				continue
			}
			name := peer.HostName
			if name == "" {
				name = peer.Name
			}
			clients = append(clients, name)
		}
		in["dnsClients"] = clients
	}
	if p.New != nil && p.New.HostName != "" && p.New.DomainJoined {
		in["target"] = p.New.HostName
	}
	oa, na := p.dcOldAddress(), p.dcNewAddress()
	in["oldAddress"], in["newAddress"], in["oldAdapter"] = oa.Address, na.Address, oa
	if step == dcStepRoles {
		roles := []string{}
		for _, r := range p.Settings.MoveRoles {
			if !dcHasRole(p.RolesMoved, r) {
				roles = append(roles, r)
			}
		}
		in["roles"] = roles
	}
	return in
}

// dcPreviewChecks are the checks a new plan from this read would get,
// before the domain is checked (so the "not checked yet" line is left out).
func dcPreviewChecks(readFor [2]string, old *dcSourceInfo, tgt *dcTargetInfo, s dcSettings) []vmFinding {
	p := &dcPlan{Name: "Preview", Source: readFor[0], Target: readFor[1], Old: old, New: tgt, Settings: s, Steps: map[string]dcStep{}}
	out := []vmFinding{}
	for _, f := range dcCheckPlan(p) {
		if f.Code == "dc-health-pending" || f.Code == "dc-settings" {
			continue
		}
		out = append(out, f)
	}
	return out
}
