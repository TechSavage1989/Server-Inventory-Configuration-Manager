package main

// The replication agent's network interface. The console manages agents
// over HTTPS: the agent keeps one long-lived self-signed certificate in its
// protected data folder and the console pins its exact SHA-256 thumbprint
// (read over the administrative share during push install, or typed by the
// operator), and every request carries the agent key the console created at
// install time. The agent stores only the key's SHA-256, never the key.

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
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	replAgentPort    = 44390
	replAgentPath    = "/agent/v1/"
	replAgentApp     = "Server Inventory and Migration Manager replication agent"
	replConfigName   = "agent.json"
	replCertName     = "agent-certificate.pem"
	replKeyName      = "agent-key.pem"
	replThumbName    = "agent-thumbprint.txt"
	replUninstallTag = "uninstall.flag"
)

// replAgentConfig is the agent's own settings file.
type replAgentConfig struct {
	Port    int    `json:"port"`
	KeyHash string `json:"keyHash"`
	// Console names the console that installed the agent, for the log.
	Console string `json:"console,omitempty"`
}

// replNewAgentKey makes a 32-character agent key.
func replNewAgentKey() string {
	raw := make([]byte, 20)
	rand.Read(raw)
	return strings.ToLower(base32.StdEncoding.EncodeToString(raw))
}

func replKeyHash(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

func loadReplAgentConfig(dir string) (replAgentConfig, error) {
	var config replAgentConfig
	raw, err := os.ReadFile(filepath.Join(dir, replConfigName))
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, fmt.Errorf("%s is damaged: %w", replConfigName, err)
	}
	if len(config.KeyHash) != 64 {
		return config, errors.New("the agent has no key; install it again from the console")
	}
	if config.Port == 0 {
		config.Port = replAgentPort
	}
	return config, nil
}

func saveReplAgentConfig(dir string, config replAgentConfig) error {
	raw, _ := json.MarshalIndent(config, "", "  ")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	name := filepath.Join(dir, replConfigName)
	if err := os.WriteFile(name+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(name+".tmp", name)
}

// replAgentCertificate loads the agent's certificate, making one the first
// time. It is valid for ten years so a long replication never has to
// re-pair, and it writes the thumbprint beside it for the console to read.
func replAgentCertificate(dir string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, replCertName), filepath.Join(dir, replKeyName)
	if certificate, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && len(certificate.Certificate) > 0 {
		sum := sha256.Sum256(certificate.Certificate[0])
		thumbprint := hex.EncodeToString(sum[:])
		os.WriteFile(filepath.Join(dir, replThumbName), []byte(thumbprint+"\r\n"), 0644)
		return certificate, thumbprint, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	host, _ := os.Hostname()
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: replAgentApp + " on " + host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	thumbprint := hex.EncodeToString(sum[:])
	os.WriteFile(filepath.Join(dir, replThumbName), []byte(thumbprint+"\r\n"), 0644)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, thumbprint, nil
}

// replAgentServer serves the agent's interface.
type replAgentServer struct {
	agent      *replAgent
	keyHash    string
	thumbprint string
	listener   net.Listener
	server     *http.Server
	version    string
}

func startReplAgentServer(address string, agent *replAgent, config replAgentConfig, certificate tls.Certificate, thumbprint, version string) (*replAgentServer, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	s := &replAgentServer{agent: agent, keyHash: config.KeyHash, thumbprint: thumbprint, listener: listener, version: version}
	s.server = &http.Server{
		Handler:           s,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go s.server.ServeTLS(listener, "", "")
	return s, nil
}

func (s *replAgentServer) Port() int { return s.listener.Addr().(*net.TCPAddr).Port }

func (s *replAgentServer) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.server.Shutdown(ctx)
}

func (s *replAgentServer) authorized(request *http.Request) bool {
	given := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	if given == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(replKeyHash(given)), []byte(s.keyHash)) == 1
}

