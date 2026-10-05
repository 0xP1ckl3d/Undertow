//go:build linux || windows

package main

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGUIConsoleHistoryPersistsAcrossClientRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gui.db")
	store, err := openClientGUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendConsoleEntry("agent-a", "console", "command", "help"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	writer := guiCommandEventWriter{encode: json.NewEncoder(response), flush: response, store: store, agentID: "agent-a", source: "modules"}
	if err := writer.event("output", "foreground result\n"); err != nil {
		t.Fatal(err)
	}
	fileEvent := `{"name":"proof.bin","size":10,"download":"/api/transfers/abc/download"}`
	if err := writer.event("file", fileEvent); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openClientGUIStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entries, err := reopened.ConsoleEntries("agent-a")
	if err != nil || len(entries) != 3 || entries[0].Text != "help" || entries[1].Source != "modules" || entries[1].Text != "foreground result\n" || entries[2].Kind != "file" || entries[2].Text != fileEvent {
		t.Fatalf("history=%+v err=%v", entries, err)
	}
	other, err := reopened.ConsoleEntries("agent-b")
	if err != nil || len(other) != 0 {
		t.Fatalf("other agent history=%+v err=%v", other, err)
	}
}

func TestAgentGUIHelpUsesTerminalMenuAndBoundCommands(t *testing.T) {
	help := (&guiServer{}).agentGUIHelp()
	for _, want := range []string{"GUI CLIENT / AGENT COMMANDS", "◆ AGENT SESSION", "◆ HOST", "◆ MODULE BANK", "route accept CIDR", "load module|wasm|bof"} {
		if !strings.Contains(help, want) {
			t.Errorf("help missing %q", want)
		}
	}
	for _, notBound := range []string{"use NUMBER", "back ", "quit / exit", "payload build", "vpn on|off"} {
		if strings.Contains(help, notBound) {
			t.Errorf("help advertises unbound command %q", notBound)
		}
	}
}
