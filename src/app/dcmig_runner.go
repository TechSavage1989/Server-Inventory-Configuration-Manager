package main

// Domain controller plans (dcmig.go): steps, phases, scheduling and roll
// back. Every call that reaches a server goes through dcHost, so go test
// runs every rule with a fake; dcmig_windows.go implements it with the
// signed Invoke-DcMigration.ps1.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dcHost reaches the servers. Mode names match Invoke-DcMigration.ps1.
type dcHost interface {
	// ReadOld reads the old DC (and the domain through it).
	ReadOld(ctx context.Context, p *dcPlan) (*dcSourceInfo, error)
	// ReadNew reads the new server.
	ReadNew(ctx context.Context, p *dcPlan) (*dcTargetInfo, error)
	// Health checks one DC (read only) and returns its findings.
	Health(ctx context.Context, p *dcPlan, dc string) ([]vmFinding, error)
	// Step runs one changing step and returns what it changed or noted.
	Step(ctx context.Context, p *dcPlan, step string) (dcStepResult, error)
	// Rollback puts the recorded changes and roles back.
	Rollback(ctx context.Context, p *dcPlan, req dcRollbackRequest) (dcStepResult, error)
}

// dcStepResult is what a step reports back.
type dcStepResult struct {
	Notes      []string          `json:"notes"`
	Changes    []dcChange        `json:"changes"`
	RolesMoved []string          `json:"rolesMoved"`
	Ready      bool              `json:"ready"`   // verify: the new DC works
	Waiting    string            `json:"waiting"` // verify: what it still waits for
	Restored   []int             `json:"restored"`
	RoleOwners map[string]string `json:"roleOwners"`
}

type dcRollbackRequest struct {
	Changes   []dcChange `json:"changes"`
	Roles     []string   `json:"roles"`
	RemoveNew bool       `json:"removeNew"`
}

type dcRunner struct {
	mu      sync.Mutex
	path    string
	plans   []*dcPlan
	host    dcHost
	now     func() time.Time
	log     func(p *dcPlan, text string)
	ready   func(p *dcPlan, phase string) string
	hold    func() (func(), bool)
	running map[string]string // plan ID: what runs
	cancels map[string]context.CancelFunc
	poll    time.Duration // verify polling
	limit   time.Duration // verify limit
}

func newDCRunner(path string, host dcHost) *dcRunner {
	return &dcRunner{path: path, host: host, now: time.Now, log: func(*dcPlan, string) {}, ready: func(*dcPlan, string) string { return "" },
		running: map[string]string{}, cancels: map[string]context.CancelFunc{}, poll: time.Minute, limit: dcVerifyLimit}
}

func dcNewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (r *dcRunner) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		r.plans = []*dcPlan{}
		return nil
	}
	if err != nil {
		return err
	}
	plans := []*dcPlan{}
	if err := json.Unmarshal(stripUTF8BOMBytes(raw), &plans); err != nil {
		return fmt.Errorf("%s: %w", r.path, err)
	}
	for _, p := range plans {
		for id, s := range p.Steps {
			if s.State == dcRunning {
				s.State, s.Ended = dcFailed, time.Time{}
				s.Message = "The application stopped during this step. Read both servers again to see where it got to, then run it again; every step checks what is already done first."
				p.Steps[id] = s
			}
		}
	}
	r.plans = plans
	return nil
}

// Caller holds r.mu.
func (r *dcRunner) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(r.plans, "", "  ")
	if err != nil {
		return err
	}
	return replWriteFileAtomic(r.path, raw)
}

func (r *dcRunner) planLocked(id string) (*dcPlan, error) {
	for _, p := range r.plans {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, errors.New("that domain controller plan no longer exists")
}

func dcClone(p *dcPlan) dcPlan {
	raw, _ := json.Marshal(p)
	var out dcPlan
	json.Unmarshal(raw, &out)
	return out
}

// View returns copies of every plan.
func (r *dcRunner) View() []dcPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]dcPlan, 0, len(r.plans))
	for _, p := range r.plans {
		out = append(out, dcClone(p))
	}
	return out
}

