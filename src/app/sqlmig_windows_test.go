//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSQLMigrationOnLocalDB moves a real database between two SQL Server
// LocalDB instances on the runner with the signed engine script: read both
// servers, copy logins with their SIDs and passwords, seed by log shipping,
// ship the log, test, cut over (the old database is left restoring),
// check the rows on the new server and roll back. The CI job sqlmig
// installs LocalDB and sets SIMM_REQUIRE_LOCALDB so a missing LocalDB fails
// there instead of skipping. It runs only there: hosted runners already have
// LocalDB, and its two minutes pushed the main Windows job's go test past
// its ten-minute limit.
func TestSQLMigrationOnLocalDB(t *testing.T) {
	if os.Getenv("SIMM_REQUIRE_LOCALDB") == "" {
		t.Skip("runs in the CI job sqlmig (set SIMM_REQUIRE_LOCALDB=1 to run it here)")
	}
	tool, err := exec.LookPath("SqlLocalDB.exe")
	if err != nil {
		for _, v := range []string{"170", "160", "150", "140", "130"} {
			candidate := filepath.Join(os.Getenv("ProgramFiles"), "Microsoft SQL Server", v, "Tools", "Binn", "SqlLocalDB.exe")
			if _, statErr := os.Stat(candidate); statErr == nil {
				tool, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		t.Fatal("SqlLocalDB.exe was not found")
	}
	instances := []string{"SIMMSrc", "SIMMDst"}
	for _, name := range instances {
		exec.Command(tool, "stop", name, "-k").Run()
		exec.Command(tool, "delete", name).Run()
		if out, err := exec.Command(tool, "create", name, "-s").CombinedOutput(); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	t.Cleanup(func() {
		for _, name := range instances {
			exec.Command(tool, "stop", name, "-k").Run()
			exec.Command(tool, "delete", name).Run()
		}
	})
	folder := t.TempDir()
	a := &application{powerShellPath: powershellPath()}
	a.update.appDataRoot = filepath.Join(folder, "AppData")
	a.sqlmig.passwords = map[string]string{}
	src, dst := sqlEndpoint{Server: `(localdb)\SIMMSrc`, Auth: sqlAuthCurrent}, sqlEndpoint{Server: `(localdb)\SIMMDst`, Auth: sqlAuthCurrent}
	setup := &sqlPlan{ID: "setup", Source: src, Target: dst}
	ctx := context.Background()
	const secret = "Synthetic-SQL-Secret-7b2d!"
	dataDir := filepath.Join(folder, "SrcData")
	os.MkdirAll(dataDir, 0700)
	run := func(on, sql string) []sqlBatchResult {
		t.Helper()
		res, err := a.sqlExec(ctx, setup, []sqlBatch{{On: on, Name: "setup", SQL: sql, Timeout: 120}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) == 0 || !res[0].OK {
			t.Fatalf("setup failed: %+v", res)
		}
		return res
	}
	run(sqlOn, "CREATE DATABASE [Sales Test] ON (NAME = N'SalesData', FILENAME = N'"+filepath.Join(dataDir, "SalesTest.mdf")+"') LOG ON (NAME = N'SalesLog', FILENAME = N'"+filepath.Join(dataDir, "SalesTest_log.ldf")+"');")
	run(sqlOn, "ALTER DATABASE [Sales Test] SET RECOVERY FULL; CREATE LOGIN [simm_app] WITH PASSWORD = N'"+secret+"', CHECK_POLICY = OFF;")
	run(sqlOn, "USE [Sales Test]; CREATE TABLE dbo.Orders (ID int IDENTITY PRIMARY KEY, Item nvarchar(50)); INSERT dbo.Orders (Item) VALUES (N'one'), (N'two'); CREATE USER [simm_app] FOR LOGIN [simm_app];")

	// Read both servers as the page does.
	read := &sqlPlan{ID: "read", Source: src, Target: dst}
	srcInst, err := sqlReadSide(ctx, a.sqlExec, read, sqlOn)
	if err != nil {
		t.Fatal(err)
	}
	dstInst, err := sqlReadSide(ctx, a.sqlExec, read, sqlDst)
	if err != nil {
		t.Fatal(err)
	}
	db := srcInst.database("Sales Test")
	if db == nil || len(db.Files) != 2 || !strings.EqualFold(db.Recovery, "FULL") {
		t.Fatalf("the database was not read: %+v", db)
	}
	r := newSQLRunner(filepath.Join(folder, "AppData", "SQL", "sql-plans.json"))
	r.exec, r.poll = a.sqlExec, time.Second
	r.log = func(p *sqlPlan, text string) { t.Log(text) }
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	dstData := filepath.Join(folder, "DstData")
	os.MkdirAll(dstData, 0700)
	plan, findings, err := r.Create(sqlCreateInput{Name: "LocalDB round trip", Source: src, Target: dst, BackupPath: filepath.Join(folder, "Backups"), DataPath: dstData, LogPath: dstData,
		Objects: sqlObjectChoice{Logins: true}, Databases: []sqlDBInput{{Name: "Sales Test", Method: sqlMethodLogShip}}}, srcInst, dstInst)
	if err != nil {
		t.Fatalf("%v %+v", err, findings)
	}
	idle := func() {
		deadline := time.Now().Add(5 * time.Minute)
		for len(r.Running()) > 0 && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
	}
	state := func() sqlDB { return *r.View()[0].Databases[0] }
	r.Tick() // objects
	idle()
	r.Tick() // seed
	idle()
	if d := state(); d.State != sqlReady {
		t.Fatalf("seed: %+v\nobjects: %s", d, r.View()[0].ObjectsNote)
	}
	run(sqlOn, "INSERT [Sales Test].dbo.Orders (Item) VALUES (N'three');")
	if _, err := r.SyncNow(plan.ID, []string{"Sales Test"}); err != nil {
		t.Fatal(err)
	}
	idle()
	if d := state(); d.State != sqlReady || len(d.Chain) < 2 {
		t.Fatalf("log round: %+v", d)
	}
	if err := r.StartTest(plan.ID, "Sales Test"); err != nil {
		t.Fatal(err)
	}
	idle()
	if d := state(); d.Test == nil {
		t.Fatalf("test copy: %s", d.Note)
	}
	res := run(sqlDst, "SELECT COUNT(*) AS n FROM [Sales Test_TEST].dbo.Orders;")
	if n := res[0].rows()[0].int("n"); n != 3 {
		t.Fatalf("test copy has %d rows", n)
	}
	if err := r.EndTest(plan.ID, "Sales Test"); err != nil {
		t.Fatal(err)
	}
	run(sqlOn, "INSERT [Sales Test].dbo.Orders (Item) VALUES (N'four');")
	if _, err := r.Cutover(plan.ID, []string{"Sales Test"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	idle()
	if d := state(); d.State != sqlDone {
		t.Fatalf("cutover: %+v", d)
	}
	res = run(sqlDst, "SELECT COUNT(*) AS n FROM [Sales Test].dbo.Orders; SELECT CONVERT(varchar(100), p.sid, 1) AS dst, CONVERT(varchar(100), u.sid, 1) AS usr FROM sys.server_principals p CROSS JOIN [Sales Test].sys.database_principals u WHERE p.name = N'simm_app' AND u.name = N'simm_app';")
	if n := res[0].Sets[0][0].int("n"); n != 4 {
		t.Fatalf("new database has %d rows", n)
	}
	if rows := res[0].rows(); len(rows) != 1 || rows[0].str("dst") != rows[0].str("usr") {
		t.Fatalf("the login was not copied with its SID: %+v", rows)
	}
	res = run(sqlOn, "SELECT CONVERT(nvarchar(60), DATABASEPROPERTYEX(N'Sales Test', 'Status')) AS status;")
	if s := res[0].rows()[0].str("status"); s != "RESTORING" {
		t.Fatalf("old database after cutover: %s", s)
	}
	if err := r.Rollback(ctx, plan.ID, []string{"Sales Test"}); err != nil {
		t.Fatal(err)
	}
	res = run(sqlOn, "SELECT COUNT(*) AS n FROM [Sales Test].dbo.Orders;")
	if n := res[0].rows()[0].int("n"); n != 4 {
		t.Fatalf("old database after rollback has %d rows", n)
	}
	res = run(sqlDst, "SELECT CONVERT(nvarchar(60), DATABASEPROPERTYEX(N'Sales Test', 'Status')) AS status;")
	if s := res[0].rows()[0].str("status"); s != "OFFLINE" {
		t.Fatalf("new copy after rollback: %s", s)
	}
	// Nothing the engine wrote holds the login's password.
	filepath.Walk(folder, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.HasSuffix(path, ".bak") && !strings.HasSuffix(path, ".trn") && !strings.HasSuffix(path, ".mdf") && !strings.HasSuffix(path, ".ldf") {
			if data, _ := os.ReadFile(path); strings.Contains(string(data), secret) {
				t.Errorf("%s holds the password", path)
			}
		}
		return nil
	})
	for _, line := range a.sqlState().Logs {
		if strings.Contains(line, secret) || strings.Contains(strings.ToUpper(line), "HASHED") {
			t.Fatalf("a log line holds a secret: %s", line)
		}
	}
}
