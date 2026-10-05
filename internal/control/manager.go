package control

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/transport"
)

type RouteDevice interface {
	AddRoute(string) error
	DelRoute(string) error
}

type AgentInfo struct {
	ArtifactIdentity
	ID                 string                  `json:"id"`
	Transport          string                  `json:"transport,omitempty"`
	Via                string                  `json:"via,omitempty"`
	RelayBind          string                  `json:"relay_bind,omitempty"`
	Depth              int                     `json:"depth,omitempty"`
	SessionID          uint64                  `json:"session_id"`
	VirtualIP          string                  `json:"virtual_ip"`
	Remote             string                  `json:"remote"`
	PublicIP           string                  `json:"public_ip,omitempty"`
	Online             bool                    `json:"online"`
	Offline            bool                    `json:"offline,omitempty"`
	DisconnectedAt     time.Time               `json:"disconnected_at,omitempty"`
	Hostname           string                  `json:"hostname,omitempty"`
	OS                 string                  `json:"os,omitempty"`
	Arch               string                  `json:"arch,omitempty"`
	Privilege          string                  `json:"privilege,omitempty"`
	Nickname           string                  `json:"nickname,omitempty"`
	Interfaces         []string                `json:"interfaces,omitempty"`
	AdvertisedRoutes   []string                `json:"advertised_routes,omitempty"`
	Routes             []NetworkRoute          `json:"routes,omitempty"`
	DefaultRoute       *NetworkRoute           `json:"default_route,omitempty"`
	Capabilities       *pivot.CapabilityReport `json:"capabilities,omitempty"`
	Connected          time.Time               `json:"connected"`
	LastSeen           time.Time               `json:"last_seen"`
	RTT                time.Duration           `json:"rtt_ns"`
	RXBytes            uint64                  `json:"rx_bytes"`
	TXBytes            uint64                  `json:"tx_bytes"`
	Retransmits        uint64                  `json:"retransmits"`
	Duplicates         uint64                  `json:"duplicates"`
	RXRate             float64                 `json:"rx_rate_bytes_per_second"`
	TXRate             float64                 `json:"tx_rate_bytes_per_second"`
	Streams            int                     `json:"streams"`
	ActiveForwards     int                     `json:"active_forwards"`
	Forwards           []ForwardInfo           `json:"forwards,omitempty"`
	InFlight           int                     `json:"in_flight"`
	Queued             int                     `json:"queued"`
	Window             int                     `json:"congestion_window"`
	ReceiveWindow      int                     `json:"receive_window"`
	PeerReceiveWindow  int                     `json:"peer_receive_window"`
	FragmentSize       int                     `json:"fragment_size"`
	PayloadAdjustments uint64                  `json:"payload_adjustments"`
	ActiveJobs         int                     `json:"active_jobs"`
}

type LifecycleEvent struct {
	At                time.Time `json:"at"`
	AgentID           string    `json:"agent_id"`
	Kind              string    `json:"kind"`
	Transport         string    `json:"transport,omitempty"`
	ProfileID         string    `json:"profile_id,omitempty"`
	ArtifactID        string    `json:"artifact_id,omitempty"`
	SessionID         uint64    `json:"session_id,omitempty"`
	DurationSeconds   int64     `json:"duration_seconds,omitempty"`
	ReconnectAttempts uint32    `json:"reconnect_attempts,omitempty"`
}

type ClientInfo struct {
	ID             string          `json:"id"`
	Transport      string          `json:"transport,omitempty"`
	SessionID      uint64          `json:"session_id"`
	Hostname       string          `json:"hostname,omitempty"`
	Remote         string          `json:"remote"`
	Internal       bool            `json:"internal"`
	VPN            bool            `json:"vpn"`
	AcceptedRoutes []AcceptedRoute `json:"accepted_routes,omitempty"`
	Connected      time.Time       `json:"connected"`
	LastSeen       time.Time       `json:"last_seen"`
	RTT            time.Duration   `json:"rtt_ns"`
	RXBytes        uint64          `json:"rx_bytes"`
	TXBytes        uint64          `json:"tx_bytes"`
	Retransmits    uint64          `json:"retransmits"`
	Streams        int             `json:"streams"`
	InFlight       int             `json:"in_flight"`
	Queued         int             `json:"queued"`
	Window         int             `json:"congestion_window"`
}

type AcceptedRoute struct {
	Prefix  string `json:"prefix"`
	AgentID string `json:"agent_id"`
	Manual  bool   `json:"manual,omitempty"`
	// Disabled records a saved client-local choice. Disabled routes are never
	// posted as server-accepted routes or installed on the client host.
	Disabled bool `json:"disabled,omitempty"`
}

type ServerInfo struct {
	Transport     string         `json:"transport"`
	Network       string         `json:"network"`
	Listen        string         `json:"listen"`
	Domain        string         `json:"domain,omitempty"`
	WebSocketPath string         `json:"websocket_path,omitempty"`
	TLSMode       string         `json:"tls_mode,omitempty"`
	Fingerprint   string         `json:"fingerprint,omitempty"`
	PublicHost    string         `json:"public_host,omitempty"`
	Listeners     []ListenerInfo `json:"listeners,omitempty"`
}

type ListenerInfo struct {
	Transport string `json:"transport"`
	Network   string `json:"network"`
	Listen    string `json:"listen"`
	TLSMode   string `json:"tls_mode,omitempty"`
	Sessions  int    `json:"sessions"`
	Agents    int    `json:"agents"`
	Clients   int    `json:"clients"`
}

type TransportStartRequest struct {
	Listen  string `json:"listen,omitempty"`
	TLSMode string `json:"tls_mode,omitempty"`
	TLSCert string `json:"tls_cert,omitempty"`
	TLSKey  string `json:"tls_key,omitempty"`
}

type TransportController interface {
	List() []ListenerInfo
	Start(string, TransportStartRequest) (ListenerInfo, error)
	Stop(string, bool) error
}

type clientState struct {
	peer     transport.Peer
	mux      *mux.Mux
	internal bool
	vpn      bool
	hostname string
	accepted map[netip.Prefix]AcceptedRoute
}

