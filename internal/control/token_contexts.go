package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"undertow/internal/authcontext"
	"undertow/internal/pivot"
)

// Read exactly the structured header; module bytes and interactive frames stay
// untouched. A body selection overrides the session default, including process.
func readTokenOperationLine(reader *bufio.Reader, selected string) ([]byte, error) {
	var line []byte
	for len(line) < 2<<20 {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		line = append(line, b)
		if b == '\n' {
			break
		}
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return nil, errors.New("operation header too large")
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(line, &body) != nil || body == nil {
		return nil, errors.New("invalid operation header")
	}
	if raw, ok := body["token_context_id"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return nil, errors.New("invalid authentication context ID")
		}
		if id != "" {
			selected = id
		}
	}
	if selected != "" && selected != "process" && !authcontext.ValidID(selected) {
		return nil, errors.New("invalid authentication context ID")
	}
	if selected != "" {
		body["token_context_id"], _ = json.Marshal(selected)
	}
	encoded, err := json.Marshal(body)
	return append(encoded, '\n'), err
}

// Defaults belong to a live authenticated operator connection, not an account,
// agent, browser preference, or durable queue. Contexts themselves are shared.
type tokenDefaultKey struct {
	session uint64
	agent   string
}

type tokenAuditKey struct{}
type tokenAuditInfo struct{ action, id string }

