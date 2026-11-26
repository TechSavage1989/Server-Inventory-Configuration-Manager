package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Pairing by approval. A sender that found a receiver (or typed its
// address) can ask for the pairing code instead of typing it: it connects
// with the receiver's exact certificate thumbprint pinned and posts its
// computer name; the receiver's screen shows the request with the sender's
// address and a check number, and the code is handed over only when the
// operator allows it there. The check number is derived from the request
// and the certificate each side sees, so a machine in the middle (which
// must present its own certificate) shows a different number than the
// receiver. Nothing is ever approved automatically.

const (
	vmPairPendingMax = 4
	vmPairExpiry     = 5 * time.Minute
)

type vmPairRequest struct {
	ID       string    `json:"id"`
	Computer string    `json:"computer"`
	Address  string    `json:"address"`
	Check    string    `json:"check"`
	Created  time.Time `json:"created"`
	State    string    `json:"state"` // pending, allowed, denied
}

// vmPairCheck is the six-digit number both screens show for a request.
func vmPairCheck(id, thumbprint string) string {
	sum := sha256.Sum256([]byte("SIMM-PAIR/1|" + strings.ToLower(id) + "|" + vmNormalizeThumbprint(thumbprint)))
	n := binary.BigEndian.Uint32(sum[:4]) % 1000000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000)
}

func vmRemoteHost(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

// Caller holds r.mu.
func (r *vmReceiver) expirePairsLocked(now time.Time) {
	for id, p := range r.pairs {
		if now.Sub(p.Created) > vmPairExpiry {
			delete(r.pairs, id)
		}
	}
}

func (r *vmReceiver) pair(w http.ResponseWriter, request *http.Request) {
	if !r.pairing {
		vmReceiverError(w, http.StatusForbidden, "this receiver does not take pairing requests; type the pairing code shown on its screen")
		return
	}
	from := vmRemoteHost(request)
	now := time.Now()
	switch request.Method {
	case http.MethodPost:
		var body struct {
			Computer string `json:"computer"`
		}
		if json.NewDecoder(io.LimitReader(request.Body, 4096)).Decode(&body) != nil {
			vmReceiverError(w, http.StatusBadRequest, "bad request")
			return
		}
		computer := vmCleanLabel(body.Computer, 64)
		if computer == "" {
			computer = "(no name given)"
		}
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			vmReceiverError(w, http.StatusInternalServerError, "no random numbers")
			return
		}
		id := hex.EncodeToString(raw)
		r.mu.Lock()
		r.expirePairsLocked(now)
		pending := 0
		for oldID, p := range r.pairs {
			if p.Address == from && p.State == "pending" {
				delete(r.pairs, oldID) // one request per sending address
				continue
			}
			if p.State == "pending" {
				pending++
			}
		}
		if pending >= vmPairPendingMax {
			r.mu.Unlock()
			vmReceiverError(w, http.StatusTooManyRequests, "this receiver already has pairing requests waiting; allow or deny them on its screen first")
			return
		}
		p := &vmPairRequest{ID: id, Computer: computer, Address: from, Check: vmPairCheck(id, r.thumbprint), Created: now, State: "pending"}
		r.pairs[id] = p
		r.mu.Unlock()
		r.log(fmt.Sprintf("Pairing request from %s (%s), check number %s. Allow or deny it on this screen.", computer, from, p.Check))
		vmReceiverJSON(w, map[string]string{"id": id})
	case http.MethodGet:
		id := strings.ToLower(request.URL.Query().Get("id"))
		r.mu.Lock()
		r.expirePairsLocked(now)
		p := r.pairs[id]
		if p == nil || p.Address != from {
			r.mu.Unlock()
			vmReceiverJSON(w, map[string]string{"state": "expired"})
			return
		}
		state, code := p.State, ""
		if state == "allowed" {
			code = r.code
			delete(r.pairs, id) // handed over once
		} else if state == "denied" {
			delete(r.pairs, id)
		}
		r.mu.Unlock()
		answer := map[string]string{"state": state}
		if code != "" {
			answer["code"] = code
		}
		vmReceiverJSON(w, answer)
	default:
		vmReceiverError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// PairRequests lists the requests waiting on this receiver's screen.
func (r *vmReceiver) PairRequests() []vmPairRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expirePairsLocked(time.Now())
	list := []vmPairRequest{}
	for _, p := range r.pairs {
		if p.State == "pending" {
			list = append(list, *p)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	return list
}

// AnswerPair allows or denies a waiting request.
func (r *vmReceiver) AnswerPair(id string, allow bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expirePairsLocked(time.Now())
	p := r.pairs[strings.ToLower(id)]
	if p == nil || p.State != "pending" {
		return errors.New("that pairing request has expired or was already answered")
	}
	if allow {
		p.State = "allowed"
		r.log(fmt.Sprintf("Pairing allowed for %s (%s).", p.Computer, p.Address))
	} else {
		p.State = "denied"
		r.log(fmt.Sprintf("Pairing denied for %s (%s).", p.Computer, p.Address))
	}
	return nil
}

// announcement is this receiver's answer to a find request.
func (r *vmReceiver) announcement() vmReceiverAnnouncement {
	a := vmReceiverAnnouncement{Name: vmCleanLabel(r.hostName, 64), Port: r.Port(), Thumbprint: r.thumbprint, Folder: r.folder != "", Version: r.version, Pairing: r.pairing}
	for _, disk := range r.Disks() {
		a.Disks++
		a.DiskBytes += disk.Size
	}
	if r.freeBytes != nil {
		a.FreeBytes = r.freeBytes()
	}
	// This computer's addresses, default route first: a sender whose job
	// cannot reach the address it found tries these (with the same pin).
	for _, address := range vmLocalAddresses() {
		if len(a.Addresses) == replMaxAlternates {
			break
		}
		a.Addresses = append(a.Addresses, address)
	}
	return a
}

// vmPairClient talks to a receiver's pairing endpoint with its certificate
// pinned.
func vmPairClient(address, thumbprint string) (*http.Client, *url.URL, error) {
	pinned := vmNormalizeThumbprint(thumbprint)
	if pinned == "" {
		return nil, nil, errors.New("enter the receiver's certificate thumbprint (64 hexadecimal characters)")
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || host == "" || port == "" {
		return nil, nil, errors.New("enter the receiver as host:port")
	}
	transport := vmReceiverTransport(pinned)
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}, &url.URL{Scheme: "https", Host: net.JoinHostPort(host, port), Path: vmReceiverAPI + "pair"}, nil
}

func vmPairDo(client *http.Client, request *http.Request, out interface{}) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &problem) == nil && problem.Error != "" {
			return errors.New(problem.Error)
		}
		return fmt.Errorf("the receiver answered %s", response.Status)
	}
	return json.Unmarshal(raw, out)
}

