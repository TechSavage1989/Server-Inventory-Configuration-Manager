package main

// Test failover when the copies are received by receiver media inside a VM
// on HyperCore, Nutanix AHV or Proxmox VE (phase 3 targets). The console
// pauses the job at a whole recovery point, has the platform clone the
// receiver VM (the clone gets its own disks), resumes replication at once,
// then isolates the clone, boots it from disk and starts it. Ending the
// test deletes only that clone. See vmtarget_testfailover.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// replTargetTest is one running test VM. It holds no password or token.
type replTargetTest struct {
	ID            string    `json:"id"`
	Platform      string    `json:"platform"`
	Server        string    `json:"server"`
	Agent         string    `json:"agent"`
	Job           string    `json:"job"`
	JobName       string    `json:"jobName"`
	SourceVM      string    `json:"sourceVM"`
	SourceID      string    `json:"sourceID"`
	Name          string    `json:"name"`
	VMID          string    `json:"vmID"`
	RecoveryPoint time.Time `json:"recoveryPoint"`
	Started       time.Time `json:"started"`
}

type replTargetTestStore struct{ path string }

func (s replTargetTestStore) Load() ([]replTargetTest, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []replTargetTest{}, nil
	}
	if err != nil {
		return nil, err
	}
	list := []replTargetTest{}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return list, nil
}

func (s replTargetTestStore) Save(list []replTargetTest) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return replWriteFileAtomic(s.path, raw)
}

// replTargetTestPlatforms are the platforms whose VMs run receiver media.
var replTargetTestPlatforms = map[string]bool{vmTargetScale: true, vmTargetNutanix: true, vmTargetProxmox: true}

// replTargetTestJob finds the job and checks that it sends to raw disks
// (receiver media), not VHDX files on a Hyper-V host.
func replTargetTestJob(jobs replAgentJobs, id string) (*replJobView, error) {
	for i := range jobs.Jobs {
		job := &jobs.Jobs[i]
		if job.Spec.ID != id {
			continue
		}
		if job.Status.State == replStateCutDone {
			return nil, errors.New("the job has been cut over")
		}
		for _, disk := range job.Spec.Disks {
			if !strings.HasPrefix(strings.ToLower(disk.Target), "disk-") {
				return nil, fmt.Errorf("the job sends %s, a VHDX file, so test it from the Disk copy view on the Hyper-V host instead", disk.Target)
			}
		}
		return job, nil
	}
	return nil, errors.New("that job no longer exists")
}

type replTargetTestOptions struct {
	Agent    string
	Job      string
	SourceVM string
	Name     string
	Poll     time.Duration
	Log      func(string)
}

// replRunTargetTest pauses the job at a whole recovery point, clones the
// receiver VM, resumes the job (unless the operator had paused it) and
// starts the isolated clone.
func replRunTargetTest(ctx context.Context, client replAgentAPI, platform vmTargetPlatform, connection vmTargetConnection, o replTargetTestOptions) (*replTargetTest, error) {
	log := o.Log
	if log == nil {
		log = func(string) {}
	}
	if o.Poll <= 0 {
		o.Poll = 5 * time.Second
	}
	if !replTargetTestPlatforms[connection.Platform] {
		return nil, errors.New("test failover here is for HyperCore, Nutanix AHV and Proxmox VE")
	}
	jobs, err := client.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	job, err := replTargetTestJob(jobs, o.Job)
	if err != nil {
		return nil, err
	}
	source, err := platform.FindVM(ctx, o.SourceVM)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, fmt.Errorf("%s has no VM named %s; name the VM that runs the receiver", connection.Server, o.SourceVM)
	}
	if existing, err := platform.FindVM(ctx, o.Name); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, fmt.Errorf("a VM named %s already exists; choose another name. Nothing was changed", o.Name)
	}
	wasPaused := job.Status.State == replStatePaused && job.Status.Consistent
	resumed := wasPaused
	resume := func() {
		if resumed {
			return
		}
		resumed = true
		background, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := client.Action(background, o.Job, "resume"); err != nil {
			log("Replication could not be resumed; resume " + job.Spec.Name + " yourself: " + err.Error())
			return
		}
		log("Replication of " + job.Spec.Name + " resumed.")
	}
	defer resume()
	if !wasPaused {
		log("Pausing " + job.Spec.Name + " when its current round finishes, so the copy is one whole recovery point.")
		if err := client.Action(ctx, o.Job, "pause-consistent"); err != nil {
			return nil, err
		}
	}
	var point time.Time
	for {
		jobs, err := client.Jobs(ctx)
		if err == nil {
			current, err := replTargetTestJob(jobs, o.Job)
			if err != nil {
				return nil, err
			}
			if current.Status.State == replStatePaused && current.Status.Consistent {
				point = current.Status.RecoveryPoint
				break
			}
			if current.Status.State == replStatePaused && !current.Status.PauseAfterRound {
				return nil, errors.New("the job was paused in the middle of a round; resume it and try again")
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(o.Poll):
		}
	}
	log(fmt.Sprintf("Paused at the recovery point of %s UTC.", point.UTC().Format("2006-01-02 15:04:05")))
	clone, err := vmTargetTestStart(ctx, platform, source.ID, o.Name, resume, log)
	if err != nil {
		return nil, err
	}
	return &replTargetTest{
		Platform: connection.Platform, Server: connection.Server, Agent: o.Agent, Job: o.Job, JobName: job.Spec.Name,
		SourceVM: source.Name, SourceID: source.ID, Name: clone.Name, VMID: clone.ID, RecoveryPoint: point, Started: time.Now().UTC(),
	}, nil
}
