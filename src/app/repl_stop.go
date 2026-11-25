package main

// How the agent's previous run ended. The agent writes running.json when it
// starts and removes it when it stops cleanly (a service stop, Windows or
// systemd shutting down, an uninstall). Finding it at the next start means
// the process ended without stopping: it crashed (the crash log was written
// after it started), or something outside ended it. Security software that
// kills and quarantines the agent is the usual case, then Task Manager, a
// kill command or a power loss.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const replRunningMarker = "running.json"

type replRunning struct {
	Started time.Time `json:"started"`
	PID     int       `json:"pid"`
	Version string    `json:"version"`
}

// replCheckPreviousStop reads the marker left by the previous run, if any,
// and returns why that run ended ("" for a clean stop) and when it started.
func replCheckPreviousStop(dir string) (string, time.Time) {
	raw, err := os.ReadFile(filepath.Join(dir, replRunningMarker))
	if err != nil {
		return "", time.Time{}
	}
	var previous replRunning
	if json.Unmarshal(raw, &previous) != nil {
		previous = replRunning{}
	}
	crashPath := filepath.Join(dir, "Logs", replCrashLog)
	if info, err := os.Stat(crashPath); err == nil && info.ModTime().After(previous.Started) && info.Size() > 0 {
		lines := replTailFile(crashPath, 6)
		detail := strings.TrimSpace(strings.Join(lines, " "))
		if len(detail) > 600 {
			detail = detail[:600] + "..."
		}
		return fmt.Sprintf("The agent crashed. Details are in %s: %s", crashPath, detail), previous.Started
	}
	reason := "The agent was ended from outside without stopping (not by a service stop or a shutdown). " + replEDRHint
	if runtime.GOOS == "linux" {
		reason = "The agent ended without stopping (a crash, or ended from outside). Its output is in journalctl -u simm-replication-agent. " + replEDRHint
	}
	return reason, previous.Started
}

// replEDRHint is the advice shown wherever the agent may have been ended by
// security software.
const replEDRHint = "Security software is the usual cause: look in SentinelOne, Microsoft Defender or CrowdStrike for a detection or quarantine of the agent, release it, and add the exclusions in the application's endpoint protection guide (docs/EDR-EXCLUSIONS.md)."

func replMarkRunning(dir, version string) error {
	raw, _ := json.Marshal(replRunning{Started: time.Now().UTC(), PID: os.Getpid(), Version: version})
	return replWriteFileAtomic(filepath.Join(dir, replRunningMarker), raw)
}

func replMarkStopped(dir string) {
	os.Remove(filepath.Join(dir, replRunningMarker))
}

// noteUncleanStop records an unclean previous stop: on the agent (shown to
// the console), in every job that was in a round, with an alert.
func (a *replAgent) noteUncleanStop(reason string) {
	if reason == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastStop, a.lastStopAt = reason, a.now().UTC()
	for _, job := range a.jobs {
		if !job.interrupted {
			continue
		}
		job.status.Message = "The agent stopped during a round; it runs again now."
		a.alertLocked(job, "warning", "stopped", "The round of "+job.spec.Name+" was cut short. "+reason)
	}
	a.saveLocked()
}
