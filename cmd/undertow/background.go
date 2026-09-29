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
)

const backgroundModeEnv = "UNDERTOW_BACKGROUND_CHILD"
const backgroundPIDEnv = "UNDERTOW_BACKGROUND_PID_FILE"

var backgroundStop <-chan struct{}

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
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return fmt.Errorf("background %s exited during startup: %v (see %s)", mode, err, logPath)
		case <-tick.C:
			raw, err := os.ReadFile(pidPath)
			if err != nil {
				continue
			}
			var state backgroundState
			if json.Unmarshal(raw, &state) == nil && state.PID == cmd.Process.Pid {
				fmt.Printf("%s process started: PID %d; log %s; stop with 'undertow %s --stop'\n", mode, state.PID, logPath, mode)
				return nil
			}
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return fmt.Errorf("background %s did not register within five seconds (see %s)", mode, logPath)
		}
	}
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
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				line, err := bufio.NewReader(io.LimitReader(conn, 129)).ReadString('\n')
				if err != nil || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(line)), []byte(state.Token)) != 1 {
					_, _ = io.WriteString(conn, "DENIED\n")
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
