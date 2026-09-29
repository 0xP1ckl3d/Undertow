package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"undertow/internal/control"
	"undertow/internal/pivot"
)

func TestConsoleSplitsQuotedExecutableArguments(t *testing.T) {
	args, err := splitConsoleCommand(`exec agent-id "/opt/program with spaces" 'one argument' plain`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "agent-id", "/opt/program with spaces", "one argument", "plain"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestConsoleHistoryAndTabCompletion(t *testing.T) {
	var output bytes.Buffer
	editor := newConsoleEditor(&output, true)
	editor.showPrompt("undertow> ", false)
	lines := make(chan string, 2)
	err := editor.read(context.Background(), strings.NewReader("sta\t\n\x1b[A\n"), lines)
	if err != io.EOF {
		t.Fatalf("read = %v", err)
	}
	if first, second := <-lines, <-lines; first != "status" || second != "status" {
		t.Fatalf("history/completion: %q, %q", first, second)
	}
}

func TestInteractiveCtrlCNeedsConfirmation(t *testing.T) {
	var output bytes.Buffer
	editor := newConsoleEditor(&output, true)
	editor.showPrompt("undertow> ", false)
	lines := make(chan string, 1)
	if err := editor.read(context.Background(), strings.NewReader("stat\x03nus\n"), lines); err != io.EOF {
		t.Fatalf("declined Ctrl+C = %v", err)
	}
	if line := <-lines; line != "status" {
		t.Fatalf("declined Ctrl+C lost command: %q", line)
	}
	if !strings.Contains(output.String(), "Stop VPN and remove its routes? [y/N]") {
		t.Fatal("confirmation prompt missing")
	}
	lines = make(chan string, 1)
	if err := editor.read(context.Background(), strings.NewReader("\x03y"), lines); err != nil {
		t.Fatalf("confirmed Ctrl+C = %v", err)
	}
	if line := <-lines; line != "quit" {
		t.Fatalf("confirmed Ctrl+C = %q", line)
	}
}

func TestBackgroundCommandDetachesWithoutStoppingVPN(t *testing.T) {
	stopped := false
	caller := func(context.Context, string, string, any) ([]byte, error) {
		return []byte(`{"agents":[]}`), nil
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("background\n"), &output, caller, func() uint64 { return 9 }, func() { stopped = true }, nil, nil); err != nil {
		t.Fatal(err)
	}
	if stopped || !strings.Contains(output.String(), "VPN continues") {
		t.Fatalf("background stopped VPN or failed to report detach: %q", output.String())
	}
}

func TestInteractiveServerAgentContext(t *testing.T) {
	var calls []string
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		calls = append(calls, method+" "+path)
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host", VirtualIP: "172.16.254.2"}}})
		}
		if strings.HasSuffix(path, "/exec") {
			return json.Marshal(pivot.ExecResult{Stdout: "1000\n"})
		}
		return nil, nil
	}
	input := strings.NewReader("agents\nuse 1\nhelp\nexec /usr/bin/id -u\nroute add 10.10.0.0/16\nback\nquit\n")
	var output bytes.Buffer
	if err := runConsole(context.Background(), input, &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pivot-host", "undertow[pivot-host]>", "exec PROGRAM", "1000", "Route added."} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %q", want, output.String())
		}
	}
	for _, want := range []string{"POST /v1/agents/agent-long-id/exec", "POST /v1/routes"} {
		if !containsCall(calls, want) {
			t.Fatalf("missing call %q in %v", want, calls)
		}
	}
}

func TestInteractiveClientAgentContext(t *testing.T) {
	var routeArgs []string
	caller := func(_ context.Context, _, path string, _ any) ([]byte, error) {
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host"}}})
		}
		return nil, nil
	}
	route := func(_ context.Context, args []string, _ io.Writer) error {
		routeArgs = append([]string(nil), args...)
		return nil
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nroute add 10.10.0.0/16\nquit\n"), &output, caller, func() uint64 { return 704 }, nil, route, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(routeArgs, []string{"route", "add", "10.10.0.0/16", "agent-long-id"}) {
		t.Fatalf("route args=%q", routeArgs)
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func TestConsoleExecSendsArgvWithoutShell(t *testing.T) {
	var request pivot.ExecRequest
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		if method != http.MethodPost || path != "/v1/agents/agent-id/exec" {
			t.Fatalf("request = %s %s", method, path)
		}
		encoded, _ := json.Marshal(body)
		if err := json.Unmarshal(encoded, &request); err != nil {
			t.Fatal(err)
		}
		return json.Marshal(pivot.ExecResult{Stdout: "done\n", ExitCode: 0})
	}
	var output bytes.Buffer
	if err := runConsoleCommand(context.Background(), &output, caller, false, 0, nil, []string{"exec", "agent-id", "/usr/bin/id", "-u"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Argv, []string{"/usr/bin/id", "-u"}) || output.String() != "done\n[exit 0]\n" {
		t.Fatalf("request=%+v output=%q", request, output.String())
	}
}

func TestVPNConsoleCannotChangeServerRoutes(t *testing.T) {
	called := false
	caller := func(context.Context, string, string, any) ([]byte, error) { called = true; return nil, nil }
	for _, args := range [][]string{{"select", "agent-id"}} {
		if err := runConsoleCommand(context.Background(), &bytes.Buffer{}, caller, true, 704, nil, args); err == nil {
			t.Fatalf("VPN client accepted %q", args)
		}
	}
	if called {
		t.Fatal("VPN client sent an operator request")
	}
}

func TestInteractiveClientUploadUsesSelectedAgentAndAbsoluteLocalPath(t *testing.T) {
	var request clientFileRequest
	caller := func(_ context.Context, _, path string, body any) ([]byte, error) {
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-long-id", Hostname: "pivot-host"}}})
		}
		if path != "/v1/file/transfer" {
			t.Fatalf("unexpected path %s", path)
		}
		request = body.(clientFileRequest)
		return json.Marshal(pivot.FileMessage{OK: true, Size: 5, SHA256: "digest"})
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nupload ./source.bin /tmp/remote.bin\nquit\n"), &output, caller, func() uint64 { return 704 }, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if request.AgentID != "agent-long-id" || request.Operation != "upload" || !filepath.IsAbs(request.LocalPath) || request.RemotePath != "/tmp/remote.bin" {
		t.Fatalf("wrong transfer request: %+v", request)
	}
	if !strings.Contains(output.String(), "Upload complete: 5 bytes") {
		t.Fatalf("missing transfer result: %q", output.String())
	}
}
