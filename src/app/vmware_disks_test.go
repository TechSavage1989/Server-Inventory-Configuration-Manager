package main

import "testing"

func TestDiskCopyNamesAndAddresses(t *testing.T) {
	for host, want := range map[string]string{
		"APP01":        "APP01-disk0.vhdx",
		"app 01/..\\x": "app-01-..-x-disk0.vhdx",
		"":             "computer-disk0.vhdx",
		"..hidden":     "hidden-disk0.vhdx",
		"münchen-fs01": "m-nchen-fs01-disk0.vhdx",
	} {
		got := vmAgentDiskName(host, 0)
		if got != want || !vmReceiverName.MatchString(got) {
			t.Fatalf("%q: got %q, want %q (and a name the receiver accepts)", host, got, want)
		}
	}
	for input, want := range map[string]string{
		"hv01.example.com":     "hv01.example.com:44380",
		" 192.0.2.40:50000 ":   "192.0.2.40:50000",
		"[2001:db8::40]:44380": "[2001:db8::40]:44380",
		"2001:db8::40":         "[2001:db8::40]:44380",
	} {
		if got, err := vmReceiverAddress(input); err != nil || got != want {
			t.Fatalf("%q: got %q %v, want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "hv01:0", "hv01:70000", "hv01:abc", "user@hv01", `\\hv01\share`} {
		if _, err := vmReceiverAddress(input); err == nil {
			t.Fatalf("%q was accepted", input)
		}
	}
	if port, err := vmReceiverPort(0); err != nil || port != vmReceiverDefaultPort {
		t.Fatalf("default port: %d %v", port, err)
	}
	for _, port := range []int{80, 1023, 65536, -1} {
		if _, err := vmReceiverPort(port); err == nil {
			t.Fatalf("port %d was accepted", port)
		}
	}
}

func TestAgentTargetNamesMapDisksToRawReceiverDisks(t *testing.T) {
	vhdx := func(disk int) string { return vmAgentDiskName("APP01", disk) }
	names, err := vmAgentTargetNames([]int{0, 1, 2}, map[string]string{"0": "disk-1", "1": "disk-0", "2": "vhdx"}, vhdx)
	if err != nil || names[0] != "disk-1" || names[1] != "disk-0" || names[2] != "APP01-disk2.vhdx" {
		t.Fatalf("%v %v", names, err)
	}
	names, err = vmAgentTargetNames([]int{3}, nil, vhdx)
	if err != nil || names[3] != "APP01-disk3.vhdx" {
		t.Fatalf("%v %v", names, err)
	}
	for _, bad := range []map[string]string{{"0": "disk-1", "1": "DISK-1"}, {"0": `..\x`}, {"5": "disk-1"}, {"0": "C:"}} {
		if _, err := vmAgentTargetNames([]int{0, 1}, bad, vhdx); err == nil {
			t.Fatalf("%v was accepted", bad)
		}
	}
}
