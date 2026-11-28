package main

// Every T-SQL statement the SQL Server migration runs. Names always go
// through sqlName (brackets, ] doubled) and values through sqlStr (N'...',
// ' doubled); nothing typed by an operator reaches a statement any other
// way. Statements are written for SQL Server 2008 and later (no THROW, no
// IIF, no CONCAT), and anything newer is reached through sp_executesql
// behind a version check.

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// sqlBatch is one batch run on the old (source) or new (target) server.
// Its text is never logged: logins carry password hashes and certificates
// carry passwords. Kind and Args describe it for tests.
type sqlBatch struct {
	On       string `json:"on"`
	Name     string `json:"name"`
	SQL      string `json:"sql"`
	Timeout  int    `json:"timeout"`
	Continue bool   `json:"continue,omitempty"`

	Kind string            `json:"-"`
	Args map[string]string `json:"-"`
}

type sqlBatchResult struct {
	Name     string     `json:"name"`
	OK       bool       `json:"ok"`
	Error    string     `json:"error"`
	Sets     [][]sqlRow `json:"sets"`
	Messages []string   `json:"messages"`
}

func (r sqlBatchResult) rows() []sqlRow {
	if len(r.Sets) == 0 {
		return nil
	}
	return r.Sets[len(r.Sets)-1]
}

func sqlName(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }

func sqlStr(s string) string { return "N'" + strings.ReplaceAll(s, "'", "''") + "'" }

func sqlBit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

const (
	sqlOn  = "source"
	sqlDst = "target"
)

// sqlBackupMarker labels this application's backups in msdb, so the checks
// tell them from other tools' backups.
func sqlBackupMarker(planID string) string { return "SIMM " + planID }

// ---------------------------------------------------------------------
// Reading an instance.

func sqlInfoBatch(on string) sqlBatch {
	return sqlBatch{On: on, Name: "info", Kind: "info", Timeout: 60, SQL: `SET NOCOUNT ON;
DECLARE @data nvarchar(512), @log nvarchar(512), @backup nvarchar(512), @dns nvarchar(256);
DECLARE @svc nvarchar(256), @agent nvarchar(256), @agentState nvarchar(60), @port int;
SELECT @data = CONVERT(nvarchar(512), SERVERPROPERTY('InstanceDefaultDataPath')), @log = CONVERT(nvarchar(512), SERVERPROPERTY('InstanceDefaultLogPath'));
BEGIN TRY
  IF @data IS NULL EXEC master.dbo.xp_instance_regread N'HKEY_LOCAL_MACHINE', N'Software\Microsoft\MSSQLServer\MSSQLServer', N'DefaultData', @data OUTPUT;
  IF @log IS NULL EXEC master.dbo.xp_instance_regread N'HKEY_LOCAL_MACHINE', N'Software\Microsoft\MSSQLServer\MSSQLServer', N'DefaultLog', @log OUTPUT;
  EXEC master.dbo.xp_instance_regread N'HKEY_LOCAL_MACHINE', N'Software\Microsoft\MSSQLServer\MSSQLServer', N'BackupDirectory', @backup OUTPUT;
  EXEC master.dbo.xp_regread N'HKEY_LOCAL_MACHINE', N'SYSTEM\CurrentControlSet\Services\Tcpip\Parameters', N'Domain', @dns OUTPUT;
END TRY BEGIN CATCH END CATCH;
IF @data IS NULL SELECT @data = LEFT(physical_name, LEN(physical_name) - CHARINDEX('\', REVERSE(physical_name))) FROM master.sys.master_files WHERE database_id = 1 AND file_id = 1;
IF @log IS NULL SELECT @log = LEFT(physical_name, LEN(physical_name) - CHARINDEX('\', REVERSE(physical_name))) FROM master.sys.master_files WHERE database_id = 1 AND file_id = 2;
IF OBJECT_ID('sys.dm_server_services') IS NOT NULL
  EXEC sp_executesql N'SELECT @s = service_account FROM sys.dm_server_services WHERE servicename LIKE N''SQL Server (%''; SELECT @a = service_account, @st = status_desc FROM sys.dm_server_services WHERE servicename LIKE N''SQL Server Agent (%'';', N'@s nvarchar(256) OUTPUT, @a nvarchar(256) OUTPUT, @st nvarchar(60) OUTPUT', @svc OUTPUT, @agent OUTPUT, @agentState OUTPUT;
SELECT @port = local_tcp_port FROM sys.dm_exec_connections WHERE session_id = @@SPID;
SELECT CONVERT(nvarchar(128), SERVERPROPERTY('MachineName')) AS machine,
  CONVERT(nvarchar(128), SERVERPROPERTY('InstanceName')) AS instance,
  CONVERT(nvarchar(256), @@SERVERNAME) AS serverName,
  CONVERT(nvarchar(64), SERVERPROPERTY('ProductVersion')) AS version,
  CONVERT(nvarchar(128), SERVERPROPERTY('Edition')) AS edition,
  CONVERT(int, SERVERPROPERTY('EngineEdition')) AS engineEdition,
  CONVERT(nvarchar(128), SERVERPROPERTY('Collation')) AS collation,
  ISNULL(CONVERT(int, SERVERPROPERTY('IsHadrEnabled')), 0) AS hadr,
  ISNULL(CONVERT(int, SERVERPROPERTY('IsClustered')), 0) AS clustered,
  ISNULL(CONVERT(int, SERVERPROPERTY('FilestreamEffectiveLevel')), 0) AS filestream,
  @data AS dataPath, @log AS logPath, @backup AS backupPath,
  @svc AS serviceAccount, @agent AS agentAccount, @agentState AS agentState,
  CONVERT(nvarchar(128), DEFAULT_DOMAIN()) AS domain, @dns AS dnsDomain, @port AS port,
  IS_SRVROLEMEMBER('sysadmin') AS sysadmin;`}
}

