package pivot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"undertow/internal/mux"
)

const ExecDestination = "exec.undertow.invalid:0"
const HostOpsDestination = "hostops.undertow.invalid:0"

type ExecRequest struct {
	Argv    []string `json:"argv,omitempty"`
	Builtin string   `json:"builtin,omitempty"`
	Args    []string `json:"args,omitempty"`
}

type ExecResult struct {
	Stdout   string       `json:"stdout"`
	Stderr   string       `json:"stderr"`
	ExitCode int          `json:"exit_code"`
	Error    string       `json:"error,omitempty"`
	Files    *FileListing `json:"files,omitempty"`
	Screens  []ScreenInfo `json:"screens,omitempty"`
}

type cappedWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if left := w.limit - w.buffer.Len(); left > 0 {
		if left < len(p) {
			p = p[:left]
		}
		_, _ = w.buffer.Write(p)
	}
	return n, nil
}

func validateArgv(argv []string) error {
	if len(argv) == 0 || len(argv) > 32 || argv[0] == "" {
		return errors.New("provide an executable and up to 31 arguments")
	}
	total := 0
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return errors.New("arguments cannot contain NUL")
		}
		total += len(arg)
		if total > 4096 {
			return errors.New("command arguments exceed 4096 bytes")
		}
	}
	return nil
}

// Execute runs one agent command over its authenticated mux session.
func Execute(ctx context.Context, agent *mux.Mux, argv []string) (ExecResult, error) {
	return ExecuteRequest(ctx, agent, ExecRequest{Argv: argv})
}

// ExecuteRequest runs a direct executable or a built-in host operation.
func ExecuteRequest(ctx context.Context, agent *mux.Mux, request ExecRequest) (ExecResult, error) {
	if err := validateExecRequest(request); err != nil {
		return ExecResult{}, err
	}
	if agent == nil {
		return ExecResult{}, errors.New("agent is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	destination := ExecDestination
	if request.Builtin != "" {
		destination = HostOpsDestination
	}
	stream, err := agent.Open(ctx, destination)
	if err != nil {
		return ExecResult{}, err
	}
	defer stream.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-done:
		}
	}()
	if err := json.NewEncoder(stream).Encode(request); err != nil {
		return ExecResult{}, err
	}
	if err := stream.CloseWrite(); err != nil {
		return ExecResult{}, err
	}
	response, err := io.ReadAll(io.LimitReader(stream, (512<<10)+1))
	if err != nil {
		return ExecResult{}, err
	}
	if len(response) > 512<<10 {
		return ExecResult{}, errors.New("agent command response too large")
	}
	var result ExecResult
	if err := json.Unmarshal(response, &result); err != nil {
		return ExecResult{}, fmt.Errorf("agent command response: %w", err)
	}
	return result, nil
}

func validateExecRequest(request ExecRequest) error {
	if request.Builtin == "" {
		if len(request.Args) != 0 {
			return errors.New("built-in arguments require a built-in command")
		}
		return validateArgv(request.Argv)
	}
	if len(request.Argv) != 0 {
		return errors.New("choose a built-in command or executable")
	}
	if len(request.Args) > 2 {
		return errors.New("too many built-in arguments")
	}
	for _, arg := range request.Args {
		if strings.ContainsRune(arg, 0) || len(arg) > 4096 {
			return errors.New("invalid built-in argument")
		}
	}
	return validateBuiltin(request.Builtin, request.Args)
}

func serveExec(ctx context.Context, stream *mux.Stream, hostOps bool) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, 10*time.Second)
	requestDone := make(chan struct{})
	watchStopped := make(chan struct{})
	go func() {
		defer close(watchStopped)
		select {
		case <-requestCtx.Done():
			_ = stream.Close()
		case <-requestDone:
		}
	}()
	var request ExecRequest
	err := json.NewDecoder(io.LimitReader(stream, 8193)).Decode(&request)
	close(requestDone)
	<-watchStopped
	requestCancel()
	if err != nil {
		writeExecResult(stream, ExecResult{Error: "invalid command request"})
		return
	}
	if err := validateExecRequest(request); err != nil {
		writeExecResult(stream, ExecResult{Error: err.Error()})
		return
	}
	if hostOps != (request.Builtin != "") {
		writeExecResult(stream, ExecResult{Error: "command type does not match stream capability"})
		return
	}
	if request.Builtin != "" {
		result := runBuiltin(ctx, request.Builtin, request.Args)
		writeExecResult(stream, result)
		return
	}
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-commandCtx.Done():
		}
	}()
	command := exec.CommandContext(commandCtx, request.Argv[0], request.Argv[1:]...)
	configureExecProcess(command)
	stdout := &cappedWriter{limit: 32 << 10}
	stderr := &cappedWriter{limit: 32 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	err = command.Run()
	result := ExecResult{Stdout: stdout.buffer.String(), Stderr: stderr.buffer.String()}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.ExitCode = exit.ExitCode()
		} else {
			result.Error = err.Error()
		}
	}
	if commandCtx.Err() != nil {
		result.Error = commandCtx.Err().Error()
	}
	writeExecResult(stream, result)
}

func writeExecResult(stream *mux.Stream, result ExecResult) {
	_ = json.NewEncoder(stream).Encode(result)
	_ = stream.CloseWrite()
}
