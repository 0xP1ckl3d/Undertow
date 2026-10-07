//go:build linux

package pivot

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func startInteractiveProcess(ctx context.Context, request InteractiveRequest) (interactiveProcess, io.ReadWriteCloser, func(uint16, uint16) error, error) {
	if request.Credential != nil {
		return nil, nil, nil, fmt.Errorf("Windows credentials require a Windows source agent")
	}
	argv := request.Argv
	if len(argv) == 0 {
		argv = []string{"/bin/sh"}
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open terminal: %w", err)
	}
	closeMaster := func(err error) (interactiveProcess, io.ReadWriteCloser, func(uint16, uint16) error, error) {
		master.Close()
		return nil, nil, nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return closeMaster(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		return closeMaster(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR, 0)
	if err != nil {
		return closeMaster(err)
	}
	resize := func(cols, rows uint16) error {
		if cols == 0 {
			cols = 80
		}
		if rows == 0 {
			rows = 24
		}
		return unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	}
	if err := resize(request.Cols, request.Rows); err != nil {
		slave.Close()
		return closeMaster(err)
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	err = command.Start()
	slave.Close()
	if err != nil {
		return closeMaster(err)
	}
	return command, master, resize, nil
}
