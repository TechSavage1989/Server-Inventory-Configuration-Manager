//go:build windows

package main

// The replication agent on Windows: the same executable installed as the
// service "SIMMReplicationAgent" (LocalSystem) on each source server. It
// reads disks through shadow copies (vmagent_windows.go), keeps its data in
// %ProgramData%\Server Inventory and Migration Manager Agent with an ACL
// limited to SYSTEM and Administrators, opens only its own firewall rule,
// and reports alerts to the Application event log so monitoring tools see
// them.
//
//	Server-Inventory-Configuration-Manager.exe --agent-install [--port 44390]
//	Server-Inventory-Configuration-Manager.exe --agent-uninstall
//	Server-Inventory-Configuration-Manager.exe --agent-service   (run by the service manager)
//	Server-Inventory-Configuration-Manager.exe --agent           (in a console, for testing)

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

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	replServiceName     = "SIMMReplicationAgent"
	replServiceDisplay  = "Server Inventory and Migration Manager replication agent"
	replEventSource     = "SIMM Replication Agent"
	replFirewallRule    = "Server Inventory and Migration Manager replication agent"
	replAgentFolderName = "Server Inventory and Migration Manager Agent"
	replAgentExeName    = "SIMM-Agent.exe"
	replProtectedSDDL   = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
)

func replAgentVersion() string { return appVersion }

// replAgentDataDir is the agent's data folder.
func replAgentDataDir() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, replAgentFolderName)
}

// replProtectFolder limits a folder to SYSTEM and Administrators, without
// inherited entries, so the agent key hash, certificate key and receiver
// pairing codes are not readable by users.
func replProtectFolder(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(replProtectedSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// replWindowsPlatform reads this computer's disks and controls its services.
type replWindowsPlatform struct {
	// TrackDir holds each job's change tracking state; empty reads every
	// used cluster each round.
	TrackDir string
}

func (replWindowsPlatform) Name() string { return "Windows" }

func (replWindowsPlatform) ListDisks() ([]replDiskInfo, error) {
	disks, err := vmListAgentDisks()
	if err != nil {
		return nil, err
	}
	list := []replDiskInfo{}
	for _, disk := range disks {
		info := replDiskInfo{ID: strconv.Itoa(disk.Number), Size: disk.Size, System: disk.System}
		for _, volume := range disk.Volumes {
			info.Volumes = append(info.Volumes, replVolumeInfo{Name: volume.Name, Mounts: volume.Mounts, FileSystem: volume.FileSystem, Length: volume.Length, Kind: volume.Kind})
		}
		list = append(list, info)
	}
	return list, nil
}

func (p replWindowsPlatform) OpenDisks(ctx context.Context, job string, sources []string, allowLive bool, log func(string)) (*replOpenedDisks, error) {
	opened := &replOpenedDisks{Sources: map[string]vmCopySource{}, Snapshot: time.Now()}
	closers := []io.Closer{}
	opened.Close = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i].Close()
		}
	}
	numbers := []int{}
	for _, id := range sources {
		number, err := strconv.Atoi(id)
		if err != nil || number < 0 || number > 63 || strconv.Itoa(number) != id {
			opened.Close()
			return nil, fmt.Errorf("%q is not a disk number", id)
		}
		numbers = append(numbers, number)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Change journal positions are read before the shadow copies, so
	// anything written in between is counted again next round, never lost.
	marks := replJournalMarks(numbers)
	set, err := openVMAgentDisks(numbers, allowLive, log)
	if err != nil {
		return nil, err
	}
	closers = append(closers, set)
	for _, number := range numbers {
		id := strconv.Itoa(number)
		opened.Sources[id] = set.Sources[number]
		shadowed, kinds := 0, map[string]int{}
		for _, volume := range set.Volumes[number] {
			if volume.Shadowed {
				shadowed++
			}
			if volume.Kind != "" && volume.Kind != "simple" {
				kinds[volume.Kind]++
			}
		}
		note := fmt.Sprintf("Disk %d: %s from shadow copies", number, vmPlural(shadowed, "volume", "volumes"))
		for _, kind := range []string{"spanned", "striped", "mirrored"} {
			if kinds[kind] > 0 {
				note += fmt.Sprintf(", %d %s", kinds[kind], kind)
			}
		}
		opened.Notes = append(opened.Notes, note+".")
	}
	if err := replPlanTrackedRound(ctx, p.TrackDir, job, numbers, set, marks, opened, log); err != nil {
		log("Change tracking is unavailable this round, so every disk is read in full: " + err.Error())
		opened.Ranges, opened.Used, opened.Commit = nil, nil, nil
	}
	return opened, nil
}

// Forget removes a deleted job's change tracking state.
func (p replWindowsPlatform) Forget(job string) {
	if p.TrackDir == "" {
		return
	}
	if store, err := newReplTrackStore(p.TrackDir, job); err == nil {
		store.Forget()
	}
}

