package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/netip"
	"strings"
	"testing"

	"undertow/internal/control"
	"undertow/internal/routing"
	"undertow/internal/transport"
)

func TestServerTransportLifecycle(t *testing.T) {
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := control.NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	servers := newServerTransports(ctx, manager, identity, token, "t.undertow.invalid", "/undertow", func(transport.Peer) {})
	defer servers.Close()
	for _, kind := range []string{"dns", "websocket", "quic"} {
		info, err := servers.Start(kind, control.TransportStartRequest{Listen: "127.0.0.1:0"})
		if err != nil {
			t.Fatalf("start %s: %v", kind, err)
		}
		if info.Listen == "" || info.Transport != kind {
			t.Fatalf("invalid %s listener: %+v", kind, info)
		}
		if kind != "dns" && info.TLSMode != "self-signed" {
			t.Fatalf("%s should default to self-signed TLS: %+v", kind, info)
		}
	}
	if list := servers.List(); len(list) != 3 {
		t.Fatalf("expected three listeners: %+v", list)
	}
	if _, err := servers.Start("QuIc", control.TransportStartRequest{Listen: "127.0.0.1:0"}); err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatalf("duplicate start: %v", err)
	}
	if err := servers.Stop("wEbSoCkEt", false); err != nil {
		t.Fatal(err)
	}
	if list := servers.List(); len(list) != 2 {
		t.Fatalf("stop disturbed other listeners: %+v", list)
	}
	if _, err := servers.Start("websocket", control.TransportStartRequest{Listen: "127.0.0.1:0"}); err != nil {
		t.Fatalf("restart WebSocket: %v", err)
	}
}
