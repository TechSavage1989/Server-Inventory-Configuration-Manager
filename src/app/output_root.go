package main

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// outputRootPattern accepts a drive path (C:\Reports) or a UNC share path
// (\\server\share\Reports), the same forms the Collection tab accepts.
var outputRootPattern = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|\\\\[^\\/]+[\\/][^\\/]+)`)

// cleanOutputRootChoice checks a typed or browsed output folder for
// discovery reports. It is the one output folder the application uses for
// Collection packages and VMware and Hyper-V discovery reports.
func cleanOutputRootChoice(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !outputRootPattern.MatchString(value) {
		return "", errors.New("Choose a full folder path, such as C:\\Reports or \\\\server\\share\\Reports.")
	}
	if strings.ContainsAny(value, "<>\"|?*") || strings.Contains(value[2:], ":") {
		return "", errors.New("The folder path contains characters Windows does not allow.")
	}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '\\' || r == '/' }) {
		if part == ".." {
			return "", errors.New("Type the folder path without \"..\".")
		}
	}
	cleaned := filepath.Clean(strings.ReplaceAll(value, "/", `\`))
	if filepath.Separator != '\\' {
		// filepath.Clean on other platforms leaves backslashes alone, so
		// only trim a trailing one that is not a drive root.
		cleaned = strings.ReplaceAll(value, "/", `\`)
		for len(cleaned) > 3 && strings.HasSuffix(cleaned, `\`) {
			cleaned = strings.TrimSuffix(cleaned, `\`)
		}
	}
	return cleaned, nil
}
