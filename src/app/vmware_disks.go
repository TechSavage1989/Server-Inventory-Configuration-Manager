package main

// Disk copy between computers from the VMware tab's Disk copy view: this
// computer can receive disks (the receiver in vmcopy_receiver.go) or send
// its own disks through shadow copies (the agent in vmagent_windows.go).

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const vmReceiverDefaultPort = 44380

// vmAgentSendResult is one disk's outcome in a send round.
type vmAgentSendResult struct {
	Disk         int    `json:"disk"`
	Name         string `json:"name"`
	BytesRead    int64  `json:"bytesRead"`
	BytesWritten int64  `json:"bytesWritten"`
	Resumed      bool   `json:"resumed"`
	Verified     bool   `json:"verified"`
	Passes       int    `json:"passes"`
	Shadowed     int    `json:"shadowed"`
	BytesSent    int64  `json:"bytesSent,omitempty"`
	Error        string `json:"error,omitempty"`
}

var vmDiskNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// vmAgentDiskName is the file the receiver writes for disk n of host. It
// stays the same on every round so a later send is a delta pass.
func vmAgentDiskName(host string, disk int) string {
	safe := strings.Trim(vmDiskNameUnsafe.ReplaceAllString(strings.TrimSpace(host), "-"), "-.")
	if safe == "" {
		safe = "computer"
	}
	if len(safe) > 90 {
		safe = safe[:90]
	}
	return fmt.Sprintf("%s-disk%d.vhdx", safe, disk)
}

// vmReceiverAddress checks the host:port the operator typed for a receiver.
// A bare host gets the default port.
func vmReceiverAddress(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("enter the receiving computer's name or address")
	}
	host, port, err := net.SplitHostPort(text)
	if err != nil {
		host, port = strings.Trim(text, "[]"), strconv.Itoa(vmReceiverDefaultPort)
	}
	if host == "" || strings.ContainsAny(host, "/\\@ ") {
		return "", errors.New("enter the receiver as a computer name or IP address, with :port if it is not " + strconv.Itoa(vmReceiverDefaultPort))
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("the receiver's port must be a number from 1 to 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}

// vmReceiverPort checks the port chosen for this computer's receiver.
func vmReceiverPort(port int) (int, error) {
	if port == 0 {
		return vmReceiverDefaultPort, nil
	}
	if port < 1024 || port > 65535 {
		return 0, errors.New("choose a port from 1024 to 65535")
	}
	return port, nil
}

// vmLocalAddresses lists this computer's non-loopback IP addresses, IPv4
// first, for the receiver panel.
func vmLocalAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var v4, v6 []string
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := item.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			network, ok := address.(*net.IPNet)
			if !ok || network.IP.IsLoopback() || network.IP.IsLinkLocalUnicast() {
				continue
			}
			if network.IP.To4() != nil {
				v4 = append(v4, network.IP.String())
			} else {
				v6 = append(v6, network.IP.String())
			}
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return vmPrimaryFirst(append(v4, v6...), vmDefaultRouteAddress())
}

// vmDefaultRouteAddress is the address this computer sends from on its
// default route: the one other networks most likely reach. A UDP "dial"
// only asks the routing table; no packet is sent. A host with storage,
// cluster or backup networks lists those addresses too, and the first in
// sorted order was often one a sender cannot reach.
func vmDefaultRouteAddress() string {
	for _, probe := range []string{"192.0.2.1:9", "[2001:db8::1]:9"} {
		conn, err := net.Dial("udp", probe)
		if err != nil {
			continue
		}
		local, _ := conn.LocalAddr().(*net.UDPAddr)
		conn.Close()
		if local != nil && !local.IP.IsLoopback() && !local.IP.IsUnspecified() {
			return local.IP.String()
		}
	}
	return ""
}

// vmPrimaryFirst moves primary to the front of addresses when it is there.
func vmPrimaryFirst(addresses []string, primary string) []string {
	if primary == "" {
		return addresses
	}
	out := make([]string, 0, len(addresses))
	found := false
	for _, address := range addresses {
		if address == primary {
			found = true
			continue
		}
		out = append(out, address)
	}
	if !found {
		return addresses
	}
	return append([]string{primary}, out...)
}

// vmReachMax limits how many pasted receiver addresses one check tries.
const vmReachMax = 8

// vmReachableAddresses opens one TCP connection to each candidate receiver
// address (all given by the operator, never a range or a port sweep) and
// returns those that answered, in the order given. It sends nothing over
// the connection: the receiver is then checked through its pinned
// certificate as usual.
func vmReachableAddresses(ctx context.Context, candidates []string, timeout time.Duration) ([]string, error) {
	if len(candidates) > vmReachMax {
		return nil, fmt.Errorf("give at most %d receiver addresses", vmReachMax)
	}
	clean := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		address, err := vmReceiverAddress(candidate)
		if err != nil {
			return nil, err
		}
		clean = append(clean, address)
	}
	ok := make([]bool, len(clean))
	var wait sync.WaitGroup
	for index, address := range clean {
		wait.Add(1)
		go func(index int, address string) {
			defer wait.Done()
			dialer := net.Dialer{Timeout: timeout}
			conn, err := dialer.DialContext(ctx, "tcp", address)
			if err == nil {
				conn.Close()
				ok[index] = true
			}
		}(index, address)
	}
	wait.Wait()
	reachable := []string{}
	for index, address := range clean {
		if ok[index] {
			reachable = append(reachable, address)
		}
	}
	return reachable, nil
}

// vmAgentTargetNames picks what the receiver writes for each source disk:
// the disk-N the operator mapped it to on a raw receiver (the new VM's or
// server's own disks), or a VHDX file named after this computer.
func vmAgentTargetNames(disks []int, targets map[string]string, vhdx func(int) string) (map[int]string, error) {
	names := map[int]string{}
	used := map[string]int{}
	for key := range targets {
		number, err := strconv.Atoi(key)
		found := false
		for _, disk := range disks {
			found = found || (err == nil && disk == number)
		}
		if !found {
			return nil, fmt.Errorf("disk %s has a target but is not being sent", key)
		}
	}
	for _, disk := range disks {
		name := strings.TrimSpace(targets[strconv.Itoa(disk)])
		if name == "" || strings.EqualFold(name, "vhdx") {
			name = vhdx(disk)
		} else if !vmReceiverDiskName.MatchString(name) {
			return nil, fmt.Errorf("choose a receiver disk for disk %d from the list", disk)
		}
		if other, taken := used[strings.ToLower(name)]; taken {
			return nil, fmt.Errorf("disks %d and %d are both going to %s; choose a different receiver disk for each", other, disk, name)
		}
		used[strings.ToLower(name)] = disk
		names[disk] = name
	}
	return names, nil
}
