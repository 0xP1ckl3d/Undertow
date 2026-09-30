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
	for _, want := range []string{"pivot", "internal", "vpn", "vpn-internal", "forward", "--tun", "--internal", "--vpn", "--forward", "--fingerprint", "--token-file"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("examples missing %q", want)
		}
	}
}
