package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func attachServer(args []string) error {
	f := flag.NewFlagSet("server attach", flag.ContinueOnError)
	pidFile := f.String("pid-file", "undertow-server.pid", "running server state file")
	controlAddress := f.String("control", "127.0.0.1:47889", "server operator API")
	controlToken := f.String("control-token-file", "control.key", "server operator token")
	logFile := f.String("log-file", "undertow-server.log", "server log file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("usage: undertow server attach [--pid-file PATH] [--control IP:PORT] [--control-token-file PATH] [--log-file PATH]")
	}
	pidPath, err := filepath.Abs(*pidFile)
	if err != nil {
		return err
	}
	logPath, err := filepath.Abs(*logFile)
	if err != nil {
		return err
	}
	return attachServerAt(pidPath, *controlAddress, *controlToken, logPath)
}

func attachServerAt(pidPath, controlAddress, controlToken, logPath string) error {
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("read server background state: %w; use 'undertow console' for a --foreground server", err)
	}
	var state backgroundState
	if err := json.Unmarshal(raw, &state); err != nil || state.PID < 1 || state.Address == "" {
		return errors.New("invalid server background state")
	}
	conn, err := net.DialTimeout("tcp4", state.Address, 2*time.Second)
	if err != nil {
		return fmt.Errorf("server worker PID %d is not responding: %w", state.PID, err)
	}
	_ = conn.Close()
	options, err := parseOperator([]string{"--control", controlAddress, "--control-token-file", controlToken})
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		if _, err := callControl(options, http.MethodGet, "/v1/status", nil); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("attach to server: %w; check --control and --control-token-file", lastErr)
	}
	features := consoleFeatures{
		serverLogPath:  logPath,
		serverAttached: true,
		stopServer: func() error {
			return stopBackground(pidPath)
		},
	}
	return consoleCommandWithOptions(options, features)
}
