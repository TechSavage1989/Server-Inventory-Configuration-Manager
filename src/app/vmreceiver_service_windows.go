//go:build windows

package main

// The disk receiver as a Windows service (2026-11-26, owner: replication
// jobs must survive reboots of the target). The operator turns it on in the
// window on the receiving computer itself ("Keep receiving after a reboot
// and while this application is closed"); it is never installed remotely.
// While the application is open the window's receiver receives as before
// (pairing approvals, transfers, failover holds); when the application
// closes while receiving, the service takes over with the same folder,
// port, certificate and pairing code, and starts with Windows. Opening the
// application again hands receiving back to the window. The service starts
// no PowerShell and uses only netsh for its own two fixed firewall rules.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	vmReceiverServiceName    = "SIMMReceiver"
	vmReceiverServiceDisplay = "Server Inventory and Migration Manager disk receiver"
	vmReceiverServiceFolder  = "Server Inventory and Migration Manager Receiver"
	vmReceiverServiceExe     = "SIMM-Receiver.exe"
)

// vmReceiverServiceConfig is what the service receives with; no secret.
type vmReceiverServiceConfig struct {
	Folder       string `json:"folder"`
	Port         int    `json:"port"`
	Firewall     bool   `json:"firewall"`
	Discoverable bool   `json:"discoverable"`
}

// vmReceiverServiceHold is one failover hold the service applies at start.
type vmReceiverServiceHold struct {
	Names  []string `json:"names"`
	Reason string   `json:"reason"`
}

func vmReceiverServiceDataDir() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, vmReceiverServiceFolder)
}

func vmReceiverServiceBinary() string {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	return filepath.Join(programFiles, vmReceiverServiceFolder, vmReceiverServiceExe)
}

// The service runs as LocalSystem, so its identity is sealed for this
// computer (not one user) inside a folder only SYSTEM and Administrators
// can read.
func vmSealMachine(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("nothing to protect")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN|windows.CRYPTPROTECT_LOCAL_MACHINE, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

// vmReceiverServiceStatus says whether the service is installed and running.
// It asks only for status, so it works without administrator rights.
func vmReceiverServiceStatus() (installed, running bool) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false, false
	}
	defer windows.CloseServiceHandle(manager)
	name, _ := windows.UTF16PtrFromString(vmReceiverServiceName)
	handle, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false, false
	}
	defer windows.CloseServiceHandle(handle)
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(handle, &status); err != nil {
		return true, false
	}
	return true, status.CurrentState == windows.SERVICE_RUNNING || status.CurrentState == windows.SERVICE_START_PENDING
}

// vmWriteReceiverServiceFiles writes the settings, the machine-sealed
// identity and the failover holds the service starts with.
func vmWriteReceiverServiceFiles(config vmReceiverServiceConfig, identity *vmReceiverIdentity, holds []vmReceiverServiceHold) error {
	dir := vmReceiverServiceDataDir()
	if err := replProtectFolder(dir); err != nil {
		return fmt.Errorf("the service's data folder could not be protected: %w", err)
	}
	if identity != nil {
		if err := vmSaveReceiverIdentity(filepath.Join(dir, "identity.json"), identity, vmSealMachine); err != nil {
			return err
		}
	}
	for name, value := range map[string]any{"service.json": config, "holds.json": holds} {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name+".tmp"), data, 0600); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(dir, name+".tmp"), filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// vmInstallReceiverService copies this program and creates the service. It
// is left stopped: the window keeps receiving until the application closes.
func vmInstallReceiverService(config vmReceiverServiceConfig, identity *vmReceiverIdentity, holds []vmReceiverServiceHold) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return vmInstallReceiverServiceFrom(self, config, identity, holds)
}

// vmInstallReceiverServiceFrom installs the program at self as the service.
func vmInstallReceiverServiceFrom(self string, config vmReceiverServiceConfig, identity *vmReceiverIdentity, holds []vmReceiverServiceHold) error {
	if !isAdministrator() {
		return errors.New("run the application as administrator to keep receiving after a reboot")
	}
	if err := vmWriteReceiverServiceFiles(config, identity, holds); err != nil {
		return err
	}
	binary := vmReceiverServiceBinary()
	if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		return err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	if service, err := manager.OpenService(vmReceiverServiceName); err == nil {
		service.Control(svc.Stop)
		replServiceWait(service, svc.Stopped, time.Minute)
		service.Close()
	}
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(binary)) {
		if err := replCopyFile(self, binary); err != nil {
			return fmt.Errorf("copying the receiver: %w", err)
		}
	}
	service, err := manager.OpenService(vmReceiverServiceName)
	if err == nil {
		current, err := service.Config()
		if err == nil {
			current.BinaryPathName = `"` + binary + `" --receiver-service`
			current.StartType = mgr.StartAutomatic
			current.DisplayName = vmReceiverServiceDisplay
			service.UpdateConfig(current)
		}
	} else {
		service, err = manager.CreateService(vmReceiverServiceName, binary, mgr.Config{
			DisplayName: vmReceiverServiceDisplay,
			Description: "Receives disk copies for migration and replication while Server Inventory and Migration Manager is closed, and after a restart. Turned on and off in that application on this computer.",
			StartType:   mgr.StartAutomatic,
		}, "--receiver-service")
		if err != nil {
			return fmt.Errorf("creating the service: %w", err)
		}
	}
	defer service.Close()
	replSetRecovery(service)
	return nil
}

