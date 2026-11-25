package main

// Replication jobs. A job keeps a server's disks copied to a receiver while
// the server keeps running: every round takes new snapshots (shadow copies
// on Windows, LVM snapshots on Linux), sends only chunks that changed, and
// records when the snapshot was taken. The recovery point is the snapshot
// time of the last round that finished, so the recovery point objective
// (RPO) a job meets is how long ago that was.
//
// Jobs run inside the replication agent (the same executable running as a
// service on the source server), so they keep going when the console is
// closed. The agent saves each job, its status and its log in its own data
// folder and raises alerts when a job falls behind or keeps failing.
//
// Cutover stops the listed services, runs a last round from snapshots taken
// after they stopped, verifies every disk against its hashes and, when asked,
// shuts the source down. If any step fails, the services are started again
// and the job goes back to replicating (rollback): nothing on the source is
// left changed.

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
	replDataVersion     = 1
	replMaxJobs         = 64
	replMaxLogLines     = 400
	replLogFileLimit    = 4 << 20
	replDefaultRPO      = 15
	replTickInterval    = 5 * time.Second
	replShutdownDelay   = 30 * time.Second
	replFailuresToAlert = 3
)

// Job states.
const (
	replStateIdle     = "idle"    // waiting for the next round
	replStateSyncing  = "syncing" // a round is running
	replStatePaused   = "paused"  // the operator paused it
	replStateError    = "error"   // the last round failed; it retries
	replStateCutover  = "cutting-over"
	replStateCutDone  = "cut-over" // final round done; nothing more runs
	replStateNeverRun = "new"
)

var replJobIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

// replReceiverLink is the receiver a job sends to. The pairing code is a
// secret: it is kept in the agent's protected data folder and never shown
// in a status answer.
type replReceiverLink struct {
	Address    string `json:"address"`
	Thumbprint string `json:"thumbprint"`
	Code       string `json:"code,omitempty"`
}

// replDiskMap sends one source disk ("0" or "/dev/sda") to a receiver
// target (a .vhdx name or disk-N).
type replDiskMap struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type replJobSpec struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Disks    []replDiskMap    `json:"disks"`
	Receiver replReceiverLink `json:"receiver"`
	// IntervalMinutes is the time from the start of one round to the
	// start of the next; 0 starts the next round a minute after one ends
	// (continuous).
	IntervalMinutes int                `json:"intervalMinutes"`
	Throttle        vmThrottleSchedule `json:"throttle"`
	Compress        bool               `json:"compress"`
	// AllowLive copies a volume that cannot be snapshotted as it is.
	AllowLive bool `json:"allowLive"`
	// RPOMinutes raises an alert when the recovery point is older.
	RPOMinutes int `json:"rpoMinutes"`
	// Cutover settings.
	StopServices         []string   `json:"stopServices,omitempty"`
	ShutdownAfterCutover bool       `json:"shutdownAfterCutover"`
	CutoverAt            *time.Time `json:"cutoverAt,omitempty"`
}

type replDiskStatus struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Size     int64  `json:"size"`
	Passes   int    `json:"passes"`
	Verified bool   `json:"verified"`
	Chunks   int    `json:"chunks"`
	Done     int    `json:"done"`
	Read     int64  `json:"read"`
	Sent     int64  `json:"sent"`
	Changed  int    `json:"changed"`
	Error    string `json:"error,omitempty"`
}

type replJobStatus struct {
	State   string `json:"state"`
	Message string `json:"message"`
	Rounds  int    `json:"rounds"`
	// RecoveryPoint is the snapshot time of the last finished round.
	RecoveryPoint time.Time        `json:"recoveryPoint"`
	LastStart     time.Time        `json:"lastStart"`
	LastEnd       time.Time        `json:"lastEnd"`
	NextRun       time.Time        `json:"nextRun"`
	LastDuration  float64          `json:"lastDurationSeconds"`
	LastRead      int64            `json:"lastRead"`
	LastSent      int64            `json:"lastSent"`
	TotalSent     int64            `json:"totalSent"`
	Failures      int              `json:"failures"`
	LastError     string           `json:"lastError,omitempty"`
	Disks         []replDiskStatus `json:"disks"`
	// RescanNext makes the next round rebuild the receiver's records from
	// the targets first (after a test failover booted them).
	RescanNext bool      `json:"rescanNext"`
	CutoverAt  time.Time `json:"cutoverDone,omitempty"`
	Notes      []string  `json:"notes,omitempty"`
}

