package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

func TestNetworkRouteValidation(t *testing.T) {
	for _, tc := range []struct {
		route              NetworkRoute
		defaultOnly, valid bool
	}{
		{NetworkRoute{Prefix: "10.20.0.0/16", Gateway: "192.168.50.1", Interface: "eth0", Direct: true}, false, true},
		{NetworkRoute{Prefix: "10.20.3.0/16"}, false, false},
		{NetworkRoute{Prefix: "10.20.0.0/16", Gateway: "bad"}, false, false},
		{NetworkRoute{Prefix: "0.0.0.0/0", Gateway: "192.168.50.1"}, true, true},
		{NetworkRoute{Prefix: "0.0.0.0/0"}, false, false},
	} {
		got, valid := validNetworkRoute(tc.route, tc.defaultOnly)
		if valid != tc.valid {
			t.Fatalf("route=%+v valid=%t want=%t", tc.route, valid, tc.valid)
		}
		if valid && got.Gateway != "" && got.Direct {
			t.Fatalf("gateway route marked direct: %+v", got)
		}
	}
}

func TestInventorySendsSeparateStructuredRoutes(t *testing.T) {
	routes, defaultRoute := collectNetworkRoutes()
	t.Logf("discovered IPv4 routes=%d default=%v", len(routes), defaultRoute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer agent.Close()
	done := make(chan error, 1)
	go func() { done <- SendInventory(ctx, agent, nil, pivot.DefaultCapabilities()) }()
	first, err := server.RecvControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var info AgentInfo
	if json.Unmarshal(first, &info) != nil || info.Hostname == "" {
		t.Fatalf("initial inventory=%s", first)
	}
	second, err := server.RecvControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var update networkRouteUpdate
	if json.Unmarshal(second, &update) != nil || !update.RouteUpdate || !update.Reset {
		t.Fatalf("network route update=%s", second)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("inventory send did not complete")
	}
}
