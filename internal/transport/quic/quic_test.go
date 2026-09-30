package quic

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/security"
)

func testCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestQUICSession(t *testing.T) {
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	cert, key := testCertificate(t)
	server, err := Listen("127.0.0.1:0", cert, key, identity, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer server.Close()
	go func() { _ = server.Serve(ctx) }()
	_, port, _ := net.SplitHostPort(server.Addr().String())
	endpoint := net.JoinHostPort("localhost", port)
	options := DialOptions{Address: endpoint, TLSInsecureSkipVerify: true}
	if _, err := Dial(ctx, DialOptions{Address: endpoint}, security.Fingerprint(identity), token, clientKey); err == nil {
		t.Fatal("self-signed TLS certificate unexpectedly verified")
	}
	if _, err := Dial(ctx, options, security.Fingerprint(clientKey), token, clientKey); err == nil {
		t.Fatal("wrong Undertow fingerprint was accepted")
	}
	client, err := Dial(ctx, options, security.Fingerprint(identity), token, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if host, _, err := net.SplitHostPort(client.RemoteAddr()); err != nil || net.ParseIP(host) == nil {
		t.Fatalf("carrier peer address %q: %v", client.RemoteAddr(), err)
	}
	peer, err := server.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if peer.Snapshot().AgentID != security.Fingerprint(clientKey)[:32] {
		t.Fatalf("wrong agent ID: %s", peer.Snapshot().AgentID)
	}
	if err := client.Send(ctx, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	message, err := peer.Channel().Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(message) != "hello" {
		t.Fatalf("received %q", message)
	}
	if err := peer.Channel().Send(ctx, []byte("world")); err != nil {
		t.Fatal(err)
	}
	message, err = client.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(message) != "world" {
		t.Fatalf("received %q", message)
	}
	serverMux := mux.New(ctx, peer.Channel(), true)
	clientMux := mux.New(ctx, client, false)
	defer serverMux.Close()
	defer clientMux.Close()
	if err := clientMux.SendControl(ctx, []byte("control")); err != nil {
		t.Fatal(err)
	}
	message, err = serverMux.RecvControl(ctx)
	if err != nil || string(message) != "control" {
		t.Fatalf("mux control %q: %v", message, err)
	}
}
