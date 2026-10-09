//go:build windows && amd64

package pivot

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"undertow/internal/bof"
)

func executeBOFWorker(ctx context.Context, object, arguments []byte, output func(byte, []byte) error) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return -1, err
	}
	command := exec.CommandContext(ctx, executable, "_bof-worker")
	configureTokenProcess(ctx, command)
	var input bytes.Buffer
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(object)))
	binary.BigEndian.PutUint32(header[4:], uint32(len(arguments)))
	input.Write(header[:])
	input.Write(object)
	input.Write(arguments)
	command.Stdin = &input
	stdout, err := command.StdoutPipe()
	if err != nil {
		return -1, err
	}
	var stderr strings.Builder
	command.Stderr = &limitedWriter{writer: &stderr, limit: 4096}
	if err := command.Start(); err != nil {
		return -1, fmt.Errorf("start BOF worker: %w", err)
	}
	code := -1
	exited := false
	workerError := ""
	for {
		kind, data, readErr := readInteractiveFrame(stdout)
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				workerError = fmt.Sprintf("BOF worker output: %v", readErr)
			}
			break
		}
		switch kind {
		case bof.WorkerOutput, bof.WorkerStderr, bof.WorkerCallback:
			if err := output(kind, data); err != nil {
				_ = command.Process.Kill()
				workerError = err.Error()
			}
		case bof.WorkerError:
			workerError = string(data)
		case bof.WorkerExit:
			if len(data) != 4 {
				workerError = "invalid BOF worker exit frame"
			} else {
				code = int(int32(binary.BigEndian.Uint32(data)))
				exited = true
			}
			goto complete
		default:
			workerError = "invalid BOF worker output kind"
			goto complete
		}
		if workerError != "" {
			break
		}
	}
complete:
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return -1, fmt.Errorf("BOF runtime: %w", ctx.Err())
	}
	if workerError != "" {
		return -1, errors.New(workerError)
	}
	if waitErr != nil {
		return -1, fmt.Errorf("BOF worker exited unexpectedly: %w (%s)", waitErr, stderr.String())
	}
	if !exited {
		return -1, errors.New("BOF worker ended without an exit frame")
	}
	return code, nil
}

type limitedWriter struct {
	writer io.Writer
	limit  int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.limit > 0 {
		take := n
		if take > w.limit {
			take = w.limit
		}
		_, _ = w.writer.Write(p[:take])
		w.limit -= take
	}
	return n, nil
}
