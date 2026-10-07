//go:build windows

package pivot

import "testing"

func TestSplitUNCAdministrativeSharePath(t *testing.T) {
	for _, test := range []struct {
		value, target, share, relative string
	}{
		{`\\app01\ADMIN$\Temp\agent.exe`, "app01", "ADMIN$", `Temp\agent.exe`},
		{`\\192.168.50.20\D$\Tools\agent.exe`, "192.168.50.20", "D$", `Tools\agent.exe`},
	} {
		target, share, relative, err := splitUNCPath(test.value)
		if err != nil || target != test.target || share != test.share || relative != test.relative {
			t.Fatalf("split %q = %q %q %q, %v", test.value, target, share, relative, err)
		}
	}
	for _, value := range []string{`C:\Windows\Temp\agent.exe`, `\\app01\Public\agent.exe`, `\\app01\ADMIN$\..\agent.exe`, `\\app01\ADMIN$\\agent.exe`} {
		if _, _, _, err := splitUNCPath(value); err == nil {
			t.Fatalf("accepted invalid path %q", value)
		}
	}
}
