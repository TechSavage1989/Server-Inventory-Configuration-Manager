package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Confirmation styles (2026-11-26, owner: "instead of having to type system
// names and plan names for confirmations, just have a check tick box or
// something or possibly a prompt to verify that can be configured"). The page
// asks for confirmation in the chosen style before any changing action; the
// request still carries the name it confirms and the server still checks it,
// so a stale or mistaken request never reaches the wrong VM, plan or server.
const (
	confirmNone   = "none"   // the action runs when its button is pressed
	confirmTick   = "tick"   // an "I understand" tick box beside the action (default)
	confirmPrompt = "prompt" // a dialog asks before the action runs
	confirmTyped  = "typed"  // type the name or number, as in earlier versions
)

var confirmStyles = []string{confirmNone, confirmTick, confirmPrompt, confirmTyped}

// uiSettings is Settings\interface.json. It is a separate file so rollback
// versions never rewrite settings.json or collection.json with fields they do
// not know.
type uiSettings struct {
	Schema       int    `json:"schema"`
	ConfirmStyle string `json:"confirmStyle"`
}

func defaultUISettings() uiSettings { return uiSettings{Schema: 1, ConfirmStyle: confirmTick} }

func validConfirmStyle(style string) bool {
	for _, s := range confirmStyles {
		if s == style {
			return true
		}
	}
	return false
}

func validateUISettings(settings *uiSettings) error {
	settings.ConfirmStyle = strings.ToLower(strings.TrimSpace(settings.ConfirmStyle))
	if settings.ConfirmStyle == "" {
		settings.ConfirmStyle = confirmTick
	}
	if !validConfirmStyle(settings.ConfirmStyle) {
		return errors.New("Choose how changes are confirmed: none, tick, prompt or typed.")
	}
	settings.Schema = 1
	return nil
}

func uiSettingsPath(settingsDir string) string { return filepath.Join(settingsDir, "interface.json") }

// loadUISettings reads interface.json; a missing, damaged or unknown file
// gives the default (tick box).
func loadUISettings(settingsDir string) uiSettings {
	file, err := os.Open(uiSettingsPath(settingsDir))
	if err != nil {
		return defaultUISettings()
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil || len(data) > 16*1024 {
		return defaultUISettings()
	}
	var saved uiSettings
	if json.Unmarshal(data, &saved) != nil || saved.Schema != 1 || validateUISettings(&saved) != nil {
		return defaultUISettings()
	}
	return saved
}

// saveUISettings replaces interface.json atomically.
func saveUISettings(settingsDir string, settings uiSettings) error {
	if err := validateUISettings(&settings); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(settingsDir, "interface-*.tmp")
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
	return os.Rename(name, uiSettingsPath(settingsDir))
}
