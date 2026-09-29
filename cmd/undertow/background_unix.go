//go:build linux || darwin

package main

import (
	"os/exec"
	"syscall"
)

func detachProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
