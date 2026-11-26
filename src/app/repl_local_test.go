package main

import "testing"

func TestReplicationJobsToThisComputerFollowItsReceiver(t *testing.T) {
	job := func(id, address, thumb, state string) replJobView {
		return replJobView{Spec: replJobSpec{ID: id, Name: id, Receiver: replReceiverLink{Address: address, Thumbprint: thumb}}, Status: replJobStatus{State: state}}
	}
	rows := []replAgentRow{
		{ID: "a1", Live: replAgentLive{Reachable: true, Jobs: []replJobView{
			job("old", "192.0.2.10:44380", "OLD", replStateIdle),
			job("same", "HV01.example.com:44380", "NEW", replStateIdle),
			job("byname", "hv01:44380", "OLD", replStateError),
			job("done", "192.0.2.10:44380", "OLD", replStateCutDone),
			job("elsewhere", "192.0.2.99:44380", "OLD", replStateIdle),
		}}},
		{ID: "a2", Live: replAgentLive{Reachable: false, Jobs: []replJobView{job("offline", "192.0.2.10:44380", "OLD", replStateIdle)}}},
	}
	local := map[string]bool{"192.0.2.10": true, "hv01": true}
	got := replJobsForLocal(rows, local, "NEW")
	if len(got) != 2 || got[0].Spec.ID != "old" || got[1].Spec.ID != "byname" || got[0].Agent != "a1" {
		t.Fatalf("repoint %+v", got)
	}
	if len(replJobsForLocal(rows, local, "")) != 0 {
		t.Fatal("repointed without a running receiver")
	}
	if !replJobsUseLocal(rows, local) || replJobsUseLocal(rows, map[string]bool{"elsewhere": true}) {
		t.Fatal("use local")
	}
}
