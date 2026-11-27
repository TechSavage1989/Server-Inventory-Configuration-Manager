//go:build windows

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var vcRememberInit sync.Mutex

// vcRememberStore is this application's remembered vCenter sign-ins.
func (a *application) vcRememberStore() *vcRemember {
	vcRememberInit.Lock()
	defer vcRememberInit.Unlock()
	if a.vcRemember == nil {
		a.vcRemember = newVCRemember(filepath.Join(a.update.settingsDir, "vcenter-signins.json"), replSeal, replUnseal)
	}
	return a.vcRemember
}

// handleVCRemember shows, turns on or off, and forgets remembered sign-ins.
// It never returns a password.
func (a *application) handleVCRemember(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	store := a.vcRememberStore()
	switch request.Method {
	case http.MethodGet:
	case http.MethodPost:
		var input struct {
			Enabled *bool  `json:"enabled"`
			Forget  string `json:"forget"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
		if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The request was invalid."})
			return
		}
		var err error
		switch {
		case input.Enabled != nil:
			err = store.SetEnabled(*input.Enabled)
		case strings.TrimSpace(input.Forget) != "":
			err = store.Forget(input.Forget, "")
		default:
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Nothing to change."})
			return
		}
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The remembered sign-ins could not be saved. Check access to the application settings folder."})
			return
		}
	default:
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET or POST is required."})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "remember": store.View()})
}

// vcAutoSignInEvery is how often a plan waiting for a remembered sign-in is
// signed in again when the last try could not reach the server.
const vcAutoSignInEvery = 5 * time.Minute

// runVCAutoSignIn signs migration plans in again with a remembered sign-in
// after the application starts, and retries while the server cannot be
// reached. It never replaces a session or an inventory the operator opened.
func (a *application) runVCAutoSignIn() {
	tried := map[string]time.Time{}
	time.Sleep(5 * time.Second)
	for {
		a.mu.Lock()
		closing := a.closing || a.installing
		a.mu.Unlock()
		if closing {
			return
		}
		a.tryVCAutoSignIn(tried, time.Now())
		time.Sleep(20 * time.Second)
	}
}

func (a *application) tryVCAutoSignIn(tried map[string]time.Time, now time.Time) {
	store := a.vcRememberStore()
	if !store.Enabled() {
		return
	}
	a.mu.Lock()
	runner := a.plans.runner
	busy := a.vmware.action != "" || a.vmware.password != ""
	open := ""
	if a.vmware.inventory != nil {
		open = a.vmware.inventory.Server
	}
	a.mu.Unlock()
	if runner == nil || busy {
		return
	}
	for _, server := range runner.WaitingServers() {
		if open != "" && !strings.EqualFold(open, server) {
			return
		}
		if !store.Has(server) || now.Sub(tried[strings.ToLower(server)]) < vcAutoSignInEvery {
			continue
		}
		user, password, thumbprint, ok := store.Get(server)
		if !ok {
			store.Forget(server, "The remembered sign-in for "+server+" could not be read on this computer, so it was forgotten. Sign in again.")
			continue
		}
		endpoint, err := vmServerURL(server)
		if err != nil {
			continue
		}
		tried[strings.ToLower(server)] = now
		if status, _ := a.startVMwareConnect(endpoint, user, password, thumbprint, true); status == 0 {
			a.vmwareLog("Signing in to %s with the remembered sign-in, so migration plans can carry on.", server)
		}
		return
	}
}

// vcRememberHint adds what a remembered sign-in is doing to a plan's
// sign-in reason.
func (a *application) vcRememberHint(server string) string {
	store := a.vcRememberStore()
	if notice := store.Notice(server); notice != "" {
		return " " + notice
	}
	if store.Has(server) {
		return " The remembered sign-in is used as soon as " + server + " answers."
	}
	return ""
}
