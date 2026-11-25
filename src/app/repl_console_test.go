package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func replTestSeal(p []byte) ([]byte, error) {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = b ^ 0x5a
	}
	return out, nil
}

func TestConsoleAddsPollsAndManagesAgentsWithoutStoringKeys(t *testing.T) {
	agent, _, _, receiver, _ := replTestSetup(t)
	dir := t.TempDir()
	key := replNewAgentKey()
	config := replAgentConfig{KeyHash: replKeyHash(key)}
	certificate, thumbprint, err := replAgentCertificate(dir)
	if err != nil {
		t.Fatal(err)
	}
	server, err := startReplAgentServer("127.0.0.1:0", agent, config, certificate, thumbprint, "3.3.0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	address := "127.0.0.1:" + strconv.Itoa(server.Port())
	path := filepath.Join(t.TempDir(), "Replication", "agents.json")
	console, err := newReplConsole(path, replTestSeal, replTestSeal)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := console.Add(ctx, address, thumbprint, replNewAgentKey(), false); err == nil {
		t.Fatal("an agent was added with the wrong key")
	}
	record, err := console.Add(ctx, address, thumbprint, key, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(key)) {
		t.Fatal("the agent key is stored in plain text")
	}
	client, err := console.Client(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SaveJob(ctx, replTestSpec(receiver)); err != nil {
		t.Fatal(err)
	}
	console.Refresh(ctx)
	rows, summary := console.Rows()
	if len(rows) != 1 || !rows[0].Live.Reachable || rows[0].Live.Version != "3.3.0" || len(rows[0].Live.Jobs) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	if summary.Jobs != 1 || summary.Behind != 1 {
		t.Fatalf("a job that has not run yet is behind: %+v", summary)
	}
	again, err := newReplConsole(path, replTestSeal, replTestSeal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Client(record.ID); err != nil {
		t.Fatal(err)
	}
	server.Stop()
	console.Refresh(ctx)
	rows, summary = console.Rows()
	if rows[0].Live.Reachable || rows[0].Live.Error == "" || len(rows[0].Live.Jobs) != 1 || summary.Unreachable != 1 {
		t.Fatalf("unreachable row %+v", rows[0].Live)
	}
	if err := console.Forget(record.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := console.Rows(); len(rows) != 0 {
		t.Fatal("the agent was not forgotten")
	}
	for _, bad := range []string{"", "a b", `\\host\share`, "host:99999"} {
		if _, err := replAgentAddress(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if got, _ := replAgentAddress("srv01"); !strings.HasSuffix(got, ":44390") {
		t.Fatal("the default agent port was not added")
	}
}
