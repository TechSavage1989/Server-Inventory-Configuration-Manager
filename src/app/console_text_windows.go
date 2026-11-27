//go:build windows

package main

// VM console (rule 68): /api/console/text (text from a file in the VM) and
// /api/console/ocr (text from the screen, read by Windows on this computer).
// See console_text.go. Temporary files live in LocalAppData Console\Text,
// which only this user can read, and are deleted before the answer is sent.

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

//go:embed Read-ScreenText.ps1
var consoleOCRScriptData []byte

const consoleOCRScriptName = "Read-ScreenText.ps1"

func (a *application) consoleTextFolder() (string, error) {
	folder := filepath.Join(a.update.appDataRoot, "Console", "Text")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return "", err
	}
	return folder, nil
}

// handleConsoleText copies one text file out of the VM through the
// platform's file transfer, reads it and deletes the local copy.
func (a *application) handleConsoleText(response http.ResponseWriter, request *http.Request) {
	var input struct {
		ID       string `json:"id"`
		Guest    string `json:"guest"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16384)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The request was not understood."})
		return
	}
	s := a.consoleMgr().get(input.ID)
	if s == nil || s.isEnded() {
		writeJSON(response, http.StatusGone, map[string]any{"ok": false, "error": "This console is closed."})
		return
	}
	if !s.Caps.FilesFrom {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": firstNonEmpty(s.Caps.FilesWhy, "This console cannot copy files out of the VM.")})
		return
	}
	if err := consoleCheckGuestTextPath(input.Guest); err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	folder, err := a.consoleTextFolder()
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	local := filepath.Join(folder, consoleTempName(".txt"))
	defer os.Remove(local)
	file := consoleFileRequest{Direction: "from", Local: local, Guest: input.Guest, User: input.User, Password: input.Password}
	input.Password = ""
	a.mu.Lock()
	options := vmConnectOptions{Server: a.vmware.server, User: a.vmware.account, Password: a.vmware.password, Thumbprint: a.vmware.thumbprint, UserAgent: displayName + " " + appVersion, AppVersion: appVersion}
	connection := a.vmware.target.connection
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Minute)
	defer cancel()
	switch s.Target.Platform {
	case "vmware":
		_, err = consoleVMwareFile(ctx, options, s.Target.ID, file, func(int64) {})
	case "proxmox":
		_, err = consoleProxmoxFile(ctx, connection, s.Target.ID, file)
	case "hyperv":
		_, err = a.consoleHyperVFile(s.Target, file)
	default:
		err = errors.New("this platform cannot copy files out of the VM")
	}
	file.Password = ""
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	data, err := readLimited(local, consoleTextLimit)
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	text, err := consoleDecodeText(data)
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "text": text})
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("the file in the VM is larger than 1 MB; copy it with Files instead")
	}
	return data, nil
}

// handleConsoleOCR reads the text in a picture of the screen with Windows'
// own text recognition.
func (a *application) handleConsoleOCR(response http.ResponseWriter, request *http.Request) {
	var input struct {
		ID  string `json:"id"`
		PNG string `json:"png"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, consoleOCRLimit*4/3+4096)).Decode(&input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "The picture of the screen is too large or was not understood."})
		return
	}
	if s := a.consoleMgr().get(input.ID); s == nil || s.isEnded() {
		writeJSON(response, http.StatusGone, map[string]any{"ok": false, "error": "This console is closed."})
		return
	}
	data, err := base64.StdEncoding.DecodeString(input.PNG)
	input.PNG = ""
	if err == nil {
		err = consoleCheckOCRImage(data)
	}
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	text, language, err := a.consoleRecognize(data)
	if err != nil {
		writeJSON(response, http.StatusOK, map[string]any{"ok": false, "error": consoleSentence(err.Error())})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "text": text, "language": language})
}

func (a *application) consoleRecognize(picture []byte) (string, string, error) {
	folder, err := a.consoleTextFolder()
	if err != nil {
		return "", "", err
	}
	image := filepath.Join(folder, consoleTempName(".png"))
	if err := os.WriteFile(image, picture, 0600); err != nil {
		return "", "", err
	}
	defer os.Remove(image)
	script := filepath.Join(a.update.appDataRoot, "Runtime", "Console", consoleOCRScriptName)
	if err := os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		return "", "", err
	}
	if existing, err := os.ReadFile(script); err != nil || !bytes.Equal(existing, consoleOCRScriptData) {
		if err := os.WriteFile(script, consoleOCRScriptData, 0600); err != nil {
			return "", "", err
		}
	}
	return consoleRunOCRScript(a.powerShellPath, script, image)
}

// consoleRunOCRScript runs Read-ScreenText.ps1 and reads its one JSON line.
func consoleRunOCRScript(powerShell, script, image string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", script, "-ImagePath", image)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := command.Output()
	if ctx.Err() != nil {
		return "", "", errors.New("Windows text recognition did not answer in time")
	}
	var answer struct {
		OK       bool   `json:"ok"`
		Text     string `json:"text"`
		Language string `json:"language"`
		Error    string `json:"error"`
	}
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &answer) == nil {
			found = true
		}
	}
	if !found {
		if err != nil {
			return "", "", errors.New("Windows text recognition could not run: " + err.Error())
		}
		return "", "", errors.New("Windows text recognition gave no answer")
	}
	if !answer.OK {
		return "", "", errors.New(firstNonEmpty(answer.Error, "Windows text recognition failed"))
	}
	return answer.Text, answer.Language, nil
}