func (r *dcRunner) Running() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for k, v := range r.running {
		out[k] = v
	}
	return out
}

// Busy is true while any step runs (every step changes a server or reads
// the domain for one that will).
func (r *dcRunner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running) > 0
}

// dcCreateInput is what the page sends to make or change a plan.
type dcCreateInput struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Source   string     `json:"source"`
	Target   string     `json:"target"`
	User     string     `json:"user"`
	Settings dcSettings `json:"settings"`
	Wave     int        `json:"wave"`
}

// Create adds a plan from a read (old and new), or changes the settings of
// an existing one. The settings cannot change once the step that uses them
// has run.
func (r *dcRunner) Create(in dcCreateInput, old *dcSourceInfo, tgt *dcTargetInfo) (*dcPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		return nil, errors.New("give the plan a name of up to 80 characters")
	}
	if err := dcCheckComputer(in.Source, "old domain controller"); err != nil {
		return nil, err
	}
	if err := dcCheckComputer(in.Target, "new server"); err != nil {
		return nil, err
	}
	if in.Wave < 0 || in.Wave > 99 {
		return nil, errors.New("the wave must be between 0 and 99")
	}
	var p *dcPlan
	if in.ID != "" {
		existing, err := r.planLocked(in.ID)
		if err != nil {
			return nil, err
		}
		if _, busy := r.running[existing.ID]; busy {
			return nil, errors.New("a step of this plan is running; change it when the step ends")
		}
		if !dcSameComputer(existing.Source, in.Source) || !dcSameComputer(existing.Target, in.Target) {
			if existing.done(dcStepPromote) || len(existing.RolesMoved) > 0 {
				return nil, errors.New("the servers of a plan cannot change once the new DC is promoted: start a new plan")
			}
		}
		p = existing
	} else {
		for _, other := range r.plans {
			if strings.EqualFold(other.Name, name) {
				return nil, fmt.Errorf("a plan named %q already exists", name)
			}
		}
		if len(r.plans) >= dcMaxPlans {
			return nil, fmt.Errorf("at most %d domain controller plans can be kept; delete one first", dcMaxPlans)
		}
		p = &dcPlan{ID: dcNewID(), Created: r.now(), Steps: map[string]dcStep{}}
	}
	settings := in.Settings
	if p.done(dcStepPromote) {
		// The build is done: keep what it used.
		settings.Site, settings.InstallDNS, settings.GlobalCatalog, settings.ReplicateFrom = p.Settings.Site, p.Settings.InstallDNS, p.Settings.GlobalCatalog, p.Settings.ReplicateFrom
		settings.DatabasePath, settings.LogPath, settings.SysvolPath = p.Settings.DatabasePath, p.Settings.LogPath, p.Settings.SysvolPath
	}
	if len(p.RolesMoved) > 0 {
		settings.MoveRoles, settings.OldDead = p.Settings.MoveRoles, p.Settings.OldDead
	}
	if p.step(dcStepRepoint).State == dcDone {
		settings.IPMode, settings.Repoint = p.Settings.IPMode, p.Settings.Repoint
	}
	p.Name, p.Source, p.Target, p.User, p.Wave = name, strings.TrimSpace(in.Source), strings.TrimSpace(in.Target), strings.TrimSpace(in.User), in.Wave
	if old != nil {
		p.Old = old
	}
	if tgt != nil {
		p.New = tgt
	}
	p.Settings = dcSuggest(settings, p.Old, p.New)
	p.Settings.MoveRoles = dcSortRoles(p.Settings.MoveRoles)
	if err := dcCheckSettings(p); err != nil {
		return nil, err
	}
	if in.ID == "" {
		r.plans = append(r.plans, p)
	}
	for _, s := range dcStepOrder {
		if !dcNeeded(p, s) && p.step(s).State == dcPending {
			p.setStep(s, dcStep{State: dcSkipped})
		} else if dcNeeded(p, s) && p.step(s).State == dcSkipped {
			p.setStep(s, dcStep{State: dcPending})
		}
	}
	if err := r.saveLocked(); err != nil {
		return nil, err
	}
	out := dcClone(p)
	return &out, nil
}

