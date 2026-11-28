package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sqlFakeDB is one database on a fake SQL Server.
type sqlFakeDB struct {
	status   string // ONLINE, RESTORING, OFFLINE
	readOnly bool
	access   string
	lsn      int64 // source: last LSN written; target: last LSN restored
	logBase  int64 // source: where the next log backup starts
	id       int
	created  string
	snapshot bool
}

type sqlFakeBackup struct {
	db          string
	kind        string
	first, last int64
}

// sqlFake answers the batches the runner sends as two SQL Servers would.
type sqlFake struct {
	mu        sync.Mutex
	src, dst  map[string]*sqlFakeDB
	files     map[string]sqlFakeBackup
	other     []map[string]any // other tools' log backups for the gap query
	fail      map[string]string
	unseen    map[string]bool // files the new server cannot see
	kinds     []string
	sqlSeen   []string
	nextID    int
	dagState  map[string]string // side+db -> sync state
	hardened  map[string]string
	dagRole   string // source DAG role
	dagExists bool
}

func newSQLFake() *sqlFake {
	return &sqlFake{src: map[string]*sqlFakeDB{}, dst: map[string]*sqlFakeDB{}, files: map[string]sqlFakeBackup{}, fail: map[string]string{}, unseen: map[string]bool{},
		nextID: 10, dagState: map[string]string{}, hardened: map[string]string{}, dagRole: "PRIMARY"}
}

func (f *sqlFake) side(on string) map[string]*sqlFakeDB {
	if on == sqlDst {
		return f.dst
	}
	return f.src
}

func (f *sqlFake) exec(_ context.Context, _ *sqlPlan, batches []sqlBatch) ([]sqlBatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlBatchResult{}
	for _, b := range batches {
		f.kinds = append(f.kinds, b.On+":"+b.Kind)
		f.sqlSeen = append(f.sqlSeen, b.SQL)
		res := sqlBatchResult{Name: b.Name, OK: true}
		var rows []sqlRow
		var err error
		if msg, ok := f.fail[b.Kind]; ok {
			err = errors.New(msg)
			delete(f.fail, b.Kind)
		} else {
			rows, err = f.one(b)
		}
		if err != nil {
			res.OK, res.Error = false, err.Error()
			out = append(out, res)
			if !b.Continue {
				break
			}
			continue
		}
		res.Sets = [][]sqlRow{rows}
		out = append(out, res)
	}
	return out, nil
}

