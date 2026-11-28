package main

// Instance objects of the SQL Server migration: logins with their SIDs and
// password hashes, server roles and permissions, SQL Server Agent jobs,
// operators and categories, linked servers, configuration options and the
// certificates of TDE databases. Everything is created on the new server
// only when it is missing there; nothing that already exists is changed or
// dropped. Copied jobs are created disabled and turned on at cutover.
// Secrets the server cannot hand out (credential secrets, linked server
// login passwords, Database Mail passwords) are listed for the operator.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type sqlLogin struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // S SQL login, U Windows user, G Windows group
	SID        string `json:"-"`
	Hash       string `json:"-"`
	DefaultDB  string `json:"defaultDb"`
	Language   string `json:"language"`
	Disabled   bool   `json:"disabled"`
	Policy     bool   `json:"policy"`
	Expiration bool   `json:"expiration"`
	Deny       bool   `json:"deny"`
}

type sqlRoleMember struct{ Role, Member string }

type sqlPerm struct{ Grantee, State, Perm string }

type sqlJob struct {
	Name, Description, Category, Owner, Operator string
	Enabled                                      bool
	EventLog, Email                              int
	StartStep                                    int
	Steps                                        []sqlJobStep
	Schedules                                    []sqlJobSchedule
	MaintenancePlan                              bool
}

type sqlJobStep struct {
	ID                                   int
	Name, Subsystem, Command, Database   string
	DBUser, Output, Proxy                string
	OnSuccess, OnSuccessStep             int
	OnFail, OnFailStep, Retry, RetryWait int
	Flags                                int
}

type sqlJobSchedule struct {
	Name                                            string
	Enabled                                         bool
	FreqType, FreqInterval, SubdayType, SubdayEvery int
	Relative, Recurrence, StartDate, EndDate        int
	StartTime, EndTime                              int
}

type sqlOperator struct {
	Name, Email, Pager string
	Enabled            bool
}

type sqlLinked struct {
	Name, Product, Provider, Source, Location, ProvStr, Catalog string
	RPCOut, DataAccess, CollationCompatible, RemoteCollation    bool
	Logins                                                      []sqlLinkedLogin
}

type sqlLinkedLogin struct {
	Local  string // "" for every login
	Self   bool
	Remote string
}

// sqlObjects is what one instance holds.
type sqlObjects struct {
	Logins      []sqlLogin
	CustomRoles []string
	Members     []sqlRoleMember
	Perms       []sqlPerm
	Jobs        []sqlJob
	Operators   []sqlOperator
	Categories  []string
	Linked      []sqlLinked
	Config      map[string]string
	Manual      []sqlObjectItem
	Certs       map[string]bool // certificate thumbprints in master (target)
	MasterKey   bool
	AgentRead   bool
}

// sqlObjectChoice is what the plan copies.
type sqlObjectChoice struct {
	Logins        bool     `json:"logins"`
	Jobs          bool     `json:"jobs"`
	LinkedServers bool     `json:"linkedServers"`
	Operators     bool     `json:"operators"`
	Config        []string `json:"config"`
	// DisableSourceJobs turns off, at cutover, the old server's jobs whose
	// databases moved (put back by rollback).
	DisableSourceJobs bool `json:"disableSourceJobs"`
}

type sqlObjectItem struct {
	Kind   string `json:"kind"` // login, role, permission, job, operator, linked, config, certificate, credential, ...
	Name   string `json:"name"`
	Action string `json:"action"` // create, exists, skip, manual, conflict, created, failed
	Detail string `json:"detail,omitempty"`
}

// Options compared and offered for copying. The first group is ticked by
// default when it differs; memory and the others are shown unticked
// because they depend on the new hardware or on security choices.
var sqlConfigDefault = []string{"cost threshold for parallelism", "max degree of parallelism", "optimize for ad hoc workloads", "backup compression default", "backup checksum default", "remote admin connections", "Database Mail XPs", "clr enabled", "contained database authentication", "Ad Hoc Distributed Queries", "Ole Automation Procedures"}
var sqlConfigShown = []string{"max server memory (MB)", "min server memory (MB)", "xp_cmdshell", "clr strict security", "filestream access level", "remote access", "fill factor (%)"}

// sqlConfigNeedsRestart are options that change only when SQL Server restarts.
var sqlConfigNeedsRestart = map[string]bool{"fill factor (%)": true, "remote access": true}

// ---------------------------------------------------------------------
// Reading.

const sqlLoginFilter = "p.name NOT LIKE '##%' AND p.name NOT LIKE 'NT SERVICE\\%' AND p.name NOT LIKE 'NT AUTHORITY\\%' AND p.name NOT LIKE 'BUILTIN\\%' AND p.principal_id > 2 AND p.name <> 'sa'"

