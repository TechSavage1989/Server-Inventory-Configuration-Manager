//go:build windows

package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func vmFakeADK(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "ADK")
	for _, name := range []string{
		`Windows Preinstallation Environment\amd64\en-us\winpe.wim`,
		`Windows Preinstallation Environment\amd64\Media\bootmgr`,
		`Deployment Tools\amd64\Oscdimg\oscdimg.exe`,
		`Deployment Tools\amd64\Oscdimg\etfsboot.com`,
		`Deployment Tools\amd64\Oscdimg\efisys_noprompt.bin`,
	} {
		path := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(path), 0700)
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestReceiverMediaPlanChecksTheADKDriversAndOutputs(t *testing.T) {
	folder := t.TempDir()
	adk := vmFakeADK(t)
	exe := filepath.Join(folder, "receiver.exe")
	os.WriteFile(exe, []byte("MZ"), 0600)
	drivers := filepath.Join(folder, "virtio")
	os.MkdirAll(filepath.Join(drivers, "viostor", "amd64"), 0700)
	os.WriteFile(filepath.Join(drivers, "viostor", "amd64", "viostor.inf"), []byte("[Version]"), 0600)
	ctx := context.Background()
	options := vmReceiverMediaOptions{Output: filepath.Join(folder, "receiver.iso"), Work: filepath.Join(folder, "work"), Drivers: []string{drivers}, Target: vmTargetProxmox, AdkRoot: adk, Plan: true}

	result, err := vmBuildReceiverMedia(ctx, powershellPath(), exe, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || len(result.Problems) != 0 || result.ReceiverArguments != "--receiver --disks blank --port 8443" {
		t.Fatalf("plan %+v", result)
	}

	// Missing ADK, an existing ISO, a busy work folder, an empty driver folder
	// and unsafe disk lists are all reported before anything is built.
	os.WriteFile(options.Output, []byte("old"), 0600)
	os.MkdirAll(options.Work, 0700)
	os.WriteFile(filepath.Join(options.Work, "keep.txt"), []byte("x"), 0600)
	empty := filepath.Join(folder, "empty")
	os.MkdirAll(empty, 0700)
	bad := options
	bad.AdkRoot = filepath.Join(folder, "no-adk")
	bad.Drivers = []string{drivers, empty}
	bad.Disks = "1;format c:"
	result, err = vmBuildReceiverMedia(ctx, powershellPath(), exe, bad, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Problems, "\n")
	for _, want := range []string{"Deployment Tools", "Windows PE add-on", "already exists", "not empty", "no .inf", "Disks must be"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("problem %q missing from %q", want, joined)
		}
	}
	if result.OK {
		t.Fatal("a plan with problems was reported as ready")
	}
	if _, err := os.Stat(filepath.Join(options.Work, "mount")); err == nil {
		t.Fatal("a refused build touched the work folder")
	}
	if _, err := vmBuildReceiverMedia(ctx, powershellPath(), exe, vmReceiverMediaOptions{Output: "x.iso", Work: "w", Target: vmTargetAzure}, nil); err == nil {
		t.Fatal("receiver media was offered for Azure")
	}
	if _, err := vmBuildReceiverMedia(ctx, powershellPath(), exe, vmReceiverMediaOptions{Output: "x.iso", Work: "w", Drivers: []string{"a|b"}}, nil); err == nil {
		t.Fatal("a driver path with | was accepted")
	}
}

// The real executable runs the receiver without its window and prints what
// the sender needs, and lists disks with the system disk protected.
func TestExecutableRunsTheReceiverFromTheCommandLine(t *testing.T) {
	exe, err := filepath.Abs(filepath.Join("..", "..", "build", stableExecutableName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatal("Build the GUI executable before tests: ", err)
	}
	records := t.TempDir()
	listed, err := exec.Command(exe, "--receiver", "--list-disks", "--records", records).CombinedOutput()
	if err != nil {
		t.Fatalf("--list-disks failed: %v\n%s", err, listed)
	}
	if !strings.Contains(string(listed), "Disks on this computer") || !strings.Contains(string(listed), "this computer's Windows") {
		t.Fatalf("listing:\n%s", listed)
	}

	incoming := t.TempDir()
	command := exec.Command(exe, "--receiver", "--folder", incoming, "--port", "0", "--records", records)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	var code, thumbprint string
	deadline := time.After(30 * time.Second)
	for code == "" || thumbprint == "" {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("the receiver exited before printing its pairing details")
			}
			if strings.Contains(line, "Pairing code:") {
				code = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			}
			if strings.Contains(line, "Thumbprint:") {
				thumbprint = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			}
		case <-deadline:
			t.Fatal("the receiver did not print its pairing details")
		}
	}
	if len(strings.ReplaceAll(code, "-", "")) != 20 || vmNormalizeThumbprint(thumbprint) == "" {
		t.Fatalf("code %q thumbprint %q", code, thumbprint)
	}
	log, _ := os.ReadFile(filepath.Join(records, "receiver.log"))
	if strings.Contains(string(log), code) || !strings.Contains(string(log), "Receiving into") {
		t.Fatalf("the log holds the pairing code or misses the start: %s", log)
	}
	// Disks it may not write are refused before it listens.
	refused, err := exec.Command(exe, "--receiver", "--disks", "1", "--overwrite-disks", "2", "--records", records).CombinedOutput()
	if err == nil || !strings.Contains(string(refused), "not in --disks") {
		t.Fatalf("an overwrite outside --disks was accepted: %v\n%s", err, refused)
	}
}
