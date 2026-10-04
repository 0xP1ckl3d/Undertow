package control

import (
	"net/netip"
	"testing"

	"undertow/internal/routing"
)

func TestTopologyShowsOnlyAcceptedRoutesAndRelayParent(t *testing.T) {
	agents := []AgentInfo{{ID: "parent", Hostname: "first", Transport: "quic"}, {ID: "child", Hostname: "second", Transport: "relay", Via: "parent", Depth: 1, AdvertisedRoutes: []string{"10.20.0.0/16", "10.30.0.0/16"}}}
	clients := []ClientInfo{{ID: "client-a", SessionID: 5, Internal: false, AcceptedRoutes: []AcceptedRoute{{Prefix: "10.20.0.0/16", AgentID: "child"}}}}
	routes := []routing.Route{{Prefix: netip.MustParsePrefix("10.20.0.0/16"), AgentID: "child", Active: true}}
	topology := BuildTopology(agents, clients, routes, []RelayInfo{{AgentID: "parent", Bind: "10.0.0.5:8443"}}, nil)
	kinds := map[string]bool{}
	for _, edge := range topology.Edges {
		kinds[edge.Kind] = true
		if edge.Kind == "relay_path" && (edge.Source != "agent:parent" || edge.Target != "agent:child") {
			t.Fatalf("wrong parent edge %+v", edge)
		}
	}
	for _, kind := range []string{"relay_path", "relay_listener", "accepted_route", "carrier"} {
		if !kinds[kind] {
			t.Fatalf("missing %s edge", kind)
		}
	}
	if kinds["advertised_route"] || kinds["server_route"] {
		t.Fatalf("unaccepted route edge displayed: %+v", topology.Edges)
	}
	for _, node := range topology.Nodes {
		if node.ID == "network:10.30.0.0/16" {
			t.Fatal("unaccepted network displayed")
		}
	}
	for _, edge := range topology.Edges {
		if edge.Kind == "accepted_route" && !edge.Active {
			t.Fatal("accepted route incorrectly tied to internal mode")
		}
	}
}

func TestEventBrokerReplayAndGap(t *testing.T) {
	b := NewEventBroker()
	b.Publish("agent.connected", "a")
	b.Publish("agent.disconnected", "a")
	replay, _, stop := b.Subscribe(1)
	defer stop()
	if len(replay) != 1 || replay[0].Seq != 2 {
		t.Fatalf("replay %+v", replay)
	}
	for i := 0; i < 2050; i++ {
		b.Publish("change", "")
	}
	gap, _, stopGap := b.Subscribe(1)
	defer stopGap()
	if len(gap) != 1 || gap[0].Kind != "resync_required" {
		t.Fatalf("gap %+v", gap)
	}
}
