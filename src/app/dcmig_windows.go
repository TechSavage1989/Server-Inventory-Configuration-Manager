//go:build windows

package main

// Move a domain controller (dcmig*.go): the page's endpoints, the scheduler
// and the PowerShell runner. Every call to a server runs through the signed
// Invoke-DcMigration.ps1, which receives everything as one JSON line on
// standard input. Passwords (the Domain Admin account, the DSRM password,
// local Administrator passwords and a workgroup server's account) stay in
// memory (a.dcmig) until Forget or exit and never reach logs, files or the
// state response.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed Invoke-DcMigration.ps1
var dcScriptData []byte

const dcScriptName = "Invoke-DcMigration.ps1"

type dcSecrets struct {
	User         string
	Password     string
	JoinUser     string
	JoinPassword string
	DSRM         string // for the next promotion
	LocalAdmin   string // the old server's new local Administrator password
	NewLocal     string // the new server's, for a roll back that demotes it
}

type dcRuntime struct {
	mu      sync.Mutex
	runner  *dcRunner
	secrets dcSecrets
	readOld *dcSourceInfo
	readNew *dcTargetInfo
	readFor [2]string
	reading bool
	message string
	failed  bool
	logs    []string
}

func (a *application) dcRoot() string {
	return filepath.Join(a.update.appDataRoot, "Domain-Controllers")
}

// startDCMig loads the plans and runs the scheduler while the application is
// open.
func (a *application) startDCMig() {
	s := &a.dcmig
	s.mu.Lock()
	r := newDCRunner(filepath.Join(a.dcRoot(), "dc-plans.json"), dcScriptHost{a})
	r.log = a.dcLog
	r.ready = a.dcReady
	r.hold = func() (func(), bool) {
		if !a.beginCriticalOperation() {
			return nil, false
		}
		return a.endCriticalOperation, true
	}
	if err := r.Load(); err != nil {
		s.message, s.failed = "The domain controller plans could not be read: "+err.Error(), true
	}
	s.runner = r
	s.mu.Unlock()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.Tick()
		case <-a.shutdown:
			return
		}
	}
}

func (a *application) dcRunnerReady() *dcRunner {
	a.dcmig.mu.Lock()
	defer a.dcmig.mu.Unlock()
	return a.dcmig.runner
}

// dcBusy is true while any step of a domain controller plan runs.
func (a *application) dcBusy() bool {
	r := a.dcRunnerReady()
	return r != nil && r.Busy()
}

func (a *application) dcLog(p *dcPlan, text string) {
	line := time.Now().Format("2006-01-02 15:04:05") + " "
	if p != nil {
		line += "[" + p.Name + "] "
	}
	line += text
	a.dcmig.mu.Lock()
	a.dcmig.logs = append(a.dcmig.logs, line)
	if len(a.dcmig.logs) > 400 {
		a.dcmig.logs = a.dcmig.logs[len(a.dcmig.logs)-400:]
	}
	a.dcmig.mu.Unlock()
	if p != nil && p.ID != "" {
		path := filepath.Join(a.dcRoot(), "Logs", safeName(p.ID)+".log")
		if os.MkdirAll(filepath.Dir(path), 0700) == nil {
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
				f.WriteString(line + "\r\n")
				f.Close()
			}
		}
	}
}

// dcReady says why a phase cannot run now: a password not typed since the
// application started.
func (a *application) dcReady(p *dcPlan, phase string) string {
	a.dcmig.mu.Lock()
	defer a.dcmig.mu.Unlock()
	sec := a.dcmig.secrets
	if phase == dcPhaseCheck {
		// Read only: this computer's sign-in works when no account was typed.
		if p.User != "" && (!strings.EqualFold(sec.User, p.User) || sec.Password == "") {
			return dcAccountWhy(p.User)
		}
		return ""
	}
	if p.User == "" || !strings.EqualFold(sec.User, p.User) || sec.Password == "" {
		return dcAccountWhy(p.User)
	}
	switch phase {
	case dcPhaseBuild:
		if !p.done(dcStepPromote) && sec.DSRM == "" {
			return "Type the Directory Services Restore Mode password for the new DC; it is kept only while the application is open."
		}
	case dcPhaseRetire:
		if !p.Settings.OldDead && !p.done(dcStepDemote) && sec.LocalAdmin == "" {
			return "Type the new local Administrator password for the old server; it is kept only while the application is open."
		}
	}
	return ""
}

