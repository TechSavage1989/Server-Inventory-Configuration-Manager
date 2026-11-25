//go:build linux

package main

// The Linux agent's platform. Whole disks are read from /dev; each LVM
// logical volume on them is read from its own LVM snapshot taken for the
// round, laid over the disk through its device-mapper segments. The LVM
// metadata areas are read before the snapshots exist, so the copy never
// records them. Partitions that are not on LVM have no snapshot: they are
// read as they are, which needs allowLive, except /boot and /boot/efi,
// which change only when the system is updated.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type replLinuxPlatform struct{}

func (replLinuxPlatform) Name() string { return "Linux" }

func replSysRead(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

type replLinuxPartition struct {
	Name   string
	Device string // major:minor
	Start  int64
	Size   int64
}

type replLinuxDisk struct {
	Name       string
	Device     string
	Size       int64
	Model      string
	Partitions []replLinuxPartition
}

func replLinuxReadDisk(name string) (replLinuxDisk, error) {
	if !replLinuxDevicePattern.MatchString(name) {
		return replLinuxDisk{}, fmt.Errorf("%q is not a disk name", name)
	}
	base := filepath.Join("/sys/block", name)
	sectors, err := strconv.ParseInt(replSysRead(filepath.Join(base, "size")), 10, 64)
	if err != nil {
		return replLinuxDisk{}, fmt.Errorf("disk %s not found", name)
	}
	disk := replLinuxDisk{Name: name, Device: replSysRead(filepath.Join(base, "dev")), Size: sectors * 512, Model: replSysRead(filepath.Join(base, "device", "model"))}
	entries, _ := os.ReadDir(base)
	for _, entry := range entries {
		dir := filepath.Join(base, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "partition")); err != nil {
			continue
		}
		start, err1 := strconv.ParseInt(replSysRead(filepath.Join(dir, "start")), 10, 64)
		size, err2 := strconv.ParseInt(replSysRead(filepath.Join(dir, "size")), 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		disk.Partitions = append(disk.Partitions, replLinuxPartition{Name: entry.Name(), Device: replSysRead(filepath.Join(dir, "dev")), Start: start * 512, Size: size * 512})
	}
	sort.Slice(disk.Partitions, func(a, b int) bool { return disk.Partitions[a].Start < disk.Partitions[b].Start })
	return disk, nil
}

func replLinuxDiskNames() []string {
	entries, _ := os.ReadDir("/sys/block")
	names := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !replLinuxDevicePattern.MatchString(name) {
			continue
		}
		if strings.HasPrefix(name, "loop") && os.Getenv("SIMM_AGENT_LOOP") != "1" {
			continue
		}
		if replSysRead(filepath.Join("/sys/block", name, "size")) == "0" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func replLinuxMounts() []replLinuxMount {
	return replParseMountInfo(replSysRead("/proc/self/mountinfo"))
}

func replExec(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LVM_SUPPRESS_FD_WARNINGS=1")
	out, err := command.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, text)
	}
	return text, nil
}

func replHaveLVM() bool {
	_, err := exec.LookPath("lvs")
	return err == nil
}

// replLinuxLV is an active linear logical volume and where it lies.
type replLinuxLV struct {
	VG, LV   string
	Size     int64
	Segments []replDMSegment
	Mounts   []string
	FS       string
	Device   string // major:minor of the LV itself
}

// replDMTable reads a device-mapper table, following a snapshot-origin to
// the real device underneath.
func replDMTable(device string) ([]replDMSegment, error) {
	parts := strings.SplitN(device, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("%q is not a device number", device)
	}
	for depth := 0; depth < 3; depth++ {
		text, err := replExec("dmsetup", "table", "-j", parts[0], "-m", parts[1])
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(text)
		if len(fields) == 4 && fields[2] == "snapshot-origin" && !strings.Contains(text, "\n") {
			parts = strings.SplitN(fields[3], ":", 2)
			continue
		}
		return replParseDMTable(text)
	}
	return nil, errors.New("too many layers of device-mapper")
}

