package quic

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	quicgo "github.com/quic-go/quic-go"
	"undertow/internal/transport"
	"undertow/internal/transport/stream"
	"undertow/internal/transport/tlscert"
)

const alpn = "undertow/1"
const maxMessage = 2048

type messageConn struct {
	connection *quicgo.Conn
	stream     *quicgo.Stream
	write      sync.Mutex
}

func (c *messageConn) RemoteAddr() string { return c.connection.RemoteAddr().String() }
func (c *messageConn) Close() error       { return c.connection.CloseWithError(0, "") }
func (c *messageConn) ReadMessage() ([]byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(c.stream, header[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(header[:]))
	if n == 0 || n > maxMessage {
		return nil, errors.New("invalid QUIC message size")
	}
	message := make([]byte, n)
	_, err := io.ReadFull(c.stream, message)
	return message, err
}
func (c *messageConn) WriteMessage(message []byte) error {
	if len(message) == 0 || len(message) > maxMessage {
		return errors.New("invalid QUIC message size")
	}
	c.write.Lock()
	defer c.write.Unlock()
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(message)))
	if err := writeAll(c.stream, header[:]); err != nil {
		return err
	}
	return writeAll(c.stream, message)
}
func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

type Server struct {
	listener *quicgo.Listener
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

func Listen(addr, certFile, keyFile string, selfSigned bool, identity ed25519.PrivateKey, token []byte) (*Server, error) {
	certificate, err := tlscert.Load(certFile, keyFile, selfSigned)
	if err != nil {
		return nil, err
	}
	listener, err := quicgo.ListenAddr(addr, &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{alpn}, MinVersion: tls.VersionTLS13}, nil)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{listener: listener, identity: identity, token: append([]byte(nil), token...), ctx: ctx, cancel: cancel, accepted: make(chan transport.Peer, 128), peers: make(map[uint64]*stream.Peer)}, nil
}

func (s *Server) Addr() net.Addr { return s.listener.Addr() }
func (s *Server) Accept(ctx context.Context) (transport.Peer, error) {
	select {
	case peer := <-s.accepted:
		return peer, nil
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
	defer s.Close()
	for {
		connection, err := s.listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil || s.ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(connection)
	}
}
func (s *Server) handle(connection *quicgo.Conn) {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	channel, err := connection.AcceptStream(ctx)
	if err != nil {
		_ = connection.CloseWithError(1, "stream required")
		return
	}
	_ = channel.SetDeadline(time.Now().Add(15 * time.Second))
	framed := &messageConn{connection: connection, stream: channel}
	peer, err := stream.Accept(s.ctx, framed, s.identity, s.token)
	_ = channel.SetDeadline(time.Time{})
	if err != nil {
		_ = framed.Close()
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
func (s *Server) Close() error {
	s.once.Do(func() {
		s.cancel()
		_ = s.listener.Close()
		s.mu.Lock()
		for _, peer := range s.peers {
			_ = peer.Close()
		}
		s.mu.Unlock()
	})
	return nil
}

type DialOptions struct {
	Address               string
	TLSServerName         string
	TLSInsecureSkipVerify bool
}

func Dial(ctx context.Context, options DialOptions, fingerprint string, token []byte, key ed25519.PrivateKey) (*stream.Connection, error) {
	framed, err := dialMessage(ctx, options)
	if err != nil {
		return nil, err
	}
	_ = framed.stream.SetDeadline(time.Now().Add(15 * time.Second))
	c, err := stream.Dial(ctx, framed, fingerprint, token, key)
	_ = framed.stream.SetDeadline(time.Time{})
	if err != nil {
		_ = framed.Close()
		return nil, err
	}
	return c, nil
}
func DiscoverFingerprint(ctx context.Context, options DialOptions) (string, error) {
	framed, err := dialMessage(ctx, options)
	if err != nil {
		return "", err
	}
	_ = framed.stream.SetDeadline(time.Now().Add(15 * time.Second))
	return stream.DiscoverFingerprint(framed)
}
func dialMessage(ctx context.Context, options DialOptions) (*messageConn, error) {
	host, _, err := net.SplitHostPort(options.Address)
	if err != nil {
		return nil, err
	}
	serverName := options.TLSServerName
	if serverName == "" {
		serverName = host
	}
	connection, err := quicgo.DialAddr(ctx, options.Address, &tls.Config{ServerName: serverName, InsecureSkipVerify: options.TLSInsecureSkipVerify, NextProtos: []string{alpn}, MinVersion: tls.VersionTLS13}, nil)
	if err != nil {
		return nil, err
	}
	channel, err := connection.OpenStreamSync(ctx)
	if err != nil {
		_ = connection.CloseWithError(1, "stream failed")
		return nil, err
	}
	return &messageConn{connection: connection, stream: channel}, nil
}
