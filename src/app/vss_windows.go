//go:build windows

package main

// Volume Shadow Copies through the VSS requester interface
// (IVssBackupComponents), the way backup software takes them. The snapshot
// set is a non-persistent, auto-release backup set of type "copy": it never
// changes application backup history, and Windows removes it when the
// requester releases it (or when this process ends for any reason), so the
// agent never deletes a shadow copy. Endpoint protection treats a process
// that deletes shadow copies, above all through PowerShell and WMI, as
// ransomware; this path has neither.

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	vssAPI          = windows.NewLazySystemDLL("vssapi.dll")
	vssCreateProc   = vssAPI.NewProc("CreateVssBackupComponentsInternal")
	vssFreePropProc = vssAPI.NewProc("VssFreeSnapshotPropertiesInternal")
)

// IVssBackupComponents vtable slots (vsbackup.h order).
const (
	vssRelease               = 2
	vssInitializeForBackup   = 5
	vssSetBackupState        = 6
	vssGatherWriterMetadata  = 9
	vssPrepareForBackup      = 14
	vssAbortBackup           = 15
	vssBackupComplete        = 27
	vssSetContext            = 35
	vssStartSnapshotSet      = 36
	vssAddToSnapshotSet      = 37
	vssDoSnapshotSet         = 38
	vssGetSnapshotProperties = 42
)

// IVssAsync vtable slots.
const (
	vssAsyncCancel      = 3
	vssAsyncWait        = 4
	vssAsyncQueryStatus = 5
)

const (
	vssCtxBackup         = 0 // non-persistent, auto-release, writers take part
	vssBackupTypeCopy    = 5
	vssAsyncFinished     = 0x0004230A
	vssAsyncPending      = 0x00042309
	vssErrSetInProgress  = 0x80042316
	vssAsyncTimeout      = 10 * time.Minute
	vssSetInProgressWait = 15 * time.Second
)

var vssErrorText = map[uint32]string{
	0x80042301: "VSS is in the wrong state for this step",
	0x80042302: "VSS hit an unexpected error",
	0x80042306: "the shadow copy provider failed",
	0x80042308: "a VSS object was not found",
	0x8004230C: "shadow copies are not supported on this volume",
	0x8004230F: "the shadow copy provider failed unexpectedly",
	0x80042312: "too many volumes are in this shadow copy set",
	0x80042316: "another shadow copy is being taken right now",
	0x80042317: "the volume already has the maximum number of shadow copies",
	0x8004231F: "there is not enough shadow copy storage space on the volume",
	0x80042320: "the shadow copy provider has too many shadow copies",
	0x80070005: "access denied (the agent must run as SYSTEM or an administrator)",
	0x8004230D: "the shadow copy provider is not registered",
	0x80042318: "a VSS writer timed out while freezing",
}

type vssError struct {
	step string
	code uint32
}

func (e vssError) Error() string {
	text := vssErrorText[e.code]
	if text == "" {
		text = "Volume Shadow Copy failed"
	}
	return fmt.Sprintf("%s: %s (0x%08X)", e.step, text, e.code)
}

// vssCall calls slot index of a COM object's vtable and turns a failing
// HRESULT into an error.
func vssCall(object unsafe.Pointer, step string, index int, args ...uintptr) error {
	vtable := *(*unsafe.Pointer)(object)
	fn := *(*uintptr)(unsafe.Add(vtable, index*int(unsafe.Sizeof(uintptr(0)))))
	result, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(object)}, args...)...)
	if int32(result) < 0 {
		return vssError{step: step, code: uint32(result)}
	}
	return nil
}

func vssReleaseObject(object unsafe.Pointer) {
	if object != nil {
		vtable := *(*unsafe.Pointer)(object)
		fn := *(*uintptr)(unsafe.Add(vtable, vssRelease*int(unsafe.Sizeof(uintptr(0)))))
		syscall.SyscallN(fn, uintptr(object))
	}
}

