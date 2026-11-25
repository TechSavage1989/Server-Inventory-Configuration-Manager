package main

// Moving a VMware VM to a Hyper-V host or failover cluster that is NOT the
// computer running the application: the admin computer holds both
// connections, vCenter/ESXi through the vSphere API and the Hyper-V host
// through PowerShell remoting (WinRM), the same management channel Windows
// Admin Center and SCVMM use. Nothing is installed or started on the Hyper-V
// host: copy rounds write each disk as a VHDX straight onto the host's storage
// through its administrative share (or through a receiver the operator
// started on the node), and the cutover creates the VM there with the Hyper-V
// module, optionally as a highly available cluster role.
//
// This file is untagged so the path, version and storage rules are tested on
// any platform; hyperv_remote_windows.go runs it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// hvRemoteVolume is a volume on the Hyper-V host: a drive or a Cluster Shared
// Volume, with its free space as the host reported it.
type hvRemoteVolume struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Free  uint64 `json:"freeBytes"`
	Total uint64 `json:"totalBytes"`
	CSV   bool   `json:"csv"`
}

type hvRemoteNode struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type hvRemoteSwitch struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// hvRemoteInfo is what Connect-HyperVTarget.ps1 read from the host.
type hvRemoteInfo struct {
	// Host is the name the operator typed (a host or a cluster name);
	// Computer is the node that answered.
	Host          string           `json:"host"`
	Computer      string           `json:"computer"`
	Domain        string           `json:"domain"`
	Caption       string           `json:"caption"`
	Version       string           `json:"version"`
	Build         int              `json:"build"`
	SystemDrive   string           `json:"systemDrive"`
	HyperV        bool             `json:"hyperV"`
	HyperVModule  bool             `json:"hyperVModule"`
	ClusterModule bool             `json:"clusterModule"`
	VHDPath       string           `json:"vhdPath"`
	VMPath        string           `json:"vmPath"`
	Cluster       string           `json:"cluster"`
	Nodes         []hvRemoteNode   `json:"nodes"`
	Volumes       []hvRemoteVolume `json:"volumes"`
	Switches      []hvRemoteSwitch `json:"switches"`
	// VMs are the names already used on the host (and, in a cluster, by the
	// cluster's VM roles), so a name collision is refused before any copy.
	VMs      []string `json:"vms"`
	Warnings []string `json:"warnings"`
	// Supported and Support come from hvRemoteSupport.
	Supported bool   `json:"supported"`
	Support   string `json:"support"`
}

const hvRemoteResultLimit = 4 << 20

var (
	hvRemoteNodePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
	hvRemoteDrivePath    = regexp.MustCompile(`^[A-Za-z]:\\`)
	hvRemoteBadPathChars = regexp.MustCompile(`[<>"|?*\x00-\x1f/]`)
)

// hvRemoteParseResult reads the script's {"ok","error","result"} file.
func hvRemoteParseResult(data []byte, into interface{}) error {
	if len(data) > hvRemoteResultLimit {
		return errors.New("the Hyper-V host's answer is too large")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("the Hyper-V host's answer could not be read: %v", err)
	}
	if !envelope.OK {
		if strings.TrimSpace(envelope.Error) == "" {
			envelope.Error = "the Hyper-V host did not answer"
		}
		return errors.New(hvRemoteFriendlyError(envelope.Error))
	}
	if into == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, into)
}

// hvRemoteFriendlyError turns the usual PowerShell remoting failures into
// what the operator can do about them.
func hvRemoteFriendlyError(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "trustedhosts") || strings.Contains(lower, "kerberos") && strings.Contains(lower, "ip address"):
		return "Windows would not authenticate to the Hyper-V host by that address. Type the host's or cluster's DNS name (as it is known in the domain) instead of an IP address, and run this from a computer in the same domain or a trusted one. " + text
	case strings.Contains(lower, "access is denied") || strings.Contains(lower, "access denied"):
		return "The Hyper-V host refused the account. Use an account that is an administrator on the host (Hyper-V Administrators is not enough to create files on its storage). " + text
	case strings.Contains(lower, "winrm") && (strings.Contains(lower, "cannot complete") || strings.Contains(lower, "cannot process") || strings.Contains(lower, "not running")):
		return "The Hyper-V host did not answer PowerShell remoting (WinRM, TCP 5985). It is on by default on Windows Server; check the name, the firewall and that the WinRM service runs on the host. " + text
	}
	return text
}