// vmRequestPairing asks a receiver for its code; the operator allows it on
// the receiver's screen. It returns the request ID and the check number to
// compare with the one shown there.
func vmRequestPairing(ctx context.Context, address, thumbprint, computer string) (string, string, error) {
	client, endpoint, err := vmPairClient(address, thumbprint)
	if err != nil {
		return "", "", err
	}
	body, _ := json.Marshal(map[string]string{"computer": vmCleanLabel(computer, 64)})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")
	var answer struct {
		ID string `json:"id"`
	}
	if err := vmPairDo(client, request, &answer); err != nil {
		return "", "", err
	}
	if len(answer.ID) != 32 {
		return "", "", errors.New("the receiver gave no pairing request")
	}
	return answer.ID, vmPairCheck(answer.ID, thumbprint), nil
}

// vmPairingStatus reads a request's state: pending, allowed (with the
// code, handed over once), denied or expired.
func vmPairingStatus(ctx context.Context, address, thumbprint, id string) (string, string, error) {
	client, endpoint, err := vmPairClient(address, thumbprint)
	if err != nil {
		return "", "", err
	}
	endpoint.RawQuery = url.Values{"id": {id}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", "", err
	}
	var answer struct {
		State string `json:"state"`
		Code  string `json:"code"`
	}
	if err := vmPairDo(client, request, &answer); err != nil {
		return "", "", err
	}
	switch answer.State {
	case "pending", "denied", "expired":
		return answer.State, "", nil
	case "allowed":
		if vmNormalizePairingCode(answer.Code) == "" {
			return "", "", errors.New("the receiver allowed the request but sent no code")
		}
		return "allowed", answer.Code, nil
	}
	return "", "", errors.New("the receiver gave an unknown answer")
}