type agentState struct {
	peer           transport.Peer
	mux            *mux.Mux
	inventory      AgentInfo
	privilege      string
	inventoryReady bool
	rateAt         time.Time
	rateRX         uint64
	rateTX         uint64
	rxRate         float64
	txRate         float64
}
type Manager struct {
	mu                 sync.RWMutex
	operations         *OperationsStore
	offlineAgents      map[string]AgentInfo
	nicknames          map[string]string
	eventBus           *EventBroker
	workerLogs         *WorkerLogBuffer
	lifecycleEvents    []LifecycleEvent
	artifactLookup     func(string) (string, string, bool)
	agentDistribution  http.Handler
	server             ServerInfo
	transports         TransportController
	relayAccept        func(context.Context, string, string, string, *mux.Stream)
	relayPayloadAccept func(context.Context, string, *mux.Stream)
	relays             map[string]map[string]*relayState
	agents             map[string]*agentState
	clients            map[uint64]*clientState
	forwards           map[string]*forwardState
	jobs               map[string]*jobState
	jobOutput          *jobOutputStore
	routes             *routing.Table
	device             RouteDevice
	selected           string
	virtualNetwork     netip.Prefix
	proxyIP            netip.Addr
	virtualByAgent     map[string]netip.Addr
	virtualUsed        map[netip.Addr]bool
}

func (m *Manager) SetOperationsStore(store *OperationsStore) error {
	if err := store.RecoverTransfers(); err != nil {
		return err
	}
	previous, err := store.LoadAgentSnapshots()
	if err != nil {
		return err
	}
	nicknames, err := store.LoadAgentNicknames()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.operations = store
	m.nicknames = nicknames
	m.offlineAgents = make(map[string]AgentInfo, len(previous))
	for _, agent := range previous {
		agent.Online = false
		agent.Offline = true
		m.offlineAgents[agent.ID] = agent
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) SetWorkerLogs(logs *WorkerLogBuffer) {
	m.mu.Lock()
	m.workerLogs = logs
	m.mu.Unlock()
}

func (m *Manager) SetArtifactLookup(lookup func(string) (string, string, bool)) {
	m.mu.Lock()
	m.artifactLookup = lookup
	m.mu.Unlock()
}

func (m *Manager) recordLifecycleLocked(event LifecycleEvent) {
	event.At = time.Now().UTC()
	m.lifecycleEvents = append(m.lifecycleEvents, event)
	if len(m.lifecycleEvents) > 1024 {
		m.lifecycleEvents = append([]LifecycleEvent(nil), m.lifecycleEvents[len(m.lifecycleEvents)-1024:]...)
	}
}

func (m *Manager) LifecycleEvents(id string) []LifecycleEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LifecycleEvent, 0, 32)
	for i := len(m.lifecycleEvents) - 1; i >= 0 && len(out) < 32; i-- {
		if m.lifecycleEvents[i].AgentID == id {
			out = append(out, m.lifecycleEvents[i])
		}
	}
	return out
}

func (m *Manager) SetAgentDistributionHandler(handler http.Handler) {
	m.mu.Lock()
	m.agentDistribution = handler
	m.mu.Unlock()
}

func (m *Manager) SetServerInfo(info ServerInfo) {
	m.mu.Lock()
	m.server = info
	m.mu.Unlock()
}

func (m *Manager) SetPublicHost(host string) {
	m.mu.Lock()
	m.server.PublicHost = host
	m.mu.Unlock()
	m.PublishEvent("server.public_host", host)
}

func validServerPublicHost(host string) bool {
	if host == "" || net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (m *Manager) ServerInfo() ServerInfo {
	m.mu.RLock()
	info, transports := m.server, m.transports
	m.mu.RUnlock()
	if transports != nil {
		info.Listeners = transports.List()
	}
	return info
}

func (m *Manager) SetTransportController(controller TransportController) {
	m.mu.Lock()
	m.transports = controller
	m.mu.Unlock()
}

func (m *Manager) SetRelayAcceptor(accept func(context.Context, string, string, string, *mux.Stream)) {
	m.mu.Lock()
	m.relayAccept = accept
	m.mu.Unlock()
}

func (m *Manager) SetRelayPayloadAcceptor(accept func(context.Context, string, *mux.Stream)) {
	m.mu.Lock()
	m.relayPayloadAccept = accept
	m.mu.Unlock()
}

func NewManager(routes *routing.Table, device RouteDevice, virtualNetwork netip.Prefix, proxyIP netip.Addr) *Manager {
	return &Manager{eventBus: NewEventBroker(), agents: make(map[string]*agentState), offlineAgents: make(map[string]AgentInfo), nicknames: make(map[string]string), clients: make(map[uint64]*clientState), forwards: make(map[string]*forwardState), jobs: make(map[string]*jobState), relays: make(map[string]map[string]*relayState), routes: routes, device: device, virtualNetwork: virtualNetwork.Masked(), proxyIP: proxyIP, virtualByAgent: make(map[string]netip.Addr), virtualUsed: make(map[netip.Addr]bool)}
}

func (m *Manager) SetAgentNickname(id, nickname string) error {
	nickname = strings.TrimSpace(nickname)
	if utf8.RuneCountInString(nickname) > 48 {
		return errors.New("nickname must be 48 characters or fewer")
	}
	for _, r := range nickname {
		if unicode.IsControl(r) {
			return errors.New("nickname must be one line")
		}
	}
	m.mu.Lock()
	if m.agents[id] == nil {
		if _, exists := m.offlineAgents[id]; !exists {
			m.mu.Unlock()
			return errors.New("agent not found")
		}
	}
	if m.operations == nil {
		m.mu.Unlock()
		return errors.New("operations store unavailable")
	}
	if err := m.operations.SetAgentNickname(id, nickname); err != nil {
		m.mu.Unlock()
		return err
	}
	if nickname == "" {
		delete(m.nicknames, id)
	} else {
		m.nicknames[id] = nickname
	}
	m.mu.Unlock()
	m.PublishEvent("agent.updated", id)
	return nil
}

func (m *Manager) RegisterClient(peer transport.Peer, streamMux *mux.Mux, internal bool, hostname string, vpn ...bool) {
	vpnEnabled := len(vpn) > 0 && vpn[0]
	m.mu.Lock()
	m.clients[peer.Snapshot().ID] = &clientState{peer: peer, mux: streamMux, internal: internal, vpn: vpnEnabled, hostname: safeHostname(hostname), accepted: make(map[netip.Prefix]AcceptedRoute)}
	m.mu.Unlock()
	m.PublishEvent("client.connected", peer.Snapshot().AgentID)
}

func (m *Manager) UnregisterClient(sessionID uint64, streamMux *mux.Mux) {
	m.mu.Lock()
	var closed []*mux.Stream
	removed := false
	if state := m.clients[sessionID]; state != nil && state.mux == streamMux {
		removed = true
		delete(m.clients, sessionID)
		for id, forward := range m.forwards {
			if forward.ClientID == sessionID {
				closed = append(closed, forward.control)
				delete(m.forwards, id)
			}
		}
	}
	m.mu.Unlock()
	if removed {
		m.PublishEvent("client.disconnected", strconv.FormatUint(sessionID, 10))
		m.interruptTransfers(sessionID)
	}
	for _, stream := range closed {
		_ = stream.Close()
	}
}

func (m *Manager) ClientInternal(sessionID uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.clients[sessionID]
	return state != nil && state.internal
}

func (m *Manager) SetClientInternal(sessionID uint64, enabled bool) error {
	m.mu.Lock()
	state := m.clients[sessionID]
	if state == nil {
		m.mu.Unlock()
		return errors.New("VPN client is not connected")
	}
	state.internal = enabled
	m.mu.Unlock()
	m.PublishEvent("client.mode", strconv.FormatUint(sessionID, 10))
	return nil
}

func (m *Manager) SetClientVPN(sessionID uint64, enabled bool) error {
	m.mu.Lock()
	state := m.clients[sessionID]
	if state == nil {
		m.mu.Unlock()
		return errors.New("VPN client is not connected")
	}
	state.vpn = enabled
	m.mu.Unlock()
	m.PublishEvent("client.mode", strconv.FormatUint(sessionID, 10))
	return nil
}

func (m *Manager) SetClientRoute(sessionID uint64, prefix netip.Prefix, agentID string, manual bool) error {
	if !prefix.IsValid() || !prefix.Addr().Is4() {
		return errors.New("accepted route must be IPv4 CIDR")
	}
	prefix = prefix.Masked()
	m.mu.Lock()
	defer m.mu.Unlock()
	client := m.clients[sessionID]
	if client == nil {
		return errors.New("VPN client is not connected")
	}
	if existing, ok := client.accepted[prefix]; ok && existing.AgentID != agentID && m.agents[existing.AgentID] != nil {
		return fmt.Errorf("route %s is owned by connected agent %s; remove it before assigning another agent", prefix, existing.AgentID)
	}
	agent := m.agents[agentID]
	if agent == nil {
		return errors.New("agent is not connected")
	}
	if !agentPivotAllowed(agent) {
		return errors.New("agent pivot capability is disabled")
	}
	advertised := false
	for _, route := range agent.inventory.AdvertisedRoutes {
		if route == prefix.String() {
			advertised = true
			break
		}
	}
	if !advertised {
		for _, route := range agent.inventory.Routes {
			if route.Prefix == prefix.String() {
				advertised = true
				break
			}
		}
	}
	if !manual && !advertised {
		return errors.New("agent has not advertised or discovered this route")
	}
	client.accepted[prefix] = AcceptedRoute{Prefix: prefix.String(), AgentID: agentID, Manual: manual}
	return nil
}

func (m *Manager) DeleteClientRoute(sessionID uint64, prefix netip.Prefix) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	client := m.clients[sessionID]
	if client == nil {
		return errors.New("VPN client is not connected")
	}
	prefix = prefix.Masked()
	if _, exists := client.accepted[prefix]; !exists {
		return errors.New("route is not accepted by this client")
	}
	delete(client.accepted, prefix)
	return nil
}

