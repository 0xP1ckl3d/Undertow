package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

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
