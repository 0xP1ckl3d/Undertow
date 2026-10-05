package websocket

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
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/deployment"
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

func TestWebSocketSession(t *testing.T) {
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
	server, err := Listen("127.0.0.1:0", "/undertow", cert, key, false, identity, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer server.Close()
	go func() { _ = server.Serve(ctx) }()
	_, port, _ := net.SplitHostPort(server.Addr().String())
	endpoint := net.JoinHostPort("localhost", port)
	options := DialOptions{Address: endpoint, Path: "/undertow", TLSInsecureSkipVerify: true}
	if _, err := Dial(ctx, DialOptions{Address: endpoint, Path: "/undertow"}, security.Fingerprint(identity), token, clientKey); err == nil {
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

func TestSelfSignedDirectIP(t *testing.T) {
	_, identity, _ := ed25519.GenerateKey(rand.Reader)
	_, clientKey, _ := ed25519.GenerateKey(rand.Reader)
	token := make([]byte, 32)
	_, _ = rand.Read(token)
	server, err := Listen("127.0.0.1:0", "/undertow", "", "", true, identity, token)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	options := DialOptions{Address: server.Addr().String(), Path: "/undertow", TLSInsecureSkipVerify: true}
	if _, err := Dial(ctx, options, security.Fingerprint(clientKey), token, clientKey); err == nil {
		t.Fatal("wrong identity accepted")
	}
	conn, err := Dial(ctx, options, security.Fingerprint(identity), token, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := server.Accept(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredPathAndRequestHeaders(t *testing.T) {
	_, identity, _ := ed25519.GenerateKey(rand.Reader)
	_, clientKey, _ := ed25519.GenerateKey(rand.Reader)
	token := make([]byte, 32)
	_, _ = rand.Read(token)
	profile := deployment.Default()
	profile.WebSocket.Path = "/site/session"
	profile.WebSocket.Headers = map[string]string{"User-Agent": "SiteClient/1", "X-Deployment": "blue"}
	server, err := Listen("127.0.0.1:0", profile.WebSocket.Path, "", "", true, identity, token, profile)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan http.Header, 1)
	server.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Clone()
		server.handle(w, r)
	})
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	conn, err := Dial(ctx, DialOptions{Address: server.Addr().String(), Path: profile.WebSocket.Path, TLSInsecureSkipVerify: true, Profile: profile}, security.Fingerprint(identity), token, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := server.Accept(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case headers := <-requests:
		if headers.Get("User-Agent") != "SiteClient/1" || headers.Get("X-Deployment") != "blue" {
			t.Fatalf("custom request headers missing: %v", headers)
		}
	case <-ctx.Done():
		t.Fatal("upgrade request was not captured")
	}
}
