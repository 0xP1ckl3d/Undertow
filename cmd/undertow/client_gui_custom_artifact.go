//go:build linux || windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"undertow/internal/agentprofile"
	"undertow/internal/control"
)

func guiRemoteArtifactError(w http.ResponseWriter, err error) {
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
}

func (g *guiServer) uploadCustomArtifact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, agentprofile.MaxCustomArtifactSize+(2<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "invalid or oversized artifact upload", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "artifact file required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	label := strings.TrimSpace(r.FormValue("label"))
	platform := strings.TrimSpace(r.FormValue("platform"))
	architecture := strings.TrimSpace(r.FormValue("architecture"))
	filename := filepath.Base(header.Filename)
	serviceCapable, _ := strconv.ParseBool(r.FormValue("service_capable"))
	if header.Size <= 0 || header.Size > agentprofile.MaxCustomArtifactSize {
		http.Error(w, "custom artifact must be between 1 byte and 512 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, agentprofile.MaxCustomArtifactSize+1))
	if err != nil || written != header.Size {
		http.Error(w, "could not read the complete artifact upload", http.StatusBadRequest)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "could not rewind the artifact upload", http.StatusInternalServerError)
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	metadata := customArtifactUploadRequest{Label: label, Platform: platform, Architecture: architecture, Filename: filename, Size: header.Size, SHA256: hex.EncodeToString(hash.Sum(nil)), ServiceCapable: serviceCapable}
	data, err := g.client.call(ctx, http.MethodPost, "/v1/agent-artifact-uploads", metadata)
	if err != nil {
		guiRemoteArtifactError(w, err)
		return
	}
	var started struct {
		ID        string `json:"id"`
		ChunkSize int    `json:"chunk_size"`
	}
	if json.Unmarshal(data, &started) != nil || started.ID == "" || started.ChunkSize <= 0 || started.ChunkSize > customArtifactChunkSize {
		http.Error(w, "invalid artifact upload session", http.StatusBadGateway)
		return
	}
	complete := false
	defer func() {
		if !complete {
			_, _ = g.client.call(ctx, http.MethodDelete, "/v1/agent-artifact-uploads/"+started.ID, nil)
		}
	}()
	buffer := make([]byte, started.ChunkSize)
	var offset int64
	for {
		count, readErr := file.Read(buffer)
		if count > 0 {
			chunk := struct {
				Offset int64  `json:"offset"`
				Data   []byte `json:"data"`
			}{Offset: offset, Data: buffer[:count]}
			if _, err := g.client.call(ctx, http.MethodPut, "/v1/agent-artifact-uploads/"+started.ID, chunk); err != nil {
				guiRemoteArtifactError(w, err)
				return
			}
			offset += int64(count)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			http.Error(w, "could not read artifact upload", http.StatusBadRequest)
			return
		}
	}
	data, err = g.client.call(ctx, http.MethodPost, "/v1/agent-artifact-uploads/"+started.ID+"/complete", map[string]any{})
	if err != nil {
		guiRemoteArtifactError(w, err)
		return
	}
	if !json.Valid(data) {
		http.Error(w, "invalid artifact response", http.StatusBadGateway)
		return
	}
	complete = true
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(data)
}
