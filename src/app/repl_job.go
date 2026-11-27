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
	"runtime"
	"runtime/debug"
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

// The stall watchdog: a round that makes no progress for replStallAfter
// (no chunk read and sent) is stopped and tried again; a read or write that
// does not return within replStallGrace after that is abandoned, so a hung
// disk or network never holds a job (or a cutover) for good. A cutover's
// final round is tried replCutoverAttempts times before it is rolled back.
var (
	replStallAfter      = 5 * time.Minute
	replStallGrace      = 2 * time.Minute
	replStallCheck      = 15 * time.Second
	replCutoverAttempts = 3
	replCutoverRetry    = 30 * time.Second
)

// errReplStalled fails a round the watchdog stopped.
var errReplStalled = errors.New("the round made no progress")

// Job states.
const (
	replStateIdle     = "idle"    // waiting for the next round
	replStateSyncing  = "syncing" // a round is running
	replStatePaused   = "paused"  // the operator paused it
	replStateError    = "error"   // the last round failed; it retries
	replStateHeld     = "held"    // the target is held for a VM started from the copy
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
	// Alternates are the receiver computer's other addresses (from Copy all
	// or a find answer). When Address does not answer, a round tries them
	// in order with the same pinned thumbprint and code, and the first that
	// answers becomes Address.
	Alternates []string `json:"alternates,omitempty"`
}

// replMaxAlternates limits how many other addresses a job keeps.
const replMaxAlternates = 8

// replCleanAlternates checks each address, drops duplicates and the primary
// address, and keeps at most replMaxAlternates.
func replCleanAlternates(primary string, list []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{strings.ToLower(primary): true}
	for _, text := range list {
		if strings.TrimSpace(text) == "" {
			continue
		}
		address, err := vmReceiverAddress(text)
		if err != nil {
			return nil, fmt.Errorf("the receiver's other address %q was not accepted: %w", text, err)
		}
		if seen[strings.ToLower(address)] {
			continue
		}
		seen[strings.ToLower(address)] = true
		out = append(out, address)
		if len(out) == replMaxAlternates {
			break
		}
	}
	return out, nil
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
	StopServices         []string `json:"stopServices,omitempty"`
	ShutdownAfterCutover bool     `json:"shutdownAfterCutover"`
	// FullReadAtCutover reads every used block in the final round instead
	// of only what change tracking reports.
	FullReadAtCutover bool `json:"fullReadAtCutover"`
	// RescanFirst makes a new job's first round compare the receiver's
	// disks with the source (a failback onto disks holding an older copy).
	RescanFirst bool       `json:"rescanFirst,omitempty"`
	CutoverAt   *time.Time `json:"cutoverAt,omitempty"`
	// Space keeps free space on the server's own volumes: a round waits
	// rather than take its shadow copies (or LVM snapshots) when a volume it
	// reads would keep less (prodguard.go). nil uses replDefaultSpace;
	// both values 0 turn the check off.
	Space *replSpace `json:"space,omitempty"`
}

// replSpace is the free space a replication job keeps on its source volumes.
type replSpace struct {
	ReservePercent int `json:"reservePercent"`
	ReserveGB      int `json:"reserveGB"`
}

// replDefaultSpace is what jobs without their own setting keep free. A
// shadow copy grows only by what is written during the round, so the
// estimate is small: 2 GB per volume.
var replDefaultSpace = replSpace{ReservePercent: 10, ReserveGB: 20}

func (s *replSpace) validate() error {
	if s == nil {
		return nil
	}
	if s.ReservePercent < 0 || s.ReservePercent > 50 || s.ReserveGB < 0 || s.ReserveGB > 100000 {
		return errors.New("keep 0 to 50% and 0 to 100000 GB free on the server's volumes")
	}
	return nil
}

func (s *replSpace) settings() (pgSettings, bool) {
	space := replDefaultSpace
	if s != nil {
		space = *s
	}
	if space.ReservePercent == 0 && space.ReserveGB == 0 {
		return pgSettings{}, false
	}
	settings := defaultPGSettings()
	settings.ReservePercent, settings.ReserveGB = space.ReservePercent, space.ReserveGB
	settings.GrowthPercent, settings.GrowthMinGB = 0, 2
	return settings, true
}

