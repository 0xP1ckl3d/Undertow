//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func isConsoleTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}

func enableConsoleOutput(file *os.File) (func(), bool) {
	if !isConsoleTerminal(file) {
		return nil, false
	}
	return func() {}, true
}

func setConsoleRaw(file *os.File) (func(), error) {
	fd := int(file.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	raw.Iflag &^= unix.ICRNL
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, old) }, nil
}
