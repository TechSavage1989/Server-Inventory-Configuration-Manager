package main

import "testing"

func TestCleanOutputRootChoice(t *testing.T) {
	good := map[string]string{
		`C:\Reports`:               `C:\Reports`,
		`  D:\Migration\VMware\  `: `D:\Migration\VMware`,
		`\\files01\share\Reports`:  `\\files01\share\Reports`,
		`E:/Reports/VMware`:        `E:\Reports\VMware`,
	}
	for input, want := range good {
		got, err := cleanOutputRootChoice(input)
		if err != nil || got != want {
			t.Errorf("cleanOutputRootChoice(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, bad := range []string{"", "Reports", `\Reports`, `\\server`, `C:Reports`, `C:\Re|ports`, `C:\a\..\Windows`, `C:\a:b`, "http://example.com/x"} {
		if got, err := cleanOutputRootChoice(bad); err == nil {
			t.Errorf("cleanOutputRootChoice(%q) = %q, want an error", bad, got)
		}
	}
}
