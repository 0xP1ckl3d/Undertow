//go:build linux || windows

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"undertow/internal/control"
	"undertow/internal/pivot"
)

//go:embed web/dist/*
var guiAssets embed.FS

type guiServer struct {
	client        *liveClientConsole
	store         *clientGUIStore
	modules       *guiModuleBank
	host          string
	launchSecret  string
	sessionSecret string
	csrfSecret    string
	transferDir   string
	transferMu    sync.Mutex
	downloads     map[string]guiDownload
}

func randomGUISecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func startClientGUI(ctx context.Context, client *liveClientConsole, address, storePath string) (string, func(), error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", nil, errors.New("GUI listener must bind a numeric loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return "", nil, err
	}
	store, err := openClientGUIStore(storePath)
	if err != nil {
		listener.Close()
		return "", nil, err
	}
	launch, err := randomGUISecret()
	if err != nil {
		listener.Close()
		store.Close()
		return "", nil, err
	}
	session, err := randomGUISecret()
	if err != nil {
		listener.Close()
		store.Close()
		return "", nil, err
	}
	csrf, err := randomGUISecret()
	if err != nil {
		listener.Close()
		store.Close()
		return "", nil, err
	}
	modules, err := openGUIModuleBank(storePath, store)
	if err != nil {
		listener.Close()
		store.Close()
		return "", nil, err
	}
	transferDir, err := os.MkdirTemp("", "undertow-gui-transfers-*")
	if err != nil {
		listener.Close()
		store.Close()
		return "", nil, err
	}
	app := &guiServer{client: client, store: store, modules: modules, host: listener.Addr().String(), launchSecret: launch, sessionSecret: session, csrfSecret: csrf, transferDir: transferDir, downloads: make(map[string]guiDownload)}
	server := &http.Server{Handler: app.handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() { _ = server.Serve(listener) }()
	return "http://" + listener.Addr().String() + "/#" + launch, func() { _ = server.Close(); _ = store.Close(); cleanupGUITransferDir(transferDir) }, nil
}