func (f *sqlFake) one(b sqlBatch) ([]sqlRow, error) {
	a := b.Args
	db := a["db"]
	switch b.Kind {
	case "folder", "after", "defaultdb", "epgrant", "createag", "createdag", "joindag", "dagmode", "tofull", "tosimple":
		if b.Kind == "createdag" {
			f.dagExists = true
		}
		return nil, nil
	case "logins", "members", "perms", "config", "linked", "linkedlogins", "manual", "certs", "roles", "jobs", "jobsteps", "jobschedules", "operators", "categories", "mail", "tdecert":
		return []sqlRow{}, nil
	case "exists":
		return []sqlRow{{"fileExists": !f.unseen[a["path"]], "parentExists": true}}, nil
	case "backupfull":
		d := f.src[db]
		if d == nil || d.status != "ONLINE" {
			return nil, fmt.Errorf("database %s is not online", db)
		}
		d.lsn += 10
		first, last := d.lsn-9, d.lsn
		if a["copyOnly"] == "0" || d.logBase == 0 {
			d.logBase = last
		}
		f.files[a["path"]] = sqlFakeBackup{db: db, kind: "full", first: first, last: last}
		return []sqlRow{{"firstLsn": fmt.Sprint(first), "lastLsn": fmt.Sprint(last)}}, nil
	case "backuplog", "backuptail":
		d := f.src[db]
		if d == nil || d.status != "ONLINE" {
			return nil, fmt.Errorf("database %s is not online", db)
		}
		if b.Kind == "backuptail" {
			if !strings.Contains(b.SQL, "SINGLE_USER") || !strings.Contains(b.SQL, "NORECOVERY") {
				return nil, errors.New("tail without NORECOVERY")
			}
			d.access, d.status = "SINGLE_USER", "RESTORING"
		}
		d.lsn += 5
		first, last := d.logBase, d.lsn
		d.logBase = last
		f.files[a["path"]] = sqlFakeBackup{db: db, kind: "log", first: first, last: last}
		return []sqlRow{{"firstLsn": fmt.Sprint(first), "lastLsn": fmt.Sprint(last)}}, nil
	case "gap":
		rows := []sqlRow{}
		for _, o := range f.other {
			rows = append(rows, sqlRow(o))
		}
		return rows, nil
	case "restorefull":
		bk, ok := f.files[a["path"]]
		if !ok {
			return nil, errors.New("cannot open backup device")
		}
		if t := f.dst[db]; t != nil {
			if !strings.Contains(b.SQL, "REPLACE") {
				return nil, fmt.Errorf("the database %s already exists (no REPLACE)", db)
			}
			if t.status != "RESTORING" {
				return nil, errors.New("not restoring")
			}
		}
		status := "ONLINE"
		if a["norecovery"] == "1" {
			status = "RESTORING"
		}
		f.nextID++
		f.dst[db] = &sqlFakeDB{status: status, lsn: bk.last, id: f.nextID, created: fmt.Sprintf("2026-11-28T10:00:%02d", f.nextID)}
		return f.testInfo(b, db), nil
	case "restorelog":
		bk, ok := f.files[a["path"]]
		if !ok {
			return nil, errors.New("cannot open backup device " + a["path"])
		}
		t := f.dst[db]
		if t == nil || t.status != "RESTORING" {
			return nil, fmt.Errorf("%s is not restoring", db)
		}
		if bk.first > t.lsn {
			return nil, fmt.Errorf("the log in this backup set begins at LSN %d, which is too recent to apply to %s (at %d)", bk.first, db, t.lsn)
		}
		if bk.last > t.lsn {
			t.lsn = bk.last
		}
		if strings.Contains(b.SQL, "WITH RECOVERY") {
			t.status = "ONLINE"
		}
		return f.testInfo(b, db), nil
	case "recover":
		t := f.side(b.On)[db]
		if t == nil || t.status != "RESTORING" {
			return nil, fmt.Errorf("%s is not restoring", db)
		}
		t.status = "ONLINE"
		return nil, nil
	case "readonly":
		f.src[db].readOnly = true
		return nil, nil
	case "rollbacksource":
		d := f.src[db]
		if d.status == "RESTORING" {
			d.status = "ONLINE"
		}
		d.readOnly, d.access = false, "MULTI_USER"
		return []sqlRow{{"status": d.status}}, nil
	case "status":
		d := f.src[db]
		access := d.access
		if access == "" {
			access = "MULTI_USER"
		}
		return []sqlRow{{"status": d.status, "access": access}}, nil
	case "offline":
		if t := f.dst[db]; t != nil {
			t.status = "OFFLINE"
		}
		return []sqlRow{{"status": "OFFLINE"}}, nil
	case "snapshot":
		f.nextID++
		f.dst[db] = &sqlFakeDB{status: "ONLINE", id: f.nextID, created: "2026-11-28T11:00:00", snapshot: true}
		return []sqlRow{{"id": f.nextID, "created": "2026-11-28T11:00:00"}}, nil
	case "droptest":
		t := f.dst[db]
		if t == nil {
			return []sqlRow{{"removed": 0}}, nil
		}
		if !strings.Contains(b.SQL, fmt.Sprintf("database_id = %d", t.id)) || !strings.Contains(b.SQL, sqlStr(t.created)) {
			return nil, errors.New("The database named " + db + " is not the test copy this application made, so it was left alone.")
		}
		delete(f.dst, db)
		return []sqlRow{{"removed": 1}}, nil
	case "dagstate":
		rows := []sqlRow{}
		side := "dst"
		if b.On == sqlOn {
			side = "src"
		}
		for k, v := range f.dagState {
			if strings.HasPrefix(k, side+":") {
				name := strings.TrimPrefix(k, side+":")
				rows = append(rows, sqlRow{"db": name, "sync": v, "hardened": f.hardened[k], "isLocal": true, "sendQueueKB": 0, "redoQueueKB": 0})
			}
		}
		return rows, nil
	case "dagsecondary":
		if b.On == sqlOn {
			f.dagRole = "SECONDARY"
		} else {
			f.dagRole = "PRIMARY"
		}
		return nil, nil
	case "dagfailover":
		if b.On == sqlDst {
			f.dagRole = "NEWPRIMARY"
		} else {
			f.dagRole = "PRIMARY"
		}
		return nil, nil
	case "dropdag":
		f.dagExists = false
		return nil, nil
	}
	return nil, nil
}

