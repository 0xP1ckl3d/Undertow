package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestStreamDeploymentArtifactUsesAgentChannelAndRecordsTransfer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := manager.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	server, agent := fileTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go pivot.ServeAgentWithExec(ctx, agent, true)
	dir := t.TempDir()
	source, remote := filepath.Join(dir, "artifact.exe"), filepath.Join(dir, "target.exe")
	content := bytes.Repeat([]byte("undertow-deployment-artifact\x00"), 8192)
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, actionContextKey{}, actionContext{ClientID: "client-a", ClientSessionID: 91, ActionClaims: ActionClaims{OperatorID: "alice", DisplayName: "Alice", Source: "gui"}})
	result, record, err := manager.StreamDeploymentArtifact(ctx, "agent-a", server, source, remote)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(content)
	wantSHA := hex.EncodeToString(wantHash[:])
	actual, err := os.ReadFile(remote)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("streamed artifact mismatch: %v", err)
	}
	if result.Size != int64(len(content)) || result.SHA256 != wantSHA {
		t.Fatalf("result: %+v", result)
	}
	if record.State != "completed" || record.SHA256 != wantSHA || record.Bytes != int64(len(content)) || record.AgentID != "agent-a" || record.OperatorID != "alice" || record.ClientSessionID != 91 {
		t.Fatalf("record: %+v", record)
	}
	history, err := store.TransferHistory(10)
	if err != nil || len(history) != 1 || history[0].ID != record.ID || history[0].State != "completed" {
		t.Fatalf("history: %+v, %v", history, err)
	}
}

func TestTransferRecordBindsUpdatesToClientSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "ops.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverMux := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agentMux := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverMux.Close()
	defer agentMux.Close()
	var keys security.Keys
	sess, err := session.New(44, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	m.Register(&dns.Peer{Session: sess, AgentID: "agent-a", Connected: time.Now()}, serverMux)
	call := func(client uint64, method, endpoint, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, endpoint, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer test-token")
		request = request.WithContext(context.WithValue(request.Context(), actionContextKey{}, actionContext{ClientID: "client-a", ClientSessionID: client, ActionClaims: ActionClaims{OperatorID: "alice", Source: "gui"}}))
		response := httptest.NewRecorder()
		m.handler("test-token").ServeHTTP(response, request)
		return response
	}
	created := call(55, http.MethodPost, "/v1/transfers", `{"agent_id":"agent-a","operation":"download","remote_path":"C:\\Temp\\report.txt"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var record TransferRecord
	if err := json.Unmarshal(created.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.ClientSessionID != 55 || record.ClientID != "client-a" || record.OperatorID != "alice" || record.State != "running" {
		t.Fatalf("record: %+v", record)
	}
	wrong := call(56, http.MethodPut, "/v1/transfers/"+record.ID, `{"state":"completed","bytes":5,"total":5,"sha256":"`+strings.Repeat("a", 64)+`"}`)
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("wrong session: %d", wrong.Code)
	}
	finished := call(55, http.MethodPut, "/v1/transfers/"+record.ID, `{"state":"completed","bytes":5,"total":5,"sha256":"`+strings.Repeat("a", 64)+`"}`)
	if finished.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", finished.Code, finished.Body.String())
	}
	records, err := store.TransferHistory(10)
	if err != nil || len(records) != 1 || records[0].State != "completed" || records[0].Bytes != 5 {
		t.Fatalf("history: %+v %v", records, err)
	}
}

func TestRunningTransferIsInterruptedAfterRestart(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record := TransferRecord{ID: "transfer-a", ClientSessionID: 55, AgentID: "agent-a", Operation: "upload", RemotePath: "target", State: "running", Started: time.Now().UTC()}
	if err := store.saveTransfer(record); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverTransfers(); err != nil {
		t.Fatal(err)
	}
	got, err := store.transfer(record.ID)
	if err != nil || got.State != "interrupted" || got.Ended.IsZero() {
		t.Fatalf("recovered: %+v %v", got, err)
	}
}
