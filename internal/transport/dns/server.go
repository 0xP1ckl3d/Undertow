package dns

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"undertow/internal/security"
	"undertow/internal/session"
)

type Peer struct {
	Session         *session.Session
	AgentID         string
	Remote          string
	Connected       time.Time
	LastSeen        time.Time
	VirtualIP       string
	transcript      []byte
	authenticated   bool
	packetErrorOnce sync.Once
	mu              sync.Mutex
}

func (p *Peer) Snapshot() PeerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PeerInfo{ID: p.Session.ID(), AgentID: p.AgentID, Remote: p.Remote, Connected: p.Connected, LastSeen: p.LastSeen, VirtualIP: p.VirtualIP, Authenticated: p.authenticated, Transport: p.Session.Stats()}
}

func (p *Peer) SetVirtualIP(address string) {
	p.mu.Lock()
	p.VirtualIP = address
	p.mu.Unlock()
}

type PeerInfo struct {
	ID                         uint64
	AgentID, Remote, VirtualIP string
	Connected, LastSeen        time.Time
	Authenticated              bool
	Transport                  session.Stats
}

type Server struct {
	conn         *net.UDPConn
	domain       string
	identity     ed25519.PrivateKey
	token        []byte
	cookieSecret [32]byte
	mu           sync.RWMutex
	peers        map[uint64]*Peer
	accepted     chan *Peer
	limit        chan struct{}
	closed       chan struct{}
}

func Listen(addr, domain string, identity ed25519.PrivateKey, token []byte) (*Server, error) {
	if len(identity) != ed25519.PrivateKeySize || len(token) < 32 {
		return nil, errors.New("identity and 32-byte token required")
	}
	if _, err := encodeName("x." + strings.TrimSuffix(domain, ".") + "."); err != nil {
		return nil, err
	}
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	c, err := net.ListenUDP("udp", a)
	if err != nil {
		return nil, err
	}
	s := &Server{conn: c, domain: strings.ToLower(strings.TrimSuffix(domain, ".")) + ".", identity: identity, token: append([]byte(nil), token...), peers: make(map[uint64]*Peer), accepted: make(chan *Peer, 128), limit: make(chan struct{}, 1024), closed: make(chan struct{})}
	if _, err = rand.Read(s.cookieSecret[:]); err != nil {
		c.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) Addr() net.Addr         { return s.conn.LocalAddr() }
func (s *Server) Accepted() <-chan *Peer { return s.accepted }

func (s *Server) Peers() []PeerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PeerInfo, 0, len(s.peers))
	for _, p := range s.peers {
		if v := p.Snapshot(); v.Authenticated {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) Serve(ctx context.Context) error {
	defer s.Close()
	go func() { <-ctx.Done(); s.Close() }()
	go s.sweep(ctx)
	buf := make([]byte, maxDNS)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.closed:
				return nil
			default:
				return err
			}
		}
		select {
		case s.limit <- struct{}{}:
		default:
			continue
		}
		packet := append([]byte(nil), buf[:n]...)
		go func() { defer func() { <-s.limit }(); s.handle(ctx, addr, packet) }()
	}
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	for _, p := range s.peers {
		p.Session.Close()
	}
	return s.conn.Close()
}

func (s *Server) sweep(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case <-t.C:
			now := time.Now()
			s.mu.Lock()
			for id, p := range s.peers {
				p.mu.Lock()
				age := now.Sub(p.LastSeen)
				auth := p.authenticated
				p.mu.Unlock()
				if (!auth && age > 10*time.Second) || age > 60*time.Second {
					p.Session.Close()
					delete(s.peers, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) handle(ctx context.Context, addr *net.UDPAddr, b []byte) {
	m, err := Decode(b)
	if err != nil || m.Response || !strings.HasSuffix(m.Name, "."+s.domain) {
		return
	}
	var out []byte
	if len(m.Payload) == 106 && m.Payload[0] == security.Hello {
		source := addr.IP.String()
		if !security.CheckCookie(s.cookieSecret[:], source, m.Payload, time.Now()) {
			out, _ = security.MakeCookie(s.cookieSecret[:], source, m.Payload, time.Now())
		} else {
			var sid uint64
			for sid == 0 {
				var b [8]byte
				if _, err = rand.Read(b[:]); err != nil {
					return
				}
				sid = binary.BigEndian.Uint64(b[:])
			}
			s.mu.Lock()
			if len(s.peers) >= 1024 {
				s.mu.Unlock()
				return
			}
			for s.peers[sid] != nil {
				var b [8]byte
				if _, err = rand.Read(b[:]); err != nil {
					s.mu.Unlock()
					return
				}
				sid = binary.BigEndian.Uint64(b[:])
			}
			var hs *security.ServerState
			out, hs, err = security.NewServerHello(s.identity, m.Payload, sid)
			if err != nil {
				s.mu.Unlock()
				return
			}
			var keys security.Keys
			keys, err = hs.Keys(m.Payload)
			if err != nil {
				s.mu.Unlock()
				return
			}
			var sess *session.Session
			fragSize := 800
			if hs.Profile == 1 {
				fragSize = 320
			}
			sess, err = session.NewWithFragment(sid, keys, false, fragSize)
			if err != nil {
				s.mu.Unlock()
				return
			}
			s.peers[sid] = &Peer{Session: sess, Remote: addr.String(), Connected: time.Now(), LastSeen: time.Now(), transcript: hs.Transcript}
			s.mu.Unlock()
		}
	} else if len(m.Payload) >= 66 {
		sid := binary.BigEndian.Uint64(m.Payload[2:10])
		s.mu.RLock()
		p := s.peers[sid]
		s.mu.RUnlock()
		if p == nil {
			return
		}
		if err = p.Session.Process(m.Payload, time.Now()); err != nil {
			p.packetErrorOnce.Do(func() { log.Printf("DNS session %d rejected packet from %s: %v", p.Session.ID(), addr, err) })
			return
		}
		p.mu.Lock()
		p.LastSeen = time.Now()
		p.Remote = addr.String()
		authenticated := p.authenticated
		p.mu.Unlock()
		if !authenticated {
			if msg, ok := p.Session.TryRecv(); ok {
				id, authErr := security.CheckAuth(s.token, msg, p.transcript)
				if authErr != nil {
					if err = p.Session.Send(ctx, []byte{security.AuthReject}); err != nil {
						return
					}
				} else {
					p.mu.Lock()
					p.AgentID = hex.EncodeToString(id[:])
					p.authenticated = true
					p.mu.Unlock()
					if err = p.Session.Send(ctx, []byte{security.AuthOK}); err != nil {
						return
					}
					select {
					case s.accepted <- p:
					default:
					}
				}
			}
		}
		if !p.Snapshot().Authenticated && !p.Session.HasWork(time.Now()) {
			return
		}
		if !p.Session.HasWork(time.Now()) {
			t := time.NewTimer(200 * time.Millisecond)
			select {
			case <-p.Session.Wake():
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				return
			case <-s.closed:
				t.Stop()
				return
			}
			t.Stop()
		}
		out, err = p.Session.NextPacket(time.Now())
		if err != nil {
			return
		}
	} else {
		return
	}
	resp, err := Encode(Message{ID: m.ID, Name: m.Name, Response: true, Payload: out})
	if err != nil {
		return
	}
	_, _ = s.conn.WriteToUDP(resp, addr)
}