type replAlert struct {
	ID      int       `json:"id"`
	Time    time.Time `json:"time"`
	Job     string    `json:"job"`
	JobName string    `json:"jobName"`
	Level   string    `json:"level"` // warning or error
	Kind    string    `json:"kind"`  // rpo, failing, cutover
	Text    string    `json:"text"`
	Cleared bool      `json:"cleared"`
}

// replJobView is what the agent shows the console: the spec without the
// pairing code, and the status.
type replJobView struct {
	Spec      replJobSpec   `json:"spec"`
	Status    replJobStatus `json:"status"`
	RPOSecond int64         `json:"rpoSeconds"`
	HasCode   bool          `json:"hasCode"`
}

// replOpenedDisks are the snapshots of one round.
type replOpenedDisks struct {
	Sources  map[string]vmCopySource
	Snapshot time.Time
	Notes    []string
	// Ranges optionally limits a disk's round to changed areas.
	Ranges map[string][]vmCopyRange
	// Commit is called after every disk of the round was sent, so change
	// tracking can move its marker forward. It may be nil.
	Commit func() error
	Close  func()
}

// replPlatform is what the agent needs from the operating system.
type replPlatform interface {
	Name() string
	ListDisks() ([]replDiskInfo, error)
	// OpenDisks snapshots the disks and opens each for reading.
	OpenDisks(ctx context.Context, job string, sources []string, allowLive bool, log func(string)) (*replOpenedDisks, error)
	StopServices(names []string, log func(string)) error
	StartServices(names []string, log func(string)) error
	Shutdown(log func(string)) error
}

type replDiskInfo struct {
	ID      string           `json:"id"`
	Size    int64            `json:"size"`
	Model   string           `json:"model,omitempty"`
	System  bool             `json:"system"`
	Volumes []replVolumeInfo `json:"volumes"`
}

type replVolumeInfo struct {
	Name       string   `json:"name"`
	Mounts     []string `json:"mounts"`
	FileSystem string   `json:"fileSystem"`
	Length     int64    `json:"length"`
	Kind       string   `json:"kind,omitempty"` // simple, spanned, striped, mirrored, lvm
}

type replJob struct {
	spec     replJobSpec
	status   replJobStatus
	running  bool
	cancel   context.CancelFunc
	throttle *vmThrottle
	logs     []string
	cutover  bool // a cutover was asked for
}

// replAgent runs jobs and keeps their records.
type replAgent struct {
	mu       sync.Mutex
	dir      string
	platform replPlatform
	jobs     map[string]*replJob
	alerts   []replAlert
	alertID  int
	now      func() time.Time
	newSink  func(ctx context.Context, link replReceiverLink, target string) (replSink, error)
	notify   func(replAlert)
	wg       sync.WaitGroup
	stopping bool
}

// replSink is a vmCopySink the agent can tune per round.
type replSink interface {
	vmCopySink
	Configure(compress bool, throttle *vmThrottle, rescan bool)
}

func (s *vmRemoteSink) Configure(compress bool, throttle *vmThrottle, rescan bool) {
	s.Compress, s.Throttle, s.Rescan = compress, throttle, rescan
}

type replSaved struct {
	Version int            `json:"version"`
	Jobs    []replSavedJob `json:"jobs"`
	Alerts  []replAlert    `json:"alerts"`
	AlertID int            `json:"alertId"`
}

type replSavedJob struct {
	Spec   replJobSpec   `json:"spec"`
	Status replJobStatus `json:"status"`
}

func newReplAgent(dir string, platform replPlatform) (*replAgent, error) {
	if err := os.MkdirAll(filepath.Join(dir, "Logs"), 0700); err != nil {
		return nil, err
	}
	a := &replAgent{dir: dir, platform: platform, jobs: map[string]*replJob{}, now: time.Now}
	a.newSink = func(ctx context.Context, link replReceiverLink, target string) (replSink, error) {
		return newVMRemoteSink(ctx, link.Address, link.Thumbprint, link.Code, target)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "jobs.json"))
	switch {
	case err == nil:
		var saved replSaved
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, fmt.Errorf("the job records in %s are damaged: %w", dir, err)
		}
		for _, item := range saved.Jobs {
			job := &replJob{spec: item.Spec, status: item.Status, throttle: newVMThrottle(item.Spec.Throttle)}
			if job.status.State == replStateSyncing || job.status.State == replStateCutover {
				// The agent stopped during a round: run it again.
				job.status.State, job.status.Message = replStateIdle, "The agent restarted during a round; it runs again now."
				job.status.NextRun = time.Time{}
			}
			job.logs = replTailFile(a.logPath(item.Spec.ID), 100)
			a.jobs[item.Spec.ID] = job
		}
		a.alerts, a.alertID = saved.Alerts, saved.AlertID
	case !os.IsNotExist(err):
		return nil, err
	}
	return a, nil
}