// replServiceWait waits for a service to reach a state.
func replServiceWait(service *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("it did not reach the wanted state within %s", timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// StopServices stops each service and the services that depend on it.
func (replWindowsPlatform) StopServices(names []string, log func(string)) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	var stop func(name string, depth int) error
	stop = func(name string, depth int) error {
		service, err := manager.OpenService(name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		defer service.Close()
		if status, err := service.Query(); err == nil && status.State == svc.Stopped {
			return nil
		}
		if depth < 4 {
			if dependents, err := service.ListDependentServices(svc.Active); err == nil {
				for _, dependent := range dependents {
					if err := stop(dependent, depth+1); err != nil {
						return err
					}
				}
			}
		}
		if _, err := service.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := replServiceWait(service, svc.Stopped, 3*time.Minute); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		log("Stopped " + name + ".")
		return nil
	}
	for _, name := range names {
		if err := stop(name, 0); err != nil {
			return err
		}
	}
	return nil
}

// StartServices starts the services in reverse order, reporting every
// failure but trying them all.
func (replWindowsPlatform) StartServices(names []string, log func(string)) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	failures := []string{}
	for i := len(names) - 1; i >= 0; i-- {
		service, err := manager.OpenService(names[i])
		if err != nil {
			failures = append(failures, names[i]+": "+err.Error())
			continue
		}
		if err := service.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			failures = append(failures, names[i]+": "+err.Error())
		} else if err := replServiceWait(service, svc.Running, 3*time.Minute); err != nil {
			failures = append(failures, names[i]+": "+err.Error())
		} else {
			log("Started " + names[i] + ".")
		}
		service.Close()
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (replWindowsPlatform) Shutdown(log func(string)) error {
	log("Shutting this server down after cutover.")
	command := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "shutdown.exe"), "/s", "/t", "10", "/d", "p:4:1", "/c", "Replication cutover finished; the new server takes over.")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func replAddFirewallRule(port int) error {
	_ = vmFirewall("delete", "rule", "name="+replFirewallRule) // only this agent's own rule
	return vmFirewall("add", "rule", "name="+replFirewallRule, "dir=in", "action=allow", "protocol=TCP", "localport="+strconv.Itoa(port), "profile=any")
}

// replAgentService is the Windows service handler.
type replAgentService struct{}

func (replAgentService) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	events, _ := eventlog.Open(replEventSource)
	logLine := func(text string) {
		if events != nil {
			events.Info(1000, text)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- replRunWindowsAgent(ctx, events, logLine) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil && events != nil {
				events.Error(1010, "The replication agent stopped: "+err.Error())
			}
			if events != nil {
				events.Close()
			}
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
			}
		}
	}
}

func replRunWindowsAgent(ctx context.Context, events *eventlog.Log, log func(string)) error {
	dir := replAgentDataDir()
	if err := replProtectFolder(dir); err != nil {
		return fmt.Errorf("the data folder could not be protected: %w", err)
	}
	config, err := loadReplAgentConfig(dir)
	if err != nil {
		return err
	}
	eventlog.InstallAsEventCreate(replEventSource, eventlog.Error|eventlog.Warning|eventlog.Info) // fails harmlessly when it exists
	if err := replAddFirewallRule(config.Port); err != nil {
		log("The firewall rule could not be added: " + err.Error())
	}
	notify := func(alert replAlert) {
		if events == nil {
			return
		}
		id := map[string]uint32{"rpo": 1001, "failing": 1002, "cutover": 1003}[alert.Kind]
		if alert.Level == "error" {
			events.Error(id, alert.Text)
		} else {
			events.Warning(id, alert.Text)
		}
	}
	err = runReplAgentWith(ctx, dir, replWindowsPlatform{TrackDir: filepath.Join(dir, "Tracking")}, replAgentVersion(), log, notify)
	if _, statErr := os.Stat(filepath.Join(dir, replUninstallTag)); statErr == nil {
		// The console is removing the agent: remove what it added here.
		vmFirewall("delete", "rule", "name="+replFirewallRule)
		eventlog.Remove(replEventSource)
	}
	return err
}

// replInstallLocal installs this executable as the agent service on this
// computer and prints the key and thumbprint to enter in the console.
func replInstallLocal(port int, out io.Writer) error {
	if !isAdministrator() {
		return errors.New("run the command from an administrator prompt")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	folder := filepath.Join(programFiles, replAgentFolderName)
	if err := os.MkdirAll(folder, 0755); err != nil {
		return err
	}
	target := filepath.Join(folder, replAgentExeName)
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(target)) {
		if err := replCopyFile(self, target); err != nil {
			return fmt.Errorf("copying the agent: %w", err)
		}
	}
	key := replNewAgentKey()
	dir := replAgentDataDir()
	if err := replProtectFolder(dir); err != nil {
		return err
	}
	os.Remove(filepath.Join(dir, replUninstallTag))
	if err := saveReplAgentConfig(dir, replAgentConfig{Port: port, KeyHash: replKeyHash(key), Console: "local install"}); err != nil {
		return err
	}
	_, thumbprint, err := replAgentCertificate(dir)
	if err != nil {
		return err
	}
	if err := replCreateService(nil, target); err != nil {
		return err
	}
	fmt.Fprintf(out, "The replication agent is installed and running on port %d.\n\nIn the console's Replication tab, add this server with:\n  Agent key:              %s\n  Certificate thumbprint: %s\n\nThe key is shown only now; it is not saved on this server.\n", port, key, vmNormalizeThumbprint(thumbprint))
	return nil
}

func replCopyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	temporary := to + ".new"
	out, err := os.Create(temporary)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(temporary)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return replReplaceFile(temporary, to)
}

// replReplaceFile moves a new agent executable into place. A running
// agent's file cannot be overwritten, but it can be renamed aside.
func replReplaceFile(temporary, to string) error {
	if err := os.Rename(temporary, to); err == nil {
		return nil
	}
	old := to + ".old"
	os.Remove(old)
	if err := os.Rename(to, old); err != nil && !os.IsNotExist(err) {
		os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, to)
}

// replCreateService creates (or updates) and starts the agent service on
// this computer, or on the computer manager is connected to.
func replCreateService(manager *mgr.Mgr, binary string) error {
	if manager == nil {
		local, err := mgr.Connect()
		if err != nil {
			return err
		}
		defer local.Disconnect()
		manager = local
		eventlog.InstallAsEventCreate(replEventSource, eventlog.Error|eventlog.Warning|eventlog.Info)
	}
	command := `"` + binary + `" --agent-service`
	service, err := manager.OpenService(replServiceName)
	if err == nil {
		// Already installed: stop it, point it at this binary, start it.
		service.Control(svc.Stop)
		replServiceWait(service, svc.Stopped, time.Minute)
		config, err := service.Config()
		if err == nil {
			config.BinaryPathName = command
			config.StartType = mgr.StartAutomatic
			config.DisplayName = replServiceDisplay
			service.UpdateConfig(config)
		}
	} else {
		service, err = manager.CreateService(replServiceName, binary, mgr.Config{
			DisplayName:      replServiceDisplay,
			Description:      "Replicates this server's disks for migration. Installed and managed by Server Inventory and Migration Manager.",
			StartType:        mgr.StartAutomatic,
			DelayedAutoStart: true,
		}, "--agent-service")
		if err != nil {
			return fmt.Errorf("creating the service: %w", err)
		}
		service.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: time.Minute}, {Type: mgr.ServiceRestart, Delay: 5 * time.Minute}}, 24*60*60)
	}
	defer service.Close()
	if err := service.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("starting the service: %w", err)
	}
	return replServiceWait(service, svc.Running, 2*time.Minute)
}

// replRemoveService stops and deletes the agent service; the data folder's
// uninstall flag tells the stopping agent to remove its firewall rule.
func replRemoveService(manager *mgr.Mgr, dataDir string) error {
	os.WriteFile(filepath.Join(dataDir, replUninstallTag), []byte("uninstall"), 0600)
	service, err := manager.OpenService(replServiceName)
	if err != nil {
		return nil // not installed
	}
	defer service.Close()
	service.Control(svc.Stop)
	replServiceWait(service, svc.Stopped, 2*time.Minute)
	return service.Delete()
}

func runReplAgentCommand(args []string) int {
	if args[0] != "--agent-service" {
		vmReceiverConsole()
	}
	switch args[0] {
	case "--agent-service":
		if inService, _ := svc.IsWindowsService(); inService {
			if err := svc.Run(replServiceName, replAgentService{}); err != nil {
				return 1
			}
			return 0
		}
		fmt.Fprintln(os.Stderr, "The service manager starts --agent-service. To try the agent in this window, use --agent.")
		return 2
	case "--agent":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		var mu sync.Mutex
		log := func(text string) { mu.Lock(); fmt.Println(time.Now().Format("15:04:05"), text); mu.Unlock() }
		if err := runReplAgentWith(ctx, replAgentDataDir(), replWindowsPlatform{TrackDir: filepath.Join(replAgentDataDir(), "Tracking")}, replAgentVersion(), log, func(alert replAlert) { log("ALERT: " + alert.Text) }); err != nil {
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
		if err := replInstallLocal(*port, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "The agent was not installed:", err)
			return 1
		}
		return 0
	case "--agent-uninstall":
		manager, err := mgr.Connect()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer manager.Disconnect()
		if err := replRemoveService(manager, replAgentDataDir()); err != nil {
			fmt.Fprintln(os.Stderr, "The agent was not removed:", err)
			return 1
		}
		os.RemoveAll(replAgentDataDir())
		fmt.Println("The replication agent was removed. Its program folder can now be deleted.")
		return 0
	}
	fmt.Fprintln(os.Stderr, "Use --agent-install [--port N], --agent-uninstall or --agent.")
	return 2
}
