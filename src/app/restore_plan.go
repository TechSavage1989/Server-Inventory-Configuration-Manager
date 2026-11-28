package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Scan results, selections and the Changes for this server of a restore
// (Restore-ServerConfiguration.ps1). Untagged so the rules are tested on
// every platform.

var restoreComponentIDs = []string{"features", "accounts", "shares", "dhcp", "nps", "printers", "iis", "dns", "gpo", "firewall", "certificates", "tasks", "acls", "registry", "secpol"}

type restoreScanItem struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Detail          string `json:"detail"`
	Available       bool   `json:"available"`
	BlockedReason   string `json:"blockedReason"`
	DefaultSelected bool   `json:"defaultSelected"`
	// NeedsSecret names a password typed under Changes for this server:
	// required for the item unless SecretOptional.
	NeedsSecret    string `json:"needsSecret"`
	SecretLabel    string `json:"secretLabel"`
	SecretOptional bool   `json:"secretOptional"`
}

type restoreScanComponent struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Impact        string            `json:"impact"`
	Available     bool              `json:"available"`
	BlockedReason string            `json:"blockedReason"`
	Warnings      []string          `json:"warnings"`
	Items         []restoreScanItem `json:"items"`
}

type restoreScan struct {
	PackagePath      string                 `json:"packagePath"`
	PackageName      string                 `json:"packageName"`
	SourceComputer   string                 `json:"sourceComputer"`
	CurrentComputer  string                 `json:"currentComputer"`
	SameComputer     bool                   `json:"sameComputer"`
	CollectedAt      string                 `json:"collectedAt"`
	ManifestVerified bool                   `json:"manifestVerified"`
	ManifestMessage  string                 `json:"manifestMessage"`
	IsAdministrator  bool                   `json:"isAdministrator"`
	Components       []restoreScanComponent `json:"components"`
	Bundle           restoreBundleInfo      `json:"bundle"`
	Mappings         []restoreMapping       `json:"mappings"`
	NotMovable       []restoreNotMovable    `json:"notMovable"`
}

// restoreBundleInfo describes the package's migration bundle. Passphrase is
// none, missing, wrong, damaged or ok; never the passphrase itself.
type restoreBundleInfo struct {
	Present    bool     `json:"present"`
	Usable     bool     `json:"usable"`
	Message    string   `json:"message"`
	Passphrase string   `json:"passphrase"`
	CreatedAt  string   `json:"createdAt"`
	Warnings   []string `json:"warnings"`
}

