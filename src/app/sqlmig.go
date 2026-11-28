package main

// SQL Server database migration (2026-11-28, owner: "SQL Server database
// migration ... three methods, picked per database: backup/restore, log
// shipping, and distributed availability groups ... carry over logins (SIDs
// and passwords), Agent jobs, linked servers and other instance objects ...
// repoint DNS or aliases at cutover"; distributed AGs only for databases
// already in an availability group, the owner's decision).
//
// Nothing is installed on either SQL Server. The application connects from
// this computer and runs T-SQL only (sqlmig_windows.go runs the batches
// through Invoke-SqlMigration.ps1). Everything that decides what runs is
// here, untagged, so go test covers it on any platform:
//   sqlmig.go          model, reading an instance, recommendations, checks
//   sqlmig_tsql.go     every T-SQL statement, with names and values quoted
//   sqlmig_objects.go  logins, roles, jobs, operators, linked servers,
//                      configuration and TDE certificates
//   sqlmig_runner.go   plans: seeding, log shipping rounds, tests, cutover,
//                      rollback and the alias

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Methods, picked per database.
const (
	sqlMethodBackup  = "backup"  // copied once at cutover: short outage
	sqlMethodLogShip = "logship" // full backup, then log backups every few minutes
	sqlMethodDAG     = "dag"     // distributed availability group (source already in an AG)
)

var sqlMethodNames = map[string]string{
	sqlMethodBackup:  "Backup and restore",
	sqlMethodLogShip: "Log shipping",
	sqlMethodDAG:     "Distributed availability group",
}

// Database states in a plan.
const (
	sqlWaiting    = "waiting" // nothing copied yet
	sqlSeeding    = "seeding" // full backup and restore, or AG seeding
	sqlReady      = "ready"   // in sync (log shipping, DAG) or ready to copy (backup)
	sqlSyncing    = "syncing" // a log round runs
	sqlTesting    = "testing" // a test copy exists
	sqlCutting    = "cutting" // cutover runs
	sqlDone       = "done"    // cut over
	sqlFailed     = "failed"  // the last step failed
	sqlRolledBack = "rolledback"
)

// Where an account comes from for a connection.
const (
	sqlAuthCurrent = "current" // this computer's Windows sign-in
	sqlAuthWindows = "windows" // another Windows account (runs on the SQL Server's computer through remoting)
	sqlAuthSQL     = "sql"     // a SQL Server login
)

const (
	sqlMaxDatabases  = 200
	sqlMaxPlans      = 50
	sqlMinSyncMinute = 5
	sqlMaxSyncMinute = 1440
	// Databases this size or smaller default to backup and restore: they
	// copy in minutes, so the outage is short.
	sqlSmallMB = 20 * 1024
	// Express edition's database size limit (10 GB of data files).
	sqlExpressMB = 10 * 1024
)

// sqlEndpoint is one SQL Server instance as clients type it.
type sqlEndpoint struct {
	Server string `json:"server"` // HOST, HOST\INSTANCE or HOST,PORT
	Auth   string `json:"auth"`   // current, windows or sql
	User   string `json:"user,omitempty"`
}

// Computer is the host part of the instance name.
func (e sqlEndpoint) Computer() string {
	s := strings.TrimSpace(e.Server)
	if i := strings.IndexAny(s, `\,`); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "tcp:"), "TCP:")
	if s == "." || strings.EqualFold(s, "(local)") || strings.EqualFold(s, "localhost") {
		return "localhost"
	}
	return s
}

var sqlServerPattern = regexp.MustCompile(`^(tcp:)?(\(localdb\)|\(local\)|\.|[A-Za-z0-9][A-Za-z0-9.\-]{0,252})(\\[A-Za-z0-9_$#\-]{1,16})?(,[0-9]{1,5})?$`)

