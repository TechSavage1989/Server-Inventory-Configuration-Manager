package main

import "testing"

func TestSubnetMasksAreShownInFullAndReadEitherWay(t *testing.T) {
	shown := map[string]string{
		"192.168.120.11/23":   "192.168.120.11 / 255.255.254.0 (/23)",
		"10.0.0.11/24":        "10.0.0.11 / 255.255.255.0 (/24)",
		"10.0.0.11/32":        "10.0.0.11 / 255.255.255.255 (/32)",
		"10.0.0.11":           "10.0.0.11",
		"fe80::1/64":          "fe80::1/64",
		"not an address/24":   "not an address/24",
		"10.0.0.11/255.0.0.0": "10.0.0.11 / 255.0.0.0 (/8)",
	}
	for in, want := range shown {
		if got := ipMaskText(in); got != want {
			t.Errorf("ipMaskText(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"10.0.20.11/24", "10.0.20.11/255.255.255.0", "10.0.20.11 255.255.255.0", "10.0.20.11 mask 255.255.255.0", "10.0.20.11 / 255.255.255.0 (/24)", " 10.0.20.11 /24 "} {
		ip, prefix, err := parseIPv4WithMask(in)
		if err != nil || ip != "10.0.20.11" || prefix != 24 {
			t.Errorf("parseIPv4WithMask(%q) = %q %d %v", in, ip, prefix, err)
		}
	}
	for _, in := range []string{"10.0.20.11", "10.0.20.11/255.0.255.0", "10.0.20.11/33", "10.0.20.11 / 255.255.255.0 (/16)", "300.0.0.1/24"} {
		if _, _, err := parseIPv4WithMask(in); err == nil {
			t.Errorf("parseIPv4WithMask(%q) accepted it", in)
		}
	}
	if got, err := hvBuildCheckIPv4CIDR("10.0.20.11 255.255.255.0"); err != nil || got != "10.0.20.11/24" {
		t.Errorf("hvBuildCheckIPv4CIDR with a dotted mask = %q %v", got, err)
	}
	if got := ipCIDRText("10.0.0.5", "255.255.252.0"); got != "10.0.0.5/22" {
		t.Errorf("ipCIDRText = %q", got)
	}
	if got := ipCIDRText("fe80::5", "64"); got != "fe80::5/64" {
		t.Errorf("ipCIDRText IPv6 = %q", got)
	}
	if got := hypervAddressList(hypervNICReport{Addresses: []string{"10.0.0.5", "fe80::5"}, Subnets: []string{"255.255.255.0", "/64"}}); len(got) != 2 || got[0] != "10.0.0.5/24" || got[1] != "fe80::5/64" {
		t.Errorf("hypervAddressList = %v", got)
	}
}
