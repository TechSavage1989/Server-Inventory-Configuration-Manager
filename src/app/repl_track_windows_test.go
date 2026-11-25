//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// replTestMoveFile moves a file's clusters to lcn with FSCTL_MOVE_FILE, as
// a defragmenter does: the file's data and change journal entries stay as
// they were.
func replTestMoveFile(t *testing.T, letter, path string, lcn, clusters int64) error {
	t.Helper()
	volume, err := replOpenVolume(`\\.\` + letter[:2])
	if err != nil {
		return err
	}
	defer windows.CloseHandle(volume)
	file, err := replOpenAttributes(path)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(file)
	in := make([]byte, 32)
	*(*uintptr)(unsafe.Pointer(&in[0])) = uintptr(file)
	binary.LittleEndian.PutUint64(in[16:], uint64(lcn))
	binary.LittleEndian.PutUint32(in[24:], uint32(clusters))
	var returned uint32
	return windows.DeviceIoControl(volume, 0x00090074 /* FSCTL_MOVE_FILE */, &in[0], uint32(len(in)), nil, 0, &returned, nil)
}

// A tracked round reads far less than the volume holds, yet misses
// nothing: reading the same shadow copies in full afterwards finds no chunk
// that the tracked round left stale. The changes cover the cases change
// tracking must follow: overwrites in place, appends, new, renamed and
// deleted files, alternate data streams, many new files in a folder,
// permission changes and a file moved to other clusters.
func TestReplicationChangeTrackingMissesNothing(t *testing.T) {
	number, letter, detach := replTestNTFSDisk(t, 1<<30)
	defer detach()
	random := func(n int) []byte {
		b := make([]byte, n)
		rand.Read(b)
		return b
	}
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(letter, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(letter, "data", "deep"), 0700)
	write(`data\database.mdf`, random(96<<20))
	write(`data\log.ldf`, random(8<<20))
	write(`data\old.txt`, random(300<<10))
	write(`data\rename-me.bin`, random(2<<20))
	write(`data\deep\keep.bin`, random(64<<10))
	write(`data\victim.bin`, random(8<<20))
	write(`data\mover.bin`, random(8<<20))
	for i := 0; i < 300; i++ {
		write(fmt.Sprintf(`data\deep\small-%03d.txt`, i), random(700))
	}
	vmPowerShell(t, "Write-VolumeCache -DriveLetter "+letter[:1])

	platform := replWindowsPlatform{TrackDir: t.TempDir()}
	job := "0123456789abcdef"
	id := strconv.Itoa(number)
	target := filepath.Join(t.TempDir(), "replica.vhdx")
	round := func(full bool) (*vmCopyResult, *vmCopyResult, *replOpenedDisks) {
		t.Helper()
		opened, err := platform.OpenDisks(context.Background(), job, []string{id}, false, func(s string) { t.Log(s) })
		if err != nil {
			t.Fatal(err)
		}
		defer opened.Close()
		if opened.Ranges == nil || opened.Commit == nil {
			t.Fatal("the round has no change tracking")
		}
		tracked, err := vmCopyDisk(context.Background(), opened.Sources[id], target, vmCopyOptions{Ranges: opened.Ranges[id]})
		if err != nil {
			t.Fatal(err)
		}
		var check *vmCopyResult
		if full {
			// Same shadow copies, every chunk: nothing may have been left.
			if check, err = vmCopyDisk(context.Background(), opened.Sources[id], target, vmCopyOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := opened.Commit(map[string]*vmCopyResult{id: tracked}); err != nil {
			t.Fatal(err)
		}
		return tracked, check, opened
	}
	first, _, _ := round(false)
	t.Logf("first round read %s", vmFormatBytes(first.Progress.BytesRead))

	// The changes.
	file, err := os.OpenFile(filepath.Join(letter, `data\database.mdf`), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{5 << 20, 47<<20 + 123, 90 << 20} {
		file.WriteAt(random(16<<10), at)
	}
	file.Close()
	log, _ := os.OpenFile(filepath.Join(letter, `data\log.ldf`), os.O_APPEND|os.O_WRONLY, 0)
	log.Write(random(3 << 20))
	log.Close()
	write(`data\new.bin`, random(5<<20))
	os.Remove(filepath.Join(letter, `data\old.txt`))
	os.Rename(filepath.Join(letter, `data\rename-me.bin`), filepath.Join(letter, `data\deep\renamed.bin`))
	write(`data\deep\keep.bin:extra`, random(256<<10))
	for i := 0; i < 400; i++ {
		write(fmt.Sprintf(`data\deep\more-%03d.txt`, i), random(900))
	}
	vmPowerShell(t, `icacls '`+filepath.Join(letter, "data")+`' /grant 'Users:(OI)(CI)RX' /T /Q | Out-Null`)
	vmPowerShell(t, "Optimize-Volume -DriveLetter "+letter[:1]+" -Defrag -ErrorAction SilentlyContinue | Out-Null; Write-VolumeCache -DriveLetter "+letter[:1])
	// A file moved into the clusters another file freed: the bitmap shows
	// them in use before and after, and the journal has no entry for the
	// moved file.
	victim, err := replPathRuns(filepath.Join(letter, `data\victim.bin`))
	if err != nil || len(victim) == 0 {
		t.Fatalf("victim runs %v %v", victim, err)
	}
	os.Remove(filepath.Join(letter, `data\victim.bin`))
	vmPowerShell(t, "Write-VolumeCache -DriveLetter "+letter[:1])
	count := victim[0].Count
	if count > (8<<20)/4096 {
		count = (8 << 20) / 4096
	}
	moveErr := error(nil)
	for try := 0; try < 10; try++ {
		if moveErr = replTestMoveFile(t, letter, filepath.Join(letter, `data\mover.bin`), victim[0].Start, count); moveErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond) // freed clusters are reusable once NTFS commits the delete
	}
	if moveErr != nil {
		t.Fatalf("moving mover.bin into victim.bin's clusters: %v", moveErr)
	}
	if moved, _ := replPathRuns(filepath.Join(letter, `data\mover.bin`)); len(moved) == 0 || moved[0].Start != victim[0].Start {
		t.Fatalf("mover.bin is at %v, not %d", moved, victim[0].Start)
	}
	vmPowerShell(t, "Write-VolumeCache -DriveLetter "+letter[:1])

	second, check, _ := round(true)
	t.Logf("tracked round read %s, %d chunks changed; full re-read found %d more", vmFormatBytes(second.Progress.BytesRead), second.Progress.Changed, check.Progress.Changed)
	if check.Progress.Changed != 0 {
		for _, index := range check.ChangedChunks {
			t.Logf("missed chunk %d at %s", index, vmFormatBytes(int64(index)*check.State.ChunkSize))
		}
		replTestNameOwners(t, letter, check.ChangedChunks, check.State.ChunkSize)
		t.Fatalf("change tracking missed %d chunks", check.Progress.Changed)
	}
	if second.Progress.BytesRead*2 > check.Progress.BytesRead {
		t.Fatalf("the tracked round read %s of the %s disk", vmFormatBytes(second.Progress.BytesRead), vmFormatBytes(check.Progress.BytesRead))
	}
}

// replTestNameOwners logs which files hold the clusters of missed chunks,
// so a tracking miss names what changed unseen.
func replTestNameOwners(t *testing.T, letter string, chunks []int, chunkSize int64) {
	t.Helper()
	offset, err := strconv.ParseInt(strings.TrimSpace(vmPowerShell(t, "[Console]::Out.Write((Get-Partition -DriveLetter "+letter[:1]+").Offset)")), 10, 64)
	if err != nil {
		t.Logf("partition offset: %v", err)
		return
	}
	reader, err := newReplMFTReader(`\\.\` + letter[:2])
	if err != nil {
		t.Logf("reading the MFT: %v", err)
		return
	}
	defer reader.Close()
	type span struct{ from, to int64 }
	spans := []span{}
	for _, index := range chunks {
		from := (int64(index)*chunkSize - offset) / reader.clusterSize
		spans = append(spans, span{from, from + chunkSize/reader.clusterSize})
	}
	named := map[int64]bool{}
	reader.Scan(context.Background(), func(index int64, record []byte) error {
		runs, _, err := replMFTOwnRuns(record)
		if err != nil || named[index] {
			return nil
		}
		for _, run := range runs {
			for _, s := range spans {
				if run.Start < s.to && run.Start+run.Count > s.from {
					named[index] = true
					t.Logf("MFT record %d (%s) holds clusters %d-%d in a missed chunk", index, replTestRecordName(record), run.Start, run.Start+run.Count-1)
					return nil
				}
			}
		}
		return nil
	})
	if len(named) == 0 {
		t.Log("no file holds the missed clusters (free space or outside the volume)")
	}
}

// replTestRecordName returns the longest $FILE_NAME in an MFT record.
func replTestRecordName(record []byte) string {
	fixed := append([]byte(nil), record...)
	usaOffset, usaCount := int(binary.LittleEndian.Uint16(fixed[4:])), int(binary.LittleEndian.Uint16(fixed[6:]))
	if usaCount >= 2 && usaOffset+2*usaCount <= len(fixed) {
		stride := len(fixed) / (usaCount - 1)
		for i := 1; i < usaCount; i++ {
			end := i*stride - 2
			fixed[end], fixed[end+1] = fixed[usaOffset+2*i], fixed[usaOffset+2*i+1]
		}
	}
	name := "?"
	for at := int(binary.LittleEndian.Uint16(fixed[20:])); at+24 <= len(fixed); {
		kind, length := binary.LittleEndian.Uint32(fixed[at:]), int(binary.LittleEndian.Uint32(fixed[at+4:]))
		if kind == 0xFFFFFFFF || length < 16 || at+length > len(fixed) {
			break
		}
		if kind == 0x30 && fixed[at+8] == 0 {
			content := at + int(binary.LittleEndian.Uint16(fixed[at+20:]))
			if content+66 <= len(fixed) {
				count := int(fixed[content+64])
				if content+66+2*count <= len(fixed) {
					chars := make([]uint16, count)
					for i := range chars {
						chars[i] = binary.LittleEndian.Uint16(fixed[content+66+2*i:])
					}
					if candidate := windows.UTF16ToString(chars); len(candidate) > len(name) || name == "?" {
						name = candidate
					}
				}
			}
		}
		at += length
	}
	return name
}
