//go:build windows

package main

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"3.2.0", "3.2.0", 0},
		{"3.1.9", "3.2.0", -1},
		{"3.2.1", "3.2.0", 1},
		{"4.0.0", "3.99.99", 1},
	}
	for _, tc := range cases {
		got, err := compareVersions(tc.left, tc.right)
		if err != nil {
			t.Fatalf("compareVersions(%q,%q): %v", tc.left, tc.right, err)
		}
		if got != tc.want {
			t.Fatalf("compareVersions(%q,%q)=%d want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestValidateManifest(t *testing.T) {
	manifest := &updateManifest{
		SchemaVersion: 1,
		AppID:         applicationID,
		Version:       "3.2.1",
		ReleaseURL:    "https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.1",
		Asset: updateAsset{
			Name:   stableExecutableName,
			URL:    "https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/download/v3.2.1/Server-Inventory-Configuration-Manager.exe",
			SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Size:   1024,
		},
	}
	if err := validateManifest(manifest); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	bad := *manifest
	bad.AppID = "Other-App"
	if err := validateManifest(&bad); err == nil {
		t.Fatal("wrong app identity was accepted")
	}

	bad = *manifest
	bad.Asset.URL = "http://example.com/update.exe"
	if err := validateManifest(&bad); err == nil {
		t.Fatal("non-HTTPS update URL was accepted")
	}

	bad = *manifest
	bad.Asset.Name = "wrong.exe"
	if err := validateManifest(&bad); err == nil {
		t.Fatal("wrong update asset name was accepted")
	}
}

func TestBinaryIdentityMarker(t *testing.T) {
	want := binaryIdentityPrefix + appVersion
	if binaryIdentityMarker != want {
		t.Fatalf("binary identity marker %q want %q", binaryIdentityMarker, want)
	}
}