func (a *replAgent) logPath(id string) string {
	return filepath.Join(a.dir, "Logs", id+".log")
}

// Caller holds a.mu.
func (a *replAgent) saveLocked() error {
	saved := replSaved{Version: replDataVersion, AlertID: a.alertID, Alerts: a.alerts}
	for _, job := range a.jobs {
		saved.Jobs = append(saved.Jobs, replSavedJob{Spec: job.spec, Status: job.status})
	}
	sort.Slice(saved.Jobs, func(i, j int) bool { return saved.Jobs[i].Spec.Name < saved.Jobs[j].Spec.Name })
	raw, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	name := filepath.Join(a.dir, "jobs.json")
	if err := os.WriteFile(name+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(name+".tmp", name)
}

// Caller holds a.mu.
func (a *replAgent) logLocked(job *replJob, format string, args ...interface{}) {
	line := a.now().Format("2006-01-02 15:04:05") + "  " + fmt.Sprintf(format, args...)
	job.logs = append(job.logs, line)
	if len(job.logs) > replMaxLogLines {
		job.logs = job.logs[len(job.logs)-replMaxLogLines:]
	}
	path := a.logPath(job.spec.ID)
	if info, err := os.Stat(path); err == nil && info.Size() > replLogFileLimit {
		os.Rename(path, path+".old")
	}
	if file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
		file.WriteString(line + "\n")
		file.Close()
	}
}

func replTailFile(path string, lines int) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	all := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return all
}

// Caller holds a.mu.
func (a *replAgent) alertLocked(job *replJob, level, kind, text string) {
	for i := range a.alerts {
		if a.alerts[i].Job == job.spec.ID && a.alerts[i].Kind == kind && !a.alerts[i].Cleared {
			return // already raised and not yet cleared
		}
	}
	a.alertID++
	alert := replAlert{ID: a.alertID, Time: a.now().UTC(), Job: job.spec.ID, JobName: job.spec.Name, Level: level, Kind: kind, Text: text}
	a.alerts = append(a.alerts, alert)
	if len(a.alerts) > 200 {
		a.alerts = a.alerts[len(a.alerts)-200:]
	}
	a.logLocked(job, "ALERT: %s", text)
	if a.notify != nil {
		go a.notify(alert)
	}
}

// Caller holds a.mu.
func (a *replAgent) clearAlertsLocked(job *replJob, kind string) {
	for i := range a.alerts {
		if a.alerts[i].Job == job.spec.ID && a.alerts[i].Kind == kind && !a.alerts[i].Cleared {
			a.alerts[i].Cleared = true
			a.logLocked(job, "Cleared: %s", a.alerts[i].Text)
		}
	}
}

func replNewID() string {
	raw := make([]byte, 8)
	rand.Read(raw)
	return hex.EncodeToString(raw)
}

var replServiceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@:-][A-Za-z0-9 _.@:-]{0,79}$`)

// validateReplSpec checks a job and fills defaults. existing is the job's
// current spec when it is being changed.
func validateReplSpec(spec *replJobSpec, existing *replJobSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" || len(spec.Name) > 80 {
		return errors.New("give the job a name of up to 80 characters")
	}
	if len(spec.Disks) == 0 || len(spec.Disks) > 32 {
		return errors.New("choose from 1 to 32 disks to replicate")
	}
	sources, targets := map[string]bool{}, map[string]bool{}
	for i, disk := range spec.Disks {
		disk.Source, disk.Target = strings.TrimSpace(disk.Source), strings.TrimSpace(disk.Target)
		if disk.Source == "" || len(disk.Source) > 64 || strings.ContainsAny(disk.Source, "\r\n\"'`$;|&<>") {
			return fmt.Errorf("disk %d has no valid source", i+1)
		}
		if !vmReceiverName.MatchString(disk.Target) && !vmReceiverDiskName.MatchString(disk.Target) {
			return fmt.Errorf("choose where disk %s goes on the receiver (a .vhdx name or disk-N)", disk.Source)
		}
		if sources[disk.Source] {
			return fmt.Errorf("disk %s is listed twice", disk.Source)
		}
		if targets[strings.ToLower(disk.Target)] {
			return fmt.Errorf("two disks go to %s; choose a different target for each", disk.Target)
		}
		sources[disk.Source], targets[strings.ToLower(disk.Target)] = true, true
		spec.Disks[i] = disk
	}
	address, err := vmReceiverAddress(spec.Receiver.Address)
	if err != nil {
		return err
	}
	spec.Receiver.Address = address
	spec.Receiver.Thumbprint = vmNormalizeThumbprint(spec.Receiver.Thumbprint)
	if spec.Receiver.Thumbprint == "" {
		return errors.New("enter the receiver's certificate thumbprint (64 characters)")
	}
	if spec.Receiver.Code == "" && existing != nil && existing.Receiver.Address == spec.Receiver.Address && existing.Receiver.Thumbprint == spec.Receiver.Thumbprint {
		spec.Receiver.Code = existing.Receiver.Code // unchanged receiver: keep its code
	}
	if len(vmNormalizePairingCode(spec.Receiver.Code)) != 20 {
		return errors.New("enter the receiver's 20-character pairing code")
	}
	if spec.IntervalMinutes < 0 || spec.IntervalMinutes > 24*60 {
		return errors.New("the interval must be from 0 (continuous) to 1440 minutes")
	}
	if spec.RPOMinutes == 0 {
		spec.RPOMinutes = replDefaultRPO
	}
	if spec.RPOMinutes < 1 || spec.RPOMinutes > 7*24*60 {
		return errors.New("the RPO alert must be from 1 minute to 7 days")
	}
	if err := spec.Throttle.Validate(); err != nil {
		return err
	}
	if len(spec.StopServices) > 50 {
		return errors.New("list at most 50 services to stop at cutover")
	}
	for i, name := range spec.StopServices {
		name = strings.TrimSpace(name)
		if !replServiceNamePattern.MatchString(name) {
			return fmt.Errorf("%q is not a service name", name)
		}
		spec.StopServices[i] = name
	}
	return nil
}

// Jobs lists every job, without pairing codes.
func (a *replAgent) Jobs() []replJobView {
	a.mu.Lock()
	defer a.mu.Unlock()
	views := []replJobView{}
	now := a.now()
	for _, job := range a.jobs {
		views = append(views, a.viewLocked(job, now))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Spec.Name < views[j].Spec.Name })
	return views
}

// Caller holds a.mu.
func (a *replAgent) viewLocked(job *replJob, now time.Time) replJobView {
	view := replJobView{Spec: job.spec, Status: job.status, HasCode: job.spec.Receiver.Code != ""}
	view.Spec.Receiver.Code = ""
	view.Spec.StopServices = append([]string(nil), job.spec.StopServices...)
	view.Status.Disks = append([]replDiskStatus(nil), job.status.Disks...)
	if !job.status.RecoveryPoint.IsZero() {
		view.RPOSecond = int64(now.Sub(job.status.RecoveryPoint) / time.Second)
	}
	return view
}

// Alerts lists recent alerts, newest first.
func (a *replAgent) Alerts() []replAlert {
	a.mu.Lock()
	defer a.mu.Unlock()
	list := append([]replAlert(nil), a.alerts...)
	sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
	return list
}

// Log returns the last lines of a job's log.
func (a *replAgent) Log(id string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	job := a.jobs[id]
	if job == nil {
		return nil, errors.New("there is no such job")
	}
	return append([]string(nil), job.logs...), nil
}

