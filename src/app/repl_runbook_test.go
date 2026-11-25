package main

import (
	"archive/zip"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReplicationRunbookListsEveryJobWithStepsInHTMLAndWord(t *testing.T) {
	recovery := time.Date(2026, 11, 25, 14, 19, 0, 0, time.UTC)
	rows := []replAgentRow{
		{Name: "app01.example.com", OS: "Windows", Live: replAgentLive{Computer: "APP01", Platform: "Windows", Jobs: []replJobView{{
			Spec:   replJobSpec{ID: "a1", Name: "APP01 <main>", Disks: []replDiskMap{{Source: "0", Target: "APP01-disk0.vhdx"}}, Receiver: replReceiverLink{Address: "192.0.2.50:44380"}, IntervalMinutes: 5, RPOMinutes: 15, StopServices: []string{"MSSQLSERVER"}},
			Status: replJobStatus{State: replStateIdle, RecoveryPoint: recovery},
		}}}},
		{Name: "web01.example.com", OS: "Linux", Live: replAgentLive{Computer: "web01", Platform: "Linux", Jobs: []replJobView{{
			Spec:   replJobSpec{ID: "b2", Name: "WEB01", Disks: []replDiskMap{{Source: "sda", Target: "disk-0"}}, Receiver: replReceiverLink{Address: "192.0.2.60:44380"}, RPOMinutes: 15, ShutdownAfterCutover: true},
			Status: replJobStatus{State: replStateHeld},
		}}}},
		{Name: "idle.example.com", Live: replAgentLive{}},
	}
	folder := t.TempDir()
	htmlPath, docxPath, err := writeReplRunbook(folder, rows, recovery, "3.3.0")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	for _, want := range []string{"APP01 &lt;main&gt;", "Test failover", "End test", "Production", "MSSQLSERVER", "2026-11-25 14:19 UTC", "paused for a test failover", "boot from its disks", "Rescan the receiver", "script-src '" + strings.ReplaceAll(vmRunbookScriptHash(), "+", "&#43;") + "'", "<script>" + vmRunbookScript + "</script>", `data-step="a1|cutover|`, "simm-replication-agent"} {
		if !strings.Contains(page, want) {
			t.Errorf("the HTML runbook lacks %q", want)
		}
	}
	if strings.Contains(page, "idle.example.com") {
		t.Error("a server without jobs is in the runbook")
	}
	if strings.Contains(page, "Shut web01 down") {
		t.Error("a job that shuts down by itself was told to shut down by hand")
	}
	archive, err := zip.OpenReader(docxPath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var document string
	for _, file := range archive.File {
		if file.Name == "word/document.xml" {
			reader, _ := file.Open()
			body, _ := io.ReadAll(reader)
			reader.Close()
			document = string(body)
		}
	}
	for _, want := range []string{"APP01 &lt;main&gt;", "WEB01", "w14:checkbox", "Roll back or fail back"} {
		if !strings.Contains(document, want) {
			t.Errorf("the Word runbook lacks %q", want)
		}
	}
	if _, _, err := writeReplRunbook(t.TempDir(), rows[2:], recovery, "3.3.0"); err == nil {
		t.Fatal("a runbook without jobs was written")
	}
}
