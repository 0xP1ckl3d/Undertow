package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/security"
	"undertow/internal/transport/dns"
)

func TestTrustOnFirstUseThenPinnedReconnect(t *testing.T) {
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{0x59}, 32)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv, err := dns.Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	path := filepath.Join(t.TempDir(), "server.fingerprint")
	address := srv.Addr().String()
	fingerprint, save, err := resolveServerFingerprint(ctx, address, "t.undertow.invalid", "", path, true)
	if err != nil || !save || fingerprint != security.Fingerprint(identity) {
		t.Fatalf("first use fingerprint=%q save=%v err=%v", fingerprint, save, err)
	}
	client, err := dns.Dial(ctx, address, "t.undertow.invalid", fingerprint, token, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if err := saveServerFingerprint(path, fingerprint); err != nil {
		t.Fatal(err)
	}
	pinned, save, err := resolveServerFingerprint(ctx, address, "t.undertow.invalid", "", path, false)
	if err != nil || save || pinned != fingerprint {
		t.Fatalf("saved fingerprint=%q save=%v err=%v", pinned, save, err)
	}
	client, err = dns.Dial(ctx, address, "t.undertow.invalid", pinned, token, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
}