func (m *Manager) ResolveClientEgress(sessionID uint64, destination netip.Addr) (*mux.Mux, bool) {
	m.mu.RLock()
	client := m.clients[sessionID]
	if client == nil {
		m.mu.RUnlock()
		return nil, false
	}
	bestBits, agentID := -1, ""
	for prefix, route := range client.accepted {
		if prefix.Contains(destination) && prefix.Bits() > bestBits {
			bestBits, agentID = prefix.Bits(), route.AgentID
		}
	}
	if agentID != "" {
		agent := m.agents[agentID]
		allowed := agentPivotAllowed(agent)
		var upstream *mux.Mux
		if allowed {
			upstream = agent.mux
		}
		m.mu.RUnlock()
		return upstream, true
	}
	internal := client.internal
	m.mu.RUnlock()
	if !internal {
		return nil, false
	}
	return m.ResolveEgress(destination)
}

func (m *Manager) ClientList() []ClientInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ClientInfo, 0, len(m.clients))
	for _, state := range m.clients {
		p := state.peer.Snapshot()
		accepted := make([]AcceptedRoute, 0, len(state.accepted))
		for _, route := range state.accepted {
			accepted = append(accepted, route)
		}
		sort.Slice(accepted, func(i, j int) bool { return accepted[i].Prefix < accepted[j].Prefix })
		out = append(out, ClientInfo{
			ID: p.AgentID, Transport: p.Carrier, SessionID: p.ID, Hostname: state.hostname, Remote: p.Remote, Internal: state.internal, VPN: state.vpn,
			AcceptedRoutes: accepted,
			Connected:      p.Connected, LastSeen: p.LastSeen, RTT: p.Transport.RTT,
			RXBytes: p.Transport.RXBytes, TXBytes: p.Transport.TXBytes,
			Retransmits: p.Transport.Retransmits, Streams: state.mux.StreamCount(),
			InFlight: p.Transport.InFlight, Queued: p.Transport.Queued, Window: p.Transport.CongestionWindow,
		})
	}
	return out
}

