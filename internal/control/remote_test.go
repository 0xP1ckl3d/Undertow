package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

type remoteTestTransport struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func (t *remoteTestTransport) Send(ctx context.Context, b []byte) error {
	select {
	case t.out <- append([]byte(nil), b...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return io.EOF
	}
}
func (t *remoteTestTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.in:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}
func (t *remoteTestTransport) Close() error { t.once.Do(func() { close(t.done) }); return nil }

func TestConnectedVPNClientHasLimitedAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	client := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer client.Close()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	go pivot.ServeVPNInteractive(ctx, server, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeRemote(ctx, "operator-secret", 704, stream)
	})
	data, err := CallRemote(ctx, client, "GET", "/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Agents []AgentInfo `json:"agents"`
	}
	if err := json.Unmarshal(data, &status); err != nil || status.Agents == nil {
		t.Fatalf("invalid status: %s: %v", data, err)
	}
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/routes", map[string]string{"prefix": "10.20.0.0/16", "agent_id": "agent-a"}},
		{"POST", "/v1/selection", map[string]string{"agent_id": "agent-a"}},
		{"POST", "/v1/clients/999/internal", map[string]bool{"enabled": true}},
		{"POST", "/v1/clients/999/routes", AcceptedRoute{Prefix: "10.10.0.0/16", AgentID: "agent-a", Manual: true}},
		{"POST", "/v1/clients/999/forwards", map[string]string{"agent_id": "agent-a", "bind": "0.0.0.0:8080", "target": "127.0.0.1:8080"}},
		{"DELETE", "/v1/clients/999/forwards?agent_id=agent-a&bind=0.0.0.0:8080", nil},
		{"DELETE", "/v1/routes?prefix=10.20.0.0%2F16", nil},
	} {
		if _, err := CallRemote(ctx, client, request.method, request.path, request.body); err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("VPN client was not denied %s %s: %v", request.method, request.path, err)
		}
	}
	if len(manager.routes.List()) != 0 {
		t.Fatal("VPN client changed server routes")
	}
}