// Delete removes a plan; nothing on any server changes.
func (r *dcRunner) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.running[id]; busy {
		return errors.New("a step of this plan is running")
	}
	for i, p := range r.plans {
		if p.ID == id {
			r.plans = append(r.plans[:i], r.plans[i+1:]...)
			return r.saveLocked()
		}
	}
	return errors.New("that domain controller plan no longer exists")
}

// Schedule sets (or clears with a zero time) when a phase runs by itself.
func (r *dcRunner) Schedule(id, phase string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(id)
	if err != nil {
		return err
	}
	if at.IsZero() {
		p.Schedule = nil
		return r.saveLocked()
	}
	if phase != dcPhaseCutover && phase != dcPhaseRetire {
		return errors.New("only moving the roles and retiring the old DC can be scheduled")
	}
	if at.Before(r.now().Add(-time.Minute)) {
		return errors.New("choose a time in the future")
	}
	if why := dcPhaseBlock(p, phase); why != "" && !strings.HasPrefix(why, "Waits for") {
		return errors.New(why)
	}
	p.Schedule = &dcSchedule{Phase: phase, At: at}
	return r.saveLocked()
}

// dcPhaseBlock says why a phase cannot run now, or "".
func dcPhaseBlock(p *dcPlan, phase string) string {
	if !p.RolledBackAt.IsZero() {
		return "This plan was rolled back; start a new plan to try again."
	}
	steps := dcPhaseSteps[phase]
	if steps == nil {
		return "Unknown phase."
	}
	if phase == dcPhaseCheck {
		if p.Old == nil {
			return "Read both servers first."
		}
		return ""
	}
	all := true
	for _, s := range steps {
		if !p.done(s) {
			all = false
		}
	}
	if all {
		return dcPhaseNames[phase] + " is done."
	}
	switch phase {
	case dcPhaseCheck:
		if p.Old == nil {
			return "Read both servers first."
		}
		return ""
	case dcPhaseBuild:
		if p.Old == nil || p.New == nil {
			return "Read both servers first."
		}
		if err := dcCheckSettings(p); err != nil {
			return dcCap(err)
		}
	case dcPhaseCutover:
		if !p.done(dcStepVerify) {
			return "Waits for the new DC to be built and working."
		}
	case dcPhaseRetire:
		if !p.done(dcStepRoles) || !p.done(dcStepRepoint) {
			return "Waits for the roles to move first."
		}
	}
	return ""
}

