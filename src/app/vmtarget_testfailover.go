package main

// Test failover on HyperCore, Nutanix AHV and Proxmox VE. The new VM runs
// the receiver and its disks hold the replica, so a test never boots it:
// the platform clones it (its own copy of every disk), the clone loses its
// network (adapters disconnected or removed), is set to boot from disk and
// started. Ending the test turns the clone off and deletes it, after
// checking it is still the same VM under the same name. The receiver VM and
// its disks are never changed, so replication simply carries on.

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// vmTargetTester is what a platform needs for test failover.
type vmTargetTester interface {
	// CloneVM makes a new VM named name with its own copy of every disk of
	// id. It never changes id.
	CloneVM(ctx context.Context, id, name, note string) (*vmTargetVM, error)
	// Isolate disconnects or removes every network adapter of id.
	Isolate(ctx context.Context, id string) error
	// DeleteVM deletes id and its disks. Only test clones are deleted.
	DeleteVM(ctx context.Context, id string) error
}

const vmTestFailoverNote = "Test failover clone made by Server Inventory and Migration Manager. Ending the test deletes it."

// vmTargetTestStart clones the receiver VM sourceID as testName, isolates
// it, boots it from disk and starts it. afterClone (optional) runs as soon
// as the clone exists: the clone has its own disks, so replication into the
// receiver VM can carry on from then.
func vmTargetTestStart(ctx context.Context, platform vmTargetPlatform, sourceID, testName string, afterClone func(), log func(string)) (*vmTargetVM, error) {
	if log == nil {
		log = func(string) {}
	}
	tester, ok := platform.(vmTargetTester)
	if !ok {
		return nil, errors.New("this platform has no test failover here")
	}
	if !vmTargetNamePattern.MatchString(testName) {
		return nil, errors.New("the test VM name must be 1 to 63 letters, digits, dots, dashes or underscores")
	}
	existing, err := platform.FindVM(ctx, testName)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("a VM named %s already exists (%s); choose another name. Nothing was changed", testName, existing.ID)
	}
	log("Cloning the replica VM as " + testName + ".")
	clone, err := tester.CloneVM(ctx, sourceID, testName, vmTestFailoverNote)
	if err != nil {
		return nil, err
	}
	if afterClone != nil {
		afterClone()
	}
	undo := func(cause error) (*vmTargetVM, error) {
		log("Removing the unfinished test VM: " + cause.Error())
		// The step may have failed because it was stopped, so the removal
		// gets its own time.
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if state, err := platform.State(cleanup, clone.ID); err == nil && state != "stopped" {
			platform.PowerOff(cleanup, clone.ID)
		}
		if err := tester.DeleteVM(cleanup, clone.ID); err != nil {
			return clone, fmt.Errorf("%w; the test VM %s could not be removed (%v), so delete it by hand", cause, testName, err)
		}
		return nil, cause
	}
	log("Disconnecting its network.")
	if err := tester.Isolate(ctx, clone.ID); err != nil {
		return undo(fmt.Errorf("its network could not be disconnected: %w", err))
	}
	log("Setting it to start from its disk.")
	if err := platform.BootFromDisk(ctx, clone.ID); err != nil {
		return undo(err)
	}
	log("Starting " + testName + ".")
	if err := platform.Start(ctx, clone.ID); err != nil {
		return undo(err)
	}
	clone.State = "running"
	return clone, nil
}

// vmTargetTestEnd turns the test clone off and deletes it, only when the VM
// named testName is still the clone testID.
func vmTargetTestEnd(ctx context.Context, platform vmTargetPlatform, testID, testName string, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	tester, ok := platform.(vmTargetTester)
	if !ok {
		return errors.New("this platform has no test failover here")
	}
	found, err := platform.FindVM(ctx, testName)
	if err != nil {
		return err
	}
	if found == nil {
		log(testName + " no longer exists.")
		return nil
	}
	if !strings.EqualFold(found.ID, testID) {
		return fmt.Errorf("the VM named %s is not the test clone this application made (%s, not %s); nothing was deleted", testName, found.ID, testID)
	}
	state, err := platform.State(ctx, testID)
	if err != nil {
		return err
	}
	if state != "stopped" {
		log("Turning off " + testName + ".")
		if err := platform.PowerOff(ctx, testID); err != nil {
			return err
		}
		deadline := time.Now().Add(5 * time.Minute)
		for state != "stopped" {
			if time.Now().After(deadline) {
				return errors.New("the test VM did not turn off within five minutes")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			if state, err = platform.State(ctx, testID); err != nil {
				return err
			}
		}
	}
	log("Deleting " + testName + " and its disks.")
	return tester.DeleteVM(ctx, testID)
}

// vmNewUUID returns a random (version 4) UUID.
func vmNewUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
