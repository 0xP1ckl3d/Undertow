//go:build linux || windows

package main

import (
	"errors"
	"strings"
	"testing"
)

func TestClientTLSVerificationError(t *testing.T) {
	certificate := errors.New("CRYPTO_ERROR 0x12a (local): tls: failed to verify certificate: x509: cannot validate certificate for 203.0.113.10 because it doesn't contain any IP SANs")
	for _, carrier := range []string{"quic", "websocket"} {
		got := clientTLSVerificationError(certificate, carrier, false, "203.0.113.10:443")
		if got == nil || !strings.Contains(got.Error(), "--tls-insecure-skip-verify") || !strings.Contains(got.Error(), "203.0.113.10:443") || !errors.Is(got, certificate) {
			t.Fatalf("%s certificate error = %v", carrier, got)
		}
	}
	if got := clientTLSVerificationError(certificate, "quic", true, "203.0.113.10:443"); got != nil {
		t.Fatalf("skip-verify certificate error = %v", got)
	}
	if got := clientTLSVerificationError(errors.New("timeout"), "quic", false, "203.0.113.10:443"); got != nil {
		t.Fatalf("transient error = %v", got)
	}
}
