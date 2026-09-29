package control

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

func TestVPNClientForwardsTCPThroughSelectedAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	service, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	go func() {
		conn, err := service.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err == nil {
			_, _ = conn.Write(buf)
		}
	}()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverAgent := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverAgent.Close()
	defer agent.Close()
	go pivot.ServeAgent(ctx, agent)
	var keys security.Keys
	agentSession, err := session.New(801, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: agentSession, AgentID: "agent-a", Connected: time.Now()}, serverAgent)
	c, d := make(chan []byte, 256), make(chan []byte, 256)
	serverVPN := mux.New(ctx, &remoteTestTransport{in: c, out: d, done: make(chan struct{})}, true)
	clientVPN := mux.New(ctx, &remoteTestTransport{in: d, out: c, done: make(chan struct{})}, false)
	defer serverVPN.Close()
	defer clientVPN.Close()
	clientSession, err := session.New(802, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.RegisterClient(&dns.Peer{Session: clientSession, AgentID: "client-a", Connected: time.Now()}, serverVPN, false, "")
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeRemote(ctx, "operator-secret", 802, stream)
	})
	go pivot.ServeClientForwards(ctx, clientVPN)
	data, err := CallRemote(ctx, clientVPN, "POST", "/v1/clients/802/forwards", map[string]string{"agent_id": "agent-a", "bind": "127.0.0.1:0", "target": service.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	var forward ForwardInfo
	if err := json.Unmarshal(data, &forward); err != nil || forward.Bind == "" {
		t.Fatalf("invalid forward: %s: %v", data, err)
	}
	if listed := manager.AgentList(); len(listed) != 1 || listed[0].ActiveForwards != 1 || len(manager.AgentForwards("agent-a")) != 1 {
		t.Fatalf("active forward absent from agent status: %+v", listed)
	}
	conn, err := net.DialTimeout("tcp4", forward.Bind, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "ping" {
		t.Fatalf("forwarded reply %q: %v", got, err)
	}
	if _, err := CallRemote(ctx, clientVPN, "DELETE", "/v1/clients/802/forwards?agent_id=agent-a&bind="+forward.Bind, nil); err != nil {
		t.Fatal(err)
	}
	if len(manager.ClientForwards(802)) != 0 {
		t.Fatal("forward remained in client state after delete")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		probe, err := net.DialTimeout("tcp4", forward.Bind, 100*time.Millisecond)
		if err != nil {
			break
		}
		_ = probe.Close()
		if time.Now().After(deadline) {
			t.Fatal("agent TCP listener remained open after delete")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