// vssWait waits for an IVssAsync operation and releases it.
func vssWait(step string, async unsafe.Pointer, err error) error {
	if err != nil {
		return err
	}
	if async == nil {
		return vssError{step: step, code: 0x80042302}
	}
	defer vssReleaseObject(async)
	if err := vssCall(async, step, vssAsyncWait, uintptr(vssAsyncTimeout/time.Millisecond)); err != nil {
		return err
	}
	var status, reserved int32
	if err := vssCall(async, step, vssAsyncQueryStatus, uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&reserved))); err != nil {
		return err
	}
	switch {
	case status < 0:
		return vssError{step: step, code: uint32(status)}
	case uint32(status) == vssAsyncPending:
		vssCall(async, step, vssAsyncCancel)
		return fmt.Errorf("%s did not finish within %s", step, vssAsyncTimeout)
	case uint32(status) != vssAsyncFinished:
		return fmt.Errorf("%s ended with status 0x%08X", step, uint32(status))
	}
	return nil
}

func vssAsyncCall(object unsafe.Pointer, step string, index int) error {
	var async unsafe.Pointer
	err := vssCall(object, step, index, uintptr(unsafe.Pointer(&async)))
	return vssWait(step, async, err)
}

// vssSnapshotProp is VSS_SNAPSHOT_PROP.
type vssSnapshotProp struct {
	SnapshotID         windows.GUID
	SnapshotSetID      windows.GUID
	SnapshotsCount     int32
	DeviceObject       *uint16
	OriginalVolumeName *uint16
	OriginatingMachine *uint16
	ServiceMachine     *uint16
	ExposedName        *uint16
	ExposedPath        *uint16
	ProviderID         windows.GUID
	SnapshotAttributes int32
	CreationTimestamp  int64
	Status             int32
}

// vssSnapshotSet is one shadow copy set held by a requester. Its COM calls
// all run on one locked thread, which ends when the set is released.
type vssSnapshotSet struct {
	// Devices maps each volume name to its shadow copy device.
	Devices map[string]string
	// Failed maps each volume that could not join the set to the reason.
	Failed  map[string]error
	release chan struct{}
	done    chan struct{}
	once    sync.Once
}

// Close releases the set; Windows then removes its shadow copies.
func (s *vssSnapshotSet) Close() error {
	s.once.Do(func() {
		close(s.release)
		<-s.done
	})
	return nil
}

// vssCreateMu keeps this process to one snapshot set being created at a
// time (VSS allows one system-wide).
var vssCreateMu sync.Mutex

// vssCreateSnapshotSet takes one shadow copy set of volumes (volume GUID
// paths with a trailing backslash). A volume that cannot join the set is
// listed in Failed; the rest are taken together, at one moment. It fails
// only when no shadow copy could be taken.
func vssCreateSnapshotSet(volumes []string) (*vssSnapshotSet, error) {
	if len(volumes) == 0 {
		return nil, errors.New("no volumes to take shadow copies of")
	}
	if err := vssAPI.Load(); err != nil {
		return nil, fmt.Errorf("Volume Shadow Copy is not available: %w", err)
	}
	vssCreateMu.Lock()
	defer vssCreateMu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(vssSetInProgressWait)
		}
		set, err := vssTryCreate(volumes)
		if err == nil {
			return set, nil
		}
		lastErr = err
		var verr vssError
		if !errors.As(err, &verr) || verr.code != vssErrSetInProgress {
			break
		}
	}
	return nil, lastErr
}

