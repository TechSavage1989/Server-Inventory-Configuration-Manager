package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Saved defaults per customer (2026-11-27, owner idea 11: "saved defaults
// per customer (folders, switch/VLAN map, sync interval, bandwidth as a named
// profile)"). A profile only fills in New migration and Migration plans; it
// never starts or changes anything by itself, and it holds no account,
// password, key or pairing code. Profiles live in Settings\migration-profiles.json,
// a separate file so rollback versions never rewrite settings.json.

const (
	migrationProfilesMax  = 50
	migrationProfileName  = 64
	migrationProfileMaps  = 200
	migrationProfilesSize = 512 * 1024
)

type migrationProfileOptions struct {
	KeepMAC          bool `json:"keepMac"`
	StartVMs         bool `json:"startVms"`
	Cluster          bool `json:"cluster"`
	Quiesce          bool `json:"quiesce"`
	EjectMedia       bool `json:"ejectMedia"`
	DisconnectSource bool `json:"disconnectSource"`
	FixUp            bool `json:"fixUp"`
}

type migrationProfile struct {
	Name        string                  `json:"name"`
	Notes       string                  `json:"notes,omitempty"`
	CopyRoot    string                  `json:"copyRoot,omitempty"`
	VMFolder    string                  `json:"vmFolder,omitempty"`
	TestSwitch  string                  `json:"testSwitch,omitempty"`
	SyncMinutes int                     `json:"syncMinutes"`
	Concurrency int                     `json:"concurrency"`
	Throttle    vmThrottleSchedule      `json:"throttle"`
	Options     migrationProfileOptions `json:"options"`
	NetworkMap  []vmNetworkMap          `json:"networkMap,omitempty"`
}

type migrationProfiles struct {
	Schema   int                `json:"schema"`
	Profiles []migrationProfile `json:"profiles"`
}

func migrationProfilesPath(settingsDir string) string {
	return filepath.Join(settingsDir, "migration-profiles.json")
}

func cleanProfileText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value))
	for utf8.RuneCountInString(value) > limit {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

// validateMigrationProfile cleans a profile in place: limits are the same as
// a migration plan's (sync 15 to 1440 minutes, 1 to 4 at once), and folders
// must be full drive or UNC paths without "..".
func validateMigrationProfile(p *migrationProfile) error {
	p.Name = cleanProfileText(p.Name, migrationProfileName)
	if p.Name == "" {
		return errors.New("Name the defaults, for example the customer's name.")
	}
	p.Notes = cleanProfileText(p.Notes, 500)
	for _, folder := range []*string{&p.CopyRoot, &p.VMFolder} {
		*folder = strings.TrimSpace(*folder)
		if *folder == "" {
			continue
		}
		if strings.Contains(*folder, "..") || !(len(*folder) >= 3 && (*folder)[1] == ':' && ((*folder)[2] == '\\' || (*folder)[2] == '/')) && !strings.HasPrefix(*folder, `\\`) {
			return errors.New("Folders must be full paths, such as D:\\Hyper-V or \\\\server\\share, without \"..\".")
		}
	}
	p.TestSwitch = cleanProfileText(p.TestSwitch, 128)
	if p.SyncMinutes == 0 {
		p.SyncMinutes = 60
	}
	if p.SyncMinutes < 15 || p.SyncMinutes > 1440 {
		return errors.New("Sync every 15 to 1440 minutes.")
	}
	if p.Concurrency == 0 {
		p.Concurrency = 2
	}
	if p.Concurrency < 1 || p.Concurrency > 4 {
		return errors.New("Copy 1 to 4 VMs at once.")
	}
	t := &p.Throttle
	if t.LimitMbps < 0 || t.BusinessMbps < 0 || t.LimitMbps > 100000 || t.BusinessMbps > 100000 {
		return errors.New("Bandwidth limits are 0 (none) to 100000 Mbps.")
	}
	if t.BusinessStart < 0 || t.BusinessStart > 24 || t.BusinessEnd < 0 || t.BusinessEnd > 24 {
		return errors.New("Business hours are 0 to 24.")
	}
	if len(p.NetworkMap) > migrationProfileMaps {
		return errors.New("A profile keeps at most 200 networks.")
	}
	maps := []vmNetworkMap{}
	for _, m := range p.NetworkMap {
		m.Source = cleanProfileText(m.Source, 128)
		m.SourceVLAN = cleanProfileText(m.SourceVLAN, 16)
		m.TargetName = cleanProfileText(m.TargetName, 128)
		m.TargetVLAN = cleanProfileText(m.TargetVLAN, 16)
		if m.Source != "" && (m.TargetName != "" || m.TargetVLAN != "") {
			maps = append(maps, m)
		}
	}
	p.NetworkMap = maps
	return nil
}

// loadMigrationProfiles reads the file; a missing or damaged file gives none.
func loadMigrationProfiles(settingsDir string) []migrationProfile {
	file, err := os.Open(migrationProfilesPath(settingsDir))
	if err != nil {
		return []migrationProfile{}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, migrationProfilesSize+1))
	if err != nil || len(data) > migrationProfilesSize {
		return []migrationProfile{}
	}
	var saved migrationProfiles
	if json.Unmarshal(data, &saved) != nil || saved.Schema != 1 {
		return []migrationProfile{}
	}
	out := []migrationProfile{}
	for _, p := range saved.Profiles {
		if validateMigrationProfile(&p) == nil && len(out) < migrationProfilesMax {
			out = append(out, p)
		}
	}
	return out
}

// putMigrationProfile adds a profile or replaces the one with the same name
// (case-insensitive), sorted by name.
func putMigrationProfile(list []migrationProfile, p migrationProfile) ([]migrationProfile, error) {
	if err := validateMigrationProfile(&p); err != nil {
		return list, err
	}
	out := []migrationProfile{}
	for _, existing := range list {
		if !strings.EqualFold(existing.Name, p.Name) {
			out = append(out, existing)
		}
	}
	if len(out) >= migrationProfilesMax {
		return list, errors.New("At most 50 saved defaults: delete one first.")
	}
	out = append(out, p)
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func deleteMigrationProfile(list []migrationProfile, name string) ([]migrationProfile, bool) {
	out, found := []migrationProfile{}, false
	for _, p := range list {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			found = true
			continue
		}
		out = append(out, p)
	}
	return out, found
}

// saveMigrationProfiles replaces the file atomically.
func saveMigrationProfiles(settingsDir string, list []migrationProfile) error {
	data, err := json.MarshalIndent(migrationProfiles{Schema: 1, Profiles: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(settingsDir, "profiles-*.tmp")
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
	return os.Rename(name, migrationProfilesPath(settingsDir))
}
