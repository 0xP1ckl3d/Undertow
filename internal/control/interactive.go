package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type interactiveRelayRequest struct {
	AgentID string `json:"agent_id"`
	Kind    string `json:"kind,omitempty"`
}

// OpenClientInteractive relays one client console session through the server.
func OpenClientInteractive(ctx context.Context, client *mux.Mux, agentID string, request pivot.InteractiveRequest) (*pivot.InteractiveSession, error) {
	if client == nil {
		return nil, errors.New("VPN session is not connected")
	}
	stream, err := client.Open(ctx, pivot.InteractiveRelayDestination)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(stream).Encode(interactiveRelayRequest{AgentID: agentID}); err != nil {
		stream.Close()
		return nil, err
	}
	return pivot.StartInteractive(ctx, stream, bufio.NewReader(stream), request)
}

func OpenClientScript(ctx context.Context, client *mux.Mux, agentID, language string, source []byte) (*pivot.InteractiveSession, error) {
	return openClientMemory(ctx, client, agentID, "script", pivot.MemoryRequest{Language: language, Size: len(source)}, source)
}

func OpenClientWASM(ctx context.Context, client *mux.Mux, agentID string, module []byte, args []string, stdin []byte) (*pivot.InteractiveSession, error) {
	return openClientMemory(ctx, client, agentID, "wasm", pivot.MemoryRequest{Args: args, Stdin: stdin, Size: len(module)}, module)
}

func OpenClientNative(ctx context.Context, client *mux.Mux, agentID string, module []byte, args []string, data []byte) (*pivot.InteractiveSession, error) {
	return openClientMemory(ctx, client, agentID, "native", pivot.MemoryRequest{Args: args, Stdin: data, Size: len(module)}, module)
}

func openClientMemory(ctx context.Context, client *mux.Mux, agentID, kind string, request pivot.MemoryRequest, source []byte) (*pivot.InteractiveSession, error) {
	if client == nil {
		return nil, errors.New("VPN session is not connected")
	}
	stream, err := client.Open(ctx, pivot.InteractiveRelayDestination)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(stream).Encode(interactiveRelayRequest{AgentID: agentID, Kind: kind}); err != nil {
		stream.Close()
		return nil, err
	}
	return pivot.StartMemorySession(ctx, stream, bufio.NewReader(stream), request, source)
}

func BridgeClientInteractive(ctx context.Context, client *mux.Mux, agentID string, local net.Conn) error {
	return bridgeClientInteractive(ctx, client, agentID, "", local)
}

func BridgeClientScript(ctx context.Context, client *mux.Mux, agentID string, local net.Conn) error {
	return bridgeClientInteractive(ctx, client, agentID, "script", local)
}

func BridgeClientWASM(ctx context.Context, client *mux.Mux, agentID string, local net.Conn) error {
	return bridgeClientInteractive(ctx, client, agentID, "wasm", local)
}

func BridgeClientNative(ctx context.Context, client *mux.Mux, agentID string, local net.Conn) error {
	return bridgeClientInteractive(ctx, client, agentID, "native", local)
}

func bridgeClientInteractive(ctx context.Context, client *mux.Mux, agentID, kind string, local net.Conn) error {
	if client == nil {
		return errors.New("VPN session is not connected")
	}
	stream, err := client.Open(ctx, pivot.InteractiveRelayDestination)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stream).Encode(interactiveRelayRequest{AgentID: agentID, Kind: kind}); err != nil {
		stream.Close()
		return err
	}
	if _, err := io.WriteString(local, "OK\n"); err != nil {
		stream.Close()
		return err
	}
	defer stream.Close()
	outputDone := make(chan struct{})
	go func() { _, _ = io.Copy(stream, local); _ = stream.CloseWrite() }()
	go func() {
		_, err := io.Copy(local, stream)
		if err != nil {
			_ = stream.Close()
		}
		if tcp, ok := local.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		close(outputDone)
	}()
	select {
	case <-outputDone:
	case <-ctx.Done():
	case <-stream.Done():
		select {
		case <-outputDone:
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

func (m *Manager) ServeInteractiveRelay(ctx context.Context, client *mux.Stream) {
	defer client.Close()
	if err := client.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReader(client)
	line, err := reader.ReadBytes('\n')
	if err != nil || len(line) > 2048 {
		pivot.RejectInteractive(client, errors.New("invalid interactive relay request"))
		return
	}
	var request interactiveRelayRequest
	if json.Unmarshal(line, &request) != nil || request.AgentID == "" {
		pivot.RejectInteractive(client, errors.New("invalid agent ID"))
		return
	}
	agent := m.Get(request.AgentID)
	if agent == nil {
		pivot.RejectInteractive(client, errors.New("agent is not connected"))
		return
	}
	destination := pivot.InteractiveDestination
	if request.Kind == "script" {
		destination = pivot.ScriptDestination
	} else if request.Kind == "wasm" {
		destination = pivot.WASMDestination
	} else if request.Kind == "native" {
		destination = pivot.NativeDestination
	} else if request.Kind != "" {
		pivot.RejectInteractive(client, errors.New("unknown task kind"))
		return
	}
	upstream, err := agent.Open(ctx, destination)
	if err != nil {
		pivot.RejectInteractive(client, err)
		return
	}
	defer upstream.Close()
	bridgeInteractive(ctx, client, reader, upstream)
}

func bridgeInteractive(ctx context.Context, client *mux.Stream, reader io.Reader, upstream *mux.Stream) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(upstream, reader); _ = upstream.CloseWrite() }()
	go func() { defer wg.Done(); _, _ = io.Copy(client, upstream); _ = client.CloseWrite() }()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		_ = client.Close()
		_ = upstream.Close()
	case <-client.Done():
		_ = upstream.Close()
	case <-upstream.Done():
		_ = client.Close()
	}
}

// InteractiveHandler is mounted inside the authenticated local control API.
func (m *Manager) interactiveHandler(w http.ResponseWriter, r *http.Request) {
	agent := m.Get(r.PathValue("id"))
	if agent == nil {
		http.Error(w, "agent is not connected", http.StatusNotFound)
		return
	}
	destination := pivot.InteractiveDestination
	if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/script" {
		destination = pivot.ScriptDestination
	} else if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/wasm" {
		destination = pivot.WASMDestination
	} else if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/native" {
		destination = pivot.NativeDestination
	}
	upstream, err := agent.Open(r.Context(), destination)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "interactive upgrade unavailable", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	defer conn.Close()
	defer upstream.Close()
	_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if rw.Flush() != nil {
		return
	}
	// The client waits for the CONNECT response before sending the request.
	outputDone := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, rw); _ = upstream.CloseWrite() }()
	go func() {
		_, err := io.Copy(conn, upstream)
		if err != nil {
			_ = upstream.Close()
		}
		_ = conn.(*net.TCPConn).CloseWrite()
		close(outputDone)
	}()
	select {
	case <-outputDone:
	case <-upstream.Done():
		select {
		case <-outputDone:
		case <-time.After(3 * time.Second):
		}
	}
}
