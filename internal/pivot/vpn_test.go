package pivot

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/security"
	"undertow/internal/transport/dns"
)

func TestVPNEgressOverDirectDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{91}, 32)
	srv, err := dns.Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	serverMuxCh := make(chan *mux.Mux, 1)
	go func() {
		select {
		case p := <-srv.Accepted():
			serverMuxCh <- mux.New(ctx, p.Session, true)
		case <-ctx.Done():
		}
	}()
	c, err := dns.Dial(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	clientMux := mux.New(ctx, c, false)
	defer clientMux.Close()
	var serverMux *mux.Mux
	select {
	case serverMux = <-serverMuxCh:
	case <-ctx.Done():
		t.Fatal("server mux not ready")
	}
	defer serverMux.Close()
	go ServeVPN(ctx, serverMux, func(netip.Addr) (*mux.Mux, bool) { return nil, false }, false)
	stream, err := clientMux.Open(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	want := []byte("vpn socket egress")
	if _, err := stream.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestVPNInternalRouteOverDirectDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{92}, 32)
	srv, err := dns.Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	dial := func() (*dns.Client, *mux.Mux) {
		_, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		c, e := dns.Dial(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, key)
		if e != nil {
			t.Fatal(e)
		}
		return c, mux.New(ctx, c, false)
	}
	agentClient, agentClientMux := dial()
	defer agentClient.Close()
	defer agentClientMux.Close()
	var agentServerMux *mux.Mux
	select {
	case peer := <-srv.Accepted():
		agentServerMux = mux.New(ctx, peer.Session, true)
	case <-ctx.Done():
		t.Fatal("agent not accepted")
	}
	defer agentServerMux.Close()
	go ServeAgent(ctx, agentClientMux)
	vpnClient, vpnClientMux := dial()
	defer vpnClient.Close()
	defer vpnClientMux.Close()
	var vpnServerMux *mux.Mux
	select {
	case peer := <-srv.Accepted():
		vpnServerMux = mux.New(ctx, peer.Session, true)
	case <-ctx.Done():
		t.Fatal("VPN not accepted")
	}
	defer vpnServerMux.Close()
	target, _ := netip.ParseAddr("127.0.0.1")
	go ServeVPN(ctx, vpnServerMux, func(ip netip.Addr) (*mux.Mux, bool) {
		if ip == target {
			return agentServerMux, true
		}
		return nil, false
	}, true)
	stream, err := vpnClientMux.Open(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	want := []byte("via agent")
	if _, err := stream.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}