// restoreMapping is one Change for this server: an old IP address (ip) or
// drive (path), the value chosen (To), the engine's suggestion and the values
// this server offers.
type restoreMapping struct {
	Kind      string   `json:"kind"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Suggested string   `json:"suggested"`
	Why       string   `json:"why"`
	Options   []string `json:"options"`
	UsedBy    []string `json:"usedBy"`
}

// restoreNotMovable is a role a package cannot move, with what to do instead.
type restoreNotMovable struct {
	Role     string `json:"role"`
	Guidance string `json:"guidance"`
}

type restoreSelection struct {
	ID    string   `json:"id"`
	Items []string `json:"items"`
}

// validateRestoreSelection keeps only items the reviewed scan marked available.
func validateRestoreSelection(scan *restoreScan, selections []restoreSelection) ([]restoreSelection, int, error) {
	if scan == nil {
		return nil, 0, errors.New("Check the package before restoring.")
	}
	components := map[string]*restoreScanComponent{}
	for index := range scan.Components {
		components[scan.Components[index].ID] = &scan.Components[index]
	}
	seen := map[string]bool{}
	result := []restoreSelection{}
	total := 0
	for _, selection := range selections {
		component := components[selection.ID]
		if component == nil {
			return nil, 0, fmt.Errorf("%q is not a restore component in this package.", selection.ID)
		}
		if seen[selection.ID] {
			return nil, 0, fmt.Errorf("%s was selected twice.", component.Title)
		}
		seen[selection.ID] = true
		available := map[string]bool{}
		for _, item := range component.Items {
			if item.Available {
				available[item.ID] = true
			}
		}
		chosen := []string{}
		picked := map[string]bool{}
		for _, id := range selection.Items {
			if !available[id] {
				return nil, 0, fmt.Errorf("An item selected under %s cannot be restored.", component.Title)
			}
			if !picked[id] {
				picked[id] = true
				chosen = append(chosen, id)
			}
		}
		if len(chosen) > 0 {
			result = append(result, restoreSelection{ID: selection.ID, Items: chosen})
			total += len(chosen)
		}
	}
	if total == 0 {
		return nil, 0, errors.New("Select at least one item to restore.")
	}
	return result, total, nil
}

// restoreChanges is what the operator chose under Changes for this server.
type restoreChanges struct {
	IP   map[string]string `json:"ip"`
	Path map[string]string `json:"path"`
}

// validateRestoreChanges keeps only mappings the scan offered, each set to a
// value this server offered for it. Mappings the operator left alone keep the
// scan's value.
func validateRestoreChanges(scan *restoreScan, chosen restoreChanges) (restoreChanges, error) {
	result := restoreChanges{IP: map[string]string{}, Path: map[string]string{}}
	if scan == nil {
		return result, errors.New("Check the package before restoring.")
	}
	for _, mapping := range scan.Mappings {
		var source map[string]string
		var target map[string]string
		switch mapping.Kind {
		case "ip":
			source, target = chosen.IP, result.IP
		case "path":
			source, target = chosen.Path, result.Path
		default:
			continue
		}
		value := mapping.To
		if picked, ok := source[mapping.From]; ok {
			value = strings.TrimSpace(picked)
		}
		allowed := false
		for _, option := range mapping.Options {
			if strings.EqualFold(option, value) {
				value = option
				allowed = true
				break
			}
		}
		if !allowed {
			return result, fmt.Errorf("%s cannot be changed to %q on this server.", mapping.From, value)
		}
		target[mapping.From] = value
	}
	for from := range chosen.IP {
		if _, ok := result.IP[from]; !ok {
			return result, fmt.Errorf("%s is not an address this package uses.", from)
		}
	}
	for from := range chosen.Path {
		if _, ok := result.Path[from]; !ok {
			return result, fmt.Errorf("%s is not a drive this package uses.", from)
		}
	}
	return result, nil
}

// validateRestorePasswords keeps only passwords that a selected item needs or
// can use, and refuses a selection that lacks a required one. The passwords
// go to the engine on standard input and are never stored.
func validateRestorePasswords(scan *restoreScan, selections []restoreSelection, passwords map[string]string) (map[string]string, error) {
	result := map[string]string{}
	if scan == nil {
		return result, errors.New("Check the package before restoring.")
	}
	selected := map[string]bool{}
	for _, selection := range selections {
		for _, id := range selection.Items {
			selected[selection.ID+"\x00"+id] = true
		}
	}
	missing := []string{}
	for _, component := range scan.Components {
		for _, item := range component.Items {
			if item.NeedsSecret == "" || !selected[component.ID+"\x00"+item.ID] {
				continue
			}
			value := passwords[item.NeedsSecret]
			if len(value) > restorePasswordLimit {
				return nil, fmt.Errorf("The %s is longer than %d characters.", strings.ToLower(item.SecretLabel), restorePasswordLimit)
			}
			if strings.ContainsAny(value, "\r\n\x00") {
				return nil, fmt.Errorf("The %s cannot contain line breaks.", strings.ToLower(item.SecretLabel))
			}
			if value == "" {
				if !item.SecretOptional {
					missing = append(missing, item.SecretLabel)
				}
				continue
			}
			result[item.NeedsSecret] = value
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("Type these under Changes for this server, or untick the items that need them: %s.", strings.Join(uniqueStrings(missing), "; "))
	}
	return result, nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