// Run starts a phase in the background; it returns at once.
func (r *dcRunner) Run(id, phase string) error {
	r.mu.Lock()
	p, err := r.planLocked(id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if _, busy := r.running[id]; busy {
		r.mu.Unlock()
		return errors.New("a step of this plan is already running")
	}
	if why := dcPhaseBlock(p, phase); why != "" {
		r.mu.Unlock()
		return errors.New(why)
	}
	if why := r.ready(p, phase); why != "" {
		r.mu.Unlock()
		return errors.New(why)
	}
	release := func() {}
	if r.hold != nil && phase != dcPhaseCheck {
		end, ok := r.hold()
		if !ok {
			r.mu.Unlock()
			return errors.New("another step that must finish first is running (or an update is being installed)")
		}
		release = end
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.running[id] = phase
	r.cancels[id] = cancel
	if p.Schedule != nil && p.Schedule.Phase == phase {
		p.Schedule = nil
		r.saveLocked()
	}
	r.mu.Unlock()
	go func() {
		defer release()
		defer cancel()
		r.runPhase(ctx, id, phase)
		r.mu.Lock()
		delete(r.running, id)
		delete(r.cancels, id)
		r.mu.Unlock()
	}()
	return nil
}

// RunHealth checks every DC again (read only), whatever ran before.
func (r *dcRunner) RunHealth(id string) error {
	r.mu.Lock()
	p, err := r.planLocked(id)
	if err == nil {
		if _, busy := r.running[id]; !busy {
			p.setStep(dcStepHealth, dcStep{State: dcPending})
		}
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return r.Run(id, dcPhaseCheck)
}

// Stop asks a running phase to stop after its current call. Promotion and
// demotion are never interrupted part way: Windows finishes them.
func (r *dcRunner) Stop(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel := r.cancels[id]; cancel != nil {
		cancel()
		return nil
	}
	return errors.New("nothing of this plan is running")
}

func (r *dcRunner) setStep(id, step string, mutate func(s *dcStep)) *dcPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(id)
	if err != nil {
		return nil
	}
	s := p.step(step)
	mutate(&s)
	p.setStep(step, s)
	r.saveLocked()
	c := dcClone(p)
	return &c
}

func (r *dcRunner) snapshot(id string) *dcPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(id)
	if err != nil {
		return nil
	}
	c := dcClone(p)
	return &c
}

func (r *dcRunner) runPhase(ctx context.Context, id, phase string) {
	for _, step := range dcPhaseSteps[phase] {
		p := r.snapshot(id)
		if p == nil {
			return
		}
		if p.done(step) || !dcNeeded(p, step) {
			if !dcNeeded(p, step) && p.step(step).State != dcDone {
				r.setStep(id, step, func(s *dcStep) { s.State = dcSkipped })
			}
			continue
		}
		if ctx.Err() != nil {
			r.log(p, "Stopped before "+dcStepNames[step]+".")
			return
		}
		r.setStep(id, step, func(s *dcStep) {
			*s = dcStep{State: dcRunning, Started: r.now()}
		})
		r.log(p, dcStepNames[step]+": started.")
		err := r.runStep(ctx, id, step)
		p = r.snapshot(id)
		if err != nil {
			msg := dcCap(err)
			if ctx.Err() != nil {
				msg = "Stopped. " + msg
			}
			r.setStep(id, step, func(s *dcStep) { s.State, s.Ended, s.Message = dcFailed, r.now(), msg })
			r.log(p, dcStepNames[step]+" failed: "+msg)
			return
		}
		r.setStep(id, step, func(s *dcStep) {
			if s.State == dcRunning {
				s.State = dcDone
			}
			s.Ended = r.now()
		})
		r.log(p, dcStepNames[step]+": done.")
	}
}

func (r *dcRunner) apply(id string, step string, res dcStepResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(id)
	if err != nil {
		return
	}
	p.Changes = append(p.Changes, res.Changes...)
	for _, role := range res.RolesMoved {
		if !dcHasRole(p.RolesMoved, role) {
			p.RolesMoved = append(p.RolesMoved, role)
		}
	}
	if len(res.RoleOwners) > 0 && p.Old != nil {
		p.Old.RoleOwners = res.RoleOwners
	}
	s := p.step(step)
	s.Notes = append(s.Notes, res.Notes...)
	p.setStep(step, s)
	r.saveLocked()
}

func (r *dcRunner) runStep(ctx context.Context, id, step string) error {
	p := r.snapshot(id)
	switch step {
	case dcStepHealth:
		return r.runHealth(ctx, id)
	case dcStepVerify:
		return r.runVerify(ctx, id)
	case dcStepPromote:
		// Read the new server again: promotion is skipped when it is a DC
		// of this domain already (an earlier run finished after the
		// application stopped).
		if tgt, err := r.host.ReadNew(ctx, p); err == nil {
			r.mu.Lock()
			if live, e := r.planLocked(id); e == nil {
				live.New = tgt
				r.saveLocked()
			}
			r.mu.Unlock()
			if tgt.IsDC && strings.EqualFold(tgt.Domain, p.Old.Domain) {
				r.apply(id, step, dcStepResult{Notes: []string{tgt.Name + " is already a domain controller of " + tgt.Domain + "; promotion was not needed."}})
				return nil
			}
		}
		findings := dcCheckPlan(p)
		if dcBlocked(findings) {
			for _, f := range findings {
				if f.Severity == vmSeverityBlocker {
					return errors.New(strings.TrimSuffix(f.Message, ".") + ". " + f.Action)
				}
			}
		}
	case dcStepRoles:
		if p.Settings.OldDead {
			r.log(p, "The old DC is marked as no longer working: the roles are seized, not transferred.")
		}
	case dcStepDemote:
		fresh, err := r.host.ReadOld(ctx, &dcPlan{Source: p.Settings.ReplicateFromForRetire(p), User: p.User, Old: p.Old, New: p.New, Settings: p.Settings})
		if err != nil {
			return fmt.Errorf("the domain could not be read again before demotion: %v", err)
		}
		if problems := dcRetireChecks(p, fresh); len(problems) > 0 {
			return errors.New("the old DC was not demoted because " + strings.Join(problems, "; "))
		}
	}
	res, err := r.host.Step(ctx, p, step)
	r.apply(id, step, res)
	return err
}

// ReplicateFromForRetire picks the DC to read the domain through before
// demotion: the new DC (it should answer now), never only the old one.
func (s dcSettings) ReplicateFromForRetire(p *dcPlan) string {
	if p.New != nil && p.New.HostName != "" {
		return p.New.HostName
	}
	return p.Target
}

func (r *dcRunner) runHealth(ctx context.Context, id string) error {
	p := r.snapshot(id)
	if p.Old == nil {
		return errors.New("read both servers first")
	}
	dcs := []string{}
	for _, peer := range p.Old.DCs {
		name := peer.HostName
		if name == "" {
			name = peer.Name
		}
		dcs = append(dcs, name)
	}
	if len(dcs) == 0 {
		dcs = []string{p.Source}
	}
	results := []dcHealth{}
	for _, dc := range dcs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.log(p, "Checking "+dc+" (read only).")
		findings, err := r.host.Health(ctx, p, dc)
		h := dcHealth{DC: dc, CheckedAt: r.now(), Findings: findings}
		if err != nil {
			h.Error = err.Error()
		}
		results = append(results, h)
	}
	r.mu.Lock()
	live, err := r.planLocked(id)
	if err == nil {
		live.Health = results
		r.saveLocked()
	}
	r.mu.Unlock()
	// The step itself is done once every DC was checked; blockers it found
	// keep Build from going on (dcCheckPlan before promotion).
	blocks := 0
	for _, h := range results {
		if h.Error != "" && dcSameComputer(h.DC, p.Old.Name) {
			blocks++
		}
		for _, f := range h.Findings {
			if f.Severity == vmSeverityBlocker {
				blocks++
			}
		}
	}
	if blocks > 0 {
		return fmt.Errorf("the checks found %d problem(s) to fix first; see Checks", blocks)
	}
	return nil
}

