package control

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
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

func TestRelayInteractiveHelper(t *testing.T) {
	if os.Getenv("UNDERTOW_RELAY_INTERACTIVE_HELPER") != "1" {
		return
	}
	buffer := make([]byte, 5)
	_, _ = io.ReadFull(os.Stdin, buffer)
	_, _ = os.Stdout.Write([]byte("relay:"))
	_, _ = os.Stdout.Write(buffer)
	os.Exit(0)
}

func TestWASMRelayConnectAndRemoteJob(t *testing.T) {
	module, err := os.ReadFile(filepath.Join("..", "pivot", "testdata", "wasm_args.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	serverAgent, agent := forwardAuditAgent(t, ctx, manager, "wasm-agent", 724, pivot.DefaultCapabilities())
	defer serverAgent.Close()
	defer agent.Close()
	serverVPN, clientVPN := forwardAuditPair(ctx)
	defer serverVPN.Close()
	defer clientVPN.Close()
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeInteractiveRelay(ctx, stream)
	})
	readResult := func(session *pivot.InteractiveSession) {
		t.Helper()
		defer session.Close()
		var output strings.Builder
		for {
			kind, data, err := session.Read()
			if err != nil {
				t.Fatal(err)
			}
			if kind == pivot.InteractiveOutput {
				output.Write(data)
			}
			if kind == pivot.InteractiveError {
				t.Fatalf("WASM error: %s", data)
			}
			if kind == pivot.InteractiveExit {
				if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 || !strings.Contains(output.String(), "relay-wasm") {
					t.Fatalf("exit=%v output=%q", data, output.String())
				}
				return
			}
		}
	}
	live, err := OpenClientWASM(ctx, clientVPN, "wasm-agent", module, []string{"relay-wasm"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	readResult(live)
	httpServer := httptest.NewServer(manager.handler("operator-secret"))
	defer httpServer.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(httpServer.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "CONNECT /v1/agents/wasm-agent/wasm HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer operator-secret\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT response=%v error=%v", response, err)
	}
	local, err := pivot.StartMemorySession(ctx, conn.(*net.TCPConn), reader, pivot.MemoryRequest{Size: len(module), Args: []string{"relay-wasm"}}, module)
	if err != nil {
		t.Fatal(err)
	}
	readResult(local)
	remoteServer, remoteClient := forwardAuditClient(t, ctx, manager, 725)
	defer remoteServer.Close()
	defer remoteClient.Close()
	data, err := CallRemote(ctx, remoteClient, "POST", "/v1/agents/wasm-agent/wasm/jobs", map[string]any{"source": module, "args": []string{"relay-wasm"}})
	if err != nil {
		t.Fatal(err)
	}
	var job JobInfo
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, manager, 725, job.ID, func(j JobInfo) bool { return j.State == "completed" })
	if finished.Kind != "wasm" || !strings.Contains(finished.Output, "relay-wasm") {
		t.Fatalf("remote WASM job=%+v", finished)
	}
}

func TestScriptRelayConnectAndRemoteJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	serverAgent, agent := forwardAuditAgent(t, ctx, manager, "script-agent", 714, pivot.DefaultCapabilities())
	defer serverAgent.Close()
	defer agent.Close()
	language, source := "bash", []byte("echo relay-script\n")
	if runtime.GOOS == "windows" {
		language, source = "powershell", []byte("[Console]::Out.WriteLine('relay-script')\n")
	}
	serverVPN, clientVPN := forwardAuditPair(ctx)
	defer serverVPN.Close()
	defer clientVPN.Close()
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		manager.ServeInteractiveRelay(ctx, stream)
	})
	readResult := func(session *pivot.InteractiveSession) {
		t.Helper()
		defer session.Close()
		var output strings.Builder
		for {
			kind, data, err := session.Read()
			if err != nil {
				t.Fatalf("script frame read: %v, output so far: %q", err, output.String())
			}
			if kind == pivot.InteractiveOutput {
				output.Write(data)
			}
			if kind == pivot.InteractiveError {
				t.Fatalf("script error: %s", data)
			}
			if kind == pivot.InteractiveExit {
				if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 || !strings.Contains(output.String(), "relay-script") {
					t.Fatalf("exit=%v output=%q", data, output.String())
				}
				return
			}
		}
	}
	live, err := OpenClientScript(ctx, clientVPN, "script-agent", language, source)
	if err != nil {
		t.Fatal(err)
	}
	readResult(live)
	httpServer := httptest.NewServer(manager.handler("operator-secret"))
	defer httpServer.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(httpServer.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "CONNECT /v1/agents/script-agent/script HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer operator-secret\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT response=%v error=%v", response, err)
	}
	local, err := pivot.StartMemorySession(ctx, conn.(*net.TCPConn), reader, pivot.MemoryRequest{Language: language, Size: len(source)}, source)
	if err != nil {
		t.Fatal(err)
	}
	readResult(local)
	remoteServer, remoteClient := forwardAuditClient(t, ctx, manager, 715)
	defer remoteServer.Close()
	defer remoteClient.Close()
	data, err := CallRemote(ctx, remoteClient, "POST", "/v1/agents/script-agent/scripts/jobs", map[string]any{"language": language, "source": source})
	if err != nil {
		t.Fatal(err)
	}
	var job JobInfo
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, manager, 715, job.ID, func(j JobInfo) bool { return j.State == "completed" })
	if !strings.Contains(finished.Output, "relay-script") {
		t.Fatalf("remote script job=%+v", finished)
	}
}

func TestVPNClientInteractiveRelay(t *testing.T) {
	t.Setenv("UNDERTOW_RELAY_INTERACTIVE_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
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
	c, d := make(chan []byte, 256), make(chan []byte, 256)
	serverVPN := mux.New(ctx, &remoteTestTransport{in: c, out: d, done: make(chan struct{})}, true)
	clientVPN := mux.New(ctx, &remoteTestTransport{in: d, out: c, done: make(chan struct{})}, false)
	defer serverVPN.Close()
	defer clientVPN.Close()
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) {
		if stream.Destination() == pivot.InteractiveRelayDestination {
			manager.ServeInteractiveRelay(ctx, stream)
		}
	})
	if _, err := OpenClientInteractive(ctx, clientVPN, "missing-agent", pivot.InteractiveRequest{}); err == nil || !strings.Contains(err.Error(), "agent is not connected") {
		t.Fatalf("missing agent error=%v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	live, err := OpenClientInteractive(ctx, clientVPN, "agent-a", pivot.InteractiveRequest{Argv: []string{exe, "-test.run=TestRelayInteractiveHelper"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.Send([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for {
		kind, data, err := live.Read()
		if err != nil {
			t.Fatal(err)
		}
		if kind == pivot.InteractiveOutput {
			output.Write(data)
		}
		if kind == pivot.InteractiveExit {
			if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 {
				t.Fatalf("exit=%v", data)
			}
			break
		}
	}
	if !strings.Contains(output.String(), "relay:ping") {
		t.Fatalf("output=%q", output.String())
	}
	httpServer := httptest.NewServer(manager.handler("operator-secret"))
	defer httpServer.Close()
	address := strings.TrimPrefix(httpServer.URL, "http://")
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "CONNECT /v1/agents/agent-a/interactive HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer operator-secret\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("CONNECT status=%s", response.Status)
	}
	local, err := pivot.StartInteractive(ctx, conn.(*net.TCPConn), reader, pivot.InteractiveRequest{Argv: []string{exe, "-test.run=TestRelayInteractiveHelper"}})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if err := local.Send([]byte("again")); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	for {
		kind, data, err := local.Read()
		if err != nil {
			t.Fatal(err)
		}
		if kind == pivot.InteractiveOutput {
			output.Write(data)
		}
		if kind == pivot.InteractiveExit {
			break
		}
	}
	if !strings.Contains(output.String(), "relay:again") {
		t.Fatalf("local output=%q", output.String())
	}
}
