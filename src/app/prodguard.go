package main

// Production protection (2026-11-27, owner: "there should always be a way to
// safely migrate and control the migration to preserve functional space for
// the production environment and bandwidth for the production network").
//
// Space: a copy round of a running VM reads a snapshot (VMware) or a
// checkpoint (Hyper-V). Until the round ends, everything the VM writes lands
// in that snapshot's delta on the VM's own datastore or volume, next to the
// production VMs, and several rounds at once add up. A datastore that fills
// pauses every VM on it. So before any snapshot or checkpoint the guard
// checks each datastore or volume the VM uses: what is free, less what the
// rounds already running there are expected to grow by (their reservation),
// less what this round is expected to grow by, must stay above the reserve.
// Otherwise the round waits (it is held, never failed) and tries again
// later; holds are listed with the reason. While a round runs the guard
// re-reads free space and stops the round, which removes its snapshot, when
// free space falls to the stop level. The copy's destination volume is
// watched the same way.
//
// Bandwidth and load: one global limit (with business hours) applies to
// every agentless copy this computer reads, on top of each plan's and each
// wave's own limit; and the guard caps how many copies run at once in all,
// per source datastore, per source host and per destination host.
//
// Settings live in Settings\production.json (a separate file, so rollback
// versions never rewrite settings.json), are shown in Help > Settings and are
// the defaults for new migration plans, waves and replication jobs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	pgGiB = int64(1) << 30
	// pgWatchEvery is how often a running round's free space is read again.
	pgWatchEvery = 30 * time.Second
	// pgRetryAfter is how long a held round waits before it asks again.
	pgRetryAfter = 2 * time.Minute
	// pgMaxCopies is the most copies the settings allow at once.
	pgMaxCopies = 8
)

type pgSettings struct {
	Schema int `json:"schema"`
	// ReservePercent and ReserveGB: a round starts only when every datastore
	// or volume it uses keeps at least this share of its capacity and at
	// least this many GB free after the snapshots expected to grow there.
	ReservePercent int `json:"reservePercent"`
	ReserveGB      int `json:"reserveGB"`
	// StopPercent and StopGB: a running round is stopped (and its snapshot
	// removed) when free space falls below the larger of the two.
	StopPercent int `json:"stopPercent"`
	StopGB      int `json:"stopGB"`
	// GrowthPercent and GrowthMinGB estimate how much a snapshot grows during
	// one round: this share of the VM's disks on that datastore, at least
	// GrowthMinGB.
	GrowthPercent int `json:"growthPercent"`
	GrowthMinGB   int `json:"growthMinGB"`
	// MaxCopies caps the copies this computer runs at once; the other caps
	// limit them per source datastore, source host and destination host
	// (0 there means no cap of its own).
	MaxCopies        int `json:"maxCopies"`
	MaxPerDatastore  int `json:"maxPerDatastore"`
	MaxPerSourceHost int `json:"maxPerSourceHost"`
	MaxPerTarget     int `json:"maxPerTarget"`
	// Bandwidth is the global limit for everything this computer copies
	// agentlessly, and the starting limit of new plans and replication jobs.
	Bandwidth vmThrottleSchedule `json:"bandwidth"`
}

func defaultPGSettings() pgSettings {
	return pgSettings{Schema: 1, ReservePercent: 10, ReserveGB: 50, StopPercent: 5, StopGB: 20, GrowthPercent: 10, GrowthMinGB: 4,
		MaxCopies: 4, MaxPerDatastore: 2, MaxPerSourceHost: 2, MaxPerTarget: 2}
}

