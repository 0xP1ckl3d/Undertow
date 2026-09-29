package pivot

import (
	"context"
	"encoding/json"
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
	if caps.Exec || caps.Upload || !caps.Pivot || !caps.HostOps || !caps.Scripts || !caps.Download || !caps.Listeners {
		t.Fatalf("capabilities not independent: %+v", caps)
	}
	report := caps.Report()
	if !reflect.DeepEqual(report.Supported, []string{"pivot", "exec", "hostops", "interactive", "scripts", "upload", "download", "listeners"}) || !reflect.DeepEqual(report.Allowed, []string{"pivot", "hostops", "interactive", "scripts", "download", "listeners"}) {
		t.Fatalf("report=%+v", report)
	}
	if _, err := ParseDenied("shell"); err == nil || !strings.Contains(err.Error(), "unknown agent capability") {
		t.Fatalf("unsupported capability was accepted: %v", err)
	}
	deniedListeners, err := ParseDenied("listeners")
	if err != nil || deniedListeners.Listeners || !deniedListeners.Exec {
		t.Fatalf("listeners capability not independent: %+v, %v", deniedListeners, err)
	}
	deniedHostOps, err := ParseDenied("hostops")
	if err != nil || deniedHostOps.HostOps || !deniedHostOps.Exec {
		t.Fatalf("hostops capability not independent: %+v, %v", deniedHostOps, err)
	}
	deniedScripts, err := ParseDenied("scripts")
	if err != nil || deniedScripts.Scripts || !deniedScripts.Exec || !deniedScripts.Interactive {
		t.Fatalf("scripts capability not independent: %+v, %v", deniedScripts, err)
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
	if result, err := ExecuteRequest(ctx, server, ExecRequest{Builtin: "pwd"}); err != nil || result.Error != "" || strings.TrimSpace(result.Stdout) == "" {
		t.Fatalf("hostops should work with exec denied: result=%+v err=%v", result, err)
	}
	stream, err := server.Open(ctx, HostOpsDestination)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(stream).Encode(ExecRequest{Argv: []string{"unused"}}); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var bypass ExecResult
	if err := json.NewDecoder(stream).Decode(&bypass); err != nil || !strings.Contains(bypass.Error, "does not match stream capability") {
		t.Fatalf("exec bypass through hostops stream: %+v err=%v", bypass, err)
	}
	stream.Close()
	if _, err := server.Open(ctx, "127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "pivot is disabled") {
		t.Fatalf("denied pivot result: %v", err)
	}
}

func TestAgentCanDenyHostOpsWithoutDenyingExec(t *testing.T) {
	t.Setenv("UNDERTOW_EXEC_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	caps := DefaultCapabilities()
	caps.HostOps = false
	go ServeAgentWithCapabilities(ctx, agent, caps)
	if _, err := ExecuteRequest(ctx, server, ExecRequest{Builtin: "pwd"}); err == nil || !strings.Contains(err.Error(), "host operations are disabled") {
		t.Fatalf("denied host operation result: %v", err)
	}
	stream, err := server.Open(ctx, ExecDestination)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(stream).Encode(ExecRequest{Builtin: "pwd"}); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var bypass ExecResult
	if err := json.NewDecoder(stream).Decode(&bypass); err != nil || !strings.Contains(bypass.Error, "does not match stream capability") {
		t.Fatalf("built-in bypass through exec stream: %+v err=%v", bypass, err)
	}
	stream.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := Execute(ctx, server, []string{exe, "-test.run=TestExecHelperProcess"})
	if err != nil || result.Error != "" || result.Stdout != "executed without a shell" {
		t.Fatalf("exec should still work: result=%+v err=%v", result, err)
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
