package main

// VM console (rule 68): the VNC-based consoles. VMware's WebMKS ticket,
// Proxmox VE's vncproxy, Nutanix Prism's VNC proxy and HyperCore's VNC port
// all lead to an RFB stream, which consoleRFBBackend shows. Opening a console
// changes nothing on the platform: the only calls are AcquireTicket
// ("webmks"), Proxmox's vncproxy (which starts a VNC proxy process for the
// session on the node, as Proxmox's own console does) and reads.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// consoleRFBBackend shows an RFB stream; closeExtra ends whatever else the
// console holds open (a vSphere session).
type consoleRFBBackend struct {
	client     *rfbClient
	closeExtra func()
	once       sync.Once
}

func newConsoleRFBBackend(conn io.ReadWriteCloser, password string, frame *consoleFrame, closeExtra func()) (*consoleRFBBackend, error) {
	client, err := rfbHandshake(conn, password, frame)
	if err != nil {
		conn.Close()
		if closeExtra != nil {
			closeExtra()
		}
		return nil, err
	}
	return &consoleRFBBackend{client: client, closeExtra: closeExtra}, nil
}

// bind sends the cursor shape and the guest's clipboard to the session.
func (b *consoleRFBBackend) bind(s *consoleSession) {
	b.client.onCursor = s.setCursor
	b.client.onClip = s.setClip
}

func (b *consoleRFBBackend) Run(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			b.client.conn.Close()
		case <-done:
		}
	}()
	err := b.client.Run()
	close(done)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (b *consoleRFBBackend) Key(keysym uint32, down bool) error { return b.client.Key(keysym, down) }

func (b *consoleRFBBackend) Pointer(x, y int, buttons byte) error {
	return b.client.Pointer(x, y, buttons)
}

func (b *consoleRFBBackend) TypeText(ctx context.Context, text string, delay time.Duration) error {
	return consoleTypeKeys(ctx, b, text, delay)
}

func (b *consoleRFBBackend) Special(name string) error {
	return consoleSendCombo(b, consoleSpecialKeys[name])
}

func (b *consoleRFBBackend) Clipboard(text string) error { return b.client.Clipboard(text) }

func (b *consoleRFBBackend) Close() error {
	var err error
	b.once.Do(func() {
		err = b.client.Close()
		if b.closeExtra != nil {
			b.closeExtra()
		}
	})
	return err
}

// consoleVNCCaps is what every VNC console offers; platforms add their notes.
func consoleVNCCaps() consoleCaps {
	return consoleCaps{
		Display:      "live",
		Keyboard:     true,
		Mouse:        true,
		TypeText:     true,
		ClipFromWhy:  "The VM's clipboard reaches this window only when the platform's console passes it on, which needs the platform's guest clipboard support.",
		KeyboardNote: "Keys and typed text are sent as a US keyboard. If the VM uses another layout, some symbols may come out differently.",
	}
}

// ---- VMware ----

// consoleVMwareTicket is what AcquireTicket("webmks") returns.
type consoleVMwareTicket struct {
	Ticket     string
	Host       string
	Port       int
	Thumbprint string
}

func (t consoleVMwareTicket) url() *url.URL {
	port := t.Port
	if port <= 0 {
		port = 443
	}
	return &url.URL{Scheme: "wss", Host: net.JoinHostPort(t.Host, strconv.Itoa(port)), Path: "/ticket/" + t.Ticket}
}

// consoleVMwareInfo is what the console reads about the VM before opening.
type consoleVMwareInfo struct {
	Name       string
	PowerState string
	ToolsOK    bool
	GuestFull  string
	HostRef    types.ManagedObjectReference
	HostName   string
}

