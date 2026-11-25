//go:build linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func replLinuxSh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// replLinuxTestDisk makes a loop disk with a 64 MB ext4 partition and an
// LVM partition holding a 300 MB ext4 logical volume, mounted.
func replLinuxTestDisk(t *testing.T) (disk, vg, mount string, cleanup func()) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("LVM tests need root")
	}
	for _, tool := range []string{"lvcreate", "sfdisk", "mkfs.ext4", "debugfs", "losetup"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	if _, err := os.Stat("/dev/mapper/control"); err != nil {
		t.Skip("this kernel has no device-mapper")
	}
	os.Setenv("SIMM_AGENT_LOOP", "1")
	folder := t.TempDir()
	image := filepath.Join(folder, "disk.img")
	replLinuxSh(t, "truncate", "-s", "700M", image)
	cmd := exec.Command("sfdisk", "-q", image)
	cmd.Stdin = strings.NewReader("label: gpt\n,64M\n,\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sfdisk: %v %s", err, out)
	}
	device := replLinuxSh(t, "losetup", "-fP", "--show", image)
	disk = filepath.Base(device)
	exec.Command("udevadm", "settle").Run()
	vg = fmt.Sprintf("simmtest%d", time.Now().UnixNano()%1000000)
	replLinuxSh(t, "mkfs.ext4", "-q", "-F", device+"p1")
	replLinuxSh(t, "pvcreate", "-q", "-y", device+"p2")
	replLinuxSh(t, "vgcreate", "-q", vg, device+"p2")
	replLinuxSh(t, "lvcreate", "-q", "-y", "-L", "300M", "-n", "data", vg)
	replLinuxSh(t, "mkfs.ext4", "-q", "-F", "/dev/"+vg+"/data")
	mount = filepath.Join(folder, "mnt")
	os.MkdirAll(mount, 0700)
	replLinuxSh(t, "mount", "/dev/"+vg+"/data", mount)
	return disk, vg, mount, func() {
		exec.Command("umount", mount).Run()
		exec.Command("vgremove", "-f", vg).Run()
		exec.Command("pvremove", "-f", device+"p2").Run()
		exec.Command("losetup", "-d", device).Run()
	}
}

// replLinuxLVBytes reads the logical volume's bytes out of a copied disk
// image, through the same linear segments the live volume uses.
func replLinuxLVBytes(t *testing.T, copy []byte, disk, vg string) []byte {
	t.Helper()
	lvs, err := replLinuxLVs()
	if err != nil {
		t.Fatal(err)
	}
	disks := map[string]replLinuxDisk{}
	disks[disk], _ = replLinuxReadDisk(disk)
	for _, lv := range lvs {
		if lv.VG != vg || lv.LV != "data" {
			continue
		}
		out := make([]byte, lv.Size)
		for _, segment := range lv.Segments {
			_, at, ok := replLinuxLocate(disks, segment.Device, segment.Offset)
			if !ok {
				t.Fatal("the volume is not on the test disk")
			}
			copyBytes := copy[at : at+segment.Length]
			copyInto := out[segment.Start : segment.Start+segment.Length]
			copyN := copyIntoSlice(copyInto, copyBytes)
			if copyN != len(copyInto) {
				t.Fatal("short segment")
			}
		}
		return out
	}
	t.Fatal("the logical volume was not found")
	return nil
}

func copyIntoSlice(to, from []byte) int { return copy(to, from) }

