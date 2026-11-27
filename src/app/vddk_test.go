package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// vddkFakeDisk reads a local file in whole sectors, as VixDiskLib_Read does.
type vddkFakeDisk struct {
	file  *os.File
	size  int64
	read  *int64
	mu    *sync.Mutex
	close func()
}

func (d *vddkFakeDisk) Capacity() int64   { return d.size }
func (d *vddkFakeDisk) Transport() string { return "nbdssl" }
func (d *vddkFakeDisk) Close() error {
	if d.close != nil {
		d.close()
	}
	return d.file.Close()
}
func (d *vddkFakeDisk) ReadSectors(start, count uint64, buffer []byte) error {
	if uint64(len(buffer)) < count*vddkSectorSize {
		return errors.New("short buffer")
	}
	if int64(start+count)*vddkSectorSize > d.size {
		return errors.New("read past the end")
	}
	d.mu.Lock()
	*d.read += int64(count) * vddkSectorSize
	d.mu.Unlock()
	_, err := d.file.ReadAt(buffer[:count*vddkSectorSize], int64(start)*vddkSectorSize)
	return err
}

type vddkFakeSession struct {
	open   func(path string) (vddkDisk, error)
	closed *int
}

func (s *vddkFakeSession) Open(path string) (vddkDisk, error) { return s.open(path) }
func (s *vddkFakeSession) Close() error                       { *s.closed++; return nil }

// Byte reads at any offset, length and edge come back exactly, in whole
// sector reads.
func TestVDDKSourceReadsAnyRangeInWholeSectors(t *testing.T) {
	size := int64(3<<20 + 1536)
	data := make([]byte, size)
	rand.Read(data)
	path := filepath.Join(t.TempDir(), "disk.raw")
	os.WriteFile(path, data, 0600)
	file, _ := os.Open(path)
	var read int64
	source := newVDDKSource("[ds] vm/vm.vmdk", &vddkFakeDisk{file: file, size: size, read: &read, mu: &sync.Mutex{}})
	defer source.Close()
	if source.Size() != size || source.Describe() != "[ds] vm/vm.vmdk" {
		t.Fatalf("size %d name %s", source.Size(), source.Describe())
	}
	for _, c := range []struct{ off, n int64 }{{0, 4096}, {1, 1}, {511, 2}, {700, 3000}, {1 << 20, 2 << 20}, {size - 100, 100}, {size - 512, 512}} {
		got := make([]byte, c.n)
		n, err := source.ReadAt(got, c.off)
		if err != nil || int64(n) != c.n || !bytes.Equal(got, data[c.off:c.off+c.n]) {
			t.Fatalf("read %d at %d: %d bytes, %v", c.n, c.off, n, err)
		}
	}
	got := make([]byte, 1000)
	if n, err := source.ReadAt(got, size-200); n != 200 || err != io.EOF || !bytes.Equal(got[:200], data[size-200:]) {
		t.Fatalf("a read past the end gave %d bytes and %v", n, err)
	}
	if _, err := source.ReadAt(got, size); err != io.EOF {
		t.Fatalf("a read at the end gave %v", err)
	}
}

