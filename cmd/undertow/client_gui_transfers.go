//go:build linux || windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"undertow/internal/control"
	"undertow/internal/pivot"
)

const maxGUITransferSize = 512 << 20

type guiDownload struct {
	path    string
	name    string
	expires time.Time
}

func cleanupGUITransferDir(dir string) {
	_ = os.RemoveAll(dir)
}

func (g *guiServer) uploadFile(w http.ResponseWriter, r *http.Request) {
	remotePath := r.URL.Query().Get("path")
	if remotePath == "" || len(remotePath) > 2048 || strings.ContainsRune(remotePath, 0) {
		http.Error(w, "remote file path required", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		http.Error(w, "binary file required", http.StatusUnsupportedMediaType)
		return
	}
	if r.ContentLength < 0 || r.ContentLength > maxGUITransferSize {
		http.Error(w, "upload size must be known and at most 512 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	stage, err := os.CreateTemp(g.transferDir, "upload-*")
	if err != nil {
		http.Error(w, "cannot stage upload", http.StatusInternalServerError)
		return
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	written, err := io.Copy(stage, http.MaxBytesReader(w, r.Body, maxGUITransferSize))
	if err != nil || written != r.ContentLength {
		http.Error(w, "incomplete upload", http.StatusBadRequest)
		return
	}
	if err := stage.Sync(); err != nil {
		http.Error(w, "cannot stage upload", http.StatusInternalServerError)
		return
	}
	g.streamGUITransfer(w, r, "upload", stage.Name(), remotePath, "")
}

func (g *guiServer) downloadFile(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request) != nil || request.Path == "" || len(request.Path) > 2048 || strings.ContainsRune(request.Path, 0) {
		http.Error(w, "remote file path required", http.StatusBadRequest)
		return
	}
	id, err := randomGUISecret()
	if err != nil {
		http.Error(w, "cannot start download", http.StatusInternalServerError)
		return
	}
	name := filepath.Base(strings.ReplaceAll(request.Path, "\\", "/"))
	if name == "." || name == ".." || name == "/" || name == "" {
		http.Error(w, "select a regular file", http.StatusBadRequest)
		return
	}
	g.streamGUITransfer(w, r, "download", filepath.Join(g.transferDir, "download-"+id), request.Path, name)
}

func (g *guiServer) streamGUITransfer(w http.ResponseWriter, r *http.Request, operation, localPath, remotePath, downloadName string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "transfer attribution unavailable", http.StatusInternalServerError)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	var size int64
	if operation == "upload" {
		if info, statErr := os.Stat(localPath); statErr == nil {
			size = info.Size()
		}
	}
	metadata := map[string]any{"agent_id": r.PathValue("id"), "operation": operation, "remote_path": remotePath, "total": size}
	created, err := g.client.call(ctx, http.MethodPost, "/v1/transfers", metadata)
	if err != nil {
		guiJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var record control.TransferRecord
	if json.Unmarshal(created, &record) != nil || record.ID == "" {
		http.Error(w, "invalid transfer record", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	encode := json.NewEncoder(w)
	emit := func(kind string, value any) {
		_ = encode.Encode(map[string]any{"kind": kind, "data": value})
		flusher.Flush()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lastUpdate := time.Time{}
	lastBytes, lastTotal := int64(0), size
	update := func(state string, bytes, total int64, hash, reason string) error {
		updateCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, err := g.client.call(control.WithActionClaims(updateCtx, claims), http.MethodPut, "/v1/transfers/"+record.ID, map[string]any{"state": state, "bytes": bytes, "total": total, "sha256": hash, "error": reason})
		return err
	}
	emit("started", map[string]string{"id": record.ID, "state": record.State})
	progress := func(p pivot.TransferProgress) {
		if p.Total > maxGUITransferSize || p.Bytes > maxGUITransferSize {
			cancel()
			return
		}
		lastBytes, lastTotal = p.Bytes, p.Total
		if time.Since(lastUpdate) >= time.Second {
			_ = update("running", p.Bytes, p.Total, "", "")
			lastUpdate = time.Now()
		}
		emit("progress", p)
	}
	result, err := g.client.transferProgress(ctx, mustGUIJSON(clientFileRequest{AgentID: r.PathValue("id"), Operation: operation, LocalPath: localPath, RemotePath: remotePath}), progress)
	if err != nil {
		if operation == "download" {
			_ = os.Remove(localPath)
		}
		if errors.Is(ctx.Err(), context.Canceled) && r.Context().Err() == nil {
			err = errors.New("transfer exceeds 512 MiB limit")
		}
		state := "failed"
		if r.Context().Err() != nil {
			state = "cancelled"
		}
		_ = update(state, lastBytes, lastTotal, "", err.Error())
		emit("error", err.Error())
		return
	}
	if err := update("completed", result.Size, result.Size, result.SHA256, ""); err != nil {
		if operation == "download" {
			_ = os.Remove(localPath)
		}
		emit("error", "transfer verified, but server history could not be finalized: "+err.Error())
		return
	}
	if operation == "download" {
		id := strings.TrimPrefix(filepath.Base(localPath), "download-")
		g.transferMu.Lock()
		for key, old := range g.downloads {
			if time.Now().After(old.expires) {
				_ = os.Remove(old.path)
				delete(g.downloads, key)
			}
		}
		g.downloads[id] = guiDownload{path: localPath, name: downloadName, expires: time.Now().Add(10 * time.Minute)}
		g.transferMu.Unlock()
		emit("complete", map[string]any{"size": result.Size, "sha256": result.SHA256, "download": "/api/transfers/" + id + "/download"})
		return
	}
	emit("complete", map[string]any{"size": result.Size, "sha256": result.SHA256})
}

func (g *guiServer) serveTransferDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.transferMu.Lock()
	item, ok := g.downloads[id]
	if ok && time.Now().After(item.expires) {
		_ = os.Remove(item.path)
		delete(g.downloads, id)
		ok = false
	}
	g.transferMu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(item.path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconvQuoteFilename(item.name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, item.name, info.ModTime(), file)
}

func strconvQuoteFilename(name string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range name {
		if c >= 32 && c < 127 && c != '"' && c != '\\' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteByte('"')
	return b.String()
}
