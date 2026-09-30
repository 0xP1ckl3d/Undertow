//go:build windows

package main

import "golang.org/x/sys/windows"

func doctorPrivileged() bool { return windows.Token(0).IsElevated() }
