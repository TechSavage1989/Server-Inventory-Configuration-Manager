//go:build windows

package main

// The receiver command line. The same executable runs without its window
// from receiver boot media (WinPE) on a new HyperCore, Nutanix or Proxmox VM
// or a physical server, and writes copies straight onto that machine's disks:
//
//	Server-Inventory-Configuration-Manager.exe --receiver --list-disks
//	Server-Inventory-Configuration-Manager.exe --receiver --disks 1,2 [--overwrite-disks 2] [--port 8443] [--records D:\records]
//	Server-Inventory-Configuration-Manager.exe --receiver --folder D:\Incoming
//
// It prints the pairing code and certificate thumbprint for the sender to
// enter. The pairing code is shown on the console only and never written to
// the log file.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	procAttachConsole = kernel32.NewProc("AttachConsole")
	procAllocConsole  = kernel32.NewProc("AllocConsole")
)

const attachParentProcess = ^uintptr(0)

// vmReceiverConsole connects this GUI-subsystem program to the console it
// was started from, or opens one.
func vmReceiverConsole() {
	if result, _, _ := procAttachConsole.Call(attachParentProcess); result == 0 {
		procAllocConsole.Call()
	}
	if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = out, out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = in
	}
}

func vmParseDiskNumbers(text string) ([]int, error) {
	numbers := []int{}
	seen := map[int]bool{}
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || number >= vmMaxPhysicalDisks {
			return nil, fmt.Errorf("%q is not a disk number", part)
		}
		if !seen[number] {
			seen[number] = true
			numbers = append(numbers, number)
		}
	}
	sort.Ints(numbers)
	return numbers, nil
}

func vmPrintDisks(out io.Writer, disks []vmWindowsDiskInfo) {
	fmt.Fprintln(out, "Disks on this computer:")
	for _, disk := range disks {
		state := "holds data"
		if disk.Blank {
			state = "blank"
		}
		if disk.Offline {
			state += ", offline"
		}
		if disk.Protected != "" {
			state += ", cannot be a target because " + disk.Protected
		}
		fmt.Fprintf(out, "  %-3d %10s  %s (%s)\n", disk.Number, vmFormatBytes(disk.Size), disk.Label(), state)
	}
}

func vmLocalAddresses() []string {
	addresses := []string{}
	interfaces, _ := net.Interfaces()
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		list, _ := item.Addrs()
		for _, address := range list {
			if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLinkLocalUnicast() {
				addresses = append(addresses, network.IP.String())
			}
		}
	}
	return addresses
}

// runVMReceiverCommand runs the receiver until Ctrl+C and returns the exit code.
func runVMReceiverCommand(args []string) int {
	vmReceiverConsole()
	out := os.Stdout
	flags := flag.NewFlagSet("receiver", flag.ContinueOnError)
	flags.SetOutput(out)
	port := flags.Int("port", 8443, "TCP port to listen on")
	folder := flags.String("folder", "", "folder to receive VHDX files into")
	diskList := flags.String("disks", "", "comma-separated disk numbers to receive onto (see --list-disks)")
	overwriteList := flags.String("overwrite-disks", "", "disk numbers that hold data and may be overwritten")
	records := flags.String("records", vmReceiverRecordsFolder(), "folder for the raw disk copy records (keep it on storage that survives a restart to resume after one)")
	listOnly := flags.Bool("list-disks", false, "list this computer's disks and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(out, "The receiver could not start: "+err.Error())
		return 1
	}
	if *listOnly {
		vmPrintDisks(out, vmListPhysicalDisks(*records))
		return 0
	}
	numbers, err := vmParseDiskNumbers(*diskList)
	if err != nil {
		return fail(err)
	}
	overwriteNumbers, err := vmParseDiskNumbers(*overwriteList)
	if err != nil {
		return fail(err)
	}
	overwrite := map[int]bool{}
	for _, number := range overwriteNumbers {
		overwrite[number] = true
	}
	for number := range overwrite {
		found := false
		for _, n := range numbers {
			found = found || n == number
		}
		if !found {
			return fail(fmt.Errorf("--overwrite-disks names disk %d, which is not in --disks", number))
		}
	}
	if *folder == "" && len(numbers) == 0 {
		vmPrintDisks(out, vmListPhysicalDisks(*records))
		return fail(errors.New("choose --disks (disk numbers from the list above) or --folder"))
	}
	config := vmReceiverConfig{Folder: *folder, Records: *records}
	if len(numbers) > 0 {
		if config.Disks, err = vmReceiverPhysicalDisks(numbers, overwrite, *records); err != nil {
			return fail(err)
		}
	}
	logFile := (*os.File)(nil)
	if *records != "" {
		if err := os.MkdirAll(*records, 0700); err == nil {
			logFile, _ = os.OpenFile(filepath.Join(*records, "receiver.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		}
	}
	log := func(message string) {
		line := time.Now().Format("2006-01-02 15:04:05") + "  " + message
		fmt.Fprintln(out, line)
		if logFile != nil {
			fmt.Fprintln(logFile, line)
		}
	}
	receiver, err := startVMReceiverWith(fmt.Sprintf(":%d", *port), config, log)
	if err != nil {
		return fail(err)
	}
	for number := range overwrite {
		log(fmt.Sprintf("Disk %d may be overwritten: anything on it is destroyed by the first copy.", number))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Enter these in the sender:")
	for _, address := range vmLocalAddresses() {
		fmt.Fprintf(out, "  Receiver address:  %s:%d\n", address, receiver.Port())
	}
	fmt.Fprintf(out, "  Pairing code:      %s\n", receiver.Code())
	fmt.Fprintf(out, "  Thumbprint:        %s\n", receiver.Thumbprint())
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Allow TCP port %d through this computer's firewall. Press Ctrl+C to stop; copy records are kept so the next receiver resumes.\n", receiver.Port())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log("Stopping the receiver.")
	receiver.Stop()
	if logFile != nil {
		logFile.Close()
	}
	return 0
}
