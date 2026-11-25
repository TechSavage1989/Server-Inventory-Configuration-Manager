package main

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var discoveryTestLogs = map[*vmReceiver]*[]string{}

func receiverLogLines(r *vmReceiver) []string { return *discoveryTestLogs[r] }

func startDiscoveryTestReceiver(t *testing.T, pairing bool) *vmReceiver {
	t.Helper()
	lines := &[]string{}
	var mu sync.Mutex
	receiver, err := startVMReceiverWith("127.0.0.1:0", vmReceiverConfig{Folder: t.TempDir(), Discovery: "127.0.0.1:0", Pairing: pairing, FreeBytes: func() uint64 { return 5 << 40 }, Version: "3.2.34"}, func(line string) {
		mu.Lock()
		*lines = append(*lines, line)
		mu.Unlock()
	})
	discoveryTestLogs[receiver] = lines
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { receiver.Stop() })
	if receiver.discovery == nil {
		t.Fatal("the receiver does not answer find requests")
	}
	return receiver
}

func TestFindReceiversAnswersOnlyAskedAndCarriesNoCode(t *testing.T) {
	receiver := startDiscoveryTestReceiver(t, true)
	target := receiver.discovery.Addr().String()
	found, err := vmFindReceivers(context.Background(), []string{target}, 700*time.Millisecond)
	if err != nil || len(found) != 1 {
		t.Fatalf("found %+v %v", found, err)
	}
	a := found[0]
	if a.Port != receiver.Port() || a.Thumbprint != vmNormalizeThumbprint(receiver.Thumbprint()) || !a.Folder || !a.Pairing || a.FreeBytes != 5<<40 || a.Version != "3.2.34" {
		t.Fatalf("announcement %+v", a)
	}
	if a.Fingerprint != vmFingerprint(receiver.Thumbprint()) || len(a.Fingerprint) != 19 {
		t.Fatalf("fingerprint %q", a.Fingerprint)
	}
	if len(a.Seen) != 1 || a.Seen[0] != net.JoinHostPort("127.0.0.1", strconv.Itoa(receiver.Port())) {
		t.Fatalf("seen %v", a.Seen)
	}
	// The answer never holds the pairing code.
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), receiver.Code()) || strings.Contains(strings.ReplaceAll(string(raw), "-", ""), strings.ReplaceAll(receiver.Code(), "-", "")) {
		t.Fatal("the find answer carries the pairing code")
	}
	// Anything but a well-formed find request gets no answer at all.
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	address, _ := net.ResolveUDPAddr("udp4", target)
	for _, bad := range []string{"hello", vmDiscoveryQuery + "short", vmDiscoveryQuery + strings.Repeat("zz", 16)} {
		conn.WriteTo([]byte(bad), address)
	}
	conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	if n, _, err := conn.ReadFrom(make([]byte, 2048)); err == nil {
		t.Fatalf("a malformed request was answered (%d bytes)", n)
	}
	// Once stopped, nothing answers.
	receiver.Stop()
	if found, _ := vmFindReceivers(context.Background(), []string{target}, 300*time.Millisecond); len(found) != 0 {
		t.Fatal("a stopped receiver still answers")
	}
}

func TestBroadcastTargetsAreOnePerAdapterWithoutSweeps(t *testing.T) {
	targets := vmBroadcastTargets(vmDiscoveryPort)
	if len(targets) < 2 || targets[0] != "255.255.255.255:44379" || targets[1] != "127.0.0.1:44379" {
		t.Fatalf("targets %v", targets)
	}
	if len(targets) > 64 {
		t.Fatalf("%d targets: a search must not sweep addresses", len(targets))
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target] {
			t.Fatalf("%s is listed twice", target)
		}
		seen[target] = true
	}
}

