package main

// Target platforms for phase 3: create the new VM on Scale Computing
// HyperCore, Nutanix AHV (Prism Central v3) or Proxmox VE, boot it from the
// receiver media, and at cutover switch it to boot from its own disk.
//
// Only these changes are made on a platform: upload the receiver ISO, create
// a new VM whose name is not already used (never change or delete an existing
// VM), start it, and at cutover power off that same VM (it is running the
// receiver, not a guest operating system), set it to boot from disk and start
// it. Credentials stay in memory and never reach logs, errors or files. The
// certificate is checked like vCenter's: the Windows trust store first, then
// only the exact SHA-256 thumbprint the operator trusted.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type vmTargetConnection struct {
	Platform string `json:"platform"`
	Server   string `json:"server"`
	// User is the HyperCore or Prism user, or for Proxmox VE the API token
	// ID (user@realm!token). Password is the password or token secret.
	User       string `json:"user"`
	Password   string `json:"-"`
	Thumbprint string `json:"thumbprint"`
	// Node is the Proxmox VE node, or the Nutanix cluster name.
	Node string `json:"node"`

	// client replaces the HTTP client in tests.
	client *http.Client
}

type vmTargetDisk struct {
	Size int64  `json:"size"`
	Bus  string `json:"bus"` // virtio, scsi, sata or ide
}

type vmTargetNIC struct {
	Network string `json:"network"` // bridge, subnet UUID or HyperCore VLAN
	VLAN    int    `json:"vlan"`
	MAC     string `json:"mac"`
	Model   string `json:"model"` // virtio or e1000
}

type vmTargetVMSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	CPUs        int            `json:"cpus"`
	MemoryMiB   int64          `json:"memoryMiB"`
	Firmware    string         `json:"firmware"` // bios or uefi
	SecureBoot  bool           `json:"secureBoot"`
	TPM         bool           `json:"tpm"`
	Windows     bool           `json:"windows"`
	Storage     string         `json:"storage"`
	Disks       []vmTargetDisk `json:"disks"`
	NICs        []vmTargetNIC  `json:"nics"`
	// ISO is the platform's reference to the uploaded receiver media.
	ISO string `json:"iso"`
	// MachineType overrides the HyperCore machine type.
	MachineType string `json:"machineType,omitempty"`
}

type vmTargetVM struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"` // running, stopped or the platform's word
}

type vmTargetChoice struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

type vmTargetInventory struct {
	Platform string           `json:"platform"`
	Version  string           `json:"version"`
	Nodes    []vmTargetChoice `json:"nodes"`
	Networks []vmTargetChoice `json:"networks"`
	Storages []vmTargetChoice `json:"storages"`
}

type vmTargetPlatform interface {
	// Inventory reads the version, nodes, networks and storage without
	// changing anything.
	Inventory(ctx context.Context) (*vmTargetInventory, error)
	UploadISO(ctx context.Context, name string, data io.Reader, size int64) (string, error)
	FindVM(ctx context.Context, name string) (*vmTargetVM, error)
	CreateVM(ctx context.Context, spec vmTargetVMSpec) (*vmTargetVM, error)
	Start(ctx context.Context, id string) error
	PowerOff(ctx context.Context, id string) error
	BootFromDisk(ctx context.Context, id string) error
	State(ctx context.Context, id string) (string, error)
}

var vmTargetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// vmTargetHTTP is the shared HTTPS client of the platform clients.
type vmTargetHTTP struct {
	base        *url.URL
	client      *http.Client
	certificate *vmCertificateError
	authorize   func(*http.Request)
	pollDelay   time.Duration
}

func vmTargetBaseURL(server string, defaultPort string, path string) (*url.URL, error) {
	server = strings.TrimSpace(server)
	if lower := strings.ToLower(server); strings.HasPrefix(lower, "https://") {
		parsed, err := url.Parse(server)
		if err != nil || parsed.Host == "" {
			return nil, errors.New("type the server's host name, for example cluster.example.com")
		}
		server = parsed.Host
	} else if strings.Contains(server, "://") {
		return nil, errors.New("only HTTPS connections are supported")
	}
	server = strings.TrimSuffix(server, "/")
	if !vmServerPattern.MatchString(server) {
		return nil, errors.New("type the server's host name, for example cluster.example.com")
	}
	if _, _, err := net.SplitHostPort(server); err != nil && defaultPort != "" {
		server = net.JoinHostPort(strings.Trim(server, "[]"), defaultPort)
	}
	return &url.URL{Scheme: "https", Host: server, Path: path}, nil
}