func (s *replAgentServer) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(request.URL.Path, replAgentPath) {
		vmReceiverError(w, http.StatusNotFound, "not found")
		return
	}
	if !s.authorized(request) {
		time.Sleep(time.Second)
		vmReceiverError(w, http.StatusUnauthorized, "the agent key is wrong")
		return
	}
	action := strings.TrimPrefix(request.URL.Path, replAgentPath)
	decode := func(out interface{}) bool {
		if request.Method != http.MethodPost || json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(out) != nil {
			vmReceiverError(w, http.StatusBadRequest, "bad request")
			return false
		}
		return true
	}
	switch action {
	case "hello":
		host, _ := os.Hostname()
		answer := map[string]interface{}{"app": replAgentApp, "version": s.version, "computer": host, "platform": s.agent.platform.Name(), "os": runtime.GOOS}
		s.agent.mu.Lock()
		if s.agent.lastStop != "" {
			answer["lastStop"], answer["lastStopAt"] = s.agent.lastStop, s.agent.lastStopAt
		}
		s.agent.mu.Unlock()
		vmReceiverJSON(w, answer)
	case "disks":
		disks, err := s.agent.platform.ListDisks()
		if err != nil {
			vmReceiverError(w, http.StatusInternalServerError, err.Error())
			return
		}
		vmReceiverJSON(w, map[string]interface{}{"disks": disks})
	case "jobs":
		vmReceiverJSON(w, map[string]interface{}{"jobs": s.agent.Jobs(), "alerts": s.agent.Alerts(), "time": s.agent.now().UTC()})
	case "job":
		var spec replJobSpec
		if !decode(&spec) {
			return
		}
		view, err := s.agent.SaveJob(spec)
		if err != nil {
			vmReceiverError(w, http.StatusBadRequest, err.Error())
			return
		}
		vmReceiverJSON(w, view)
	case "action":
		var body struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if !decode(&body) {
			return
		}
		if err := s.agent.Action(body.ID, body.Action); err != nil {
			vmReceiverError(w, http.StatusConflict, err.Error())
			return
		}
		vmReceiverJSON(w, map[string]bool{"ok": true})
	case "schedule":
		var body struct {
			ID string     `json:"id"`
			At *time.Time `json:"at"`
		}
		if !decode(&body) {
			return
		}
		if err := s.agent.Schedule(body.ID, body.At); err != nil {
			vmReceiverError(w, http.StatusConflict, err.Error())
			return
		}
		vmReceiverJSON(w, map[string]bool{"ok": true})
	case "log":
		lines, err := s.agent.Log(request.URL.Query().Get("id"))
		if err != nil {
			vmReceiverError(w, http.StatusNotFound, err.Error())
			return
		}
		vmReceiverJSON(w, map[string]interface{}{"lines": lines})
	default:
		vmReceiverError(w, http.StatusNotFound, "not found")
	}
}

// replPinnedClient trusts only the certificate with the given thumbprint.
func replPinnedClient(thumbprint string, timeout time.Duration) (*http.Client, error) {
	pinned := vmNormalizeThumbprint(thumbprint)
	if pinned == "" {
		return nil, errors.New("enter the agent's certificate thumbprint (64 hexadecimal characters)")
	}
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // replaced by the exact thumbprint check below
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("the agent sent no certificate")
				}
				sum := sha256.Sum256(state.PeerCertificates[0].Raw)
				if got := vmNormalizeThumbprint(hex.EncodeToString(sum[:])); got != pinned {
					return fmt.Errorf("the agent's certificate thumbprint is %s, not the one recorded; check that this is the right server", got)
				}
				return nil
			},
		},
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

// replAgentClient is the console's connection to one agent.
type replAgentClient struct {
	address string
	key     string
	client  *http.Client
}

func newReplAgentClient(address, thumbprint, key string) (*replAgentClient, error) {
	client, err := replPinnedClient(thumbprint, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("the agent key is missing")
	}
	return &replAgentClient{address: address, key: key, client: client}, nil
}

