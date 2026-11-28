package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func dcTestOld() *dcSourceInfo {
	return &dcSourceInfo{
		Name: "DC01", HostName: "DC01.example.com", Domain: "example.com", NetBIOS: "EXAMPLE", Forest: "example.com",
		DomainMode: "Windows2016Domain", ForestMode: "Windows2016Forest", Site: "HQ", Sites: []string{"HQ", "Branch"},
		OS: "Windows Server 2012 R2 Standard", OSVersion: "6.3.9600", GlobalCatalog: true, DNSServer: true,
		Roles:      []string{dcRoleRID, dcRolePDC, dcRoleSchema, dcRoleNaming, dcRoleInfrastructure},
		RoleOwners: map[string]string{dcRoleSchema: "DC01.example.com", dcRoleNaming: "DC01.example.com", dcRolePDC: "DC01.example.com", dcRoleRID: "DC01.example.com", dcRoleInfrastructure: "DC01.example.com"},
		Forwarders: []string{"192.0.2.53"}, Addresses: []dcAddress{{Interface: "Ethernet", Index: 4, Address: "10.0.0.10", Prefix: 24, Gateway: "10.0.0.1", DNS: []string{"10.0.0.10", "127.0.0.1"}}},
		NtpServer: "pool.example.com,0x9", TimeType: "NTP", Sysvol: "DFSR", DatabasePath: `C:\Windows\NTDS`, LogPath: `C:\Windows\NTDS`, SysvolPath: `C:\Windows\SYSVOL`,
		DCs:         []dcPeer{{Name: "DC01", HostName: "DC01.example.com", Site: "HQ", Address: "10.0.0.10", GlobalCatalog: true}},
		DHCPServers: []string{"dhcp01.example.com"},
	}
}

func dcTestNew() *dcTargetInfo {
	return &dcTargetInfo{Name: "DC02", HostName: "DC02.example.com", OS: "Windows Server 2022 Standard", OSVersion: "10.0.20348", DomainJoined: true, Domain: "example.com",
		FreeGB: 80, Addresses: []dcAddress{{Interface: "Ethernet", Index: 6, Address: "10.0.0.20", Prefix: 24, Gateway: "10.0.0.1", DNS: []string{"10.0.0.10"}}}}
}

func dcTestSettings() dcSettings {
	s := dcSuggest(dcSettings{}, dcTestOld(), dcTestNew())
	s.OldNewAddress = "10.0.0.30"
	s.Suggested["oldNewAddress"] = false
	return s
}

func TestDCSuggestFillsEverythingFromTheOldDC(t *testing.T) {
	s := dcSuggest(dcSettings{}, dcTestOld(), dcTestNew())
	if s.Site != "HQ" || !s.InstallDNS || !s.GlobalCatalog || s.ReplicateFrom != "DC01.example.com" {
		t.Fatalf("settings: %+v", s)
	}
	if strings.Join(s.MoveRoles, ",") != "SchemaMaster,DomainNamingMaster,PDCEmulator,RIDMaster,InfrastructureMaster" {
		t.Fatalf("roles in order: %v", s.MoveRoles)
	}
	if s.NtpServer != "pool.example.com" {
		t.Fatalf("time source copied without flags: %q", s.NtpServer)
	}
	if s.IPMode != dcIPTakeover || s.OldNewPrefix != 24 || s.OldNewGateway != "10.0.0.1" {
		t.Fatalf("takeover by default: %+v", s)
	}
	for _, k := range []string{"site", "installDns", "moveRoles", "ntpServer", "ipMode", "oldNewAddress"} {
		if s.Why[k] == "" {
			t.Fatalf("no reason for %s", k)
		}
	}
	// A field the operator changed is kept by the next suggestion.
	s.Site, s.Suggested["site"] = "Branch", false
	s = dcSuggest(s, dcTestOld(), dcTestNew())
	if s.Site != "Branch" {
		t.Fatalf("changed site replaced: %q", s.Site)
	}
	// Another subnet cannot take over the address.
	tgt := dcTestNew()
	tgt.Addresses[0].Address = "10.9.0.20"
	if s2 := dcSuggest(dcSettings{}, dcTestOld(), tgt); s2.IPMode != dcIPRepoint {
		t.Fatalf("other subnet should repoint: %q", s2.IPMode)
	}
	// An old PDC with no external source gets one, said so.
	old := dcTestOld()
	old.TimeType = "NT5DS"
	if s3 := dcSuggest(dcSettings{}, old, dcTestNew()); s3.NtpServer != "time.windows.com" || !strings.Contains(s3.Why["ntpServer"], "no external time source") {
		t.Fatalf("time: %q %q", s3.NtpServer, s3.Why["ntpServer"])
	}
}

