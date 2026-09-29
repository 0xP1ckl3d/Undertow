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

type ExecRequest struct {
	Argv []string `json:"argv"`
}

type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
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
	if err := validateArgv(argv); err != nil {
		return ExecResult{}, err
	}
	if agent == nil {
		return ExecResult{}, errors.New("agent is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	stream, err := agent.Open(ctx, ExecDestination)
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
	if err := json.NewEncoder(stream).Encode(ExecRequest{Argv: argv}); err != nil {
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

func serveExec(ctx context.Context, stream *mux.Stream) {
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
	if err := validateArgv(request.Argv); err != nil {
		writeExecResult(stream, ExecResult{Error: err.Error()})
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
