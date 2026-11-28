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

// What the old server has, listed by Discover before anything is read or
// copied (2026-11-28, owner: "I don't think forcing all the server roles to
// migrate without choice was ever my intention"; AGENTS rule 76). The
// operator picks from it and Read carries only the picked items.
type smDiscovery struct {
	Computer  string `json:"computer"`
	OSCaption string `json:"osCaption"`
	Features  []struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"features"`
	Users []struct {
		Name     string `json:"name"`
		FullName string `json:"fullName"`
		Disabled bool   `json:"disabled"`
	} `json:"users"`
	Groups []struct {
		Name    string `json:"name"`
		BuiltIn bool   `json:"builtIn"`
		Members int    `json:"members"`
	} `json:"groups"`
	Shares []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"shares"`
	IIS struct {
		Installed bool `json:"installed"`
		Sites     []struct {
			Name     string   `json:"name"`
			Bindings []string `json:"bindings"`
		} `json:"sites"`
		AppPools int `json:"appPools"`
		Roots    []struct {
			Path      string `json:"path"`
			SizeBytes int64  `json:"sizeBytes"`
			Reason    string `json:"reason"`
		} `json:"roots"`
	} `json:"iis"`
	Certificates []struct {
		Thumbprint   string   `json:"thumbprint"`
		Subject      string   `json:"subject"`
		FriendlyName string   `json:"friendlyName"`
		NotAfter     string   `json:"notAfter"`
		Exportable   bool     `json:"exportable"`
		UsedBy       []string `json:"usedBy"`
	} `json:"certificates"`
	DNS struct {
		Installed  bool     `json:"installed"`
		Forwarders []string `json:"forwarders"`
		Zones      []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"zones"`
	} `json:"dns"`
	DHCP struct {
		Installed  bool `json:"installed"`
		Authorized bool `json:"authorized"`
	} `json:"dhcp"`
	Warnings []string `json:"warnings"`
}

// smSelection is what the operator picked to move. Names that the old
// server did not list are dropped by smCleanSelection.
type smSelection struct {
	Features      []string `json:"features"`
	IIS           bool     `json:"iis"`
	WebRoots      []string `json:"webRoots"`
	Certificates  []string `json:"certificates"`
	Users         []string `json:"users"`
	Groups        []string `json:"groups"`
	Shares        []string `json:"shares"`
	DNSForwarders bool     `json:"dnsForwarders"`
	DNSZones      []string `json:"dnsZones"`
	DHCP          bool     `json:"dhcp"`
}

// Count is how many items are picked.
func (s smSelection) Count() int {
	n := len(s.Features) + len(s.WebRoots) + len(s.Certificates) + len(s.Users) + len(s.Groups) + len(s.Shares) + len(s.DNSZones)
	for _, on := range []bool{s.IIS, s.DNSForwarders, s.DHCP} {
		if on {
			n++
		}
	}
	return n
}

// smParseDiscovery reads the bundle engine's list from a Discover reply.
func smParseDiscovery(reply smRunnerReply) (*smDiscovery, error) {
	var summary struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		smDiscovery
	}
	if strings.TrimSpace(reply.Result) == "" {
		return nil, errors.New("the old server did not list what it has")
	}
	if err := json.Unmarshal(stripUTF8BOMBytes([]byte(reply.Result)), &summary); err != nil {
		return nil, fmt.Errorf("the old server's list was invalid: %v", err)
	}
	if summary.Status != "succeeded" {
		message := summary.Message
		if message == "" {
			message = "the old server could not be listed"
		}
		return nil, errors.New(message)
	}
	d := summary.smDiscovery
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	return &d, nil
}

// smPick keeps the names in wanted that are in known (any case), spelled as
// known has them, once each.
func smPick(wanted, known []string) []string {
	index := map[string]string{}
	for _, name := range known {
		index[strings.ToLower(name)] = name
	}
	seen := map[string]bool{}
	out := []string{}
	for _, name := range wanted {
		key := strings.ToLower(strings.TrimSpace(name))
		if real, ok := index[key]; ok && !seen[key] {
			seen[key] = true
			out = append(out, real)
		}
	}
	return out
}

// smCleanSelection checks a selection against what the old server listed
// and the website files limit, and returns the bytes of website files it
// copies. It refuses an empty selection and website folders without IIS.
func smCleanSelection(s smSelection, d *smDiscovery, limitGB int) (smSelection, int64, error) {
	if d == nil {
		return smSelection{}, 0, errors.New("Look at the old server first, then pick what to move.")
	}
	var names []string
	out := smSelection{}
	for _, f := range d.Features {
		names = append(names, f.Name)
	}
	out.Features = smPick(s.Features, names)
	names = names[:0]
	for _, u := range d.Users {
		names = append(names, u.Name)
	}
	out.Users = smPick(s.Users, names)
	names = names[:0]
	for _, g := range d.Groups {
		names = append(names, g.Name)
	}
	out.Groups = smPick(s.Groups, names)
	names = names[:0]
	for _, share := range d.Shares {
		names = append(names, share.Name)
	}
	out.Shares = smPick(s.Shares, names)
	names = names[:0]
	for _, c := range d.Certificates {
		if c.Exportable {
			names = append(names, c.Thumbprint)
		}
	}
	out.Certificates = smPick(s.Certificates, names)
	names = names[:0]
	for _, z := range d.DNS.Zones {
		names = append(names, z.Name)
	}
	out.DNSZones = smPick(s.DNSZones, names)
	out.DNSForwarders = s.DNSForwarders && len(d.DNS.Forwarders) > 0
	out.DHCP = s.DHCP && d.DHCP.Authorized
	out.IIS = s.IIS && d.IIS.Installed
	var size int64
	if len(s.WebRoots) > 0 {
		if !out.IIS {
			return smSelection{}, 0, errors.New("Website files go with IIS sites and app pools: pick IIS too, or untick the website folders.")
		}
		names = names[:0]
		sizes := map[string]int64{}
		for _, r := range d.IIS.Roots {
			if r.Reason == "" {
				names = append(names, r.Path)
				sizes[strings.ToLower(r.Path)] = r.SizeBytes
			}
		}
		out.WebRoots = smPick(s.WebRoots, names)
		for _, root := range out.WebRoots {
			size += sizes[strings.ToLower(root)]
		}
		if limit := int64(smCheckLimit(limitGB)) << 30; size > limit {
			return smSelection{}, 0, fmt.Errorf("The website folders you picked hold %.1f GB, more than the %d GB limit. Raise the limit or pick fewer folders.", float64(size)/float64(1<<30), smCheckLimit(limitGB))
		}
	}
	if out.Count() == 0 {
		return smSelection{}, 0, errors.New("Pick at least one thing to move.")
	}
	return out, size, nil
}