func consoleVMwareRead(ctx context.Context, vsphere *vmSession, vmID string) (consoleVMwareInfo, error) {
	var machine mo.VirtualMachine
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmID}
	if err := property.DefaultCollector(vsphere.client).RetrieveOne(ctx, ref, []string{"name", "runtime.powerState", "runtime.host", "guest.toolsRunningStatus", "guest.guestFullName"}, &machine); err != nil {
		return consoleVMwareInfo{}, fmt.Errorf("the VM could not be read: %v", vmFriendlyConnectError(err, vsphere.host))
	}
	info := consoleVMwareInfo{Name: machine.Name, PowerState: string(machine.Runtime.PowerState)}
	if machine.Guest != nil {
		info.ToolsOK = machine.Guest.ToolsRunningStatus == string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)
		info.GuestFull = machine.Guest.GuestFullName
	}
	if machine.Runtime.Host != nil {
		info.HostRef = *machine.Runtime.Host
		var host mo.HostSystem
		if err := property.DefaultCollector(vsphere.client).RetrieveOne(ctx, info.HostRef, []string{"name"}, &host); err == nil {
			info.HostName = host.Name
		}
	}
	return info, nil
}

// consoleOpenVMware opens a VM's WebMKS console: a vSphere session (kept
// until the console closes), AcquireTicket("webmks"), and a WebSocket to the
// ESXi host trusted through Windows or the SHA-1 thumbprint vSphere gave with
// the ticket, never anything else.
func consoleOpenVMware(ctx context.Context, options vmConnectOptions, target consoleTarget, frame *consoleFrame, dial func(context.Context, string) (net.Conn, error)) (*consoleRFBBackend, consoleCaps, string, error) {
	caps := consoleVNCCaps()
	vsphere, err := openVSphereSession(ctx, options, func(string) {})
	if err != nil {
		return nil, caps, "", err
	}
	info, err := consoleVMwareRead(ctx, vsphere, target.ID)
	if err != nil {
		vsphere.logout()
		return nil, caps, "", err
	}
	if target.Name != "" && info.Name != target.Name {
		vsphere.logout()
		return nil, caps, "", fmt.Errorf("the VM with this ID is now called %q, not %q; read the inventory again", info.Name, target.Name)
	}
	if info.PowerState != string(types.VirtualMachinePowerStatePoweredOn) {
		vsphere.logout()
		return nil, caps, "", errors.New("the VM is not running, so it has no console to show. Start it in vSphere first")
	}
	request := types.AcquireTicket{This: types.ManagedObjectReference{Type: "VirtualMachine", Value: target.ID}, TicketType: "webmks"}
	answer, err := consoleAcquireTicket(ctx, vsphere.client, &request)
	if err != nil {
		vsphere.logout()
		return nil, caps, "", fmt.Errorf("vSphere did not give a console ticket: %v. The account needs the Virtual machine > Interaction > Console interaction privilege", err)
	}
	ticket := consoleVMwareTicket{Ticket: answer.Ticket, Host: answer.Host, Port: int(answer.Port), Thumbprint: answer.SslThumbprint}
	if ticket.Host == "" {
		ticket.Host, _, _ = net.SplitHostPort(vsphere.host)
		if ticket.Host == "" {
			ticket.Host = vsphere.host
		}
	}
	tlsConfig := vmExportTLSConfig(ticket.Host, ticket.Thumbprint)
	ws, err := consoleDialWebSocket(ctx, ticket.url(), tlsConfig, nil, []string{"binary"}, dial)
	if err != nil {
		vsphere.logout()
		return nil, caps, "", fmt.Errorf("could not open the console on %s: %v. The computer running this application must reach the ESXi host on port %d", ticket.Host, err, cnPort(ticket.Port, 443))
	}
	backend, err := newConsoleRFBBackend(ws, "", frame, vsphere.logout)
	if err != nil {
		return nil, caps, "", fmt.Errorf("the VMware console did not start: %v", err)
	}
	where := "ESXi host " + firstNonEmpty(info.HostName, ticket.Host)
	caps.ClipToGuest = false
	caps.ClipFromWhy = "VMware's web console does not pass the VM's clipboard out (VMware Remote Console does only when the VM allows it with isolation.tools.copy.disable set to FALSE, which this application does not change). Read text from the screen, or save the text to a file in the VM and read it here."
	if info.ToolsOK {
		caps.FilesTo, caps.FilesFrom = true, true
		caps.FilesHow = "Through VMware Tools, already running in the VM (its file transfer only: no program is started in the VM). Needs a user name and password of the VM's operating system."
	} else {
		caps.FilesWhy = "VMware Tools is not running in this VM, so files cannot be copied in or out through vSphere."
	}
	caps.Native = []string{"vmrc"}
	caps.Notes = append(caps.Notes, "Shown through vSphere's web console ticket, from "+where+".")
	return backend, caps, where, nil
}

