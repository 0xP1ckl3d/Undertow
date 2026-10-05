package control

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type OperatorCredentials struct {
	ID       string `json:"id"`
	Password string `json:"password"`
}

func (m *Manager) AuthenticateOperator(id, password string) (OperatorAccount, error) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		// In-memory managers used by protocol tests have no persisted accounts.
		// The server startup path always configures a store and requires bootstrap.
		return OperatorAccount{}, nil
	}
	return store.AuthenticateOperator(id, password)
}

func OperatorCredentialsFromHello(data []byte) OperatorCredentials {
	var hello struct {
		Operator OperatorCredentials `json:"operator"`
	}
	_ = json.Unmarshal(data, &hello)
	return hello.Operator
}

func (m *Manager) ActiveOperator(clientID uint64) (OperatorAccount, error) {
	m.mu.RLock()
	client := m.clients[clientID]
	store := m.operations
	m.mu.RUnlock()
	if client == nil {
		return OperatorAccount{}, errors.New("operator session is not authenticated")
	}
	if store == nil {
		return OperatorAccount{}, nil
	}
	if client.operator.ID == "" {
		return OperatorAccount{}, errors.New("operator session is not authenticated")
	}
	current, err := store.Operator(client.operator.ID)
	if err != nil || current.Disabled || current.Version != client.operator.Version {
		return OperatorAccount{}, errors.New("operator account changed; reconnect with current credentials")
	}
	return current, nil
}

func (m *Manager) DisconnectOperator(id string) {
	m.mu.RLock()
	var sessions []*clientState
	for _, c := range m.clients {
		if c.operator.ID == id {
			sessions = append(sessions, c)
		}
	}
	m.mu.RUnlock()
	for _, c := range sessions {
		_ = c.mux.Close()
	}
}

func (m *Manager) operatorHTTPHandlers(muxer *http.ServeMux) {
	muxer.HandleFunc("GET /v1/operator/me", func(w http.ResponseWriter, r *http.Request) {
		actor := boundActionFromContext(r.Context())
		if actor.ClientSessionID == 0 {
			http.Error(w, "operator client required", http.StatusForbidden)
			return
		}
		a, err := m.ActiveOperator(actor.ClientSessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		jsonReply(w, http.StatusOK, a)
	})
	leader := func(w http.ResponseWriter, r *http.Request) (*OperationsStore, bool) {
		actor := boundActionFromContext(r.Context())
		a, err := m.ActiveOperator(actor.ClientSessionID)
		if err != nil || a.Role != TeamLeaderRole {
			http.Error(w, "Team Leader required", http.StatusForbidden)
			return nil, false
		}
		m.mu.RLock()
		store := m.operations
		m.mu.RUnlock()
		return store, true
	}
	muxer.HandleFunc("GET /v1/operators", func(w http.ResponseWriter, r *http.Request) {
		s, ok := leader(w, r)
		if !ok {
			return
		}
		a, err := s.ListOperators()
		if err != nil {
			http.Error(w, "operators unavailable", 500)
			return
		}
		jsonReply(w, 200, a)
	})
	muxer.HandleFunc("POST /v1/operators", func(w http.ResponseWriter, r *http.Request) {
		s, ok := leader(w, r)
		if !ok {
			return
		}
		var in struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
			Password    string `json:"password"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil {
			http.Error(w, "invalid account", 400)
			return
		}
		if err := s.CreateOperator(in.ID, strings.TrimSpace(in.DisplayName), in.Password, in.Role); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(201)
	})
	muxer.HandleFunc("PUT /v1/operators/{id}", func(w http.ResponseWriter, r *http.Request) {
		s, ok := leader(w, r)
		if !ok {
			return
		}
		var in struct {
			Role     *string `json:"role"`
			Disabled *bool   `json:"disabled"`
			Password *string `json:"password"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || (in.Role == nil && in.Disabled == nil && in.Password == nil) {
			http.Error(w, "invalid account update", 400)
			return
		}
		id := r.PathValue("id")
		if err := s.UpdateOperator(id, in.Role, in.Disabled, in.Password); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(204)
		time.AfterFunc(time.Second, func() { m.DisconnectOperator(id) })
	})
	muxer.HandleFunc("DELETE /v1/operators/{id}", func(w http.ResponseWriter, r *http.Request) {
		s, ok := leader(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if err := s.DeleteOperator(id); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(204)
		time.AfterFunc(time.Second, func() { m.DisconnectOperator(id) })
	})
}