func sqlObjectBatches(on string, major int) []sqlBatch {
	out := []sqlBatch{
		{On: on, Name: "logins", Kind: "logins", Timeout: 120, SQL: `SET NOCOUNT ON;
SELECT p.name, p.type, CONVERT(varchar(200), p.sid, 1) AS sid,
  CONVERT(varchar(600), CONVERT(varbinary(256), LOGINPROPERTY(p.name, 'PasswordHash')), 1) AS hash,
  p.default_database_name AS defaultDb, p.default_language_name AS language, p.is_disabled AS disabled,
  ISNULL(l.is_policy_checked, 0) AS policy, ISNULL(l.is_expiration_checked, 0) AS expiration,
  CASE WHEN EXISTS (SELECT 1 FROM sys.server_permissions sp WHERE sp.grantee_principal_id = p.principal_id AND sp.type = 'COSQ' AND sp.state = 'D') THEN 1 ELSE 0 END AS deny
FROM sys.server_principals p LEFT JOIN sys.sql_logins l ON l.principal_id = p.principal_id
WHERE p.type IN ('S', 'U', 'G') AND ` + sqlLoginFilter + ` ORDER BY p.name;`},
		{On: on, Name: "role members", Kind: "members", Timeout: 120, SQL: `SET NOCOUNT ON;
SELECT r.name AS role, p.name AS member FROM sys.server_role_members rm
JOIN sys.server_principals r ON r.principal_id = rm.role_principal_id
JOIN sys.server_principals p ON p.principal_id = rm.member_principal_id
WHERE ` + sqlLoginFilter + ` ORDER BY r.name, p.name;`},
		{On: on, Name: "permissions", Kind: "perms", Timeout: 120, SQL: `SET NOCOUNT ON;
SELECT p.name AS grantee, sp.state_desc AS state, sp.permission_name AS perm FROM sys.server_permissions sp
JOIN sys.server_principals p ON p.principal_id = sp.grantee_principal_id
WHERE sp.class = 100 AND sp.type <> 'COSQ' AND p.type IN ('S', 'U', 'G', 'R') AND ` + sqlLoginFilter + ` ORDER BY p.name;`},
		{On: on, Name: "configuration", Kind: "config", Timeout: 60, SQL: "SET NOCOUNT ON; SELECT name, CONVERT(nvarchar(60), value_in_use) AS value FROM sys.configurations;"},
		{On: on, Name: "linked servers", Kind: "linked", Timeout: 120, SQL: `SET NOCOUNT ON;
SELECT s.name, s.product, s.provider, s.data_source AS source, s.location, s.provider_string AS provStr, s.catalog,
  s.is_rpc_out_enabled AS rpcOut, s.is_data_access_enabled AS dataAccess, s.is_collation_compatible AS collCompat, s.uses_remote_collation AS remoteColl
FROM sys.servers s WHERE s.is_linked = 1 AND s.server_id > 0 ORDER BY s.name;`},
		{On: on, Name: "linked logins", Kind: "linkedlogins", Timeout: 120, SQL: `SET NOCOUNT ON;
SELECT s.name AS server, CASE WHEN ll.local_principal_id = 0 THEN N'' ELSE SUSER_NAME(ll.local_principal_id) END AS local,
  ll.uses_self_credential AS self, ll.remote_name AS remote
FROM sys.linked_logins ll JOIN sys.servers s ON s.server_id = ll.server_id WHERE s.is_linked = 1 AND s.server_id > 0;`},
		{On: on, Name: "other objects", Kind: "manual", Timeout: 120, SQL: `SET NOCOUNT ON;
SELECT N'credential' AS kind, name, credential_identity AS detail FROM sys.credentials
UNION ALL SELECT N'server trigger', name, N'' FROM sys.server_triggers WHERE parent_class = 100
UNION ALL SELECT N'backup device', name, physical_name FROM sys.backup_devices
UNION ALL SELECT N'audit', name, N'' FROM sys.server_audits
UNION ALL SELECT N'endpoint', name, type_desc FROM sys.endpoints WHERE endpoint_id > 65535 AND type <> 4;`},
		{On: on, Name: "certificates", Kind: "certs", Timeout: 60, SQL: "SET NOCOUNT ON; SELECT CONVERT(varchar(100), thumbprint, 1) AS thumbprint, name, CASE WHEN EXISTS (SELECT 1 FROM master.sys.symmetric_keys WHERE name = '##MS_DatabaseMasterKey##') THEN 1 ELSE 0 END AS masterKey FROM master.sys.certificates;"},
	}
	if major >= 11 {
		out = append(out, sqlBatch{On: on, Name: "server roles", Kind: "roles", Timeout: 60, SQL: "SET NOCOUNT ON; SELECT name FROM sys.server_principals WHERE type = 'R' AND is_fixed_role = 0 AND principal_id > 2 ORDER BY name;"})
	}
	agent := []sqlBatch{
		{On: on, Name: "jobs", Kind: "jobs", Timeout: 120, Continue: true, SQL: `SET NOCOUNT ON;
SELECT j.name, j.enabled, ISNULL(j.description, N'') AS description, c.name AS category, SUSER_SNAME(j.owner_sid) AS owner,
  j.notify_level_eventlog AS eventLog, j.notify_level_email AS email, ISNULL(o.name, N'') AS operator, j.start_step_id AS startStep,
  CASE WHEN EXISTS (SELECT 1 FROM msdb.dbo.sysjobsteps s WHERE s.job_id = j.job_id AND s.subsystem = N'SSIS' AND s.command LIKE N'%Maintenance Plans%') OR EXISTS (SELECT 1 FROM msdb.dbo.sysmaintplan_subplans mp WHERE mp.job_id = j.job_id) THEN 1 ELSE 0 END AS maintPlan
FROM msdb.dbo.sysjobs j LEFT JOIN msdb.dbo.syscategories c ON c.category_id = j.category_id
LEFT JOIN msdb.dbo.sysoperators o ON o.id = j.notify_email_operator_id ORDER BY j.name;`},
		{On: on, Name: "job steps", Kind: "jobsteps", Timeout: 120, Continue: true, SQL: `SET NOCOUNT ON;
SELECT j.name AS job, s.step_id AS id, s.step_name AS name, s.subsystem, s.command, ISNULL(s.database_name, N'') AS db,
  ISNULL(s.database_user_name, N'') AS dbUser, s.on_success_action AS onSuccess, s.on_success_step_id AS onSuccessStep,
  s.on_fail_action AS onFail, s.on_fail_step_id AS onFailStep, s.retry_attempts AS retry, s.retry_interval AS retryWait,
  ISNULL(s.output_file_name, N'') AS output, s.flags, ISNULL(p.name, N'') AS proxy
FROM msdb.dbo.sysjobsteps s JOIN msdb.dbo.sysjobs j ON j.job_id = s.job_id LEFT JOIN msdb.dbo.sysproxies p ON p.proxy_id = s.proxy_id
ORDER BY j.name, s.step_id;`},
		{On: on, Name: "job schedules", Kind: "jobschedules", Timeout: 120, Continue: true, SQL: `SET NOCOUNT ON;
SELECT j.name AS job, sc.name, sc.enabled, sc.freq_type AS freqType, sc.freq_interval AS freqInterval, sc.freq_subday_type AS subdayType,
  sc.freq_subday_interval AS subdayEvery, sc.freq_relative_interval AS relative, sc.freq_recurrence_factor AS recurrence,
  sc.active_start_date AS startDate, sc.active_end_date AS endDate, sc.active_start_time AS startTime, sc.active_end_time AS endTime
FROM msdb.dbo.sysjobschedules js JOIN msdb.dbo.sysjobs j ON j.job_id = js.job_id JOIN msdb.dbo.sysschedules sc ON sc.schedule_id = js.schedule_id
ORDER BY j.name, sc.name;`},
		{On: on, Name: "operators", Kind: "operators", Timeout: 60, Continue: true, SQL: "SET NOCOUNT ON; SELECT name, enabled, ISNULL(email_address, N'') AS email, ISNULL(pager_address, N'') AS pager FROM msdb.dbo.sysoperators ORDER BY name;"},
		{On: on, Name: "categories", Kind: "categories", Timeout: 60, Continue: true, SQL: "SET NOCOUNT ON; SELECT name FROM msdb.dbo.syscategories WHERE category_class = 1;"},
		{On: on, Name: "mail profiles", Kind: "mail", Timeout: 60, Continue: true, SQL: "SET NOCOUNT ON; IF OBJECT_ID('msdb.dbo.sysmail_profile') IS NOT NULL SELECT name FROM msdb.dbo.sysmail_profile ELSE SELECT CONVERT(sysname, NULL) AS name WHERE 1 = 0;"},
	}
	return append(out, agent...)
}

