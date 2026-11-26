package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// replHangSource blocks its reads past an offset, ignoring any context, as a
// hung disk or volume shadow copy read does.
type replHangSource struct {
	vmCopySource
	from  int64
	until <-chan struct{} // reads block until it closes
	once  *int32          // when set, only the first blocking read blocks, for pause
	pause time.Duration
}

func (s *replHangSource) ReadAt(p []byte, offset int64) (int, error) {
	if offset >= s.from {
		if s.once != nil {
			if atomic.CompareAndSwapInt32(s.once, 0, 1) {
				time.Sleep(s.pause)
			}
		} else if s.until != nil {
			<-s.until
		}
	}
	return s.vmCopySource.ReadAt(p, offset)
}

func replFastWatchdog(t *testing.T) {
	after, grace, check, retry := replStallAfter, replStallGrace, replStallCheck, replCutoverRetry
	replStallAfter, replStallGrace, replStallCheck, replCutoverRetry = 3*time.Second, 500*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { replStallAfter, replStallGrace, replStallCheck, replCutoverRetry = after, grace, check, retry })
}

func replJobLog(agent *replAgent, id string) string {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return strings.Join(agent.jobs[id].logs, "\n")
}

func TestReplicationWatchdogStopsAStalledRoundAndTheNextResumes(t *testing.T) {
	replFastWatchdog(t)
	agent, platform, _, receiver, folder := replTestSetup(t)
	agent.now = time.Now // the watchdog measures real time
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	platform.wrap = func(source vmCopySource) vmCopySource {
		return &replHangSource{vmCopySource: source, from: 8 << 20, until: release}
	}
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateError || !strings.Contains(view.Status.LastError, "no progress") || !strings.Contains(view.Status.Message, "so it was stopped") {
		t.Fatalf("a stalled round was not stopped: %+v", view.Status)
	}
	if view.Status.LogFolder == "" || view.Status.Phase != "" {
		t.Fatalf("status after a stall: %+v", view.Status)
	}
	if log := replJobLog(agent, id); !strings.Contains(log, "No progress for") || !strings.Contains(log, "abandoned") {
		t.Fatalf("log:\n%s", log)
	}

	// The hung read returns later; the next round takes over the copy at
	// once (the same job owns the receiver's session) and finishes it.
	close(release)
	released = true
	platform.mu.Lock()
	platform.wrap = nil
	platform.mu.Unlock()
	if err := agent.Action(id, "sync"); err != nil {
		t.Fatal(err)
	}
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateIdle || view.Status.Rounds != 1 || view.Status.LastError != "" {
		t.Fatalf("the round after a stall: %+v", view.Status)
	}
	for _, disk := range []string{"0", "1"} {
		if !bytes.Equal(vmReadVHDX(t, filepath.Join(folder, "APP01-disk"+disk+".vhdx")), platform.disks[disk]) {
			t.Fatalf("disk %s differs from the source after the stalled round", disk)
		}
	}
}

func TestReplicationCutoverTriesAStalledFinalRoundAgain(t *testing.T) {
	replFastWatchdog(t)
	agent, platform, _, receiver, folder := replTestSetup(t)
	agent.now = time.Now
	spec := replTestSpec(receiver)
	view, err := agent.SaveJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	agent.Tick()
	replWaitIdle(t, agent, id)

	var once int32
	platform.mu.Lock()
	platform.wrap = func(source vmCopySource) vmCopySource {
		return &replHangSource{vmCopySource: source, from: 8 << 20, once: &once, pause: 8 * time.Second}
	}
	platform.mu.Unlock()
	if err := agent.Action(id, "cutover"); err != nil {
		t.Fatal(err)
	}
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateCutDone || !view.Status.Disks[0].Verified || !view.Status.Disks[1].Verified {
		t.Fatalf("the cutover did not finish after a stalled attempt: %+v\n%s", view.Status, replJobLog(agent, id))
	}
	if len(platform.started) != 0 {
		t.Fatal("services were started again although the cutover succeeded")
	}
	if log := replJobLog(agent, id); !strings.Contains(log, "attempt 2 of 3") {
		t.Fatalf("log:\n%s", log)
	}
	if !bytes.Equal(vmReadVHDX(t, filepath.Join(folder, "APP01-disk0.vhdx")), platform.disks["0"]) {
		t.Fatal("the cutover copy differs from the source")
	}
}