func (r *dcRunner) runVerify(ctx context.Context, id string) error {
	deadline := r.now().Add(r.limit)
	for {
		p := r.snapshot(id)
		res, err := r.host.Step(ctx, p, dcStepVerify)
		if err == nil && res.Ready {
			r.apply(id, dcStepVerify, dcStepResult{Notes: res.Notes})
			return nil
		}
		why := res.Waiting
		if err != nil {
			why = err.Error()
		}
		r.setStep(id, dcStepVerify, func(s *dcStep) { s.Message = "Waiting: " + why })
		if r.now().After(deadline) {
			return fmt.Errorf("the new DC is still not working after %d minutes: %s", int(r.limit.Minutes()), why)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.poll):
		}
	}
}

// Rollback puts the recorded changes and roles back (and optionally demotes
// the new DC). It runs in the foreground and returns what it did.
func (r *dcRunner) Rollback(id string, removeNew bool) ([]string, error) {
	r.mu.Lock()
	p, err := r.planLocked(id)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if _, busy := r.running[id]; busy {
		r.mu.Unlock()
		return nil, errors.New("a step of this plan is running; stop it first")
	}
	if ok, why := dcRollbackAllowed(p); !ok {
		r.mu.Unlock()
		return nil, errors.New(why)
	}
	if why := r.ready(p, "rollback"); why != "" {
		r.mu.Unlock()
		return nil, errors.New(why)
	}
	release := func() {}
	if r.hold != nil {
		end, ok := r.hold()
		if !ok {
			r.mu.Unlock()
			return nil, errors.New("another step that must finish first is running")
		}
		release = end
	}
	r.running[id] = "rollback"
	snap := dcClone(p)
	r.mu.Unlock()
	defer func() {
		release()
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}()
	req := dcRollbackRequest{RemoveNew: removeNew && snap.done(dcStepPromote)}
	for _, c := range snap.Changes {
		if !c.Restored {
			req.Changes = append(req.Changes, c)
		}
	}
	req.Roles = append([]string{}, snap.RolesMoved...)
	steps := dcRollbackSteps(&snap, req.RemoveNew)
	r.log(&snap, "Roll back started: "+strings.Join(steps, " "))
	res, err := r.host.Rollback(context.Background(), &snap, req)
	r.mu.Lock()
	defer r.mu.Unlock()
	live, e := r.planLocked(id)
	if e != nil {
		return steps, err
	}
	open := 0
	for i := range live.Changes {
		if live.Changes[i].Restored {
			continue
		}
		for _, n := range res.Restored {
			if n >= 0 && n < len(req.Changes) && dcSameChange(req.Changes[n], live.Changes[i]) {
				live.Changes[i].Restored = true
			}
		}
		if !live.Changes[i].Restored {
			open++
		}
	}
	if len(res.RolesMoved) > 0 {
		left := []string{}
		for _, role := range live.RolesMoved {
			if !dcHasRole(res.RolesMoved, role) {
				left = append(left, role)
			}
		}
		live.RolesMoved = left
		if len(left) == 0 {
			live.setStep(dcStepRoles, dcStep{State: dcPending, Notes: []string{"Moved back by Roll back."}})
		}
	}
	if open == 0 {
		live.setStep(dcStepRepoint, dcStep{State: map[bool]string{true: dcPending, false: dcSkipped}[dcNeeded(live, dcStepRepoint)]})
	}
	if req.RemoveNew && err == nil {
		for _, s := range []string{dcStepPromote, dcStepVerify} {
			live.setStep(s, dcStep{State: dcPending})
		}
	}
	if err == nil && open == 0 && len(live.RolesMoved) == 0 {
		live.RolledBackAt = r.now()
		live.Schedule = nil
	}
	live.Note = strings.Join(res.Notes, " ")
	r.saveLocked()
	if err != nil {
		return steps, err
	}
	return steps, nil
}