func (m *Manager) bindTokenManagementAudit(r *http.Request) (*http.Request, error) {
	parts := strings.Split(r.URL.Path, "/")
	if r.Method != http.MethodPost || len(parts) != 5 || parts[1] != "v1" || parts[2] != "agents" || parts[4] != "tokens" {
		return r, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(data) > 8192 {
		return r, errors.New("authentication context request too large")
	}
	var request pivot.TokenRequest
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil {
		return r, errors.New("invalid authentication context request; logon material must be sealed by the operator client")
	}
	switch request.Action {
	case "list", "discover", "creation-key", "import", "create", "use", "revert", "remove", "clear":
	default:
		return r, errors.New("unknown authentication context action")
	}
	info := &tokenAuditInfo{action: request.Action}
	if authcontext.ValidID(request.ID) {
		info.id = request.ID
	}
	r = r.WithContext(context.WithValue(r.Context(), tokenAuditKey{}, info))
	r.Body = io.NopCloser(bytes.NewReader(data))
	return r, nil
}

func (m *Manager) tokenDefault(session uint64, agent string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tokenDefaults[tokenDefaultKey{session, agent}]
}
func (m *Manager) resolveTokenContext(ctx context.Context, session uint64, agent, id string) (string, error) {
	if id == "" {
		id = m.tokenDefault(session, agent)
	}
	if id == "" || id == "process" {
		return id, nil
	}
	if !authcontext.ValidID(id) {
		return "", errors.New("invalid authentication context ID")
	}
	m.mu.RLock()
	state := m.agents[agent]
	info := m.offlineAgents[agent]
	if state != nil {
		info = state.inventory
	}
	m.mu.RUnlock()
	if !info.Capabilities.Allows("tokens") {
		return "", errors.New("agent authentication contexts are unsupported or disabled")
	}
	return id, nil
}
func tokenOperationPath(path string) bool {
	path = strings.SplitN(path, "?", 2)[0]
	parts := strings.Split(path, "/")
	if len(parts) < 5 || parts[1] != "v1" || parts[2] != "agents" {
		return false
	}
	return len(parts) == 5 && (parts[4] == "exec" || parts[4] == "jobs") || len(parts) == 6 && parts[5] == "jobs" && (parts[4] == "scripts" || parts[4] == "wasm" || parts[4] == "native" || parts[4] == "assembly" || parts[4] == "bof")
}

// TokenOperationBody adds only an opaque selection to an existing operation.
func TokenOperationBody(ctx context.Context, path string, body any) (any, error) {
	id := pivot.TokenContextID(ctx)
	if id == "" || !tokenOperationPath(path) {
		return body, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, errors.New("invalid operation body")
	}
	fields["token_context_id"], _ = json.Marshal(id)
	return fields, nil
}

// Resolve at submission time, before audit and enqueue. The queue freezes the
// chosen ID; a subsequent default change never changes an accepted operation.
func (m *Manager) bindTokenOperation(r *http.Request) (*http.Request, error) {
	if r.Method != http.MethodPost || !tokenOperationPath(r.URL.Path) {
		return r, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (12<<20)+1))
	if err != nil || len(data) > 12<<20 {
		return r, errors.New("operation request too large")
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(data, &body) != nil || body == nil {
		return r, errors.New("invalid operation request")
	}
	id := pivot.TokenContextID(r.Context())
	if raw, ok := body["token_context_id"]; ok {
		if json.Unmarshal(raw, &id) != nil {
			return r, errors.New("invalid authentication context ID")
		}
	}
	agent := strings.Split(r.URL.Path, "/")[3]
	id, err = m.resolveTokenContext(r.Context(), jobOwner(r.Context()), agent, id)
	if err != nil {
		return r, err
	}
	if id != "" {
		body["token_context_id"], _ = json.Marshal(id)
	}
	data, err = json.Marshal(body)
	if err != nil {
		return r, err
	}
	r = r.WithContext(pivot.WithTokenContext(r.Context(), id))
	r.Body = io.NopCloser(bytes.NewReader(data))
	return r, nil
}
func (m *Manager) tokenHTTPHandlers(routes *http.ServeMux) {
	routes.HandleFunc("GET /v1/agents/{id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		m.manageTokenRequest(w, r, pivot.TokenRequest{Action: "list"})
	})
	routes.HandleFunc("POST /v1/agents/{id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		var request pivot.TokenRequest
		d := json.NewDecoder(io.LimitReader(r.Body, 8192))
		d.DisallowUnknownFields()
		if d.Decode(&request) != nil {
			http.Error(w, "invalid authentication context request", 400)
			return
		}
		if request.Action != "create" && request.SealedLogon != nil {
			http.Error(w, "sealed logon is accepted only for live context creation", 400)
			return
		}
		m.manageTokenRequest(w, r, request)
	})
}
func (m *Manager) manageTokenRequest(w http.ResponseWriter, r *http.Request, request pivot.TokenRequest) {
	agent, owner := r.PathValue("id"), jobOwner(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	// use/revert are session-local choices and never impersonate the agent.
	if request.Action == "revert" {
		m.mu.Lock()
		delete(m.tokenDefaults, tokenDefaultKey{owner, agent})
		m.mu.Unlock()
		jsonReply(w, 200, pivot.TokenResponse{Contexts: []authcontext.Metadata{}})
		return
	}
	m.mu.RLock()
	state := m.agents[agent]
	m.mu.RUnlock()
	if state == nil || state.mux == nil {
		http.Error(w, "token management requires a live connected agent; creation cannot be queued", 409)
		return
	}
	if !state.inventory.Capabilities.Allows("tokens") {
		http.Error(w, "agent authentication contexts are unsupported or disabled", 409)
		return
	}
	if request.Action == "use" {
		if owner == 0 {
			http.Error(w, "defaults require an authenticated operator connection; use --token-context for this operation", 409)
			return
		}
		if !authcontext.ValidID(request.ID) {
			http.Error(w, "invalid authentication context ID", 400)
			return
		}
		result, err := pivot.ManageTokens(r.Context(), state.mux, pivot.TokenRequest{Action: "list"})
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		found := false
		for _, item := range result.Contexts {
			if item.ID == request.ID {
				found = true
			}
		}
		if !found {
			http.Error(w, "authentication context unavailable", 409)
			return
		}
		m.mu.Lock()
		if m.tokenDefaults == nil {
			m.tokenDefaults = map[tokenDefaultKey]string{}
		}
		m.tokenDefaults[tokenDefaultKey{owner, agent}] = request.ID
		m.mu.Unlock()
		result.DefaultContextID = request.ID
		jsonReply(w, 200, result)
		return
	}
	result, err := pivot.ManageTokens(r.Context(), state.mux, request)

	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if audit, _ := r.Context().Value(tokenAuditKey{}).(*tokenAuditInfo); audit != nil && result.Created != nil && authcontext.ValidID(result.Created.ID) {
		audit.id = result.Created.ID
	}

	result.DefaultContextID = m.tokenDefault(owner, agent)
	// A stale default remains explicit and fails closed until the operator
	// selects a live context or reverts; never silently substitute process identity.
	jsonReply(w, 200, result)
}
