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

func TestGUIModuleBankPreloadsCommandsWithoutAgentActivity(t *testing.T) {
	t.Setenv("UNDERTOW_MODULES_DIR", filepath.Join("..", "..", "modules"))
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bank, err := openGUIModuleBank(filepath.Join(t.TempDir(), "ui.db"), store)
	if err != nil {
		t.Fatal(err)
	}
	modules := bank.list()
	if len(modules) < 40 {
		t.Fatalf("expected packaged module bank, got %d commands; preload errors: %v", len(modules), bank.preloadErrors)
	}
	if bank.preloadErrors == nil {
		t.Fatal("empty preload issues must serialize as an array")
	}
	var calls int
	client := &liveClientConsole{request: func(context.Context, string, string, any) ([]byte, error) { calls++; return nil, nil }}
	gui := &guiServer{client: client, store: store, modules: bank}
	for _, line := range []string{"help", "modules", "bofs", "help " + modules[0].Name} {
		result, err := gui.runAgentGUICommand(context.Background(), "agent-a", line)
		if err != nil || result.Output == "" {
			t.Fatalf("%q: %+v %v", line, result, err)
		}
	}
	result, err := gui.runAgentGUICommand(context.Background(), "agent-a", modules[0].Name)
	if err != nil || result.ModuleRun == nil || result.ModuleRun.Name != modules[0].Name {
		t.Fatalf("explicit foreground command: %+v %v", result, err)
	}
	if calls != 0 {
		t.Fatalf("passive catalog or foreground dispatch contacted agent %d times", calls)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/agent-a/command-stream", strings.NewReader(`{"line":"modules"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "agent-a")
	rec := httptest.NewRecorder()
	gui.agentCommandStream(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), modules[0].Name) {
		t.Fatalf("streamed catalog: %d %s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("catalog stream contacted agent %d times", calls)
	}
}
