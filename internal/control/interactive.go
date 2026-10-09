package control

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type interactiveRelayRequest struct {
	AgentID        string       `json:"agent_id"`
	Kind           string       `json:"kind,omitempty"`
	Actor          ActionClaims `json:"actor,omitempty"`
	TokenContextID string       `json:"token_context_id,omitempty"`
}

func selectInteractiveToken(ctx context.Context, r pivot.InteractiveRequest) string {
	if r.TokenContextID != "" {
		return r.TokenContextID
	}
	return pivot.TokenContextID(ctx)
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
	if err := json.NewEncoder(stream).Encode(interactiveRelayRequest{TokenContextID: selectInteractiveToken(ctx, request), AgentID: agentID, Actor: claimsFromContext(ctx)}); err != nil {
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

func OpenClientAssembly(ctx context.Context, client *mux.Mux, agentID string, source []byte, args []string) (*pivot.InteractiveSession, error) {
	return openClientMemory(ctx, client, agentID, "assembly", pivot.MemoryRequest{Args: args, Size: len(source)}, source)
}

func OpenClientBOF(ctx context.Context, client *mux.Mux, agentID string, object, arguments []byte) (*pivot.InteractiveSession, error) {
	return openClientMemory(ctx, client, agentID, "bof", pivot.MemoryRequest{Size: len(object), Stdin: arguments}, object)
}

func openClientMemory(ctx context.Context, client *mux.Mux, agentID, kind string, request pivot.MemoryRequest, source []byte) (*pivot.InteractiveSession, error) {
	if client == nil {
		return nil, errors.New("VPN session is not connected")
	}
	stream, err := client.Open(ctx, pivot.InteractiveRelayDestination)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(stream).Encode(interactiveRelayRequest{TokenContextID: pivot.TokenContextID(ctx), AgentID: agentID, Kind: kind, Actor: claimsFromContext(ctx)}); err != nil {
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

func BridgeClientAssembly(ctx context.Context, client *mux.Mux, agentID string, local net.Conn) error {
	return bridgeClientInteractive(ctx, client, agentID, "assembly", local)
}

func BridgeClientBOF(ctx context.Context, client *mux.Mux, agentID string, local net.Conn) error {
	return bridgeClientInteractive(ctx, client, agentID, "bof", local)
}

func bridgeClientInteractive(ctx context.Context, client *mux.Mux, agentID, kind string, local net.Conn) error {
	if client == nil {
		return errors.New("VPN session is not connected")
	}
	stream, err := client.Open(ctx, pivot.InteractiveRelayDestination)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stream).Encode(interactiveRelayRequest{TokenContextID: pivot.TokenContextID(ctx), AgentID: agentID, Kind: kind}); err != nil {
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
	m.ServeInteractiveRelayForClient(ctx, 0, client)
}

func (m *Manager) ServeInteractiveRelayForClient(ctx context.Context, clientID uint64, client *mux.Stream) {
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
	headerTimer := time.AfterFunc(30*time.Second, func() { _ = client.Close() })
	operationLine, err := readTokenOperationLine(reader, request.TokenContextID)
	headerTimer.Stop()
	if err != nil {
		pivot.RejectInteractive(client, err)
		return
	}
	var selection struct {
		TokenContextID string `json:"token_context_id"`
	}
	if json.Unmarshal(operationLine, &selection) != nil {
		pivot.RejectInteractive(client, errors.New("invalid operation request"))
		return
	}
	id, err := m.resolveTokenContext(ctx, clientID, request.AgentID, selection.TokenContextID)
	if err != nil {
		pivot.RejectInteractive(client, err)
		return
	}
	request.TokenContextID = id
	finishAudit, err := m.startInteractiveAudit(clientID, request)
	if err != nil {
		pivot.RejectInteractive(client, errors.New("audit unavailable"))
		return
	}
	destination := pivot.InteractiveDestination
	if request.Kind == "script" {
		destination = pivot.ScriptDestination
	} else if request.Kind == "wasm" {
		destination = pivot.WASMDestination
	} else if request.Kind == "native" {
		destination = pivot.NativeDestination
	} else if request.Kind == "assembly" {
		destination = pivot.AssemblyDestination
	} else if request.Kind == "bof" {
		destination = pivot.BOFDestination
	} else if request.Kind != "" {
		finishAudit(http.StatusBadRequest)
		pivot.RejectInteractive(client, errors.New("unknown task kind"))
		return
	}
	if request.Kind != "" {
		turnCtx, cancelTurn := context.WithCancel(ctx)
		defer cancelTurn()
		go func() {
			select {
			case <-client.Done():
				cancelTurn()
			case <-turnCtx.Done():
			}
		}()
		release, turnErr := m.foregroundTurn(turnCtx, request.AgentID)
		if turnErr != nil {
			finishAudit(http.StatusRequestTimeout)
			pivot.RejectInteractive(client, turnErr)
			return
		}
		defer release()
	}
	upstream, err := m.openAgentForOperator(ctx, client.Done(), request.AgentID, destination)
	if err != nil {
		finishAudit(http.StatusBadGateway)
		pivot.RejectInteractive(client, err)
		return
	}
	defer upstream.Close()
	finishAudit(http.StatusOK)
	var selectedBody map[string]json.RawMessage
	_ = json.Unmarshal(operationLine, &selectedBody)
	if request.TokenContextID != "" {
		selectedBody["token_context_id"], _ = json.Marshal(request.TokenContextID)
	}
	operationLine, err = json.Marshal(selectedBody)
	if err != nil {
		return
	}
	operationLine = append(operationLine, '\n')
	if _, err = upstream.Write(operationLine); err != nil {
		return
	}
	bridgeInteractive(ctx, client, reader, upstream)
}

func (m *Manager) startInteractiveAudit(clientID uint64, request interactiveRelayRequest) (func(int), error) {
	m.mu.RLock()
	store := m.operations
	client := m.clients[clientID]
	m.mu.RUnlock()
	if store == nil {
		return func(int) {}, nil
	}
	if clientID != 0 && client == nil {
		return nil, errors.New("client disconnected")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(random[:])
	key := ""
	if client != nil {
		key = client.peer.Snapshot().AgentID
	}
	source := "server_console"
	trust := "local_unverified"
	if clientID != 0 {
		source = "client_console"
		trust = "client_bound_operator_claim"
		if request.Actor.Source == "gui" {
			source = "gui"
		}
	}
	operatorID, displayName := "", ""
	if client != nil {
		operator, err := m.ActiveOperator(clientID)
		if err != nil {
			return nil, err
		}
		operatorID, displayName = operator.ID, operator.DisplayName
		if operatorID != "" {
			trust = "server_authenticated_operator"
		}
	}
	path := "/v1/agents/" + request.AgentID + "/interactive"
	if request.Kind != "" {
		path = "/v1/agents/" + request.AgentID + "/" + request.Kind
	}
	record := AuditRecord{TokenContextID: request.TokenContextID, ID: id, ActionID: truncateClaim(request.Actor.ActionID, 128), At: time.Now().UTC(), Action: "CONNECT", Target: path, ClientID: key, ClientSessionID: clientID, OperatorID: operatorID, DisplayName: displayName, Source: source, IdentityTrust: trust}
	if err := store.RecordAudit(record); err != nil {
		return nil, err
	}
	return func(status int) {
		if err := store.CompleteAudit(id, status); err != nil {
			log.Printf("interactive audit result: %v", err)
		}
		m.PublishEvent("audit.recorded", id)
	}, nil
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
	id, tokenErr := m.resolveTokenContext(r.Context(), jobOwner(r.Context()), r.PathValue("id"), r.Header.Get("X-Undertow-Token-Context"))
	if tokenErr != nil {
		http.Error(w, tokenErr.Error(), 409)
		return
	}
	destination := pivot.InteractiveDestination
	if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/script" {
		destination = pivot.ScriptDestination
	} else if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/wasm" {
		destination = pivot.WASMDestination
	} else if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/native" {
		destination = pivot.NativeDestination
	} else if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/assembly" {
		destination = pivot.AssemblyDestination
	} else if r.URL.Path == "/v1/agents/"+r.PathValue("id")+"/bof" {
		destination = pivot.BOFDestination
	}
	if destination != pivot.InteractiveDestination {
		release, turnErr := m.foregroundTurn(r.Context(), r.PathValue("id"))
		if turnErr != nil {
			http.Error(w, turnErr.Error(), http.StatusRequestTimeout)
			return
		}
		defer release()
	}
	upstream, err := m.openAgentForOperator(r.Context(), r.Context().Done(), r.PathValue("id"), destination)
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
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		line, err := readTokenOperationLine(rw.Reader, id)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			_ = pivot.WriteInteractiveError(conn, err)
			_ = upstream.Close()
			return
		}
		var selected struct {
			TokenContextID string `json:"token_context_id"`
		}
		_ = json.Unmarshal(line, &selected)
		selectedID, err := m.resolveTokenContext(r.Context(), jobOwner(r.Context()), r.PathValue("id"), selected.TokenContextID)
		if err != nil {
			_ = pivot.WriteInteractiveError(conn, err)
			_ = upstream.Close()
			return
		}
		kind := ""
		if destination != pivot.InteractiveDestination {
			parts := strings.Split(r.URL.Path, "/")
			kind = parts[len(parts)-1]
		}
		finishAudit, err := m.startInteractiveAudit(0, interactiveRelayRequest{AgentID: r.PathValue("id"), Kind: kind, TokenContextID: selectedID})
		if err != nil {
			_ = pivot.WriteInteractiveError(conn, errors.New("audit unavailable"))
			_ = upstream.Close()
			return
		}
		if _, err = upstream.Write(line); err != nil {
			finishAudit(http.StatusBadGateway)
			return
		}
		finishAudit(http.StatusOK)
		_, _ = io.Copy(upstream, rw)
		_ = upstream.CloseWrite()
	}()
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
