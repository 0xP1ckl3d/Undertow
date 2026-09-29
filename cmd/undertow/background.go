package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"undertow/internal/pivot"
)

const backgroundModeEnv = "UNDERTOW_BACKGROUND_CHILD"
const backgroundPIDEnv = "UNDERTOW_BACKGROUND_PID_FILE"

var backgroundStop <-chan struct{}
var backgroundConsoleHandler func(context.Context, consoleRPCRequest) consoleRPCResponse
var backgroundInteractiveHandler func(context.Context, string, net.Conn) error
var backgroundScriptHandler func(context.Context, string, net.Conn) error
var backgroundTransferHandler func(context.Context, json.RawMessage, func(pivot.TransferProgress)) (pivot.FileMessage, error)
var backgroundConsoleMu sync.RWMutex

func setBackgroundConsoleHandler(handler func(context.Context, consoleRPCRequest) consoleRPCResponse) {
	backgroundConsoleMu.Lock()
	backgroundConsoleHandler = handler
	backgroundConsoleMu.Unlock()
}

func setBackgroundInteractiveHandler(handler func(context.Context, string, net.Conn) error) {
	backgroundConsoleMu.Lock()
	backgroundInteractiveHandler = handler
	backgroundConsoleMu.Unlock()
}

func setBackgroundScriptHandler(handler func(context.Context, string, net.Conn) error) {
	backgroundConsoleMu.Lock()
	backgroundScriptHandler = handler
	backgroundConsoleMu.Unlock()
}

func setBackgroundTransferHandler(handler func(context.Context, json.RawMessage, func(pivot.TransferProgress)) (pivot.FileMessage, error)) {
	backgroundConsoleMu.Lock()
	backgroundTransferHandler = handler
	backgroundConsoleMu.Unlock()
}

type consoleTransferEvent struct {
	Progress *pivot.TransferProgress `json:"progress,omitempty"`
	Result   *pivot.FileMessage      `json:"result,omitempty"`
	Error    string                  `json:"error,omitempty"`
}

type consoleRPCRequest struct {
	Action  string          `json:"action"`
	AgentID string          `json:"agent_id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Path    string          `json:"path,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
	Args    []string        `json:"args,omitempty"`
}

