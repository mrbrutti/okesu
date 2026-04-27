//go:build !linux && !darwin && !freebsd && !windows

package node

// diskFreeMB on platforms without a uniform Statfs (openbsd, netbsd,
// plan9, solaris, etc.) is a no-op. Cross-compiles still produce a
// runnable binary; the metadata probe just leaves DiskFreeMB at zero.
func diskFreeMB(_ string) (int64, bool) {
	return 0, false
}
