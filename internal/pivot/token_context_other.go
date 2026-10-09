//go:build !windows

package pivot

import (
	"context"
	"errors"
	"os/exec"
)

func configureTokenProcess(context.Context, *exec.Cmd) {}
func enterTokenThread(ctx context.Context) (func() error, error) {
	if ctx.Value(tokenLeaseKey{}) != nil {
		return nil, errors.New("authentication contexts require Windows")
	}
	return func() error { return nil }, nil
}
