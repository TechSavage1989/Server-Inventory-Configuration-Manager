package main

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Browse buttons for path fields (2026-11-26, user-requested): every
// save-to or location field of a migration or conversion tool opens the
// native Windows picker through /api/pick. A field that names a path on a
// remote Hyper-V host opens the picker on that host's administrative share
// (\\host\D$) with this computer's Windows account and gets the local path
// on the host back (D:\...). Picking a path changes nothing anywhere.

var pickHostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,62})(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,62}))*$`)

// pickTitle keeps a caller's dialog text printable and short.
func pickTitle(title, fallback string) string {
	clean := strings.Map(func(r rune) rune {
		if r == 0 || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)
	clean = strings.Join(strings.Fields(clean), " ")
	if clean == "" {
		clean = fallback
	}
	if runes := []rune(clean); len(runes) > 240 {
		clean = string(runes[:240])
	}
	return clean
}

// pickHost checks a remote host name: a DNS or NetBIOS name only, never an
// address range, a path or a share.
func pickHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" || len(host) > 253 || !pickHostPattern.MatchString(host) {
		return "", errors.New("connect to the Hyper-V host first, then Browse its folders")
	}
	return host, nil
}

// pickRemoteStart turns a path on the host (D:\Hyper-V\VM) into the
// administrative share path this computer opens (\\host\D$\Hyper-V\VM).
// Anything that is not a full drive path starts at the host's C: drive.
func pickRemoteStart(host, current string) (root, start string) {
	drive := "C"
	rest := ""
	current = strings.TrimSpace(current)
	if len(current) >= 2 && current[1] == ':' && isDriveLetter(current[0]) {
		drive = strings.ToUpper(current[:1])
		rest = strings.Trim(current[2:], `\/`)
		if strings.Contains(rest, "..") {
			rest = ""
		}
	}
	root = `\\` + host + `\` + drive + `$`
	start = root
	if rest != "" {
		start = root + `\` + strings.ReplaceAll(rest, "/", `\`)
	}
	return root, start
}

// pickRemoteLocal maps a folder chosen on \\host\X$\... back to X:\... on the
// host. A folder anywhere else (another computer, a normal share) is refused.
func pickRemoteLocal(host, selected string) (string, error) {
	clean := strings.TrimRight(strings.ReplaceAll(selected, "/", `\`), `\`)
	prefix := `\\` + strings.ToLower(host) + `\`
	lower := strings.ToLower(clean)
	if !strings.HasPrefix(lower, prefix) {
		return "", errors.New("choose a folder on " + host + "'s drives (its C$, D$ ... administrative shares)")
	}
	tail := clean[len(prefix):]
	if len(tail) < 2 || tail[1] != '$' || !isDriveLetter(tail[0]) || (len(tail) > 2 && tail[2] != '\\') {
		return "", errors.New("choose a folder on " + host + "'s drives (its C$, D$ ... administrative shares)")
	}
	local := strings.ToUpper(tail[:1]) + `:\` + strings.TrimLeft(tail[2:], `\`)
	return filepath.Clean(local), nil
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// pickFilter returns the comdlg32 filter for a file field.
func pickFilter(kind string) string {
	switch kind {
	case "iso":
		return "ISO images (*.iso)\x00*.iso\x00All files (*.*)\x00*.*\x00\x00"
	}
	return "All files (*.*)\x00*.*\x00\x00"
}
