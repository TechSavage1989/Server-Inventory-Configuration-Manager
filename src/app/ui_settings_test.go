package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUISettingsDefaultToATickBoxAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := loadUISettings(dir); got.ConfirmStyle != confirmTick {
		t.Fatalf("missing file: got %q, want tick", got.ConfirmStyle)
	}
	for _, style := range confirmStyles {
		if err := saveUISettings(dir, uiSettings{ConfirmStyle: style}); err != nil {
			t.Fatalf("save %s: %v", style, err)
		}
		if got := loadUISettings(dir); got.ConfirmStyle != style || got.Schema != 1 {
			t.Fatalf("round trip %s: got %+v", style, got)
		}
	}
	if err := saveUISettings(dir, uiSettings{ConfirmStyle: "skip"}); err == nil {
		t.Fatal("an unknown style was saved")
	}
	if got := loadUISettings(dir); got.ConfirmStyle != confirmTyped {
		t.Fatalf("a refused save changed the file: %q", got.ConfirmStyle)
	}
	s := uiSettings{ConfirmStyle: " Prompt "}
	if err := validateUISettings(&s); err != nil || s.ConfirmStyle != confirmPrompt {
		t.Fatalf("normalise: %v %q", err, s.ConfirmStyle)
	}
	for _, bad := range []string{`{"schema":2,"confirmStyle":"none"}`, `{"schema":1,"confirmStyle":"whatever"}`, `not json`} {
		if err := os.WriteFile(filepath.Join(dir, "interface.json"), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := loadUISettings(dir); got.ConfirmStyle != confirmTick {
			t.Fatalf("%s: got %q, want the default", bad, got.ConfirmStyle)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "interface-*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
}
