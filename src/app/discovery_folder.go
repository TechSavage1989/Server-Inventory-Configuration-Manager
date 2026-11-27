package main

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// vmDiscoveryHistoryKeep is how many earlier inventories a discovery folder
// keeps in its History folder.
const vmDiscoveryHistoryKeep = 20

// vmDiscoveryFolder is the one folder a discovery of server writes to (owner
// 2026-11-27: every read made a new VMware_<server>_<time> folder on the
// Desktop). The folder already open for the same server is used again, even
// one the operator chose with Open a saved inventory; otherwise it is
// <root>\<name> (VMware_<server> or HyperV_<server>), without a time, so
// every read of that server updates the same folder.
func vmDiscoveryFolder(root, name, server, currentServer, currentFolder string) string {
	if strings.TrimSpace(currentFolder) != "" && strings.EqualFold(strings.TrimSpace(currentServer), strings.TrimSpace(server)) {
		return filepath.Clean(currentFolder)
	}
	return filepath.Join(root, name)
}

// vmKeepDiscoveryHistory copies the inventory a folder holds into its History
// folder before a new read replaces it, and keeps the newest
// vmDiscoveryHistoryKeep copies. Nothing else is kept: the reports are
// written again from the new inventory.
func vmKeepDiscoveryHistory(folder string) error {
	source := filepath.Join(folder, vmInventoryFile)
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	history := filepath.Join(folder, "History")
	if err := os.MkdirAll(history, 0700); err != nil {
		return err
	}
	name := strings.TrimSuffix(vmInventoryFile, filepath.Ext(vmInventoryFile)) + "_" + info.ModTime().Format("20060102_150405") + filepath.Ext(vmInventoryFile)
	target := filepath.Join(history, name)
	if _, err := os.Stat(target); err != nil {
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		if closeErr := out.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			os.Remove(target)
			return copyErr
		}
		os.Chtimes(target, time.Now(), info.ModTime())
	}
	entries, err := os.ReadDir(history)
	if err != nil {
		return err
	}
	prefix := strings.TrimSuffix(vmInventoryFile, filepath.Ext(vmInventoryFile)) + "_"
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), filepath.Ext(vmInventoryFile)) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for len(names) > vmDiscoveryHistoryKeep {
		os.Remove(filepath.Join(history, names[0]))
		names = names[1:]
	}
	return nil
}
