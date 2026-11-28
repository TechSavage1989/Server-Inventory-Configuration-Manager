package main

// SQL Server migration plans: seeding, log shipping rounds, distributed AG
// health, tests, cutover, rollback and the DNS alias. The runner decides
// what runs and when; it reaches the servers only through exec (T-SQL
// batches) and dns (the alias), which sqlmig_windows.go provides and tests
// replace with fakes.
//
// Rules kept here:
//   - nothing on the new server that exists already is replaced: restores
//     run without REPLACE (a re-seed replaces only the plan's own database
//     while it is still restoring), objects are created only when missing;
//   - the old database is stopped only at cutover, by a tail-log backup
//     WITH NORECOVERY (log shipping) or READ_ONLY (backup and restore), so
//     rollback brings it back exactly as it was;
//   - a cutover that fails before the new database is online brings the
//     old one back at once; nothing is ever dropped except this plan's own
//     test copies and distributed AG, each checked by name and identity.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type sqlBrief struct {
	Server         string `json:"server"`
	Machine        string `json:"machine"`
	ServerName     string `json:"serverName"`
	FQDN           string `json:"fqdn"`
	Version        string `json:"version"`
	Major          int    `json:"major"`
	Edition        string `json:"edition"`
	Enterprise     bool   `json:"enterprise"`
	Express        bool   `json:"express"`
	Compression    bool   `json:"compression"`
	ServiceAccount string `json:"serviceAccount"`
	Domain         string `json:"domain"`
	DNSDomain      string `json:"dnsDomain"`
	Port           int    `json:"port"`
	DataPath       string `json:"dataPath"`
	LogPath        string `json:"logPath"`
	EndpointName   string `json:"endpointName"`
	EndpointPort   int    `json:"endpointPort"`
	ClusterNeeded  bool   `json:"clusterNeeded"`
}

func sqlBriefOf(i *sqlInstance) sqlBrief {
	return sqlBrief{Server: i.Server, Machine: i.Machine, ServerName: i.ServerName, FQDN: i.FQDN(), Version: i.Version, Major: i.Major, Edition: i.Edition,
		Enterprise: i.Enterprise(), Express: i.Express(), Compression: i.Compression(), ServiceAccount: i.ServiceAccount, Domain: i.Domain, DNSDomain: i.DNSDomain,
		Port: i.Port, DataPath: i.DataPath, LogPath: i.LogPath, EndpointName: i.Endpoint.Name, EndpointPort: i.Endpoint.Port, ClusterNeeded: i.ClusterTypeNeeded()}
}

// instance rebuilds the facts the T-SQL builders need.
func (b sqlBrief) instance() *sqlInstance {
	i := &sqlInstance{Server: b.Server, Machine: b.Machine, ServerName: b.ServerName, Version: b.Version, Major: b.Major, Edition: b.Edition,
		ServiceAccount: b.ServiceAccount, Domain: b.Domain, DNSDomain: b.DNSDomain, Port: b.Port, DataPath: b.DataPath, LogPath: b.LogPath,
		Endpoint: sqlHadrEP{Name: b.EndpointName, Port: b.EndpointPort}, Clustered: !b.ClusterNeeded}
	if b.Enterprise {
		i.EngineEdition = 3
	} else if b.Express {
		i.EngineEdition = 4
	}
	if b.FQDN != "" && strings.Contains(b.FQDN, ".") && i.DNSDomain == "" {
		i.DNSDomain = b.FQDN[strings.Index(b.FQDN, ".")+1:]
	}
	return i
}

type sqlPlan struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Created     time.Time       `json:"created"`
	Source      sqlEndpoint     `json:"source"`
	Target      sqlEndpoint     `json:"target"`
	SourceInfo  sqlBrief        `json:"sourceInfo"`
	TargetInfo  sqlBrief        `json:"targetInfo"`
	BackupPath  string          `json:"backupPath"`
	DataPath    string          `json:"dataPath"`
	LogPath     string          `json:"logPath"`
	SyncMinutes int             `json:"syncMinutes"`
	Concurrency int             `json:"concurrency"`
	StartAt     time.Time       `json:"startAt"`
	Paused      bool            `json:"paused"`
	Objects     sqlObjectChoice `json:"objects"`
	Alias       sqlAlias        `json:"alias"`
	AliasState  *sqlAliasState  `json:"aliasState,omitempty"`
	DAGs        []*sqlDAGLink   `json:"dags,omitempty"`
	Databases   []*sqlDB        `json:"databases"`
	ObjectsAt   time.Time       `json:"objectsAt"`
	ObjectItems []sqlObjectItem `json:"objectItems,omitempty"`
	ObjectsNote string          `json:"objectsNote,omitempty"`
	Certs       []string        `json:"certs,omitempty"`   // thumbprints already on the new server
	JobsOn      []string        `json:"jobsOn,omitempty"`  // jobs this plan turned on on the new server
	JobsOff     []string        `json:"jobsOff,omitempty"` // jobs this plan turned off on the old server
	Note        string          `json:"note,omitempty"`
}

type sqlDB struct {
	Name         string         `json:"name"`
	TargetName   string         `json:"targetName,omitempty"`
	Method       string         `json:"method"`
	Why          string         `json:"why,omitempty"`
	Wave         int            `json:"wave"`
	SwitchToFull bool           `json:"switchToFull,omitempty"`
	Facts        sqlDatabase    `json:"facts"`
	State        string         `json:"state"`
	Step         string         `json:"step,omitempty"`
	Error        string         `json:"error,omitempty"`
	Failures     int            `json:"failures,omitempty"`
	NextTry      time.Time      `json:"nextTry"`
	Seeded       time.Time      `json:"seeded"`
	LastRound    time.Time      `json:"lastRound"`
	LastLSN      string         `json:"lastLsn,omitempty"`
	Chain        []sqlBackupRef `json:"chain,omitempty"`
	SwitchedFull bool           `json:"switchedFull,omitempty"`
	CutoverAt    time.Time      `json:"cutoverAt"`
	CutAt        time.Time      `json:"cutAt"`
	TestedAt     time.Time      `json:"testedAt"`
	Test         *sqlTest       `json:"test,omitempty"`
	SourceHeld   string         `json:"sourceHeld,omitempty"` // restoring or readonly: what cutover did to the old database
	RolledBackAt time.Time      `json:"rolledBackAt"`
	Sync         string         `json:"sync,omitempty"`    // DAG synchronization state on the new server
	QueueKB      float64        `json:"queueKB,omitempty"` // DAG log send plus redo queue
	Note         string         `json:"note,omitempty"`
}

func (d *sqlDB) target() string {
	if strings.TrimSpace(d.TargetName) != "" {
		return d.TargetName
	}
	return d.Name
}

// Owns reports whether a database on the new server is this plan's copy.
func (d *sqlDB) Owns(t *sqlDatabase) bool {
	return t != nil && strings.EqualFold(t.Name, d.target()) && (!d.Seeded.IsZero() || !d.CutAt.IsZero())
}

type sqlBackupRef struct {
	Path  string    `json:"path"`
	Kind  string    `json:"kind"` // full or log
	First string    `json:"first"`
	Last  string    `json:"last"`
	At    time.Time `json:"at"`
	Other bool      `json:"other,omitempty"` // another tool's backup restored to fill a gap
}

type sqlTest struct {
	Name    string    `json:"name"`
	ID      int       `json:"id"`
	Created string    `json:"created"`
	At      time.Time `json:"at"`
}

type sqlDAGLink struct {
	Name      string    `json:"name"`
	SourceAG  string    `json:"sourceAg"`
	TargetAG  string    `json:"targetAg"`
	SourceURL string    `json:"sourceUrl"`
	TargetURL string    `json:"targetUrl"`
	Created   time.Time `json:"created"`
	CutAt     time.Time `json:"cutAt"`
	Removed   time.Time `json:"removed"`
}

type sqlAliasState struct {
	Previous  string    `json:"previous"` // the CNAME's target before, "" when the record was made
	Created   bool      `json:"created"`
	Target    string    `json:"target"`
	ChangedAt time.Time `json:"changedAt"`
	SPNs      []string  `json:"spns,omitempty"`
	SPNFrom   string    `json:"spnFrom,omitempty"` // account the SPNs were moved from
	Restored  time.Time `json:"restored"`
}