func (m *Manager) Register(peer transport.Peer, streamMux *mux.Mux) {
	id := peer.Snapshot().AgentID
	m.mu.Lock()
	if parent := peer.Snapshot().Via; parent != "" {
		if parent == id {
			m.mu.Unlock()
			log.Printf("relay agent %s rejected: cannot relay through itself", id)
			streamMux.Close()
			return
		}
		ancestor := parent
		depth := 0
		for ancestor != "" {
			state := m.agents[ancestor]
			if state == nil || ancestor == id || depth >= 8 {
				m.mu.Unlock()
				if state == nil {
					log.Printf("relay agent %s rejected: parent %s is offline", id, ancestor)
				} else if ancestor == id {
					log.Printf("relay agent %s rejected: parent loop", id)
				} else {
					log.Printf("relay agent %s rejected: maximum relay depth of 8 exceeded", id)
				}
				streamMux.Close()
				return
			}
			depth++
			ancestor = state.inventory.Via
		}
	}
	virtual := m.virtualByAgent[id]
	if !virtual.IsValid() {
		for ip := m.virtualNetwork.Addr().Next(); m.virtualNetwork.Contains(ip); ip = ip.Next() {
			if !ip.Next().IsValid() || !m.virtualNetwork.Contains(ip.Next()) {
				break
			}
			if ip == m.proxyIP || m.virtualUsed[ip] {
				continue
			}
			virtual = ip
			m.virtualByAgent[id] = ip
			m.virtualUsed[ip] = true
			break
		}
	}
	if !virtual.IsValid() {
		m.mu.Unlock()
		log.Printf("agent %s rejected: no virtual IP available", id)
		streamMux.Close()
		return
	}
	peer.SetVirtualIP(virtual.String())
	old := m.agents[id]
	for _, route := range m.routes.List() {
		if route.AgentID != id || !route.Active {
			continue
		}
		if m.device != nil {
			if err := m.device.DelRoute(route.Prefix.String()); err != nil {
				log.Printf("route cleanup %s: %v", route.Prefix, err)
			}
		}
		m.routes.SetRouteActive(route.Prefix, false)
	}
	depth := 0
	if parent := peer.Snapshot().Via; parent != "" {
		depth = m.agents[parent].inventory.Depth + 1
	}
	privilege := ""
	if old != nil {
		privilege = old.privilege
	} else if snapshot, found := m.offlineAgents[id]; found {
		privilege = snapshot.Privilege
	}
	m.agents[id] = &agentState{peer: peer, mux: streamMux, privilege: privilege, inventory: AgentInfo{Via: peer.Snapshot().Via, RelayBind: peer.Snapshot().RelayBind, Depth: depth}}
	delete(m.offlineAgents, id)
	m.recordLifecycleLocked(LifecycleEvent{AgentID: id, Kind: "connected", Transport: peer.Snapshot().Carrier, SessionID: peer.Snapshot().ID})
	m.mu.Unlock()
	m.PublishEvent("agent.connected", id)
	if old != nil {
		old.mux.Close()
	}
	go m.receiveInventory(id, streamMux)
	go m.serveAgentForwards(id, streamMux)
	go func() { <-streamMux.Done(); m.Unregister(id, streamMux) }()
}

func (m *Manager) receiveInventory(id string, streamMux *mux.Mux) {
	for {
		b, err := streamMux.RecvControl(context.Background())
		if err != nil {
			return
		}
		m.UpdateInventory(id, streamMux, b)
	}
}

// UpdateInventory applies the first agent control message and later refreshes.
func (m *Manager) UpdateInventory(id string, streamMux *mux.Mux, b []byte) {
	var update networkRouteUpdate
	if json.Unmarshal(b, &update) == nil && update.RouteUpdate {
		m.updateNetworkRoutes(id, streamMux, update)
		return
	}
	var info AgentInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return
	}
	if state := m.Get(id); state == streamMux {
		m.mu.RLock()
		bound := ""
		lookup := m.artifactLookup
		if current := m.agents[id]; current != nil && current.mux == streamMux {
			bound = current.peer.Snapshot().EnrollmentArtifactID
		}
		m.mu.RUnlock()
		if bound != "" && info.ArtifactID != bound {
			log.Printf("agent %s rejected: artifact enrollment does not match inventory", id)
			streamMux.Close()
			return
		}
		if lookup != nil && info.ArtifactID != "" {
			name, _, scoped := lookup(info.ArtifactID)
			if name == "" || (scoped && bound == "") {
				log.Printf("agent %s rejected: inventory artifact is not bound to its enrollment", id)
				streamMux.Close()
				return
			}
		}
	}
	for _, address := range info.Interfaces {
		_, value, ok := strings.Cut(address, "=")
		if !ok {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		prefix = prefix.Masked()
		if prefix.Contains(m.virtualNetwork.Addr()) || m.virtualNetwork.Contains(prefix.Addr()) {
			log.Printf("agent %s network %s conflicts with tunnel network %s", id, prefix, m.virtualNetwork)
			streamMux.Close()
			return
		}
	}
	validRoutes := make([]string, 0, len(info.AdvertisedRoutes))
	for _, raw := range info.AdvertisedRoutes {
		if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Addr().Is4() && prefix == prefix.Masked() {
			validRoutes = append(validRoutes, prefix.String())
		}
	}
	m.mu.Lock()
	if state := m.agents[id]; state != nil && state.mux == streamMux {
		if !state.inventoryReady {
			for i := len(m.lifecycleEvents) - 1; i >= 0; i-- {
				event := &m.lifecycleEvents[i]
				if event.AgentID == id && event.SessionID == state.peer.Snapshot().ID && event.Kind == "connected" {
					event.ProfileID, event.ArtifactID = info.ProfileID, info.ArtifactID
					break
				}
			}
		}
		if !state.inventoryReady && info.ReconnectAttempts > 0 {
			m.recordLifecycleLocked(LifecycleEvent{AgentID: id, Kind: "reconnect_observed", Transport: state.peer.Snapshot().Carrier, SessionID: state.peer.Snapshot().ID, ReconnectAttempts: info.ReconnectAttempts})
		}
		state.inventory.Hostname = safeHostname(info.Hostname)
		state.inventory.OS = info.OS
		state.inventory.Arch = info.Arch
		if info.Privilege == "high" || info.Privilege == "low" {
			state.privilege = info.Privilege
		}
		state.inventory.Interfaces = append([]string(nil), info.Interfaces...)
		state.inventory.AdvertisedRoutes = validRoutes
		state.inventory.Capabilities = info.Capabilities
		state.inventory.ArtifactIdentity = info.ArtifactIdentity
		if m.artifactLookup != nil && info.ArtifactID != "" {
			state.inventory.Profile, state.inventory.UndertowVersion, _ = m.artifactLookup(info.ArtifactID)
		}
		state.inventoryReady = true
		for _, route := range m.routes.List() {
			if route.AgentID != id || route.Active == agentPivotAllowed(state) {
				continue
			}
			if agentPivotAllowed(state) {
				if m.device != nil {
					if err := m.device.AddRoute(route.Prefix.String()); err != nil {
						log.Printf("route %s remains inactive: %v", route.Prefix, err)
						continue
					}
				}
				m.routes.SetRouteActive(route.Prefix, true)
			} else {
				if m.device != nil {
					if err := m.device.DelRoute(route.Prefix.String()); err != nil {
						log.Printf("route cleanup %s: %v", route.Prefix, err)
					}
				}
				m.routes.SetRouteActive(route.Prefix, false)
			}
		}
	}
	m.mu.Unlock()
	m.persistAgentSnapshot(id)
	m.PublishEvent("agent.updated", id)
}

