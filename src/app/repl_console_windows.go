//go:build windows

package main

// The Replication tab's handlers: the central console that installs agents
// on many servers at once, adds agents installed by hand, and creates,
// watches and controls their replication jobs.
//
// Push install works like other Windows management tools: it connects to
// the server's IPC$ share (with the account typed, or the signed-in user),
// copies this executable into Program Files through the administrative
// share, writes the agent's settings (only the SHA-256 of a new agent key),
// and creates and starts the agent service through the remote service
// manager. The agent's certificate thumbprint is then read back over the
// same authenticated share, so the console pins the right certificate
// without trusting anything on first use. The account is used for that
// push only and is never saved or logged.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

var (
	replMpr                   = windows.NewLazySystemDLL("mpr.dll")
	replWNetAddConnection2    = replMpr.NewProc("WNetAddConnection2W")
	replWNetCancelConnection2 = replMpr.NewProc("WNetCancelConnection2W")
	replNetapi                = windows.NewLazySystemDLL("netapi32.dll")
	replNetShareGetInfo       = replNetapi.NewProc("NetShareGetInfo")
	replHostPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
)

type replRuntime struct {
	console *replConsole
	loadErr string
	pushing bool
	failing bool // a VM is being started from copies, or a test ended
	// Test failover on a target platform (repl_targettest_windows.go).
	ttRunning bool
	ttLog     []string
	ttError   string
	ttCancel  context.CancelFunc
	runbook   string
	pushLog   []string
	pushDone  []replPushResult
	serial    int
	cancel    context.CancelFunc
	refreshed time.Time
}

