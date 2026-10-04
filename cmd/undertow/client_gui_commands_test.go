//go:build linux || windows

package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

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
	for _, line := range []string{"help", "shell"} {
		result, err := gui.runAgentGUICommand(context.Background(), "agent-a", line)
		if err != nil {
			t.Fatal(err)
		}
		if line == "shell" && !result.OpenShell {
			t.Fatal("shell did not require explicit shell panel")
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
		case "GET /v1/agents/agent-a/screens":
			return []byte(`[{"number":1,"name":"primary","width":1920,"height":1080}]`), nil
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
