//go:build linux || windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"undertow/internal/control"
)

// JSON session IDs are uint64 in Undertow. Preserve their decimal spelling
// for JavaScript, whose Number type cannot represent the full range exactly.
func guiExactSessionIDs(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var visit func(any) any
	visit = func(v any) any {
		switch item := v.(type) {
		case map[string]any:
			for key, child := range item {
				if key == "session_id" || key == "client_session_id" {
					if number, ok := child.(json.Number); ok {
						if number.String() == "0" {
							item[key] = ""
						} else {
							item[key] = number.String()
						}
						continue
					}
				}
				item[key] = visit(child)
			}
		case []any:
			for i, child := range item {
				item[i] = visit(child)
			}
		}
		return v
	}
	return json.Marshal(visit(value))
}

func (g *guiServer) killAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/%\\") {
		http.Error(w, "invalid agent", http.StatusBadRequest)
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	data, err := g.client.call(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		guiJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var status struct {
		Agents []control.AgentInfo `json:"agents"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		guiJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid server status"})
		return
	}
	var agent control.AgentInfo
	for _, candidate := range status.Agents {
		if candidate.ID == id {
			agent = candidate
			break
		}
	}
	if agent.SessionID == 0 {
		guiJSON(w, http.StatusConflict, map[string]string{"error": "agent has no active session"})
		return
	}
	_, err = g.client.call(ctx, http.MethodPost, fmt.Sprintf("/v1/sessions/%d/kill", agent.SessionID), map[string]any{})
	if err != nil {
		guiJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// These handlers bind actions to the local client session. The browser never
// chooses another client's session ID or receives the server control token.
func (g *guiServer) clientForwards(method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := g.client.id()
		if id == 0 {
			guiJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "client is disconnected"})
			return
		}
		path := fmt.Sprintf("/v1/clients/%d/forwards", id)
		var body any
		switch method {
		case http.MethodPost:
			var forward struct {
				AgentID string `json:"agent_id"`
				Bind    string `json:"bind"`
				Target  string `json:"target"`
			}
			if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&forward) != nil || forward.AgentID == "" || forward.Bind == "" || forward.Target == "" {
				http.Error(w, "agent, bind, and target are required", http.StatusBadRequest)
				return
			}
			body = forward
		case http.MethodDelete:
			agent, bind := r.URL.Query().Get("agent_id"), r.URL.Query().Get("bind")
			if agent == "" || bind == "" {
				http.Error(w, "agent and bind are required", http.StatusBadRequest)
				return
			}
			path += "?agent_id=" + url.QueryEscape(agent) + "&bind=" + url.QueryEscape(bind)
		}
		claims, err := g.actionClaims()
		if err != nil {
			http.Error(w, "preferences unavailable", http.StatusInternalServerError)
			return
		}
		data, err := g.client.call(control.WithActionClaims(r.Context(), claims), method, path, body)
		if err != nil {
			guiJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if len(data) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

func (g *guiServer) clientMode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "vpn" && name != "internal" {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request) != nil || request.Enabled == nil {
		http.Error(w, "enabled boolean is required", http.StatusBadRequest)
		return
	}
	if g.client.operatorOnly {
		guiJSON(w, http.StatusConflict, map[string]string{"error": "routing modes require a VPN client with a TUN device"})
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	mode := "off"
	if *request.Enabled {
		mode = "on"
	}
	var out bytes.Buffer
	g.client.routeMu.Lock()
	if name == "vpn" {
		err = g.client.vpnCommand(control.WithActionClaims(r.Context(), claims), []string{"vpn", mode}, &out)
	} else {
		err = g.client.internalCommand(control.WithActionClaims(r.Context(), claims), []string{"internal", mode}, &out)
	}
	g.client.routeMu.Unlock()
	if err != nil {
		guiJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	guiJSON(w, http.StatusOK, map[string]string{"message": out.String()})
}

// A local GUI must not sever the carrier carrying its own control session.
// The server still applies its usual active-session checks for other carriers.
func (g *guiServer) stopTransport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "dns" && name != "quic" && name != "websocket" {
		http.NotFound(w, r)
		return
	}
	g.client.mu.RLock()
	current, connected := g.client.transport, g.client.sessionID != 0
	g.client.mu.RUnlock()
	if connected && name == current {
		guiJSON(w, http.StatusConflict, map[string]string{"error": "switch this client to another carrier before stopping its current carrier"})
		return
	}
	path := "/v1/transports/" + url.PathEscape(name)
	if r.URL.Query().Get("force") == "true" {
		path += "?force=true"
	}
	g.remote(http.MethodDelete, func(*http.Request) string { return path })(w, r)
}
