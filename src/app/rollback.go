package main

// Roll back a cutover (2026-11-27, owner: "a built in roll back option
// should exist in the app for every migration method, or at least clear
// instructions associated with a rollback selection"; "as long as the old
// VMs are fully functional to be restored/reinstated at that point").
//
// Every cutover that creates a VM (Move VMs to this host or to a remote
// Hyper-V host or cluster, Migration plans, New migration, a platform move to
// HyperCore, Nutanix AHV or Proxmox VE, and a production VM started from
// agent replication copies) leaves a record here. Roll back then:
//
//  1. shuts the new VM down (and turns it off only when the operator allowed
//     it), disconnects its network adapters and stops it starting with the
//     host, so it can never come back on the network beside the old one;
//  2. reads the new VM again and goes on only when it is off;
//  3. puts the old VM's network adapters back exactly as the record saved
//     them before the cutover disconnected them;
//  4. starts the old VM (VMware or the source Hyper-V host), or for an agent
//     job asks the agent to start the services the cutover stopped.
//
// Nothing is deleted: the new VM, its disks and every copy stay for review.
// The cutover never changed the old VM otherwise (the guest is shut down
// through VMware Tools or the Hyper-V shutdown component, our snapshot or
// checkpoint is removed, the IP settings, VMware Tools removal and other
// fix-ups happen only in the new VM, and no VM is renamed or annotated), so
// it comes back as it was at the cutover. The rules live here, untagged, so
// they are tested on any platform through rbOps fakes.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	rbMethodMove     = "move"     // Move VMs, a migration plan or New migration, to this Hyper-V host
	rbMethodHVRemote = "hvremote" // the same, to a remote Hyper-V host or cluster node
	rbMethodPlatform = "platform" // HyperCore, Nutanix AHV or Proxmox VE
	rbMethodCopies   = "copies"   // a production VM started from agent replication copies

	rbStateReady   = "ready"
	rbStateRunning = "running"
	rbStateDone    = "done"
	rbStatePartial = "partial"
	rbStateFailed  = "failed"

	rbStepOK      = "ok"
	rbStepFailed  = "failed"
	rbStepSkipped = "skipped"
	rbStepManual  = "manual"

	// rbShutdownWait is how long the new VM gets to shut down before it is
	// turned off (when the operator allowed that) or the rollback stops.
	rbShutdownWait = 10 * time.Minute
)

var rbGUIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// rbSource is the original machine the rollback brings back.
type rbSource struct {
	// Kind is vmware, hyperv or agent.
	Kind string `json:"kind"`
	// Server is the vCenter or ESXi server (vmware) or the Hyper-V host the
	// inventory was read from (hyperv).
	Server     string `json:"server,omitempty"`
	VMID       string `json:"vmId,omitempty"`
	Name       string `json:"name"`
	Host       string `json:"host,omitempty"`
	Datacenter string `json:"datacenter,omitempty"`
	// AdapterRecord is the JSON file the cutover saved before it set the old
	// VM's adapters to not connect; empty when it did not.
	AdapterRecord string `json:"adapterRecord,omitempty"`
	// Agent and Job name an agent replication job (kind agent).
	Agent    string   `json:"agent,omitempty"`
	Job      string   `json:"job,omitempty"`
	Services []string `json:"services,omitempty"`
}

// rbNew is the VM the cutover created.
type rbNew struct {
	// Kind is hyperv or platform.
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// Host is the Hyper-V node for a remote host ("" for this computer).
	Host string `json:"host,omitempty"`
	// Platform is scale, nutanix or proxmox and PlatformHost its address.
	Platform     string `json:"platform,omitempty"`
	PlatformHost string `json:"platformHost,omitempty"`
	// Adapters are the new VM's adapters and the switch each was on, saved
	// when a rollback disconnected them.
	Adapters []rbAdapter `json:"adapters,omitempty"`
	// StartAction is the new VM's automatic start action before a rollback
	// set it to Nothing.
	StartAction string `json:"startAction,omitempty"`
}

type rbAdapter struct {
	Name   string `json:"name"`
	Switch string `json:"switch"`
}