func newVMTargetHTTP(connection vmTargetConnection, defaultPort, path string) (*vmTargetHTTP, error) {
	base, err := vmTargetBaseURL(connection.Server, defaultPort, path)
	if err != nil {
		return nil, err
	}
	h := &vmTargetHTTP{base: base, pollDelay: 2 * time.Second}
	if connection.client != nil {
		h.client = connection.client
		return h, nil
	}
	host, _, _ := net.SplitHostPort(base.Host)
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       vmTLSConfig(strings.Trim(host, "[]"), vmNormalizeThumbprint(connection.Thumbprint), &h.certificate),
		ResponseHeaderTimeout: 2 * time.Minute,
	}
	h.client = &http.Client{Transport: transport, Timeout: 0}
	return h, nil
}

type vmTargetHTTPError struct {
	Status  int
	Message string
}

func (e *vmTargetHTTPError) Error() string { return e.Message }

// do sends one request; body may be nil, a []byte (JSON) or an io.Reader with
// a content type. out receives the decoded JSON answer.
func (h *vmTargetHTTP) do(ctx context.Context, method, path string, query url.Values, body interface{}, contentType string, size int64, out interface{}) error {
	u := *h.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = query.Encode()
	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(value)
		if contentType == "" {
			contentType = "application/json"
		}
		size = int64(len(value))
	case io.Reader:
		reader = value
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		reader, size = bytes.NewReader(raw), int64(len(raw))
		if contentType == "" {
			contentType = "application/json"
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	if reader != nil && size > 0 {
		request.ContentLength = size
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	if h.authorize != nil {
		h.authorize(request)
	}
	response, err := h.client.Do(request)
	if err != nil {
		if h.certificate != nil {
			return h.certificate
		}
		var tlsErr *tls.CertificateVerificationError
		if errors.As(err, &tlsErr) {
			return err
		}
		return fmt.Errorf("could not reach %s: %w", h.base.Host, vmTargetStripURL(err))
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		message := vmTargetErrorText(raw)
		switch response.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			message = "the server refused the user name, password or token (" + response.Status + ")"
			if text := vmTargetErrorText(raw); text != "" {
				message += ": " + text
			}
		default:
			if message == "" {
				message = response.Status
			} else {
				message = response.Status + ": " + message
			}
		}
		return &vmTargetHTTPError{Status: response.StatusCode, Message: message}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("the server's answer to %s %s was not understood: %w", method, path, err)
		}
	}
	return nil
}

// vmTargetStripURL keeps query strings (which never hold secrets here, but
// could hold IDs) out of connection errors.
func vmTargetStripURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

func vmTargetErrorText(raw []byte) string {
	var parsed map[string]interface{}
	if json.Unmarshal(raw, &parsed) != nil {
		text := strings.TrimSpace(string(raw))
		if len(text) > 300 {
			text = text[:300]
		}
		return text
	}
	for _, key := range []string{"message", "error", "errors", "message_list", "status"} {
		if value, ok := parsed[key]; ok && value != nil {
			raw, _ := json.Marshal(value)
			text := strings.Trim(string(raw), `"`)
			if len(text) > 300 {
				text = text[:300]
			}
			return text
		}
	}
	return ""
}

func (h *vmTargetHTTP) sleep(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(h.pollDelay):
		return nil
	}
}

func newVMTargetPlatform(connection vmTargetConnection) (vmTargetPlatform, error) {
	switch connection.Platform {
	case vmTargetScale:
		return newVMHyperCore(connection)
	case vmTargetNutanix:
		return newVMNutanix(connection)
	case vmTargetProxmox:
		return newVMProxmox(connection)
	}
	return nil, fmt.Errorf("%s has no VM creation here; choose Scale HyperCore, Nutanix AHV or Proxmox VE", vmTargetLabel(connection.Platform))
}

