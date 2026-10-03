//go:build !linux

package main

func setLocalOutputOwner(string) error { return nil }
