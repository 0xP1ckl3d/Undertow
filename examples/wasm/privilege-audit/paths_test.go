package main

import (
	"reflect"
	"testing"
)

func TestUnquotedPathProbes(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`C:\Program Files\Example App\service.exe -k run`, []string{`C:\Program.exe`, `C:\Program Files\Example.exe`}},
		{`"C:\Program Files\Example App\service.exe" -k run`, nil},
		{`C:\Tools\service.exe -k value with spaces`, nil},
		{`C:\Tools\service.dll`, nil},
	} {
		if got := unquotedPathProbes(tc.command); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.command, got, tc.want)
		}
	}
	if got := parent(`C:\Program.exe`); got != `C:\` {
		t.Errorf("root parent: %q", got)
	}
}
