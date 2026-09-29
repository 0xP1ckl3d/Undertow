//go:build !windows

package pivot

import "os/exec"

func configureExecProcess(*exec.Cmd) {}
