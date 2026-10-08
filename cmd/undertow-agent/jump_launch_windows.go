//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const winRMLaunchMarker = "Undertow WinRM launch accepted"

// launchJumpAgent is the WinRM-only detach boundary. The staged agent starts
// its configured copy outside the WinRM job, confirms that start to WinRS,
// and exits. Other Jump methods never call this entry point.
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
	const (
		detachedProcess        = 0x00000008
		createBreakawayFromJob = 0x01000000
	)
	child.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess | createBreakawayFromJob,
		HideWindow:    true,
	}
	if err := child.Start(); err != nil {
		return true, fmt.Errorf("start agent outside the WinRM job: %w", err)
	}
	if err := child.Process.Release(); err != nil {
		return true, errors.New("release detached WinRM agent process")
	}
	_, _ = fmt.Fprintln(os.Stdout, winRMLaunchMarker)
	return true, nil
}
