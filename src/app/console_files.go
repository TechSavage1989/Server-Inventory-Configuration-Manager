package main

// VM console (rule 68): copying files into and out of a VM from its console
// window, using only what is already in the VM. VMware: VMware Tools' file
// transfer (GuestFileManager InitiateFileTransferToGuest and FromGuest only;
// never the process manager, so no program is started in the VM). Proxmox VE:
// the QEMU guest agent's file-read and file-write. Hyper-V: PowerShell Direct
// (console_hyperv_windows.go). An existing file is never replaced on either
// side.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmware/govmomi/guest"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// consoleFileRequest is one copy the operator asked for. The guest account
// is used for this copy only and never kept.
type consoleFileRequest struct {
	Direction string `json:"direction"` // to (into the VM) or from
	Local     string `json:"local"`
	Guest     string `json:"guest"`
	User      string `json:"user"`
	Password  string `json:"password"`
}

const consoleFileLimit = 4 << 30

func (r consoleFileRequest) validate() error {
	if r.Direction != "to" && r.Direction != "from" {
		return errors.New("choose whether to copy into or out of the VM")
	}
	if strings.TrimSpace(r.Local) == "" || strings.TrimSpace(r.Guest) == "" {
		return errors.New("choose the file on this computer and the path in the VM")
	}
	if strings.ContainsAny(r.Guest, "\x00\r\n") || len(r.Guest) > 1024 {
		return errors.New("the path in the VM is not valid")
	}
	if !filepath.IsAbs(r.Local) {
		return errors.New("give the full path of the file on this computer")
	}
	return nil
}

// consoleOpenLocalTarget creates the local file for a copy out of the VM,
// never replacing one that exists.
func consoleOpenLocalTarget(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("%s already exists on this computer; choose another name", path)
	}
	return file, err
}