func TestPairingNeedsApprovalOnTheReceiverAndHandsTheCodeOverOnce(t *testing.T) {
	receiver := startDiscoveryTestReceiver(t, true)
	ctx := context.Background()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(receiver.Port()))
	// A wrong thumbprint never reaches the receiver.
	if _, _, err := vmRequestPairing(ctx, address, strings.Repeat("ab", 32), "ADMIN01"); err == nil || !strings.Contains(err.Error(), "thumbprint") {
		t.Fatalf("wrong thumbprint: %v", err)
	}
	id, check, err := vmRequestPairing(ctx, address, receiver.Thumbprint(), "ADMIN01\x07")
	if err != nil {
		t.Fatal(err)
	}
	pending := receiver.PairRequests()
	if len(pending) != 1 || pending[0].Computer != "ADMIN01" || pending[0].Address != "127.0.0.1" || pending[0].Check != check {
		t.Fatalf("pending %+v, check %s", pending, check)
	}
	// Nothing is handed over before the operator allows it.
	state, code, err := vmPairingStatus(ctx, address, receiver.Thumbprint(), id)
	if err != nil || state != "pending" || code != "" {
		t.Fatalf("before approval: %s %q %v", state, code, err)
	}
	if err := receiver.AnswerPair(id, true); err != nil {
		t.Fatal(err)
	}
	state, code, err = vmPairingStatus(ctx, address, receiver.Thumbprint(), id)
	if err != nil || state != "allowed" || code != receiver.Code() {
		t.Fatalf("after approval: %s %v", state, err)
	}
	// The code works for copying, and is handed over only once.
	if _, err := newVMReceiverClient(ctx, address, receiver.Thumbprint(), code); err != nil {
		t.Fatal(err)
	}
	if state, code, _ = vmPairingStatus(ctx, address, receiver.Thumbprint(), id); state != "expired" || code != "" {
		t.Fatalf("second read: %s %q", state, code)
	}
	if err := receiver.AnswerPair(id, true); err == nil {
		t.Fatal("an answered request was answered again")
	}
	// A denied request gets no code; a new request from the same address
	// replaces the one still waiting.
	first, _, _ := vmRequestPairing(ctx, address, receiver.Thumbprint(), "ADMIN01")
	second, _, _ := vmRequestPairing(ctx, address, receiver.Thumbprint(), "ADMIN01")
	if list := receiver.PairRequests(); len(list) != 1 || list[0].ID != second {
		t.Fatalf("one request per address: %+v", list)
	}
	if state, _, _ := vmPairingStatus(ctx, address, receiver.Thumbprint(), first); state != "expired" {
		t.Fatalf("the replaced request is %s", state)
	}
	receiver.AnswerPair(second, false)
	if state, code, _ := vmPairingStatus(ctx, address, receiver.Thumbprint(), second); state != "denied" || code != "" {
		t.Fatalf("denied: %s %q", state, code)
	}
	// The receiver never logs the code.
	if strings.Contains(strings.Join(receiverLogLines(receiver), "\n"), receiver.Code()) {
		t.Fatal("the pairing code was logged")
	}
}

func TestPairingIsRefusedWhenTheReceiverTakesNoRequests(t *testing.T) {
	receiver := startDiscoveryTestReceiver(t, false)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(receiver.Port()))
	if _, _, err := vmRequestPairing(context.Background(), address, receiver.Thumbprint(), "ADMIN01"); err == nil || !strings.Contains(err.Error(), "type the pairing code") {
		t.Fatalf("pairing on a receiver without a screen: %v", err)
	}
	if found, _ := vmFindReceivers(context.Background(), []string{receiver.discovery.Addr().String()}, 500*time.Millisecond); len(found) != 1 || found[0].Pairing {
		t.Fatalf("found %+v", found)
	}
}

func TestPairCheckDiffersWhenTheCertificateDiffers(t *testing.T) {
	id := strings.Repeat("0f", 16)
	a, b := vmPairCheck(id, strings.Repeat("ab", 32)), vmPairCheck(id, strings.Repeat("cd", 32))
	if a == b || len(a) != 7 || a[3] != ' ' {
		t.Fatalf("checks %q %q", a, b)
	}
	if vmPairCheck(strings.ToUpper(id), strings.Repeat("AB", 32)) != a {
		t.Fatal("the check depends on letter case")
	}
	if vmCleanLabel("A\x00B\x1bC "+strings.Repeat("x", 100), 10) != "ABC xxxxxx" {
		t.Fatalf("clean %q", vmCleanLabel("A\x00B\x1bC "+strings.Repeat("x", 100), 10))
	}
}