// sqlDetailBatches read databases, files, volumes, availability groups and
// the AG endpoint, with only the columns this version has.
func sqlDetailBatches(on string, major int, hadr bool) []sqlBatch {
	containment := "CONVERT(nvarchar(60), N'NONE')"
	if major >= 11 {
		containment = "d.containment_desc"
	}
	dbs := `SET NOCOUNT ON;
SELECT d.name, d.database_id AS id, d.state_desc AS state, d.recovery_model_desc AS recovery, d.compatibility_level AS compat,
  d.collation_name AS collation, d.is_read_only AS readOnly, d.user_access_desc AS userAccess, d.is_encrypted AS encrypted,
  SUSER_SNAME(d.owner_sid) AS owner, d.is_trustworthy_on AS trustworthy, d.is_db_chaining_on AS dbChaining,
  d.is_broker_enabled AS broker, d.is_cdc_enabled AS cdc,
  CASE WHEN d.is_published = 1 OR d.is_subscribed = 1 OR d.is_merge_published = 1 OR d.is_distributor = 1 THEN 1 ELSE 0 END AS replicated,
  CASE WHEN m.mirroring_guid IS NULL THEN 0 ELSE 1 END AS mirrored, ` + containment + ` AS containment,
  CONVERT(float, ISNULL((SELECT SUM(CONVERT(bigint, f.size)) FROM sys.master_files f WHERE f.database_id = d.database_id AND f.type <> 1), 0)) * 8 / 1024 AS dataMB,
  CONVERT(float, ISNULL((SELECT SUM(CONVERT(bigint, f.size)) FROM sys.master_files f WHERE f.database_id = d.database_id AND f.type = 1), 0)) * 8 / 1024 AS logMB,
  (SELECT COUNT(*) FROM sys.master_files f WHERE f.database_id = d.database_id AND f.type = 2) AS filestreamFiles,
  CASE WHEN rs.last_log_backup_lsn IS NULL THEN 1 ELSE 0 END AS noChain,
  CONVERT(varchar(30), (SELECT MAX(b.backup_finish_date) FROM msdb.dbo.backupset b WHERE b.database_name = d.name AND b.type = 'D'), 126) AS lastFull,
  (SELECT COUNT(*) FROM msdb.dbo.backupset b WHERE b.database_name = d.name AND b.type = 'L' AND b.is_copy_only = 0
     AND b.backup_finish_date > DATEADD(day, -7, GETDATE()) AND (b.description IS NULL OR b.description NOT LIKE N'SIMM %')) AS otherLogs,
  (SELECT COUNT(*) FROM msdb.dbo.backupset b JOIN msdb.dbo.backupmediafamily mf ON mf.media_set_id = b.media_set_id
     WHERE b.database_name = d.name AND b.type = 'L' AND b.is_copy_only = 0 AND b.backup_finish_date > DATEADD(day, -7, GETDATE())
     AND (b.description IS NULL OR b.description NOT LIKE N'SIMM %') AND mf.device_type IN (5, 7, 9)) AS otherLogsVirtual
FROM sys.databases d
LEFT JOIN sys.database_recovery_status rs ON rs.database_id = d.database_id
LEFT JOIN sys.database_mirroring m ON m.database_id = d.database_id
WHERE d.database_id > 4 AND d.source_database_id IS NULL;`
	files := `SET NOCOUNT ON;
SELECT DB_NAME(f.database_id) AS db, f.name AS logical, f.type_desc AS type, f.physical_name AS physical,
  CONVERT(float, f.size) * 8 / 1024 AS sizeMB, f.file_id AS fileId
FROM sys.master_files f WHERE f.database_id > 4 ORDER BY f.database_id, f.file_id;`
	volumes := `SET NOCOUNT ON;
IF OBJECT_ID('sys.dm_os_volume_stats') IS NOT NULL
  EXEC sp_executesql N'SELECT DISTINCT vs.volume_mount_point AS mount, CONVERT(float, vs.total_bytes) / 1048576 AS totalMB, CONVERT(float, vs.available_bytes) / 1048576 AS freeMB FROM sys.master_files f CROSS APPLY sys.dm_os_volume_stats(f.database_id, f.file_id) vs';
ELSE SELECT CONVERT(nvarchar(260), NULL) AS mount, CONVERT(float, 0) AS totalMB, CONVERT(float, 0) AS freeMB WHERE 1 = 0;`
	out := []sqlBatch{
		{On: on, Name: "databases", Kind: "databases", Timeout: 120, SQL: dbs},
		{On: on, Name: "files", Kind: "files", Timeout: 60, SQL: files},
		{On: on, Name: "volumes", Kind: "volumes", Timeout: 60, SQL: volumes},
	}
	ags := `SET NOCOUNT ON; SELECT CONVERT(nvarchar(128), NULL) AS ag WHERE 1 = 0;`
	ep := `SET NOCOUNT ON;
SELECT TOP 1 e.name, t.port, e.state_desc AS state, e.connection_auth_desc AS auth
FROM sys.database_mirroring_endpoints e JOIN sys.tcp_endpoints t ON t.endpoint_id = e.endpoint_id;`
	if major >= 11 && hadr {
		clusterType, distributed := "N'WSFC'", "0"
		if major >= 13 {
			distributed = "ag.is_distributed"
		}
		if major >= 14 {
			clusterType = "ag.cluster_type_desc"
		}
		ags = `SET NOCOUNT ON;
SELECT ag.name AS ag, ISNULL(ars.role_desc, N'') AS role, ` + clusterType + ` AS clusterType, ` + distributed + ` AS distributed,
  (SELECT TOP 1 l.dns_name FROM sys.availability_group_listeners l WHERE l.group_id = ag.group_id) AS listener,
  (SELECT TOP 1 l.port FROM sys.availability_group_listeners l WHERE l.group_id = ag.group_id) AS listenerPort,
  adc.database_name AS db
FROM sys.availability_groups ag
LEFT JOIN sys.dm_hadr_availability_replica_states ars ON ars.group_id = ag.group_id AND ars.is_local = 1
LEFT JOIN sys.availability_databases_cluster adc ON adc.group_id = ag.group_id
ORDER BY ag.name;`
	}
	out = append(out, sqlBatch{On: on, Name: "ags", Kind: "ags", Timeout: 60, SQL: ags}, sqlBatch{On: on, Name: "endpoint", Kind: "endpoint", Timeout: 60, SQL: ep})
	return out
}