// consoleVMwareFile copies one file through VMware Tools.
func consoleVMwareFile(ctx context.Context, options vmConnectOptions, vmID string, request consoleFileRequest, progress func(int64)) (int64, error) {
	if err := request.validate(); err != nil {
		return 0, err
	}
	if request.User == "" || request.Password == "" {
		return 0, errors.New("type a user name and password of the VM's operating system")
	}
	vsphere, err := openVSphereSession(ctx, options, func(string) {})
	if err != nil {
		return 0, err
	}
	defer vsphere.logout()
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: vmID}
	var machine mo.VirtualMachine
	if err := property.DefaultCollector(vsphere.client).RetrieveOne(ctx, ref, []string{"runtime.host"}, &machine); err != nil {
		return 0, fmt.Errorf("the VM could not be read: %v", err)
	}
	var hostPEM []byte
	if machine.Runtime.Host != nil {
		var host mo.HostSystem
		if err := property.DefaultCollector(vsphere.client).RetrieveOne(ctx, *machine.Runtime.Host, []string{"name", "config.certificate"}, &host); err == nil {
			if host.Config != nil {
				hostPEM = host.Config.Certificate
			}
		}
	}
	manager, err := guest.NewOperationsManager(vsphere.client, ref).FileManager(ctx)
	if err != nil {
		return 0, fmt.Errorf("VMware Tools file transfer is not available: %v", err)
	}
	auth := &types.NamePasswordAuthentication{Username: request.User, Password: request.Password}
	transfer := func(raw string) (*url.URL, error) {
		u, err := manager.TransferURL(ctx, raw)
		if err != nil {
			return nil, err
		}
		if u.Scheme != "https" {
			return nil, errors.New("vSphere gave a file transfer address that is not HTTPS")
		}
		return u, nil
	}
	newClient := func(u *url.URL) *http.Client {
		return &http.Client{Transport: &http.Transport{TLSClientConfig: consolePinnedHostTLS(u.Hostname(), hostPEM), ResponseHeaderTimeout: 2 * time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if request.Direction == "to" {
		file, err := os.Open(request.Local)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil {
			return 0, err
		}
		if stat.IsDir() || stat.Size() > consoleFileLimit {
			return 0, errors.New("choose one file of at most 4 GB")
		}
		raw, err := manager.InitiateFileTransferToGuest(ctx, auth, request.Guest, &types.GuestFileAttributes{}, stat.Size(), false)
		if err != nil {
			return 0, consoleGuestError(err)
		}
		u, err := transfer(raw)
		if err != nil {
			return 0, err
		}
		body := &consoleProgressReader{r: file, progress: progress}
		put, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
		if err != nil {
			return 0, err
		}
		put.ContentLength = stat.Size()
		response, err := newClient(u).Do(put)
		if err != nil {
			return body.n, fmt.Errorf("the copy into the VM failed: %v", vmTargetStripURL(err))
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode > 299 {
			return body.n, fmt.Errorf("the ESXi host refused the copy (%s)", response.Status)
		}
		return body.n, nil
	}
	info, err := manager.InitiateFileTransferFromGuest(ctx, auth, request.Guest)
	if err != nil {
		return 0, consoleGuestError(err)
	}
	if info.Size > consoleFileLimit {
		return 0, errors.New("the file in the VM is larger than 4 GB")
	}
	u, err := transfer(info.Url)
	if err != nil {
		return 0, err
	}
	out, err := consoleOpenLocalTarget(request.Local)
	if err != nil {
		return 0, err
	}
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		out.Close()
		os.Remove(request.Local)
		return 0, err
	}
	response, err := newClient(u).Do(get)
	if err != nil {
		out.Close()
		os.Remove(request.Local)
		return 0, fmt.Errorf("the copy out of the VM failed: %v", vmTargetStripURL(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		out.Close()
		os.Remove(request.Local)
		return 0, fmt.Errorf("the ESXi host refused the copy (%s)", response.Status)
	}
	n, err := io.Copy(out, &consoleProgressReader{r: io.LimitReader(response.Body, consoleFileLimit), progress: progress})
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(request.Local)
		return n, fmt.Errorf("the copy out of the VM failed: %v", err)
	}
	return n, nil
}

func consoleGuestError(err error) error {
	text := err.Error()
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "invalidguestlogin"), strings.Contains(lower, "authentication"):
		return errors.New("the VM's operating system refused the user name or password")
	case strings.Contains(lower, "fileAlreadyExists"), strings.Contains(lower, "filealreadyexists"):
		return errors.New("that file already exists in the VM; choose another name. Files are never replaced")
	case strings.Contains(lower, "filenotfound"):
		return errors.New("that file or folder does not exist in the VM")
	case strings.Contains(lower, "toolsunavailable"), strings.Contains(lower, "guestoperationsunavailable"):
		return errors.New("VMware Tools in the VM is not answering, so files cannot be copied now")
	}
	return fmt.Errorf("VMware Tools file transfer failed: %s", text)
}

type consoleProgressReader struct {
	r        io.Reader
	n        int64
	progress func(int64)
	last     time.Time
}

func (p *consoleProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.progress != nil && time.Since(p.last) > 300*time.Millisecond {
		p.last = time.Now()
		p.progress(p.n)
	}
	return n, err
}

// Proxmox VE's agent file-write takes at most 61440 characters of content.
const consoleProxmoxWriteLimit = 45 << 10

// consoleProxmoxFile copies a small file through the QEMU guest agent.
func consoleProxmoxFile(ctx context.Context, connection vmTargetConnection, vmid string, request consoleFileRequest) (int64, error) {
	if err := request.validate(); err != nil {
		return 0, err
	}
	p, err := newVMProxmox(connection)
	if err != nil {
		return 0, err
	}
	node, _, err := consoleProxmoxNode(ctx, p, vmid)
	if err != nil {
		return 0, err
	}
	base := "/nodes/" + url.PathEscape(node) + "/qemu/" + url.PathEscape(vmid) + "/agent"
	read := func() (string, error) {
		var answer struct {
			Content string `json:"content"`
		}
		err := p.get(ctx, base+"/file-read", url.Values{"file": {request.Guest}}, &answer)
		return answer.Content, err
	}
	if request.Direction == "to" {
		data, err := os.ReadFile(request.Local)
		if err != nil {
			return 0, err
		}
		if len(data) > consoleProxmoxWriteLimit {
			return 0, fmt.Errorf("the QEMU guest agent copies at most %d KB into a VM through Proxmox VE", consoleProxmoxWriteLimit>>10)
		}
		if _, err := read(); err == nil {
			return 0, errors.New("that file already exists in the VM; choose another name. Files are never replaced")
		}
		values := url.Values{"file": {request.Guest}, "content": {base64.StdEncoding.EncodeToString(data)}, "encode": {"0"}}
		if _, err := p.form(ctx, http.MethodPost, base+"/file-write", values); err != nil {
			return 0, fmt.Errorf("the QEMU guest agent did not write the file: %v. It must already be running in the VM and enabled in the VM's Options", err)
		}
		return int64(len(data)), nil
	}
	content, err := read()
	if err != nil {
		return 0, fmt.Errorf("the QEMU guest agent did not read the file: %v. It must already be running in the VM and enabled in the VM's Options", err)
	}
	out, err := consoleOpenLocalTarget(request.Local)
	if err != nil {
		return 0, err
	}
	n, err := io.WriteString(out, content)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(request.Local)
		return 0, err
	}
	return int64(n), nil
}
