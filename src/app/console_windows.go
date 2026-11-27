//go:build windows

package main

// VM console (rule 63): the Windows side. It opens each platform's console
// with the connection the application already holds (the vSphere session's
// account, the connected remote Hyper-V host, the connected Proxmox VE,
// Nutanix or HyperCore target) and serves /api/console/*. Passwords never
// reach the page, logs or files.

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vmware/govmomi/session"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

//go:embed Connect-HyperVConsole.ps1
var consoleHyperVScriptData []byte

const consoleHyperVScriptName = "Connect-HyperVConsole.ps1"

var consoleManagerInit sync.Mutex

// consoleMgr returns this window's console manager, made on first use.
func (a *application) consoleMgr() *consoleManager {
	consoleManagerInit.Lock()
	defer consoleManagerInit.Unlock()
	if a.consoles == nil {
		a.consoles = newConsoleManager()
	}
	return a.consoles
}

func (a *application) handleConsole(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	m := a.consoleMgr()
	switch strings.TrimPrefix(request.URL.Path, "/api/console/") {
	case "stream":
		m.handleStream(response, request)
		return
	case "list":
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "sessions": m.views()})
		return
	}
	if request.Method != http.MethodPost {
		writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "Use POST."})
		return
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/console/") {
	case "open":
		a.handleConsoleOpen(response, request)
	case "close":
		var input struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096)).Decode(&input)
		m.close(input.ID)
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "input":
		m.handleInput(response, request)
	case "keys":
		m.handleKeys(response, request)
	case "type":
		m.handleType(response, request)
	case "file":
		a.handleConsoleFile(response, request)
	case "native":
		a.handleConsoleNative(response, request)
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "Not found."})
	}
}

