package control

import (
	"fmt"
	"sort"
	"time"

	"undertow/internal/routing"
)

// Topology is a server-derived view of observed connections and route state.
// Layout coordinates are deliberately absent: each operator owns their layout.
type Topology struct {
	Version int            `json:"version"`
	At      time.Time      `json:"at"`
	Nodes   []TopologyNode `json:"nodes"`
	Edges   []TopologyEdge `json:"edges"`
}

type TopologyNode struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Label     string         `json:"label"`
	AgentID   string         `json:"agent_id,omitempty"`
	ClientID  string         `json:"client_id,omitempty"`
	SessionID uint64         `json:"session_id,omitempty"`
	Carrier   string         `json:"carrier,omitempty"`
	Remote    string         `json:"remote,omitempty"`
	LastSeen  time.Time      `json:"last_seen,omitempty"`
	RTTNs     int64          `json:"rtt_ns,omitempty"`
	Depth     int            `json:"depth,omitempty"`
	OS        string         `json:"os,omitempty"`
	Arch      string         `json:"arch,omitempty"`
	Connected time.Time      `json:"connected,omitempty"`
	Privilege string         `json:"privilege,omitempty"`
	Internal  bool           `json:"internal,omitempty"`
	Listeners []ListenerInfo `json:"listeners,omitempty"`
	Active    bool           `json:"active"`
}

type TopologyEdge struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Source         string          `json:"source"`
	Target         string          `json:"target"`
	Label          string          `json:"label,omitempty"`
	Active         bool            `json:"active"`
	ClientID       string          `json:"client_id,omitempty"`
	SessionID      uint64          `json:"session_id,omitempty"`
	Internal       bool            `json:"internal,omitempty"`
	AcceptedRoutes []AcceptedRoute `json:"accepted_routes,omitempty"`
	RTTNs          int64           `json:"rtt_ns,omitempty"`
	LastSeen       time.Time       `json:"last_seen,omitempty"`
}

func (m *Manager) Topology() Topology {
	return BuildTopology(m.AgentList(), m.ClientList(), m.routes.List(), m.RelayList(""), m.allForwards(), m.ServerInfo())
}

func (m *Manager) allForwards() []forwardState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]forwardState, 0, len(m.forwards))
	for _, value := range m.forwards {
		out = append(out, forwardState{ForwardInfo: value.ForwardInfo, ClientID: value.ClientID})
	}
	return out
}

func BuildTopology(agents []AgentInfo, clients []ClientInfo, routes []routing.Route, relays []RelayInfo, forwards []forwardState, serverInfo ...ServerInfo) Topology {
	server := TopologyNode{ID: "server", Kind: "server", Label: "Undertow server", Active: true}
	if len(serverInfo) > 0 {
		server.Listeners = serverInfo[0].Listeners
	}
	t := Topology{Version: 2, At: time.Now().UTC(), Nodes: []TopologyNode{server}, Edges: []TopologyEdge{}}
	_ = routes // Server configured routes remain in the Routes view; they do not imply client acceptance.
	networks := make(map[string]bool)
	addNetwork := func(prefix string) string {
		id := "network:" + prefix
		if !networks[id] {
			networks[id] = true
			t.Nodes = append(t.Nodes, TopologyNode{ID: id, Kind: "network", Label: prefix, Active: true})
		}
		return id
	}
	for _, a := range agents {
		id := "agent:" + a.ID
		label := a.Hostname
		if label == "" {
			label = a.ID
		}
		t.Nodes = append(t.Nodes, TopologyNode{ID: id, Kind: "agent", Label: label, AgentID: a.ID, SessionID: a.SessionID, Carrier: a.Transport, Remote: a.Remote, LastSeen: a.LastSeen, RTTNs: int64(a.RTT), Depth: a.Depth, OS: a.OS, Arch: a.Arch, Connected: a.Connected, Privilege: a.Privilege, Active: true})
		parent, kind := "server", "carrier"
		if a.Via != "" {
			parent, kind = "agent:"+a.Via, "relay_path"
		}
		t.Edges = append(t.Edges, TopologyEdge{ID: kind + ":" + parent + ":" + id, Kind: kind, Source: parent, Target: id, Label: a.Transport, Active: true, SessionID: a.SessionID, RTTNs: int64(a.RTT), LastSeen: a.LastSeen})
	}
	for _, c := range clients {
		id := "client:" + c.ID
		if c.ID == "" {
			id = fmt.Sprintf("client-session:%d", c.SessionID)
		}
		label := c.Hostname
		if label == "" {
			label = c.ID
		}
		t.Nodes = append(t.Nodes, TopologyNode{ID: id, Kind: "client", Label: label, ClientID: c.ID, SessionID: c.SessionID, Carrier: c.Transport, Remote: c.Remote, LastSeen: c.LastSeen, RTTNs: int64(c.RTT), Connected: c.Connected, Internal: c.Internal, Active: true})
		t.Edges = append(t.Edges, TopologyEdge{ID: "carrier:" + id, Kind: "carrier", Source: id, Target: "server", Label: c.Transport, Active: true, ClientID: c.ID, SessionID: c.SessionID, Internal: c.Internal, AcceptedRoutes: append([]AcceptedRoute(nil), c.AcceptedRoutes...), RTTNs: int64(c.RTT), LastSeen: c.LastSeen})
		for _, r := range c.AcceptedRoutes {
			target := addNetwork(r.Prefix)
			t.Edges = append(t.Edges, TopologyEdge{ID: "accepted:" + id + ":" + r.Prefix, Kind: "accepted_route", Source: "agent:" + r.AgentID, Target: target, Label: "accepted by " + label, Active: true, ClientID: c.ID, SessionID: c.SessionID})
		}
	}
	for _, r := range relays {
		id := "relay:" + r.AgentID + ":" + r.Bind
		t.Nodes = append(t.Nodes, TopologyNode{ID: id, Kind: "relay", Label: r.Bind, AgentID: r.AgentID, Active: true})
		t.Edges = append(t.Edges, TopologyEdge{ID: "relay-listener:" + id, Kind: "relay_listener", Source: "agent:" + r.AgentID, Target: id, Active: true})
	}
	for _, f := range forwards {
		client := fmt.Sprintf("client-session:%d", f.ClientID)
		for _, c := range clients {
			if c.SessionID == f.ClientID && c.ID != "" {
				client = "client:" + c.ID
				break
			}
		}
		t.Edges = append(t.Edges, TopologyEdge{ID: "forward:" + f.ID, Kind: "forward", Source: client, Target: "agent:" + f.AgentID, Label: f.Bind + " → " + f.Target, Active: true})
	}
	sort.Slice(t.Nodes, func(i, j int) bool { return t.Nodes[i].ID < t.Nodes[j].ID })
	sort.Slice(t.Edges, func(i, j int) bool { return t.Edges[i].ID < t.Edges[j].ID })
	return t
}
