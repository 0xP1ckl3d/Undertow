//go:build linux || windows

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGUIMutationRequiresLaunchSessionOriginAndCSRF(t *testing.T) {
	client := &liveClientConsole{request: func(context.Context, string, string, any) ([]byte, error) { return []byte(`{"ok":true}`), nil }}
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	gui := &guiServer{client: client, store: store, host: "127.0.0.1:9000", launchSecret: "launch", sessionSecret: "session", csrfSecret: "csrf"}
	handler := gui.handler()
	request := func(path, origin, csrf string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000"+path, strings.NewReader(`{"argv":["id"]}`))
		r.Host = "127.0.0.1:9000"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Undertow-CSRF", csrf)
		if cookie {
			r.AddCookie(&http.Cookie{Name: "undertow_gui", Value: "session"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request("/api/agents/agent/jobs", "http://evil.example", "csrf", true).Code; got != http.StatusForbidden {
		t.Fatalf("cross-origin status %d", got)
	}
	if got := request("/api/agents/agent/jobs", "http://127.0.0.1:9000", "", true).Code; got != http.StatusForbidden {
		t.Fatalf("missing CSRF status %d", got)
	}
	if got := request("/api/agents/agent/jobs", "http://127.0.0.1:9000", "csrf", false).Code; got != http.StatusUnauthorized {
		t.Fatalf("missing session status %d", got)
	}
	if got := request("/api/agents/agent/jobs", "http://127.0.0.1:9000", "csrf", true).Code; got != http.StatusOK {
		t.Fatalf("valid status %d", got)
	}
	badHost := httptest.NewRequest(http.MethodGet, "http://evil.example/api/status", nil)
	badHost.Host = "evil.example"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, badHost)
	if w.Code != http.StatusForbidden {
		t.Fatalf("host status %d", w.Code)
	}
}

func TestGUIRejectsRemoteBinding(t *testing.T) {
	if _, _, err := startClientGUI(context.Background(), &liveClientConsole{}, "0.0.0.0:0", filepath.Join(t.TempDir(), "ui.db")); err == nil {
		t.Fatal("non-loopback bind accepted")
	}
}
