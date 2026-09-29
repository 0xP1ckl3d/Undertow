//go:build windows

package main

import "os"

func consoleSize(*os.File) (uint16, uint16) { return 80, 24 }