// ---------------------------------------------------------------------
// Backups and restores.

// sqlFileSafe makes a database name usable in a file name.
func sqlFileSafe(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 60 {
		s = s[:60]
	}
	if s == "" {
		s = "db"
	}
	return s
}

// sqlJoin joins a Windows folder (a drive path or UNC path) and a name.
func sqlJoin(folder string, parts ...string) string {
	out := strings.TrimRight(folder, `\`)
	for _, p := range parts {
		out += `\` + strings.Trim(p, `\`)
	}
	return out
}

// sqlPlanFolder is the plan's own folder inside the backup folder.
func sqlPlanFolder(p *sqlPlan) string {
	return sqlJoin(p.BackupPath, "SIMM-"+p.ID)
}

func sqlBackupFile(p *sqlPlan, db, kind string, at time.Time) string {
	ext := ".trn"
	if kind == "full" || kind == "test" || kind == "copy" {
		ext = ".bak"
	}
	return sqlJoin(sqlPlanFolder(p), sqlFileSafe(db), fmt.Sprintf("%s_%s_%s%s", sqlFileSafe(db), kind, at.UTC().Format("20060102_150405"), ext))
}

func sqlCreateFolderBatch(on, folder string) sqlBatch {
	return sqlBatch{On: on, Name: "folder", Kind: "folder", Timeout: 60, Args: map[string]string{"path": folder},
		SQL: "SET NOCOUNT ON; EXEC master.dbo.xp_create_subdir " + sqlStr(folder) + ";"}
}

// sqlFileExistsBatch checks the new server's SQL Server service can see a
// file the old one wrote.
func sqlFileExistsBatch(on, file string) sqlBatch {
	return sqlBatch{On: on, Name: "exists", Kind: "exists", Timeout: 60, Args: map[string]string{"path": file},
		SQL: "SET NOCOUNT ON; CREATE TABLE #f (e int, d int, p int); INSERT #f EXEC master.dbo.xp_fileexist " + sqlStr(file) + "; SELECT e AS fileExists, p AS parentExists FROM #f; DROP TABLE #f;"}
}

func sqlWith(opts ...string) string {
	out := []string{}
	for _, o := range opts {
		if o != "" {
			out = append(out, o)
		}
	}
	return strings.Join(out, ", ")
}

// sqlLSNSelect reads the LSN range msdb recorded for the backup written to
// file.
func sqlLSNSelect(db, file string) string {
	return "SELECT TOP 1 CONVERT(varchar(40), bs.first_lsn) AS firstLsn, CONVERT(varchar(40), bs.last_lsn) AS lastLsn, CONVERT(varchar(40), bs.database_backup_lsn) AS baseLsn, bs.type AS kind FROM msdb.dbo.backupset bs JOIN msdb.dbo.backupmediafamily mf ON mf.media_set_id = bs.media_set_id WHERE bs.database_name = " + sqlStr(db) + " AND mf.physical_device_name = " + sqlStr(file) + " ORDER BY bs.backup_set_id DESC;"
}

// sqlBackupFullBatch takes a full backup. copyOnly leaves the database's
// differential base alone; a database that never had a full backup needs
// an ordinary one, because log backups need a chain to follow.
func sqlBackupFullBatch(p *sqlPlan, db, file string, copyOnly, compress bool) sqlBatch {
	opts := sqlWith(map[bool]string{true: "COPY_ONLY"}[copyOnly], "INIT", "FORMAT", "CHECKSUM", map[bool]string{true: "COMPRESSION"}[compress], "DESCRIPTION = "+sqlStr(sqlBackupMarker(p.ID)), "STATS = 10")
	return sqlBatch{On: sqlOn, Name: "backup " + db, Kind: "backupfull", Timeout: 0, Args: map[string]string{"db": db, "path": file, "copyOnly": sqlBit(copyOnly)},
		SQL: "SET NOCOUNT ON;\nBACKUP DATABASE " + sqlName(db) + " TO DISK = " + sqlStr(file) + " WITH " + opts + ";\n" + sqlLSNSelect(db, file)}
}

// sqlBackupLogBatch backs up the log. tail stops the database for
// cutover: it is set to single user (other sessions are rolled back) and
// the tail of the log is backed up WITH NORECOVERY, which leaves the old
// database restoring, so nothing more is written there and rollback can
// bring it back unchanged.
func sqlBackupLogBatch(p *sqlPlan, db, file string, compress, tail bool) sqlBatch {
	opts := sqlWith("INIT", "FORMAT", "CHECKSUM", map[bool]string{true: "COMPRESSION"}[compress], map[bool]string{true: "NORECOVERY"}[tail], "DESCRIPTION = "+sqlStr(sqlBackupMarker(p.ID)))
	sql := "SET NOCOUNT ON;\n"
	if tail {
		sql += "ALTER DATABASE " + sqlName(db) + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE;\n"
	}
	sql += "BACKUP LOG " + sqlName(db) + " TO DISK = " + sqlStr(file) + " WITH " + opts + ";\n" + sqlLSNSelect(db, file)
	kind := "backuplog"
	if tail {
		kind = "backuptail"
	}
	return sqlBatch{On: sqlOn, Name: "log backup " + db, Kind: kind, Args: map[string]string{"db": db, "path": file}, SQL: sql}
}

// sqlReadOnlyBatch stops writes for a backup and restore cutover.
func sqlReadOnlyBatch(db string) sqlBatch {
	return sqlBatch{On: sqlOn, Name: "read only " + db, Kind: "readonly", Timeout: 600, Args: map[string]string{"db": db},
		SQL: "SET NOCOUNT ON; ALTER DATABASE " + sqlName(db) + " SET READ_ONLY WITH ROLLBACK IMMEDIATE;"}
}

// sqlRecoveryFullBatch switches a simple-recovery database to full before
// log shipping (only when the operator ticked it).
func sqlRecoveryFullBatch(db string) sqlBatch {
	return sqlBatch{On: sqlOn, Name: "full recovery " + db, Kind: "tofull", Timeout: 600, Args: map[string]string{"db": db},
		SQL: "SET NOCOUNT ON; ALTER DATABASE " + sqlName(db) + " SET RECOVERY FULL;"}
}

type sqlMove struct {
	Logical, Physical string
}

// sqlMoves places every file of the database under the new server's data
// and log folders, named after the new database so a copy under another
// name never meets the original's files.
func sqlMoves(target string, files []sqlFile, dataPath, logPath, suffix string) []sqlMove {
	out := []sqlMove{}
	safe := sqlFileSafe(target)
	for _, f := range files {
		folder := dataPath
		if f.Type == "LOG" {
			folder = logPath
		}
		ext := strings.ToLower(path.Ext(strings.ReplaceAll(f.Physical, `\`, "/")))
		switch f.Type {
		case "FILESTREAM", "FULLTEXT":
			ext = ""
		default:
			if ext == "" || len(ext) > 5 {
				ext = map[bool]string{true: ".ldf", false: ".ndf"}[f.Type == "LOG"]
				if f.FileID == 1 {
					ext = ".mdf"
				}
			}
		}
		name := safe + suffix + "_" + sqlFileSafe(f.Logical) + ext
		out = append(out, sqlMove{Logical: f.Logical, Physical: sqlJoin(folder, name)})
	}
	return out
}

func sqlMoveClause(moves []sqlMove) string {
	parts := []string{}
	for _, m := range moves {
		parts = append(parts, "MOVE "+sqlStr(m.Logical)+" TO "+sqlStr(m.Physical))
	}
	return strings.Join(parts, ", ")
}

// sqlRestoreFullBatch restores a full backup under the target name. It
// never replaces a database: without REPLACE SQL Server refuses a name or
// file already in use.
func sqlRestoreFullBatch(target, file string, moves []sqlMove, norecovery, keepCDC bool) sqlBatch {
	opts := sqlWith(sqlMoveClause(moves), map[bool]string{true: "NORECOVERY", false: "RECOVERY"}[norecovery], "CHECKSUM", map[bool]string{true: "KEEP_CDC"}[keepCDC && !norecovery], "STATS = 10")
	return sqlBatch{On: sqlDst, Name: "restore " + target, Kind: "restorefull", Args: map[string]string{"db": target, "path": file, "norecovery": sqlBit(norecovery)},
		SQL: "SET NOCOUNT ON;\nRESTORE DATABASE " + sqlName(target) + " FROM DISK = " + sqlStr(file) + " WITH " + opts + ";"}
}

func sqlRestoreLogBatch(target, file string) sqlBatch {
	return sqlBatch{On: sqlDst, Name: "restore log " + target, Kind: "restorelog", Args: map[string]string{"db": target, "path": file},
		SQL: "SET NOCOUNT ON;\nRESTORE LOG " + sqlName(target) + " FROM DISK = " + sqlStr(file) + " WITH NORECOVERY, CHECKSUM;"}
}

func sqlRecoverBatch(on, db string, keepCDC bool) sqlBatch {
	return sqlBatch{On: on, Name: "recover " + db, Kind: "recover", Timeout: 0, Args: map[string]string{"db": db},
		SQL: "SET NOCOUNT ON;\nRESTORE DATABASE " + sqlName(db) + " WITH " + sqlWith("RECOVERY", map[bool]string{true: "KEEP_CDC"}[keepCDC]) + ";"}
}

// sqlAfterRestoreBatch puts back what a restore does not carry or a
// cutover changed: the old database's user access (the tail backup set it
// to single user) and read-write state, TRUSTWORTHY and cross-database
// chaining (a restore turns both off), the owner when that login exists,
// and maps database users whose login has the same name but another SID.
func sqlAfterRestoreBatch(target string, src *sqlDatabase, dstMajor int) sqlBatch {
	q := sqlName(target)
	var b strings.Builder
	b.WriteString("SET NOCOUNT ON;\n")
	access := strings.ToUpper(src.UserAccess)
	if access != "SINGLE_USER" && access != "RESTRICTED_USER" {
		access = "MULTI_USER"
	}
	b.WriteString("ALTER DATABASE " + q + " SET " + access + ";\n")
	if !src.ReadOnly {
		b.WriteString("IF DATABASEPROPERTYEX(" + sqlStr(target) + ", 'Updateability') = 'READ_ONLY' ALTER DATABASE " + q + " SET READ_WRITE WITH ROLLBACK IMMEDIATE;\n")
	}
	if src.Trustworthy {
		b.WriteString("ALTER DATABASE " + q + " SET TRUSTWORTHY ON;\n")
	}
	if src.DBChaining {
		b.WriteString("ALTER DATABASE " + q + " SET DB_CHAINING ON;\n")
	}
	if src.Owner != "" {
		b.WriteString("IF EXISTS (SELECT 1 FROM sys.server_principals WHERE name = " + sqlStr(src.Owner) + ") AND SUSER_SNAME((SELECT owner_sid FROM sys.databases WHERE name = " + sqlStr(target) + ")) <> " + sqlStr(src.Owner) + " ALTER AUTHORIZATION ON DATABASE::" + q + " TO " + sqlName(src.Owner) + ";\n")
	}
	// Contained users (SQL Server 2012 and later) have no login to map to.
	contained := ""
	if dstMajor >= 11 {
		contained = " AND dp.authentication_type_desc <> ''DATABASE''"
	}
	b.WriteString(`DECLARE @fix nvarchar(max);
SET @fix = N'DECLARE @u sysname, @s nvarchar(600);
DECLARE c CURSOR LOCAL FAST_FORWARD FOR
  SELECT dp.name FROM sys.database_principals dp JOIN sys.server_principals sp ON sp.name = dp.name COLLATE DATABASE_DEFAULT
  WHERE dp.type = ''S'' AND sp.type = ''S'' AND dp.sid <> sp.sid AND dp.principal_id > 4` + contained + `;
OPEN c; FETCH NEXT FROM c INTO @u;
WHILE @@FETCH_STATUS = 0 BEGIN SET @s = N''ALTER USER '' + QUOTENAME(@u) + N'' WITH LOGIN = '' + QUOTENAME(@u); EXEC (@s); FETCH NEXT FROM c INTO @u; END;
CLOSE c; DEALLOCATE c;';
`)
	b.WriteString("EXEC " + q + ".sys.sp_executesql @fix;\n")
	return sqlBatch{On: sqlDst, Name: "finish " + target, Kind: "after", Timeout: 600, Args: map[string]string{"db": target}, SQL: b.String()}
}

// ---------------------------------------------------------------------
// Tests: a separate copy, never the one being kept in sync.

// sqlTestInfoSelect returns the test database's ID and creation time, which
// End test checks before it removes it.
func sqlTestInfoSelect(name string) string {
	return "SELECT database_id AS id, CONVERT(varchar(30), create_date, 126) AS created FROM sys.databases WHERE name = " + sqlStr(name) + ";"
}

func sqlTestRestoreBatches(test, full string, logs []string, moves []sqlMove, keepCDC bool) []sqlBatch {
	out := []sqlBatch{}
	last := len(logs) == 0
	out = append(out, sqlRestoreFullBatch(test, full, moves, !last, keepCDC))
	for i, l := range logs {
		b := sqlRestoreLogBatch(test, l)
		if i == len(logs)-1 {
			b.SQL = strings.Replace(b.SQL, "WITH NORECOVERY", "WITH RECOVERY", 1)
		}
		out = append(out, b)
	}
	out[len(out)-1].SQL += "\nALTER DATABASE " + sqlName(test) + " SET MULTI_USER;\n" + sqlTestInfoSelect(test)
	for i := range out {
		out[i].Name = "test " + out[i].Name
	}
	return out
}

// sqlSnapshotBatch makes a database snapshot of a DAG secondary for a test.
func sqlSnapshotBatch(test, db string, files []sqlFile, dataPath string) sqlBatch {
	parts := []string{}
	for _, f := range files {
		if f.Type != "ROWS" {
			continue
		}
		parts = append(parts, "(NAME = "+sqlStr(f.Logical)+", FILENAME = "+sqlStr(sqlJoin(dataPath, sqlFileSafe(test)+"_"+sqlFileSafe(f.Logical)+".ss"))+")")
	}
	return sqlBatch{On: sqlDst, Name: "test snapshot " + db, Kind: "snapshot", Timeout: 600, Args: map[string]string{"db": test, "of": db},
		SQL: "SET NOCOUNT ON;\nCREATE DATABASE " + sqlName(test) + " ON " + strings.Join(parts, ", ") + " AS SNAPSHOT OF " + sqlName(db) + ";\n" + sqlTestInfoSelect(test)}
}

// sqlDropTestBatch removes the test copy only when it is the same database
// the test made (same name, ID and creation time).
func sqlDropTestBatch(name string, id int, created string) sqlBatch {
	cond := "name = " + sqlStr(name) + " AND database_id = " + fmt.Sprint(id) + " AND CONVERT(varchar(30), create_date, 126) = " + sqlStr(created)
	return sqlBatch{On: sqlDst, Name: "end test " + name, Kind: "droptest", Timeout: 600, Args: map[string]string{"db": name},
		SQL: "SET NOCOUNT ON;\nIF NOT EXISTS (SELECT 1 FROM sys.databases WHERE name = " + sqlStr(name) + ")\n  SELECT 0 AS removed;\nELSE IF EXISTS (SELECT 1 FROM sys.databases WHERE " + cond + ")\nBEGIN\n  IF EXISTS (SELECT 1 FROM sys.databases WHERE " + cond + " AND source_database_id IS NULL) ALTER DATABASE " + sqlName(name) + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE;\n  DROP DATABASE " + sqlName(name) + ";\n  SELECT 1 AS removed;\nEND\nELSE RAISERROR(N'The database named %s is not the test copy this application made, so it was left alone.', 16, 1, " + sqlStr(name) + ");"}
}

// ---------------------------------------------------------------------
// Rollback.

// sqlRollbackSourceBatch brings the old database back: a database left
// restoring by the tail backup is recovered (nothing restored on top, so
// it is exactly as it was), a read-only one is made writable again, and
// the user access it had is put back.
func sqlRollbackSourceBatch(db string, src *sqlDatabase, method string) sqlBatch {
	q := sqlName(db)
	var b strings.Builder
	b.WriteString("SET NOCOUNT ON;\n")
	b.WriteString("IF DATABASEPROPERTYEX(" + sqlStr(db) + ", 'Status') = 'RESTORING' RESTORE DATABASE " + q + " WITH RECOVERY;\n")
	if !src.ReadOnly {
		b.WriteString("IF DATABASEPROPERTYEX(" + sqlStr(db) + ", 'Updateability') = 'READ_ONLY' ALTER DATABASE " + q + " SET READ_WRITE WITH ROLLBACK IMMEDIATE;\n")
	}
	access := strings.ToUpper(src.UserAccess)
	if access != "SINGLE_USER" && access != "RESTRICTED_USER" {
		access = "MULTI_USER"
	}
	b.WriteString("ALTER DATABASE " + q + " SET " + access + ";\n")
	b.WriteString("SELECT CONVERT(nvarchar(60), DATABASEPROPERTYEX(" + sqlStr(db) + ", 'Status')) AS status;")
	return sqlBatch{On: sqlOn, Name: "bring back " + db, Kind: "rollbacksource", Timeout: 0, Args: map[string]string{"db": db, "method": method}, SQL: b.String()}
}

// sqlOfflineBatch takes the new copy offline during a rollback. It is never
// dropped: the copy and its files stay for review.
func sqlOfflineBatch(target string) sqlBatch {
	return sqlBatch{On: sqlDst, Name: "offline " + target, Kind: "offline", Timeout: 600, Args: map[string]string{"db": target},
		SQL: "SET NOCOUNT ON;\nIF DB_ID(" + sqlStr(target) + ") IS NOT NULL AND DATABASEPROPERTYEX(" + sqlStr(target) + ", 'Status') = 'ONLINE' ALTER DATABASE " + sqlName(target) + " SET OFFLINE WITH ROLLBACK IMMEDIATE;\nSELECT CONVERT(nvarchar(60), DATABASEPROPERTYEX(" + sqlStr(target) + ", 'Status')) AS status;"}
}

// ---------------------------------------------------------------------
// Distributed availability groups (only for databases already in an AG).

func sqlEndpointURL(host string, port int) string {
	if port == 0 {
		port = 5022
	}
	return fmt.Sprintf("TCP://%s:%d", host, port)
}

func sqlDAGReplica(ag, url string) string {
	return sqlStr(ag) + " WITH (LISTENER_URL = " + sqlStr(url) + ", AVAILABILITY_MODE = ASYNCHRONOUS_COMMIT, FAILOVER_MODE = MANUAL, SEEDING_MODE = AUTOMATIC)"
}

// sqlEndpointAccessBatch lets the other server's SQL Server service connect
// to this server's AG endpoint (Windows authentication).
func sqlEndpointAccessBatch(on, endpoint, account string) sqlBatch {
	return sqlBatch{On: on, Name: "endpoint access", Kind: "epgrant", Timeout: 120, Args: map[string]string{"account": account},
		SQL: "SET NOCOUNT ON;\nIF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = " + sqlStr(account) + ") CREATE LOGIN " + sqlName(account) + " FROM WINDOWS;\nGRANT CONNECT ON ENDPOINT::" + sqlName(endpoint) + " TO " + sqlName(account) + ";"}
}

// sqlCreateTargetAGBatch makes the new server's availability group with no
// databases (automatic seeding fills it through the distributed AG).
func sqlCreateTargetAGBatch(ag string, dst *sqlInstance) sqlBatch {
	cluster := ""
	if dst.Major >= 14 && dst.ClusterTypeNeeded() {
		cluster = " WITH (CLUSTER_TYPE = NONE)"
	}
	url := sqlEndpointURL(dst.FQDN(), dst.Endpoint.Port)
	return sqlBatch{On: sqlDst, Name: "new availability group " + ag, Kind: "createag", Timeout: 300, Args: map[string]string{"ag": ag},
		SQL: "SET NOCOUNT ON;\nIF NOT EXISTS (SELECT 1 FROM sys.availability_groups WHERE name = " + sqlStr(ag) + ")\nBEGIN\n  CREATE AVAILABILITY GROUP " + sqlName(ag) + cluster + " FOR REPLICA ON " + sqlStr(dst.ServerName) + " WITH (ENDPOINT_URL = " + sqlStr(url) + ", AVAILABILITY_MODE = ASYNCHRONOUS_COMMIT, FAILOVER_MODE = MANUAL, SEEDING_MODE = AUTOMATIC);\nEND;\nALTER AVAILABILITY GROUP " + sqlName(ag) + " GRANT CREATE ANY DATABASE;"}
}

func sqlCreateDAGBatch(dag, srcAG, srcURL, dstAG, dstURL string) sqlBatch {
	return sqlBatch{On: sqlOn, Name: "distributed availability group " + dag, Kind: "createdag", Timeout: 300, Args: map[string]string{"dag": dag},
		SQL: "SET NOCOUNT ON;\nIF NOT EXISTS (SELECT 1 FROM sys.availability_groups WHERE name = " + sqlStr(dag) + ")\n  CREATE AVAILABILITY GROUP " + sqlName(dag) + " WITH (DISTRIBUTED) AVAILABILITY GROUP ON " + sqlDAGReplica(srcAG, srcURL) + ", " + sqlDAGReplica(dstAG, dstURL) + ";"}
}

func sqlJoinDAGBatch(dag, srcAG, srcURL, dstAG, dstURL string) sqlBatch {
	return sqlBatch{On: sqlDst, Name: "join " + dag, Kind: "joindag", Timeout: 300, Args: map[string]string{"dag": dag},
		SQL: "SET NOCOUNT ON;\nIF NOT EXISTS (SELECT 1 FROM sys.availability_groups WHERE name = " + sqlStr(dag) + ")\n  ALTER AVAILABILITY GROUP " + sqlName(dag) + " JOIN AVAILABILITY GROUP ON " + sqlDAGReplica(srcAG, srcURL) + ", " + sqlDAGReplica(dstAG, dstURL) + ";"}
}

// sqlDAGStateBatch reads each database's state in the distributed AG on
// one side: synchronization state and the last hardened LSN.
func sqlDAGStateBatch(on, dag string) sqlBatch {
	return sqlBatch{On: on, Name: "state " + dag, Kind: "dagstate", Timeout: 120, Args: map[string]string{"dag": dag},
		SQL: "SET NOCOUNT ON;\nSELECT DB_NAME(drs.database_id) AS db, drs.synchronization_state_desc AS sync, CONVERT(varchar(40), drs.last_hardened_lsn) AS hardened, drs.is_local AS isLocal, ISNULL(drs.log_send_queue_size, 0) AS sendQueueKB, ISNULL(drs.redo_queue_size, 0) AS redoQueueKB\nFROM sys.dm_hadr_database_replica_states drs JOIN sys.availability_groups ag ON ag.group_id = drs.group_id WHERE ag.name = " + sqlStr(dag) + ";"}
}

func sqlDAGModeBatch(on, dag, srcAG, dstAG, mode string) sqlBatch {
	return sqlBatch{On: on, Name: "commit mode " + dag, Kind: "dagmode", Timeout: 120, Args: map[string]string{"dag": dag, "mode": mode},
		SQL: "SET NOCOUNT ON;\nALTER AVAILABILITY GROUP " + sqlName(dag) + " MODIFY AVAILABILITY GROUP ON " + sqlStr(srcAG) + " WITH (AVAILABILITY_MODE = " + mode + "), " + sqlStr(dstAG) + " WITH (AVAILABILITY_MODE = " + mode + ");"}
}

func sqlDAGSecondaryBatch(on, dag string) sqlBatch {
	return sqlBatch{On: on, Name: "secondary " + dag, Kind: "dagsecondary", Timeout: 300, Args: map[string]string{"dag": dag},
		SQL: "SET NOCOUNT ON;\nALTER AVAILABILITY GROUP " + sqlName(dag) + " SET (ROLE = SECONDARY);"}
}

func sqlDAGFailoverBatch(on, dag string) sqlBatch {
	return sqlBatch{On: on, Name: "fail over " + dag, Kind: "dagfailover", Timeout: 600, Args: map[string]string{"dag": dag},
		SQL: "SET NOCOUNT ON;\nALTER AVAILABILITY GROUP " + sqlName(dag) + " FORCE_FAILOVER_ALLOW_DATA_LOSS;"}
}

// sqlDropDAGBatch removes only the distributed AG this plan made, by its
// recorded name, and only when it is distributed.
func sqlDropDAGBatch(on, dag string) sqlBatch {
	return sqlBatch{On: on, Name: "remove " + dag, Kind: "dropdag", Timeout: 300, Args: map[string]string{"dag": dag},
		SQL: "SET NOCOUNT ON;\nIF EXISTS (SELECT 1 FROM sys.availability_groups WHERE name = " + sqlStr(dag) + " AND is_distributed = 1) DROP AVAILABILITY GROUP " + sqlName(dag) + ";"}
}