// hvRemoteSupport says whether a Hyper-V host of this build can receive a
// moved VM, and what it offers.
func hvRemoteSupport(info *hvRemoteInfo) (bool, string) {
	if info == nil {
		return false, ""
	}
	name := strings.TrimSpace(strings.TrimPrefix(info.Caption, "Microsoft "))
	if name == "" {
		name = "Windows build " + fmt.Sprint(info.Build)
	}
	switch {
	case !info.HyperV:
		return false, info.Computer + " (" + name + ") does not have the Hyper-V role."
	case !info.HyperVModule:
		return false, info.Computer + " has the Hyper-V role but not the Hyper-V Module for Windows PowerShell. Add it on the host (Server Manager, Remote Server Administration Tools, Hyper-V Management Tools) and connect again."
	case info.Build > 0 && info.Build < 9600:
		return false, name + " is older than Windows Server 2012 R2, which this move does not support."
	}
	notes := []string{name + " is supported"}
	if info.Build > 0 && info.Build < 14393 {
		notes = append(notes, "Windows Server 2012 R2 hosts get generation 1 or 2 VMs without a virtual TPM (the TPM needs Windows Server 2016 or later)")
	}
	if info.Cluster != "" {
		if info.ClusterModule {
			notes = append(notes, "failover cluster "+info.Cluster+" can make the VM highly available")
		} else {
			notes = append(notes, "the cluster's PowerShell module (Failover Cluster Module for Windows PowerShell) is missing on "+info.Computer+", so the VM cannot be made highly available from here")
		}
	}
	return true, strings.Join(notes, "; ") + "."
}

