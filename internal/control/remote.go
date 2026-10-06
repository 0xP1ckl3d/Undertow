package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type remoteRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
	Actor  ActionClaims    `json:"actor,omitempty"`
}

type remoteResponse struct {
	Status int    `json:"status"`
	Body   []byte `json:"body"`
}

// IsForegroundRequest identifies operations that may wait through a check-in.
// Their request deadline must cover sleeping time; it does not create a Job.
func IsForegroundRequest(method, path string) bool {
	path = strings.SplitN(path, "?", 2)[0]
	if method == http.MethodPost && strings.HasPrefix(path, "/v1/agents/") {
		return strings.HasSuffix(path, "/exec") || strings.HasSuffix(path, "/screenshots") || strings.HasSuffix(path, "/shutdown")
	}
	if method == http.MethodPost && strings.HasPrefix(path, "/v1/sessions/") {
		return strings.HasSuffix(path, "/kill")
	}
	return method == http.MethodGet && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/screens")
}

func truncateClaim(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		value = value[:max]
	}
	return value
}

// ServeRemote exposes the established client API to an authenticated operator session.
func (m *Manager) ServeRemote(ctx context.Context, token string, clientID uint64, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	readCtx, cancelRead := context.WithTimeout(ctx, 50*time.Second)
	readDone := make(chan struct{})
	go func() {
		select {
		case <-readCtx.Done():
			select {
			case <-readDone:
			default:
				_ = stream.Close()
			}
		case <-readDone:
		}
	}()
	defer cancelRead()
	readComplete := false
	defer func() {
		if !readComplete {
			close(readDone)
		}
	}()
	var request remoteRequest
	if err := json.NewDecoder(io.LimitReader(stream, 6<<20)).Decode(&request); err != nil {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusBadRequest, Body: []byte("invalid request")})
		return
	}
	// The decoder can finish at the JSON newline before the client's FIN is
	// delivered. Wait for that half-close so the deferred stream.Close does
	// not reset and discard a response that is still queued for sending.
	if _, err := io.CopyN(io.Discard, stream, 6<<20); err != io.EOF {
		return
	}
	close(readDone)
	readComplete = true
	cancelRead()
	if !strings.HasPrefix(request.Path, "/v1/") || len(request.Path) > 8192 || request.Method != http.MethodGet && request.Method != http.MethodPost && request.Method != http.MethodPut && request.Method != http.MethodDelete {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusBadRequest, Body: []byte("invalid API request")})
		return
	}
	operationTimeout := 50 * time.Second
	if IsForegroundRequest(request.Method, request.Path) {
		operationTimeout = 40 * time.Hour
	}
	operationCtx, cancelOperation := context.WithTimeout(ctx, operationTimeout)
	defer cancelOperation()
	go func() {
		select {
		case <-stream.Done():
			cancelOperation()
		case <-operationCtx.Done():
			_ = stream.Close()
		}
	}()
	m.mu.RLock()
	client := m.clients[clientID]
	clientKey := ""
	if client != nil {
		clientKey = client.peer.Snapshot().AgentID
	}
	m.mu.RUnlock()
	if client == nil {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusForbidden, Body: []byte("client session is no longer connected")})
		return
	}
	operator, err := m.ActiveOperator(clientID)
	if err != nil {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusForbidden, Body: []byte(err.Error())})
		return
	}
	action := actionContext{ActionClaims: request.Actor, ClientID: clientKey, ClientSessionID: clientID}
	if action.Source != "gui" {
		action.Source = "client_console"
	}
	action.ActionID = truncateClaim(action.ActionID, 128)
	action.OperatorID = operator.ID
	action.DisplayName = operator.DisplayName
	requestContext := context.WithValue(context.WithValue(operationCtx, jobOwnerKey{}, clientID), actionContextKey{}, action)
	httpRequest, err := http.NewRequestWithContext(requestContext, request.Method, "http://localhost"+request.Path, bytes.NewReader(request.Body))
	if err != nil {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusBadRequest, Body: []byte("invalid API request")})
		return
	}
	if !clientRequestAllowed(httpRequest, clientID) {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusForbidden, Body: []byte("server operator command unavailable from VPN client")})
		return
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	m.handler(token).ServeHTTP(recorder, httpRequest)
	if recorder.Body.Len() > 1<<20 {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusBadGateway, Body: []byte("response too large")})
		return
	}
	writeRemoteResponse(stream, remoteResponse{Status: recorder.Code, Body: recorder.Body.Bytes()})
}