func validatePGSettings(s *pgSettings) error {
	s.Schema = 1
	switch {
	case s.ReservePercent < 0 || s.ReservePercent > 50:
		return errors.New("keep 0 to 50% of each datastore or volume free")
	case s.ReserveGB < 0 || s.ReserveGB > 100000:
		return errors.New("keep 0 to 100000 GB of each datastore or volume free")
	case s.StopPercent < 0 || s.StopPercent > 50 || s.StopGB < 0 || s.StopGB > 100000:
		return errors.New("stop a copy at 0 to 50% or 0 to 100000 GB free")
	case s.StopPercent > s.ReservePercent || s.StopGB > s.ReserveGB:
		return errors.New("the stop level must not be above the free space kept for production")
	case s.GrowthPercent < 0 || s.GrowthPercent > 100 || s.GrowthMinGB < 0 || s.GrowthMinGB > 10000:
		return errors.New("estimate snapshot growth as 0 to 100% of the VM's disks and 0 to 10000 GB at least")
	case s.MaxCopies < 1 || s.MaxCopies > pgMaxCopies:
		return fmt.Errorf("run 1 to %d copies at once", pgMaxCopies)
	case s.MaxPerDatastore < 0 || s.MaxPerDatastore > pgMaxCopies || s.MaxPerSourceHost < 0 || s.MaxPerSourceHost > pgMaxCopies || s.MaxPerTarget < 0 || s.MaxPerTarget > pgMaxCopies:
		return fmt.Errorf("copies per datastore, source host and destination host must be 0 (no cap) to %d", pgMaxCopies)
	}
	return s.Bandwidth.Validate()
}

func pgSettingsPath(settingsDir string) string { return filepath.Join(settingsDir, "production.json") }

