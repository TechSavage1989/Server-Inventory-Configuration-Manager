//go:build windows

package main

// Runs the automatic fix-up (guest_autofix.go) after a cutover: on this
// Hyper-V host through PowerShell Direct, or on a node of the connected
// remote host or cluster through PowerShell remoting to that node, which then
// uses PowerShell Direct. Nothing is copied into the guest and no service or
// task is created (Invoke-GuestRemote.ps1); the account reaches PowerShell
// only on standard input.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// guestAutoAccount is the guest administrator account for the automatic
// fix-up. The password is never written anywhere.
type guestAutoAccount struct {
	user     string
	password string
}

type guestAutoState struct {
	User        string `json:"user"`
	HasPassword bool   `json:"hasPassword"`
	// Remembered is true when the account is kept sealed on this computer
	// (Remember sign-ins in Help > Settings); Notice says why it was
	// forgotten.
	Remembered bool   `json:"remembered,omitempty"`
	RememberOn bool   `json:"rememberOn,omitempty"`
	Notice     string `json:"notice,omitempty"`
}

// guestAutoStateLocked is the guest account as the page sees it: never the
// password. Caller holds a.mu.
func (a *application) guestAutoStateLocked() guestAutoState {
	store := a.vcRememberStore()
	return guestAutoState{User: a.guestAuto.user, HasPassword: a.guestAuto.password != "", Remembered: store.Has(vcRememberGuestKey), RememberOn: store.Enabled(), Notice: store.Notice(vcRememberGuestKey)}
}

func validGuestAccountName(user string) bool {
	user = strings.TrimSpace(user)
	return user != "" && len(user) <= 256 && !strings.ContainsAny(user, "\x00\r\n\"/[]:;|=,+*?<>")
}