func dcSameChange(a, b dcChange) bool {
	return a.Kind == b.Kind && strings.EqualFold(a.Server, b.Server) && a.Key == b.Key && strings.Join(a.After, ",") == strings.Join(b.After, ",")
}

// Tick runs scheduled phases that are due.
func (r *dcRunner) Tick() {
	r.mu.Lock()
	due := []dcSchedule{}
	ids := []string{}
	for _, p := range r.plans {
		if p.Schedule == nil || p.Schedule.At.After(r.now()) {
			continue
		}
		if _, busy := r.running[p.ID]; busy {
			continue
		}
		due = append(due, *p.Schedule)
		ids = append(ids, p.ID)
	}
	r.mu.Unlock()
	for i, s := range due {
		if err := r.Run(ids[i], s.Phase); err != nil {
			r.mu.Lock()
			p, e := r.planLocked(ids[i])
			if e == nil {
				// A scheduled phase that cannot start is not retried late:
				// the operator chose that time.
				p.Schedule = nil
				p.Note = "The scheduled " + strings.ToLower(dcPhaseNames[s.Phase]) + " at " + s.At.Local().Format("2006-01-02 15:04") + " did not start: " + dcCap(err)
				r.saveLocked()
			}
			r.mu.Unlock()
		}
	}
}
