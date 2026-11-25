package main

// Change tracking for replication rounds. Reading a whole disk every round
// makes the recovery point of a large server as long as the read takes, so
// for each simple NTFS volume a round reads only:
//
//   - clusters the volume has in use (free space never matters to NTFS), on
//     a "used" pass; or, between checks,
//   - clusters that became allocated since the last round (from the volume
//     bitmap), every cluster of the NTFS metadata files, and every cluster of
//     the files and folders the change journal lists since the last round.
//
// Everything outside those volumes (partition tables, boot, recovery and
// other partitions, other file systems, dynamic volumes) is read in full.
// A periodic check reads every used cluster and compares what changed with
// what tracking would have chosen; any miss turns tracking off for that
// volume, so a gap can cost speed but never a stale replica.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// replTrackCheckEvery is how often a round reads every used cluster
	// and checks change tracking against it.
	replTrackCheckEvery = 6 * time.Hour
	// replTrackMaxFiles is the most changed files a tracked round follows;
	// more than that reads every used cluster instead.
	replTrackMaxFiles = 250000
	// replTrackTail is always read at the end of a volume (NTFS keeps its
	// backup boot sector in the last sector, outside the cluster map).
	replTrackTail = 1 << 20
)

// replRun is a run of clusters.
type replRun struct {
	Start, Count int64
}

// replAllocatedRuns lists the set bits of a volume bitmap (bit i of byte
// i/8, least significant first) as runs, over total clusters.
func replAllocatedRuns(bitmap []byte, total int64) []replRun {
	return replBitRuns(total, func(i int64) bool { return bitmap[i/8]&(1<<(uint(i)%8)) != 0 })
}

// replNewlyAllocated lists clusters set in current but not in previous.
func replNewlyAllocated(previous, current []byte, total int64) []replRun {
	return replBitRuns(total, func(i int64) bool {
		bit := byte(1 << (uint(i) % 8))
		return current[i/8]&bit != 0 && previous[i/8]&bit == 0
	})
}

func replBitRuns(total int64, set func(int64) bool) []replRun {
	runs := []replRun{}
	for i := int64(0); i < total; {
		// Skip whole zero (or whole unset-by-rule) bytes quickly.
		if i%8 == 0 && i+8 <= total {
			any := false
			for j := i; j < i+8; j++ {
				if set(j) {
					any = true
					break
				}
			}
			if !any {
				i += 8
				continue
			}
		}
		if !set(i) {
			i++
			continue
		}
		start := i
		for i < total && set(i) {
			i++
		}
		runs = append(runs, replRun{Start: start, Count: i - start})
	}
	return runs
}

// replVolumeToDisk turns cluster runs of a volume that starts at byte
// volumeStart of its disk into disk byte ranges.
func replVolumeToDisk(volumeStart, clusterSize int64, runs []replRun) []vmCopyRange {
	ranges := make([]vmCopyRange, 0, len(runs))
	for _, run := range runs {
		if run.Count <= 0 || run.Start < 0 {
			continue
		}
		ranges = append(ranges, vmCopyRange{Start: volumeStart + run.Start*clusterSize, Length: run.Count * clusterSize})
	}
	return ranges
}