func sqlCheckEndpoint(e *sqlEndpoint, side string) error {
	e.Server = strings.TrimSpace(e.Server)
	e.User = strings.TrimSpace(e.User)
	if e.Server == "" {
		return fmt.Errorf("type the %s SQL Server instance (SERVER or SERVER\\INSTANCE)", side)
	}
	if !sqlServerPattern.MatchString(e.Server) {
		return fmt.Errorf("type the %s SQL Server as SERVER, SERVER\\INSTANCE or SERVER,PORT", side)
	}
	switch e.Auth {
	case "", sqlAuthCurrent:
		e.Auth, e.User = sqlAuthCurrent, ""
	case sqlAuthWindows:
		if e.User == "" || !strings.ContainsAny(e.User, `\@`) || len(e.User) > 256 {
			return fmt.Errorf(`type the %s server's Windows account as DOMAIN\user or user@domain`, side)
		}
		if strings.HasPrefix(e.Server, "(localdb)") {
			return fmt.Errorf("LocalDB is reached only with your current sign-in")
		}
	case sqlAuthSQL:
		if e.User == "" || len(e.User) > 128 || strings.ContainsAny(e.User, ";=\x00") {
			return fmt.Errorf("type the %s server's SQL Server login", side)
		}
	default:
		return fmt.Errorf("choose how to sign in to the %s server", side)
	}
	return nil
}

// sqlKey identifies an endpoint and its account for the passwords held in
// memory.
func sqlKey(e sqlEndpoint) string {
	return strings.ToLower(e.Server) + "\x00" + e.Auth + "\x00" + strings.ToLower(e.User)
}

// ---------------------------------------------------------------------
// What an instance holds (read only).

type sqlInstance struct {
	Server         string        `json:"server"`
	Machine        string        `json:"machine"`
	InstanceName   string        `json:"instanceName"`
	ServerName     string        `json:"serverName"` // @@SERVERNAME
	Version        string        `json:"version"`
	Major          int           `json:"major"`
	Edition        string        `json:"edition"`
	EngineEdition  int           `json:"engineEdition"`
	Collation      string        `json:"collation"`
	Hadr           bool          `json:"hadr"`
	Clustered      bool          `json:"clustered"`
	Filestream     int           `json:"filestream"`
	DataPath       string        `json:"dataPath"`
	LogPath        string        `json:"logPath"`
	BackupPath     string        `json:"backupPath"`
	ServiceAccount string        `json:"serviceAccount"`
	AgentAccount   string        `json:"agentAccount"`
	AgentState     string        `json:"agentState"`
	Domain         string        `json:"domain"`    // NetBIOS
	DNSDomain      string        `json:"dnsDomain"` // from the TCP/IP settings
	Port           int           `json:"port"`
	Sysadmin       bool          `json:"sysadmin"`
	Databases      []sqlDatabase `json:"databases"`
	Volumes        []sqlVolume   `json:"volumes"`
	AGs            []sqlAG       `json:"ags"`
	Endpoint       sqlHadrEP     `json:"endpoint"`
	ReadAt         time.Time     `json:"readAt"`
}

type sqlDatabase struct {
	Name        string    `json:"name"`
	ID          int       `json:"id"`
	State       string    `json:"state"`
	Recovery    string    `json:"recovery"`
	Compat      int       `json:"compat"`
	Collation   string    `json:"collation"`
	ReadOnly    bool      `json:"readOnly"`
	UserAccess  string    `json:"userAccess"`
	Encrypted   bool      `json:"encrypted"`
	Owner       string    `json:"owner"`
	Trustworthy bool      `json:"trustworthy"`
	DBChaining  bool      `json:"dbChaining"`
	Broker      bool      `json:"broker"`
	CDC         bool      `json:"cdc"`
	Replicated  bool      `json:"replicated"`
	Mirrored    bool      `json:"mirrored"`
	Containment string    `json:"containment"`
	DataMB      float64   `json:"dataMB"`
	LogMB       float64   `json:"logMB"`
	Filestream  bool      `json:"filestream"`
	InMemory    bool      `json:"inMemory"`
	NoChain     bool      `json:"noChain"` // never had a full backup: no log chain yet
	LastFull    string    `json:"lastFull"`
	OtherLogs   int       `json:"otherLogs"`        // log backups by other tools in 7 days
	OtherVDI    int       `json:"otherLogsVirtual"` // of them, to a backup product's virtual device
	AG          string    `json:"ag"`
	Files       []sqlFile `json:"files"`
}

type sqlFile struct {
	Logical  string  `json:"logical"`
	Type     string  `json:"type"` // ROWS, LOG, FILESTREAM, FULLTEXT
	Physical string  `json:"physical"`
	SizeMB   float64 `json:"sizeMB"`
	FileID   int     `json:"fileId"`
}

