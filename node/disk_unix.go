//go:build linux || darwin || freebsd

package node

import "syscall"

// diskFreeMB reports free megabytes available at `path` via Statfs.
// Field names line up across linux / darwin / freebsd. OpenBSD's
// Statfs_t uses F_-prefixed names; netbsd diverges further; both
// fall back to the no-op stub in disk_other.go since we don't ship
// daemons there as a primary target.
//
// Returns (0, false) when Statfs fails — caller treats that as "skip
// this field" so partial probes don't overwrite stale values.
func diskFreeMB(path string) (int64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, false
	}
	mb := int64(stat.Bavail) * int64(stat.Bsize) / (1024 * 1024)
	return mb, true
}
