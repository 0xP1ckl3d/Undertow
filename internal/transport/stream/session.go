package stream

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport"
)

// MessageConn is the small carrier-specific boundary used by persistent
// WebSocket and QUIC connections. Each message contains one Undertow wire
// packet or one handshake message.
type MessageConn interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
	RemoteAddr() string
	Close() error
}

type Connection struct {
	Session *session.Session
	conn    MessageConn
	cancel  context.CancelFunc
	once    sync.Once
}

var _ transport.Connection = (*Connection)(nil)

func (c *Connection) ID() uint64         { return c.Session.ID() }
func (c *Connection) RemoteAddr() string { return c.conn.RemoteAddr() }
func (c *Connection) Send(ctx context.Context, b []byte) error {
	return c.Session.Send(ctx, b)
}
func (c *Connection) SendPriority(ctx context.Context, b []byte) error {
	return c.Session.SendPriority(ctx, b)
}
func (c *Connection) Recv(ctx context.Context) ([]byte, error) { return c.Session.Recv(ctx) }
func (c *Connection) Close() error {
	c.once.Do(func() {
		c.cancel()
		_ = c.Session.Close()
		_ = c.conn.Close()
	})
	return nil
}

type Peer struct {
	*Connection
	mu        sync.Mutex
	agentID   string
	remote    string
	connected time.Time
	lastSeen  time.Time
	virtualIP string
}

var _ transport.Peer = (*Peer)(nil)

func (p *Peer) Snapshot() transport.PeerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return transport.PeerInfo{
		ID: p.ID(), AgentID: p.agentID, Remote: p.remote,
		Connected: p.connected, LastSeen: p.lastSeen, VirtualIP: p.virtualIP,
		Authenticated: true, Transport: p.Session.Stats(),
	}
}
func (p *Peer) SetVirtualIP(value string) {
	p.mu.Lock()
	p.virtualIP = value
	p.mu.Unlock()
}
func (p *Peer) Channel() transport.SessionTransport { return p.Connection }

func Dial(ctx context.Context, conn MessageConn, fingerprint string, token []byte, key ed25519.PrivateKey) (*Connection, error) {
	if len(token) < 32 || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("identity key and 32-byte enrollment secret required")
	}
	hs, err := security.NewClient()
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(hs.Hello()); err != nil {
		return nil, err
	}
	cookie, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if err := hs.AcceptCookie(cookie); err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(hs.Hello()); err != nil {
		return nil, err
	}
	hello, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	keys, err := hs.VerifyServerHello(hello, fingerprint)
	if err != nil {
		return nil, err
	}
	sess, err := session.NewWithFragment(hs.SessionID, keys, true, 800)
	if err != nil {
		return nil, err
	}
	c := start(ctx, conn, sess, nil)
	if err := c.Send(ctx, security.MakeAuth(token, key, hs.Transcript)); err != nil {
		c.Close()
		return nil, err
	}
	authCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	reply, err := c.Recv(authCtx)
	if err != nil {
		c.Close()
		return nil, err
	}
	if len(reply) != 1 || reply[0] != security.AuthOK {
		c.Close()
		return nil, errors.New("enrollment rejected by server; check --auth and credential")
	}
	return c, nil
}

func Accept(ctx context.Context, conn MessageConn, identity ed25519.PrivateKey, token []byte) (*Peer, error) {
	if len(identity) != ed25519.PrivateKeySize || len(token) < 32 {
		return nil, errors.New("server identity and 32-byte enrollment secret required")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	hello, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	cookie, err := security.MakeCookie(secret[:], conn.RemoteAddr(), hello, time.Now())
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(cookie); err != nil {
		return nil, err
	}
	hello, err = conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if !security.CheckCookie(secret[:], conn.RemoteAddr(), hello, time.Now()) {
		return nil, security.ErrHandshake
	}
	var sid uint64
	for sid == 0 {
		var id [8]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		sid = binary.BigEndian.Uint64(id[:])
	}
	response, serverState, err := security.NewServerHello(identity, hello, sid)
	if err != nil {
		return nil, err
	}
	keys, err := serverState.Keys(hello)
	if err != nil {
		return nil, err
	}
	if err := conn.WriteMessage(response); err != nil {
		return nil, err
	}
	sess, err := session.NewWithFragment(sid, keys, false, 800)
	if err != nil {
		return nil, err
	}
	connected := time.Now()
	peer := &Peer{remote: conn.RemoteAddr(), connected: connected, lastSeen: connected}
	c := start(ctx, conn, sess, func() {
		peer.mu.Lock()
		peer.lastSeen = time.Now()
		peer.mu.Unlock()
	})
	peer.Connection = c
	authCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	auth, err := c.Recv(authCtx)
	if err != nil {
		c.Close()
		return nil, err
	}
	id, err := security.CheckAuth(token, auth, serverState.Transcript)
	if err != nil {
		_ = c.Send(ctx, []byte{security.AuthReject})
		c.Close()
		return nil, err
	}
	peer.agentID = hex.EncodeToString(id[:])
	if err := c.Send(ctx, []byte{security.AuthOK}); err != nil {
		c.Close()
		return nil, err
	}
	return peer, nil
}

func DiscoverFingerprint(conn MessageConn) (string, error) {
	defer conn.Close()
	hs, err := security.NewClient()
	if err != nil {
		return "", err
	}
	if err := conn.WriteMessage(hs.Hello()); err != nil {
		return "", err
	}
	cookie, err := conn.ReadMessage()
	if err != nil {
		return "", err
	}
	if err := hs.AcceptCookie(cookie); err != nil {
		return "", err
	}
	if err := conn.WriteMessage(hs.Hello()); err != nil {
		return "", err
	}
	hello, err := conn.ReadMessage()
	if err != nil {
		return "", err
	}
	return security.ServerHelloFingerprint(hello)
}

func start(parent context.Context, conn MessageConn, sess *session.Session, onPacket func()) *Connection {
	ctx, cancel := context.WithCancel(parent)
	c := &Connection{Session: sess, conn: conn, cancel: cancel}
	go func() {
		for ctx.Err() == nil {
			wire, err := conn.ReadMessage()
			if err != nil || sess.Process(wire, time.Now()) != nil {
				break
			}
			if onPacket != nil {
				onPacket()
			}
		}
		c.Close()
	}()
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sess.Wake():
			case <-ticker.C:
			}
			for sess.HasWork(time.Now()) {
				wire, err := sess.NextPacket(time.Now())
				if err != nil || conn.WriteMessage(wire) != nil {
					c.Close()
					return
				}
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
		case <-sess.Done():
		}
		c.Close()
	}()
	return c
}
