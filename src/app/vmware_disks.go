package main

// Disk copy between computers from the VMware tab's Disk copy view: this
// computer can receive disks (the receiver in vmcopy_receiver.go) or send
// its own disks through shadow copies (the agent in vmagent_windows.go).

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	return append(v4, v6...)
}
