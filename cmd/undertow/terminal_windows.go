//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func isConsoleTerminal(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

func setConsoleRaw(file *os.File) (func(), error) {
	handle := windows.Handle(file.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	raw := mode &^ (windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT)
	raw |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	if err := windows.SetConsoleMode(handle, raw); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(handle, mode) }, nil
}
