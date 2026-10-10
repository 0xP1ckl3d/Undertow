//go:build !windows || !amd64

package pivot

import (
	"context"
	"errors"
	"io"
)

func executeNativeForContext(ctx context.Context, dll, args []byte, write func(byte, []byte) error) (int, error) {
	return executeNative(ctx, dll, args, write)
}

func executeNativeShellForContext(ctx context.Context, dll []byte, input <-chan []byte, write func(byte, []byte) error) (int, error) {
	return executeNativeShell(ctx, dll, input, write)
}

func NativeWorkerMain(io.Reader, io.Writer) error {
	return errors.New("native worker requires Windows amd64")
}
