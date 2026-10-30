//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Keep scope choices outside the updater's existing settings file. Old versions
// can roll back without rewriting preferences they do not understand.
type collectionPreferences struct {
	Schema  int               `json:"schema"`
	Options collectionOptions `json:"options"`
}

const inventoryCategoryNames = "Hardware OperatingSystem Storage Networking DNS DHCP ActiveDirectory GroupPolicy FileServices SharesPermissions PrintServices IIS SQLServer HyperV FailoverClustering WindowsServerBackup Certificates ScheduledTasks Services InstalledSoftware WindowsFeaturesRoles Firewall RRAS NPS BitLocker SMB DFS WindowsUpdate EventLogs LocalAccounts Power TimeNTP Security RemoteManagement"
const exportCategoryNames = "DHCP DNS GroupPolicy IIS Firewall Certificates ScheduledTasks SharesPermissions PrintServices RRAS SQLServer Registry NPS"

func validateCollectionOptions(options *collectionOptions) error {
	options.OutputRoot = strings.TrimSpace(options.OutputRoot)
	if options.OutputRoot == "" || !filepath.IsAbs(options.OutputRoot) {
		return errors.New("Choose an absolute output folder, such as C:\\Reports or \\\\server\\share.")
	}
	options.OutputRoot = filepath.Clean(options.OutputRoot)
	if len(options.InventoryCategories) == 0 {
		return errors.New("Select at least one inventory category.")
	}
	validate := func(values []string, allowed string) error {
		seen := map[string]bool{}
		for _, value := range values {
			if !strings.Contains(" "+allowed+" ", " "+value+" ") || strings.ContainsAny(value, " \t\r\n") || value == "" || seen[value] {
				return errors.New("The collection scope contains an unknown or duplicate category.")
			}
			seen[value] = true
		}
		return nil
	}
	if err := validate(options.InventoryCategories, inventoryCategoryNames); err != nil {
		return err
	}
	if err := validate(options.ExportCategories, exportCategoryNames); err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, name := range options.InventoryCategories {
		selected[name] = true
	}
	for _, name := range options.ExportCategories {
		if name != "Registry" && !selected[name] {
			return errors.New("Select the matching inventory category for each configuration export.")
		}
		if name == "NPS" && !options.IncludeSensitive {
			return errors.New("NPS full export requires Allow sensitive exports. Enable it explicitly or deselect NPS full export.")
		}
	}
	if options.EventLogDays < 1 || options.EventLogDays > 365 {
		return errors.New("Event history must be a whole number from 1 to 365 days.")
	}
	return nil
}

func (a *application) preferencesPath() string {
	return filepath.Join(filepath.Dir(a.update.settingsPath), "collection.json")
}

func (a *application) loadCollectionPreferences() {
	file, err := os.Open(a.preferencesPath())
	if err != nil {
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return
	}
	var saved collectionPreferences
	if json.Unmarshal(data, &saved) != nil || saved.Schema != 1 || validateCollectionOptions(&saved.Options) != nil {
		return
	}
	// Consent is session-specific, including when someone edits the file by hand.
	saved.Options = safeSavedOptions(saved.Options)
	a.collectionPreferences = &saved
	a.defaultOutputRoot = saved.Options.OutputRoot
}

func safeSavedOptions(options collectionOptions) collectionOptions {
	options.IncludeSensitive = false
	options.InventoryCategories = append([]string{}, options.InventoryCategories...)
	exports := []string{}
	for _, value := range options.ExportCategories {
		if value != "NPS" {
			exports = append(exports, value)
		}
	}
	options.ExportCategories = exports
	return options
}

// Caller holds a.mu. Atomic replacement keeps the last complete choices if a
// write fails; no paths other than this owned preferences file are replaced.
func (a *application) saveCollectionPreferences(options collectionOptions) error {
	saved := collectionPreferences{Schema: 1, Options: safeSavedOptions(options)}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	path := a.preferencesPath()
	temp, err := os.CreateTemp(filepath.Dir(path), "collection-*.tmp")
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
	if err = atomicReplaceFile(name, path); err != nil {
		return err
	}
	a.collectionPreferences = &saved
	return nil
}

func (a *application) handlePreferences(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "POST is required."})
		return
	}
	var options collectionOptions
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
	if err := decoder.Decode(&options); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "The collection choices could not be read."})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": "Send one collection choices object within the size limit."})
		return
	}
	if err := validateCollectionOptions(&options); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Running || a.installing || a.closing {
		writeJSON(response, http.StatusConflict, map[string]any{"error": "Finish the current operation before saving choices."})
		return
	}
	if err := a.saveCollectionPreferences(options); err != nil {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"error": "Choices could not be saved. Check access to the application settings folder."})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}