func (a *application) handleConsoleOpen(response http.ResponseWriter, request *http.Request) {
	var target consoleTarget
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8192)).Decode(&target); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The console request was not understood."})
		return
	}
	target.ID, target.Name, target.Host = strings.TrimSpace(target.ID), strings.TrimSpace(target.Name), strings.TrimSpace(target.Host)
	if err := target.validate(); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// One console per VM: open the existing one again.
	for _, view := range a.consoleMgr().views() {
		if view.Target.Platform == target.Platform && strings.EqualFold(view.Target.ID, target.ID) && strings.EqualFold(view.Target.Host, target.Host) && view.Status == "connected" {
			writeJSON(response, http.StatusOK, map[string]any{"ok": true, "session": view})
			return
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
	defer cancel()
	frame := newConsoleFrame()
	var (
		backend consoleBackend
		caps    consoleCaps
		where   string
		err     error
		rfb     *consoleRFBBackend
	)
	switch target.Platform {
	case "vmware":
		a.mu.Lock()
		options := vmConnectOptions{Server: a.vmware.server, User: a.vmware.account, Password: a.vmware.password, Thumbprint: a.vmware.thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
		a.mu.Unlock()
		if options.Server == "" || options.Password == "" {
			writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": "Sign in to vCenter or ESXi in Discovery first; the console uses that sign-in.", "needSignIn": "vmware"})
			return
		}
		rfb, caps, where, err = consoleOpenVMware(ctx, options, target, frame, nil)
		if rfb != nil {
			backend = rfb
		}
	case "proxmox", "nutanix", "scale":
		a.mu.Lock()
		connection := a.vmware.target.connection
		a.mu.Unlock()
		if connection.Platform != target.Platform || connection.Password == "" {
			writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": "Connect to " + consolePlatforms[target.Platform] + " first (New migration's Where step, or the VM's Move panel); the console uses that connection.", "needSignIn": target.Platform})
			return
		}
		switch target.Platform {
		case "proxmox":
			rfb, caps, where, err = consoleOpenProxmox(ctx, connection, target, frame, nil)
		case "nutanix":
			rfb, caps, where, err = consoleOpenNutanix(ctx, connection, target, frame, nil)
		default:
			rfb, caps, where, err = consoleOpenHyperCore(ctx, connection, target, frame, nil)
		}
		if rfb != nil {
			backend = rfb
		}
	case "hyperv":
		var hv *consoleHyperVBackend
		hv, caps, where, err = a.consoleOpenHyperV(target, frame)
		if hv != nil {
			backend = hv
		}
	}
	if err != nil {
		var certificate *vmCertificateError
		if errors.As(err, &certificate) {
			writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": "The server's certificate is not trusted. Connect again where you signed in and trust its thumbprint first.", "certificate": certificate})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	s, err := a.consoleMgr().start(target, where, caps, frame, backend)
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if rfb != nil {
		rfb.bind(s)
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "session": s.view()})
}

func consoleSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "The console could not be opened."
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

// consoleHyperVAccount picks the account for a Hyper-V host: the connected
// remote host's account when it is that host, otherwise this computer's
// Windows account.
func (a *application) consoleHyperVAccount(host string) (string, string, bool) {
	local := host == "" || strings.EqualFold(host, "localhost") || strings.EqualFold(host, ".") || strings.EqualFold(strings.SplitN(host, ".", 2)[0], os.Getenv("COMPUTERNAME"))
	if local {
		return "", "", true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	remote := a.hvRemote.host
	if remote != "" && a.hvRemote.password != "" {
		matches := strings.EqualFold(remote, host)
		if info := a.hvRemote.info; info != nil {
			for _, node := range info.Nodes {
				if strings.EqualFold(node.Name, host) || strings.EqualFold(strings.SplitN(node.Name, ".", 2)[0], strings.SplitN(host, ".", 2)[0]) {
					matches = true
				}
			}
		}
		if matches {
			return a.hvRemote.user, a.hvRemote.password, false
		}
	}
	return "", "", false
}

func (a *application) consoleScriptPath() (string, error) {
	folder := filepath.Join(a.update.appDataRoot, "Runtime", "Console")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return "", err
	}
	script := filepath.Join(folder, consoleHyperVScriptName)
	if existing, err := os.ReadFile(script); err != nil || !bytes.Equal(existing, consoleHyperVScriptData) {
		if err := os.WriteFile(script, consoleHyperVScriptData, 0600); err != nil {
			return "", err
		}
	}
	return script, nil
}

// consoleHVProcess is Connect-HyperVConsole.ps1 running for one console.
type consoleHVProcess struct {
	mu      sync.Mutex
	command *exec.Cmd
	stdin   io.WriteCloser
	out     *bufio.Reader
	done    bool
}

func (p *consoleHVProcess) call(request map[string]any, answer any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return errors.New("the console has closed")
	}
	line, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		return err
	}
	for {
		reply, err := p.out.ReadString('\n')
		if err != nil {
			return errors.New("the Hyper-V console helper stopped")
		}
		reply = strings.TrimSpace(reply)
		if !strings.HasPrefix(reply, "{") {
			continue // a warning PowerShell printed
		}
		return json.Unmarshal([]byte(reply), answer)
	}
}

func (p *consoleHVProcess) close() error {
	p.mu.Lock()
	if p.done {
		p.mu.Unlock()
		return nil
	}
	p.done = true
	_, _ = io.WriteString(p.stdin, "{\"op\":\"quit\"}\n")
	p.stdin.Close()
	p.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		_ = p.command.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		_ = p.command.Process.Kill()
	}
	return nil
}

// consoleStartHyperV starts the helper; the account goes on standard input.
func (a *application) consoleStartHyperV(host, vmID, mode, user, password string, extra map[string]string) (*consoleHVProcess, error) {
	script, err := a.consoleScriptPath()
	if err != nil {
		return nil, err
	}
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-VMId", vmID, "-Mode", mode}
	if host != "" {
		arguments = append(arguments, "-ComputerName", host)
	}
	first := map[string]string{}
	for k, v := range extra {
		first[k] = v
	}
	if user != "" {
		arguments = append(arguments, "-CredentialFromInput")
		first["user"], first["password"] = user, password
	}
	command := exec.Command(a.powerShellPath, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("Windows PowerShell could not start: %v", err)
	}
	line, _ := json.Marshal(first)
	for k := range first {
		first[k] = ""
	}
	if _, err := stdin.Write(append(line, '\n')); err != nil {
		_ = command.Process.Kill()
		return nil, err
	}
	return &consoleHVProcess{command: command, stdin: stdin, out: bufio.NewReaderSize(stdout, 1<<20)}, nil
}

