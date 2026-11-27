//go:build windows

package main

import (
	"encoding/json"
	"io"
	"net/http"
)

// handleMigrationProfiles serves /api/profiles: GET lists the saved defaults,
// POST {action:"save", profile} or {action:"delete", name} changes them.
func (a *application) handleMigrationProfiles(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "profiles": loadMigrationProfiles(a.update.settingsDir)})
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET or POST is required."})
		return
	}
	var input struct {
		Action  string           `json:"action"`
		Name    string           `json:"name"`
		Profile migrationProfile `json:"profile"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 128*1024))
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The defaults could not be read."})
		return
	}
	list := loadMigrationProfiles(a.update.settingsDir)
	switch input.Action {
	case "save":
		next, err := putMigrationProfile(list, input.Profile)
		if err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		list = next
	case "delete":
		next, found := deleteMigrationProfile(list, input.Name)
		if !found {
			writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "Those defaults no longer exist."})
			return
		}
		list = next
	default:
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Unknown action."})
		return
	}
	if err := saveMigrationProfiles(a.update.settingsDir, list); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The defaults could not be saved. Check access to the application settings folder."})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "profiles": list})
}
