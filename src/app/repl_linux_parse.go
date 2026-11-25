package main

// Parsers for the Linux agent: kept untagged so they are tested on every
// platform. They read /proc/self/mountinfo, device-mapper tables and LVM's
// JSON reports.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// replLinuxDevicePattern accepts the whole-disk names the Linux agent
// copies: sda, vdb, xvdc, nvme0n1, mmcblk0 and loop devices (tests).
var replLinuxDevicePattern = regexp.MustCompile(`^(sd[a-z]{1,3}|vd[a-z]{1,3}|xvd[a-z]{1,3}|hd[a-z]{1,3}|nvme[0-9]{1,3}n[0-9]{1,3}|mmcblk[0-9]{1,3}|loop[0-9]{1,4})$`)

// replLinuxMount is one line of /proc/self/mountinfo.
type replLinuxMount struct {
	Device     string // major:minor
	MountPoint string
	FileSystem string
	Source     string
}

// replParseMountInfo reads /proc/self/mountinfo.
func replParseMountInfo(text string) []replLinuxMount {
	mounts := []replLinuxMount{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if len(fields) < 5 || separator < 6 || separator+2 >= len(fields) {
			continue
		}
		mounts = append(mounts, replLinuxMount{Device: fields[2], MountPoint: replUnescapeMount(fields[4]), FileSystem: fields[separator+1], Source: replUnescapeMount(fields[separator+2])})
	}
	return mounts
}

// replUnescapeMount undoes mountinfo's octal escapes (\040 for a space).
func replUnescapeMount(text string) string {
	if !strings.Contains(text, `\`) {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+3 < len(text) {
			if value, err := strconv.ParseUint(text[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(value))
				i += 3
				continue
			}
		}
		out.WriteByte(text[i])
	}
	return out.String()
}

// replDMSegment is one linear segment of a device-mapper table, in bytes.
type replDMSegment struct {
	Start  int64  // offset in the mapped device
	Length int64  //
	Device string // major:minor of the device it maps to
	Offset int64  // offset in that device
}

// replParseDMTable reads `dmsetup table` output. Only linear targets are
// accepted; anything else (striped, mirror, raid, thin, snapshot, crypt)
// is refused with its name.
func replParseDMTable(text string) ([]replDMSegment, error) {
	segments := []replDMSegment{}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 3 {
			return nil, fmt.Errorf("unexpected device-mapper line %q", line)
		}
		if fields[2] != "linear" {
			return nil, fmt.Errorf("it uses a %s mapping; only linear logical volumes can be copied", fields[2])
		}
		if len(fields) != 5 {
			return nil, fmt.Errorf("unexpected linear line %q", line)
		}
		start, err1 := strconv.ParseInt(fields[0], 10, 64)
		length, err2 := strconv.ParseInt(fields[1], 10, 64)
		offset, err3 := strconv.ParseInt(fields[4], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || start < 0 || length <= 0 || offset < 0 || !regexp.MustCompile(`^\d+:\d+$`).MatchString(fields[3]) {
			return nil, fmt.Errorf("unexpected linear line %q", line)
		}
		segments = append(segments, replDMSegment{Start: start * 512, Length: length * 512, Device: fields[3], Offset: offset * 512})
	}
	if len(segments) == 0 {
		return nil, errors.New("the device has no mapping")
	}
	at := int64(0)
	for _, segment := range segments {
		if segment.Start != at {
			return nil, errors.New("the mapping has a gap")
		}
		at += segment.Length
	}
	return segments, nil
}

// replLVMReport is the part of `lvs`/`pvs --reportformat json` read here.
type replLVMReport struct {
	Report []struct {
		LV []map[string]string `json:"lv"`
		PV []map[string]string `json:"pv"`
		VG []map[string]string `json:"vg"`
	} `json:"report"`
}

// replParseLVMRows returns the rows of the first report section that has
// any, whichever kind (lv, pv, vg).
func replParseLVMRows(raw []byte) ([]map[string]string, error) {
	var report replLVMReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("unexpected LVM report: %w", err)
	}
	rows := []map[string]string{}
	for _, section := range report.Report {
		rows = append(rows, section.LV...)
		rows = append(rows, section.PV...)
		rows = append(rows, section.VG...)
	}
	return rows, nil
}

// replLVMBytes reads an LVM size printed with --units b --nosuffix.
func replLVMBytes(text string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(text), "B"), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("unexpected LVM size %q", text)
	}
	return value, nil
}

// replSnapshotName is the name of this agent's snapshot of one logical
// volume during a job's round.
func replSnapshotName(job string, index int) string {
	return fmt.Sprintf("simmsnap_%s_%d", job, index)
}

var replSnapshotNamePattern = regexp.MustCompile(`^simmsnap_[a-f0-9]{16}_[0-9]+$`)
