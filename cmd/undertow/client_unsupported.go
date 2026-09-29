//go:build !linux && !windows

package main

import "errors"

func clientCommand([]string) error { return errors.New("VPN client requires Linux or Windows") }
