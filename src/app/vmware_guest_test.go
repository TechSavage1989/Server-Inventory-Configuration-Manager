//go:build windows

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGuestActivateTakesOnlyOneWellFormedProductKey(t *testing.T) {
	if plan, err := validateGuestItems("activate", nil); err != nil || plan != nil {
		t.Fatalf("activate without a key: %s %v", plan, err)
	}
	plan, err := validateGuestItems("activate", []json.RawMessage{json.RawMessage(`{"productKey":" yqb4h-nkhhj-q6k4r-4vmy6-vch67 "}`)})
	if err != nil || !strings.Contains(string(plan), `"productKey":"YQB4H-NKHHJ-Q6K4R-4VMY6-VCH67"`) {
		t.Fatalf("plan %s %v", plan, err)
	}
	for _, bad := range []string{`{"productKey":"YQB4H-NKHHJ"}`, `{"productKey":"YQB4H-NKHHJ-Q6K4R-4VMY6-VCH67'; Remove-Item C:\\"}`, `{}`} {
		if _, err := validateGuestItems("activate", []json.RawMessage{json.RawMessage(bad)}); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	two := []json.RawMessage{json.RawMessage(`{"productKey":"YQB4H-NKHHJ-Q6K4R-4VMY6-VCH67"}`), json.RawMessage(`{"productKey":"WWVGQ-PNHV9-B89P4-8GGM9-9HPQ4"}`)}
	if _, err := validateGuestItems("activate", two); err == nil {
		t.Error("two keys were accepted")
	}
	if !guestChangingModes["activate"] || guestModes["activate"] != "Activate" {
		t.Error("activate must be a changing mode that needs the typed computer name")
	}
}
