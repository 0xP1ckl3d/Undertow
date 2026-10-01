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
}

type remoteResponse struct {
	Status int    `json:"status"`
	Body   []byte `json:"body"`
}

// ServeRemote exposes only client status, agent execution, and the caller's
// own pivot setting. Server operator actions remain on the loopback API.
func (m *Manager) ServeRemote(ctx context.Context, token string, clientID uint64, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-done:
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
	if !strings.HasPrefix(request.Path, "/v1/") || len(request.Path) > 2048 || request.Method != http.MethodGet && request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writeRemoteResponse(stream, remoteResponse{Status: http.StatusBadRequest, Body: []byte("invalid API request")})
		return
	}
	httpRequest, err := http.NewRequestWithContext(context.WithValue(ctx, jobOwnerKey{}, clientID), request.Method, "http://localhost"+request.Path, bytes.NewReader(request.Body))
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
	if request.Method == http.MethodGet && path == "/v1/status" && request.URL.RawQuery == "" {
		return true
	}
	if request.Method == http.MethodGet && (path == "/v1/jobs" || strings.HasPrefix(path, "/v1/jobs/")) {
		return true
	}
	if request.Method == http.MethodPost && strings.HasPrefix(path, "/v1/jobs/") && strings.HasSuffix(path, "/cancel") && request.URL.RawQuery == "" {
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
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		return false
	}
	if path == clientPrefix+"/internal" || path == clientPrefix+"/routes" || path == clientPrefix+"/forwards" {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) == 6 && parts[1] == "v1" && parts[2] == "agents" && parts[3] != "" && (parts[4] == "scripts" || parts[4] == "wasm" || parts[4] == "native" || parts[4] == "bof") && parts[5] == "jobs" && !strings.ContainsAny(parts[3], "%\\") {
		return true
	}
	return len(parts) == 5 && parts[1] == "v1" && parts[2] == "agents" && parts[3] != "" && (parts[4] == "exec" || parts[4] == "jobs") && !strings.ContainsAny(parts[3], "%\\")
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
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
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
	if err := json.NewEncoder(stream).Encode(remoteRequest{Method: method, Path: path, Body: raw}); err != nil {
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