func dcAccountWhy(user string) string {
	if user == "" {
		return "Type a Domain Admin account: promotion, role moves and demotion pass it to other domain controllers, which Windows cannot do with your own sign-in without CredSSP (never used)."
	}
	return "Sign in again as " + user + ": passwords are kept only while the application is open."
}

func (a *application) dcRunScript(ctx context.Context, mode string, input map[string]any, plan *dcPlan) ([]byte, error) {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "DC-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, dcScriptName)
	if err := os.WriteFile(script, dcScriptData, 0600); err != nil {
		return nil, err
	}
	resultPath := filepath.Join(directory, "Result.json")
	a.dcmig.mu.Lock()
	sec := a.dcmig.secrets
	a.dcmig.mu.Unlock()
	input["user"], input["password"] = sec.User, sec.Password
	input["joinUser"], input["joinPassword"] = sec.JoinUser, sec.JoinPassword
	input["dsrm"], input["localAdmin"] = sec.DSRM, sec.LocalAdmin
	if mode == "Rollback" {
		input["localAdmin"] = sec.NewLocal
	}
	command := exec.Command(a.powerShellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Mode", mode, "-ResultPath", resultPath)
	line, err := json.Marshal(input)
	for k := range input {
		if k == "password" || k == "joinPassword" || k == "dsrm" || k == "localAdmin" {
			input[k] = ""
		}
	}
	if err != nil {
		return nil, err
	}
	line = append(line, '\n')
	command.Stdin = bytes.NewReader(line)
	defer func() {
		for i := range line {
			line[i] = 0
		}
	}()
	done := make(chan struct{})
	defer close(done)
	waitErr := a.runEngineProcess(command, func(text string) {
		if i := strings.Index(text, "[INFO] "); i >= 0 {
			text = text[i+7:]
		}
		a.dcLog(plan, text)
	}, func(c *exec.Cmd) {
		go func() {
			select {
			case <-ctx.Done():
				// Promotion and demotion are left to finish on the server;
				// stopping ends only this computer's wait.
				if c.Process != nil {
					c.Process.Kill()
				}
			case <-done:
			}
		}()
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return nil, fmt.Errorf("the domain controller engine stopped: %v", waitErr)
		}
		return nil, errors.New("the domain controller engine wrote no result")
	}
	return stripUTF8BOM(data), nil
}

type dcEngineResult struct {
	OK         bool              `json:"ok"`
	Error      string            `json:"error"`
	Old        *dcSourceInfo     `json:"old"`
	New        *dcTargetInfo     `json:"new"`
	Notes      []string          `json:"notes"`
	Changes    []dcChange        `json:"changes"`
	RolesMoved []string          `json:"rolesMoved"`
	Restored   []int             `json:"restored"`
	Ready      bool              `json:"ready"`
	Waiting    string            `json:"waiting"`
	RoleOwners map[string]string `json:"roleOwners"`
}

func (a *application) dcCall(ctx context.Context, mode string, input map[string]any, plan *dcPlan) (dcEngineResult, error) {
	var out dcEngineResult
	data, err := a.dcRunScript(ctx, mode, input, plan)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("the domain controller engine's result could not be read: %v", err)
	}
	if !out.OK {
		return out, errors.New(strings.TrimSpace(out.Error))
	}
	return out, nil
}

// dcScriptHost is the dcHost that reaches real servers.
type dcScriptHost struct{ a *application }

func (h dcScriptHost) ReadOld(ctx context.Context, p *dcPlan) (*dcSourceInfo, error) {
	res, err := h.a.dcCall(ctx, "ReadOld", map[string]any{"source": p.Source}, p)
	if err != nil {
		return nil, err
	}
	if res.Old == nil {
		return nil, errors.New("the old DC returned nothing")
	}
	return res.Old, nil
}

func (h dcScriptHost) ReadNew(ctx context.Context, p *dcPlan) (*dcTargetInfo, error) {
	res, err := h.a.dcCall(ctx, "ReadNew", map[string]any{"target": p.Target}, p)
	if err != nil {
		return nil, err
	}
	if res.New == nil {
		return nil, errors.New("the new server returned nothing")
	}
	return res.New, nil
}

func (h dcScriptHost) Health(ctx context.Context, p *dcPlan, dc string) ([]vmFinding, error) {
	h.a.dcmig.mu.Lock()
	user, pass := h.a.dcmig.secrets.User, h.a.dcmig.secrets.Password
	h.a.dcmig.mu.Unlock()
	facts, err := h.a.runPreflightScript(preflightItemState{ID: dc, Name: dc, Computer: dc, Via: "winrm"}, []string{preflightCheckDC}, user, pass)
	if err != nil {
		return nil, err
	}
	if facts == nil || facts.DC == nil {
		return nil, errors.New("the checks returned nothing")
	}
	return preflightDCFindings(facts.DC, facts, time.Now().Format("2006-01-02 15:04")), nil
}

