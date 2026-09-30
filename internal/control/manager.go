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
	ID                 string                  `json:"id"`
	SessionID          uint64                  `json:"session_id"`
	VirtualIP          string                  `json:"virtual_ip"`
	Remote             string                  `json:"remote"`
	Hostname           string                  `json:"hostname,omitempty"`
	OS                 string                  `json:"os,omitempty"`
	Arch               string                  `json:"arch,omitempty"`
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

type ClientInfo struct {
	ID             string          `json:"id"`
	SessionID      uint64          `json:"session_id"`
	Hostname       string          `json:"hostname,omitempty"`
	Remote         string          `json:"remote"`
	Internal       bool            `json:"internal"`
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
}

type ServerInfo struct {
	Transport     string `json:"transport"`
	Network       string `json:"network"`
	Listen        string `json:"listen"`
	Domain        string `json:"domain,omitempty"`
	WebSocketPath string `json:"websocket_path,omitempty"`
	TLSMode       string `json:"tls_mode,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
}

type clientState struct {
	peer     transport.Peer
	mux      *mux.Mux
	internal bool
	hostname string
	accepted map[netip.Prefix]AcceptedRoute
}

type agentState struct {
	peer           transport.Peer
	mux            *mux.Mux
	inventory      AgentInfo
	inventoryReady bool
	rateAt         time.Time
	rateRX         uint64
	rateTX         uint64
	rxRate         float64
	txRate         float64
}
type Manager struct {
	mu             sync.RWMutex
	server         ServerInfo
	agents         map[string]*agentState
	clients        map[uint64]*clientState
	forwards       map[string]*forwardState
	jobs           map[string]*jobState
	routes         *routing.Table
	device         RouteDevice
	selected       string
	virtualNetwork netip.Prefix
	proxyIP        netip.Addr
	virtualByAgent map[string]netip.Addr
	virtualUsed    map[netip.Addr]bool
}

func (m *Manager) SetServerInfo(info ServerInfo) {
	m.mu.Lock()
	m.server = info
	m.mu.Unlock()
}

func NewManager(routes *routing.Table, device RouteDevice, virtualNetwork netip.Prefix, proxyIP netip.Addr) *Manager {
	return &Manager{agents: make(map[string]*agentState), clients: make(map[uint64]*clientState), forwards: make(map[string]*forwardState), jobs: make(map[string]*jobState), routes: routes, device: device, virtualNetwork: virtualNetwork.Masked(), proxyIP: proxyIP, virtualByAgent: make(map[string]netip.Addr), virtualUsed: make(map[netip.Addr]bool)}
}

func (m *Manager) RegisterClient(peer transport.Peer, streamMux *mux.Mux, internal bool, hostname string) {
	m.mu.Lock()
	m.clients[peer.Snapshot().ID] = &clientState{peer: peer, mux: streamMux, internal: internal, hostname: safeHostname(hostname), accepted: make(map[netip.Prefix]AcceptedRoute)}
	m.mu.Unlock()
}

func (m *Manager) UnregisterClient(sessionID uint64, streamMux *mux.Mux) {
	m.mu.Lock()
	var closed []*mux.Stream
	if state := m.clients[sessionID]; state != nil && state.mux == streamMux {
		delete(m.clients, sessionID)
		for id, forward := range m.forwards {
			if forward.ClientID == sessionID {
				closed = append(closed, forward.control)
				delete(m.forwards, id)
			}
		}
	}
	m.mu.Unlock()
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
	defer m.mu.Unlock()
	state := m.clients[sessionID]
	if state == nil {
		return errors.New("VPN client is not connected")
	}
	state.internal = enabled
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
			ID: p.AgentID, SessionID: p.ID, Hostname: state.hostname, Remote: p.Remote, Internal: state.internal,
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
	m.agents[id] = &agentState{peer: peer, mux: streamMux}
	m.mu.Unlock()
	if old != nil {
		old.mux.Close()
	}
	go m.receiveInventory(id, streamMux)
	go m.serveAgentForwards(streamMux)
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
		state.inventory.Hostname = safeHostname(info.Hostname)
		state.inventory.OS = info.OS
		state.inventory.Arch = info.Arch
		state.inventory.Interfaces = append([]string(nil), info.Interfaces...)
		state.inventory.AdvertisedRoutes = validRoutes
		state.inventory.Capabilities = info.Capabilities
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
	m.mu.Lock()
	if state := m.agents[id]; state == nil || state.mux != streamMux {
		m.mu.Unlock()
		return
	}
	delete(m.agents, id)
	var closed []*mux.Stream
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
	for _, stream := range closed {
		_ = stream.Close()
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
		info.SessionID = p.ID
		info.VirtualIP = p.VirtualIP
		info.Remote = p.Remote
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
	m.jobHTTPHandlers(muxer)
	muxer.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		selected := m.selected
		server := m.server
		m.mu.RUnlock()
		jsonReply(w, http.StatusOK, map[string]any{"server": server, "agents": m.AgentList(), "clients": m.ClientList(), "routes": m.routes.List(), "selected_agent": selected})
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
		jsonReply(w, http.StatusOK, result)
	})
	muxer.HandleFunc("CONNECT /v1/agents/{id}/interactive", m.interactiveHandler)
	muxer.HandleFunc("CONNECT /v1/agents/{id}/script", m.interactiveHandler)
	muxer.HandleFunc("CONNECT /v1/agents/{id}/wasm", m.interactiveHandler)
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
		muxer.ServeHTTP(w, r)
	})
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
