//go:build linux

package main

import "os"

func doctorPrivileged() bool { return os.Geteuid() == 0 }
