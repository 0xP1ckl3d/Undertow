//go:build windows && amd64

package pivot

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"undertow/internal/nativemodule"
)

// CLR-managed threads do not inherit a Go thread's impersonation token.
// Launch selected native modules in a worker whose process primary token is
// the selected identity, so every thread and child process starts there.
func executeNativeForContext(ctx context.Context, dll, args []byte, write func(byte, []byte) error) (int, error) {
	if operationToken(ctx) == 0 {
		return executeNative(ctx, dll, args, write)
	}
	return executeSelectedNativeWorker(ctx, false, dll, args, nil, write)
}

func executeNativeShellForContext(ctx context.Context, dll []byte, input <-chan []byte, write func(byte, []byte) error) (int, error) {
	if operationToken(ctx) == 0 {
		return executeNativeShell(ctx, dll, input, write)
	}
	return executeSelectedNativeWorker(ctx, true, dll, nil, input, write)
}

func executeSelectedNativeWorker(ctx context.Context, shell bool, dll, args []byte, input <-chan []byte, write func(byte, []byte) error) (int, error) {
	if !operationTokenCanLaunch(ctx) {
		return -1, errors.New("selected authentication context supports impersonation but lacks process-launch token rights")
	}
	executable, cleanup, err := bofWorkerExecutable(ctx)
	if err != nil {
		return -1, err
	}
	defer cleanup()
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	process, raw, _, err := startPipedInteractiveProcess(workerCtx, executable, []string{"_native-worker"})
	if err != nil {
		return -1, fmt.Errorf("start selected-token native worker: %w", err)
	}
	defer raw.Close()
	var header [9]byte
	if shell {
		header[0] = 1
	}
	binary.BigEndian.PutUint32(header[1:5], uint32(len(dll)))
	binary.BigEndian.PutUint32(header[5:9], uint32(len(args)))
	if _, err = raw.Write(header[:]); err == nil {
		_, err = raw.Write(dll)
	}
	if err == nil && len(args) != 0 {
		_, err = raw.Write(args)
	}
	if err != nil {
		cancel()
		_ = process.Wait()
		return -1, fmt.Errorf("send selected-token native module: %w", err)
	}
	inputDone := make(chan struct{})
	if shell {
		go func() {
			defer close(inputDone)
			for {
				select {
				case data, ok := <-input:
					if !ok {
						return
					}
					for len(data) > 0 {
						n := min(len(data), interactiveFrameLimit)
						if writeInteractiveFrame(raw, InteractiveInput, data[:n]) != nil {
							return
						}
						data = data[n:]
					}
				case <-workerCtx.Done():
					return
				}
			}
		}()
	} else {
		close(inputDone)
	}
	code, gotExit := -1, false
	var workerErr error
	for {
		kind, data, readErr := readInteractiveFrame(raw)
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				workerErr = fmt.Errorf("selected-token native worker output: %w", readErr)
			}
			break
		}
		switch kind {
		case InteractiveOutput, InteractiveStderr:
			if err := write(kind, data); err != nil {
				workerErr = err
			}
		case InteractiveError:
			workerErr = errors.New(string(data))
		case InteractiveExit:
			if len(data) != 4 {
				workerErr = errors.New("invalid selected-token native worker exit frame")
			} else {
				code = int(int32(binary.BigEndian.Uint32(data)))
				gotExit = true
			}
		default:
			workerErr = errors.New("invalid selected-token native worker output frame")
		}
		if workerErr != nil || gotExit {
			break
		}
	}
	cancel()
	_ = raw.Close()
	<-inputDone
	waitErr := process.Wait()
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if workerErr != nil {
		return -1, workerErr
	}
	if !gotExit {
		return -1, fmt.Errorf("selected-token native worker ended without an exit frame: %v", waitErr)
	}
	if waitErr != nil && code == 0 {
		return -1, fmt.Errorf("selected-token native worker exited unexpectedly: %w", waitErr)
	}
	return code, nil
}

// NativeWorkerMain is entered only by the short-lived agent child. The
// operation token itself never crosses the pipe; Windows grants the worker its
// selected process identity at creation.
func NativeWorkerMain(input io.Reader, output io.Writer) error {
	var header [9]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return err
	}
	shell := header[0] == 1
	size := binary.BigEndian.Uint32(header[1:5])
	argSize := binary.BigEndian.Uint32(header[5:9])
	if header[0] > 1 || size == 0 || size > nativemodule.MaxSize || argSize > 128<<10 || shell && argSize != 0 {
		return errors.New("invalid native worker request")
	}
	dll := make([]byte, size)
	if _, err := io.ReadFull(input, dll); err != nil {
		return err
	}
	args := make([]byte, argSize)
	if _, err := io.ReadFull(input, args); err != nil {
		return err
	}
	var writeMu sync.Mutex
	write := func(kind byte, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		for len(data) != 0 {
			n := min(len(data), interactiveFrameLimit)
			if err := writeInteractiveFrame(output, kind, data[:n]); err != nil {
				return err
			}
			data = data[n:]
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var code int
	var runErr error
	if shell {
		frames := bufio.NewReader(input)
		commands := make(chan []byte, 32)
		go func() {
			defer close(commands)
			defer cancel()
			for {
				kind, data, err := readInteractiveFrame(frames)
				if err != nil || kind != InteractiveInput {
					return
				}
				select {
				case commands <- data:
				case <-ctx.Done():
					return
				}
			}
		}()
		code, runErr = executeNativeShell(ctx, dll, commands, write)
	} else {
		code, runErr = executeNative(ctx, dll, args, write)
	}
	if runErr != nil {
		_ = write(InteractiveError, []byte(runErr.Error()))
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	_ = write(InteractiveExit, result[:])
	return nil
}