func (h dcScriptHost) Step(ctx context.Context, p *dcPlan, step string) (dcStepResult, error) {
	input := dcStepInput(p, step)
	res, err := h.a.dcCall(ctx, "Step", input, p)
	return dcStepResult{Notes: res.Notes, Changes: res.Changes, RolesMoved: res.RolesMoved, Ready: res.Ready, Waiting: res.Waiting, RoleOwners: res.RoleOwners}, err
}

func (h dcScriptHost) Rollback(ctx context.Context, p *dcPlan, req dcRollbackRequest) (dcStepResult, error) {
	input := dcStepInput(p, "")
	input["rollback"] = req
	res, err := h.a.dcCall(ctx, "Rollback", input, p)
	return dcStepResult{Notes: res.Notes, RolesMoved: res.RolesMoved, Restored: res.Restored}, err
}

func (a *application) dcSetSecrets(update func(s *dcSecrets)) {
	a.dcmig.mu.Lock()
	update(&a.dcmig.secrets)
	a.dcmig.mu.Unlock()
}

type dcStateResponse struct {
	Plans       []dcPlan               `json:"plans"`
	Running     map[string]string      `json:"running"`
	Ready       map[string]string      `json:"ready"` // plan ID: why the next phase cannot run
	Checks      map[string][]vmFinding `json:"checks"`
	Rollback    map[string]string      `json:"rollback"` // plan ID: why Roll back is off ("" when allowed)
	Guide       map[string][]string    `json:"guide"`    // plan ID: what to do after demotion
	ReadOld     *dcSourceInfo          `json:"readOld"`
	ReadNew     *dcTargetInfo          `json:"readNew"`
	ReadFor     [2]string              `json:"readFor"`
	Reading     bool                   `json:"reading"`
	Message     string                 `json:"message"`
	Failed      bool                   `json:"failed"`
	User        string                 `json:"user"` // held account (no password)
	JoinUser    string                 `json:"joinUser"`
	HasDSRM     bool                   `json:"hasDsrm"`
	HasLocal    bool                   `json:"hasLocal"`
	HasNewLocal bool                   `json:"hasNewLocal"`
	Signed      string                 `json:"signedInAccount"`
	Logs        []string               `json:"logs"`
	Roles       map[string]string      `json:"roles"`
	Steps       map[string]string      `json:"steps"`
	Phases      map[string]string      `json:"phases"`
	PhaseSteps  map[string][]string    `json:"phaseSteps"`
	IPModes     map[string]string      `json:"ipModes"`
}

func (a *application) dcState() dcStateResponse {
	out := dcStateResponse{Plans: []dcPlan{}, Running: map[string]string{}, Ready: map[string]string{}, Checks: map[string][]vmFinding{}, Rollback: map[string]string{}, Guide: map[string][]string{},
		Roles: dcRoleNames, Steps: dcStepNames, Phases: dcPhaseNames, PhaseSteps: dcPhaseSteps, IPModes: dcIPModeText}
	r := a.dcRunnerReady()
	if r != nil {
		out.Plans = r.View()
		out.Running = r.Running()
		for i := range out.Plans {
			p := &out.Plans[i]
			out.Checks[p.ID] = dcCheckPlan(p)
			if ok, why := dcRollbackAllowed(p); !ok {
				out.Rollback[p.ID] = why
			} else {
				out.Rollback[p.ID] = ""
			}
			out.Guide[p.ID] = dcRollbackGuide(p)
			for _, phase := range []string{dcPhaseBuild, dcPhaseCutover, dcPhaseRetire} {
				if dcPhaseBlock(p, phase) == "" {
					if why := a.dcReady(p, phase); why != "" {
						out.Ready[p.ID] = why
					}
					break
				}
			}
		}
	}
	a.mu.Lock()
	out.Signed = a.signedInAccount
	a.mu.Unlock()
	a.dcmig.mu.Lock()
	defer a.dcmig.mu.Unlock()
	out.ReadOld, out.ReadNew, out.ReadFor, out.Reading, out.Message, out.Failed = a.dcmig.readOld, a.dcmig.readNew, a.dcmig.readFor, a.dcmig.reading, a.dcmig.message, a.dcmig.failed
	sec := a.dcmig.secrets
	if sec.Password != "" {
		out.User = sec.User
	}
	out.JoinUser = sec.JoinUser
	out.HasDSRM, out.HasLocal, out.HasNewLocal = sec.DSRM != "", sec.LocalAdmin != "", sec.NewLocal != ""
	out.Logs = append([]string{}, a.dcmig.logs...)
	if len(out.Logs) > 150 {
		out.Logs = out.Logs[len(out.Logs)-150:]
	}
	return out
}

