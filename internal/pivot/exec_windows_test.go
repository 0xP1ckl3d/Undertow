//go:build windows

package pivot

import (
	"os/exec"
	"testing"
)

func TestAgentExecHidesWindowsConsole(t *testing.T) {
	command := exec.Command("powershell.exe", "-NoProfile", "whoami")
	configureExecProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow || command.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("agent command would create a visible Windows console")
	}
}
