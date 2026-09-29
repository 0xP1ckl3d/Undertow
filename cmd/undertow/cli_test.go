//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/control"
	"undertow/internal/pivot"
)

func TestLocalClientConsoleRPC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.pid")
	cleanup, err := startBackgroundControl(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	setBackgroundConsoleHandler(func(_ context.Context, request consoleRPCRequest) consoleRPCResponse {
		if request.Action == "session" {
			return consoleRPCResponse{SessionID: 42}
		}
		return consoleRPCResponse{Error: "unexpected action"}
	})
	defer setBackgroundConsoleHandler(nil)
	response, err := callClientConsole(path, consoleRPCRequest{Action: "session"})
	if err != nil || response.SessionID != 42 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestStatusShowsAgentAndVPNHostnames(t *testing.T) {
	report := pivot.Capabilities{Pivot: true, Exec: false, Upload: false, Download: true}.Report()
	data, err := json.Marshal(map[string]any{
		"agents":  []control.AgentInfo{{ID: "agent-one", Hostname: "agent-host", Capabilities: &report}},
		"clients": []control.ClientInfo{{SessionID: 7, Hostname: "vpn-host"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := renderStatus(&output, data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "agent-host") || !strings.Contains(output.String(), "vpn-host") {
		t.Fatalf("hostnames missing from status: %s", output.String())
	}
	if !strings.Contains(output.String(), "supported=pivot,exec,upload,download allowed=pivot,download") {
		t.Fatalf("agent capabilities missing from status: %s", output.String())
	}
}

func TestDuplicateModeArgumentIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func([]string) error
	}{
		{"server", serve}, {"client", clientCommand}, {"agent", agent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run([]string{tc.name, "--tun"})
			if err == nil || !strings.Contains(err.Error(), "unexpected "+tc.name+" argument") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestVerificationTarget(t *testing.T) {
	got, err := resolveVerificationTarget(context.Background(), "https://127.0.0.1/check")
	if err != nil || got != "127.0.0.1:443" {
		t.Fatalf("target=%q err=%v", got, err)
	}
	if _, err := resolveVerificationTarget(context.Background(), "file:///tmp/address"); err == nil {
		t.Fatal("accepted non-HTTP URL")
	}
}
