//go:build linux

package main

import (
	"os"
	"golang.org/x/sys/unix"
)

func consoleSize(file *os.File) (uint16, uint16) {
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil || size.Col == 0 || size.Row == 0 { return 80, 24 }
	return size.Col, size.Row
}
