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
	"strings"
	"time"

	"undertow/internal/pivot"
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
		action := "call"
		if route == "/v1/file/transfer" {
			action = "transfer"
		}
		response, err := callClientConsole(path, consoleRPCRequest{Action: action, Method: method, Path: route, Body: encoded})
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
	opener := func(ctx context.Context, agentID string, request pivot.InteractiveRequest) (*pivot.InteractiveSession, error) {
		return openAttachedInteractive(ctx, path, agentID, request)
	}
	return runConsole(ctx, os.Stdin, os.Stdout, caller, clientID, quit, routes, nil, opener)
}

func openAttachedInteractive(ctx context.Context, path, agentID string, request pivot.InteractiveRequest) (*pivot.InteractiveSession, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state backgroundState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil || host != "127.0.0.1" {
		return nil, errors.New("invalid local client control address")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", state.Address)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(consoleRPCRequest{Action: "interactive", AgentID: agentID})
	if _, err := fmt.Fprintf(conn, "%s %s\n", state.Token, encoded); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if line != "OK\n" {
		conn.Close()
		return nil, errors.New(strings.TrimSpace(line))
	}
	return pivot.StartInteractive(ctx, conn.(*net.TCPConn), reader, request)
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
	deadline := 30 * time.Second
	if request.Action == "transfer" {
		deadline = 30 * time.Minute
	} else if request.Action == "call" && request.Method == "GET" {
		deadline = 5 * time.Second
	}
	_ = conn.SetDeadline(time.Now().Add(deadline))
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
