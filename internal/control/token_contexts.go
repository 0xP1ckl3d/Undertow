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
	"time"

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

type tokenSnapshot struct {
	storeInstanceID string
	contexts        []authcontext.Metadata
	candidates      []authcontext.Metadata
}

func cloneTokenMetadata(items []authcontext.Metadata) []authcontext.Metadata {
	return append([]authcontext.Metadata(nil), items...)
}

func (m *Manager) cachedTokenResponse(agent string) (pivot.TokenResponse, bool) {
	m.mu.RLock()
	snapshot, ok := m.tokenSnapshots[agent]
	m.mu.RUnlock()
	if !ok {
		return pivot.TokenResponse{}, false
	}
	result := pivot.TokenResponse{StoreInstanceID: snapshot.storeInstanceID, Contexts: cloneTokenMetadata(snapshot.contexts), Candidates: cloneTokenMetadata(snapshot.candidates)}
	now := time.Now()
	for i := range result.Candidates {
		if !result.Candidates[i].ExpiresAt.IsZero() && !now.Before(result.Candidates[i].ExpiresAt) {
			result.Candidates[i].State = "expired"
		}
	}
	return result, true
}

func (m *Manager) rememberTokenResponse(agent string, result pivot.TokenResponse) {
	m.mu.Lock()
	if m.tokenSnapshots == nil {
		m.tokenSnapshots = make(map[string]tokenSnapshot)
	}
	m.tokenSnapshots[agent] = tokenSnapshot{storeInstanceID: result.StoreInstanceID, contexts: cloneTokenMetadata(result.Contexts), candidates: cloneTokenMetadata(result.Candidates)}
	m.mu.Unlock()
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
	parts := strings.Split(strings.SplitN(r.URL.Path, "?", 2)[0], "/")
	if r.Method == http.MethodPost && len(parts) == 5 && parts[1] == "v1" && parts[2] == "deployments" && parts[4] == "start" {
		data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(data) > 4096 {
			return r, errors.New("deployment start request too large")
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(data, &body) != nil || body == nil {
			return r, errors.New("invalid deployment start request")
		}
		m.mu.RLock()
		store := m.operations
		m.mu.RUnlock()
		if store == nil {
			return r, errors.New("deployment history unavailable")
		}
		record, err := store.Deployment(parts[3])
		if err != nil {
			return r, errors.New("deployment unavailable")
		}
		id := ""
		rawSelection, selectionProvided := body["token_context_id"]
		if len(rawSelection) != 0 && json.Unmarshal(rawSelection, &id) != nil {
			return r, errors.New("invalid authentication context ID")
		}
		id, err = m.resolveTokenContext(r.Context(), jobOwner(r.Context()), record.SourceAgentID, id)
		if err != nil {
			return r, err
		}
		hasCredential := false
		for _, name := range []string{"username", "password", "nt_hash"} {
			var value string
			if raw := body[name]; len(raw) != 0 && json.Unmarshal(raw, &value) == nil && value != "" {
				hasCredential = true
			}
		}
		if hasCredential && !selectionProvided {
			id = "process"
		}
		if hasCredential && id != "" && id != "process" {
			return r, errors.New("choose a token context or supplied Jump credentials")
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
		agent := r.PathValue("id")
		if result, ok := m.cachedTokenResponse(agent); ok {
			result.DefaultContextID = m.tokenDefault(jobOwner(r.Context()), agent)
			w.Header().Set("Cache-Control", "no-store")
			jsonReply(w, 200, result)
			return
		}
		// Opening the workspace is a metadata read, not an agent operation. A
		// cache miss must not occupy the foreground turn or wait for a sleeping
		// agent. Operators can explicitly refresh to enqueue a live list request.
		m.mu.RLock()
		state := m.agents[agent]
		info := m.offlineAgents[agent]
		if state != nil {
			info = state.inventory
		}
		m.mu.RUnlock()
		if info.Capabilities == nil || !info.Capabilities.Allows("tokens") {
			http.Error(w, "agent authentication contexts are unsupported or disabled", 409)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		jsonReply(w, 200, pivot.TokenResponse{
			StoreInstanceID:  info.Capabilities.TokenStoreInstanceID,
			Contexts:         []authcontext.Metadata{},
			Candidates:       []authcontext.Metadata{},
			DefaultContextID: m.tokenDefault(jobOwner(r.Context()), agent),
		})
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
	info := m.offlineAgents[agent]
	if state != nil {
		info = state.inventory
	}
	m.mu.RUnlock()
	if info.Capabilities == nil || !info.Capabilities.Allows("tokens") {
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
		result, cached := m.cachedTokenResponse(agent)
		found := !cached
		for _, item := range result.Contexts {
			found = found || item.ID == request.ID
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
	releaseTurn, err := m.foregroundTurn(r.Context(), agent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestTimeout)
		return
	}
	defer releaseTurn()
	stream, err := m.openAgentForOperator(r.Context(), r.Context().Done(), agent, pivot.TokenDestination)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	result, err := pivot.ManageTokensOnStream(r.Context(), stream, request)

	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	m.rememberTokenResponse(agent, result)
	if audit, _ := r.Context().Value(tokenAuditKey{}).(*tokenAuditInfo); audit != nil && result.Created != nil && authcontext.ValidID(result.Created.ID) {
		audit.id = result.Created.ID
	}

	result.DefaultContextID = m.tokenDefault(owner, agent)
	// A stale default remains explicit and fails closed until the operator
	// selects a live context or reverts; never silently substitute process identity.
	jsonReply(w, 200, result)
}
