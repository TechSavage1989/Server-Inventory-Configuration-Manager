package main

// Starting a Hyper-V VM from replicated copies, on the Hyper-V host that
// receives them. A test failover attaches new differencing disks whose
// parents are the copies, so the test VM's writes never reach the copies,
// and holds the copies on the receiver so no round writes into a parent
// while the test runs (that would corrupt the test disks). Ending the test
// turns the test VM off, removes it and its differencing disks, and releases
// the copies; the source never stopped, so the next round sends everything
// that changed meanwhile. A production start attaches the copies themselves
// after the job's cutover and keeps them held.
//
// The copies of all disks must come from the same replication round
// (vmCopyState.Round), so a multi-disk server starts from one moment.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type replCopyInfo struct {
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	Complete      bool      `json:"complete"`
	Verified      bool      `json:"verified"`
	Round         string    `json:"round,omitempty"`
	Job           string    `json:"job,omitempty"`
	RecoveryPoint time.Time `json:"recoveryPoint,omitempty"`
	Updated       time.Time `json:"updated"`
	Busy          bool      `json:"busy"`
	Held          string    `json:"held,omitempty"`
	Generation    int       `json:"generation"`
}

// replParseRound splits a round tag (job ID @ recovery point).
func replParseRound(round string) (string, time.Time) {
	at := strings.LastIndex(round, "@")
	if at < 0 {
		return "", time.Time{}
	}
	when, err := time.Parse(time.RFC3339, round[at+1:])
	if err != nil {
		return "", time.Time{}
	}
	return round[:at], when
}

// replListCopies lists the VHDX copies in a receiver folder with their copy
// records.
func replListCopies(folder string, busy func(string) bool, held func(string) string) ([]replCopyInfo, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		if os.IsNotExist(err) {
			return []replCopyInfo{}, nil
		}
		return nil, err
	}
	copies := []replCopyInfo{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(name), ".vhdx") {
			continue
		}
		path := filepath.Join(folder, name)
		state, err := loadVMCopyState(path)
		if err != nil || state == nil {
			continue // not a copy this application made
		}
		info := replCopyInfo{Name: name, Size: state.Size, Complete: state.Complete, Verified: state.Verified, Round: state.Round, Updated: state.Updated, Generation: 1}
		info.Job, info.RecoveryPoint = replParseRound(state.Round)
		if busy != nil {
			info.Busy = busy(name)
		}
		if held != nil {
			info.Held = held(name)
		}
		if info.Complete && !info.Busy {
			info.Generation = replDetectGeneration(path)
		}
		copies = append(copies, info)
	}
	sort.Slice(copies, func(a, b int) bool { return strings.ToLower(copies[a].Name) < strings.ToLower(copies[b].Name) })
	return copies, nil
}

// replDetectGeneration reads the protective MBR and GPT header of a copy: a
// GPT disk boots with UEFI (generation 2), anything else with BIOS.
func replDetectGeneration(path string) int {
	image, err := openDiskImage(path)
	if err != nil {
		return 1
	}
	defer image.Close()
	header := make([]byte, 520)
	if _, err := image.ReadAt(header, 512); err != nil {
		return 1
	}
	if string(header[:8]) == "EFI PART" {
		return 2
	}
	return 1
}

// replCheckCopies refuses copies that are unfinished, being received, held,
// or from different rounds.
func replCheckCopies(chosen []replCopyInfo) error {
	if len(chosen) == 0 {
		return errors.New("choose the copied disks to start the VM from")
	}
	round := chosen[0].Round
	for _, info := range chosen {
		switch {
		case info.Busy:
			return fmt.Errorf("%s is being received right now; wait for the round to finish", info.Name)
		case info.Held != "":
			return fmt.Errorf("%s is already in use: %s", info.Name, info.Held)
		case !info.Complete:
			return fmt.Errorf("%s is not a finished copy yet; wait for a round to finish", info.Name)
		case info.Round != round:
			return fmt.Errorf("%s and %s come from different replication rounds; wait for the round in progress to finish so every disk is from the same moment", chosen[0].Name, info.Name)
		}
	}
	return nil
}

