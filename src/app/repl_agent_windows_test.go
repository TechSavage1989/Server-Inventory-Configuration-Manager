//go:build windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// replTestNTFSDisk attaches a new VHDX formatted NTFS and returns its disk
// number and drive letter.
func replTestNTFSDisk(t *testing.T, size int64) (int, string, func()) {
	t.Helper()
	image := filepath.Join(t.TempDir(), "source.vhdx")
	disk, err := createVHDX(image, size)
	if err != nil {
		t.Fatal(err)
	}
	disk.Close()
	quoted := strings.ReplaceAll(image, "'", "''")
	setup := vmPowerShell(t, "$d = Mount-DiskImage -ImagePath '"+quoted+"' -PassThru | Get-Disk; "+
		"Initialize-Disk -Number $d.Number -PartitionStyle GPT; "+
		"$p = New-Partition -DiskNumber $d.Number -UseMaximumSize -AssignDriveLetter; "+
		"Format-Volume -Partition $p -FileSystem NTFS -NewFileSystemLabel ReplTest -Confirm:$false | Out-Null; "+
		"[Console]::Out.Write([string]$d.Number + '|' + $p.DriveLetter)")
	parts := strings.Split(setup, "|")
	if len(parts) != 2 {
		t.Fatalf("unexpected setup output %q", setup)
	}
	number, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	return number, parts[1] + `:\`, func() { vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null") }
}

func replWaitState(t *testing.T, agent *replAgent, id string, done func(replJobView) bool) replJobView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		agent.Tick()
		for _, view := range agent.Jobs() {
			if view.Spec.ID == id && done(view) {
				return view
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatal("the job did not reach the expected state")
	return replJobView{}
}

// A job on the Windows platform replicates a live NTFS disk through shadow
// copies, sends a later change as a delta round, and at cutover stops a
// real service, verifies the copy and leaves the service stopped.
func TestWindowsReplicationJobReplicatesALiveDiskAndCutsOver(t *testing.T) {
	number, letter, detach := replTestNTFSDisk(t, 4<<30)
	defer detach()
	if err := os.WriteFile(filepath.Join(letter, "orders.txt"), []byte("round one"), 0600); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	receiver, err := startVMReceiver("127.0.0.1:0", folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	agent, err := newReplAgent(t.TempDir(), replWindowsPlatform{TrackDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop()
	// The print spooler stands in for an application service.
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	spooler, err := manager.OpenService("Spooler")
	if err != nil {
		t.Skip("the print spooler service is not present:", err)
	}
	defer spooler.Close()
	defer replWindowsPlatform{}.StartServices([]string{"Spooler"}, func(string) {})

	view, err := agent.SaveJob(replJobSpec{
		Name:                 "REPLTEST",
		Disks:                []replDiskMap{{Source: strconv.Itoa(number), Target: "REPLTEST-disk.vhdx"}},
		Receiver:             replReceiverLink{Address: "127.0.0.1:" + strconv.Itoa(receiver.Port()), Thumbprint: receiver.Thumbprint(), Code: receiver.Code()},
		IntervalMinutes:      0,
		Compress:             true,
		StopServices:         []string{"Spooler"},
		ShutdownAfterCutover: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	view = replWaitState(t, agent, id, func(v replJobView) bool { return v.Status.Rounds >= 1 && v.Status.State != replStateSyncing })
	if view.Status.State != replStateIdle || view.Status.Disks[0].Sent <= 0 {
		t.Fatalf("first round: %+v", view.Status)
	}
	if err := os.WriteFile(filepath.Join(letter, "orders.txt"), []byte("round two, after the first copy"), 0600); err != nil {
		t.Fatal(err)
	}
	vmPowerShell(t, "Write-VolumeCache -DriveLetter "+letter[:1])
	if err := agent.Action(id, "sync"); err != nil {
		t.Fatal(err)
	}
	view = replWaitState(t, agent, id, func(v replJobView) bool { return v.Status.Rounds >= 2 && v.Status.State != replStateSyncing })
	first := view.Status.Disks[0]
	// Shadow copy storage and NTFS metadata change too, but far less than
	// the disk.
	if first.Changed == 0 || first.Changed > first.Chunks/8 {
		t.Fatalf("the delta round changed %d chunks", first.Changed)
	}
	if err := agent.Action(id, "cutover"); err != nil {
		t.Fatal(err)
	}
	view = replWaitState(t, agent, id, func(v replJobView) bool {
		return v.Status.State == replStateCutDone || v.Status.State == replStateError
	})
	if view.Status.State != replStateCutDone || !view.Status.Disks[0].Verified {
		t.Fatalf("cutover: %+v", view.Status)
	}
	if status, err := spooler.Query(); err != nil || status.State != svc.Stopped {
		t.Fatalf("the spooler should be stopped after cutover: %v %v", status.State, err)
	}
	// The copy mounts and holds the last data written before cutover.
	target := filepath.Join(folder, "REPLTEST-disk.vhdx")
	quoted := strings.ReplaceAll(target, "'", "''")
	copyLetter := vmPowerShell(t, "$d = Mount-DiskImage -ImagePath '"+quoted+"' -PassThru | Get-Disk; "+
		"$p = Get-Partition -DiskNumber $d.Number | Where-Object Type -eq 'Basic'; "+
		"if (-not $p.DriveLetter) { $p | Add-PartitionAccessPath -AssignDriveLetter; $p = Get-Partition -DiskNumber $d.Number | Where-Object Type -eq 'Basic' }; "+
		"[Console]::Out.Write($p.DriveLetter)")
	defer vmPowerShell(t, "Dismount-DiskImage -ImagePath '"+quoted+"' | Out-Null")
	raw, err := os.ReadFile(copyLetter + `:\orders.txt`)
	if err != nil || string(raw) != "round two, after the first copy" {
		t.Fatalf("the replica holds %q, %v", raw, err)
	}
}

// The real executable installs itself as the agent service, prints the key
// and thumbprint the console needs, answers the console, and uninstalls.
func TestExecutableInstallsTheReplicationAgentService(t *testing.T) {
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
	port := 44391
	out, err := exec.Command(exe, "--agent-install", "--port", strconv.Itoa(port)).CombinedOutput()
	defer exec.Command(exe, "--agent-uninstall").Run()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	key := regexp.MustCompile(`Agent key:\s+([a-z2-7]{32})`).FindSubmatch(out)
	thumbprint := regexp.MustCompile(`Certificate thumbprint:\s+([0-9A-F:]{95})`).FindSubmatch(out)
	if key == nil || thumbprint == nil {
		t.Fatalf("the install did not print the key and thumbprint:\n%s", out)
	}
	config, err := os.ReadFile(filepath.Join(replAgentDataDir(), replConfigName))
	if err != nil || bytes.Contains(config, key[1]) {
		t.Fatalf("the agent settings must hold only the key's hash: %v", err)
	}
	client, err := newReplAgentClient("127.0.0.1:"+strconv.Itoa(port), string(thumbprint[1]), string(key[1]))
	if err != nil {
		t.Fatal(err)
	}
	var hello replAgentHello
	for attempt := 0; attempt < 30; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		hello, err = client.Hello(ctx)
		cancel()
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil || hello.Platform != "Windows" || hello.Version != appVersion {
		t.Fatalf("the service did not answer: %+v %v", hello, err)
	}
	disks, err := client.Disks(context.Background())
	if err != nil || len(disks) == 0 {
		t.Fatalf("disks %v %v", disks, err)
	}
	if out, err := exec.Command(exe, "--agent-uninstall").CombinedOutput(); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	if service, err := manager.OpenService(replServiceName); err == nil {
		service.Close()
		t.Fatal("the service is still installed")
	}
	if _, err := os.Stat(replAgentDataDir()); !os.IsNotExist(err) {
		t.Fatal("the agent's data folder was left behind")
	}
}