func (g *guiServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth", g.auth)
	mux.HandleFunc("GET /api/status", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/status" }))
	mux.HandleFunc("GET /api/events", g.events)
	mux.HandleFunc("GET /api/agents/{id}/tokens", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/tokens" }))
	mux.HandleFunc("POST /api/agents/{id}/tokens", g.manageTokens)
	mux.HandleFunc("POST /api/agents/{id}/tokens/from-credential", g.remote(http.MethodPost, func(r *http.Request) string {
		return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/tokens/from-credential"
	}))
	mux.HandleFunc("GET /api/credentials", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/credentials" }))
	mux.HandleFunc("POST /api/credentials", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/credentials" }))
	mux.HandleFunc("PUT /api/credentials/{id}", g.remote(http.MethodPut, func(r *http.Request) string { return "/v1/credentials/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("DELETE /api/credentials/{id}", g.remote(http.MethodDelete, func(r *http.Request) string { return "/v1/credentials/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("GET /api/topology", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/topology" }))
	mux.HandleFunc("GET /api/history", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/history" }))
	mux.HandleFunc("GET /api/deployments", g.remote(http.MethodGet, func(r *http.Request) string {
		if id := r.URL.Query().Get("source_agent_id"); id != "" {
			return "/v1/deployments?source_agent_id=" + url.QueryEscape(id)
		}
		return "/v1/deployments"
	}))
	mux.HandleFunc("POST /api/deployments", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/deployments" }))
	mux.HandleFunc("GET /api/deployments/{id}", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/deployments/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("POST /api/deployments/{id}/prepare", g.remote(http.MethodPost, func(r *http.Request) string {
		return "/v1/deployments/" + url.PathEscape(r.PathValue("id")) + "/prepare"
	}))
	mux.HandleFunc("POST /api/deployments/{id}/start", g.remote(http.MethodPost, func(r *http.Request) string {
		return "/v1/deployments/" + url.PathEscape(r.PathValue("id")) + "/start"
	}))
	mux.HandleFunc("POST /api/deployments/{id}/link", g.remote(http.MethodPost, func(r *http.Request) string { return "/v1/deployments/" + url.PathEscape(r.PathValue("id")) + "/link" }))
	mux.HandleFunc("GET /api/operator/me", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/operator/me" }))
	mux.HandleFunc("GET /api/operators", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/operators" }))
	mux.HandleFunc("GET /api/team/operators", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/team/operators" }))
	mux.HandleFunc("GET /api/team/messages", g.remote(http.MethodGet, func(r *http.Request) string {
		query := url.Values{}
		for _, key := range []string{"peer", "before", "after"} {
			if value := r.URL.Query().Get(key); value != "" {
				query.Set(key, value)
			}
		}
		if encoded := query.Encode(); encoded != "" {
			return "/v1/team/messages?" + encoded
		}
		return "/v1/team/messages"
	}))
	mux.HandleFunc("POST /api/team/messages", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/team/messages" }))
	mux.HandleFunc("GET /api/team/tasks", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/team/tasks" }))
	mux.HandleFunc("POST /api/team/tasks", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/team/tasks" }))
	mux.HandleFunc("PUT /api/team/tasks/{id}", g.remote(http.MethodPut, func(r *http.Request) string { return "/v1/team/tasks/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("POST /api/operators", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/operators" }))
	mux.HandleFunc("PUT /api/operators/{id}", g.remote(http.MethodPut, func(r *http.Request) string { return "/v1/operators/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("DELETE /api/operators/{id}", g.remote(http.MethodDelete, func(r *http.Request) string { return "/v1/operators/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("GET /api/transfers", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/transfers" }))
	mux.HandleFunc("GET /api/worker-logs", g.remote(http.MethodGet, func(r *http.Request) string {
		if after := r.URL.Query().Get("after"); after != "" {
			return "/v1/worker-logs?after=" + url.QueryEscape(after)
		}
		return "/v1/worker-logs"
	}))
	mux.HandleFunc("GET /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		p, err := g.store.Preferences()
		if err != nil {
			http.Error(w, "preferences unavailable", http.StatusInternalServerError)
			return
		}
		guiJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("PUT /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		var p guiPreferences
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&p) != nil {
			http.Error(w, "invalid preferences", http.StatusBadRequest)
			return
		}
		if err := g.store.SavePreferences(p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := g.store.Preferences()
		if err != nil {
			http.Error(w, "preferences unavailable", http.StatusInternalServerError)
			return
		}
		guiJSON(w, http.StatusOK, saved)
	})
	mux.HandleFunc("PUT /api/preferences/archived-visibility", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ShowArchived *bool `json:"show_archived"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 512))
		decoder.DisallowUnknownFields()
		if r.Header.Get("Content-Type") != "application/json" || decoder.Decode(&request) != nil || request.ShowArchived == nil {
			http.Error(w, "invalid archived visibility preference", http.StatusBadRequest)
			return
		}
		if err := g.store.SetArchivedVisibility(*request.ShowArchived); err != nil {
			http.Error(w, "could not save archived visibility", http.StatusInternalServerError)
			return
		}
		guiJSON(w, http.StatusOK, map[string]bool{"show_archived": *request.ShowArchived})
	})
	mux.HandleFunc("GET /api/layout", func(w http.ResponseWriter, r *http.Request) {
		positions, err := g.store.Layout()
		if err != nil {
			http.Error(w, "layout unavailable", http.StatusInternalServerError)
			return
		}
		guiJSON(w, http.StatusOK, positions)
	})
	mux.HandleFunc("PUT /api/layout", func(w http.ResponseWriter, r *http.Request) {
		var position guiPosition
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&position) != nil {
			http.Error(w, "invalid layout", http.StatusBadRequest)
			return
		}
		if err := g.store.SavePosition(position); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/agents/{id}", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("PUT /api/agents/{id}/nickname", g.remote(http.MethodPut, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/nickname" }))
	mux.HandleFunc("PUT /api/agents/{id}/sleep", g.remote(http.MethodPut, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/sleep" }))
	mux.HandleFunc("PUT /api/agents/{id}/archive", g.remote(http.MethodPut, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/archive" }))
	mux.HandleFunc("POST /api/agents/{id}/shutdown", g.remote(http.MethodPost, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/shutdown" }))
	mux.HandleFunc("POST /api/agents/{id}/session/kill", g.killAgentSession)
	mux.HandleFunc("GET /api/agents/{id}/host-results", g.remote(http.MethodGet, func(r *http.Request) string {
		return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/host-results"
	}))
	mux.HandleFunc("GET /api/agents/{id}/files", g.remote(http.MethodGet, func(r *http.Request) string {
		values := url.Values{}
		if path := r.URL.Query().Get("path"); path != "" {
			values.Set("path", path)
		}
		if offset := r.URL.Query().Get("offset"); offset != "" {
			values.Set("offset", offset)
		}
		endpoint := "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/files"
		if encoded := values.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}
		return endpoint
	}))
	mux.HandleFunc("POST /api/agents/{id}/files/upload", g.uploadFile)
	mux.HandleFunc("POST /api/agents/{id}/files/download", g.downloadFile)
	mux.HandleFunc("GET /api/transfers/{id}/download", g.serveTransferDownload)
	mux.HandleFunc("GET /api/agents/{id}/screens", g.remote(http.MethodGet, func(r *http.Request) string {
		return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/screens"
	}))
	mux.HandleFunc("POST /api/agents/{id}/screenshots", g.remote(http.MethodPost, func(r *http.Request) string {
		return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/screenshots"
	}))
	mux.HandleFunc("GET /api/screenshots", g.remote(http.MethodGet, func(r *http.Request) string {
		endpoint := "/v1/screenshots"
		if agentID := r.URL.Query().Get("agent_id"); agentID != "" {
			endpoint += "?agent_id=" + url.QueryEscape(agentID)
		}
		return endpoint
	}))
	mux.HandleFunc("GET /api/screenshots/{id}/image", g.screenshotImage)
	mux.HandleFunc("GET /api/agents/{id}/terminal", g.terminal)
	mux.HandleFunc("POST /api/agents/{id}/command", g.agentCommand)
	mux.HandleFunc("POST /api/agents/{id}/command-stream", g.agentCommandStream)
	mux.HandleFunc("GET /api/agents/{id}/console-history", g.consoleHistory)
	mux.HandleFunc("POST /api/agents/{id}/exec", g.remote(http.MethodPost, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/exec" }))
	mux.HandleFunc("POST /api/agents/{id}/jobs", g.remote(http.MethodPost, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/jobs" }))
	mux.HandleFunc("GET /api/jobs", g.remote(http.MethodGet, func(r *http.Request) string {
		if id := r.URL.Query().Get("agent_id"); id != "" {
			return "/v1/jobs?agent_id=" + url.QueryEscape(id)
		}
		return "/v1/jobs"
	}))
	mux.HandleFunc("GET /api/jobs/{id}", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/jobs/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("GET /api/jobs/{id}/output", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/jobs/" + url.PathEscape(r.PathValue("id")) + "/output" }))
	mux.HandleFunc("GET /api/jobs/{id}/download", g.downloadJobOutput)
	mux.HandleFunc("GET /api/jobs/{id}/files/{fileid}/download", g.downloadJobFile)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", g.remote(http.MethodPost, func(r *http.Request) string { return "/v1/jobs/" + url.PathEscape(r.PathValue("id")) + "/cancel" }))
	mux.HandleFunc("DELETE /api/jobs/{id}", g.remote(http.MethodDelete, func(r *http.Request) string { return "/v1/jobs/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("GET /api/relays", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/relays" }))
	mux.HandleFunc("POST /api/agents/{id}/relays", g.remote(http.MethodPost, func(r *http.Request) string { return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/relays" }))
	mux.HandleFunc("DELETE /api/agents/{id}/relays", g.remote(http.MethodDelete, func(r *http.Request) string {
		return "/v1/agents/" + url.PathEscape(r.PathValue("id")) + "/relays?bind=" + url.QueryEscape(r.URL.Query().Get("bind"))
	}))
	mux.HandleFunc("POST /api/routes", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/routes" }))
	mux.HandleFunc("DELETE /api/routes", g.remote(http.MethodDelete, func(r *http.Request) string {
		return "/v1/routes?prefix=" + url.QueryEscape(r.URL.Query().Get("prefix"))
	}))
	mux.HandleFunc("GET /api/forwards", g.clientForwards(http.MethodGet))
	mux.HandleFunc("POST /api/forwards", g.clientForwards(http.MethodPost))
	mux.HandleFunc("DELETE /api/forwards", g.clientForwards(http.MethodDelete))
	mux.HandleFunc("GET /api/transports", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/transports" }))
	mux.HandleFunc("GET /api/server-public-host", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/server-public-host" }))
	mux.HandleFunc("PUT /api/server-public-host", g.remote(http.MethodPut, func(*http.Request) string { return "/v1/server-public-host" }))
	mux.HandleFunc("POST /api/transports/{name}", g.remote(http.MethodPost, func(r *http.Request) string { return "/v1/transports/" + url.PathEscape(r.PathValue("name")) }))
	mux.HandleFunc("DELETE /api/transports/{name}", g.stopTransport)
	mux.HandleFunc("GET /api/payload-retrieval-host", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/payload-retrieval-host" }))
	mux.HandleFunc("PUT /api/payload-retrieval-host", g.remote(http.MethodPut, func(*http.Request) string { return "/v1/payload-retrieval-host" }))
	mux.HandleFunc("GET /api/payload-retrieval-path", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/payload-retrieval-path" }))
	mux.HandleFunc("PUT /api/payload-retrieval-path", g.remote(http.MethodPut, func(*http.Request) string { return "/v1/payload-retrieval-path" }))
	mux.HandleFunc("GET /api/profiles", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/agent-profiles" }))
	mux.HandleFunc("POST /api/profiles", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/agent-profiles" }))
	mux.HandleFunc("GET /api/profiles/{name}", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/agent-profiles/" + url.PathEscape(r.PathValue("name")) }))
	mux.HandleFunc("PUT /api/profiles/{name}", g.remote(http.MethodPut, func(r *http.Request) string { return "/v1/agent-profiles/" + url.PathEscape(r.PathValue("name")) }))
	mux.HandleFunc("DELETE /api/profiles/{name}", g.remote(http.MethodDelete, func(r *http.Request) string { return "/v1/agent-profiles/" + url.PathEscape(r.PathValue("name")) }))
	mux.HandleFunc("GET /api/artifacts", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/agent-artifacts" }))
	mux.HandleFunc("POST /api/artifacts", g.remote(http.MethodPost, func(*http.Request) string { return "/v1/agent-artifacts" }))
	mux.HandleFunc("POST /api/artifacts/upload", g.uploadCustomArtifact)
	mux.HandleFunc("GET /api/artifacts/{id}", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("DELETE /api/artifacts/{id}", g.remote(http.MethodDelete, func(r *http.Request) string { return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("GET /api/agent-hosts", g.remote(http.MethodGet, func(*http.Request) string { return "/v1/agent-hosts" }))
	mux.HandleFunc("GET /api/agent-hosts/{id}", g.remote(http.MethodGet, func(r *http.Request) string { return "/v1/agent-hosts/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("DELETE /api/agent-hosts/{id}", g.remote(http.MethodDelete, func(r *http.Request) string { return "/v1/agent-hosts/" + url.PathEscape(r.PathValue("id")) }))
	mux.HandleFunc("GET /api/artifacts/{id}/agent-hosts", g.remote(http.MethodGet, func(r *http.Request) string {
		return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) + "/agent-hosts"
	}))
	mux.HandleFunc("POST /api/artifacts/{id}/agent-hosts", g.remote(http.MethodPost, func(r *http.Request) string {
		return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) + "/agent-hosts"
	}))
	mux.HandleFunc("GET /api/artifacts/{id}/host", g.remote(http.MethodGet, func(r *http.Request) string {
		return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) + "/host"
	}))
	mux.HandleFunc("POST /api/artifacts/{id}/host", g.remote(http.MethodPost, func(r *http.Request) string {
		return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) + "/host"
	}))
	mux.HandleFunc("DELETE /api/artifacts/{id}/host", g.remote(http.MethodDelete, func(r *http.Request) string {
		return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) + "/host"
	}))
	mux.HandleFunc("POST /api/artifacts/{id}/revoke", g.remote(http.MethodPost, func(r *http.Request) string {
		return "/v1/agent-artifacts/" + url.PathEscape(r.PathValue("id")) + "/revoke"
	}))
	mux.HandleFunc("GET /api/artifacts/{id}/deploy-script", g.deployScript)
	mux.HandleFunc("GET /api/agent-hosts/{id}/deploy-script", g.agentHostDeployScript)
	mux.HandleFunc("GET /api/agent-hosts/{id}/probe-script", g.agentHostProbeScript)
	mux.HandleFunc("GET /api/artifacts/{id}/download", g.downloadArtifact)
	mux.HandleFunc("GET /api/modules", g.listModules)
	mux.HandleFunc("POST /api/modules", g.importModule)
	mux.HandleFunc("DELETE /api/modules/{name}", g.unloadModule)
	mux.HandleFunc("POST /api/agents/{id}/modules/{name}/run", g.runModule)
	mux.HandleFunc("GET /api/client", func(w http.ResponseWriter, r *http.Request) {
		if !g.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		g.client.mu.RLock()
		sessionID := ""
		if g.client.sessionID != 0 {
			sessionID = strconv.FormatUint(g.client.sessionID, 10)
		}
		response := map[string]any{"session_id": sessionID, "transport": g.client.transport, "server_address": g.client.serverAddress, "vpn": g.client.vpn, "internal": g.client.internal, "operator_only": g.client.operatorOnly, "csrf": g.csrfSecret}
		g.client.mu.RUnlock()
		guiJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /api/client/routes", func(w http.ResponseWriter, r *http.Request) {
		g.client.routeMu.Lock()
		out := make([]map[string]any, 0, len(g.client.routes))
		for _, route := range g.client.routes {
			out = append(out, map[string]any{"prefix": route.Prefix, "agent_id": route.AgentID, "manual": route.Manual, "disabled": route.Disabled, "installed": g.client.active[route.Prefix]})
		}
		g.client.routeMu.Unlock()
		guiJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/client/routes", func(w http.ResponseWriter, r *http.Request) {
		var route control.AcceptedRoute
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&route) != nil || route.AgentID == "" {
			http.Error(w, "invalid route", http.StatusBadRequest)
			return
		}
		claims, err := g.actionClaims()
		if err != nil {
			http.Error(w, "preferences unavailable", http.StatusInternalServerError)
			return
		}
		if _, err := g.client.AcceptClientRoute(control.WithActionClaims(r.Context(), claims), route.Prefix, route.AgentID, route.Manual); err != nil {
			guiJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/client/routes", func(w http.ResponseWriter, r *http.Request) {
		claims, err := g.actionClaims()
		if err != nil {
			http.Error(w, "preferences unavailable", http.StatusInternalServerError)
			return
		}
		if err := g.client.RemoveClientRoute(control.WithActionClaims(r.Context(), claims), r.URL.Query().Get("prefix"), r.URL.Query().Get("agent_id")); err != nil {
			guiJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /api/client/routes", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Prefix  string `json:"prefix"`
			AgentID string `json:"agent_id"`
			Enabled *bool  `json:"enabled"`
		}
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&request) != nil || request.AgentID == "" || request.Enabled == nil {
			http.Error(w, "prefix, agent, and enabled are required", http.StatusBadRequest)
			return
		}
		claims, err := g.actionClaims()
		if err != nil {
			http.Error(w, "preferences unavailable", http.StatusInternalServerError)
			return
		}
		if err := g.client.SetClientRouteEnabled(control.WithActionClaims(r.Context(), claims), request.Prefix, request.AgentID, *request.Enabled); err != nil {
			guiJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PUT /api/client/mode/{name}", g.clientMode)
	static, _ := fs.Sub(guiAssets, "web/dist")
	mux.Handle("/", http.FileServer(http.FS(static)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != g.host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/auth" && !g.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !g.sameOrigin(r) {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			if r.URL.Path != "/api/auth" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Undertow-CSRF")), []byte(g.csrfSecret)) != 1 {
				http.Error(w, "invalid CSRF token", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (g *guiServer) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "http://"+g.host
}

func (g *guiServer) authorized(r *http.Request) bool {
	cookie, err := r.Cookie("undertow_gui")
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(g.sessionSecret)) == 1
}

func (g *guiServer) auth(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Secret string `json:"secret"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 256)).Decode(&body) != nil || subtle.ConstantTimeCompare([]byte(body.Secret), []byte(g.launchSecret)) != 1 {
		http.Error(w, "invalid launch token", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "undertow_gui", Value: g.sessionSecret, Path: "/api/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	guiJSON(w, http.StatusOK, map[string]string{"csrf": g.csrfSecret})
}

func (g *guiServer) remote(method string, path func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body any
		if method == http.MethodPost || method == http.MethodPut {
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
				return
			}
			var raw json.RawMessage
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&raw); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			body = raw
		}
		claims, err := g.actionClaims()
		if err != nil {
			http.Error(w, "preferences unavailable", http.StatusInternalServerError)
			return
		}
		data, err := g.client.call(control.WithActionClaims(r.Context(), claims), method, path(r), body)
		if err != nil {
			var remoteErr *control.RemoteAPIError
			if errors.As(err, &remoteErr) && remoteErr.Status >= 400 && remoteErr.Status <= 599 {
				message := remoteAPIErrorMessage(remoteErr.Body)
				if message == "" {
					message = http.StatusText(remoteErr.Status)
				}
				guiJSON(w, remoteErr.Status, map[string]string{"error": message})
				return
			}
			guiJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if len(data) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !json.Valid(data) {
			guiJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid server response"})
			return
		}
		data, err = guiExactSessionIDs(data)
		if err != nil {
			guiJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid server response"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

func remoteAPIErrorMessage(body string) string {
	var response struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(body), &response) == nil && strings.TrimSpace(response.Error) != "" {
		return strings.TrimSpace(response.Error)
	}
	return strings.TrimSpace(body)
}

func (g *guiServer) actionClaims() (control.ActionClaims, error) {
	id, err := randomGUISecret()
	if err != nil {
		return control.ActionClaims{}, err
	}
	return control.ActionClaims{ActionID: id, Source: "gui"}, nil
}

func guiJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (g *guiServer) events(w http.ResponseWriter, r *http.Request) {
	g.client.mu.RLock()
	session := g.client.session
	g.client.mu.RUnlock()
	if session == nil {
		http.Error(w, "client is disconnected", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	after := control.EventCursor(r.Header.Get("Last-Event-ID"))
	_ = control.StreamEvents(r.Context(), session, after, func(event control.Event) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, "id: "+strconv.FormatUint(event.Seq, 10)+"\nevent: change\ndata: "+string(data)+"\n\n"); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
}

func (g *guiServer) terminal(w http.ResponseWriter, r *http.Request) {
	if !g.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("csrf")), []byte(g.csrfSecret)) != 1 {
		http.Error(w, "invalid terminal request", http.StatusForbidden)
		return
	}
	g.client.mu.RLock()
	clientSession := g.client.session
	g.client.mu.RUnlock()
	if clientSession == nil {
		http.Error(w, "client is disconnected", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	claims, err := g.actionClaims()
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, mustGUIJSON(map[string]string{"type": "error", "data": "preferences unavailable"}))
		return
	}
	messageType, startData, err := conn.Read(ctx)
	if err != nil {
		return
	}
	request, err := parseGUITerminalStart(messageType, startData)
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, mustGUIJSON(map[string]string{"type": "error", "data": err.Error()}))
		return
	}
	session, err := control.OpenClientInteractive(control.WithActionClaims(ctx, claims), clientSession, r.PathValue("id"), request)
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, mustGUIJSON(map[string]string{"type": "error", "data": err.Error()}))
		return
	}
	defer session.Close()
	if err := conn.Write(ctx, websocket.MessageText, mustGUIJSON(map[string]string{"type": "ready"})); err != nil {
		return
	}
	go func() {
		defer cancel()
		for {
			kind, data, err := session.Read()
			if err != nil {
				return
			}
			typeName := "output"
			switch kind {
			case pivot.InteractiveStderr:
				typeName = "stderr"
			case pivot.InteractiveExit:
				typeName = "exit"
			case pivot.InteractiveError:
				typeName = "error"
			}
			if conn.Write(ctx, websocket.MessageText, mustGUIJSON(map[string]string{"type": typeName, "data": string(data)})) != nil {
				return
			}
			if kind == pivot.InteractiveExit || kind == pivot.InteractiveError {
				return
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var message struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if len(data) > 33000 || json.Unmarshal(data, &message) != nil {
			return
		}
		switch message.Type {
		case "input":
			if len(message.Data) > 32<<10 || session.Send([]byte(message.Data)) != nil {
				return
			}
		case "resize":
			if message.Cols == 0 || message.Rows == 0 || session.Resize(message.Cols, message.Rows) != nil {
				return
			}
		default:
			return
		}
	}
}

func parseGUITerminalStart(messageType websocket.MessageType, data []byte) (pivot.InteractiveRequest, error) {
	if messageType != websocket.MessageText || len(data) > 8192 {
		return pivot.InteractiveRequest{}, errors.New("invalid shell start request")
	}
	var message struct {
		TokenContextID string   `json:"token_context_id,omitempty"`
		Type           string   `json:"type"`
		Argv           []string `json:"argv"`
	}
	if json.Unmarshal(data, &message) != nil || message.Type != "start" || len(message.Argv) > 32 {
		return pivot.InteractiveRequest{}, errors.New("invalid shell start request")
	}
	return pivot.InteractiveRequest{TokenContextID: message.TokenContextID, Argv: message.Argv, Cols: 100, Rows: 30}, nil
}

func mustGUIJSON(value any) []byte { data, _ := json.Marshal(value); return data }
