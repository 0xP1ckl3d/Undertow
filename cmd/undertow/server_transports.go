package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/transport/dns"
	"undertow/internal/transport/quic"
	"undertow/internal/transport/websocket"
)

type activeTransport struct {
	listener transport.Listener
	info     control.ListenerInfo
	peers    map[uint64]transport.Peer
}

type serverTransports struct {
	artifactHTTP       http.Handler
	enrollmentVerifier security.EnrollmentVerifier
	mu                 sync.Mutex
	ctx                context.Context
	manager            *control.Manager
	identity           ed25519.PrivateKey
	token              []byte
	domain, path       string
	handle             func(transport.Peer)
	active             map[string]*activeTransport
}

func (s *serverTransports) SetArtifactHandler(handler http.Handler) { s.artifactHTTP = handler }
func (s *serverTransports) SetEnrollmentVerifier(v security.EnrollmentVerifier) {
	s.enrollmentVerifier = v
}

func newServerTransports(ctx context.Context, manager *control.Manager, identity ed25519.PrivateKey, token []byte, domain, path string, handle func(transport.Peer)) *serverTransports {
	return &serverTransports{ctx: ctx, manager: manager, identity: identity, token: token, domain: domain, path: path, handle: handle, active: make(map[string]*activeTransport)}
}

func canonicalTransport(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "dns":
		return "dns", nil
	case "websocket", "ws":
		return "websocket", nil
	case "quic":
		return "quic", nil
	default:
		return "", fmt.Errorf("unknown transport %q; use dns, websocket, or quic", name)
	}
}

