package main

import (
	"strings"
	"testing"
)

func TestPickRemotePathsMapToTheHostsDrives(t *testing.T) {
	root, start := pickRemoteStart("hv01.example.com", `D:\Hyper-V\web01`)
	if root != `\\hv01.example.com\D$` || start != `\\hv01.example.com\D$\Hyper-V\web01` {
		t.Fatalf("start %q %q", root, start)
	}
	if _, start := pickRemoteStart("hv01", `..\x`); start != `\\hv01\C$` {
		t.Fatalf("relative path start %q", start)
	}
	if _, start := pickRemoteStart("hv01", `C:\ClusterStorage\..\Windows`); start != `\\hv01\C$` {
		t.Fatalf("dot-dot start %q", start)
	}
	for selected, want := range map[string]string{
		`\\HV01\d$\Hyper-V\web01`:           `D:\Hyper-V\web01`,
		`\\hv01\C$\ClusterStorage\Volume1\`: `C:\ClusterStorage\Volume1`,
		`\\hv01\E$`:                         `E:\`,
	} {
		got, err := pickRemoteLocal("hv01", selected)
		if err != nil || got != want {
			t.Fatalf("%s: %q %v", selected, got, err)
		}
	}
	for _, selected := range []string{`\\hv02\D$\x`, `\\hv01\Share\x`, `\\hv01\DD$\x`, `C:\Temp`, `\\hv01\D$x`} {
		if _, err := pickRemoteLocal("hv01", selected); err == nil {
			t.Fatalf("%s was accepted", selected)
		}
	}
}

func TestPickRefusesHostsThatAreNotNames(t *testing.T) {
	for _, host := range []string{"", `hv01\D$`, "192.0.2.0/24", "hv01 hv02", "-hv01", strings.Repeat("a", 64)} {
		if _, err := pickHost(host); err == nil {
			t.Fatalf("%q was accepted", host)
		}
	}
	for _, host := range []string{"hv01", "hv01.example.com", "192.0.2.10"} {
		if _, err := pickHost(host); err != nil {
			t.Fatalf("%q: %v", host, err)
		}
	}
}

func TestPickTitleIsPrintableAndShort(t *testing.T) {
	if got := pickTitle("a\x00b\r\nc", "x"); got != "a b c" {
		t.Fatalf("%q", got)
	}
	if got := pickTitle("  ", "fallback"); got != "fallback" {
		t.Fatalf("%q", got)
	}
	if got := pickTitle(strings.Repeat("é", 500), "x"); len([]rune(got)) != 240 {
		t.Fatalf("length %d", len([]rune(got)))
	}
}
