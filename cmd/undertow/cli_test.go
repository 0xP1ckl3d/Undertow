//go:build linux || windows

package main

import (
	"context"
	"strings"
	"testing"
)

func TestDuplicateModeArgumentIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func([]string) error
	}{
		{"server", serve}, {"client", clientCommand}, {"agent", agent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run([]string{tc.name, "--tun"})
			if err == nil || !strings.Contains(err.Error(), "unexpected "+tc.name+" argument") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestVerificationTarget(t *testing.T) {
	got, err := resolveVerificationTarget(context.Background(), "https://127.0.0.1/check")
	if err != nil || got != "127.0.0.1:443" {
		t.Fatalf("target=%q err=%v", got, err)
	}
	if _, err := resolveVerificationTarget(context.Background(), "file:///tmp/address"); err == nil {
		t.Fatal("accepted non-HTTP URL")
	}
}
