package main

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Subnet masks are shown in full everywhere (owner, 2026-11-28: "provide the
// full subnet mask, and not just the cidr notation"). Values are still kept
// as address/prefix internally; ipMaskText is for anything a person reads
// (pages, reports, runbooks, logs) and parseIPv4WithMask accepts either form
// wherever a mask is typed. index.html's ipMaskText and ipMaskParse must
// give the same results.

// ipv4MaskText returns the dotted mask of an IPv4 prefix length, such as
// 255.255.254.0 for 23, or "" when the length is out of range.
func ipv4MaskText(prefix int) string {
	if prefix < 0 || prefix > 32 {
		return ""
	}
	return net.IP(net.CIDRMask(prefix, 32)).String()
}

// ipv4PrefixOf reads a prefix length typed as 23, /23 or 255.255.254.0.
// A mask whose bits are not contiguous is refused.
func ipv4PrefixOf(text string) (int, error) {
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "/"))
	if text == "" {
		return 0, errors.New("no subnet mask")
	}
	if !strings.Contains(text, ".") {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 || n > 32 {
			return 0, errors.New(strconv.Quote(text) + " is not a subnet mask or prefix length")
		}
		return n, nil
	}
	ip := net.ParseIP(text)
	if ip == nil || ip.To4() == nil {
		return 0, errors.New(strconv.Quote(text) + " is not a subnet mask")
	}
	ones, bits := net.IPMask(ip.To4()).Size()
	if bits == 0 {
		return 0, errors.New(strconv.Quote(text) + " is not a valid subnet mask: its bits are not contiguous")
	}
	return ones, nil
}

var ipMaskInput = regexp.MustCompile(`^(\d{1,3}(?:\.\d{1,3}){3})\s*(?:/\s*|\s+(?:mask\s+)?)(\d{1,3}(?:\.\d{1,3}){3}|/?\d{1,2})(?:\s*\(\s*/?\d{1,2}\s*\))?$`)

// parseIPv4WithMask reads an IPv4 address with its subnet typed in any of
// the usual ways: 10.0.0.11/24, 10.0.0.11/255.255.255.0,
// 10.0.0.11 255.255.255.0, 10.0.0.11 mask 255.255.255.0 or the way this
// application shows it, 10.0.0.11 / 255.255.255.0 (/24). It returns the
// address and prefix length; when both a mask and a (/n) are given they must
// agree.
func parseIPv4WithMask(text string) (string, int, error) {
	text = strings.TrimSpace(text)
	m := ipMaskInput.FindStringSubmatch(strings.ToLower(text))
	if m == nil {
		return "", 0, errors.New(strconv.Quote(text) + " is not an IPv4 address with a subnet mask, such as 10.0.20.11 / 255.255.255.0 or 10.0.20.11/24")
	}
	ip := net.ParseIP(m[1])
	if ip == nil || ip.To4() == nil {
		return "", 0, errors.New(strconv.Quote(text) + " is not an IPv4 address")
	}
	prefix, err := ipv4PrefixOf(m[2])
	if err != nil {
		return "", 0, err
	}
	if open := strings.LastIndex(text, "("); open > 0 && strings.Contains(m[2], ".") {
		if check, err := ipv4PrefixOf(strings.Trim(text[open:], "() ")); err != nil || check != prefix {
			return "", 0, errors.New(strconv.Quote(text) + ": the subnet mask and the prefix length do not match")
		}
	}
	return ip.To4().String(), prefix, nil
}

// ipMaskText shows an address/prefix with its full subnet mask:
// 192.168.120.11/23 becomes 192.168.120.11 / 255.255.254.0 (/23). Text that
// is not an IPv4 address with a prefix (IPv6, a plain address) is returned
// unchanged, because IPv6 has no dotted mask.
func ipMaskText(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.Contains(trimmed, "/") || strings.Contains(trimmed, ":") {
		return text
	}
	ip, prefix, err := parseIPv4WithMask(trimmed)
	if err != nil {
		return text
	}
	return ip + " / " + ipv4MaskText(prefix) + " (/" + strconv.Itoa(prefix) + ")"
}

// ipMaskList applies ipMaskText to every item.
func ipMaskList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, ipMaskText(item))
	}
	return out
}

// ipCIDRText turns an address with a dotted mask (as Windows and Hyper-V
// report them) into address/prefix, and returns the address alone when the
// mask cannot be read.
func ipCIDRText(address, mask string) string {
	address, mask = strings.TrimSpace(address), strings.TrimSpace(mask)
	if mask == "" {
		return address
	}
	if strings.Contains(address, ":") {
		if n, err := strconv.Atoi(strings.TrimPrefix(mask, "/")); err == nil && n >= 0 && n <= 128 {
			return address + "/" + strconv.Itoa(n)
		}
		return address
	}
	prefix, err := ipv4PrefixOf(mask)
	if err != nil {
		return address
	}
	return address + "/" + strconv.Itoa(prefix)
}
