//go:build !windows

package main

import "undertow/internal/agent"

func runWindowsService(agent.Config) (bool, error) { return false, nil }