func (a *application) consoleOpenHyperV(target consoleTarget, frame *consoleFrame) (*consoleHyperVBackend, consoleCaps, string, error) {
	if !consoleGUIDPattern(strings.Trim(target.ID, "{}")) {
		return nil, consoleCaps{}, "", errors.New("the Hyper-V VM ID is not a GUID")
	}
	user, password, local := a.consoleHyperVAccount(target.Host)
	host := target.Host
	if local {
		host = ""
	}
	process, err := a.consoleStartHyperV(host, strings.Trim(target.ID, "{}"), "Console", user, password, nil)
	if err != nil {
		return nil, consoleCaps{}, "", err
	}
	backend, info, err := newConsoleHyperVBackend(process, frame)
	if err != nil {
		return nil, consoleCaps{}, "", err
	}
	// A Hyper-V VM ID is a GUID, so it cannot point at another VM.
	where := "Hyper-V host " + firstNonEmpty(info.Host, host, "this computer")
	if local {
		where = "this Hyper-V host"
	}
	return backend, consoleHyperVCaps(info, local), where, nil
}

// handleConsoleFile copies one file into or out of the VM in the background.
func (a *application) handleConsoleFile(response http.ResponseWriter, request *http.Request) {
	var input struct {
		ID string `json:"id"`
		consoleFileRequest
	}
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16384)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The file request was not understood."})
		return
	}
	s := a.consoleMgr().get(input.ID)
	if s == nil || s.isEnded() {
		writeJSON(response, http.StatusGone, map[string]any{"ok": false, "error": "This console is closed."})
		return
	}
	file := input.consoleFileRequest
	input.Password = ""
	if err := file.validate(); err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	if (file.Direction == "to" && !s.Caps.FilesTo) || (file.Direction == "from" && !s.Caps.FilesFrom) {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": firstNonEmpty(s.Caps.FilesWhy, "This console cannot copy files.")})
		return
	}
	s.mu.Lock()
	if s.file != nil && s.file.Status == "running" {
		s.mu.Unlock()
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "A file is still being copied for this console."})
		return
	}
	s.file = &consoleFileJob{Direction: file.Direction, Local: file.Local, Guest: file.Guest, Status: "running", Message: "Copying.", Started: time.Now()}
	s.mu.Unlock()
	s.poke()
	a.mu.Lock()
	options := vmConnectOptions{Server: a.vmware.server, User: a.vmware.account, Password: a.vmware.password, Thumbprint: a.vmware.thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
	connection := a.vmware.target.connection
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		progress := func(n int64) {
			s.mu.Lock()
			if s.file != nil {
				s.file.Bytes = n
			}
			s.mu.Unlock()
			s.poke()
		}
		var n int64
		var err error
		switch s.Target.Platform {
		case "vmware":
			n, err = consoleVMwareFile(ctx, options, s.Target.ID, file, progress)
		case "proxmox":
			n, err = consoleProxmoxFile(ctx, connection, s.Target.ID, file)
		case "hyperv":
			n, err = a.consoleHyperVFile(s.Target, file)
		default:
			err = errors.New("this platform cannot copy files")
		}
		file.Password = ""
		s.mu.Lock()
		if err != nil {
			s.file.Status, s.file.Message = "failed", consoleSentence(err.Error())
		} else {
			s.file.Status, s.file.Bytes = "done", n
			s.file.Message = fmt.Sprintf("Copied %s.", vmHumanBytes(n))
		}
		s.mu.Unlock()
		s.poke()
	}()
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}

func vmHumanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

