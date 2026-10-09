package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type credentialAuditKey struct{}
type credentialAuditInfo struct{ id, action string }

func (m *Manager) bindCredentialAudit(r *http.Request) (*http.Request, error) {
	path := r.URL.Path
	info := &credentialAuditInfo{}
	if path == "/v1/credentials" && r.Method == http.MethodPost {
		info.action = "create"
	}
	if strings.HasPrefix(path, "/v1/credentials/") && (r.Method == http.MethodPut || r.Method == http.MethodDelete) {
		info.id = strings.TrimPrefix(path, "/v1/credentials/")
		if !credentialIDValid(info.id) {
			return r, nil
		}
		if r.Method == http.MethodPut {
			info.action = "replace"
		} else {
			info.action = "remove"
		}
	}
	if r.Method == http.MethodPost && (strings.HasSuffix(path, "/tokens/from-credential") || strings.HasPrefix(path, "/v1/deployments/") && strings.HasSuffix(path, "/start")) {
		limit := int64(4097)
		data, err := io.ReadAll(io.LimitReader(r.Body, limit))
		if err != nil || len(data) >= int(limit) {
			return r, io.ErrUnexpectedEOF
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		var fields struct {
			CredentialID string `json:"credential_id"`
		}
		if json.Unmarshal(data, &fields) == nil && credentialIDValid(fields.CredentialID) {
			info.id = fields.CredentialID
			info.action = "use"
		}
	}
	if info.action != "" {
		r = r.WithContext(context.WithValue(r.Context(), credentialAuditKey{}, info))
	}
	return r, nil
}

func (m *Manager) authorizedCredentialStore(w http.ResponseWriter, r *http.Request) (*OperationsStore, OperatorAccount, bool) {
	w.Header().Set("Cache-Control", "no-store")
	actor := boundActionFromContext(r.Context())
	account, err := m.ActiveOperator(actor.ClientSessionID)
	if err != nil || account.ID == "" {
		http.Error(w, "authenticated operator required", http.StatusForbidden)
		return nil, OperatorAccount{}, false
	}
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		http.Error(w, "credential store unavailable", http.StatusServiceUnavailable)
		return nil, OperatorAccount{}, false
	}
	return store, account, true
}

func decodeCredentialInput(r *http.Request) (CredentialInput, error) {
	var input CredentialInput
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2049))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return CredentialInput{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return CredentialInput{}, io.ErrUnexpectedEOF
	}
	return input, nil
}

func (m *Manager) credentialHTTPHandlers(routes *http.ServeMux) {
	routes.HandleFunc("GET /v1/credentials", func(w http.ResponseWriter, r *http.Request) {
		store, operator, ok := m.authorizedCredentialStore(w, r)
		if !ok {
			return
		}
		items, err := store.ListCredentials(operator.ID, operator.Role)
		if err != nil {
			http.Error(w, "credential list unavailable", 500)
			return
		}
		jsonReply(w, 200, items)
	})
	routes.HandleFunc("POST /v1/credentials", func(w http.ResponseWriter, r *http.Request) {
		store, operator, ok := m.authorizedCredentialStore(w, r)
		if !ok {
			return
		}
		input, err := decodeCredentialInput(r)
		if err != nil {
			http.Error(w, "invalid credential request", 400)
			return
		}
		defer func() { input.Secret = "" }()
		record, err := store.CreateCredential(operator.ID, input)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if audit, _ := r.Context().Value(credentialAuditKey{}).(*credentialAuditInfo); audit != nil {
			audit.id = record.ID
		}
		jsonReply(w, 201, record)
	})
	routes.HandleFunc("PUT /v1/credentials/{id}", func(w http.ResponseWriter, r *http.Request) {
		store, operator, ok := m.authorizedCredentialStore(w, r)
		if !ok {
			return
		}
		input, err := decodeCredentialInput(r)
		if err != nil {
			http.Error(w, "invalid credential request", 400)
			return
		}
		defer func() { input.Secret = "" }()
		record, err := store.ReplaceCredential(r.PathValue("id"), operator.ID, operator.Role, input)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonReply(w, 200, record)
	})
	routes.HandleFunc("DELETE /v1/credentials/{id}", func(w http.ResponseWriter, r *http.Request) {
		store, operator, ok := m.authorizedCredentialStore(w, r)
		if !ok {
			return
		}
		if err := store.DeleteCredential(r.PathValue("id"), operator.ID, operator.Role); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.WriteHeader(204)
	})
}

func credentialIDValid(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// Recheck the operator account and entry ACL when queued work dispatches.
// Durable Jobs contain only the opaque reference and non-secret account label.
func (m *Manager) resolveStoredCredential(operatorID, id string) (credentialMaterial, error) {
	if operatorID == "" || !credentialIDValid(id) {
		return credentialMaterial{}, errCredentialUnavailable
	}
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		return credentialMaterial{}, errCredentialUnavailable
	}
	operator, err := store.Operator(operatorID)
	if err != nil || operator.Disabled {
		return credentialMaterial{}, errCredentialUnavailable
	}
	return store.ResolveCredential(id, operatorID, operator.Role)
}
