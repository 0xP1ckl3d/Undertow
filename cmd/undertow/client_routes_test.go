//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"undertow/internal/control"
	"undertow/internal/routing"
)

func TestInternalOnlyMirrorsActiveServerRoutes(t *testing.T) {
	device := &recordingRouteDevice{}
	c := &liveClientConsole{
		device: device, sessionID: 7, global: make(map[string]bool), active: make(map[string]bool),
		serverIP: netip.MustParseAddr("203.0.113.10"), tunnelPrefix: netip.MustParsePrefix("172.16.253.0/24"),
		localNetworks: []netip.Prefix{netip.MustParsePrefix("192.168.50.0/24")},
	}
	status, _ := json.Marshal(map[string]any{
		"clients": []map[string]any{{"session_id": 7, "internal": true}},
		"routes": []map[string]any{
			{"prefix": "10.20.0.0/16", "active": true},
			{"prefix": "10.30.0.0/16", "active": false},
			{"prefix": "192.168.50.0/24", "active": true},
			{"prefix": "203.0.113.10/32", "active": true},
		},
	})
	if err := c.applyGlobalRouteStatus(status); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(device.added, []string{"10.20.0.0/16"}) {
		t.Fatalf("unexpected internal routes: %v", device.added)
	}
	status, _ = json.Marshal(map[string]any{"clients": []map[string]any{{"session_id": 7, "internal": false}}})
	if err := c.applyGlobalRouteStatus(status); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(device.deleted, []string{"10.20.0.0/16"}) {
		t.Fatalf("stale route was not removed: %v", device.deleted)
	}
}

type recordingRouteDevice struct {
	added   []string
	deleted []string
	fail    string
}

func (d *recordingRouteDevice) AddRoute(prefix string) error {
	if prefix == d.fail {
		return errors.New("bind failed")
	}
	d.added = append(d.added, prefix)
	return nil
}

func (d *recordingRouteDevice) DelRoute(prefix string) error {
	d.deleted = append(d.deleted, prefix)
	return nil
}

func TestClientModeRoutes(t *testing.T) {
	internal := &recordingRouteDevice{}
	cleanup, err := installClientModeRoutes(internal, false)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if len(internal.added) != 0 || len(internal.deleted) != 0 {
		t.Fatalf("internal-only mode changed Internet routes: %+v", internal)
	}
	vpn := &recordingRouteDevice{}
	cleanup, err = installClientModeRoutes(vpn, true)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if !reflect.DeepEqual(vpn.added, []string{"0.0.0.0/1", "128.0.0.0/1"}) || !reflect.DeepEqual(vpn.deleted, []string{"128.0.0.0/1", "0.0.0.0/1"}) {
		t.Fatalf("VPN route lifecycle: %+v", vpn)
	}
	failing := &recordingRouteDevice{fail: "128.0.0.0/1"}
	if _, err := installClientModeRoutes(failing, true); err == nil || !reflect.DeepEqual(failing.deleted, []string{"0.0.0.0/1"}) {
		t.Fatalf("failed route did not roll back: %+v err=%v", failing, err)
	}
}

func TestClientConsoleTogglesInternetRoutes(t *testing.T) {
	device := &recordingRouteDevice{}
	mode := &clientModeRoutes{device: device}
	client := &liveClientConsole{modeRoutes: mode, verifyURL: ""}
	var output bytes.Buffer
	for _, args := range [][]string{{"vpn", "status"}, {"vpn", "on"}, {"vpn", "status"}, {"vpn", "off"}} {
		if err := client.routeCommand(context.Background(), args, &output); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	mode.close()
	if !reflect.DeepEqual(device.added, []string{"0.0.0.0/1", "128.0.0.0/1"}) || !reflect.DeepEqual(device.deleted, []string{"128.0.0.0/1", "0.0.0.0/1"}) {
		t.Fatalf("VPN toggle routes: %+v", device)
	}
	if !strings.Contains(output.String(), "Internet egress through Undertow: on") || !strings.Contains(output.String(), "Internet egress through Undertow: off") {
		t.Fatalf("VPN toggle output: %s", output.String())
	}
}

func TestClientModeChoicesSurviveDisconnect(t *testing.T) {
	client := &liveClientConsole{vpn: true, internal: true, events: make(chan string, 1)}
	client.set(nil, 0, nil, nil, "")
	if !client.vpn || !client.internal {
		t.Fatalf("mode choices changed on disconnect: vpn=%t internal=%t", client.vpn, client.internal)
	}
}

func TestClientStatusNamesTransportEgressAndActiveRouteAgents(t *testing.T) {
	client := &liveClientConsole{
		vpn: true, transport: "quic", publicIP: "13.210.247.60", sessionID: 42,
		routes: []control.AcceptedRoute{{Prefix: "10.10.10.0/24", AgentID: "agent-talon"}},
		active: map[string]bool{"10.10.10.0/24": true},
		global: map[string]bool{"192.168.20.0/24": true},
	}
	var output bytes.Buffer
	if err := client.routeCommand(context.Background(), []string{"vpn", "status"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Internet egress through Undertow: on", "VPN transport: quic", "Public egress verified: 13.210.247.60"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("VPN status missing %q: %q", want, output.String())
		}
	}
	output.Reset()
	client.printInternalRoutes(&output, []control.AgentInfo{{ID: "agent-talon", Hostname: "TALON"}, {ID: "agent-ws01", Hostname: "WS01"}}, []routing.Route{{Prefix: netip.MustParsePrefix("192.168.20.0/24"), AgentID: "agent-ws01"}})
	for _, want := range []string{"10.10.10.0/24 via TALON (agent-talon)", "192.168.20.0/24 via WS01 (agent-ws01)"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("internal routes missing %q: %q", want, output.String())
		}
	}
}

