package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testOlder(a, b string) bool {
	parse := func(v string) (x [3]int) { fmt.Sscanf(v, "%d.%d.%d", &x[0], &x[1], &x[2]); return }
	pa, pb := parse(a), parse(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func TestInstallOutcomeNoticeExplainsEveryFailedSwap(t *testing.T) {
	now := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	stamp := now.Add(-time.Minute).Format(time.RFC3339)
	exe := `C:\Tools\Server-Inventory-Configuration-Manager.exe`
	ready := `C:\Users\op\AppData\Local\Server Inventory Configuration Manager\Updates\Ready\Server-Inventory-Configuration-Manager.exe`
	cases := []struct {
		name   string
		record installRecord
		want   []string
	}{
		{"replace refused", installRecord{Status: "failed", Target: "3.2.36", From: "3.2.35", Time: stamp, Message: installFailureReason(11, "Access is denied."), Ready: ready, Log: `C:\x\Updates.log`},
			[]string{"Version 3.2.36 was not installed.", "could not be replaced", "Access is denied.", "copy " + ready + " over " + exe, `C:\x\Updates.log`}},
		{"helper ended from outside", installRecord{Status: "started", Target: "3.2.36", Time: stamp},
			[]string{"stopped before it finished", "Security software", "download the new version", exe}},
		{"rolled back", installRecord{Status: "rollback", Target: "3.2.36", Time: stamp, Message: installFailureReason(17, "")},
			[]string{"healthy visible startup", "previous version was restored"}},
		{"reported success but old copy runs", installRecord{Status: "installed", Target: "3.2.36", Time: stamp},
			[]string{"still 3.2.35", "different folder"}},
	}
	for _, c := range cases {
		got := installOutcomeNotice(c.record, "3.2.35", exe, now, testOlder)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: notice %q lacks %q", c.name, got, w)
			}
		}
	}
	for name, r := range map[string]installRecord{
		"installed":         {Status: "installed", Target: "3.2.35", Time: stamp},
		"started but newer": {Status: "started", Target: "3.2.35", Time: stamp},
		"failed then later": {Status: "failed", Target: "3.2.34", Time: stamp, Message: "x"},
		"too old":           {Status: "failed", Target: "3.2.36", Time: now.Add(-15 * 24 * time.Hour).Format(time.RFC3339), Message: "x"},
		"old success":       {Status: "installed", Version: "3.2.30", Time: stamp},
	} {
		if got := installOutcomeNotice(r, "3.2.35", exe, now, testOlder); got != "" {
			t.Errorf("%s: expected no notice, got %q", name, got)
		}
	}
	// A record written by an older helper keeps its message.
	if got := installOutcomeNotice(installRecord{Status: "rollback", Version: "3.2.34", Message: "restored", Time: stamp}, "3.2.34", exe, now, testOlder); got != "restored" {
		t.Errorf("legacy record: %q", got)
	}
}

func TestRetryFileOperationRetriesOnlyLocks(t *testing.T) {
	noSleep := func(time.Duration) {}
	locked := &os.LinkError{Op: "rename", Err: syscall.Errno(32)}
	calls := 0
	tries, err := retryFileOperation(func() error {
		calls++
		if calls < 3 {
			return locked
		}
		return nil
	}, time.Minute, noSleep)
	if err != nil || tries != 3 {
		t.Fatalf("lock that clears: err=%v tries=%d", err, tries)
	}
	tries, err = retryFileOperation(func() error { return errors.New("disk full") }, time.Minute, noSleep)
	if err == nil || tries != 1 {
		t.Fatalf("lasting error retried: tries=%d", tries)
	}
	tries, err = retryFileOperation(func() error { return locked }, 600*time.Millisecond, func(d time.Duration) { time.Sleep(d) })
	if !isTransientFileError(err) || tries < 2 {
		t.Fatalf("lock that never clears: err=%v tries=%d", err, tries)
	}
}

func TestInstallFailureReasonsAreUnique(t *testing.T) {
	seen := map[string]int{}
	for _, code := range []int{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 16, 17} {
		r := installFailureReason(code, "")
		if r == installFailureReason(99, "") {
			t.Errorf("code %d has no reason", code)
		}
		if other, ok := seen[r]; ok {
			t.Errorf("codes %d and %d share a reason", code, other)
		}
		seen[r] = code
	}
}
