package control

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

func decodeTeamRequest(w http.ResponseWriter, r *http.Request, max int64, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	decoder.DisallowUnknownFields()
	if r.Header.Get("Content-Type") != "application/json" || decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid team request", http.StatusBadRequest)
		return false
	}
	return true
}

func (m *Manager) teamHTTPHandlers(muxer *http.ServeMux) {
	authorized := func(w http.ResponseWriter, r *http.Request) (OperatorAccount, *OperationsStore, bool) {
		actor := boundActionFromContext(r.Context())
		if actor.ClientSessionID == 0 {
			http.Error(w, "authenticated operator client required", http.StatusForbidden)
			return OperatorAccount{}, nil, false
		}
		account, err := m.ActiveOperator(actor.ClientSessionID)
		if err != nil {
			http.Error(w, "operator session is unavailable", http.StatusForbidden)
			return OperatorAccount{}, nil, false
		}
		m.mu.RLock()
		store := m.operations
		m.mu.RUnlock()
		if store == nil {
			http.Error(w, "team history unavailable", http.StatusServiceUnavailable)
			return OperatorAccount{}, nil, false
		}
		return account, store, true
	}
	muxer.HandleFunc("GET /v1/team/operators", func(w http.ResponseWriter, r *http.Request) {
		_, store, ok := authorized(w, r)
		if !ok {
			return
		}
		accounts, err := store.ListOperators()
		if err != nil {
			http.Error(w, "operator roster unavailable", http.StatusInternalServerError)
			return
		}
		active := make([]OperatorAccount, 0, len(accounts))
		for _, account := range accounts {
			if !account.Disabled && !account.Revoked {
				active = append(active, account)
			}
		}
		jsonReply(w, http.StatusOK, active)
	})
	muxer.HandleFunc("GET /v1/team/messages", func(w http.ResponseWriter, r *http.Request) {
		actor, store, ok := authorized(w, r)
		if !ok {
			return
		}
		query := r.URL.Query()
		for key := range query {
			if key != "peer" && key != "before" && key != "after" || len(query[key]) != 1 {
				http.Error(w, "invalid conversation filter", http.StatusBadRequest)
				return
			}
		}
		var before, after int64
		var err error
		if query.Has("before") {
			before, err = strconv.ParseInt(query.Get("before"), 10, 64)
		}
		if err == nil && query.Has("after") {
			after, err = strconv.ParseInt(query.Get("after"), 10, 64)
		}
		if err != nil {
			http.Error(w, "invalid message cursor", http.StatusBadRequest)
			return
		}
		messages, err := store.TeamMessages(actor.ID, query.Get("peer"), before, after)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusOK, messages)
	})
	muxer.HandleFunc("POST /v1/team/messages", func(w http.ResponseWriter, r *http.Request) {
		actor, store, ok := authorized(w, r)
		if !ok {
			return
		}
		var request struct {
			RecipientID string `json:"recipient_id"`
			Body        string `json:"body"`
		}
		if !decodeTeamRequest(w, r, 5<<10, &request) {
			return
		}
		message, err := store.PostTeamMessage(actor, request.RecipientID, request.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusCreated, message)
		m.PublishEvent("team.changed", "")
	})
	muxer.HandleFunc("GET /v1/team/tasks", func(w http.ResponseWriter, r *http.Request) {
		_, store, ok := authorized(w, r)
		if !ok {
			return
		}
		tasks, err := store.TeamTasks()
		if err != nil {
			http.Error(w, "team assignments unavailable", http.StatusInternalServerError)
			return
		}
		jsonReply(w, http.StatusOK, tasks)
	})
	muxer.HandleFunc("POST /v1/team/tasks", func(w http.ResponseWriter, r *http.Request) {
		actor, store, ok := authorized(w, r)
		if !ok {
			return
		}
		var request struct {
			AssigneeID  string `json:"assignee_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if !decodeTeamRequest(w, r, 5<<10, &request) {
			return
		}
		task, err := store.CreateTeamTask(actor, request.AssigneeID, request.Title, request.Description)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusCreated, task)
		m.PublishEvent("team.changed", "")
	})
	muxer.HandleFunc("PUT /v1/team/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		actor, store, ok := authorized(w, r)
		if !ok {
			return
		}
		var request struct {
			Status string `json:"status"`
		}
		if !decodeTeamRequest(w, r, 256, &request) {
			return
		}
		task, err := store.UpdateTeamTask(actor, r.PathValue("id"), request.Status)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrTeamTaskNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, ErrTeamTaskForbidden) {
				status = http.StatusForbidden
			}
			http.Error(w, err.Error(), status)
			return
		}
		jsonReply(w, http.StatusOK, task)
		m.PublishEvent("team.changed", "")
	})
}
