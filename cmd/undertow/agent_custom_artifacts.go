package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"undertow/internal/agentprofile"
)

const customArtifactChunkSize = 1 << 20

type customArtifactUpload struct {
	ID             string
	Label          string
	Platform       string
	Architecture   string
	Filename       string
	Size           int64
	SHA256         string
	ServiceCapable bool
	Received       int64
	Created        time.Time
	Path           string
	File           *os.File
}

type customArtifactUploadRequest struct {
	Label          string `json:"label"`
	Platform       string `json:"platform"`
	Architecture   string `json:"architecture"`
	Filename       string `json:"filename"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	ServiceCapable bool   `json:"service_capable,omitempty"`
}

func decodeCustomArtifactRequest(r *http.Request, limit int64, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected request data")
	}
	return nil
}

func (d *agentDistribution) beginCustomArtifactUpload(w http.ResponseWriter, r *http.Request) {
	var request customArtifactUploadRequest
	if err := decodeCustomArtifactRequest(r, 4096, &request); err != nil {
		distributionError(w, errors.New("invalid custom artifact upload request"))
		return
	}
	request.Label = strings.TrimSpace(request.Label)
	request.SHA256 = strings.ToLower(strings.TrimSpace(request.SHA256))
	// Store validation is repeated at completion. These checks reject invalid
	// metadata before the server accepts a potentially large upload.
	if request.Label == "" || len(request.Label) > 80 || strings.ContainsAny(request.Label, "\r\n\x00") || request.Filename == "" || request.Size <= 0 || request.Size > agentprofile.MaxCustomArtifactSize || len(request.SHA256) != 64 {
		distributionError(w, errors.New("invalid custom artifact metadata"))
		return
	}
	id, err := agentprofile.ID()
	if err != nil {
		distributionError(w, err)
		return
	}
	file, err := os.CreateTemp("", "undertow-custom-artifact-*")
	if err != nil {
		distributionError(w, err)
		return
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		os.Remove(file.Name())
		distributionError(w, err)
		return
	}
	upload := &customArtifactUpload{ID: id, Label: request.Label, Platform: request.Platform, Architecture: request.Architecture, Filename: request.Filename, Size: request.Size, SHA256: request.SHA256, ServiceCapable: request.ServiceCapable, Created: time.Now().UTC(), Path: file.Name(), File: file}
	d.uploadsMu.Lock()
	if d.customUploads == nil {
		d.customUploads = make(map[string]*customArtifactUpload)
	}
	for existingID, existing := range d.customUploads {
		if time.Since(existing.Created) > time.Hour {
			existing.File.Close()
			os.Remove(existing.Path)
			delete(d.customUploads, existingID)
		}
	}
	d.customUploads[id] = upload
	d.uploadsMu.Unlock()
	distributionJSON(w, http.StatusCreated, map[string]any{"id": id, "chunk_size": customArtifactChunkSize})
}

func (d *agentDistribution) appendCustomArtifactUpload(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		Offset int64  `json:"offset"`
		Data   []byte `json:"data"`
	}
	if err := decodeCustomArtifactRequest(r, 2<<20, &request); err != nil || len(request.Data) == 0 || len(request.Data) > customArtifactChunkSize {
		distributionError(w, errors.New("invalid custom artifact chunk"))
		return
	}
	d.uploadsMu.Lock()
	defer d.uploadsMu.Unlock()
	upload := d.customUploads[id]
	if upload == nil {
		http.NotFound(w, r)
		return
	}
	if request.Offset != upload.Received || upload.Received+int64(len(request.Data)) > upload.Size {
		distributionError(w, errors.New("custom artifact chunk offset or size is invalid"))
		return
	}
	written, err := upload.File.Write(request.Data)
	if err != nil || written != len(request.Data) {
		distributionError(w, errors.New("could not store custom artifact chunk"))
		return
	}
	upload.Received += int64(written)
	distributionJSON(w, http.StatusOK, map[string]int64{"received": upload.Received})
}

func (d *agentDistribution) finishCustomArtifactUpload(w http.ResponseWriter, r *http.Request, id string) {
	d.uploadsMu.Lock()
	upload := d.customUploads[id]
	if upload != nil {
		delete(d.customUploads, id)
	}
	d.uploadsMu.Unlock()
	if upload == nil {
		http.NotFound(w, r)
		return
	}
	defer os.Remove(upload.Path)
	if upload.Received != upload.Size {
		upload.File.Close()
		distributionError(w, errors.New("custom artifact upload is incomplete"))
		return
	}
	if err := upload.File.Sync(); err != nil {
		upload.File.Close()
		distributionError(w, err)
		return
	}
	if err := upload.File.Close(); err != nil {
		distributionError(w, err)
		return
	}
	file, err := os.Open(upload.Path)
	if err != nil {
		distributionError(w, err)
		return
	}
	defer file.Close()
	artifact, err := d.store.ImportCustom(upload.Label, upload.Platform, upload.Architecture, upload.Filename, upload.Size, upload.SHA256, upload.ServiceCapable, file)
	if err != nil {
		distributionError(w, err)
		return
	}
	distributionJSON(w, http.StatusCreated, d.artifactInfo(artifact))
}

func (d *agentDistribution) cancelCustomArtifactUpload(w http.ResponseWriter, r *http.Request, id string) {
	d.uploadsMu.Lock()
	upload := d.customUploads[id]
	if upload != nil {
		delete(d.customUploads, id)
	}
	d.uploadsMu.Unlock()
	if upload == nil {
		http.NotFound(w, r)
		return
	}
	upload.File.Close()
	os.Remove(upload.Path)
	w.WriteHeader(http.StatusNoContent)
}

func (d *agentDistribution) handleCustomArtifactUpload(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/v1/agent-artifact-uploads" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		d.beginCustomArtifactUpload(w, r)
		return
	}
	rest := strings.TrimPrefix(path, "/v1/agent-artifact-uploads/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && parts[0] != "" {
		switch r.Method {
		case http.MethodPut:
			d.appendCustomArtifactUpload(w, r, parts[0])
		case http.MethodDelete:
			d.cancelCustomArtifactUpload(w, r, parts[0])
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "complete" && r.Method == http.MethodPost {
		d.finishCustomArtifactUpload(w, r, parts[0])
		return
	}
	http.NotFound(w, r)
}
