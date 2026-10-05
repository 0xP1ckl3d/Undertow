//go:build windows

package control

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The process token is already available at check-in. This sends only a
// coarse classification and never starts a host operation or child process.
func currentPrivilege() string {
	var elevated uint32
	var size uint32
	err := windows.GetTokenInformation(windows.GetCurrentProcessToken(), windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)), &size)
	if err != nil {
		return ""
	}
	if elevated != 0 {
		return "high"
	}
	return "low"
}
