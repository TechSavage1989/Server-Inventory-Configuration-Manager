package main

import (
	"errors"
	"strings"
	"syscall"
	"time"
)

// installRecord is Health\last-update.json. The helper (the downloaded
// version) writes "started" before it changes anything and a final status
// on every exit, so the version that runs next can tell whether the swap
// took. Older versions read only status and message.
type installRecord struct {
	Status  string `json:"status"`           // started, installed, failed, rollback
	Version string `json:"version"`          // the version left installed (older versions show this)
	Message string `json:"message"`          // the reason, in words
	Time    string `json:"time"`             // RFC 3339, UTC
	Target  string `json:"target,omitempty"` // the version being installed
	From    string `json:"from,omitempty"`   // the version being replaced
	Path    string `json:"path,omitempty"`   // the executable being replaced
	Code    int    `json:"code,omitempty"`   // the helper's exit code
	Log     string `json:"log,omitempty"`    // the update log
	Ready   string `json:"ready,omitempty"`  // the verified download, for a manual install
}

// installRecordMaxAge keeps an old failure from being reported forever.
const installRecordMaxAge = 14 * 24 * time.Hour

// installOutcomeNotice says why the last install did not take, with a
// manual fallback, or "" when there is nothing to report. older(a, b)
// reports whether version a is older than version b.
func installOutcomeNotice(record installRecord, running, exePath string, now time.Time, older func(a, b string) bool) string {
	if when, err := time.Parse(time.RFC3339, record.Time); err == nil && now.Sub(when) > installRecordMaxAge {
		return ""
	}
	if record.Target == "" {
		// Written by a version before 3.2.36: only failures carry a message.
		if record.Status == "rollback" || record.Status == "failed" {
			return strings.TrimSpace(record.Message)
		}
		return ""
	}
	if !older(running, record.Target) {
		return "" // the new version, or a later one, is running
	}
	var reason string
	switch record.Status {
	case "failed", "rollback":
		reason = strings.TrimSpace(record.Message)
	case "installed":
		reason = "The installer reported success, but this copy is still " + running + ". Another copy of the application may be running from a different folder."
	default: // started: the helper never wrote a result
		reason = "The installer stopped before it finished. Security software may have blocked or ended it."
	}
	if reason == "" {
		reason = "The installer did not finish."
	}
	text := "Version " + record.Target + " was not installed. " + reason + " " + manualUpdateFallback(exePath, record.Ready)
	if record.Log != "" {
		text += " Details are in " + record.Log + "."
	}
	return text
}

func manualUpdateFallback(exePath, readyPath string) string {
	if exePath == "" {
		exePath = "the application's executable"
	}
	if readyPath != "" {
		return "To install it by hand, close this application and copy " + readyPath + " over " + exePath + ", or download the new version from the release page and replace that file."
	}
	return "To install it by hand, download the new version from the release page, close this application and replace " + exePath + "."
}

// Windows error codes for a file another process holds open. Endpoint
// protection often keeps a just-written executable open while it scans it,
// so a replace that fails with one of these is retried for a short while.
const (
	errorAccessDenied     = syscall.Errno(5)
	errorSharingViolation = syscall.Errno(32)
	errorLockViolation    = syscall.Errno(33)
)

func isTransientFileError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errorAccessDenied || errno == errorSharingViolation || errno == errorLockViolation
}

// retryFileOperation runs op until it succeeds, fails with a lasting error,
// or the budget runs out, sleeping between tries. It returns how many tries
// it made and the last error.
func retryFileOperation(op func() error, budget time.Duration, sleep func(time.Duration)) (int, error) {
	deadline := time.Now().Add(budget)
	wait := 250 * time.Millisecond
	tries := 0
	for {
		tries++
		err := op()
		if err == nil || !isTransientFileError(err) || time.Now().Add(wait).After(deadline) {
			return tries, err
		}
		sleep(wait)
		if wait < 2*time.Second {
			wait *= 2
		}
	}
}

// installFailureReason turns a helper exit code into words for the person.
func installFailureReason(code int, detail string) string {
	reasons := map[int]string{
		4:  "The running copy did not close within the time allowed, so nothing was replaced.",
		5:  "The downloaded update failed its checks, so nothing was replaced.",
		6:  "The downloaded update's checksum did not match, so nothing was replaced.",
		7:  "The update could not be written to the application's folder. The folder may be read-only, or security software may have blocked the file.",
		8:  "The copy written to the application's folder did not match the download, so nothing was replaced. Security software may have changed or removed it.",
		9:  "The rollback folder could not be created, so nothing was replaced.",
		10: "The installed executable could not be copied for rollback because it was locked or unreadable. The installed file was left unchanged.",
		11: "The installed executable could not be replaced because another program held it open or access was denied. The installed file was left unchanged.",
		12: "The updated application could not be started, so the previous version was restored.",
		13: "The update did not start correctly and the previous version could not be restored automatically.",
		14: "After the replace, the installed file did not match the update. Security software may have restored or quarantined it. The previous version was restored.",
		16: "The previous version was restored but could not be started.",
		17: "The update did not confirm a healthy visible startup. The previous version was restored.",
	}
	text, ok := reasons[code]
	if !ok {
		text = "The installer did not finish."
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		text += " (" + detail + ")"
	}
	return text
}