// SaveJob creates a job or changes one that is not running a round.
func (a *replAgent) SaveJob(spec replJobSpec) (replJobView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var existing *replJob
	if spec.ID != "" {
		if existing = a.jobs[spec.ID]; existing == nil {
			return replJobView{}, errors.New("there is no such job")
		}
		if existing.running || existing.status.State == replStateCutover {
			return replJobView{}, errors.New("the job is running a round; pause it before changing it")
		}
		if existing.status.State == replStateCutDone {
			return replJobView{}, errors.New("the job has been cut over; create a new job to replicate again")
		}
	} else {
		if len(a.jobs) >= replMaxJobs {
			return replJobView{}, fmt.Errorf("an agent runs at most %d jobs", replMaxJobs)
		}
		spec.ID = replNewID()
	}
	var previous *replJobSpec
	if existing != nil {
		previous = &existing.spec
	}
	if err := validateReplSpec(&spec, previous); err != nil {
		return replJobView{}, err
	}
	for id, other := range a.jobs {
		if id == spec.ID || other.status.State == replStateCutDone {
			continue
		}
		for _, mine := range spec.Disks {
			for _, theirs := range other.spec.Disks {
				if strings.EqualFold(other.spec.Receiver.Address, spec.Receiver.Address) && strings.EqualFold(mine.Target, theirs.Target) {
					return replJobView{}, fmt.Errorf("the job %s already sends to %s on that receiver", other.spec.Name, mine.Target)
				}
			}
		}
	}
	if existing == nil {
		existing = &replJob{status: replJobStatus{State: replStateNeverRun, Message: "Waiting for its first round."}}
		a.jobs[spec.ID] = existing
		existing.spec = spec
		a.logLocked(existing, "Job created: %s.", replDescribeSpec(spec))
	} else {
		existing.spec = spec
		a.logLocked(existing, "Job changed: %s.", replDescribeSpec(spec))
	}
	existing.throttle = newVMThrottle(spec.Throttle)
	existing.status.Disks = replDiskStatuses(spec, existing.status.Disks)
	if existing.status.State == replStateError {
		existing.status.NextRun = time.Time{} // try the new settings now
	}
	if err := a.saveLocked(); err != nil {
		return replJobView{}, err
	}
	return a.viewLocked(existing, a.now()), nil
}

func replDescribeSpec(spec replJobSpec) string {
	parts := []string{}
	for _, disk := range spec.Disks {
		parts = append(parts, disk.Source+" to "+disk.Target)
	}
	every := "continuously"
	if spec.IntervalMinutes > 0 {
		every = fmt.Sprintf("every %d minutes", spec.IntervalMinutes)
	}
	return fmt.Sprintf("%s on %s, %s, RPO alert after %d minutes, bandwidth %s, compression %t", strings.Join(parts, ", "), spec.Receiver.Address, every, spec.RPOMinutes, spec.Throttle.Describe(), spec.Compress)
}

func replDiskStatuses(spec replJobSpec, previous []replDiskStatus) []replDiskStatus {
	list := []replDiskStatus{}
	for _, disk := range spec.Disks {
		item := replDiskStatus{Source: disk.Source, Target: disk.Target}
		for _, old := range previous {
			if old.Source == disk.Source && old.Target == disk.Target {
				item = old
			}
		}
		list = append(list, item)
	}
	return list
}

// Action runs an operator action on a job: pause, resume, sync (now),
// rescan (rebuild the receiver records on the next round), cutover,
// cancel-cutover (a scheduled one), delete and clear (alerts).
func (a *replAgent) Action(id, action string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	job := a.jobs[id]
	if job == nil {
		return errors.New("there is no such job")
	}
	done := job.status.State == replStateCutDone
	switch action {
	case "pause":
		if done {
			return errors.New("the job has been cut over")
		}
		if job.status.State == replStateCutover {
			return errors.New("the job is cutting over")
		}
		if job.cancel != nil {
			job.cancel()
		}
		job.status.State, job.status.Message = replStatePaused, "Paused. Resume to replicate again."
		a.logLocked(job, "Paused by the operator.")
	case "resume":
		if job.status.State != replStatePaused {
			return errors.New("the job is not paused")
		}
		job.status.State, job.status.Message, job.status.NextRun = replStateIdle, "Resumed.", time.Time{}
		a.logLocked(job, "Resumed by the operator.")
	case "sync":
		if done || job.status.State == replStatePaused || job.status.State == replStateCutover {
			return errors.New("the job is " + job.status.State)
		}
		job.status.NextRun = time.Time{}
		a.logLocked(job, "A round was asked for now.")
	case "rescan":
		if done {
			return errors.New("the job has been cut over")
		}
		job.status.RescanNext = true
		a.logLocked(job, "The next round rescans the receiver's disks first, so only what differs is sent.")
	case "cutover":
		if done || job.status.State == replStateCutover {
			return errors.New("the job is " + job.status.State)
		}
		if job.status.Rounds == 0 || job.status.RecoveryPoint.IsZero() {
			return errors.New("the job has not finished a round yet; let the first copy finish before cutting over")
		}
		if job.cancel != nil {
			job.cancel()
		}
		job.cutover = true
		a.logLocked(job, "Cutover was asked for.")
	case "cancel-cutover":
		if job.spec.CutoverAt == nil {
			return errors.New("no cutover is scheduled")
		}
		job.spec.CutoverAt = nil
		a.logLocked(job, "The scheduled cutover was cancelled.")
	case "delete":
		if job.running || job.status.State == replStateCutover {
			if job.cancel != nil {
				job.cancel()
			}
			return errors.New("the job is stopping its round; delete it again in a moment")
		}
		delete(a.jobs, id)
		os.Remove(a.logPath(id))
		os.Remove(a.logPath(id) + ".old")
		for i := range a.alerts {
			if a.alerts[i].Job == id {
				a.alerts[i].Cleared = true
			}
		}
	case "clear":
		for i := range a.alerts {
			if a.alerts[i].Job == id {
				a.alerts[i].Cleared = true
			}
		}
	default:
		return errors.New("unknown action")
	}
	return a.saveLocked()
}