func clientRequestAllowed(request *http.Request, clientID uint64) bool {
	path := request.URL.EscapedPath()
	if path == "/v1/team/operators" || path == "/v1/team/tasks" {
		return request.URL.RawQuery == "" && (request.Method == http.MethodGet || path == "/v1/team/tasks" && request.Method == http.MethodPost)
	}
	if path == "/v1/team/messages" {
		if request.Method == http.MethodPost {
			return request.URL.RawQuery == ""
		}
		if request.Method != http.MethodGet {
			return false
		}
		query := request.URL.Query()
		for key, values := range query {
			if key != "peer" && key != "before" && key != "after" || len(values) != 1 {
				return false
			}
		}
		if peer := query.Get("peer"); peer != "" && !operatorIDPattern.MatchString(peer) {
			return false
		}
		for _, key := range []string{"before", "after"} {
			if query.Has(key) {
				n, err := strconv.ParseInt(query.Get(key), 10, 64)
				if err != nil || n <= 0 {
					return false
				}
			}
		}
		return !query.Has("before") || !query.Has("after")
	}
	if strings.HasPrefix(path, "/v1/team/tasks/") && request.Method == http.MethodPut && request.URL.RawQuery == "" {
		id := strings.TrimPrefix(path, "/v1/team/tasks/")
		return len(id) == 24 && strings.Trim(id, "0123456789abcdef") == ""
	}
	if path == "/v1/operator/me" && request.Method == http.MethodGet && request.URL.RawQuery == "" {
		return true
	}
	if path == "/v1/operators" && (request.Method == http.MethodGet || request.Method == http.MethodPost) && request.URL.RawQuery == "" {
		return true
	}
	if strings.HasPrefix(path, "/v1/operators/") && (request.Method == http.MethodPut || request.Method == http.MethodDelete) && request.URL.RawQuery == "" {
		parts := strings.Split(path, "/")
		return len(parts) == 4 && parts[3] != "" && !strings.ContainsAny(parts[3], "%\\")
	}
	if request.Method == http.MethodGet && path == "/v1/status" && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodGet && path == "/v1/topology" && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodGet && path == "/v1/history" && request.URL.RawQuery == "" {
		return true
	}
	if path == "/v1/transfers" && request.URL.RawQuery == "" && (request.Method == http.MethodGet || request.Method == http.MethodPost) {
		return true
	}
	if request.Method == http.MethodPut && strings.HasPrefix(path, "/v1/transfers/") && request.URL.RawQuery == "" {
		parts := strings.Split(path, "/")
		return len(parts) == 4 && parts[3] != "" && !strings.ContainsAny(parts[3], "%\\")
	}
	if path == "/v1/server-public-host" && request.URL.RawQuery == "" && (request.Method == http.MethodGet || request.Method == http.MethodPut) {
		return true
	}
	if request.Method == http.MethodGet && path == "/v1/worker-logs" {
		if request.URL.RawQuery == "" {
			return true
		}
		query := request.URL.Query()
		if len(query) != 1 || len(query["after"]) != 1 {
			return false
		}
		_, err := strconv.ParseUint(query.Get("after"), 10, 64)
		return err == nil
	}
	if request.URL.RawQuery == "" && (path == "/v1/transports" && request.Method == http.MethodGet || path == "/v1/relays" && request.Method == http.MethodGet || path == "/v1/routes" && request.Method == http.MethodPost) {
		return true
	}
	if path == "/v1/routes" && request.Method == http.MethodDelete {
		_, err := netip.ParsePrefix(request.URL.Query().Get("prefix"))
		return err == nil
	}
	if strings.HasPrefix(path, "/v1/transports/") {
		if request.Method == http.MethodPost {
			return request.URL.RawQuery == ""
		}
		if request.Method == http.MethodDelete {
			return request.URL.RawQuery == "" || request.URL.RawQuery == "force=true"
		}
	}
	if strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/relays") && request.URL.RawQuery == "" && request.Method == http.MethodPost {
		return true
	}
	if strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/relays") && request.Method == http.MethodDelete {
		return true
	}
	if distributionRequest(request) || payloadChunkRequest(request) {
		return true
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/events") && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodPut && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/nickname") && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodPut && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/sleep") && request.URL.RawQuery == "" {
		parts := strings.Split(path, "/")
		return len(parts) == 5 && parts[3] != "" && !strings.ContainsAny(parts[3], "%\\")
	}
	if request.Method == http.MethodPut && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/archive") && request.URL.RawQuery == "" {
		parts := strings.Split(path, "/")
		return len(parts) == 5 && parts[3] != "" && !strings.ContainsAny(parts[3], "%\\")
	}
	if request.Method == http.MethodGet && request.URL.RawQuery == "" {
		parts := strings.Split(path, "/")
		if len(parts) == 4 && parts[1] == "v1" && parts[2] == "agents" && parts[3] != "" && !strings.ContainsAny(parts[3], "%\\") {
			return true
		}
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/host-results") && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/files") {
		query := request.URL.Query()
		for key, values := range query {
			if key != "path" && key != "offset" || len(values) != 1 {
				return false
			}
		}
		return len(query.Get("path")) <= 4096
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/screens") && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodPost && strings.HasPrefix(path, "/v1/agents/") && strings.HasSuffix(path, "/screenshots") && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodGet && path == "/v1/screenshots" {
		query := request.URL.Query()
		return len(query) == 0 || len(query) == 1 && len(query["agent_id"]) == 1
	}
	if request.Method == http.MethodGet && strings.HasPrefix(path, "/v1/screenshots/") {
		parts := strings.Split(path, "/")
		if len(parts) == 4 && parts[3] != "" && request.URL.RawQuery == "" {
			return true
		}
		if len(parts) == 5 && parts[4] == "chunk" && parts[3] != "" {
			query := request.URL.Query()
			if len(query) != 1 || len(query["offset"]) != 1 {
				return false
			}
			offset, err := strconv.ParseInt(query.Get("offset"), 10, 64)
			return err == nil && offset >= 0
		}
	}
	if request.Method == http.MethodGet && (path == "/v1/jobs" || strings.HasPrefix(path, "/v1/jobs/")) {
		return true
	}
	if request.Method == http.MethodPost && strings.HasPrefix(path, "/v1/jobs/") && strings.HasSuffix(path, "/cancel") && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodDelete && strings.HasPrefix(path, "/v1/jobs/") && request.URL.RawQuery == "" {
		return true
	}
	clientPrefix := "/v1/clients/" + strconv.FormatUint(clientID, 10)
	if request.Method == http.MethodGet && path == clientPrefix+"/forwards" && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodDelete && path == clientPrefix+"/routes" {
		_, err := netip.ParsePrefix(request.URL.Query().Get("prefix"))
		return err == nil
	}
	if request.Method == http.MethodDelete && path == clientPrefix+"/forwards" {
		return request.URL.Query().Get("agent_id") != "" && request.URL.Query().Get("bind") != ""
	}
	if request.Method != http.MethodPost || request.URL.RawQuery != "" && !(strings.HasSuffix(path, "/exec") && request.URL.RawQuery == "foreground=1") {
		return false
	}
	if path == clientPrefix+"/internal" || path == clientPrefix+"/vpn" || path == clientPrefix+"/routes" || path == clientPrefix+"/forwards" {
		return true
	}
	if strings.HasPrefix(path, "/v1/sessions/") && strings.HasSuffix(path, "/kill") {
		parts := strings.Split(path, "/")
		return len(parts) == 5 && parts[3] != "" && !strings.ContainsAny(parts[3], "%\\")
	}
	parts := strings.Split(path, "/")
	if len(parts) == 6 && parts[1] == "v1" && parts[2] == "agents" && parts[3] != "" && (parts[4] == "scripts" || parts[4] == "wasm" || parts[4] == "native" || parts[4] == "assembly" || parts[4] == "bof") && parts[5] == "jobs" && !strings.ContainsAny(parts[3], "%\\") {
		return true
	}
	return len(parts) == 5 && parts[1] == "v1" && parts[2] == "agents" && parts[3] != "" && (parts[4] == "exec" || parts[4] == "jobs" || parts[4] == "shutdown") && !strings.ContainsAny(parts[3], "%\\")
}

