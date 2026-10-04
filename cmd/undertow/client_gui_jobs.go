//go:build linux || windows

package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"undertow/internal/control"
)

// The server owns the retained output. This handler streams its chunk API to
// the local browser without asking the browser to assemble or cache the file.
func (g *guiServer) downloadJobOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	base := "/v1/jobs/" + url.PathEscape(id)
	data, err := g.client.call(r.Context(), http.MethodGet, base, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var job control.JobInfo
	if err := json.Unmarshal(data, &job); err != nil || job.ID != id {
		http.Error(w, "invalid server job record", http.StatusBadGateway)
		return
	}
	if job.OutputBytes == 0 {
		http.Error(w, "this job has no retained output", http.StatusNotFound)
		return
	}
	fetch := func(offset uint64) (control.JobOutputChunk, error) {
		path := fmt.Sprintf("%s/output/chunk?offset=%d", base, offset)
		chunkData, err := g.client.call(r.Context(), http.MethodGet, path, nil)
		if err != nil {
			return control.JobOutputChunk{}, err
		}
		var chunk control.JobOutputChunk
		if err := json.Unmarshal(chunkData, &chunk); err != nil {
			return control.JobOutputChunk{}, err
		}
		if chunk.Offset != offset || chunk.Total < job.OutputBytes || len(chunk.Data) == 0 {
			return control.JobOutputChunk{}, fmt.Errorf("invalid retained output chunk at %d", offset)
		}
		return chunk, nil
	}
	first, err := fetch(0)
	if err != nil {
		http.Error(w, "server job output unavailable", http.StatusBadGateway)
		return
	}
	name := "undertow-job-output.txt"
	if len(job.ID) >= 12 && len(job.ID)%2 == 0 {
		if _, err := hex.DecodeString(job.ID); err == nil {
			name = "undertow-job-" + job.ID[:12] + ".txt"
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Content-Length", fmt.Sprint(job.OutputBytes))
	w.Header().Set("Cache-Control", "no-store")
	var offset uint64
	for offset < job.OutputBytes {
		chunk := first
		if offset != 0 {
			chunk, err = fetch(offset)
			if err != nil {
				return
			}
		}
		bytes := chunk.Data
		if remaining := job.OutputBytes - offset; uint64(len(bytes)) > remaining {
			bytes = bytes[:remaining]
		}
		if _, err := w.Write(bytes); err != nil {
			return
		}
		offset += uint64(len(bytes))
	}
}