// hvCleanWinPath normalizes a Windows path given as text, on any platform.
func hvCleanWinPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "/", `\`))
	for strings.Contains(path, `\\`) && !strings.HasPrefix(path, `\\`) {
		path = strings.ReplaceAll(path, `\\`, `\`)
	}
	if len(path) > 3 {
		path = strings.TrimRight(path, `\`)
	}
	return path
}

// hvRemoteCheckLocalPath checks a folder path as the Hyper-V host sees it:
// a full drive path, no parent references, nothing inside Windows, the
// program or profile folders, and not a drive root.
func hvRemoteCheckLocalPath(path string) (string, error) {
	path = hvCleanWinPath(path)
	if !hvRemoteDrivePath.MatchString(path) {
		return "", errors.New("type a full folder path on the Hyper-V host, such as C:\\ClusterStorage\\Volume1\\VMs or D:\\Hyper-V")
	}
	if hvRemoteBadPathChars.MatchString(path[2:]) || strings.Contains(path[3:], ":") {
		return "", errors.New("the folder path has characters Windows does not allow")
	}
	for _, part := range strings.Split(path[3:], `\`) {
		if part == ".." || part == "." {
			return "", errors.New("the folder path must not contain . or ..")
		}
	}
	if len(path) <= 3 {
		return "", errors.New("choose a folder, not a drive root")
	}
	lower := strings.ToLower(path[3:]) + `\`
	for _, protected := range []string{`windows\`, `program files\`, `program files (x86)\`, `programdata\`, `users\`} {
		if strings.HasPrefix(lower, protected) {
			return "", errors.New("choose a folder outside Windows, Program Files, ProgramData and the user profiles on the Hyper-V host")
		}
	}
	return path, nil
}

// hvRemoteOnCSV reports whether a host path is inside a Cluster Shared Volume.
func hvRemoteOnCSV(info *hvRemoteInfo, path string) bool {
	path = strings.ToLower(hvCleanWinPath(path))
	for _, volume := range info.Volumes {
		root := strings.ToLower(hvCleanWinPath(volume.Path))
		if volume.CSV && root != "" && strings.HasPrefix(path, root+`\`) {
			return true
		}
	}
	drive := info.SystemDrive
	if drive == "" {
		drive = "C:"
	}
	return strings.HasPrefix(path, strings.ToLower(drive)+`\clusterstorage\`) && len(strings.Split(path, `\`)) > 3
}

// hvRemoteVolumeFor returns the host volume holding path (longest match).
func hvRemoteVolumeFor(info *hvRemoteInfo, path string) *hvRemoteVolume {
	path = strings.ToLower(hvCleanWinPath(path))
	var best *hvRemoteVolume
	for index := range info.Volumes {
		volume := &info.Volumes[index]
		root := strings.ToLower(hvCleanWinPath(volume.Path))
		if root == "" {
			continue
		}
		if path == root || strings.HasPrefix(path, strings.TrimRight(root, `\`)+`\`) {
			if best == nil || len(root) > len(hvCleanWinPath(best.Path)) {
				best = volume
			}
		}
	}
	return best
}

// hvRemoteAdminPath gives the administrative-share path of a host folder:
// D:\Hyper-V\web01 on node HV1 is \\HV1\D$\Hyper-V\web01.
func hvRemoteAdminPath(node, local string) (string, error) {
	node = strings.TrimSpace(node)
	if !hvRemoteNodePattern.MatchString(node) {
		return "", errors.New("the Hyper-V node's name is not valid")
	}
	clean, err := hvRemoteCheckLocalPath(local)
	if err != nil {
		return "", err
	}
	return `\\` + node + `\` + strings.ToUpper(clean[:1]) + `$` + clean[2:], nil
}

// hvRemoteLocalPath is the reverse of hvRemoteAdminPath.
func hvRemoteLocalPath(node, admin string) (string, error) {
	prefix := `\\` + strings.ToLower(node) + `\`
	clean := strings.ReplaceAll(admin, "/", `\`)
	if !strings.HasPrefix(strings.ToLower(clean), prefix) || len(clean) < len(prefix)+3 || clean[len(prefix)+1] != '$' {
		return "", fmt.Errorf("%s is not on %s's administrative share", admin, node)
	}
	rest := clean[len(prefix):]
	return strings.ToUpper(rest[:1]) + ":" + rest[2:], nil
}

// hvRemoteNodeFor checks that node is the host itself or one of its cluster
// nodes and is up.
func hvRemoteNodeFor(info *hvRemoteInfo, node string) (string, error) {
	node = strings.TrimSpace(node)
	if node == "" {
		node = info.Computer
	}
	short := func(name string) string { return strings.ToLower(strings.SplitN(name, ".", 2)[0]) }
	if len(info.Nodes) == 0 {
		if short(node) == short(info.Computer) {
			return info.Computer, nil
		}
		return "", fmt.Errorf("%s is not the connected Hyper-V host %s", node, info.Computer)
	}
	for _, candidate := range info.Nodes {
		if short(candidate.Name) == short(node) {
			if candidate.State != "" && !strings.EqualFold(candidate.State, "Up") {
				return "", fmt.Errorf("cluster node %s is %s; choose a node that is up", candidate.Name, candidate.State)
			}
			return candidate.Name, nil
		}
	}
	return "", fmt.Errorf("%s is not a node of cluster %s", node, info.Cluster)
}

// hvRemoteNameUsed reports whether a VM of this name already exists on the
// host or in its cluster.
func hvRemoteNameUsed(info *hvRemoteInfo, name string) bool {
	for _, existing := range info.VMs {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// hvRemoteMoveChoice is what the operator chose for one VM.
type hvRemoteMoveChoice struct {
	Node       string
	CopyFolder string
	VMFolder   string
	Cluster    bool
	Cutover    bool
	// NeedBytes is the room the first copy needs (0 skips the check);
	// HaveBytes is what earlier copies already hold there.
	NeedBytes uint64
	HaveBytes uint64
}

// hvRemotePlanMove checks the operator's choices against what the host
// reported and returns the node, the host-local folders and the
// administrative-share folder the copies are written to.
func hvRemotePlanMove(info *hvRemoteInfo, vmName string, choice hvRemoteMoveChoice) (node, copyLocal, vmLocal, copyAdmin string, err error) {
	if info == nil {
		return "", "", "", "", errors.New("connect to the Hyper-V host first")
	}
	if ok, why := hvRemoteSupport(info); !ok {
		return "", "", "", "", errors.New(why)
	}
	if node, err = hvRemoteNodeFor(info, choice.Node); err != nil {
		return "", "", "", "", err
	}
	copyFolder := strings.TrimSpace(choice.CopyFolder)
	if copyFolder == "" {
		base := info.VHDPath
		if base == "" {
			return "", "", "", "", errors.New("choose the folder on the Hyper-V host for the copied disks")
		}
		copyFolder = strings.TrimRight(hvCleanWinPath(base), `\`) + `\` + vmReplicaBaseName(vmName)
	}
	if copyLocal, err = hvRemoteCheckLocalPath(copyFolder); err != nil {
		return "", "", "", "", fmt.Errorf("copied disks: %v", err)
	}
	if strings.TrimSpace(choice.VMFolder) != "" {
		if vmLocal, err = hvRemoteCheckLocalPath(choice.VMFolder); err != nil {
			return "", "", "", "", fmt.Errorf("Hyper-V VM folder: %v", err)
		}
	}
	if choice.Cluster {
		if info.Cluster == "" {
			return "", "", "", "", errors.New("the Hyper-V host is not in a failover cluster; untick Make it highly available")
		}
		if !info.ClusterModule {
			return "", "", "", "", errors.New("the Failover Cluster Module for Windows PowerShell is missing on the host, so the VM cannot be made highly available from here")
		}
		if !hvRemoteOnCSV(info, copyLocal) || vmLocal == "" || !hvRemoteOnCSV(info, vmLocal) {
			return "", "", "", "", errors.New("a highly available VM must keep its disks and its configuration on a Cluster Shared Volume (C:\\ClusterStorage\\...); choose one for both folders, or untick Make it highly available")
		}
	}
	if !choice.Cutover && choice.NeedBytes > 0 && choice.NeedBytes > choice.HaveBytes {
		if volume := hvRemoteVolumeFor(info, copyLocal); volume != nil && volume.Free < choice.NeedBytes-choice.HaveBytes {
			return "", "", "", "", fmt.Errorf("there is not enough free space on %s for %s's disks: about %s is needed and %s is free. Choose a larger volume, such as another Cluster Shared Volume", volume.Path, vmName, vmFormatBytes(int64(choice.NeedBytes-choice.HaveBytes)), vmFormatBytes(int64(volume.Free)))
		}
	}
	if copyAdmin, err = hvRemoteAdminPath(node, copyLocal); err != nil {
		return "", "", "", "", err
	}
	return node, copyLocal, vmLocal, copyAdmin, nil
}

// vmReplicaBaseName is the VM's name as a folder name.
func vmReplicaBaseName(vm string) string {
	return strings.TrimSuffix(vmReplicaFileName(vm, 0), "-disk0.vhdx")
}

// hvRemoteReceiverNames names each disk on a receiver: the receiver accepts
// only plain .vhdx names.
func hvRemoteReceiverNames(vm string, disks int) []string {
	names := make([]string, disks)
	base := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(vm, "-"), "-.")
	if base == "" {
		base = "vm"
	}
	if len(base) > 100 {
		base = base[:100]
	}
	for index := range names {
		names[index] = fmt.Sprintf("%s-disk%d.vhdx", base, index)
	}
	return names
}

// hvRemoteSameComputer compares two computer names by their first label.
func hvRemoteSameComputer(a, b string) bool {
	short := func(name string) string { return strings.ToLower(strings.SplitN(strings.TrimSpace(name), ".", 2)[0]) }
	return short(a) != "" && short(a) == short(b)
}

// hvRemoteSwitchNames lists the host's switch names, sorted.
func hvRemoteSwitchNames(info *hvRemoteInfo) []string {
	names := []string{}
	for _, item := range info.Switches {
		if strings.TrimSpace(item.Name) != "" {
			names = append(names, item.Name)
		}
	}
	sort.Strings(names)
	return names
}

// hvRemoteCheckSwitches warns about plan switches the host does not have:
// New-VM would fail at the cutover after the source VM is already off.
func hvRemoteCheckSwitches(info *hvRemoteInfo, plan *vmHyperVPlan) error {
	have := map[string]bool{}
	for _, name := range hvRemoteSwitchNames(info) {
		have[strings.ToLower(name)] = true
	}
	missing := []string{}
	for _, adapter := range plan.Adapters {
		if adapter.Switch != "" && !have[strings.ToLower(adapter.Switch)] {
			missing = append(missing, adapter.Switch)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the Hyper-V host has no virtual switch named %s; fix the plan's network map (switch names on the host: %s)", strings.Join(missing, ", "), strings.Join(hvRemoteSwitchNames(info), ", "))
	}
	return nil
}

// hvRemoteCreatedID reads the creation script's output lines: the cluster
// report, then the new VM's ID on the last line.
func hvRemoteCreatedID(lines []string, plan *vmHyperVPlan) (string, error) {
	id := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, vmClusterRoleAdded) || strings.HasPrefix(line, vmClusterRoleFailed) {
			plan.ClusterResult = line
		}
		if line != "" {
			id = line
		}
	}
	if !vmHyperVIDTextPattern.MatchString(id) {
		return "", errors.New("Hyper-V did not return the new VM's ID: " + strings.Join(lines, " "))
	}
	return id, nil
}

var vmHyperVIDTextPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
