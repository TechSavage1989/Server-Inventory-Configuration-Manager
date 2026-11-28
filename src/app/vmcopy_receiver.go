package main

// Receiver mode: the target host (for example the Hyper-V server) runs a
// receiver that writes VHDX files into one folder, and the computer that can
// read the source disks sends chunks to it over HTTPS. Only chunks whose
// hash changed travel, zero chunks travel as a hash alone, and every chunk is
// checked against its SHA-256 before it is written.
//
// The receiver has a self-signed certificate and a random pairing code, kept
// across restarts when started from the window (vmreceiver_identity.go) and
// new each start on boot media. The sender must be given both: it accepts only the
// certificate with that SHA-256 thumbprint and sends the code with every
// request. Nothing else is trusted, so no certificate authority or Windows
// trust store change is needed.

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	vmReceiverMaxChunk = 64 << 20
	vmReceiverAPI      = "/v1/"
)

var (
	vmReceiverName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}\.vhdx$`)
	vmReceiverDiskName = regexp.MustCompile(`^disk-[0-9]{1,3}$`)
)

// vmReceiverTarget is what a receiver session writes into: a VHDX in the
// receiver's folder or one of the raw disks it was started with.
type vmReceiverTarget interface {
	vmCopySink
	State() *vmCopyState
}

func (s *vmLocalSink) State() *vmCopyState { return s.state }

// vmReceiverDisk is a raw disk the receiver may write, named disk-N.
type vmReceiverDisk struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Size       int64  `json:"size"`
	SectorSize int64  `json:"sectorSize"`
	Overwrite  bool   `json:"overwrite"`
	open       func() (vmBlockDevice, error)
}

// vmReceiverConfig says where a receiver writes: VHDX files in Folder,
// and/or the listed raw disks with their copy records in Records.
type vmReceiverConfig struct {
	Folder  string
	Disks   []vmReceiverDisk
	Records string
	// Discovery is the UDP address that answers find requests (":44379");
	// empty means senders must type the address.
	Discovery string
	// Pairing lets a sender ask for the code, which is given only after the
	// operator allows the request on this receiver's screen. Off for the
	// receiver command on boot media, which has no screen to allow it on.
	Pairing bool
	// FreeBytes reports the free space where copies are written, for the
	// find answer. Optional.
	FreeBytes func() uint64
	Version   string
	// Identity is the certificate and code to use, kept across restarts
	// (vmreceiver_identity.go); nil makes new ones for this run only.
	Identity *vmReceiverIdentity
}

// vmReceiverRawTarget closes its device with the session.
type vmReceiverRawTarget struct {
	*vmRawSink
}

func (t vmReceiverRawTarget) Close() error {
	err := t.vmRawSink.Close()
	if closeErr := t.device.Close(); err == nil {
		err = closeErr
	}
	return err
}

type vmReceiver struct {
	// stopping ends work that outlives a request (a verification) when the
	// receiver stops.
	stopping   context.Context
	stopAll    context.CancelFunc
	folder     string
	records    string
	disks      map[string]vmReceiverDisk
	code       string
	thumbprint string
	server     *http.Server
	listener   net.Listener
	log        func(string)

	mu       sync.Mutex
	sessions map[string]*vmReceiverSession
	names    map[string]string
	// holds are disk names a VM was started from (a test failover or the
	// production VM); they are refused until released.
	holds map[string]string
	// recent keeps finished copies for the Receive panel (an hour, up to
	// vmTransferMax).
	recent []vmTransfer

	pairing   bool
	pairs     map[string]*vmPairRequest
	discovery *vmDiscoveryResponder
	freeBytes func() uint64
	version   string
	hostName  string
}

// errVMReceiverHeld is wrapped by the sender when the receiver refuses a disk
// held for a VM started from the copy.
var errVMReceiverHeld = errors.New("the target disk is in use by a VM started from the copy")

// Hold refuses new copies into the named disks until Release. It fails when
// any of them is being received now, so a held copy is never half written.
func (r *vmReceiver) Hold(names []string, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		if _, busy := r.names[strings.ToLower(name)]; busy {
			return fmt.Errorf("%s is being received right now; wait for the round to finish", name)
		}
	}
	if r.holds == nil {
		r.holds = map[string]string{}
	}
	for _, name := range names {
		r.holds[strings.ToLower(name)] = reason
	}
	return nil
}

// Release lets copies into the named disks again.
func (r *vmReceiver) Release(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		delete(r.holds, strings.ToLower(name))
	}
}

// Busy reports whether a disk is being received now.
func (r *vmReceiver) Busy(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, busy := r.names[strings.ToLower(name)]
	return busy
}

type vmReceiverSession struct {
	mu    sync.Mutex
	name  string
	owner string
	sink  vmReceiverTarget
	seen  time.Time

	// finishing is set while a finish (with its verification, which reads
	// the whole disk back) runs; it is read without the session lock.
	finishing atomic.Bool
	// finished remembers the last successful finish, so a sender that
	// gave up waiting and asks again gets the result instead of a second
	// verification. Any write clears it.
	finishedKey   string
	finishedState *vmCopyState

	// A rescan rebuilds the record from the target's content in the
	// background; chunks are refused until it ends.
	rescanning bool
	rescanDone int
	rescanErr  string
	rescanned  bool

	// Progress for the Receive panel. The sender's address is shown so the
	// operator can see who is connected; nothing secret is kept.
	progress vmTransfer
	meter    vmRateMeter
	position vmRateMeter

	// shown is the last snapshot, for Transfers while a long request (a
	// verification) holds mu: the window never waits on a busy session.
	shownMu sync.Mutex
	shown   vmTransfer
}

// show records the snapshot Transfers uses while the session is busy.
// Caller holds s.mu.
func (s *vmReceiverSession) show(t vmTransfer) {
	s.shownMu.Lock()
	s.shown = t
	s.shownMu.Unlock()
}

func (s *vmReceiverSession) lastShown() vmTransfer {
	s.shownMu.Lock()
	defer s.shownMu.Unlock()
	return s.shown
}

// snapshot is the session's progress as a transfer. Caller holds s.mu.
func (s *vmReceiverSession) snapshot(now time.Time) vmTransfer {
	t := s.progress
	t.Updated = s.seen
	if s.rescanning {
		t.Label = "Rescanning the target"
		if total := len(s.sink.State().Hashes); total > 0 {
			t.Chunks, t.Done = total, s.rescanDone
		}
	}
	vmTransferFill(&t, s.meter.Rate(now), s.position.Rate(now))
	return t
}

type vmReceiverOpen struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Size      int64  `json:"size"`
	ChunkSize int64  `json:"chunkSize"`
	// Rescan rebuilds the copy record from the target's content before any
	// chunk is accepted (after a test failover, or for a failback).
	Rescan bool `json:"rescan,omitempty"`
	// Owner names the sender (a replication job's ID): the same owner may
	// take over its own idle session at once, after a round it abandoned
	// because a read hung, instead of waiting two minutes.
	Owner string `json:"owner,omitempty"`
}

type vmReceiverOpened struct {
	Session string       `json:"session"`
	Resumed bool         `json:"resumed"`
	State   *vmCopyState `json:"state"`
}

type vmReceiverFinish struct {
	Session string       `json:"session"`
	State   *vmCopyState `json:"state"`
	Verify  bool         `json:"verify"`
}

// vmPairingCode is 20 random characters in groups of five, easy to read out.
func vmPairingCode() (string, error) {
	raw := make([]byte, 15)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	text := base32.StdEncoding.EncodeToString(raw)
	return text[0:5] + "-" + text[5:10] + "-" + text[10:15] + "-" + text[15:20], nil
}

func vmNormalizePairingCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func vmSelfSignedCertificate(host string) (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Server Inventory and Migration Manager receiver"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0), // pinned by thumbprint; kept across restarts
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, vmNormalizeThumbprint(hex.EncodeToString(sum[:])), nil
}

// startVMReceiver listens on address (":8443" or "0.0.0.0:0") and writes into
// folder.
func startVMReceiver(address, folder string, log func(string)) (*vmReceiver, error) {
	return startVMReceiverWith(address, vmReceiverConfig{Folder: folder}, log)
}

func startVMReceiverWith(address string, config vmReceiverConfig, log func(string)) (*vmReceiver, error) {
	if log == nil {
		log = func(string) {}
	}
	if config.Folder == "" && len(config.Disks) == 0 {
		return nil, errors.New("choose a folder or at least one disk to receive into")
	}
	folder := ""
	if config.Folder != "" {
		absolute, err := filepath.Abs(config.Folder)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(absolute, 0700); err != nil {
			return nil, err
		}
		folder = absolute
	}
	disks := map[string]vmReceiverDisk{}
	records := ""
	if len(config.Disks) > 0 {
		if config.Records == "" {
			return nil, errors.New("choose a folder for the raw disk copy records")
		}
		absolute, err := filepath.Abs(config.Records)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(absolute, 0700); err != nil {
			return nil, err
		}
		records = absolute
		for _, disk := range config.Disks {
			if !vmReceiverDiskName.MatchString(disk.Name) || disk.open == nil {
				return nil, fmt.Errorf("%q is not a receiver disk", disk.Name)
			}
			if _, duplicate := disks[disk.Name]; duplicate {
				return nil, fmt.Errorf("%s is listed twice", disk.Name)
			}
			disks[disk.Name] = disk
		}
	}
	hostName, _ := os.Hostname()
	identity := config.Identity
	if identity == nil {
		made, err := vmNewReceiverIdentity(hostName)
		if err != nil {
			return nil, err
		}
		identity = made
	}
	code, certificate, thumbprint := identity.Code, identity.Certificate, identity.Thumbprint
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	stopping, stopAll := context.WithCancel(context.Background())
	r := &vmReceiver{stopping: stopping, stopAll: stopAll, folder: folder, records: records, disks: disks, code: code, thumbprint: thumbprint, listener: listener, log: log, sessions: map[string]*vmReceiverSession{}, names: map[string]string{}, pairing: config.Pairing, pairs: map[string]*vmPairRequest{}, freeBytes: config.FreeBytes, version: config.Version, hostName: hostName}
	r.server = &http.Server{
		Handler:           r,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go r.server.ServeTLS(listener, "", "")
	if config.Discovery != "" {
		responder, err := startVMDiscoveryResponder(config.Discovery, r.announcement)
		if err != nil {
			log("Senders cannot find this receiver automatically (" + err.Error() + "); type its address on the sending computer.")
		} else {
			r.discovery = responder
			log(fmt.Sprintf("Senders on this network can find this receiver (UDP port %d). Its fingerprint is %s.", responder.Addr().(*net.UDPAddr).Port, vmFingerprint(thumbprint)))
		}
	}
	if folder != "" {
		log(fmt.Sprintf("Receiving into %s on port %d.", folder, r.Port()))
	}
	for _, disk := range r.Disks() {
		log(fmt.Sprintf("Receiving onto %s (%s, %s) on port %d.", disk.Name, disk.Label, vmFormatBytes(disk.Size), r.Port()))
	}
	return r, nil
}

func (r *vmReceiver) Port() int          { return r.listener.Addr().(*net.TCPAddr).Port }
func (r *vmReceiver) Code() string       { return r.code }
func (r *vmReceiver) Thumbprint() string { return r.thumbprint }
func (r *vmReceiver) Folder() string     { return r.folder }

// Disks lists the raw disks this receiver writes, by name.
func (r *vmReceiver) Disks() []vmReceiverDisk {
	list := make([]vmReceiverDisk, 0, len(r.disks))
	for _, disk := range r.disks {
		list = append(list, disk)
	}
	sort.Slice(list, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimPrefix(list[i].Name, "disk-"))
		b, _ := strconv.Atoi(strings.TrimPrefix(list[j].Name, "disk-"))
		return a < b
	})
	return list
}

func (r *vmReceiver) ActiveSessions() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.sessions) }

// Transfers lists the copies being received now and those finished in the
// last hour, with the sender's address, progress and speed.
func (r *vmReceiver) Transfers() []vmTransfer {
	now := time.Now()
	r.mu.Lock()
	sessions := make([]*vmReceiverSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		sessions = append(sessions, session)
	}
	list := make([]vmTransfer, 0, len(sessions)+len(r.recent))
	for _, t := range r.recent {
		if now.Sub(t.Finished) <= vmTransferKeep {
			list = append(list, t)
		}
	}
	r.mu.Unlock()
	for _, session := range sessions {
		if session.mu.TryLock() {
			t := session.snapshot(now)
			session.show(t)
			session.mu.Unlock()
			list = append(list, t)
			continue
		}
		// Busy (verifying, most often): show the last known progress
		// rather than wait, so the window keeps answering.
		t := session.lastShown()
		if session.finishing.Load() {
			t.Label = "Verifying the copy (reading it back)"
		}
		list = append(list, t)
	}
	vmSortTransfers(list)
	return list
}

// retire moves a session's progress to the recent list. Caller holds r.mu
// and session.mu.
func (r *vmReceiver) retire(session *vmReceiverSession, state string) {
	t := session.snapshot(time.Now())
	if t.State == "running" {
		t.State = state
		if t.Error != "" && state != "stopped" {
			t.State = "failed"
		}
	}
	t.Finished, t.Rate, t.ETASeconds = time.Now(), 0, 0
	if t.State == "done" {
		t.Percent = 100
	}
	r.recent = append(r.recent, t)
	if len(r.recent) > vmTransferMax {
		r.recent = r.recent[len(r.recent)-vmTransferMax:]
	}
}

// Stop closes the listener and every open disk, saving its copy record so
// the sender can resume against a new receiver later.
func (r *vmReceiver) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r.stopAll()
	if r.discovery != nil {
		r.discovery.Close()
	}
	err := r.server.Shutdown(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, session := range r.sessions {
		session.mu.Lock()
		session.sink.Close()
		r.retire(session, "stopped")
		session.mu.Unlock()
		delete(r.sessions, id)
	}
	r.names = map[string]string{}
	return err
}

func vmReceiverError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func vmReceiverJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

func (r *vmReceiver) authorized(request *http.Request) bool {
	given := vmNormalizePairingCode(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	want := vmNormalizePairingCode(r.code)
	return subtle.ConstantTimeCompare([]byte(given), []byte(want)) == 1
}

func (r *vmReceiver) session(id string) *vmReceiverSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

func (r *vmReceiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(request.URL.Path, vmReceiverAPI) {
		vmReceiverError(w, http.StatusNotFound, "not found")
		return
	}
	if request.URL.Path == vmReceiverAPI+"pair" {
		r.pair(w, request) // asks for the code; needs no code
		return
	}
	if !r.authorized(request) {
		time.Sleep(time.Second)
		vmReceiverError(w, http.StatusUnauthorized, "the pairing code is wrong")
		return
	}
	switch strings.TrimPrefix(request.URL.Path, vmReceiverAPI) {
	case "hello":
		vmReceiverJSON(w, map[string]string{"app": "Server Inventory and Migration Manager receiver"})
	case "disks":
		// folderPath and computer let a console that creates the VM on this
		// host (Move VMs from an admin computer) find the copied files and
		// check it is talking to the node it chose. Both are shown only to a
		// sender that knows the pairing code.
		computer, _ := os.Hostname()
		// deflate tells a sender it may compress chunks (enc=deflate);
		// receivers that do not say so are sent chunks uncompressed.
		vmReceiverJSON(w, map[string]interface{}{"folder": r.folder != "", "folderPath": r.folder, "computer": computer, "disks": r.Disks(), "deflate": true})
	case "open":
		r.open(w, request)
	case "chunk":
		r.chunk(w, request)
	case "rescan":
		r.rescan(w, request)
	case "checkpoint", "finish", "close":
		r.lifecycle(w, request, strings.TrimPrefix(request.URL.Path, vmReceiverAPI))
	default:
		vmReceiverError(w, http.StatusNotFound, "not found")
	}
}

func (r *vmReceiver) open(w http.ResponseWriter, request *http.Request) {
	var body vmReceiverOpen
	if request.Method != http.MethodPost || json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&body) != nil {
		vmReceiverError(w, http.StatusBadRequest, "bad request")
		return
	}
	disk, raw := r.disks[body.Name]
	if !raw && (r.folder == "" || !vmReceiverName.MatchString(body.Name) || strings.Contains(body.Name, "..")) {
		if r.folder == "" {
			vmReceiverError(w, http.StatusBadRequest, "this receiver writes only its listed disks; "+body.Name+" is not one of them")
			return
		}
		vmReceiverError(w, http.StatusBadRequest, "the disk name must be a plain .vhdx file name")
		return
	}
	if body.ChunkSize <= 0 || body.ChunkSize > vmReceiverMaxChunk || body.ChunkSize%vhdxLogicalSector != 0 || body.Size <= 0 || body.Size > vhdxMaxSize {
		vmReceiverError(w, http.StatusBadRequest, "unsupported disk or chunk size")
		return
	}
	key := strings.ToLower(body.Name)
	r.mu.Lock()
	if reason, held := r.holds[key]; held {
		r.mu.Unlock()
		vmReceiverError(w, http.StatusLocked, body.Name+" is held: "+reason+". Copies resume when that ends.")
		return
	}
	if id, busy := r.names[key]; busy {
		// A sender that vanished (lost connection, closed application)
		// leaves its session open; after two idle minutes a new sender may
		// take over, resuming from the saved record.
		if old := r.sessions[id]; old != nil && old.mu.TryLock() {
			own := body.Owner != "" && body.Owner == old.owner
			if (own || time.Since(old.seen) > 2*time.Minute) && !old.rescanning {
				old.sink.Close()
				old.progress.Error = "the sender stopped answering; another sender took over"
				if own {
					old.progress.Error = "the sender started this copy again"
				}
				r.retire(old, "stopped")
				delete(r.sessions, id)
				delete(r.names, key)
				busy = false
			}
			old.mu.Unlock()
		}
		if !busy {
			goto claim
		}
		finishing := false
		if old := r.sessions[id]; old != nil {
			finishing = old.finishing.Load()
		}
		r.mu.Unlock()
		if finishing {
			vmReceiverError(w, http.StatusConflict, body.Name+" is still being verified from the previous round; try again in a few minutes")
			return
		}
		vmReceiverError(w, http.StatusConflict, body.Name+" is already being received")
		return
	}
claim:
	r.names[key] = ""
	r.mu.Unlock()
	var sink vmReceiverTarget
	var state *vmCopyState
	var resumed bool
	var err error
	if raw {
		var device vmBlockDevice
		device, err = disk.open()
		if err == nil {
			rawSink := vmReceiverRawTarget{newVMRawSink(device, r.records, disk.Overwrite)}
			state, resumed, err = rawSink.Open(body.Source, body.Size, body.ChunkSize)
			if err != nil {
				device.Close()
			}
			sink = rawSink
		}
	} else {
		local := &vmLocalSink{target: filepath.Join(r.folder, body.Name)}
		state, resumed, err = local.Open(body.Source, body.Size, body.ChunkSize)
		sink = local
	}
	if err == nil && state.Complete {
		// Mark the record unfinished before any chunk lands, so a copy whose
		// round stops part-way is never taken as a whole copy.
		if err = sink.Checkpoint(&vmCopyState{Passes: state.Passes}); err != nil {
			sink.Close()
		}
	}
	if err != nil {
		r.mu.Lock()
		delete(r.names, key)
		r.mu.Unlock()
		vmReceiverError(w, http.StatusConflict, err.Error())
		return
	}
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	id := hex.EncodeToString(idBytes)
	session := &vmReceiverSession{name: key, owner: body.Owner, sink: sink, seen: time.Now()}
	peer := request.RemoteAddr
	if host, _, splitErr := net.SplitHostPort(peer); splitErr == nil {
		peer = host
	}
	session.progress = vmTransfer{ID: id, Kind: "receive", Label: body.Name, Source: body.Source, Target: body.Name, Peer: peer, Size: body.Size,
		Chunks: len(state.Hashes), Pass: state.Passes + 1, Started: time.Now(), State: "running"}
	if body.Rescan {
		rescanner, ok := sink.(vmRescanner)
		if !ok {
			sink.Close()
			r.mu.Lock()
			delete(r.names, key)
			r.mu.Unlock()
			vmReceiverError(w, http.StatusBadRequest, "this target cannot be rescanned")
			return
		}
		session.rescanning = true
		go func() {
			err := rescanner.Rescan(context.Background(), func(done, total int) {
				session.mu.Lock()
				session.rescanDone, session.seen = done, time.Now()
				session.mu.Unlock()
			})
			session.mu.Lock()
			session.rescanning, session.rescanned, session.seen = false, err == nil, time.Now()
			if err != nil {
				session.rescanErr = err.Error()
			}
			session.mu.Unlock()
			if err == nil {
				r.log(fmt.Sprintf("Rescanned %s: only chunks that differ will be written.", body.Name))
			}
		}()
	}
	r.mu.Lock()
	r.sessions[id] = session
	r.names[key] = id
	r.mu.Unlock()
	r.log(fmt.Sprintf("Receiving %s (%s, resumed: %t, rescan: %t).", body.Name, vmFormatBytes(body.Size), resumed, body.Rescan))
	state.Target = body.Name
	vmReceiverJSON(w, vmReceiverOpened{Session: id, Resumed: resumed, State: state})
}

func (r *vmReceiver) chunk(w http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	session := r.session(query.Get("session"))
	index, err := strconv.Atoi(query.Get("index"))
	hash := query.Get("hash")
	if request.Method != http.MethodPut || session == nil || err != nil {
		vmReceiverError(w, http.StatusBadRequest, "bad request")
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.seen = time.Now()
	if session.rescanning || session.rescanErr != "" {
		vmReceiverError(w, http.StatusConflict, "the target is still being rescanned")
		return
	}
	state := session.sink.State()
	if index < 0 || index >= len(state.Hashes) {
		vmReceiverError(w, http.StatusBadRequest, "chunk outside the disk")
		return
	}
	length := vmChunkLength(state, index)
	var data []byte
	if hash == vmCopyZeroHash {
		data = make([]byte, length)
	} else {
		var body io.Reader = io.LimitReader(request.Body, length+1)
		if query.Get("enc") == "deflate" {
			// The compressed body is limited too: deflate never grows data
			// by more than a few bytes per 64 KB block.
			inflater := flate.NewReader(io.LimitReader(request.Body, length+length/64+1024))
			defer inflater.Close()
			body = io.LimitReader(inflater, length+1)
		} else if query.Get("enc") != "" {
			vmReceiverError(w, http.StatusBadRequest, "unsupported chunk encoding")
			return
		}
		data, err = io.ReadAll(body)
		if err != nil || int64(len(data)) != length {
			vmReceiverError(w, http.StatusBadRequest, "the chunk is incomplete")
			return
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != hash || vmIsZero(data) {
			vmReceiverError(w, http.StatusBadRequest, "the chunk does not match its hash")
			return
		}
	}
	session.finishedKey, session.finishedState = "", nil
	written, err := session.sink.Write(index, data, hash)
	if err != nil {
		session.progress.Error = err.Error()
		vmReceiverError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	p := &session.progress
	p.Error = ""
	p.BytesRead += length
	p.BytesWritten += written
	if request.ContentLength > 0 && hash != vmCopyZeroHash {
		p.BytesSent += request.ContentLength
	}
	if index+1 > p.Done {
		p.Done = index + 1
	}
	session.meter.Add(now, p.BytesRead)
	session.position.Add(now, int64(p.Done))
	vmReceiverJSON(w, map[string]int64{"written": written})
}

// rescan reports the progress of a rescan started by open, with the
// rebuilt record once it is done.
func (r *vmReceiver) rescan(w http.ResponseWriter, request *http.Request) {
	session := r.session(request.URL.Query().Get("session"))
	if request.Method != http.MethodGet || session == nil {
		vmReceiverError(w, http.StatusBadRequest, "bad request")
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.seen = time.Now()
	answer := map[string]interface{}{"rescanning": session.rescanning, "done": session.rescanDone, "total": len(session.sink.State().Hashes)}
	if session.rescanErr != "" {
		answer["error"] = session.rescanErr
	}
	if session.rescanned {
		state := vmCopyStateClone(session.sink.State())
		state.Target = filepath.Base(state.Target)
		answer["state"] = state
	}
	vmReceiverJSON(w, answer)
}

func (r *vmReceiver) lifecycle(w http.ResponseWriter, request *http.Request, action string) {
	var body vmReceiverFinish
	if request.Method != http.MethodPost || json.NewDecoder(io.LimitReader(request.Body, 64<<20)).Decode(&body) != nil {
		vmReceiverError(w, http.StatusBadRequest, "bad request")
		return
	}
	session := r.session(body.Session)
	if session == nil {
		vmReceiverError(w, http.StatusNotFound, "that copy is not open; open it again to resume")
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.seen = time.Now()
	var state *vmCopyState
	var err error
	switch action {
	case "checkpoint":
		session.finishedKey, session.finishedState = "", nil
		if body.State == nil {
			body.State = &vmCopyState{Passes: session.sink.State().Passes}
		}
		err = session.sink.Checkpoint(body.State)
	case "finish":
		if body.State == nil || len(body.State.Hashes) != len(session.sink.State().Hashes) {
			err = errors.New("the finish request does not describe this disk")
			break
		}
		raw, _ := json.Marshal(body.State)
		sum := sha256.Sum256(raw)
		key := fmt.Sprintf("%x %t", sum, body.Verify)
		if session.finishedKey == key && session.finishedState != nil {
			// The same finish again (the sender stopped waiting for the
			// first): answer with its result.
			state = vmCopyStateClone(session.finishedState)
			state.Target = filepath.Base(state.Target)
			break
		}
		if body.Verify {
			r.log(fmt.Sprintf("Verifying %s: reading the whole copy back.", session.name))
		}
		shown := session.snapshot(time.Now())
		shown.Label = "Verifying the copy (reading it back)"
		session.show(shown)
		session.finishing.Store(true)
		// Not the request's context: a sender that stops waiting must not
		// throw away a verification that is nearly done. Only stopping the
		// receiver ends it.
		state, err = session.sink.Finish(r.stopping, body.State, body.Verify)
		session.finishing.Store(false)
		session.seen = time.Now()
		if err != nil {
			session.progress.Error = err.Error()
		}
		if err == nil {
			session.finishedKey, session.finishedState = key, vmCopyStateClone(state)
			session.progress.State, session.progress.Verified, session.progress.Done = "done", state.Verified, session.progress.Chunks
			state.Target = filepath.Base(state.Target)
			r.log(fmt.Sprintf("Finished %s (verified: %t).", state.Target, state.Verified))
		}
	case "close":
		err = session.sink.Close()
		r.mu.Lock()
		r.retire(session, "stopped")
		delete(r.sessions, body.Session)
		delete(r.names, session.name)
		r.mu.Unlock()
	}
	if err != nil {
		vmReceiverError(w, http.StatusConflict, err.Error())
		return
	}
	vmReceiverJSON(w, map[string]interface{}{"ok": true, "state": state})
}

// The sender side.

type vmRemoteSink struct {
	ctx     context.Context
	client  *http.Client
	base    *url.URL
	code    string
	name    string
	session string
	retries int
	delay   time.Duration
	size    int64 // the disk being copied, for the finish time limit

	// Owner is sent when a copy opens (see vmReceiverOpen.Owner).
	Owner string
	// Compress deflates chunks on the wire; the receiver inflates and
	// checks each against its hash as before.
	Compress bool
	// Throttle paces what is sent (after compression); nil sends at full speed.
	Throttle *vmThrottle
	// Rescan rebuilds the receiver's record from the target before copying.
	Rescan bool
	// RescanProgress reports a rescan's progress.
	RescanProgress func(done, total int)

	sentMu sync.Mutex
	sent   int64
}

// SentBytes is what travelled over the network for chunk data.
func (s *vmRemoteSink) SentBytes() int64 {
	s.sentMu.Lock()
	defer s.sentMu.Unlock()
	return s.sent
}

// newVMRemoteSink connects to a receiver at "host:port", accepting only the
// certificate with the given thumbprint. name is a .vhdx file name, or disk-N
// for one of a raw-disk receiver's disks.
func newVMRemoteSink(ctx context.Context, address, thumbprint, code, name string) (*vmRemoteSink, error) {
	if !vmReceiverName.MatchString(name) && !vmReceiverDiskName.MatchString(name) {
		return nil, errors.New("the disk name must be a plain .vhdx file name or disk-N")
	}
	sink, err := newVMReceiverClient(ctx, address, thumbprint, code)
	if err != nil {
		return nil, err
	}
	sink.name = name
	return sink, nil
}

// TakesDeflate reports whether the receiver says it inflates compressed
// chunks. A receiver older than that answer, or one that cannot be asked,
// is sent chunks uncompressed.
func (s *vmRemoteSink) TakesDeflate() bool {
	var answer struct {
		Deflate bool `json:"deflate"`
	}
	return s.call(http.MethodGet, "disks", nil, nil, &answer) == nil && answer.Deflate
}

// vmReceiverListDisks asks a receiver which raw disks it writes.
func vmReceiverListDisks(ctx context.Context, address, thumbprint, code string) ([]vmReceiverDisk, error) {
	client, err := newVMReceiverClient(ctx, address, thumbprint, code)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Disks []vmReceiverDisk `json:"disks"`
	}
	if err := client.call(http.MethodGet, "disks", nil, nil, &answer); err != nil {
		return nil, err
	}
	return answer.Disks, nil
}

// vmReceiverDescribe asks a receiver whether it writes VHDX files and which
// raw disks it writes.
func vmReceiverDescribe(ctx context.Context, address, thumbprint, code string) (bool, []vmReceiverDisk, error) {
	client, err := newVMReceiverClient(ctx, address, thumbprint, code)
	if err != nil {
		return false, nil, err
	}
	var answer struct {
		Folder bool             `json:"folder"`
		Disks  []vmReceiverDisk `json:"disks"`
	}
	if err := client.call(http.MethodGet, "disks", nil, nil, &answer); err != nil {
		return false, nil, err
	}
	if answer.Disks == nil {
		answer.Disks = []vmReceiverDisk{}
	}
	return answer.Folder, answer.Disks, nil
}

// vmReceiverTransport connects only to a receiver whose certificate has
// exactly the pinned SHA-256 thumbprint: no CA, no proxy.
func vmReceiverTransport(pinned string) *http.Transport {
	return &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // replaced by the exact thumbprint check below
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("the receiver sent no certificate")
				}
				sum := sha256.Sum256(state.PeerCertificates[0].Raw)
				if got := vmNormalizeThumbprint(hex.EncodeToString(sum[:])); got != pinned {
					return fmt.Errorf("the receiver's certificate thumbprint is %s, not the one entered; check that you are connecting to the right receiver", got)
				}
				return nil
			},
		},
		MaxIdleConnsPerHost:   4,
		ResponseHeaderTimeout: 5 * time.Minute,
	}
}

// vmReceiverPlace is where a receiver writes VHDX files and on which
// computer; older receivers leave both empty.
type vmReceiverPlace struct {
	Folder     bool   `json:"folder"`
	FolderPath string `json:"folderPath"`
	Computer   string `json:"computer"`
}

// vmReceiverWhere asks a receiver for its VHDX folder and computer name.
func vmReceiverWhere(ctx context.Context, address, thumbprint, code string) (vmReceiverPlace, error) {
	var place vmReceiverPlace
	client, err := newVMReceiverClient(ctx, address, thumbprint, code)
	if err != nil {
		return place, err
	}
	err = client.call(http.MethodGet, "disks", nil, nil, &place)
	return place, err
}

func newVMReceiverClient(ctx context.Context, address, thumbprint, code string) (*vmRemoteSink, error) {
	pinned := vmNormalizeThumbprint(thumbprint)
	if pinned == "" {
		return nil, errors.New("enter the receiver's certificate thumbprint (64 hexadecimal characters)")
	}
	if vmNormalizePairingCode(code) == "" {
		return nil, errors.New("enter the receiver's pairing code")
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || host == "" || port == "" {
		return nil, errors.New("enter the receiver as host:port")
	}
	transport := vmReceiverTransport(pinned)
	// Every request has its own limit (callTimeout); a fixed header limit
	// would fail a long verification.
	transport.ResponseHeaderTimeout = 0
	sink := &vmRemoteSink{ctx: ctx, client: &http.Client{Transport: transport}, base: &url.URL{Scheme: "https", Host: net.JoinHostPort(host, port), Path: vmReceiverAPI}, code: code, retries: 4, delay: 2 * time.Second}
	if err := sink.call(http.MethodPost, "hello", nil, nil, nil); err != nil {
		return nil, err
	}
	return sink, nil
}

func (s *vmRemoteSink) call(method, action string, query url.Values, body []byte, out interface{}) error {
	u := *s.base
	u.Path += action
	u.RawQuery = query.Encode()
	var last error
	for attempt := 0; attempt < s.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-s.ctx.Done():
				return s.ctx.Err()
			case <-time.After(s.delay * time.Duration(attempt)):
			}
		}
		// Each attempt has its own time limit, so a receiver that stops
		// answering fails the request (and it is tried again) instead of
		// holding the copy for good.
		attemptCtx, cancel := context.WithTimeout(s.ctx, s.callTimeout(action))
		request, err := http.NewRequestWithContext(attemptCtx, method, u.String(), bytes.NewReader(body))
		if err != nil {
			cancel()
			return err
		}
		request.Header.Set("Authorization", "Bearer "+s.code)
		response, err := s.client.Do(request)
		if err != nil {
			cancel()
			last = err
			if s.ctx.Err() != nil {
				return s.ctx.Err()
			}
			if attemptCtx.Err() == context.DeadlineExceeded {
				last = fmt.Errorf("the receiver did not answer within %s (%s)", replAge(s.callTimeout(action)), action)
			}
			if strings.Contains(err.Error(), "thumbprint") {
				return err
			}
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<20))
		response.Body.Close()
		cancel()
		if readErr != nil && response.StatusCode == http.StatusOK {
			last = readErr
			if s.ctx.Err() != nil {
				return s.ctx.Err()
			}
			continue
		}
		if response.StatusCode == http.StatusOK {
			if out != nil {
				return json.Unmarshal(raw, out)
			}
			return nil
		}
		var problem struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &problem)
		if problem.Error == "" {
			problem.Error = response.Status
		}
		last = errors.New("the receiver refused: " + problem.Error)
		if response.StatusCode == http.StatusLocked {
			return fmt.Errorf("%w (%s)", errVMReceiverHeld, last.Error())
		}
		if response.StatusCode < 500 {
			return last
		}
	}
	return last
}

// vmReceiverCallLimit bounds one request to a receiver; finishing a copy
// with verification reads the whole disk back, so it gets
// vmReceiverFinishLimit plus time for the disk's size.
var (
	vmReceiverCallLimit   = 3 * time.Minute
	vmReceiverFinishLimit = 15 * time.Minute
)

func (s *vmRemoteSink) callTimeout(action string) time.Duration {
	if action != "finish" {
		return vmReceiverCallLimit
	}
	// At least 20 MB/s for the read back, which every disk managed in
	// testing; a slower one fails the round, which is tried again.
	return vmReceiverFinishLimit + time.Duration(s.size/(20<<20))*time.Second
}

func (s *vmRemoteSink) Open(source string, size, chunkSize int64) (*vmCopyState, bool, error) {
	s.size = size
	raw, _ := json.Marshal(vmReceiverOpen{Name: s.name, Source: source, Size: size, ChunkSize: chunkSize, Rescan: s.Rescan, Owner: s.Owner})
	var opened vmReceiverOpened
	if err := s.call(http.MethodPost, "open", nil, raw, &opened); err != nil {
		return nil, false, err
	}
	if opened.State == nil || opened.State.Size != size || int64(len(opened.State.Hashes)) != (size+chunkSize-1)/chunkSize {
		return nil, false, errors.New("the receiver returned a copy record that does not match this disk")
	}
	s.session = opened.Session
	if s.Rescan {
		state, err := s.waitRescan()
		if err != nil {
			return nil, false, err
		}
		if state.Size != size || len(state.Hashes) != len(opened.State.Hashes) {
			return nil, false, errors.New("the receiver returned a rescanned record that does not match this disk")
		}
		opened.State = state
	}
	return opened.State, opened.Resumed, nil
}

// waitRescan polls the receiver until its rescan ends.
func (s *vmRemoteSink) waitRescan() (*vmCopyState, error) {
	for {
		var answer struct {
			Rescanning bool         `json:"rescanning"`
			Done       int          `json:"done"`
			Total      int          `json:"total"`
			Error      string       `json:"error"`
			State      *vmCopyState `json:"state"`
		}
		if err := s.call(http.MethodGet, "rescan", url.Values{"session": {s.session}}, nil, &answer); err != nil {
			return nil, err
		}
		if s.RescanProgress != nil {
			s.RescanProgress(answer.Done, answer.Total)
		}
		switch {
		case answer.Error != "":
			return nil, errors.New("the receiver could not rescan the target: " + answer.Error)
		case !answer.Rescanning && answer.State != nil:
			return answer.State, nil
		case !answer.Rescanning:
			return nil, errors.New("the receiver ended the rescan without a record")
		}
		if err := vmSleep(s.ctx, s.delay/2+250*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

func (s *vmRemoteSink) Write(index int, data []byte, hash string) (int64, error) {
	body := data
	query := url.Values{"session": {s.session}, "index": {strconv.Itoa(index)}, "hash": {hash}}
	if hash == vmCopyZeroHash {
		body = nil
	} else if s.Compress {
		var packed bytes.Buffer
		writer, _ := flate.NewWriter(&packed, flate.BestSpeed)
		writer.Write(data)
		writer.Close()
		if packed.Len() < len(data) {
			body = packed.Bytes()
			query.Set("enc", "deflate")
		}
	}
	if err := s.Throttle.Wait(s.ctx, int64(len(body))); err != nil {
		return 0, err
	}
	var answer struct {
		Written int64 `json:"written"`
	}
	err := s.call(http.MethodPut, "chunk", query, body, &answer)
	if err == nil {
		s.sentMu.Lock()
		s.sent += int64(len(body))
		s.sentMu.Unlock()
	}
	return answer.Written, err
}

func (s *vmRemoteSink) Checkpoint(state *vmCopyState) error {
	raw, _ := json.Marshal(vmReceiverFinish{Session: s.session, State: &vmCopyState{Passes: state.Passes}})
	return s.call(http.MethodPost, "checkpoint", nil, raw, nil)
}

func (s *vmRemoteSink) Finish(ctx context.Context, state *vmCopyState, verify bool) (*vmCopyState, error) {
	raw, _ := json.Marshal(vmReceiverFinish{Session: s.session, State: state, Verify: verify})
	var answer struct {
		State *vmCopyState `json:"state"`
	}
	if err := s.call(http.MethodPost, "finish", nil, raw, &answer); err != nil {
		return nil, err
	}
	if answer.State == nil {
		return nil, errors.New("the receiver did not return the finished copy record")
	}
	return answer.State, nil
}

func (s *vmRemoteSink) Close() error {
	if s.session == "" {
		return nil
	}
	raw, _ := json.Marshal(vmReceiverFinish{Session: s.session})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	saved := s.ctx
	s.ctx = ctx
	err := s.call(http.MethodPost, "close", nil, raw, nil)
	s.ctx, s.session = saved, ""
	return err
}
