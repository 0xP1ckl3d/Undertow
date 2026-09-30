package dns

import (
	"bytes"
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
	Session      *session.Session
	server       *net.UDPAddr
	domain       string
	lastSeen     atomic.Int64
	queries      atomic.Uint64
	responses    atomic.Uint64
	cancel       context.CancelFunc
	workers      sync.WaitGroup
	sockets      chan *pollSocket
	outstanding  atomic.Int64
	target       atomic.Int64
	healthWindow atomic.Int64
}

type pollSocket struct {
	conn *net.UDPConn
	buf  [maxDNS]byte
}

type AdaptiveStats struct {
	Outstanding  int64
	Target       int64
	HealthWindow int64
}

func Dial(ctx context.Context, serverAddr, domain, fingerprint string, token []byte, agentKey ed25519.PrivateKey) (*Client, error) {
	return DialAdaptive(ctx, serverAddr, domain, fingerprint, token, agentKey)
}

func DialAdaptive(ctx context.Context, serverAddr, domain, fingerprint string, token []byte, agentKey ed25519.PrivateKey) (*Client, error) {
	size, err := DiscoverPayloadSize(ctx, serverAddr, domain)
	if err != nil {
		return nil, fmt.Errorf("DNS payload discovery: %w", err)
	}
	return dialWithSize(ctx, serverAddr, domain, fingerprint, token, agentKey, security.AdaptiveProfile, size)
}

