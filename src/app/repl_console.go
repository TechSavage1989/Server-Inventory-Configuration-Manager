package main

// The central console: the Replication tab keeps a list of the agents it
// manages, polls each for its jobs and alerts, and passes job changes on.
// Agent keys are stored only encrypted for the signed-in Windows user
// (DPAPI), never in plain text, logs or state answers.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const replPollInterval = 10 * time.Second

type replAgentRecord struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Address    string    `json:"address"`
	Thumbprint string    `json:"thumbprint"`
	SealedKey  string    `json:"sealedKey"`
	OS         string    `json:"os"`
	Added      time.Time `json:"added"`
	Pushed     bool      `json:"pushed"`
	// SSH is how a Linux agent was installed (its SSH port and the host
	// key fingerprint trusted then), so it can be uninstalled the same way.
	SSHPort    int    `json:"sshPort,omitempty"`
	SSHHostKey string `json:"sshHostKey,omitempty"`
	key        string
}

type replAgentLive struct {
	Reachable bool          `json:"reachable"`
	Error     string        `json:"error,omitempty"`
	// Hint explains Error in plain words (see replAgentErrorHint).
	Hint string `json:"hint,omitempty"`
	// LastStop is the agent's own account of an unclean end of its
	// previous run (a crash, or ended from outside).
	LastStop   string    `json:"lastStop,omitempty"`
	LastStopAt time.Time `json:"lastStopAt,omitempty"`
	Checked   time.Time     `json:"checked"`
	Version   string        `json:"version,omitempty"`
	Computer  string        `json:"computer,omitempty"`
	Platform  string        `json:"platform,omitempty"`
	Jobs      []replJobView `json:"jobs"`
	Alerts    []replAlert   `json:"alerts"`
}

// replAgentRow is one agent as the console page sees it (no key).
type replAgentRow struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Address    string        `json:"address"`
	Thumbprint string        `json:"thumbprint"`
	OS         string        `json:"os"`
	Pushed     bool          `json:"pushed"`
	Live       replAgentLive `json:"live"`
}

type replSummary struct {
	Agents      int `json:"agents"`
	Unreachable int `json:"unreachable"`
	Jobs        int `json:"jobs"`
	Healthy     int `json:"healthy"`
	Behind      int `json:"behind"`
	Failing     int `json:"failing"`
	Paused      int `json:"paused"`
	CutOver     int `json:"cutOver"`
	Alerts      int `json:"alerts"`
}

type replConsole struct {
	mu        sync.Mutex
	path      string
	agents    []*replAgentRecord
	live      map[string]*replAgentLive
	seal      func([]byte) ([]byte, error)
	unseal    func([]byte) ([]byte, error)
	newClient func(record *replAgentRecord) (replAgentAPI, error)
	refreshMu sync.Mutex
}

// replAgentAPI is what the console calls on an agent (a fake in tests).
type replAgentAPI interface {
	Hello(ctx context.Context) (replAgentHello, error)
	Disks(ctx context.Context) ([]replDiskInfo, error)
	Jobs(ctx context.Context) (replAgentJobs, error)
	SaveJob(ctx context.Context, spec replJobSpec) (replJobView, error)
	Action(ctx context.Context, id, action string) error
	Schedule(ctx context.Context, id string, at *time.Time) error
	Log(ctx context.Context, id string) ([]string, error)
}

