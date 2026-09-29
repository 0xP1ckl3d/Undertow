package dns

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"undertow/internal/security"
	"undertow/internal/session"
)

type Client struct {
	Session   *session.Session
	server    *net.UDPAddr
	domain    string
	lastSeen  atomic.Int64
	queries   atomic.Uint64
	responses atomic.Uint64
	cancel    context.CancelFunc
	workers   sync.WaitGroup
}

func Dial(ctx context.Context, serverAddr, domain, fingerprint string, token []byte, agentKey ed25519.PrivateKey) (*Client, error) {
	return DialProfile(ctx, serverAddr, domain, fingerprint, token, agentKey, 0)
}

// DiscoverFingerprint reads the server identity for an explicit trust-on-first-
// use enrollment. Callers must finish an authenticated handshake before saving
// the returned fingerprint as a durable pin.
func DiscoverFingerprint(ctx context.Context, serverAddr, domain string) (string, error) {
	host, _, err := net.SplitHostPort(serverAddr)
	if err != nil || net.ParseIP(host) == nil {
		return "", errors.New("direct DNS requires a server IP literal")
	}
	server, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return "", err
	}
	conn, err := net.DialUDP("udp", nil, server)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	hs, err := security.NewClient()
	if err != nil {
		return "", err
	}
	domain = strings.ToLower(strings.TrimSuffix(domain, ".")) + "."
	for i := 0; i < 4; i++ {
		response, err := exchange(ctx, conn, domain, hs.Hello())
		if err != nil {
			return "", err
		}
		if len(response) > 0 && response[0] == security.Cookie {
			if err := hs.AcceptCookie(response); err != nil {
				return "", err
			}
			continue
		}
		fingerprint, err := security.ServerHelloFingerprint(response)
		if err != nil {
			return "", err
		}
		if _, err := hs.VerifyServerHello(response, fingerprint); err != nil {
			return "", err
		}
		return fingerprint, nil
	}
	return "", security.ErrHandshake
}

func DialProfile(ctx context.Context, serverAddr, domain, fingerprint string, token []byte, agentKey ed25519.PrivateKey, profile byte) (*Client, error) {
	if len(token) < 32 || len(agentKey) != ed25519.PrivateKeySize {
		return nil, errors.New("agent key and 32-byte token required")
	}
	if profile > 1 {
		return nil, errors.New("invalid payload profile")
	}
	host, _, err := net.SplitHostPort(serverAddr)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) == nil {
		return nil, errors.New("direct DNS requires an IP literal")
	}
	server, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return nil, err
	}
	if server.IP == nil {
		return nil, errors.New("direct DNS requires an IP address")
	}
	domain = strings.ToLower(strings.TrimSuffix(domain, ".")) + "."
	conn, err := net.DialUDP("udp", nil, server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	hs, err := security.NewClient()
	if err != nil {
		return nil, err
	}
	hs.Profile = profile
	var response []byte
	for i := 0; i < 4; i++ {
		response, err = exchange(ctx, conn, domain, hs.Hello())
		if err != nil {
			return nil, err
		}
		if len(response) > 0 && response[0] == security.Cookie {
			if err = hs.AcceptCookie(response); err != nil {
				return nil, err
			}
			continue
		}
		if len(response) > 0 && response[0] == security.ServerHello {
			break
		}
		return nil, security.ErrHandshake
	}
	keys, err := hs.VerifyServerHello(response, fingerprint)
	if err != nil {
		return nil, err
	}
	fragSize := 800
	if profile == 1 {
		fragSize = 320
	}
	sess, err := session.NewWithFragment(hs.SessionID, keys, true, fragSize)
	if err != nil {
		return nil, err
	}
	if err = sess.Send(ctx, security.MakeAuth(token, agentKey, hs.Transcript)); err != nil {
		return nil, err
	}
	authed := false
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !authed {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("authentication timed out")
		default:
		}
		wire, err := sess.NextPacket(time.Now())
		if err != nil {
			return nil, err
		}
		resp, err := exchange(ctx, conn, domain, wire)
		if err != nil {
			continue
		}
		if err = sess.Process(resp, time.Now()); err != nil {
			return nil, fmt.Errorf("authentication response: %w", err)
		}
		if b, ok := sess.TryRecv(); ok {
			if len(b) == 1 && b[0] == security.AuthReject {
				return nil, errors.New("enrollment rejected by server; check --auth and credential")
			}
			if len(b) != 1 || b[0] != security.AuthOK {
				return nil, security.ErrHandshake
			}
			authed = true
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	c := &Client{Session: sess, server: server, domain: domain, cancel: cancel}
	c.lastSeen.Store(time.Now().UnixNano())
	for i := 0; i < 16; i++ {
		c.workers.Add(1)
		go c.poll(runCtx)
	}
	go func() { <-runCtx.Done(); sess.Close() }()
	return c, nil
}

func (c *Client) Send(ctx context.Context, b []byte) error { return c.Session.Send(ctx, b) }
func (c *Client) Recv(ctx context.Context) ([]byte, error) { return c.Session.Recv(ctx) }
func (c *Client) Stats() (session.Stats, uint64, uint64) {
	return c.Session.Stats(), c.queries.Load(), c.responses.Load()
}
func (c *Client) Close() error { c.cancel(); c.Session.Close(); c.workers.Wait(); return nil }

func (c *Client) poll(ctx context.Context) {
	defer c.workers.Done()
	conn, err := net.DialUDP("udp", nil, c.server)
	if err != nil {
		c.Session.Close()
		return
	}
	defer conn.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.Session.Done():
			return
		default:
		}
		wire, err := c.Session.NextPacket(time.Now())
		if err != nil {
			return
		}
		c.queries.Add(1)
		resp, err := exchange(ctx, conn, c.domain, wire)
		if err != nil {
			if time.Since(time.Unix(0, c.lastSeen.Load())) > 15*time.Second {
				c.Session.Close()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		c.responses.Add(1)
		if err = c.Session.Process(resp, time.Now()); err != nil {
			c.Session.Close()
			return
		}
		c.lastSeen.Store(time.Now().UnixNano())
	}
}

func exchange(ctx context.Context, conn *net.UDPConn, domain string, payload []byte) ([]byte, error) {
	name, err := RandomName(domain)
	if err != nil {
		return nil, err
	}
	var idBytes [2]byte
	if _, err = rand.Read(idBytes[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	b, err := Encode(Message{ID: id, Name: name, Payload: payload})
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err = conn.Write(b); err != nil {
		return nil, err
	}
	buf := make([]byte, maxDNS)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	m, err := Decode(buf[:n])
	if err != nil {
		return nil, err
	}
	if !m.Response || m.ID != id || m.Name != name || m.RCode != 0 {
		return nil, errors.New("DNS response mismatch")
	}
	if len(m.Payload) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return m.Payload, nil
}
