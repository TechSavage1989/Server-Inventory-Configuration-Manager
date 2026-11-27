package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Remembered vCenter and ESXi sign-ins for migration plans (2026-11-27,
// owner's opt-in decision). Off by default. When the operator turns it on,
// each sign-in that reads vCenter successfully is kept sealed for the current
// Windows user (DPAPI on Windows), so plans sign in again by themselves after
// the application restarts or updates. The password never reaches a log, a
// plan file, the state response or the page; turning it off or Forget removes
// it, and a password the server refuses is forgotten at once.

// vcRememberEntry is one remembered sign-in. Sealed holds the sealed
// vcRememberSecret; the server, user and thumbprint are not secret.
type vcRememberEntry struct {
	Server     string    `json:"server"`
	User       string    `json:"user"`
	Thumbprint string    `json:"thumbprint,omitempty"`
	Saved      time.Time `json:"saved"`
	Sealed     []byte    `json:"sealed"`
}

// vcRememberSecret is what is sealed: the server and user travel inside so a
// sealed blob moved to another entry is refused.
type vcRememberSecret struct {
	Server   string `json:"s"`
	User     string `json:"u"`
	Password string `json:"p"`
}

type vcRememberFile struct {
	Enabled bool              `json:"enabled"`
	Entries []vcRememberEntry `json:"entries,omitempty"`
}

// vcRememberView is what the page sees: never the password.
type vcRememberView struct {
	Enabled bool                 `json:"enabled"`
	Entries []vcRememberListItem `json:"entries"`
	Notice  string               `json:"notice,omitempty"`
}

type vcRememberListItem struct {
	Server string    `json:"server"`
	User   string    `json:"user"`
	Saved  time.Time `json:"saved"`
}

type vcRemember struct {
	mu     sync.Mutex
	path   string
	seal   func([]byte) ([]byte, error)
	unseal func([]byte) ([]byte, error)
	data   vcRememberFile
	// notices says, per server, why a remembered sign-in was forgotten.
	notices map[string]string
	loadErr string
}

func newVCRemember(path string, seal, unseal func([]byte) ([]byte, error)) *vcRemember {
	r := &vcRemember{path: path, seal: seal, unseal: unseal, notices: map[string]string{}}
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, &r.data); err != nil {
			r.data = vcRememberFile{}
			r.loadErr = "The remembered sign-ins could not be read and were ignored."
		}
	}
	return r
}

func vcRememberKey(server string) string { return strings.ToLower(strings.TrimSpace(server)) }

func (r *vcRemember) saveLocked() error {
	if !r.data.Enabled {
		r.data.Entries = nil
	}
	raw, err := json.MarshalIndent(r.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Enabled says whether sign-ins are remembered.
func (r *vcRemember) Enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Enabled
}

// SetEnabled turns remembering on or off; off forgets every sign-in.
func (r *vcRemember) SetEnabled(on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data.Enabled = on
	if !on {
		r.data.Entries = nil
		r.notices = map[string]string{}
	}
	return r.saveLocked()
}

// Remember keeps a sign-in that just worked, when remembering is on.
func (r *vcRemember) Remember(server, user, password, thumbprint string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.data.Enabled {
		return nil
	}
	if strings.TrimSpace(server) == "" || strings.TrimSpace(user) == "" || password == "" {
		return errors.New("nothing to remember")
	}
	plain, err := json.Marshal(vcRememberSecret{Server: vcRememberKey(server), User: user, Password: password})
	if err != nil {
		return err
	}
	sealed, err := r.seal(plain)
	for i := range plain {
		plain[i] = 0
	}
	if err != nil {
		return err
	}
	entry := vcRememberEntry{Server: server, User: user, Thumbprint: thumbprint, Saved: now.UTC(), Sealed: sealed}
	kept := []vcRememberEntry{}
	for _, e := range r.data.Entries {
		if vcRememberKey(e.Server) != vcRememberKey(server) {
			kept = append(kept, e)
		}
	}
	r.data.Entries = append(kept, entry)
	delete(r.notices, vcRememberKey(server))
	return r.saveLocked()
}

// Get returns the remembered sign-in of a server.
func (r *vcRemember) Get(server string) (user, password, thumbprint string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.data.Enabled {
		return "", "", "", false
	}
	for _, e := range r.data.Entries {
		if vcRememberKey(e.Server) != vcRememberKey(server) {
			continue
		}
		plain, err := r.unseal(e.Sealed)
		if err != nil {
			return "", "", "", false
		}
		var secret vcRememberSecret
		err = json.Unmarshal(plain, &secret)
		for i := range plain {
			plain[i] = 0
		}
		if err != nil || secret.Server != vcRememberKey(e.Server) || secret.User != e.User || secret.Password == "" {
			return "", "", "", false
		}
		return e.User, secret.Password, e.Thumbprint, true
	}
	return "", "", "", false
}

// Has says whether a sign-in is remembered for the server.
func (r *vcRemember) Has(server string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.data.Enabled {
		return false
	}
	for _, e := range r.data.Entries {
		if vcRememberKey(e.Server) == vcRememberKey(server) {
			return true
		}
	}
	return false
}

// Servers lists the servers with a remembered sign-in.
func (r *vcRemember) Servers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := []string{}
	if !r.data.Enabled {
		return list
	}
	for _, e := range r.data.Entries {
		list = append(list, e.Server)
	}
	return list
}

// Forget removes a server's remembered sign-in, with a reason to show when
// it was refused rather than removed by the operator.
func (r *vcRemember) Forget(server, notice string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := []vcRememberEntry{}
	for _, e := range r.data.Entries {
		if vcRememberKey(e.Server) != vcRememberKey(server) {
			kept = append(kept, e)
		}
	}
	r.data.Entries = kept
	if notice != "" {
		r.notices[vcRememberKey(server)] = notice
	} else {
		delete(r.notices, vcRememberKey(server))
	}
	return r.saveLocked()
}

// Notice says why a server's remembered sign-in was forgotten, or "".
func (r *vcRemember) Notice(server string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.notices[vcRememberKey(server)]
}

// View is what the page shows.
func (r *vcRemember) View() vcRememberView {
	r.mu.Lock()
	defer r.mu.Unlock()
	view := vcRememberView{Enabled: r.data.Enabled, Entries: []vcRememberListItem{}, Notice: r.loadErr}
	if r.data.Enabled {
		for _, e := range r.data.Entries {
			view.Entries = append(view.Entries, vcRememberListItem{Server: e.Server, User: e.User, Saved: e.Saved})
		}
	}
	notes := []string{}
	for _, n := range r.notices {
		notes = append(notes, n)
	}
	sort.Strings(notes)
	if len(notes) > 0 {
		if view.Notice != "" {
			view.Notice += " "
		}
		view.Notice += strings.Join(notes, " ")
	}
	return view
}

// vcRememberRefused says whether a connect error means the server refused
// the user name or password (and not that it could not be reached).
func vcRememberRefused(message string) bool {
	return strings.Contains(message, "refused the user name or password")
}