// consoleAcquireTicket is replaced in tests (vcsim has no WebMKS).
var consoleAcquireTicket = func(ctx context.Context, client soap.RoundTripper, request *types.AcquireTicket) (*types.VirtualMachineTicket, error) {
	response, err := methods.AcquireTicket(ctx, client, request)
	if err != nil {
		return nil, err
	}
	return &response.Returnval, nil
}

func cnPort(port, fallback int) int {
	if port <= 0 {
		return fallback
	}
	return port
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// consoleVMRCURL makes the vmrc:// link that opens VMware Remote Console
// with a one-time clone ticket of the operator's session.
func consoleVMRCURL(server, ticket, vmID string) string {
	host := server
	if h, _, err := net.SplitHostPort(server); err == nil {
		host = h
	}
	return "vmrc://clone:" + url.PathEscape(ticket) + "@" + host + "/?moid=" + url.QueryEscape(vmID)
}

// consolePinnedHostTLS trusts a host only through Windows or the exact
// certificate vCenter reports for it (read over the signed-in session).
func consolePinnedHostTLS(hostName string, pem []byte) *tls.Config {
	var pinned *x509.Certificate
	if certificate, err := consoleParsePEM(pem); err == nil {
		pinned = certificate
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // verified below
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("the host sent no certificate")
			}
			leaf := state.PeerCertificates[0]
			intermediates := x509.NewCertPool()
			for _, c := range state.PeerCertificates[1:] {
				intermediates.AddCert(c)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{DNSName: hostName, Intermediates: intermediates}); err == nil {
				return nil
			}
			if pinned != nil && leaf.Equal(pinned) {
				return nil
			}
			return errors.New("the ESXi host's certificate is not trusted by Windows and is not the one vCenter reports for it")
		},
	}
}

func consoleParsePEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ---- Proxmox VE ----

// consoleProxmoxNode finds the node a VM runs on.
func consoleProxmoxNode(ctx context.Context, p *vmProxmox, vmid string) (string, string, error) {
	var resources []struct {
		VMID   int    `json:"vmid"`
		Name   string `json:"name"`
		Node   string `json:"node"`
		Status string `json:"status"`
		Type   string `json:"type"`
	}
	if err := p.get(ctx, "/cluster/resources", url.Values{"type": {"vm"}}, &resources); err != nil {
		return "", "", err
	}
	for _, r := range resources {
		if strconv.Itoa(r.VMID) == vmid && r.Type != "lxc" {
			return r.Node, r.Status, nil
		}
	}
	return "", "", fmt.Errorf("Proxmox VE has no VM with ID %s", vmid)
}

