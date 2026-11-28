//go:build windows

package main

// Move SQL databases (sqlmig*.go, rule 71): the page's endpoints, the
// scheduler and the PowerShell runner. T-SQL batches and DNS calls run
// through the signed Invoke-SqlMigration.ps1, which receives everything as
// one JSON line on standard input. Passwords stay in memory (a.sqlmig)
// until Forget or exit and never reach logs, files or the state response.

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

//go:embed Invoke-SqlMigration.ps1
var sqlScriptData []byte

const sqlScriptName = "Invoke-SqlMigration.ps1"

type sqlRuntime struct {
	mu        sync.Mutex
	runner    *sqlRunner
	passwords map[string]string // sqlKey -> password
	dnsUser   string
	dnsPass   string
	read      *sqlReadView
	readPlan  *sqlPlan
	reading   bool
	message   string
	failed    bool
	logs      []string
}

func (a *application) sqlRoot() string {
	return filepath.Join(a.update.appDataRoot, "SQL")
}

// startSQL loads the plans and runs the scheduler while the application is
// open.
func (a *application) startSQL() {
	s := &a.sqlmig
	s.mu.Lock()
	s.passwords = map[string]string{}
	r := newSQLRunner(filepath.Join(a.sqlRoot(), "sql-plans.json"))
	r.exec = a.sqlExec
	r.dns = a.sqlDNS
	r.guard = a.prodGuard
	r.ready = a.sqlReady
	r.hold = func() (func(), bool) {
		if !a.beginCriticalOperation() {
			return nil, false
		}
		return a.endCriticalOperation, true
	}
	r.log = func(p *sqlPlan, text string) { a.sqlLog(p, text) }
	if err := r.Load(); err != nil {
		s.message, s.failed = "The SQL plans could not be read: "+err.Error(), true
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

func (a *application) sqlRunnerReady() *sqlRunner {
	a.sqlmig.mu.Lock()
	defer a.sqlmig.mu.Unlock()
	return a.sqlmig.runner
}

// sqlBusy is true while a cutover, test or rollback runs.
func (a *application) sqlBusy() bool {
	r := a.sqlRunnerReady()
	return r != nil && r.Busy()
}

func (a *application) sqlLog(p *sqlPlan, text string) {
	line := time.Now().Format("2006-01-02 15:04:05") + " "
	if p != nil {
		line += "[" + p.Name + "] "
	}
	line += text
	a.sqlmig.mu.Lock()
	a.sqlmig.logs = append(a.sqlmig.logs, line)
	if len(a.sqlmig.logs) > 400 {
		a.sqlmig.logs = a.sqlmig.logs[len(a.sqlmig.logs)-400:]
	}
	a.sqlmig.mu.Unlock()
	if p != nil {
		path := filepath.Join(a.sqlRoot(), "Logs", safeName(p.ID)+".log")
		if os.MkdirAll(filepath.Dir(path), 0700) == nil {
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
				f.WriteString(line + "\r\n")
				f.Close()
			}
		}
	}
}

// sqlReady says why a plan cannot run now (a password not typed since the
// application started), or "".
func (a *application) sqlReady(p *sqlPlan) string {
	a.sqlmig.mu.Lock()
	defer a.sqlmig.mu.Unlock()
	for _, e := range []struct {
		end  sqlEndpoint
		side string
	}{{p.Source, "old"}, {p.Target, "new"}} {
		if e.end.Auth == sqlAuthCurrent {
			continue
		}
		if a.sqlmig.passwords[sqlKey(e.end)] == "" {
			return fmt.Sprintf("Sign in to the %s server %s as %s again: passwords are kept only while the application is open.", e.side, e.end.Server, e.end.User)
		}
	}
	return ""
}

type sqlSideInput struct {
	Server   string `json:"server"`
	Auth     string `json:"auth"`
	User     string `json:"user"`
	Password string `json:"password"`
	Computer string `json:"computer"`
}

func (a *application) sqlSide(e sqlEndpoint) sqlSideInput {
	a.sqlmig.mu.Lock()
	defer a.sqlmig.mu.Unlock()
	side := sqlSideInput{Server: e.Server, Auth: e.Auth, User: e.User, Computer: e.Computer()}
	if e.Auth != sqlAuthCurrent {
		side.Password = a.sqlmig.passwords[sqlKey(e)]
	}
	return side
}

func (a *application) sqlRunScript(ctx context.Context, mode string, input any, plan *sqlPlan) ([]byte, error) {
	runtimeRoot := filepath.Join(a.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "SQL-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, sqlScriptName)
	if err := os.WriteFile(script, sqlScriptData, 0600); err != nil {
		return nil, err
	}
	resultPath := filepath.Join(directory, "Result.json")
	command := exec.Command(a.powerShellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-Mode", mode, "-ResultPath", resultPath)
	line, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	// The request is cleared only after PowerShell has read it: clearing it
	// here emptied what standard input was about to send (append can share
	// the array), so the engine read only zero bytes.
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
		a.sqlLog(plan, text)
	}, func(c *exec.Cmd) {
		go func() {
			select {
			case <-ctx.Done():
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
			return nil, fmt.Errorf("the SQL engine stopped: %v", waitErr)
		}
		return nil, errors.New("the SQL engine wrote no result")
	}
	return data, nil
}

// sqlExec runs T-SQL batches on the plan's servers.
func (a *application) sqlExec(ctx context.Context, plan *sqlPlan, batches []sqlBatch) ([]sqlBatchResult, error) {
	input := map[string]any{"source": a.sqlSide(plan.Source), "target": a.sqlSide(plan.Target), "batches": batches}
	data, err := a.sqlRunScript(ctx, "Batches", input, plan)
	if err != nil {
		return nil, err
	}
	var result struct {
		OK    bool               `json:"ok"`
		Error string             `json:"error"`
		Parts [][]sqlBatchResult `json:"parts"`
	}
	if err := json.Unmarshal(stripUTF8BOM(data), &result); err != nil {
		return nil, fmt.Errorf("the SQL engine's result could not be read: %v", err)
	}
	if !result.OK {
		return nil, errors.New(strings.TrimSpace(result.Error))
	}
	out := []sqlBatchResult{}
	for _, part := range result.Parts {
		out = append(out, part...)
	}
	return out, nil
}

// sqlDNS moves the alias on the DNS server, with this Windows account or
// the account typed for DNS.
func (a *application) sqlDNS(ctx context.Context, plan *sqlPlan, req sqlDNSRequest) (sqlDNSResult, error) {
	a.sqlmig.mu.Lock()
	account := map[string]string{"user": a.sqlmig.dnsUser, "password": a.sqlmig.dnsPass}
	a.sqlmig.mu.Unlock()
	data, err := a.sqlRunScript(ctx, "Dns", map[string]any{"dns": req, "account": account}, plan)
	if err != nil {
		return sqlDNSResult{}, err
	}
	var result struct {
		OK    bool         `json:"ok"`
		Error string       `json:"error"`
		DNS   sqlDNSResult `json:"dns"`
	}
	if err := json.Unmarshal(stripUTF8BOM(data), &result); err != nil {
		return sqlDNSResult{}, err
	}
	if !result.OK {
		return sqlDNSResult{}, errors.New(strings.TrimSpace(result.Error))
	}
	return result.DNS, nil
}

// sqlKeepPasswords stores the typed passwords for the plan's endpoints.
func (a *application) sqlKeepPasswords(src, dst sqlEndpoint, srcPass, dstPass string) error {
	a.sqlmig.mu.Lock()
	defer a.sqlmig.mu.Unlock()
	for _, x := range []struct {
		e    sqlEndpoint
		pass string
		side string
	}{{src, srcPass, "old"}, {dst, dstPass, "new"}} {
		if x.e.Auth == sqlAuthCurrent {
			continue
		}
		if len(x.pass) > 256 {
			return errors.New("that password is too long")
		}
		if x.pass != "" {
			a.sqlmig.passwords[sqlKey(x.e)] = x.pass
		}
		if a.sqlmig.passwords[sqlKey(x.e)] == "" {
			return fmt.Errorf("type the password for the %s server's account", x.side)
		}
	}
	return nil
}

type sqlStateResponse struct {
	Plans    []sqlPlan         `json:"plans"`
	Running  map[string]string `json:"running"`
	Read     *sqlReadView      `json:"read"`
	Reading  bool              `json:"reading"`
	Message  string            `json:"message"`
	Failed   bool              `json:"failed"`
	Held     []sqlEndpoint     `json:"held"` // accounts with a password in memory (no passwords)
	DNSUser  string            `json:"dnsUser"`
	Logs     []string          `json:"logs"`
	Ready    map[string]string `json:"ready"`
	Methods  map[string]string `json:"methods"`
	Signed   string            `json:"signedInAccount"`
	Settings sqlLimits         `json:"limits"`
}

type sqlLimits struct {
	MinSync int `json:"minSync"`
	MaxSync int `json:"maxSync"`
}

func (a *application) sqlState() sqlStateResponse {
	r := a.sqlRunnerReady()
	out := sqlStateResponse{Plans: []sqlPlan{}, Running: map[string]string{}, Ready: map[string]string{}, Methods: sqlMethodNames, Settings: sqlLimits{sqlMinSyncMinute, sqlMaxSyncMinute}}
	if r != nil {
		out.Plans = r.View()
		out.Running = r.Running()
		for i := range out.Plans {
			if why := a.sqlReady(&out.Plans[i]); why != "" {
				out.Ready[out.Plans[i].ID] = why
			}
		}
	}
	a.mu.Lock()
	out.Signed = a.signedInAccount
	a.mu.Unlock()
	a.sqlmig.mu.Lock()
	defer a.sqlmig.mu.Unlock()
	out.Read, out.Reading, out.Message, out.Failed, out.DNSUser = a.sqlmig.read, a.sqlmig.reading, a.sqlmig.message, a.sqlmig.failed, a.sqlmig.dnsUser
	out.Logs = append([]string{}, a.sqlmig.logs...)
	if len(out.Logs) > 120 {
		out.Logs = out.Logs[len(out.Logs)-120:]
	}
	for key := range a.sqlmig.passwords {
		parts := strings.Split(key, "\x00")
		if len(parts) == 3 {
			out.Held = append(out.Held, sqlEndpoint{Server: parts[0], Auth: parts[1], User: parts[2]})
		}
	}
	return out
}

// handleSQL serves /api/sql/*.
func (a *application) handleSQL(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	r := a.sqlRunnerReady()
	if r == nil {
		fail(http.StatusServiceUnavailable, "Move SQL databases is starting; try again in a moment.")
		return
	}
	action := strings.TrimPrefix(request.URL.Path, "/api/sql/")
	if action == "state" {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "sql": a.sqlState()})
		return
	}
	switch action {
	case "read":
		var in struct {
			Source         sqlEndpoint `json:"source"`
			Target         sqlEndpoint `json:"target"`
			SourcePassword string      `json:"sourcePassword"`
			TargetPassword string      `json:"targetPassword"`
		}
		if !decodeVMwareRequest(response, request, &in) {
			return
		}
		if err := sqlCheckEndpoint(&in.Source, "old"); err != nil {
			fail(http.StatusBadRequest, sqlCap(err))
			return
		}
		if err := sqlCheckEndpoint(&in.Target, "new"); err != nil {
			fail(http.StatusBadRequest, sqlCap(err))
			return
		}
		if err := a.sqlKeepPasswords(in.Source, in.Target, in.SourcePassword, in.TargetPassword); err != nil {
			fail(http.StatusBadRequest, sqlCap(err))
			return
		}
		a.sqlmig.mu.Lock()
		if a.sqlmig.reading {
			a.sqlmig.mu.Unlock()
			fail(http.StatusConflict, "The servers are being read.")
			return
		}
		a.sqlmig.reading, a.sqlmig.message, a.sqlmig.failed = true, "Reading "+in.Source.Server+" and "+in.Target.Server+" (read only).", false
		a.sqlmig.mu.Unlock()
		plan := &sqlPlan{ID: "read", Name: "Read", Source: in.Source, Target: in.Target}
		go a.sqlRead(plan)
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
	case "check", "create":
		var in struct {
			sqlCreateInput
			Confirm string `json:"confirm"`
		}
		if !decodeVMwareRequest(response, request, &in) {
			return
		}
		a.sqlmig.mu.Lock()
		view := a.sqlmig.read
		a.sqlmig.mu.Unlock()
		if view == nil || view.Source == nil || view.Target == nil {
			fail(http.StatusBadRequest, "Read both servers first.")
			return
		}
		if action == "check" {
			p := sqlPreviewPlan(in.sqlCreateInput, view.Source, view.Target)
			writeJSON(response, http.StatusOK, map[string]any{"ok": true, "findings": sqlCheckPlan(view.Source, view.Target, p)})
			return
		}
		if strings.TrimSpace(in.Confirm) != strings.TrimSpace(in.Name) {
			fail(http.StatusBadRequest, "Confirm the plan's name to start it.")
			return
		}
		plan, findings, err := r.Create(in.sqlCreateInput, view.Source, view.Target)
		if err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": sqlCap(err), "findings": findings})
			return
		}
		go r.Tick()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "plan": plan.ID, "findings": findings})
	case "checkfolder":
		var in struct {
			Source     sqlEndpoint `json:"source"`
			Target     sqlEndpoint `json:"target"`
			BackupPath string      `json:"backupPath"`
		}
		if !decodeVMwareRequest(response, request, &in) {
			return
		}
		if err := sqlCheckFolder(in.BackupPath, "backup folder", true); err != nil || strings.TrimSpace(in.BackupPath) == "" {
			fail(http.StatusBadRequest, "Type the backup folder as a share both SQL Server services can reach.")
			return
		}
		a.sqlmig.mu.Lock()
		view := a.sqlmig.read
		a.sqlmig.mu.Unlock()
		if view == nil {
			fail(http.StatusBadRequest, "Read both servers first.")
			return
		}
		plan := &sqlPlan{ID: "check", Name: "Check", Source: in.Source, Target: in.Target}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		folder := strings.TrimRight(strings.TrimSpace(in.BackupPath), `\`)
		res, err := a.sqlExec(ctx, plan, sqlCheckFolderBatches(folder))
		if err == nil {
			err = sqlCheckFolderResult(res, folder, sqlBriefOf(view.Source), sqlBriefOf(view.Target))
		}
		if err != nil {
			fail(http.StatusBadRequest, sqlCap(err))
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "message": "Both SQL Server services can use " + folder + "."})
	case "signin":
		var in struct {
			Plan     string `json:"plan"`
			Source   string `json:"sourcePassword"`
			Target   string `json:"targetPassword"`
			DNSUser  string `json:"dnsUser"`
			DNSPass  string `json:"dnsPassword"`
			DNSClear bool   `json:"dnsClear"`
		}
		if !decodeVMwareRequest(response, request, &in) {
			return
		}
		if in.DNSClear || in.DNSUser != "" {
			user := strings.TrimSpace(in.DNSUser)
			if !in.DNSClear && (!strings.ContainsAny(user, `\@`) || in.DNSPass == "" || len(in.DNSPass) > 256) {
				fail(http.StatusBadRequest, `Type the DNS account as DOMAIN\user with its password, or use your current sign-in.`)
				return
			}
			a.sqlmig.mu.Lock()
			if in.DNSClear {
				a.sqlmig.dnsUser, a.sqlmig.dnsPass = "", ""
			} else {
				a.sqlmig.dnsUser, a.sqlmig.dnsPass = user, in.DNSPass
			}
			a.sqlmig.mu.Unlock()
		}
		if in.Plan != "" {
			var plan *sqlPlan
			for _, p := range r.View() {
				if p.ID == in.Plan {
					c := p
					plan = &c
				}
			}
			if plan == nil {
				fail(http.StatusNotFound, "That SQL plan no longer exists.")
				return
			}
			if err := a.sqlKeepPasswords(plan.Source, plan.Target, in.Source, in.Target); err != nil {
				fail(http.StatusBadRequest, sqlCap(err))
				return
			}
			go r.Tick()
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "forget":
		if !decodeVMwareRequest(response, request, nil) {
			return
		}
		a.sqlmig.mu.Lock()
		a.sqlmig.passwords = map[string]string{}
		a.sqlmig.dnsUser, a.sqlmig.dnsPass = "", ""
		a.sqlmig.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "action":
		var in struct {
			Plan    string    `json:"plan"`
			Action  string    `json:"action"`
			Names   []string  `json:"names"`
			At      time.Time `json:"at"`
			Wave    int       `json:"wave"`
			Method  string    `json:"method"`
			AG      string    `json:"ag"`
			Confirm string    `json:"confirm"`
		}
		if !decodeVMwareRequest(response, request, &in) {
			return
		}
		var plan *sqlPlan
		for _, p := range r.View() {
			if p.ID == in.Plan {
				c := p
				plan = &c
			}
		}
		if plan == nil {
			fail(http.StatusNotFound, "That SQL plan no longer exists.")
			return
		}
		needsConfirm := map[string]string{"cutover": "count", "schedule": "count", "rollback": "count", "delete": "name", "finishdag": "name", "alias": "name", "unalias": "name"}
		switch needsConfirm[in.Action] {
		case "count":
			if strings.TrimSpace(in.Confirm) != fmt.Sprint(len(in.Names)) {
				fail(http.StatusBadRequest, "Confirm the number of databases to go ahead.")
				return
			}
		case "name":
			if strings.TrimSpace(in.Confirm) != plan.Name {
				fail(http.StatusBadRequest, "Confirm the plan's name to go ahead.")
				return
			}
		}
		var err error
		message := ""
		ctx := context.Background()
		switch in.Action {
		case "sync":
			var n int
			n, err = r.SyncNow(plan.ID, in.Names)
			message = fmt.Sprintf("Shipping the log of %s now.", vmPlural(n, "database", "databases"))
			if why := a.sqlReady(plan); why != "" {
				message = why
			}
		case "test":
			for _, n := range in.Names {
				if e := r.StartTest(plan.ID, n); e != nil && err == nil {
					err = e
				}
			}
		case "endtest":
			for _, n := range in.Names {
				if e := r.EndTest(plan.ID, n); e != nil && err == nil {
					err = e
				}
			}
		case "cutover":
			_, err = r.Cutover(plan.ID, in.Names, time.Time{})
		case "schedule":
			if in.At.IsZero() || in.At.Before(time.Now().Add(-time.Minute)) {
				err = errors.New("choose a time in the future")
			} else {
				_, err = r.Cutover(plan.ID, in.Names, in.At)
			}
		case "unschedule":
			err = r.Unschedule(plan.ID, in.Names)
		case "rollback":
			go func() {
				if e := r.Rollback(ctx, plan.ID, in.Names); e != nil {
					a.sqlLog(plan, "Rollback: "+e.Error())
				}
			}()
			message = "Rolling back."
		case "finishdag":
			err = r.FinishDAG(ctx, plan.ID, in.AG)
		case "alias":
			err = r.MoveAlias(ctx, plan.ID)
		case "unalias":
			err = r.RestoreAlias(ctx, plan.ID)
		case "pause", "resume":
			err = r.SetPaused(plan.ID, in.Action == "pause")
		case "reseed":
			for _, n := range in.Names {
				if e := r.Reseed(plan.ID, n); e != nil && err == nil {
					err = e
				}
			}
		case "method":
			for _, n := range in.Names {
				if e := r.SetMethod(plan.ID, n, in.Method); e != nil && err == nil {
					err = e
				}
			}
		case "wave":
			err = r.SetWave(plan.ID, in.Names, in.Wave)
		case "delete":
			err = r.Delete(plan.ID)
		default:
			fail(http.StatusBadRequest, "Unknown action.")
			return
		}
		if err != nil {
			fail(http.StatusConflict, sqlCap(err))
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "message": message})
	default:
		fail(http.StatusNotFound, "Not found.")
	}
}

func sqlCap(err error) string {
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

// sqlPreviewPlan builds a plan from the form for the checks only.
func sqlPreviewPlan(in sqlCreateInput, src, dst *sqlInstance) *sqlPlan {
	p := &sqlPlan{BackupPath: in.BackupPath, DataPath: in.DataPath, LogPath: in.LogPath, Objects: in.Objects, Alias: in.Alias}
	for _, d := range in.Databases {
		db := sqlDB{Name: d.Name, TargetName: d.TargetName, Method: d.Method, Wave: d.Wave, SwitchToFull: d.SwitchToFull}
		if db.Method == "" {
			if x := src.database(d.Name); x != nil {
				db.Method = sqlRecommend(src, dst, x).Method
			}
		}
		p.Databases = append(p.Databases, &db)
	}
	if strings.TrimSpace(p.Alias.Name) == "" {
		p.Alias = sqlAlias{}
	}
	return p
}

func (a *application) sqlRead(plan *sqlPlan) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var src, dst *sqlInstance
	var srcErr, dstErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); src, srcErr = sqlReadSide(ctx, a.sqlExec, plan, sqlOn) }()
	go func() { defer wg.Done(); dst, dstErr = sqlReadSide(ctx, a.sqlExec, plan, sqlDst) }()
	wg.Wait()
	finish := func(view *sqlReadView, message string, failed bool) {
		a.sqlmig.mu.Lock()
		if view != nil {
			a.sqlmig.read = view
		}
		a.sqlmig.reading, a.sqlmig.message, a.sqlmig.failed = false, message, failed
		a.sqlmig.mu.Unlock()
	}
	if srcErr != nil || dstErr != nil {
		parts := []string{}
		for _, e := range []error{srcErr, dstErr} {
			if e != nil {
				parts = append(parts, sqlCap(e))
			}
		}
		finish(nil, strings.Join(parts, " "), true)
		return
	}
	var srcObj, dstObj sqlObjects
	var objErr error
	r := &sqlRunner{exec: a.sqlExec}
	srcObj, dstObj, objErr = r.readObjects(ctx, plan)
	view := sqlBuildReadView(src, dst, &srcObj, &dstObj, objErr)
	now := time.Now().UTC()
	src.ReadAt, dst.ReadAt = now, now
	message := fmt.Sprintf("Read %s (%s, %s) and %s (%s).", src.Server, sqlVersionName(src.Major), vmPlural(len(src.Databases), "database", "databases"), dst.Server, sqlVersionName(dst.Major))
	finish(&view, message, false)
}
