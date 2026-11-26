package main

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// replDeadAddress is a local address with nothing listening on it.
func replDeadAddress(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func TestReplicationJobSwitchesToTheReceiverAddressThatAnswers(t *testing.T) {
	agent, _, _, receiver, _ := replTestSetup(t)
	limit := replReceiverProbeLimit
	replReceiverProbeLimit = 3 * time.Second
	t.Cleanup(func() { replReceiverProbeLimit = limit })
	good := "127.0.0.1:" + strconv.Itoa(receiver.Port())
	dead := replDeadAddress(t)

	// Another receiver (another certificate) answers at one alternate: it
	// must never be chosen.
	other, err := startVMReceiver("127.0.0.1:0", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Stop()
	impostor := "127.0.0.1:" + strconv.Itoa(other.Port())

	spec := replTestSpec(receiver)
	spec.Receiver.Address = dead
	spec.Receiver.Alternates = []string{dead, impostor, " ", good, good}
	view, err := agent.SaveJob(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(view.Spec.Receiver.Alternates, ","); got != impostor+","+good {
		t.Fatalf("alternates were not cleaned: %s", got)
	}
	// Check receiver from the agent finds the address it reaches.
	link := view.Spec.Receiver
	link.Code = receiver.Code()
	check, err := agent.CheckReceiver(context.Background(), link)
	if err != nil || check.Address != good || !check.Folder || len(check.Tried) != 2 {
		t.Fatalf("check receiver: %v %+v", err, check)
	}

	id := view.Spec.ID
	agent.Tick()
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateIdle || view.Status.Rounds != 1 {
		t.Fatalf("the round did not use the address that answers: %+v\n%s", view.Status, replJobLog(agent, id))
	}
	if view.Spec.Receiver.Address != good || strings.Join(view.Spec.Receiver.Alternates, ",") != dead+","+impostor {
		t.Fatalf("the job did not keep the address that answered: %+v", view.Spec.Receiver)
	}
	if log := replJobLog(agent, id); !strings.Contains(log, "answered at "+good) {
		t.Fatalf("log:\n%s", log)
	}
	// Saved: an agent started again uses the new address.
	restarted, err := newReplAgent(agent.dir, agent.platform)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range restarted.Jobs() {
		if v.Spec.Receiver.Address != good {
			t.Fatalf("the new address was not saved: %+v", v.Spec.Receiver)
		}
	}
	// The console saving the job with its new address keeps the code.
	spec = view.Spec
	spec.Receiver.Code = ""
	if _, err := agent.SaveJob(spec); err != nil {
		t.Fatalf("saving with the switched address: %v", err)
	}
}

func TestReceiverCheckFailsWhenNoAddressAnswers(t *testing.T) {
	agent, _, _, receiver, _ := replTestSetup(t)
	limit := replReceiverProbeLimit
	replReceiverProbeLimit = 2 * time.Second
	t.Cleanup(func() { replReceiverProbeLimit = limit })
	link := replTestSpec(receiver).Receiver
	link.Address, link.Alternates = replDeadAddress(t), []string{replDeadAddress(t)}
	_, err := agent.CheckReceiver(context.Background(), link)
	if err == nil || !strings.Contains(err.Error(), "none of the receiver's addresses answered") {
		t.Fatalf("check with no address answering: %v", err)
	}
}

func TestFindAnswerKeepsOnlyPlainOtherAddresses(t *testing.T) {
	got := vmAnnouncedAddresses([]string{"10.0.0.5", "127.0.0.1", "fe80::1", "host.example.com", "10.0.0.5", "192.0.2.7", "[2001:db8::5]", "0.0.0.0", "224.0.0.1"}, 44380, "192.0.2.7:44380")
	if strings.Join(got, ",") != "10.0.0.5:44380,[2001:db8::5]:44380" {
		t.Fatalf("announced addresses: %v", got)
	}
}

func TestReplicationJobCutoverFinishedByHandStopsReplicating(t *testing.T) {
	agent, platform, _, receiver, _ := replTestSetup(t)
	view, err := agent.SaveJob(replTestSpec(receiver))
	if err != nil {
		t.Fatal(err)
	}
	id := view.Spec.ID
	if err := agent.Action(id, "finish-by-hand"); err == nil {
		t.Fatal("finished by hand before any round")
	}
	agent.Tick()
	replWaitIdle(t, agent, id)
	if err := agent.Action(id, "finish-by-hand"); err != nil {
		t.Fatal(err)
	}
	view = replWaitIdle(t, agent, id)
	if view.Status.State != replStateCutDone || !strings.Contains(view.Status.Message, "finished by hand") {
		t.Fatalf("status: %+v", view.Status)
	}
	if len(platform.stopped) != 0 || len(platform.started) != 0 {
		t.Fatal("finishing by hand changed services on the server")
	}
	if err := agent.Action(id, "sync"); err == nil {
		t.Fatal("a job finished by hand still syncs")
	}
	if err := agent.Action(id, "finish-by-hand"); err == nil {
		t.Fatal("finished twice")
	}
}

func TestTestFailoverOnAnExternalSwitchNeedsConfirmation(t *testing.T) {
	r := replFailoverRequest{Mode: "test", Switch: "vSwitch-VM"}
	if replTestNetworkRisk(r, "External") == nil {
		t.Fatal("a test VM on an external switch was allowed without confirmation")
	}
	for _, kind := range []string{"Internal", "Private"} {
		if err := replTestNetworkRisk(r, kind); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	r.JoinNetwork = true
	if replTestNetworkRisk(r, "External") != nil {
		t.Fatal("a confirmed external switch was refused")
	}
	if replTestNetworkRisk(replFailoverRequest{Mode: "production", Switch: "vSwitch-VM"}, "External") != nil {
		t.Fatal("production was refused")
	}
	if replTestNetworkRisk(replFailoverRequest{Mode: "test"}, "") != nil {
		t.Fatal("not connected was refused")
	}
}