// consoleOpenProxmox asks for a VNC ticket (vncproxy with websocket=1) and
// connects to vncwebsocket with the API token; the ticket is the VNC password.
func consoleOpenProxmox(ctx context.Context, connection vmTargetConnection, target consoleTarget, frame *consoleFrame, dial func(context.Context, string) (net.Conn, error)) (*consoleRFBBackend, consoleCaps, string, error) {
	caps := consoleVNCCaps()
	p, err := newVMProxmox(connection)
	if err != nil {
		return nil, caps, "", err
	}
	if _, err := strconv.Atoi(target.ID); err != nil {
		return nil, caps, "", errors.New("the Proxmox VE VM ID must be a number")
	}
	node, status, err := consoleProxmoxNode(ctx, p, target.ID)
	if err != nil {
		return nil, caps, "", err
	}
	if status != "running" {
		return nil, caps, "", errors.New("the VM is not running, so it has no console to show. Start it in Proxmox VE first")
	}
	var proxy struct {
		Port   interface{} `json:"port"`
		Ticket string      `json:"ticket"`
	}
	base := "/nodes/" + url.PathEscape(node) + "/qemu/" + url.PathEscape(target.ID)
	if err := p.http.do(ctx, http.MethodPost, base+"/vncproxy", nil, []byte(url.Values{"websocket": {"1"}}.Encode()), "application/x-www-form-urlencoded", 0, &vmProxmoxAnswer{Data: &proxy}); err != nil {
		return nil, caps, "", fmt.Errorf("Proxmox VE did not give a console ticket: %v. The token needs the VM.Console privilege", err)
	}
	port := fmt.Sprint(proxy.Port)
	if proxy.Ticket == "" || port == "" {
		return nil, caps, "", errors.New("Proxmox VE did not give a console ticket")
	}
	u := *p.http.base
	u.Scheme = "wss"
	u.Path = strings.TrimSuffix(u.Path, "/") + base + "/vncwebsocket"
	u.RawQuery = url.Values{"port": {port}, "vncticket": {proxy.Ticket}}.Encode()
	header := http.Header{}
	header.Set("Authorization", "PVEAPIToken="+connection.User+"="+connection.Password)
	host, _, _ := net.SplitHostPort(u.Host)
	var captured *vmCertificateError
	ws, err := consoleDialWebSocket(ctx, &u, vmTLSConfig(strings.Trim(host, "[]"), vmNormalizeThumbprint(connection.Thumbprint), &captured), header, []string{"binary"}, dial)
	if err != nil {
		return nil, caps, "", fmt.Errorf("could not open the Proxmox VE console: %v", err)
	}
	backend, err := newConsoleRFBBackend(ws, proxy.Ticket, frame, nil)
	if err != nil {
		return nil, caps, "", fmt.Errorf("the Proxmox VE console did not start: %v", err)
	}
	caps.ClipToGuest = false
	caps.ClipFromWhy = "Proxmox VE's VNC console does not pass the VM's clipboard out. Read text from the screen, or save the text to a file in the VM and read it here through the QEMU guest agent."
	caps.FilesTo, caps.FilesFrom = true, true
	caps.FilesHow = "Through the QEMU guest agent, if it is already running in the VM (Proxmox VE's agent file-read and file-write). Small files only: up to 60 KB in and 16 MB out."
	caps.Notes = append(caps.Notes, "Shown through Proxmox VE's own VNC console on node "+node+".")
	return backend, caps, "Proxmox VE node " + node, nil
}

// ---- Nutanix ----

// consoleOpenNutanix connects to Prism's VNC proxy for the VM
// (/vnc/vm/<uuid>/proxy, with the VM's cluster for Prism Central).
func consoleOpenNutanix(ctx context.Context, connection vmTargetConnection, target consoleTarget, frame *consoleFrame, dial func(context.Context, string) (net.Conn, error)) (*consoleRFBBackend, consoleCaps, string, error) {
	caps := consoleVNCCaps()
	n, err := newVMNutanix(connection)
	if err != nil {
		return nil, caps, "", err
	}
	var vm vmNutanixEntity
	if err := n.http.do(ctx, http.MethodGet, "/vms/"+url.PathEscape(target.ID), nil, nil, "", 0, &vm); err != nil {
		return nil, caps, "", fmt.Errorf("Prism could not read the VM: %v", err)
	}
	cluster := ""
	if spec, ok := vm.Spec["cluster_reference"].(map[string]interface{}); ok {
		cluster, _ = spec["uuid"].(string)
	}
	if resources, ok := vm.Status["resources"].(map[string]interface{}); ok {
		if state, _ := resources["power_state"].(string); state != "" && !strings.EqualFold(state, "ON") {
			return nil, caps, "", errors.New("the VM is not running, so it has no console to show. Start it in Prism first")
		}
	}
	u := *n.http.base
	u.Scheme = "wss"
	u.Path = "/vnc/vm/" + url.PathEscape(target.ID) + "/proxy"
	if cluster != "" {
		u.RawQuery = url.Values{"proxyClusterUuid": {cluster}}.Encode()
	}
	header := http.Header{}
	request, _ := http.NewRequest(http.MethodGet, "https://x/", nil)
	request.SetBasicAuth(connection.User, connection.Password)
	header.Set("Authorization", request.Header.Get("Authorization"))
	host, _, _ := net.SplitHostPort(u.Host)
	var captured *vmCertificateError
	ws, err := consoleDialWebSocket(ctx, &u, vmTLSConfig(strings.Trim(host, "[]"), vmNormalizeThumbprint(connection.Thumbprint), &captured), header, []string{"binary"}, dial)
	if err != nil {
		return nil, caps, "", fmt.Errorf("could not open the Prism console: %v", err)
	}
	backend, err := newConsoleRFBBackend(ws, "", frame, nil)
	if err != nil {
		return nil, caps, "", fmt.Errorf("the Prism console did not start: %v", err)
	}
	caps.ClipFromWhy = "Prism's VNC console does not pass the VM's clipboard out, and Nutanix offers no file copy through its console. Read text from the screen."
	caps.FilesWhy = "Prism has no API to copy files into or out of a VM's operating system."
	caps.Notes = append(caps.Notes, "Shown through Prism's own VNC console proxy.")
	return backend, caps, "Prism " + n.http.base.Hostname(), nil
}

