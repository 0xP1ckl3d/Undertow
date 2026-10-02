package main

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// An interactive launch hands the long-running agent to a new session so
// closing the launching terminal does not send it SIGHUP. Service managers and
// test harnesses that already supply nonterminal I/O retain process ownership.
func detachIfInteractive() (bool, error) {
	if !hasTerminal() {
		return false, nil
	}
	path, err := os.Executable()
	if err != nil {
		return false, err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer null.Close()
	child := exec.Command(path)
	child.Stdin, child.Stdout, child.Stderr = null, null, null
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return false, err
	}
	if err := child.Process.Release(); err != nil {
		return false, err
	}
	return true, nil
}

func hasTerminal() bool {
	for _, fd := range []int{int(os.Stdin.Fd()), int(os.Stdout.Fd())} {
		if _, err := unix.IoctlGetTermios(fd, unix.TCGETS); err == nil {
			return true
		}
	}
	return false
}
