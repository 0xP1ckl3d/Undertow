//go:build windows

package windowsdeploy

import (
	"slices"
	"strings"
	"testing"
)

func TestWinRMUsesDirectAgentLaunch(t *testing.T) {
	args := winRMArguments("app01", `C:\Windows\Temp\agent.exe`)
	if !slices.Equal(args, []string{"-r:app01", `C:\Windows\Temp\agent.exe`, "_jump-launch"}) {
		t.Fatalf("WinRM arguments = %q", args)
	}
	command := strings.ToLower(strings.Join(args, " "))
	if strings.Contains(command, "cmd.exe") || strings.Contains(command, "schtasks") || strings.Contains(command, "powershell") {
		t.Fatalf("WinRM delegated through another launch method: %s", command)
	}
}
