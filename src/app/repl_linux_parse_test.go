package main

import (
	"reflect"
	"testing"
)

func TestReplLinuxParsers(t *testing.T) {
	info := "22 1 8:2 / / rw,relatime shared:1 - ext4 /dev/sda2 rw\n" +
		"30 22 253:0 / /srv/my\\040data rw shared:2 - xfs /dev/mapper/vg0-data rw\n" +
		"31 22 0:5 / /proc rw - proc proc rw\n"
	mounts := replParseMountInfo(info)
	if len(mounts) != 3 || mounts[1].MountPoint != "/srv/my data" || mounts[1].Device != "253:0" || mounts[0].FileSystem != "ext4" || mounts[1].Source != "/dev/mapper/vg0-data" {
		t.Fatalf("mounts %+v", mounts)
	}
	segments, err := replParseDMTable("0 2097152 linear 8:3 2048\n2097152 1024 linear 8:17 384\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []replDMSegment{{0, 2097152 * 512, "8:3", 2048 * 512}, {2097152 * 512, 1024 * 512, "8:17", 384 * 512}}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("segments %+v", segments)
	}
	for _, bad := range []string{"0 100 striped 2 128 8:3 0 8:4 0", "0 100 linear 8:3", "0 100 linear 8:3 0\n200 10 linear 8:3 100", "", "0 100 crypt aes 0 0 8:3 0"} {
		if _, err := replParseDMTable(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	rows, err := replParseLVMRows([]byte(`{"report":[{"lv":[{"vg_name":"vg0","lv_name":"data","lv_size":"1073741824B","lv_attr":"-wi-ao----"}]}]}`))
	if err != nil || len(rows) != 1 || rows[0]["lv_name"] != "data" {
		t.Fatal(rows, err)
	}
	if size, err := replLVMBytes(rows[0]["lv_size"]); err != nil || size != 1<<30 {
		t.Fatal(size, err)
	}
	for name, ok := range map[string]bool{"sda": true, "nvme0n1": true, "vdb": true, "loop7": true, "sda1": false, "dm-0": false, "../sda": false, "md0": false} {
		if replLinuxDevicePattern.MatchString(name) != ok {
			t.Fatalf("%s: %v", name, !ok)
		}
	}
	if name := replSnapshotName("0123456789abcdef", 2); !replSnapshotNamePattern.MatchString(name) {
		t.Fatal(name)
	}
}
