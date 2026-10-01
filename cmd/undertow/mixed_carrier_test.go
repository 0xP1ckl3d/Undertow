package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/transport/dns"
	"undertow/internal/transport/quic"
	"undertow/internal/transport/websocket"
)

func TestMixedCarrierRouting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	identity := testKey(t)
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	fingerprint := security.Fingerprint(identity)
	manager := control.NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	servers := newServerTransports(ctx, manager, identity, token, "t.undertow.invalid", "/undertow", func(peer transport.Peer) { handleServerPeer(ctx, manager, "", false, peer) })
	defer servers.Close()
	addresses := make(map[string]string)
	for _, kind := range []string{"dns", "websocket", "quic"} {
		info, err := servers.Start(kind, control.TransportStartRequest{Listen: "127.0.0.1:0"})
		if err != nil {
			t.Fatal(err)
		}
		addresses[kind] = info.Listen
	}
	agentIDs := make(map[string]string)
	for _, kind := range []string{"dns", "websocket", "quic"} {
		key := testKey(t)
		var connection transport.Connection
		var err error
		switch kind {
		case "dns":
			connection, err = dns.Dial(ctx, addresses[kind], "t.undertow.invalid", fingerprint, token, key)
		case "websocket":
			connection, err = websocket.Dial(ctx, websocket.DialOptions{Address: addresses[kind], Path: "/undertow", TLSInsecureSkipVerify: true}, fingerprint, token, key)
		case "quic":
			connection, err = quic.Dial(ctx, quic.DialOptions{Address: addresses[kind], TLSInsecureSkipVerify: true}, fingerprint, token, key)
		}
		if err != nil {
			t.Fatalf("dial %s agent: %v", kind, err)
		}
		agentMux := mux.New(ctx, connection, false)
		defer agentMux.Close()
		if err := control.SendInventory(ctx, agentMux, nil, pivot.DefaultCapabilities()); err != nil {
			t.Fatal(err)
		}
		go pivot.ServeAgentWithCapabilities(ctx, agentMux, pivot.DefaultCapabilities())
		id := security.Fingerprint(key)[:32]
		agent := waitForAgent(t, manager, id)
		if agent.Transport != kind {
			t.Fatalf("agent carrier = %s, want %s", agent.Transport, kind)
		}
		agentIDs[kind] = id
	}
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	for _, scenario := range []struct{ client, agent string }{{"quic", "dns"}, {"quic", "websocket"}, {"websocket", "quic"}} {
		key := testKey(t)
		var connection transport.Connection
		if scenario.client == "quic" {
			connection, err = quic.Dial(ctx, quic.DialOptions{Address: addresses["quic"], TLSInsecureSkipVerify: true}, fingerprint, token, key)
		} else {
			connection, err = websocket.Dial(ctx, websocket.DialOptions{Address: addresses["websocket"], Path: "/undertow", TLSInsecureSkipVerify: true}, fingerprint, token, key)
		}
		if err != nil {
			t.Fatal(err)
		}
		clientMux := mux.New(ctx, connection, false)
		hello, _ := json.Marshal(map[string]any{"mode": "vpn", "internal": true, "hostname": "mixed-test"})
		if err := clientMux.SendControl(ctx, hello); err != nil {
			t.Fatal(err)
		}
		if _, err := clientMux.RecvControl(ctx); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for len(manager.ClientList()) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if err := manager.SetClientRoute(connection.ID(), netip.MustParsePrefix("127.0.0.0/8"), agentIDs[scenario.agent], true); err != nil {
			t.Fatal(err)
		}
		stream, err := clientMux.Open(ctx, echo.Addr().String())
		if err != nil {
			t.Fatalf("%s client via %s agent: %v", scenario.client, scenario.agent, err)
		}
		if _, err := stream.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		var response [4]byte
		if _, err := io.ReadFull(stream, response[:]); err != nil {
			t.Fatal(err)
		}
		if string(response[:]) != "ping" {
			t.Fatalf("corrupt cross-carrier echo: %q", response[:])
		}
		_ = stream.Close()
		clientMux.Close()
	}
	if err := servers.Stop("dns", false); err == nil {
		t.Fatal("active DNS agent was stopped without force")
	}
	if err := servers.Stop("dns", true); err != nil {
		t.Fatal(err)
	}
	if manager.Get(agentIDs["websocket"]) == nil || manager.Get(agentIDs["quic"]) == nil {
		t.Fatal("stopping DNS disturbed other carriers")
	}
	if _, err := servers.Start("dns", control.TransportStartRequest{Listen: "127.0.0.1:0"}); err != nil {
		t.Fatalf("restart DNS: %v", err)
	}
}
