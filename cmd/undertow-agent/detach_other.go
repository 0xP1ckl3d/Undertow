//go:build !linux

package main

func detachIfInteractive() (bool, error) { return false, nil }
