//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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

	second, check, _ := round(true)
	t.Logf("tracked round read %s, %d chunks changed; full re-read found %d more", vmFormatBytes(second.Progress.BytesRead), second.Progress.Changed, check.Progress.Changed)
	if check.Progress.Changed != 0 {
		for _, index := range check.ChangedChunks {
			t.Logf("missed chunk %d at %s", index, vmFormatBytes(int64(index)*check.State.ChunkSize))
		}
		t.Fatalf("change tracking missed %d chunks", check.Progress.Changed)
	}
	if second.Progress.BytesRead*2 > check.Progress.BytesRead {
		t.Fatalf("the tracked round read %s of the %s disk", vmFormatBytes(second.Progress.BytesRead), vmFormatBytes(check.Progress.BytesRead))
	}
}