func replLinuxLVs() ([]replLinuxLV, error) {
	if !replHaveLVM() {
		return nil, nil
	}
	out, err := replExec("lvs", "--reportformat", "json", "--units", "b", "--nosuffix", "-o", "vg_name,lv_name,lv_size,lv_attr,lv_kernel_major,lv_kernel_minor")
	if err != nil {
		return nil, err
	}
	rows, err := replParseLVMRows([]byte(out))
	if err != nil {
		return nil, err
	}
	mounts := replLinuxMounts()
	list := []replLinuxLV{}
	for _, row := range rows {
		attr := row["lv_attr"]
		if len(attr) < 5 || attr[4] != 'a' {
			continue // not active
		}
		size, err := replLVMBytes(row["lv_size"])
		if err != nil {
			return nil, err
		}
		lv := replLinuxLV{VG: row["vg_name"], LV: row["lv_name"], Size: size, Device: strings.TrimSpace(row["lv_kernel_major"]) + ":" + strings.TrimSpace(row["lv_kernel_minor"])}
		switch attr[0] {
		case '-', 'o':
		default:
			// Snapshots, thin pools and volumes, RAID and mirror images
			// are listed so they can be refused when on a copied disk.
			lv.Segments = nil
			lv.FS = "unsupported:" + string(attr[0])
			list = append(list, lv)
			continue
		}
		segments, err := replDMTable(lv.Device)
		if err != nil {
			lv.FS = "unsupported:" + err.Error()
			list = append(list, lv)
			continue
		}
		lv.Segments = segments
		for _, mount := range mounts {
			if mount.Device == lv.Device {
				lv.Mounts = append(lv.Mounts, mount.MountPoint)
				lv.FS = mount.FileSystem
			}
		}
		list = append(list, lv)
	}
	return list, nil
}

// replLinuxLocate finds the disk and byte offset of a device offset when
// the device is a whole disk or a partition of one of the disks.
func replLinuxLocate(disks map[string]replLinuxDisk, device string, offset int64) (string, int64, bool) {
	for name, disk := range disks {
		if disk.Device == device {
			return name, offset, true
		}
		for _, partition := range disk.Partitions {
			if partition.Device == device {
				return name, partition.Start + offset, true
			}
		}
	}
	return "", 0, false
}

func (replLinuxPlatform) ListDisks() ([]replDiskInfo, error) {
	mounts := replLinuxMounts()
	lvs, _ := replLinuxLVs()
	rootDevice := ""
	for _, mount := range mounts {
		if mount.MountPoint == "/" {
			rootDevice = mount.Device
		}
	}
	list := []replDiskInfo{}
	for _, name := range replLinuxDiskNames() {
		disk, err := replLinuxReadDisk(name)
		if err != nil {
			continue
		}
		info := replDiskInfo{ID: name, Size: disk.Size, Model: disk.Model}
		single := map[string]replLinuxDisk{name: disk}
		for _, partition := range disk.Partitions {
			volume := replVolumeInfo{Name: "/dev/" + partition.Name, Length: partition.Size, Kind: "simple"}
			for _, mount := range mounts {
				if mount.Device == partition.Device {
					volume.Mounts = append(volume.Mounts, mount.MountPoint)
					volume.FileSystem = mount.FileSystem
				}
			}
			if partition.Device == rootDevice {
				info.System = true
			}
			info.Volumes = append(info.Volumes, volume)
		}
		for _, lv := range lvs {
			on := false
			for _, segment := range lv.Segments {
				if _, _, ok := replLinuxLocate(single, segment.Device, segment.Offset); ok {
					on = true
				}
			}
			if on {
				info.Volumes = append(info.Volumes, replVolumeInfo{Name: "/dev/" + lv.VG + "/" + lv.LV, Mounts: lv.Mounts, FileSystem: lv.FS, Length: lv.Size, Kind: "lvm"})
				if lv.Device == rootDevice {
					info.System = true
				}
			}
		}
		list = append(list, info)
	}
	return list, nil
}

// replLiveAllowed are mount points read as they are without allowLive.
var replLiveAllowed = map[string]bool{"/boot": true, "/boot/efi": true, "/efi": true}

type replFileCloser struct{ file *os.File }

func (c replFileCloser) Close() error { return c.file.Close() }

type replFuncCloser func() error

func (f replFuncCloser) Close() error { return f() }