type sqlVolume struct {
	Mount   string  `json:"mount"`
	TotalMB float64 `json:"totalMB"`
	FreeMB  float64 `json:"freeMB"`
}

type sqlAG struct {
	Name         string   `json:"name"`
	Role         string   `json:"role"` // PRIMARY or SECONDARY on this instance
	ClusterType  string   `json:"clusterType"`
	Listener     string   `json:"listener"` // DNS name
	ListenerPort int      `json:"listenerPort"`
	Distributed  bool     `json:"distributed"`
	Databases    []string `json:"databases"`
}

type sqlHadrEP struct {
	Name  string `json:"name"`
	Port  int    `json:"port"`
	State string `json:"state"`
	Auth  string `json:"auth"` // WINDOWS, CERTIFICATE, ...
}

func (i *sqlInstance) database(name string) *sqlDatabase {
	for k := range i.Databases {
		if strings.EqualFold(i.Databases[k].Name, name) {
			return &i.Databases[k]
		}
	}
	return nil
}

func (i *sqlInstance) ag(name string) *sqlAG {
	for k := range i.AGs {
		if strings.EqualFold(i.AGs[k].Name, name) {
			return &i.AGs[k]
		}
	}
	return nil
}

// Express is true for Express and LocalDB.
func (i *sqlInstance) Express() bool {
	return i.EngineEdition == 4 || strings.Contains(strings.ToLower(i.Edition), "express")
}

// Enterprise is true for Enterprise, Developer and Evaluation.
func (i *sqlInstance) Enterprise() bool {
	return i.EngineEdition == 3
}

// Compression is whether BACKUP ... WITH COMPRESSION is allowed.
func (i *sqlInstance) Compression() bool {
	e := strings.ToLower(i.Edition)
	if i.Express() || strings.Contains(e, "web") || strings.Contains(e, "workgroup") {
		return false
	}
	if i.Major < 10 || (i.Major == 10 && !i.Enterprise() && !strings.HasPrefix(i.Version, "10.5")) {
		return false // 2008 Standard has none; 2008 R2 (10.50) Standard has it
	}
	return true
}

// Host is the instance's computer name with its DNS domain when known.
func (i *sqlInstance) FQDN() string {
	host := i.Machine
	if host == "" {
		return ""
	}
	if i.DNSDomain != "" && !strings.Contains(host, ".") {
		return strings.ToLower(host + "." + i.DNSDomain)
	}
	return strings.ToLower(host)
}