func newReplConsole(path string, seal, unseal func([]byte) ([]byte, error)) (*replConsole, error) {
	c := &replConsole{path: path, live: map[string]*replAgentLive{}, seal: seal, unseal: unseal}
	c.newClient = func(record *replAgentRecord) (replAgentAPI, error) {
		return newReplAgentClient(record.Address, record.Thumbprint, record.key)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	var saved struct {
		Agents []*replAgentRecord `json:"agents"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", path, err)
	}
	for _, record := range saved.Agents {
		sealed, err := base64.StdEncoding.DecodeString(record.SealedKey)
		if err == nil {
			var plain []byte
			if plain, err = unseal(sealed); err == nil {
				record.key = string(plain)
			}
		}
		if err != nil {
			// Another Windows user's key cannot be read; the agent is
			// listed so it can be added again with its key.
			c.live[record.ID] = &replAgentLive{Error: "The agent key could not be read for this Windows user; add the agent again with its key."}
		}
		c.agents = append(c.agents, record)
	}
	return c, nil
}

// Caller holds c.mu.
func (c *replConsole) saveLocked() error {
	saved := struct {
		Agents []*replAgentRecord `json:"agents"`
	}{c.agents}
	raw, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(c.path+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(c.path+".tmp", c.path)
}

// replAgentAddress checks host or host:port for an agent.
func replAgentAddress(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("enter the server's name or address")
	}
	host, port, err := net.SplitHostPort(text)
	if err != nil {
		host, port = strings.Trim(text, "[]"), fmt.Sprint(replAgentPort)
	}
	if host == "" || strings.ContainsAny(host, `/\@ "'`) {
		return "", errors.New("enter the server as a name or IP address, with :port if it is not " + fmt.Sprint(replAgentPort))
	}
	var number int
	if _, err := fmt.Sscan(port, &number); err != nil || number < 1 || number > 65535 {
		return "", errors.New("the port must be a number from 1 to 65535")
	}
	return net.JoinHostPort(host, fmt.Sprint(number)), nil
}

// Add checks an agent answers with this key and certificate, then keeps it.
// An agent already listed at the same address is replaced (a reinstall).
func (c *replConsole) Add(ctx context.Context, address, thumbprint, key string, pushed bool) (*replAgentRecord, error) {
	address, err := replAgentAddress(address)
	if err != nil {
		return nil, err
	}
	thumbprint = vmNormalizeThumbprint(thumbprint)
	if thumbprint == "" {
		return nil, errors.New("enter the agent's certificate thumbprint (64 characters)")
	}
	key = strings.TrimSpace(key)
	if len(key) < 20 {
		return nil, errors.New("enter the agent key the install printed")
	}
	record := &replAgentRecord{ID: replNewID(), Address: address, Thumbprint: thumbprint, key: key, Added: time.Now().UTC(), Pushed: pushed}
	client, err := c.newClient(record)
	if err != nil {
		return nil, err
	}
	hello, err := client.Hello(ctx)
	if err != nil {
		return nil, fmt.Errorf("the agent did not answer: %w", err)
	}
	record.Name, record.OS = hello.Computer, hello.Platform
	if record.Name == "" {
		record.Name = address
	}
	sealed, err := c.seal([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("the agent key could not be protected: %w", err)
	}
	record.SealedKey = base64.StdEncoding.EncodeToString(sealed)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, existing := range c.agents {
		if strings.EqualFold(existing.Address, address) {
			record.ID = existing.ID
			c.agents[i] = record
			c.live[record.ID] = &replAgentLive{Reachable: true, Checked: time.Now().UTC(), Version: hello.Version, Computer: hello.Computer, Platform: hello.Platform}
			return record, c.saveLocked()
		}
	}
	c.agents = append(c.agents, record)
	c.live[record.ID] = &replAgentLive{Reachable: true, Checked: time.Now().UTC(), Version: hello.Version, Computer: hello.Computer, Platform: hello.Platform}
	return record, c.saveLocked()
}

// Forget removes an agent from the console only.
func (c *replConsole) Forget(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, record := range c.agents {
		if record.ID == id {
			c.agents = append(c.agents[:i], c.agents[i+1:]...)
			delete(c.live, id)
			return c.saveLocked()
		}
	}
	return errors.New("that agent is not listed")
}

// SetSSH records how a Linux agent was installed.
func (c *replConsole) SetSSH(id string, port int, hostKey string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, record := range c.agents {
		if record.ID == id {
			record.SSHPort, record.SSHHostKey = port, hostKey
			return c.saveLocked()
		}
	}
	return errors.New("that agent is not listed")
}

func (c *replConsole) Record(id string) (replAgentRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, record := range c.agents {
		if record.ID == id {
			return *record, true
		}
	}
	return replAgentRecord{}, false
}

// Client connects to one listed agent.
func (c *replConsole) Client(id string) (replAgentAPI, error) {
	c.mu.Lock()
	var found *replAgentRecord
	for _, record := range c.agents {
		if record.ID == id {
			copy := *record
			found = &copy
		}
	}
	c.mu.Unlock()
	if found == nil {
		return nil, errors.New("that agent is not listed")
	}
	if found.key == "" {
		return nil, errors.New("the agent key could not be read; add the agent again with its key")
	}
	return c.newClient(found)
}

// Refresh polls every agent at once (at most eight at a time).
func (c *replConsole) Refresh(ctx context.Context) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	ids := []string{}
	for _, record := range c.agents {
		if record.key != "" {
			ids = append(ids, record.ID)
		}
	}
	c.mu.Unlock()
	limit := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			c.RefreshOne(ctx, id)
		}(id)
	}
	wg.Wait()
}

