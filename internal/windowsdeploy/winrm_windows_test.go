//go:build windows

package windowsdeploy

import (
	"strings"
	"testing"
)

func TestWinRMProcessInputUsesWSManWithoutShellOrTask(t *testing.T) {
	input := winRMProcessInput(`C:\Windows\Temp\agent.exe`)
	if !strings.Contains(input, `<p:CommandLine>C:\Windows\Temp\agent.exe</p:CommandLine>`) {
		t.Fatalf("process input=%q", input)
	}
	for _, forbidden := range []string{"schtasks", "cmd.exe", "powershell", "_jump-launch"} {
		if strings.Contains(strings.ToLower(input), forbidden) {
			t.Fatalf("WSMan input contains %q: %q", forbidden, input)
		}
	}
}

func TestParseWinRMProcessResponse(t *testing.T) {
	const response = `<p:Create_OUTPUT xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/cimv2/Win32_Process"><p:ProcessId>4321</p:ProcessId><p:ReturnValue>0</p:ReturnValue></p:Create_OUTPUT>`
	processID, err := parseWinRMProcessResponse(response)
	if err != nil || processID != 4321 {
		t.Fatalf("processID=%d err=%v", processID, err)
	}
	if _, err := parseWinRMProcessResponse(strings.Replace(response, `<p:ReturnValue>0</p:ReturnValue>`, `<p:ReturnValue>5</p:ReturnValue>`, 1)); err == nil {
		t.Fatal("accepted failed Win32_Process.Create response")
	}
}