func (s *serverTransports) Start(name string, request control.TransportStartRequest) (control.ListenerInfo, error) {
	kind, err := canonicalTransport(name)
	if err != nil {
		return control.ListenerInfo{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[kind]; exists {
		return control.ListenerInfo{}, fmt.Errorf("%s is already listening", kind)
	}
	if s.ctx.Err() != nil {
		return control.ListenerInfo{}, errors.New("server is stopping")
	}
	addr := request.Listen
	if addr == "" {
		if kind == "dns" {
			addr = "0.0.0.0:53"
		} else {
			addr = "0.0.0.0:443"
		}
	}
	info := control.ListenerInfo{Transport: kind, Listen: addr, Network: "udp"}
	var listener transport.Listener
	if kind == "dns" {
		if request.TLSMode != "" || request.TLSCert != "" || request.TLSKey != "" {
			return control.ListenerInfo{}, errors.New("DNS does not use TLS options")
		}
		listener, err = dns.Listen(addr, s.domain, s.identity, s.token)
		if err == nil {
			listener.(*dns.Server).SetEnrollmentVerifier(s.enrollmentVerifier)
		}
	} else {
		if (request.TLSCert == "") != (request.TLSKey == "") {
			return control.ListenerInfo{}, errors.New("tls-cert and tls-key must be supplied together")
		}
		if request.TLSMode != "" && request.TLSMode != "self-signed" && request.TLSMode != "certificate" {
			return control.ListenerInfo{}, errors.New("TLS mode must be self-signed or certificate")
		}
		selfSigned := request.TLSCert == ""
		if request.TLSMode == "self-signed" && !selfSigned {
			return control.ListenerInfo{}, errors.New("self-signed TLS cannot use certificate files")
		}
		if request.TLSMode == "certificate" && selfSigned {
			return control.ListenerInfo{}, errors.New("certificate TLS requires tls-cert and tls-key")
		}
		info.TLSMode = "self-signed"
		if !selfSigned {
			info.TLSMode = "certificate"
		}
		if kind == "websocket" {
			info.Network = "tcp"
			listener, err = websocket.Listen(addr, s.path, request.TLSCert, request.TLSKey, selfSigned, s.identity, s.token)
			if err == nil {
				listener.(*websocket.Server).SetArtifactHandler(s.artifactHTTP)
				listener.(*websocket.Server).SetEnrollmentVerifier(s.enrollmentVerifier)
			}
		} else {
			listener, err = quic.Listen(addr, request.TLSCert, request.TLSKey, selfSigned, s.identity, s.token)
			if err == nil {
				listener.(*quic.Server).SetEnrollmentVerifier(s.enrollmentVerifier)
			}
		}
	}
	if err != nil {
		return control.ListenerInfo{}, fmt.Errorf("start %s on %s: %w", kind, addr, err)
	}
	info.Listen = listener.Addr().String()
	entry := &activeTransport{listener: listener, info: info, peers: make(map[uint64]transport.Peer)}
	s.active[kind] = entry
	go s.accept(kind, entry)
	go func() {
		if err := listener.Serve(s.ctx); err != nil && s.ctx.Err() == nil {
			log.Printf("%s listener stopped: %v", kind, err)
		}
		s.mu.Lock()
		if s.active[kind] == entry {
			delete(s.active, kind)
		}
		s.mu.Unlock()
	}()
	log.Printf("%s listening on %s", kind, info.Listen)
	return info, nil
}

func (s *serverTransports) accept(kind string, entry *activeTransport) {
	for {
		peer, err := entry.listener.Accept(s.ctx)
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.active[kind] != entry {
			s.mu.Unlock()
			peer.Channel().Close()
			return
		}
		entry.peers[peer.Snapshot().ID] = peer
		s.mu.Unlock()
		go s.handle(peer)
	}
}

func (s *serverTransports) List() []control.ListenerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	agents := s.manager.AgentList()
	clients := s.manager.ClientList()
	out := make([]control.ListenerInfo, 0, len(s.active))
	for _, entry := range s.active {
		info := entry.info
		for _, agent := range agents {
			if agent.Transport == info.Transport {
				info.Agents++
				info.Sessions++
			}
		}
		for _, client := range clients {
			if client.Transport == info.Transport {
				info.Clients++
				info.Sessions++
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Transport < out[j].Transport })
	return out
}

func (s *serverTransports) Stop(name string, force bool) error {
	kind, err := canonicalTransport(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	entry := s.active[kind]
	if entry == nil {
		s.mu.Unlock()
		return fmt.Errorf("%s is not listening", kind)
	}
	count, agents, clients := 0, 0, 0
	for _, agent := range s.manager.AgentList() {
		if agent.Transport == kind {
			agents++
			count++
		}
	}
	for _, client := range s.manager.ClientList() {
		if client.Transport == kind {
			clients++
			count++
		}
	}
	if count > 0 && !force {
		s.mu.Unlock()
		return fmt.Errorf("%s has %d active sessions (%d agents, %d clients); refusing to stop while sessions are active; use: stop transport %s force", strings.ToUpper(kind), count, agents, clients, kind)
	}
	delete(s.active, kind)
	s.mu.Unlock()
	_ = entry.listener.Close()
	if force {
		for _, peer := range entry.peers {
			_ = peer.Channel().Close()
		}
	}
	return nil
}

func (s *serverTransports) Close() {
	s.mu.Lock()
	listeners := make([]transport.Listener, 0, len(s.active))
	for _, entry := range s.active {
		listeners = append(listeners, entry.listener)
	}
	s.active = make(map[string]*activeTransport)
	s.mu.Unlock()
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func handleServerPeer(ctx context.Context, manager *control.Manager, controlToken string, probeEcho bool, peer transport.Peer) {
	log.Printf("session connected: %s from %s via %s", peer.Snapshot().AgentID, peer.Snapshot().Remote, peer.Snapshot().Carrier)
	if probeEcho {
		echo(ctx, peer)
		return
	}
	streamMux := mux.New(ctx, peer.Channel(), true)
	helloCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	hello, err := streamMux.RecvControl(helloCtx)
	cancel()
	if err != nil {
		log.Printf("session %d control handshake failed: %v", peer.Snapshot().ID, err)
		streamMux.Close()
		return
	}
	if control.IsVPNHello(hello) {
		if peer.Snapshot().EnrollmentArtifactID != "" {
			log.Printf("artifact enrollment rejected for VPN session %d", peer.Snapshot().ID)
			streamMux.Close()
			return
		}
		internal := control.VPNInternal(hello)
		log.Printf("VPN client ready: session=%d remote=%s internal=%t", peer.Snapshot().ID, peer.Snapshot().Remote, internal)
		if err := streamMux.SendControl(ctx, []byte(`{"mode":"vpn","ready":true}`)); err != nil {
			streamMux.Close()
			return
		}
		manager.RegisterClient(peer, streamMux, internal, control.VPNHostname(hello))
		pivot.ServeVPNInteractive(ctx, streamMux, func(destination netip.Addr) (*mux.Mux, bool) {
			return manager.ResolveClientEgress(peer.Snapshot().ID, destination)
		}, func() bool { return true }, func(ctx context.Context, stream *mux.Stream) {
			if stream.Destination() == pivot.FileDestination {
				manager.ServeFileRelay(ctx, stream)
			} else if stream.Destination() == pivot.InteractiveRelayDestination {
				manager.ServeInteractiveRelay(ctx, stream)
			} else {
				manager.ServeRemote(ctx, controlToken, peer.Snapshot().ID, stream)
			}
		})
		manager.UnregisterClient(peer.Snapshot().ID, streamMux)
		log.Printf("VPN client disconnected: session=%d", peer.Snapshot().ID)
		streamMux.Close()
		return
	}
	manager.Register(peer, streamMux)
	manager.UpdateInventory(peer.Snapshot().AgentID, streamMux, hello)
}
