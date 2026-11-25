package main

import (
	"strings"
	"testing"
)

func hvRemoteTestCluster() *hvRemoteInfo {
	info := &hvRemoteInfo{
		Host: "hvclu01", Computer: "HV01", Caption: "Microsoft Windows Server 2022 Datacenter", Build: 20348, SystemDrive: "C:",
		HyperV: true, HyperVModule: true, ClusterModule: true, Cluster: "HVCLU01",
		VHDPath: `C:\ClusterStorage\Volume1\Hyper-V`, VMPath: `C:\ClusterStorage\Volume1\Hyper-V`,
		Nodes: []hvRemoteNode{{Name: "HV01", State: "Up"}, {Name: "HV02", State: "Up"}, {Name: "HV03", State: "Paused"}},
		Volumes: []hvRemoteVolume{
			{Path: `C:\`, Free: 40 << 30},
			{Path: `C:\ClusterStorage\Volume1`, Label: "Cluster Virtual Disk (CSV1)", Free: 500 << 30, CSV: true},
			{Path: `C:\ClusterStorage\Volume2`, Label: "Cluster Virtual Disk (CSV2)", Free: 10 << 30, CSV: true},
		},
		Switches: []hvRemoteSwitch{{Name: "LAN", Type: "External"}, {Name: "DMZ", Type: "External"}},
		VMs:      []string{"existing01"},
	}
	info.Supported, info.Support = hvRemoteSupport(info)
	return info
}

func TestHVRemoteSupportNamesVersionsAndRefusesOldOrIncompleteHosts(t *testing.T) {
	info := hvRemoteTestCluster()
	if !info.Supported || !strings.Contains(info.Support, "Windows Server 2022 Datacenter is supported") || !strings.Contains(info.Support, "HVCLU01") {
		t.Fatalf("support = %v %q", info.Supported, info.Support)
	}
	old := &hvRemoteInfo{Computer: "OLD", Caption: "Microsoft Windows Server 2012 R2 Standard", Build: 9600, HyperV: true, HyperVModule: true}
	if ok, why := hvRemoteSupport(old); !ok || !strings.Contains(why, "virtual TPM") {
		t.Fatalf("2012 R2: %v %q", ok, why)
	}
	for _, refused := range []*hvRemoteInfo{
		{Computer: "X", Caption: "Microsoft Windows Server 2012 Standard", Build: 9200, HyperV: true, HyperVModule: true},
		{Computer: "X", Caption: "Microsoft Windows Server 2019 Standard", Build: 17763},
		{Computer: "X", Caption: "Microsoft Windows Server 2019 Standard", Build: 17763, HyperV: true},
	} {
		if ok, why := hvRemoteSupport(refused); ok || why == "" {
			t.Fatalf("%+v was accepted: %q", refused, why)
		}
	}
	noModule := hvRemoteTestCluster()
	noModule.ClusterModule = false
	if _, why := hvRemoteSupport(noModule); !strings.Contains(why, "Failover Cluster Module") {
		t.Fatalf("missing cluster module not named: %q", why)
	}
}

func TestHVRemotePathsUseTheNodesAdministrativeShare(t *testing.T) {
	admin, err := hvRemoteAdminPath("HV02", `C:\ClusterStorage\Volume1\Hyper-V\web01`)
	if err != nil || admin != `\\HV02\C$\ClusterStorage\Volume1\Hyper-V\web01` {
		t.Fatalf("admin = %q, %v", admin, err)
	}
	local, err := hvRemoteLocalPath("HV02", admin+`\web01-disk0.vhdx`)
	if err != nil || local != `C:\ClusterStorage\Volume1\Hyper-V\web01\web01-disk0.vhdx` {
		t.Fatalf("local = %q, %v", local, err)
	}
	if _, err := hvRemoteLocalPath("HV01", admin); err == nil {
		t.Fatal("a path on another node was accepted")
	}
	for _, bad := range []string{`relative\path`, `C:\`, `D:`, `C:\Windows\VMs`, `c:\program files\x`, `C:\ProgramData\x`, `C:\Users\admin\VMs`, `C:\VMs\..\Windows`, `\\server\share\x`, `C:\VMs\a|b`, `C:\VMs\a:b`} {
		if _, err := hvRemoteCheckLocalPath(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	if _, err := hvRemoteAdminPath("bad name", `D:\VMs`); err == nil {
		t.Fatal("a bad node name was accepted")
	}
}

func TestHVRemotePlanMoveChecksNodeStorageAndCluster(t *testing.T) {
	info := hvRemoteTestCluster()
	node, copyLocal, vmLocal, copyAdmin, err := hvRemotePlanMove(info, "web 01", hvRemoteMoveChoice{Node: "hv02.example.com", CopyFolder: `C:\ClusterStorage\Volume1\VMs\web01`, VMFolder: `C:\ClusterStorage\Volume1\VMs`, Cluster: true, NeedBytes: 100 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if node != "HV02" || copyLocal != `C:\ClusterStorage\Volume1\VMs\web01` || vmLocal != `C:\ClusterStorage\Volume1\VMs` || copyAdmin != `\\HV02\C$\ClusterStorage\Volume1\VMs\web01` {
		t.Fatalf("plan = %q %q %q %q", node, copyLocal, vmLocal, copyAdmin)
	}
	// The default folder is the host's virtual hard disk folder.
	if _, copyLocal, _, _, err := hvRemotePlanMove(info, "web 01", hvRemoteMoveChoice{}); err != nil || copyLocal != `C:\ClusterStorage\Volume1\Hyper-V\web-01` {
		t.Fatalf("default = %q, %v", copyLocal, err)
	}
	cases := map[string]hvRemoteMoveChoice{
		"paused node":         {Node: "HV03", CopyFolder: `C:\ClusterStorage\Volume1\VMs\a`},
		"not a node":          {Node: "HV09", CopyFolder: `C:\ClusterStorage\Volume1\VMs\a`},
		"HA off CSV":          {Node: "HV01", CopyFolder: `D:\VMs\a`, VMFolder: `C:\ClusterStorage\Volume1\VMs`, Cluster: true},
		"HA no VM folder":     {Node: "HV01", CopyFolder: `C:\ClusterStorage\Volume1\VMs\a`, Cluster: true},
		"short of space":      {Node: "HV01", CopyFolder: `C:\ClusterStorage\Volume2\VMs\a`, NeedBytes: 50 << 30},
		"protected VM folder": {Node: "HV01", CopyFolder: `C:\ClusterStorage\Volume1\VMs\a`, VMFolder: `C:\Windows`},
	}
	for name, choice := range cases {
		if _, _, _, _, err := hvRemotePlanMove(info, "a", choice); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	// Space already held by earlier copies counts, and a cutover rewrites
	// in place.
	if _, _, _, _, err := hvRemotePlanMove(info, "a", hvRemoteMoveChoice{Node: "HV01", CopyFolder: `C:\ClusterStorage\Volume2\VMs\a`, NeedBytes: 50 << 30, HaveBytes: 45 << 30}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := hvRemotePlanMove(info, "a", hvRemoteMoveChoice{Node: "HV01", CopyFolder: `C:\ClusterStorage\Volume2\VMs\a`, NeedBytes: 50 << 30, Cutover: true}); err != nil {
		t.Fatal(err)
	}
	standalone := &hvRemoteInfo{Computer: "HV10", Caption: "Microsoft Windows Server 2019 Standard", Build: 17763, HyperV: true, HyperVModule: true, Volumes: []hvRemoteVolume{{Path: `D:\`, Free: 1 << 40}}}
	if node, _, _, admin, err := hvRemotePlanMove(standalone, "a", hvRemoteMoveChoice{CopyFolder: `D:\Hyper-V\a`}); err != nil || node != "HV10" || admin != `\\HV10\D$\Hyper-V\a` {
		t.Fatalf("standalone = %q %q %v", node, admin, err)
	}
	if _, _, _, _, err := hvRemotePlanMove(standalone, "a", hvRemoteMoveChoice{CopyFolder: `D:\Hyper-V\a`, Cluster: true}); err == nil {
		t.Fatal("HA on a standalone host was accepted")
	}
	if _, _, _, _, err := hvRemotePlanMove(standalone, "a", hvRemoteMoveChoice{Node: "HV11", CopyFolder: `D:\Hyper-V\a`}); err == nil {
		t.Fatal("another computer was accepted as the node")
	}
}

func TestHVRemoteNamesSwitchesAndCreatedID(t *testing.T) {
	info := hvRemoteTestCluster()
	if !hvRemoteNameUsed(info, " EXISTING01 ") || hvRemoteNameUsed(info, "web01") {
		t.Fatal("name check is wrong")
	}
	names := hvRemoteReceiverNames("web 01 (prod)", 2)
	for _, name := range names {
		if !vmReceiverName.MatchString(name) {
			t.Fatalf("%q is not a receiver name", name)
		}
	}
	if names[0] != "web-01-prod-disk0.vhdx" || names[1] != "web-01-prod-disk1.vhdx" {
		t.Fatalf("names = %v", names)
	}
	plan := &vmHyperVPlan{Adapters: []vmHyperVAdapter{{Switch: "lan"}, {Switch: ""}}}
	if err := hvRemoteCheckSwitches(info, plan); err != nil {
		t.Fatal(err)
	}
	plan.Adapters = append(plan.Adapters, vmHyperVAdapter{Switch: "Storage"})
	if err := hvRemoteCheckSwitches(info, plan); err == nil || !strings.Contains(err.Error(), "Storage") || !strings.Contains(err.Error(), "DMZ, LAN") {
		t.Fatalf("missing switch: %v", err)
	}
	id, err := hvRemoteCreatedID([]string{"", vmClusterRoleAdded, "0D4C1A2B-3E4F-5061-7283-94A5B6C7D8E9"}, plan)
	if err != nil || id != "0D4C1A2B-3E4F-5061-7283-94A5B6C7D8E9" || plan.ClusterResult != vmClusterRoleAdded {
		t.Fatalf("id = %q %v %q", id, err, plan.ClusterResult)
	}
	if _, err := hvRemoteCreatedID([]string{"something else"}, plan); err == nil {
		t.Fatal("a missing ID was accepted")
	}
	if !hvRemoteSameComputer("hv02.example.com", "HV02") || hvRemoteSameComputer("hv02", "hv03") || hvRemoteSameComputer("", "") {
		t.Fatal("computer comparison is wrong")
	}
}

func TestHVRemoteParseResultExplainsRemotingFailures(t *testing.T) {
	info := &hvRemoteInfo{}
	if err := hvRemoteParseResult([]byte("\ufeff"+`{"ok":true,"result":{"computer":"HV01","build":20348,"volumes":[{"path":"C:\\ClusterStorage\\Volume1","freeBytes":5,"csv":true}]}}`), info); err != nil || info.Computer != "HV01" || !info.Volumes[0].CSV {
		t.Fatalf("parse = %+v %v", info, err)
	}
	err := hvRemoteParseResult([]byte(`{"ok":false,"error":"Connecting to remote server 10.0.0.5 failed: ... TrustedHosts list"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "DNS name") {
		t.Fatalf("TrustedHosts: %v", err)
	}
	err = hvRemoteParseResult([]byte(`{"ok":false,"error":"Access is denied."}`), nil)
	if err == nil || !strings.Contains(err.Error(), "administrator on the host") {
		t.Fatalf("access denied: %v", err)
	}
	if err := hvRemoteParseResult([]byte("not json"), nil); err == nil {
		t.Fatal("bad JSON was accepted")
	}
}

func TestHyperVPlanScriptReportsThroughThePipelineWhenRemote(t *testing.T) {
	vm := vmMachine{Name: "web01", CPUs: 2, MemoryMB: 4096, Firmware: "efi", Disks: []vmDisk{{Label: "Hard disk 1"}}}
	plan, err := vmHyperVPlanFor(vm, nil, vmHyperVOptions{Disks: []string{`C:\ClusterStorage\Volume1\VMs\web01\web01-disk0.vhdx`}, ClusterRole: true})
	if err != nil {
		t.Fatal(err)
	}
	local := plan.Script()
	if !strings.Contains(local, "[Console]::Out.Write($vm.Id.ToString())") || !strings.Contains(local, "[Console]::Out.WriteLine('"+vmClusterRoleAdded+"')") {
		t.Fatalf("local script changed:\n%s", local)
	}
	plan.Remote = true
	remote := plan.Script()
	if strings.Contains(remote, "[Console]::Out") {
		t.Fatalf("the remote script writes to a console nobody sees:\n%s", remote)
	}
	if !strings.Contains(remote, "Write-Output $vm.Id.ToString()") || !strings.Contains(remote, "Write-Output ('"+vmClusterRoleAdded+"')") || !strings.Contains(remote, "Add-ClusterVirtualMachineRole -VMName $name") {
		t.Fatalf("remote script:\n%s", remote)
	}
	if !strings.HasSuffix(strings.TrimSpace(remote), "Write-Output $vm.Id.ToString()") {
		t.Fatal("the ID must be the last output line")
	}
}
