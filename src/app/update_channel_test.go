package main

import (
	"strings"
	"testing"
)

func channelManifest(version string) *updateManifest {
	return &updateManifest{SchemaVersion: 1, AppID: "Server-Inventory-Configuration-Manager", Version: version}
}

func TestVersionsOrderTestBuildsBeforeTheirStableRelease(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"3.2.0", "3.2.0", 0},
		{"3.1.9", "3.2.0", -1},
		{"4.0.0", "3.99.99", 1},
		{"3.3.0-test.1", "3.3.0-test.1", 0},
		{"3.3.0-test.1", "3.3.0-test.2", -1},
		{"3.3.0-test.10", "3.3.0-test.9", 1},
		{"3.3.0-test.5", "3.3.0", -1},
		{"3.3.0-test.5", "3.2.40", 1},
		{"3.3.1", "3.3.0-test.99", 1},
	}
	for _, tc := range cases {
		got, err := compareVersions(tc.left, tc.right)
		if err != nil || got != tc.want {
			t.Fatalf("compareVersions(%q,%q)=%d,%v want %d", tc.left, tc.right, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "3.2", "3.2.0.1", "03.2.0", "3.2.0-test", "3.2.0-test.0", "3.2.0-test.01", "3.2.0-beta.1", "3.2.0-test.1-test.2", "3.2.0-test.70000", "v3.2.0"} {
		if _, err := parseVersion(bad); err == nil {
			t.Fatalf("parseVersion(%q) accepted an invalid version", bad)
		}
	}
	if v, err := parseVersion("3.3.0-test.7"); err != nil || v != [4]int{3, 3, 0, 7} {
		t.Fatalf("parseVersion test build = %v, %v", v, err)
	}
	if v, err := parseVersion("3.2.38"); err != nil || v != [4]int{3, 2, 38, 0} {
		t.Fatalf("parseVersion stable = %v, %v", v, err)
	}
}

func TestStableChannelNeverOffersATestBuild(t *testing.T) {
	stable, test := channelManifest("3.2.39"), channelManifest("3.3.0-test.4")
	choice := chooseUpdate("3.2.38", updateChannelStable, stable, test)
	if !choice.Offer || choice.Manifest.Version != "3.2.39" || choice.ToStable {
		t.Fatalf("stable channel offer = %+v", choice)
	}
	if choice := chooseUpdate("3.2.39", updateChannelStable, stable, test); choice.Offer {
		t.Fatalf("stable channel offered %s", choice.Manifest.Version)
	}
	// A stable feed that somehow names a test build is never offered.
	if choice := chooseUpdate("3.2.38", updateChannelStable, channelManifest("3.3.0-test.1"), nil); choice.Offer {
		t.Fatal("a test build from the stable feed was offered")
	}
	// A test manifest passed as the test candidate must be a test build.
	if choice := chooseUpdate("3.2.38", updateChannelTest, stable, channelManifest("3.4.0")); choice.Manifest.Version != "3.2.39" {
		t.Fatalf("a stable version in the test slot was offered: %+v", choice)
	}
}

func TestTestChannelOffersTheNewerOfTestAndStable(t *testing.T) {
	stable, test := channelManifest("3.2.39"), channelManifest("3.3.0-test.4")
	if c := chooseUpdate("3.2.38", updateChannelTest, stable, test); !c.Offer || c.Manifest.Version != "3.3.0-test.4" {
		t.Fatalf("test channel = %+v", c)
	}
	if c := chooseUpdate("3.3.0-test.3", updateChannelTest, stable, test); !c.Offer || c.Manifest.Version != "3.3.0-test.4" {
		t.Fatalf("test channel next build = %+v", c)
	}
	// The stable release of the same numbers overtakes its test builds.
	if c := chooseUpdate("3.3.0-test.4", updateChannelTest, channelManifest("3.3.0"), test); !c.Offer || c.Manifest.Version != "3.3.0" || c.ToStable {
		t.Fatalf("stable overtaking test = %+v", c)
	}
	if c := chooseUpdate("3.3.0-test.4", updateChannelTest, stable, test); c.Offer {
		t.Fatalf("current test build offered %+v", c)
	}
	// No test release published yet: the test channel follows stable.
	if c := chooseUpdate("3.2.38", updateChannelTest, stable, nil); !c.Offer || c.Manifest.Version != "3.2.39" {
		t.Fatalf("test channel without test builds = %+v", c)
	}
}

func TestChoosingStableAgainOffersTheNewestStableBuild(t *testing.T) {
	stable := channelManifest("3.2.40")
	c := chooseUpdate("3.3.0-test.4", updateChannelStable, stable, channelManifest("3.3.0-test.5"))
	if !c.Offer || !c.ToStable || c.Manifest.Version != "3.2.40" {
		t.Fatalf("return to stable = %+v", c)
	}
	if c := chooseUpdate("3.2.40", updateChannelStable, stable, nil); c.Offer {
		t.Fatalf("stable build offered itself: %+v", c)
	}
}

func TestPickTestReleaseChoosesTheNewestPublishedTestPrerelease(t *testing.T) {
	listing := `[
	 {"tag_name":"v3.3.0-test.9","draft":true,"prerelease":true,"assets":[{"name":"update-manifest.json"}]},
	 {"tag_name":"v3.3.0-test.3","draft":false,"prerelease":true,"assets":[{"name":"update-manifest.json"}]},
	 {"tag_name":"v3.3.0-test.10","draft":false,"prerelease":true,"assets":[{"name":"update-manifest.json"},{"name":"Server-Inventory-Configuration-Manager.exe"}]},
	 {"tag_name":"v3.3.0-test.11","draft":false,"prerelease":true,"assets":[{"name":"Server-Inventory-Configuration-Manager.exe"}]},
	 {"tag_name":"v3.3.0-test.12","draft":false,"prerelease":false,"assets":[{"name":"update-manifest.json"}]},
	 {"tag_name":"v9.0.0","draft":false,"prerelease":false,"assets":[{"name":"update-manifest.json"}]},
	 {"tag_name":"v9.0.0-beta.1","draft":false,"prerelease":true,"assets":[{"name":"update-manifest.json"}]}
	]`
	tag, err := pickTestRelease([]byte(listing))
	if err != nil || tag != "v3.3.0-test.10" {
		t.Fatalf("pickTestRelease = %q, %v", tag, err)
	}
	if tag, err := pickTestRelease([]byte(`[]`)); err != nil || tag != "" {
		t.Fatalf("empty listing = %q, %v", tag, err)
	}
	if _, err := pickTestRelease([]byte(`{"message":"rate limited"}`)); err == nil {
		t.Fatal("a non-list answer was accepted")
	}
	if got := testManifestURL("v3.3.0-test.10"); !strings.HasPrefix(got, repositoryURL+"/releases/download/v3.3.0-test.10/") {
		t.Fatalf("manifest URL %s", got)
	}
}

func TestUpdateChannelSettingDefaultsToStable(t *testing.T) {
	dir := t.TempDir()
	if got := loadUpdateChannel(dir); got != updateChannelStable {
		t.Fatalf("default channel %q", got)
	}
	if err := saveUpdateChannel(dir, "beta"); err == nil {
		t.Fatal("an unknown channel was saved")
	}
	if err := saveUpdateChannel(dir, updateChannelTest); err != nil {
		t.Fatal(err)
	}
	if got := loadUpdateChannel(dir); got != updateChannelTest {
		t.Fatalf("saved channel %q", got)
	}
	if err := saveUpdateChannel(dir, updateChannelStable); err != nil {
		t.Fatal(err)
	}
	if got := loadUpdateChannel(dir); got != updateChannelStable {
		t.Fatalf("saved channel %q", got)
	}
}
