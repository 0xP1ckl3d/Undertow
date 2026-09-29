package control

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
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
