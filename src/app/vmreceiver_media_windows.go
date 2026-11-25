//go:build windows

package main

// Receiver boot media: runs the embedded Build-ReceiverMedia.ps1, which uses
// the operator's Windows ADK and Windows PE add-on to make an ISO that boots
// a new VM or physical server straight into this program's receiver.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const receiverMediaScript = "Build-ReceiverMedia.ps1"

type vmReceiverMediaOptions struct {
	Output  string   `json:"output"`
	Work    string   `json:"work"`
	Drivers []string `json:"drivers"`
	Target  string   `json:"target"`
	Port    int      `json:"port"`
	Disks   string   `json:"disks"`
	AdkRoot string   `json:"adkRoot,omitempty"`
	// Plan checks the inputs and reports what would be built.
	Plan bool `json:"plan"`
}

type vmReceiverMediaResult struct {
	OK                bool     `json:"ok"`
	Output            string   `json:"output"`
	SHA256            string   `json:"sha256"`
	Size              int64    `json:"size"`
	Target            string   `json:"target"`
	ReceiverArguments string   `json:"receiverArguments"`
	Problems          []string `json:"problems"`
	Error             string   `json:"error"`
}

// vmBuildReceiverMedia builds (or with Plan, checks) receiver media using
// receiverExe as the program placed on it.
func vmBuildReceiverMedia(ctx context.Context, powershell, receiverExe string, options vmReceiverMediaOptions, log func(string)) (*vmReceiverMediaResult, error) {
	if log == nil {
		log = func(string) {}
	}
	if options.Target == "" {
		options.Target = vmTargetProxmox
	}
	if !vmIsTarget(options.Target) || options.Target == vmTargetAzure || options.Target == vmTargetAWS {
		return nil, fmt.Errorf("receiver media is for Hyper-V, HyperCore, Nutanix, Proxmox or physical servers, not %s", vmTargetLabel(options.Target))
	}
	if options.Port == 0 {
		options.Port = 8443
	}
	if options.Disks == "" {
		options.Disks = "blank"
	}
	if options.Output == "" || options.Work == "" {
		return nil, errors.New("choose the ISO file and an empty work folder")
	}
	runtime, err := os.MkdirTemp("", "ReceiverMedia-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(runtime)
	script := filepath.Join(runtime, receiverMediaScript)
	if _, err := writeEmbeddedFile(receiverMediaScript, script); err != nil {
		return nil, err
	}
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script,
		"-OutputIso", options.Output, "-WorkFolder", options.Work, "-ReceiverExe", receiverExe,
		"-Target", options.Target, "-Port", strconv.Itoa(options.Port), "-Disks", options.Disks}
	if len(options.Drivers) > 0 {
		for _, folder := range options.Drivers {
			if strings.Contains(folder, "|") {
				return nil, fmt.Errorf("%q is not a folder path", folder)
			}
		}
		arguments = append(arguments, "-DriverFolders", strings.Join(options.Drivers, "|"))
	}
	if options.AdkRoot != "" {
		arguments = append(arguments, "-AdkRoot", options.AdkRoot)
	}
	if options.Plan {
		arguments = append(arguments, "-Plan")
	}
	command := exec.CommandContext(ctx, powershell, arguments...)
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		return nil, err
	}
	var result *vmReceiverMediaResult
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "RESULT:") {
			parsed := &vmReceiverMediaResult{}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "RESULT:")), parsed) == nil {
				result = parsed
			}
			continue
		}
		if line != "" {
			log(line)
		}
	}
	waitErr := command.Wait()
	if result == nil {
		if waitErr == nil {
			waitErr = errors.New("the media builder returned no result")
		}
		return nil, fmt.Errorf("building the receiver media failed: %w", waitErr)
	}
	return result, nil
}
