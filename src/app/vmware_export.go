package main

// Reading a running VM's disks through vSphere's export of a snapshot.
//
// The datastore file interface (/folder) reads a VM's disk files directly.
// ESXi refuses that for a disk the running VM holds open (it answered 500
// Internal Server Error for MATCHC-DC01's -flat.vmdk while its small
// descriptor read fine), and whether it allows the base disk under a
// snapshot depends on the host. The supported way to read a running VM is
// VirtualMachineSnapshot.ExportSnapshot: an NFC export lease whose disk URLs
// stream the disks as they were when the snapshot was taken, as
// stream-optimized VMDK. The round falls back to it when /folder refuses a
// disk, and only reads: the lease is aborted or completed when the round
// ends, before the round removes its snapshot.

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vmware/govmomi/nfc"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// vmExportDisk is one disk URL of an export lease.
type vmExportDisk struct {
	Key        string
	URL        *url.URL
	Thumbprint string
}

// vmSnapshotExport is an open export lease for one snapshot.
type vmSnapshotExport interface {
	Disks() []vmExportDisk
	// Open starts reading one disk URL from its first byte.
	Open(ctx context.Context, disk vmExportDisk) (io.ReadCloser, error)
	// Read reports bytes read, so the lease is kept alive.
	Read(n int64)
	// End completes the lease after a whole round, otherwise aborts it.
	End(ok bool)
}

// vmOpenSnapshotExport starts an export lease; tests replace it because the
// vSphere simulator does not implement ExportSnapshot.
var vmOpenSnapshotExport = func(ctx context.Context, client *vim25.Client, snapshot types.ManagedObjectReference, total int64, log func(string)) (vmSnapshotExport, error) {
	response, err := methods.ExportSnapshot(ctx, client, &types.ExportSnapshot{This: snapshot})
	if err != nil {
		return nil, fmt.Errorf("vSphere did not start the export of the snapshot: %w", err)
	}
	lease := nfc.NewLease(client, response.Returnval)
	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	info, err := lease.Wait(wait, nil)
	if err != nil {
		_ = lease.Abort(context.Background(), nil)
		return nil, fmt.Errorf("the export of the snapshot did not become ready: %w", err)
	}
	export := &vmLeaseExport{lease: lease, total: total, done: make(chan struct{})}
	for _, device := range info.DeviceUrl {
		if device.Disk != nil && !*device.Disk {
			continue
		}
		u, err := client.ParseURL(device.Url)
		if err != nil {
			continue
		}
		export.disks = append(export.disks, vmExportDisk{Key: device.Key, URL: u, Thumbprint: device.SslThumbprint})
	}
	if len(export.disks) == 0 {
		_ = lease.Abort(context.Background(), nil)
		return nil, errors.New("the export of the snapshot lists no disks")
	}
	go export.keepAlive()
	return export, nil
}

type vmLeaseExport struct {
	lease *nfc.Lease
	disks []vmExportDisk
	total int64
	read  int64
	done  chan struct{}
	once  sync.Once
}

func (e *vmLeaseExport) Disks() []vmExportDisk { return e.disks }
func (e *vmLeaseExport) Read(n int64)          { atomic.AddInt64(&e.read, n) }

// keepAlive reports progress every 30 seconds; a lease with no progress for
// five minutes times out.
func (e *vmLeaseExport) keepAlive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.done:
			return
		case <-ticker.C:
			percent := int32(0)
			if e.total > 0 {
				percent = int32(atomic.LoadInt64(&e.read) * 100 / e.total)
			}
			if percent > 99 {
				percent = 99
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			_ = e.lease.Progress(ctx, percent)
			cancel()
		}
	}
}

func (e *vmLeaseExport) End(ok bool) {
	e.once.Do(func() {
		close(e.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if ok {
			if e.lease.Complete(ctx) == nil {
				return
			}
		}
		_ = e.lease.Abort(ctx, nil)
	})
}

func (e *vmLeaseExport) Open(ctx context.Context, disk vmExportDisk) (io.ReadCloser, error) {
	return vmExportGet(ctx, disk)
}

// vmExportGet downloads one export URL. The URL usually names the ESXi host
// that holds the VM, not the vCenter the session is on, so it gets its own
// connection: the host's certificate is accepted when Windows trusts it for
// that name, or when it matches the thumbprint vCenter gave for it in the
// lease (read over the session whose certificate was already verified).
// The URL carries the lease's ticket; no password or session cookie is
// sent to the host.
func vmExportGet(ctx context.Context, disk vmExportDisk) (io.ReadCloser, error) {
	config := vmExportTLSConfig(disk.URL.Hostname(), disk.Thumbprint)
	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}, Config: config}
			return dialer.DialContext(ctx, network, address)
		},
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, disk.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("could not reach %s for the export: %w", disk.URL.Host, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("%s answered %s to the export of the disk", disk.URL.Host, response.Status)
	}
	return &vmExportBody{ReadCloser: response.Body, transport: transport}, nil
}