// replMergeRanges sorts ranges and joins those that touch or overlap.
func replMergeRanges(ranges []vmCopyRange) []vmCopyRange {
	list := []vmCopyRange{}
	for _, r := range ranges {
		if r.Length > 0 {
			list = append(list, r)
		}
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Start < list[b].Start })
	merged := []vmCopyRange{}
	for _, r := range list {
		if n := len(merged); n > 0 && r.Start <= merged[n-1].Start+merged[n-1].Length {
			if end := r.Start + r.Length; end > merged[n-1].Start+merged[n-1].Length {
				merged[n-1].Length = end - merged[n-1].Start
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// replOutside lists the parts of [0,size) not covered by the excluded
// ranges.
func replOutside(size int64, excluded []vmCopyRange) []vmCopyRange {
	outside := []vmCopyRange{}
	at := int64(0)
	for _, r := range replMergeRanges(excluded) {
		if r.Start > at {
			outside = append(outside, vmCopyRange{Start: at, Length: r.Start - at})
		}
		if end := r.Start + r.Length; end > at {
			at = end
		}
	}
	if at < size {
		outside = append(outside, vmCopyRange{Start: at, Length: size - at})
	}
	return outside
}

// replMissedChunks counts changed chunks that the tracked ranges did not
// cover.
func replMissedChunks(tracked []vmCopyRange, changed []int, chunkSize int64) []int {
	missed := []int{}
	merged := replMergeRanges(tracked)
	for _, index := range changed {
		start, end := int64(index)*chunkSize, int64(index+1)*chunkSize
		i := sort.Search(len(merged), func(i int) bool { return merged[i].Start+merged[i].Length > start })
		if i == len(merged) || merged[i].Start >= end {
			missed = append(missed, index)
		}
	}
	return missed
}

// replRangeBytes adds up the length of merged ranges.
func replRangeBytes(ranges []vmCopyRange) int64 {
	total := int64(0)
	for _, r := range replMergeRanges(ranges) {
		total += r.Length
	}
	return total
}

// replVolumeTrack is what a job remembers about one volume between rounds.
type replVolumeTrack struct {
	JournalID   uint64    `json:"journalId"`
	Mark        int64     `json:"mark"` // the journal's next USN, read before the last round's shadow copy
	ClusterSize int64     `json:"clusterSize"`
	Clusters    int64     `json:"clusters"`
	LastCheck   time.Time `json:"lastCheck"`
	Disabled    string    `json:"disabled,omitempty"`
}

type replTrackState struct {
	Volumes map[string]replVolumeTrack `json:"volumes"`
}

// replTrackStore keeps one job's tracking state and volume bitmaps.
type replTrackStore struct {
	dir string
}

func newReplTrackStore(root, job string) (*replTrackStore, error) {
	if !replJobIDPattern.MatchString(job) {
		return nil, fmt.Errorf("%q is not a job ID", job)
	}
	return &replTrackStore{dir: filepath.Join(root, job)}, nil
}

func (s *replTrackStore) Load() replTrackState {
	state := replTrackState{Volumes: map[string]replVolumeTrack{}}
	raw, err := os.ReadFile(filepath.Join(s.dir, "tracking.json"))
	if err == nil && json.Unmarshal(raw, &state) == nil && state.Volumes != nil {
		return state
	}
	return replTrackState{Volumes: map[string]replVolumeTrack{}}
}

func (s *replTrackStore) Save(state replTrackState) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return replWriteFileAtomic(filepath.Join(s.dir, "tracking.json"), raw)
}

func (s *replTrackStore) bitmapPath(key string) string {
	return filepath.Join(s.dir, key+".bitmap")
}

func (s *replTrackStore) LoadBitmap(key string, length int) ([]byte, error) {
	raw, err := os.ReadFile(s.bitmapPath(key))
	if err != nil {
		return nil, err
	}
	if len(raw) != length {
		return nil, errors.New("the saved volume bitmap has a different size")
	}
	return raw, nil
}

func (s *replTrackStore) SaveBitmap(key string, bitmap []byte) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	return replWriteFileAtomic(s.bitmapPath(key), bitmap)
}

func (s *replTrackStore) locationsPath(key string) string {
	return filepath.Join(s.dir, key+".locations")
}

// LoadLocations reads the last round's MFT record locations (see
// replMFTLocation); nil when there are none yet.
func (s *replTrackStore) LoadLocations(key string) ([]uint64, error) {
	raw, err := os.ReadFile(s.locationsPath(key))
	if err != nil {
		return nil, err
	}
	if len(raw)%8 != 0 {
		return nil, errors.New("the saved file locations are damaged")
	}
	list := make([]uint64, len(raw)/8)
	for i := range list {
		list[i] = binary.LittleEndian.Uint64(raw[i*8:])
	}
	return list, nil
}

func (s *replTrackStore) RemoveLocations(key string) error {
	if err := os.Remove(s.locationsPath(key)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *replTrackStore) SaveLocations(key string, list []uint64) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	raw := make([]byte, len(list)*8)
	for i, v := range list {
		binary.LittleEndian.PutUint64(raw[i*8:], v)
	}
	return replWriteFileAtomic(s.locationsPath(key), raw)
}

func (s *replTrackStore) Forget() error { return os.RemoveAll(s.dir) }

func replWriteFileAtomic(path string, data []byte) error {
	temp := path + ".new"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		os.Remove(temp)
		return err
	}
	return nil
}

// replToggled lists clusters whose allocation changed either way.
func replToggled(previous, current []byte, total int64) []replRun {
	return replBitRuns(total, func(i int64) bool {
		bit := byte(1 << (uint(i) % 8))
		return (current[i/8]^previous[i/8])&bit != 0
	})
}
