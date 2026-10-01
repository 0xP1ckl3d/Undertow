package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestExamplesCoverPrimaryModes(t *testing.T) {
	var output bytes.Buffer
	if err := writeExamples(&output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pivot", "internal", "vpn", "vpn-internal", "forward", "--tun", "--internal", "--vpn", "--forward", "--fingerprint", "token.key", "--transport quic", "--tls-insecure-skip-verify"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("examples missing %q", want)
		}
	}
}

func TestHelpRelayIncludesConsoleUsage(t *testing.T) {
	var output bytes.Buffer
	if err := writeHelp(&output, "relay"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"relay start [BIND]", "relay list", "relay stop [BIND]", "--transport relay"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("relay help missing %q", want)
		}
	}
}
