//go:build !windows

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// replTestSSHServer runs commands with sh for the user "root" with the
// given password, and records every command line it ran.
func replTestSSHServer(t *testing.T, password string) (port int, fingerprint string, commands func() []string) {
	t.Helper()
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, given []byte) (*ssh.Permissions, error) {
		if meta.User() == "root" && string(given) == password {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var mu sync.Mutex
	ran := []string{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				_, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					conn.Close()
					return
				}
				go ssh.DiscardRequests(requests)
				for newChannel := range channels {
					channel, requests, err := newChannel.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							if request.Type != "exec" {
								request.Reply(false, nil)
								continue
							}
							length := binary.BigEndian.Uint32(request.Payload)
							command := string(request.Payload[4 : 4+length])
							request.Reply(true, nil)
							mu.Lock()
							ran = append(ran, command)
							mu.Unlock()
							input, _ := io.ReadAll(channel)
							cmd := exec.Command("sh", "-c", command)
							cmd.Stdin = bytes.NewReader(input)
							out, err := cmd.CombinedOutput()
							channel.Write(out)
							status := 0
							if err != nil {
								status = 1
								if exit, ok := err.(*exec.ExitError); ok {
									status = exit.ExitCode()
								}
							}
							payload := make([]byte, 4)
							binary.BigEndian.PutUint32(payload, uint32(status))
							channel.SendRequest("exit-status", false, payload)
							return
						}
					}()
				}
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, ssh.FingerprintSHA256(signer.PublicKey()), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), ran...)
	}
}

func TestSSHInstallTrustsOnlyTheHostKeyAndKeepsThePasswordOffCommandLines(t *testing.T) {
	password := "Fict1onal-Pass!"
	port, fingerprint, commands := replTestSSHServer(t, password)
	target := replSSHTarget{Host: "127.0.0.1", Port: port, User: "root", Password: password}
	// A stand-in agent: checks its arguments and prints what the real one does.
	thumb := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	agent := []byte("#!/bin/sh\n[ \"$1\" = --agent-install ] && [ \"$3\" = 44390 ] || exit 3\necho 'Agent key:              abcdefghijklmnopqrstuvwxyz234567'\necho 'Certificate thumbprint: " + thumb + "'\n")
	var logged []string
	log := func(s string) { logged = append(logged, s) }

	_, _, err := replSSHInstallAgent(context.Background(), target, agent, 44390, log)
	var untrusted *replUntrustedHostKey
	if !errors.As(err, &untrusted) || untrusted.Fingerprint != fingerprint || untrusted.Changed {
		t.Fatalf("an unknown host key must be refused with its fingerprint: %v", err)
	}
	target.HostKey = "SHA256:somethingelse"
	if _, _, err := replSSHInstallAgent(context.Background(), target, agent, 44390, log); !errors.As(err, &untrusted) || !untrusted.Changed {
		t.Fatalf("a different host key must be refused as changed: %v", err)
	}
	target.HostKey = fingerprint
	wrong := target
	wrong.Password = "nope"
	if _, _, err := replSSHInstallAgent(context.Background(), wrong, agent, 44390, log); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a wrong password: %v", err)
	}
	key, thumbprint, err := replSSHInstallAgent(context.Background(), target, agent, 44390, log)
	if err != nil {
		t.Fatal(err)
	}
	if key != "abcdefghijklmnopqrstuvwxyz234567" || thumbprint != thumb {
		t.Fatalf("key %q thumbprint %q", key, thumbprint)
	}
	for _, command := range commands() {
		if strings.Contains(command, password) {
			t.Fatal("the password reached a command line")
		}
	}
	for _, line := range logged {
		if strings.Contains(line, password) {
			t.Fatal("the password reached the log")
		}
	}
	if leftovers, _ := filepath.Glob("/tmp/simm-agent.*"); len(leftovers) > 0 {
		for _, dir := range leftovers {
			if info, err := os.Stat(dir); err == nil && info.ModTime().After(testStart) {
				t.Fatalf("the upload folder %s was left behind", dir)
			}
		}
	}
	// A failing install reports the agent's output, without the password.
	failing := []byte("#!/bin/sh\necho 'disk full " + password + "' >&2\nexit 1\n")
	if _, _, err := replSSHInstallAgent(context.Background(), target, failing, 44390, log); err == nil || strings.Contains(err.Error(), password) || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("a failing install: %v", err)
	}
	if _, err := replLoadLinuxAgent(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing agent file was accepted")
	}
	script := filepath.Join(t.TempDir(), "agent")
	os.WriteFile(script, agent, 0700)
	if _, err := replLoadLinuxAgent(script); err == nil {
		t.Fatal("a script was accepted as the Linux agent")
	}
	_ = strconv.Itoa
}

var testStart = func() (t0 time.Time) { return time.Now() }()
