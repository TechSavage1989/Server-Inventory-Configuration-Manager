//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// runMigrationBundleStep writes the migration bundle into a collected package
// and adds it to the package ZIP. A bundle that fails leaves the inventory
// package as the collector wrote it and says why; it never fails the
// collection, whose reports are already validated.
func (a *application) runMigrationBundleStep(packagePath, zipPath string, options collectionOptions, passphrase string) {
	a.mu.Lock()
	a.state.Phase = "Preparing the package for a new server"
	a.state.Message = "Exporting certificates, IIS passwords and settings into the migration bundle."
	a.state.CurrentStep = "Migration bundle"
	a.mu.Unlock()
	summary, err := a.writeMigrationBundle(packagePath, zipPath, options, passphrase)
	message := ""
	if err != nil {
		message = "The migration bundle was not written: " + err.Error() + ". The inventory package itself is complete."
	} else {
		message = summary.text()
	}
	a.logLauncher("%s", message)
	a.mu.Lock()
	a.state.Bundle = message
	a.state.BundleFailed = err != nil
	a.mu.Unlock()
}

func (a *application) writeMigrationBundle(packagePath, zipPath string, options collectionOptions, passphrase string) (bundleSummary, error) {
	var summary bundleSummary
	if passphrase == "" {
		return summary, errors.New("no passphrase was given")
	}
	directory, script, err := a.prepareRestoreEngineNamed("Bundle-", bundleEngineName)
	if err != nil {
		return summary, fmt.Errorf("the bundle engine could not be prepared: %v", err)
	}
	defer os.RemoveAll(directory)
	resultPath := filepath.Join(directory, "Bundle-Result.json")
	arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned",
		"-File", script, "-PackagePath", packagePath, "-ResultPath", resultPath,
		"-WebContentLimitGB", strconv.Itoa(options.BundleContentLimitGB)}
	if options.BundleWebContent {
		arguments = append(arguments, "-IncludeWebContent")
	}
	command := exec.Command(a.powerShellPath, arguments...)
	input, _ := json.Marshal(map[string]string{"passphrase": passphrase})
	command.Stdin = strings.NewReader(string(input) + "\n")
	var lastError string
	waitErr := a.runEngineProcess(command, func(line string) {
		a.appendLog(line)
		if index := strings.Index(line, "[ERROR] "); index >= 0 {
			lastError = line[index+8:]
		}
	}, func(*exec.Cmd) {})
	data, readErr := os.ReadFile(resultPath)
	if readErr == nil {
		_ = json.Unmarshal(stripUTF8BOM(data), &summary)
	}
	if waitErr != nil || summary.Status != "succeeded" {
		message := summary.Message
		if message == "" {
			message = lastError
		}
		if message == "" && waitErr != nil {
			message = waitErr.Error()
		}
		return summary, errors.New(strings.TrimSuffix(message, "."))
	}
	if zipPath != "" {
		if _, err := os.Stat(zipPath); err == nil {
			hash, err := appendFolderToZip(zipPath, packagePath, migrationBundleFolder)
			if err != nil {
				return summary, fmt.Errorf("the bundle was written to the package folder but could not be added to the ZIP: %v", err)
			}
			if err := os.WriteFile(zipPath+".sha256.txt", []byte(zipChecksumLine(hash, zipPath)+"\r\n"), 0644); err != nil {
				return summary, fmt.Errorf("the ZIP checksum could not be updated: %v", err)
			}
		}
	}
	return summary, nil
}