// RefreshOne polls one agent.
func (c *replConsole) RefreshOne(ctx context.Context, id string) {
	live := &replAgentLive{Checked: time.Now().UTC()}
	client, err := c.Client(id)
	if err == nil {
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		var hello replAgentHello
		if hello, err = client.Hello(callCtx); err == nil {
			var jobs replAgentJobs
			if jobs, err = client.Jobs(callCtx); err == nil {
				live.Reachable, live.Version, live.Computer, live.Platform = true, hello.Version, hello.Computer, hello.Platform
				live.LastStop, live.LastStopAt = hello.LastStop, hello.LastStopAt
				live.Jobs, live.Alerts = jobs.Jobs, jobs.Alerts
			}
		}
		cancel()
	}
	if err != nil {
		live.Error = err.Error()
		live.Hint = replAgentErrorHint(err)
		c.mu.Lock()
		if previous := c.live[id]; previous != nil {
			// Keep the last known jobs so the page still shows them.
			live.Jobs, live.Alerts, live.Version, live.Computer, live.Platform = previous.Jobs, previous.Alerts, previous.Version, previous.Computer, previous.Platform
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.live[id] = live
	c.mu.Unlock()
}

// Rows lists the agents with their last poll and a summary across them.
func (c *replConsole) Rows() ([]replAgentRow, replSummary) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := []replAgentRow{}
	summary := replSummary{}
	for _, record := range c.agents {
		row := replAgentRow{ID: record.ID, Name: record.Name, Address: record.Address, Thumbprint: record.Thumbprint, OS: record.OS, Pushed: record.Pushed}
		if live := c.live[record.ID]; live != nil {
			row.Live = *live
		}
		if row.Live.Jobs == nil {
			row.Live.Jobs = []replJobView{}
		}
		if row.Live.Alerts == nil {
			row.Live.Alerts = []replAlert{}
		}
		summary.Agents++
		if !row.Live.Reachable {
			summary.Unreachable++
		}
		for _, job := range row.Live.Jobs {
			summary.Jobs++
			switch {
			case job.Status.State == replStateCutDone:
				summary.CutOver++
			case job.Status.State == replStatePaused || job.Status.State == replStateHeld:
				summary.Paused++
			case job.Status.State == replStateError:
				summary.Failing++
			case job.Status.RecoveryPoint.IsZero() || job.RPOSecond > int64(job.Spec.RPOMinutes)*60:
				summary.Behind++
			default:
				summary.Healthy++
			}
		}
		for _, alert := range row.Live.Alerts {
			if !alert.Cleared {
				summary.Alerts++
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name) })
	return rows, summary
}

// replAgentErrorHint says what an agent that does not answer most likely
// means, for the console.
func replAgentErrorHint(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "actively refused") || strings.Contains(text, "connection refused"):
		return "The server answers, but nothing is listening on the agent's port, so the agent service is not running. " +
			"If it stopped during a round, security software may have ended it: look in SentinelOne, Microsoft Defender or CrowdStrike for a detection or quarantine of SIMM-Agent.exe, release it and add the exclusions in the endpoint protection guide. " +
			"Then start the service on the server (Start-Service SIMMReplicationAgent), or run Install agents for it again. Its jobs carry on where they stopped."
	case strings.Contains(text, "timeout") || strings.Contains(text, "deadline exceeded") || strings.Contains(text, "did not properly respond"):
		return "The server did not answer in time: it may be off, unreachable from this computer, or a firewall drops the agent's port."
	case strings.Contains(text, "no such host"):
		return "The server's name could not be found in DNS from this computer."
	case strings.Contains(text, "certificate") || strings.Contains(text, "thumbprint"):
		return "Something answers on the agent's port with a different certificate. If the agent was reinstalled, remove the server from the list and add it again with its new thumbprint."
	}
	return ""
}