// sqlParseObjects reads the results of sqlObjectBatches.
func sqlParseObjects(results []sqlBatchResult) sqlObjects {
	o := sqlObjects{Config: map[string]string{}, Certs: map[string]bool{}}
	jobs := map[string]*sqlJob{}
	linked := map[string]*sqlLinked{}
	for _, res := range results {
		rows := res.rows()
		switch res.Name {
		case "logins":
			for _, r := range rows {
				o.Logins = append(o.Logins, sqlLogin{Name: r.str("name"), Type: strings.TrimSpace(r.str("type")), SID: r.str("sid"), Hash: r.str("hash"), DefaultDB: r.str("defaultDb"), Language: r.str("language"), Disabled: r.flag("disabled"), Policy: r.flag("policy"), Expiration: r.flag("expiration"), Deny: r.flag("deny")})
			}
		case "role members":
			for _, r := range rows {
				o.Members = append(o.Members, sqlRoleMember{Role: r.str("role"), Member: r.str("member")})
			}
		case "permissions":
			for _, r := range rows {
				o.Perms = append(o.Perms, sqlPerm{Grantee: r.str("grantee"), State: r.str("state"), Perm: r.str("perm")})
			}
		case "configuration":
			for _, r := range rows {
				o.Config[r.str("name")] = r.str("value")
			}
		case "server roles":
			for _, r := range rows {
				o.CustomRoles = append(o.CustomRoles, r.str("name"))
			}
		case "linked servers":
			for _, r := range rows {
				l := &sqlLinked{Name: r.str("name"), Product: r.str("product"), Provider: r.str("provider"), Source: r.str("source"), Location: r.str("location"), ProvStr: r.str("provStr"), Catalog: r.str("catalog"), RPCOut: r.flag("rpcOut"), DataAccess: r.flag("dataAccess"), CollationCompatible: r.flag("collCompat"), RemoteCollation: r.flag("remoteColl")}
				linked[strings.ToLower(l.Name)] = l
				o.Linked = append(o.Linked, sqlLinked{})
				o.Linked[len(o.Linked)-1] = *l
			}
		case "linked logins":
			for _, r := range rows {
				for i := range o.Linked {
					if strings.EqualFold(o.Linked[i].Name, r.str("server")) {
						o.Linked[i].Logins = append(o.Linked[i].Logins, sqlLinkedLogin{Local: r.str("local"), Self: r.flag("self"), Remote: r.str("remote")})
					}
				}
			}
		case "other objects":
			for _, r := range rows {
				o.Manual = append(o.Manual, sqlObjectItem{Kind: r.str("kind"), Name: r.str("name"), Action: "manual", Detail: sqlManualText(r.str("kind"), r.str("detail"))})
			}
		case "certificates":
			for _, r := range rows {
				o.Certs[strings.ToUpper(r.str("thumbprint"))] = true
				if r.flag("masterKey") {
					o.MasterKey = true
				}
			}
		case "jobs":
			o.AgentRead = res.OK
			for _, r := range rows {
				j := &sqlJob{Name: r.str("name"), Enabled: r.flag("enabled"), Description: r.str("description"), Category: r.str("category"), Owner: r.str("owner"), EventLog: r.int("eventLog"), Email: r.int("email"), Operator: r.str("operator"), StartStep: r.int("startStep"), MaintenancePlan: r.flag("maintPlan")}
				jobs[strings.ToLower(j.Name)] = j
			}
		case "job steps":
			for _, r := range rows {
				if j := jobs[strings.ToLower(r.str("job"))]; j != nil {
					j.Steps = append(j.Steps, sqlJobStep{ID: r.int("id"), Name: r.str("name"), Subsystem: r.str("subsystem"), Command: r.str("command"), Database: r.str("db"), DBUser: r.str("dbUser"), OnSuccess: r.int("onSuccess"), OnSuccessStep: r.int("onSuccessStep"), OnFail: r.int("onFail"), OnFailStep: r.int("onFailStep"), Retry: r.int("retry"), RetryWait: r.int("retryWait"), Output: r.str("output"), Flags: r.int("flags"), Proxy: r.str("proxy")})
				}
			}
		case "job schedules":
			for _, r := range rows {
				if j := jobs[strings.ToLower(r.str("job"))]; j != nil {
					j.Schedules = append(j.Schedules, sqlJobSchedule{Name: r.str("name"), Enabled: r.flag("enabled"), FreqType: r.int("freqType"), FreqInterval: r.int("freqInterval"), SubdayType: r.int("subdayType"), SubdayEvery: r.int("subdayEvery"), Relative: r.int("relative"), Recurrence: r.int("recurrence"), StartDate: r.int("startDate"), EndDate: r.int("endDate"), StartTime: r.int("startTime"), EndTime: r.int("endTime")})
				}
			}
		case "operators":
			for _, r := range rows {
				o.Operators = append(o.Operators, sqlOperator{Name: r.str("name"), Enabled: r.flag("enabled"), Email: r.str("email"), Pager: r.str("pager")})
			}
		case "categories":
			for _, r := range rows {
				o.Categories = append(o.Categories, r.str("name"))
			}
		case "mail profiles":
			for _, r := range rows {
				o.Manual = append(o.Manual, sqlObjectItem{Kind: "mail profile", Name: r.str("name"), Action: "manual", Detail: "Set up Database Mail on the new server with this profile name (its account passwords cannot be read)."})
			}
		}
	}
	names := make([]string, 0, len(jobs))
	for k := range jobs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		o.Jobs = append(o.Jobs, *jobs[k])
	}
	_ = linked
	return o
}

