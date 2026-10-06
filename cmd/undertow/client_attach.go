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

	"undertow/internal/control"
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
	if removed, err := removeDeadBackgroundState(path); err != nil {
		return err
	} else if removed {
		return errors.New("client process is no longer running; removed stale PID file")
	}
	if _, err := callClientConsole(path, consoleRPCRequest{Action: "session"}); err != nil {
		if removed, checkErr := removeDeadBackgroundState(path); checkErr == nil && removed {
			return errors.New("client process is no longer running; removed stale PID file")
		}
		return fmt.Errorf("attach to VPN client: %w", err)
	}
	if gui, err := callClientConsole(path, consoleRPCRequest{Action: "gui"}); err == nil && gui.Output != "" {
		fmt.Fprintf(os.Stdout, "Browser GUI: %s\nOpen this URL on the client host. Use 'undertow client gui' to show it again.\n\n", gui.Output)
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
	script := func(ctx context.Context, agentID, language string, source []byte) (*pivot.InteractiveSession, error) {
		return openAttachedScript(ctx, path, agentID, language, source)
	}
	wasm := func(ctx context.Context, agentID string, module []byte, args []string, stdin []byte) (*pivot.InteractiveSession, error) {
		return openAttachedWASM(ctx, path, agentID, module, args, stdin)
	}
	native := func(ctx context.Context, agentID string, module []byte, args []string, data []byte) (*pivot.InteractiveSession, error) {
		return openAttachedNative(ctx, path, agentID, module, args, data)
	}
	assemblyOpen := func(ctx context.Context, agentID string, source []byte, args []string) (*pivot.InteractiveSession, error) {
		return openAttachedAssembly(ctx, path, agentID, source, args)
	}
	bofOpen := func(ctx context.Context, agentID string, object, arguments []byte) (*pivot.InteractiveSession, error) {
		return openAttachedBOF(ctx, path, agentID, object, arguments)
	}
	transfer := func(ctx context.Context, request clientFileRequest, progress func(pivot.TransferProgress)) (pivot.FileMessage, error) {
		return attachedTransfer(ctx, path, request, progress)
	}
	return runConsole(ctx, os.Stdin, os.Stdout, caller, clientID, quit, routes, nil, consoleFeatures{open: opener, script: script, wasm: wasm, native: native, assembly: assemblyOpen, bof: bofOpen, transfer: transfer})
}

func openAttachedInteractive(ctx context.Context, path, agentID string, request pivot.InteractiveRequest) (*pivot.InteractiveSession, error) {
	conn, reader, err := openAttachedSession(ctx, path, agentID, "interactive")
	if err != nil {
		return nil, err
	}
	return pivot.StartInteractive(ctx, conn, reader, request)
}

func openAttachedScript(ctx context.Context, path, agentID, language string, source []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openAttachedSession(ctx, path, agentID, "script")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Language: language, Size: len(source)}, source)
}

func openAttachedWASM(ctx context.Context, path, agentID string, module []byte, args []string, stdin []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openAttachedSession(ctx, path, agentID, "wasm")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Args: args, Stdin: stdin, Size: len(module)}, module)
}

func openAttachedNative(ctx context.Context, path, agentID string, module []byte, args []string, data []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openAttachedSession(ctx, path, agentID, "native")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Args: args, Stdin: data, Size: len(module)}, module)
}

func openAttachedAssembly(ctx context.Context, path, agentID string, source []byte, args []string) (*pivot.InteractiveSession, error) {
	conn, reader, err := openAttachedSession(ctx, path, agentID, "assembly")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Args: args, Size: len(source)}, source)
}

func openAttachedBOF(ctx context.Context, path, agentID string, object, arguments []byte) (*pivot.InteractiveSession, error) {
	conn, reader, err := openAttachedSession(ctx, path, agentID, "bof")
	if err != nil {
		return nil, err
	}
	return pivot.StartMemorySession(ctx, conn, reader, pivot.MemoryRequest{Size: len(object), Stdin: arguments}, object)
}

func openAttachedSession(ctx context.Context, path, agentID, action string) (*net.TCPConn, *bufio.Reader, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var state backgroundState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, nil, err
	}
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil || host != "127.0.0.1" {
		return nil, nil, errors.New("invalid local client control address")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", state.Address)
	if err != nil {
		return nil, nil, err
	}
	encoded, _ := json.Marshal(consoleRPCRequest{Action: action, AgentID: agentID})
	if _, err := fmt.Fprintf(conn, "%s %s\n", state.Token, encoded); err != nil {
		conn.Close()
		return nil, nil, err
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if line != "OK\n" {
		conn.Close()
		return nil, nil, errors.New(strings.TrimSpace(line))
	}
	return conn.(*net.TCPConn), reader, nil
}

func attachedTransfer(ctx context.Context, path string, request clientFileRequest, progress func(pivot.TransferProgress)) (pivot.FileMessage, error) {
	var result pivot.FileMessage
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	var state backgroundState
	if err := json.Unmarshal(raw, &state); err != nil {
		return result, err
	}
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil || host != "127.0.0.1" {
		return result, errors.New("invalid local client control address")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", state.Address)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	body, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	encoded, err := json.Marshal(consoleRPCRequest{Action: "transfer-stream", Body: body})
	if err != nil {
		return result, err
	}
	if _, err := fmt.Fprintf(conn, "%s %s\n", state.Token, encoded); err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bufio.NewReader(conn))
	for {
		var event consoleTransferEvent
		if err := decoder.Decode(&event); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, err
		}
		if event.Error != "" {
			return result, errors.New(event.Error)
		}
		if event.Progress != nil && progress != nil {
			progress(*event.Progress)
		}
		if event.Result != nil {
			return *event.Result, nil
		}
	}
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
	} else if request.Action == "call" && control.IsForegroundRequest(request.Method, request.Path) {
		deadline = 40 * time.Hour
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