func TestClientRejectsAcceptedRouteCollidingWithLocalNetwork(t *testing.T) {
	client := &liveClientConsole{serverIP: netip.MustParseAddr("203.0.113.10"), tunnelPrefix: netip.MustParsePrefix("172.16.253.0/24"), localNetworks: []netip.Prefix{netip.MustParsePrefix("192.168.50.0/24")}}
	var output bytes.Buffer
	err := client.routeCommand(context.Background(), []string{"route", "accept", "192.168.50.0/24", "agent-a"}, &output)
	if err == nil || !strings.Contains(err.Error(), "conflicts with local network") {
		t.Fatalf("collision=%v", err)
	}
}

func TestClientRejectsRouteContainingCarrierEndpoint(t *testing.T) {
	client := &liveClientConsole{serverIP: netip.MustParseAddr("203.0.113.10"), carrierIP: netip.MustParseAddr("198.51.100.20"), tunnelPrefix: netip.MustParsePrefix("172.16.253.0/24")}
	var output bytes.Buffer
	err := client.routeCommand(context.Background(), []string{"route", "add", "198.51.100.0/24", "agent-a"}, &output)
	if err == nil || !strings.Contains(err.Error(), "carrier endpoint") {
		t.Fatalf("carrier route collision=%v", err)
	}
}

func TestClientRoutesPersistAcrossLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-routes.json")
	want := []control.AcceptedRoute{{Prefix: "192.168.0.0/22", AgentID: "agent-a"}, {Prefix: "10.10.0.0/16", AgentID: "agent-a", Manual: true}}
	for i := 0; i < 2; i++ {
		if err := saveClientRoutes(path, want); err != nil {
			t.Fatal(err)
		}
		got, err := loadClientRoutes(path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("routes=%+v err=%v", got, err)
		}
	}
}

func TestClientReassignsRouteFromDisconnectedAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-routes.json")
	old := control.AcceptedRoute{Prefix: "10.10.10.0/24", AgentID: "old-agent"}
	if err := saveClientRoutes(path, []control.AcceptedRoute{old}); err != nil {
		t.Fatal(err)
	}
	device := &recordingRouteDevice{}
	client := &liveClientConsole{
		device: device, sessionID: 7, routeFile: path,
		serverIP: netip.MustParseAddr("203.0.113.10"), tunnelPrefix: netip.MustParsePrefix("172.16.253.0/24"),
		routes: []control.AcceptedRoute{old}, active: map[string]bool{old.Prefix: true}, global: make(map[string]bool),
	}
	posted := false
	client.request = func(_ context.Context, method, path string, body any) ([]byte, error) {
		switch {
		case method == http.MethodGet && path == "/v1/status":
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "new-agent", Hostname: "WS01"}}})
		case method == http.MethodPost && path == "/v1/clients/7/routes":
			route, ok := body.(control.AcceptedRoute)
			if !ok || route.Prefix != old.Prefix || route.AgentID != "new-agent" {
				t.Fatalf("unexpected reassignment body: %+v", body)
			}
			posted = true
			return nil, nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", method, path)
		}
	}
	var output bytes.Buffer
	if err := client.routeCommand(context.Background(), []string{"route", "accept", old.Prefix, "new-agent"}, &output); err != nil {
		t.Fatal(err)
	}
	got, err := loadClientRoutes(path)
	if err != nil {
		t.Fatal(err)
	}
	if !posted || len(got) != 1 || got[0].AgentID != "new-agent" || !reflect.DeepEqual(got, client.routes) || len(device.added) != 0 || len(device.deleted) != 0 || !strings.Contains(output.String(), "reassigned from old-agent to new-agent") {
		t.Fatalf("reassignment: posted=%t saved=%+v current=%+v device=%+v output=%q", posted, got, client.routes, device, output.String())
	}
}

func TestClientBlocksReassignmentFromConnectedAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-routes.json")
	old := control.AcceptedRoute{Prefix: "10.10.10.0/24", AgentID: "old-agent"}
	if err := saveClientRoutes(path, []control.AcceptedRoute{old}); err != nil {
		t.Fatal(err)
	}
	client := &liveClientConsole{
		routeFile: path, serverIP: netip.MustParseAddr("203.0.113.10"), tunnelPrefix: netip.MustParsePrefix("172.16.253.0/24"),
		routes: []control.AcceptedRoute{old},
	}
	client.request = func(_ context.Context, method, path string, _ any) ([]byte, error) {
		if method != http.MethodGet || path != "/v1/status" {
			return nil, fmt.Errorf("unexpected request %s %s", method, path)
		}
		return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "old-agent", Hostname: "TALON"}, {ID: "new-agent", Hostname: "WS01"}}})
	}
	var output bytes.Buffer
	err := client.routeCommand(context.Background(), []string{"route", "accept", old.Prefix, "new-agent"}, &output)
	if err == nil || !strings.Contains(err.Error(), "owned by connected agent TALON (old-agent)") {
		t.Fatalf("expected connected owner warning, got %v", err)
	}
	got, err := loadClientRoutes(path)
	if err != nil || !reflect.DeepEqual(got, []control.AcceptedRoute{old}) || !reflect.DeepEqual(client.routes, got) {
		t.Fatalf("route changed after rejection: saved=%+v current=%+v err=%v", got, client.routes, err)
	}
}

func TestClientReassignmentFailureKeepsOldRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-routes.json")
	old := control.AcceptedRoute{Prefix: "10.10.10.0/24", AgentID: "old-agent"}
	if err := saveClientRoutes(path, []control.AcceptedRoute{old}); err != nil {
		t.Fatal(err)
	}
	client := &liveClientConsole{
		sessionID: 7, routeFile: path, serverIP: netip.MustParseAddr("203.0.113.10"), tunnelPrefix: netip.MustParsePrefix("172.16.253.0/24"),
		routes: []control.AcceptedRoute{old}, active: map[string]bool{old.Prefix: true},
	}
	client.request = func(_ context.Context, method, path string, _ any) ([]byte, error) {
		if method == http.MethodGet && path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "new-agent"}}})
		}
		return nil, errors.New("target agent rejected route")
	}
	var output bytes.Buffer
	err := client.routeCommand(context.Background(), []string{"route", "accept", old.Prefix, "new-agent"}, &output)
	if err == nil || !strings.Contains(err.Error(), "target agent rejected route") {
		t.Fatalf("expected reassignment error, got %v", err)
	}
	got, err := loadClientRoutes(path)
	if err != nil || !reflect.DeepEqual(got, []control.AcceptedRoute{old}) || !reflect.DeepEqual(client.routes, got) {
		t.Fatalf("route changed after failure: saved=%+v current=%+v err=%v", got, client.routes, err)
	}
}

func TestClientDeletesRouteOwnedByAnotherAgent(t *testing.T) {
	for _, verb := range []string{"del", "delete"} {
		t.Run(verb, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client-routes.json")
			old := control.AcceptedRoute{Prefix: "10.10.10.0/24", AgentID: "old-agent"}
			if err := saveClientRoutes(path, []control.AcceptedRoute{old}); err != nil {
				t.Fatal(err)
			}
			device := &recordingRouteDevice{}
			client := &liveClientConsole{
				device: device, sessionID: 7, routeFile: path,
				routes: []control.AcceptedRoute{old}, active: map[string]bool{old.Prefix: true},
			}
			client.request = func(_ context.Context, method, requestPath string, _ any) ([]byte, error) {
				if method != http.MethodDelete || requestPath != "/v1/clients/7/routes?prefix=10.10.10.0%2F24" {
					return nil, fmt.Errorf("unexpected request %s %s", method, requestPath)
				}
				return nil, nil
			}
			var output bytes.Buffer
			if err := client.routeCommand(context.Background(), []string{"route", verb, old.Prefix}, &output); err != nil {
				t.Fatal(err)
			}
			got, err := loadClientRoutes(path)
			if err != nil || len(got) != 0 || len(client.routes) != 0 || !reflect.DeepEqual(device.deleted, []string{old.Prefix}) {
				t.Fatalf("deletion: saved=%+v current=%+v device=%+v err=%v", got, client.routes, device, err)
			}
		})
	}
}