// Schedule sets or clears a job's scheduled cutover time.
func (a *replAgent) Schedule(id string, at *time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	job := a.jobs[id]
	if job == nil {
		return errors.New("there is no such job")
	}
	if job.status.State == replStateCutDone {
		return errors.New("the job has been cut over")
	}
	if at != nil {
		if at.Before(a.now().Add(-time.Minute)) {
			return errors.New("choose a cutover time in the future")
		}
		utc := at.UTC()
		at = &utc
		a.logLocked(job, "Cutover scheduled for %s UTC.", utc.Format("2006-01-02 15:04"))
	} else {
		a.logLocked(job, "The scheduled cutover was cancelled.")
	}
	job.spec.CutoverAt = at
	return a.saveLocked()
}

// Tick starts rounds and cutovers that are due and raises RPO alerts. The
// agent calls it every few seconds.
func (a *replAgent) Tick() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopping {
		return
	}
	now := a.now()
	changed := false
	for _, job := range a.jobs {
		state := job.status.State
		if state == replStateCutDone {
			continue
		}
		// Recovery point age.
		if state != replStatePaused && !job.status.RecoveryPoint.IsZero() {
			age := now.Sub(job.status.RecoveryPoint)
			limit := time.Duration(job.spec.RPOMinutes) * time.Minute
			if age > limit {
				a.alertLocked(job, "warning", "rpo", fmt.Sprintf("%s is behind: its recovery point is %s old, more than the %d minutes allowed.", job.spec.Name, replAge(age), job.spec.RPOMinutes))
				changed = true
			}
		}
		if job.running || state == replStatePaused {
			continue
		}
		if job.spec.CutoverAt != nil && !now.Before(*job.spec.CutoverAt) && !job.status.RecoveryPoint.IsZero() {
			job.cutover = true
			job.spec.CutoverAt = nil
			a.logLocked(job, "The scheduled cutover time has come.")
			changed = true
		}
		if job.cutover {
			a.startLocked(job, true)
			changed = true
			continue
		}
		if job.status.NextRun.IsZero() || !now.Before(job.status.NextRun) {
			a.startLocked(job, false)
			changed = true
		}
	}
	if changed {
		a.saveLocked()
	}
}

func replAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
	return fmt.Sprintf("%.1f days", d.Hours()/24)
}

// Caller holds a.mu.
func (a *replAgent) startLocked(job *replJob, cutover bool) {
	ctx, cancel := context.WithCancel(context.Background())
	job.running, job.cancel = true, cancel
	job.status.LastStart = a.now().UTC()
	if cutover {
		job.status.State, job.status.Message = replStateCutover, "Cutting over: stopping services, then the last round."
	} else {
		job.status.State, job.status.Message = replStateSyncing, "Taking snapshots."
	}
	spec := job.spec
	spec.Disks = append([]replDiskMap(nil), job.spec.Disks...)
	spec.StopServices = append([]string(nil), job.spec.StopServices...)
	rescan := job.status.RescanNext
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		if cutover {
			a.runCutover(ctx, job, spec, rescan)
		} else {
			a.runRound(ctx, job, spec, rescan, false)
		}
	}()
}

func (a *replAgent) jobLog(job *replJob) func(string) {
	return func(text string) {
		a.mu.Lock()
		a.logLocked(job, "%s", text)
		a.mu.Unlock()
	}
}