func (a *application) consoleHyperVFile(target consoleTarget, file consoleFileRequest) (int64, error) {
	if _, _, local := a.consoleHyperVAccount(target.Host); !local {
		return 0, errors.New("files are copied with PowerShell Direct, which runs only on the Hyper-V host itself")
	}
	if file.User == "" || file.Password == "" {
		return 0, errors.New("type a user name and password of Windows in the VM")
	}
	process, err := a.consoleStartHyperV("", strings.Trim(target.ID, "{}"), "Files", file.User, file.Password, map[string]string{"direction": file.Direction, "local": file.Local, "guest": file.Guest})
	if err != nil {
		return 0, err
	}
	defer process.close()
	reply, err := process.out.ReadString('\n')
	for err == nil && !strings.HasPrefix(strings.TrimSpace(reply), "{") {
		reply, err = process.out.ReadString('\n')
	}
	if err != nil {
		return 0, errors.New("PowerShell Direct stopped without an answer")
	}
	var answer struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Bytes int64  `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(reply)), &answer); err != nil {
		return 0, err
	}
	if !answer.OK {
		return 0, errors.New(firstNonEmpty(answer.Error, "the copy failed"))
	}
	return answer.Bytes, nil
}

// handleConsoleNative opens the platform's own console window: VMware Remote
// Console (vmrc), Hyper-V's Virtual Machine Connection (vmconnect) or a
// Hyper-V enhanced session (.rdp through Remote Desktop Connection).
func (a *application) handleConsoleNative(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Kind   string        `json:"kind"`
		Target consoleTarget `json:"target"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8192)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The request was not understood."})
		return
	}
	if err := input.Target.validate(); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	system := filepath.Join(os.Getenv("SystemRoot"), "System32")
	host := strings.TrimSpace(input.Target.Host)
	if host == "" {
		host = os.Getenv("COMPUTERNAME")
	}
	var err error
	switch input.Kind {
	case "vmconnect":
		if input.Target.Platform != "hyperv" {
			err = errors.New("Virtual Machine Connection opens Hyper-V VMs only")
			break
		}
		program := filepath.Join(system, "vmconnect.exe")
		if _, statErr := os.Stat(program); statErr != nil {
			err = errors.New("Virtual Machine Connection is not installed on this computer. Add the Hyper-V management tools (Hyper-V GUI Management Tools) to use it")
			break
		}
		id := strings.Trim(input.Target.ID, "{}")
		if !consoleGUIDPattern(id) {
			err = errors.New("the Hyper-V VM ID is not a GUID")
			break
		}
		command := exec.Command(program, host, "-G", id)
		allowForegroundHandoff()
		err = command.Start()
		if err == nil {
			go func() { _ = command.Wait() }()
		}
	case "enhanced":
		if input.Target.Platform != "hyperv" {
			err = errors.New("Enhanced session opens Hyper-V VMs only")
			break
		}
		var text string
		text, err = consoleEnhancedRDP(host, input.Target.ID)
		if err != nil {
			break
		}
		folder := filepath.Join(a.update.appDataRoot, "Console")
		if err = os.MkdirAll(folder, 0700); err != nil {
			break
		}
		file := filepath.Join(folder, strings.Trim(input.Target.ID, "{}")+".rdp")
		if err = os.WriteFile(file, []byte(text), 0600); err != nil {
			break
		}
		command := exec.Command(filepath.Join(system, "mstsc.exe"), file)
		allowForegroundHandoff()
		err = command.Start()
		if err == nil {
			go func() { _ = command.Wait() }()
		}
	case "vmrc":
		if input.Target.Platform != "vmware" {
			err = errors.New("VMware Remote Console opens VMware VMs only")
			break
		}
		if key, openErr := registry.OpenKey(registry.CLASSES_ROOT, `vmrc\shell\open\command`, registry.QUERY_VALUE); openErr != nil {
			err = errors.New("VMware Remote Console is not installed on this computer. Install it from Broadcom to use it, or use this window")
			break
		} else {
			key.Close()
		}
		a.mu.Lock()
		options := vmConnectOptions{Server: a.vmware.server, User: a.vmware.account, Password: a.vmware.password, Thumbprint: a.vmware.thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
		a.mu.Unlock()
		if options.Password == "" {
			err = errors.New("sign in to vCenter or ESXi in Discovery first")
			break
		}
		ctx, cancel := context.WithTimeout(request.Context(), time.Minute)
		var vsphere *vmSession
		vsphere, err = openVSphereSession(ctx, options, func(string) {})
		if err == nil {
			var ticket string
			ticket, err = session.NewManager(vsphere.client).AcquireCloneTicket(ctx)
			if err == nil {
				allowForegroundHandoff()
				err = shellOpen(consoleVMRCURL(vsphere.host, ticket, input.Target.ID))
			}
			// VMware Remote Console clones this session as it starts; sign
			// the session out once it has had time to.
			go func(v *vmSession) {
				time.Sleep(2 * time.Minute)
				v.logout()
			}(vsphere)
		}
		cancel()
	default:
		err = errors.New("that console is not offered")
	}
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true})
}
