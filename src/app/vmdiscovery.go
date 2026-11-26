package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Receiver discovery. A computer looking for receivers sends one "find"
// request to the local network's broadcast address (or to one host the
// operator names); each running receiver answers that computer directly with
// its name, port, certificate thumbprint and what it writes. Receivers never
// announce themselves unasked and nothing is scanned: there is one request
// per network adapter per search. The answer carries no pairing code, and a
// found receiver is never trusted on its own: the operator compares its
// fingerprint with the one on the receiver's screen, and the code comes
// either from the screen or from the receiver's own approval (vmReceiver
// pairing).

const (
	vmDiscoveryPort    = 44379
	vmDiscoveryQuery   = "SIMM-FIND/1 "
	vmDiscoveryMaxSize = 2048
)

// vmReceiverAnnouncement is a receiver's answer to a find request.
type vmReceiverAnnouncement struct {
	SIMM        int      `json:"simm"`
	Nonce       string   `json:"nonce"`
	Name        string   `json:"name"`
	Port        int      `json:"port"`
	Thumbprint  string   `json:"thumbprint"`
	Folder      bool     `json:"folder"`
	Disks       int      `json:"disks"`
	DiskBytes   int64    `json:"diskBytes,omitempty"`
	FreeBytes   uint64   `json:"freeBytes,omitempty"`
	Version     string   `json:"version,omitempty"`
	Pairing     bool     `json:"pairing"`
	Addresses   []string `json:"addresses,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	// Seen is filled by the finder: the address the answer came from.
	Seen []string `json:"seen,omitempty"`
}

// vmAnnouncedAddresses keeps the plain IP addresses a find answer lists, as
// address:port on the receiver's own port, without the address it was heard
// from, at most replMaxAlternates.
func vmAnnouncedAddresses(list []string, port int, seen string) []string {
	out := []string{}
	for _, text := range list {
		ip := net.ParseIP(strings.Trim(strings.TrimSpace(text), "[]"))
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		address := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		if strings.EqualFold(address, seen) || vmContains(out, address) {
			continue
		}
		out = append(out, address)
		if len(out) == replMaxAlternates {
			break
		}
	}
	return out
}

// vmFingerprint is the short form of a thumbprint people compare by eye:
// the first 16 hexadecimal characters in groups of four.
func vmFingerprint(thumbprint string) string {
	t := strings.ToUpper(vmNormalizeThumbprint(thumbprint))
	if len(t) < 16 {
		return ""
	}
	return t[0:4] + " " + t[4:8] + " " + t[8:12] + " " + t[12:16]
}

// vmDiscoveryResponder answers find requests for one receiver.
type vmDiscoveryResponder struct {
	conn    net.PacketConn
	info    func() vmReceiverAnnouncement
	mu      sync.Mutex
	window  time.Time
	answers int
	done    chan struct{}
}

// startVMDiscoveryResponder listens on the discovery port. A port already
// in use (another receiver on this computer) is not an error for the
// receiver: it simply is not found automatically.
func startVMDiscoveryResponder(address string, info func() vmReceiverAnnouncement) (*vmDiscoveryResponder, error) {
	conn, err := net.ListenPacket("udp4", address)
	if err != nil {
		return nil, err
	}
	r := &vmDiscoveryResponder{conn: conn, info: info, done: make(chan struct{})}
	go r.serve()
	return r, nil
}

func (r *vmDiscoveryResponder) Addr() net.Addr { return r.conn.LocalAddr() }

func (r *vmDiscoveryResponder) Close() error {
	err := r.conn.Close()
	<-r.done
	return err
}

// allow limits answers to 20 a second, so the receiver cannot be used to
// flood anyone.
func (r *vmDiscoveryResponder) allow(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.window) >= time.Second {
		r.window, r.answers = now, 0
	}
	if r.answers >= 20 {
		return false
	}
	r.answers++
	return true
}

func (r *vmDiscoveryResponder) serve() {
	defer close(r.done)
	buffer := make([]byte, vmDiscoveryMaxSize)
	for {
		n, from, err := r.conn.ReadFrom(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		text := string(buffer[:n])
		if !strings.HasPrefix(text, vmDiscoveryQuery) {
			continue
		}
		nonce := strings.TrimSpace(strings.TrimPrefix(text, vmDiscoveryQuery))
		if len(nonce) != 32 {
			continue
		}
		if _, err := hex.DecodeString(nonce); err != nil || !r.allow(time.Now()) {
			continue
		}
		answer := r.info()
		answer.SIMM, answer.Nonce = 1, nonce
		answer.Fingerprint = vmFingerprint(answer.Thumbprint)
		raw, err := json.Marshal(answer)
		if err != nil || len(raw) > vmDiscoveryMaxSize {
			continue
		}
		r.conn.WriteTo(raw, from)
	}
}

// vmBroadcastTargets lists where one search goes: the limited broadcast
// address and each IPv4 adapter's own broadcast address. Loopback is added
// so a receiver on this computer is found too.
func vmBroadcastTargets(port int) []string {
	seen := map[string]bool{}
	targets := []string{}
	add := func(ip net.IP) {
		key := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		if !seen[key] {
			seen[key] = true
			targets = append(targets, key)
		}
	}
	add(net.IPv4bcast)
	add(net.IPv4(127, 0, 0, 1))
	interfaces, _ := net.Interfaces()
	for _, adapter := range interfaces {
		if adapter.Flags&net.FlagUp == 0 || adapter.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addresses, _ := adapter.Addrs()
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			ip := network.IP.To4()
			if ip == nil || len(network.Mask) != net.IPv4len {
				continue
			}
			broadcast := make(net.IP, 4)
			for i := range ip {
				broadcast[i] = ip[i] | ^network.Mask[i]
			}
			add(broadcast)
		}
	}
	return targets
}

// vmFindReceivers sends one find request to each target and collects the
// answers that arrive within wait. Answers are merged by thumbprint, with
// every address each was heard from.
func vmFindReceivers(ctx context.Context, targets []string, wait time.Duration) ([]vmReceiverAnnouncement, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(nonceBytes)
	query := []byte(vmDiscoveryQuery + nonce)
	sent := 0
	for _, target := range targets {
		address, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			continue
		}
		if _, err := conn.WriteTo(query, address); err == nil {
			sent++
		}
	}
	if sent == 0 {
		return nil, errors.New("the search could not be sent on any network adapter")
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	found := map[string]*vmReceiverAnnouncement{}
	buffer := make([]byte, vmDiscoveryMaxSize)
	for {
		conn.SetReadDeadline(deadline)
		n, from, err := conn.ReadFrom(buffer)
		if err != nil {
			break
		}
		var answer vmReceiverAnnouncement
		if json.Unmarshal(buffer[:n], &answer) != nil || answer.SIMM != 1 || answer.Nonce != nonce || answer.Port <= 0 || answer.Port > 65535 {
			continue
		}
		thumbprint := vmNormalizeThumbprint(answer.Thumbprint)
		if thumbprint == "" {
			continue
		}
		answer.Thumbprint, answer.Name, answer.Fingerprint = thumbprint, vmCleanLabel(answer.Name, 64), vmFingerprint(thumbprint)
		answer.Version = vmCleanLabel(answer.Version, 32)
		host, _, _ := net.SplitHostPort(from.String())
		seen := net.JoinHostPort(host, strconv.Itoa(answer.Port))
		// The address we heard it from is the one used; the addresses it
		// lists are only kept as other addresses to try (plain IPs on its
		// own port, nothing trusted: the job's pin still decides).
		answer.Addresses = vmAnnouncedAddresses(answer.Addresses, answer.Port, seen)
		key := thumbprint + "|" + strconv.Itoa(answer.Port)
		if existing := found[key]; existing != nil {
			if !vmContains(existing.Seen, seen) {
				existing.Seen = append(existing.Seen, seen)
			}
			continue
		}
		answer.Seen = []string{seen}
		found[key] = &answer
	}
	list := make([]vmReceiverAnnouncement, 0, len(found))
	for _, answer := range found {
		sort.Strings(answer.Seen)
		list = append(list, *answer)
	}
	sort.Slice(list, func(i, j int) bool {
		if !strings.EqualFold(list[i].Name, list[j].Name) {
			return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
		}
		return list[i].Thumbprint < list[j].Thumbprint
	})
	return list, nil
}

// vmCleanLabel keeps a short, printable label from another computer.
func vmCleanLabel(value string, limit int) string {
	var b strings.Builder
	for _, r := range value {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= limit {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
