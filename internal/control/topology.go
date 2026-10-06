package control

import (
	"fmt"
	"sort"
	"time"

	"undertow/internal/pivot"
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
	ID                   string         `json:"id"`
	Kind                 string         `json:"kind"`
	Label                string         `json:"label"`
	Hostname             string         `json:"hostname,omitempty"`
	AgentID              string         `json:"agent_id,omitempty"`
	Via                  string         `json:"via,omitempty"`
	ClientID             string         `json:"client_id,omitempty"`
	SessionID            uint64         `json:"session_id,omitempty"`
	Carrier              string         `json:"carrier,omitempty"`
	RelayBind            string         `json:"relay_bind,omitempty"`
	Remote               string         `json:"remote,omitempty"`
	PublicIP             string         `json:"public_ip,omitempty"`
	LastSeen             time.Time      `json:"last_seen,omitempty"`
	RTTNs                int64          `json:"rtt_ns,omitempty"`
	Depth                int            `json:"depth,omitempty"`
	OS                   string         `json:"os,omitempty"`
	Arch                 string         `json:"arch,omitempty"`
	Connected            time.Time      `json:"connected,omitempty"`
	DisconnectedAt       time.Time      `json:"disconnected_at,omitempty"`
	Privilege            string         `json:"privilege,omitempty"`
	Archived             bool           `json:"archived,omitempty"`
	ConnectionMode       string         `json:"connection_mode,omitempty"`
	ConnectionState      string         `json:"connection_state,omitempty"`
	ConnectionReason     string         `json:"connection_reason,omitempty"`
	Sleep                SleepPolicy    `json:"sleep"`
	SleepProtocolVersion int            `json:"sleep_protocol_version,omitempty"`
	IdleGraceSeconds     int            `json:"idle_grace_seconds,omitempty"`
	ExpectedCheckIn      time.Time      `json:"expected_checkin,omitempty"`
	SleepLostAfter       time.Time      `json:"sleep_lost_after,omitempty"`
	Internal             bool           `json:"internal,omitempty"`
	VPN                  bool           `json:"vpn,omitempty"`
	AcceptedCount        int            `json:"accepted_count,omitempty"`
	Listeners            []ListenerInfo `json:"listeners,omitempty"`
	Carriers             []CarrierState `json:"carriers,omitempty"`
	PublicHost           string         `json:"public_host,omitempty"`
	Active               bool           `json:"active"`
}

type CarrierState struct {
	Transport string `json:"transport"`
	Active    bool   `json:"active"`
	Listen    string `json:"listen,omitempty"`
	Sessions  int    `json:"sessions,omitempty"`
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
	VPN            bool            `json:"vpn,omitempty"`
	AcceptedRoutes []AcceptedRoute `json:"accepted_routes,omitempty"`
	AcceptedBy     []string        `json:"accepted_by,omitempty"`
	RTTNs          int64           `json:"rtt_ns,omitempty"`
	LastSeen       time.Time       `json:"last_seen,omitempty"`
}