// runRound runs one round and records its outcome. final rounds verify
// every disk. It returns whether the round succeeded.
func (a *replAgent) runRound(ctx context.Context, job *replJob, spec replJobSpec, rescan, final bool) bool {
	log := a.jobLog(job)
	sources := []string{}
	for _, disk := range spec.Disks {
		sources = append(sources, disk.Source)
	}
	started := a.now()
	err := func() error {
		opened, err := a.platform.OpenDisks(ctx, spec.ID, sources, spec.AllowLive, log)
		if err != nil {
			return err
		}
		defer opened.Close()
		a.mu.Lock()
		job.status.Notes = opened.Notes
		if job.status.State == replStateSyncing {
			job.status.Message = "Sending what changed."
		}
		a.mu.Unlock()
		var read, sent int64
		for index, disk := range spec.Disks {
			source := opened.Sources[disk.Source]
			if source == nil {
				return fmt.Errorf("disk %s was not opened", disk.Source)
			}
			sink, err := a.newSink(ctx, spec.Receiver, disk.Target)
			if err != nil {
				return fmt.Errorf("disk %s: %w", disk.Source, err)
			}
			sink.Configure(spec.Compress, job.throttle, rescan)
			if rescan {
				log(fmt.Sprintf("Disk %s: rescanning %s on the receiver.", disk.Source, disk.Target))
			}
			options := vmCopyOptions{Verify: final, Ranges: opened.Ranges[disk.Source], Progress: func(p vmCopyProgress) {
				a.mu.Lock()
				if index < len(job.status.Disks) {
					d := &job.status.Disks[index]
					d.Chunks, d.Done, d.Read, d.Sent, d.Changed = p.Chunks, p.Done, p.BytesRead, p.BytesSent, p.Changed
				}
				a.mu.Unlock()
			}}
			result, err := vmCopyTo(ctx, source, sink, options)
			a.mu.Lock()
			if index < len(job.status.Disks) {
				d := &job.status.Disks[index]
				d.Error = ""
				if err != nil {
					d.Error = err.Error()
				} else {
					d.Size, d.Passes, d.Verified = result.State.Size, result.State.Passes, result.State.Verified
					d.Chunks, d.Done, d.Read, d.Sent, d.Changed = result.Progress.Chunks, result.Progress.Done, result.Progress.BytesRead, result.Progress.BytesSent, result.Progress.Changed
				}
			}
			a.mu.Unlock()
			if err != nil {
				return fmt.Errorf("disk %s: %w", disk.Source, err)
			}
			read += result.Progress.BytesRead
			sent += result.Progress.BytesSent
			log(fmt.Sprintf("Disk %s to %s: %s read, %s sent, %d chunks changed%s.", disk.Source, disk.Target, vmFormatBytes(result.Progress.BytesRead), vmFormatBytes(result.Progress.BytesSent), result.Progress.Changed, map[bool]string{true: ", verified", false: ""}[result.State.Verified]))
		}
		if opened.Commit != nil {
			if err := opened.Commit(); err != nil {
				log("Change tracking could not record this round, so the next round reads every disk: " + err.Error())
			}
		}
		a.mu.Lock()
		job.status.RecoveryPoint = opened.Snapshot.UTC()
		job.status.LastRead, job.status.LastSent = read, sent
		job.status.TotalSent += sent
		a.mu.Unlock()
		return nil
	}()
	a.mu.Lock()
	defer a.mu.Unlock()
	job.running, job.cancel = false, nil
	job.status.LastEnd = a.now().UTC()
	job.status.LastDuration = a.now().Sub(started).Seconds()
	interval := time.Duration(spec.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Minute
	}
	switch {
	case err == nil:
		job.status.Rounds++
		job.status.Failures, job.status.LastError = 0, ""
		if rescan {
			job.status.RescanNext = false
		}
		a.clearAlertsLocked(job, "failing")
		if !job.status.RecoveryPoint.IsZero() && a.now().Sub(job.status.RecoveryPoint) <= time.Duration(spec.RPOMinutes)*time.Minute {
			a.clearAlertsLocked(job, "rpo")
		}
		if !final {
			job.status.State = replStateIdle
			next := started.Add(interval)
			if spec.IntervalMinutes == 0 {
				next = a.now().Add(interval)
			}
			job.status.NextRun = next.UTC()
			job.status.Message = fmt.Sprintf("Up to date as of %s UTC. Next round at %s UTC.", job.status.RecoveryPoint.Format("15:04:05"), job.status.NextRun.Format("15:04"))
		}
		a.logLocked(job, "Round %d finished in %s; recovery point %s UTC.", job.status.Rounds, replAge(a.now().Sub(started)), job.status.RecoveryPoint.Format("2006-01-02 15:04:05"))
	case errors.Is(err, context.Canceled):
		if job.status.State == replStateSyncing {
			job.status.State, job.status.Message = replStateIdle, "The round was stopped."
		}
		a.logLocked(job, "The round was stopped; the next one resumes where it stopped.")
	default:
		job.status.Failures++
		job.status.LastError = err.Error()
		if !final {
			backoff := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute}
			wait := backoff[len(backoff)-1]
			if job.status.Failures <= len(backoff) {
				wait = backoff[job.status.Failures-1]
			}
			job.status.State = replStateError
			job.status.NextRun = a.now().Add(wait).UTC()
			job.status.Message = fmt.Sprintf("The round failed; it tries again at %s UTC.", job.status.NextRun.Format("15:04"))
		}
		a.logLocked(job, "The round failed: %s", err.Error())
		if job.status.Failures >= replFailuresToAlert {
			a.alertLocked(job, "error", "failing", fmt.Sprintf("%s has failed %d rounds in a row: %s", spec.Name, job.status.Failures, err.Error()))
		}
	}
	a.saveLocked()
	return err == nil
}

