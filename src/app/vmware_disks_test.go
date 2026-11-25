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
