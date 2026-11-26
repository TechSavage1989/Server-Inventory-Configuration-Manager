package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// replReceiverProbeLimit is how long one address may take to answer when a
// round or Check receiver chooses between a receiver's addresses.
var replReceiverProbeLimit = 20 * time.Second

// replLinkHasAddress reports whether address is the link's address or one of
// its alternates.
func replLinkHasAddress(link replReceiverLink, address string) bool {
	if strings.EqualFold(link.Address, address) {
		return true
	}
	for _, other := range link.Alternates {
		if strings.EqualFold(other, address) {
			return true
		}
	}
	return false
}

// replReachTry is one address tried and why it did not answer.
type replReachTry struct {
	Address string `json:"address"`
	Error   string `json:"error,omitempty"`
}

// replFindReceiver asks the link's address and then each alternate, in
// order, with the same pinned thumbprint and code, and returns the first
// that answers. A wrong certificate or code at an address counts as not
// answering, so a different receiver is never chosen.
func replFindReceiver(ctx context.Context, probe func(context.Context, string, string, string) (bool, []vmReceiverDisk, error), link replReceiverLink) (string, bool, []vmReceiverDisk, []replReachTry, error) {
	tries := []replReachTry{}
	var first error
	for _, address := range append([]string{link.Address}, link.Alternates...) {
		if ctx.Err() != nil {
			return "", false, nil, tries, ctx.Err()
		}
		probeCtx, cancel := context.WithTimeout(ctx, replReceiverProbeLimit)
		folder, disks, err := probe(probeCtx, address, link.Thumbprint, link.Code)
		cancel()
		if err == nil {
			return address, folder, disks, tries, nil
		}
		if first == nil {
			first = err
		}
		tries = append(tries, replReachTry{Address: address, Error: err.Error()})
	}
	return "", false, nil, tries, first
}

// reachReceiver is called at the start of each round of a job that knows
// other addresses of its receiver. When the job's address does not answer
// and another does, the job switches to it (saved, so later rounds and the
// console use it) and the old address becomes an alternate. When none
// answers the round goes on with the job's address and fails or waits as
// usual.
func (a *replAgent) reachReceiver(ctx context.Context, job *replJob, link replReceiverLink, log func(string)) replReceiverLink {
	if len(link.Alternates) == 0 || a.probeReceiver == nil {
		return link
	}
	address, _, _, tries, err := replFindReceiver(ctx, a.probeReceiver, link)
	if err != nil || strings.EqualFold(address, link.Address) {
		return link
	}
	others := []string{link.Address}
	for _, other := range link.Alternates {
		if !strings.EqualFold(other, address) {
			others = append(others, other)
		}
	}
	reason := ""
	if len(tries) > 0 {
		reason = " (" + tries[0].Error + ")"
	}
	a.mu.Lock()
	if strings.EqualFold(job.spec.Receiver.Address, link.Address) && job.spec.Receiver.Thumbprint == link.Thumbprint {
		job.spec.Receiver.Address, job.spec.Receiver.Alternates = address, others
		a.saveLocked()
	}
	a.mu.Unlock()
	log(fmt.Sprintf("The receiver did not answer at %s%s but answered at %s, so this job now sends there.", link.Address, reason, address))
	link.Address, link.Alternates = address, others
	return link
}

// replReceiverCheck is the agent's answer to Check receiver.
type replReceiverCheck struct {
	Address string           `json:"address"`
	Folder  bool             `json:"folder"`
	Disks   []vmReceiverDisk `json:"disks"`
	Tried   []replReachTry   `json:"tried,omitempty"`
}

var errReplAgentNoReceiverCheck = errors.New("this agent is older than the receiver check; update it")

// CheckReceiver tries the receiver's addresses from this server and says
// which answered, so the console fills the address the agent really uses.
func (a *replAgent) CheckReceiver(ctx context.Context, link replReceiverLink) (replReceiverCheck, error) {
	address, err := vmReceiverAddress(link.Address)
	if err != nil {
		return replReceiverCheck{}, err
	}
	link.Address = address
	if link.Alternates, err = replCleanAlternates(address, link.Alternates); err != nil {
		return replReceiverCheck{}, err
	}
	link.Thumbprint = vmNormalizeThumbprint(link.Thumbprint)
	if link.Thumbprint == "" {
		return replReceiverCheck{}, errors.New("enter the receiver's certificate thumbprint (64 characters)")
	}
	probe := a.probeReceiver
	if probe == nil {
		probe = vmReceiverDescribe
	}
	found, folder, disks, tried, err := replFindReceiver(ctx, probe, link)
	if err != nil {
		if len(tried) > 1 {
			parts := []string{}
			for _, t := range tried {
				parts = append(parts, t.Address+": "+t.Error)
			}
			return replReceiverCheck{Tried: tried}, fmt.Errorf("none of the receiver's addresses answered from this server (%s)", strings.Join(parts, "; "))
		}
		return replReceiverCheck{Tried: tried}, err
	}
	if disks == nil {
		disks = []vmReceiverDisk{}
	}
	return replReceiverCheck{Address: found, Folder: folder, Disks: disks, Tried: tried}, nil
}
