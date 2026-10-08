//go:build !windows

package main

func launchJumpAgent() (bool, error) { return false, nil }
