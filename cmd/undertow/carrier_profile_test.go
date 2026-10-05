package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestDeploymentProfilePathAndExplicitFlag(t *testing.T) {
	file := filepath.Join(t.TempDir(), "deployment.json")
	if err := os.WriteFile(file, []byte(`{"websocket":{"path":"/site"},"quic":{"alpn":"site/2"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"--deployment-profile", file}, "/site"},
		{[]string{"--deployment-profile", file, "--websocket-path", "/override"}, "/override"},
	} {
		flags := flag.NewFlagSet("carrier", flag.ContinueOnError)
		carrier := addCarrierFlags(flags)
		if err := flags.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		if err := carrier.load(flags); err != nil {
			t.Fatal(err)
		}
		if got := carrier.webOptions("localhost:443").Path; got != tc.path {
			t.Fatalf("path = %q, want %q", got, tc.path)
		}
		if got := carrier.quicOptions("localhost:443").Profile.QUIC.ALPN; got != "site/2" {
			t.Fatalf("ALPN = %q", got)
		}
	}
}
