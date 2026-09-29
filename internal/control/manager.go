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
	"strings"
	"sync"
	"time"

	"undertow/internal/mux"
	"undertow/internal/routing"
	"undertow/internal/transport/dns"
)

type RouteDevice interface {
	AddRoute(string) error
	DelRoute(string) error
}

type AgentInfo struct {
	ID          string        `json:"id"`
	SessionID   uint64        `json:"session_id"`
	VirtualIP   string        `json:"virtual_ip"`
	Remote      string        `json:"remote"`
	Hostname    string        `json:"hostname,omitempty"`
	OS          string        `json:"os,omitempty"`
	Arch        string        `json:"arch,omitempty"`
	Interfaces  []string      `json:"interfaces,omitempty"`
	Connected   time.Time     `json:"connected"`
	LastSeen    time.Time     `json:"last_seen"`
	RTT         time.Duration `json:"rtt_ns"`
	RXBytes     uint64        `json:"rx_bytes"`
	TXBytes     uint64        `json:"tx_bytes"`
	Retransmits uint64        `json:"retransmits"`
	Streams     int           `json:"streams"`
}

type agentState struct {
	peer      *dns.Peer
	mux       *mux.Mux
	inventory AgentInfo
}
type Manager struct {
	mu             sync.RWMutex
	agents         map[string]*agentState
	routes         *routing.Table
	device         RouteDevice
	selected       string
	virtualNetwork netip.Prefix
	proxyIP        netip.Addr
	virtualByAgent map[string]netip.Addr
	virtualUsed    map[netip.Addr]bool
}

func NewManager(routes *routing.Table, device RouteDevice, virtualNetwork netip.Prefix, proxyIP netip.Addr) *Manager {
	return &Manager{agents: make(map[string]*agentState), routes: routes, device: device, virtualNetwork: virtualNetwork.Masked(), proxyIP: proxyIP, virtualByAgent: make(map[string]netip.Addr), virtualUsed: make(map[netip.Addr]bool)}
}

func (m *Manager) Register(peer *dns.Peer, streamMux *mux.Mux) {
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
	m.agents[id] = &agentState{peer: peer, mux: streamMux}
	m.mu.Unlock()
	if old != nil {
		old.mux.Close()
	}
	for _, r := range m.routes.List() {
		if r.AgentID != id {
			continue
		}
		if r.Active {
			continue
		}
		if m.device != nil {
			if err := m.device.AddRoute(r.Prefix.String()); err != nil {
				log.Printf("route %s remains inactive: %v", r.Prefix, err)
				continue
			}
		}
		m.routes.SetRouteActive(r.Prefix, true)
	}
	go m.receiveInventory(id, streamMux)
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
	m.mu.Lock()
	if state := m.agents[id]; state != nil && state.mux == streamMux {
		state.inventory.Hostname = info.Hostname
		state.inventory.OS = info.OS
		state.inventory.Arch = info.Arch
		state.inventory.Interfaces = append([]string(nil), info.Interfaces...)
	}
	m.mu.Unlock()
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

func (m *Manager) Unregister(id string, streamMux *mux.Mux) {
	m.mu.Lock()
	if state := m.agents[id]; state == nil || state.mux != streamMux {
		m.mu.Unlock()
		return
	}
	delete(m.agents, id)
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
	m.mu.RLock()
	defer m.mu.RUnlock()
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
		info.Streams = state.mux.StreamCount()
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
	if err := m.routes.Add(prefix, agentID); err != nil {
		return err
	}
	if m.Get(agentID) != nil {
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
	muxer.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		selected := m.selected
		m.mu.RUnlock()
		jsonReply(w, http.StatusOK, map[string]any{"agents": m.AgentList(), "routes": m.routes.List(), "selected_agent": selected})
	})
	muxer.HandleFunc("GET /v1/agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		for _, a := range m.AgentList() {
			if a.ID == r.PathValue("id") {
				jsonReply(w, http.StatusOK, a)
				return
			}
		}
		http.Error(w, "agent not found", http.StatusNotFound)
	})
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