// handleGuestAuto keeps the account (POST), forgets it (/forget) or runs the
// fix-up again for one VM (/run).
func (a *application) handleGuestAuto(response http.ResponseWriter, request *http.Request) {
	if !a.requireAuthorization(response, request) {
		return
	}
	fail := func(status int, message string) {
		writeJSON(response, status, map[string]any{"ok": false, "error": message})
	}
	switch strings.TrimPrefix(request.URL.Path, "/api/vmware/guest-auto") {
	case "", "/":
		var input struct {
			User     string `json:"user"`
			Password string `json:"password"`
			// Remember turns on Remember sign-ins (Help > Settings) first.
			Remember bool `json:"remember"`
		}
		if !decodeVMwareRequest(response, request, &input) {
			return
		}
		user := strings.TrimSpace(input.User)
		// A user name with a slash is DOMAIN\user; ':' and the rest stay out.
		if !validGuestAccountName(strings.ReplaceAll(user, `\`, "")) {
			fail(http.StatusBadRequest, `Type the guest administrator account, for example .\Administrator or EXAMPLE\admin.`)
			return
		}
		a.mu.Lock()
		password := input.Password
		if password == "" && strings.EqualFold(user, a.guestAuto.user) {
			password = a.guestAuto.password
		}
		if password == "" || len(password) > 256 {
			a.mu.Unlock()
			fail(http.StatusBadRequest, "Type the account's password.")
			return
		}
		a.guestAuto = guestAutoAccount{user: user, password: password}
		a.mu.Unlock()
		a.vmwareLog("The new VMs will be fixed up after each cutover with the account %s.", user)
		// With Remember sign-ins on (Help > Settings), the account is kept
		// sealed for this Windows user like vCenter sign-ins.
		store := a.vcRememberStore()
		if input.Remember && !store.Enabled() {
			if err := store.SetEnabled(true); err != nil {
				a.vmwareLog("Remember sign-ins could not be turned on: %s", err.Error())
			}
		}
		if store.Enabled() {
			if err := store.Remember(vcRememberGuestKey, user, password, "", time.Now()); err != nil {
				a.vmwareLog("The guest administrator account could not be remembered: %s", err.Error())
			}
		}
		writeJSON(response, http.StatusOK, map[string]any{"ok": true, "user": user})
	case "/forget":
		if !decodeVMwareRequest(response, request, nil) {
			return
		}
		a.mu.Lock()
		a.guestAuto = guestAutoAccount{}
		a.mu.Unlock()
		a.vcRememberStore().Forget(vcRememberGuestKey, "")
		writeJSON(response, http.StatusOK, map[string]any{"ok": true})
	case "/run":
		var input struct {
			VM       string `json:"vm"`
			HyperVID string `json:"hyperVId"`
			Host     string `json:"host"`
		}
		if !decodeVMwareRequest(response, request, &input) {
			return
		}
		a.mu.Lock()
		inventory := a.vmware.inventory
		a.mu.Unlock()
		var vm *vmMachine
		if inventory != nil {
			for index := range inventory.VMs {
				if strings.EqualFold(inventory.VMs[index].Name, input.VM) {
					vm = &inventory.VMs[index]
				}
			}
		}
		if vm == nil {
			fail(http.StatusBadRequest, "That VM is not in the inventory open now.")
			return
		}
		if !vmHyperVIDPattern.MatchString(input.HyperVID) {
			fail(http.StatusBadRequest, "The new VM's ID is not known; use Prepare and fix up.")
			return
		}
		if previous := a.fixups.Get(vm.Name); previous != nil && (previous.State == guestFixWaiting || previous.State == guestFixRunning) {
			fail(http.StatusConflict, "The fix-up of "+vm.Name+" is still running.")
			return
		}
		if err := a.startAutoFixup(*vm, input.HyperVID, strings.TrimSpace(input.Host), true); err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(response, http.StatusAccepted, map[string]any{"ok": true})
	default:
		fail(http.StatusNotFound, "Not found.")
	}
}

// loadRememberedGuest puts the remembered guest administrator account in
// memory when the application starts, unless one was typed already.
func (a *application) loadRememberedGuest() {
	store := a.vcRememberStore()
	if !store.Has(vcRememberGuestKey) {
		return
	}
	user, password, _, ok := store.Get(vcRememberGuestKey)
	if !ok {
		store.Forget(vcRememberGuestKey, "The remembered guest administrator account could not be read on this computer, so it was forgotten. Type it again.")
		return
	}
	a.mu.Lock()
	if a.guestAuto.password == "" {
		a.guestAuto = guestAutoAccount{user: user, password: password}
	}
	a.mu.Unlock()
}

// forgetRefusedGuest forgets a remembered guest account a new VM refused.
func (a *application) forgetRefusedGuest(user, message string) {
	if !strings.Contains(message, "refused the account") {
		return
	}
	store := a.vcRememberStore()
	if remembered, _, _, ok := store.Get(vcRememberGuestKey); ok && strings.EqualFold(remembered, user) {
		store.Forget(vcRememberGuestKey, "The remembered guest administrator account "+user+" was refused by a new VM, so it was forgotten. Type it again.")
	}
}

// guestAutoTarget is where the fix-up reaches the new VM: PowerShell Direct
// here, or through the connected remote host's node.
func (a *application) guestAutoTarget(vm vmMachine, hyperVID, host string) (guestTarget, error) {
	a.mu.Lock()
	account := a.guestAuto
	a.mu.Unlock()
	target := guestTarget{Mode: "direct", VM: vm.Name, User: account.user, password: account.password}
	if host != "" && !strings.EqualFold(host, a.computerName) {
		a.mu.Lock()
		err := a.guestCheckHostLocked(host)
		a.mu.Unlock()
		if err != nil {
			return target, err
		}
		target.Mode, target.Host = "hostdirect", host
	} else if !vmHyperVHere() {
		return target, errors.New("this computer is not a Hyper-V host")
	}
	return target, nil
}

// afterCutover runs what follows a successful cutover: the old VMware VM's
// adapters set to not connect (so its address is never used twice), the
// rollback record (rollback_windows.go, with the adapters' record so Roll
// back puts them back exactly), then the automatic fix-up of the new VM in
// the background. plan names the migration plan ("" for Move VMs). It
// returns notes for the move result; nothing here fails the cutover.
func (a *application) afterCutover(ctx context.Context, vm vmMachine, hyperVID, host string, started, disconnect, fixUp bool, options vmConnectOptions, recordFolder, plan string, log func(string)) []string {
	notes := []string{}
	adapterRecord := ""
	defer func() { a.recordVMCutover(vm, hyperVID, host, plan, adapterRecord) }()
	if disconnect && vm.Source != vmSourceHyperV {
		changed, record, err := vmDisconnectSourceAdaptersRecord(ctx, options, vm.ID, vm.Name, recordFolder, log)
		adapterRecord = record
		switch {
		case err != nil:
			notes = append(notes, "The old VM's network adapters were not disconnected: "+err.Error()+".")
		case changed > 0:
			notes = append(notes, fmt.Sprintf("The old VM's %d network adapter(s) were set to not connect.", changed))
		}
	}
	for _, note := range notes {
		log(note)
	}
	if !fixUp {
		return notes
	}
	a.mu.Lock()
	user := a.guestAuto.user
	a.mu.Unlock()
	if ok, why := guestAutoFixWanted(vm, started, user); !ok {
		a.fixups.Set(guestFixStatus{VM: vm.Name, HyperVID: hyperVID, Host: host, Via: "PowerShell Direct", State: guestFixSkipped, Message: why, Started: time.Now().UTC(), Finished: time.Now().UTC()})
		log(why)
		return append(notes, why)
	}
	if err := a.startAutoFixup(vm, hyperVID, host, false); err != nil {
		message := "The new VM was not fixed up: " + err.Error() + ". Use Prepare and fix up."
		a.fixups.Set(guestFixStatus{VM: vm.Name, HyperVID: hyperVID, Host: host, Via: "PowerShell Direct", State: guestFixFailed, Message: message, Started: time.Now().UTC(), Finished: time.Now().UTC()})
		log(message)
		return append(notes, message)
	}
	return append(notes, "Fixing up "+vm.Name+" once Windows has started (IP settings, hidden adapters, disks, VMware Tools, activation).")
}

// startAutoFixup starts the fix-up of one new VM in the background. again is
// true for Fix up now, which does not wait as long for the VM.
func (a *application) startAutoFixup(vm vmMachine, hyperVID, host string, again bool) error {
	target, err := a.guestAutoTarget(vm, hyperVID, host)
	if err != nil {
		return err
	}
	if target.User == "" || target.password == "" {
		return errors.New("enter the guest administrator account first")
	}
	plan, err := guestAutoFixPlan(vm, guestAutoFixDefaults())
	if err != nil {
		return err
	}
	if !a.beginCriticalOperation() {
		return errors.New("an update is being installed")
	}
	via := "PowerShell Direct on this host"
	if target.Mode == "hostdirect" {
		via = "PowerShell Direct through " + target.Host
	}
	a.fixups.Set(guestFixStatus{VM: vm.Name, HyperVID: hyperVID, Host: host, Via: via, State: guestFixWaiting, Message: "Waiting for Windows in " + vm.Name + " to start.", Started: time.Now().UTC()})
	go func() {
		defer a.endCriticalOperation()
		wait := guestAutoFixWait
		if again {
			wait = 3 * time.Minute
		}
		a.vmwareLog("Fixing up %s through %s once Windows answers.", vm.Name, via)
		raw, err := a.runGuestRemoteWith(target, "AutoFixup", plan, guestRemoteExtra{WaitSeconds: int(wait / time.Second), VMID: hyperVID, Started: func() {
			a.fixups.Update(vm.Name, guestFixRunning, "Windows answers; fixing up "+vm.Name+".")
		}})
		target.password = ""
		var report *guestAutoFixReport
		state := guestFixFailed
		if err == nil {
			report, state, err = guestAutoFixParse(raw)
		}
		message := ""
		if err != nil {
			message = vm.Name + " was not fixed up: " + err.Error() + ". Use Prepare and fix up for this VM (A VM on this Hyper-V host, or on the connected host)."
			a.forgetRefusedGuest(target.User, err.Error())
		} else {
			message = guestFixSummary(vm.Name, state, report)
		}
		status := guestFixStatus{VM: vm.Name, HyperVID: hyperVID, Host: host, Via: via, State: state, Message: message, Finished: time.Now().UTC()}
		if previous := a.fixups.Get(vm.Name); previous != nil {
			status.Started = previous.Started
		}
		if report != nil {
			status.Steps = report.Steps
		}
		a.fixups.Set(status)
		a.vmwareLog("%s", message)
	}()
	return nil
}

// guestRemoteExtra are the options only the automatic fix-up uses.
type guestRemoteExtra struct {
	WaitSeconds int
	VMID        string
	// Started is called once the guest engine starts inside the VM.
	Started func()
}

func (a *application) runGuestRemoteWith(target guestTarget, mode string, plan []byte, extra guestRemoteExtra) (json.RawMessage, error) {
	return a.runGuestRemoteOptions(target, mode, plan, extra)
}
