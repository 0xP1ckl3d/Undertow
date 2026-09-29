//go:build windows

package pivot

import (
	"os/exec"
	"syscall"
)

func configureExecProcess(command *exec.Cmd) {
	const createNoWindow = 0x08000000
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
