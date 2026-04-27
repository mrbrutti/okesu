//go:build windows

package node

import (
	"syscall"
	"unsafe"
)

// diskFreeMB on Windows uses GetDiskFreeSpaceExW. Returns (0, false)
// on any failure. Best-effort — node metadata is informational; we
// never fail the probe when one field can't be collected.
func diskFreeMB(path string) (int64, bool) {
	kernel32, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		return 0, false
	}
	defer kernel32.Release()
	proc, err := kernel32.FindProc("GetDiskFreeSpaceExW")
	if err != nil {
		return 0, false
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64
	r1, _, _ := proc.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if r1 == 0 {
		return 0, false
	}
	return int64(freeBytesAvailable / (1024 * 1024)), true
}