func distributionRequest(request *http.Request) bool {
	path := request.URL.EscapedPath()
	return request.URL.RawQuery == "" && (path == "/v1/payload-retrieval-path" || path == "/v1/payload-retrieval-host" || path == "/v1/agent-profiles" || strings.HasPrefix(path, "/v1/agent-profiles/") || path == "/v1/agent-artifacts" || strings.HasPrefix(path, "/v1/agent-artifacts/") || path == "/v1/agent-hosts" || strings.HasPrefix(path, "/v1/agent-hosts/"))
}

func payloadChunkRequest(request *http.Request) bool {
	if request.Method != http.MethodGet {
		return false
	}
	parts := strings.Split(request.URL.EscapedPath(), "/")
	if len(parts) != 6 || parts[1] != "v1" || parts[2] != "agent-artifacts" || parts[3] == "" || parts[4] != "download" || parts[5] != "chunk" {
		return false
	}
	query := request.URL.Query()
	values := query["offset"]
	if len(query) != 1 || len(values) != 1 {
		return false
	}
	offset, err := strconv.ParseInt(values[0], 10, 64)
	return err == nil && offset >= 0
}

func writeRemoteResponse(stream *mux.Stream, response remoteResponse) {
	_ = json.NewEncoder(stream).Encode(response)
	_ = stream.CloseWrite()
}

func CallRemote(ctx context.Context, session *mux.Mux, method, path string, body any) ([]byte, error) {
	if session == nil {
		return nil, errors.New("VPN session is not connected")
	}
	var raw json.RawMessage
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	timeout := 50 * time.Second
	if IsForegroundRequest(method, path) {
		timeout = 40 * time.Hour
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stream, err := session.Open(ctx, pivot.ControlDestination)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-done:
		}
	}()
	if err := json.NewEncoder(stream).Encode(remoteRequest{Method: method, Path: path, Body: raw, Actor: claimsFromContext(ctx)}); err != nil {
		return nil, err
	}
	if err := stream.CloseWrite(); err != nil {
		return nil, err
	}
	responseBytes, err := io.ReadAll(io.LimitReader(stream, 2<<20))
	if err != nil {
		return nil, err
	}
	var response remoteResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return nil, fmt.Errorf("operator response: %w", err)
	}
	if response.Status >= 300 {
		return nil, fmt.Errorf("operator API: HTTP %d: %s", response.Status, strings.TrimSpace(string(response.Body)))
	}
	return response.Body, nil
}