func (m *Manager) updateNetworkRoutes(id string, streamMux *mux.Mux, update networkRouteUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.agents[id]
	if state == nil || state.mux != streamMux {
		return
	}
	if update.Reset {
		state.inventory.Routes = nil
		state.inventory.DefaultRoute = nil
	}
	if update.DefaultRoute != nil {
		if route, ok := validNetworkRoute(*update.DefaultRoute, true); ok {
			state.inventory.DefaultRoute = &route
		}
	}
	for _, candidate := range update.Routes {
		if len(state.inventory.Routes) >= 64 {
			break
		}
		route, ok := validNetworkRoute(candidate, false)
		if !ok {
			continue
		}
		prefix, _ := netip.ParsePrefix(route.Prefix)
		if prefix.Overlaps(m.virtualNetwork) {
			continue
		}
		duplicate := false
		for _, existing := range state.inventory.Routes {
			if existing.Prefix == route.Prefix && existing.Gateway == route.Gateway && existing.Interface == route.Interface {
				duplicate = true
				break
			}
		}
		if !duplicate {
			state.inventory.Routes = append(state.inventory.Routes, route)
		}
	}
}

func agentPivotAllowed(state *agentState) bool {
	if state == nil || !state.inventoryReady {
		return false
	}
	if state.inventory.Capabilities == nil {
		return true // Older agents did not report capability state.
	}
	for _, name := range state.inventory.Capabilities.Allowed {
		if name == "pivot" {
			return true
		}
	}
	return false
}

func IsVPNHello(b []byte) bool {
	var hello struct {
		Mode string `json:"mode"`
	}
	return json.Unmarshal(b, &hello) == nil && hello.Mode == "vpn"
}

func VPNInternal(b []byte) bool {
	var hello struct {
		Internal bool `json:"internal"`
	}
	_ = json.Unmarshal(b, &hello)
	return hello.Internal
}

func VPNEnabled(b []byte) bool {
	var hello struct {
		VPN bool `json:"vpn"`
	}
	_ = json.Unmarshal(b, &hello)
	return hello.VPN
}

func VPNHostname(b []byte) string {
	var hello struct {
		Hostname string `json:"hostname"`
	}
	if json.Unmarshal(b, &hello) != nil {
		return ""
	}
	return safeHostname(hello.Hostname)
}

func safeHostname(value string) string {
	if len(value) > 253 {
		return ""
	}
	for _, char := range value {
		if char <= ' ' || char == 0x7f {
			return ""
		}
	}
	return value
}

func (m *Manager) Unregister(id string, streamMux *mux.Mux) {
	var last AgentInfo
	for _, agent := range m.AgentList() {
		if agent.ID == id {
			last = agent
			break
		}
	}
	m.mu.Lock()
	if state := m.agents[id]; state == nil || state.mux != streamMux {
		m.mu.Unlock()
		return
	}
	state := m.agents[id]
	peerInfo := state.peer.Snapshot()
	duration := int64(0)
	if !peerInfo.Connected.IsZero() {
		duration = int64(time.Since(peerInfo.Connected).Seconds())
	}
	m.recordLifecycleLocked(LifecycleEvent{AgentID: id, Kind: "disconnected", Transport: peerInfo.Carrier, ProfileID: state.inventory.ProfileID, ArtifactID: state.inventory.ArtifactID, SessionID: peerInfo.ID, DurationSeconds: duration})
	delete(m.agents, id)
	last.Online = false
	last.Offline = true
	last.DisconnectedAt = time.Now().UTC()
	last.Streams = 0
	last.ActiveForwards = 0
	last.ActiveJobs = 0
	if last.ID != "" {
		m.offlineAgents[id] = last
	}
	store := m.operations
	var closed []*mux.Stream
	var descendants []struct {
		id        string
		streamMux *mux.Mux
	}
	for childID, state := range m.agents {
		if state.inventory.Via == id {
			descendants = append(descendants, struct {
				id        string
				streamMux *mux.Mux
			}{childID, state.mux})
		}
	}
	for _, relay := range m.relays[id] {
		closed = append(closed, relay.stream)
	}
	delete(m.relays, id)
	for forwardID, forward := range m.forwards {
		if forward.AgentID == id {
			closed = append(closed, forward.control)
			delete(m.forwards, forwardID)
		}
	}
	for _, r := range m.routes.List() {
		if r.AgentID == id && r.Active {
			if m.device != nil {
				if err := m.device.DelRoute(r.Prefix.String()); err != nil {
					log.Printf("route cleanup %s: %v", r.Prefix, err)
				}
			}
			m.routes.SetRouteActive(r.Prefix, false)
		}
	}
	m.mu.Unlock()
	if store != nil && last.ID != "" {
		if err := store.SaveAgentSnapshot(last); err != nil {
			log.Printf("save disconnected agent %s: %v", id, err)
		}
	}
	m.PublishEvent("agent.disconnected", id)
	for _, stream := range closed {
		_ = stream.Close()
	}
	for _, descendant := range descendants {
		_ = descendant.streamMux.Close()
		m.Unregister(descendant.id, descendant.streamMux)
	}
}

func (m *Manager) Choose(destination netip.Addr) *mux.Mux {
	id, ok := m.routes.Resolve(destination)
	if !ok {
		return nil
	}
	return m.Get(id)
}

func (m *Manager) ResolveEgress(destination netip.Addr) (*mux.Mux, bool) {
	route, configured := m.routes.Lookup(destination)
	if !configured {
		return nil, false
	}
	if !route.Active {
		return nil, true
	}
	return m.Get(route.AgentID), true
}

func (m *Manager) Get(id string) *mux.Mux {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if state := m.agents[id]; state != nil {
		return state.mux
	}
	return nil
}

func (m *Manager) Only() *mux.Mux {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.agents) != 1 {
		return nil
	}
	for _, state := range m.agents {
		return state.mux
	}
	return nil
}