func dcCheckPassword(p string) error {
	if len(p) > 256 {
		return errors.New("that password is too long")
	}
	return nil
}

// handleDC serves /api/dc/*.
func (a *application) handleDC(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	r := a.dcRunnerReady()
	if r == nil {
		fail(http.StatusServiceUnavailable, "Move a domain controller is starting; try again in a moment.")
		return
	}
	action := strings.TrimPrefix(request.URL.Path, "/api/dc/")
	if action == "state" {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "dc": a.dcState()})
		return
	}
	if request.Method != http.MethodPost {
		fail(http.StatusMethodNotAllowed, "POST is required.")
		return
	}
	var in struct {
		dcCreateInput
		Password     string `json:"password"`
		JoinUser     string `json:"joinUser"`
		JoinPassword string `json:"joinPassword"`
		DSRM         string `json:"dsrm"`
		LocalAdmin   string `json:"localAdmin"`
		NewLocal     string `json:"newLocal"`
		Phase        string `json:"phase"`
		At           string `json:"at"`
		Confirm      string `json:"confirm"`
		RemoveNew    bool   `json:"removeNew"`
	}
	if !decodeVMwareRequest(response, request, &in) {
		return
	}
	defer func() { in.Password, in.JoinPassword, in.DSRM, in.LocalAdmin, in.NewLocal = "", "", "", "", "" }()
	for _, p := range []string{in.Password, in.JoinPassword, in.DSRM, in.LocalAdmin, in.NewLocal} {
		if err := dcCheckPassword(p); err != nil {
			fail(http.StatusBadRequest, dcCap(err))
			return
		}
	}
	// Passwords typed with any request are kept for the account they belong to.
	keep := func() error {
		user := strings.TrimSpace(in.User)
		a.dcmig.mu.Lock()
		defer a.dcmig.mu.Unlock()
		s := &a.dcmig.secrets
		if user != "" && !strings.EqualFold(user, s.User) {
			*s = dcSecrets{JoinUser: s.JoinUser, JoinPassword: s.JoinPassword}
			s.User = user
		}
		if in.Password != "" {
			if user == "" {
				return errors.New("type the account the password belongs to")
			}
			s.Password = in.Password
		}
		if in.JoinUser != "" {
			s.JoinUser = strings.TrimSpace(in.JoinUser)
		}
		if in.JoinPassword != "" {
			s.JoinPassword = in.JoinPassword
		}
		if in.DSRM != "" {
			s.DSRM = in.DSRM
		}
		if in.LocalAdmin != "" {
			s.LocalAdmin = in.LocalAdmin
		}
		if in.NewLocal != "" {
			s.NewLocal = in.NewLocal
		}
		return nil
	}
	if err := keep(); err != nil {
		fail(http.StatusBadRequest, dcCap(err))
		return
	}
	confirmed := func(p *dcPlan) bool { return strings.TrimSpace(in.Confirm) == strings.TrimSpace(p.Name) }
	find := func(id string) *dcPlan {
		for _, p := range r.View() {
			if p.ID == id {
				c := p
				return &c
			}
		}
		return nil
	}
	switch action {
	case "account":
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "forget":
		a.dcSetSecrets(func(s *dcSecrets) { *s = dcSecrets{} })
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "read":
		if err := dcCheckComputer(in.Source, "old domain controller"); err != nil {
			fail(http.StatusBadRequest, dcCap(err))
			return
		}
		if err := dcCheckComputer(in.Target, "new server"); err != nil {
			fail(http.StatusBadRequest, dcCap(err))
			return
		}
		a.dcmig.mu.Lock()
		if a.dcmig.reading {
			a.dcmig.mu.Unlock()
			fail(http.StatusConflict, "The servers are being read.")
			return
		}
		a.dcmig.reading, a.dcmig.message, a.dcmig.failed = true, "Reading "+in.Source+" and "+in.Target+" (read only).", false
		a.dcmig.mu.Unlock()
		go a.dcRead(strings.TrimSpace(in.Source), strings.TrimSpace(in.Target))
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
	case "save":
		a.dcmig.mu.Lock()
		old, tgt, readFor := a.dcmig.readOld, a.dcmig.readNew, a.dcmig.readFor
		a.dcmig.mu.Unlock()
		if !dcSameComputer(readFor[0], in.Source) || !dcSameComputer(readFor[1], in.Target) {
			old, tgt = nil, nil
		}
		if in.ID == "" && (old == nil || tgt == nil) {
			fail(http.StatusBadRequest, "Read both servers first.")
			return
		}
		plan, err := r.Create(in.dcCreateInput, old, tgt)
		if err != nil {
			fail(http.StatusBadRequest, dcCap(err))
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "plan": plan, "checks": dcCheckPlan(plan)})
	case "run":
		p := find(in.ID)
		if p == nil {
			fail(http.StatusNotFound, "That domain controller plan no longer exists.")
			return
		}
		if !confirmed(p) {
			fail(http.StatusBadRequest, "Confirm the plan's name to "+strings.ToLower(dcPhaseNames[in.Phase])+".")
			return
		}
		if err := r.Run(in.ID, in.Phase); err != nil {
			fail(http.StatusConflict, dcCap(err))
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
	case "check":
		// Only the read-only check of every DC; nothing changes.
		p := find(in.ID)
		if p == nil {
			fail(http.StatusNotFound, "That domain controller plan no longer exists.")
			return
		}
		if err := r.RunHealth(in.ID); err != nil {
			fail(http.StatusConflict, dcCap(err))
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
	case "stop":
		if err := r.Stop(in.ID); err != nil {
			fail(http.StatusConflict, dcCap(err))
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "schedule":
		p := find(in.ID)
		if p == nil {
			fail(http.StatusNotFound, "That domain controller plan no longer exists.")
			return
		}
		var at time.Time
		if strings.TrimSpace(in.At) != "" {
			parsed, err := time.ParseInLocation("2006-01-02T15:04", in.At, time.Local)
			if err != nil {
				fail(http.StatusBadRequest, "Choose a date and time.")
				return
			}
			at = parsed
			if !confirmed(p) {
				fail(http.StatusBadRequest, "Confirm the plan's name to schedule it.")
				return
			}
			if why := a.dcReady(p, in.Phase); why != "" {
				fail(http.StatusBadRequest, why)
				return
			}
		}
		if err := r.Schedule(in.ID, in.Phase, at); err != nil {
			fail(http.StatusBadRequest, dcCap(err))
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "rollback":
		p := find(in.ID)
		if p == nil {
			fail(http.StatusNotFound, "That domain controller plan no longer exists.")
			return
		}
		if !confirmed(p) {
			fail(http.StatusBadRequest, "Confirm the plan's name to roll back.")
			return
		}
		if in.RemoveNew {
			a.dcmig.mu.Lock()
			missing := a.dcmig.secrets.NewLocal == ""
			a.dcmig.mu.Unlock()
			if missing {
				fail(http.StatusBadRequest, "Type a local Administrator password for the new server, which becomes a member server again.")
				return
			}
		}
		steps, err := r.Rollback(in.ID, in.RemoveNew)
		if err != nil {
			writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": dcCap(err), "steps": steps})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "steps": steps})
	case "delete":
		p := find(in.ID)
		if p == nil {
			fail(http.StatusNotFound, "That domain controller plan no longer exists.")
			return
		}
		if !confirmed(p) {
			fail(http.StatusBadRequest, "Confirm the plan's name to delete it.")
			return
		}
		if err := r.Delete(in.ID); err != nil {
			fail(http.StatusConflict, dcCap(err))
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	default:
		fail(http.StatusNotFound, "Not found.")
	}
}

// dcRead reads both servers in the background (read only).
func (a *application) dcRead(source, target string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	plan := &dcPlan{Name: "Read", Source: source, Target: target}
	h := dcScriptHost{a}
	old, errOld := h.ReadOld(ctx, plan)
	tgt, errNew := h.ReadNew(ctx, plan)
	a.dcmig.mu.Lock()
	defer a.dcmig.mu.Unlock()
	a.dcmig.reading = false
	parts := []string{}
	if errOld != nil {
		parts = append(parts, "The old DC "+source+" could not be read: "+errOld.Error())
	}
	if errNew != nil {
		parts = append(parts, "The new server "+target+" could not be read: "+errNew.Error())
	}
	if len(parts) > 0 {
		a.dcmig.message, a.dcmig.failed = strings.Join(parts, " "), true
		return
	}
	a.dcmig.readOld, a.dcmig.readNew, a.dcmig.readFor = old, tgt, [2]string{source, target}
	a.dcmig.message, a.dcmig.failed = "Read "+source+" and "+target+".", false
}