func vssTryCreate(volumes []string) (*vssSnapshotSet, error) {
	set := &vssSnapshotSet{Devices: map[string]string{}, Failed: map[string]error{}, release: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		// The thread is never unlocked, so it ends with this goroutine and
		// takes its COM state with it.
		runtime.LockOSThread()
		defer close(set.done)
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil && !errors.Is(err, syscall.Errno(1)) { // S_FALSE
			ready <- fmt.Errorf("COM could not start for Volume Shadow Copy: %w", err)
			return
		}
		defer windows.CoUninitialize()
		var components unsafe.Pointer
		if result, _, _ := vssCreateProc.Call(uintptr(unsafe.Pointer(&components))); int32(result) < 0 || components == nil {
			ready <- vssError{step: "starting Volume Shadow Copy", code: uint32(result)}
			return
		}
		defer vssReleaseObject(components) // releases the shadow copies
		prepared := false
		fail := func(err error) {
			if prepared {
				vssCall(components, "aborting the shadow copy", vssAbortBackup)
			}
			ready <- err
		}
		if err := vssCall(components, "starting the shadow copy", vssInitializeForBackup, 0); err != nil {
			fail(err)
			return
		}
		if err := vssCall(components, "starting the shadow copy", vssSetContext, vssCtxBackup); err != nil {
			fail(err)
			return
		}
		if err := vssCall(components, "starting the shadow copy", vssSetBackupState, 0, 0, vssBackupTypeCopy, 0); err != nil {
			fail(err)
			return
		}
		if err := vssAsyncCall(components, "asking the VSS writers to report", vssGatherWriterMetadata); err != nil {
			fail(err)
			return
		}
		var setID windows.GUID
		if err := vssCall(components, "starting the shadow copy set", vssStartSnapshotSet, uintptr(unsafe.Pointer(&setID))); err != nil {
			fail(err)
			return
		}
		prepared = true
		ids := map[string]windows.GUID{}
		var provider windows.GUID // the system chooses the provider
		for _, volume := range volumes {
			name, err := windows.UTF16PtrFromString(volume)
			if err != nil {
				set.Failed[volume] = err
				continue
			}
			var id windows.GUID
			if err := vssCall(components, "adding "+volume+" to the shadow copy", vssAddToSnapshotSet, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&provider)), uintptr(unsafe.Pointer(&id))); err != nil {
				set.Failed[volume] = err
				continue
			}
			ids[volume] = id
		}
		if len(ids) == 0 {
			fail(fmt.Errorf("no volume could join the shadow copy set: %v", firstVSSFailure(set.Failed)))
			return
		}
		if err := vssAsyncCall(components, "preparing the shadow copy", vssPrepareForBackup); err != nil {
			fail(err)
			return
		}
		if err := vssAsyncCall(components, "taking the shadow copy", vssDoSnapshotSet); err != nil {
			fail(err)
			return
		}
		for volume, id := range ids {
			var prop vssSnapshotProp
			if err := vssCall(components, "reading the shadow copy of "+volume, vssGetSnapshotProperties, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&prop))); err != nil {
				set.Failed[volume] = err
				continue
			}
			if prop.DeviceObject != nil {
				set.Devices[volume] = windows.UTF16PtrToString(prop.DeviceObject)
			}
			vssFreePropProc.Call(uintptr(unsafe.Pointer(&prop)))
			if set.Devices[volume] == "" {
				set.Failed[volume] = errors.New("the shadow copy has no device")
			}
		}
		if len(set.Devices) == 0 {
			fail(fmt.Errorf("no shadow copy could be opened: %v", firstVSSFailure(set.Failed)))
			return
		}
		ready <- nil
		<-set.release
		// Tells the writers the copy is over; then the release removes the
		// shadow copies.
		vssAsyncCall(components, "ending the shadow copy", vssBackupComplete)
	}()
	if err := <-ready; err != nil {
		<-set.done
		return nil, err
	}
	return set, nil
}

func firstVSSFailure(failed map[string]error) error {
	names := []string{}
	for name := range failed {
		names = append(names, name)
	}
	if len(names) == 0 {
		return errors.New("unknown reason")
	}
	parts := []string{}
	for _, name := range names {
		parts = append(parts, failed[name].Error())
	}
	return errors.New(strings.Join(parts, "; "))
}
