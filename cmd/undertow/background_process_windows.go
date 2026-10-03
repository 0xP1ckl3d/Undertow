//go:build windows

package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
)

func backgroundProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, errors.New("invalid background PID")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		// Access denied can mean a protected live process; preserve its state.
		return true, nil
	}
	defer windows.CloseHandle(handle)
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return true, err
	}
	if state == 0x102 {
		return true, nil
	} // WAIT_TIMEOUT
	if state == 0 {
		return false, nil
	} // WAIT_OBJECT_0
	return true, fmt.Errorf("unexpected process wait status %d", state)
}
