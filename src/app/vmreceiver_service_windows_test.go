//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc/mgr"
)

// The receiver service keeps the window receiver's certificate and pairing
// code, so a sender (or replication job) that pinned them before keeps
// working after the application closes or the computer restarts, holds the
// copies a VM runs from, and is removed cleanly.
func TestExecutableRunsTheReceiverAsAServiceWithTheSameIdentity(t *testing.T) {
	exe, err := filepath.Abs(filepath.Join("..", "..", "build", stableExecutableName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatal("Build the GUI executable before tests: ", err)
	}
	if !isAdministrator() {
		t.Skip("installing a service needs administrator rights")
	}
	defer vmRemoveReceiverService()
	folder := t.TempDir()
	identity, err := vmNewReceiverIdentity("test")
	if err != nil {
		t.Fatal(err)
	}
	port := 44388
	holds := []vmReceiverServiceHold{{Names: []string{"held.vhdx"}, Reason: "test failover APP01-TEST"}}
	if err := vmInstallReceiverServiceFrom(exe, vmReceiverServiceConfig{Folder: folder, Port: port, Discoverable: false}, identity, holds); err != nil {
		t.Fatal(err)
	}
	if installed, running := vmReceiverServiceStatus(); !installed || running {
		t.Fatalf("the service must be installed and left stopped until the application closes: installed=%v running=%v", installed, running)
	}
	if err := vmControlReceiverService(true); err != nil {
		t.Fatal(err)
	}
	address := "127.0.0.1:" + strconv.Itoa(port)
	for attempt := 0; attempt < 30; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err = vmReceiverDescribe(ctx, address, identity.Thumbprint, identity.Code)
		cancel()
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("a sender with the window receiver's thumbprint and code was refused by the service: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sink, err := newVMRemoteSink(ctx, address, identity.Thumbprint, identity.Code, "held.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sink.Open("test", 64<<20, 4<<20); !errors.Is(err, errVMReceiverHeld) {
		t.Fatalf("the service must hold the copies a VM runs from: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(vmReceiverServiceDataDir(), "identity.json"))
	if len(data) == 0 || strings.Contains(strings.ToUpper(string(data)), strings.ToUpper(identity.Code)) {
		t.Fatal("the service's identity must be saved sealed")
	}
	if err := vmControlReceiverService(false); err != nil {
		t.Fatal(err)
	}
	if err := vmRemoveReceiverService(); err != nil {
		t.Fatal(err)
	}
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	if service, err := manager.OpenService(vmReceiverServiceName); err == nil {
		service.Close()
		t.Fatal("the service is still installed")
	}
	for _, path := range []string{vmReceiverServiceDataDir(), filepath.Dir(vmReceiverServiceBinary())} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s was left behind", path)
		}
	}
}