func TestReplicationRunningCutoverCanBeCancelledAndRollsBack(t *testing.T) {
	replFastWatchdog(t)
	agent, platform, _, receiver, _ := replTestSetup(t)
	agent.now = time.Now
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	agent.Tick()
	replWaitIdle(t, agent, id)

	gate := make(chan struct{})
	defer close(gate)
	platform.gate = gate // the snapshots never come back
	replStallAfter = time.Hour
	if err := agent.Action(id, "cutover"); err != nil {
		t.Fatal(err)
	}
	agent.Tick()
	if err := agent.Action(id, "delete"); err == nil || !strings.Contains(err.Error(), "cancel the cutover first") {
		t.Fatalf("delete during a cutover: %v", err)
	}
	if err := agent.Action(id, "cancel-cutover"); err != nil {
		t.Fatal(err)
	}
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateIdle || !strings.Contains(view.Status.Message, "cancelled and rolled back") {
		t.Fatalf("cancelled cutover: %+v", view.Status)
	}
	if strings.Join(platform.started, ",") != "MSSQLSERVER,W3SVC" {
		t.Fatalf("services started again: %v", platform.started)
	}
	for _, alert := range agent.Alerts() {
		if alert.Kind == "cutover" && !alert.Cleared {
			t.Fatalf("a cancelled cutover raised an alert: %+v", alert)
		}
	}
}

func TestReplicationAgentRestartedDuringCutoverStartsTheServicesAgain(t *testing.T) {
	agent, platform, _, receiver, _ := replTestSetup(t)
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	agent.Tick()
	replWaitIdle(t, agent, id)
	// Record the job as the agent leaves it when it ends mid-cutover.
	path := filepath.Join(agent.dir, "jobs.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved replSaved
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Jobs[0].Status.State = replStateCutover
	raw, _ = json.Marshal(saved)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := newReplAgent(agent.dir, platform)
	if err != nil {
		t.Fatal(err)
	}
	restarted.restartInterruptedServices()
	if strings.Join(platform.started, ",") != "MSSQLSERVER,W3SVC" {
		t.Fatalf("services started after the restart: %v", platform.started)
	}
	for _, view := range restarted.Jobs() {
		if view.Status.State != replStateIdle || !strings.Contains(view.Status.Message, "rolled back") {
			t.Fatalf("job after a restart during cutover: %+v", view.Status)
		}
	}
	// Only once.
	restarted.restartInterruptedServices()
	if len(platform.started) != 2 {
		t.Fatalf("services started twice: %v", platform.started)
	}
}

func TestReceiverAnswersARepeatedFinishWithoutVerifyingAgainAndLetsItsOwnerTakeOver(t *testing.T) {
	folder := t.TempDir()
	var mu sync.Mutex
	var lines []string
	receiver, err := startVMReceiver("127.0.0.1:0", folder, func(text string) {
		mu.Lock()
		lines = append(lines, text)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Stop()
	address := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	data := vmTestDiskBytes(36 << 20)
	source := &vmBytesSource{data: data, name: "owned disk"}
	sink, err := newVMRemoteSink(context.Background(), address, receiver.Thumbprint(), receiver.Code(), "OWNED.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	sink.Owner = "job-1"
	state, _, err := sink.Open(source.Describe(), source.Size(), vmCopyChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	for index := range state.Hashes {
		offset := int64(index) * state.ChunkSize
		chunk := data[offset : offset+vmChunkLength(state, index)]
		sum := sha256.Sum256(chunk)
		state.Hashes[index] = hex.EncodeToString(sum[:])
		if vmIsZero(chunk) {
			state.Hashes[index] = vmCopyZeroHash
		}
		if _, err := sink.Write(index, chunk, state.Hashes[index]); err != nil {
			t.Fatal(err)
		}
	}
	state.Complete = true
	first, err := sink.Finish(context.Background(), state, true)
	if err != nil || !first.Verified {
		t.Fatalf("finish: %v %+v", err, first)
	}
	again, err := sink.Finish(context.Background(), state, true)
	if err != nil || !again.Verified || again.Target != "OWNED.vhdx" {
		t.Fatalf("repeated finish: %v %+v", err, again)
	}
	mu.Lock()
	verifies := strings.Count(strings.ToLower(strings.Join(lines, "\n")), "verifying owned.vhdx")
	mu.Unlock()
	if verifies != 1 {
		t.Fatalf("the copy was verified %d times", verifies)
	}

	// The sender went away without closing. Another sender waits; the
	// same owner takes the disk over at once.
	other, err := newVMRemoteSink(context.Background(), address, receiver.Thumbprint(), receiver.Code(), "OWNED.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	other.Owner = "job-2"
	if _, _, err := other.Open(source.Describe(), source.Size(), vmCopyChunkSize); err == nil || !strings.Contains(err.Error(), "already being received") {
		t.Fatalf("another sender took the disk: %v", err)
	}
	if waiting, _ := replReceiverWait(errors.New("the receiver refused: OWNED.vhdx is already being received"), address); !waiting {
		t.Fatal("a busy disk was not taken as a wait")
	}
	same, err := newVMRemoteSink(context.Background(), address, receiver.Thumbprint(), receiver.Code(), "OWNED.vhdx")
	if err != nil {
		t.Fatal(err)
	}
	same.Owner = "job-1"
	resumed, _, err := same.Open(source.Describe(), source.Size(), vmCopyChunkSize)
	if err != nil {
		t.Fatalf("the owner could not take its disk back: %v", err)
	}
	if !resumed.Complete || resumed.Hashes[0] != state.Hashes[0] {
		t.Fatal("the copy record was not kept when the owner took over")
	}
	same.Close()
}
