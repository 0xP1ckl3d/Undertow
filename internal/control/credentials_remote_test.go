package control

import (
	"bytes"
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

func TestCredentialRemoteAccessAndAudit(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.BootstrapOperator("leader", "Leader", "leader test password"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperator("alice", "Alice", "alice test password", OperatorRole); err != nil {
		t.Fatal(err)
	}
	leader, err := store.AuthenticateOperator("leader", "leader test password")
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	m.operations = store
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	in, out := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: in, out: out, done: make(chan struct{})}, true)
	client := mux.New(ctx, &remoteTestTransport{in: out, out: in, done: make(chan struct{})}, false)
	defer server.Close()
	defer client.Close()
	var keys security.Keys
	sess, err := session.New(929, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	m.RegisterAuthenticatedClient(&dns.Peer{Session: sess, AgentID: "client", Connected: time.Now()}, server, false, "", false, leader)
	go pivot.ServeVPNInteractive(ctx, server, m.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) { m.ServeRemote(ctx, "token", 929, stream) })
	const secret = "remote-api-secret-84a6"
	data, err := CallRemote(ctx, client, "POST", "/v1/credentials", CredentialInput{Label: "Private", Domain: "LAB", Username: "operator", Kind: "password", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	var record CredentialRecord
	if err := json.Unmarshal(data, &record); err != nil || record.ID == "" || bytes.Contains(data, []byte(secret)) {
		t.Fatalf("unsafe create response %s %v", data, err)
	}
	data, err = CallRemote(ctx, client, "GET", "/v1/credentials", nil)
	if err != nil || bytes.Contains(data, []byte(secret)) {
		t.Fatalf("unsafe list response %s %v", data, err)
	}
	audit, err := store.AuditHistory(5)
	if err != nil || len(audit) == 0 || audit[0].CredentialID != record.ID || audit[0].OperatorID != "leader" {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
	encoded, _ := json.Marshal(audit)
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatal("secret in audit")
	}
	alice, err := store.AuthenticateOperator("alice", "alice test password")
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.clients[929].operator = alice
	m.mu.Unlock()
	data, err = CallRemote(ctx, client, "GET", "/v1/credentials", nil)
	if err != nil {
		t.Fatal(err)
	}
	var visible []CredentialRecord
	if json.Unmarshal(data, &visible) != nil || len(visible) != 0 {
		t.Fatalf("private credential visible to another operator: %s", data)
	}
	if _, err := CallRemote(ctx, client, "DELETE", "/v1/credentials/"+record.ID, nil); err == nil {
		t.Fatal("another operator removed private credential")
	}
}
