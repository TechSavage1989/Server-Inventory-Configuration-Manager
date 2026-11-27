//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Read-ScreenText.ps1 reads text from a picture with Windows' own text
// recognition, under the RemoteSigned policy (never Bypass). The picture here
// is drawn by System.Drawing with synthetic text; a runner without Windows
// text recognition or an OCR language skips the recognition part but must
// still answer with its plain reason.
func TestReadScreenTextScriptReadsTextFromAPicture(t *testing.T) {
	folder := t.TempDir()
	script := filepath.Join(folder, consoleOCRScriptName)
	if err := os.WriteFile(script, consoleOCRScriptData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := consoleRunOCRScript(powershellPath(), script, filepath.Join(folder, "missing.png")); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a missing picture must be refused, got %v", err)
	}
	picture := filepath.Join(folder, "screen.png")
	draw := `Add-Type -AssemblyName System.Drawing
$b = New-Object System.Drawing.Bitmap 900, 160
$g = [System.Drawing.Graphics]::FromImage($b)
$g.Clear([System.Drawing.Color]::Black)
$g.TextRenderingHint = [System.Drawing.Text.TextRenderingHint]::AntiAliasGridFit
$f = New-Object System.Drawing.Font 'Consolas', 40
$g.DrawString('IPv4 Address 192.0.2.25', $f, [System.Drawing.Brushes]::White, 10, 40)
$g.Dispose()
$b.Save('` + strings.ReplaceAll(picture, "'", "''") + `', [System.Drawing.Imaging.ImageFormat]::Png)
$b.Dispose()`
	if output, err := exec.Command(powershellPath(), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", draw).CombinedOutput(); err != nil {
		t.Skipf("System.Drawing could not draw the test picture: %v %s", err, output)
	}
	data, err := os.ReadFile(picture)
	if err != nil {
		t.Fatal(err)
	}
	if err := consoleCheckOCRImage(data); err != nil {
		t.Fatalf("the drawn picture is refused: %v", err)
	}
	text, language, err := consoleRunOCRScript(powershellPath(), script, picture)
	if err != nil {
		if strings.Contains(err.Error(), "not available on this computer") || strings.Contains(err.Error(), "no text recognition language") {
			t.Skipf("Windows text recognition is not available on this runner: %v", err)
		}
		t.Fatalf("text recognition failed: %v", err)
	}
	t.Logf("read %q (%s)", text, language)
	if !strings.Contains(strings.ReplaceAll(text, " ", ""), "192.0.2.25") {
		t.Fatalf("the address was not read: %q", text)
	}
}
