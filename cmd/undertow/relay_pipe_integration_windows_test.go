//go:build windows

package main

import (
	"context"
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

func TestWindowsNamedPipeRelayPreservesChildIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	identity := testKey(t)
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	manager := control.NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	servers := newServerTransports(ctx, manager, identity, token, "t.undertow.invalid", "/undertow", func(peer transport.Peer) { handleServerPeer(ctx, manager, "", false, peer) })
	defer servers.Close()
	manager.SetRelayAcceptor(func(_ context.Context, parentID, carrier, bind string, stream *mux.Stream) {
		peer, err := relay.Accept(ctx, stream, parentID, identity, token)
		if err != nil {
			_ = stream.Close()
			return
		}
		peer.Carrier = carrier
		peer.RelayBind = bind
		handleServerPeer(ctx, manager, "", false, peer)
	})
	listener, err := servers.Start("websocket", control.TransportStartRequest{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	parentKey := testKey(t)
	parent, err := websocket.Dial(ctx, websocket.DialOptions{Address: listener.Listen, Path: "/undertow", TLSInsecureSkipVerify: true}, security.Fingerprint(identity), token, parentKey)
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
	bind := `\\.\pipe\undertow-test-` + strings.ReplaceAll(t.Name(), "/", "-")
	if _, err := manager.StartRelay(ctx, parentID, bind); err != nil {
		t.Fatal(err)
	}
	childKey := testKey(t)
	child, err := relay.DialPipe(ctx, bind, security.Fingerprint(identity), token, childKey)
	if err != nil {
		t.Fatal(err)
	}
	childMux := mux.New(ctx, child, false)
	defer childMux.Close()
	if err := sendIsolatedTestInventory(ctx, childMux); err != nil {
		t.Fatal(err)
	}
	go pivot.ServeAgentWithCapabilities(ctx, childMux, pivot.DefaultCapabilities())
	info := waitForAgent(t, manager, security.Fingerprint(childKey)[:32])
	if info.Via != parentID || info.Transport != "relay-smb" || info.Depth != 1 || info.RelayBind != bind {
		t.Fatalf("named-pipe child: %+v", info)
	}
	result, err := pivot.ExecuteRequest(ctx, manager.Get(info.ID), pivot.ExecRequest{Builtin: "whoami"})
	if err != nil || result.Error != "" {
		t.Fatalf("child host operation: %+v %v", result, err)
	}
}