func TestVDDKFolderSettings(t *testing.T) {
	for _, bad := range []string{"VDDK", `C:\VDDK\..\Windows`, `relative\bin`} {
		if _, err := cleanVDDKFolder(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	if folder, err := cleanVDDKFolder(`  C:\VDDK\vix-disklib\  `); err != nil || folder != filepath.Clean(`C:\VDDK\vix-disklib`) {
		t.Fatalf("got %q, %v", folder, err)
	}
	if folder, err := cleanVDDKFolder(""); err != nil || folder != "" {
		t.Fatalf("an empty folder must turn VDDK off: %q %v", folder, err)
	}
	dir := t.TempDir()
	if loadVDDKSettings(dir).Folder != "" {
		t.Fatal("no file must mean off")
	}
	if err := saveVDDKSettings(dir, vddkSettings{Folder: `D:\VDDK`}); err != nil {
		t.Fatal(err)
	}
	if got := loadVDDKSettings(dir).Folder; got != filepath.Clean(`D:\VDDK`) {
		t.Fatalf("saved folder read back as %q", got)
	}
	os.WriteFile(vddkSettingsPath(dir), []byte(`{"schema":1,"folder":"..\\x"}`), 0600)
	if loadVDDKSettings(dir).Folder != "" {
		t.Fatal("a bad saved folder must be ignored")
	}

	root := t.TempDir()
	if _, err := vddkBinFolder(root); err == nil {
		t.Fatal("a folder without vixDiskLib.dll was accepted")
	}
	os.MkdirAll(filepath.Join(root, "bin"), 0700)
	os.WriteFile(filepath.Join(root, "bin", "vixDiskLib.dll"), []byte("x"), 0600)
	if bin, err := vddkBinFolder(root); err != nil || bin != filepath.Join(root, "bin") {
		t.Fatalf("the bin folder was not found: %q %v", bin, err)
	}
	for _, v := range []struct {
		major, minor, patch uint16
		ok                  bool
	}{{6, 7, 3, false}, {7, 0, 2, false}, {7, 0, 3, true}, {8, 0, 3, true}, {9, 1, 0, true}} {
		if vddkSupportedVersion(v.major, v.minor, v.patch) != v.ok {
			t.Fatalf("version %+v", v)
		}
	}
	if err := vddkErrorText(0x10000000000003e8, ""); !strings.Contains(err.Error(), "1000") {
		t.Fatalf("error text %v", err)
	}
}

// With a VDDK folder set, a round opens its own snapshot through VDDK, never
// asks the datastore, and a changed-block round reads only the changed
// blocks. When VDDK cannot connect, the round reads the datastore as before.
func TestReplicationReadsChangedBlocksThroughVDDK(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	server := model.Service.NewServer()
	defer server.Close()
	ctx := context.Background()
	client, err := vim25.NewClient(ctx, soap.NewClient(server.URL, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewManager(client).Login(ctx, server.URL.User); err != nil {
		t.Fatal(err)
	}
	size := int64(80 << 20)
	ref, flatPath, data := replicaTestVM(t, ctx, client, size)
	simVM := simulator.Map.Get(ref).(*simulator.VirtualMachine)
	changeIDs := 0
	setChangeID := func() {
		changeIDs++
		for _, device := range simVM.Config.Hardware.Device {
			if disk, ok := device.(*types.VirtualDisk); ok {
				disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo).ChangeId = "52 aa bb/" + strconv.Itoa(changeIDs)
			}
		}
	}
	setChangeID()
	changed := []types.DiskChangeExtent{}
	originalAreas, originalOpen := vmChangedAreas, vddkOpenSession
	defer func() { vmChangedAreas, vddkOpenSession = originalAreas, originalOpen }()
	vmChangedAreas = func(ctx context.Context, client *vim25.Client, vm, snapshot types.ManagedObjectReference, key int32, offset int64, changeID string) (types.DiskChangeInfo, error) {
		if changeID == "*" {
			return types.DiskChangeInfo{StartOffset: 0, Length: size, ChangedArea: []types.DiskChangeExtent{{Start: 0, Length: size}}}, nil
		}
		return types.DiskChangeInfo{StartOffset: 0, Length: size, ChangedArea: changed}, nil
	}
	var read int64
	var mu sync.Mutex
	var connects []vddkConnect
	var opened []string
	sessionsClosed, disksOpen := 0, 0
	failConnect := false
	vddkOpenSession = func(ctx context.Context, folder string, params vddkConnect) (vddkSession, error) {
		if folder != `C:\VDDK` {
			t.Errorf("VDDK opened from %q", folder)
		}
		connects = append(connects, params)
		if failConnect {
			return nil, errors.New("port 902 refused")
		}
		return &vddkFakeSession{closed: &sessionsClosed, open: func(path string) (vddkDisk, error) {
			opened = append(opened, path)
			file, err := os.Open(flatPath)
			if err != nil {
				return nil, err
			}
			disksOpen++
			return &vddkFakeDisk{file: file, size: size, read: &read, mu: &mu, close: func() { disksOpen-- }}, nil
		}}, nil
	}

	folder := filepath.Join(t.TempDir(), "replica")
	var logs []string
	options := vmReplicaOptions{Datacenter: "ha-datacenter", Folder: folder, EnableCBT: true, Verify: true,
		VDDK: &vddkConnect{Server: "vc.example.com", User: "migrate@vsphere.local", Password: "secret", Thumbprint: "AA:BB"}, VDDKFolder: `C:\VDDK`,
		Log: func(line string) { logs = append(logs, line) }}
	first, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(connects) != 1 || connects[0].VMRef != ref.Value || !strings.HasPrefix(connects[0].Snapshot, "snapshot-") || connects[0].Password != "secret" {
		t.Fatalf("VDDK was not connected to the round's snapshot: %+v", connects)
	}
	if len(opened) != 1 || !strings.HasSuffix(opened[0], "] app01/app01.vmdk") {
		t.Fatalf("VDDK opened %v", opened)
	}
	if !bytes.Equal(vmReadVHDX(t, first.Disks[0].Target), data) || sessionsClosed != 1 || disksOpen != 0 {
		t.Fatalf("the VDDK copy differs or was left open (sessions closed %d, disks open %d)", sessionsClosed, disksOpen)
	}
	for _, line := range logs {
		if strings.Contains(line, "secret") {
			t.Fatalf("the password reached the log: %s", line)
		}
	}
	if !strings.Contains(strings.Join(logs, "\n"), "through VDDK") {
		t.Fatalf("the log does not say VDDK was used: %v", logs)
	}

	// One sector changes; the next round reads only that chunk.
	rand.Read(data[60<<20 : 60<<20+512])
	os.WriteFile(flatPath, data, 0600)
	changed = []types.DiskChangeExtent{{Start: 60 << 20, Length: 512}}
	setChangeID()
	read = 0
	second, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disks[0].Mode != "changed" || second.Disks[0].Changed != 1 || !bytes.Equal(vmReadVHDX(t, second.Disks[0].Target), data) {
		t.Fatalf("the VDDK delta round was wrong: %+v", second.Disks[0])
	}
	if read > vmCopyChunkSize {
		t.Fatalf("the delta round read %d bytes through VDDK, more than the one changed chunk", read)
	}

	// VDDK cannot connect: the round says so and reads the datastore.
	failConnect = true
	rand.Read(data[10<<20 : 10<<20+512])
	os.WriteFile(flatPath, data, 0600)
	changed = []types.DiskChangeExtent{{Start: 10 << 20, Length: 512}}
	setChangeID()
	logs = nil
	third, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(vmReadVHDX(t, third.Disks[0].Target), data) || third.Disks[0].Mode != "changed" {
		t.Fatalf("the fallback round was wrong: %+v", third.Disks[0])
	}
	if !strings.Contains(strings.Join(logs, "\n"), "port 902 refused") {
		t.Fatalf("the log does not give VDDK's reason: %v", logs)
	}
}
