//go:build windows

package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAndLegacyReportsKeepAuthenticationAndContentIsolation(t *testing.T) {
	for _, name := range []string{"sample-report.html", "baseline-report.html"} {
		path := filepath.Join("build", "report-validation", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("Generate the synthetic report fixtures before Go tests:", err)
		}
		a := &application{token: "report-endpoint-test", loadedReportPath: path}
		denied := httptest.NewRecorder()
		a.handleCurrentReport(denied, httptest.NewRequest("GET", "/report/current?token=wrong", nil))
		if denied.Code != 403 {
			t.Fatal("report was served without the correct local token", name)
		}
		response := httptest.NewRecorder()
		a.handleCurrentReport(response, httptest.NewRequest("GET", "/report/current?token=report-endpoint-test", nil))
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) {
			t.Fatal("report data changed while serving", name)
		}
		policy := response.Header().Get("Content-Security-Policy")
		if policy != "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'" || strings.Contains(policy, "https:") {
			t.Fatal("report content policy was broadened", policy)
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("report response lost isolation headers", name)
		}
	}
}
