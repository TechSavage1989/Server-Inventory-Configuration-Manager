//go:build windows

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// This describes one bounded recursive ACL export, not overall collection percent.
type aclExportState struct {
	RunID          string `json:"runId"`
	State          string `json:"state"`
	ShareIndex     int    `json:"shareIndex"`
	ShareCount     int    `json:"shareCount"`
	ShareName      string `json:"shareName"`
	ElapsedSeconds int    `json:"elapsedSeconds"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	SkipRequested  bool   `json:"skipRequested"`
	Incomplete     bool   `json:"incomplete"`
}

// Caller holds a.mu. Only the current collector can update its local UI state.
func (a *application) parseACLExportLineLocked(line string) bool {
	const prefix = "@@ACL_EXPORT@@"
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	if len(line) > 8192 || !a.state.Running {
		return true
	}
	var event aclExportState
	if json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &event) != nil || event.RunID != a.state.RunID || event.RunID == "" {
		return true
	}
	switch event.State {
	case "Running", "Completed", "TimedOut", "Failed", "Skipped", "Idle":
	default:
		return true
	}
	if event.ShareIndex < 0 || event.ShareCount < 0 || event.ShareIndex > event.ShareCount || event.ElapsedSeconds < 0 || event.TimeoutSeconds < 30 || event.TimeoutSeconds > 3600 || len(event.ShareName) > 1024 {
		return true
	}
	// These flags belong to the host and cannot be cleared by a late heartbeat.
	event.SkipRequested = a.state.ACLExport.SkipRequested
	event.Incomplete = a.state.ACLExport.Incomplete || event.State == "TimedOut" || event.State == "Failed" || event.State == "Skipped"
	a.state.ACLExport = event
	return true
}

func (a *application) handleSkipACLExport(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "POST is required."})
		return
	}
	var body struct {
		ExpectedRunID string `json:"expectedRunId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096))
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "Send the current run identifier."})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.state.Running || a.closing || a.installing || a.state.CancelRequested || body.ExpectedRunID == "" || body.ExpectedRunID != a.state.RunID || a.state.ACLExport.RunID != a.state.RunID || a.state.ACLExport.State != "Running" {
		writeJSON(response, http.StatusConflict, map[string]any{"error": "That recursive ACL export is no longer active. Refresh its status."})
		return
	}
	if a.state.ACLExport.SkipRequested {
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
		return
	}
	// Never accept a path from the request. The run owns this local Runtime directory.
	if a.temporaryDirectory == "" || a.aclControlPath == "" || !strings.EqualFold(filepath.Clean(a.aclControlPath), filepath.Join(a.temporaryDirectory, "ACL-Control.json")) {
		writeJSON(response, http.StatusConflict, map[string]any{"error": "The active export control channel is unavailable."})
		return
	}
	data, _ := json.Marshal(struct {
		RunID         string `json:"RunId"`
		SkipRemaining bool   `json:"SkipRemaining"`
	}{a.state.RunID, true})
	if err := writeACLControl(a.aclControlPath, data); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"error": "Could not request the skip. The export is still running; you can cancel the collection."})
		return
	}
	a.state.ACLExport.SkipRequested = true
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func writeACLControl(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "acl-control-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return atomicReplaceFile(name, path)
}
