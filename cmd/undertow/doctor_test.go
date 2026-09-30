package main

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAgentReportsCredentialBlockerAndRemedy(t *testing.T) {
	var output bytes.Buffer
	err := doctorCommand([]string{
		"agent", "--server", "127.0.0.1:1053",
		"--fingerprint", strings.Repeat("a", 64),
		"--token-file", filepath.Join(t.TempDir(), "missing.key"),
		"--pid-file", filepath.Join(t.TempDir(), "missing.pid"),
	}, &output)
	if err == nil || !strings.Contains(output.String(), "FAIL enrollment") || !strings.Contains(output.String(), "copy token.key") {
		t.Fatalf("err=%v output=%s", err, output.String())
	}
}

func TestDoctorAgentAcceptsLocalPrerequisites(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "token.key")
	if err := os.WriteFile(token, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := doctorCommand([]string{
		"agent", "--server", "127.0.0.1:1053",
		"--fingerprint", strings.Repeat("b", 64),
		"--token-file", token,
		"--pid-file", filepath.Join(dir, "missing.pid"),
	}, &output)
	if err != nil || !strings.Contains(output.String(), "Result: 0 fail(s)") {
		t.Fatalf("err=%v output=%s", err, output.String())
	}
}

func TestDoctorAddressAndOverlap(t *testing.T) {
	for _, bad := range []string{"example.com:53", "127.0.0.1:0", "127.0.0.1:65536", "bad"} {
		if _, _, err := doctorAddress(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if !doctorOverlap(netip.MustParsePrefix("10.20.0.0/16"), netip.MustParsePrefix("10.20.1.0/24")) {
		t.Fatal("missed overlap")
	}
	if doctorOverlap(netip.MustParsePrefix("10.20.0.0/16"), netip.MustParsePrefix("10.30.0.0/16")) {
		t.Fatal("reported unrelated networks as overlapping")
	}
}