// volumeFor is the volume holding path (the longest mount point that starts
// it), or nil when the instance has no file there.
func (i *sqlInstance) volumeFor(path string) *sqlVolume {
	var best *sqlVolume
	p := strings.ToLower(strings.TrimRight(path, `\`) + `\`)
	for k := range i.Volumes {
		m := strings.ToLower(strings.TrimRight(i.Volumes[k].Mount, `\`) + `\`)
		if strings.HasPrefix(p, m) && (best == nil || len(m) > len(strings.TrimRight(best.Mount, `\`)+`\`)) {
			best = &i.Volumes[k]
		}
	}
	return best
}

// Row helpers: the batch runner returns each row as a map of column to a
// string, number, bool or nil.

type sqlRow map[string]any

func (r sqlRow) str(key string) string {
	switch v := r[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprint(v)
	}
}

func (r sqlRow) num(key string) float64 {
	switch v := r[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}

func (r sqlRow) int(key string) int { return int(r.num(key)) }

func (r sqlRow) flag(key string) bool {
	switch v := r[key].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "1" || s == "true"
	}
	return false
}

func sqlMajor(version string) int {
	n, _ := strconv.Atoi(strings.SplitN(strings.TrimSpace(version), ".", 2)[0])
	return n
}

// sqlVersionName is the product name of a major version.
func sqlVersionName(major int) string {
	switch major {
	case 9:
		return "SQL Server 2005"
	case 10:
		return "SQL Server 2008 or 2008 R2"
	case 11:
		return "SQL Server 2012"
	case 12:
		return "SQL Server 2014"
	case 13:
		return "SQL Server 2016"
	case 14:
		return "SQL Server 2017"
	case 15:
		return "SQL Server 2019"
	case 16:
		return "SQL Server 2022"
	case 17:
		return "SQL Server 2025"
	}
	return fmt.Sprintf("SQL Server (version %d)", major)
}

// sqlParseInfo fills the instance facts from the info batch.
func sqlParseInfo(inst *sqlInstance, rows []sqlRow) error {
	if len(rows) == 0 {
		return errors.New("the instance returned no facts")
	}
	r := rows[0]
	inst.Machine = r.str("machine")
	inst.InstanceName = r.str("instance")
	inst.ServerName = r.str("serverName")
	inst.Version = r.str("version")
	inst.Major = sqlMajor(inst.Version)
	inst.Edition = r.str("edition")
	inst.EngineEdition = r.int("engineEdition")
	inst.Collation = r.str("collation")
	inst.Hadr = r.flag("hadr")
	inst.Clustered = r.flag("clustered")
	inst.Filestream = r.int("filestream")
	inst.DataPath = strings.TrimRight(r.str("dataPath"), `\`)
	inst.LogPath = strings.TrimRight(r.str("logPath"), `\`)
	inst.BackupPath = strings.TrimRight(r.str("backupPath"), `\`)
	inst.ServiceAccount = r.str("serviceAccount")
	inst.AgentAccount = r.str("agentAccount")
	inst.AgentState = r.str("agentState")
	inst.Domain = r.str("domain")
	inst.DNSDomain = strings.Trim(r.str("dnsDomain"), ". ")
	inst.Port = r.int("port")
	inst.Sysadmin = r.flag("sysadmin")
	if inst.Major < 10 {
		return fmt.Errorf("%s is %s; only SQL Server 2008 and later can be moved here", inst.Server, sqlVersionName(inst.Major))
	}
	if inst.EngineEdition == 5 || inst.EngineEdition == 8 {
		return fmt.Errorf("%s is Azure SQL; only SQL Server instances on Windows can be moved here", inst.Server)
	}
	return nil
}

func sqlParseDatabases(inst *sqlInstance, dbs, files, volumes, ags, ep []sqlRow) {
	inst.Databases = nil
	for _, r := range dbs {
		inst.Databases = append(inst.Databases, sqlDatabase{
			Name: r.str("name"), ID: r.int("id"), State: r.str("state"), Recovery: r.str("recovery"), Compat: r.int("compat"),
			Collation: r.str("collation"), ReadOnly: r.flag("readOnly"), UserAccess: r.str("userAccess"), Encrypted: r.flag("encrypted"),
			Owner: r.str("owner"), Trustworthy: r.flag("trustworthy"), DBChaining: r.flag("dbChaining"), Broker: r.flag("broker"),
			CDC: r.flag("cdc"), Replicated: r.flag("replicated"), Mirrored: r.flag("mirrored"), Containment: r.str("containment"),
			DataMB: r.num("dataMB"), LogMB: r.num("logMB"), Filestream: r.int("filestreamFiles") > 0, InMemory: r.int("inMemory") > 0,
			NoChain: r.flag("noChain"), LastFull: r.str("lastFull"), OtherLogs: r.int("otherLogs"), OtherVDI: r.int("otherLogsVirtual"),
		})
	}
	for _, r := range files {
		if db := inst.database(r.str("db")); db != nil {
			db.Files = append(db.Files, sqlFile{Logical: r.str("logical"), Type: strings.ToUpper(r.str("type")), Physical: r.str("physical"), SizeMB: r.num("sizeMB"), FileID: r.int("fileId")})
		}
	}
	inst.Volumes = nil
	for _, r := range volumes {
		inst.Volumes = append(inst.Volumes, sqlVolume{Mount: r.str("mount"), TotalMB: r.num("totalMB"), FreeMB: r.num("freeMB")})
	}
	inst.AGs = nil
	for _, r := range ags {
		name := r.str("ag")
		ag := inst.ag(name)
		if ag == nil {
			inst.AGs = append(inst.AGs, sqlAG{Name: name, Role: strings.ToUpper(r.str("role")), ClusterType: strings.ToUpper(r.str("clusterType")), Listener: r.str("listener"), ListenerPort: r.int("listenerPort"), Distributed: r.flag("distributed")})
			ag = &inst.AGs[len(inst.AGs)-1]
		}
		if db := r.str("db"); db != "" {
			ag.Databases = append(ag.Databases, db)
			if d := inst.database(db); d != nil && !ag.Distributed {
				d.AG = ag.Name
			}
		}
	}
	if len(ep) > 0 {
		inst.Endpoint = sqlHadrEP{Name: ep[0].str("name"), Port: ep[0].int("port"), State: ep[0].str("state"), Auth: strings.ToUpper(ep[0].str("auth"))}
	}
	sort.Slice(inst.Databases, func(a, b int) bool {
		return strings.ToLower(inst.Databases[a].Name) < strings.ToLower(inst.Databases[b].Name)
	})
}

// ---------------------------------------------------------------------
// Recommendations: what each database moves with, and why. The operator
// can change every one.

type sqlChoice struct {
	Method    string   `json:"method"`
	Why       string   `json:"why"`
	Available []string `json:"available"`
	// Unavailable says why a method cannot be picked, by method.
	Unavailable map[string]string `json:"unavailable"`
}

func sqlRecommend(src, dst *sqlInstance, db *sqlDatabase) sqlChoice {
	c := sqlChoice{Unavailable: map[string]string{}}
	c.Available = append(c.Available, sqlMethodBackup)
	full := !strings.EqualFold(db.Recovery, "SIMPLE")
	if full {
		c.Available = append(c.Available, sqlMethodLogShip)
	} else {
		c.Unavailable[sqlMethodLogShip] = "It uses the simple recovery model, which keeps no log backups. Tick Switch to full recovery to ship its log."
	}
	if why := sqlDAGUnavailable(src, dst, db); why == "" {
		c.Available = append(c.Available, sqlMethodDAG)
	} else {
		c.Unavailable[sqlMethodDAG] = why
	}
	size := db.DataMB + db.LogMB
	switch {
	case c.Unavailable[sqlMethodDAG] == "":
		c.Method, c.Why = sqlMethodDAG, "It is already in the availability group "+db.AG+", so a distributed availability group keeps the new server in sync with no change to how it runs now. Cutover takes seconds."
	case !full:
		c.Method, c.Why = sqlMethodBackup, "It uses the simple recovery model, so it is copied once at cutover. The outage lasts as long as the backup and restore ("+sqlSizeText(size)+")."
	case db.OtherVDI > 0:
		c.Method, c.Why = sqlMethodBackup, "A backup product takes its log backups to its own storage, which the new server cannot restore from, so shipping the log would split that product's chain. It is copied once at cutover instead ("+sqlSizeText(size)+")."
	case size <= sqlSmallMB:
		c.Method, c.Why = sqlMethodBackup, "It is small ("+sqlSizeText(size)+"), so a backup and restore at cutover takes only minutes."
	default:
		c.Method, c.Why = sqlMethodLogShip, "It is "+sqlSizeText(size)+", so it is copied ahead and kept a few minutes behind with log backups. Cutover copies only the last log backup."
	}
	return c
}

// sqlDAGUnavailable says why a distributed AG cannot carry this database,
// or "". Owner's decision: only databases already in an availability group.
func sqlDAGUnavailable(src, dst *sqlInstance, db *sqlDatabase) string {
	switch {
	case db.AG == "":
		return "It is not in an availability group. Distributed availability groups are offered only for databases already in one; log shipping gives the same short cutover."
	case src.Major < 13 || dst.Major < 13:
		return "Distributed availability groups need SQL Server 2016 or later on both servers."
	case !src.Enterprise() || !dst.Enterprise():
		return "Distributed availability groups need Enterprise edition on both servers."
	case !dst.Hadr:
		return "Always On availability groups are not turned on for the new server. Turn them on in SQL Server Configuration Manager (it restarts that SQL Server), then read the servers again."
	case dst.Endpoint.Port == 0:
		return "The new server has no availability group endpoint. Create one (Hadr_endpoint, port 5022), then read the servers again."
	case dst.Endpoint.Auth != "" && !strings.Contains(dst.Endpoint.Auth, "WINDOWS"):
		return "The new server's endpoint uses certificates only; this application sets up endpoints that use Windows accounts."
	}
	ag := src.ag(db.AG)
	if ag == nil {
		return "Its availability group could not be read."
	}
	if ag.Role != "PRIMARY" {
		return "This server holds a secondary replica of " + ag.Name + ". Connect to the primary replica (or the listener " + ag.Listener + ") as the old server."
	}
	if dst.Major < 14 && dst.ClusterTypeNeeded() {
		return "The new server is SQL Server 2016 without a failover cluster; its availability group needs a cluster (SQL Server 2017 and later can do without one)."
	}
	return ""
}

// ClusterTypeNeeded reports whether this instance can make an availability
// group only inside a Windows failover cluster.
func (i *sqlInstance) ClusterTypeNeeded() bool {
	return !i.Clustered && !i.inWSFC()
}

func (i *sqlInstance) inWSFC() bool {
	for _, ag := range i.AGs {
		if ag.ClusterType == "" || ag.ClusterType == "WSFC" {
			return true
		}
	}
	return false
}

func sqlSizeText(mb float64) string {
	switch {
	case mb >= 1024*1024:
		return fmt.Sprintf("%.1f TB", mb/1024/1024)
	case mb >= 1024:
		return fmt.Sprintf("%.1f GB", mb/1024)
	default:
		return fmt.Sprintf("%.0f MB", math.Max(mb, 1))
	}
}

// ---------------------------------------------------------------------
// Checks before anything changes.

type sqlFinding struct {
	DB       string `json:"db,omitempty"` // empty for the whole plan
	Severity string `json:"severity"`     // blocker, warning, info
	Code     string `json:"code"`
	Message  string `json:"message"`
}

var sqlSystemDatabases = map[string]string{
	"master": "", "model": "", "msdb": "", "tempdb": "",
	"distribution": "It is the replication distribution database: set replication up again on the new server instead.",
	"ssisdb":       "It is the Integration Services catalog: move it with the SSIS catalog's own migration (it needs its master key).",
}

func sqlCheckPlan(src, dst *sqlInstance, p *sqlPlan) []sqlFinding {
	out := []sqlFinding{}
	add := func(db, sev, code, msg string) {
		out = append(out, sqlFinding{DB: db, Severity: sev, Code: code, Message: msg})
	}
	if src == nil || dst == nil {
		add("", "blocker", "notread", "Read both servers first.")
		return out
	}
	if strings.EqualFold(src.ServerName, dst.ServerName) && src.ServerName != "" {
		add("", "blocker", "same", "The old and new server are the same instance.")
	}
	if dst.Major < src.Major {
		add("", "blocker", "older", fmt.Sprintf("The new server is %s and the old one %s. A database can only be restored on the same or a later version.", sqlVersionName(dst.Major), sqlVersionName(src.Major)))
	}
	if !src.Sysadmin {
		add("", "blocker", "sysadmin-src", "The account used for the old server is not a member of sysadmin there; backups, logins and jobs need it.")
	}
	if !dst.Sysadmin {
		add("", "blocker", "sysadmin-dst", "The account used for the new server is not a member of sysadmin there; restores, logins and jobs need it.")
	}
	if !strings.EqualFold(src.Collation, dst.Collation) && src.Collation != "" && dst.Collation != "" {
		add("", "warning", "collation", "The servers use different collations ("+src.Collation+" and "+dst.Collation+"). Databases keep their own, but queries that join temporary tables to them can fail with collation conflicts.")
	}
	if strings.TrimSpace(p.BackupPath) == "" {
		add("", "blocker", "backup-path", "Choose a backup folder both SQL Server services can reach.")
	}
	needMB := map[string]float64{}
	ags := map[string][]string{}
	for _, d := range p.Databases {
		db := src.database(d.Name)
		if db == nil {
			add(d.Name, "blocker", "missing", "It is no longer on the old server. Read the servers again.")
			continue
		}
		if why, system := sqlSystemDatabases[strings.ToLower(db.Name)]; system {
			if why == "" {
				why = "System databases stay with each server; their contents (logins, jobs, linked servers) move as instance objects."
			}
			add(d.Name, "blocker", "system", why)
			continue
		}
		if !strings.EqualFold(db.State, "ONLINE") {
			add(d.Name, "blocker", "offline", "It is "+strings.ToLower(db.State)+", not online.")
		}
		target := d.target()
		if t := dst.database(target); t != nil && !d.Owns(t) {
			add(d.Name, "blocker", "exists", "The new server already has a database named "+target+". Give the copy another name, or remove that database there first.")
		}
		if dst.Express() && db.DataMB > sqlExpressMB {
			add(d.Name, "blocker", "express", "The new server is Express edition, which holds databases up to 10 GB; this one has "+sqlSizeText(db.DataMB)+" of data.")
		}
		if db.Filestream && dst.Filestream == 0 {
			add(d.Name, "blocker", "filestream", "It uses FILESTREAM, which is not turned on for the new server. Turn it on in SQL Server Configuration Manager and sp_configure 'filestream access level'.")
		}
		if strings.HasPrefix(strings.ToLower(db.Name), "reportserver") {
			add(d.Name, "warning", "ssrs", "It is a Reporting Services database. Move the Reporting Services encryption key with Report Server Configuration Manager too, or reports with stored credentials stop working.")
		}
		if db.Replicated {
			add(d.Name, "warning", "replication", "It takes part in replication, which is not moved. Set the publications or subscriptions up again on the new server after cutover.")
		}
		if db.CDC {
			add(d.Name, "warning", "cdc", "Change data capture is kept (KEEP_CDC), but its capture and cleanup jobs must be created again on the new server (sys.sp_cdc_add_job).")
		}
		if db.Mirrored {
			add(d.Name, "warning", "mirroring", "It is in a database mirroring session. Backups still work; remove the mirroring after cutover.")
		}
		if db.Encrypted {
			add(d.Name, "info", "tde", "It uses Transparent Data Encryption. Its certificate is copied to the new server before the first restore.")
		}
		if strings.EqualFold(db.Containment, "PARTIAL") {
			add(d.Name, "info", "contained", "It is a contained database: its own users move with it.")
		}
		method := d.Method
		choice := sqlRecommend(src, dst, db)
		if method == sqlMethodLogShip && strings.EqualFold(db.Recovery, "SIMPLE") && !d.SwitchToFull {
			add(d.Name, "blocker", "simple", "Log shipping needs the full recovery model. Tick Switch to full recovery, or use backup and restore.")
		}
		if method == sqlMethodDAG {
			if why := choice.Unavailable[sqlMethodDAG]; why != "" {
				add(d.Name, "blocker", "dag", why)
			} else {
				ags[db.AG] = append(ags[db.AG], d.Name)
			}
		}
		if method == sqlMethodLogShip && db.OtherLogs > 0 {
			if db.OtherVDI > 0 {
				add(d.Name, "warning", "otherlogs-vdi", fmt.Sprintf("Another tool took %d log backups in the last 7 days to its own storage. While log shipping runs, its log chain and this one split: pause its log backups for this database, or use backup and restore.", db.OtherLogs))
			} else {
				add(d.Name, "info", "otherlogs", fmt.Sprintf("Another job took %d log backups in the last 7 days. Each round also restores those backup files (the new server's SQL Server service must be able to read them); pausing that job until cutover is simpler.", db.OtherLogs))
			}
		}
		if method == sqlMethodLogShip && d.SwitchToFull && strings.EqualFold(db.Recovery, "SIMPLE") {
			add(d.Name, "warning", "tofull", "It is switched to the full recovery model on the old server before its first copy. Its log then grows until log backups run; the shipping rounds back it up every few minutes.")
		}
		// Space on the new server.
		dataPath, logPath := p.dataPath(dst), p.logPath(dst)
		needMB[strings.ToLower(dataPath)] += db.DataMB
		needMB[strings.ToLower(logPath)] += db.LogMB
	}
	for ag, names := range ags {
		agInfo := src.ag(ag)
		if agInfo == nil {
			continue
		}
		missing := []string{}
		for _, n := range agInfo.Databases {
			found := false
			for _, m := range names {
				if strings.EqualFold(m, n) {
					found = true
				}
			}
			if !found {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			add("", "blocker", "dag-partial", "A distributed availability group moves every database of "+ag+" together; add "+strings.Join(missing, ", ")+" to the plan with the same method.")
		}
		waves := map[int]bool{}
		for _, d := range p.Databases {
			if d.Method == sqlMethodDAG && strings.EqualFold(src.database(d.Name).AG, ag) {
				waves[d.Wave] = true
			}
		}
		if len(waves) > 1 {
			add("", "blocker", "dag-waves", "The databases of "+ag+" cut over together; put them in the same wave.")
		}
	}
	for path, mb := range needMB {
		if v := dst.volumeFor(path); v != nil && v.TotalMB > 0 {
			reserve := math.Max(v.TotalMB*0.10, 10*1024)
			if reserve > v.TotalMB/2 {
				reserve = v.TotalMB / 2
			}
			if v.FreeMB-mb < reserve {
				add("", "blocker", "space", fmt.Sprintf("The new server's %s has %s free; the databases need %s there and %s stays free for production.", v.Mount, sqlSizeText(v.FreeMB), sqlSizeText(mb), sqlSizeText(reserve)))
			}
		}
	}
	if dst.AgentState != "" && !strings.EqualFold(dst.AgentState, "Running") && p.Objects.Jobs {
		add("", "warning", "agent", "SQL Server Agent is not running on the new server ("+dst.AgentState+"). Jobs are copied but do not run until it starts.")
	}
	if dst.Express() && p.Objects.Jobs {
		add("", "warning", "agent-express", "The new server is Express edition, which has no SQL Server Agent: jobs are not copied.")
	}
	if p.Alias.Name != "" {
		if err := sqlCheckAlias(p.Alias, src); err != nil {
			add("", "blocker", "alias", err.Error())
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return sqlSeverityRank(out[a].Severity) < sqlSeverityRank(out[b].Severity) })
	return out
}

func sqlSeverityRank(s string) int {
	switch s {
	case "blocker":
		return 0
	case "warning":
		return 1
	}
	return 2
}

func sqlBlockers(findings []sqlFinding) []sqlFinding {
	out := []sqlFinding{}
	for _, f := range findings {
		if f.Severity == "blocker" {
			out = append(out, f)
		}
	}
	return out
}

// ---------------------------------------------------------------------
// The DNS alias moved at cutover.

type sqlAlias struct {
	Name      string `json:"name"`      // the alias clients use, a DNS name
	Zone      string `json:"zone"`      // DNS zone, the domain by default
	DNSServer string `json:"dnsServer"` // reached with PowerShell remoting
	SPN       bool   `json:"spn"`       // register the Kerberos SPNs for the alias
	AtCutover bool   `json:"atCutover"` // move it after the last database is cut over
}

var sqlHostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9\-]{0,61}[A-Za-z0-9])?$`)

func sqlCheckAlias(a sqlAlias, src *sqlInstance) error {
	name := strings.TrimSpace(a.Name)
	label := strings.SplitN(name, ".", 2)[0]
	if !sqlHostLabel.MatchString(label) {
		return errors.New("type the alias as a DNS name such as sql-app or sql-app.corp.example.com")
	}
	if src != nil && strings.EqualFold(label, src.Machine) {
		return errors.New("the alias cannot be the old server's own computer name: that server registers its own address record. Use a separate alias name (a CNAME) that applications connect to")
	}
	if strings.TrimSpace(a.Zone) == "" {
		return errors.New("type the DNS zone of the alias")
	}
	if strings.TrimSpace(a.DNSServer) == "" {
		return errors.New("type the DNS server that holds the zone (usually a domain controller)")
	}
	if !sqlServerPattern.MatchString(strings.TrimSpace(a.DNSServer)) || strings.ContainsAny(a.DNSServer, `\,`) {
		return errors.New("type the DNS server's computer name")
	}
	return nil
}

// sqlAliasRecordName is the alias relative to its zone.
func sqlAliasRecordName(a sqlAlias) string {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a.Name)), ".")
	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a.Zone)), ".")
	if zone != "" && strings.HasSuffix(name, "."+zone) {
		return strings.TrimSuffix(name, "."+zone)
	}
	return strings.SplitN(name, ".", 2)[0]
}

func sqlAliasFQDN(a sqlAlias) string {
	return strings.ToLower(sqlAliasRecordName(a) + "." + strings.TrimSuffix(strings.TrimSpace(a.Zone), "."))
}

// sqlSPNAccount is the account the new server's SPNs belong on: its domain
// service account, or its computer account for built-in and virtual
// accounts.
func sqlSPNAccount(dst *sqlInstance) string {
	acct := strings.TrimSpace(dst.ServiceAccount)
	l := strings.ToLower(acct)
	if acct == "" || strings.HasPrefix(l, "nt service\\") || strings.HasPrefix(l, "nt authority\\") || l == "localsystem" || strings.HasPrefix(l, ".\\") {
		dom := dst.Domain
		if dom == "" {
			return ""
		}
		return dom + `\` + dst.Machine + "$"
	}
	if strings.HasSuffix(acct, "$") || strings.Contains(acct, `\`) || strings.Contains(acct, "@") {
		return acct
	}
	return ""
}
