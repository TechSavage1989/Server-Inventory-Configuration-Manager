package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// The migration bundle (Export-MigrationBundle.ps1) makes a collection
// package restorable on a different server: certificates with private keys,
// the passwords IIS keeps encrypted, website files and structured settings. Its
// secrets are encrypted with a passphrase the operator types for that one
// collection; the passphrase is never saved, logged or shown again.

const (
	migrationBundleFolder      = "09-Migration-Bundle"
	bundleEngineName           = "Export-MigrationBundle.ps1"
	bundlePassphraseMin        = 12
	bundlePassphraseMax        = 256
	bundleContentLimitDefault  = 10
	bundleContentLimitMaxGB    = 2048
	bundleSecretsFileName      = "Secrets.simmsec"
	bundlePassphraseCheckName  = "Passphrase-Check.simmsec"
	bundleManifestName         = "Bundle-Manifest.csv"
	bundleSettingsFileName     = "Bundle.json"
	restorePasswordLimit       = 256
	restoreSecretsRequestLimit = 64 * 1024
)

// checkBundlePassphrase returns why a passphrase cannot protect a bundle.
func checkBundlePassphrase(passphrase, confirm string) error {
	if utf8.RuneCountInString(passphrase) < bundlePassphraseMin {
		return fmt.Errorf("The bundle passphrase must be at least %d characters.", bundlePassphraseMin)
	}
	if len(passphrase) > bundlePassphraseMax {
		return fmt.Errorf("The bundle passphrase can be at most %d characters.", bundlePassphraseMax)
	}
	if strings.ContainsAny(passphrase, "\r\n\x00") {
		return errors.New("The bundle passphrase cannot contain line breaks.")
	}
	if passphrase != confirm {
		return errors.New("The two passphrase entries do not match.")
	}
	return nil
}

// normalizeBundleLimit fills the default website files limit and checks it.
func normalizeBundleLimit(limit int) (int, error) {
	if limit == 0 {
		limit = bundleContentLimitDefault
	}
	if limit < 1 || limit > bundleContentLimitMaxGB {
		return 0, fmt.Errorf("The website files limit must be a whole number from 1 to %d GB.", bundleContentLimitMaxGB)
	}
	return limit, nil
}

// appendFolderToZip adds every file under packagePath\folder to the package
// ZIP, keeping the entries already in it byte for byte, and returns the new
// archive's SHA-256 in upper case. The archive is rebuilt beside the original
// and swapped in only once it is complete.
func appendFolderToZip(zipPath, packagePath, folder string) (string, error) {
	source := filepath.Join(packagePath, folder)
	var files []string
	if err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("the package ZIP could not be opened: %w", err)
	}
	defer reader.Close()
	separator := "/"
	prefix := filepath.ToSlash(folder) + "/"
	for _, entry := range reader.File {
		// Windows PowerShell 5.1's Compress-Archive writes backslashes; match
		// whatever the collector wrote so the folder extracts in one place.
		if strings.Contains(entry.Name, "\\") {
			separator = "\\"
		}
		if strings.HasPrefix(strings.ReplaceAll(entry.Name, "\\", "/"), prefix) {
			return "", errors.New("the package ZIP already contains a migration bundle")
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(zipPath), filepath.Base(zipPath)+".*.tmp")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tempName)
		}
	}()
	writer := zip.NewWriter(temp)
	for _, entry := range reader.File {
		if err := writer.Copy(entry); err != nil {
			temp.Close()
			return "", fmt.Errorf("copying %s into the new ZIP failed: %w", entry.Name, err)
		}
	}
	for _, path := range files {
		relative, err := filepath.Rel(packagePath, path)
		if err != nil || strings.HasPrefix(relative, "..") {
			temp.Close()
			return "", fmt.Errorf("%s is outside the package", path)
		}
		name := strings.ReplaceAll(filepath.ToSlash(relative), "/", separator)
		info, err := os.Stat(path)
		if err != nil {
			temp.Close()
			return "", err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			temp.Close()
			return "", err
		}
		header.Name = name
		header.Method = zip.Deflate
		destination, err := writer.CreateHeader(header)
		if err != nil {
			temp.Close()
			return "", err
		}
		input, err := os.Open(path)
		if err != nil {
			temp.Close()
			return "", err
		}
		_, err = io.Copy(destination, input)
		input.Close()
		if err != nil {
			temp.Close()
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	reader.Close()
	if err := os.Rename(tempName, zipPath); err != nil {
		return "", fmt.Errorf("the new package ZIP could not replace the old one: %w", err)
	}
	keep = true
	return fileSHA256Upper(zipPath)
}

func fileSHA256Upper(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil))), nil
}

// zipChecksumLine is the line the collector writes to <zip>.sha256.txt.
func zipChecksumLine(hash, zipPath string) string {
	return fmt.Sprintf("%s *%s", hash, filepath.Base(zipPath))
}

// bundleSummary is the result Export-MigrationBundle.ps1 writes.
type bundleSummary struct {
	Status               string   `json:"status"`
	Message              string   `json:"message"`
	BundlePath           string   `json:"bundlePath"`
	Features             int      `json:"features"`
	LocalUsers           int      `json:"localUsers"`
	LocalGroups          int      `json:"localGroups"`
	Shares               int      `json:"shares"`
	IISSites             int      `json:"iisSites"`
	CertificatesInBundle int      `json:"certificatesInBundle"`
	CertificatesLeftOut  int      `json:"certificatesLeftOut"`
	IISPasswords         int      `json:"iisPasswords"`
	WebContentFolders    int      `json:"webContentFolders"`
	Warnings             []string `json:"warnings"`
}

func (s bundleSummary) text() string {
	parts := []string{fmt.Sprintf("%d role(s) and feature(s)", s.Features)}
	if s.LocalUsers+s.LocalGroups > 0 {
		parts = append(parts, fmt.Sprintf("%d local user(s), %d local group(s)", s.LocalUsers, s.LocalGroups))
	}
	if s.Shares > 0 {
		parts = append(parts, fmt.Sprintf("%d file share(s)", s.Shares))
	}
	if s.IISSites > 0 {
		parts = append(parts, fmt.Sprintf("%d IIS site(s) with %d password(s) and %d website folder(s)", s.IISSites, s.IISPasswords, s.WebContentFolders))
	}
	parts = append(parts, fmt.Sprintf("%d certificate(s) with private keys", s.CertificatesInBundle))
	text := "Migration bundle: " + strings.Join(parts, ", ") + "."
	if s.CertificatesLeftOut > 0 {
		text += fmt.Sprintf(" %d certificate(s) could not be carried; see the bundle's warnings.", s.CertificatesLeftOut)
	}
	return text
}