type vmExportBody struct {
	io.ReadCloser
	transport *http.Transport
}

func (b *vmExportBody) Close() error {
	err := b.ReadCloser.Close()
	b.transport.CloseIdleConnections()
	return err
}

// vmThumbprintSHA1 is the form vCenter uses for host thumbprints in leases.
func vmThumbprintSHA1(certificate *x509.Certificate) string {
	sum := sha1.Sum(certificate.Raw)
	parts := make([]string, len(sum))
	for index, b := range sum {
		parts[index] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func vmExportTLSConfig(hostName, thumbprint string) *tls.Config {
	want := strings.ToUpper(strings.TrimSpace(thumbprint))
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // verified below, with the system roots or vCenter's thumbprint
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("the host sent no certificate")
			}
			leaf := state.PeerCertificates[0]
			intermediates := x509.NewCertPool()
			for _, certificate := range state.PeerCertificates[1:] {
				intermediates.AddCert(certificate)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{DNSName: hostName, Intermediates: intermediates}); err == nil {
				return nil
			}
			if want != "" && (want == vmThumbprintSHA1(leaf) || want == soap.ThumbprintSHA256(leaf)) {
				return nil
			}
			return fmt.Errorf("the certificate of %s is not trusted by Windows and does not match the thumbprint vCenter gave for it", hostName)
		},
	}
}

// vmExportKeyMatches reports whether an export URL's key names a disk: the
// device key itself, or its controller slot ("/vm-42/VirtualLsiLogicSASController0:0").
func vmExportKeyMatches(key string, disk vmReplicaDisk) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if key == fmt.Sprint(disk.key) || strings.HasSuffix(key, "/"+fmt.Sprint(disk.key)) {
		return true
	}
	return disk.slot != "" && (strings.EqualFold(key, disk.slot) || strings.HasSuffix(strings.ToLower(key), "/"+strings.ToLower(disk.slot)))
}

// vmExportDiskFor picks the export URL of one of the round's disks. A key
// that names the disk wins; with one disk on each side that one is used;
// otherwise the disk is refused rather than guessed.
func vmExportDiskFor(export vmSnapshotExport, disk vmReplicaDisk, diskCount int) (vmExportDisk, error) {
	disks := export.Disks()
	for _, item := range disks {
		if vmExportKeyMatches(item.Key, disk) {
			return item, nil
		}
	}
	if len(disks) == 1 && diskCount == 1 {
		return disks[0], nil
	}
	keys := make([]string, 0, len(disks))
	for _, item := range disks {
		keys = append(keys, item.Key)
	}
	return vmExportDisk{}, fmt.Errorf("the export of the snapshot does not say which of its disks (%s) is %s", strings.Join(keys, ", "), disk.label)
}

// vmExportSource is one disk read from an export stream.
type vmExportSource struct {
	*vmStreamVMDK
	export vmSnapshotExport
	name   string
}

func (s *vmExportSource) Describe() string { return s.name }

func (s *vmExportSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := s.vmStreamVMDK.ReadAt(p, off)
	s.export.Read(int64(n))
	return n, err
}

// openExportedDisk opens one disk of the export. name is the disk's
// datastore path, so copy records match those written through /folder.
func openExportedDisk(ctx context.Context, export vmSnapshotExport, disk vmReplicaDisk, diskCount int) (vmCopySource, error) {
	item, err := vmExportDiskFor(export, disk, diskCount)
	if err != nil {
		return nil, err
	}
	body, err := export.Open(ctx, item)
	if err != nil {
		return nil, err
	}
	stream, err := openVMStreamVMDK(disk.file, body, func() (io.ReadCloser, error) { return export.Open(ctx, item) })
	if err != nil {
		return nil, err
	}
	return &vmExportSource{vmStreamVMDK: stream, export: export, name: disk.file}, nil
}

// vmFolderRefused reports whether opening a disk through /folder failed
// because the host answered with an HTTP error (a running VM's disk is
// locked), as opposed to a bad path or a network failure.
func vmFolderRefused(err error) bool {
	var status *vmDatastoreStatusError
	return errors.As(err, &status)
}