func TestServerGrantControlsClientDistributionMutations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	client := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer client.Close()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	var calls atomic.Int32
	manager.SetAgentDistributionHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"office"}`))
	}))
	var keys security.Keys
	sess, err := session.New(704, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.RegisterClient(&dns.Peer{Session: sess, AgentID: "client", Connected: time.Now()}, server, false, "")
	go pivot.ServeVPNInteractive(ctx, server, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeRemote(ctx, "operator-secret", 704, stream)
	})
	if _, err := CallRemote(ctx, client, http.MethodPost, "/v1/agent-profiles", map[string]string{"name": "office"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("mutation without grant: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("distribution handler was reached without a grant")
	}
	if err := manager.SetClientDistributionAdmin(704, true); err != nil {
		t.Fatal(err)
	}
	if _, err := CallRemote(ctx, client, http.MethodPost, "/v1/agent-profiles", map[string]string{"name": "office"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("granted mutation did not reach handler")
	}
	manager.UnregisterClient(704, server)
	if _, err := CallRemote(ctx, client, http.MethodPost, "/v1/agent-profiles", map[string]string{"name": "office"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("grant survived disconnect: %v", err)
	}
}

func TestRemoteResponseWaitsForRequestFin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	client := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer client.Close()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	go pivot.ServeVPNInteractive(ctx, server, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeRemote(ctx, "operator-secret", 704, stream)
	})
	stream, err := client.Open(ctx, pivot.ControlDestination)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := json.NewEncoder(stream).Encode(remoteRequest{Method: http.MethodGet, Path: "/v1/status"}); err != nil {
		t.Fatal(err)
	}
	// The JSON request is complete before its FIN arrives. The server must
	// keep the stream open long enough to return the response body.
	time.Sleep(50 * time.Millisecond)
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	responseBytes, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	var response remoteResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil || response.Status != http.StatusOK || len(response.Body) == 0 {
		t.Fatalf("remote response lost: status=%d bytes=%d err=%v", response.Status, len(response.Body), err)
	}
}

func TestVPNClientJobPathsAreScopedToJobAPI(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/v1/agents/agent-a/jobs", true},
		{"GET", "/v1/jobs", true},
		{"GET", "/v1/jobs/abc/output", true},
		{"POST", "/v1/jobs/abc/cancel", true},
		{"POST", "/v1/jobs/abc", false},
		{"DELETE", "/v1/jobs/abc", false},
		{"GET", "/v1/routes", false},
		{"POST", "/v1/agents/agent-a/shutdown", true},
		{"GET", "/v1/agents/agent-a/events", true},
		{"POST", "/v1/sessions/agent-a/kill", true},
	} {
		request, err := http.NewRequest(tc.method, "http://localhost"+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := clientRequestAllowed(request, 705); got != tc.allowed {
			t.Fatalf("%s %s: allowed=%t", tc.method, tc.path, got)
		}
	}
}

func TestVPNClientDistributionIsReadOnlyByDefault(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/agent-profiles"},
		{http.MethodPost, "/v1/agent-profiles"},
		{http.MethodGet, "/v1/agent-profiles/office"},
		{http.MethodPut, "/v1/agent-profiles/office"},
		{http.MethodDelete, "/v1/agent-profiles/office"},
		{http.MethodGet, "/v1/agent-artifacts"},
		{http.MethodPost, "/v1/agent-artifacts"},
		{http.MethodPost, "/v1/agent-artifacts/abc/host"},
		{http.MethodGet, "/v1/agent-artifacts/abc/host"},
		{http.MethodDelete, "/v1/agent-artifacts/abc/host"},
	} {
		req, err := http.NewRequest(tc.method, "http://localhost"+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		allowed := tc.method == http.MethodGet && !strings.HasSuffix(tc.path, "/host")
		if clientRequestAllowed(req, 705) != allowed {
			t.Fatalf("unexpected distribution access: %s %s", tc.method, tc.path)
		}
	}
}

func TestRemoteExecHelperProcess(t *testing.T) {
	if os.Getenv("UNDERTOW_REMOTE_EXEC_HELPER") != "1" {
		return
	}
	_, _ = io.WriteString(os.Stdout, "remote agent command completed")
	os.Exit(0)
}

func TestVPNClientExecutesOnDefaultAgent(t *testing.T) {
	t.Setenv("UNDERTOW_REMOTE_EXEC_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverAgent := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverAgent.Close()
	defer agent.Close()
	go pivot.ServeAgent(ctx, agent)
	var keys security.Keys
	transport, err := session.New(704, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: transport, AgentID: "agent-a", Connected: time.Now()}, serverAgent)
	manager.UpdateInventory("agent-a", serverAgent, []byte(`{"advertised_routes":["192.168.0.0/22"]}`))
	c, d := make(chan []byte, 256), make(chan []byte, 256)
	serverVPN := mux.New(ctx, &remoteTestTransport{in: c, out: d, done: make(chan struct{})}, true)
	clientVPN := mux.New(ctx, &remoteTestTransport{in: d, out: c, done: make(chan struct{})}, false)
	defer serverVPN.Close()
	defer clientVPN.Close()
	clientSession, err := session.New(705, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.RegisterClient(&dns.Peer{Session: clientSession, AgentID: "client-a", Connected: time.Now()}, serverVPN, false, "")
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeRemote(ctx, "operator-secret", 705, stream)
	})
	for _, route := range []AcceptedRoute{{Prefix: "192.168.0.0/22", AgentID: "agent-a"}, {Prefix: "10.10.0.0/16", AgentID: "agent-a", Manual: true}} {
		if _, err := CallRemote(ctx, clientVPN, "POST", "/v1/clients/705/routes", route); err != nil {
			t.Fatal(err)
		}
	}
	if got, configured := manager.ResolveClientEgress(705, netip.MustParseAddr("10.10.1.7")); !configured || got != serverAgent {
		t.Fatal("client manual route did not reach agent")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := CallRemote(ctx, clientVPN, "POST", "/v1/agents/agent-a/exec", pivot.ExecRequest{Argv: []string{exe, "-test.run=TestRemoteExecHelperProcess"}})
	if err != nil {
		t.Fatal(err)
	}
	var result pivot.ExecResult
	if err := json.Unmarshal(data, &result); err != nil || result.Error != "" || result.ExitCode != 0 || result.Stdout != "remote agent command completed" {
		t.Fatalf("unexpected remote result: %+v: %v", result, err)
	}
}