func (m *Manager) AgentList() []AgentInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AgentInfo, 0, len(m.agents))
	for _, state := range m.agents {
		p := state.peer.Snapshot()
		info := state.inventory
		info.ID = p.AgentID
		info.Online = true
		info.Offline = false
		info.DisconnectedAt = time.Time{}
		info.Privilege = state.privilege
		info.Nickname = m.nicknames[info.ID]
		info.Transport = p.Carrier
		info.Via = p.Via
		info.RelayBind = p.RelayBind
		info.SessionID = p.ID
		info.VirtualIP = p.VirtualIP
		info.Remote = p.Remote
		info.PublicIP = observedPublicIP(p.Remote)
		info.Connected = p.Connected
		info.LastSeen = p.LastSeen
		info.RTT = p.Transport.RTT
		info.RXBytes = p.Transport.RXBytes
		info.TXBytes = p.Transport.TXBytes
		info.Retransmits = p.Transport.Retransmits
		info.Duplicates = p.Transport.Duplicates
		now := time.Now()
		if elapsed := now.Sub(state.rateAt); !state.rateAt.IsZero() && elapsed >= 250*time.Millisecond {
			state.rxRate = float64(p.Transport.RXBytes-state.rateRX) / elapsed.Seconds()
			state.txRate = float64(p.Transport.TXBytes-state.rateTX) / elapsed.Seconds()
		}
		if state.rateAt.IsZero() || now.Sub(state.rateAt) >= 250*time.Millisecond {
			state.rateAt, state.rateRX, state.rateTX = now, p.Transport.RXBytes, p.Transport.TXBytes
		}
		info.RXRate, info.TXRate = state.rxRate, state.txRate
		info.Streams = state.mux.StreamCount()
		info.InFlight = p.Transport.InFlight
		info.Queued = p.Transport.Queued
		info.Window = p.Transport.CongestionWindow
		info.ReceiveWindow = p.Transport.ReceiveWindow
		info.PeerReceiveWindow = p.Transport.PeerReceiveWindow
		info.FragmentSize = p.Transport.FragmentSize
		info.PayloadAdjustments = p.Transport.PayloadAdjustments
		for _, forward := range m.forwards {
			if forward.AgentID == info.ID {
				info.ActiveForwards++
			}
		}
		for _, job := range m.jobs {
			if job.info.AgentID == info.ID && job.info.State == "running" {
				info.ActiveJobs++
			}
		}
		out = append(out, info)
	}
	return out
}

// AgentCatalog adds retained disconnected agents to the live peer list.
// Operational lookups continue to use m.agents and cannot target a snapshot.
func (m *Manager) AgentCatalog() []AgentInfo {
	live := m.AgentList()
	seen := make(map[string]bool, len(live))
	for _, agent := range live {
		seen[agent.ID] = true
	}
	m.mu.RLock()
	for id, agent := range m.offlineAgents {
		if !seen[id] {
			agent.Nickname = m.nicknames[id]
			live = append(live, agent)
		}
	}
	m.mu.RUnlock()
	sort.Slice(live, func(i, j int) bool {
		if live[i].Online != live[j].Online {
			return live[i].Online
		}
		if live[i].Hostname != live[j].Hostname {
			return live[i].Hostname < live[j].Hostname
		}
		return live[i].ID < live[j].ID
	})
	return live
}

func (m *Manager) persistAgentSnapshot(id string) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		return
	}
	for _, agent := range m.AgentList() {
		if agent.ID == id {
			if err := store.SaveAgentSnapshot(agent); err != nil {
				log.Printf("save agent %s: %v", id, err)
			}
			return
		}
	}
}

func observedPublicIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return ""
	}
	return ip.String()
}

func (m *Manager) AddRoute(prefix netip.Prefix, agentID string) error {
	if agentID == "" {
		m.mu.RLock()
		agentID = m.selected
		m.mu.RUnlock()
	}
	if agentID == "" {
		return errors.New("agent ID required; use --via or agent select")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.routes.Add(prefix, agentID); err != nil {
		return err
	}
	state := m.agents[agentID]
	active := agentPivotAllowed(state)
	if active {
		if m.device != nil {
			if err := m.device.AddRoute(prefix.Masked().String()); err != nil {
				m.routes.Delete(prefix)
				return err
			}
		}
		m.routes.SetRouteActive(prefix, true)
	}
	return nil
}

func (m *Manager) DeleteRoute(prefix netip.Prefix) error {
	prefix = prefix.Masked()
	for _, r := range m.routes.List() {
		if r.Prefix != prefix {
			continue
		}
		if r.Active && m.device != nil {
			if err := m.device.DelRoute(prefix.String()); err != nil {
				return err
			}
		}
		m.routes.Delete(prefix)
		return nil
	}
	return errors.New("route not found")
}

func (m *Manager) Select(id string) error {
	if m.Get(id) == nil {
		return errors.New("agent is not connected")
	}
	m.mu.Lock()
	m.selected = id
	m.mu.Unlock()
	return nil
}

func (m *Manager) Kill(id string) error {
	streamMux := m.Get(id)
	if streamMux == nil {
		return errors.New("agent is not connected")
	}
	return streamMux.Close()
}

func (m *Manager) ShutdownAgent(ctx context.Context, id string) error {
	streamMux := m.Get(id)
	if streamMux == nil {
		return errors.New("agent is not connected")
	}
	m.mu.Lock()
	if state := m.agents[id]; state == nil || state.mux != streamMux || state.inventory.ArtifactID == "" {
		m.mu.Unlock()
		return errors.New("agent shutdown requires a connected configured agent; use session kill to close a manual agent session")
	}
	if state := m.agents[id]; state != nil && state.mux == streamMux {
		m.recordLifecycleLocked(LifecycleEvent{AgentID: id, Kind: "shutdown_requested", Transport: state.peer.Snapshot().Carrier, ArtifactID: state.inventory.ArtifactID, ProfileID: state.inventory.ProfileID, SessionID: state.peer.Snapshot().ID})
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stream, err := streamMux.Open(ctx, pivot.ShutdownDestination)
	if err != nil {
		return err
	}
	defer stream.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stream.Close()
		case <-done:
		}
	}()
	var ack [1]byte
	if _, err = io.ReadFull(stream, ack[:]); err != nil {
		return err
	}
	if ack[0] != 1 {
		return errors.New("invalid agent shutdown acknowledgement")
	}
	if _, err := stream.Write([]byte{1}); err != nil {
		return err
	}
	if _, err := io.ReadFull(stream, ack[:]); err != nil {
		return err
	}
	if ack[0] != 2 {
		return errors.New("invalid agent shutdown completion")
	}
	if _, err := stream.Read(ack[:]); err != io.EOF {
		return errors.New("agent shutdown stream did not finish")
	}
	m.mu.Lock()
	if state := m.agents[id]; state != nil && state.mux == streamMux {
		m.recordLifecycleLocked(LifecycleEvent{AgentID: id, Kind: "shutdown_acknowledged", Transport: state.peer.Snapshot().Carrier, ArtifactID: state.inventory.ArtifactID, ProfileID: state.inventory.ProfileID, SessionID: state.peer.Snapshot().ID})
	}
	m.mu.Unlock()
	return stream.CloseWrite()
}

// LoadOrCreateToken keeps the local operator credential separate from the
// agent enrolment token.
func LoadOrCreateToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) != 64 {
			return "", errors.New("invalid control token")
		}
		if _, err = hex.DecodeString(token); err != nil {
			return "", errors.New("invalid control token")
		}
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return LoadOrCreateToken(path)
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err = io.WriteString(f, token+"\n"); err != nil {
		return "", err
	}
	return token, nil
}