func (c *replAgentClient) call(ctx context.Context, method, action string, query url.Values, body, out interface{}) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	u := url.URL{Scheme: "https", Host: c.address, Path: replAgentPath + action, RawQuery: query.Encode()}
	request, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.key)
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &problem)
		if problem.Error == "" {
			problem.Error = response.Status
		}
		return errors.New(problem.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type replAgentHello struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	Computer string `json:"computer"`
	Platform string `json:"platform"`
	OS       string `json:"os"`
	// LastStop says how the previous run ended when it did not stop
	// cleanly (a crash, or ended from outside); empty otherwise.
	LastStop   string    `json:"lastStop,omitempty"`
	LastStopAt time.Time `json:"lastStopAt,omitempty"`
}

func (c *replAgentClient) Hello(ctx context.Context) (replAgentHello, error) {
	var hello replAgentHello
	err := c.call(ctx, http.MethodGet, "hello", nil, nil, &hello)
	if err == nil && hello.App != replAgentApp {
		err = errors.New("that address does not answer as a replication agent")
	}
	return hello, err
}

func (c *replAgentClient) Disks(ctx context.Context) ([]replDiskInfo, error) {
	var answer struct {
		Disks []replDiskInfo `json:"disks"`
	}
	err := c.call(ctx, http.MethodGet, "disks", nil, nil, &answer)
	return answer.Disks, err
}

type replAgentJobs struct {
	Jobs   []replJobView `json:"jobs"`
	Alerts []replAlert   `json:"alerts"`
	Time   time.Time     `json:"time"`
}

func (c *replAgentClient) Jobs(ctx context.Context) (replAgentJobs, error) {
	var answer replAgentJobs
	err := c.call(ctx, http.MethodGet, "jobs", nil, nil, &answer)
	return answer, err
}

func (c *replAgentClient) SaveJob(ctx context.Context, spec replJobSpec) (replJobView, error) {
	var view replJobView
	err := c.call(ctx, http.MethodPost, "job", nil, spec, &view)
	return view, err
}

func (c *replAgentClient) Action(ctx context.Context, id, action string) error {
	return c.call(ctx, http.MethodPost, "action", nil, map[string]string{"id": id, "action": action}, nil)
}

func (c *replAgentClient) Schedule(ctx context.Context, id string, at *time.Time) error {
	return c.call(ctx, http.MethodPost, "schedule", nil, map[string]interface{}{"id": id, "at": at}, nil)
}

func (c *replAgentClient) Log(ctx context.Context, id string) ([]string, error) {
	var answer struct {
		Lines []string `json:"lines"`
	}
	err := c.call(ctx, http.MethodGet, "log", url.Values{"id": {id}}, nil, &answer)
	return answer.Lines, err
}

// runReplAgent runs an agent from its data folder until ctx ends: it loads
// its settings and certificate, starts the jobs and serves the console.
func runReplAgentWith(ctx context.Context, dir string, platform replPlatform, version string, log func(string), notify func(replAlert)) error {
	config, err := loadReplAgentConfig(dir)
	if err != nil {
		return err
	}
	certificate, thumbprint, err := replAgentCertificate(dir)
	if err != nil {
		return fmt.Errorf("the agent certificate could not be made: %w", err)
	}
	agent, err := newReplAgent(dir, platform)
	if err != nil {
		return err
	}
	agent.notify = notify
	if reason, _ := replCheckPreviousStop(dir); reason != "" {
		log("The previous run of the agent did not stop cleanly. " + reason)
		agent.noteUncleanStop(reason)
	}
	if err := replMarkRunning(dir, version); err != nil {
		log("The agent could not record that it is running: " + err.Error())
	}
	server, err := startReplAgentServer(fmt.Sprintf(":%d", config.Port), agent, config, certificate, thumbprint, version)
	if err != nil {
		return fmt.Errorf("the agent could not listen on port %d: %w", config.Port, err)
	}
	log(fmt.Sprintf("Replication agent %s listening on port %d, certificate %s.", version, server.Port(), thumbprint))
	agent.Run(ctx)
	server.Stop()
	replMarkStopped(dir)
	log("Replication agent stopped.")
	return nil
}
