//go:build windows

package main

import (
	"encoding/json"
	"io"
	"net/http"
)

// handleUISettings saves interface settings (how changing actions are
// confirmed). The current values are part of every state response.
func (a *application) handleUISettings(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST is required."})
		return
	}
	var settings uiSettings
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
	if err := decoder.Decode(&settings); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The settings could not be read."})
		return
	}
	if err := validateUISettings(&settings); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := saveUISettings(a.update.settingsDir, settings); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"ok": false, "error": "The settings could not be saved. Check access to the application settings folder."})
		return
	}
	a.uiSettings = settings
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "ui": settings})
}