func (m *Manager) Topology() Topology {
	return BuildTopology(m.AgentCatalog(), m.ClientList(), m.routes.List(), m.RelayList(""), m.allForwards(), m.ServerInfo())
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
		server.PublicHost = serverInfo[0].PublicHost
		for _, kind := range []string{"dns", "quic", "websocket"} {
			state := CarrierState{Transport: kind}
			for _, listener := range server.Listeners {
				if listener.Transport == kind {
					state.Active = true
					state.Listen = listener.Listen
					state.Sessions = listener.Sessions
					break
				}
			}
			server.Carriers = append(server.Carriers, state)
		}
	}
	t := Topology{Version: 8, At: time.Now().UTC(), Nodes: []TopologyNode{server}, Edges: []TopologyEdge{}}
	_ = routes // Server configured routes remain in the Routes view; they do not imply client acceptance.
	type relayNode struct {
		agentID, bind string
		active        bool
	}
	relayNodes := make(map[string]relayNode)
	for _, relay := range relays {
		relayNodes["relay:"+relay.AgentID+":"+relay.Bind] = relayNode{relay.AgentID, relay.Bind, relay.State != "pending"}
	}
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
		label := a.Nickname
		if label == "" {
			label = a.Hostname
		}
		if label == "" {
			label = a.ID
		}
		t.Nodes = append(t.Nodes, TopologyNode{ID: id, Kind: "agent", Label: label, Hostname: a.Hostname, AgentID: a.ID, Via: a.Via, SessionID: a.SessionID, Carrier: a.Transport, RelayBind: a.RelayBind, Remote: a.Remote, PublicIP: a.PublicIP, LastSeen: a.LastSeen, RTTNs: int64(a.RTT), Depth: a.Depth, OS: a.OS, Arch: a.Arch, Connected: a.Connected, DisconnectedAt: a.DisconnectedAt, Privilege: a.Privilege, Archived: a.Archived, ConnectionMode: a.ConnectionMode, ConnectionState: a.ConnectionState, ConnectionReason: a.ConnectionReason, Sleep: a.Sleep, SleepProtocolVersion: a.SleepProtocolVersion, IdleGraceSeconds: a.IdleGraceSeconds, ExpectedCheckIn: a.ExpectedCheckIn, SleepLostAfter: a.SleepLostAfter, Active: a.Online})
		parent, kind := "server", "carrier"
		if a.Via != "" {
			parent, kind = "agent:"+a.Via, "relay_path"
			bind := a.RelayBind
			if bind == "" {
				matches := 0
				for _, relay := range relays {
					if relay.AgentID != a.Via || pivot.IsPipeRelayBind(relay.Bind) != (a.Transport == "relay-smb") {
						continue
					}
					matches++
					bind = relay.Bind
				}
				if matches != 1 {
					bind = ""
				} // Never guess among same-carrier listeners.
			}
			if bind != "" {
				parent = "relay:" + a.Via + ":" + bind
				if _, exists := relayNodes[parent]; !exists {
					relayNodes[parent] = relayNode{a.Via, bind, false} // Retain the last known path after disconnect.
				}
			}
		}
		t.Edges = append(t.Edges, TopologyEdge{ID: kind + ":" + parent + ":" + id, Kind: kind, Source: parent, Target: id, Label: a.Transport, Active: a.Online, SessionID: a.SessionID, RTTNs: int64(a.RTT), LastSeen: a.LastSeen})
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
		t.Nodes = append(t.Nodes, TopologyNode{ID: id, Kind: "client", Label: label, ClientID: c.ID, SessionID: c.SessionID, Carrier: c.Transport, Remote: c.Remote, LastSeen: c.LastSeen, RTTNs: int64(c.RTT), Connected: c.Connected, Internal: c.Internal, VPN: c.VPN, AcceptedCount: len(c.AcceptedRoutes), Active: true})
		t.Edges = append(t.Edges, TopologyEdge{ID: "carrier:" + id, Kind: "carrier", Source: id, Target: "server", Label: c.Transport, Active: true, ClientID: c.ID, SessionID: c.SessionID, Internal: c.Internal, VPN: c.VPN, AcceptedRoutes: append([]AcceptedRoute(nil), c.AcceptedRoutes...), RTTNs: int64(c.RTT), LastSeen: c.LastSeen})
		for _, r := range c.AcceptedRoutes {
			target := addNetwork(r.Prefix)
			t.Edges = append(t.Edges, TopologyEdge{ID: "accepted:" + id + ":" + r.Prefix, Kind: "accepted_route", Source: "agent:" + r.AgentID, Target: target, Label: "accepted by " + label, Active: true, ClientID: c.ID, SessionID: c.SessionID})
		}
	}
	for id, relay := range relayNodes {
		depth := 0
		for _, a := range agents {
			if a.ID == relay.agentID {
				depth = a.Depth
				break
			}
		}
		t.Nodes = append(t.Nodes, TopologyNode{ID: id, Kind: "relay", Label: relay.bind, AgentID: relay.agentID, Depth: depth, Active: relay.active})
		t.Edges = append(t.Edges, TopologyEdge{ID: "relay-listener:" + id, Kind: "relay_listener", Source: "agent:" + relay.agentID, Target: id, Active: relay.active})
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
	// Mark the actual carrier and relay edges taken by each client's accepted
	// route. Rendering can highlight one continuous client-to-network path
	// without inventing a direct client-to-agent connection.
	edgeByID := make(map[string]*TopologyEdge, len(t.Edges))
	upstream := make(map[string]*TopologyEdge)
	for i := range t.Edges {
		edge := &t.Edges[i]
		edgeByID[edge.ID] = edge
		if edge.Kind == "carrier" || edge.Kind == "relay_path" || edge.Kind == "relay_listener" {
			upstream[edge.Target] = edge
		}
	}
	mark := func(edge *TopologyEdge, client string) {
		if edge == nil {
			return
		}
		for _, existing := range edge.AcceptedBy {
			if existing == client {
				return
			}
		}
		edge.AcceptedBy = append(edge.AcceptedBy, client)
	}
	for _, client := range clients {
		clientNode := "client:" + client.ID
		if client.ID == "" {
			clientNode = fmt.Sprintf("client-session:%d", client.SessionID)
		}
		for _, route := range client.AcceptedRoutes {
			mark(edgeByID["carrier:"+clientNode], clientNode)
			mark(edgeByID["accepted:"+clientNode+":"+route.Prefix], clientNode)
			current := "agent:" + route.AgentID
			seen := make(map[string]bool)
			for current != "server" && !seen[current] {
				seen[current] = true
				edge := upstream[current]
				if edge == nil {
					break
				}
				mark(edge, clientNode)
				current = edge.Source
			}
		}
	}
	sort.Slice(t.Nodes, func(i, j int) bool { return t.Nodes[i].ID < t.Nodes[j].ID })
	sort.Slice(t.Edges, func(i, j int) bool { return t.Edges[i].ID < t.Edges[j].ID })
	return t
}