type replPushResult struct {
	Host  string `json:"host"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// HostKey is an SSH host key to trust before retrying, and Changed
	// says it differs from the one trusted.
	HostKey string `json:"hostKey,omitempty"`
	Changed bool   `json:"changed,omitempty"`
}

func replSeal(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("nothing to protect")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func replUnseal(sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, errors.New("nothing to read")
	}
	in := windows.DataBlob{Size: uint32(len(sealed)), Data: &sealed[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

// replConsoleLocked opens the console's records the first time. Caller holds a.mu.
func (a *application) replConsoleLocked() *replConsole {
	if a.repl.console == nil && a.repl.loadErr == "" {
		console, err := newReplConsole(filepath.Join(a.update.appDataRoot, "Replication", "agents.json"), replSeal, replUnseal)
		if err != nil {
			a.repl.loadErr = err.Error()
			return nil
		}
		a.repl.console = console
		go a.replPoll()
	}
	return a.repl.console
}

// replPoll refreshes every agent while the application runs.
func (a *application) replPoll() {
	for {
		a.mu.Lock()
		console, closing := a.repl.console, a.closing
		a.mu.Unlock()
		if closing {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		console.Refresh(ctx)
		cancel()
		a.replKeepLocalReceiver(console)
		a.mu.Lock()
		a.repl.refreshed = time.Now()
		a.mu.Unlock()
		time.Sleep(replPollInterval)
	}
}

type replNetResource struct {
	Scope, Type, DisplayType, Usage          uint32
	LocalName, RemoteName, Comment, Provider *uint16
}

// replConnectIPC opens an authenticated session to \\host\IPC$ with the
// account given; the returned function closes it.
func replConnectIPC(host, user, password string) (func(), error) {
	if user == "" {
		return func() {}, nil
	}
	remote, _ := windows.UTF16PtrFromString(`\\` + host + `\IPC$`)
	userPtr, _ := windows.UTF16PtrFromString(user)
	passPtr, _ := windows.UTF16PtrFromString(password)
	resource := replNetResource{RemoteName: remote}
	result, _, _ := replWNetAddConnection2.Call(uintptr(unsafe.Pointer(&resource)), uintptr(unsafe.Pointer(passPtr)), uintptr(unsafe.Pointer(userPtr)), 0)
	switch windows.Errno(result) {
	case 0:
	case windows.Errno(1219):
		return nil, errors.New("this computer already has a connection to " + host + " under another account; leave the account blank to use it, or close that connection (net use) first")
	case windows.ERROR_ACCESS_DENIED, windows.Errno(1326):
		return nil, errors.New("the account or password was refused by " + host)
	default:
		return nil, fmt.Errorf("could not connect to %s: %w", host, windows.Errno(result))
	}
	return func() { replWNetCancelConnection2.Call(uintptr(unsafe.Pointer(remote)), 0, 1) }, nil
}

// replIPCShared counts the users of each run-scoped IPC$ connection, so
// several copy rounds to the same host (a migration plan copies VMs in
// parallel) share one connection and the last to finish removes it.
var replIPCShared = struct {
	sync.Mutex
	hosts map[string]*replIPCEntry
}{hosts: map[string]*replIPCEntry{}}

type replIPCEntry struct {
	user  string
	count int
	close func()
}

// replConnectIPCShared is replConnectIPC for callers that may overlap.
func replConnectIPCShared(host, user, password string) (func(), error) {
	if user == "" {
		return func() {}, nil
	}
	key := strings.ToLower(strings.TrimSpace(host))
	replIPCShared.Lock()
	defer replIPCShared.Unlock()
	entry := replIPCShared.hosts[key]
	if entry != nil && !strings.EqualFold(entry.user, user) {
		return nil, errors.New("this computer is already connected to " + host + " under another account for another copy; wait for it to finish")
	}
	if entry == nil {
		closer, err := replConnectIPC(host, user, password)
		if err != nil {
			return nil, err
		}
		entry = &replIPCEntry{user: user, close: closer}
		replIPCShared.hosts[key] = entry
	}
	entry.count++
	var once sync.Once
	return func() {
		once.Do(func() {
			replIPCShared.Lock()
			defer replIPCShared.Unlock()
			entry.count--
			if entry.count == 0 {
				entry.close()
				if replIPCShared.hosts[key] == entry {
					delete(replIPCShared.hosts, key)
				}
			}
		})
	}, nil
}

// replSystemDrive asks the server where ADMIN$ points (its Windows folder)
// and returns that drive's letter.
func replSystemDrive(host string) (string, error) {
	server, _ := windows.UTF16PtrFromString(`\\` + host)
	share, _ := windows.UTF16PtrFromString("ADMIN$")
	var buffer *byte
	result, _, _ := replNetShareGetInfo.Call(uintptr(unsafe.Pointer(server)), uintptr(unsafe.Pointer(share)), 2, uintptr(unsafe.Pointer(&buffer)))
	if result != 0 {
		return "", fmt.Errorf("the administrative share of %s could not be read (%w); check that you are an administrator there and that File and Printer Sharing is allowed", host, windows.Errno(result))
	}
	defer windows.NetApiBufferFree(buffer)
	type shareInfo2 struct {
		Name        *uint16
		Type        uint32
		Remark      *uint16
		Permissions uint32
		MaxUses     uint32
		CurrentUses uint32
		Path        *uint16
		Password    *uint16
	}
	info := (*shareInfo2)(unsafe.Pointer(buffer))
	path := windows.UTF16PtrToString(info.Path)
	if len(path) < 2 || path[1] != ':' {
		return "", fmt.Errorf("the Windows folder of %s is %q, not on a drive", host, path)
	}
	return strings.ToUpper(path[:1]), nil
}

// replPushOne installs or updates the agent on one server.
func (a *application) replPushOne(ctx context.Context, host, user, password string, port int, log func(string)) error {
	disconnect, err := replConnectIPC(host, user, password)
	if err != nil {
		return err
	}
	defer disconnect()
	drive, err := replSystemDrive(host)
	if err != nil {
		return err
	}
	unc := `\\` + host + `\` + drive + `$`
	programDir := filepath.Join(unc, "Program Files", replAgentFolderName)
	dataDir := filepath.Join(unc, "ProgramData", replAgentFolderName)
	for _, dir := range []string{programDir, dataDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("could not create %s: %w", dir, err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	log(fmt.Sprintf("%s: copying the agent.", host))
	if err := replCopyFile(self, filepath.Join(programDir, replAgentExeName)); err != nil {
		return fmt.Errorf("copying the agent: %w", err)
	}
	key := replNewAgentKey()
	os.Remove(filepath.Join(dataDir, replUninstallTag))
	thisComputer, _ := os.Hostname()
	if err := saveReplAgentConfig(dataDir, replAgentConfig{Port: port, KeyHash: replKeyHash(key), Console: thisComputer}); err != nil {
		return fmt.Errorf("writing the agent settings: %w", err)
	}
	log(fmt.Sprintf("%s: creating and starting the agent service.", host))
	manager, err := mgr.ConnectRemote(host)
	if err != nil {
		return fmt.Errorf("the service manager of %s could not be reached: %w", host, err)
	}
	err = replCreateService(manager, drive+`:\Program Files\`+replAgentFolderName+`\`+replAgentExeName)
	manager.Disconnect()
	if err != nil {
		return err
	}
	thumbprint := ""
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if raw, err := os.ReadFile(filepath.Join(dataDir, replThumbName)); err == nil {
			thumbprint = strings.TrimSpace(string(raw))
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if vmNormalizeThumbprint(thumbprint) == "" {
		return errors.New("the agent started but did not write its certificate thumbprint; check the Application event log on " + host)
	}
	_, err = a.replRegister(ctx, host, port, thumbprint, key, log)
	return err
}

// replRegister adds a newly installed agent to the console, waiting for it
// to answer.
func (a *application) replRegister(ctx context.Context, host string, port int, thumbprint, key string, log func(string)) (*replAgentRecord, error) {
	a.mu.Lock()
	console := a.replConsoleLocked()
	a.mu.Unlock()
	if console == nil {
		return nil, errors.New("the console's records could not be opened")
	}
	var last error
	for attempt := 0; attempt < 15; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		record, err := console.Add(callCtx, net.JoinHostPort(host, strconv.Itoa(port)), thumbprint, key, true)
		cancel()
		if err == nil {
			log(fmt.Sprintf("%s: the agent is installed and answering.", host))
			return record, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		time.Sleep(4 * time.Second)
	}
	return nil, fmt.Errorf("the agent is installed but does not answer on port %d (%v); check that the port is open between this computer and %s", port, last, host)
}

// replPushLinux installs the agent on a Linux server over SSH.
func (a *application) replPushLinux(ctx context.Context, target replSSHTarget, agent []byte, port int, log func(string)) error {
	key, thumbprint, err := replSSHInstallAgent(ctx, target, agent, port, log)
	if err != nil {
		return err
	}
	record, err := a.replRegister(ctx, target.Host, port, thumbprint, key, log)
	if err != nil {
		return err
	}
	a.mu.Lock()
	console := a.replConsoleLocked()
	a.mu.Unlock()
	if console != nil {
		console.SetSSH(record.ID, target.Port, target.HostKey)
	}
	return nil
}

// replUninstallOne removes the agent from one server.
func replUninstallOne(host, user, password string) error {
	disconnect, err := replConnectIPC(host, user, password)
	if err != nil {
		return err
	}
	defer disconnect()
	drive, err := replSystemDrive(host)
	if err != nil {
		return err
	}
	unc := `\\` + host + `\` + drive + `$`
	dataDir := filepath.Join(unc, "ProgramData", replAgentFolderName)
	manager, err := mgr.ConnectRemote(host)
	if err != nil {
		return fmt.Errorf("the service manager of %s could not be reached: %w", host, err)
	}
	err = replRemoveService(manager, dataDir)
	manager.Disconnect()
	if err != nil {
		return err
	}
	programDir := filepath.Join(unc, "Program Files", replAgentFolderName)
	for attempt := 0; attempt < 10; attempt++ {
		os.RemoveAll(dataDir)
		os.RemoveAll(programDir)
		if _, err := os.Stat(programDir); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("the agent service was removed, but its program folder is still in use; delete " + programDir + " later")
}

func replSplitHosts(text string) ([]string, error) {
	hosts := []string{}
	seen := map[string]bool{}
	for _, field := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t' }) {
		host := strings.TrimPrefix(strings.TrimSpace(field), `\\`)
		if host == "" {
			continue
		}
		if !replHostPattern.MatchString(host) {
			return nil, fmt.Errorf("%q is not a server name or IPv4 address", host)
		}
		if !seen[strings.ToLower(host)] {
			seen[strings.ToLower(host)] = true
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil, errors.New("enter at least one server name")
	}
	if len(hosts) > 100 {
		return nil, errors.New("install on at most 100 servers at a time")
	}
	return hosts, nil
}

func (a *application) replLog(text string) {
	a.mu.Lock()
	line := time.Now().Format("15:04:05") + "  " + text
	a.repl.pushLog = append(a.repl.pushLog, line)
	if len(a.repl.pushLog) > 300 {
		a.repl.pushLog = a.repl.pushLog[len(a.repl.pushLog)-300:]
	}
	a.mu.Unlock()
}

func (a *application) handleReplState(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	a.mu.Lock()
	console := a.replConsoleLocked()
	state := map[string]any{"ok": true, "pushing": a.repl.pushing, "pushLog": append([]string(nil), a.repl.pushLog...), "pushResults": a.repl.pushDone, "serial": a.repl.serial, "isAdministrator": isAdministrator(), "error": a.repl.loadErr, "defaultPort": replAgentPort, "local": a.replLocalStateLocked()}
	if !a.repl.refreshed.IsZero() {
		state["refreshed"] = a.repl.refreshed.UTC()
	}
	a.mu.Unlock()
	rows, summary := []replAgentRow{}, replSummary{}
	if console != nil {
		rows, summary = console.Rows()
	}
	state["agents"], state["summary"], state["now"] = rows, summary, time.Now().UTC()
	writeJSON(response, http.StatusOK, state)
}

// handleReplRunbook writes the replication runbook (HTML and Word) for every
// job the console sees into a new folder in the output folder.
func (a *application) handleReplRunbook(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) || !decodeVMwareRequest(response, request, nil) {
		return
	}
	console := a.replConsoleFor(response)
	if console == nil {
		return
	}
	rows, _ := console.Rows()
	folder := a.vmwareOutputFolder("Replication")
	htmlPath, docxPath, err := writeReplRunbook(folder, rows, time.Now(), appVersion)
	if err != nil {
		a.replFail(response, http.StatusBadRequest, "The runbook could not be written: "+err.Error()+".")
		return
	}
	a.mu.Lock()
	a.repl.runbook = htmlPath
	a.mu.Unlock()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "runbookPath": htmlPath, "docxPath": docxPath})
}

func (a *application) replFail(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]any{"ok": false, "error": message})
}

func (a *application) replConsoleFor(response http.ResponseWriter) *replConsole {
	a.mu.Lock()
	console, loadErr := a.replConsoleLocked(), a.repl.loadErr
	a.mu.Unlock()
	if console == nil {
		a.replFail(response, http.StatusInternalServerError, "The console's records could not be opened: "+loadErr)
	}
	return console
}

func (a *application) handleReplPush(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Hosts    string `json:"hosts"`
		User     string `json:"user"`
		Password string `json:"password"`
		Port     int    `json:"port"`
		// Linux servers are reached over SSH.
		Platform string            `json:"platform"`
		SSHPort  int               `json:"sshPort"`
		HostKeys map[string]string `json:"hostKeys"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	hosts, err := replSplitHosts(input.Hosts)
	if err != nil {
		a.replFail(response, http.StatusBadRequest, "The servers were not accepted: "+err.Error()+".")
		return
	}
	port := input.Port
	if port == 0 {
		port = replAgentPort
	}
	if port < 1024 || port > 65535 {
		a.replFail(response, http.StatusBadRequest, "Choose an agent port from 1024 to 65535.")
		return
	}
	user, password := strings.TrimSpace(input.User), input.Password
	linux := input.Platform == "linux"
	var linuxAgent []byte
	sshPort := input.SSHPort
	if linux {
		if user == "" || password == "" {
			a.replFail(response, http.StatusBadRequest, "Type the Linux account (root, or an account that may use sudo) and its password.")
			return
		}
		if sshPort == 0 {
			sshPort = 22
		}
		if sshPort < 1 || sshPort > 65535 {
			a.replFail(response, http.StatusBadRequest, "Choose an SSH port from 1 to 65535.")
			return
		}
		self, err := os.Executable()
		if err == nil {
			linuxAgent, err = replLoadLinuxAgent(filepath.Join(filepath.Dir(self), replLinuxAgentName))
		}
		if err != nil {
			a.replFail(response, http.StatusBadRequest, err.Error()+".")
			return
		}
	} else if user != "" && password == "" {
		a.replFail(response, http.StatusBadRequest, "Type the account's password, or leave the account blank to use your own sign-in.")
		return
	}
	if a.replConsoleFor(response) == nil {
		return
	}
	if !a.beginCriticalOperation() {
		a.replFail(response, http.StatusConflict, "An update is being installed.")
		return
	}
	a.mu.Lock()
	if a.repl.pushing || a.closing {
		a.mu.Unlock()
		a.endCriticalOperation()
		a.replFail(response, http.StatusConflict, "Agents are already being installed.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.repl.pushing, a.repl.cancel, a.repl.pushDone, a.repl.pushLog = true, cancel, nil, nil
	a.mu.Unlock()
	a.replLog(fmt.Sprintf("Installing the agent on %s.", vmPlural(len(hosts), "server", "servers")))
	go func() {
		defer a.endCriticalOperation()
		defer cancel()
		results := []replPushResult{}
		for _, host := range hosts {
			if ctx.Err() != nil {
				results = append(results, replPushResult{Host: host, Error: "Stopped before this server."})
				continue
			}
			var err error
			if linux {
				err = a.replPushLinux(ctx, replSSHTarget{Host: host, Port: sshPort, User: user, Password: password, HostKey: input.HostKeys[host]}, linuxAgent, port, a.replLog)
			} else {
				err = a.replPushOne(ctx, host, user, password, port, a.replLog)
			}
			result := replPushResult{Host: host, OK: err == nil}
			if err != nil {
				result.Error = err.Error()
				var untrusted *replUntrustedHostKey
				if errors.As(err, &untrusted) {
					result.HostKey, result.Changed = untrusted.Fingerprint, untrusted.Changed
				}
				a.replLog(host + ": " + err.Error())
			}
			results = append(results, result)
		}
		a.mu.Lock()
		a.repl.pushing, a.repl.cancel, a.repl.pushDone = false, nil, results
		a.repl.serial++
		a.mu.Unlock()
		a.replLog("Finished installing agents.")
	}()
	writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *application) handleReplAgent(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Action     string `json:"action"` // add, forget, uninstall, stop-push, refresh, disks
		ID         string `json:"id"`
		Address    string `json:"address"`
		Thumbprint string `json:"thumbprint"`
		Key        string `json:"key"`
		Code       string `json:"code"`
		User       string `json:"user"`
		Password   string `json:"password"`
		HostKey    string `json:"hostKey"`
		// Alternates are the receiver's other addresses (receiverdisks).
		Alternates []string `json:"alternates"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	console := a.replConsoleFor(response)
	if console == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	switch input.Action {
	case "add":
		record, err := console.Add(ctx, input.Address, input.Thumbprint, input.Key, false)
		if err != nil {
			a.replFail(response, http.StatusBadRequest, "The agent was not added: "+err.Error()+".")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "id": record.ID, "name": record.Name})
	case "forget":
		if err := console.Forget(input.ID); err != nil {
			a.replFail(response, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "uninstall":
		record, ok := console.Record(input.ID)
		if !ok {
			a.replFail(response, http.StatusBadRequest, "That agent is not listed.")
			return
		}
		host := record.Address
		if index := strings.LastIndex(host, ":"); index > 0 {
			host = strings.Trim(host[:index], "[]")
		}
		var err error
		if record.OS == "Linux" {
			hostKey := record.SSHHostKey
			if input.HostKey != "" {
				hostKey = input.HostKey
			}
			err = replSSHUninstallAgent(ctx, replSSHTarget{Host: host, Port: record.SSHPort, User: strings.TrimSpace(input.User), Password: input.Password, HostKey: hostKey})
		} else {
			err = replUninstallOne(host, strings.TrimSpace(input.User), input.Password)
		}
		if err != nil {
			a.replFail(response, http.StatusBadRequest, "The agent was not removed from "+host+": "+err.Error())
			return
		}
		console.Forget(input.ID)
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "stop-push":
		a.mu.Lock()
		if a.repl.cancel != nil {
			a.repl.cancel()
		}
		a.mu.Unlock()
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "refresh":
		if input.ID != "" {
			console.RefreshOne(ctx, input.ID)
		} else {
			console.Refresh(ctx)
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "receiverdisks":
		address, err := vmReceiverAddress(input.Address)
		if err != nil {
			a.replFail(response, http.StatusBadRequest, "The receiver was not accepted: "+err.Error()+".")
			return
		}
		// The agent sends the copies, so the agent checks: this console may
		// reach an address (a storage or cluster network) the server cannot.
		if input.ID != "" {
			if client, err := console.Client(input.ID); err == nil {
				check, err := client.CheckReceiver(ctx, replReceiverLink{Address: address, Alternates: input.Alternates, Thumbprint: input.Thumbprint, Code: input.Code})
				if err == nil {
					writeJSON(response, http.StatusOK, map[string]any{"ok": true, "folder": check.Folder, "disks": check.Disks, "address": check.Address, "from": "agent", "tried": check.Tried})
					return
				}
				if !errors.Is(err, errReplAgentNoReceiverCheck) {
					a.replFail(response, http.StatusBadRequest, "The server could not reach the receiver: "+err.Error())
					return
				}
			}
		}
		folder, disks, err := vmReceiverDescribe(ctx, address, input.Thumbprint, input.Code)
		if err != nil {
			a.replFail(response, http.StatusBadRequest, "The receiver could not be reached from this computer: "+err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "folder": folder, "disks": disks, "address": address, "from": "console"})
	case "disks":
		client, err := console.Client(input.ID)
		if err == nil {
			var disks []replDiskInfo
			if disks, err = client.Disks(ctx); err == nil {
				writeJSON(response, http.StatusOK, map[string]any{"ok": true, "disks": disks})
				return
			}
		}
		a.replFail(response, http.StatusBadRequest, "The agent's disks could not be listed: "+err.Error())
	default:
		a.replFail(response, http.StatusBadRequest, "Unknown action.")
	}
}

func (a *application) handleReplJob(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	var input struct {
		Agent  string       `json:"agent"`
		Action string       `json:"action"` // save, pause, resume, sync, rescan, cutover, delete, clear, schedule, cancel-cutover, log
		ID     string       `json:"id"`
		Spec   *replJobSpec `json:"spec"`
		At     *time.Time   `json:"at"`
		Typed  string       `json:"typed"`
		// Local sends the job to this computer's receiver (started when
		// needed) in Folder; the pairing code never reaches the page.
		Local  bool   `json:"local"`
		Folder string `json:"folder"`
	}
	if !decodeVMwareRequest(response, request, &input) {
		return
	}
	console := a.replConsoleFor(response)
	if console == nil {
		return
	}
	client, err := console.Client(input.Agent)
	if err != nil {
		a.replFail(response, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer console.RefreshOne(context.Background(), input.Agent)
	switch input.Action {
	case "save":
		if input.Spec == nil {
			a.replFail(response, http.StatusBadRequest, "The job is missing.")
			return
		}
		if input.Local {
			record, _ := console.Record(input.Agent)
			link, err := a.replLocalReceiver(record.Address, input.Folder)
			if err != nil {
				a.replFail(response, http.StatusBadRequest, "The job was not saved: "+err.Error()+".")
				return
			}
			input.Spec.Receiver = link
		}
		view, err := client.SaveJob(ctx, *input.Spec)
		if err != nil {
			a.replFail(response, http.StatusBadRequest, "The job was not saved: "+err.Error()+".")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "job": view})
	case "log":
		lines, err := client.Log(ctx, input.ID)
		if err != nil {
			a.replFail(response, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "lines": lines})
	case "schedule", "cutover", "delete", "finish-by-hand":
		// Changing steps need the job's name typed, as elsewhere.
		jobs, err := client.Jobs(ctx)
		if err != nil {
			a.replFail(response, http.StatusBadRequest, err.Error())
			return
		}
		name := ""
		for _, job := range jobs.Jobs {
			if job.Spec.ID == input.ID {
				name = job.Spec.Name
			}
		}
		if name == "" {
			a.replFail(response, http.StatusBadRequest, "That job no longer exists.")
			return
		}
		if (input.Action != "schedule" || input.At != nil) && !strings.EqualFold(strings.TrimSpace(input.Typed), name) {
			a.replFail(response, http.StatusBadRequest, "Type the job's name, "+name+", to confirm.")
			return
		}
		if input.Action == "schedule" {
			err = client.Schedule(ctx, input.ID, input.At)
		} else {
			err = client.Action(ctx, input.ID, input.Action)
		}
		if err != nil {
			a.replFail(response, http.StatusConflict, err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "pause", "resume", "sync", "rescan", "clear", "cancel-cutover":
		if err := client.Action(ctx, input.ID, input.Action); err != nil {
			a.replFail(response, http.StatusConflict, err.Error())
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	default:
		a.replFail(response, http.StatusBadRequest, "Unknown action.")
	}
}
