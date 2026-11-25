package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplicationRoundSurvivesAnInternalError(t *testing.T) {
	agent, platform, _, receiver, _ := replTestSetup(t)
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	platform.panics = true
	agent.Tick()
	view = replWaitIdle(t, agent, view.Spec.ID)
	if view.Status.State != replStateError || view.Status.Failures != 1 || !strings.Contains(view.Status.LastError, "internal error") {
		t.Fatalf("an internal error should fail the round, not the agent: %+v", view.Status)
	}
	raw, err := os.ReadFile(filepath.Join(agent.dir, "Logs", replErrorLog))
	if err != nil || !strings.Contains(string(raw), "goroutine") {
		t.Fatalf("the stack was not saved: %v %q", err, raw)
	}
	// The next round runs normally.
	platform.panics = false
	agent.mu.Lock()
	agent.jobs[view.Spec.ID].status.NextRun = time.Time{}
	agent.mu.Unlock()
	agent.Tick()
	if view = replWaitIdle(t, agent, view.Spec.ID); view.Status.Rounds != 1 {
		t.Fatalf("the round after the internal error: %+v", view.Status)
	}
}

func TestReplicationAgentReportsHowItsLastRunEnded(t *testing.T) {
	dir := t.TempDir()
	if reason, _ := replCheckPreviousStop(dir); reason != "" {
		t.Fatalf("a first start reported %q", reason)
	}
	if err := replMarkRunning(dir, "1.0"); err != nil {
		t.Fatal(err)
	}
	replMarkStopped(dir)
	if reason, _ := replCheckPreviousStop(dir); reason != "" {
		t.Fatalf("a clean stop reported %q", reason)
	}

	// Ended from outside during a round (security software, Task Manager).
	agent, _, _, receiver, _ := replTestSetup(t)
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	agent.jobs[view.Spec.ID].status.State = replStateSyncing
	agent.saveLocked()
	agent.mu.Unlock()
	if err := replMarkRunning(agent.dir, "1.0"); err != nil {
		t.Fatal(err)
	}
	reason, _ := replCheckPreviousStop(agent.dir)
	if !strings.Contains(reason, "from outside") && !strings.Contains(reason, "without stopping") || !strings.Contains(reason, "SentinelOne") {
		t.Fatalf("reason %q", reason)
	}
	restarted, err := newReplAgent(agent.dir, agent.platform)
	if err != nil {
		t.Fatal(err)
	}
	restarted.noteUncleanStop(reason)
	alerts := restarted.Jobs()
	if len(alerts) != 1 || alerts[0].Status.State != replStateIdle {
		t.Fatalf("the interrupted job should run again: %+v", alerts)
	}
	found := false
	for _, alert := range restarted.alerts {
		found = found || (alert.Kind == "stopped" && strings.Contains(alert.Text, "SentinelOne"))
	}
	if !found || restarted.lastStop == "" {
		t.Fatalf("no stopped alert: %+v", restarted.alerts)
	}

	// A crash: the runtime wrote the crash log after the run started.
	time.Sleep(20 * time.Millisecond)
	if err := os.MkdirAll(filepath.Join(dir, "Logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := replMarkRunning(dir, "1.0"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "Logs", replCrashLog), []byte("fatal error: out of memory\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if reason, _ := replCheckPreviousStop(dir); !strings.Contains(reason, "crashed") || !strings.Contains(reason, "out of memory") {
		t.Fatalf("crash reason %q", reason)
	}
}

func TestConsoleExplainsAnAgentThatIsNotListening(t *testing.T) {
	hint := replAgentErrorHint(errors.New(`Get "https://server:44390/agent/v1/hello": dial tcp 192.0.2.20:44390: connectex: No connection could be made because the target machine actively refused it.`))
	if !strings.Contains(hint, "not running") || !strings.Contains(hint, "SentinelOne") {
		t.Fatalf("hint %q", hint)
	}
	if replAgentErrorHint(errors.New("something else")) != "" {
		t.Fatal("an unknown error got a hint")
	}
}

// The agent runs on customers' servers next to endpoint protection, which
// treats shadow copy deletion and hidden PowerShell as ransomware. Its code
// must take shadow copies only through the VSS requester interface
// (vss_windows.go) and start no PowerShell.
func TestAgentCodeStartsNoPowerShellAndDeletesNoShadowCopies(t *testing.T) {
	agentFiles := []string{"vmagent.go", "vmagent_windows.go", "vss_windows.go", "repl_agent_windows.go", "repl_agent_server.go", "repl_job.go", "repl_stop.go", "repl_track.go", "repl_track_windows.go", "repl_mft.go", "vmvolume_layout.go", "vmcopy_engine.go", "vmcopy_source.go", "vmcopy_raw_windows.go"}
	banned := []string{"vmRunPowerShell(", "powershellPath(", "Win32_ShadowCopy", "vssadmin", "DeleteSnapshots", "-EncodedCommand", "wmic"}
	checked := 0
	for _, name := range agentFiles {
		raw, err := os.ReadFile(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		checked++
		code := string(raw)
		for _, word := range banned {
			if strings.Contains(code, word) {
				t.Errorf("%s uses %s", name, word)
			}
		}
	}
	if checked < 8 {
		t.Fatalf("only %d agent files were found", checked)
	}
}

// The owner had to press Change while a round ran, then Pause, then Save
// several times: Save refused while the cancelled round was still ending.
// One Save must now stop the round, apply the change and start again.
func TestReplicationJobChangeDuringARoundTakesOneSave(t *testing.T) {
	agent, platform, _, receiver, _ := replTestSetup(t)
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	platform.gate = make(chan struct{})
	agent.Tick()
	agent.mu.Lock()
	running := agent.jobs[id].running
	agent.mu.Unlock()
	if !running {
		t.Fatal("the round did not start")
	}
	changed := replTestSpec(receiver)
	changed.ID, changed.IntervalMinutes, changed.Compress, changed.Receiver.Code = id, 10, false, ""
	view, err = agent.SaveJob(changed)
	if err != nil {
		t.Fatalf("a change during a round was refused: %v", err)
	}
	if view.Spec.IntervalMinutes != 10 || view.Spec.Receiver.Code != "" || !view.HasCode || !strings.Contains(view.Status.Message, "Stopping the current round") {
		t.Fatalf("the saved change was not reported: %+v", view)
	}
	close(platform.gate)
	view = replWaitIdle(t, agent, id)
	if view.Spec.IntervalMinutes != 10 || view.Spec.Compress || !view.HasCode {
		t.Fatalf("the change was not applied when the round ended: %+v", view.Spec)
	}
	if view.Status.State != replStateIdle || !view.Status.NextRun.IsZero() || !strings.Contains(view.Status.Message, "Changed") {
		t.Fatalf("a new round should start with the new settings: %+v", view.Status)
	}
	platform.gate = nil
	agent.Tick()
	if view = replWaitIdle(t, agent, id); view.Status.Rounds != 1 {
		t.Fatalf("the round with the new settings: %+v", view.Status)
	}

	// A paused job changes at once and stays paused.
	if err := agent.Action(id, "pause"); err != nil {
		t.Fatal(err)
	}
	changed.IntervalMinutes = 15
	if view, err = agent.SaveJob(changed); err != nil || view.Spec.IntervalMinutes != 15 || view.Status.State != replStatePaused {
		t.Fatalf("a paused job should change and stay paused: %v %+v", err, view.Status)
	}
}
