//go:build linux || windows

package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"undertow/internal/control"
)

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