func sqlManualText(kind, detail string) string {
	switch kind {
	case "credential":
		return "Create it on the new server with the identity " + detail + " and its secret (the secret cannot be read)."
	case "server trigger":
		return "Script it from the old server and create it on the new one after reviewing it."
	case "backup device":
		return "Create it on the new server if backups still use it (" + detail + ")."
	case "audit":
		return "Set the server audit up again on the new server; its file path is usually different there."
	case "endpoint":
		return "Create the " + strings.ToLower(detail) + " endpoint on the new server if it is still used."
	}
	return detail
}

// ---------------------------------------------------------------------
// Writing to the new server.

func sqlIsLocalAccount(name string, src *sqlInstance) bool {
	i := strings.Index(name, `\`)
	if i < 0 {
		return false
	}
	return strings.EqualFold(name[:i], src.Machine)
}

// sqlObjectPlan works out what to create on the new server, as one batch
// per object (each may fail on its own without stopping the rest), and the
// list shown for review. dbNames maps old database names to their new
// names for default databases and job steps.
func sqlObjectPlan(src, dst sqlObjects, srcInst, dstInst *sqlInstance, choice sqlObjectChoice, dbNames map[string]string) ([]sqlBatch, []sqlObjectItem) {
	batches := []sqlBatch{}
	items := []sqlObjectItem{}
	add := func(kind, name, action, detail string) {
		items = append(items, sqlObjectItem{Kind: kind, Name: name, Action: action, Detail: detail})
	}
	batch := func(kind, name, sql string) {
		batches = append(batches, sqlBatch{On: sqlDst, Name: kind + ": " + name, Kind: "object", Timeout: 120, Continue: true, Args: map[string]string{"kind": kind, "name": name}, SQL: "SET NOCOUNT ON;\n" + sql})
	}
	dstLogins := map[string]sqlLogin{}
	for _, l := range dst.Logins {
		dstLogins[strings.ToLower(l.Name)] = l
	}
	created := map[string]bool{}
	if choice.Logins {
		for _, r := range src.CustomRoles {
			if !sqlHasFold(dst.CustomRoles, r) {
				add("server role", r, "create", "")
				batch("server role", r, "IF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = "+sqlStr(r)+") CREATE SERVER ROLE "+sqlName(r)+";")
			}
		}
		for _, l := range src.Logins {
			if have, ok := dstLogins[strings.ToLower(l.Name)]; ok {
				if l.Type == "S" && have.SID != "" && !strings.EqualFold(have.SID, l.SID) {
					add("login", l.Name, "conflict", "A login with this name exists on the new server with another SID. Database users are mapped to it at cutover; its password stays as it is there.")
				} else {
					add("login", l.Name, "exists", "")
				}
				continue
			}
			if (l.Type == "U" || l.Type == "G") && sqlIsLocalAccount(l.Name, srcInst) {
				add("login", l.Name, "skip", "It is an account of the old server's own computer, which does not exist on the new one.")
				continue
			}
			sql, detail := sqlLoginSQL(l)
			if sql == "" {
				add("login", l.Name, "skip", detail)
				continue
			}
			created[strings.ToLower(l.Name)] = true
			add("login", l.Name, "create", detail)
			batch("login", l.Name, sql)
		}
		dstMembers := map[string]bool{}
		for _, m := range dst.Members {
			dstMembers[strings.ToLower(m.Role+"\x00"+m.Member)] = true
		}
		for _, m := range src.Members {
			if dstMembers[strings.ToLower(m.Role+"\x00"+m.Member)] {
				continue
			}
			if _, ok := dstLogins[strings.ToLower(m.Member)]; !ok && !created[strings.ToLower(m.Member)] && !sqlHasFold(src.CustomRoles, m.Member) {
				continue
			}
			add("role member", m.Member+" in "+m.Role, "create", "")
			stmt := "ALTER SERVER ROLE " + sqlName(m.Role) + " ADD MEMBER " + sqlName(m.Member) + ";"
			if dstInst.Major < 11 {
				stmt = "EXEC sp_addsrvrolemember " + sqlStr(m.Member) + ", " + sqlStr(m.Role) + ";"
			}
			batch("role member", m.Member+" in "+m.Role, "IF IS_SRVROLEMEMBER("+sqlStr(m.Role)+", "+sqlStr(m.Member)+") = 0 "+stmt)
		}
		for _, p := range src.Perms {
			if _, ok := dstLogins[strings.ToLower(p.Grantee)]; !ok && !created[strings.ToLower(p.Grantee)] && !sqlHasFold(src.CustomRoles, p.Grantee) {
				continue
			}
			state := strings.ToUpper(p.State)
			verb := "GRANT"
			suffix := ""
			switch state {
			case "DENY":
				verb = "DENY"
			case "GRANT_WITH_GRANT_OPTION":
				suffix = " WITH GRANT OPTION"
			}
			if !sqlPermPattern(p.Perm) {
				continue
			}
			add("permission", verb+" "+p.Perm+" to "+p.Grantee, "create", "")
			batch("permission", verb+" "+p.Perm+" to "+p.Grantee, verb+" "+strings.ToUpper(p.Perm)+" TO "+sqlName(p.Grantee)+suffix+";")
		}
	}
	if choice.Operators || choice.Jobs {
		if dstInst.Express() {
			add("job", "SQL Server Agent", "skip", "The new server is Express edition, which has no SQL Server Agent.")
		} else {
			for _, op := range src.Operators {
				if sqlHasFoldOp(dst.Operators, op.Name) {
					add("operator", op.Name, "exists", "")
					continue
				}
				add("operator", op.Name, "create", "")
				batch("operator", op.Name, "IF NOT EXISTS (SELECT 1 FROM msdb.dbo.sysoperators WHERE name = "+sqlStr(op.Name)+") EXEC msdb.dbo.sp_add_operator @name = "+sqlStr(op.Name)+", @enabled = "+sqlBit(op.Enabled)+", @email_address = "+sqlStr(op.Email)+", @pager_address = "+sqlStr(op.Pager)+";")
			}
		}
	}
	if choice.Jobs && !dstInst.Express() {
		for _, c := range src.Categories {
			if !sqlHasFold(dst.Categories, c) && !strings.HasPrefix(c, "[") {
				batch("job category", c, "IF NOT EXISTS (SELECT 1 FROM msdb.dbo.syscategories WHERE name = "+sqlStr(c)+" AND category_class = 1) EXEC msdb.dbo.sp_add_category @class = N'JOB', @type = N'LOCAL', @name = "+sqlStr(c)+";")
			}
		}
		dstJobs := map[string]bool{}
		for _, j := range dst.Jobs {
			dstJobs[strings.ToLower(j.Name)] = true
		}
		for _, j := range src.Jobs {
			switch {
			case dstJobs[strings.ToLower(j.Name)]:
				add("job", j.Name, "exists", "")
			case j.MaintenancePlan:
				add("job", j.Name, "manual", "It runs a maintenance plan, which lives in msdb as an SSIS package. Create the maintenance plan again on the new server.")
			case strings.EqualFold(j.Name, "syspolicy_purge_history") || strings.HasPrefix(strings.ToLower(j.Category), "repl-") || strings.EqualFold(j.Category, "Log Shipping") || strings.HasPrefix(strings.ToLower(j.Name), "cdc."):
				add("job", j.Name, "skip", "SQL Server makes this job itself where it is needed.")
			default:
				sql, notes := sqlJobSQL(j, dstLogins, created, dbNames)
				add("job", j.Name, "create", strings.Join(append([]string{"Created disabled; turned on at cutover when its databases have moved."}, notes...), " "))
				batch("job", j.Name, sql)
			}
		}
	}
	if choice.LinkedServers {
		dstLinked := map[string]bool{}
		for _, l := range dst.Linked {
			dstLinked[strings.ToLower(l.Name)] = true
		}
		for _, l := range src.Linked {
			if dstLinked[strings.ToLower(l.Name)] {
				add("linked server", l.Name, "exists", "")
				continue
			}
			sql, manual := sqlLinkedSQL(l)
			detail := ""
			if len(manual) > 0 {
				detail = "Type the remote password on the new server for: " + strings.Join(manual, ", ") + " (passwords cannot be read)."
			}
			add("linked server", l.Name, "create", detail)
			batch("linked server", l.Name, sql)
		}
	}
	for _, name := range choice.Config {
		want, ok := src.Config[name]
		have := dst.Config[name]
		if !ok || want == have || !sqlConfigAllowed(name) {
			continue
		}
		if _, err := strconv.ParseInt(want, 10, 64); err != nil {
			continue
		}
		detail := "from " + have + " to " + want
		if sqlConfigNeedsRestart[name] {
			detail += "; takes effect when the new server's SQL Server restarts"
		}
		add("configuration", name, "create", detail)
		batch("configuration", name, "DECLARE @adv int; SELECT @adv = CONVERT(int, value_in_use) FROM sys.configurations WHERE name = N'show advanced options';\nIF @adv = 0 BEGIN EXEC sp_configure N'show advanced options', 1; RECONFIGURE; END;\nEXEC sp_configure "+sqlStr(name)+", "+want+"; RECONFIGURE;\nIF @adv = 0 BEGIN EXEC sp_configure N'show advanced options', 0; RECONFIGURE; END;")
	}
	for _, m := range src.Manual {
		items = append(items, m)
	}
	return batches, items
}

func sqlConfigAllowed(name string) bool {
	return sqlHasFold(sqlConfigDefault, name) || sqlHasFold(sqlConfigShown, name)
}

// sqlConfigDiff lists the compared options that differ, ticked by default
// when they are in the default group.
type sqlConfigDiff struct {
	Name    string `json:"name"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Default bool   `json:"default"`
}

func sqlConfigDiffs(src, dst sqlObjects) []sqlConfigDiff {
	out := []sqlConfigDiff{}
	for _, group := range [][]string{sqlConfigDefault, sqlConfigShown} {
		for _, name := range group {
			a, ok := src.Config[name]
			b := dst.Config[name]
			if !ok || a == b {
				continue
			}
			out = append(out, sqlConfigDiff{Name: name, Old: a, New: b, Default: sqlHasFold(sqlConfigDefault, name)})
		}
	}
	return out
}

func sqlPermPattern(p string) bool {
	if p == "" || len(p) > 60 {
		return false
	}
	for _, r := range p {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == ' ') {
			return false
		}
	}
	return true
}

func sqlHasFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func sqlHasFoldOp(list []sqlOperator, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x.Name, s) {
			return true
		}
	}
	return false
}

func sqlHexOK(s string) bool {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") || len(s) < 4 || len(s) > 600 {
		return false
	}
	for _, r := range s[2:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// sqlLoginSQL creates one login. SQL logins keep their SID and password
// hash, so applications and database users keep working with the same
// password; Windows logins come from the domain. The default database is
// master until the database has moved (cutover sets it).
func sqlLoginSQL(l sqlLogin) (string, string) {
	q := sqlName(l.Name)
	var b strings.Builder
	b.WriteString("IF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = " + sqlStr(l.Name) + ")\nBEGIN\n")
	lang := ""
	if l.Language != "" {
		lang = ", DEFAULT_LANGUAGE = " + sqlName(l.Language)
	}
	switch l.Type {
	case "S":
		if !sqlHexOK(l.SID) || !sqlHexOK(l.Hash) {
			return "", "Its password hash could not be read; create it on the new server by hand."
		}
		b.WriteString("  CREATE LOGIN " + q + " WITH PASSWORD = " + l.Hash + " HASHED, SID = " + l.SID + ", DEFAULT_DATABASE = [master]" + lang + ", CHECK_POLICY = OFF, CHECK_EXPIRATION = OFF;\n")
		if l.Policy {
			b.WriteString("  ALTER LOGIN " + q + " WITH CHECK_POLICY = ON;\n")
		}
		if l.Expiration {
			b.WriteString("  ALTER LOGIN " + q + " WITH CHECK_EXPIRATION = ON;\n")
		}
	case "U", "G":
		b.WriteString("  CREATE LOGIN " + q + " FROM WINDOWS WITH DEFAULT_DATABASE = [master]" + lang + ";\n")
	default:
		return "", "Logins of this kind are not copied."
	}
	if l.Disabled {
		b.WriteString("  ALTER LOGIN " + q + " DISABLE;\n")
	}
	if l.Deny {
		b.WriteString("  DENY CONNECT SQL TO " + q + ";\n")
	}
	b.WriteString("END;")
	detail := ""
	if l.Type == "S" {
		detail = "Same SID and password."
	}
	return b.String(), detail
}

// sqlDefaultDBBatch sets the default database of the copied logins whose
// default was a database that has now moved.
func sqlDefaultDBBatch(logins []sqlLogin, oldName, newName string) *sqlBatch {
	var b strings.Builder
	for _, l := range logins {
		if strings.EqualFold(l.DefaultDB, oldName) {
			b.WriteString("IF EXISTS (SELECT 1 FROM sys.server_principals WHERE name = " + sqlStr(l.Name) + " AND default_database_name = N'master') ALTER LOGIN " + sqlName(l.Name) + " WITH DEFAULT_DATABASE = " + sqlName(newName) + ";\n")
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return &sqlBatch{On: sqlDst, Name: "default database " + newName, Kind: "defaultdb", Timeout: 120, Continue: true, SQL: "SET NOCOUNT ON;\n" + b.String()}
}

// sqlJobSQL creates one job, disabled, with its steps, schedules and
// notification, in one transaction.
func sqlJobSQL(j sqlJob, dstLogins map[string]sqlLogin, created map[string]bool, dbNames map[string]string) (string, []string) {
	notes := []string{}
	var b strings.Builder
	n := sqlStr(j.Name)
	b.WriteString("IF NOT EXISTS (SELECT 1 FROM msdb.dbo.sysjobs WHERE name = " + n + ")\nBEGIN\nBEGIN TRY\nBEGIN TRANSACTION;\n")
	owner := ""
	if j.Owner != "" {
		if _, ok := dstLogins[strings.ToLower(j.Owner)]; ok || created[strings.ToLower(j.Owner)] || strings.EqualFold(j.Owner, "sa") {
			owner = ", @owner_login_name = " + sqlStr(j.Owner)
		} else {
			notes = append(notes, "Its owner "+j.Owner+" is not on the new server, so the account running the copy owns it.")
		}
	}
	cat := ""
	if j.Category != "" {
		cat = ", @category_name = " + sqlStr(j.Category)
	}
	notify := ""
	if j.Operator != "" && j.Email > 0 {
		notify = fmt.Sprintf(", @notify_level_email = %d, @notify_email_operator_name = %s", j.Email, sqlStr(j.Operator))
	}
	b.WriteString(fmt.Sprintf("EXEC msdb.dbo.sp_add_job @job_name = %s, @enabled = 0, @description = %s%s%s, @notify_level_eventlog = %d%s;\n", n, sqlStr(j.Description), cat, owner, j.EventLog, notify))
	for _, s := range j.Steps {
		db := s.Database
		if moved, ok := dbNames[strings.ToLower(db)]; ok {
			db = moved
		}
		args := []string{
			"@job_name = " + n, fmt.Sprintf("@step_id = %d", s.ID), "@step_name = " + sqlStr(s.Name), "@subsystem = " + sqlStr(s.Subsystem),
			"@command = " + sqlStr(s.Command), fmt.Sprintf("@on_success_action = %d", s.OnSuccess), fmt.Sprintf("@on_success_step_id = %d", s.OnSuccessStep),
			fmt.Sprintf("@on_fail_action = %d", s.OnFail), fmt.Sprintf("@on_fail_step_id = %d", s.OnFailStep), fmt.Sprintf("@retry_attempts = %d", s.Retry),
			fmt.Sprintf("@retry_interval = %d", s.RetryWait), fmt.Sprintf("@flags = %d", s.Flags),
		}
		if db != "" {
			args = append(args, "@database_name = "+sqlStr(db))
		}
		if s.DBUser != "" {
			args = append(args, "@database_user_name = "+sqlStr(s.DBUser))
		}
		if s.Output != "" {
			args = append(args, "@output_file_name = "+sqlStr(s.Output))
			notes = append(notes, "Step "+s.Name+" writes to "+s.Output+"; check that folder exists on the new server.")
		}
		if s.Proxy != "" {
			args = append(args, "@proxy_name = "+sqlStr(s.Proxy))
			notes = append(notes, "Step "+s.Name+" runs as the proxy "+s.Proxy+", which must be created on the new server first.")
		}
		b.WriteString("EXEC msdb.dbo.sp_add_jobstep " + strings.Join(args, ", ") + ";\n")
	}
	if j.StartStep > 1 {
		b.WriteString(fmt.Sprintf("EXEC msdb.dbo.sp_update_job @job_name = %s, @start_step_id = %d;\n", n, j.StartStep))
	}
	for _, sc := range j.Schedules {
		b.WriteString(fmt.Sprintf("EXEC msdb.dbo.sp_add_jobschedule @job_name = %s, @name = %s, @enabled = %s, @freq_type = %d, @freq_interval = %d, @freq_subday_type = %d, @freq_subday_interval = %d, @freq_relative_interval = %d, @freq_recurrence_factor = %d, @active_start_date = %d, @active_end_date = %d, @active_start_time = %d, @active_end_time = %d;\n",
			n, sqlStr(sc.Name), sqlBit(sc.Enabled), sc.FreqType, sc.FreqInterval, sc.SubdayType, sc.SubdayEvery, sc.Relative, sc.Recurrence, sc.StartDate, sc.EndDate, sc.StartTime, sc.EndTime))
	}
	b.WriteString("EXEC msdb.dbo.sp_add_jobserver @job_name = " + n + ", @server_name = N'(local)';\nCOMMIT TRANSACTION;\nEND TRY\nBEGIN CATCH\nIF @@TRANCOUNT > 0 ROLLBACK TRANSACTION;\nDECLARE @m nvarchar(2048); SET @m = ERROR_MESSAGE(); RAISERROR(@m, 16, 1);\nEND CATCH;\nEND;")
	return b.String(), notes
}

// sqlLinkedSQL creates one linked server with its options and the login
// mappings that use the caller's own credentials. Mappings to a remote
// login with a password cannot be copied: their names are returned.
func sqlLinkedSQL(l sqlLinked) (string, []string) {
	var b strings.Builder
	n := sqlStr(l.Name)
	b.WriteString("IF NOT EXISTS (SELECT 1 FROM sys.servers WHERE name = " + n + ")\nBEGIN\n")
	if strings.EqualFold(l.Product, "SQL Server") {
		b.WriteString("  EXEC master.dbo.sp_addlinkedserver @server = " + n + ", @srvproduct = N'SQL Server';\n")
	} else {
		b.WriteString("  EXEC master.dbo.sp_addlinkedserver @server = " + n + ", @srvproduct = " + sqlStr(l.Product) + ", @provider = " + sqlStr(l.Provider) + ", @datasrc = " + sqlStr(l.Source) + ", @location = " + sqlStr(l.Location) + ", @provstr = " + sqlStr(l.ProvStr) + ", @catalog = " + sqlStr(l.Catalog) + ";\n")
	}
	opt := func(name string, on bool) {
		b.WriteString("  EXEC master.dbo.sp_serveroption @server = " + n + ", @optname = " + sqlStr(name) + ", @optvalue = " + sqlStr(map[bool]string{true: "true", false: "false"}[on]) + ";\n")
	}
	opt("rpc out", l.RPCOut)
	opt("data access", l.DataAccess)
	opt("collation compatible", l.CollationCompatible)
	opt("use remote collation", l.RemoteCollation)
	manual := []string{}
	selfAll := false
	for _, m := range l.Logins {
		if m.Local == "" && m.Self {
			selfAll = true
		}
	}
	if !selfAll {
		// sp_addlinkedserver adds a mapping of every login to itself; the
		// old server had none, so the new linked server must not either.
		b.WriteString("  EXEC master.dbo.sp_droplinkedsrvlogin @rmtsrvname = " + n + ", @locallogin = NULL;\n")
	}
	for _, m := range l.Logins {
		local := "NULL"
		if m.Local != "" {
			local = sqlStr(m.Local)
		}
		if m.Self {
			if m.Local != "" {
				b.WriteString("  IF EXISTS (SELECT 1 FROM sys.server_principals WHERE name = " + sqlStr(m.Local) + ") EXEC master.dbo.sp_addlinkedsrvlogin @rmtsrvname = " + n + ", @useself = N'True', @locallogin = " + local + ";\n")
			}
			continue
		}
		who := m.Local
		if who == "" {
			who = "every login"
		}
		manual = append(manual, who+" as "+m.Remote)
	}
	b.WriteString("END;")
	return b.String(), manual
}

// ---------------------------------------------------------------------
// TDE certificates.

// sqlTDECertBatch reads the certificate protecting a database's encryption
// key on the old server.
func sqlTDECertBatch(db string) sqlBatch {
	return sqlBatch{On: sqlOn, Name: "certificate " + db, Kind: "tdecert", Timeout: 60, Args: map[string]string{"db": db},
		SQL: "SET NOCOUNT ON;\nSELECT c.name, CONVERT(varchar(100), c.thumbprint, 1) AS thumbprint, k.encryptor_type AS encryptorType FROM sys.dm_database_encryption_keys k LEFT JOIN master.sys.certificates c ON c.thumbprint = k.encryptor_thumbprint WHERE k.database_id = DB_ID(" + sqlStr(db) + ");"}
}

// sqlCopyCertBatches back the certificate up from the old server into the
// plan's folder (its private key encrypted with a password made for this
// copy only and never stored) and create it on the new server, with a
// database master key there when it has none (protected by the service
// master key, so its password is not needed again).
func sqlCopyCertBatches(cert, thumbprint, folder, filePassword, masterPassword string, needMaster bool) []sqlBatch {
	cer := sqlJoin(folder, "cert-"+sqlFileSafe(cert)+"-"+strings.ToLower(strings.TrimPrefix(thumbprint, "0x"))[:8]+".cer")
	pvk := strings.TrimSuffix(cer, ".cer") + ".pvk"
	src := "SET NOCOUNT ON;\nUSE master;\nBACKUP CERTIFICATE " + sqlName(cert) + " TO FILE = " + sqlStr(cer) + " WITH PRIVATE KEY (FILE = " + sqlStr(pvk) + ", ENCRYPTION BY PASSWORD = " + sqlStr(filePassword) + ");"
	dst := "SET NOCOUNT ON;\nUSE master;\n"
	if needMaster {
		dst += "IF NOT EXISTS (SELECT 1 FROM sys.symmetric_keys WHERE name = '##MS_DatabaseMasterKey##') CREATE MASTER KEY ENCRYPTION BY PASSWORD = " + sqlStr(masterPassword) + ";\n"
	}
	dst += "IF NOT EXISTS (SELECT 1 FROM sys.certificates WHERE thumbprint = " + thumbprint + ") CREATE CERTIFICATE " + sqlName(cert) + " FROM FILE = " + sqlStr(cer) + " WITH PRIVATE KEY (FILE = " + sqlStr(pvk) + ", DECRYPTION BY PASSWORD = " + sqlStr(filePassword) + ");"
	return []sqlBatch{
		{On: sqlOn, Name: "export certificate " + cert, Kind: "certout", Timeout: 120, Args: map[string]string{"cert": cert}, SQL: src},
		{On: sqlDst, Name: "import certificate " + cert, Kind: "certin", Timeout: 120, Args: map[string]string{"cert": cert, "thumbprint": thumbprint}, SQL: dst},
	}
}

// ---------------------------------------------------------------------
// Jobs at cutover.

// sqlJobsFor lists the copied jobs whose step databases have all moved
// (or that name none of the plan's databases and the plan is finished).
func sqlJobsFor(jobs []sqlJob, moved map[string]bool, planDBs map[string]bool, all bool) []sqlJob {
	out := []sqlJob{}
	for _, j := range jobs {
		if !j.Enabled || j.MaintenancePlan {
			continue
		}
		uses, ready := false, true
		for _, s := range j.Steps {
			db := strings.ToLower(s.Database)
			if planDBs[db] {
				uses = true
				if !moved[db] {
					ready = false
				}
			}
		}
		if (uses && ready) || (!uses && all) {
			out = append(out, j)
		}
	}
	return out
}

func sqlJobEnableBatch(on string, names []string, enabled bool) sqlBatch {
	var b strings.Builder
	b.WriteString("SET NOCOUNT ON;\n")
	for _, n := range names {
		b.WriteString("IF EXISTS (SELECT 1 FROM msdb.dbo.sysjobs WHERE name = " + sqlStr(n) + " AND enabled = " + sqlBit(!enabled) + ") EXEC msdb.dbo.sp_update_job @job_name = " + sqlStr(n) + ", @enabled = " + sqlBit(enabled) + ";\n")
	}
	kind := "jobsoff"
	if enabled {
		kind = "jobson"
	}
	return sqlBatch{On: on, Name: map[bool]string{true: "turn jobs on", false: "turn jobs off"}[enabled], Kind: kind, Timeout: 120, Continue: true, Args: map[string]string{"jobs": strings.Join(names, "\n")}, SQL: b.String()}
}