func replLinuxDebugfsCat(t *testing.T, image []byte, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lv.img")
	if err := os.WriteFile(path, image, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("debugfs", "-R", "cat /"+name, path).Output()
	if err != nil {
		t.Fatalf("debugfs: %v", err)
	}
	return string(out)
}

// A logical volume is read from an LVM snapshot taken for the round: a
// write made after the snapshot stays out of the copy, the copied LVM
// metadata never mentions the snapshot, and the snapshot is removed when
// the round closes.
func TestLinuxAgentCopiesAnLVMVolumeFromASnapshot(t *testing.T) {
	disk, vg, mount, cleanup := replLinuxTestDisk(t)
	defer cleanup()
	if err := os.WriteFile(filepath.Join(mount, "ledger.txt"), []byte("before the snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	replLinuxSh(t, "sync")
	platform := replLinuxPlatform{}
	disks, err := platform.ListDisks()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, info := range disks {
		for _, volume := range info.Volumes {
			if info.ID == disk && volume.Kind == "lvm" && volume.Name == "/dev/"+vg+"/data" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the disk list does not show the logical volume: %+v", disks)
	}
	job := "0123456789abcdef"
	opened, err := platform.OpenDisks(context.Background(), job, []string{disk}, false, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	if out := replLinuxSh(t, "lvs", "--noheadings", "-o", "lv_name", vg); !strings.Contains(out, replSnapshotName(job, 0)) {
		opened.Close()
		t.Fatalf("no snapshot during the round: %s", out)
	}
	if err := os.WriteFile(filepath.Join(mount, "ledger.txt"), []byte("after the snapshot, must not be copied"), 0600); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	replLinuxSh(t, "sync")
	target := filepath.Join(t.TempDir(), "copy.vhdx")
	if _, err := vmCopyDisk(context.Background(), opened.Sources[disk], target, vmCopyOptions{Verify: true}); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	opened.Close()
	if out := replLinuxSh(t, "lvs", "--noheadings", "-o", "lv_name", vg); strings.Contains(out, "simmsnap") {
		t.Fatalf("the snapshot was left behind: %s", out)
	}
	copy := vmReadVHDX(t, target)
	if bytes.Contains(copy[:64<<20+4<<20], []byte("simmsnap")) || bytes.Contains(copy, []byte("simmsnap_"+job)) {
		t.Fatal("the copied LVM metadata mentions the snapshot")
	}
	if got := replLinuxDebugfsCat(t, replLinuxLVBytes(t, copy, disk, vg), "ledger.txt"); got != "before the snapshot" {
		t.Fatalf("the copied volume holds %q", got)
	}
}

// A replication job on Linux sends rounds to a receiver and cuts over.
func TestLinuxReplicationJobRunsAndCutsOver(t *testing.T) {
	disk, vg, mount, cleanup := replLinuxTestDisk(t)
	defer cleanup()
	os.WriteFile(filepath.Join(mount, "orders.txt"), []byte("round one"), 0600)
	replLinuxSh(t, "sync")
	folder := t.TempDir()
	receiver, err := startVMReceiver("127.0.0.1:0", folder, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	agent, err := newReplAgent(t.TempDir(), replLinuxPlatform{})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Stop()
	view, err := agent.SaveJob(replJobSpec{Name: "LINUXTEST", Disks: []replDiskMap{{Source: disk, Target: "linux-disk.vhdx"}},
		Receiver: replReceiverLink{Address: "127.0.0.1:" + strconv.Itoa(receiver.Port()), Thumbprint: receiver.Thumbprint(), Code: receiver.Code()}, Compress: true})
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	wait := func(done func(replJobView) bool) replJobView {
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			agent.Tick()
			for _, v := range agent.Jobs() {
				if v.Spec.ID == id && done(v) {
					return v
				}
			}
			time.Sleep(time.Second)
		}
		t.Fatal("the job did not reach the expected state")
		return replJobView{}
	}
	view = wait(func(v replJobView) bool { return v.Status.Rounds >= 1 && v.Status.State != replStateSyncing })
	if view.Status.State != replStateIdle {
		t.Fatalf("first round: %+v", view.Status)
	}
	os.WriteFile(filepath.Join(mount, "orders.txt"), []byte("round two"), 0600)
	replLinuxSh(t, "sync")
	if err := agent.Action(id, "cutover"); err != nil {
		t.Fatal(err)
	}
	view = wait(func(v replJobView) bool {
		return v.Status.State == replStateCutDone || v.Status.State == replStateError
	})
	if view.Status.State != replStateCutDone || !view.Status.Disks[0].Verified {
		t.Fatalf("cutover: %+v", view.Status)
	}
	copy := vmReadVHDX(t, filepath.Join(folder, "linux-disk.vhdx"))
	if got := replLinuxDebugfsCat(t, replLinuxLVBytes(t, copy, disk, vg), "orders.txt"); got != "round two" {
		t.Fatalf("the replica holds %q", got)
	}
}

// The real Linux agent binary installs itself as a systemd service, prints
// the key and thumbprint, answers the console, and uninstalls.
func TestLinuxExecutableInstallsTheAgentService(t *testing.T) {
	binary, err := filepath.Abs(filepath.Join("..", "..", "build", "simm-agent-linux-amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skip("build the Linux agent first: ", err)
	}
	if os.Geteuid() != 0 {
		t.Skip("installing a service needs root")
	}
	// A freshly started runner can still be booting ("starting"): wait for
	// systemd to finish, a few minutes at most.
	wait, cancelWait := context.WithTimeout(context.Background(), 3*time.Minute)
	out, err := exec.CommandContext(wait, "systemctl", "is-system-running", "--wait").CombinedOutput()
	cancelWait()
	if err != nil && !strings.Contains(string(out), "degraded") && !strings.Contains(string(out), "running") {
		t.Skip("systemd is not running: ", strings.TrimSpace(string(out)))
	}
	port := 44392
	out, err = exec.Command(binary, "--agent-install", "--port", strconv.Itoa(port)).CombinedOutput()
	defer exec.Command(binary, "--agent-uninstall").Run()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	key := regexp.MustCompile(`Agent key:\s+([a-z2-7]{32})`).FindSubmatch(out)
	thumbprint := regexp.MustCompile(`Certificate thumbprint:\s+([0-9A-F:]{95})`).FindSubmatch(out)
	if key == nil || thumbprint == nil {
		t.Fatalf("the install did not print the key and thumbprint:\n%s", out)
	}
	if info, err := os.Stat(replLinuxDataDir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("the data folder must be private: %v %v", info, err)
	}
	config, _ := os.ReadFile(filepath.Join(replLinuxDataDir, replConfigName))
	if bytes.Contains(config, key[1]) {
		t.Fatal("the agent settings must hold only the key's hash")
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
		time.Sleep(time.Second)
	}
	if err != nil || hello.Platform != "Linux" {
		t.Fatalf("the service did not answer: %+v %v\n%s", hello, err, replLinuxSh(t, "journalctl", "-u", replLinuxUnit, "--no-pager", "-n", "30"))
	}
	if out, err := exec.Command(binary, "--agent-uninstall").CombinedOutput(); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	for _, path := range []string{replLinuxUnitPath, replLinuxDataDir, replLinuxBinary} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s was left behind", path)
		}
	}
}
