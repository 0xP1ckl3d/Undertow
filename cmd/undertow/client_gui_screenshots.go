//go:build linux || windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"undertow/internal/control"
)

// The browser gets a verified PNG from its local client. The server control
// connection and screenshot store remain inaccessible to browser JavaScript.
func (g *guiServer) screenshotImage(w http.ResponseWriter, r *http.Request) {
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	id := r.PathValue("id")
	data, err := g.client.call(ctx, http.MethodGet, "/v1/screenshots/"+url.PathEscape(id), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var entry control.ScreenshotInfo
	if err := json.Unmarshal(data, &entry); err != nil || entry.ID != id || entry.Size < 8 || entry.Size > 64<<20 || len(entry.SHA256) != 64 {
		http.Error(w, "invalid screenshot metadata", http.StatusBadGateway)
		return
	}
	expected, err := hex.DecodeString(entry.SHA256)
	if err != nil || len(expected) != sha256.Size {
		http.Error(w, "invalid screenshot hash", http.StatusBadGateway)
		return
	}
	file, err := os.CreateTemp("", "undertow-gui-screenshot-*.png")
	if err != nil {
		http.Error(w, "image workspace unavailable", http.StatusInternalServerError)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	hash := sha256.New()
	for offset := int64(0); offset < entry.Size; {
		chunk, err := g.client.call(ctx, http.MethodGet, fmt.Sprintf("/v1/screenshots/%s/chunk?offset=%d", url.PathEscape(id), offset), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		remaining := entry.Size - offset
		if remaining > 256<<10 {
			remaining = 256 << 10
		}
		if int64(len(chunk)) != remaining {
			http.Error(w, "incomplete screenshot chunk", http.StatusBadGateway)
			return
		}
		if _, err := file.Write(chunk); err != nil {
			http.Error(w, "image workspace unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = hash.Write(chunk)
		offset += int64(len(chunk))
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), entry.SHA256) {
		http.Error(w, "screenshot hash mismatch", http.StatusBadGateway)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "image workspace unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	filename := "undertow-screen-" + strconv.Itoa(entry.Screen) + "-" + entry.ID + ".png"
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
	_, _ = io.Copy(w, file)
}
