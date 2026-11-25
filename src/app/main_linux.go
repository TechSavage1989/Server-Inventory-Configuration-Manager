//go:build linux

package main

import "os"

// linuxAgentVersion is set at build time with
// -ldflags "-X main.linuxAgentVersion=<version>".
var linuxAgentVersion = "dev"

// The Linux build is only the replication agent; the application itself
// runs on Windows.
func main() {
	os.Exit(runReplAgentLinuxCommand(os.Args[1:]))
}
