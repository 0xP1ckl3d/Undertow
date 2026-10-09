//go:build linux || windows

package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/control"
)

func TestAgentGUISleepCommandUsesAgentPolicyAPI(t *testing.T) {
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var saved control.SleepPolicy
	client := &liveClientConsole{request: func(_ context.Context, method, path string, body any) ([]byte, error) {
		switch method + " " + path {
		case "GET /v1/agents/agent-a":
			return []byte(`{"id":"agent-a","sleep_supported":true,"sleep":{"interval_seconds":3,"jitter_percent":10}}`), nil
		case "PUT /v1/agents/agent-a/sleep":
			saved = body.(control.SleepPolicy)
			return []byte(`{"interval_seconds":7,"jitter_percent":20}`), nil
		}
		return nil, errors.New("unexpected request")
	}}
	gui := &guiServer{client: client, store: store}
	shown, err := gui.runAgentGUICommand(context.Background(), "agent-a", "agent sleep")
	if err != nil || !strings.Contains(shown.Output, "3 seconds, 10%") {
		t.Fatalf("show: %+v %v", shown, err)
	}
	if _, err := gui.runAgentGUICommand(context.Background(), "agent-a", "agent sleep 7 20"); err != nil || saved != (control.SleepPolicy{IntervalSeconds: 7, JitterPercent: 20}) {
		t.Fatalf("save: %+v %v", saved, err)
	}
}

func TestAgentGUIConsoleRequiresExplicitAgentCommand(t *testing.T) {
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var calls []string
	client := &liveClientConsole{request: func(ctx context.Context, method, path string, body any) ([]byte, error) {
		calls = append(calls, method+" "+path)
		if method == http.MethodGet && path == "/v1/status" {
			return []byte(`{"agents":[{"id":"agent-a","hostname":"lab-win","os":"windows","arch":"amd64"}]}`), nil
		}
		if method == http.MethodPost && path == "/v1/agents/agent-a/exec" {
			return []byte(`{"stdout":"agent-a\n","stderr":"","exit_code":0}`), nil
		}
		return nil, errors.New("unexpected request")
	}}
	gui := &guiServer{client: client, store: store}
	for _, line := range []string{"help", "shell", "shell --token-context process"} {
		result, err := gui.runAgentGUICommand(context.Background(), "agent-a", line)
		if err != nil {
			t.Fatal(err)
		}
		if line == "shell" && !result.OpenShell {
			t.Fatal("shell did not require explicit shell panel")
		}
		if line == "shell --token-context process" && (!result.OpenShell || result.ShellTokenContextID != "process") {
			t.Fatal("shell context selection lost")
		}
	}
	for _, line := range []string{"quit", "exit", "back", "use agent-b", "status", "payload"} {
		if _, err := gui.runAgentGUICommand(context.Background(), "agent-a", line); err == nil {
			t.Fatalf("top-level command %q accepted", line)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("passive console opened agent operation: %v", calls)
	}
	result, err := gui.runAgentGUICommand(context.Background(), "agent-a", "whoami")
	if err != nil || result.Output != "agent-a\n" {
		t.Fatalf("agent command: %+v %v", result, err)
	}
	if len(calls) != 1 || calls[0] != "POST /v1/agents/agent-a/exec" {
		t.Fatalf("command target: %v", calls)
	}
	shown, err := gui.runAgentGUICommand(context.Background(), "agent-a", "show")
	if err != nil || !strings.Contains(shown.Output, "lab-win") || calls[len(calls)-1] != "GET /v1/status" {
		t.Fatalf("show via structured status: %+v %v %v", shown, err, calls)
	}
}

func TestAgentGUIConsoleScreenshotRequiresExplicitCommand(t *testing.T) {
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var calls []string
	client := &liveClientConsole{request: func(_ context.Context, method, path string, body any) ([]byte, error) {
		calls = append(calls, method+" "+path)
		switch method + " " + path {
		case "POST /v1/agents/agent-a/exec":
			return []byte(`{"screens":[{"number":1,"name":"primary","width":1920,"height":1080}]}`), nil
		case "POST /v1/agents/agent-a/screenshots":
			return []byte(`{"id":"0123456789abcdef0123456789abcdef","agent_id":"agent-a","screen":1,"size":123,"sha256":"hash"}`), nil
		}
		return nil, errors.New("unexpected request")
	}}
	gui := &guiServer{client: client, store: store}
	if _, err := gui.runAgentGUICommand(context.Background(), "agent-a", "help"); err != nil || len(calls) != 0 {
		t.Fatalf("help contacted agent: %v %v", calls, err)
	}
	listed, err := gui.runAgentGUICommand(context.Background(), "agent-a", "screens")
	if err != nil || !strings.Contains(listed.Output, "primary") || len(calls) != 1 {
		t.Fatalf("list screens: %+v %v %v", listed, err, calls)
	}
	shot, err := gui.runAgentGUICommand(context.Background(), "agent-a", "screenshot 1")
	if err != nil || !strings.Contains(shot.Output, "server history") || len(calls) != 2 || calls[1] != "POST /v1/agents/agent-a/screenshots" {
		t.Fatalf("capture: %+v %v %v", shot, err, calls)
	}
}

func TestAgentGUIConsoleKillsByStableAgentID(t *testing.T) {
	var path string
	client := &liveClientConsole{request: func(_ context.Context, method, target string, _ any) ([]byte, error) {
		if method != http.MethodPost {
			return nil, errors.New("unexpected method")
		}
		path = target
		return nil, nil
	}}
	gui := &guiServer{client: client}
	if _, err := gui.runAgentGUICommand(context.Background(), "agent-a", "session kill"); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/sessions/agent-a/kill" {
		t.Fatalf("session kill targeted %q, want stable agent ID", path)
	}
}
