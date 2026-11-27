//go:build !windows

package main

import "syscall"

// pgMountFree reads a mounted file system's free (for unprivileged users)
// and total bytes.
func pgMountFree(mount string) (uint64, uint64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mount, &stat); err != nil {
		return 0, 0, false
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), uint64(stat.Blocks) * uint64(stat.Bsize), true
}