// loadPGSettings reads production.json; a missing or damaged file gives the
// defaults, which already protect production.
func loadPGSettings(settingsDir string) pgSettings {
	file, err := os.Open(pgSettingsPath(settingsDir))
	if err != nil {
		return defaultPGSettings()
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil || len(data) > 16*1024 {
		return defaultPGSettings()
	}
	saved := defaultPGSettings()
	if json.Unmarshal(data, &saved) != nil || saved.Schema != 1 || validatePGSettings(&saved) != nil {
		return defaultPGSettings()
	}
	return saved
}

func savePGSettings(settingsDir string, s pgSettings) error {
	if err := validatePGSettings(&s); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(settingsDir, "production-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, pgSettingsPath(settingsDir))
}

// pgVolume is a datastore or volume a round uses. VMBytes is the size of the
// VM's disks there (what its snapshot can grow from); Target marks the
// copy's destination, which is watched but grows by the copy itself.
type pgVolume struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Free     int64  `json:"free"`
	Capacity int64  `json:"capacity"`
	VMBytes  int64  `json:"vmBytes,omitempty"`
	Target   bool   `json:"target,omitempty"`
}

// pgHoldError is a round that waits for space or a free slot. It is never a
// failure: callers try again later.
type pgHoldError struct {
	Reason string
	// Stopped is true when a running round was stopped to protect space.
	Stopped bool
}

func (e *pgHoldError) Error() string { return e.Reason }

func pgIsHold(err error) (*pgHoldError, bool) {
	var hold *pgHoldError
	if errors.As(err, &hold) {
		return hold, true
	}
	return nil, false
}

// Reserve is the free space kept on a volume of this capacity.
func (s pgSettings) Reserve(capacity int64) int64 {
	return pgLevel(capacity, s.ReservePercent, s.ReserveGB)
}

// StopLevel is the free space at which a running round is stopped.
func (s pgSettings) StopLevel(capacity int64) int64 {
	return pgLevel(capacity, s.StopPercent, s.StopGB)
}

func pgLevel(capacity int64, percent, gb int) int64 {
	level := capacity / 100 * int64(percent)
	if g := int64(gb) * pgGiB; g > level {
		level = g
	}
	// Never ask for more than the volume holds: a small volume would wait
	// forever. Keep half of it instead.
	if capacity > 0 && level > capacity/2 {
		level = capacity / 2
	}
	return level
}

// Growth estimates how much a snapshot of vmBytes of disks grows in a round.
func (s pgSettings) Growth(vmBytes int64) int64 {
	if vmBytes <= 0 {
		return 0
	}
	growth := vmBytes / 100 * int64(s.GrowthPercent)
	if min := int64(s.GrowthMinGB) * pgGiB; growth < min {
		growth = min
	}
	return growth
}

func pgGB(n int64) string { return vmFormatBytes(n) }

// pgSlotKeys say where a copy reads and writes, for the concurrency caps.
type pgSlotKeys struct {
	Datastores []string `json:"datastores,omitempty"`
	SourceHost string   `json:"sourceHost,omitempty"`
	Target     string   `json:"target,omitempty"`
}

type pgClaim struct {
	guard   *pgGuard
	id      int
	Owner   string
	Started time.Time
	Volumes []pgVolume
	// Needs is each volume's expected growth, by key.
	Needs   map[string]int64
	mu      sync.Mutex
	stopped string
}

type pgSlot struct {
	id    int
	owner string
	keys  pgSlotKeys
	since time.Time
}

// pgHold is a round waiting for space or a slot, for the status views.
type pgHold struct {
	Owner  string    `json:"owner"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
	Kind   string    `json:"kind"` // space, slot or stopped
}

// pgGuard is this computer's one production guard.
type pgGuard struct {
	mu       sync.Mutex
	settings pgSettings
	next     int
	claims   map[int]*pgClaim
	slots    map[int]*pgSlot
	holds    map[string]pgHold
	global   *vmThrottle
	now      func() time.Time
	every    time.Duration
}

func newPGGuard(settings pgSettings) *pgGuard {
	return &pgGuard{settings: settings, claims: map[int]*pgClaim{}, slots: map[int]*pgSlot{}, holds: map[string]pgHold{}, global: newVMThrottle(settings.Bandwidth), now: time.Now, every: pgWatchEvery}
}

func (g *pgGuard) Settings() pgSettings {
	if g == nil {
		return defaultPGSettings()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.settings
}

func (g *pgGuard) SetSettings(s pgSettings) {
	g.mu.Lock()
	g.settings = s
	g.mu.Unlock()
	g.global.SetSchedule(s.Bandwidth)
}

// Throttle is the global bandwidth limit every agentless round waits for.
func (g *pgGuard) Throttle() *vmThrottle {
	if g == nil {
		return nil
	}
	return g.global
}

func (g *pgGuard) holdLocked(owner, kind, reason string) {
	since := g.now()
	if old, ok := g.holds[owner]; ok && old.Kind == kind {
		since = old.Since
	}
	g.holds[owner] = pgHold{Owner: owner, Reason: reason, Since: since, Kind: kind}
}

// Clear forgets a hold, for example when its plan or VM is removed.
func (g *pgGuard) Clear(owner string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.holds, owner)
	g.mu.Unlock()
}

// Admit checks the volumes a new snapshot or checkpoint will use and, when
// production keeps its reserve on each, records the round's expected growth
// so later rounds count it. It returns a pgHoldError otherwise.
func (g *pgGuard) Admit(owner string, volumes []pgVolume) (*pgClaim, error) {
	if g == nil {
		return nil, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.settings
	needs := map[string]int64{}
	for _, v := range volumes {
		if v.Capacity <= 0 {
			continue
		}
		need := int64(0)
		if !v.Target {
			need = s.Growth(v.VMBytes)
		}
		others := int64(0)
		running := 0
		for _, c := range g.claims {
			if n, ok := c.Needs[v.Key]; ok {
				others += n
				running++
			}
		}
		reserve := s.Reserve(v.Capacity)
		left := v.Free - others - need
		if left < reserve {
			what := "datastore"
			if v.Target {
				what = "destination volume"
			}
			reason := fmt.Sprintf("Waiting for space on %s %s: %s of %s is free", what, v.Label, pgGB(v.Free), pgGB(v.Capacity))
			if running > 0 {
				reason += fmt.Sprintf(", %d running cop%s may still use %s", running, map[bool]string{true: "y", false: "ies"}[running == 1], pgGB(others))
			}
			if need > 0 {
				reason += fmt.Sprintf(", this VM's snapshot may grow by %s", pgGB(need))
			}
			reason += fmt.Sprintf(", and %s is kept free for production (Help > Settings). It tries again when space allows.", pgGB(reserve))
			g.holdLocked(owner, "space", reason)
			return nil, &pgHoldError{Reason: reason}
		}
		needs[v.Key] = need
	}
	g.next++
	claim := &pgClaim{guard: g, id: g.next, Owner: owner, Started: g.now(), Volumes: volumes, Needs: needs}
	g.claims[claim.id] = claim
	delete(g.holds, owner)
	return claim, nil
}

// Release ends the claim when the snapshot is gone.
func (c *pgClaim) Release() {
	if c == nil {
		return
	}
	c.guard.mu.Lock()
	delete(c.guard.claims, c.id)
	c.guard.mu.Unlock()
}

// Stopped is why the guard stopped the round, or "".
func (c *pgClaim) Stopped() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// Check reads the volumes again and returns why the round must stop, or "".
func (c *pgClaim) Check(volumes []pgVolume) string {
	if c == nil {
		return ""
	}
	s := c.guard.Settings()
	for _, v := range volumes {
		if v.Capacity <= 0 {
			continue
		}
		if stop := s.StopLevel(v.Capacity); v.Free < stop {
			what := "datastore"
			if v.Target {
				what = "destination volume"
			}
			return fmt.Sprintf("Stopped to protect production: %s %s fell to %s free, below the stop level of %s (Help > Settings). The snapshot was removed; the copy tries again when space allows and carries on where it stopped.", what, v.Label, pgGB(v.Free), pgGB(stop))
		}
	}
	return ""
}

// Watch re-reads free space while the round runs and cancels it at the stop
// level. It returns when ctx ends.
func (c *pgClaim) Watch(ctx context.Context, cancel context.CancelFunc, read func(context.Context) ([]pgVolume, error), log func(string)) {
	if c == nil || read == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(c.guard.every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			volumes, err := read(ctx)
			if err != nil {
				continue
			}
			if why := c.Check(volumes); why != "" {
				c.mu.Lock()
				c.stopped = why
				c.mu.Unlock()
				c.guard.mu.Lock()
				c.guard.holdLocked(c.Owner, "stopped", why)
				c.guard.mu.Unlock()
				if log != nil {
					log(why)
				}
				cancel()
				return
			}
		}
	}()
}

// Wrap turns the error of a round the guard stopped into a hold.
func (c *pgClaim) Wrap(err error) error {
	if why := c.Stopped(); why != "" && err != nil {
		return &pgHoldError{Reason: why, Stopped: true}
	}
	return err
}

// slotReasonLocked says which cap keeps keys from starting, or "".
func (g *pgGuard) slotReasonLocked(keys pgSlotKeys, extra []pgSlotKeys) string {
	s := g.settings
	all := make([]pgSlotKeys, 0, len(g.slots)+len(extra))
	for _, slot := range g.slots {
		all = append(all, slot.keys)
	}
	all = append(all, extra...)
	if len(all) >= s.MaxCopies {
		return fmt.Sprintf("Waiting for a free copy slot: %d copies are running and this computer runs at most %d at once (Help > Settings).", len(all), s.MaxCopies)
	}
	count := func(match func(pgSlotKeys) bool) int {
		n := 0
		for _, k := range all {
			if match(k) {
				n++
			}
		}
		return n
	}
	if s.MaxPerDatastore > 0 {
		for _, ds := range keys.Datastores {
			n := count(func(k pgSlotKeys) bool {
				for _, other := range k.Datastores {
					if strings.EqualFold(other, ds) {
						return true
					}
				}
				return false
			})
			if n >= s.MaxPerDatastore {
				return fmt.Sprintf("Waiting: %d copies already read datastore %s, the most allowed at once (Help > Settings).", n, pgKeyLabel(ds))
			}
		}
	}
	if s.MaxPerSourceHost > 0 && keys.SourceHost != "" {
		if n := count(func(k pgSlotKeys) bool { return strings.EqualFold(k.SourceHost, keys.SourceHost) }); n >= s.MaxPerSourceHost {
			return fmt.Sprintf("Waiting: %d copies already read from host %s, the most allowed at once (Help > Settings).", n, keys.SourceHost)
		}
	}
	if s.MaxPerTarget > 0 && keys.Target != "" {
		if n := count(func(k pgSlotKeys) bool { return strings.EqualFold(k.Target, keys.Target) }); n >= s.MaxPerTarget {
			return fmt.Sprintf("Waiting: %d copies already write to %s, the most allowed at once (Help > Settings).", n, keys.Target)
		}
	}
	return ""
}

// pgKeyLabel shows a datastore key ("datacenter\x00name") by its name.
func pgKeyLabel(key string) string {
	if i := strings.LastIndexByte(key, 0); i >= 0 {
		return key[i+1:]
	}
	return key
}

// TryAcquire takes a copy slot when the caps allow it.
func (g *pgGuard) TryAcquire(owner string, keys pgSlotKeys) (func(), string) {
	if g == nil {
		return func() {}, ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if why := g.slotReasonLocked(keys, nil); why != "" {
		g.holdLocked(owner, "slot", why)
		return nil, why
	}
	g.next++
	id := g.next
	g.slots[id] = &pgSlot{id: id, owner: owner, keys: keys, since: g.now()}
	if h, ok := g.holds[owner]; ok && h.Kind == "slot" {
		delete(g.holds, owner)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			delete(g.slots, id)
			g.mu.Unlock()
		})
	}, ""
}

// Acquire waits for a copy slot, logging why once, until ctx ends.
func (g *pgGuard) Acquire(ctx context.Context, owner string, keys pgSlotKeys, log func(string)) (func(), error) {
	said := ""
	for {
		release, why := g.TryAcquire(owner, keys)
		if why == "" {
			return release, nil
		}
		if why != said && log != nil {
			log(why)
			said = why
		}
		if err := vmSleep(ctx, 5*time.Second); err != nil {
			g.Clear(owner)
			return nil, err
		}
	}
}

// pgPlanner checks caps for several copies about to start together (a
// migration plan's sweep), counting the ones it already let through.
type pgPlanner struct {
	g     *pgGuard
	extra []pgSlotKeys
}

func (g *pgGuard) Planner() *pgPlanner { return &pgPlanner{g: g} }

// Try reports why keys cannot start now, or "" (and counts them).
func (p *pgPlanner) Try(keys pgSlotKeys) string {
	if p == nil || p.g == nil {
		return ""
	}
	p.g.mu.Lock()
	defer p.g.mu.Unlock()
	if why := p.g.slotReasonLocked(keys, p.extra); why != "" {
		return why
	}
	p.extra = append(p.extra, keys)
	return ""
}

// pgView is the guard's state for the page.
type pgView struct {
	Settings pgSettings     `json:"settings"`
	Defaults pgSettings     `json:"defaults"`
	Holds    []pgHold       `json:"holds"`
	Active   []pgActiveView `json:"active"`
	Running  int            `json:"running"`
	Limit    string         `json:"limit"`
}

type pgActiveView struct {
	Owner   string     `json:"owner"`
	Started time.Time  `json:"started"`
	Volumes []pgVolume `json:"volumes"`
	Growth  int64      `json:"growth"`
}

func (g *pgGuard) View() pgView {
	if g == nil {
		return pgView{Settings: defaultPGSettings(), Defaults: defaultPGSettings(), Holds: []pgHold{}, Active: []pgActiveView{}}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	v := pgView{Settings: g.settings, Defaults: defaultPGSettings(), Holds: []pgHold{}, Active: []pgActiveView{}, Running: len(g.slots), Limit: g.settings.Bandwidth.Describe()}
	for _, h := range g.holds {
		v.Holds = append(v.Holds, h)
	}
	sort.Slice(v.Holds, func(i, j int) bool { return v.Holds[i].Owner < v.Holds[j].Owner })
	for _, c := range g.claims {
		a := pgActiveView{Owner: c.Owner, Started: c.Started, Volumes: c.Volumes}
		for _, n := range c.Needs {
			a.Growth += n
		}
		v.Active = append(v.Active, a)
	}
	sort.Slice(v.Active, func(i, j int) bool { return v.Active[i].Owner < v.Active[j].Owner })
	return v
}

// pgSlotKeysFor names where a VM of an inventory is read from; target is the
// destination host ("" when unknown).
func pgSlotKeysFor(vm vmMachine, target string) pgSlotKeys {
	keys := pgSlotKeys{SourceHost: vm.Host, Target: target}
	seen := map[string]bool{}
	for _, disk := range vm.Disks {
		if disk.Datastore == "" {
			continue
		}
		key := vm.Datacenter + "\x00" + disk.Datastore
		if !seen[strings.ToLower(key)] {
			seen[strings.ToLower(key)] = true
			keys.Datastores = append(keys.Datastores, key)
		}
	}
	return keys
}

// pgFolderVolume describes the destination volume of a copy folder through
// free, which returns the volume's free and total bytes.
func pgFolderVolume(folder string, free func(string) (uint64, uint64, bool)) (pgVolume, bool) {
	if folder == "" || free == nil {
		return pgVolume{}, false
	}
	have, total, ok := free(folder)
	if !ok || total == 0 {
		return pgVolume{}, false
	}
	return pgVolume{Key: "target:" + strings.ToLower(pgVolumeRoot(folder)), Label: pgVolumeRoot(folder), Free: int64(have), Capacity: int64(total), Target: true}, true
}

// pgVolumeRoot is the drive, share or cluster volume a path lives on, for
// labels and to tell volumes apart.
func pgVolumeRoot(path string) string {
	clean := filepath.Clean(path)
	lower := strings.ToLower(clean)
	if strings.HasPrefix(clean, `\\`) {
		parts := strings.Split(strings.TrimPrefix(clean, `\\`), `\`)
		if len(parts) >= 2 {
			root := `\\` + parts[0] + `\` + parts[1]
			if len(parts) >= 4 && strings.EqualFold(parts[2], "ClusterStorage") {
				root += `\` + parts[2] + `\` + parts[3]
			}
			return root
		}
		return clean
	}
	if len(clean) >= 2 && clean[1] == ':' {
		if strings.HasPrefix(lower[2:], `\clusterstorage\`) {
			parts := strings.Split(clean, `\`)
			if len(parts) >= 3 {
				return strings.Join(parts[:3], `\`)
			}
		}
		return clean[:2] + `\`
	}
	return clean
}

// pgPathVolumes groups disk files by volume for a Hyper-V checkpoint: each
// volume's VMBytes is the size of the VM's disks on it. size returns a
// file's size, free a volume's free and total bytes.
func pgPathVolumes(paths []string, size func(string) int64, free func(string) (uint64, uint64, bool)) []pgVolume {
	byKey := map[string]*pgVolume{}
	order := []string{}
	for _, path := range paths {
		root := pgVolumeRoot(path)
		key := "volume:" + strings.ToLower(root)
		v := byKey[key]
		if v == nil {
			have, total, ok := free(filepath.Dir(path))
			if !ok {
				continue
			}
			v = &pgVolume{Key: key, Label: root, Free: int64(have), Capacity: int64(total)}
			byKey[key] = v
			order = append(order, key)
		}
		v.VMBytes += size(path)
	}
	out := []pgVolume{}
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	return out
}

// pgCheckFree is the check for one copy without a running claim (a
// replication agent before it takes its shadow copies): every volume must
// keep its reserve after the expected growth.
func pgCheckFree(s pgSettings, volumes []pgVolume) error {
	g := newPGGuard(s)
	_, err := g.Admit("check", volumes)
	return err
}

// pgHeld reports whether err is a round the guard held or stopped.
func pgHeld(err error) bool {
	_, ok := pgIsHold(err)
	return ok
}

// MbpsAt is the limit in force at now in Mbps; 0 means none.
func (s vmThrottleSchedule) MbpsAt(now time.Time) int {
	if s.Business(now) {
		return s.BusinessMbps
	}
	return s.LimitMbps
}

// migrateBandwidthMbps is the lower of a file share migration's own limit
// and the console's global limit at now (0: no limit).
func migrateBandwidthMbps(now time.Time, own, global vmThrottleSchedule) int {
	a, b := own.MbpsAt(now), global.MbpsAt(now)
	switch {
	case a == 0:
		return b
	case b == 0 || a < b:
		return a
	}
	return b
}
