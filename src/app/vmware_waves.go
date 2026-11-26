package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Suggest waves (2026-11-26, user-requested): a wave is a group of VMs moved
// together in one maintenance window. Suggestions group VMs by role and risk
// rather than filling waves in inventory order: test, development and
// powered-off VMs go first as a pilot, one domain controller moves on its own
// early and the others last (never two in the same wave), each database server
// moves alone, servers of one application move together, and VMware
// infrastructure and backup appliances are left out. Roles are guessed from
// VM names, folders and guest OS only, so every suggestion says why it was
// made and the operator can change any of them. VMs the operator set by hand
// (vmPlanVM.Manual) are kept unless resetManual is set.

// vmWaveInfo describes one wave. Title, Window and Notes can be edited;
// Edited keeps them when waves are suggested again. Window is the planned
// maintenance window (local time, 2006-01-02T15:04), shown in the plan and
// the runbook; it does not start anything by itself (Migration plans
// schedule real cutovers).
type vmWaveInfo struct {
	Wave   int    `json:"wave"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
	Window string `json:"window,omitempty"`
	Notes  string `json:"notes,omitempty"`
	Edited bool   `json:"edited,omitempty"`
}

const vmWaveWindowLayout = "2006-01-02T15:04"

// validateVMWaves checks the wave descriptions of an edited plan and returns
// them sorted by wave number.
func validateVMWaves(list []vmWaveInfo) ([]vmWaveInfo, error) {
	seen := map[int]bool{}
	out := []vmWaveInfo{}
	for _, info := range list {
		if info.Wave < 1 || info.Wave > vmPlanMaxWaves {
			return nil, fmt.Errorf("Waves are numbered 1 to %d.", vmPlanMaxWaves)
		}
		if seen[info.Wave] {
			return nil, fmt.Errorf("Wave %d is described twice.", info.Wave)
		}
		seen[info.Wave] = true
		info.Title, info.Window, info.Notes = strings.TrimSpace(info.Title), strings.TrimSpace(info.Window), strings.TrimSpace(info.Notes)
		if len(info.Title) > 100 || strings.ContainsAny(info.Title, "\x00\r\n") {
			return nil, fmt.Errorf("The name of wave %d is limited to 100 characters on one line.", info.Wave)
		}
		if len(info.Reason) > 1000 || len(info.Notes) > 500 || strings.ContainsRune(info.Reason+info.Notes, 0) {
			return nil, fmt.Errorf("The notes of wave %d are limited to 500 characters.", info.Wave)
		}
		if info.Window != "" {
			if _, err := time.ParseInLocation(vmWaveWindowLayout, info.Window, time.Local); err != nil {
				return nil, fmt.Errorf("The planned window of wave %d is not a date and time.", info.Wave)
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Wave < out[j].Wave })
	return out, nil
}

const (
	vmWaveRolePilot    = "pilot"
	vmWaveRoleDC       = "dc"
	vmWaveRoleDatabase = "database"
	vmWaveRoleApp      = "app"
	vmWaveRoleGeneral  = "general"
)

var (
	vmWaveTestPattern     = regexp.MustCompile(`(^|[^a-z])(test|tst|dev|devel|lab|qa|uat|stg|stage|staging|sandbox|sbx|poc|demo|train|training|temp|tmp)([^a-z]|$)`)
	vmWaveDCPattern       = regexp.MustCompile(`(^|[^a-z])(dc|pdc|bdc|adc|adds|ad)[0-9]*([^a-z]|$)`)
	vmWaveDatabasePattern = regexp.MustCompile(`sql|(^|[^a-z])(db|dbs|database|ora|oracle|pg|pgsql|postgres|postgresql|mysql|maria|mariadb|mongo|mongodb|redis|hana)[0-9]*([^a-z]|$)`)
	vmWaveAppPattern      = regexp.MustCompile(`(^|[^a-z])(app|apps|web|www|iis|api|rds|rdsh|ts|citrix|ctx|xa|erp|crm|exch|exchange|mail|sp|sharepoint|portal|front)[0-9]*([^a-z]|$)`)
	vmWaveBackupPattern   = regexp.MustCompile(`veeam|rubrik|cohesity|commvault|avamar|zerto|datadomain|vrops|vrealize|vrli|loginsight|(^|[^a-z])(nsx|backup|vbr)[0-9]*([^a-z]|$)`)
	vmWaveStemPattern     = regexp.MustCompile(`^[a-z]+`)
	vmWaveGenericWords    = regexp.MustCompile(`^(app|apps|web|www|iis|api|srv|svr|server|vm|prod|prd|corp)$`)
)

// vmWaveRoleOf guesses how risky a VM is to move from its name, folder,
// power state and guest OS. The second value says why, in plain words.
func vmWaveRoleOf(vm vmMachine) (string, string) {
	name := strings.ToLower(vm.Name)
	folder := strings.ToLower(vm.Folder)
	windows := strings.Contains(strings.ToLower(vm.GuestOS+" "+vm.GuestOSRunning+" "+vm.GuestID), "win")
	switch {
	case !strings.EqualFold(vm.PowerState, "poweredOn"):
		return vmWaveRolePilot, "Powered off, so nobody is using it."
	case vm.Preflight.domainController():
		return vmWaveRoleDC, "Checked: it is a domain controller."
	case vm.Preflight.sqlServer():
		return vmWaveRoleDatabase, "Checked: it runs SQL Server."
	case vmWaveTestPattern.MatchString(name) || vmWaveTestPattern.MatchString(folder):
		return vmWaveRolePilot, "Named like a test or development VM."
	case windows && vmWaveDCPattern.MatchString(name):
		return vmWaveRoleDC, "Named like a domain controller."
	case vmWaveDatabasePattern.MatchString(name):
		return vmWaveRoleDatabase, "Named like a database server."
	case vmWaveAppPattern.MatchString(name):
		return vmWaveRoleApp, "Named like an application or web server."
	}
	return vmWaveRoleGeneral, "A running server with no special role in its name."
}

// vmWaveExcludeReason names VMware infrastructure and backup appliances,
// which stay behind unless the operator includes them.
func vmWaveExcludeReason(vm vmMachine) string {
	if vm.Template {
		return "A template, not a running VM."
	}
	if role := vmInfrastructureRole(vm); role != "" {
		return "This is " + role + ", which stays with VMware."
	}
	if vmAlreadyMoved(vm) {
		return "Its name says it was already migrated or retired. Include it if it really moves."
	}
	if vmWaveBackupPattern.MatchString(strings.ToLower(vm.Name)) {
		return "Named like a backup or VMware management appliance, which usually stays with VMware or is redeployed. Include it if it moves."
	}
	return ""
}

// vmWaveStem is the application a server belongs to, from its name: the
// letters before the first digit or separator, without a role word at the
// end (ERP-APP01 and ERP-WEB01 are both erp).
func vmWaveStem(name string) string {
	lower := strings.ToLower(name)
	parts := strings.FieldsFunc(lower, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
	for _, part := range parts {
		stem := vmWaveStemPattern.FindString(part)
		if stem == "" || vmWaveGenericWords.MatchString(stem) {
			continue
		}
		return stem
	}
	return lower
}

// vmAutoWaveSize picks a wave size when the operator leaves it on Auto:
// about four waves of running servers, between 2 and 10 VMs each.
func vmAutoWaveSize(count int) int {
	size := (count + 3) / 4
	if size < 2 {
		size = 2
	}
	if size > 10 {
		size = 10
	}
	return size
}

// suggestWaves replaces the suggested waves of every VM the operator did not
// set by hand (all of them with resetManual) and returns the wave size used.
func suggestWaves(plan vmPlan, inventory *vmInventory, waveSize int) vmPlan {
	plan, _ = suggestWavesWith(plan, inventory, waveSize, false)
	return plan
}

func suggestWavesWith(plan vmPlan, inventory *vmInventory, waveSize int, resetManual bool) (vmPlan, int) {
	byID := map[string]vmMachine{}
	for _, vm := range inventory.VMs {
		byID[vm.ID] = vm
	}
	type candidate struct {
		index int
		vm    vmMachine
		role  string
		why   string
	}
	groups := map[string][]candidate{}
	running := 0
	plan.VMs = append([]vmPlanVM{}, plan.VMs...)
	for index := range plan.VMs {
		item := &plan.VMs[index]
		vm, ok := byID[item.ID]
		if !ok {
			continue
		}
		if item.Manual && !resetManual {
			continue
		}
		item.Manual, item.Wave, item.Hold, item.Exclude, item.Why = false, 0, false, false, ""
		if reason := vmWaveExcludeReason(vm); reason != "" {
			item.Exclude, item.Why = true, reason
			continue
		}
		if readiness := vm.readinessFor(plan.Target); readiness != nil && readiness.Status == vmStatusBlocked {
			item.Hold, item.Why = true, "Blocked for "+vmWaveTargetName(plan.Target)+": fix what its readiness lists, then give it a wave."
			continue
		}
		role, why := vmWaveRoleOf(vm)
		if role != vmWaveRolePilot && role != vmWaveRoleDC && role != vmWaveRoleDatabase {
			running++
		}
		groups[role] = append(groups[role], candidate{index: index, vm: vm, role: role, why: why})
	}
	if waveSize <= 0 {
		waveSize = vmAutoWaveSize(running)
	}
	if waveSize > 200 {
		waveSize = 200
	}
	byName := func(list []candidate) {
		sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].vm.Name) < strings.ToLower(list[j].vm.Name) })
	}
	infos := []vmWaveInfo{}
	next := 1
	place := func(list []candidate, title, reason string) {
		if len(list) == 0 {
			return
		}
		wave := next
		if wave > vmPlanMaxWaves {
			wave = vmPlanMaxWaves
		}
		for _, c := range list {
			plan.VMs[c.index].Wave = wave
			plan.VMs[c.index].Why = c.why
		}
		if next <= vmPlanMaxWaves {
			infos = append(infos, vmWaveInfo{Wave: wave, Title: title, Reason: reason})
		}
		next++
	}
	chunks := func(list []candidate, title, reason string) {
		for start := 0; start < len(list); start += waveSize {
			end := start + waveSize
			if end > len(list) {
				end = len(list)
			}
			place(list[start:end], title, reason)
		}
	}
	pilot := groups[vmWaveRolePilot]
	byName(pilot)
	chunks(pilot, "Pilot", "Powered-off, test and development VMs go first: if something about the new platform is wrong, nobody depends on them. Check them on the new platform before the next wave.")
	dcs := groups[vmWaveRoleDC]
	byName(dcs)
	if len(dcs) > 0 {
		reason := "One domain controller on its own, so the domain is proven on the new platform while the other domain controllers keep answering on VMware."
		if len(dcs) == 1 {
			reason = "The only domain controller moves on its own, early, while everything else still runs. Have a second domain controller or a tested backup before this wave."
		}
		place(dcs[:1], "Domain controller", reason)
	}
	general := groups[vmWaveRoleGeneral]
	byName(general)
	chunks(general, "General servers", "Running servers with no special role in their names, such as file, print and utility servers.")
	databases := groups[vmWaveRoleDatabase]
	byName(databases)
	for _, c := range databases {
		place([]candidate{c}, "Database: "+c.vm.Name, "A database server moves alone, so its cutover window is short and its applications are tested against it before anything else moves.")
	}
	apps := groups[vmWaveRoleApp]
	sort.SliceStable(apps, func(i, j int) bool {
		a, b := vmWaveStem(apps[i].vm.Name), vmWaveStem(apps[j].vm.Name)
		if a != b {
			return a < b
		}
		return strings.ToLower(apps[i].vm.Name) < strings.ToLower(apps[j].vm.Name)
	})
	// Servers of one application stay in one wave when it fits.
	var batch []candidate
	var stems []string
	flush := func() {
		if len(batch) > 0 {
			place(batch, "Application servers", "Servers of the same application ("+strings.Join(stems, ", ")+") move together, so the application is tested once, as a whole.")
		}
		batch, stems = nil, nil
	}
	for start := 0; start < len(apps); {
		stem := vmWaveStem(apps[start].vm.Name)
		end := start
		for end < len(apps) && vmWaveStem(apps[end].vm.Name) == stem {
			end++
		}
		group := apps[start:end]
		if len(batch)+len(group) > waveSize {
			flush()
		}
		for len(group) > waveSize {
			batch, stems = append([]candidate{}, group[:waveSize]...), []string{stem}
			flush()
			group = group[waveSize:]
		}
		batch = append(batch, group...)
		stems = append(stems, stem)
		start = end
	}
	flush()
	for _, c := range dcs[vmMinInt(1, len(dcs)):] {
		place([]candidate{c}, "Domain controller: "+c.vm.Name, "The other domain controllers move last, one per wave, so there is always a domain controller running while each one moves.")
	}
	// Keep what the operator wrote for a wave number, and the descriptions
	// of waves that still hold VMs set by hand.
	edited := map[int]vmWaveInfo{}
	for _, info := range plan.Waves {
		if info.Edited {
			edited[info.Wave] = info
		}
	}
	used := map[int]bool{}
	for index := range infos {
		used[infos[index].Wave] = true
		if old, ok := edited[infos[index].Wave]; ok {
			infos[index].Title, infos[index].Window, infos[index].Notes, infos[index].Edited = old.Title, old.Window, old.Notes, true
		}
	}
	for _, item := range plan.VMs {
		if item.Manual && item.Wave > 0 && !used[item.Wave] {
			used[item.Wave] = true
			info, ok := edited[item.Wave]
			if !ok {
				info = vmWaveInfo{Wave: item.Wave, Title: fmt.Sprintf("Wave %d", item.Wave)}
				for _, old := range plan.Waves {
					if old.Wave == item.Wave {
						info = old
					}
				}
			}
			infos = append(infos, info)
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Wave < infos[j].Wave })
	plan.Waves = infos
	plan.WaveSize = waveSize
	return plan, waveSize
}

func vmWaveTargetName(target string) string {
	if profile, ok := vmTargetProfileFor(target); ok && profile.Short != "" {
		return profile.Short
	}
	return "the target"
}

func vmMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// vmWaveSummaryText is one line per wave for logs and messages.
func vmWaveSummaryText(plan vmPlan) string {
	counts := map[int]int{}
	for _, item := range plan.VMs {
		if !item.Exclude && !item.Hold && item.Wave > 0 {
			counts[item.Wave]++
		}
	}
	parts := []string{}
	for _, info := range plan.Waves {
		parts = append(parts, fmt.Sprintf("wave %d %s (%d)", info.Wave, strings.ToLower(info.Title), counts[info.Wave]))
	}
	return strings.Join(parts, ", ")
}
