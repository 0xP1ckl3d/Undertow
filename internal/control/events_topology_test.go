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
	acceptedPath := map[string]bool{}
	for _, edge := range topology.Edges {
		kinds[edge.Kind] = true
		if len(edge.AcceptedBy) == 1 && edge.AcceptedBy[0] == "client:client-a" {
			acceptedPath[edge.ID] = true
		}
		if edge.Kind == "relay_path" && (edge.Source != "relay:parent:10.0.0.5:8443" || edge.Target != "agent:child") {
			t.Fatalf("wrong parent edge %+v", edge)
		}
	}
	for _, id := range []string{"carrier:client:client-a", "carrier:server:agent:parent", "relay-listener:relay:parent:10.0.0.5:8443", "relay_path:relay:parent:10.0.0.5:8443:agent:child", "accepted:client:client-a:10.20.0.0/16"} {
		if !acceptedPath[id] {
			t.Errorf("accepted path missing edge %s", id)
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

func TestTopologyUsesExactRelayListenerWithMultipleBinds(t *testing.T) {
	bind := `\\.\pipe\branch`
	agents := []AgentInfo{
		{ID: "parent", Hostname: "parent", Online: true},
		{ID: "tcp-child", Via: "parent", Transport: "relay", RelayBind: "10.0.0.5:8443", Online: true},
		{ID: "pipe-child", Via: "parent", Transport: "relay-smb", RelayBind: bind, Online: true},
	}
	relays := []RelayInfo{{AgentID: "parent", Bind: "10.0.0.5:8443"}, {AgentID: "parent", Bind: "10.0.0.5:9443"}, {AgentID: "parent", Bind: bind}}
	topology := BuildTopology(agents, nil, nil, relays, nil)
	want := map[string]string{"agent:tcp-child": "relay:parent:10.0.0.5:8443", "agent:pipe-child": "relay:parent:" + bind}
	for _, edge := range topology.Edges {
		if expected, ok := want[edge.Target]; ok && edge.Kind == "relay_path" {
			if edge.Source != expected {
				t.Fatalf("%s came from %s, want %s", edge.Target, edge.Source, expected)
			}
			delete(want, edge.Target)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing relay paths: %v", want)
	}
}

func TestEventBrokerReplayAndGap(t *testing.T) {
	b := NewEventBroker()
	restarted, _, stopRestarted := b.Subscribe(900)
	stopRestarted()
	if len(restarted) != 1 || restarted[0].Kind != "resync_required" {
		t.Fatalf("stale cursor after restart: %+v", restarted)
	}
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
