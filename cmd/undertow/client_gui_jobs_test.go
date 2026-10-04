//go:build linux || windows

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"undertow/internal/control"
)

func TestGUIJobDownloadStreamsServerOwnedOutput(t *testing.T) {
	id := strings.Repeat("a", 32)
	var paths []string
	client := &liveClientConsole{request: func(_ context.Context, method, path string, _ any) ([]byte, error) {
		paths = append(paths, method+" "+path)
		var result any
		switch path {
		case "/v1/jobs/" + id:
			result = control.JobInfo{ID: id, OutputBytes: 6}
		case "/v1/jobs/" + id + "/output/chunk?offset=0":
			result = control.JobOutputChunk{Offset: 0, Data: []byte("abcdef"), Total: 6, EOF: true}
		default:
			t.Fatalf("unexpected server request %s", path)
		}
		return json.Marshal(result)
	}}
	gui := &guiServer{client: client}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/download", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	gui.downloadJobOutput(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "abcdef" || rec.Header().Get("Content-Length") != "6" || !strings.Contains(rec.Header().Get("Content-Disposition"), id[:12]) {
		t.Fatalf("download: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if len(paths) != 2 || paths[0] != "GET /v1/jobs/"+id || paths[1] != "GET /v1/jobs/"+id+"/output/chunk?offset=0" {
		t.Fatalf("server sources: %v", paths)
	}
	bad := &liveClientConsole{request: func(_ context.Context, _, path string, _ any) ([]byte, error) {
		if path == "/v1/jobs/"+id {
			return json.Marshal(control.JobInfo{ID: id, OutputBytes: 6})
		}
		return json.Marshal(control.JobOutputChunk{Offset: 0, Total: 6})
	}}
	failed := httptest.NewRecorder()
	(&guiServer{client: bad}).downloadJobOutput(failed, req)
	if failed.Code != http.StatusBadGateway || strings.Contains(failed.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("missing first chunk offered as download: %d %v", failed.Code, failed.Header())
	}
}
