package control

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

func fileTestMuxPair(ctx context.Context) (*mux.Mux, *mux.Mux) {
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	return mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true),
		mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
}

func TestVPNClientRelaysFileToAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	serverAgent, agent := fileTestMuxPair(ctx)
	defer serverAgent.Close()
	defer agent.Close()
	go pivot.ServeAgentWithExec(ctx, agent, true)
	var keys security.Keys
	transport, err := session.New(704, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: transport, AgentID: "agent-a", Connected: time.Now()}, serverAgent)
	serverVPN, clientVPN := fileTestMuxPair(ctx)
	defer serverVPN.Close()
	defer clientVPN.Close()
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeFileRelay(ctx, stream)
	})
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	remote := filepath.Join(dir, "remote")
	download := filepath.Join(dir, "download")
	// This crosses thousands of stream frames and exercises relay flow control.
	content := bytes.Repeat([]byte("transfer-through-server\x00"), 1<<18)
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pivot.TransferFile(ctx, clientVPN, "agent-a", "upload", source, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := pivot.TransferFile(ctx, clientVPN, "agent-a", "upload", source, remote); err == nil || !strings.Contains(err.Error(), "upload destination already exists") {
		t.Fatalf("duplicate upload response: %v", err)
	}
	if _, err := pivot.TransferFile(ctx, clientVPN, "agent-a", "download", download, remote); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(download)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("relayed file content mismatch: %v", err)
	}
	if _, err := pivot.TransferFile(ctx, clientVPN, "offline", "download", filepath.Join(dir, "missing"), remote); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected agent offline error: %v", err)
	}
}
