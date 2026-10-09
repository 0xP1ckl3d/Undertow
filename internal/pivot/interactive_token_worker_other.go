//go:build !windows

package pivot

import (
	"errors"
	"io"
)

func ShellWorkerMain(io.Reader, io.Writer) error {
	return errors.New("shell worker requires Windows")
}