func TestDCCheckSettingsRefusesWhatTheStepsCannotUse(t *testing.T) {
	p := &dcPlan{Old: dcTestOld(), New: dcTestNew(), Settings: dcTestSettings()}
	if err := dcCheckSettings(p); err != nil {
		t.Fatal(err)
	}
	bad := []func(s *dcSettings){
		func(s *dcSettings) { s.Site = "Nowhere" },
		func(s *dcSettings) { s.DatabasePath = `..\x` },
		func(s *dcSettings) { s.MoveRoles = []string{"Boss"} },
		func(s *dcSettings) { s.OldNewAddress = "" },
		func(s *dcSettings) { s.OldNewAddress = "10.0.0.10" },
		func(s *dcSettings) { s.OldNewPrefix = 33 },
		func(s *dcSettings) { s.IPMode = "" },
		func(s *dcSettings) { s.NtpServer = "a;b" },
		func(s *dcSettings) { s.Forwarders = []string{"not-an-ip"} },
	}
	for i, change := range bad {
		q := &dcPlan{Old: dcTestOld(), New: dcTestNew(), Settings: dcTestSettings()}
		change(&q.Settings)
		if err := dcCheckSettings(q); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	// Repoint needs no spare address.
	q := &dcPlan{Old: dcTestOld(), New: dcTestNew(), Settings: dcTestSettings()}
	q.Settings.IPMode, q.Settings.OldNewAddress = dcIPRepoint, ""
	if err := dcCheckSettings(q); err != nil {
		t.Fatal(err)
	}
}

func TestDCCheckPlanBlocksUnsafeMoves(t *testing.T) {
	codes := func(p *dcPlan) map[string]string {
		out := map[string]string{}
		for _, f := range dcCheckPlan(p) {
			out[f.Code] = f.Severity
		}
		return out
	}
	base := func() *dcPlan {
		p := &dcPlan{Source: "DC01", Target: "DC02", Old: dcTestOld(), New: dcTestNew(), Settings: dcTestSettings()}
		p.setStep(dcStepHealth, dcStep{State: dcDone})
		return p
	}
	if c := codes(base()); len(c) != 1 || c["dc-only-dc"] != vmSeverityInfo {
		t.Fatalf("clean plan: %v", c)
	}
	cases := map[string]func(p *dcPlan){
		"dc-frs":              func(p *dcPlan) { p.Old.Sysvol = "FRS" },
		"dc-rodc":             func(p *dcPlan) { p.Old.ReadOnly = true },
		"dc-same":             func(p *dcPlan) { p.Target = "dc01.example.com" },
		"dc-new-os":           func(p *dcPlan) { p.New.OSVersion = "6.3.9600" },
		"dc-level-2025":       func(p *dcPlan) { p.New.OSVersion = "10.0.26100"; p.Old.DomainMode = "Windows2012R2Domain" },
		"dc-new-reboot":       func(p *dcPlan) { p.New.PendingReboot = true },
		"dc-new-space":        func(p *dcPlan) { p.New.FreeGB = 4 },
		"dc-new-dhcp":         func(p *dcPlan) { p.New.Addresses[0].DHCP = true },
		"dc-new-other-domain": func(p *dcPlan) { p.New.Domain = "other.example" },
		"dc-health-pending":   func(p *dcPlan) { p.setStep(dcStepHealth, dcStep{State: dcPending}) },
		"dc-health-unreachable": func(p *dcPlan) {
			p.Health = []dcHealth{{DC: "DC01.example.com", Error: "WinRM cannot complete the operation"}}
		},
	}
	for code, change := range cases {
		p := base()
		change(p)
		if c := codes(p); c[code] != vmSeverityBlocker {
			t.Fatalf("%s not blocked: %v", code, c)
		}
	}
	p := base()
	p.Old.DHCPServer = true
	p.Old.OtherRoles = []string{"Active Directory Certificate Services"}
	p.New.Addresses[0].DNS = []string{"8.8.8.8"}
	c := codes(p)
	if c["dc-old-dhcp"] != vmSeverityWarning || c["dc-old-roles"] != vmSeverityWarning || c["dc-new-dns"] != vmSeverityWarning {
		t.Fatalf("warnings: %v", c)
	}
	// A health finding is named with its DC.
	p = base()
	p.Health = []dcHealth{{DC: "DC01.example.com", Findings: []vmFinding{{Severity: vmSeverityBlocker, Code: "dc-repl-failing", Message: "Replication fails."}}}}
	found := false
	for _, f := range dcCheckPlan(p) {
		if f.Code == "dc-repl-failing" && strings.HasPrefix(f.Message, "DC01.example.com: ") {
			found = true
		}
	}
	if !found {
		t.Fatal("health finding missing")
	}
}

// dcFakeHost records the steps run and plays the servers.
type dcFakeHost struct {
	mu        sync.Mutex
	steps     []string
	fail      map[string]error
	verifyN   int
	health    map[string][]vmFinding
	fresh     *dcSourceInfo
	rollbacks []dcRollbackRequest
}

func (h *dcFakeHost) ReadOld(ctx context.Context, p *dcPlan) (*dcSourceInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fresh != nil {
		c := *h.fresh
		return &c, nil
	}
	return dcTestOld(), nil
}

func (h *dcFakeHost) ReadNew(ctx context.Context, p *dcPlan) (*dcTargetInfo, error) {
	return dcTestNew(), nil
}

func (h *dcFakeHost) Health(ctx context.Context, p *dcPlan, dc string) ([]vmFinding, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.health[dc], nil
}

func (h *dcFakeHost) Step(ctx context.Context, p *dcPlan, step string) (dcStepResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steps = append(h.steps, step)
	if err := h.fail[step]; err != nil {
		return dcStepResult{}, err
	}
	switch step {
	case dcStepVerify:
		h.verifyN++
		if h.verifyN < 2 {
			return dcStepResult{Waiting: "SYSVOL is not ready"}, nil
		}
		return dcStepResult{Ready: true, Notes: []string{"working"}}, nil
	case dcStepRoles:
		roles, _ := dcStepInput(p, step)["roles"].([]string)
		return dcStepResult{RolesMoved: roles, Changes: []dcChange{{Kind: "time", Server: "DC02.example.com", Key: "pdc", Before: []string{"", "NT5DS"}, After: []string{"pool.example.com", "NTP"}}}}, nil
	case dcStepRepoint:
		return dcStepResult{Changes: []dcChange{{Kind: "dhcp-scope", Server: "dhcp01.example.com", Key: "10.0.0.0", Before: []string{"10.0.0.10"}, After: []string{"10.0.0.20"}}}}, nil
	}
	return dcStepResult{}, nil
}

func (h *dcFakeHost) Rollback(ctx context.Context, p *dcPlan, req dcRollbackRequest) (dcStepResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rollbacks = append(h.rollbacks, req)
	restored := []int{}
	for i := range req.Changes {
		restored = append(restored, i)
	}
	return dcStepResult{RolesMoved: req.Roles, Restored: restored}, nil
}

func dcTestRunner(t *testing.T, host *dcFakeHost) *dcRunner {
	r := newDCRunner(filepath.Join(t.TempDir(), "dc-plans.json"), host)
	r.poll = time.Millisecond
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	return r
}

func dcWait(t *testing.T, r *dcRunner, id string) dcPlan {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, busy := r.Running()[id]; !busy {
			return *r.snapshot(id)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("phase did not end")
	return dcPlan{}
}

func dcTestCreate(t *testing.T, r *dcRunner, mode string) *dcPlan {
	t.Helper()
	s := dcTestSettings()
	if mode == dcIPRepoint {
		s.IPMode, s.Suggested["ipMode"] = dcIPRepoint, false
	}
	p, err := r.Create(dcCreateInput{Name: "Replace DC01", Source: "DC01", Target: "DC02", User: `EXAMPLE\admin`, Settings: s}, dcTestOld(), dcTestNew())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDCPlanRunsBuildCutoverAndRetireInOrder(t *testing.T) {
	host := &dcFakeHost{}
	r := dcTestRunner(t, host)
	p := dcTestCreate(t, r, dcIPTakeover)
	if p.step(dcStepJoin).State != dcSkipped || p.step(dcStepRepoint).State != dcSkipped || p.step(dcStepIP).State != dcPending {
		t.Fatalf("steps for a joined server with takeover: %+v", p.Steps)
	}
	if err := r.Run(p.ID, dcPhaseCutover); err == nil {
		t.Fatal("roles moved before the build")
	}
	if err := r.Run(p.ID, dcPhaseBuild); err != nil {
		t.Fatal(err)
	}
	got := dcWait(t, r, p.ID)
	if !got.done(dcStepVerify) || got.step(dcStepPromote).State != dcDone {
		t.Fatalf("build: %+v", got.Steps)
	}
	if strings.Join(host.steps, ",") != "install,promote,verify,verify" {
		t.Fatalf("build steps: %v", host.steps)
	}
	if err := r.Run(p.ID, dcPhaseCutover); err != nil {
		t.Fatal(err)
	}
	got = dcWait(t, r, p.ID)
	if len(got.RolesMoved) != 5 || len(got.Changes) != 1 {
		t.Fatalf("cutover: roles %v changes %v", got.RolesMoved, got.Changes)
	}
	if ok, why := dcRollbackAllowed(&got); !ok {
		t.Fatalf("roll back refused before demotion: %s", why)
	}
	// Retire refuses while the fresh read shows the old DC holding a role.
	host.fresh = dcTestOld()
	host.fresh.DCs = append(host.fresh.DCs, dcPeer{Name: "DC02", HostName: "DC02.example.com", GlobalCatalog: true})
	if err := r.Run(p.ID, dcPhaseRetire); err != nil {
		t.Fatal(err)
	}
	got = dcWait(t, r, p.ID)
	if got.step(dcStepDemote).State != dcFailed || !strings.Contains(got.step(dcStepDemote).Message, "still holds") {
		t.Fatalf("demotion with roles on the old DC: %+v", got.step(dcStepDemote))
	}
	for role := range host.fresh.RoleOwners {
		host.fresh.RoleOwners[role] = "DC02.example.com"
	}
	if err := r.Run(p.ID, dcPhaseRetire); err != nil {
		t.Fatal(err)
	}
	got = dcWait(t, r, p.ID)
	if got.step(dcStepDemote).State != dcDone || got.step(dcStepIP).State != dcDone {
		t.Fatalf("retire: %+v", got.Steps)
	}
	if ok, _ := dcRollbackAllowed(&got); ok {
		t.Fatal("roll back offered after demotion")
	}
	if len(dcRollbackGuide(&got)) == 0 {
		t.Fatal("no guide after demotion")
	}
}

func TestDCPlanRepointsAndRollsBack(t *testing.T) {
	host := &dcFakeHost{}
	r := dcTestRunner(t, host)
	p := dcTestCreate(t, r, dcIPRepoint)
	if p.step(dcStepIP).State != dcSkipped || p.step(dcStepRepoint).State != dcPending {
		t.Fatalf("repoint plan steps: %+v", p.Steps)
	}
	r.Run(p.ID, dcPhaseBuild)
	dcWait(t, r, p.ID)
	r.Run(p.ID, dcPhaseCutover)
	got := dcWait(t, r, p.ID)
	if len(got.Changes) != 2 {
		t.Fatalf("changes: %+v", got.Changes)
	}
	steps := dcRollbackSteps(&got, true)
	if len(steps) != 4 || !strings.Contains(steps[0], "dhcp01") || !strings.Contains(steps[2], "back to DC01") {
		t.Fatalf("roll back steps (newest change first): %v", steps)
	}
	done, err := r.Rollback(p.ID, false)
	if err != nil || len(done) == 0 {
		t.Fatalf("rollback: %v %v", done, err)
	}
	got = *r.snapshot(p.ID)
	if got.RolledBackAt.IsZero() || len(got.RolesMoved) != 0 || dcChangesOpen(&got) != 0 {
		t.Fatalf("after roll back: %+v", got)
	}
	if len(host.rollbacks) != 1 || len(host.rollbacks[0].Roles) != 5 || host.rollbacks[0].RemoveNew {
		t.Fatalf("rollback request: %+v", host.rollbacks)
	}
	if _, err := r.Rollback(p.ID, false); err == nil {
		t.Fatal("rolled back twice")
	}
	if err := r.Run(p.ID, dcPhaseCutover); err == nil {
		t.Fatal("a rolled back plan ran again")
	}
}

func TestDCPlanStopsAtAFailedStepAndHealthBlocks(t *testing.T) {
	host := &dcFakeHost{fail: map[string]error{dcStepPromote: errors.New("the prerequisites check failed: the DSRM password does not meet complexity")}}
	r := dcTestRunner(t, host)
	p := dcTestCreate(t, r, dcIPTakeover)
	r.Run(p.ID, dcPhaseBuild)
	got := dcWait(t, r, p.ID)
	if got.step(dcStepPromote).State != dcFailed || got.step(dcStepVerify).State != dcPending {
		t.Fatalf("after a failed promotion: %+v", got.Steps)
	}
	if !strings.HasPrefix(got.step(dcStepPromote).Message, "The prerequisites") {
		t.Fatalf("message: %q", got.step(dcStepPromote).Message)
	}
	// A failing replication check stops the build before any change.
	host2 := &dcFakeHost{health: map[string][]vmFinding{"DC01.example.com": {{Severity: vmSeverityBlocker, Code: "dc-repl-failing", Message: "Replication fails."}}}}
	r2 := dcTestRunner(t, host2)
	p2 := dcTestCreate(t, r2, dcIPTakeover)
	r2.Run(p2.ID, dcPhaseBuild)
	got = dcWait(t, r2, p2.ID)
	if got.step(dcStepHealth).State != dcFailed || len(host2.steps) != 0 {
		t.Fatalf("health did not block: %+v steps %v", got.Steps, host2.steps)
	}
	// Checking again (read only) is allowed at any time.
	host2.health = nil
	if err := r2.RunHealth(p2.ID); err != nil {
		t.Fatal(err)
	}
	got = dcWait(t, r2, p2.ID)
	if got.step(dcStepHealth).State != dcDone {
		t.Fatalf("health again: %+v", got.step(dcStepHealth))
	}
}

func TestDCPlanSchedulesAndSurvivesARestart(t *testing.T) {
	host := &dcFakeHost{}
	r := dcTestRunner(t, host)
	p := dcTestCreate(t, r, dcIPTakeover)
	now := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	if err := r.Schedule(p.ID, dcPhaseBuild, now.Add(time.Hour)); err == nil {
		t.Fatal("build scheduled")
	}
	r.Run(p.ID, dcPhaseBuild)
	dcWait(t, r, p.ID)
	if err := r.Schedule(p.ID, dcPhaseCutover, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.Tick()
	if len(r.Running()) != 0 {
		t.Fatal("ran early")
	}
	now = now.Add(2 * time.Hour)
	r.Tick()
	got := dcWait(t, r, p.ID)
	if got.Schedule != nil || len(got.RolesMoved) != 5 {
		t.Fatalf("scheduled cutover: %+v", got)
	}
	// A step left running by a stopped application is failed on load.
	r.mu.Lock()
	live, _ := r.planLocked(p.ID)
	live.setStep(dcStepDemote, dcStep{State: dcRunning})
	r.saveLocked()
	r.mu.Unlock()
	r2 := newDCRunner(r.path, host)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	if s := r2.snapshot(p.ID).step(dcStepDemote); s.State != dcFailed || !strings.Contains(s.Message, "stopped") {
		t.Fatalf("after restart: %+v", s)
	}
}

func TestDCStepInputAndHelpers(t *testing.T) {
	p := &dcPlan{Source: "DC01", Target: "DC02", Old: dcTestOld(), New: dcTestNew(), Settings: dcTestSettings(), RolesMoved: []string{dcRolePDC}}
	p.Old.DCs = append(p.Old.DCs, dcPeer{Name: "DC02", HostName: "DC02.example.com"}, dcPeer{Name: "DC03", HostName: "DC03.example.com"})
	in := dcStepInput(p, dcStepRoles)
	if in["source"] != "DC01.example.com" || in["target"] != "DC02.example.com" || in["oldAddress"] != "10.0.0.10" || in["newAddress"] != "10.0.0.20" {
		t.Fatalf("input: %+v", in)
	}
	if roles := in["roles"].([]string); len(roles) != 4 || dcHasRole(roles, dcRolePDC) {
		t.Fatalf("roles already moved are not moved again: %v", roles)
	}
	if clients := in["dnsClients"].([]string); len(clients) != 1 || clients[0] != "DC03.example.com" {
		t.Fatalf("dns clients: %v", clients)
	}
	for _, k := range []string{"password", "dsrm", "localAdmin"} {
		if _, ok := in[k]; ok {
			t.Fatalf("%s in the input", k)
		}
	}
	if out, changed := dcReplaceAddress([]string{"10.0.0.10", "10.0.0.20", "10.0.0.11"}, "10.0.0.10", "10.0.0.20"); !changed || strings.Join(out, ",") != "10.0.0.20,10.0.0.11" {
		t.Fatalf("replace: %v", out)
	}
	for name, want := range map[string]bool{"DC01": true, "dc01.example.com": true, "10.0.0.0/24": false, "a b": false, `\\x`: false, "": false, "x..y": false} {
		if (dcCheckComputer(name, "x") == nil) != want {
			t.Fatalf("computer %q", name)
		}
	}
	if dcOSLabel(dcOSBuild("10.0.26100")) != "Windows Server 2025" || dcModeLevel("Windows2008R2Domain") != 4 {
		t.Fatal("labels")
	}
}
