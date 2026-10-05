package control

import (
	"context"
	"encoding/json"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

func TestOperatorBootstrapPersistenceAndFinalLeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateOperator("leader", "wrong"); err == nil {
		t.Fatal("empty store authenticated")
	}
	if err := store.BootstrapOperator("leader", "First Leader", "a unique strong password"); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapOperator("other", "Other", "another strong password"); err == nil {
		t.Fatal("second bootstrap succeeded")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account, err := store.AuthenticateOperator("leader", "a unique strong password")
	if err != nil || account.Role != TeamLeaderRole {
		t.Fatalf("persisted login: %+v %v", account, err)
	}
	role := OperatorRole
	disabled := true
	if err := store.UpdateOperator("leader", &role, nil, nil); err == nil {
		t.Fatal("final leader demoted")
	}
	if err := store.UpdateOperator("leader", nil, &disabled, nil); err == nil {
		t.Fatal("final leader disabled")
	}
	if err := store.CreateOperator("second", "Second Leader", "another strong password", TeamLeaderRole); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOperator("leader", &role, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOperator("second", nil, &disabled, nil); err == nil {
		t.Fatal("new final leader disabled")
	}
	password := "a rotated strong password"
	if err := store.UpdateOperator("leader", nil, nil, &password); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateOperator("leader", "a unique strong password"); err == nil {
		t.Fatal("old password accepted")
	}
	if _, err := store.AuthenticateOperator("leader", password); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOperator("second"); err == nil {
		t.Fatal("final leader revoked")
	}
	if err := store.DeleteOperator("leader"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateOperator("leader", password); err == nil {
		t.Fatal("revoked account authenticated")
	}
	enabled := false
	if err := store.UpdateOperator("leader", nil, &enabled, nil); err == nil {
		t.Fatal("revoked account reenabled")
	}
}

func TestRemoteAuditUsesAuthenticatedOperator(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.BootstrapOperator("leader", "Trusted Leader", "a unique strong password"); err != nil {
		t.Fatal(err)
	}
	a, err := store.AuthenticateOperator("leader", "a unique strong password")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	manager.operations = store
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, out := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: in, out: out, done: make(chan struct{})}, true)
	client := mux.New(ctx, &remoteTestTransport{in: out, out: in, done: make(chan struct{})}, false)
	defer server.Close()
	defer client.Close()
	var keys security.Keys
	sess, err := session.New(973, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.RegisterAuthenticatedClient(&dns.Peer{Session: sess, AgentID: "client", Connected: time.Now()}, server, false, "", false, a)
	go pivot.ServeVPNInteractive(ctx, server, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) { manager.ServeRemote(ctx, "token", 973, stream) })
	forged := WithActionClaims(ctx, ActionClaims{OperatorID: "forged", DisplayName: "Forged Name", Source: "gui"})
	if _, err := CallRemote(forged, client, "PUT", "/v1/server-public-host", map[string]string{"host": "example.com"}); err != nil {
		t.Fatal(err)
	}
	data, err := CallRemote(ctx, client, "GET", "/v1/operator/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	var me OperatorAccount
	if err := json.Unmarshal(data, &me); err != nil || me.ID != "leader" {
		t.Fatalf("me=%+v %v", me, err)
	}
	records, err := store.AuditHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 || records[0].OperatorID != "leader" || records[0].DisplayName != "Trusted Leader" || records[0].IdentityTrust != "server_authenticated_operator" {
		t.Fatalf("audit=%+v", records)
	}
	if _, err := CallRemote(ctx, client, "POST", "/v1/operators", map[string]string{"id": "alice", "display_name": "Alice", "role": OperatorRole, "password": "another strong password"}); err != nil {
		t.Fatal(err)
	}
	alice, err := store.AuthenticateOperator("alice", "another strong password")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.clients[973].operator = alice
	manager.mu.Unlock()
	if _, err := CallRemote(ctx, client, "GET", "/v1/operators", nil); err == nil {
		t.Fatal("Operator listed accounts")
	}
}