// checkSpace reads the free space of the volumes on the job's disks and
// holds the round when one would keep less than the job's reserve. A volume
// whose space cannot be read is not checked; the round goes on.
func (a *replAgent) checkSpace(spec replJobSpec, sources []string) error {
	settings, on := spec.Space.settings()
	if !on || a.spaceFree == nil {
		return nil
	}
	disks, err := a.platform.ListDisks()
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, source := range sources {
		want[source] = true
	}
	volumes := []pgVolume{}
	for _, disk := range disks {
		if !want[disk.ID] {
			continue
		}
		for _, volume := range disk.Volumes {
			if len(volume.Mounts) == 0 {
				continue
			}
			free, total, ok := a.spaceFree(volume.Mounts[0])
			if !ok || total == 0 {
				continue
			}
			volumes = append(volumes, pgVolume{Key: volume.Name, Label: volume.Mounts[0], Free: int64(free), Capacity: int64(total), VMBytes: volume.Length})
		}
	}
	if err := pgCheckFree(settings, volumes); err != nil {
		text := strings.Replace(err.Error(), "Waiting for space on datastore", "Waiting for space on volume", 1)
		text = strings.Replace(text, "this VM's snapshot may grow by", "the shadow copy may grow by", 1)
		text = strings.Replace(text, "kept free for production (Help > Settings)", "kept free for this server (the job's settings)", 1)
		return &pgHoldError{Reason: text}
	}
	return nil
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
	RecoveryPoint time.Time `json:"recoveryPoint"`
	// InitialCopyAt is when the first full copy finished: from then on the
	// receiver holds a whole copy and the job can be tested or cut over.
	InitialCopyAt time.Time        `json:"initialCopyAt"`
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
	RescanNext bool `json:"rescanNext"`
	// Consistent is true while the receiver's disks hold exactly the last
	// recovery point: after a finished round and until the next one starts.
	Consistent bool `json:"consistent"`
	// PauseAfterRound pauses the job when the running (or next) round
	// finishes, so the receiver's disks stay at one recovery point (a test
	// failover on a platform clones them).
	PauseAfterRound bool      `json:"pauseAfterRound,omitempty"`
	CutoverAt       time.Time `json:"cutoverDone,omitempty"`
	Notes           []string  `json:"notes,omitempty"`
	// LastProgress is when the running round last read or sent a chunk,
	// and Phase what it was doing (copying, finishing); the console warns
	// when it stops moving.
	LastProgress time.Time `json:"lastProgress,omitempty"`
	Phase        string    `json:"phase,omitempty"`
	// StallAfterSeconds is how long the watchdog waits before it stops a
	// round that is not moving and tries again.
	StallAfterSeconds int64 `json:"stallAfterSeconds,omitempty"`
	// CutoverAttempt is the attempt of the final round while cutting over.
	CutoverAttempt int `json:"cutoverAttempt,omitempty"`
	// LogFolder is where the agent keeps this job's log on its computer.
	LogFolder string `json:"logFolder,omitempty"`
}

type replAlert struct {
	ID      int       `json:"id"`
	Time    time.Time `json:"time"`
	Job     string    `json:"job"`
	JobName string    `json:"jobName"`
	Level   string    `json:"level"` // warning or error
	Kind    string    `json:"kind"`  // rpo, failing, cutover, stopped
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
	// IdleSeconds is how long the running round has gone without reading
	// or sending anything, measured on the agent's clock.
	IdleSeconds int64 `json:"idleSeconds,omitempty"`
}

// replOpenedDisks are the snapshots of one round.
type replOpenedDisks struct {
	Sources  map[string]vmCopySource
	Snapshot time.Time
	Notes    []string
	// Ranges optionally limits a disk's round to changed areas, and Used
	// to the areas in use (both nil: read everything).
	Ranges map[string][]vmCopyRange
	Used   map[string][]vmCopyRange
	// Commit is called after every disk of the round was sent, with each
	// disk's result, so change tracking can move its marker forward. It
	// may be nil.
	Commit func(results map[string]*vmCopyResult) error
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
	// pending is a change saved while a round ran: the round is stopped and
	// the change applied the moment it ends (applyPendingLocked).
	pending *replJobSpec
	// interrupted: the agent's previous run ended during this job's round.
	interrupted bool
	// restartServices: the agent's previous run ended during a cutover
	// that had stopped services; they are started again (rollback).
	restartServices bool
	// generation counts rounds, so a round the watchdog abandoned never
	// writes into a later round's status.
	generation int
	// lastErr is the error of the last round (nil when it succeeded).
	lastErr error
	// cancelCutover: the operator cancelled the running cutover;
	// stopCutover stops it, including between attempts.
	cancelCutover bool
	stopCutover   context.CancelFunc
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
	// probeReceiver asks a receiver for its disks with the job's pin and
	// code; reachReceiver uses it to choose between the receiver's addresses.
	probeReceiver func(ctx context.Context, address, thumbprint, code string) (bool, []vmReceiverDisk, error)
	// spaceFree reads a mounted volume's free and total bytes for the
	// space check; nil skips it.
	spaceFree func(mount string) (free, total uint64, ok bool)
	notify    func(replAlert)
	wg        sync.WaitGroup
	stopping  bool
	// lastStop says how the agent's previous run ended when that was not
	// a clean stop (see replCheckPreviousStop); empty otherwise.
	lastStop   string
	lastStopAt time.Time
}

