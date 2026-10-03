package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBackgroundState(t *testing.T, path string, pid int) {
	t.Helper()
	data, err := json.Marshal(backgroundState{PID: pid, Address: "127.0.0.1:1", Token: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDeadBackgroundStateIsRemovedForStartAttachAndStop(t *testing.T) {
	deadPID := 1 << 29
	if alive, err := backgroundProcessAlive(deadPID); err != nil || alive {
		t.Skipf("test PID unexpectedly alive or cannot be checked: alive=%t err=%v", alive, err)
	}
	path := filepath.Join(t.TempDir(), "undertow-client.pid")
	writeBackgroundState(t, path, deadPID)
	removed, err := removeDeadBackgroundState(path)
	if err != nil || !removed {
		t.Fatalf("start cleanup: removed=%t err=%v", removed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state remains: %v", err)
	}
	writeBackgroundState(t, path, deadPID)
	if err := attachClientAt(path); err == nil || !strings.Contains(err.Error(), "no longer running") {
		t.Fatalf("attach error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("attach left state: %v", err)
	}
	writeBackgroundState(t, path, deadPID)
	if err := stopBackground(path); err != nil {
		t.Fatalf("stop stale state: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stop left state: %v", err)
	}
}

func TestLiveBackgroundStateIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undertow-client.pid")
	writeBackgroundState(t, path, os.Getpid())
	removed, err := removeDeadBackgroundState(path)
	if err != nil || removed {
		t.Fatalf("live state: removed=%t err=%v", removed, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live state removed: %v", err)
	}
}