// runCutover stops services, runs the verified last round and, when asked,
// shuts the source down. On failure it starts the services again.
func (a *replAgent) runCutover(ctx context.Context, job *replJob, spec replJobSpec, rescan bool) {
	log := a.jobLog(job)
	fail := func(text string) {
		if len(spec.StopServices) > 0 {
			log("Starting the stopped services again (rollback).")
			if err := a.platform.StartServices(spec.StopServices, log); err != nil {
				log("Some services did not start again: " + err.Error())
			}
		}
		a.mu.Lock()
		job.cutover = false
		job.running, job.cancel = false, nil
		job.status.State, job.status.Message = replStateError, "Cutover failed and was rolled back: "+text
		job.status.NextRun = a.now().Add(time.Minute).UTC()
		a.alertLocked(job, "error", "cutover", fmt.Sprintf("Cutover of %s failed and was rolled back: %s", spec.Name, text))
		a.saveLocked()
		a.mu.Unlock()
	}
	if len(spec.StopServices) > 0 {
		log("Stopping " + strings.Join(spec.StopServices, ", ") + ".")
		if err := a.platform.StopServices(spec.StopServices, log); err != nil {
			fail("the services could not be stopped: " + err.Error())
			return
		}
	}
	a.mu.Lock()
	job.status.Message = "Cutting over: sending the last changes and verifying every disk."
	job.running = true
	a.mu.Unlock()
	if !a.runRound(ctx, job, spec, rescan, true) {
		a.mu.Lock()
		text := job.status.LastError
		a.mu.Unlock()
		if text == "" {
			text = "the last round was stopped"
		}
		fail(text)
		return
	}
	a.mu.Lock()
	job.cutover = false
	job.status.State = replStateCutDone
	job.status.CutoverAt = a.now().UTC()
	job.status.NextRun = time.Time{}
	job.status.Message = "Cut over. Every disk matches the source as of " + job.status.RecoveryPoint.Format("2006-01-02 15:04:05") + " UTC. Start the new server now."
	a.clearAlertsLocked(job, "rpo")
	a.clearAlertsLocked(job, "cutover")
	a.logLocked(job, "Cutover finished; every disk was verified.")
	shutdown := spec.ShutdownAfterCutover
	if shutdown {
		job.status.Message += " This server shuts down in 30 seconds."
	}
	a.saveLocked()
	a.mu.Unlock()
	if shutdown {
		go func() {
			time.Sleep(replShutdownDelay)
			if err := a.platform.Shutdown(log); err != nil {
				log("This server could not be shut down: " + err.Error())
			}
		}()
	}
}

// Run calls Tick until ctx ends, then stops every round.
func (a *replAgent) Run(ctx context.Context) {
	ticker := time.NewTicker(replTickInterval)
	defer ticker.Stop()
	a.Tick()
	for {
		select {
		case <-ctx.Done():
			a.Stop()
			return
		case <-ticker.C:
			a.Tick()
		}
	}
}

// Stop cancels running rounds and waits for them.
func (a *replAgent) Stop() {
	a.mu.Lock()
	a.stopping = true
	for _, job := range a.jobs {
		if job.cancel != nil {
			job.cancel()
		}
	}
	a.mu.Unlock()
	a.wg.Wait()
	a.mu.Lock()
	a.saveLocked()
	a.mu.Unlock()
}