func (f *sqlFake) testInfo(b sqlBatch, db string) []sqlRow {
	if strings.Contains(b.SQL, "SELECT database_id AS id") && f.dst[db] != nil {
		return []sqlRow{{"id": f.dst[db].id, "created": f.dst[db].created}}
	}
	return nil
}

func (f *sqlFake) count(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range f.kinds {
		if strings.HasSuffix(k, ":"+kind) {
			n++
		}
	}
	return n
}

func sqlTestInstances() (*sqlInstance, *sqlInstance) {
	src := &sqlInstance{Server: "OLDSQL", Machine: "OLDSQL", ServerName: "OLDSQL", Version: "13.0.5026.0", Major: 13, Edition: "Standard Edition (64-bit)", EngineEdition: 2,
		Collation: "SQL_Latin1_General_CP1_CI_AS", Sysadmin: true, DataPath: `D:\Data`, LogPath: `L:\Log`, ServiceAccount: `CORP\svc-sql`, Domain: "CORP", DNSDomain: "corp.example.com", Port: 1433}
	dst := &sqlInstance{Server: "NEWSQL", Machine: "NEWSQL", ServerName: "NEWSQL", Version: "16.0.4135.4", Major: 16, Edition: "Standard Edition (64-bit)", EngineEdition: 2,
		Collation: "SQL_Latin1_General_CP1_CI_AS", Sysadmin: true, DataPath: `E:\Data`, LogPath: `F:\Log`, ServiceAccount: `CORP\svc-sql2`, Domain: "CORP", DNSDomain: "corp.example.com", Port: 1433, AgentState: "Running",
		Volumes: []sqlVolume{{Mount: `E:\`, TotalMB: 500000, FreeMB: 400000}, {Mount: `F:\`, TotalMB: 200000, FreeMB: 150000}}}
	files := func(n string) []sqlFile {
		return []sqlFile{{Logical: n, Type: "ROWS", Physical: `D:\Data\` + n + ".mdf", FileID: 1}, {Logical: n + "_log", Type: "LOG", Physical: `L:\Log\` + n + "_log.ldf", FileID: 2}}
	}
	src.Databases = []sqlDatabase{
		{Name: "Sales", State: "ONLINE", Recovery: "FULL", UserAccess: "MULTI_USER", DataMB: 60000, LogMB: 4000, Owner: "sa", Files: files("Sales")},
		{Name: "Small", State: "ONLINE", Recovery: "SIMPLE", UserAccess: "MULTI_USER", DataMB: 500, LogMB: 100, Owner: "sa", Files: files("Small")},
		{Name: "master", State: "ONLINE", Recovery: "SIMPLE"},
	}
	return src, dst
}

type sqlTestEnv struct {
	r    *sqlRunner
	fake *sqlFake
	now  time.Time
	mu   sync.Mutex
	dns  map[string]sqlDNSResult
	dnsC []sqlDNSRequest
	src  *sqlInstance
	dst  *sqlInstance
}

func newSQLTestEnv(t *testing.T) *sqlTestEnv {
	e := &sqlTestEnv{fake: newSQLFake(), now: time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC), dns: map[string]sqlDNSResult{}}
	e.src, e.dst = sqlTestInstances()
	for _, d := range e.src.Databases {
		e.fake.src[d.Name] = &sqlFakeDB{status: "ONLINE", lsn: 1000, access: "MULTI_USER"}
	}
	e.r = newSQLRunner(filepath.Join(t.TempDir(), "sql-plans.json"))
	e.r.exec = e.fake.exec
	e.r.now = func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
	e.r.poll = time.Millisecond
	e.r.dns = func(_ context.Context, _ *sqlPlan, req sqlDNSRequest) (sqlDNSResult, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.dnsC = append(e.dnsC, req)
		cur := e.dns[req.Name]
		switch req.Mode {
		case "read":
			return cur, nil
		case "set":
			if cur.Type != "" && cur.Type != "CNAME" {
				return cur, errors.New("not a CNAME")
			}
			e.dns[req.Name] = sqlDNSResult{Type: "CNAME", Value: req.Target + "."}
		case "remove":
			if strings.TrimSuffix(cur.Value, ".") != req.Expect {
				return cur, errors.New("changed by someone else")
			}
			delete(e.dns, req.Name)
		case "spn":
			return sqlDNSResult{SPNOwner: `CORP\svc-sql`}, nil
		}
		return sqlDNSResult{}, nil
	}
	if err := e.r.Load(); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *sqlTestEnv) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *sqlTestEnv) tick(t *testing.T) {
	t.Helper()
	e.r.Tick()
	e.idle(t)
}

func (e *sqlTestEnv) idle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(e.r.Running()) == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("still running: %v", e.r.Running())
}

func (e *sqlTestEnv) create(t *testing.T, dbs ...sqlDBInput) *sqlPlan {
	t.Helper()
	p, _, err := e.r.Create(sqlCreateInput{Name: "Plan A", Source: sqlEndpoint{Server: "OLDSQL"}, Target: sqlEndpoint{Server: "NEWSQL"}, BackupPath: `\\files\sqlmove`, Databases: dbs}, e.src, e.dst)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *sqlTestEnv) db(t *testing.T, planID, name string) sqlDB {
	t.Helper()
	for _, p := range e.r.View() {
		if p.ID == planID {
			for _, d := range p.Databases {
				if d.Name == name {
					return *d
				}
			}
		}
	}
	t.Fatalf("%s not found", name)
	return sqlDB{}
}

func TestSQLQuotingNeverBreaksOut(t *testing.T) {
	if got := sqlName("a]; DROP DATABASE x; --"); got != "[a]]; DROP DATABASE x; --]" {
		t.Fatalf("sqlName: %s", got)
	}
	if got := sqlStr("it's"); got != "N'it''s'" {
		t.Fatalf("sqlStr: %s", got)
	}
	b := sqlRestoreFullBatch("x'y]", `\\s\f\x'y.bak`, nil, true, false)
	if !strings.Contains(b.SQL, "[x'y]]]") || !strings.Contains(b.SQL, `N'\\s\f\x''y.bak'`) || strings.Contains(b.SQL, "REPLACE") {
		t.Fatalf("restore: %s", b.SQL)
	}
}

func TestSQLRecommendationsAndChecks(t *testing.T) {
	src, dst := sqlTestInstances()
	if c := sqlRecommend(src, dst, src.database("Sales")); c.Method != sqlMethodLogShip {
		t.Fatalf("large full-recovery database: %+v", c)
	}
	if c := sqlRecommend(src, dst, src.database("Small")); c.Method != sqlMethodBackup || c.Unavailable[sqlMethodLogShip] == "" {
		t.Fatalf("simple recovery: %+v", c)
	}
	if c := sqlRecommend(src, dst, src.database("Sales")); c.Unavailable[sqlMethodDAG] == "" || !strings.Contains(c.Unavailable[sqlMethodDAG], "not in an availability group") {
		t.Fatalf("a standalone database must not be offered a distributed AG: %+v", c)
	}
	p := &sqlPlan{BackupPath: `\\f\s`, Databases: []*sqlDB{{Name: "Small", Method: sqlMethodLogShip}, {Name: "master", Method: sqlMethodBackup}}}
	codes := map[string]bool{}
	for _, f := range sqlCheckPlan(src, dst, p) {
		if f.Severity == "blocker" {
			codes[f.Code] = true
		}
	}
	if !codes["simple"] || !codes["system"] {
		t.Fatalf("blockers: %v", codes)
	}
	dst.Databases = []sqlDatabase{{Name: "Sales", State: "ONLINE"}}
	old := *dst
	old.Major = 12
	p = &sqlPlan{BackupPath: `\\f\s`, Databases: []*sqlDB{{Name: "Sales", Method: sqlMethodLogShip}}}
	codes = map[string]bool{}
	for _, f := range sqlCheckPlan(src, &old, p) {
		codes[f.Code] = true
	}
	if !codes["exists"] || !codes["older"] {
		t.Fatalf("expected exists and older: %v", codes)
	}
	if err := sqlCheckAlias(sqlAlias{Name: "oldsql", Zone: "corp.example.com", DNSServer: "DC1"}, src); err == nil {
		t.Fatal("the old server's own name was accepted as an alias")
	}
}

func TestSQLLogShippingSeedsSyncsTestsCutsOverAndRollsBack(t *testing.T) {
	e := newSQLTestEnv(t)
	p := e.create(t, sqlDBInput{Name: "Sales"})
	if d := e.db(t, p.ID, "Sales"); d.Method != sqlMethodLogShip || d.State != sqlWaiting {
		t.Fatalf("created: %+v", d)
	}
	e.tick(t)
	d := e.db(t, p.ID, "Sales")
	if d.State != sqlReady || d.Seeded.IsZero() || e.fake.dst["Sales"].status != "RESTORING" {
		t.Fatalf("after seed: %+v %+v", d, e.fake.dst["Sales"])
	}
	for _, s := range e.fake.sqlSeen {
		if strings.Contains(s, "RESTORE DATABASE") && strings.Contains(s, "REPLACE") {
			t.Fatal("the first restore used REPLACE")
		}
		if strings.Contains(s, "BACKUP DATABASE") && !strings.Contains(s, "COPY_ONLY") {
			t.Fatal("the seed backup was not copy-only, so it changed the source's differential base")
		}
	}
	// A round runs only once the interval has passed.
	e.tick(t)
	if n := e.fake.count("backuplog"); n != 0 {
		t.Fatalf("log shipped too early: %d", n)
	}
	e.advance(16 * time.Minute)
	e.fake.src["Sales"].lsn += 100 // work on the old server
	e.tick(t)
	if n := e.fake.count("restorelog"); n != 1 {
		t.Fatalf("log rounds restored: %d", n)
	}
	if e.fake.dst["Sales"].lsn != e.fake.src["Sales"].lsn {
		t.Fatalf("new copy at %d, old at %d", e.fake.dst["Sales"].lsn, e.fake.src["Sales"].lsn)
	}
	// Test copy.
	if err := e.r.StartTest(p.ID, "Sales"); err != nil {
		t.Fatal(err)
	}
	e.idle(t)
	d = e.db(t, p.ID, "Sales")
	if d.Test == nil || e.fake.dst["Sales_TEST"] == nil || e.fake.dst["Sales_TEST"].status != "ONLINE" {
		t.Fatalf("test copy: %+v note %q", d.Test, d.Note)
	}
	if _, err := e.r.Cutover(p.ID, []string{"Sales"}, time.Time{}); err == nil {
		t.Fatal("cutover allowed while a test copy exists")
	}
	// A database of the same name made by someone else is never dropped.
	e.fake.dst["Sales_TEST"].id = 999
	if err := e.r.EndTest(p.ID, "Sales"); err == nil || e.fake.dst["Sales_TEST"] == nil {
		t.Fatalf("End test removed a database it did not make: %v", err)
	}
	e.fake.dst["Sales_TEST"].id = d.Test.ID
	if err := e.r.EndTest(p.ID, "Sales"); err != nil || e.fake.dst["Sales_TEST"] != nil {
		t.Fatalf("End test: %v", err)
	}
	if d = e.db(t, p.ID, "Sales"); d.TestedAt.IsZero() || d.State != sqlReady {
		t.Fatalf("after test: %+v", d)
	}
	// Cutover.
	if _, err := e.r.Cutover(p.ID, []string{"Sales"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.idle(t)
	e.idle(t)
	d = e.db(t, p.ID, "Sales")
	if d.State != sqlDone || d.SourceHeld != "restoring" {
		t.Fatalf("after cutover: %+v", d)
	}
	if e.fake.src["Sales"].status != "RESTORING" || e.fake.dst["Sales"].status != "ONLINE" || e.fake.dst["Sales"].lsn != e.fake.src["Sales"].lsn {
		t.Fatalf("servers after cutover: old %+v new %+v", e.fake.src["Sales"], e.fake.dst["Sales"])
	}
	// Rollback: old online, new offline but kept.
	if err := e.r.Rollback(context.Background(), p.ID, []string{"Sales"}); err != nil {
		t.Fatal(err)
	}
	d = e.db(t, p.ID, "Sales")
	if d.State != sqlRolledBack || e.fake.src["Sales"].status != "ONLINE" || e.fake.src["Sales"].access != "MULTI_USER" {
		t.Fatalf("after rollback: %+v %+v", d, e.fake.src["Sales"])
	}
	if e.fake.dst["Sales"] == nil || e.fake.dst["Sales"].status != "OFFLINE" {
		t.Fatalf("the new copy must be taken offline and kept: %+v", e.fake.dst["Sales"])
	}
	for _, s := range e.fake.sqlSeen {
		if strings.Contains(s, "DROP DATABASE [Sales]") {
			t.Fatal("rollback dropped a database")
		}
	}
}

func TestSQLBackupCutoverBringsTheOldDatabaseBackWhenTheRestoreFails(t *testing.T) {
	e := newSQLTestEnv(t)
	p := e.create(t, sqlDBInput{Name: "Small"})
	e.tick(t)
	if n := e.fake.count("backupfull"); n != 0 {
		t.Fatal("a backup and restore database was copied before its cutover")
	}
	e.fake.fail["restorefull"] = "Not enough disk space"
	if _, err := e.r.Cutover(p.ID, []string{"Small"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.idle(t)
	d := e.db(t, p.ID, "Small")
	if d.State != sqlFailed || !strings.Contains(d.Error, "brought back online") {
		t.Fatalf("failed cutover: %+v", d)
	}
	if s := e.fake.src["Small"]; s.readOnly || s.status != "ONLINE" {
		t.Fatalf("old database left stopped: %+v", s)
	}
	// Cut over again: works.
	if _, err := e.r.Cutover(p.ID, []string{"Small"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.idle(t)
	if d = e.db(t, p.ID, "Small"); d.State != sqlDone || !e.fake.src["Small"].readOnly || e.fake.dst["Small"].status != "ONLINE" {
		t.Fatalf("second cutover: %+v", d)
	}
	if err := e.r.Rollback(context.Background(), p.ID, []string{"Small"}); err != nil || e.fake.src["Small"].readOnly {
		t.Fatalf("rollback: %v %+v", err, e.fake.src["Small"])
	}
}

func TestSQLLogShippingRestoresAnotherJobsLogBackups(t *testing.T) {
	e := newSQLTestEnv(t)
	p := e.create(t, sqlDBInput{Name: "Sales"})
	e.tick(t)
	// Another job backs up the log to a file on the old server.
	s := e.fake.src["Sales"]
	s.lsn += 7
	e.fake.files[`\\OLDSQL\D$\Backup\Sales_1.trn`] = sqlFakeBackup{db: "Sales", kind: "log", first: s.logBase, last: s.lsn}
	e.fake.other = []map[string]any{{"path": `D:\Backup\Sales_1.trn`, "deviceType": 2, "firstLsn": fmt.Sprint(s.logBase), "lastLsn": fmt.Sprint(s.lsn)}}
	s.logBase = s.lsn
	e.advance(16 * time.Minute)
	e.tick(t)
	d := e.db(t, p.ID, "Sales")
	if d.State != sqlReady || e.fake.dst["Sales"].lsn != s.lsn {
		t.Fatalf("gap not filled: %+v (new at %d, old at %d)", d, e.fake.dst["Sales"].lsn, s.lsn)
	}
	// A backup product's own device cannot be restored: the round fails
	// with the reason and the copy is left as it was.
	s.lsn += 7
	e.fake.other = []map[string]any{{"path": "{GUID-1}", "deviceType": 7, "firstLsn": fmt.Sprint(s.logBase), "lastLsn": fmt.Sprint(s.lsn)}}
	s.logBase = s.lsn
	e.advance(16 * time.Minute)
	e.tick(t)
	d = e.db(t, p.ID, "Sales")
	if d.State != sqlFailed || !strings.Contains(d.Error, "its own storage") {
		t.Fatalf("expected a refusal: %+v", d)
	}
	if err := e.r.Reseed(p.ID, "Sales"); err != nil {
		t.Fatal(err)
	}
	e.fake.other = nil
	e.idle(t)
	if d = e.db(t, p.ID, "Sales"); d.State != sqlReady {
		t.Fatalf("after copy again: %+v", d)
	}
	reseed := false
	for _, q := range e.fake.sqlSeen {
		if strings.Contains(q, "WITH REPLACE") {
			reseed = strings.Contains(q, "'RESTORING'")
		}
	}
	if !reseed {
		t.Fatal("copy again must replace only a copy that is still restoring")
	}
}

func TestSQLDistributedAGCutsOverAndFailsBack(t *testing.T) {
	e := newSQLTestEnv(t)
	for _, i := range []*sqlInstance{e.src, e.dst} {
		i.EngineEdition, i.Edition, i.Hadr, i.Major = 3, "Enterprise Edition", true, 16
		i.Endpoint = sqlHadrEP{Name: "Hadr_endpoint", Port: 5022, Auth: "WINDOWS NEGOTIATE"}
	}
	e.src.AGs = []sqlAG{{Name: "AG1", Role: "PRIMARY", ClusterType: "WSFC", Listener: "ag1-listener", Databases: []string{"Sales"}}}
	e.src.Databases[0].AG = "AG1"
	p := e.create(t, sqlDBInput{Name: "Sales"})
	if d := e.db(t, p.ID, "Sales"); d.Method != sqlMethodDAG {
		t.Fatalf("an AG database should default to a distributed AG: %+v", d)
	}
	e.tick(t)
	if !e.fake.dagExists || e.fake.count("createag") != 1 {
		t.Fatalf("DAG not created: %v", e.fake.kinds)
	}
	e.fake.dagState["dst:Sales"], e.fake.dagState["src:Sales"] = "SYNCHRONIZED", "SYNCHRONIZED"
	e.fake.hardened["dst:Sales"], e.fake.hardened["src:Sales"] = "500", "500"
	e.advance(2 * time.Minute)
	e.tick(t)
	if d := e.db(t, p.ID, "Sales"); d.State != sqlReady || d.Seeded.IsZero() {
		t.Fatalf("not seeded: %+v", d)
	}
	if _, err := e.r.Cutover(p.ID, []string{"Sales"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.idle(t)
	if d := e.db(t, p.ID, "Sales"); d.State != sqlDone || e.fake.dagRole != "NEWPRIMARY" {
		t.Fatalf("DAG cutover: %+v role %s err", d, e.fake.dagRole)
	}
	order := strings.Join(e.fake.kinds, ",")
	if strings.Index(order, "source:dagsecondary") > strings.Index(order, "target:dagfailover") {
		t.Fatalf("the old AG must be secondary before the new one takes over: %s", order)
	}
	if err := e.r.Rollback(context.Background(), p.ID, []string{"Sales"}); err != nil {
		t.Fatal(err)
	}
	if d := e.db(t, p.ID, "Sales"); d.State != sqlReady || e.fake.dagRole != "PRIMARY" {
		t.Fatalf("fail back: %+v role %s", d, e.fake.dagRole)
	}
}

func TestSQLDistributedAGFailsBackWhenTheNewSideDoesNotCatchUp(t *testing.T) {
	e := newSQLTestEnv(t)
	for _, i := range []*sqlInstance{e.src, e.dst} {
		i.EngineEdition, i.Edition, i.Hadr, i.Major = 3, "Enterprise Edition", true, 16
		i.Endpoint = sqlHadrEP{Name: "Hadr_endpoint", Port: 5022, Auth: "WINDOWS"}
	}
	e.src.AGs = []sqlAG{{Name: "AG1", Role: "PRIMARY", Databases: []string{"Sales"}}}
	e.src.Databases[0].AG = "AG1"
	p := e.create(t, sqlDBInput{Name: "Sales"})
	e.tick(t)
	e.fake.dagState["dst:Sales"], e.fake.dagState["src:Sales"] = "SYNCHRONIZED", "SYNCHRONIZED"
	e.fake.hardened["dst:Sales"], e.fake.hardened["src:Sales"] = "500", "500"
	e.advance(2 * time.Minute)
	e.tick(t)
	e.fake.fail["dagfailover"] = "The availability group is not ready"
	if _, err := e.r.Cutover(p.ID, []string{"Sales"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.idle(t)
	d := e.db(t, p.ID, "Sales")
	if d.State != sqlFailed || !strings.Contains(d.Error, "made primary again") || e.fake.dagRole != "PRIMARY" {
		t.Fatalf("failed DAG cutover: %+v role %s", d, e.fake.dagRole)
	}
}

func TestSQLAliasMovesAndIsPutBack(t *testing.T) {
	e := newSQLTestEnv(t)
	p, _, err := e.r.Create(sqlCreateInput{Name: "Plan B", Source: sqlEndpoint{Server: "OLDSQL"}, Target: sqlEndpoint{Server: "NEWSQL"}, BackupPath: `\\files\sqlmove`,
		Alias: sqlAlias{Name: "sql-app", Zone: "corp.example.com", DNSServer: "DC1", SPN: true, AtCutover: true}, Databases: []sqlDBInput{{Name: "Small"}}}, e.src, e.dst)
	if err != nil {
		t.Fatal(err)
	}
	e.dns["sql-app"] = sqlDNSResult{Type: "CNAME", Value: "oldsql.corp.example.com."}
	if _, err := e.r.Cutover(p.ID, []string{"Small"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e.idle(t)
	if got := e.dns["sql-app"].Value; got != "newsql.corp.example.com." {
		t.Fatalf("alias not moved at cutover: %q (%+v)", got, e.r.View()[0].Note)
	}
	if err := e.r.Rollback(context.Background(), p.ID, []string{"Small"}); err != nil {
		t.Fatal(err)
	}
	if got := e.dns["sql-app"].Value; got != "oldsql.corp.example.com." {
		t.Fatalf("alias not put back: %q", got)
	}
	// An address record is never changed.
	e.dns["sql-app"] = sqlDNSResult{Type: "A", Value: "192.0.2.10"}
	if err := e.r.MoveAlias(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "not a CNAME") {
		t.Fatalf("A record changed: %v", err)
	}
}

func TestSQLRestartMarksInterruptedWorkAndKeepsNoPassword(t *testing.T) {
	e := newSQLTestEnv(t)
	p := e.create(t, sqlDBInput{Name: "Sales"})
	e.r.mu.Lock()
	e.r.plans[0].Databases[0].State = sqlCutting
	e.r.saveLocked()
	e.r.mu.Unlock()
	raw, _ := os.ReadFile(e.r.path)
	if strings.Contains(strings.ToLower(string(raw)), "password") {
		t.Fatal("the plan file mentions a password")
	}
	r2 := newSQLRunner(e.r.path)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	d := r2.View()[0].Databases[0]
	if d.State != sqlFailed || !d.CutoverAt.IsZero() || !strings.Contains(d.Error, "Roll back") {
		t.Fatalf("interrupted cutover after restart: %+v", d)
	}
	if err := r2.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSQLScheduledCutoverWaitsAndCancelsWithoutACopy(t *testing.T) {
	e := newSQLTestEnv(t)
	p := e.create(t, sqlDBInput{Name: "Sales", CutoverAt: time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)})
	e.r.mu.Lock()
	e.r.plans[0].StartAt = e.now.Add(time.Hour) // not started yet
	e.r.mu.Unlock()
	e.tick(t)
	if d := e.db(t, p.ID, "Sales"); !d.CutoverAt.IsZero() || !strings.Contains(d.Note, "cancelled") {
		t.Fatalf("a cutover without a copy must be cancelled with a note: %+v", d)
	}
}

func TestSQLLSNComparison(t *testing.T) {
	big1, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	if !sqlLSNLess("5", big1.String()) || sqlLSNLess(big1.String(), "5") || sqlLSNLess("", "5") {
		t.Fatal("LSN comparison")
	}
}
