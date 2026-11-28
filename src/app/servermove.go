package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// Moving a server live (2026-11-28, owner: "yes, build the full IIS move";
// AGENTS rule 75). The Restore tab's "Another server, live" reads the old
// server through PowerShell remoting into a package on that server (the
// migration bundle with the IIS configuration backup and the website files),
// copies the package to the new server with Robocopy over the administrative
// shares, and then checks and restores it on the new server (or on this
// computer) with the ordinary restore engine. The bundle passphrase is made
// here for each read and lives only in memory. These rules are untagged so
// they are tested on every platform; the Windows side is servermove_windows.go.

const (
	smRunnerName = "Invoke-ServerMoveRemote.ps1"
	// smPassphraseLength: random bundle passphrase made for each read.
	smPassphraseLength = 40
	smDefaultLimitGB   = 10
)

var smServerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,252}$`)

// smAccount is one server's account: empty User means this Windows account.
type smAccount struct {
	User     string
	Password string
}

// smReadResult is what Read reports from the old server's bundle engine.
type smReadResult struct {
	Features             int      `json:"features"`
	LocalUsers           int      `json:"localUsers"`
	LocalGroups          int      `json:"localGroups"`
	Shares               int      `json:"shares"`
	IISSites             int      `json:"iisSites"`
	SiteNames            []string `json:"siteNames"`
	CertificatesInBundle int      `json:"certificatesInBundle"`
	CertificatesLeftOut  int      `json:"certificatesLeftOut"`
	IISPasswords         int      `json:"iisPasswords"`
	WebContentFolders    int      `json:"webContentFolders"`
	Warnings             []string `json:"warnings"`
	SizeBytes            int64    `json:"sizeBytes"`
}

// smCleanServer checks a server name typed by the operator.
func smCleanServer(value, which string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, `\\`)
	if value == "" {
		return "", fmt.Errorf("Type the %s server's name.", which)
	}
	if !smServerPattern.MatchString(value) {
		return "", fmt.Errorf("The %s server's name is not valid. Type its computer or DNS name.", which)
	}
	return value, nil
}

// smShortName is a server's computer name without its DNS suffix, upper case.
func smShortName(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '.'); index > 0 && !smLooksLikeIP(value) {
		value = value[:index]
	}
	return strings.ToUpper(value)
}

func smLooksLikeIP(value string) bool {
	return regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`).MatchString(value)
}

// smSameServer says whether two names are the same server (short or full name).
func smSameServer(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && b != "" && (strings.EqualFold(a, b) || strings.EqualFold(smShortName(a), smShortName(b)))
}

// smWorkName names the move's folder on both servers: OLD_TO_NEW.
func smWorkName(old, new string) string {
	clean := func(value string) string {
		var b strings.Builder
		for _, r := range strings.ToUpper(smShortName(value)) {
			if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return clean(old) + "_TO_" + clean(new)
}

// smPackageName is the package folder's name. The restore engine reads the
// source computer from a name shaped like a collection's.
func smPackageName(oldComputer string, at time.Time) string {
	name := smShortName(oldComputer)
	if name == "" || len(name) > 63 || !regexp.MustCompile(`^[A-Z0-9.\-]+$`).MatchString(name) {
		name = "SERVER"
	}
	return name + "_ServerInventory_" + at.Format("20060102_150405")
}

// smAdminPath gives the administrative-share path of a folder on a server:
// C:\ProgramData\X on OLD is \\OLD\C$\ProgramData\X.
func smAdminPath(server, local string) (string, error) {
	server = strings.TrimSpace(server)
	if !smServerPattern.MatchString(server) {
		return "", errors.New("the server's name is not valid")
	}
	clean := strings.ReplaceAll(strings.TrimSpace(local), "/", `\`)
	if len(clean) < 3 || clean[1] != ':' || clean[2] != '\\' || !(clean[0] >= 'A' && clean[0] <= 'Z' || clean[0] >= 'a' && clean[0] <= 'z') {
		return "", fmt.Errorf("%s is not a full drive path", local)
	}
	for _, part := range strings.Split(clean[3:], `\`) {
		if part == ".." {
			return "", fmt.Errorf("%s is not a plain path", local)
		}
	}
	if !strings.Contains(strings.ToLower(clean), `\server inventory and migration manager\server-move\`) {
		return "", fmt.Errorf("%s is not a server move folder", local)
	}
	return `\\` + server + `\` + strings.ToUpper(clean[:1]) + `$` + clean[2:], nil
}

// smRobocopyArgs copies the package from the old server to the new one:
// a mirror of this move's own package folder, so a second copy sends only
// what changed. /MIR only ever runs into the move's own Package folder.
func smRobocopyArgs(source, destination string) []string {
	return []string{source, destination, "/MIR", "/COPY:DAT", "/DCOPY:T", "/XJ", "/R:2", "/W:5", "/MT:8", "/NP", "/NDL", "/NFL", "/NJH"}
}

// smRobocopyOK: Robocopy exit codes below 8 mean every file was copied.
func smRobocopyOK(code int) bool { return code >= 0 && code < 8 }

// smNewPassphrase makes the random passphrase that seals the bundle's
// secrets for one read. It never leaves this application's memory except to
// the two engines, inside the encrypted remoting session.
func smNewPassphrase() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	var b strings.Builder
	limit := big.NewInt(int64(len(alphabet)))
	for b.Len() < smPassphraseLength {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String(), nil
}

// smRunnerInput is the one JSON line the runner reads.
func smRunnerInput(account smAccount, passphrase string, passwords map[string]string) []byte {
	if passwords == nil {
		passwords = map[string]string{}
	}
	data, _ := json.Marshal(map[string]any{"user": account.User, "password": account.Password, "passphrase": passphrase, "passwords": passwords})
	return append(data, '\n')
}

// smRunnerReply is what the runner writes to its result file.
type smRunnerReply struct {
	ExitCode  int    `json:"exitCode"`
	Result    string `json:"result"`
	Package   string `json:"package"`
	SizeBytes int64  `json:"sizeBytes"`
	Rollback  string `json:"rollback"`
	Work      string `json:"work"`
	Computer  string `json:"computer"`
	// Prepare
	IIS bool `json:"iis"`
	// StopSites, StartSites
	Changed []string `json:"changed"`
	Failed  []string `json:"failed"`
}

// smParseRead reads the bundle engine's summary from a Read reply.
func smParseRead(reply smRunnerReply) (*smReadResult, error) {
	var summary struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		smReadResult
	}
	if strings.TrimSpace(reply.Result) == "" {
		return nil, errors.New("the old server's read did not produce a result")
	}
	if err := json.Unmarshal(stripUTF8BOMBytes([]byte(reply.Result)), &summary); err != nil {
		return nil, fmt.Errorf("the old server's read result was invalid: %v", err)
	}
	if summary.Status != "succeeded" {
		message := summary.Message
		if message == "" {
			message = "the migration bundle could not be written"
		}
		return nil, errors.New(message)
	}
	result := summary.smReadResult
	result.SizeBytes = reply.SizeBytes
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	result.SiteNames = smSitesFromScan(result.SiteNames)
	return &result, nil
}

// smSitesFromScan lists the IIS site names a scan of the package found, from
// its IIS component, for stopping and starting them on the old server.
func smSitesFromScan(names []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[strings.ToLower(name)] || strings.ContainsAny(name, "\"\r\n") {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out
}

// smCheckLimit keeps the website files limit in the bundle engine's range.
func smCheckLimit(value int) int {
	if value < 1 {
		return smDefaultLimitGB
	}
	if value > 2048 {
		return 2048
	}
	return value
}
