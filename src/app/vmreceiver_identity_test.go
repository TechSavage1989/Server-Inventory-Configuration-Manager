package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A fake seal that is not the identity, so the test sees nothing is written
// in the clear.
func testSeal(plain []byte) ([]byte, error) {
	out := make([]byte, len(plain))
	for i, b := range plain {
		out[i] = b ^ 0x5a
	}
	return out, nil
}

func TestReceiverKeepsItsCertificateAndCodeAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Receiver", "identity.json")
	first, kept, saveErr, err := vmReceiverIdentityFor(path, "hv01", false, testSeal, testSeal)
	if err != nil || saveErr != nil || kept {
		t.Fatalf("first start: kept=%v save=%v err=%v", kept, saveErr, err)
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte(first.Code)) || bytes.Contains(data, []byte(vmNormalizePairingCode(first.Code))) {
		t.Fatal("the pairing code was written unsealed")
	}
	second, kept, _, err := vmReceiverIdentityFor(path, "hv01", false, testSeal, testSeal)
	if err != nil || !kept || second.Code != first.Code || second.Thumbprint != first.Thumbprint {
		t.Fatalf("restart did not keep the identity: kept=%v err=%v", kept, err)
	}
	// A receiver started with the kept identity answers with the pinned
	// thumbprint and code that a job saved before the restart.
	r, err := startVMReceiverWith("127.0.0.1:0", vmReceiverConfig{Folder: t.TempDir(), Identity: second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	if r.Thumbprint() != first.Thumbprint || r.Code() != first.Code {
		t.Fatal("the receiver did not use the kept identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := vmReceiverListDisks(ctx, "127.0.0.1:"+strconv.Itoa(r.Port()), strings.ReplaceAll(strings.ToLower(first.Thumbprint), ":", ""), first.Code); err != nil {
		t.Fatalf("a sender with the saved thumbprint and code was refused: %v", err)
	}
	renewed, kept, _, err := vmReceiverIdentityFor(path, "hv01", true, testSeal, testSeal)
	if err != nil || kept || renewed.Code == first.Code || renewed.Thumbprint == first.Thumbprint {
		t.Fatal("asking for a new identity must replace both the code and the certificate")
	}
	if again, kept, _, _ := vmReceiverIdentityFor(path, "hv01", false, testSeal, testSeal); !kept || again.Thumbprint != renewed.Thumbprint {
		t.Fatal("the new identity was not kept")
	}
	os.WriteFile(path, []byte("{not json"), 0600)
	if fresh, kept, _, err := vmReceiverIdentityFor(path, "hv01", false, testSeal, testSeal); err != nil || kept || fresh == nil {
		t.Fatal("a damaged identity file must be replaced, not used")
	}
}

func TestReceiverListsItsDefaultRouteAddressFirst(t *testing.T) {
	got := vmPrimaryFirst([]string{"10.10.100.11", "10.10.111.11", "192.168.120.32", "192.168.120.45"}, "192.168.120.32")
	if strings.Join(got, ",") != "192.168.120.32,10.10.100.11,10.10.111.11,192.168.120.45" {
		t.Fatal(got)
	}
	if got := vmPrimaryFirst([]string{"a", "b"}, "c"); strings.Join(got, ",") != "a,b" {
		t.Fatal(got)
	}
}

func TestReachableAddressesKeepsOnlyThoseThatAnswer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := closed.Addr().String()
	closed.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := vmReachableAddresses(ctx, []string{dead, listener.Addr().String()}, 2*time.Second)
	if err != nil || len(got) != 1 || got[0] != listener.Addr().String() {
		t.Fatalf("got %v, %v", got, err)
	}
	many := make([]string, vmReachMax+1)
	for i := range many {
		many[i] = "192.0.2.1:44380"
	}
	if _, err := vmReachableAddresses(ctx, many, time.Second); err == nil {
		t.Fatal("more than the limit must be refused")
	}
	if _, err := vmReachableAddresses(ctx, []string{"a b:44380"}, time.Second); err == nil {
		t.Fatal("a malformed address must be refused")
	}
}

func TestReplicationJobWaitsForARestartingReceiver(t *testing.T) {
	waiting, reason := replReceiverWait(errors.New(`disk 0: Post "https://192.168.120.32:44380/v1/hello": dial tcp 192.168.120.32:44380: connectex: A connection attempt failed`), "192.168.120.32:44380")
	if !waiting || !strings.Contains(reason, "Waiting for the receiver at 192.168.120.32:44380") {
		t.Fatal(waiting, reason)
	}
	waiting, reason = replReceiverWait(errors.New("the receiver's certificate thumbprint is AB:CD, not the one entered"), "hv01:44380")
	if waiting || !strings.Contains(reason, "new thumbprint") {
		t.Fatal(waiting, reason)
	}
	if waiting, reason := replReceiverWait(errors.New("disk full"), "hv01"); waiting || reason != "" {
		t.Fatal(waiting, reason)
	}
}
