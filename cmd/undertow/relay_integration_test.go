package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/netip"
	"strings"
	"testing"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/transport/relay"
	"undertow/internal/transport/websocket"
)

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func waitForAgent(t *testing.T, manager *control.Manager, id string) control.AgentInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, agent := range manager.AgentList() {
			if agent.ID == id && agent.Capabilities != nil {
				return agent
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent %s did not register", id)
	return control.AgentInfo{}
}

func TestNestedRelayAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	manager.SetRelayAcceptor(func(_ context.Context, parentID string, upstream *mux.Stream) {
		peer, err := relay.Accept(ctx, upstream, parentID, identity, token)
		if err != nil {
			_ = upstream.Close()
			return
		}
		handleServerPeer(ctx, manager, "", false, peer)
	})
	info, err := servers.Start("websocket", control.TransportStartRequest{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	parentKey := testKey(t)
	parent, err := websocket.Dial(ctx, websocket.DialOptions{Address: info.Listen, Path: "/undertow", TLSInsecureSkipVerify: true}, fingerprint, token, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	parentMux := mux.New(ctx, parent, false)
	defer parentMux.Close()
	if err := sendIsolatedTestInventory(ctx, parentMux); err != nil {
		t.Fatal(err)
	}
	go pivot.ServeAgentWithCapabilities(ctx, parentMux, pivot.DefaultCapabilities())
	parentID := security.Fingerprint(parentKey)[:32]
	waitForAgent(t, manager, parentID)
	first, err := manager.StartRelay(ctx, parentID, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.RelayList(parentID)) != 1 {
		t.Fatal("relay listener was not recorded")
	}
	childKey := testKey(t)
	child, err := relay.Dial(ctx, first.Bind, fingerprint, token, childKey)
	if err != nil {
		t.Fatal(err)
	}
	childMux := mux.New(ctx, child, false)
	defer childMux.Close()
	if err := sendIsolatedTestInventory(ctx, childMux); err != nil {
		t.Fatal(err)
	}
	go pivot.ServeAgentWithCapabilities(ctx, childMux, pivot.DefaultCapabilities())
	childID := security.Fingerprint(childKey)[:32]
	childInfo := waitForAgent(t, manager, childID)
	if childInfo.Via != parentID || childInfo.Depth != 1 || childInfo.Transport != "relay" {
		t.Fatalf("incorrect child topology: %+v", childInfo)
	}
	result, err := pivot.ExecuteRequest(ctx, manager.Get(childID), pivot.ExecRequest{Builtin: "whoami"})
	if err != nil || result.Error != "" || strings.TrimSpace(result.Stdout) == "" {
		t.Fatalf("child host operation: %+v, %v", result, err)
	}
	second, err := manager.StartRelay(ctx, childID, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grandchildKey := testKey(t)
	grandchild, err := relay.Dial(ctx, second.Bind, fingerprint, token, grandchildKey)
	if err != nil {
		t.Fatal(err)
	}
	grandchildMux := mux.New(ctx, grandchild, false)
	defer grandchildMux.Close()
	if err := sendIsolatedTestInventory(ctx, grandchildMux); err != nil {
		t.Fatal(err)
	}
	go pivot.ServeAgentWithCapabilities(ctx, grandchildMux, pivot.DefaultCapabilities())
	grandchildID := security.Fingerprint(grandchildKey)[:32]
	grandchildInfo := waitForAgent(t, manager, grandchildID)
	if grandchildInfo.Via != childID || grandchildInfo.Depth != 2 {
		t.Fatalf("incorrect grandchild topology: %+v", grandchildInfo)
	}
	parentMux.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if manager.Get(parentID) == nil && manager.Get(childID) == nil && manager.Get(grandchildID) == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("parent disconnect left descendants registered")
}
