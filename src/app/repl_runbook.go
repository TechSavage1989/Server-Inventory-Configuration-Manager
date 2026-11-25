package main

// The replication runbook: every job the console knows, with where each part
// runs, the job's settings and numbered steps (before cutover, test failover,
// cutover, verify, roll back or fail back), written as HTML (ticks saved in
// the browser by vmRunbookScript) and Word (check box content controls). No
// Windows dependency.

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	replRunbookHTML = "Replication-Runbook.html"
	replRunbookDocx = "Replication-Runbook.docx"
)

type replRunbookJob struct {
	ID       string
	Server   string
	Platform string
	Name     string
	State    string
	Facts    [][2]string
	Disks    []string
	Receiver string // hyperv or media
	Before   []string
	Test     []string
	Cutover  []string
	Verify   []string
	Back     []string
}

type replRunbookServer struct {
	Name string
	Jobs []replRunbookJob
}

// replRunbookRoles says where each part of a replication runs.
func replRunbookRoles() []vmMoveRole {
	return []vmMoveRole{
		{Where: "Your admin computer (the console)", Does: "Run this application here: Replication tab. Install agents, create jobs, watch recovery points and alerts, start cutovers and write this runbook. Disks do not pass through this computer.", Needs: "Windows, and HTTPS access to each server's agent port (44390). For Linux servers, SSH access and simm-agent-linux-amd64 beside this program."},
		{Where: "Each server being replicated (the source)", Does: "The replication agent runs here as a service (SIMMReplicationAgent on Windows, simm-replication-agent on Linux). It reads every disk from snapshots each round and sends only what changed to the receiver.", Needs: "Administrator or root rights to install the agent, a few GB free for Volume Shadow Copy or LVM snapshots, a network path to the receiver (port 44380 by default), and endpoint protection exclusions for the agent (docs/EDR-EXCLUSIONS.md) added before the first round."},
		{Where: "The Hyper-V host, for a Hyper-V target", Does: "Run this application here as administrator: VMware tab, Disk copy, Receive. Copies are written here as VHDX files. Test failovers and the production VM are started here (Start a VM from the copies).", Needs: "The Hyper-V role and its PowerShell module, room for the disks, and the receiver port open to the servers."},
		{Where: "The new VM or physical server, for any other target", Does: "Boot it from the receiver media. It shows its address, pairing code and certificate thumbprint and writes each disk onto its own blank disk. At cutover it is set to boot from its disks.", Needs: "Blank disks at least as large as the source disks, and its storage and network drivers on the media."},
	}
}

func replRunbookFor(rows []replAgentRow) []replRunbookServer {
	servers := []replRunbookServer{}
	for _, row := range rows {
		platform := vmNonEmpty(row.Live.Platform, row.OS)
		server := replRunbookServer{Name: vmNonEmpty(row.Live.Computer, row.Name)}
		for _, job := range row.Live.Jobs {
			server.Jobs = append(server.Jobs, replRunbookJobFor(server.Name, platform, job))
		}
		if len(server.Jobs) > 0 {
			servers = append(servers, server)
		}
	}
	sort.SliceStable(servers, func(a, b int) bool { return strings.ToLower(servers[a].Name) < strings.ToLower(servers[b].Name) })
	return servers
}

