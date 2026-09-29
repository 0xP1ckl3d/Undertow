package pivot

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseDeniedCapabilities(t *testing.T) {
	caps, err := ParseDenied("exec, upload")
	if err != nil {
		t.Fatal(err)
	}
	if caps.Exec || caps.Upload || !caps.Pivot || !caps.Download || !caps.Listeners {
		t.Fatalf("capabilities not independent: %+v", caps)
	}
	report := caps.Report()
	if !reflect.DeepEqual(report.Supported, []string{"pivot", "exec", "upload", "download", "listeners"}) || !reflect.DeepEqual(report.Allowed, []string{"pivot", "download", "listeners"}) {
		t.Fatalf("report=%+v", report)
	}
	if _, err := ParseDenied("shell"); err == nil || !strings.Contains(err.Error(), "unknown agent capability") {
		t.Fatalf("unsupported capability was accepted: %v", err)
	}
	deniedListeners, err := ParseDenied("listeners")
	if err != nil || deniedListeners.Listeners || !deniedListeners.Exec {
		t.Fatalf("listeners capability not independent: %+v, %v", deniedListeners, err)
	}
}

func TestAgentCapabilitiesAreEnforcedIndependently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	caps := DefaultCapabilities()
	caps.Pivot = false
	caps.Exec = false
	caps.Download = false
	go ServeAgentWithCapabilities(ctx, agent, caps)
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("upload allowed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := TransferFile(ctx, server, "agent-id", "upload", source, destination); err != nil {
		t.Fatalf("allowed upload failed: %v", err)
	}
	if _, err := TransferFile(ctx, server, "agent-id", "download", filepath.Join(dir, "copy"), destination); err == nil || !strings.Contains(err.Error(), "download is disabled") {
		t.Fatalf("denied download result: %v", err)
	}
	if _, err := Execute(ctx, server, []string{"unused"}); err == nil || !strings.Contains(err.Error(), "execution is disabled") {
		t.Fatalf("denied exec result: %v", err)
	}
	if _, err := server.Open(ctx, "127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "pivot is disabled") {
		t.Fatalf("denied pivot result: %v", err)
	}
}

func TestAgentCanDenyListeners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	caps := DefaultCapabilities()
	caps.Listeners = false
	go ServeAgentWithCapabilities(ctx, agent, caps)
	if _, err := server.Open(ctx, ListenerDestination); err == nil || !strings.Contains(err.Error(), "listeners are disabled") {
		t.Fatalf("denied listener result: %v", err)
	}
}
