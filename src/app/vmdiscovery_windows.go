//go:build windows

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// handleVMwareReceiverPair allows or denies a pairing request waiting on
// this computer's receiver. Only the operator at this screen answers.
func (a *application) handleVMwareReceiverPair(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	a.mu.Lock()
	r := a.vmReceive.receiver
	a.mu.Unlock()
	if r == nil {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "The receiver is not running."})
		return
	}
	if err := r.AnswerPair(input.ID, input.Allow); err != nil {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "That request has expired or was already answered. Ask the sender to request pairing again."})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

// vmFindHost checks a typed computer for a find request across subnets:
// one name or address, never a range.
func vmFindHost(host string) (string, bool) {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/*, \t") {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(vmDiscoveryPort)), true
}

// handleReceiversFind sends one find request to each adapter's broadcast
// address (or to one typed computer) and lists the receivers that answer.
func (a *application) handleReceiversFind(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Host string `json:"host"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	targets := vmBroadcastTargets(vmDiscoveryPort)
	if strings.TrimSpace(input.Host) != "" {
		target, ok := vmFindHost(input.Host)
		if !ok {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "Enter one computer name or IP address (not a range)."})
			return
		}
		targets = []string{target}
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	found, err := vmFindReceivers(ctx, targets, 1500*time.Millisecond)
	if err != nil && len(found) == 0 {
		writeJSON(response, http.StatusBadGateway, map[string]any{"ok": false, "error": "The search could not be sent: " + err.Error()})
		return
	}
	if found == nil {
		found = []vmReceiverAnnouncement{}
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "receivers": found})
}

// handleReceiversPair asks a receiver for its pairing code. The receiver's
// certificate thumbprint is pinned, and the code is handed over only after
// the operator allows the request on the receiver's screen.
func (a *application) handleReceiversPair(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Address    string `json:"address"`
		Thumbprint string `json:"thumbprint"`
		ID         string `json:"id"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	if input.ID == "" {
		computer, _ := os.Hostname()
		id, check, err := vmRequestPairing(ctx, input.Address, input.Thumbprint, computer)
		if err != nil {
			writeJSON(response, http.StatusBadGateway, map[string]any{"ok": false, "error": "Pairing could not be requested: " + err.Error()})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "id": id, "check": check, "state": "pending"})
		return
	}
	state, code, err := vmPairingStatus(ctx, input.Address, input.Thumbprint, input.ID)
	if err != nil {
		writeJSON(response, http.StatusBadGateway, map[string]any{"ok": false, "error": "The receiver did not answer: " + err.Error()})
		return
	}
	answer := map[string]any{"ok": true, "state": state}
	if code != "" {
		answer["code"] = code
	}
	writeJSON(response, http.StatusOK, answer)
}
