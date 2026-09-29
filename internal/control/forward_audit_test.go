package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

func forwardAuditPair(ctx context.Context) (*mux.Mux, *mux.Mux) {
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	return mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true), mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
}

func forwardAuditAgent(t *testing.T, ctx context.Context, m *Manager, id string, number uint64, caps pivot.Capabilities) (*mux.Mux, *mux.Mux) {
	t.Helper()
	server, agent := forwardAuditPair(ctx)
	go pivot.ServeAgentWithCapabilities(ctx, agent, caps)
	var keys security.Keys
	s, err := session.New(number, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	m.Register(&dns.Peer{Session: s, AgentID: id, Connected: time.Now()}, server)
	data, _ := json.Marshal(map[string]any{"capabilities": caps.Report()})
	m.UpdateInventory(id, server, data)
	return server, agent
}

func forwardAuditClient(t *testing.T, ctx context.Context, m *Manager, id uint64) (*mux.Mux, *mux.Mux) {
	t.Helper()
	server, client := forwardAuditPair(ctx)
	var keys security.Keys
	s, err := session.New(id, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	m.RegisterClient(&dns.Peer{Session: s, AgentID: fmt.Sprintf("client-%d", id), Connected: time.Now()}, server, false, "")
	go pivot.ServeVPNInteractive(ctx, server, m.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) { m.ServeRemote(ctx, "operator-secret", id, stream) })
	go pivot.ServeClientForwards(ctx, client)
	return server, client
}

func forwardAuditService(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	return listener
}

func forwardAuditEcho(t *testing.T, address string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(conn, buffer); err != nil || string(buffer) != "ping" {
		t.Fatalf("echo %s: %q %v", address, buffer, err)
	}
}

func forwardAuditClosed(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s remained open", address)
}

func TestMultipleForwardsAgentsAndDisconnectCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	serviceA, serviceB := forwardAuditService(t), forwardAuditService(t)
	defer serviceA.Close()
	defer serviceB.Close()
	serverA, agentA := forwardAuditAgent(t, ctx, m, "agent-a", 901, pivot.DefaultCapabilities())
	serverB, agentB := forwardAuditAgent(t, ctx, m, "agent-b", 902, pivot.DefaultCapabilities())
	defer serverA.Close()
	defer agentA.Close()
	defer serverB.Close()
	defer agentB.Close()
	serverOne, clientOne := forwardAuditClient(t, ctx, m, 911)
	serverTwo, clientTwo := forwardAuditClient(t, ctx, m, 912)
	defer serverOne.Close()
	defer clientOne.Close()
	defer serverTwo.Close()
	defer clientTwo.Close()
	first, err := m.AddClientForward(ctx, 911, "agent-a", "127.0.0.1:0", serviceA.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.AddClientForward(ctx, 911, "agent-a", "127.0.0.1:0", serviceB.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	third, err := m.AddClientForward(ctx, 912, "agent-b", "127.0.0.1:0", serviceB.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ClientForwards(911)) != 2 || len(m.ClientForwards(912)) != 1 || len(m.AgentForwards("agent-a")) != 2 {
		t.Fatal("multiple forwards absent from inventory")
	}
	var wg sync.WaitGroup
	for _, address := range []string{first.Bind, second.Bind, third.Bind} {
		wg.Add(1)
		go func(address string) { defer wg.Done(); forwardAuditEcho(t, address) }(address)
	}
	wg.Wait()
	if _, err := CallRemote(ctx, clientOne, "GET", "/v1/status", nil); err != nil {
		t.Fatalf("forwarding blocked control traffic: %v", err)
	}
	m.UnregisterClient(911, serverOne)
	clientOne.Close()
	serverOne.Close()
	forwardAuditClosed(t, first.Bind)
	forwardAuditClosed(t, second.Bind)
	forwardAuditEcho(t, third.Bind)
	m.Unregister("agent-b", serverB)
	serverB.Close()
	agentB.Close()
	forwardAuditClosed(t, third.Bind)
}

func TestForwardBindConflictsAndListenerDenial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	server, agent := forwardAuditAgent(t, ctx, m, "agent-a", 921, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	clientServer, client := forwardAuditClient(t, ctx, m, 922)
	defer clientServer.Close()
	defer client.Close()
	service := forwardAuditService(t)
	defer service.Close()
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	_, err = m.AddClientForward(ctx, 922, "agent-a", occupied.Addr().String(), service.Addr().String())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "127.0.0.1") {
		t.Fatalf("bind conflict lacks useful address: %v", err)
	}
	allowed, err := m.AddClientForward(ctx, 922, "agent-a", "127.0.0.1:0", service.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AddClientForward(ctx, 922, "agent-a", allowed.Bind, service.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "already forwarded") {
		t.Fatalf("duplicate forward conflict=%v", err)
	}
	deniedCaps := pivot.DefaultCapabilities()
	deniedCaps.Listeners = false
	deniedServer, deniedAgent := forwardAuditAgent(t, ctx, m, "agent-denied", 923, deniedCaps)
	defer deniedServer.Close()
	defer deniedAgent.Close()
	_, err = m.AddClientForward(ctx, 922, "agent-denied", "127.0.0.1:0", service.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "listeners capability is disabled") {
		t.Fatalf("listener denial=%v", err)
	}
	forwardAuditEcho(t, allowed.Bind)
}