func (p *sqlPlan) dataPath(dst *sqlInstance) string {
	if strings.TrimSpace(p.DataPath) != "" {
		return strings.TrimRight(p.DataPath, `\`)
	}
	return dst.DataPath
}

func (p *sqlPlan) logPath(dst *sqlInstance) string {
	if strings.TrimSpace(p.LogPath) != "" {
		return strings.TrimRight(p.LogPath, `\`)
	}
	return dst.LogPath
}

func (p *sqlPlan) db(name string) *sqlDB {
	for _, d := range p.Databases {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

func (p *sqlPlan) dag(ag string) *sqlDAGLink {
	for _, l := range p.DAGs {
		if strings.EqualFold(l.SourceAG, ag) {
			return l
		}
	}
	return nil
}

func (p *sqlPlan) dbNames() map[string]string {
	out := map[string]string{}
	for _, d := range p.Databases {
		out[strings.ToLower(d.Name)] = d.target()
	}
	return out
}

// sqlDNSRequest and sqlDNSResult are one call to the DNS server.
type sqlDNSRequest struct {
	Server     string   `json:"server"`
	Zone       string   `json:"zone"`
	Name       string   `json:"name"`
	Mode       string   `json:"mode"` // read, set, remove, spn, unspn
	Target     string   `json:"target,omitempty"`
	Expect     string   `json:"expect,omitempty"` // remove only when the record still points here
	SPNAccount string   `json:"spnAccount,omitempty"`
	SPNs       []string `json:"spns,omitempty"`
}

type sqlDNSResult struct {
	Type     string   `json:"type"`
	Value    string   `json:"value"`
	SPNOwner string   `json:"spnOwner"`
	Notes    []string `json:"notes"`
}

type sqlRunner struct {
	mu       sync.Mutex
	path     string
	plans    []*sqlPlan
	exec     func(ctx context.Context, plan *sqlPlan, batches []sqlBatch) ([]sqlBatchResult, error)
	dns      func(ctx context.Context, plan *sqlPlan, req sqlDNSRequest) (sqlDNSResult, error)
	log      func(plan *sqlPlan, text string)
	now      func() time.Time
	ready    func(plan *sqlPlan) string
	guard    *pgGuard
	running  map[string]string // work key: what runs
	releases map[string]func()
	// hold marks a step that must not be cut short (cutover, test,
	// rollback): closing and update install wait for it.
	hold    func() (func(), bool)
	cancels map[string]context.CancelFunc
	secret  func() string
	poll    time.Duration
}

const sqlMaxRunning = 4

func newSQLRunner(path string) *sqlRunner {
	return &sqlRunner{path: path, now: time.Now, log: func(*sqlPlan, string) {}, ready: func(*sqlPlan) string { return "" },
		running: map[string]string{}, releases: map[string]func(){}, cancels: map[string]context.CancelFunc{}, secret: sqlRandomSecret, poll: 15 * time.Second}
}

func sqlRandomSecret() string {
	b := make([]byte, 24)
	rand.Read(b)
	return "Simm!" + hex.EncodeToString(b) + "aZ9"
}

func (r *sqlRunner) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		r.plans = []*sqlPlan{}
		return nil
	}
	if err != nil {
		return err
	}
	plans := []*sqlPlan{}
	if err := json.Unmarshal(stripUTF8BOMBytes(raw), &plans); err != nil {
		return fmt.Errorf("%s: %w", r.path, err)
	}
	for _, p := range plans {
		for _, d := range p.Databases {
			switch d.State {
			case sqlSeeding, sqlSyncing:
				d.State, d.Error, d.Step = sqlFailed, "The application stopped during this step; it runs again by itself.", ""
				d.NextTry = time.Time{}
			case sqlCutting:
				d.State, d.Step, d.CutoverAt = sqlFailed, "", time.Time{}
				d.Error = "The application stopped during the cutover. Check the old database (it may be restoring or read only) and use Roll back or Cut over again."
			}
		}
	}
	r.plans = plans
	return nil
}

// Caller holds r.mu.
func (r *sqlRunner) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(r.plans, "", "  ")
	if err != nil {
		return err
	}
	return replWriteFileAtomic(r.path, raw)
}

func (r *sqlRunner) planLocked(id string) (*sqlPlan, error) {
	for _, p := range r.plans {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, errors.New("that SQL migration plan no longer exists")
}

// View is a copy of every plan for the page.
func (r *sqlRunner) View() []sqlPlan {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlPlan, 0, len(r.plans))
	for _, p := range r.plans {
		data, _ := json.Marshal(p)
		var c sqlPlan
		json.Unmarshal(data, &c)
		out = append(out, c)
	}
	return out
}

// Busy reports whether anything runs (closing and update install wait).
func (r *sqlRunner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, what := range r.running {
		if what != "round" {
			return true
		}
	}
	return false
}

func (r *sqlRunner) Running() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for k, v := range r.running {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------
// Creating a plan.

type sqlCreateInput struct {
	Name        string          `json:"name"`
	Source      sqlEndpoint     `json:"source"`
	Target      sqlEndpoint     `json:"target"`
	BackupPath  string          `json:"backupPath"`
	DataPath    string          `json:"dataPath"`
	LogPath     string          `json:"logPath"`
	SyncMinutes int             `json:"syncMinutes"`
	Concurrency int             `json:"concurrency"`
	StartAt     time.Time       `json:"startAt"`
	Objects     sqlObjectChoice `json:"objects"`
	Alias       sqlAlias        `json:"alias"`
	Databases   []sqlDBInput    `json:"databases"`
}

type sqlDBInput struct {
	Name         string    `json:"name"`
	TargetName   string    `json:"targetName"`
	Method       string    `json:"method"`
	Wave         int       `json:"wave"`
	SwitchToFull bool      `json:"switchToFull"`
	CutoverAt    time.Time `json:"cutoverAt"`
}

var sqlPlanNamePattern = replVMNamePattern

func sqlCheckFolder(path, what string, unc bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if strings.Contains(path, "..") || strings.ContainsAny(path, "\"<>|?*\x00") || len(path) > 240 {
		return fmt.Errorf("the %s cannot contain .. or the characters \" < > | ? *", what)
	}
	isUNC := strings.HasPrefix(path, `\\`) && len(strings.Split(strings.TrimPrefix(path, `\\`), `\`)) >= 2
	isDrive := len(path) >= 3 && path[1] == ':' && path[2] == '\\' && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z'))
	if unc && !isUNC && !isDrive {
		return fmt.Errorf("type the %s as a share (\\\\server\\share\\folder) or a full drive path", what)
	}
	if !unc && !isDrive {
		return fmt.Errorf("type the %s as a full drive path on the new server (D:\\SQLData)", what)
	}
	return nil
}

// Create checks everything again on the servers' facts and adds the plan.
func (r *sqlRunner) Create(in sqlCreateInput, src, dst *sqlInstance) (*sqlPlan, []sqlFinding, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !sqlPlanNamePattern.MatchString(in.Name) {
		return nil, nil, errors.New("name the plan with letters, digits, spaces, dots, dashes, underscores or brackets")
	}
	if err := sqlCheckEndpoint(&in.Source, "old"); err != nil {
		return nil, nil, err
	}
	if err := sqlCheckEndpoint(&in.Target, "new"); err != nil {
		return nil, nil, err
	}
	if err := sqlCheckFolder(in.BackupPath, "backup folder", true); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.BackupPath) == "" {
		return nil, nil, errors.New("choose a backup folder both SQL Server services can reach")
	}
	if err := sqlCheckFolder(in.DataPath, "data folder", false); err != nil {
		return nil, nil, err
	}
	if err := sqlCheckFolder(in.LogPath, "log folder", false); err != nil {
		return nil, nil, err
	}
	if in.SyncMinutes == 0 {
		in.SyncMinutes = 15
	}
	if in.SyncMinutes < sqlMinSyncMinute || in.SyncMinutes > sqlMaxSyncMinute {
		return nil, nil, fmt.Errorf("ship the log every %d to %d minutes", sqlMinSyncMinute, sqlMaxSyncMinute)
	}
	if in.Concurrency == 0 {
		in.Concurrency = 2
	}
	if in.Concurrency < 1 || in.Concurrency > 4 {
		return nil, nil, errors.New("copy 1 to 4 databases at a time")
	}
	if len(in.Databases) == 0 {
		return nil, nil, errors.New("choose at least one database")
	}
	if len(in.Databases) > sqlMaxDatabases {
		return nil, nil, fmt.Errorf("a plan holds at most %d databases", sqlMaxDatabases)
	}
	if src == nil || dst == nil {
		return nil, nil, errors.New("read both servers first")
	}
	if !strings.EqualFold(src.Server, in.Source.Server) || !strings.EqualFold(dst.Server, in.Target.Server) {
		return nil, nil, errors.New("the servers were read under other names; read them again")
	}
	plan := &sqlPlan{ID: mbNewID(), Name: in.Name, Created: r.now().UTC(), Source: in.Source, Target: in.Target, SourceInfo: sqlBriefOf(src), TargetInfo: sqlBriefOf(dst),
		BackupPath: strings.TrimRight(strings.TrimSpace(in.BackupPath), `\`), DataPath: strings.TrimSpace(in.DataPath), LogPath: strings.TrimSpace(in.LogPath),
		SyncMinutes: in.SyncMinutes, Concurrency: in.Concurrency, StartAt: in.StartAt.UTC(), Objects: in.Objects, Alias: in.Alias}
	if plan.Alias.Name = strings.TrimSpace(plan.Alias.Name); plan.Alias.Name == "" {
		plan.Alias = sqlAlias{}
	}
	seen := map[string]bool{}
	targets := map[string]bool{}
	for _, d := range in.Databases {
		db := src.database(d.Name)
		if db == nil {
			return nil, nil, fmt.Errorf("%s is not on the old server; read the servers again", d.Name)
		}
		if seen[strings.ToLower(db.Name)] {
			continue
		}
		seen[strings.ToLower(db.Name)] = true
		t := strings.TrimSpace(d.TargetName)
		if t == db.Name {
			t = ""
		}
		if t != "" && (len(t) > 120 || strings.ContainsAny(t, "\x00[]")) {
			return nil, nil, fmt.Errorf("the new name of %s cannot contain brackets", db.Name)
		}
		name := t
		if name == "" {
			name = db.Name
		}
		if targets[strings.ToLower(name)] {
			return nil, nil, fmt.Errorf("two databases would be called %s on the new server", name)
		}
		targets[strings.ToLower(name)] = true
		choice := sqlRecommend(src, dst, db)
		method := d.Method
		if method == "" {
			method = choice.Method
		}
		if _, ok := sqlMethodNames[method]; !ok {
			return nil, nil, fmt.Errorf("choose how %s moves", db.Name)
		}
		why := choice.Why
		if method != choice.Method {
			why = "Chosen by you."
		}
		if d.Wave < 0 || d.Wave > 99 {
			return nil, nil, errors.New("waves are numbered 0 to 99")
		}
		plan.Databases = append(plan.Databases, &sqlDB{Name: db.Name, TargetName: t, Method: method, Why: why, Wave: d.Wave, SwitchToFull: d.SwitchToFull && method == sqlMethodLogShip, Facts: *db, State: sqlWaiting, CutoverAt: d.CutoverAt.UTC()})
	}
	findings := sqlCheckPlan(src, dst, plan)
	if b := sqlBlockers(findings); len(b) > 0 {
		return nil, findings, fmt.Errorf("%s", sqlFindingText(b[0]))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.plans) >= sqlMaxPlans {
		return nil, findings, fmt.Errorf("there are already %d SQL plans; delete a finished one first", sqlMaxPlans)
	}
	for _, p := range r.plans {
		if strings.EqualFold(p.Name, plan.Name) {
			return nil, findings, errors.New("a SQL plan with that name exists; choose another name")
		}
		for _, d := range p.Databases {
			if d.State != sqlDone && d.State != sqlRolledBack && strings.EqualFold(p.Source.Server, plan.Source.Server) && plan.db(d.Name) != nil {
				return nil, findings, fmt.Errorf("%s is already in the SQL plan %s", d.Name, p.Name)
			}
		}
	}
	r.plans = append(r.plans, plan)
	if err := r.saveLocked(); err != nil {
		return nil, findings, err
	}
	r.log(plan, fmt.Sprintf("Plan created: %d databases from %s to %s.", len(plan.Databases), plan.Source.Server, plan.Target.Server))
	return plan, findings, nil
}

func sqlFindingText(f sqlFinding) string {
	if f.DB != "" {
		return f.DB + ": " + f.Message
	}
	return f.Message
}

// ---------------------------------------------------------------------
// Scheduling.

type sqlWork struct {
	plan *sqlPlan
	dbs  []*sqlDB
	kind string // objects, seed, round, cutover
	key  string
}

func sqlWorkKey(planID, name string) string { return planID + "|" + strings.ToLower(name) }

// lsnLess compares two LSNs written as decimal numbers.
func sqlLSNLess(a, b string) bool {
	x, okA := new(big.Int).SetString(strings.TrimSpace(a), 10)
	y, okB := new(big.Int).SetString(strings.TrimSpace(b), 10)
	if !okA || !okB {
		return false
	}
	return x.Cmp(y) < 0
}

func (r *sqlRunner) runningCountLocked(planID string) (plan, all int) {
	for k, what := range r.running {
		if what == "cutover" {
			continue
		}
		all++
		if strings.HasPrefix(k, planID+"|") {
			plan++
		}
	}
	return
}

// dueLocked lists the work due now, cutovers first.
func (r *sqlRunner) dueLocked(now time.Time) []sqlWork {
	cut, other := []sqlWork{}, []sqlWork{}
	for _, p := range r.plans {
		if r.ready(p) != "" {
			continue
		}
		// Objects first, once, before anything is seeded.
		wantObjects := p.Objects.Logins || p.Objects.Jobs || p.Objects.LinkedServers || p.Objects.Operators || len(p.Objects.Config) > 0
		if wantObjects && p.ObjectsAt.IsZero() && !p.Paused && !now.Before(p.StartAt) {
			if _, busy := r.running[sqlWorkKey(p.ID, "*objects")]; !busy {
				other = append(other, sqlWork{plan: p, kind: "objects", key: sqlWorkKey(p.ID, "*objects")})
			}
			continue
		}
		dagDue := map[string][]*sqlDB{}
		for _, d := range p.Databases {
			key := sqlWorkKey(p.ID, d.Name)
			if _, busy := r.running[key]; busy {
				continue
			}
			if d.Method == sqlMethodDAG && d.Facts.AG != "" {
				if _, busy := r.running[sqlWorkKey(p.ID, "*ag "+d.Facts.AG)]; busy {
					continue
				}
			}
			if !d.CutoverAt.IsZero() && !now.Before(d.CutoverAt) {
				switch {
				case d.State == sqlReady || (d.Method == sqlMethodBackup && (d.State == sqlWaiting || d.State == sqlFailed && d.CutAt.IsZero())):
					if d.Method == sqlMethodDAG {
						dagDue[d.Facts.AG] = append(dagDue[d.Facts.AG], d)
					} else {
						cut = append(cut, sqlWork{plan: p, dbs: []*sqlDB{d}, kind: "cutover", key: key})
					}
				case d.State == sqlTesting:
					// waits for End test
				case d.Seeded.IsZero() && d.Method != sqlMethodBackup:
					d.CutoverAt = time.Time{}
					d.Note = "The scheduled cutover was cancelled: the first copy had not finished. Schedule it again once it is in sync."
				}
				continue
			}
			if p.Paused || now.Before(p.StartAt) {
				continue
			}
			switch {
			case d.Method == sqlMethodBackup:
				// Copied at cutover only.
			case d.State == sqlWaiting:
				other = append(other, sqlWork{plan: p, dbs: []*sqlDB{d}, kind: "seed", key: key})
			case d.State == sqlFailed && d.CutAt.IsZero() && !d.NextTry.IsZero() && !now.Before(d.NextTry):
				if d.Seeded.IsZero() {
					other = append(other, sqlWork{plan: p, dbs: []*sqlDB{d}, kind: "seed", key: key})
				} else {
					other = append(other, sqlWork{plan: p, dbs: []*sqlDB{d}, kind: "round", key: key})
				}
			case d.State == sqlReady || d.State == sqlTesting || (d.State == sqlSeeding && d.Method == sqlMethodDAG):
				if d.Method == sqlMethodDAG && d.State == sqlSeeding {
					if now.Sub(d.LastRound) >= time.Minute {
						other = append(other, sqlWork{plan: p, dbs: []*sqlDB{d}, kind: "round", key: key})
					}
				} else if now.Sub(d.LastRound) >= time.Duration(p.SyncMinutes)*time.Minute {
					other = append(other, sqlWork{plan: p, dbs: []*sqlDB{d}, kind: "round", key: key})
				}
			}
		}
		for ag, dbs := range dagDue {
			cut = append(cut, sqlWork{plan: p, dbs: r.agDBsLocked(p, ag, dbs), kind: "cutover", key: sqlWorkKey(p.ID, "*ag "+ag)})
		}
	}
	sort.SliceStable(other, func(a, b int) bool {
		wa, wb := 0, 0
		if len(other[a].dbs) > 0 {
			wa = other[a].dbs[0].Wave
		}
		if len(other[b].dbs) > 0 {
			wb = other[b].dbs[0].Wave
		}
		return wa < wb
	})
	return append(cut, other...)
}

// agDBsLocked is every database of the plan in that availability group.
func (r *sqlRunner) agDBsLocked(p *sqlPlan, ag string, fallback []*sqlDB) []*sqlDB {
	out := []*sqlDB{}
	for _, d := range p.Databases {
		if d.Method == sqlMethodDAG && strings.EqualFold(d.Facts.AG, ag) {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// Tick starts the work that is due, within the caps. It returns at once;
// the work runs in the background.
func (r *sqlRunner) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.dueLocked(r.now()) {
		inPlan, all := r.runningCountLocked(w.plan.ID)
		if w.kind != "cutover" && (inPlan >= w.plan.Concurrency || all >= sqlMaxRunning) {
			continue
		}
		if (w.kind == "seed" || w.kind == "round") && r.guard != nil {
			release, why := r.guard.TryAcquire("sql:"+w.key, pgSlotKeys{SourceHost: "sql:" + strings.ToLower(w.plan.SourceInfo.Machine), Target: "sql:" + strings.ToLower(w.plan.TargetInfo.Machine)})
			if why != "" {
				for _, d := range w.dbs {
					d.Step = "Waiting: " + why
				}
				continue
			}
			r.releases[w.key] = release
		}
		if w.kind != "round" && r.hold != nil {
			done, ok := r.hold()
			if !ok {
				if release := r.releases[w.key]; release != nil {
					delete(r.releases, w.key)
					release()
				}
				continue
			}
			prev := r.releases[w.key]
			r.releases[w.key] = func() {
				done()
				if prev != nil {
					prev()
				}
			}
		}
		r.start(w)
	}
	r.saveLocked()
}

// start marks the work running and runs it. Caller holds r.mu.
func (r *sqlRunner) start(w sqlWork) {
	r.running[w.key] = w.kind
	ctx, cancel := context.WithCancel(context.Background())
	if w.kind != "cutover" {
		r.cancels[w.key] = cancel
	}
	for _, d := range w.dbs {
		switch w.kind {
		case "seed":
			d.State, d.Step = sqlSeeding, "Starting the first copy."
		case "round":
			if d.State == sqlReady {
				d.State = sqlSyncing
			}
			d.Step = "Shipping the log."
		case "cutover":
			d.State, d.Step = sqlCutting, "Starting the cutover."
		}
	}
	go func() {
		defer cancel()
		var err error
		switch w.kind {
		case "objects":
			err = r.doObjects(ctx, w.plan)
		case "seed":
			err = r.doSeed(ctx, w.plan, w.dbs[0])
		case "round":
			err = r.doRound(ctx, w.plan, w.dbs[0])
		case "cutover":
			err = r.doCutover(context.Background(), w.plan, w.dbs)
		}
		r.finish(w, err)
	}()
}

func (r *sqlRunner) finish(w sqlWork, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, w.key)
	delete(r.cancels, w.key)
	if release := r.releases[w.key]; release != nil {
		delete(r.releases, w.key)
		release()
	}
	now := r.now().UTC()
	if w.kind == "objects" {
		if err != nil {
			w.plan.ObjectsNote = "Instance objects could not be read or copied: " + err.Error()
			w.plan.ObjectsAt = now // seeding goes on; the objects pass runs again at cutover
		}
	}
	for _, d := range w.dbs {
		d.Step = ""
		if err == nil {
			if d.State == sqlSyncing {
				d.State = sqlReady
			}
			d.Failures, d.Error = 0, ""
			continue
		}
		if errors.Is(err, context.Canceled) && w.kind != "cutover" {
			if d.State == sqlSyncing {
				d.State = sqlReady
			} else if d.State == sqlSeeding {
				d.State = sqlWaiting
			}
			continue
		}
		if d.State == sqlDone || d.State == sqlReady && w.kind == "cutover" {
			d.Note = err.Error()
			continue
		}
		d.State, d.Error = sqlFailed, err.Error()
		if w.kind == "cutover" {
			d.CutoverAt, d.NextTry = time.Time{}, time.Time{}
			continue
		}
		d.Failures++
		wait := 5 * time.Minute << uint(sqlMinInt(d.Failures-1, 2))
		if limit := time.Duration(w.plan.SyncMinutes) * time.Minute; wait > limit && limit >= 5*time.Minute {
			wait = limit
		}
		d.NextTry = now.Add(wait)
	}
	if err != nil {
		names := []string{}
		for _, d := range w.dbs {
			names = append(names, d.Name)
		}
		r.log(w.plan, fmt.Sprintf("%s of %s failed: %v", w.kind, strings.Join(names, ", "), err))
	}
	r.saveLocked()
}

func sqlMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (r *sqlRunner) setStep(d *sqlDB, text string) {
	r.mu.Lock()
	d.Step = text
	r.mu.Unlock()
}

func (r *sqlRunner) run(ctx context.Context, p *sqlPlan, batches ...sqlBatch) ([]sqlBatchResult, error) {
	res, err := r.exec(ctx, p, batches)
	if err != nil {
		return res, err
	}
	for _, x := range res {
		if !x.OK {
			if x.Error == "" {
				x.Error = "it failed"
			}
			for _, b := range batches {
				if b.Name == x.Name && !b.Continue {
					return res, fmt.Errorf("%s: %s", x.Name, x.Error)
				}
			}
		}
	}
	return res, nil
}

func sqlResult(res []sqlBatchResult, name string) *sqlBatchResult {
	for i := len(res) - 1; i >= 0; i-- {
		if res[i].Name == name {
			return &res[i]
		}
	}
	return nil
}

func sqlLSNs(res []sqlBatchResult, name string) (first, last string, err error) {
	x := sqlResult(res, name)
	if x == nil || len(x.rows()) == 0 {
		return "", "", errors.New("the backup was taken but msdb has no record of it")
	}
	row := x.rows()[0]
	return row.str("firstLsn"), row.str("lastLsn"), nil
}

// ---------------------------------------------------------------------
// Instance objects.

func (r *sqlRunner) readObjects(ctx context.Context, p *sqlPlan) (sqlObjects, sqlObjects, error) {
	srcRes, err := r.exec(ctx, p, sqlObjectBatches(sqlOn, p.SourceInfo.Major))
	if err != nil {
		return sqlObjects{}, sqlObjects{}, err
	}
	dstRes, err := r.exec(ctx, p, sqlObjectBatches(sqlDst, p.TargetInfo.Major))
	if err != nil {
		return sqlObjects{}, sqlObjects{}, err
	}
	for _, x := range append(append([]sqlBatchResult{}, srcRes...), dstRes...) {
		if !x.OK && x.Name == "logins" {
			return sqlObjects{}, sqlObjects{}, fmt.Errorf("logins could not be read: %s", x.Error)
		}
	}
	return sqlParseObjects(srcRes), sqlParseObjects(dstRes), nil
}

// doObjects copies the instance objects that are missing on the new server.
func (r *sqlRunner) doObjects(ctx context.Context, p *sqlPlan) error {
	_, err := r.copyObjects(ctx, p)
	return err
}

func (r *sqlRunner) copyObjects(ctx context.Context, p *sqlPlan) (sqlObjects, error) {
	src, dst, err := r.readObjects(ctx, p)
	if err != nil {
		return src, err
	}
	batches, items := sqlObjectPlan(src, dst, p.SourceInfo.instance(), p.TargetInfo.instance(), p.Objects, p.dbNames())
	if len(batches) > 0 {
		res, err := r.exec(ctx, p, batches)
		if err != nil {
			return src, err
		}
		for i := range items {
			if items[i].Action != "create" {
				continue
			}
			for _, x := range res {
				if strings.HasSuffix(x.Name, ": "+items[i].Name) && strings.HasPrefix(x.Name, items[i].Kind) {
					if x.OK {
						items[i].Action = "created"
					} else {
						items[i].Action, items[i].Detail = "failed", x.Error
					}
				}
			}
		}
	}
	created, failed := 0, 0
	for _, it := range items {
		switch it.Action {
		case "created":
			created++
		case "failed":
			failed++
		}
	}
	r.mu.Lock()
	p.ObjectsAt, p.ObjectItems = r.now().UTC(), items
	p.ObjectsNote = fmt.Sprintf("%d instance objects created on the new server", created)
	if failed > 0 {
		p.ObjectsNote += fmt.Sprintf(", %d failed (see the list)", failed)
	}
	p.ObjectsNote += "."
	r.saveLocked()
	r.mu.Unlock()
	r.log(p, p.ObjectsNote)
	return src, nil
}

// ensureCert copies the TDE certificate of an encrypted database once.
func (r *sqlRunner) ensureCert(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	res, err := r.run(ctx, p, sqlTDECertBatch(d.Name))
	if err != nil {
		return err
	}
	rows := sqlResult(res, "certificate "+d.Name).rows()
	if len(rows) == 0 {
		return nil
	}
	cert, thumb := rows[0].str("name"), rows[0].str("thumbprint")
	if cert == "" || !sqlHexOK(thumb) || len(thumb) < 12 {
		return fmt.Errorf("%s is encrypted with an asymmetric key or a key in an external key manager; move that key to the new server by hand before the first copy", d.Name)
	}
	r.mu.Lock()
	have := sqlHasFold(p.Certs, thumb)
	r.mu.Unlock()
	if have {
		return nil
	}
	r.setStep(d, "Copying the TDE certificate "+cert+".")
	folder := sqlPlanFolder(p)
	batches := []sqlBatch{sqlCreateFolderBatch(sqlOn, folder)}
	batches = append(batches, sqlCopyCertBatches(cert, thumb, folder, r.secret(), r.secret(), true)...)
	if _, err := r.run(ctx, p, batches...); err != nil {
		return fmt.Errorf("the TDE certificate %s could not be copied: %v", cert, err)
	}
	r.mu.Lock()
	p.Certs = append(p.Certs, thumb)
	r.mu.Unlock()
	r.log(p, "Copied the TDE certificate "+cert+" to the new server.")
	return nil
}

// ---------------------------------------------------------------------
// Seeding and log shipping.

func (r *sqlRunner) moves(p *sqlPlan, d *sqlDB, suffix string) []sqlMove {
	dst := p.TargetInfo.instance()
	return sqlMoves(d.target()+suffix, d.Facts.Files, p.dataPath(dst), p.logPath(dst), "")
}

// checkReachable makes sure the new server's SQL Server service can read
// what the old one wrote.
func (r *sqlRunner) checkReachable(ctx context.Context, p *sqlPlan, file string) error {
	res, err := r.run(ctx, p, sqlFileExistsBatch(sqlDst, file))
	if err != nil {
		return err
	}
	rows := sqlResult(res, "exists").rows()
	if len(rows) == 0 || !rows[0].flag("fileExists") {
		return fmt.Errorf("the new server's SQL Server service (%s) cannot see %s, which the old one wrote. Give both SQL Server service accounts (%s on the old server) read and write access to %s", sqlOr(p.TargetInfo.ServiceAccount, "its service account"), file, sqlOr(p.SourceInfo.ServiceAccount, "its service account"), p.BackupPath)
	}
	return nil
}

func sqlOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (r *sqlRunner) doSeed(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	if d.Method == sqlMethodDAG {
		return r.doSeedDAG(ctx, p, d)
	}
	if d.Facts.Encrypted {
		if err := r.ensureCert(ctx, p, d); err != nil {
			return err
		}
	}
	if d.SwitchToFull && strings.EqualFold(d.Facts.Recovery, "SIMPLE") && !d.SwitchedFull {
		r.setStep(d, "Switching to the full recovery model.")
		if _, err := r.run(ctx, p, sqlRecoveryFullBatch(d.Name)); err != nil {
			return err
		}
		r.mu.Lock()
		d.SwitchedFull = true
		r.saveLocked()
		r.mu.Unlock()
		r.log(p, d.Name+": switched to the full recovery model for log shipping.")
	}
	now := r.now()
	file := sqlBackupFile(p, d.Name, "full", now)
	copyOnly := !d.Facts.NoChain && !d.SwitchedFull
	r.setStep(d, "Full backup on the old server ("+sqlSizeText(d.Facts.DataMB)+").")
	res, err := r.run(ctx, p, sqlCreateFolderBatch(sqlOn, filepath.Dir(strings.ReplaceAll(file, `\`, "/"))), sqlBackupFullBatch(p, d.Name, file, copyOnly, p.SourceInfo.Compression))
	if err != nil {
		return err
	}
	first, last, err := sqlLSNs(res, "backup "+d.Name)
	if err != nil {
		return err
	}
	if err := r.checkReachable(ctx, p, file); err != nil {
		return err
	}
	r.setStep(d, "Restoring on the new server.")
	restore := sqlRestoreFullBatch(d.target(), file, r.moves(p, d, ""), true, false)
	if !d.Seeded.IsZero() {
		// A new seed after a broken chain replaces only this plan's own
		// copy, and only while it is still restoring.
		restore.SQL = "SET NOCOUNT ON;\nIF DATABASEPROPERTYEX(" + sqlStr(d.target()) + ", 'Status') <> 'RESTORING' RAISERROR(N'The copy on the new server is no longer restoring, so it was left alone.', 16, 1);\nELSE " + strings.TrimPrefix(strings.Replace(restore.SQL, "WITH ", "WITH REPLACE, ", 1), "SET NOCOUNT ON;\n")
	}
	if _, err := r.run(ctx, p, restore); err != nil {
		return err
	}
	r.mu.Lock()
	d.Chain = []sqlBackupRef{{Path: file, Kind: "full", First: first, Last: last, At: now.UTC()}}
	d.LastLSN, d.Seeded, d.LastRound, d.State = last, r.now().UTC(), r.now().UTC(), sqlReady
	r.saveLocked()
	r.mu.Unlock()
	r.log(p, fmt.Sprintf("%s: first copy restored on the new server (log shipping every %d minutes from now).", d.Name, p.SyncMinutes))
	return nil
}

func (r *sqlRunner) doRound(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	if d.Method == sqlMethodDAG {
		return r.doDAGHealth(ctx, p, d)
	}
	_, err := r.shipLog(ctx, p, d, false)
	return err
}

// shipLog backs the log up and restores it (and any gap another tool's log
// backups left) on the new server. tail is the cutover's last backup.
func (r *sqlRunner) shipLog(ctx context.Context, p *sqlPlan, d *sqlDB, tail bool) (string, error) {
	now := r.now()
	kind := "log"
	if tail {
		kind = "tail"
	}
	file := sqlBackupFile(p, d.Name, kind, now)
	r.setStep(d, map[bool]string{true: "Last log backup on the old server (it stops taking changes).", false: "Log backup on the old server."}[tail])
	b := sqlBackupLogBatch(p, d.Name, file, p.SourceInfo.Compression, tail)
	res, err := r.run(ctx, p, b)
	if err != nil {
		return file, err
	}
	first, last, err := sqlLSNs(res, b.Name)
	if err != nil {
		return file, err
	}
	if sqlLSNLess(d.LastLSN, first) {
		if err := r.fillGap(ctx, p, d, first); err != nil {
			return file, err
		}
	}
	if sqlLSNLess(d.LastLSN, last) {
		r.setStep(d, "Restoring the log on the new server.")
		if _, err := r.run(ctx, p, sqlRestoreLogBatch(d.target(), file)); err != nil {
			return file, err
		}
	}
	r.mu.Lock()
	d.Chain = append(d.Chain, sqlBackupRef{Path: file, Kind: "log", First: first, Last: last, At: now.UTC()})
	if sqlLSNLess(d.LastLSN, last) {
		d.LastLSN = last
	}
	d.LastRound = r.now().UTC()
	r.saveLocked()
	r.mu.Unlock()
	return file, nil
}

// fillGap restores another tool's log backups that came between this
// plan's last restored backup and the next one, when they are files.
func (r *sqlRunner) fillGap(ctx context.Context, p *sqlPlan, d *sqlDB, upTo string) error {
	r.setStep(d, "Another tool backed the log up; restoring its backups too.")
	q := sqlBatch{On: sqlOn, Name: "other log backups", Kind: "gap", Timeout: 120, Args: map[string]string{"db": d.Name, "from": d.LastLSN, "to": upTo}, SQL: "SET NOCOUNT ON;\nSELECT mf.physical_device_name AS path, mf.device_type AS deviceType, CONVERT(varchar(40), bs.first_lsn) AS firstLsn, CONVERT(varchar(40), bs.last_lsn) AS lastLsn FROM msdb.dbo.backupset bs JOIN msdb.dbo.backupmediafamily mf ON mf.media_set_id = bs.media_set_id WHERE bs.database_name = " + sqlStr(d.Name) + " AND bs.type = 'L' AND bs.last_lsn > " + sqlLSNLiteral(d.LastLSN) + " AND bs.first_lsn < " + sqlLSNLiteral(upTo) + " ORDER BY bs.first_lsn;"}
	res, err := r.run(ctx, p, q)
	if err != nil {
		return err
	}
	rows := sqlResult(res, "other log backups").rows()
	for _, row := range rows {
		if row.int("deviceType") != 2 {
			return fmt.Errorf("another tool backed up the log of %s to its own storage (%s), which the new server cannot restore from. Pause that tool's log backups for this database and choose Copy again from a full backup", d.Name, row.str("path"))
		}
		path := sqlUNCFor(row.str("path"), p.SourceInfo.Machine)
		if err := r.checkReachable(ctx, p, path); err != nil {
			return fmt.Errorf("another job backed up the log of %s to %s, and the new server cannot read it: %v", d.Name, path, err)
		}
		if _, err := r.run(ctx, p, sqlRestoreLogBatch(d.target(), path)); err != nil {
			return err
		}
		r.mu.Lock()
		d.Chain = append(d.Chain, sqlBackupRef{Path: path, Kind: "log", First: row.str("firstLsn"), Last: row.str("lastLsn"), At: r.now().UTC(), Other: true})
		d.LastLSN = row.str("lastLsn")
		r.mu.Unlock()
		r.log(p, d.Name+": restored another job's log backup "+path+".")
	}
	if sqlLSNLess(d.LastLSN, upTo) {
		return fmt.Errorf("the log chain of %s has a gap that no log backup file fills (another tool may have truncated the log). Choose Copy again from a full backup", d.Name)
	}
	return nil
}

func sqlLSNLiteral(lsn string) string {
	if _, ok := new(big.Int).SetString(strings.TrimSpace(lsn), 10); !ok {
		return "0"
	}
	return strings.TrimSpace(lsn)
}

// sqlUNCFor turns a drive path on the old server into its administrative
// share, so the new server can read another job's backup file.
func sqlUNCFor(path, machine string) string {
	if len(path) >= 3 && path[1] == ':' && path[2] == '\\' && machine != "" {
		return `\\` + machine + `\` + string(path[0]) + `$` + path[2:]
	}
	return path
}

// ---------------------------------------------------------------------
// Distributed availability groups.

func (r *sqlRunner) dagLinkFor(p *sqlPlan, ag string) *sqlDAGLink {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := p.dag(ag); l != nil {
		return l
	}
	src := p.SourceInfo.instance()
	dst := p.TargetInfo.instance()
	srcURL := sqlEndpointURL(src.FQDN(), src.Endpoint.Port)
	l := &sqlDAGLink{Name: sqlDAGName(ag), SourceAG: ag, TargetAG: sqlTargetAGName(ag), SourceURL: srcURL, TargetURL: sqlEndpointURL(dst.FQDN(), dst.Endpoint.Port)}
	p.DAGs = append(p.DAGs, l)
	return l
}

func sqlDAGName(ag string) string      { return sqlTrimName(ag+"-DAG", 128) }
func sqlTargetAGName(ag string) string { return sqlTrimName(ag+"-NEW", 128) }
func sqlTrimName(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// sqlSourceListenerURL is the source AG's listener when it has one, else
// its primary replica's endpoint.
func sqlSourceListenerURL(src *sqlInstance, ag *sqlAG) string {
	if ag != nil && ag.Listener != "" {
		host := ag.Listener
		if !strings.Contains(host, ".") && src.DNSDomain != "" {
			host += "." + src.DNSDomain
		}
		return sqlEndpointURL(strings.ToLower(host), src.Endpoint.Port)
	}
	return sqlEndpointURL(src.FQDN(), src.Endpoint.Port)
}

func (r *sqlRunner) doSeedDAG(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	ag := d.Facts.AG
	link := r.dagLinkFor(p, ag)
	if link.Created.IsZero() {
		r.setStep(d, "Setting up the distributed availability group "+link.Name+".")
		batches := []sqlBatch{}
		if acct := sqlSPNAccount(p.TargetInfo.instance()); acct != "" && p.SourceInfo.EndpointName != "" {
			batches = append(batches, sqlEndpointAccessBatch(sqlOn, p.SourceInfo.EndpointName, acct))
		}
		if acct := sqlSPNAccount(p.SourceInfo.instance()); acct != "" && p.TargetInfo.EndpointName != "" {
			batches = append(batches, sqlEndpointAccessBatch(sqlDst, p.TargetInfo.EndpointName, acct))
		}
		batches = append(batches, sqlCreateTargetAGBatch(link.TargetAG, p.TargetInfo.instance()),
			sqlCreateDAGBatch(link.Name, link.SourceAG, link.SourceURL, link.TargetAG, link.TargetURL),
			sqlJoinDAGBatch(link.Name, link.SourceAG, link.SourceURL, link.TargetAG, link.TargetURL))
		if _, err := r.run(ctx, p, batches...); err != nil {
			return err
		}
		r.mu.Lock()
		link.Created = r.now().UTC()
		for _, x := range p.Databases {
			if x.Method == sqlMethodDAG && strings.EqualFold(x.Facts.AG, ag) && x.State == sqlWaiting {
				x.State = sqlSeeding
			}
		}
		r.saveLocked()
		r.mu.Unlock()
		r.log(p, "Distributed availability group "+link.Name+" created: "+link.SourceAG+" seeds "+link.TargetAG+" on the new server.")
	}
	r.mu.Lock()
	d.State, d.LastRound = sqlSeeding, time.Time{}
	r.mu.Unlock()
	return r.doDAGHealth(ctx, p, d)
}

// doDAGHealth reads the new server's copy of each database of the DAG.
func (r *sqlRunner) doDAGHealth(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	link := p.dag(d.Facts.AG)
	if link == nil {
		return errors.New("the distributed availability group is not set up yet")
	}
	res, err := r.run(ctx, p, sqlDAGStateBatch(sqlDst, link.Name))
	if err != nil {
		return err
	}
	rows := sqlResult(res, "state "+link.Name).rows()
	r.mu.Lock()
	defer r.mu.Unlock()
	d.LastRound = r.now().UTC()
	for _, row := range rows {
		if !row.flag("isLocal") || !strings.EqualFold(row.str("db"), d.target()) {
			continue
		}
		d.Sync = row.str("sync")
		d.QueueKB = row.num("sendQueueKB") + row.num("redoQueueKB")
		if strings.EqualFold(d.Sync, "SYNCHRONIZING") || strings.EqualFold(d.Sync, "SYNCHRONIZED") {
			if d.Seeded.IsZero() {
				d.Seeded = r.now().UTC()
				r.log(p, d.Name+": seeded on the new server through "+link.Name+".")
			}
			if d.State == sqlSeeding || d.State == sqlSyncing {
				d.State = sqlReady
			}
		}
		return nil
	}
	if d.State == sqlSyncing {
		d.State = sqlSeeding
	}
	d.Step = "Waiting for automatic seeding on the new server."
	return nil
}

// dagSynced waits until every database of the link is SYNCHRONIZED on
// both sides with the same hardened LSN.
func (r *sqlRunner) dagSynced(ctx context.Context, p *sqlPlan, link *sqlDAGLink, dbs []*sqlDB, sourceSide bool) error {
	deadline := r.now().Add(10 * time.Minute)
	for {
		res, err := r.run(ctx, p, sqlDAGStateBatch(sqlOn, link.Name), sqlBatch{On: sqlDst, Name: "state target", Kind: "dagstate", Timeout: 120, Args: map[string]string{"dag": link.Name}, SQL: sqlDAGStateBatch(sqlDst, link.Name).SQL})
		if err != nil {
			return err
		}
		srcRows, dstRows := sqlResult(res, "state "+link.Name).rows(), sqlResult(res, "state target").rows()
		ok := len(dbs) > 0
		for _, d := range dbs {
			var a, b sqlRow
			for _, row := range srcRows {
				if row.flag("isLocal") && strings.EqualFold(row.str("db"), d.Name) {
					a = row
				}
			}
			for _, row := range dstRows {
				if row.flag("isLocal") && strings.EqualFold(row.str("db"), d.target()) {
					b = row
				}
			}
			if a == nil || b == nil || a.str("hardened") != b.str("hardened") || (!sourceSide && (!strings.EqualFold(a.str("sync"), "SYNCHRONIZED") || !strings.EqualFold(b.str("sync"), "SYNCHRONIZED"))) {
				ok = false
			}
		}
		if ok {
			return nil
		}
		if !r.now().Before(deadline) {
			return errors.New("the databases did not reach the same point on both servers within 10 minutes")
		}
		if err := vmSleep(ctx, r.poll); err != nil {
			return err
		}
	}
}

// ---------------------------------------------------------------------
// Tests.

func sqlTestName(d *sqlDB) string { return sqlTrimName(d.target()+"_TEST", 128) }

// holdStep marks a manual step critical; it returns a no-op when there is
// no hook.
func (r *sqlRunner) holdStep() (func(), error) {
	if r.hold == nil {
		return func() {}, nil
	}
	done, ok := r.hold()
	if !ok {
		return nil, errors.New("an update is being installed; try again after the application restarts")
	}
	return done, nil
}

func (r *sqlRunner) StartTest(planID, name string) error {
	r.mu.Lock()
	p, err := r.planLocked(planID)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	d := p.db(name)
	key := sqlWorkKey(planID, name)
	switch {
	case d == nil:
		err = errors.New("that database is not in the plan")
	case r.running[key] != "":
		err = errors.New(d.Name + " is busy; try again when its step finishes")
	case d.Test != nil:
		err = errors.New("a test copy of " + d.Name + " already exists; end that test first")
	case d.State == sqlDone || d.State == sqlCutting:
		err = errors.New(d.Name + " is already cut over")
	case d.Method != sqlMethodBackup && d.Seeded.IsZero():
		err = errors.New(d.Name + " has no copy on the new server yet")
	case r.ready(p) != "":
		err = errors.New(r.ready(p))
	}
	if err != nil {
		r.mu.Unlock()
		return err
	}
	done, herr := r.holdStep()
	if herr != nil {
		r.mu.Unlock()
		return herr
	}
	r.running[key] = "test"
	d.Step = "Making the test copy " + sqlTestName(d) + "."
	prev := d.State
	r.mu.Unlock()
	go func() {
		defer done()
		test, err := r.makeTest(context.Background(), p, d)
		r.mu.Lock()
		delete(r.running, key)
		d.Step = ""
		if err != nil {
			d.Note = "The test copy could not be made: " + err.Error()
		} else {
			d.Test, d.State, d.Note = test, sqlTesting, ""
			if prev != sqlReady && prev != sqlTesting && d.Method == sqlMethodBackup {
				d.State = sqlTesting
			}
		}
		r.saveLocked()
		r.mu.Unlock()
		if err == nil {
			r.log(p, d.Name+": test copy "+test.Name+" is online on the new server.")
		} else {
			r.log(p, d.Name+": test copy failed: "+err.Error())
		}
	}()
	return nil
}

func (r *sqlRunner) makeTest(ctx context.Context, p *sqlPlan, d *sqlDB) (*sqlTest, error) {
	name := sqlTestName(d)
	dst := p.TargetInfo.instance()
	var res []sqlBatchResult
	var err error
	switch d.Method {
	case sqlMethodDAG:
		res, err = r.run(ctx, p, sqlSnapshotBatch(name, d.target(), d.Facts.Files, p.dataPath(dst)))
	case sqlMethodLogShip:
		full, logs := "", []string{}
		for _, c := range d.Chain {
			if c.Kind == "full" {
				full, logs = c.Path, nil
			} else if full != "" {
				logs = append(logs, c.Path)
			}
		}
		if full == "" {
			return nil, errors.New("the first copy's backup is not recorded")
		}
		res, err = r.run(ctx, p, sqlTestRestoreBatches(name, full, sqlApplicable(d.Chain), sqlMoves(name, d.Facts.Files, p.dataPath(dst), p.logPath(dst), ""), d.Facts.CDC)...)
		_ = logs
	default:
		if d.Facts.Encrypted {
			if err := r.ensureCert(ctx, p, d); err != nil {
				return nil, err
			}
		}
		file := sqlBackupFile(p, d.Name, "test", r.now())
		if _, err = r.run(ctx, p, sqlCreateFolderBatch(sqlOn, filepath.Dir(strings.ReplaceAll(file, `\`, "/"))), sqlBackupFullBatch(p, d.Name, file, true, p.SourceInfo.Compression)); err != nil {
			return nil, err
		}
		if err := r.checkReachable(ctx, p, file); err != nil {
			return nil, err
		}
		res, err = r.run(ctx, p, sqlTestRestoreBatches(name, file, nil, sqlMoves(name, d.Facts.Files, p.dataPath(dst), p.logPath(dst), ""), d.Facts.CDC)...)
	}
	if err != nil {
		return nil, err
	}
	var row sqlRow
	for i := len(res) - 1; i >= 0 && row == nil; i-- {
		if rows := res[i].rows(); len(rows) > 0 && rows[0]["id"] != nil {
			row = rows[0]
		}
	}
	if row == nil {
		return nil, errors.New("the test copy was made but could not be found afterwards")
	}
	return &sqlTest{Name: name, ID: row.int("id"), Created: row.str("created"), At: r.now().UTC()}, nil
}

// sqlApplicable lists the log backups after the last full one in order
// (the files the restored copy has had applied).
func sqlApplicable(chain []sqlBackupRef) []string {
	out := []string{}
	last := ""
	for _, c := range chain {
		if c.Kind == "full" {
			out, last = nil, c.Last
			continue
		}
		if sqlLSNLess(last, c.Last) {
			out = append(out, c.Path)
			last = c.Last
		}
	}
	return out
}

func (r *sqlRunner) EndTest(planID, name string) error {
	r.mu.Lock()
	p, err := r.planLocked(planID)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	d := p.db(name)
	key := sqlWorkKey(planID, name) + "\x00test"
	if d == nil || d.Test == nil {
		r.mu.Unlock()
		return errors.New("there is no test copy to remove")
	}
	if r.running[key] != "" {
		r.mu.Unlock()
		return errors.New("the test copy is being removed")
	}
	done, herr := r.holdStep()
	if herr != nil {
		r.mu.Unlock()
		return herr
	}
	defer done()
	r.running[key] = "endtest"
	test := *d.Test
	r.mu.Unlock()
	_, err = r.run(context.Background(), p, sqlDropTestBatch(test.Name, test.ID, test.Created))
	r.mu.Lock()
	delete(r.running, key)
	if err == nil {
		d.Test, d.TestedAt = nil, r.now().UTC()
		if d.State == sqlTesting {
			d.State = sqlReady
			if d.Method != sqlMethodBackup && d.Seeded.IsZero() {
				d.State = sqlWaiting
			}
		}
	}
	r.saveLocked()
	r.mu.Unlock()
	if err == nil {
		r.log(p, d.Name+": test copy "+test.Name+" removed.")
	}
	return err
}

// ---------------------------------------------------------------------
// Cutover.

// Cutover cuts the named databases over now, or schedules them at at.
func (r *sqlRunner) Cutover(planID string, names []string, at time.Time) (int, error) {
	kick := false
	defer func() {
		if kick {
			r.Tick()
		}
	}()
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(planID)
	if err != nil {
		return 0, err
	}
	if why := r.ready(p); why != "" && at.IsZero() {
		return 0, errors.New(why)
	}
	picked := []*sqlDB{}
	for _, n := range names {
		d := p.db(n)
		if d == nil {
			return 0, fmt.Errorf("%s is not in the plan", n)
		}
		switch {
		case d.State == sqlDone:
			continue
		case d.Test != nil:
			return 0, fmt.Errorf("end the test of %s before its cutover", d.Name)
		case d.Method != sqlMethodBackup && d.Seeded.IsZero() && at.IsZero():
			return 0, fmt.Errorf("%s has no copy on the new server yet", d.Name)
		case r.running[sqlWorkKey(p.ID, d.Name)] == "cutover":
			continue
		}
		picked = append(picked, d)
	}
	// A distributed AG moves all its databases together.
	for _, d := range picked {
		if d.Method != sqlMethodDAG {
			continue
		}
		for _, x := range r.agDBsLocked(p, d.Facts.AG, nil) {
			found := false
			for _, y := range picked {
				if y == x {
					found = true
				}
			}
			if !found && x.State != sqlDone {
				return 0, fmt.Errorf("the databases of %s cut over together; choose %s too", d.Facts.AG, x.Name)
			}
		}
	}
	when := at.UTC()
	if when.IsZero() {
		when = r.now().UTC()
	}
	for _, d := range picked {
		d.CutoverAt, d.Note = when, ""
	}
	r.saveLocked()
	kick = at.IsZero()
	return len(picked), nil
}

func (r *sqlRunner) Unschedule(planID string, names []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(planID)
	if err != nil {
		return err
	}
	for _, n := range names {
		if d := p.db(n); d != nil && d.State != sqlCutting {
			d.CutoverAt = time.Time{}
		}
	}
	return r.saveLocked()
}

func (r *sqlRunner) doCutover(ctx context.Context, p *sqlPlan, dbs []*sqlDB) error {
	if p.Objects.Logins || p.Objects.Jobs || p.Objects.LinkedServers || p.Objects.Operators || len(p.Objects.Config) > 0 {
		for _, d := range dbs {
			r.setStep(d, "Copying new logins, jobs and other instance objects.")
		}
		if _, err := r.copyObjects(ctx, p); err != nil {
			return fmt.Errorf("instance objects could not be copied, so nothing was cut over: %v", err)
		}
	}
	if len(dbs) > 0 && dbs[0].Method == sqlMethodDAG {
		if err := r.cutoverDAG(ctx, p, dbs); err != nil {
			return err
		}
	} else {
		for _, d := range dbs {
			var err error
			if d.Method == sqlMethodBackup {
				err = r.cutoverBackup(ctx, p, d)
			} else {
				err = r.cutoverLogShip(ctx, p, d)
			}
			if err != nil {
				return err
			}
		}
	}
	r.afterCutover(ctx, p, dbs)
	return nil
}

// bringBack restores the old database after a cutover that failed before
// the new one was online.
func (r *sqlRunner) bringBack(ctx context.Context, p *sqlPlan, d *sqlDB, cause error) error {
	r.setStep(d, "The cutover failed; bringing the old database back.")
	if _, err := r.run(ctx, p, sqlRollbackSourceBatch(d.Name, &d.Facts, d.Method)); err != nil {
		return fmt.Errorf("%v. The old database could not be brought back either (%v): run RESTORE DATABASE %s WITH RECOVERY on the old server", cause, err, sqlName(d.Name))
	}
	r.mu.Lock()
	d.SourceHeld = ""
	r.mu.Unlock()
	return fmt.Errorf("%v. The old database was brought back online and nothing else changed", cause)
}

func (r *sqlRunner) cutoverLogShip(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	// One more round first keeps the tail backup small.
	if _, err := r.shipLog(ctx, p, d, false); err != nil {
		return err
	}
	if _, err := r.shipLog(ctx, p, d, true); err != nil {
		if strings.Contains(err.Error(), "restore log") || strings.Contains(err.Error(), "Restoring") {
			r.mu.Lock()
			d.SourceHeld = "restoring"
			r.mu.Unlock()
			return r.bringBack(ctx, p, d, err)
		}
		return r.bringBackIfHeld(ctx, p, d, err)
	}
	r.mu.Lock()
	d.SourceHeld = "restoring"
	r.saveLocked()
	r.mu.Unlock()
	r.setStep(d, "Bringing the new database online.")
	if _, err := r.run(ctx, p, sqlRecoverBatch(sqlDst, d.target(), d.Facts.CDC)); err != nil {
		return r.bringBack(ctx, p, d, err)
	}
	return r.finishDB(ctx, p, d)
}

// bringBackIfHeld checks whether the tail backup stopped the old database
// before failing, and brings it back if so.
func (r *sqlRunner) bringBackIfHeld(ctx context.Context, p *sqlPlan, d *sqlDB, cause error) error {
	res, err := r.run(ctx, p, sqlBatch{On: sqlOn, Name: "status", Kind: "status", Timeout: 60, Args: map[string]string{"db": d.Name}, SQL: "SET NOCOUNT ON; SELECT CONVERT(nvarchar(60), DATABASEPROPERTYEX(" + sqlStr(d.Name) + ", 'Status')) AS status, CONVERT(nvarchar(60), DATABASEPROPERTYEX(" + sqlStr(d.Name) + ", 'UserAccess')) AS access;"})
	if err == nil {
		if rows := sqlResult(res, "status").rows(); len(rows) > 0 && (rows[0].str("status") != "ONLINE" || rows[0].str("access") != strings.ToUpper(sqlOr(d.Facts.UserAccess, "MULTI_USER"))) {
			return r.bringBack(ctx, p, d, cause)
		}
	}
	return cause
}

func (r *sqlRunner) cutoverBackup(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	if d.Facts.Encrypted {
		if err := r.ensureCert(ctx, p, d); err != nil {
			return err
		}
	}
	r.setStep(d, "Setting the old database read only.")
	if !d.Facts.ReadOnly {
		if _, err := r.run(ctx, p, sqlReadOnlyBatch(d.Name)); err != nil {
			return r.bringBackIfHeld(ctx, p, d, err)
		}
		r.mu.Lock()
		d.SourceHeld = "readonly"
		r.saveLocked()
		r.mu.Unlock()
	}
	now := r.now()
	file := sqlBackupFile(p, d.Name, "copy", now)
	r.setStep(d, "Backing up "+sqlSizeText(d.Facts.DataMB)+" on the old server.")
	if _, err := r.run(ctx, p, sqlCreateFolderBatch(sqlOn, filepath.Dir(strings.ReplaceAll(file, `\`, "/"))), sqlBackupFullBatch(p, d.Name, file, true, p.SourceInfo.Compression)); err != nil {
		return r.bringBack(ctx, p, d, err)
	}
	if err := r.checkReachable(ctx, p, file); err != nil {
		return r.bringBack(ctx, p, d, err)
	}
	r.setStep(d, "Restoring on the new server.")
	if _, err := r.run(ctx, p, sqlRestoreFullBatch(d.target(), file, r.moves(p, d, ""), false, d.Facts.CDC)); err != nil {
		return r.bringBack(ctx, p, d, err)
	}
	r.mu.Lock()
	d.Seeded = r.now().UTC()
	d.Chain = append(d.Chain, sqlBackupRef{Path: file, Kind: "full", At: now.UTC()})
	r.mu.Unlock()
	return r.finishDB(ctx, p, d)
}

// finishDB runs what follows a database coming online on the new server.
func (r *sqlRunner) finishDB(ctx context.Context, p *sqlPlan, d *sqlDB) error {
	r.setStep(d, "Putting back user access, TRUSTWORTHY, the owner and user mappings.")
	note := ""
	if _, err := r.run(ctx, p, sqlAfterRestoreBatch(d.target(), &d.Facts, p.TargetInfo.Major)); err != nil {
		note = "The database is online on the new server, but finishing touches failed: " + err.Error()
	}
	r.mu.Lock()
	d.State, d.CutAt, d.CutoverAt, d.Note, d.Error = sqlDone, r.now().UTC(), time.Time{}, note, ""
	r.saveLocked()
	r.mu.Unlock()
	r.log(p, d.Name+": cut over; "+d.target()+" is online on "+p.Target.Server+".")
	return nil
}

func (r *sqlRunner) cutoverDAG(ctx context.Context, p *sqlPlan, dbs []*sqlDB) error {
	link := p.dag(dbs[0].Facts.AG)
	if link == nil || link.Created.IsZero() {
		return errors.New("the distributed availability group is not set up")
	}
	step := func(text string) {
		for _, d := range dbs {
			r.setStep(d, text)
		}
	}
	step("Switching " + link.Name + " to synchronous commit.")
	if _, err := r.run(ctx, p, sqlDAGModeBatch(sqlOn, link.Name, link.SourceAG, link.TargetAG, "SYNCHRONOUS_COMMIT"), sqlDAGModeBatch(sqlDst, link.Name, link.SourceAG, link.TargetAG, "SYNCHRONOUS_COMMIT")); err != nil {
		return err
	}
	async := func() {
		r.run(ctx, p, sqlDAGModeBatch(sqlOn, link.Name, link.SourceAG, link.TargetAG, "ASYNCHRONOUS_COMMIT"), sqlDAGModeBatch(sqlDst, link.Name, link.SourceAG, link.TargetAG, "ASYNCHRONOUS_COMMIT"))
	}
	step("Waiting until both servers hold the same data.")
	if err := r.dagSynced(ctx, p, link, dbs, false); err != nil {
		async()
		return fmt.Errorf("%v; nothing was cut over and the old databases keep running", err)
	}
	step("Making the old availability group secondary.")
	if _, err := r.run(ctx, p, sqlDAGSecondaryBatch(sqlOn, link.Name)); err != nil {
		async()
		return err
	}
	for _, d := range dbs {
		r.mu.Lock()
		d.SourceHeld = "secondary"
		r.mu.Unlock()
	}
	failBack := func(cause error) error {
		if _, err := r.run(ctx, p, sqlDAGFailoverBatch(sqlOn, link.Name)); err != nil {
			return fmt.Errorf("%v. The old availability group could not be made primary again (%v): run ALTER AVAILABILITY GROUP %s FORCE_FAILOVER_ALLOW_DATA_LOSS on the old server", cause, err, sqlName(link.Name))
		}
		async()
		for _, d := range dbs {
			r.mu.Lock()
			d.SourceHeld = ""
			r.mu.Unlock()
		}
		return fmt.Errorf("%v. The old availability group was made primary again; nothing was lost", cause)
	}
	if err := r.dagSynced(ctx, p, link, dbs, true); err != nil {
		return failBack(err)
	}
	step("Failing over to the new server.")
	if _, err := r.run(ctx, p, sqlDAGFailoverBatch(sqlDst, link.Name)); err != nil {
		return failBack(err)
	}
	r.mu.Lock()
	link.CutAt = r.now().UTC()
	r.mu.Unlock()
	for _, d := range dbs {
		if err := r.finishDB(ctx, p, d); err != nil {
			return err
		}
	}
	return nil
}

// afterCutover sets default databases, turns jobs on (and the old ones off
// when asked) and moves the alias once every database is cut over.
func (r *sqlRunner) afterCutover(ctx context.Context, p *sqlPlan, dbs []*sqlDB) {
	src, _, err := r.readObjects(ctx, p)
	if err == nil {
		batches := []sqlBatch{}
		for _, d := range dbs {
			if b := sqlDefaultDBBatch(src.Logins, d.Name, d.target()); b != nil {
				batches = append(batches, *b)
			}
		}
		moved, planDBs := map[string]bool{}, map[string]bool{}
		all := true
		r.mu.Lock()
		for _, d := range p.Databases {
			planDBs[strings.ToLower(d.Name)] = true
			if d.State == sqlDone {
				moved[strings.ToLower(d.Name)] = true
			} else {
				all = false
			}
		}
		r.mu.Unlock()
		if p.Objects.Jobs {
			names := []string{}
			for _, j := range sqlJobsFor(src.Jobs, moved, planDBs, all) {
				if !sqlHasFold(p.JobsOn, j.Name) {
					names = append(names, j.Name)
				}
			}
			if len(names) > 0 {
				batches = append(batches, sqlJobEnableBatch(sqlDst, names, true))
				if p.Objects.DisableSourceJobs {
					batches = append(batches, sqlJobEnableBatch(sqlOn, names, false))
				}
				r.mu.Lock()
				p.JobsOn = append(p.JobsOn, names...)
				if p.Objects.DisableSourceJobs {
					p.JobsOff = append(p.JobsOff, names...)
				}
				r.mu.Unlock()
				r.log(p, "Turned on "+fmt.Sprint(len(names))+" jobs on the new server: "+strings.Join(names, ", ")+".")
			}
		}
		if len(batches) > 0 {
			r.run(ctx, p, batches...)
		}
		if all && p.Alias.Name != "" && p.Alias.AtCutover && (p.AliasState == nil || !p.AliasState.Restored.IsZero()) {
			if err := r.MoveAlias(ctx, p.ID); err != nil {
				r.mu.Lock()
				p.Note = "The alias was not moved: " + err.Error()
				r.mu.Unlock()
			}
		}
	} else {
		r.mu.Lock()
		p.Note = "Jobs and default databases were not updated after the cutover: " + err.Error()
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.saveLocked()
	r.mu.Unlock()
}

// ---------------------------------------------------------------------
// Rollback.

// Rollback brings the old databases back after a cutover: the new copy is
// taken offline (never dropped), the old database is recovered or made
// writable again, jobs are switched back, and the alias is put back when
// nothing remains cut over. For a distributed AG it fails back, and the
// new server stays in sync for a later cutover.
func (r *sqlRunner) Rollback(ctx context.Context, planID string, names []string) error {
	r.mu.Lock()
	p, err := r.planLocked(planID)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if why := r.ready(p); why != "" {
		r.mu.Unlock()
		return errors.New(why)
	}
	dbs := []*sqlDB{}
	for _, n := range names {
		d := p.db(n)
		if d == nil {
			r.mu.Unlock()
			return fmt.Errorf("%s is not in the plan", n)
		}
		if d.State != sqlDone && d.SourceHeld == "" {
			continue
		}
		if r.running[sqlWorkKey(p.ID, d.Name)] != "" {
			r.mu.Unlock()
			return fmt.Errorf("%s is busy", d.Name)
		}
		dbs = append(dbs, d)
	}
	if len(dbs) == 0 {
		r.mu.Unlock()
		return errors.New("none of those databases is cut over")
	}
	done, herr := r.holdStep()
	if herr != nil {
		r.mu.Unlock()
		return herr
	}
	defer done()
	for _, d := range dbs {
		r.running[sqlWorkKey(p.ID, d.Name)] = "rollback"
		d.Step = "Rolling back."
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		for _, d := range dbs {
			delete(r.running, sqlWorkKey(p.ID, d.Name))
			d.Step = ""
		}
		r.saveLocked()
		r.mu.Unlock()
	}()
	var firstErr error
	dagDone := map[string]bool{}
	for _, d := range dbs {
		if d.Method == sqlMethodDAG {
			if dagDone[d.Facts.AG] {
				continue
			}
			dagDone[d.Facts.AG] = true
			if err := r.failBackDAG(ctx, p, r.agDBsLocked(p, d.Facts.AG, []*sqlDB{d})); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		batches := []sqlBatch{sqlOfflineBatch(d.target()), sqlRollbackSourceBatch(d.Name, &d.Facts, d.Method)}
		if d.SwitchedFull {
			batches = append(batches, sqlBatch{On: sqlOn, Name: "simple recovery " + d.Name, Kind: "tosimple", Timeout: 600, Continue: true, SQL: "SET NOCOUNT ON; ALTER DATABASE " + sqlName(d.Name) + " SET RECOVERY SIMPLE;"})
		}
		if _, err := r.run(ctx, p, batches...); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %v", d.Name, err)
			}
			continue
		}
		r.mu.Lock()
		d.State, d.SourceHeld, d.RolledBackAt, d.Note = sqlRolledBack, "", r.now().UTC(), "Rolled back: the old database is online again and the new copy is offline on the new server (not deleted). Changes made on the new server after the cutover are not on the old one."
		r.mu.Unlock()
		r.log(p, d.Name+": rolled back to "+p.Source.Server+".")
	}
	r.rollbackJobs(ctx, p)
	r.mu.Lock()
	anyDone := false
	for _, d := range p.Databases {
		if d.State == sqlDone {
			anyDone = true
		}
	}
	moveBack := !anyDone && p.AliasState != nil && p.AliasState.Restored.IsZero()
	r.mu.Unlock()
	if moveBack {
		if err := r.RestoreAlias(ctx, p.ID); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("the databases are back, but the alias was not put back: %v", err)
		}
	}
	return firstErr
}

// rollbackJobs turns the jobs whose databases are no longer cut over off
// on the new server and back on on the old one.
func (r *sqlRunner) rollbackJobs(ctx context.Context, p *sqlPlan) {
	if len(p.JobsOn) == 0 && len(p.JobsOff) == 0 {
		return
	}
	src, _, err := r.readObjects(ctx, p)
	if err != nil {
		return
	}
	moved, planDBs := map[string]bool{}, map[string]bool{}
	all := true
	r.mu.Lock()
	for _, d := range p.Databases {
		planDBs[strings.ToLower(d.Name)] = true
		if d.State == sqlDone {
			moved[strings.ToLower(d.Name)] = true
		} else {
			all = false
		}
	}
	keep := map[string]bool{}
	for _, j := range sqlJobsFor(src.Jobs, moved, planDBs, all) {
		keep[strings.ToLower(j.Name)] = true
	}
	off, on := []string{}, []string{}
	restOn, restOff := []string{}, []string{}
	for _, n := range p.JobsOn {
		if keep[strings.ToLower(n)] {
			restOn = append(restOn, n)
		} else {
			off = append(off, n)
		}
	}
	for _, n := range p.JobsOff {
		if keep[strings.ToLower(n)] {
			restOff = append(restOff, n)
		} else {
			on = append(on, n)
		}
	}
	r.mu.Unlock()
	batches := []sqlBatch{}
	if len(off) > 0 {
		batches = append(batches, sqlJobEnableBatch(sqlDst, off, false))
	}
	if len(on) > 0 {
		batches = append(batches, sqlJobEnableBatch(sqlOn, on, true))
	}
	if len(batches) == 0 {
		return
	}
	if _, err := r.run(ctx, p, batches...); err == nil {
		r.mu.Lock()
		p.JobsOn, p.JobsOff = restOn, restOff
		r.mu.Unlock()
	}
}

func (r *sqlRunner) failBackDAG(ctx context.Context, p *sqlPlan, dbs []*sqlDB) error {
	link := p.dag(dbs[0].Facts.AG)
	if link == nil {
		return errors.New("the distributed availability group is not recorded")
	}
	if _, err := r.run(ctx, p, sqlDAGModeBatch(sqlDst, link.Name, link.SourceAG, link.TargetAG, "SYNCHRONOUS_COMMIT"), sqlDAGModeBatch(sqlOn, link.Name, link.SourceAG, link.TargetAG, "SYNCHRONOUS_COMMIT")); err != nil {
		return err
	}
	if err := r.dagSyncedReverse(ctx, p, link, dbs); err != nil {
		return err
	}
	if _, err := r.run(ctx, p, sqlDAGSecondaryBatch(sqlDst, link.Name)); err != nil {
		return err
	}
	if err := r.dagSynced(ctx, p, link, dbs, true); err != nil {
		return err
	}
	if _, err := r.run(ctx, p, sqlDAGFailoverBatch(sqlOn, link.Name)); err != nil {
		return err
	}
	r.run(ctx, p, sqlDAGModeBatch(sqlOn, link.Name, link.SourceAG, link.TargetAG, "ASYNCHRONOUS_COMMIT"), sqlDAGModeBatch(sqlDst, link.Name, link.SourceAG, link.TargetAG, "ASYNCHRONOUS_COMMIT"))
	r.mu.Lock()
	link.CutAt = time.Time{}
	for _, d := range dbs {
		d.State, d.SourceHeld, d.RolledBackAt, d.CutAt = sqlReady, "", r.now().UTC(), time.Time{}
		d.Note = "Failed back: the old availability group is primary again, and the new server keeps a copy in sync for a later cutover."
	}
	r.mu.Unlock()
	r.log(p, link.Name+": failed back to "+link.SourceAG+".")
	return nil
}

func (r *sqlRunner) dagSyncedReverse(ctx context.Context, p *sqlPlan, link *sqlDAGLink, dbs []*sqlDB) error {
	return r.dagSynced(ctx, p, link, dbs, false)
}

// FinishDAG removes this plan's distributed AG once its databases are cut
// over and kept (the new availability group stays).
func (r *sqlRunner) FinishDAG(ctx context.Context, planID, ag string) error {
	r.mu.Lock()
	p, err := r.planLocked(planID)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	link := p.dag(ag)
	if link == nil || link.CutAt.IsZero() || !link.Removed.IsZero() {
		r.mu.Unlock()
		return errors.New("that distributed availability group is not cut over, or was already removed")
	}
	r.mu.Unlock()
	if _, err := r.run(ctx, p, sqlDropDAGBatch(sqlDst, link.Name), sqlDropDAGBatch(sqlOn, link.Name)); err != nil {
		return err
	}
	r.mu.Lock()
	link.Removed = r.now().UTC()
	r.saveLocked()
	r.mu.Unlock()
	r.log(p, link.Name+" removed; the new availability group "+link.TargetAG+" runs on its own.")
	return nil
}

// ---------------------------------------------------------------------
// The alias.

func (r *sqlRunner) aliasSPNs(p *sqlPlan) []string {
	fqdn := sqlAliasFQDN(p.Alias)
	out := []string{"MSSQLSvc/" + fqdn}
	port := p.TargetInfo.Port
	if port == 0 {
		port = 1433
	}
	out = append(out, fmt.Sprintf("MSSQLSvc/%s:%d", fqdn, port))
	return out
}

// MoveAlias points the alias at the new server: an existing CNAME is
// changed (its old target saved first), a missing one is created; an
// address record is refused. With SPN on, the Kerberos names follow.
func (r *sqlRunner) MoveAlias(ctx context.Context, planID string) error {
	r.mu.Lock()
	p, err := r.planLocked(planID)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if p.Alias.Name == "" {
		return errors.New("the plan has no alias")
	}
	if err := sqlCheckAlias(p.Alias, p.SourceInfo.instance()); err != nil {
		return err
	}
	target := p.TargetInfo.FQDN
	if target == "" {
		return errors.New("the new server's DNS name is not known")
	}
	base := sqlDNSRequest{Server: p.Alias.DNSServer, Zone: p.Alias.Zone, Name: sqlAliasRecordName(p.Alias)}
	read := base
	read.Mode = "read"
	cur, err := r.dns(ctx, p, read)
	if err != nil {
		return err
	}
	state := &sqlAliasState{Target: target}
	switch strings.ToUpper(cur.Type) {
	case "":
		state.Created = true
	case "CNAME":
		state.Previous = strings.TrimSuffix(strings.ToLower(cur.Value), ".")
		if state.Previous == strings.ToLower(target) {
			r.log(p, "The alias already points at the new server.")
		}
	default:
		return fmt.Errorf("%s is a %s record, not a CNAME. Only an alias (CNAME) is moved; an address record belongs to a computer", sqlAliasFQDN(p.Alias), cur.Type)
	}
	r.mu.Lock()
	if p.AliasState != nil && p.AliasState.Restored.IsZero() && p.AliasState.Previous != "" {
		state.Previous, state.Created = p.AliasState.Previous, p.AliasState.Created
	}
	p.AliasState = state
	r.saveLocked()
	r.mu.Unlock()
	set := base
	set.Mode, set.Target = "set", target
	if _, err := r.dns(ctx, p, set); err != nil {
		return err
	}
	r.mu.Lock()
	state.ChangedAt = r.now().UTC()
	r.saveLocked()
	r.mu.Unlock()
	r.log(p, fmt.Sprintf("The alias %s now points at %s (it pointed at %s).", sqlAliasFQDN(p.Alias), target, sqlOr(state.Previous, "nothing")))
	if p.Alias.SPN {
		acct := sqlSPNAccount(p.TargetInfo.instance())
		if acct == "" {
			return errors.New("the alias was moved, but the new server's service account is not a domain account, so its Kerberos names were not registered")
		}
		spn := base
		spn.Mode, spn.SPNAccount, spn.SPNs = "spn", acct, r.aliasSPNs(p)
		res, err := r.dns(ctx, p, spn)
		if err != nil {
			return fmt.Errorf("the alias was moved, but its Kerberos names were not registered: %v", err)
		}
		r.mu.Lock()
		state.SPNs, state.SPNFrom = spn.SPNs, res.SPNOwner
		r.saveLocked()
		r.mu.Unlock()
	}
	return nil
}

// RestoreAlias puts the alias back as it was before MoveAlias.
func (r *sqlRunner) RestoreAlias(ctx context.Context, planID string) error {
	r.mu.Lock()
	p, err := r.planLocked(planID)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	st := p.AliasState
	if st == nil || !st.Restored.IsZero() || st.ChangedAt.IsZero() {
		return errors.New("the alias was not moved by this plan")
	}
	base := sqlDNSRequest{Server: p.Alias.DNSServer, Zone: p.Alias.Zone, Name: sqlAliasRecordName(p.Alias)}
	req := base
	if st.Created {
		req.Mode, req.Expect = "remove", st.Target
	} else {
		req.Mode, req.Target = "set", st.Previous
	}
	if _, err := r.dns(ctx, p, req); err != nil {
		return err
	}
	if len(st.SPNs) > 0 {
		un := base
		un.Mode, un.SPNAccount, un.SPNs, un.Target = "unspn", sqlSPNAccount(p.TargetInfo.instance()), st.SPNs, st.SPNFrom
		r.dns(ctx, p, un)
	}
	r.mu.Lock()
	st.Restored = r.now().UTC()
	r.saveLocked()
	r.mu.Unlock()
	r.log(p, "The alias "+sqlAliasFQDN(p.Alias)+" was put back.")
	return nil
}

// ---------------------------------------------------------------------
// Other actions.

func (r *sqlRunner) SetPaused(planID string, paused bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(planID)
	if err != nil {
		return err
	}
	p.Paused = paused
	if paused {
		for k, cancel := range r.cancels {
			if strings.HasPrefix(k, planID+"|") && r.running[k] == "round" {
				cancel()
			}
		}
	}
	return r.saveLocked()
}

// SyncNow ships the log of the named databases at once.
func (r *sqlRunner) SyncNow(planID string, names []string) (int, error) {
	r.mu.Lock()
	p, err := r.planLocked(planID)
	if err != nil {
		r.mu.Unlock()
		return 0, err
	}
	n := 0
	for _, name := range names {
		d := p.db(name)
		if d == nil || d.Method == sqlMethodBackup || d.State == sqlDone || d.State == sqlCutting || r.running[sqlWorkKey(planID, d.Name)] != "" {
			continue
		}
		switch {
		case d.Seeded.IsZero() && d.Method != sqlMethodDAG:
			d.State, d.NextTry = sqlWaiting, time.Time{}
		default:
			d.LastRound, d.NextTry = time.Time{}, r.now()
			if d.State == sqlFailed {
				d.NextTry = r.now()
			}
		}
		n++
	}
	r.saveLocked()
	r.mu.Unlock()
	r.Tick()
	return n, nil
}

// Reseed copies the database again from a new full backup (after a broken
// log chain).
func (r *sqlRunner) Reseed(planID, name string) error {
	kick := false
	defer func() {
		if kick {
			r.Tick()
		}
	}()
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(planID)
	if err != nil {
		return err
	}
	d := p.db(name)
	if d == nil || d.Method != sqlMethodLogShip || d.State == sqlDone || d.State == sqlCutting || d.Test != nil || r.running[sqlWorkKey(planID, d.Name)] != "" {
		return errors.New("only a log shipping database that is not cut over, tested or busy can be copied again")
	}
	d.State, d.NextTry, d.Error = sqlWaiting, time.Time{}, ""
	kick = true
	return r.saveLocked()
}

// SetMethod changes how a database moves while nothing has been copied.
func (r *sqlRunner) SetMethod(planID, name, method string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(planID)
	if err != nil {
		return err
	}
	d := p.db(name)
	if d == nil {
		return errors.New("that database is not in the plan")
	}
	if _, ok := sqlMethodNames[method]; !ok || method == sqlMethodDAG && d.Facts.AG == "" {
		return errors.New("that method is not available for " + d.Name)
	}
	if !d.Seeded.IsZero() || r.running[sqlWorkKey(planID, d.Name)] != "" || d.State == sqlDone {
		return errors.New(d.Name + " already has a copy; its method cannot change now")
	}
	d.Method, d.Why = method, "Chosen by you."
	return r.saveLocked()
}

func (r *sqlRunner) SetWave(planID string, names []string, wave int) error {
	if wave < 0 || wave > 99 {
		return errors.New("waves are numbered 0 to 99")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(planID)
	if err != nil {
		return err
	}
	for _, n := range names {
		if d := p.db(n); d != nil {
			d.Wave = wave
		}
	}
	return r.saveLocked()
}

// Delete removes the plan record only; it never touches either server.
func (r *sqlRunner) Delete(planID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := r.planLocked(planID)
	if err != nil {
		return err
	}
	for k := range r.running {
		if strings.HasPrefix(k, planID+"|") {
			return errors.New("wait for the running step to finish, or pause the plan first")
		}
	}
	for _, d := range p.Databases {
		if d.Test != nil {
			return errors.New("end the test of " + d.Name + " first")
		}
		if d.SourceHeld != "" && d.State != sqlDone {
			return errors.New(d.Name + " is stopped on the old server by a cutover that did not finish; roll it back first")
		}
	}
	out := r.plans[:0]
	for _, x := range r.plans {
		if x.ID != planID {
			out = append(out, x)
		}
	}
	r.plans = out
	return r.saveLocked()
}
