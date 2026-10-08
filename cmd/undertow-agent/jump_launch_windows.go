//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// _jump-launch is the small process boundary used by one-shot remote Windows
// management methods. The remotely managed process can return immediately,
// while the configured agent continues without inheriting WinRS, WMI, or Task
// Scheduler handles.
func launchJumpAgent() (bool, error) {
	if len(os.Args) != 2 || os.Args[1] != "_jump-launch" {
		return false, nil
	}
	path, err := os.Executable()
	if err != nil {
		return true, err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return true, err
	}
	defer null.Close()
	child := exec.Command(path)
	child.Stdin, child.Stdout, child.Stderr = null, null, null
	const detachedProcess = 0x00000008
	child.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
		HideWindow:    true,
	}
	if err := child.Start(); err != nil {
		return true, err
	}
	if err := child.Process.Release(); err != nil {
		return true, err
	}
	return true, nil
}
