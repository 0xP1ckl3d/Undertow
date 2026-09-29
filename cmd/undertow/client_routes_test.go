//go:build linux || windows

package main

import (
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"reflect"
	"testing"

	"undertow/internal/control"
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
