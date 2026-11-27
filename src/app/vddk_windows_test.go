//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The folder check reads Windows' own verdict and the signer's name: an
// unsigned DLL and a DLL Microsoft signed are both refused, and nothing is
// loaded.
func TestVDDKFolderCheckRefusesUnsignedAndOtherSigners(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.MkdirAll(bin, 0700)
	system := filepath.Join(os.Getenv("SystemRoot"), "System32", "version.dll")
	signed, err := os.ReadFile(system)
	if err != nil {
		t.Skip("no version.dll to copy: " + err.Error())
	}
	os.WriteFile(filepath.Join(bin, "vixDiskLib.dll"), signed, 0600)
	os.WriteFile(filepath.Join(bin, "zlib1.dll"), []byte("MZ not signed"), 0600)
	status, _, err := vddkCheck(root)
	if err == nil || status.Ready {
		t.Fatal("a folder of unsigned and Microsoft-signed DLLs was accepted")
	}
	text := strings.Join(status.Problems, "\n")
	if !strings.Contains(text, "zlib1.dll") || !strings.Contains(text, "vixDiskLib.dll") {
		t.Fatalf("problems %v", status.Problems)
	}
	signer, err := vddkVerifySignature(system)
	if err != nil {
		// version.dll is catalog-signed on some Windows builds; then the
		// embedded-signature check rightly refuses it.
		t.Logf("version.dll: %v", err)
	} else if !strings.Contains(signer, "Microsoft") || vddkTrustedSigner(signer) {
		t.Fatalf("version.dll signer %q", signer)
	}
	vddkLoaded.Lock()
	loaded := vddkLoaded.lib
	vddkLoaded.Unlock()
	if loaded != nil {
		t.Fatal("the check loaded a library")
	}
	if got := vddkStatusFor(""); got.Ready || !strings.Contains(got.Message, "Off") {
		t.Fatalf("an empty folder status %+v", got)
	}
}
