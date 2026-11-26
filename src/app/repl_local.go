package main

import "strings"

// replLocalRepoint is one job to send to this computer's receiver again.
type replLocalRepoint struct {
	Agent string
	Spec  replJobSpec
}

// replJobsForLocal lists the jobs whose receiver is this computer (its
// address or name is in local) but carries another certificate: the
// receiver here started again with a new certificate and pairing code, so
// those jobs are pointed at it again. Jobs already cut over are left alone.
func replJobsForLocal(rows []replAgentRow, local map[string]bool, thumbprint string) []replLocalRepoint {
	out := []replLocalRepoint{}
	if thumbprint == "" {
		return out
	}
	for _, agent := range rows {
		if !agent.Live.Reachable {
			continue
		}
		for _, job := range agent.Live.Jobs {
			host := asHostOf(job.Spec.Receiver.Address)
			if host == "" || !(local[host] || local[asShortName(host)]) || job.Status.State == replStateCutDone || job.Status.State == replStateCutover {
				continue
			}
			if strings.EqualFold(job.Spec.Receiver.Thumbprint, thumbprint) {
				continue
			}
			out = append(out, replLocalRepoint{Agent: agent.ID, Spec: job.Spec})
		}
	}
	return out
}

// replJobsUseLocal says whether any job sends to this computer.
func replJobsUseLocal(rows []replAgentRow, local map[string]bool) bool {
	for _, agent := range rows {
		for _, job := range agent.Live.Jobs {
			if host := asHostOf(job.Spec.Receiver.Address); host != "" && (local[host] || local[asShortName(host)]) && job.Status.State != replStateCutDone {
				return true
			}
		}
	}
	return false
}
