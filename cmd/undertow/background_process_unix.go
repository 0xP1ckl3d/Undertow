//go:build linux || darwin

package main

import (
	"errors"
	"syscall"
)

func backgroundProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, errors.New("invalid background PID")
	}
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return true, err
}