// replCrashLog receives the Go runtime's own output when the agent process
// crashes (standard error is pointed at it); replErrorLog receives internal
// errors the agent caught, which failed one round but not the agent.
const (
	replCrashLog = "agent-crash.log"
	replErrorLog = "agent-errors.log"
)

// recordPanic turns a recovered panic into an error and saves its stack to
// the agent's crash log, so the details survive.
func (a *replAgent) recordPanic(job *replJob, r interface{}) error {
	stack := debug.Stack()
	err := fmt.Errorf("internal error in the agent: %v", r)
	path := filepath.Join(a.dir, "Logs", replErrorLog)
	if file, openErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); openErr == nil {
		fmt.Fprintf(file, "%s  job %s: %v (recovered; the round failed)\n%s\n", a.now().UTC().Format(time.RFC3339), job.spec.ID, r, stack)
		file.Close()
	}
	a.mu.Lock()
	a.logLocked(job, "%s. Details are in %s.", err.Error(), path)
	a.mu.Unlock()
	return err
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
	a.probeReceiver = vmReceiverDescribe
	// A Windows shadow copy grows inside the volume it copies, so the
	// volume's free space decides whether a round may start. A Linux LVM
	// snapshot takes its space from the volume group, and lvcreate refuses
	// a snapshot the group cannot hold, so the file system is not checked.
	if runtime.GOOS == "windows" {
		a.spaceFree = pgMountFree
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
				if job.status.State == replStateCutover && len(job.spec.StopServices) > 0 {
					job.restartServices = true
				}
				if job.status.State == replStateCutover {
					job.status.Message = "The agent restarted during the cutover, so it was rolled back; replication carries on. Cut over again when ready."
				} else {
					job.status.Message = "The agent restarted during a round; it runs again now."
				}
				job.status.State = replStateIdle
				job.status.CutoverAttempt, job.status.Phase = 0, ""
				job.interrupted = true
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

// noteLocked records a milestone (an information alert that needs no
// action, so it is stored already cleared and never counts as open) and
// passes it to the event log. Caller holds a.mu.
func (a *replAgent) noteLocked(job *replJob, kind, text string) {
	a.alertID++
	alert := replAlert{ID: a.alertID, Time: a.now().UTC(), Job: job.spec.ID, JobName: job.spec.Name, Level: "info", Kind: kind, Text: text, Cleared: true}
	a.alerts = append(a.alerts, alert)
	if len(a.alerts) > 200 {
		a.alerts = a.alerts[len(a.alerts)-200:]
	}
	a.logLocked(job, "%s", text)
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
	if spec.Receiver.Alternates, err = replCleanAlternates(address, spec.Receiver.Alternates); err != nil {
		return err
	}
	spec.Receiver.Thumbprint = vmNormalizeThumbprint(spec.Receiver.Thumbprint)
	if spec.Receiver.Thumbprint == "" {
		return errors.New("enter the receiver's certificate thumbprint (64 characters)")
	}
	if spec.Receiver.Code == "" && existing != nil && existing.Receiver.Thumbprint == spec.Receiver.Thumbprint && replLinkHasAddress(existing.Receiver, spec.Receiver.Address) {
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
	if err := spec.Space.validate(); err != nil {
		return err
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
	if job.running && !job.status.LastProgress.IsZero() {
		view.IdleSeconds = int64(now.Sub(job.status.LastProgress) / time.Second)
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

// SaveJob creates a job or changes one. Changing a job that is running a
// round stops that round and applies the change the moment it ends, then a
// new round starts with the new settings (unless the job is paused), so the
// operator never has to pause first or save twice.
func (a *replAgent) SaveJob(spec replJobSpec) (replJobView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var existing *replJob
	if spec.ID != "" {
		if existing = a.jobs[spec.ID]; existing == nil {
			return replJobView{}, errors.New("there is no such job")
		}
		if existing.status.State == replStateCutover || existing.cutover {
			return replJobView{}, errors.New("the job is cutting over; wait for the cutover to finish")
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
	if existing != nil && existing.running {
		pending := spec
		existing.pending = &pending
		if existing.cancel != nil {
			existing.cancel()
		}
		existing.status.Message = "Stopping the current round to apply your change. A new round starts with the new settings; it resumes where this one stopped."
		a.logLocked(existing, "A change was saved during a round; the round is being stopped so the change can be applied.")
		view := a.viewLocked(existing, a.now())
		view.Spec = pending
		view.Spec.Receiver.Code = ""
		view.HasCode = pending.Receiver.Code != ""
		return view, nil
	}
	a.applySpecLocked(existing, spec)
	if err := a.saveLocked(); err != nil {
		return replJobView{}, err
	}
	return a.viewLocked(a.jobs[spec.ID], a.now()), nil
}

// applySpecLocked stores spec as the job's settings, creating the job when
// job is nil. Caller holds a.mu.
func (a *replAgent) applySpecLocked(existing *replJob, spec replJobSpec) {
	if existing == nil {
		existing = &replJob{status: replJobStatus{State: replStateNeverRun, Message: "Waiting for its first round.", RescanNext: spec.RescanFirst}}
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
}

// applyPendingLocked applies a change saved during the round that just
// ended and starts the next round now with it, unless the job is paused.
// Caller holds a.mu.
func (a *replAgent) applyPendingLocked(job *replJob) {
	if job.pending == nil {
		return
	}
	spec := *job.pending
	job.pending = nil
	if job.status.State == replStateCutDone {
		return
	}
	a.applySpecLocked(job, spec)
	if job.status.State != replStatePaused {
		job.status.State, job.status.NextRun = replStateIdle, time.Time{}
		job.status.Message = "Changed. A new round starts with the new settings."
	} else {
		job.status.Message = "Changed. Resume to replicate with the new settings."
	}
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
		job.status.PauseAfterRound = false
		a.logLocked(job, "Paused by the operator.")
	case "pause-consistent":
		// Pause with the receiver's disks at one whole recovery point:
		// never stop a round half way.
		if done || job.status.State == replStateCutover {
			return errors.New("the job is " + job.status.State)
		}
		if job.status.State == replStatePaused && job.status.Consistent {
			return nil
		}
		if !job.running && job.status.Consistent {
			job.status.State, job.status.PauseAfterRound = replStatePaused, false
			job.status.Message = fmt.Sprintf("Paused at the recovery point of %s UTC. Resume to replicate again.", job.status.RecoveryPoint.Format("2006-01-02 15:04:05"))
			a.logLocked(job, "Paused at a whole recovery point.")
			break
		}
		job.status.PauseAfterRound = true
		if !job.running {
			if job.status.State == replStatePaused {
				job.status.State = replStateIdle
			}
			job.status.NextRun = time.Time{}
		}
		a.logLocked(job, "The job pauses when the next round finishes, so the receiver's disks hold one whole recovery point.")
	case "resume":
		if job.status.State != replStatePaused && !job.status.PauseAfterRound {
			return errors.New("the job is not paused")
		}
		job.status.PauseAfterRound = false
		if job.running {
			a.logLocked(job, "The pause after this round was cancelled.")
			break
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
	case "finish-by-hand":
		// The operator finished the cutover themselves (the source is shut
		// down and the new VM was started from the copies): the job stops
		// replicating and shows cut over. Nothing on this server changes.
		if done || job.status.State == replStateCutover {
			return errors.New("the job is " + job.status.State)
		}
		if job.status.Rounds == 0 || job.status.RecoveryPoint.IsZero() {
			return errors.New("the job has not finished a round yet, so there is no copy to cut over to")
		}
		if job.running {
			return errors.New("a round is running; pause the job first, then finish the cutover by hand")
		}
		job.cutover, job.spec.CutoverAt = false, nil
		job.status.State, job.status.CutoverAt, job.status.NextRun = replStateCutDone, a.now().UTC(), time.Time{}
		job.status.Message = "Cutover finished by hand: replication stopped at the recovery point of " + job.status.RecoveryPoint.UTC().Format("2006-01-02 15:04:05") + " UTC. Changes made here after it are not on the copy."
		for _, kind := range []string{"rpo", "cutover", "failing"} {
			a.clearAlertsLocked(job, kind)
		}
		a.logLocked(job, "The operator marked the cutover as finished by hand; replication stopped. Nothing on this server was changed.")
	case "rollback":
		// Roll back a finished cutover (rollback.go): the console has turned
		// the new VM off. Start the services the cutover stopped and pause
		// the job; the next round after Resume rescans the receiver's disks,
		// because the new VM wrote to them. Nothing else here changes.
		if !done {
			return errors.New("the job is not cut over, so there is nothing to roll back")
		}
		job.cutover, job.spec.CutoverAt = false, nil
		job.status.State, job.status.NextRun, job.status.RescanNext = replStatePaused, time.Time{}, true
		job.status.Message = "Rolled back: this server runs again and the job is paused. To migrate again, remove the new VM on the Hyper-V host, then Resume: the first round rescans the copies, so only what differs is sent."
		a.logLocked(job, "Rolled back by the operator after the cutover; the job is paused and its next round rescans the receiver's disks.")
		if services := append([]string(nil), job.spec.StopServices...); len(services) > 0 {
			log := a.jobLog(job)
			go func() {
				log("Starting " + strings.Join(services, ", ") + " again (rollback).")
				if err := a.platform.StartServices(services, log); err != nil {
					log("Some services did not start again: " + err.Error())
				}
			}()
		}
	case "cancel-cutover":
		if job.status.State == replStateCutover && job.stopCutover != nil {
			// A running cutover: stop the final round and roll back
			// (the stopped services start again), then carry on
			// replicating. The source has not been shut down: that only
			// happens after the final round is verified.
			job.cancelCutover = true
			job.cutover = false
			job.spec.CutoverAt = nil
			job.stopCutover()
			a.logLocked(job, "The cutover was cancelled by the operator; stopping the final round and rolling back.")
			break
		}
		if job.spec.CutoverAt == nil {
			return errors.New("no cutover is scheduled")
		}
		job.spec.CutoverAt = nil
		a.logLocked(job, "The scheduled cutover was cancelled.")
	case "delete":
		if job.status.State == replStateCutover {
			return errors.New("the job is cutting over; cancel the cutover first")
		}
		if job.running {
			if job.cancel != nil {
				job.cancel()
			}
			return errors.New("the job is stopping its round; delete it again in a moment")
		}
		delete(a.jobs, id)
		if forgetter, ok := a.platform.(interface{ Forget(job string) }); ok {
			forgetter.Forget(id)
		}
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
		if state != replStatePaused && state != replStateHeld && !job.status.RecoveryPoint.IsZero() {
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
	job.stopCutover, job.cancelCutover = nil, false
	if cutover {
		job.stopCutover = cancel
		job.status.State, job.status.Message = replStateCutover, "Cutting over: stopping services, then the last round."
	} else if job.status.State == replStateHeld {
		job.status.Message = "Checking whether the VM started from the copy has been removed."
	} else {
		job.status.State, job.status.Message = replStateSyncing, "Taking snapshots."
	}
	spec := job.spec
	spec.Disks = append([]replDiskMap(nil), job.spec.Disks...)
	spec.StopServices = append([]string(nil), job.spec.StopServices...)
	rescan := job.status.RescanNext
	// From here the receiver's disks may hold parts of two recovery points.
	job.status.Consistent = false
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				err := a.recordPanic(job, r)
				a.mu.Lock()
				job.running, job.cancel = false, nil
				job.status.Failures++
				job.status.LastError = err.Error()
				job.status.State = replStateError
				job.status.NextRun = a.now().Add(5 * time.Minute).UTC()
				job.status.Message = "The agent hit an internal error; it tries again at " + job.status.NextRun.Format("15:04") + " UTC."
				a.applyPendingLocked(job)
				a.saveLocked()
				a.mu.Unlock()
			}
		}()
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
	roundCtx, stopRound := context.WithCancel(ctx)
	defer stopRound()
	a.mu.Lock()
	job.generation++
	generation := job.generation
	job.status.LastProgress, job.status.Phase = a.now().UTC(), "opening"
	job.status.StallAfterSeconds = int64(replStallAfter / time.Second)
	job.status.LogFolder = filepath.Join(a.dir, "Logs")
	a.mu.Unlock()
	// live reports whether this round still owns the job's status (caller
	// holds a.mu): false once the watchdog abandoned it.
	live := func() bool { return job.generation == generation }
	var activityMu sync.Mutex
	lastActivity, phase := a.now(), "opening"
	activity := func(p string) {
		activityMu.Lock()
		now := a.now()
		changed := p != phase || now.Sub(lastActivity) >= 5*time.Second
		lastActivity, phase = now, p
		activityMu.Unlock()
		if changed {
			a.mu.Lock()
			if live() {
				job.status.LastProgress, job.status.Phase = now.UTC(), p
			}
			a.mu.Unlock()
		}
	}
	work := func() (err error) {
		// An internal error fails this round like any other failure (with
		// backoff) instead of ending the agent; the disks are closed first.
		defer func() {
			if r := recover(); r != nil {
				err = a.recordPanic(job, r)
			}
		}()
		if !final {
			if err := a.checkSpace(spec, sources); err != nil {
				return err
			}
		}
		spec.Receiver = a.reachReceiver(roundCtx, job, spec.Receiver, log)
		opened, err := a.platform.OpenDisks(roundCtx, spec.ID, sources, spec.AllowLive, log)
		if err != nil {
			return err
		}
		defer opened.Close()
		activity("copying")
		a.mu.Lock()
		if !live() {
			a.mu.Unlock()
			return errReplStalled
		}
		job.status.Notes = opened.Notes
		// The job row's progress bar starts each round at zero; the last
		// round's numbers would show it already done.
		for index := range job.status.Disks {
			d := &job.status.Disks[index]
			d.Done, d.Read, d.Sent, d.Changed, d.Error = 0, 0, 0, 0, ""
			if size := opened.Sources[d.Source]; size != nil && d.Chunks == 0 {
				d.Chunks = int((size.Size() + vmCopyChunkSize - 1) / vmCopyChunkSize)
			}
		}
		if job.status.State == replStateSyncing {
			job.status.Message = "Sending what changed."
		}
		a.mu.Unlock()
		var read, sent int64
		results := map[string]*vmCopyResult{}
		for index, disk := range spec.Disks {
			source := opened.Sources[disk.Source]
			if source == nil {
				return fmt.Errorf("disk %s was not opened", disk.Source)
			}
			sink, err := a.newSink(roundCtx, spec.Receiver, disk.Target)
			if err != nil {
				return fmt.Errorf("disk %s: %w", disk.Source, err)
			}
			sink.Configure(spec.Compress, job.throttle, rescan)
			if remote, ok := sink.(*vmRemoteSink); ok {
				remote.Owner = spec.ID
			}
			if rescan {
				log(fmt.Sprintf("Disk %s: rescanning %s on the receiver.", disk.Source, disk.Target))
			}
			ranges := opened.Ranges[disk.Source]
			switch {
			case rescan:
				ranges = nil // compare the whole receiver disk
			case final && spec.FullReadAtCutover:
				ranges = opened.Used[disk.Source]
			}
			round := spec.ID + "@" + opened.Snapshot.UTC().Format(time.RFC3339)
			machine := replMachineInfo()
			options := vmCopyOptions{Verify: final, Ranges: ranges, Round: round, Machine: &machine, Activity: activity, Progress: func(p vmCopyProgress) {
				a.mu.Lock()
				if live() && index < len(job.status.Disks) {
					d := &job.status.Disks[index]
					d.Chunks, d.Done, d.Read, d.Sent, d.Changed = p.Chunks, p.Done, p.BytesRead, p.BytesSent, p.Changed
				}
				a.mu.Unlock()
			}}
			result, err := vmCopyTo(roundCtx, source, sink, options)
			a.mu.Lock()
			if live() && index < len(job.status.Disks) {
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
			results[disk.Source] = result
			read += result.Progress.BytesRead
			sent += result.Progress.BytesSent
			log(fmt.Sprintf("Disk %s to %s: %s read, %s sent, %d chunks changed%s.", disk.Source, disk.Target, vmFormatBytes(result.Progress.BytesRead), vmFormatBytes(result.Progress.BytesSent), result.Progress.Changed, map[bool]string{true: ", verified", false: ""}[result.State.Verified]))
		}
		a.mu.Lock()
		owned := live()
		a.mu.Unlock()
		if !owned {
			return errReplStalled
		}
		if opened.Commit != nil {
			if err := opened.Commit(results); err != nil {
				log("Change tracking could not record this round, so the next round reads every disk: " + err.Error())
			}
		}
		a.mu.Lock()
		if !live() {
			a.mu.Unlock()
			return errReplStalled
		}
		job.status.RecoveryPoint = opened.Snapshot.UTC()
		job.status.LastRead, job.status.LastSent = read, sent
		job.status.TotalSent += sent
		a.mu.Unlock()
		return nil
	}
	err := a.watchRound(roundCtx, ctx, stopRound, job, spec, work, func() (time.Time, string) {
		activityMu.Lock()
		defer activityMu.Unlock()
		return lastActivity, phase
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	job.generation++ // an abandoned round can no longer write here
	job.lastErr = err
	job.status.LastProgress, job.status.Phase = time.Time{}, ""
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
		if job.status.InitialCopyAt.IsZero() {
			job.status.InitialCopyAt = a.now().UTC()
			a.noteLocked(job, "seeded", fmt.Sprintf("Initial copy of %s is complete: the copy is whole and ready for a test failover or cutover. Changes keep replicating.", job.spec.Name))
		}
		if rescan {
			job.status.RescanNext = false
		}
		a.clearAlertsLocked(job, "failing")
		a.clearAlertsLocked(job, "stopped")
		if !job.status.RecoveryPoint.IsZero() && a.now().Sub(job.status.RecoveryPoint) <= time.Duration(spec.RPOMinutes)*time.Minute {
			a.clearAlertsLocked(job, "rpo")
		}
		job.status.Consistent = true
		if !final && job.status.PauseAfterRound {
			job.status.State, job.status.PauseAfterRound = replStatePaused, false
			job.status.Message = fmt.Sprintf("Paused at the recovery point of %s UTC. Resume to replicate again.", job.status.RecoveryPoint.Format("2006-01-02 15:04:05"))
			a.logLocked(job, "Paused after the round, at a whole recovery point.")
		} else if !final {
			job.status.State = replStateIdle
			next := started.Add(interval)
			if spec.IntervalMinutes == 0 {
				next = a.now().Add(interval)
			}
			job.status.NextRun = next.UTC()
			job.status.Message = fmt.Sprintf("Up to date as of %s UTC. Next round at %s UTC.", job.status.RecoveryPoint.Format("15:04:05"), job.status.NextRun.Format("15:04"))
		}
		a.logLocked(job, "Round %d finished in %s; recovery point %s UTC.", job.status.Rounds, replAge(a.now().Sub(started)), job.status.RecoveryPoint.Format("2006-01-02 15:04:05"))
	case errors.Is(err, errVMReceiverHeld) && !final:
		// A VM was started from the copy on the target (a test failover):
		// not a failure. The source keeps changing and the next round after
		// the test ends sends everything that changed.
		job.status.State = replStateHeld
		job.status.NextRun = a.now().Add(2 * time.Minute).UTC()
		job.status.Message = "Paused while a VM runs from the copy on the target (a test failover). Rounds resume by themselves when the test ends."
		a.clearAlertsLocked(job, "rpo")
		if job.status.LastError != err.Error() {
			a.logLocked(job, "The receiver holds the copy for a VM started from it; rounds resume when it is released.")
		}
		job.status.LastError = err.Error()
	case pgHeld(err) && !final:
		// Waiting for space on the server: not a failure, and nothing was
		// snapshotted. It asks again in a few minutes.
		job.status.State = replStateIdle
		job.status.NextRun = a.now().Add(5 * time.Minute).UTC()
		job.status.Message = err.Error() + " Next check at " + job.status.NextRun.Format("15:04") + " UTC."
		if job.status.LastError != err.Error() {
			a.logLocked(job, "%s", err.Error())
		}
		job.status.LastError = err.Error()
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
			waiting, reason := replReceiverWait(err, spec.Receiver.Address)
			if errors.Is(err, errReplStalled) {
				waiting = true
				text := err.Error()
				if i := strings.Index(text, "; it was stopped"); i > 0 {
					text = text[:i]
				}
				reason = strings.ToUpper(text[:1]) + text[1:] + ", so it was stopped."
			}
			if waiting {
				// A receiver that is restarting (its computer or the
				// application) comes back on its own: try again often.
				backoff = []time.Duration{time.Minute, time.Minute, 2 * time.Minute, 5 * time.Minute}
			}
			wait := backoff[len(backoff)-1]
			if job.status.Failures <= len(backoff) {
				wait = backoff[job.status.Failures-1]
			}
			job.status.State = replStateError
			job.status.NextRun = a.now().Add(wait).UTC()
			if reason != "" {
				job.status.Message = fmt.Sprintf("%s It tries again at %s UTC and carries on from the last good round.", reason, job.status.NextRun.Format("15:04"))
			} else {
				job.status.Message = fmt.Sprintf("The round failed; it tries again at %s UTC.", job.status.NextRun.Format("15:04"))
			}
		}
		a.logLocked(job, "The round failed: %s", err.Error())
		if job.status.Failures >= replFailuresToAlert {
			a.alertLocked(job, "error", "failing", fmt.Sprintf("%s has failed %d rounds in a row: %s", spec.Name, job.status.Failures, err.Error()))
		}
	}
	a.applyPendingLocked(job)
	a.saveLocked()
	return err == nil
}

// watchRound runs a round's work and watches it. When nothing moves for
// replStallAfter (outside finishing, which has its own time limits) the
// round is stopped; when the work does not return within replStallGrace
// after a stop (a read or write that hangs in the disk or network), it is
// abandoned: its disks close when the call returns, and it never writes
// into the job's status again.
func (a *replAgent) watchRound(roundCtx, ctx context.Context, stopRound context.CancelFunc, job *replJob, spec replJobSpec, work func() error, last func() (time.Time, string)) error {
	done := make(chan error, 1)
	go func() { done <- work() }()
	ticker := time.NewTicker(replStallCheck)
	defer ticker.Stop()
	var stalled error
	var grace <-chan time.Time
	stopped := ctx.Done()
	for {
		select {
		case err := <-done:
			if stalled != nil && err != nil {
				return stalled
			}
			return err
		case <-stopped:
			stopped = nil
			if grace == nil {
				grace = time.After(replStallGrace)
			}
		case <-ticker.C:
			if stalled != nil {
				continue
			}
			at, phase := last()
			idle := a.now().Sub(at)
			if phase == "finishing" || idle < replStallAfter {
				continue
			}
			what := map[string]string{"opening": "taking the snapshots", "copying": "reading and sending the disks"}[phase]
			if what == "" {
				what = phase
			}
			stalled = fmt.Errorf("%w for %s while %s (receiver %s); it was stopped and tried again, carrying on from where it stopped", errReplStalled, replAge(idle), what, spec.Receiver.Address)
			a.mu.Lock()
			a.logLocked(job, "No progress for %s while %s. Stopping this round; the next one carries on from where it stopped.", replAge(idle), what)
			a.mu.Unlock()
			stopRound()
			grace = time.After(replStallGrace)
		case <-grace:
			a.mu.Lock()
			a.logLocked(job, "A disk read or a request to the receiver did not return %s after the round was stopped; the round was abandoned.", replAge(replStallGrace))
			a.mu.Unlock()
			if stalled != nil {
				return stalled
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return roundCtx.Err()
		}
	}
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
		job.running, job.cancel, job.stopCutover = false, nil, nil
		job.status.CutoverAttempt = 0
		if job.cancelCutover {
			job.cancelCutover = false
			job.status.State = replStateIdle
			job.status.Message = "The cutover was cancelled and rolled back; this server keeps running and replication carries on. Cut over again when ready."
			job.status.NextRun = a.now().Add(time.Minute).UTC()
			a.logLocked(job, "The cutover was cancelled and rolled back.")
		} else {
			job.status.State, job.status.Message = replStateError, "Cutover failed and was rolled back: "+text
			job.status.NextRun = a.now().Add(time.Minute).UTC()
			a.alertLocked(job, "error", "cutover", fmt.Sprintf("Cutover of %s failed and was rolled back: %s", spec.Name, text))
		}
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
	for attempt := 1; ; attempt++ {
		a.mu.Lock()
		job.status.CutoverAttempt = attempt
		job.status.Message = "Cutting over: sending the last changes and verifying every disk."
		if attempt > 1 {
			job.status.Message += fmt.Sprintf(" Attempt %d of %d.", attempt, replCutoverAttempts)
		}
		job.running = true
		a.mu.Unlock()
		if a.runRound(ctx, job, spec, rescan, true) {
			break
		}
		a.mu.Lock()
		err, text, cancelled := job.lastErr, job.status.LastError, job.cancelCutover
		// Between attempts the job still counts as running, so no
		// ordinary round starts in the gap.
		job.running = true
		a.mu.Unlock()
		if text == "" {
			text = "the last round was stopped"
		}
		waiting, _ := replReceiverWait(err, spec.Receiver.Address)
		retry := !cancelled && ctx.Err() == nil && attempt < replCutoverAttempts && (errors.Is(err, errReplStalled) || waiting)
		if !retry {
			fail(text)
			return
		}
		a.mu.Lock()
		job.status.Message = fmt.Sprintf("Cutting over: the final round did not finish (%s). Trying again in %s, attempt %d of %d; the services stay stopped and the copy carries on from where it stopped.", text, replAge(replCutoverRetry), attempt+1, replCutoverAttempts)
		a.logLocked(job, "The final round did not finish: %s. Trying again in %s (attempt %d of %d).", text, replAge(replCutoverRetry), attempt+1, replCutoverAttempts)
		a.saveLocked()
		a.mu.Unlock()
		if vmSleep(ctx, replCutoverRetry) != nil {
			fail("the cutover was stopped")
			return
		}
	}
	a.mu.Lock()
	job.cutover = false
	job.running, job.cancel, job.stopCutover = false, nil, nil
	job.status.CutoverAttempt = 0
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

// restartInterruptedServices starts the services a cutover stopped when
// the agent's previous run ended during that cutover (the rollback the
// cutover would have done).
func (a *replAgent) restartInterruptedServices() {
	a.mu.Lock()
	type pending struct {
		job      *replJob
		services []string
	}
	var list []pending
	for _, job := range a.jobs {
		if job.restartServices {
			job.restartServices = false
			list = append(list, pending{job, append([]string(nil), job.spec.StopServices...)})
		}
	}
	a.mu.Unlock()
	for _, item := range list {
		log := a.jobLog(item.job)
		log("The agent restarted during a cutover; starting " + strings.Join(item.services, ", ") + " again (rollback).")
		if err := a.platform.StartServices(item.services, log); err != nil {
			log("Some services did not start again: " + err.Error())
		}
	}
}

// Run calls Tick until ctx ends, then stops every round.
func (a *replAgent) Run(ctx context.Context) {
	ticker := time.NewTicker(replTickInterval)
	defer ticker.Stop()
	a.restartInterruptedServices()
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

// replReceiverWait explains a round that failed reaching the receiver.
// waiting is true when the receiver is most likely restarting or stopped
// (nothing answers), so the job retries often and needs nothing from the
// operator; a changed certificate or code needs the job's receiver details
// updated, which retrying cannot fix.
func replReceiverWait(err error, address string) (waiting bool, reason string) {
	if err == nil {
		return false, ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "thumbprint is"):
		return false, "The receiver at " + address + " now has a different certificate: it was started with a new code and certificate. Change the job and enter the receiver's new thumbprint and pairing code."
	case strings.Contains(text, "pairing code is wrong"):
		return false, "The receiver at " + address + " has a different pairing code now. Change the job and enter the receiver's new pairing code."
	case strings.Contains(text, "connectex"), strings.Contains(text, "connection refused"), strings.Contains(text, "actively refused"),
		strings.Contains(text, "no route to host"), strings.Contains(text, "i/o timeout"), strings.Contains(text, "deadline exceeded"),
		strings.Contains(text, "connection reset"), strings.Contains(text, "network is unreachable"), strings.Contains(text, "eof"),
		strings.Contains(text, "did not answer within"):
		return true, "Waiting for the receiver at " + address + ": it does not answer (it may be restarting, or the application on it was closed)."
	case strings.Contains(text, "still being verified"):
		return true, "Waiting for the receiver at " + address + ": it is still verifying this disk from the previous round."
	case strings.Contains(text, "is already being received"):
		return true, "Waiting for the receiver at " + address + ": it still has this disk open from an earlier round (it releases it two minutes after that round stops)."
	}
	return false, ""
}
