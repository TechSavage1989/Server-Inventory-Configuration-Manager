//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// vmOtherInstance is another copy of this application that still listens
// on the receiver's port: most often one left running after its window was
// closed while it was busy. Only its process ID and start time are shown.
type vmOtherInstance struct {
	PID     uint32    `json:"pid"`
	Started time.Time `json:"started"`
	Port    int       `json:"port"`
}

// tcp6ListenerPID finds the process listening on a TCP port over IPv6,
// which is where a Go listener on ":port" (dual stack) appears.
func tcp6ListenerPID(port int) uint32 {
	var size uint32
	procGetExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, 23, 3, 0)
	if size < 4 || size > 16*1024*1024 {
		return 0
	}
	buf := make([]byte, size)
	code, _, _ := procGetExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 23, 3, 0)
	if code != 0 {
		return 0
	}
	const rowSize = 56 // MIB_TCP6ROW_OWNER_PID
	count := int(binary.LittleEndian.Uint32(buf))
	if count > (len(buf)-4)/rowSize {
		return 0
	}
	for i := 0; i < count; i++ {
		row := buf[4+i*rowSize : 4+(i+1)*rowSize]
		if int(binary.BigEndian.Uint16(row[20:22])) == port {
			return binary.LittleEndian.Uint32(row[52:56])
		}
	}
	return 0
}

// vmProcessImage returns a process's executable path and start time.
func vmProcessImage(handle windows.Handle) (string, time.Time, error) {
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", time.Time{}, err
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", time.Time{}, err
	}
	return windows.UTF16ToString(buffer[:size]), time.Unix(0, created.Nanoseconds()), nil
}

// vmSameApplication reports whether an executable is this application:
// the same file, or a copy with the same name (an older version beside it).
func vmSameApplication(path string) bool {
	self, err := os.Executable()
	if err != nil || path == "" {
		return false
	}
	if strings.EqualFold(filepath.Clean(path), filepath.Clean(self)) {
		return true
	}
	return strings.EqualFold(filepath.Base(path), filepath.Base(self))
}

// vmOtherInstanceOn returns the other copy of this application listening
// on port, if there is one. Any other program holding the port is left
// alone and not named.
func vmOtherInstanceOn(port int) (vmOtherInstance, bool) {
	pid := tcpListenerPID(port)
	if pid == 0 {
		pid = tcp6ListenerPID(port)
	}
	if pid == 0 || pid == uint32(os.Getpid()) {
		return vmOtherInstance{}, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return vmOtherInstance{}, false
	}
	defer windows.CloseHandle(handle)
	path, started, err := vmProcessImage(handle)
	if err != nil || !vmSameApplication(path) {
		return vmOtherInstance{}, false
	}
	return vmOtherInstance{PID: pid, Started: started.UTC(), Port: port}, true
}

// vmCloseOtherInstance ends the other copy of this application that holds
// the receiver's port. It checks again, just before, that the process with
// that ID still listens on the port and is this application, and never
// touches any other process.
func vmCloseOtherInstance(port int, pid uint32) error {
	other, ok := vmOtherInstanceOn(port)
	if !ok || other.PID != pid {
		return errors.New("that copy of the application no longer holds the port; start the receiver again")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return fmt.Errorf("Windows refused to close it (%v); run this application as administrator, or end process %d in Task Manager", err, pid)
	}
	defer windows.CloseHandle(handle)
	if path, _, err := vmProcessImage(handle); err != nil || !vmSameApplication(path) {
		return errors.New("that process is not this application, so it was left alone")
	}
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return fmt.Errorf("it could not be closed: %v", err)
	}
	if event, _ := windows.WaitForSingleObject(handle, 10000); event != windows.WAIT_OBJECT_0 {
		return errors.New("it did not end within 10 seconds; try again")
	}
	return nil
}