// DiscoverPayloadSize tests both DNS directions before establishing a session.
// The server echoes a same-sized probe, so a successful candidate confirms
// that both the query and response fit the current path.
func DiscoverPayloadSize(ctx context.Context, serverAddr, domain string) (int, error) {
	host, _, err := net.SplitHostPort(serverAddr)
	if err != nil || net.ParseIP(host) == nil {
		return 0, errors.New("direct DNS requires a server IP literal")
	}
	server, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return 0, err
	}
	conn, err := net.DialUDP("udp", nil, server)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	domain = strings.ToLower(strings.TrimSuffix(domain, ".")) + "."
	sizes := [...]int{128, 192, 256, 320, 384, 448, 512, 576, 640, 704, 768, 800}
	best, lo, hi := -1, 0, len(sizes)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		probe := make([]byte, 84+sizes[mid])
		probe[0] = security.PayloadProbe
		if _, err = rand.Read(probe[1:]); err != nil {
			return 0, err
		}
		passed := false
		for attempt := 0; attempt < 3 && ctx.Err() == nil; attempt++ {
			attemptCtx, cancel := context.WithTimeout(ctx, 650*time.Millisecond)
			response, probeErr := exchange(attemptCtx, conn, domain, probe)
			cancel()
			if probeErr == nil && bytes.Equal(response, probe) {
				passed = true
				break
			}
		}
		if passed {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if best < 0 {
		return 0, errors.New("server did not answer payload probes")
	}
	return sizes[best], nil
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
	response, err := exchangeHello(ctx, conn, domain, hs)
	if err != nil {
		return "", err
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

func DialProfile(ctx context.Context, serverAddr, domain, fingerprint string, token []byte, agentKey ed25519.PrivateKey, profile byte) (*Client, error) {
	if profile > 1 {
		return nil, errors.New("invalid payload profile")
	}
	size := 800
	if profile == 1 {
		size = 320
	}
	return dialWithSize(ctx, serverAddr, domain, fingerprint, token, agentKey, profile, size)
}

func dialWithSize(ctx context.Context, serverAddr, domain, fingerprint string, token []byte, agentKey ed25519.PrivateKey, profile byte, size int) (*Client, error) {
	if len(token) < 32 || len(agentKey) != ed25519.PrivateKeySize {
		return nil, errors.New("agent key and 32-byte token required")
	}
	if profile > security.AdaptiveProfile {
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
	hs.FragmentSize = uint16(size)
	response, err := exchangeHello(ctx, conn, domain, hs)
	if err != nil {
		return nil, err
	}
	keys, err := hs.VerifyServerHello(response, fingerprint)
	if err != nil {
		return nil, err
	}
	var sess *session.Session
	if profile == security.AdaptiveProfile {
		sess, err = session.NewAdaptive(hs.SessionID, keys, true, size)
	} else {
		sess, err = session.NewWithFragment(hs.SessionID, keys, true, size)
	}
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
	c.sockets = make(chan *pollSocket, 64)
	c.healthWindow.Store(16)
	c.workers.Add(1)
	go c.run(runCtx)
	go func() { <-runCtx.Done(); sess.Close() }()
	return c, nil
}

// A lost cookie or server hello is retried with the same client nonce and
// ephemeral key. Authentication still verifies the signed transcript and pin.
func exchangeHello(ctx context.Context, conn *net.UDPConn, domain string, hs *security.ClientState) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		response, err := exchange(ctx, conn, domain, hs.Hello())
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if len(response) > 0 && response[0] == security.Cookie {
			if err := hs.AcceptCookie(response); err != nil {
				return nil, err
			}
			continue
		}
		if len(response) > 0 && response[0] == security.ServerHello {
			return response, nil
		}
		return nil, security.ErrHandshake
	}
	if lastErr != nil {
		return nil, fmt.Errorf("handshake retries exhausted: %w", lastErr)
	}
	return nil, security.ErrHandshake
}

func (c *Client) Send(ctx context.Context, b []byte) error { return c.Session.Send(ctx, b) }
func (c *Client) SendPriority(ctx context.Context, b []byte) error {
	return c.Session.SendPriority(ctx, b)
}
func (c *Client) Recv(ctx context.Context) ([]byte, error) { return c.Session.Recv(ctx) }
func (c *Client) Stats() (session.Stats, uint64, uint64) {
	return c.Session.Stats(), c.queries.Load(), c.responses.Load()
}
func (c *Client) AdaptiveStats() AdaptiveStats {
	return AdaptiveStats{Outstanding: c.outstanding.Load(), Target: c.target.Load(), HealthWindow: c.healthWindow.Load()}
}
func (c *Client) Close() error {
	c.cancel()
	c.Session.Close()
	c.workers.Wait()
	for len(c.sockets) > 0 {
		(<-c.sockets).conn.Close()
	}
	return nil
}

func pollTarget(stats session.Stats, health int) int {
	if stats.Queued+stats.InFlight == 0 {
		return 1
	}
	limit := min(64, stats.CongestionWindow, health, stats.PeerReceiveWindow+stats.InFlight)
	if stats.RTT > 0 && stats.RTT < 20*time.Millisecond {
		limit = min(limit, 16)
	}
	// Keep one slot beyond queued/in-flight data so a long idle poll cannot
	// delay the first new packet until its server-side hold expires.
	return max(1, min(limit, stats.Queued+stats.InFlight+1))
}

func (c *Client) run(ctx context.Context) {
	defer c.workers.Done()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	results := make(chan error, 64)
	active := 0
	health := 16
	lastCongestion := uint64(0)
	for {
		stats := c.Session.Stats()
		if stats.CongestionEvents > lastCongestion {
			health = max(2, health/2)
			lastCongestion = stats.CongestionEvents
		}
		c.healthWindow.Store(int64(health))
		target := pollTarget(stats, health)
		c.target.Store(int64(target))
		for active < target {
			wire, err := c.Session.NextPacket(time.Now())
			if err != nil {
				return
			}
			active++
			c.outstanding.Store(int64(active))
			c.workers.Add(1)
			go c.query(ctx, wire, results, responseTimeout(wire, stats.RTT))
		}
		select {
		case <-ctx.Done():
			return
		case <-c.Session.Done():
			return
		case <-c.Session.Wake():
		case <-ticker.C:
		case err := <-results:
			active--
			c.outstanding.Store(int64(active))
			if err == nil && stats.Queued+stats.InFlight > 0 && health < 64 {
				health++
			}
			if err != nil {
				if time.Since(time.Unix(0, c.lastSeen.Load())) > 15*time.Second {
					c.Session.Close()
					return
				}
			}
		}
	}
}

// A data packet should receive an immediate ACK or data response. Retire a
// lost data query sooner on a proven low-RTT path. Long-held idle polls and
// higher-RTT paths keep the full deadline to allow the server's one-second
// hold and scheduler variance.
func responseTimeout(wire []byte, rtt time.Duration) time.Duration {
	if len(wire) < 2 || wire[1]&1 == 0 || rtt == 0 || rtt >= 50*time.Millisecond {
		return 2 * time.Second
	}
	return 500 * time.Millisecond
}

func (c *Client) query(ctx context.Context, wire []byte, results chan<- error, timeout time.Duration) {
	defer c.workers.Done()
	var socket *pollSocket
	select {
	case socket = <-c.sockets:
	default:
		conn, err := net.DialUDP("udp", nil, c.server)
		if err != nil {
			results <- err
			return
		}
		socket = &pollSocket{conn: conn}
	}
	c.queries.Add(1)
	resp, err := exchangeBufferTimeout(ctx, socket.conn, c.domain, wire, socket.buf[:], timeout)
	if err == nil {
		c.responses.Add(1)
		err = c.Session.Process(resp, time.Now())
		if err == nil || errors.Is(err, session.ErrReceiveQueueFull) {
			c.lastSeen.Store(time.Now().UnixNano())
		} else {
			c.Session.Close()
		}
	}
	select {
	case c.sockets <- socket:
	default:
		socket.conn.Close()
	}
	results <- err
}

func exchange(ctx context.Context, conn *net.UDPConn, domain string, payload []byte) ([]byte, error) {
	return exchangeBuffer(ctx, conn, domain, payload, make([]byte, maxDNS))
}

func exchangeBuffer(ctx context.Context, conn *net.UDPConn, domain string, payload, buf []byte) ([]byte, error) {
	return exchangeBufferTimeout(ctx, conn, domain, payload, buf, 2*time.Second)
}

func exchangeBufferTimeout(ctx context.Context, conn *net.UDPConn, domain string, payload, buf []byte, timeout time.Duration) ([]byte, error) {
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
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err = conn.Write(b); err != nil {
		return nil, err
	}
	for {
		n, readErr := conn.Read(buf)
		if readErr != nil {
			return nil, readErr
		}
		m, decodeErr := decode(buf[:n], false)
		if decodeErr != nil || !m.Response || m.ID != id || m.Name != name {
			continue // Delayed response to an earlier query on this socket.
		}
		if m.RCode != 0 {
			return nil, errors.New("DNS response error")
		}
		if len(m.Payload) == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return m.Payload, nil
	}
}
