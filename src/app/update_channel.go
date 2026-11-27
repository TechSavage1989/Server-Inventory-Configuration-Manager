package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Update channels (2026-11-27, owner: "a Test opt-in option ... that opt in
// will allow the choice of updating to the new test branch or the main
// version branch ... only updated to by choice"). Stable is the default and
// reads only the stable feed (GitHub's latest release, which never names a
// prerelease). Test also reads the newest signed test prerelease
// (X.Y.Z-test.N, built from the test branch) and offers whichever of the two
// is newer. Choosing Stable again while a test build runs offers the newest
// stable build, even though its version number is lower.
const (
	updateChannelStable = "stable"
	updateChannelTest   = "test"

	// testReleasesURL lists the repository's releases. It is the only
	// api.github.com request the application makes, and only on the Test
	// channel; manifests and downloads stay on the allowlisted release hosts.
	testReleasesURL  = "https://api.github.com/repos/TechSavage1989/Server-Inventory-Configuration-Manager/releases?per_page=30"
	testManifestName = "update-manifest.json"
	testVersionLabel = "-test."
)

// repositoryURL is the public source and release repository.
const repositoryURL = "https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager"

type updateAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type updateManifest struct {
	SchemaVersion int         `json:"schema_version"`
	AppID         string      `json:"app_id"`
	Version       string      `json:"version"`
	PublishedAt   string      `json:"published_at"`
	ReleaseURL    string      `json:"release_url"`
	Notes         []string    `json:"notes"`
	ReadMore      bool        `json:"read_more"`
	Asset         updateAsset `json:"asset"`
}

// appVersionParts is a parsed version: the three numbers and, for a test
// build, its test number (0 for a stable build).
type appVersionParts struct {
	Core [3]int
	Test int
}

// parseVersion accepts X.Y.Z (stable) and X.Y.Z-test.N (test builds, N from
// 1). It returns the four numbers the Windows version resource holds: the
// test number is the fourth (build) field, 0 for stable.
func parseVersion(value string) ([4]int, error) {
	parts, err := parseAppVersion(value)
	if err != nil {
		return [4]int{}, err
	}
	return [4]int{parts.Core[0], parts.Core[1], parts.Core[2], parts.Test}, nil
}

func parseAppVersion(value string) (appVersionParts, error) {
	var result appVersionParts
	value = strings.TrimSpace(value)
	core := value
	if at := strings.Index(value, testVersionLabel); at >= 0 {
		core = value[:at]
		n, err := versionNumber(value[at+len(testVersionLabel):])
		if err != nil || n < 1 {
			return result, fmt.Errorf("invalid test version %q", value)
		}
		result.Test = n
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return result, fmt.Errorf("invalid semantic version %q", value)
	}
	for i, part := range parts {
		n, err := versionNumber(part)
		if err != nil {
			return result, fmt.Errorf("invalid semantic version %q", value)
		}
		result.Core[i] = n
	}
	return result, nil
}

func versionNumber(part string) (int, error) {
	if part == "" || (len(part) > 1 && part[0] == '0') || strings.Trim(part, "0123456789") != "" || len(part) > 5 {
		return 0, errors.New("invalid version number")
	}
	n, err := strconv.Atoi(part)
	if err != nil || n < 0 || n > 65535 {
		return 0, errors.New("invalid version number")
	}
	return n, nil
}

// isTestVersion reports whether value is a test build's version.
func isTestVersion(value string) bool {
	parts, err := parseAppVersion(value)
	return err == nil && parts.Test > 0
}

// compareVersions orders versions as semantic versioning does: 3.3.0-test.2
// comes after 3.3.0-test.1 and 3.2.40, and before 3.3.0.
func compareVersions(left, right string) (int, error) {
	l, err := parseAppVersion(left)
	if err != nil {
		return 0, err
	}
	r, err := parseAppVersion(right)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if l.Core[i] != r.Core[i] {
			if l.Core[i] < r.Core[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	rank := func(test int) int {
		if test == 0 {
			return 1 << 20 // a stable build follows every test build of the same numbers
		}
		return test
	}
	switch {
	case rank(l.Test) < rank(r.Test):
		return -1, nil
	case rank(l.Test) > rank(r.Test):
		return 1, nil
	}
	return 0, nil
}

// updateChoice is what the updater offers on a channel.
type updateChoice struct {
	Manifest *updateManifest // the build to show and offer (nil when no feed answered)
	Offer    bool            // an install is offered
	ToStable bool            // the offer replaces a test build with the newest stable build
}

// chooseUpdate picks the build to offer. stable comes from the stable feed;
// test is the newest test prerelease and is used only on the Test channel.
func chooseUpdate(installed, channel string, stable, test *updateManifest) updateChoice {
	var best *updateManifest
	consider := func(m *updateManifest) {
		if m == nil {
			return
		}
		if best == nil {
			best = m
			return
		}
		if cmp, err := compareVersions(best.Version, m.Version); err == nil && cmp < 0 {
			best = m
		}
	}
	if stable != nil && !isTestVersion(stable.Version) {
		consider(stable)
	}
	if channel == updateChannelTest && test != nil && isTestVersion(test.Version) {
		consider(test)
	}
	if best == nil {
		return updateChoice{}
	}
	if cmp, err := compareVersions(installed, best.Version); err == nil && cmp < 0 {
		return updateChoice{Manifest: best, Offer: true}
	}
	if channel != updateChannelTest && isTestVersion(installed) && stable != nil && !isTestVersion(stable.Version) && stable.Version != installed {
		return updateChoice{Manifest: stable, Offer: true, ToStable: true}
	}
	return updateChoice{Manifest: best}
}

// pickTestRelease returns the tag of the newest published test prerelease in
// a GitHub releases listing that carries an update manifest, or "" when
// there is none. Drafts, stable releases and other tags are ignored.
func pickTestRelease(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var releases []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", fmt.Errorf("the release list could not be read: %w", err)
	}
	best := ""
	for _, release := range releases {
		if release.Draft || !release.Prerelease || !strings.HasPrefix(release.TagName, "v") {
			continue
		}
		version := strings.TrimPrefix(release.TagName, "v")
		if !isTestVersion(version) {
			continue
		}
		hasManifest := false
		for _, asset := range release.Assets {
			if asset.Name == testManifestName {
				hasManifest = true
			}
		}
		if !hasManifest {
			continue
		}
		if best == "" {
			best = version
		} else if cmp, err := compareVersions(best, version); err == nil && cmp < 0 {
			best = version
		}
	}
	if best == "" {
		return "", nil
	}
	return "v" + best, nil
}

// testManifestURL is where a test release's manifest is downloaded from.
func testManifestURL(tag string) string {
	return repositoryURL + "/releases/download/" + tag + "/" + testManifestName
}

// updateChannelSettings is Settings\update-channel.json, a separate file so
// earlier versions (which know only the stable feed) never rewrite it.
type updateChannelSettings struct {
	Schema  int    `json:"schema"`
	Channel string `json:"channel"`
}

func updateChannelPath(settingsDir string) string {
	return filepath.Join(settingsDir, "update-channel.json")
}

func validUpdateChannel(channel string) bool {
	return channel == updateChannelStable || channel == updateChannelTest
}

// loadUpdateChannel gives the saved channel; a missing, damaged or unknown
// file means Stable.
func loadUpdateChannel(settingsDir string) string {
	file, err := os.Open(updateChannelPath(settingsDir))
	if err != nil {
		return updateChannelStable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4*1024+1))
	if err != nil || len(data) > 4*1024 {
		return updateChannelStable
	}
	var saved updateChannelSettings
	if json.Unmarshal(data, &saved) != nil || saved.Schema != 1 || !validUpdateChannel(saved.Channel) {
		return updateChannelStable
	}
	return saved.Channel
}

// saveUpdateChannel replaces update-channel.json atomically.
func saveUpdateChannel(settingsDir, channel string) error {
	if !validUpdateChannel(channel) {
		return errors.New("Choose the Stable or Test update channel.")
	}
	data, err := json.MarshalIndent(updateChannelSettings{Schema: 1, Channel: channel}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	path := updateChannelPath(settingsDir)
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
