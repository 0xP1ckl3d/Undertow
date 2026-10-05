//go:build !linux && !windows

package control

func currentPrivilege() string { return "" }
