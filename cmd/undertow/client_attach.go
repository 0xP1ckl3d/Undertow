//go:build linux || windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

func attachClient(args []string) error {
	f := flag.NewFlagSet("client attach", flag.ContinueOnError)
	pidFile := f.String("pid-file", "undertow-client.pid", "running client state file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("usage: undertow client attach [--pid-file PATH]")
	}
	path, err := filepath.Abs(*pidFile)
	if err != nil {
		return err
	}
	return attachClientAt(path)
}

func attachClientAt(path string) error {
	if _, err := callClientConsole(path, consoleRPCRequest{Action: "session"}); err != nil {
		return fmt.Errorf("attach to VPN client: %w", err)
	}
	ctx, cancel := commandContext()
	defer cancel()
	caller := func(_ context.Context, method, route string, body any) ([]byte, error) {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		response, err := callClientConsole(path, consoleRPCRequest{Action: "call", Method: method, Path: route, Body: encoded})
		return response.Data, err
	}
	clientID := func() uint64 {
		response, err := callClientConsole(path, consoleRPCRequest{Action: "session"})
		if err != nil {
			return 0
		}
		return response.SessionID
	}
	routes := func(_ context.Context, args []string, output io.Writer) error {
		response, err := callClientConsole(path, consoleRPCRequest{Action: "routes", Args: args})
		if err == nil {
			_, _ = io.WriteString(output, response.Output)
		}
		return err
	}
	quit := func() {
		if err := stopBackground(path); err != nil {
			fmt.Fprintln(os.Stderr, "stop VPN:", err)
		}
	}
	return runConsole(ctx, os.Stdin, os.Stdout, caller, clientID, quit, routes, nil)
}

func callClientConsole(path string, request consoleRPCRequest) (consoleRPCResponse, error) {
	var response consoleRPCResponse
	raw, err := os.ReadFile(path)
	if err != nil {
		return response, err
	}
	var state backgroundState
	if err := json.Unmarshal(raw, &state); err != nil {
		return response, err
	}
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil || host != "127.0.0.1" {
		return response, errors.New("invalid local client control address")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	conn, err := net.DialTimeout("tcp4", state.Address, 3*time.Second)
	if err != nil {
		return response, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprintf(conn, "%s %s\n", state.Token, encoded); err != nil {
		return response, err
	}
	if err := json.NewDecoder(bufio.NewReader(io.LimitReader(conn, 4<<20))).Decode(&response); err != nil {
		return response, err
	}
	if response.Error != "" {
		return response, errors.New(response.Error)
	}
	return response, nil
}
