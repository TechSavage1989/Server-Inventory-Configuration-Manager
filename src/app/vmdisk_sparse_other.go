//go:build !windows

package main

import "os"

// vmMarkSparse is a no-op where files are sparse by default.
func vmMarkSparse(*os.File) {}
