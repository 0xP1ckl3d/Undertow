package websocket

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"undertow/internal/transport"
	"undertow/internal/transport/stream"
)

const upgradeGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type Server struct {
	listener net.Listener
	http     *http.Server
	path     string
	identity ed25519.PrivateKey
	token    []byte
	ctx      context.Context
	cancel   context.CancelFunc
	accepted chan transport.Peer
	mu       sync.RWMutex
	peers    map[uint64]*stream.Peer
	once     sync.Once
}

var _ transport.Listener = (*Server)(nil)

func Listen(addr, path, certFile, keyFile string, identity ed25519.PrivateKey, token []byte) (*Server, error) {
	if path == "" || path[0] != '/' || strings.ContainsAny(path, "?#") {
		return nil, errors.New("WebSocket path must start with / and contain no query or fragment")
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("WebSocket server requires --tls-cert and --tls-key")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{listener: listener, path: path, identity: identity, token: append([]byte(nil), token...), ctx: ctx, cancel: cancel, accepted: make(chan transport.Peer, 128), peers: make(map[uint64]*stream.Peer)}
	s.http = &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 10 * time.Second}
	s.listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	return s, nil
}

func (s *Server) Addr() net.Addr { return s.listener.Addr() }
func (s *Server) Accept(ctx context.Context) (transport.Peer, error) {
	select {
	case p := <-s.accepted:
		return p, nil
	case <-s.ctx.Done():
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *Server) Peers() []transport.PeerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	peers := make([]transport.PeerInfo, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer.Snapshot())
	}
	return peers
}
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.ctx.Done():
		}
	}()
	err := s.http.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) || s.ctx.Err() != nil {
		return nil
	}
	return err
}
func (s *Server) Close() error {
	s.once.Do(func() {
		s.cancel()
		_ = s.http.Close()
		_ = s.listener.Close()
		s.mu.Lock()
		for _, peer := range s.peers {
			_ = peer.Close()
		}
		s.mu.Unlock()
	})
	return nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet || !headerToken(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "WebSocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		http.Error(w, "invalid WebSocket key", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket upgrade unavailable", http.StatusInternalServerError)
		return
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	accept := websocketAccept(key)
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		_ = conn.Close()
		return
	}
	if err := buffered.Flush(); err != nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	framed := &messageConn{conn: conn, reader: buffered.Reader}
	peer, err := stream.Accept(s.ctx, framed, s.identity, s.token)
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return
	}
	s.mu.Lock()
	s.peers[peer.ID()] = peer
	s.mu.Unlock()
	select {
	case s.accepted <- peer:
	case <-s.ctx.Done():
		_ = peer.Close()
		return
	}
	go func() {
		<-peer.Session.Done()
		s.mu.Lock()
		if s.peers[peer.ID()] == peer {
			delete(s.peers, peer.ID())
		}
		s.mu.Unlock()
	}()
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + upgradeGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerToken(value, token string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), token) {
			return true
		}
	}
	return false
}

type DialOptions struct {
	Address               string
	Path                  string
	TLSServerName         string
	TLSInsecureSkipVerify bool
}

func Dial(ctx context.Context, options DialOptions, fingerprint string, token []byte, key ed25519.PrivateKey) (*stream.Connection, error) {
	conn, err := dialMessage(ctx, options)
	if err != nil {
		return nil, err
	}
	_ = conn.conn.SetDeadline(time.Now().Add(15 * time.Second))
	c, err := stream.Dial(ctx, conn, fingerprint, token, key)
	_ = conn.conn.SetDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func DiscoverFingerprint(ctx context.Context, options DialOptions) (string, error) {
	conn, err := dialMessage(ctx, options)
	if err != nil {
		return "", err
	}
	_ = conn.conn.SetDeadline(time.Now().Add(15 * time.Second))
	return stream.DiscoverFingerprint(conn)
}

func dialMessage(ctx context.Context, options DialOptions) (*messageConn, error) {
	if options.Path == "" || options.Path[0] != '/' || strings.ContainsAny(options.Path, "?#") {
		return nil, errors.New("WebSocket path must start with / and contain no query or fragment")
	}
	host, _, err := net.SplitHostPort(options.Address)
	if err != nil {
		return nil, err
	}
	requestURL := &url.URL{Scheme: "https", Host: options.Address, Path: options.Path}
	proxyRequest := &http.Request{URL: requestURL}
	proxyURL, err := http.ProxyFromEnvironment(proxyRequest)
	if err != nil {
		return nil, err
	}
	var raw net.Conn
	if proxyURL == nil {
		raw, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", options.Address)
	} else {
		raw, err = dialProxy(ctx, proxyURL, options.Address)
	}
	if err != nil {
		return nil, err
	}
	serverName := options.TLSServerName
	if serverName == "" {
		serverName = host
	}
	tlsConn := tls.Client(raw, &tls.Config{ServerName: serverName, InsecureSkipVerify: options.TLSInsecureSkipVerify, MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	_ = tlsConn.SetDeadline(time.Now().Add(15 * time.Second))
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	writer := bufio.NewWriter(tlsConn)
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", options.Path, options.Address, key)
	if _, err := writer.WriteString(request); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	if err := writer.Flush(); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	reader := bufio.NewReader(tlsConn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols || !headerToken(response.Header.Get("Connection"), "upgrade") || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") || response.Header.Get("Sec-WebSocket-Accept") != websocketAccept(key) {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("WebSocket upgrade rejected: %s", response.Status)
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return &messageConn{conn: tlsConn, reader: reader, client: true}, nil
}

func dialProxy(ctx context.Context, proxyURL *url.URL, target string) (net.Conn, error) {
	address := proxyURL.Host
	if _, _, err := net.SplitHostPort(address); err != nil {
		port := "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
		address = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if proxyURL.Scheme == "https" {
		proxyTLS := tls.Client(conn, &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
		if err := proxyTLS.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = proxyTLS
	} else if proxyURL.Scheme != "http" {
		_ = conn.Close()
		return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	w := bufio.NewWriter(conn)
	if _, err := fmt.Fprintf(w, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credentials := proxyURL.User.Username() + ":" + password
		if _, err := fmt.Fprintf(w, "Proxy-Authorization: Basic %s\r\n", base64.StdEncoding.EncodeToString([]byte(credentials))); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	if _, err := w.WriteString("\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := w.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed: %s", response.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
