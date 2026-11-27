package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stand-in for DPAPI: reversible, and never the plain text.
func vcTestSeal(plain []byte) ([]byte, error) {
	out := make([]byte, len(plain))
	for i, b := range plain {
		out[i] = b ^ 0x5a
	}
	return append([]byte("sealed:"), out...), nil
}

func vcTestUnseal(sealed []byte) ([]byte, error) {
	if !strings.HasPrefix(string(sealed), "sealed:") {
		return nil, errors.New("not sealed here")
	}
	out, _ := vcTestSeal(sealed[len("sealed:"):])
	return out[len("sealed:"):], nil
}

// Remembered vCenter sign-ins are off by default, sealed when on, never
// written in plain text, and gone when turned off, forgotten or refused.
func TestRememberedVCenterSignInsAreOptInSealedAndForgettable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Settings", "vcenter-signins.json")
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	store := newVCRemember(path, vcTestSeal, vcTestUnseal)
	if store.Enabled() {
		t.Fatal("remembering is on by default")
	}
	if err := store.Remember("vc01.example.com", "svc-migrate@vsphere.local", "Pa55-word!", "", now); err != nil {
		t.Fatal(err)
	}
	if store.Has("vc01.example.com") {
		t.Fatal("a sign-in was remembered while remembering was off")
	}
	if err := store.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := store.Remember("vc01.example.com", "svc-migrate@vsphere.local", "Pa55-word!", "AB:CD", now); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Pa55-word!") {
		t.Fatal("the password was written in plain text")
	}
	// A new store (the application restarted) reads it back.
	store = newVCRemember(path, vcTestSeal, vcTestUnseal)
	user, password, thumb, ok := store.Get("VC01.example.com")
	if !ok || user != "svc-migrate@vsphere.local" || password != "Pa55-word!" || thumb != "AB:CD" {
		t.Fatalf("Get = %q %q %q %v", user, password, thumb, ok)
	}
	view := store.View()
	if !view.Enabled || len(view.Entries) != 1 || view.Entries[0].User != "svc-migrate@vsphere.local" {
		t.Fatalf("view = %+v", view)
	}
	// A blob that names another server is refused.
	store.data.Entries = append(store.data.Entries, vcRememberEntry{Server: "vc02.example.com", User: "svc-migrate@vsphere.local", Sealed: store.data.Entries[0].Sealed})
	if _, _, _, ok := store.Get("vc02.example.com"); ok {
		t.Fatal("a sealed sign-in of another server was accepted")
	}
	// Refused: forgotten, with the reason to show.
	if err := store.Forget("vc01.example.com", "The remembered password was refused, so it was forgotten."); err != nil {
		t.Fatal(err)
	}
	if store.Has("vc01.example.com") || !strings.Contains(store.View().Notice, "refused") || store.Notice("VC01.EXAMPLE.COM") == "" {
		t.Fatalf("after a refusal: %+v", store.View())
	}
	store.Remember("vc01.example.com", "svc-migrate@vsphere.local", "New-pass", "", now)
	if store.Notice("vc01.example.com") != "" {
		t.Fatal("a new sign-in kept the old refusal")
	}
	// Turning it off forgets everything.
	if err := store.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "sealed") || strings.Contains(string(raw), "vc01") {
		t.Fatalf("turning it off kept a sign-in: %s", raw)
	}
	if !vcRememberRefused(vmFriendlyConnectError(errors.New("ServerFaultCode: Cannot complete login due to an incorrect user name or password."), "vc01").Error()) {
		t.Fatal("a refused password is not recognised")
	}
	if vcRememberRefused(vmFriendlyConnectError(errors.New("dial tcp: i/o timeout"), "vc01").Error()) {
		t.Fatal("an unreachable server was taken as a refused password")
	}
}

// Plans waiting for a sign-in name their servers once, leaving out paused
// plans and plans with nothing left to move.
func TestPlansListTheServersTheyWaitFor(t *testing.T) {
	target := &mbFakeTarget{fail: map[string]error{}}
	runner, now := mbTestRunner(t, target)
	runner.ready = func(plan *mbPlan) string {
		if plan.Server == "ready.example.com" {
			return ""
		}
		return "Sign in to " + plan.Server + " in Discovery again"
	}
	settings := mbSettings{StartAt: *now, SyncMinutes: 30, Concurrency: 1}
	for i, server := range []string{"vc01.example.com", "VC01.example.com", "ready.example.com", "vc02.example.com"} {
		if _, err := runner.Create(fmt.Sprintf("Plan %d", i), server, settings, mbVMs(fmt.Sprintf("app%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	paused, _ := runner.Create("Paused", "vc03.example.com", settings, mbVMs("app99"))
	runner.SetPaused(paused.ID, true)
	got := strings.Join(runner.WaitingServers(), ",")
	if got != "vc01.example.com,vc02.example.com" {
		t.Fatalf("waiting servers = %s", got)
	}
}

// The guest administrator account is remembered under its own key with the
// same opt-in, shown as a guest account, and never offered as a server.
func TestTheGuestAdministratorAccountIsRememberedWithTheSameOptIn(t *testing.T) {
	seal := func(b []byte) ([]byte, error) { return append([]byte("sealed:"), b...), nil }
	unseal := func(b []byte) ([]byte, error) { return b[len("sealed:"):], nil }
	store := newVCRemember(filepath.Join(t.TempDir(), "signins.json"), seal, unseal)
	if err := store.Remember(vcRememberGuestKey, `.\Administrator`, "Synthetic-Guest", "", time.Now()); err != nil || store.Has(vcRememberGuestKey) {
		t.Fatal("remembered while the setting is off", err)
	}
	if err := store.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := store.Remember(vcRememberGuestKey, `.\Administrator`, "Synthetic-Guest", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	user, password, _, ok := store.Get(vcRememberGuestKey)
	if !ok || user != `.\Administrator` || password != "Synthetic-Guest" {
		t.Fatal("the guest account did not come back", user, ok)
	}
	view := store.View()
	if len(view.Entries) != 1 || view.Entries[0].Kind != "guest" {
		t.Fatalf("the guest account is not listed as one: %+v", view.Entries)
	}
}
