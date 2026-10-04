//go:build linux || windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"undertow/internal/control"
)

// Download and script generation reuse the same typed Go workflow as the
// terminal. The browser receives an artifact or a script, never a server token.
func (g *guiServer) downloadArtifact(w http.ResponseWriter, r *http.Request) {
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	a, err := resolvePayload(ctx, g.client.call, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if a.Size <= 0 || a.Size > 512<<20 {
		http.Error(w, "artifact size is outside the GUI download limit", http.StatusRequestEntityTooLarge)
		return
	}
	dir, err := os.MkdirTemp("", "undertow-gui-payload-")
	if err != nil {
		http.Error(w, "download workspace unavailable", http.StatusInternalServerError)
		return
	}
	defer os.Remove(dir)
	path := filepath.Join(dir, a.Filename)
	defer os.Remove(path)
	if err := runPayloadDownload(ctx, io.Discard, g.client.call, a.ID, path); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "verified artifact unavailable", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "verified artifact unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, a.Filename, stat.ModTime(), file)
}

func (g *guiServer) deployScript(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != "powershell" && format != "shell" {
		http.Error(w, "format must be powershell or shell", http.StatusBadRequest)
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	a, err := resolvePayload(ctx, g.client.call, r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if !a.Hosted {
		http.Error(w, "host the artifact before generating a deploy script", http.StatusConflict)
		return
	}
	hosted, err := fetchHostedPayload(ctx, g.client.call, a.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if !strings.HasPrefix(hosted.Retrieval, "https://") {
		http.Error(w, "no HTTPS download URL is available", http.StatusConflict)
		return
	}
	var script bytes.Buffer
	if err := printDeployScript(&script, hosted, format); err != nil {
		if !errors.Is(err, r.Context().Err()) {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
	if script.Len() > 64<<10 {
		http.Error(w, "deploy script exceeds local limit", http.StatusBadGateway)
		return
	}
	guiJSON(w, http.StatusOK, map[string]string{"script": script.String(), "filename": "deploy-" + a.ID + map[string]string{"powershell": ".ps1", "shell": ".sh"}[format]})
}

func (g *guiServer) agentHostDeployScript(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != "powershell" && format != "shell" {
		http.Error(w, "format must be powershell or shell", http.StatusBadRequest)
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	data, err := g.client.call(ctx, http.MethodGet, "/v1/agent-hosts/"+url.PathEscape(r.PathValue("id")), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var host agentPayloadHostInfo
	if err := json.Unmarshal(data, &host); err != nil || host.ID == "" || !strings.HasPrefix(host.Retrieval, "https://") {
		http.Error(w, "invalid agent-hosted payload record", http.StatusBadGateway)
		return
	}
	a, err := resolvePayload(ctx, g.client.call, host.ArtifactID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hosted := hostedArtifactInfo{Artifact: a.Artifact, Retrieval: host.Retrieval, RetrievalPath: host.RetrievalPath, TLSSelfSigned: host.TLSSelfSigned, TLSCertSHA256: host.TLSCertSHA256, TLSPublicKeyPin: host.TLSPublicKeyPin}
	var script bytes.Buffer
	if err := printDeployScript(&script, hosted, format); err != nil || script.Len() > 64<<10 {
		http.Error(w, "could not generate agent-hosted deploy script", http.StatusBadGateway)
		return
	}
	guiJSON(w, http.StatusOK, map[string]string{"script": script.String(), "filename": "deploy-" + a.ID + map[string]string{"powershell": ".ps1", "shell": ".sh"}[format]})
}
