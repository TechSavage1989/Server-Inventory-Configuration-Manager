package main

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// Moving file shares between two other servers (2026-11-27, owner request:
// "Can file shares be migrated from one remote machine to another remote
// machine, or does the target machine have to run the software"). Move file
// shares can run on any admin computer: Invoke-ShareMigrationRemote.ps1 runs
// the file share engine inside the new server through PowerShell remoting,
// so the new server copies straight from the old one with Robocopy (files
// cross the network once, never through the admin computer) and nothing is
// installed on either server. These are the platform-independent rules; the
// handlers live in migrate.go.

const migrateRemoteRunnerName = "Invoke-ShareMigrationRemote.ps1"

// migrateRemoteModes are the engine modes that run inside the new server.
// DfsReport, AliasCheck and Alias run on the admin computer with -TargetName:
// from inside the new server they would need this computer's sign-in passed
// on to a domain controller, which Windows does not do without delegation.
var migrateRemoteModes = map[string]bool{"Discover": true, "Precheck": true, "Sync": true, "Cutover": true, "SourceReadOnly": true, "SourceRestore": true}

var migrateHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$`)

// migrateShortHost is the first label of a host name, upper case.
func migrateShortHost(name string) string {
	return strings.ToUpper(strings.Split(strings.Trim(strings.TrimSpace(name), `\`), ".")[0])
}

// migrateCleanTarget checks the new server's name. An empty name, or this
// computer's own name, means the shares move to this computer ("").
func migrateCleanTarget(target, thisComputer, source string) (string, error) {
	target = strings.Trim(strings.TrimSpace(target), `\`)
	if target == "" {
		return "", nil
	}
	if !migrateHostPattern.MatchString(target) {
		return "", errors.New("Type the new server's name, for example FS02 or fs02.example.com.")
	}
	lower := strings.ToLower(target)
	if lower == "localhost" || lower == "127.0.0.1" || migrateShortHost(target) == migrateShortHost(thisComputer) {
		return "", nil
	}
	if source != "" && migrateShortHost(target) == migrateShortHost(source) {
		return "", errors.New("The new server must be a different server from the old one.")
	}
	return target, nil
}

// migrateWorkKey names a migration's work folder on this computer. A move to
// this computer keeps the name it has always had (the old server), so saved
// migrations still resume; a move to another server adds its name.
func migrateWorkKey(source, target string) string {
	if source == "" {
		return ""
	}
	if target == "" {
		return strings.ToUpper(source)
	}
	return strings.ToUpper(source) + "_TO_" + strings.ToUpper(target)
}

// migrateRemoteWorkName is the folder name the new server keeps its copy logs
// and saved old share permissions in (under ProgramData): letters, digits,
// dots, hyphens and underscores only.
func migrateRemoteWorkName(source string) string {
	var builder strings.Builder
	for _, r := range strings.ToUpper(source) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	name := builder.String()
	if name == "" {
		name = "SERVER"
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

// migrateRemoteFolderProblem says why a folder on the new server must not
// receive a mirrored copy, or "" when it may. The new server's own Windows
// folders are not known here, so its standard locations are refused by path.
func migrateRemoteFolderProblem(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(path), "/", `\`), `\`)
	if len(path) < 2 || path[1] != ':' || !isDriveLetter(path[0]) {
		return "not a folder on a local drive of the new server"
	}
	if len(path) <= 3 {
		return "a drive root"
	}
	for _, part := range strings.Split(path[3:], `\`) {
		if part == ".." || part == "." {
			return "a path with . or .."
		}
	}
	lower := strings.ToLower(path[3:]) + `\`
	reasons := []struct{ prefix, reason string }{
		{`windows\`, "inside the Windows folder"},
		{`program files\`, "inside Program Files"},
		{`program files (x86)\`, "inside Program Files"},
		{`programdata\`, "inside ProgramData"},
		{`users\`, "inside the user profiles folder"},
	}
	for _, item := range reasons {
		if strings.HasPrefix(lower, item.prefix) {
			return item.reason
		}
	}
	return ""
}

// migrateConfirmName is the name a cutover or an overwrite is confirmed
// with: the new server's short name, or this computer's.
func migrateConfirmName(target, thisComputer string) string {
	if target != "" {
		return migrateShortHost(target)
	}
	return thisComputer
}

// migrateConfirmMatches accepts the new server's short or full name.
func migrateConfirmMatches(typed, target, thisComputer string) bool {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return false
	}
	if target == "" {
		return strings.EqualFold(typed, thisComputer)
	}
	return strings.EqualFold(typed, target) || strings.EqualFold(typed, migrateShortHost(target))
}

// Keep copying until cutover: a copy pass every few minutes while this
// application stays open, so the cutover's final pass is short.
const (
	migrateAutoSyncMin = 15
	migrateAutoSyncMax = 1440
)

// migrateCheckAutoSync accepts 0 (off) or 15 to 1440 minutes.
func migrateCheckAutoSync(minutes int) error {
	if minutes == 0 || (minutes >= migrateAutoSyncMin && minutes <= migrateAutoSyncMax) {
		return nil
	}
	return errors.New("Choose a copy interval from 15 minutes to 24 hours, or turn automatic copies off.")
}

// migrateAutoSyncDue reports whether the next automatic copy is due: the
// interval has passed since the last copy ended (or since automatic copies
// were turned on, when no copy has run).
func migrateAutoSyncDue(minutes int, last, now time.Time) bool {
	if minutes <= 0 {
		return false
	}
	return !now.Before(last.Add(time.Duration(minutes) * time.Minute))
}
