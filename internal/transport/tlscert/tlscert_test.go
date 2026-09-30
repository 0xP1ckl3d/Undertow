package tlscert

import (
	"crypto/x509"
	"strings"
	"testing"
)

func TestEphemeralSelfSigned(t *testing.T) {
	first, err := Load("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Fatal("certificate was reused")
	}
	parsed, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.CheckSignature(parsed.SignatureAlgorithm, parsed.RawTBSCertificate, parsed.Signature); err != nil {
		t.Fatal(err)
	}
	if len(parsed.ExtKeyUsage) != 1 || parsed.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatal("missing server auth usage")
	}
}

func TestOptions(t *testing.T) {
	for _, args := range [][3]interface{}{{"cert", "key", true}, {"", "", false}, {"cert", "", false}} {
		_, err := Load(args[0].(string), args[1].(string), args[2].(bool))
		if err == nil || !strings.Contains(err.Error(), "--tls-") {
			t.Fatalf("unexpected result: %v", err)
		}
	}
}
