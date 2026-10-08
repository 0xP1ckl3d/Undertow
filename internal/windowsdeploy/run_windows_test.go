//go:build windows

package windowsdeploy

import (
	"strings"
	"testing"
)

func TestWinRMUsesDirectNativeLaunch(t *testing.T) {
	args := winRMArguments("file01", `C:\Windows\Temp\agent.exe`)
	joined := strings.ToLower(strings.Join(args, " "))
	if len(args) != 3 || args[0] != "-r:file01" || args[1] != `C:\Windows\Temp\agent.exe` || args[2] != "_jump-launch" {
		t.Fatalf("WinRM arguments=%q", args)
	}
	for _, forbidden := range []string{"schtasks", "cmd.exe", "powershell", "wmi", "sc.exe"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("direct WinRM arguments contain %q: %q", forbidden, args)
		}
	}
}
