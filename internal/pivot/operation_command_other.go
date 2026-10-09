//go:build !windows

package pivot

import (
	"context"
	"os/exec"
)

func startOperationCommand(_ context.Context, command *exec.Cmd) (operationProcess, error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	return &execOperationProcess{command: command}, nil
}