func (replLinuxPlatform) OpenDisks(ctx context.Context, job string, sources []string, allowLive bool, log func(string)) (*replOpenedDisks, error) {
	if !replJobIDPattern.MatchString(job) {
		return nil, fmt.Errorf("%q is not a job ID", job)
	}
	opened := &replOpenedDisks{Sources: map[string]vmCopySource{}, Snapshot: time.Now()}
	closers := []io.Closer{}
	opened.Close = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i].Close()
		}
		closers = nil
	}
	fail := func(err error) (*replOpenedDisks, error) {
		opened.Close()
		return nil, err
	}
	disks := map[string]replLinuxDisk{}
	for _, name := range sources {
		disk, err := replLinuxReadDisk(name)
		if err != nil {
			return nil, err
		}
		disks[name] = disk
	}
	overlays := map[string][]vmOverlay{}
	covered := map[string]bool{} // partitions (major:minor) read from a snapshot or LVM
	notes := map[string][]string{}
	if replHaveLVM() {
		// Snapshots left by a round that was cut off.
		if out, err := replExec("lvs", "--reportformat", "json", "-o", "vg_name,lv_name"); err == nil {
			if rows, err := replParseLVMRows([]byte(out)); err == nil {
				for _, row := range rows {
					if replSnapshotNamePattern.MatchString(row["lv_name"]) && strings.Contains(row["lv_name"], "_"+job+"_") {
						replExec("lvremove", "-f", row["vg_name"]+"/"+row["lv_name"])
					}
				}
			}
		}
		lvs, err := replLinuxLVs()
		if err != nil {
			return fail(err)
		}
		// Physical volumes: their metadata areas are read now, before
		// any snapshot is added to the metadata.
		out, err := replExec("pvs", "--reportformat", "json", "--units", "b", "--nosuffix", "-o", "pv_name,vg_name,pe_start,pv_size,vg_extent_size,pv_pe_count")
		if err != nil {
			return fail(err)
		}
		rows, err := replParseLVMRows([]byte(out))
		if err != nil {
			return fail(err)
		}
		for _, row := range rows {
			device := replSysRead(filepath.Join("/sys/class/block", filepath.Base(row["pv_name"]), "dev"))
			disk, at, ok := replLinuxLocate(disks, device, 0)
			if !ok {
				continue
			}
			peStart, err1 := replLVMBytes(row["pe_start"])
			pvSize, err2 := replLVMBytes(row["pv_size"])
			extent, err3 := replLVMBytes(row["vg_extent_size"])
			count, err4 := strconv.ParseInt(strings.TrimSpace(row["pv_pe_count"]), 10, 64)
			if err := errors.Join(err1, err2, err3, err4); err != nil {
				return fail(err)
			}
			pv, err := os.Open(row["pv_name"])
			if err != nil {
				return fail(err)
			}
			for _, area := range []vmCopyRange{{Start: 0, Length: peStart}, {Start: peStart + count*extent, Length: pvSize - peStart - count*extent}} {
				area.Length = area.Length / 512 * 512
				if area.Length <= 0 {
					continue
				}
				data := make([]byte, area.Length)
				if _, err := pv.ReadAt(data, area.Start); err != nil {
					pv.Close()
					return fail(fmt.Errorf("reading the LVM metadata of %s: %w", row["pv_name"], err))
				}
				overlays[disk] = append(overlays[disk], vmOverlay{Start: at + area.Start, Length: area.Length, Reader: bytes.NewReader(data), Name: row["pv_name"] + " LVM metadata"})
			}
			pv.Close()
			covered[device] = true
		}
		index := 0
		for _, lv := range lvs {
			onJob, offJob := false, false
			for _, segment := range lv.Segments {
				if _, _, ok := replLinuxLocate(disks, segment.Device, segment.Offset); ok {
					onJob = true
				} else {
					offJob = true
				}
			}
			label := "/dev/" + lv.VG + "/" + lv.LV
			if strings.HasPrefix(lv.FS, "unsupported:") {
				// Only refuse what lies on the copied disks; we cannot
				// see its segments, so check its physical volumes.
				pvsOut, err := replExec("lvs", "--reportformat", "json", "-o", "devices", lv.VG+"/"+lv.LV)
				if err == nil {
					for _, name := range sources {
						if strings.Contains(pvsOut, "/dev/"+name) {
							return fail(fmt.Errorf("the logical volume %s is a kind this agent cannot copy (%s); copy a server without it or convert it to a linear volume", label, strings.TrimPrefix(lv.FS, "unsupported:")))
						}
					}
				}
				continue
			}
			if !onJob {
				continue
			}
			if offJob {
				return fail(fmt.Errorf("the logical volume %s also lies on a disk that is not in this job; add that disk", label))
			}
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			name := replSnapshotName(job, index)
			index++
			size := lv.Size / 5
			if size < 512<<20 {
				size = 512 << 20
			}
			free, _ := replExec("vgs", "--noheadings", "--units", "b", "--nosuffix", "-o", "vg_free", lv.VG)
			if available, err := replLVMBytes(free); err == nil && available < size {
				size = available / (4 << 20) * (4 << 20)
			}
			var reader io.ReaderAt
			if size >= 64<<20 {
				if _, err := replExec("lvcreate", "-s", "-n", name, "-L", strconv.FormatInt(size, 10)+"b", lv.VG+"/"+lv.LV); err == nil {
					vg := lv.VG
					closers = append(closers, replFuncCloser(func() error { _, err := replExec("lvremove", "-f", vg+"/"+name); return err }))
					snapshot, err := os.Open("/dev/" + lv.VG + "/" + name)
					if err != nil {
						return fail(err)
					}
					closers = append(closers, replFileCloser{snapshot})
					reader = snapshot
					log(fmt.Sprintf("Snapshot of %s taken (%s for changes during the round).", label, vmFormatBytes(size)))
				} else {
					log(fmt.Sprintf("No snapshot of %s: %v", label, err))
				}
			}
			if reader == nil {
				if !allowLive {
					return fail(fmt.Errorf("the volume group %s has too little free space for a snapshot of %s; free some space or allow live copies", lv.VG, label))
				}
				log(fmt.Sprintf("Copying %s as it is, without a snapshot.", label))
				continue
			}
			for _, segment := range lv.Segments {
				disk, at, _ := replLinuxLocate(disks, segment.Device, segment.Offset)
				s, start := segment, at
				overlays[disk] = append(overlays[disk], vmOverlay{Start: start, Length: s.Length, Reader: reader, Name: label, VolumeLength: lv.Size, Map: func(diskOffset int64) (int64, int64) {
					delta := diskOffset - start
					return s.Start + delta, s.Length - delta
				}})
			}
			first := sourcesFor(disks, lv.Segments[0].Device)
			notes[first] = append(notes[first], label)
		}
	}
	// Partitions with a mounted file system and no snapshot.
	mounts := replLinuxMounts()
	for _, name := range sources {
		for _, partition := range disks[name].Partitions {
			if covered[partition.Device] {
				continue
			}
			for _, mount := range mounts {
				if mount.Device != partition.Device {
					continue
				}
				if !replLiveAllowed[mount.MountPoint] && !allowLive {
					return fail(fmt.Errorf("%s is mounted at %s and is not on LVM, so it has no snapshot; allow live copies to copy it as it is (the last round at cutover runs after the services stop)", "/dev/"+partition.Name, mount.MountPoint))
				}
				log(fmt.Sprintf("Copying /dev/%s (%s) as it is.", partition.Name, mount.MountPoint))
			}
		}
	}
	host, _ := os.Hostname()
	for _, name := range sources {
		file, err := os.Open("/dev/" + name)
		if err != nil {
			return fail(err)
		}
		source, err := newVMCompositeSource(fmt.Sprintf("%s disk %s", host, name), file, disks[name].Size, overlays[name], []io.Closer{file})
		if err != nil {
			file.Close()
			return fail(err)
		}
		closers = append(closers, source)
		opened.Sources[name] = source
		note := fmt.Sprintf("Disk %s: ", name)
		if len(notes[name]) > 0 {
			note += fmt.Sprintf("%s from LVM snapshots.", strings.Join(notes[name], ", "))
		} else {
			note += "read as it is."
		}
		opened.Notes = append(opened.Notes, note)
	}
	// Flush what the kernel holds, so a partition read as it is is as
	// current as it can be.
	replExec("sync")
	return opened, nil
}

// sourcesFor names the disk holding a device.
func sourcesFor(disks map[string]replLinuxDisk, device string) string {
	name, _, _ := replLinuxLocate(disks, device, 0)
	return name
}

func (replLinuxPlatform) StopServices(names []string, log func(string)) error {
	for _, name := range names {
		if !replServiceNamePattern.MatchString(name) || strings.ContainsAny(name, " ") {
			return fmt.Errorf("%q is not a service name", name)
		}
		if _, err := replExec("systemctl", "stop", name); err != nil {
			return err
		}
		log("Stopped " + name + ".")
	}
	replExec("sync")
	return nil
}

func (replLinuxPlatform) StartServices(names []string, log func(string)) error {
	var first error
	for i := len(names) - 1; i >= 0; i-- {
		if _, err := replExec("systemctl", "start", names[i]); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		log("Started " + names[i] + ".")
	}
	return first
}

func (replLinuxPlatform) Shutdown(log func(string)) error {
	log("Shutting down in one minute.")
	_, err := replExec("shutdown", "-h", "+1", "Replication cutover finished; the new server takes over.")
	return err
}
