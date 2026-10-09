//go:build linux || windows

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"undertow/internal/control"
)

func (g *guiServer) archivedAgentsBulk(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid archived-agent request", http.StatusBadRequest)
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	response, err := g.client.call(control.WithActionClaims(r.Context(), claims), http.MethodPost, "/v1/agents/archive/bulk", request)
	if err != nil {
		var remoteErr *control.RemoteAPIError
		if errors.As(err, &remoteErr) && remoteErr.Status >= 400 && remoteErr.Status <= 599 {
			guiJSON(w, remoteErr.Status, map[string]string{"error": remoteAPIErrorMessage(remoteErr.Body)})
			return
		}
		guiJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if request.Action == "delete" {
		if err := g.store.DeleteAgentRecords(request.IDs); err != nil {
			guiJSON(w, http.StatusInternalServerError, map[string]string{"error": "Server records were deleted, but this client's console cache could not be cleared: " + err.Error()})
			return
		}
	}
	if !json.Valid(response) {
		guiJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid server response"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(response)
}