func (m *Manager) ServeHTTP(ctx context.Context, address, token string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("control listener must bind a numeric loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: m.handler(token), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("local control API listening on %s", listener.Addr())
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (m *Manager) handler(token string) http.Handler {
	muxer := http.NewServeMux()
	m.registerTransferHandlers(muxer)
	muxer.Handle("/v1/agent-profiles/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		handler := m.agentDistribution
		m.mu.RUnlock()
		if handler == nil {
			http.Error(w, "agent distribution unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	muxer.Handle("/v1/agent-profiles", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		handler := m.agentDistribution
		m.mu.RUnlock()
		if handler == nil {
			http.Error(w, "agent distribution unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	muxer.Handle("/v1/agent-artifacts/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		handler := m.agentDistribution
		m.mu.RUnlock()
		if handler == nil {
			http.Error(w, "agent distribution unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	muxer.Handle("/v1/agent-artifacts", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		handler := m.agentDistribution
		m.mu.RUnlock()
		if handler == nil {
			http.Error(w, "agent distribution unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	for _, path := range []string{"/v1/agent-hosts", "/v1/agent-hosts/"} {
		muxer.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.mu.RLock()
			handler := m.agentDistribution
			m.mu.RUnlock()
			if handler == nil {
				http.Error(w, "agent distribution unavailable", http.StatusServiceUnavailable)
				return
			}
			handler.ServeHTTP(w, r)
		}))
	}
	muxer.Handle("/v1/payload-retrieval-path", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		handler := m.agentDistribution
		m.mu.RUnlock()
		if handler == nil {
			http.Error(w, "payload distribution unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	muxer.Handle("/v1/payload-retrieval-host", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		handler := m.agentDistribution
		m.mu.RUnlock()
		if handler == nil {
			http.Error(w, "payload distribution unavailable", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	m.jobHTTPHandlers(muxer)
	muxer.HandleFunc("GET /v1/history", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		store := m.operations
		m.mu.RUnlock()
		if store == nil {
			jsonReply(w, http.StatusOK, []AuditRecord{})
			return
		}
		records, err := store.AuditHistory(200)
		if err != nil {
			http.Error(w, "history unavailable", http.StatusInternalServerError)
			return
		}
		jsonReply(w, http.StatusOK, records)
	})
	muxer.HandleFunc("GET /v1/topology", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, http.StatusOK, m.Topology())
	})
	muxer.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		selected := m.selected
		server := m.server
		m.mu.RUnlock()
		if m.transports != nil {
			server.Listeners = m.transports.List()
			server.Transport, server.Network, server.Listen, server.TLSMode = "", "", "", ""
			if len(server.Listeners) == 1 {
				listener := server.Listeners[0]
				server.Transport, server.Network, server.Listen, server.TLSMode = listener.Transport, listener.Network, listener.Listen, listener.TLSMode
			}
		}
		jsonReply(w, http.StatusOK, map[string]any{"server": server, "agents": m.AgentCatalog(), "clients": m.ClientList(), "routes": m.routes.List(), "selected_agent": selected})
	})
	muxer.HandleFunc("GET /v1/transports", func(w http.ResponseWriter, r *http.Request) {
		if m.transports == nil {
			http.Error(w, "transport management unavailable", http.StatusServiceUnavailable)
			return
		}
		jsonReply(w, http.StatusOK, m.transports.List())
	})
	muxer.HandleFunc("GET /v1/server-public-host", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, http.StatusOK, map[string]string{"host": m.ServerInfo().PublicHost})
	})
	muxer.HandleFunc("PUT /v1/server-public-host", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Host string `json:"host"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request); err != nil || !validServerPublicHost(request.Host) {
			http.Error(w, "public host must be an IP address or DNS hostname without a port", http.StatusBadRequest)
			return
		}
		m.mu.RLock()
		store := m.operations
		m.mu.RUnlock()
		if store == nil {
			http.Error(w, "server settings unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := store.SetPublicHost(request.Host); err != nil {
			http.Error(w, "could not save public host", http.StatusInternalServerError)
			return
		}
		m.SetPublicHost(request.Host)
		jsonReply(w, http.StatusOK, map[string]string{"host": request.Host})
	})
	muxer.HandleFunc("GET /v1/worker-logs", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		logs := m.workerLogs
		m.mu.RUnlock()
		if logs == nil {
			http.Error(w, "worker logs unavailable", http.StatusServiceUnavailable)
			return
		}
		after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		if r.URL.Query().Get("after") == "" {
			after = 0
			err = nil
		}
		if err != nil {
			http.Error(w, "invalid log cursor", http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusOK, logs.Snapshot(after))
	})
	muxer.HandleFunc("POST /v1/transports/{name}", func(w http.ResponseWriter, r *http.Request) {
		if m.transports == nil {
			http.Error(w, "transport management unavailable", http.StatusServiceUnavailable)
			return
		}
		var request TransportStartRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&request); err != nil {
			http.Error(w, "invalid transport request", http.StatusBadRequest)
			return
		}
		info, err := m.transports.Start(r.PathValue("name"), request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		jsonReply(w, http.StatusCreated, info)
	})
	muxer.HandleFunc("DELETE /v1/transports/{name}", func(w http.ResponseWriter, r *http.Request) {
		if m.transports == nil {
			http.Error(w, "transport management unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := m.transports.Stop(r.PathValue("name"), r.URL.Query().Get("force") == "true"); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("GET /v1/relays", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, http.StatusOK, m.RelayList(r.URL.Query().Get("agent_id")))
	})
	muxer.HandleFunc("POST /v1/agents/{id}/relays", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Bind string `json:"bind"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil {
			http.Error(w, "invalid relay request", http.StatusBadRequest)
			return
		}
		info, err := m.StartRelay(r.Context(), r.PathValue("id"), body.Bind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		jsonReply(w, http.StatusCreated, info)
	})
	muxer.HandleFunc("DELETE /v1/agents/{id}/relays", func(w http.ResponseWriter, r *http.Request) {
		if err := m.StopRelay(r.PathValue("id"), r.URL.Query().Get("bind")); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("GET /v1/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		for _, a := range m.AgentList() {
			if a.ID == r.PathValue("id") {
				a.Forwards = m.AgentForwards(a.ID)
				jsonReply(w, http.StatusOK, a)
				return
			}
		}
		http.Error(w, "agent not found", http.StatusNotFound)
	})
	muxer.HandleFunc("PUT /v1/agents/{id}/nickname", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Nickname string `json:"nickname"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 512))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid nickname request", http.StatusBadRequest)
			return
		}
		if err := m.SetAgentNickname(r.PathValue("id"), request.Nickname); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusOK, map[string]string{"nickname": strings.TrimSpace(request.Nickname)})
	})
	muxer.HandleFunc("GET /v1/agents/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, http.StatusOK, m.LifecycleEvents(r.PathValue("id")))
	})
	muxer.HandleFunc("GET /v1/agents/{id}/host-results", m.hostResultsHandler)
	muxer.HandleFunc("GET /v1/agents/{id}/files", m.filesHandler)
	muxer.HandleFunc("GET /v1/agents/{id}/screens", m.screensHandler)
	muxer.HandleFunc("POST /v1/agents/{id}/screenshots", m.captureScreenshotHandler)
	muxer.HandleFunc("GET /v1/screenshots", m.screenshotsHandler)
	muxer.HandleFunc("GET /v1/screenshots/{id}", m.screenshotHandler)
	muxer.HandleFunc("GET /v1/screenshots/{id}/chunk", m.screenshotChunkHandler)
	muxer.HandleFunc("POST /v1/agents/{id}/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if err := m.ShutdownAgent(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("POST /v1/agents/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		agent := m.Get(r.PathValue("id"))
		if agent == nil {
			http.Error(w, "agent is not connected", http.StatusNotFound)
			return
		}
		var request pivot.ExecRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8193)).Decode(&request); err != nil {
			http.Error(w, "invalid command request", http.StatusBadRequest)
			return
		}
		result, err := pivot.ExecuteRequest(r.Context(), agent, request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if retainedHostOperation(request.Builtin) && len(request.Args) == 0 {
			m.retainHostResult(r.PathValue("id"), request.Builtin, result)
		}
		jsonReply(w, http.StatusOK, result)
	})
	muxer.HandleFunc("CONNECT /v1/agents/{id}/interactive", m.interactiveHandler)
	muxer.HandleFunc("CONNECT /v1/agents/{id}/script", m.interactiveHandler)
	muxer.HandleFunc("CONNECT /v1/agents/{id}/wasm", m.interactiveHandler)
	muxer.HandleFunc("CONNECT /v1/agents/{id}/native", m.interactiveHandler)
	muxer.HandleFunc("CONNECT /v1/agents/{id}/bof", m.interactiveHandler)
	muxer.HandleFunc("POST /v1/selection", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AgentID string `json:"agent_id"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if err := m.Select(body.AgentID); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("POST /v1/clients/{id}/internal", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err != nil || json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body) != nil {
			http.Error(w, "invalid client setting", http.StatusBadRequest)
			return
		}
		if err := m.SetClientInternal(id, body.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("POST /v1/clients/{id}/vpn", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err != nil || json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body) != nil {
			http.Error(w, "invalid client setting", http.StatusBadRequest)
			return
		}
		if err := m.SetClientVPN(id, body.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("POST /v1/clients/{id}/routes", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		var body AcceptedRoute
		if err != nil || json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&body) != nil {
			http.Error(w, "invalid client route", http.StatusBadRequest)
			return
		}
		prefix, err := netip.ParsePrefix(body.Prefix)
		if err == nil {
			err = m.SetClientRoute(id, prefix, body.AgentID, body.Manual)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	muxer.HandleFunc("DELETE /v1/clients/{id}/routes", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid client ID", http.StatusBadRequest)
			return
		}
		prefix, err := netip.ParsePrefix(r.URL.Query().Get("prefix"))
		if err == nil {
			err = m.DeleteClientRoute(id, prefix)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("GET /v1/clients/{id}/forwards", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid client ID", http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusOK, m.ClientForwards(id))
	})
	muxer.HandleFunc("POST /v1/clients/{id}/forwards", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		var body struct {
			AgentID string `json:"agent_id"`
			Bind    string `json:"bind"`
			Target  string `json:"target"`
		}
		if err != nil || json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&body) != nil {
			http.Error(w, "invalid forward request", http.StatusBadRequest)
			return
		}
		info, err := m.AddClientForward(r.Context(), id, body.AgentID, body.Bind, body.Target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusCreated, info)
	})
	muxer.HandleFunc("DELETE /v1/clients/{id}/forwards", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
		if err == nil {
			err = m.DeleteClientForward(id, r.URL.Query().Get("agent_id"), r.URL.Query().Get("bind"))
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("POST /v1/routes", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prefix  string `json:"prefix"`
			AgentID string `json:"agent_id"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&body) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		prefix, err := netip.ParsePrefix(body.Prefix)
		if err == nil {
			err = m.AddRoute(prefix, body.AgentID)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	muxer.HandleFunc("DELETE /v1/routes", func(w http.ResponseWriter, r *http.Request) {
		prefix, err := netip.ParsePrefix(r.URL.Query().Get("prefix"))
		if err == nil {
			err = m.DeleteRoute(prefix)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("POST /v1/sessions/{id}/kill", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Kill(r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete) && !(r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/transfers/")) {
			m.mu.RLock()
			store := m.operations
			m.mu.RUnlock()
			var auditID string
			if store != nil {
				var bytes [16]byte
				if _, err := rand.Read(bytes[:]); err != nil {
					http.Error(w, "audit unavailable", http.StatusInternalServerError)
					return
				}
				auditID = hex.EncodeToString(bytes[:])
				actor := boundActionFromContext(r.Context())
				source, trust := actor.Source, "local_unverified"
				if source == "" {
					source = "server_console"
				}
				if actor.ClientID != "" {
					trust = "client_bound_operator_claim"
				}
				record := AuditRecord{ID: auditID, ActionID: actor.ActionID, At: time.Now().UTC(), Action: r.Method, Target: r.URL.Path, ClientID: actor.ClientID, ClientSessionID: actor.ClientSessionID, OperatorID: actor.OperatorID, DisplayName: actor.DisplayName, Source: source, IdentityTrust: trust}
				if err := store.RecordAudit(record); err != nil {
					log.Printf("audit start: %v", err)
					http.Error(w, "audit unavailable", http.StatusInternalServerError)
					return
				}
			}
			tracked := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
			muxer.ServeHTTP(tracked, r)
			if store != nil {
				if err := store.CompleteAudit(auditID, tracked.status); err != nil {
					log.Printf("audit result: %v", err)
				}
				m.PublishEvent("audit.recorded", auditID)
			}
			if tracked.status < 400 {
				m.PublishEvent("operation.changed", r.URL.Path)
			}
			return
		}
		muxer.ServeHTTP(w, r)
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func jsonReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("control response: %v", err)
	}
}

func (m *Manager) String() string {
	return fmt.Sprintf("%d agents, %d routes", len(m.AgentList()), len(m.routes.List()))
}
