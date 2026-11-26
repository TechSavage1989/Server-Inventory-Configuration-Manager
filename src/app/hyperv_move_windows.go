//go:build windows

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed Move-HyperVVM.ps1
var hypervMoveScriptData []byte

const hypervMoveScript = "Move-HyperVVM.ps1"

// hypervScriptHost runs Move-HyperVVM.ps1 against one Hyper-V host with the
// account running this application.
type hypervScriptHost struct {
	app   *application
	host  string // empty for this computer
	local bool
	log   func(string)
}

func (a *application) hypervHostFor(server string, log func(string)) *hypervScriptHost {
	local := a.hypervIsLocal(server)
	host := server
	if local {
		host = ""
	}
	return &hypervScriptHost{app: a, host: host, local: local, log: log}
}

func (h *hypervScriptHost) run(ctx context.Context, mode, vmID, checkpoint string, out any) error {
	runtimeRoot := filepath.Join(h.app.update.appDataRoot, "Runtime")
	if err := os.MkdirAll(runtimeRoot, 0700); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(runtimeRoot, "HyperVMove-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	script := filepath.Join(directory, hypervMoveScript)
	if err := os.WriteFile(script, hypervMoveScriptData, 0600); err != nil {
		return err
	}
	resultPath := filepath.Join(directory, "Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "-Mode", mode, "-VMId", vmID, "-ResultPath", resultPath}
	if h.host != "" {
		arguments = append(arguments, "-ComputerName", h.host)
	}
	if checkpoint != "" {
		arguments = append(arguments, "-CheckpointId", checkpoint)
	}
	command := exec.CommandContext(ctx, h.app.powerShellPath, arguments...)
	waitErr := h.app.runEngineProcess(command, func(line string) {
		if h.log != nil {
			h.log(line)
		}
	}, func(*exec.Cmd) {})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	data, readErr := os.ReadFile(resultPath)
	if readErr != nil {
		if waitErr != nil {
			return fmt.Errorf("the Hyper-V step stopped: %v", waitErr)
		}
		return errors.New("the Hyper-V step wrote no result")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &envelope); err != nil {
		return errors.New("the Hyper-V step's result could not be read")
	}
	if !envelope.OK {
		return errors.New(vmNonEmpty(envelope.Error, "The Hyper-V step failed."))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

func (h *hypervScriptHost) reach(disks []hypervDiskRef) {
	for index := range disks {
		disks[index].Reach = hypervReachablePath(disks[index].Path, h.host, h.local)
	}
}

func (h *hypervScriptHost) State(ctx context.Context, vmID string) (*hypervVMState, error) {
	var state hypervVMState
	if err := h.run(ctx, "State", vmID, "", &state); err != nil {
		return nil, err
	}
	h.reach(state.Disks)
	return &state, nil
}

func (h *hypervScriptHost) Checkpoint(ctx context.Context, vmID string) (*hypervCheckpoint, error) {
	var checkpoint hypervCheckpoint
	if err := h.run(ctx, "Checkpoint", vmID, "", &checkpoint); err != nil {
		return nil, err
	}
	if checkpoint.ID == "" {
		return nil, errors.New("Hyper-V did not return the checkpoint")
	}
	h.reach(checkpoint.Disks)
	return &checkpoint, nil
}

func (h *hypervScriptHost) RemoveCheckpoint(ctx context.Context, vmID, checkpointID string) error {
	return h.run(ctx, "RemoveCheckpoint", vmID, checkpointID, nil)
}

func (h *hypervScriptHost) Shutdown(ctx context.Context, vmID string) (string, error) {
	var result struct {
		PowerState string `json:"powerState"`
	}
	if err := h.run(ctx, "Shutdown", vmID, "", &result); err != nil {
		return "", err
	}
	return result.PowerState, nil
}

// runMoveRound runs a copy round or cutover for a VM from either source:
// vSphere replication for VMware VMs, checkpoints for Hyper-V VMs.
func (a *application) runMoveRound(ctx context.Context, inventory *vmInventory, vm vmMachine, options vmConnectOptions, request vmMoveRequest, log func(string)) (*vmMoveResult, error) {
	if vm.Source != vmSourceHyperV {
		return vmMoveRound(ctx, options, request, log)
	}
	order := []string{}
	for _, disk := range vm.Disks {
		if disk.RDM != "" {
			return nil, errors.New("the VM has a pass-through disk, which is not in a virtual hard disk; copy its data separately and remove it from the VM first")
		}
		order = append(order, disk.File)
	}
	return hypervMoveRound(ctx, a.hypervHostFor(inventory.Server, log), hypervMoveRequest{
		VMID: vm.ID, Name: vm.Name, CopyFolder: request.CopyFolder, Cutover: request.Cutover, DiskOrder: order, NewSink: request.NewSink, Progress: request.Progress, Throttle: request.Throttle,
	}, log)
}