func replRunbookJobFor(server, platform string, view replJobView) replRunbookJob {
	spec, status := view.Spec, view.Status
	job := replRunbookJob{ID: spec.ID, Server: server, Platform: platform, Name: spec.Name, State: replStateText(status.State), Receiver: "hyperv"}
	interval := "continuous"
	if spec.IntervalMinutes > 0 {
		interval = fmt.Sprintf("every %d minutes", spec.IntervalMinutes)
	}
	recovery := "none yet"
	if !status.RecoveryPoint.IsZero() {
		recovery = status.RecoveryPoint.UTC().Format("2006-01-02 15:04 UTC")
	}
	cutover := "not scheduled"
	if spec.CutoverAt != nil {
		cutover = "scheduled for " + spec.CutoverAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	services := "none"
	if len(spec.StopServices) > 0 {
		services = strings.Join(spec.StopServices, ", ")
	}
	job.Facts = [][2]string{
		{"Server", server + " (" + vmNonEmpty(platform, "unknown platform") + ")"},
		{"Receiver", spec.Receiver.Address},
		{"Rounds", interval + fmt.Sprintf(", alert when the recovery point is older than %d minutes", spec.RPOMinutes)},
		{"Recovery point when written", recovery},
		{"State when written", job.State},
		{"Cutover", cutover},
		{"Services stopped at cutover", services},
		{"After cutover", map[bool]string{true: "the server shuts down", false: "the server is left running with those services stopped"}[spec.ShutdownAfterCutover]},
	}
	for _, disk := range spec.Disks {
		job.Disks = append(job.Disks, "Disk "+disk.Source+" to "+disk.Target)
		if !strings.HasSuffix(strings.ToLower(disk.Target), ".vhdx") {
			job.Receiver = "media"
		}
	}
	job.Before = []string{
		"Check that " + spec.Name + " shows Up to date and its recovery point is within its limit.",
		"Note the server's IP settings, DNS records and any firewall rules that name its address.",
		"Endpoint protection: confirm that SentinelOne, Microsoft Defender, CrowdStrike or whatever runs on "+server+" has the replication agent's exclusions (the application's docs/EDR-EXCLUSIONS.md), so it cannot end or quarantine the agent during cutover.",
		"Agree the cutover window with the application owner and tell users.",
		"Make sure the new machine has the network and storage drivers it needs (on the receiver media, or Hyper-V integration services, which recent Windows and Linux include).",
	}
	if job.Receiver == "hyperv" {
		job.Test = []string{
			"On the Hyper-V host: VMware tab, Disk copy, Start a VM from the copies. Choose Find copies and tick every disk of " + server + ".",
			"Choose Test failover, leave the network Not connected (or choose an isolated switch), type the VM name and create it. Replication into those copies pauses and the job shows Paused for a test failover.",
			"Sign in to the test VM and check that it starts and the application's data is there. Do not connect it to the production network.",
			"Choose End test (type the VM name). The test VM and its test disks are removed and replication catches up on its next round.",
		}
	} else {
		job.Test = []string{
			"For a receiver VM on HyperCore, Nutanix AHV or Proxmox VE: in the Replication tab, open " + spec.Name + " and use Test failover on the target platform. Connect to the platform, name the VM that runs the receiver and the test VM, type the test VM's name and start it.",
			"The job pauses when its current round ends, the platform clones the receiver VM (the clone has its own disks) and replication resumes at once. The clone's network is disconnected; it starts from its disk.",
			"Open the test VM's console on the platform, sign in and check that it starts and the application's data is there. Do not connect it to the production network.",
			"Choose End test (type the test VM's name): the test VM is turned off and deleted with its disks. The receiver VM and its disks are never changed, so no rescan is needed.",
			"For a physical server receiving the copy there is no clone: test by starting it from its disks on an isolated network, boot it from the receiver media again, then choose Rescan the receiver on " + spec.Name + " so the next round sends only what differs.",
		}
	}
	job.Cutover = []string{
		"In the Replication tab, open " + spec.Name + " and choose Cut over (type the job name), or let the scheduled cutover run.",
		"Wait until the job shows Cut over: services stopped (" + services + "), the last round sent and every disk checked against its hashes.",
	}
	if !spec.ShutdownAfterCutover {
		job.Cutover = append(job.Cutover, "Shut "+server+" down, or disconnect it from the network, so it cannot answer for its old address.")
	}
	if job.Receiver == "hyperv" {
		job.Cutover = append(job.Cutover, "On the Hyper-V host: Start a VM from the copies, choose Production, connect it to the production switch and VLAN, and start it.")
	} else {
		job.Cutover = append(job.Cutover, "Set the new machine to boot from its disks and start it (Cut over in its move panel for HyperCore, Nutanix or Proxmox, or the boot menu on a physical server).")
	}
	job.Cutover = append(job.Cutover, "Give the new machine the old IP settings if they did not carry over, and check DNS.")
	job.Verify = []string{
		"Sign in and check that the application's services are running and users can reach it.",
		"Collect an inventory of the new machine on the Setup tab and compare it with the one taken before (VMware tab, Before and after).",
		"Keep the old server powered off but intact until the application owner signs off.",
	}
	job.Back = []string{
		"Before anyone works on the new machine: turn it off and start the old server again; its disks were never changed.",
		"After work happened on the new machine: install the agent on it and create a job back to a receiver on the old machine with The receiver's disks already hold an older copy ticked, so only the changes go back. Then cut over the same way.",
	}
	return job
}

func replStateText(state string) string {
	switch state {
	case replStateNeverRun:
		return "waiting for the first round"
	case replStateIdle:
		return "up to date"
	case replStateSyncing:
		return "sending changes"
	case replStatePaused:
		return "paused"
	case replStateHeld:
		return "paused for a test failover"
	case replStateError:
		return "failing, will retry"
	case replStateCutover:
		return "cutting over"
	case replStateCutDone:
		return "cut over"
	}
	return state
}

const replRunbookTemplate = `<!DOCTYPE html>
<html lang="en" data-runbook="replication|{{.Generated}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src '{{.ScriptHash}}'"><title>Replication runbook</title><style>{{.Style}}</style></head>
<body><main>
<h1>Replication runbook</h1>
<p class="sub">{{len .Servers}} servers, {{.Jobs}} jobs · written {{.Generated}} · {{.Generator}} {{.Version}}</p>
<div class="note">Work through each job in order. Keep every old server powered off but intact until the application owner signs off, so each cutover can be rolled back.</div>
<div class="tools" id="tools" hidden><span>Tick each step as it is done. Ticks are saved in this browser for this runbook. <b class="progress" id="progressAll"></b></span><button type="button" id="printRunbook">Print</button><button type="button" id="clearTicks">Clear all ticks</button></div>
<h2>Where each part runs</h2>
<table><tr><th>Computer</th><th>What runs there</th><th>What it needs</th></tr>{{range .Roles}}<tr><td><b>{{.Where}}</b></td><td>{{.Does}}</td><td>{{.Needs}}</td></tr>{{end}}</table>
<h2>Jobs</h2>
<table><tr><th>Server</th><th>Job</th><th>Disks</th><th>State when written</th><th>Done</th></tr>{{range $s, $server := .Servers}}{{range $server.Jobs}}<tr><td>{{$server.Name}}</td><td>{{.Name}}</td><td>{{len .Disks}}</td><td>{{.State}}</td><td><span class="progress" data-wave-row="server-{{$s}}"></span></td></tr>{{end}}{{end}}</table>
{{range $s, $server := .Servers}}<section class="wave" id="server-{{$s}}"><h2>{{$server.Name}} <span class="progress"></span></h2>
{{range $server.Jobs}}{{$id := .ID}}<div class="vm"><h3>{{.Name}} <span class="progress"></span></h3>
<table>{{range .Facts}}<tr><th>{{index . 0}}</th><td>{{index . 1}}</td></tr>{{end}}<tr><th>Disks</th><td>{{range $i, $d := .Disks}}{{if $i}}; {{end}}{{$d}}{{end}}</td></tr></table>
<div class="grid2"><div><h3>Before cutover</h3><ol class="steps">{{range .Before}}<li><label><input type="checkbox" class="step" data-step="{{$id}}|before|{{.}}"><span>{{.}}</span></label></li>{{end}}</ol></div><div><h3>Test failover</h3><ol class="steps">{{range .Test}}<li><label><input type="checkbox" class="step" data-step="{{$id}}|test|{{.}}"><span>{{.}}</span></label></li>{{end}}</ol></div><div><h3>Cutover</h3><ol class="steps">{{range .Cutover}}<li><label><input type="checkbox" class="step" data-step="{{$id}}|cutover|{{.}}"><span>{{.}}</span></label></li>{{end}}</ol></div><div><h3>Verify</h3><ol class="steps">{{range .Verify}}<li><label><input type="checkbox" class="step" data-step="{{$id}}|verify|{{.}}"><span>{{.}}</span></label></li>{{end}}</ol></div><div><h3>Roll back or fail back</h3><ol>{{range .Back}}<li>{{.}}</li>{{end}}</ol></div></div>
</div>{{end}}</section>{{end}}
</main><script>{{.Script}}</script></body></html>`

var replRunbookTmpl = template.Must(template.New("replrunbook").Parse(replRunbookTemplate))

// writeReplRunbook writes the HTML and Word runbooks into folder.
func writeReplRunbook(folder string, rows []replAgentRow, generated time.Time, version string) (string, string, error) {
	servers := replRunbookFor(rows)
	jobs := 0
	for _, server := range servers {
		jobs += len(server.Jobs)
	}
	if jobs == 0 {
		return "", "", fmt.Errorf("there are no replication jobs to write a runbook for")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		return "", "", err
	}
	stamp := generated.UTC().Format("2006-01-02 15:04 UTC")
	var page bytes.Buffer
	if err := replRunbookTmpl.Execute(&page, map[string]any{"Servers": servers, "Jobs": jobs, "Roles": replRunbookRoles(), "Generated": stamp, "Generator": vmReportGeneratorLine, "Version": version, "Style": template.CSS(vmReportStylesheet + vmRunbookStyle), "Script": template.JS(vmRunbookScript), "ScriptHash": vmRunbookScriptHash()}); err != nil {
		return "", "", err
	}
	htmlPath := filepath.Join(folder, replRunbookHTML)
	if err := os.WriteFile(htmlPath, page.Bytes(), 0600); err != nil {
		return "", "", err
	}
	var d docxBuilder
	d.paragraph("Title", "Replication runbook")
	d.paragraph("Subtitle", fmt.Sprintf("%d servers, %d jobs · written %s · %s %s", len(servers), jobs, stamp, vmReportGeneratorLine, version))
	d.paragraph("Note", "Work through each job in order. Keep every old server powered off but intact until the application owner signs off, so each cutover can be rolled back.")
	d.paragraph("Heading2", "Where each part runs")
	roles := [][]string{}
	for _, role := range replRunbookRoles() {
		roles = append(roles, []string{role.Where, role.Does, role.Needs})
	}
	d.table([]string{"Computer", "What runs there", "What it needs"}, roles)
	summary := [][]string{}
	for _, server := range servers {
		for _, job := range server.Jobs {
			summary = append(summary, []string{server.Name, job.Name, fmt.Sprint(len(job.Disks)), job.State})
		}
	}
	d.paragraph("Heading2", "Jobs")
	d.table([]string{"Server", "Job", "Disks", "State when written"}, summary)
	for _, server := range servers {
		d.heading(1, server.Name)
		for _, job := range server.Jobs {
			d.heading(2, job.Name)
			facts := [][]string{}
			for _, fact := range job.Facts {
				facts = append(facts, []string{fact[0], fact[1]})
			}
			facts = append(facts, []string{"Disks", strings.Join(job.Disks, "; ")})
			d.table([]string{"", ""}, facts)
			for _, section := range []struct {
				title string
				steps []string
				box   bool
			}{{"Before cutover", job.Before, true}, {"Test failover", job.Test, true}, {"Cutover", job.Cutover, true}, {"Verify", job.Verify, true}, {"Roll back or fail back", job.Back, false}} {
				d.heading(3, section.title)
				d.checklist(section.steps, section.box)
			}
		}
	}
	docxPath := filepath.Join(folder, replRunbookDocx)
	if err := d.save(docxPath, "Replication runbook"); err != nil {
		return "", "", err
	}
	return htmlPath, docxPath, nil
}