// vmSetReceiverServiceAutomatic turns starting with Windows on (receiving)
// or off (the operator stopped receiving).
func vmSetReceiverServiceAutomatic(automatic bool) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(vmReceiverServiceName)
	if err != nil {
		return err
	}
	defer service.Close()
	config, err := service.Config()
	if err != nil {
		return err
	}
	config.StartType = mgr.StartManual
	if automatic {
		config.StartType = mgr.StartAutomatic
	}
	return service.UpdateConfig(config)
}

// vmStartReceiverService starts the service (the application is closing
// while receiving) or stops it (the window takes over).
func vmControlReceiverService(start bool) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(vmReceiverServiceName)
	if err != nil {
		return err
	}
	defer service.Close()
	if start {
		if err := service.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return err
		}
		return replServiceWait(service, svc.Running, time.Minute)
	}
	if _, err := service.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	return replServiceWait(service, svc.Stopped, time.Minute)
}

// vmRemoveReceiverService stops and deletes the service and removes its
// program and data folders. The service removes its firewall rules as it
// stops; they are removed here too in case it was not running.
func vmRemoveReceiverService() error {
	if !isAdministrator() {
		return errors.New("run the application as administrator to remove the receiver service")
	}
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	if service, err := manager.OpenService(vmReceiverServiceName); err == nil {
		service.Control(svc.Stop)
		replServiceWait(service, svc.Stopped, 2*time.Minute)
		err := service.Delete()
		service.Close()
		if err != nil {
			return fmt.Errorf("deleting the service: %w", err)
		}
	}
	os.RemoveAll(vmReceiverServiceDataDir())
	binary := vmReceiverServiceBinary()
	for attempt := 0; attempt < 10; attempt++ {
		if err := os.RemoveAll(filepath.Dir(binary)); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// vmReceiverServiceHandler runs the receiver under the service manager.
type vmReceiverServiceHandler struct{}

func (vmReceiverServiceHandler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	dir := vmReceiverServiceDataDir()
	replRedirectCrashOutput(dir)
	logs := filepath.Join(dir, "Logs")
	os.MkdirAll(logs, 0700)
	var mu sync.Mutex
	logFile, _ := os.OpenFile(filepath.Join(logs, "receiver.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	log := func(text string) {
		mu.Lock()
		defer mu.Unlock()
		if logFile != nil {
			fmt.Fprintln(logFile, time.Now().Format("2006-01-02 15:04:05"), text)
		}
	}
	stop, err := vmRunReceiverService(dir, log)
	if err != nil {
		log("The receiver service could not start: " + err.Error())
		if logFile != nil {
			logFile.Close()
		}
		return true, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			status <- request.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			stop()
			log("The receiver service stopped.")
			if logFile != nil {
				logFile.Close()
			}
			return false, 0
		}
	}
	return false, 0
}

// vmRunReceiverService starts the receiver from the service's files and
// returns the function that stops it.
func vmRunReceiverService(dir string, log func(string)) (func(), error) {
	data, err := os.ReadFile(filepath.Join(dir, "service.json"))
	if err != nil {
		return nil, fmt.Errorf("its settings could not be read: %w", err)
	}
	var config vmReceiverServiceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	identity, err := vmLoadReceiverIdentity(filepath.Join(dir, "identity.json"), replUnseal)
	if err != nil {
		return nil, fmt.Errorf("its certificate and pairing code could not be read: %w", err)
	}
	receiverConfig := vmReceiverConfig{Folder: config.Folder, Identity: identity, Version: appVersion, FreeBytes: func() uint64 {
		free, _, _ := vmFreeSpace(config.Folder)
		return free
	}}
	if config.Discoverable {
		receiverConfig.Discovery = ":" + strconv.Itoa(vmDiscoveryPort)
	}
	r, err := startVMReceiverWith(":"+strconv.Itoa(config.Port), receiverConfig, log)
	if err != nil {
		return nil, err
	}
	var holds []vmReceiverServiceHold
	if data, err := os.ReadFile(filepath.Join(dir, "holds.json")); err == nil {
		json.Unmarshal(data, &holds)
	}
	for _, hold := range holds {
		if err := r.Hold(hold.Names, hold.Reason); err != nil {
			log("A copy could not be held: " + err.Error())
		}
	}
	if config.Firewall {
		if err := vmAddReceiverFirewallRule(config.Port, r.discovery != nil); err != nil {
			log("The firewall port could not be opened: " + err.Error())
		}
	}
	log(fmt.Sprintf("Receiving on port %d into %s with the same certificate (fingerprint %s) as the application's receiver.", config.Port, config.Folder, vmFingerprint(identity.Thumbprint)))
	return func() {
		r.Stop()
		if config.Firewall {
			vmRemoveReceiverFirewallRule()
		}
	}, nil
}

func runVMReceiverServiceCommand(args []string) int {
	switch args[0] {
	case "--receiver-service":
		if inService, _ := svc.IsWindowsService(); inService {
			if err := svc.Run(vmReceiverServiceName, vmReceiverServiceHandler{}); err != nil {
				return 1
			}
			return 0
		}
		fmt.Fprintln(os.Stderr, "The service manager starts --receiver-service. Turn the service on or off in the application's Receive panel.")
		return 2
	case "--receiver-service-uninstall":
		vmReceiverConsole()
		if err := vmRemoveReceiverService(); err != nil {
			fmt.Fprintln(os.Stderr, "The receiver service was not removed:", err)
			return 1
		}
		fmt.Println("The receiver service, its firewall rules and its folders were removed.")
		return 0
	}
	return 2
}
