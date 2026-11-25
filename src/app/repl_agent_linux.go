//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	replLinuxDataDir  = "/var/lib/simm-agent"
	replLinuxBinary   = "/usr/local/sbin/simm-agent"
	replLinuxUnit     = "simm-replication-agent.service"
	replLinuxUnitPath = "/etc/systemd/system/" + replLinuxUnit
	replLinuxFirewall = "firewall.txt" // what the install opened, for uninstall
)

func replAgentVersion() string { return linuxAgentVersion }

func replAgentDataDir() string { return replLinuxDataDir }

func replLinuxUnitText(binary string) string {
	return "[Unit]\nDescription=Server Inventory and Migration Manager replication agent\nAfter=network-online.target lvm2-monitor.service\nWants=network-online.target\n\n" +
		"[Service]\nExecStart=" + binary + " --agent-service\nRestart=always\nRestartSec=10\nKillMode=mixed\nTimeoutStopSec=60\n\n[Install]\nWantedBy=multi-user.target\n"
}

func replLinuxCopy(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	temporary := to + ".new"
	out, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(temporary)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, to)
}

// replLinuxOpenFirewall opens the port in firewalld or ufw when either is
// running, and records what it did.
func replLinuxOpenFirewall(port int) string {
	rule := strconv.Itoa(port) + "/tcp"
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		if state, _ := replExec("firewall-cmd", "--state"); state == "running" {
			if _, err := replExec("firewall-cmd", "--permanent", "--add-port="+rule); err == nil {
				replExec("firewall-cmd", "--reload")
				return "firewalld " + rule
			}
		}
	}
	if _, err := exec.LookPath("ufw"); err == nil {
		if status, _ := replExec("ufw", "status"); strings.Contains(status, "Status: active") {
			if _, err := replExec("ufw", "allow", rule); err == nil {
				return "ufw " + rule
			}
		}
	}
	return ""
}

func replLinuxCloseFirewall(record string) {
	fields := strings.Fields(record)
	if len(fields) != 2 || !regexpPortRule(fields[1]) {
		return
	}
	switch fields[0] {
	case "firewalld":
		replExec("firewall-cmd", "--permanent", "--remove-port="+fields[1])
		replExec("firewall-cmd", "--reload")
	case "ufw":
		replExec("ufw", "delete", "allow", fields[1])
	}
}

func regexpPortRule(rule string) bool {
	port, err := strconv.Atoi(strings.TrimSuffix(rule, "/tcp"))
	return err == nil && strings.HasSuffix(rule, "/tcp") && port >= 1024 && port <= 65535
}

// replInstallLinux installs this program as the agent service and prints
// the key and thumbprint to enter in the console.
func replInstallLinux(port int, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("run the command as root (sudo)")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("this system has no systemd; the agent needs it to run as a service")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Clean(self) != replLinuxBinary {
		if err := os.MkdirAll(filepath.Dir(replLinuxBinary), 0755); err != nil {
			return err
		}
		if err := replLinuxCopy(self, replLinuxBinary); err != nil {
			return fmt.Errorf("copying the agent: %w", err)
		}
	}
	if err := os.MkdirAll(replLinuxDataDir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(replLinuxDataDir, 0700); err != nil {
		return err
	}
	key := replNewAgentKey()
	if err := saveReplAgentConfig(replLinuxDataDir, replAgentConfig{Port: port, KeyHash: replKeyHash(key), Console: "local install"}); err != nil {
		return err
	}
	_, thumbprint, err := replAgentCertificate(replLinuxDataDir)
	if err != nil {
		return err
	}
	if err := os.WriteFile(replLinuxUnitPath, []byte(replLinuxUnitText(replLinuxBinary)), 0644); err != nil {
		return err
	}
	if _, err := replExec("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := replExec("systemctl", "enable", replLinuxUnit); err != nil {
		return err
	}
	if _, err := replExec("systemctl", "restart", replLinuxUnit); err != nil {
		return err
	}
	if record := replLinuxOpenFirewall(port); record != "" {
		os.WriteFile(filepath.Join(replLinuxDataDir, replLinuxFirewall), []byte(record), 0600)
	}
	fmt.Fprintf(out, "The replication agent is installed and running on port %d.\n\nIn the console's Migration and replication tab, Replicate servers, add this server with:\n  Agent key:              %s\n  Certificate thumbprint: %s\n\nThe key is shown only now; it is not saved on this server.\n", port, key, vmNormalizeThumbprint(thumbprint))
	return nil
}

func replUninstallLinux() error {
	if os.Geteuid() != 0 {
		return errors.New("run the command as root (sudo)")
	}
	replExec("systemctl", "disable", "--now", replLinuxUnit)
	if err := os.Remove(replLinuxUnitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	replExec("systemctl", "daemon-reload")
	if record, err := os.ReadFile(filepath.Join(replLinuxDataDir, replLinuxFirewall)); err == nil {
		replLinuxCloseFirewall(string(record))
	}
	if err := os.RemoveAll(replLinuxDataDir); err != nil {
		return err
	}
	if err := os.Remove(replLinuxBinary); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func runReplAgentLinuxCommand(args []string) int {
	if len(args) == 0 {
		args = []string{""}
	}
	switch args[0] {
	case "--agent-service", "--agent":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		var mu sync.Mutex
		service := args[0] == "--agent-service"
		log := func(text string) {
			mu.Lock()
			defer mu.Unlock()
			if service {
				fmt.Println(text) // journald adds the time
			} else {
				fmt.Println(time.Now().Format("15:04:05"), text)
			}
		}
		notify := func(alert replAlert) {
			// sd-daemon priority prefixes: 3 error, 4 warning.
			prefix := "<4>"
			if alert.Level == "error" {
				prefix = "<3>"
			}
			log(prefix + "ALERT " + alert.Kind + ": " + alert.Text)
		}
		if err := runReplAgentWith(ctx, replLinuxDataDir, replLinuxPlatform{}, replAgentVersion(), log, notify); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "--agent-install":
		flags := flag.NewFlagSet("agent-install", flag.ContinueOnError)
		port := flags.Int("port", replAgentPort, "TCP port")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if *port < 1024 || *port > 65535 {
			fmt.Fprintln(os.Stderr, "Choose a port from 1024 to 65535.")
			return 2
		}
		if err := replInstallLinux(*port, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "The agent was not installed:", err)
			return 1
		}
		return 0
	case "--agent-uninstall":
		if err := replUninstallLinux(); err != nil {
			fmt.Fprintln(os.Stderr, "The agent was not removed:", err)
			return 1
		}
		fmt.Println("The replication agent was removed.")
		return 0
	case "--version":
		fmt.Println(replAgentVersion())
		return 0
	}
	fmt.Fprintln(os.Stderr, "Server Inventory and Migration Manager replication agent for Linux.\nUse --agent-install [--port N] (as root), --agent-uninstall or --agent.")
	return 2
}
