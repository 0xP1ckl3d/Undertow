package mux

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"net"
	"sync"
	"time"

	"undertow/internal/security"
	"undertow/internal/session"
)

// orderedSessionConn is the byte-stream adapter required by yamux and smux.
// Each 692-byte chunk has a sequence prefix. Missing chunks hold later bytes.
type orderedSessionConn struct {
	s                 *session.Session
	readMu, writeMu   sync.Mutex
	readSeq, writeSeq uint64
	pending           map[uint64][]byte
	current           []byte
}

func (c *orderedSessionConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.current) == 0 {
		if b, ok := c.pending[c.readSeq+1]; ok {
			c.readSeq++
			delete(c.pending, c.readSeq)
			c.current = b
			break
		}
		b, err := c.s.Recv(context.Background())
		if err != nil {
			return 0, err
		}
		if len(b) < 9 {
			return 0, io.ErrUnexpectedEOF
		}
		seq := binary.BigEndian.Uint64(b[:8])
		if seq > c.readSeq {
			c.pending[seq] = b[8:]
		}
	}
	n := copy(p, c.current)
	c.current = c.current[n:]
	return n, nil
}
func (c *orderedSessionConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > 692 {
			n = 692
		}
		b := make([]byte, 8+n)
		c.writeSeq++
		binary.BigEndian.PutUint64(b[:8], c.writeSeq)
		copy(b[8:], p[:n])
		for {
			err := c.s.Send(context.Background(), b)
			if err == nil {
				break
			}
			if !errors.Is(err, session.ErrQueueFull) {
				return written, err
			}
			time.Sleep(time.Millisecond)
		}
		written += n
		p = p[n:]
	}
	return written, nil
}
func (c *orderedSessionConn) Close() error                     { return c.s.Close() }
func (c *orderedSessionConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *orderedSessionConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *orderedSessionConn) SetDeadline(time.Time) error      { return nil }
func (c *orderedSessionConn) SetReadDeadline(time.Time) error  { return nil }
func (c *orderedSessionConn) SetWriteDeadline(time.Time) error { return nil }

func makeLossyBenchPeer(kind string) (benchPeer, error) {
	var keys security.Keys
	for i := range keys.ClientToServer {
		keys.ClientToServer[i], keys.ServerToClient[i] = byte(i+1), byte(i+51)
	}
	copy(keys.ClientPrefix[:], []byte{1, 2, 3, 4})
	copy(keys.ServerPrefix[:], []byte{5, 6, 7, 8})
	a, err := session.New(144, keys, true)
	if err != nil {
		return benchPeer{}, err
	}
	b, err := session.New(144, keys, false)
	if err != nil {
		a.Close()
		return benchPeer{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		rng := rand.New(rand.NewSource(103))
		now := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			now = now.Add(20 * time.Millisecond)
			for _, direction := range [][2]*session.Session{{a, b}, {b, a}} {
				if !direction[0].HasWork(now) {
					continue
				}
				wire, err := direction[0].NextPacket(now)
				if err != nil {
					return
				}
				if rng.Intn(5) == 0 {
					continue
				}
				_ = direction[1].Process(wire, now)
			}
		}
	}()
	stop := func() { cancel(); a.Close(); b.Close() }
	ca := &orderedSessionConn{s: a, pending: make(map[uint64][]byte)}
	cb := &orderedSessionConn{s: b, pending: make(map[uint64][]byte)}
	return makeBenchPeerOnCarrier(kind, ca, cb, a, b, stop)
}