type replFailoverRequest struct {
	Mode       string   `json:"mode"` // test or production
	Name       string   `json:"name"`
	Copies     []string `json:"copies"` // boot disk first
	Generation int      `json:"generation"`
	CPUs       int      `json:"cpus"`
	MemoryMB   int64    `json:"memoryMB"`
	SecureBoot string   `json:"secureBoot"` // windows, linux or off
	TPM        bool     `json:"tpm"`
	Switch     string   `json:"switch"`
	VLAN       int      `json:"vlan"`
	Start      bool     `json:"start"`
}

// replFailover records a VM started from copies, so a test can be ended
// after the application restarts.
type replFailover struct {
	ID            string    `json:"id"`
	Mode          string    `json:"mode"`
	Name          string    `json:"name"`
	VMID          string    `json:"vmId"`
	Copies        []string  `json:"copies"`
	Children      []string  `json:"children,omitempty"`
	Folder        string    `json:"folder"`
	Switch        string    `json:"switch,omitempty"`
	RecoveryPoint time.Time `json:"recoveryPoint,omitempty"`
	Started       time.Time `json:"started"`
}

var replVMNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()-]{0,99}$`)

// replFailoverPlan builds the Hyper-V VM for the copies in folder. For a
// test, the disks are differencing children made in testFolder.
func replFailoverPlan(request replFailoverRequest, folder, testFolder string) (*vmHyperVPlan, []string, error) {
	request.Name = strings.TrimSpace(request.Name)
	if !replVMNamePattern.MatchString(request.Name) {
		return nil, nil, errors.New("type a VM name of letters, digits, spaces, dots, dashes, underscores or brackets")
	}
	if request.Mode != "test" && request.Mode != "production" {
		return nil, nil, errors.New("choose a test or a production start")
	}
	if request.Generation != 1 && request.Generation != 2 {
		return nil, nil, errors.New("choose generation 1 (BIOS) or 2 (UEFI)")
	}
	if request.CPUs < 1 || request.CPUs > 240 {
		return nil, nil, errors.New("choose 1 to 240 virtual processors")
	}
	if request.MemoryMB < 512 || request.MemoryMB > 12<<20 {
		return nil, nil, errors.New("choose at least 512 MB of memory")
	}
	if request.VLAN < 0 || request.VLAN > 4094 {
		return nil, nil, errors.New("the VLAN must be 1 to 4094, or empty")
	}
	if len(request.Copies) == 0 {
		return nil, nil, errors.New("choose the copied disks to start the VM from")
	}
	plan := &vmHyperVPlan{Name: request.Name, Generation: request.Generation, CPUs: request.CPUs, MemoryBytes: (request.MemoryMB + request.MemoryMB%2) << 20, Start: request.Start}
	plan.Path = filepath.Join(testFolder, request.Name)
	plan.Notes = "Started from replicated copies by Server Inventory and Migration Manager (production)."
	if request.Mode == "test" {
		plan.Notes = "Test failover from replicated copies by Server Inventory and Migration Manager. Ending the test in the application removes this VM and its test disks."
	}
	if request.Generation == 2 {
		switch request.SecureBoot {
		case "windows":
			plan.SecureBoot, plan.SecureBootTemplate = true, "MicrosoftWindows"
		case "linux":
			plan.SecureBoot, plan.SecureBootTemplate = true, "MicrosoftUEFICertificateAuthority"
		case "off", "":
		default:
			return nil, nil, errors.New("choose Windows, Linux or no secure boot")
		}
		plan.TPM = request.TPM
	}
	seen := map[string]bool{}
	children := []string{}
	for index, name := range request.Copies {
		if strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") || !strings.EqualFold(filepath.Ext(name), ".vhdx") || seen[strings.ToLower(name)] {
			return nil, nil, fmt.Errorf("%q is not one of the copies", name)
		}
		seen[strings.ToLower(name)] = true
		parent := filepath.Join(folder, name)
		if request.Mode == "production" {
			plan.Disks = append(plan.Disks, parent)
			continue
		}
		child := filepath.Join(testFolder, request.Name, fmt.Sprintf("test-%d-%s", index, name))
		plan.Disks = append(plan.Disks, child)
		children = append(children, child)
	}
	plan.Adapters = []vmHyperVAdapter{{Label: "Network Adapter", Switch: request.Switch, VLAN: request.VLAN}}
	return plan, children, nil
}

// replFailoverScript returns the PowerShell that makes the test disks (for a
// test) and creates the VM. It refuses existing test disks and VM names.
func replFailoverScript(plan *vmHyperVPlan, folder string, copies, children []string) string {
	var s strings.Builder
	if len(children) > 0 {
		s.WriteString("$ErrorActionPreference = 'Stop'\nImport-Module Hyper-V\n")
		fmt.Fprintf(&s, "if (Get-VM -Name %s -ErrorAction SilentlyContinue) { throw ('A Hyper-V VM named ' + %s + ' already exists; nothing was changed.') }\n", vmPSQuote(plan.Name), vmPSQuote(plan.Name))
		fmt.Fprintf(&s, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", vmPSQuote(filepath.Dir(children[0])))
		for index, child := range children {
			parent := filepath.Join(folder, copies[index])
			fmt.Fprintf(&s, "if (Test-Path -LiteralPath %s) { throw ('The test disk ' + %s + ' already exists; end the earlier test first.') }\n", vmPSQuote(child), vmPSQuote(child))
			fmt.Fprintf(&s, "New-VHD -Path %s -ParentPath %s -Differencing | Out-Null\n", vmPSQuote(child), vmPSQuote(parent))
		}
	}
	s.WriteString(plan.Script())
	return s.String()
}

// replFailoverEndScript turns the test VM off and removes it and its test
// disks. It checks the VM's ID and name, and removes only the differencing
// disks it made, each checked to be a child of its copy.
func replFailoverEndScript(f replFailover, folder string) string {
	var s strings.Builder
	line := func(format string, args ...interface{}) { fmt.Fprintf(&s, format+"\n", args...) }
	line("$ErrorActionPreference = 'Stop'")
	line("Import-Module Hyper-V")
	line("$vm = Get-VM -Id %s -ErrorAction SilentlyContinue", vmPSQuote(f.VMID))
	line("if ($vm) {")
	line("  if ($vm.Name -ne %s) { throw ('The VM with this ID is now named ' + $vm.Name + '; it was not removed.') }", vmPSQuote(f.Name))
	line("  if ($vm.State -ne 'Off') { Stop-VM -VM $vm -TurnOff -Force }")
	line("  Remove-VM -VM $vm -Force")
	line("}")
	for index, child := range f.Children {
		parent := filepath.Join(folder, f.Copies[index])
		line("if (Test-Path -LiteralPath %s) {", vmPSQuote(child))
		line("  $vhd = Get-VHD -Path %s", vmPSQuote(child))
		line("  if ($vhd.VhdType -ne 'Differencing' -or $vhd.ParentPath -ne %s) { throw ('%s is not the test disk this application made; it was left in place.') }", vmPSQuote(parent), strings.ReplaceAll(child, "'", "''"))
		line("  Remove-Item -LiteralPath %s -Force", vmPSQuote(child))
		line("}")
	}
	if len(f.Children) > 0 {
		dir := filepath.Dir(f.Children[0])
		line("if ((Test-Path -LiteralPath %s) -and -not (Get-ChildItem -LiteralPath %s -Recurse -File)) { Remove-Item -LiteralPath %s -Recurse -Force }", vmPSQuote(dir), vmPSQuote(dir), vmPSQuote(dir))
	}
	return s.String()
}

// replFailoverStore keeps the started VMs in one JSON file.
type replFailoverStore struct{ path string }

func (s replFailoverStore) Load() ([]replFailover, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []replFailover{}, nil
	}
	if err != nil {
		return nil, err
	}
	list := []replFailover{}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return list, nil
}

func (s replFailoverStore) Save(list []replFailover) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return replWriteFileAtomic(s.path, raw)
}