type rbStep struct {
	Key     string    `json:"key"`
	Title   string    `json:"title"`
	State   string    `json:"state,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// rbRecord is one cutover that can be rolled back.
type rbRecord struct {
	ID        string    `json:"id"`
	Method    string    `json:"method"`
	Name      string    `json:"name"`
	Plan      string    `json:"plan,omitempty"`
	CutoverAt time.Time `json:"cutoverAt"`
	Source    rbSource  `json:"source"`
	New       rbNew     `json:"new"`
	// DC is set when the old machine is, or is named like, a domain
	// controller; DCWhy says how that was found.
	DC    bool   `json:"dc"`
	DCWhy string `json:"dcWhy,omitempty"`
	// RollingHost is the source host a rolling conversion rebuilds; once
	// it is rebuilt the old VM may be gone.
	RollingHost string `json:"rollingHost,omitempty"`
	// Media are CD/DVD image files ejected before the copy; they stay
	// ejected (a missing image would stop the old VM starting).
	Media    []string  `json:"media,omitempty"`
	State    string    `json:"state"`
	Message  string    `json:"message,omitempty"`
	Steps    []rbStep  `json:"steps,omitempty"`
	Manual   []string  `json:"manual,omitempty"`
	Started  time.Time `json:"started,omitempty"`
	Finished time.Time `json:"finished,omitempty"`
	LogPath  string    `json:"logPath,omitempty"`
}

// rbOptions are the operator's choices for one rollback.
type rbOptions struct {
	// TurnOff turns the new VM off when it has not shut down within
	// rbShutdownWait. Data not yet written to its disks is lost, but it is
	// lost anyway: the rollback leaves the new VM.
	TurnOff bool `json:"turnOff"`
	// Reconnect puts the old VM's adapters back as recorded.
	Reconnect bool `json:"reconnect"`
	// Start starts the old VM (or its stopped services) at the end.
	Start bool `json:"start"`
	// DCConfirmed is the domain controller tick box.
	DCConfirmed bool `json:"dcConfirmed"`
}

// rbOps are the platform steps. Each must change only the machine its record
// names, after checking its ID and name.
type rbOps interface {
	// StopNew shuts the new VM down (turning it off when turnOff and it does
	// not shut down within rbShutdownWait), disconnects its adapters and
	// stops it starting with the host. It returns the adapters it
	// disconnected.
	StopNew(ctx context.Context, rec *rbRecord, turnOff bool, log func(string)) ([]rbAdapter, error)
	// NewState reads the new VM's power state again: off, running or other.
	NewState(ctx context.Context, rec *rbRecord) (string, error)
	// ReconnectSource puts the old VM's adapters back from the record and
	// returns how many it changed.
	ReconnectSource(ctx context.Context, rec *rbRecord, log func(string)) (int, error)
	// StartSource starts the old VM.
	StartSource(ctx context.Context, rec *rbRecord, log func(string)) error
	// RollbackAgent asks an agent to start the services the cutover stopped
	// and to pause its job. errRBAgentUnreachable means the server is off or
	// the agent is too old.
	RollbackAgent(ctx context.Context, rec *rbRecord, log func(string)) error
}

var errRBAgentUnreachable = errors.New("the agent did not answer")

// rbNewRecord fills a record's ID, state and domain controller check.
func rbNewRecord(method string, source rbSource, created rbNew, vm *vmMachine) rbRecord {
	raw := make([]byte, 8)
	rand.Read(raw)
	rec := rbRecord{ID: hex.EncodeToString(raw), Method: method, Name: vmNonEmpty(source.Name, created.Name), CutoverAt: time.Now().UTC(), Source: source, New: created, State: rbStateReady}
	if vm != nil {
		rec.DC, rec.DCWhy = rbDomainController(*vm)
		rec.Media = append([]string(nil), vm.MediaImages...)
	} else {
		rec.DC, rec.DCWhy = rbDomainControllerName(source.Name, true)
	}
	return rec
}

// rbDomainController says whether a VM is a domain controller: a check that
// read it wins, otherwise its name (the power state does not matter here,
// unlike vmWaveRoleOf: after a cutover the old VM is always off).
func rbDomainController(vm vmMachine) (bool, string) {
	if vm.Preflight.domainController() {
		return true, "The SQL Server and domain controller check found it is a domain controller."
	}
	windows := strings.Contains(strings.ToLower(vm.GuestOS+" "+vm.GuestOSRunning+" "+vm.GuestID), "win")
	return rbDomainControllerName(vm.Name, windows)
}

func rbDomainControllerName(name string, windows bool) (bool, string) {
	if windows && vmWaveDCPattern.MatchString(strings.ToLower(name)) {
		return true, "It is named like a domain controller."
	}
	return false, ""
}

// rbDCWarning is shown, with a tick box, before a domain controller is
// rolled back.
const rbDCWarning = "This is a domain controller. Starting the old copy after the new one has run on the network can cause USN rollback: the other domain controllers ignore its changes and replication between them breaks for good. " +
	"Only roll back if the new domain controller never replicated with the others (it was never started, or only isolated), or if you will remove the new one's metadata and repair the domain right away. " +
	"Usually it is safer to keep the new domain controller and fix it, or to demote it and add a fresh domain controller."

// rbDataWarning is shown before every rollback.
func rbDataWarning(rec rbRecord, now time.Time) string {
	return fmt.Sprintf("Anything written on the new %s since the cutover %s (%s ago) is not on the old one. Users' changes, mail, database rows and files saved there since then are left on the new VM's disks and must be copied back by hand.", vmNonEmpty(rec.New.Name, rec.Name), rec.CutoverAt.UTC().Format("2006-01-02 15:04 UTC"), rbAge(now.Sub(rec.CutoverAt)))
}

func rbAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return vmPluralWord(int(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return vmPluralWord(int(d/time.Hour), "hour")
	}
	return vmPluralWord(int(d/(24*time.Hour)), "day")
}

// rbWindow says how long the rollback stays possible and what ends it.
func rbWindow(rec rbRecord) string {
	switch rec.Source.Kind {
	case "vmware":
		text := "Roll back works as long as the old VM stays in " + vmNonEmpty(rec.Source.Server, "vCenter") + " with its disks: it is off and unchanged. It ends when the old VM is deleted, removed from the inventory, or its datastore or host is reused."
		if rec.RollingHost != "" {
			text += " This plan rebuilds " + rec.RollingHost + ", the old VM's host: once that host is removed from " + vmNonEmpty(rec.Source.Server, "vCenter") + " or rebuilt, or its storage is reused, the old VM is gone and only the steps for a rolling conversion are left."
		}
		return text
	case "hyperv":
		return "Roll back works as long as the old VM stays on " + vmNonEmpty(rec.Source.Server, "the source Hyper-V host") + ", off and unchanged. It ends when the old VM or its disks are deleted, or the host is rebuilt."
	case "agent":
		return "Roll back works as long as the source server is kept: it was only stopped (or shut down) at the cutover. It ends when the server is rebuilt, wiped or retired."
	}
	return ""
}

// rbPlanSteps are the steps a rollback takes, in order, for the dialog.
func rbPlanSteps(rec rbRecord, options rbOptions) []rbStep {
	steps := []rbStep{}
	where := "on this Hyper-V host"
	switch rec.New.Kind {
	case "platform":
		where = "on " + vmTargetLabel(rec.New.Platform)
	default:
		if rec.New.Host != "" {
			where = "on " + rec.New.Host
		}
	}
	stop := "Shut the new VM " + vmNonEmpty(rec.New.Name, rec.Name) + " down " + where
	if options.TurnOff {
		stop += ", and turn it off if it has not shut down in " + rbAge(rbShutdownWait)
	}
	if rec.New.Kind == "platform" {
		stop = "Turn the new VM " + vmNonEmpty(rec.New.Name, rec.Name) + " off " + where
	} else {
		stop += "; disconnect its network adapters and stop it starting with the host. It is not deleted."
	}
	steps = append(steps, rbStep{Key: "stop", Title: stop}, rbStep{Key: "check", Title: "Check the new VM is off before anything else happens, so its addresses are never used twice"})
	switch rec.Source.Kind {
	case "vmware", "hyperv":
		if rec.Source.Kind == "vmware" && options.Reconnect {
			title := "Put the old VM's network adapters back as they were before the cutover"
			if rec.Source.AdapterRecord == "" {
				title = "Check the old VM's network adapters (the cutover did not disconnect them)"
			}
			steps = append(steps, rbStep{Key: "network", Title: title})
		}
		if options.Start {
			steps = append(steps, rbStep{Key: "start", Title: "Start the old VM " + rec.Source.Name + " on " + vmNonEmpty(rec.Source.Server, "its host")})
		}
	case "agent":
		if options.Start {
			title := "Ask the agent on " + rec.Source.Name + " to pause its job"
			if len(rec.Source.Services) > 0 {
				title = "Ask the agent on " + rec.Source.Name + " to start " + strings.Join(rec.Source.Services, ", ") + " again and pause its job"
			}
			steps = append(steps, rbStep{Key: "agent", Title: title})
		}
	}
	return steps
}

// rbCheck says why a rollback cannot start, or "".
func rbCheck(rec rbRecord, options rbOptions) string {
	switch rec.State {
	case rbStateRunning:
		return "This rollback is already running."
	case rbStateDone:
		return rec.Name + " was already rolled back."
	}
	if rec.DC && !options.DCConfirmed {
		return "Tick the box to confirm you understand the domain controller warning."
	}
	if rec.New.ID == "" {
		return "The new VM's ID was not recorded, so it cannot be found safely. Follow the steps to roll back by hand."
	}
	if rec.New.Kind == "hyperv" && !rbGUIDPattern.MatchString(rec.New.ID) {
		return "The new VM's ID is not a Hyper-V VM ID."
	}
	if (rec.Source.Kind == "vmware" || rec.Source.Kind == "hyperv") && rec.Source.VMID == "" {
		return "The old VM's ID was not recorded. Follow the steps to roll back by hand."
	}
	return ""
}

// rbRun rolls one record back. It never starts the old machine unless the
// new one was read back as off. It returns the record as it ended.
func rbRun(ctx context.Context, rec rbRecord, options rbOptions, ops rbOps, log func(string)) rbRecord {
	if log == nil {
		log = func(string) {}
	}
	rec.State, rec.Started, rec.Finished, rec.Message, rec.Manual = rbStateRunning, time.Now().UTC(), time.Time{}, "", nil
	rec.Steps = rbPlanSteps(rec, options)
	index := func(key string) int {
		for i := range rec.Steps {
			if rec.Steps[i].Key == key {
				return i
			}
		}
		return -1
	}
	mark := func(key, state, message string) {
		if i := index(key); i >= 0 {
			rec.Steps[i].State, rec.Steps[i].Message, rec.Steps[i].At = state, message, time.Now().UTC()
		}
		if message != "" {
			log(message)
		}
	}
	finish := func(state, message string) rbRecord {
		for i := range rec.Steps {
			if rec.Steps[i].State == "" {
				rec.Steps[i].State = rbStepSkipped
			}
		}
		rec.State, rec.Message, rec.Finished = state, message, time.Now().UTC()
		log(message)
		return rec
	}
	log(fmt.Sprintf("Rolling %s back (cut over %s).", rec.Name, rec.CutoverAt.UTC().Format("2006-01-02 15:04:05 UTC")))
	adapters, err := ops.StopNew(ctx, &rec, options.TurnOff, log)
	if len(adapters) > 0 {
		rec.New.Adapters = adapters
	}
	if err != nil {
		mark("stop", rbStepFailed, "The new VM was not turned off: "+err.Error()+".")
		return finish(rbStateFailed, "Nothing was rolled back: the old VM was not touched because the new one is not off. "+rbStopHint(rec, options))
	}
	text := "The new VM is shut down."
	if len(adapters) > 0 {
		text += fmt.Sprintf(" Its %s disconnected and it no longer starts with the host.", vmPluralWord(len(adapters), "network adapter")+map[bool]string{true: " is", false: " are"}[len(adapters) == 1])
	}
	mark("stop", rbStepOK, text)
	state, err := ops.NewState(ctx, &rec)
	if err != nil || state != "off" {
		why := state
		if err != nil {
			why = err.Error()
		}
		mark("check", rbStepFailed, "The new VM could not be confirmed off ("+why+").")
		return finish(rbStateFailed, "The old VM was not started, because the new VM could not be confirmed off. Turn the new VM off, then roll back again.")
	}
	mark("check", rbStepOK, "The new VM is off.")
	if ctx.Err() != nil {
		return finish(rbStateFailed, "The rollback was stopped after the new VM was turned off. Roll back again to finish.")
	}
	switch rec.Source.Kind {
	case "vmware", "hyperv":
		if index("network") >= 0 {
			changed, err := ops.ReconnectSource(ctx, &rec, log)
			if err != nil {
				mark("network", rbStepFailed, "The old VM's network adapters were not put back: "+err.Error()+".")
				return finish(rbStatePartial, "The new VM is off, but the old VM was not started because its network could not be put back. Set its adapters to Connect at power on in vSphere (the record is "+vmNonEmpty(rec.Source.AdapterRecord, "not available")+"), then start it.")
			}
			text := "The old VM's network adapters are as they were before the cutover."
			if changed > 0 {
				text = fmt.Sprintf("Put %s of the old VM back as they were before the cutover.", vmPluralWord(changed, "network adapter"))
			}
			mark("network", rbStepOK, text)
		}
		if index("start") >= 0 {
			if err := ops.StartSource(ctx, &rec, log); err != nil {
				mark("start", rbStepFailed, "The old VM did not start: "+err.Error()+".")
				rec.Manual = rbStartHelp(rec)
				return finish(rbStatePartial, "The new VM is off, but the old VM did not start. Follow the steps below.")
			}
			mark("start", rbStepOK, "The old VM "+rec.Source.Name+" is starting with its own disks, addresses and network.")
		}
	case "agent":
		if index("agent") >= 0 {
			err := ops.RollbackAgent(ctx, &rec, log)
			switch {
			case errors.Is(err, errRBAgentUnreachable):
				mark("agent", rbStepManual, "The agent on "+rec.Source.Name+" did not answer.")
				rec.Manual = rbAgentManual(rec)
				return finish(rbStatePartial, "The new VM is off. "+rec.Source.Name+" did not answer (it is probably shut down): follow the steps below to bring it back.")
			case err != nil:
				mark("agent", rbStepFailed, "The agent on "+rec.Source.Name+" did not roll its job back: "+err.Error()+".")
				rec.Manual = rbAgentManual(rec)
				return finish(rbStatePartial, "The new VM is off, but the job was not rolled back on "+rec.Source.Name+". Follow the steps below.")
			}
			mark("agent", rbStepOK, rec.Source.Name+" runs its services again; its job is paused.")
		}
	}
	if rec.Source.Kind != "vmware" && rec.Source.Kind != "hyperv" && rec.Source.Kind != "agent" {
		rec.Manual = []string{
			"Make sure the new VM stays off (it is disconnected and does not start with the host).",
			"Turn the original server " + rec.Source.Name + " on where it was (its hypervisor, its iLO/iDRAC or its power button) and check it answers on its usual address.",
		}
		return finish(rbStatePartial, "The new VM is off. This application does not know where the original server runs: turn it on by hand.")
	}
	return finish(rbStateDone, rec.Name+" is rolled back: the new VM is off and disconnected, and the old one is back. "+rbAfterHelp(rec))
}

func rbStopHint(rec rbRecord, options rbOptions) string {
	if !options.TurnOff {
		return "Shut it down from inside, or roll back again with Turn it off if it does not shut down ticked."
	}
	if rec.New.Kind == "hyperv" && rec.New.Host != "" {
		return "If it moved to another node of the cluster, turn it off there (Failover Cluster Manager), then roll back again."
	}
	return "Turn it off where it runs, then roll back again."
}

// rbStartHelp is what to do when the old VM did not start.
func rbStartHelp(rec rbRecord) []string {
	switch rec.Source.Kind {
	case "vmware":
		steps := []string{
			"Open " + vmNonEmpty(rec.Source.Server, "vCenter") + " in the vSphere Client and find " + rec.Source.Name + ".",
			"If vSphere asks whether the VM was moved or copied, answer I moved it (its files never moved).",
			"Edit its settings: each network adapter should have Connect at power on ticked, on the same port group as before the cutover.",
			"Power it on and check it answers on its usual address.",
		}
		if rec.RollingHost != "" {
			steps = append([]string{"If " + rec.RollingHost + " was already rebuilt, the old VM no longer exists: see Roll back after a rolling conversion host was rebuilt."}, steps...)
		}
		return steps
	case "hyperv":
		return []string{
			"Open Hyper-V Manager (or Failover Cluster Manager) for " + vmNonEmpty(rec.Source.Server, "the source host") + ".",
			"Find " + rec.Source.Name + " and check its network adapters are on the same switch as before.",
			"Start it and check it answers on its usual address.",
		}
	}
	return nil
}

// rbAgentManual is what to do when the source server's agent does not
// answer.
func rbAgentManual(rec rbRecord) []string {
	steps := []string{
		"Make sure the new VM stays off (it is disconnected and does not start with the host).",
		"Turn " + rec.Source.Name + " on (from its hypervisor, its iLO/iDRAC or its power button) if the cutover shut it down.",
	}
	if len(rec.Source.Services) > 0 {
		steps = append(steps, "On "+rec.Source.Name+", start "+strings.Join(rec.Source.Services, ", ")+" if they are not running (they start by themselves after a restart when set to Automatic).")
	}
	steps = append(steps, "In Replicate servers, the job shows Cut over. Leave it, or delete it, and create a new job with Rescan the receiver's disks first ticked when you migrate again.",
		"Click Roll back again once the server answers: it pauses the job and starts the services for you.")
	return steps
}

// rbAfterHelp says what comes next after a rollback.
func rbAfterHelp(rec rbRecord) string {
	switch rec.Method {
	case rbMethodCopies:
		return "To migrate again later, remove the new VM in Hyper-V Manager (its disks are the copies), then Resume the job in Replicate servers: its first round rescans the copies, so only what differs is sent."
	case rbMethodPlatform:
		return "The new VM stays on " + vmTargetLabel(rec.New.Platform) + " for review; delete it there when you no longer need it."
	}
	return "The new VM stays for review; delete it in Hyper-V Manager when you no longer need it. To migrate again, delete it, then copy and cut over again in Move VMs: the copies are kept, so the next copy sends only what changed."
}

// rbGuide is the rollback for one way of moving, for the Roll back tool's
// list: built in, or the steps to follow by hand.
type rbGuide struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Automatic string   `json:"automatic"` // yes, partly or no
	Covers    string   `json:"covers"`
	Steps     []string `json:"steps"`
	Warnings  []string `json:"warnings"`
}

// rbGuides lists every way of moving with its rollback. Keep it true to the
// code: "yes" only where Roll back does every step.
func rbGuides() []rbGuide {
	dataLoss := "Anything written on the new machine since the cutover is not on the old one."
	dc := "Domain controllers: roll back only if the new one never replicated with the others, or USN rollback breaks replication. Prefer fixing or demoting the new one."
	return []rbGuide{
		{ID: "movevms", Title: "Move VMs, Migration plans and New migration (VMware or Hyper-V to Hyper-V)", Automatic: "yes",
			Covers: "VMware VMs from vCenter Server or ESXi and Hyper-V VMs moved to this Hyper-V host, or to a remote Hyper-V host or cluster from an admin computer.",
			Steps: []string{
				"Use Roll back beside the VM below, in the migration plan or in Move VMs. It shuts the new VM down, disconnects it and stops it starting with the host, checks it is off, puts the old VM's network adapters back as they were and starts the old VM.",
				"By hand: in Hyper-V Manager shut the new VM down, set each network adapter to Not connected and Automatic start action to Nothing.",
				"In the vSphere Client edit the old VM: tick Connect at power on for each adapter (the cutover saved how they were in LocalAppData\\Server Inventory Configuration Manager\\VMware\\<server>\\Move\\*-adapters.json). Then power it on.",
				"For a Hyper-V source, start the old VM in Hyper-V Manager on its source host.",
			}, Warnings: []string{dataLoss, dc}},
		{ID: "platform", Title: "Move VMs to Scale HyperCore, Nutanix AHV or Proxmox VE", Automatic: "yes",
			Covers: "VMs cut over through the Move panel while this application is connected to the platform.",
			Steps: []string{
				"Connect to the platform in Move VMs, then use Roll back. It turns the new VM off on the platform, checks it is off and starts the old VM in vCenter.",
				"By hand: turn the new VM off on the platform and disconnect its adapters there; then in vCenter tick Connect at power on for the old VM's adapters and power it on.",
			}, Warnings: []string{dataLoss, dc}},
		{ID: "replicate", Title: "Replicate servers (agent) and Start VMs from copies", Automatic: "partly",
			Covers: "Windows and Linux servers, physical or virtual, whose production VM was started from the copies on this Hyper-V host.",
			Steps: []string{
				"Use Roll back beside the server below. It shuts the production VM down and disconnects it, checks it is off, then asks the agent to start the services the cutover stopped and to pause the job.",
				"If the cutover shut the source server down, its agent cannot answer: turn the server on (hypervisor, iLO/iDRAC or power button), then press Roll back again to finish.",
				"To migrate again, remove the new VM in Hyper-V Manager (its disks are the copies) and Resume the job: its first round rescans, so only what differs is sent.",
			}, Warnings: []string{dataLoss, dc}},
		{ID: "rolling", Title: "Rolling conversion after a host was rebuilt (same hardware)", Automatic: "no",
			Covers: "VMs whose original lived on an ESXi host, its local datastore or a reused LUN that was rebuilt or formatted for Hyper-V.",
			Steps: []string{
				"Before each host is rebuilt, the plan's stage lists the VMs whose rollback ends there. Test every VM already moved and roll back the ones that do not work while the host still exists.",
				"Once the host is rebuilt or its LUN reused, the old VM's files are gone: Roll back cannot bring it back.",
				"Recover from backup to a remaining ESXi host, or keep the new VM and fix it (Prepare and fix up, or the Hyper-V console).",
				"While the host is still in vCenter but in maintenance mode, the old VMs can still be registered and powered on from a remaining host if they are on shared storage: browse the datastore, right-click the .vmx file and Register VM.",
			}, Warnings: []string{"Rebuilding a host erases its local datastores; presenting a reused LUN to Hyper-V formats it."}},
		{ID: "p2v", Title: "Physical to virtual (agent replication, Send by hand or Convert disk images)", Automatic: "no",
			Covers: "A physical server copied to a VM. Replicate servers is covered above; these steps are for Send by hand and Convert disk images.",
			Steps: []string{
				"Shut the new VM down and set its network adapters to Not connected (Hyper-V Manager or the platform's console).",
				"Turn the physical server on and check it answers on its usual address. Nothing on it was changed by the copy.",
				"Check it is still joined to the domain (Test-ComputerSecureChannel). If the new VM ran on the network for days, reset the secure channel (Test-ComputerSecureChannel -Repair).",
			}, Warnings: []string{dataLoss, dc}},
		{ID: "v2p", Title: "Virtual to physical (receiver media on the new server)", Automatic: "no",
			Covers: "A VM copied onto a physical server's disks with the boot-media receiver.",
			Steps: []string{
				"Shut the new physical server down and unplug or disable its network.",
				"Start the old VM in vCenter or Hyper-V (tick Connect at power on for its adapters if they were disconnected).",
				"Keep the physical server off the network until it is rebuilt or copied again.",
			}, Warnings: []string{dataLoss}},
		{ID: "convert", Title: "Convert disk images and Send to a host", Automatic: "no",
			Covers: "Disks converted from files of a VM that was shut down, and VMs created from them on another host.",
			Steps: []string{
				"Shut the new VM down and disconnect its network adapters on its host.",
				"Start the original VM where it was: the conversion only read its disk files, so it is unchanged.",
				"If you ran Fix up on the converted disk, that only changed the new image; Undo is in Convert disk images.",
			}, Warnings: []string{dataLoss}},
		{ID: "shares", Title: "Move file shares", Automatic: "partly",
			Covers: "Shares, files and permissions moved to a new file server.",
			Steps: []string{
				"In Move file shares, use Undo read-only on the old server: it puts back the share and NTFS permissions saved before Make the old server read-only.",
				"Point users back to the old server's shares (logon scripts, mapped drives, DFS targets). If the old name was added to the new server as an alias, remove it there (netdom computername <new> /remove:<old name>) and restart it.",
				"Copy files changed on the new server since the cutover back to the old one (robocopy with /XO so newer files on the old side are kept).",
			}, Warnings: []string{"The new shares stay; remove them on the new server when no longer needed."}},
		{ID: "restore", Title: "Restore (configuration restore onto a server)", Automatic: "no",
			Covers: "Configuration restored from a collection package.",
			Steps: []string{
				"Before each item was changed, Restore saved that item's configuration in %LOCALAPPDATA%\\Server Inventory Configuration Manager\\Restore-Rollback on the restored server.",
				"Restore the saved files from that folder: registry .reg files with reg import, DHCP with Import-DhcpServer, DNS zones from their files, NPS with Import-NpsConfiguration.",
				"The NPS rollback holds shared secrets: delete it once you no longer need it.",
			}, Warnings: []string{"Items restored after the one that failed were not changed."}},
	}
}

// rbStore keeps the records in one JSON file under LocalAppData Rollback.
type rbStore struct {
	mu   sync.Mutex
	path string
}

const rbStoreLimit = 500

func (s *rbStore) load() ([]rbRecord, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []rbRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	list := []rbRecord{}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return list, nil
}

func (s *rbStore) save(list []rbRecord) error {
	sort.SliceStable(list, func(i, j int) bool { return list[i].CutoverAt.After(list[j].CutoverAt) })
	if len(list) > rbStoreLimit {
		list = list[:rbStoreLimit]
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(s.path+".tmp", s.path)
}

// List returns the records, newest first. A record left running by a
// previous run of the application is shown as failed.
func (s *rbStore) List() ([]rbRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Add saves a new record.
func (s *rbStore) Add(rec rbRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	return s.save(append([]rbRecord{rec}, list...))
}

// Update changes one record through fn.
func (s *rbStore) Update(id string, fn func(*rbRecord) error) (rbRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return rbRecord{}, err
	}
	for i := range list {
		if list[i].ID == id {
			if err := fn(&list[i]); err != nil {
				return list[i], err
			}
			return list[i], s.save(list)
		}
	}
	return rbRecord{}, errors.New("that cutover is no longer in the list")
}

// RecoverInterrupted marks rollbacks a previous run left running as failed.
func (s *rbStore) RecoverInterrupted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return
	}
	changed := false
	for i := range list {
		if list[i].State == rbStateRunning {
			list[i].State, list[i].Finished = rbStateFailed, time.Now().UTC()
			list[i].Message = "The application closed while this rollback ran. Check the new VM is off, then roll back again."
			changed = true
		}
	}
	if changed {
		s.save(list)
	}
}

// rbLatestFor is the newest record for a VM or server name (any state).
func rbLatestFor(list []rbRecord, name string) *rbRecord {
	for i := range list {
		if strings.EqualFold(list[i].Name, name) || strings.EqualFold(list[i].Source.Name, name) {
			return &list[i]
		}
	}
	return nil
}

// rbRollingHostFor names the source host a rolling plan rebuilds when the VM
// lives on it.
func rbRollingHostFor(plan *vmPlan, vm vmMachine) string {
	if plan == nil || vmPlanScenario(*plan) != vmScenarioRolling || plan.Rolling == nil || vm.Host == "" {
		return ""
	}
	for _, stage := range plan.Rolling.Stages {
		if strings.EqualFold(stage.Host, vm.Host) {
			return stage.Host
		}
	}
	return ""
}

// rbHyperVShutdownService is the Hyper-V shutdown integration service's ID.
const rbHyperVShutdownService = "9F8233AC-BE49-4C79-8EE3-E7E1985B2077"

// rbHyperVStopScript shuts the new Hyper-V VM down for a rollback: by its ID
// and name only, through the guest's shutdown service, turned off only when
// turnOff; then its adapters are disconnected and its automatic start action
// set to Nothing, so it never comes back beside the old VM. It never removes
// anything. Reports go through Write-Output so they also come back over
// PowerShell remoting.
func rbHyperVStopScript(id, name string, turnOff bool) string {
	var s strings.Builder
	line := func(format string, args ...interface{}) { fmt.Fprintf(&s, format+"\n", args...) }
	line("$ErrorActionPreference = 'Stop'")
	line("Import-Module Hyper-V")
	line("$vm = Get-VM -Id %s -ErrorAction SilentlyContinue", vmPSQuote(id))
	line("if (-not $vm) { throw ('The new VM is not on ' + $env:COMPUTERNAME + ': it was removed, or it runs on another node of the cluster. Turn it off where it runs, then roll back again.') }")
	line("if ($vm.Name -ne %s) { throw ('The VM with this ID is now named ' + $vm.Name + '; it was not changed.') }", vmPSQuote(name))
	line("foreach ($adapter in @(Get-VMNetworkAdapter -VM $vm)) { Write-Output ('RB-ADAPTER' + [char]9 + $adapter.Name + [char]9 + [string]$adapter.SwitchName) }")
	line("if ([string]$vm.AutomaticStartAction -ne 'Nothing') { Write-Output ('RB-STARTACTION ' + [string]$vm.AutomaticStartAction); Set-VM -VM $vm -AutomaticStartAction Nothing }")
	line("if ($vm.State -eq 'Running') {")
	line("  $shutdown = @(Get-VMIntegrationService -VM $vm | Where-Object { [string]$_.Id -like '*%s*' -and $_.Enabled })", rbHyperVShutdownService)
	line("  if ($shutdown.Count -gt 0) {")
	line("    Write-Output 'Asking Windows in the new VM to shut down.'")
	line("    try { Stop-VM -VM $vm -Force -AsJob | Out-Null } catch { Write-Output ('The guest did not take the shutdown request: ' + $_.Exception.Message) }")
	line("    $deadline = (Get-Date).AddSeconds(%d)", int(rbShutdownWait/time.Second))
	line("    while ((Get-Date) -lt $deadline -and (Get-VM -Id $vm.Id).State -ne 'Off') { Start-Sleep -Seconds 5 }")
	line("  } else { Write-Output 'The new VM has no shutdown integration service.' }")
	line("}")
	line("if ((Get-VM -Id $vm.Id).State -ne 'Off') {")
	if turnOff {
		line("  Write-Output 'Turning the new VM off.'")
		line("  Stop-VM -VM $vm -TurnOff -Force")
	} else {
		line("  throw ('The new VM did not shut down within ten minutes (it is ' + [string](Get-VM -Id $vm.Id).State + ')')")
	}
	line("}")
	line("Get-VMNetworkAdapter -VM $vm | Disconnect-VMNetworkAdapter")
	line("Write-Output ('RB-STATE ' + [string](Get-VM -Id $vm.Id).State)")
	return s.String()
}

// rbHyperVStateScript reads the new VM's state again, by ID and name.
func rbHyperVStateScript(id, name string) string {
	return "$ErrorActionPreference = 'Stop'\nImport-Module Hyper-V\n" +
		"$vm = Get-VM -Id " + vmPSQuote(id) + " -ErrorAction SilentlyContinue\n" +
		"if (-not $vm) { throw 'The new VM is not on this host any more.' }\n" +
		"if ($vm.Name -ne " + vmPSQuote(name) + ") { throw ('The VM with this ID is now named ' + $vm.Name) }\n" +
		"Write-Output ('RB-STATE ' + [string]$vm.State)\n"
}

// rbParseHyperV reads the RB- lines of the scripts above.
func rbParseHyperV(lines []string) (adapters []rbAdapter, startAction, state string) {
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "RB-ADAPTER\t"):
			parts := strings.SplitN(strings.TrimPrefix(line, "RB-ADAPTER\t"), "\t", 2)
			adapter := rbAdapter{Name: parts[0]}
			if len(parts) == 2 {
				adapter.Switch = parts[1]
			}
			adapters = append(adapters, adapter)
		case strings.HasPrefix(line, "RB-STARTACTION "):
			startAction = strings.TrimSpace(strings.TrimPrefix(line, "RB-STARTACTION "))
		case strings.HasPrefix(line, "RB-STATE "):
			state = rbPowerWord(strings.TrimSpace(strings.TrimPrefix(line, "RB-STATE ")))
		}
	}
	return adapters, startAction, state
}

// rbPowerWord maps a Hyper-V or platform state to off, running or other.
func rbPowerWord(state string) string {
	switch strings.ToLower(state) {
	case "off", "stopped", "poweredoff", "shutdown":
		return "off"
	case "running", "poweredon", "started":
		return "running"
	}
	return strings.ToLower(vmNonEmpty(state, "unknown"))
}
