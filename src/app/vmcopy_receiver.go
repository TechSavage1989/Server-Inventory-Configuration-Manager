package main

// Receiver mode: the target host (for example the Hyper-V server) runs a
// receiver that writes VHDX files into one folder, and the computer that can
// read the source disks sends chunks to it over HTTPS. Only chunks whose
// hash changed travel, zero chunks travel as a hash alone, and every chunk is
// checked against its SHA-256 before it is written.
//
// The receiver makes a new self-signed certificate and a random pairing code
// each time it starts. The sender must be given both: it accepts only the
// certificate with that SHA-256 thumbprint and sends the code with every
// request. Nothing else is trusted, so no certificate authority or Windows
// trust store change is needed.

import (
	"bytes"
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
}

type vmReceiverSession struct {
	mu   sync.Mutex
	name string
	sink vmReceiverTarget
	seen time.Time
}

type vmReceiverOpen struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Size      int64  `json:"size"`
	ChunkSize int64  `json:"chunkSize"`
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
		NotAfter:     time.Now().Add(30 * 24 * time.Hour),
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
	code, err := vmPairingCode()
	if err != nil {
		return nil, err
	}
	hostName, _ := os.Hostname()
	certificate, thumbprint, err := vmSelfSignedCertificate(hostName)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	r := &vmReceiver{folder: folder, records: records, disks: disks, code: code, thumbprint: thumbprint, listener: listener, log: log, sessions: map[string]*vmReceiverSession{}, names: map[string]string{}}
	r.server = &http.Server{
		Handler:           r,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go r.server.ServeTLS(listener, "", "")
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
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func (r *vmReceiver) ActiveSessions() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.sessions) }

// Stop closes the listener and every open disk, saving its copy record so
// the sender can resume against a new receiver later.
func (r *vmReceiver) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := r.server.Shutdown(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, session := range r.sessions {
		session.mu.Lock()
		session.sink.Close()
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
	if !r.authorized(request) {
		time.Sleep(time.Second)
		vmReceiverError(w, http.StatusUnauthorized, "the pairing code is wrong")
		return
	}
	switch strings.TrimPrefix(request.URL.Path, vmReceiverAPI) {
	case "hello":
		vmReceiverJSON(w, map[string]string{"app": "Server Inventory and Migration Manager receiver"})
	case "disks":
		vmReceiverJSON(w, map[string]interface{}{"folder": r.folder != "", "disks": r.Disks()})
	case "open":
		r.open(w, request)
	case "chunk":
		r.chunk(w, request)
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
	if id, busy := r.names[key]; busy {
		// A sender that vanished (lost connection, closed application)
		// leaves its session open; after two idle minutes a new sender may
		// take over, resuming from the saved record.
		if old := r.sessions[id]; old != nil && old.mu.TryLock() {
			if time.Since(old.seen) > 2*time.Minute {
				old.sink.Close()
				delete(r.sessions, id)
				delete(r.names, key)
				busy = false
			}
			old.mu.Unlock()
		}
		if !busy {
			goto claim
		}
		r.mu.Unlock()
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
	r.mu.Lock()
	r.sessions[id] = &vmReceiverSession{name: key, sink: sink, seen: time.Now()}
	r.names[key] = id
	r.mu.Unlock()
	r.log(fmt.Sprintf("Receiving %s (%s, resumed: %t).", body.Name, vmFormatBytes(body.Size), resumed))
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
		data, err = io.ReadAll(io.LimitReader(request.Body, length+1))
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
	written, err := session.sink.Write(index, data, hash)
	if err != nil {
		vmReceiverError(w, http.StatusInternalServerError, err.Error())
		return
	}
	vmReceiverJSON(w, map[string]int64{"written": written})
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
		if body.State == nil {
			body.State = &vmCopyState{Passes: session.sink.State().Passes}
		}
		err = session.sink.Checkpoint(body.State)
	case "finish":
		if body.State == nil || len(body.State.Hashes) != len(session.sink.State().Hashes) {
			err = errors.New("the finish request does not describe this disk")
			break
		}
		state, err = session.sink.Finish(request.Context(), body.State, body.Verify)
		if err == nil {
			state.Target = filepath.Base(state.Target)
			r.log(fmt.Sprintf("Finished %s (verified: %t).", state.Target, state.Verified))
		}
	case "close":
		err = session.sink.Close()
		r.mu.Lock()
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
	transport := &http.Transport{
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
	sink := &vmRemoteSink{ctx: ctx, client: &http.Client{Transport: transport, Timeout: 10 * time.Minute}, base: &url.URL{Scheme: "https", Host: net.JoinHostPort(host, port), Path: vmReceiverAPI}, code: code, retries: 4, delay: 2 * time.Second}
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
		request, err := http.NewRequestWithContext(s.ctx, method, u.String(), bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+s.code)
		response, err := s.client.Do(request)
		if err != nil {
			last = err
			if s.ctx.Err() != nil {
				return s.ctx.Err()
			}
			if strings.Contains(err.Error(), "thumbprint") {
				return err
			}
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<20))
		response.Body.Close()
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
		if response.StatusCode < 500 {
			return last
		}
	}
	return last
}

func (s *vmRemoteSink) Open(source string, size, chunkSize int64) (*vmCopyState, bool, error) {
	raw, _ := json.Marshal(vmReceiverOpen{Name: s.name, Source: source, Size: size, ChunkSize: chunkSize})
	var opened vmReceiverOpened
	if err := s.call(http.MethodPost, "open", nil, raw, &opened); err != nil {
		return nil, false, err
	}
	if opened.State == nil || opened.State.Size != size || int64(len(opened.State.Hashes)) != (size+chunkSize-1)/chunkSize {
		return nil, false, errors.New("the receiver returned a copy record that does not match this disk")
	}
	s.session = opened.Session
	return opened.State, opened.Resumed, nil
}

func (s *vmRemoteSink) Write(index int, data []byte, hash string) (int64, error) {
	body := data
	if hash == vmCopyZeroHash {
		body = nil
	}
	var answer struct {
		Written int64 `json:"written"`
	}
	err := s.call(http.MethodPut, "chunk", url.Values{"session": {s.session}, "index": {strconv.Itoa(index)}, "hash": {hash}}, body, &answer)
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
