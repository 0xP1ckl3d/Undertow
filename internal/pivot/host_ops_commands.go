package pivot

import (
	"context"
	"errors"
	"os/exec"
)

func inventoryCommand(ctx context.Context, program string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, program, args...)
	configureExecProcess(command)
	configureTokenProcess(ctx, command)
	stdout := &cappedWriter{limit: 32 << 10}
	stderr := &cappedWriter{limit: 4 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if stderr.buffer.Len() != 0 {
			return "", errors.New(stderr.buffer.String())
		}
		return "", err
	}
	return stdout.buffer.String(), nil
}
