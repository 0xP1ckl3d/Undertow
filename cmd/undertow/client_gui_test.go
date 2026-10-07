//go:build linux || windows

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"undertow/internal/control"
)

func TestGUITerminalStartKeepsSelectedProcessAndArguments(t *testing.T) {
	for _, test := range []struct {
		name string
		json string
		want []string
	}{
		{name: "default", json: `{"type":"start","argv":[]}`},
		{name: "PowerShell", json: `{"type":"start","argv":["pwsh.exe","-NoProfile"]}`, want: []string{"pwsh.exe", "-NoProfile"}},
		{name: "custom path", json: `{"type":"start","argv":["C:\\Program Files\\Custom Shell\\shell.exe","argument with spaces"]}`, want: []string{`C:\Program Files\Custom Shell\shell.exe`, "argument with spaces"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := parseGUITerminalStart(websocket.MessageText, []byte(test.json))
			if err != nil || len(request.Argv) != len(test.want) || request.Cols != 100 || request.Rows != 30 {
				t.Fatalf("request=%+v err=%v", request, err)
			}
			for i, arg := range test.want {
				if request.Argv[i] != arg {
					t.Fatalf("argv=%q want=%q", request.Argv, test.want)
				}
			}
		})
	}
	for _, data := range []string{`{"type":"input","data":"id"}`, `{"type":"start","argv":{}}`, strings.Repeat("x", 8193)} {
		if _, err := parseGUITerminalStart(websocket.MessageText, []byte(data)); err == nil {
			t.Fatalf("accepted invalid start %q", data[:min(len(data), 60)])
		}
	}
}

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

func TestGUIKeepsRemoteAPIStatusAndMessage(t *testing.T) {
	client := &liveClientConsole{request: func(context.Context, string, string, any) ([]byte, error) {
		return nil, &control.RemoteAPIError{Status: http.StatusConflict, Body: `{"error":"source and artifact versions do not match"}`}
	}}
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	gui := &guiServer{client: client, store: store, host: "127.0.0.1:9000", sessionSecret: "session", csrfSecret: "csrf"}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/deployments", strings.NewReader(`{}`))
	request.Host = "127.0.0.1:9000"
	request.Header.Set("Origin", "http://127.0.0.1:9000")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Undertow-CSRF", "csrf")
	request.AddCookie(&http.Cookie{Name: "undertow_gui", Value: "session"})
	response := httptest.NewRecorder()
	gui.handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "source and artifact versions do not match") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