type consoleRPCResponse struct {
	Data      json.RawMessage `json:"data,omitempty"`
	Output    string          `json:"output,omitempty"`
	SessionID uint64          `json:"session_id,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type lifecycleFlags struct {
	mode       string
	background *bool
	foreground *bool
	stop       *bool
	logFile    *string
	pidFile    *string
}

func addLifecycleFlags(f *flag.FlagSet, mode string) *lifecycleFlags {
	return &lifecycleFlags{
		mode:       mode,
		background: f.Bool("background", false, "run in the background"),
		foreground: f.Bool("foreground", false, "run in the foreground (default)"),
		stop:       f.Bool("stop", false, "gracefully stop the background process"),
		logFile:    f.String("log-file", "undertow-"+mode+".log", "background log path"),
		pidFile:    f.String("pid-file", "undertow-"+mode+".pid", "background process state path"),
	}
}

func (l *lifecycleFlags) handle(args []string) (bool, func(), error) {
	if *l.background && *l.foreground || *l.stop && (*l.background || *l.foreground) {
		return false, nil, errors.New("choose one of --foreground, --background, or --stop")
	}
	pidPath, err := filepath.Abs(*l.pidFile)
	if err != nil {
		return false, nil, err
	}
	if *l.stop {
		return true, nil, stopBackground(pidPath)
	}
	if os.Getenv(backgroundModeEnv) == l.mode {
		if *l.background {
			return false, nil, errors.New("background child must run in foreground")
		}
		if expected := os.Getenv(backgroundPIDEnv); expected != pidPath {
			return false, nil, errors.New("background state path mismatch")
		}
		cleanup, err := startBackgroundControl(pidPath)
		return false, cleanup, err
	}
	if *l.background {
		logPath, err := filepath.Abs(*l.logFile)
		if err != nil {
			return false, nil, err
		}
		return true, nil, launchBackground(l.mode, args, logPath, pidPath)
	}
	return false, nil, nil
}

type backgroundState struct {
	PID     int    `json:"pid"`
	Address string `json:"address"`
	Token   string `json:"token"`
	Ready   bool   `json:"ready"`
}

func launchBackground(mode string, args []string, logPath, pidPath string) error {
	if _, err := os.Lstat(pidPath); err == nil {
		return fmt.Errorf("background state %s already exists; use '%s --stop' or inspect and remove a stale file", pidPath, mode)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logOffset, err := logFile.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	childArgs := []string{mode}
	for _, arg := range args {
		if arg == "--background" || arg == "-background" || arg == "--background=true" || arg == "-background=true" {
			continue
		}
		childArgs = append(childArgs, arg)
	}
	cmd := exec.Command(exe, childArgs...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), backgroundModeEnv+"="+mode, backgroundPIDEnv+"="+pidPath)
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	startupTimeout := 15 * time.Second
	if mode == "agent" {
		startupTimeout = 30 * time.Second
	} else if mode == "client" {
		startupTimeout = 60 * time.Second
	}
	deadline := time.NewTimer(startupTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return backgroundStartupError(mode, err, logPath, logOffset)
		case <-tick.C:
			raw, err := os.ReadFile(pidPath)
			if err != nil {
				continue
			}
			var state backgroundState
			if json.Unmarshal(raw, &state) == nil && state.PID == cmd.Process.Pid && state.Ready {
				select {
				case err := <-done:
					return backgroundStartupError(mode, err, logPath, logOffset)
				default:
				}
				status := map[string]string{"server": "listening", "agent": "connected", "client": "VPN active"}[mode]
				fmt.Printf("%s %s in background: PID %d; log %s; stop with 'undertow %s --stop'\n", mode, status, state.PID, logPath, mode)
				return nil
			}
		case <-deadline.C:
			_ = stopBackground(pidPath)
			select {
			case err := <-done:
				if err != nil {
					return backgroundStartupError(mode, err, logPath, logOffset)
				}
				return fmt.Errorf("background %s did not become ready within %s (log %s)", mode, startupTimeout, logPath)
			case <-time.After(15 * time.Second):
				return fmt.Errorf("background %s did not become ready within %s; stop was requested but PID %d is still running (log %s)", mode, startupTimeout, cmd.Process.Pid, logPath)
			}
		}
	}
}

func backgroundStartupError(mode string, childErr error, logPath string, offset int64) error {
	file, err := os.Open(logPath)
	if err == nil {
		defer file.Close()
		if _, err = file.Seek(offset, io.SeekStart); err == nil {
			data, _ := io.ReadAll(io.LimitReader(file, 16<<10))
			for _, line := range strings.Split(string(data), "\n") {
				if _, detail, found := strings.Cut(strings.TrimSpace(line), "error: "); found {
					return fmt.Errorf("background %s failed: %s (log %s)", mode, detail, logPath)
				}
			}
		}
	}
	return fmt.Errorf("background %s exited during startup: %v (log %s)", mode, childErr, logPath)
}

func markBackgroundReady() error {
	path := os.Getenv(backgroundPIDEnv)
	if path == "" || os.Getenv(backgroundModeEnv) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read background state: %w", err)
	}
	var state backgroundState
	if err := json.Unmarshal(raw, &state); err != nil || state.PID != os.Getpid() {
		return errors.New("background state does not match this process")
	}
	if state.Ready {
		return nil
	}
	state.Ready = true
	raw, err = json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return fmt.Errorf("mark background ready: %w", err)
	}
	return nil
}

func startBackgroundControl(pidPath string) (func(), error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		listener.Close()
		return nil, err
	}
	state := backgroundState{PID: os.Getpid(), Address: listener.Addr().String(), Token: hex.EncodeToString(secret)}
	raw, err := json.Marshal(state)
	if err != nil {
		listener.Close()
		return nil, err
	}
	file, err := os.OpenFile(pidPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		listener.Close()
		return nil, err
	}
	if _, err = file.Write(raw); err != nil {
		file.Close()
		listener.Close()
		os.Remove(pidPath)
		return nil, err
	}
	if err = file.Close(); err != nil {
		listener.Close()
		os.Remove(pidPath)
		return nil, err
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	backgroundStop = stop
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
				line, err := bufio.NewReader(io.LimitReader(conn, 6<<20)).ReadString('\n')
				if err != nil {
					_, _ = io.WriteString(conn, "DENIED\n")
					return
				}
				line = strings.TrimSuffix(line, "\n")
				token, request, hasRequest := strings.Cut(line, " ")
				if subtle.ConstantTimeCompare([]byte(token), []byte(state.Token)) != 1 {
					_, _ = io.WriteString(conn, "DENIED\n")
					return
				}
				if hasRequest {
					var input consoleRPCRequest
					response := consoleRPCResponse{}
					if err := json.Unmarshal([]byte(request), &input); err != nil {
						response.Error = "invalid console request"
					} else {
						if input.Action == "interactive" || input.Action == "script" {
							_ = conn.SetDeadline(time.Time{})
							backgroundConsoleMu.RLock()
							interactive := backgroundInteractiveHandler
							if input.Action == "script" {
								interactive = backgroundScriptHandler
							}
							backgroundConsoleMu.RUnlock()
							if interactive == nil {
								_, _ = io.WriteString(conn, "ERROR client console is unavailable\n")
							} else if err := interactive(context.Background(), input.AgentID, conn); err != nil {
								_, _ = io.WriteString(conn, "ERROR "+err.Error()+"\n")
							}
							return
						}
						if input.Action == "transfer-stream" {
							_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
							backgroundConsoleMu.RLock()
							transfer := backgroundTransferHandler
							backgroundConsoleMu.RUnlock()
							encoder := json.NewEncoder(conn)
							if transfer == nil {
								_ = encoder.Encode(consoleTransferEvent{Error: "client console is unavailable"})
								return
							}
							transferCtx, cancel := context.WithCancel(context.Background())
							defer cancel()
							go func() { _, _ = io.Copy(io.Discard, conn); cancel() }()
							result, err := transfer(transferCtx, input.Body, func(progress pivot.TransferProgress) {
								if encoder.Encode(consoleTransferEvent{Progress: &progress}) != nil {
									cancel()
								}
							})
							if err != nil {
								_ = encoder.Encode(consoleTransferEvent{Error: err.Error()})
							} else {
								_ = encoder.Encode(consoleTransferEvent{Result: &result})
							}
							return
						}
						if input.Action == "transfer" {
							_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
						}
						backgroundConsoleMu.RLock()
						handler := backgroundConsoleHandler
						backgroundConsoleMu.RUnlock()
						if handler == nil {
							response.Error = "client console is unavailable"
						} else {
							response = handler(context.Background(), input)
						}
					}
					_ = json.NewEncoder(conn).Encode(response)
					return
				}
				_, _ = io.WriteString(conn, "OK\n")
				stopOnce.Do(func() { close(stop) })
			}(conn)
		}
	}()
	return func() {
		listener.Close()
		backgroundStop = nil
		if current, err := os.ReadFile(pidPath); err == nil {
			var saved backgroundState
			if json.Unmarshal(current, &saved) == nil && saved.PID == state.PID && saved.Token == state.Token {
				_ = os.Remove(pidPath)
			}
		}
	}, nil
}

func stopBackground(pidPath string) error {
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return err
	}
	var state backgroundState
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(state.Address)
	if err != nil || host != "127.0.0.1" {
		return errors.New("invalid background control address")
	}
	conn, err := net.DialTimeout("tcp4", state.Address, 3*time.Second)
	if err != nil {
		return fmt.Errorf("background PID %d is not responding: %w", state.PID, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintln(conn, state.Token); err != nil {
		return err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || reply != "OK\n" {
		return errors.New("background process refused stop request")
	}
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		if _, err := os.Stat(pidPath); errors.Is(err, os.ErrNotExist) {
			fmt.Printf("background process %d stopped\n", state.PID)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("stop requested for PID %d; waiting for cleanup (state: %s)", state.PID, pidPath)
}

func commandContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	stopCh := backgroundStop
	if stopCh != nil {
		go func() {
			select {
			case <-stopCh:
				stop()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, stop
}
