package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// vmStreamBytes writes data as a stream-optimized VMDK in memory.
func vmStreamBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	raw := vmWriteTestFile(t, filepath.Join(t.TempDir(), "source.raw"), data)
	source, err := openDiskImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var out bytes.Buffer
	if _, err := vmWriteStreamOptimized(context.Background(), source, &out, "disk.vmdk", nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// A stream-optimized VMDK read as it arrives gives the source's bytes when
// read in order, with a retried read of the same chunk, and with a read
// further back (which opens the stream again).
func TestStreamVMDKReadsInOrderAndReopensForEarlierReads(t *testing.T) {
	data := vmConvertTestData(t)
	stream := vmStreamBytes(t, data)
	opens := 0
	open := func() (io.ReadCloser, error) {
		opens++
		return io.NopCloser(bytes.NewReader(stream)), nil
	}
	body, _ := open()
	reader, err := openVMStreamVMDK("disk.vmdk", body, open)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if reader.Size() != int64(len(data)) {
		t.Fatalf("the stream says %d bytes, the source has %d", reader.Size(), len(data))
	}
	got := make([]byte, len(data))
	for off := 0; off < len(data); off += vmCopyChunkSize {
		end := off + vmCopyChunkSize
		if end > len(data) {
			end = len(data)
		}
		if n, err := reader.ReadAt(got[off:end], int64(off)); err != nil || n != end-off {
			t.Fatalf("reading at %d: %d bytes, %v", off, n, err)
		}
		// The copy engine retries a failed read at the same offset.
		again := make([]byte, end-off)
		if _, err := reader.ReadAt(again, int64(off)); err != nil || !bytes.Equal(again, got[off:end]) {
			t.Fatalf("a repeated read at %d differed: %v", off, err)
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatal("the stream read back different bytes")
	}
	if opens != 1 {
		t.Fatalf("reading in order opened the stream %d times", opens)
	}
	// Only some chunks, skipping forward (a changed-blocks round).
	early := make([]byte, 4096)
	if _, err := reader.ReadAt(early, 1<<20); err != nil || !bytes.Equal(early, data[1<<20:1<<20+4096]) {
		t.Fatalf("an earlier read after the end gave the wrong bytes: %v", err)
	}
	if opens != 2 {
		t.Fatalf("a read far behind did not open the stream again (%d opens)", opens)
	}
	late := make([]byte, 512)
	if _, err := reader.ReadAt(late, 50<<20+4096); err != nil || !bytes.Equal(late, data[50<<20+4096:50<<20+4608]) {
		t.Fatalf("a forward skip gave the wrong bytes: %v", err)
	}

	// Without a way to open it again, a read far back is refused.
	once, err := openVMStreamVMDK("disk.vmdk", io.NopCloser(bytes.NewReader(stream)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := once.ReadAt(make([]byte, 512), 60<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := once.ReadAt(make([]byte, 512), 0); err == nil {
		t.Fatal("a stream that cannot be opened again was read backwards")
	}

	// A cut-off stream is an error, never zeros.
	cut, err := openVMStreamVMDK("disk.vmdk", io.NopCloser(bytes.NewReader(stream[:len(stream)/2])), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cut.ReadAt(make([]byte, 4096), int64(len(data))-4096); err == nil {
		t.Fatal("a cut-off stream read as complete")
	}
	if _, err := openVMStreamVMDK("x", io.NopCloser(bytes.NewReader(data[:4096])), nil); err == nil {
		t.Fatal("bytes that are not a VMDK stream were accepted")
	}
}

func TestExportKeysNameTheirDisks(t *testing.T) {
	disk := vmReplicaDisk{key: 2000, label: "Hard disk 1", slot: "VirtualLsiLogicSASController0:0"}
	for _, key := range []string{"/vm-1095/VirtualLsiLogicSASController0:0", "VirtualLsiLogicSASController0:0", "2000", "/vm-1/2000"} {
		if !vmExportKeyMatches(key, disk) {
			t.Fatalf("%q did not match the disk", key)
		}
	}
	for _, key := range []string{"/vm-1095/VirtualLsiLogicSASController0:1", "20000", ""} {
		if vmExportKeyMatches(key, disk) {
			t.Fatalf("%q matched the wrong disk", key)
		}
	}
	two := &vmFakeExport{disks: []vmExportDisk{{Key: "/vm-1/a"}, {Key: "/vm-1/b"}}}
	if _, err := vmExportDiskFor(two, disk, 2); err == nil {
		t.Fatal("a disk was guessed among several export URLs")
	}
	one := &vmFakeExport{disks: []vmExportDisk{{Key: "/vm-1/a"}}}
	if _, err := vmExportDiskFor(one, disk, 1); err != nil {
		t.Fatalf("a VM with one disk did not use its only export URL: %v", err)
	}
}

type vmFakeExport struct {
	disks []vmExportDisk
	body  func() []byte
	mu    sync.Mutex
	ended []bool
	read  int64
}

func (e *vmFakeExport) Disks() []vmExportDisk { return e.disks }
func (e *vmFakeExport) Read(n int64)          { e.mu.Lock(); e.read += n; e.mu.Unlock() }
func (e *vmFakeExport) End(ok bool)           { e.mu.Lock(); e.ended = append(e.ended, ok); e.mu.Unlock() }
func (e *vmFakeExport) Open(ctx context.Context, disk vmExportDisk) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(e.body())), nil
}

// A host that refuses a running VM's flat disk over /folder (MATCHC-DC01:
// "the server answered 500 Internal Server Error") gets the round's
// snapshot read through vSphere's export instead, and later rounds stay
// delta passes of the same copy.
func TestReplicationReadsTheSnapshotExportWhenTheDatastoreRefusesTheDisk(t *testing.T) {
	model := simulator.ESX()
	defer model.Remove()
	if err := model.Create(); err != nil {
		t.Fatal(err)
	}
	server := model.Service.NewServer()
	defer server.Close()

	// The host answers 500 for the flat extent, as ESXi does for a disk the
	// running VM holds open; the descriptor still reads.
	backend, _ := url.Parse(server.URL.String())
	backend.Path, backend.User = "", nil
	proxy := httputil.NewSingleHostReverseProxy(backend)
	refused := 0
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "-flat.vmdk") {
			refused++
			http.Error(w, "locked", http.StatusInternalServerError)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer front.Close()
	frontURL, _ := url.Parse(front.URL + "/sdk")

	ctx := context.Background()
	client, err := vim25.NewClient(ctx, soap.NewClient(frontURL, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewManager(client).Login(ctx, server.URL.User); err != nil {
		t.Fatal(err)
	}
	size := int64(80 << 20)
	ref, flatPath, data := replicaTestVM(t, ctx, client, size)
	simVM := simulator.Map.Get(ref).(*simulator.VirtualMachine)
	var diskKey int32
	for _, device := range simVM.Config.Hardware.Device {
		if disk, ok := device.(*types.VirtualDisk); ok {
			diskKey = disk.Key
			disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo).ChangeId = "52 aa bb/1"
		}
	}

	original, originalAreas := vmOpenSnapshotExport, vmChangedAreas
	defer func() { vmOpenSnapshotExport, vmChangedAreas = original, originalAreas }()
	changed := []types.DiskChangeExtent{}
	vmChangedAreas = func(ctx context.Context, client *vim25.Client, vm, snapshot types.ManagedObjectReference, key int32, offset int64, changeID string) (types.DiskChangeInfo, error) {
		if changeID == "*" {
			return types.DiskChangeInfo{StartOffset: 0, Length: size, ChangedArea: []types.DiskChangeExtent{{Start: 0, Length: size}}}, nil
		}
		return types.DiskChangeInfo{StartOffset: 0, Length: size, ChangedArea: changed}, nil
	}
	exports := []*vmFakeExport{}
	var snapshotsAtStart []int
	vmOpenSnapshotExport = func(ctx context.Context, client *vim25.Client, snapshot types.ManagedObjectReference, total int64, log func(string)) (vmSnapshotExport, error) {
		var machine mo.VirtualMachine
		property.DefaultCollector(client).RetrieveOne(ctx, ref, []string{"snapshot"}, &machine)
		count := 0
		if machine.Snapshot != nil {
			count = len(machine.Snapshot.RootSnapshotList)
		}
		snapshotsAtStart = append(snapshotsAtStart, count)
		current, _ := os.ReadFile(flatPath)
		stream := vmStreamBytes(t, current)
		// Two URLs, so the disk must be found by its key.
		export := &vmFakeExport{disks: []vmExportDisk{{Key: "/" + ref.Value + "/VirtualIDEController1:0"}, {Key: "/" + ref.Value + "/" + strconv.Itoa(int(diskKey))}}, body: func() []byte { return stream }}
		exports = append(exports, export)
		return export, nil
	}

	folder := filepath.Join(t.TempDir(), "replica")
	var logs []string
	options := vmReplicaOptions{Datacenter: "ha-datacenter", Folder: folder, EnableCBT: true, Verify: true, Log: func(line string) { logs = append(logs, line) }}
	first, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if refused == 0 || len(exports) != 1 || snapshotsAtStart[0] != 1 {
		t.Fatalf("the round did not fall back to the snapshot export (refused %d, exports %d, snapshots %v)", refused, len(exports), snapshotsAtStart)
	}
	if !first.Disks[0].Verified || !bytes.Equal(vmReadVHDX(t, first.Disks[0].Target), data) {
		t.Fatal("the copy read through the export differs from the VM disk")
	}
	if len(exports[0].ended) != 1 || !exports[0].ended[0] {
		t.Fatalf("the export was not completed once: %v", exports[0].ended)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "export") {
		t.Fatalf("the log does not say the export was used: %v", logs)
	}
	var machine mo.VirtualMachine
	property.DefaultCollector(client).RetrieveOne(ctx, ref, []string{"snapshot"}, &machine)
	if machine.Snapshot != nil && len(machine.Snapshot.RootSnapshotList) > 0 {
		t.Fatal("the replication snapshot was left behind")
	}

	// The guest writes; the next round is a delta pass of the same copy.
	rand.Read(data[50<<20 : 50<<20+512])
	os.WriteFile(flatPath, data, 0600)
	changed = []types.DiskChangeExtent{{Start: 50 << 20, Length: 512}}
	for _, device := range simVM.Config.Hardware.Device {
		if disk, ok := device.(*types.VirtualDisk); ok {
			disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo).ChangeId = "52 aa bb/2"
		}
	}
	second, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disks[0].Mode != "changed" || second.Disks[0].Changed != 1 || !bytes.Equal(vmReadVHDX(t, second.Disks[0].Target), data) {
		t.Fatalf("the second round through the export was not a correct delta pass: %+v", second.Disks[0])
	}

	// When the export cannot start either, the round fails with both reasons.
	vmOpenSnapshotExport = func(ctx context.Context, client *vim25.Client, snapshot types.ManagedObjectReference, total int64, log func(string)) (vmSnapshotExport, error) {
		return nil, errors.New("no export privilege")
	}
	if _, err := vmReplicateRound(ctx, client, ref, options); err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "no export privilege") {
		t.Fatalf("a failed fallback did not give both reasons: %v", err)
	}

	// A CD/DVD drive naming an ISO that no longer exists makes vSphere's
	// export fail (EPICHyperspaceLocal, 2026-11-26). Without the tick the
	// error says how to fix it; with it the round sets the drive to a client
	// device first, records the ISO, and the export works.
	vmObject := object.NewVirtualMachine(client, ref)
	devices, err := vmObject.Device(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ide, err := devices.FindIDEController("")
	if err != nil {
		t.Fatal(err)
	}
	cdrom, err := devices.CreateCdrom(ide)
	if err != nil {
		t.Fatal(err)
	}
	iso := "[LocalDS_0] ISO Files/missing.iso"
	cdrom = devices.InsertIso(cdrom, iso)
	cdrom.Connectable = &types.VirtualDeviceConnectInfo{StartConnected: true, AllowGuestControl: true}
	if err := vmObject.AddDevice(ctx, cdrom); err != nil {
		t.Fatal(err)
	}
	vmOpenSnapshotExport = func(ctx context.Context, client *vim25.Client, snapshot types.ManagedObjectReference, total int64, log func(string)) (vmSnapshotExport, error) {
		var frozen mo.VirtualMachineSnapshot
		property.DefaultCollector(client).RetrieveOne(ctx, snapshot, []string{"config"}, &frozen)
		if len(vmImageMedia(frozen.Config.Hardware.Device)) > 0 {
			return nil, errors.New("File ds:///vmfs/volumes/x/ISO Files/missing.iso was not found")
		}
		current, _ := os.ReadFile(flatPath)
		stream := vmStreamBytes(t, current)
		return &vmFakeExport{disks: []vmExportDisk{{Key: "/" + ref.Value + "/" + strconv.Itoa(int(diskKey))}}, body: func() []byte { return stream }}, nil
	}
	if _, err := vmReplicateRound(ctx, client, ref, options); err == nil || !strings.Contains(err.Error(), "missing.iso") || !strings.Contains(err.Error(), vmEjectMediaHint) {
		t.Fatalf("a missing ISO did not end in the eject hint: %v", err)
	}
	records := t.TempDir()
	options.EjectMedia, options.RecordFolder = true, records
	ejected, err := vmReplicateRound(ctx, client, ref, options)
	if err != nil {
		t.Fatalf("the round with Eject ticked failed: %v", err)
	}
	if len(ejected.EjectedMedia) != 1 || !strings.Contains(ejected.EjectedMedia[0], iso) {
		t.Fatalf("the round did not report the ejected ISO: %v", ejected.EjectedMedia)
	}
	devices, _ = vmObject.Device(ctx)
	clients := 0
	for _, device := range devices.SelectByType((*types.VirtualCdrom)(nil)) {
		base := device.GetVirtualDevice()
		switch base.Backing.(type) {
		case *types.VirtualCdromIsoBackingInfo:
			t.Fatal("the CD/DVD drive still names the ISO")
		case *types.VirtualCdromRemotePassthroughBackingInfo:
			if base.Connectable == nil || base.Connectable.Connected || base.Connectable.StartConnected {
				t.Fatalf("the ejected drive is still set to connect: %#v", base.Connectable)
			}
			clients++
		}
	}
	if clients != 1 {
		t.Fatalf("%d drives were set to a client device, want 1", clients)
	}
	found := false
	entries, _ := os.ReadDir(records)
	for _, entry := range entries {
		raw, _ := os.ReadFile(filepath.Join(records, entry.Name()))
		if strings.HasSuffix(entry.Name(), "-media.json") && strings.Contains(string(raw), "missing.iso") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no record kept the ISO's path before it was ejected: %v", entries)
	}
}

// The export URL names the ESXi host, so its certificate is checked against
// the thumbprint vCenter gave in the lease, never accepted blindly.
func TestExportDownloadPinsTheHostThumbprintFromTheLease(t *testing.T) {
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "stream") }))
	defer host.Close()
	address, _ := url.Parse(host.URL + "/nfc/ticket/disk-0.vmdk")
	good := vmThumbprintSHA1(host.Certificate())
	body, err := vmExportGet(context.Background(), vmExportDisk{URL: address, Thumbprint: strings.ToLower(good)})
	if err != nil {
		t.Fatalf("the host with vCenter's thumbprint was refused: %v", err)
	}
	text, _ := io.ReadAll(body)
	body.Close()
	if string(text) != "stream" {
		t.Fatalf("read %q", text)
	}
	if _, err := vmExportGet(context.Background(), vmExportDisk{URL: address, Thumbprint: "AA:BB"}); err == nil {
		t.Fatal("a host whose certificate does not match the lease was trusted")
	}
	if _, err := vmExportGet(context.Background(), vmExportDisk{URL: address}); err == nil {
		t.Fatal("an untrusted host with no thumbprint was trusted")
	}
}