// vmValidateTargetSpec checks a VM specification before anything is sent.
func vmValidateTargetSpec(spec *vmTargetVMSpec) error {
	if !vmTargetNamePattern.MatchString(spec.Name) {
		return errors.New("the VM name must be 1 to 63 letters, digits, dots, dashes or underscores")
	}
	if spec.CPUs < 1 || spec.CPUs > 240 {
		return errors.New("choose between 1 and 240 virtual CPUs")
	}
	if spec.MemoryMiB < 256 || spec.MemoryMiB > 12<<20 {
		return errors.New("choose between 256 MiB and 12 TiB of memory")
	}
	switch spec.Firmware {
	case "":
		spec.Firmware = "bios"
	case "bios", "uefi":
	default:
		return errors.New("firmware must be BIOS or UEFI")
	}
	if spec.Firmware == "bios" && (spec.SecureBoot || spec.TPM) {
		return errors.New("Secure Boot and a virtual TPM need UEFI firmware")
	}
	if len(spec.Disks) == 0 || len(spec.Disks) > 16 {
		return errors.New("add between 1 and 16 disks")
	}
	for index := range spec.Disks {
		disk := &spec.Disks[index]
		if disk.Size < 1<<20 {
			return fmt.Errorf("disk %d has no size", index)
		}
		switch disk.Bus {
		case "":
			disk.Bus = "virtio"
		case "virtio", "scsi", "sata", "ide":
		default:
			return fmt.Errorf("disk %d: %q is not a disk bus", index, disk.Bus)
		}
	}
	if len(spec.NICs) > 8 {
		return errors.New("add at most 8 network adapters")
	}
	for index := range spec.NICs {
		nic := &spec.NICs[index]
		switch nic.Model {
		case "":
			nic.Model = "virtio"
		case "virtio", "e1000":
		default:
			return fmt.Errorf("adapter %d: %q is not an adapter model", index, nic.Model)
		}
		if nic.VLAN < 0 || nic.VLAN > 4094 {
			return fmt.Errorf("adapter %d: VLAN must be 0 to 4094", index)
		}
		if nic.MAC != "" {
			mac, err := net.ParseMAC(nic.MAC)
			if err != nil || len(mac) != 6 {
				return fmt.Errorf("adapter %d: %q is not a MAC address", index, nic.MAC)
			}
			nic.MAC = strings.ToUpper(mac.String())
		}
	}
	if spec.ISO == "" {
		return errors.New("upload the receiver media first")
	}
	return nil
}

// vmTargetCreate makes the new VM and starts it from the receiver media. It
// never reuses or changes a VM that already has the name.
func vmTargetCreate(ctx context.Context, platform vmTargetPlatform, spec vmTargetVMSpec, log func(string)) (*vmTargetVM, error) {
	if log == nil {
		log = func(string) {}
	}
	if err := vmValidateTargetSpec(&spec); err != nil {
		return nil, err
	}
	existing, err := platform.FindVM(ctx, spec.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("a VM named %s already exists (%s); choose another name. Nothing was changed", spec.Name, existing.ID)
	}
	log(fmt.Sprintf("Creating %s with %d disks.", spec.Name, len(spec.Disks)))
	vm, err := platform.CreateVM(ctx, spec)
	if err != nil {
		return nil, err
	}
	log(fmt.Sprintf("Starting %s from the receiver media.", spec.Name))
	if err := platform.Start(ctx, vm.ID); err != nil {
		return vm, fmt.Errorf("%s was created but did not start: %w", spec.Name, err)
	}
	vm.State = "running"
	return vm, nil
}

// vmTargetCutover is the last step after the final copy round: power off
// the receiver, boot from disk, start the migrated server.
func vmTargetCutover(ctx context.Context, platform vmTargetPlatform, id string, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	state, err := platform.State(ctx, id)
	if err != nil {
		return err
	}
	if state != "stopped" {
		log("Turning off the receiver.")
		if err := platform.PowerOff(ctx, id); err != nil {
			return err
		}
		deadline := time.Now().Add(5 * time.Minute)
		for {
			if state, err = platform.State(ctx, id); err != nil {
				return err
			}
			if state == "stopped" {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("the VM did not turn off within five minutes")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	log("Setting the VM to start from its disk.")
	if err := platform.BootFromDisk(ctx, id); err != nil {
		return err
	}
	log("Starting the migrated server.")
	return platform.Start(ctx, id)
}

func vmCeilDiv(value, unit int64) int64 { return (value + unit - 1) / unit }
