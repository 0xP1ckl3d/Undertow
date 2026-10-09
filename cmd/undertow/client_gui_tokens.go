//go:build linux || windows

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"undertow/internal/authcontext"
	"undertow/internal/control"
	"undertow/internal/pivot"
)

// Browser credentials enter only the local operator client. Seal them for the
// live agent before the server sees the request. This path never writes GUI
// console history, preferences, Jobs, audit bodies or transfer storage.
func (g *guiServer) manageTokens(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action string                    `json:"action"`
		ID     string                    `json:"id,omitempty"`
		Logon  *authcontext.LogonRequest `json:"logon,omitempty"`
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 4096))
	d.DisallowUnknownFields()
	if r.Header.Get("Content-Type") != "application/json" || d.Decode(&request) != nil {
		http.Error(w, "invalid authentication context request", 400)
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "operator client unavailable", 500)
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	call := func(ctx context.Context, method, path string, body any) ([]byte, error) {
		return g.client.call(ctx, method, path, body)
	}
	forward := pivot.TokenRequest{Action: request.Action, ID: request.ID}
	if request.Action == "create" {
		if request.Logon == nil {
			http.Error(w, "logon request required", 400)
			return
		}
		sealed, sealErr := sealTokenLogon(ctx, call, r.PathValue("id"), *request.Logon)
		request.Logon.Password = ""
		if sealErr != nil {
			guiJSON(w, 409, map[string]string{"error": sealErr.Error()})
			return
		}
		forward.SealedLogon = &sealed
	} else if request.Logon != nil {
		http.Error(w, "logon material is accepted only for creation", 400)
		return
	}
	data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(r.PathValue("id"))+"/tokens", forward)
	if err != nil {
		guiJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
