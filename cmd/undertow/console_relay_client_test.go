package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestClientConsoleRelayAndCarrierCommands(t *testing.T) {
	const agentID = "0123456789abcdef0123456789abcdef"
	var requests []string
	call := func(_ context.Context, method, path string, _ any) ([]byte, error) {
		requests = append(requests, method+" "+path)
		switch method + " " + path {
		case "GET /v1/status":
			return []byte(`{"agents":[{"id":"` + agentID + `","hostname":"PARENT"}],"clients":[{"session_id":42,"transport":"websocket"}]}`), nil
		case "GET /v1/relays":
			return []byte(`[{"agent_id":"` + agentID + `","bind":"127.0.0.1:8443"},{"agent_id":"other","bind":"127.0.0.1:8444"}]`), nil
		case "POST /v1/agents/" + agentID + "/relays":
			return []byte(`{"agent_id":"` + agentID + `","bind":"127.0.0.1:8443"}`), nil
		case "GET /v1/transports":
			return []byte(`[{"transport":"websocket","network":"tcp","listen":"127.0.0.1:443","sessions":1}]`), nil
		case "DELETE /v1/transports/quic":
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", method, path)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"topology"}, "PARENT"},
		{[]string{"relay", agentID, "start"}, "Relay listening"},
		{[]string{"relay", agentID, "list"}, "Relay listeners (1)"},
		{[]string{"transports"}, "WEBSOCKET"},
		{[]string{"stop", "transport", "quic"}, "QUIC listener stopped"},
	} {
		var out bytes.Buffer
		if err := runConsoleCommand(context.Background(), &out, call, true, 42, nil, tc.args); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%v output = %q; want %q", tc.args, out.String(), tc.want)
		}
	}
	var out bytes.Buffer
	if err := runConsoleCommand(context.Background(), &out, call, true, 42, nil, []string{"stop", "transport", "websocket", "force"}); err == nil || !strings.Contains(err.Error(), "current carrier") {
		t.Fatalf("stopping own carrier: %v", err)
	}
	for _, request := range requests {
		if request == http.MethodDelete+" /v1/transports/websocket?force=true" {
			t.Fatal("client sent a stop request for its own carrier")
		}
	}
}
