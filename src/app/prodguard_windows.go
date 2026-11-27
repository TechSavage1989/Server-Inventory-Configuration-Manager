//go:build windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
)

// protectMove applies the production guard to one copy round: it waits for
// a copy slot (the caps per computer, source datastore, source host and
// destination), adds the global bandwidth limit to the round's own and asks
// the round to check free space before its snapshot or checkpoint. The
// cutover takes no slot and is never held, because its source is already
// shut down or about to be.
func (a *application) protectMove(ctx context.Context, vm vmMachine, request *vmMoveRequest, target string, log func(string)) (func(), error) {
	guard := a.prodGuard
	if guard == nil {
		return func() {}, nil
	}
	request.Throttle = vmThrottleAll(request.Throttle, guard.Throttle())
	if request.Cutover {
		return func() {}, nil
	}
	owner := vm.Name
	request.Space, request.SpaceOwner = guard, owner
	if request.NewSink == nil && request.CopyFolder != "" {
		folder := request.CopyFolder
		request.TargetVolume = func() (pgVolume, bool) { return pgFolderVolume(folder, vmFreeSpace) }
	}
	release, err := guard.Acquire(ctx, owner, pgSlotKeysFor(vm, target), log)
	if err != nil {
		return nil, err
	}
	return release, nil
}

// pgLocalPathVolumes groups a Hyper-V VM's disk files (paths this computer
// opens, an administrative share for a remote host) by volume.
func pgLocalPathVolumes(paths []string) []pgVolume {
	return pgPathVolumes(paths, func(path string) int64 {
		info, err := os.Stat(path)
		if err != nil {
			return 0
		}
		return info.Size()
	}, vmFreeSpace)
}

// handleProduction shows (GET) or saves (POST) the production protection
// settings: free space kept, the stop level, snapshot growth, copy caps and
// the global bandwidth limit.
func (a *application) handleProduction(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "production": a.prodGuard.View()})
		return
	case http.MethodPost:
	default:
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET or POST is required."})
		return
	}
	settings := defaultPGSettings()
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
	if err := decoder.Decode(&settings); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The settings could not be read."})
		return
	}
	if err := validatePGSettings(&settings); err != nil {
		message := err.Error()
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": strings.ToUpper(message[:1]) + message[1:] + "."})
		return
	}
	if err := savePGSettings(a.update.settingsDir, settings); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The settings could not be saved. Check access to the application settings folder."})
		return
	}
	a.prodGuard.SetSettings(settings)
	a.vmwareLog("Production protection settings saved: keep %d%% and %d GB free, stop copies at %d%% or %d GB, at most %d copies at once, bandwidth %s.", settings.ReservePercent, settings.ReserveGB, settings.StopPercent, settings.StopGB, settings.MaxCopies, settings.Bandwidth.Describe())
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "production": a.prodGuard.View()})
}

// pgMountFree reads a mounted volume's free and total bytes.
func pgMountFree(mount string) (uint64, uint64, bool) { return vmFreeSpace(mount) }
