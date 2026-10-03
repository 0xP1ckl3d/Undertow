//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func consoleSize(file *os.File) (uint16, uint16) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(file.Fd()), &info); err != nil {
		return 80, 24
	}
	cols := info.Window.Right - info.Window.Left + 1
	rows := info.Window.Bottom - info.Window.Top + 1
	if cols <= 0 || rows <= 0 {
		return 80, 24
	}
	return uint16(cols), uint16(rows)
}
