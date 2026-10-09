package pivot

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
)

type operationProcess interface {
	Wait() error
	Kill() error
}

type execOperationProcess struct{ command *exec.Cmd }

func (p *execOperationProcess) Wait() error {
	err := p.command.Wait()
	if closer, ok := p.command.Stdout.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	if closer, ok := p.command.Stderr.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	return err
}
func (p *execOperationProcess) Kill() error {
	if p.command.Process == nil {
		return errors.New("process has not started")
	}
	return p.command.Process.Kill()
}

type operationExitError struct{ code int }

func (e operationExitError) Error() string { return "exit status " + strconv.Itoa(e.code) }
func (e operationExitError) ExitCode() int { return e.code }

func operationExitCode(err error) (int, bool) {
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return exit.ExitCode(), true
	}
	return 0, false
}

func runOperationCommand(ctx context.Context, command *exec.Cmd) error {
	process, err := startOperationCommand(ctx, command)
	if err != nil {
		return err
	}
	return process.Wait()
}