// ---- Scale HyperCore ----

// consoleOpenHyperCore reads the VM's VNC address from HyperCore
// (VirDomain console ip and port) and connects to it.
func consoleOpenHyperCore(ctx context.Context, connection vmTargetConnection, target consoleTarget, frame *consoleFrame, dial func(context.Context, string) (net.Conn, error)) (*consoleRFBBackend, consoleCaps, string, error) {
	caps := consoleVNCCaps()
	c, err := newVMHyperCore(connection)
	if err != nil {
		return nil, caps, "", err
	}
	var domains []struct {
		UUID    string `json:"uuid"`
		Name    string `json:"name"`
		State   string `json:"state"`
		Console struct {
			Type string `json:"type"`
			IP   string `json:"ip"`
			Port int    `json:"port"`
		} `json:"console"`
	}
	if err := c.http.do(ctx, http.MethodGet, "/VirDomain/"+url.PathEscape(target.ID), nil, nil, "", 0, &domains); err != nil {
		return nil, caps, "", fmt.Errorf("HyperCore could not read the VM: %v", err)
	}
	if len(domains) == 0 {
		return nil, caps, "", errors.New("HyperCore has no VM with this ID")
	}
	domain := domains[0]
	if !strings.EqualFold(domain.State, "RUNNING") {
		return nil, caps, "", errors.New("the VM is not running, so it has no console to show. Start it in HyperCore first")
	}
	if domain.Console.Port <= 0 {
		return nil, caps, "", errors.New("HyperCore did not report a VNC console for this VM. Open it in HyperCore's own web interface instead")
	}
	address := domain.Console.IP
	if address == "" {
		address = c.http.base.Hostname()
	}
	if dial == nil {
		dialer := &net.Dialer{Timeout: 20 * time.Second}
		dial = func(ctx context.Context, a string) (net.Conn, error) { return dialer.DialContext(ctx, "tcp", a) }
	}
	conn, err := dial(ctx, net.JoinHostPort(address, strconv.Itoa(domain.Console.Port)))
	if err != nil {
		return nil, caps, "", fmt.Errorf("could not reach the VM's VNC console at %s port %d: %v. The computer running this application must reach the HyperCore node on that port", address, domain.Console.Port, err)
	}
	backend, err := newConsoleRFBBackend(conn, "", frame, nil)
	if err != nil {
		return nil, caps, "", fmt.Errorf("the HyperCore console did not start: %v", err)
	}
	caps.ClipFromWhy = "HyperCore's VNC console does not pass the VM's clipboard out, and HyperCore offers no file copy through its console. Read text from the screen."
	caps.FilesWhy = "HyperCore has no API to copy files into or out of a VM's operating system."
	caps.Notes = append(caps.Notes, "Shown through the VM's VNC console on HyperCore node "+address+". The connection is not encrypted, as HyperCore's own VNC port is not.")
	return backend, caps, "HyperCore node " + address, nil
}
