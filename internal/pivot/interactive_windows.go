//go:build windows

package pivot

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
)

type interactivePipes struct {
	input       io.WriteCloser
	output      io.ReadCloser
	closeOutput io.WriteCloser
	mu          sync.Mutex
}

func (p *interactivePipes) Read(b []byte) (int, error)  { return p.output.Read(b) }
func (p *interactivePipes) Write(b []byte) (int, error) { return p.input.Write(b) }
func (p *interactivePipes) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.input.Close()
	_ = p.closeOutput.Close()
	return p.output.Close()
}

func startInteractiveProcess(ctx context.Context, request InteractiveRequest) (*exec.Cmd, io.ReadWriteCloser, func(uint16, uint16) error, error) {
	argv := request.Argv
	if len(argv) == 0 {
		argv = []string{"cmd.exe"}
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	configureExecProcess(command)
	command.Env = append(os.Environ(), "PROMPT=$P$G")
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	read, write := io.Pipe()
	command.Stdout, command.Stderr = write, write
	if err := command.Start(); err != nil {
		stdin.Close()
		read.Close()
		write.Close()
		return nil, nil, nil, err
	}
	return command, &interactivePipes{input: stdin, output: read, closeOutput: write}, func(uint16, uint16) error { return nil }, nil
}
