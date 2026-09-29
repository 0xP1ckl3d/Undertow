package mux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"undertow/internal/session"
	"undertow/internal/transport"
)

const streamWindow = 32 << 10

type Mux struct {
	transport transport.SessionTransport
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	streams   map[uint64]*Stream
	order     []uint64
	nextID    uint64
	round     int
	initiator bool
	control   chan frame
	controlIn chan []byte
	accepted  chan *Stream
	wake      chan struct{}
	done      chan struct{}
	once      sync.Once
}

// New starts independent reader and fair sender loops over one reliable session.
// initiator=true is the proxy side and allocates even stream IDs.
func New(parent context.Context, t transport.SessionTransport, initiator bool) *Mux {
	ctx, cancel := context.WithCancel(parent)
	next := uint64(1)
	if initiator {
		next = 2
	}
	m := &Mux{transport: t, ctx: ctx, cancel: cancel, streams: make(map[uint64]*Stream), nextID: next, initiator: initiator, control: make(chan frame, 1024), controlIn: make(chan []byte, 64), accepted: make(chan *Stream, 256), wake: make(chan struct{}, 1), done: make(chan struct{})}
	go m.readLoop()
	go m.sendLoop()
	go func() { <-ctx.Done(); m.Close() }()
	return m
}

func (m *Mux) Done() <-chan struct{} { return m.done }
func (m *Mux) StreamCount() int      { m.mu.Lock(); defer m.mu.Unlock(); return len(m.streams) }

// SendControl uses reserved stream 0 for authenticated session control messages.
func (m *Mux) SendControl(ctx context.Context, data []byte) error {
	if len(data) == 0 || len(data) > maxFrameData {
		return errFrame
	}
	return m.controlFrame(ctx, frame{kind: frameControl, id: 0, data: append([]byte(nil), data...)})
}

func (m *Mux) RecvControl(ctx context.Context) ([]byte, error) {
	select {
	case data := <-m.controlIn:
		return data, nil
	case <-m.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (m *Mux) Accept(ctx context.Context) (*Stream, error) {
	select {
	case s := <-m.accepted:
		return s, nil
	case <-m.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Mux) Open(ctx context.Context, destination string) (*Stream, error) {
	if len(destination) == 0 || len(destination) > 255 {
		return nil, errors.New("invalid destination")
	}
	if _, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(destination, "udp://"), "icmp://")); err != nil {
		return nil, err
	}
	m.mu.Lock()
	select {
	case <-m.done:
		m.mu.Unlock()
		return nil, io.EOF
	default:
	}
	id := m.nextID
	m.nextID += 2
	s := newStream(m, id, destination)
	m.streams[id] = s
	m.order = append(m.order, id)
	m.mu.Unlock()
	if err := m.controlFrame(ctx, frame{kind: frameOpen, id: id, data: []byte(destination)}); err != nil {
		s.resetLocal()
		m.remove(id)
		return nil, err
	}
	select {
	case err := <-s.openResult:
		if err != nil {
			s.resetLocal()
			m.remove(id)
			return nil, err
		}
		return s, nil
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	case <-m.done:
		return nil, io.EOF
	}
}

func (m *Mux) Close() error {
	m.once.Do(func() {
		close(m.done)
		m.cancel()
		m.transport.Close()
		m.mu.Lock()
		for _, s := range m.streams {
			s.resetLocal()
		}
		m.streams = make(map[uint64]*Stream)
		m.mu.Unlock()
	})
	return nil
}

func (m *Mux) controlFrame(ctx context.Context, f frame) error {
	select {
	case m.control <- f:
		m.signal()
		return nil
	case <-m.done:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Mux) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Mux) readLoop() {
	defer m.Close()
	for {
		b, err := m.transport.Recv(m.ctx)
		if err != nil {
			return
		}
		f, err := decodeFrame(b)
		if err != nil {
			return
		}
		if err = m.handle(f); err != nil {
			return
		}
	}
}

func (m *Mux) handle(f frame) error {
	if f.kind == frameControl {
		if f.id != 0 || f.offset != 0 || len(f.data) == 0 {
			return errFrame
		}
		select {
		case m.controlIn <- append([]byte(nil), f.data...):
			return nil
		default:
			return errors.New("control receive queue full")
		}
	}
	if f.kind == framePing {
		return m.controlFrame(m.ctx, frame{kind: framePong})
	}
	if f.kind == framePong {
		return nil
	}
	if f.kind == frameOpen {
		if len(f.data) == 0 || len(f.data) > 255 || f.offset != 0 {
			return errFrame
		}
		if _, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(string(f.data), "udp://"), "icmp://")); err != nil {
			return errFrame
		}
		if m.initiator && f.id%2 != 1 || !m.initiator && f.id%2 != 0 {
			return errFrame
		}
		m.mu.Lock()
		if m.streams[f.id] != nil {
			m.mu.Unlock()
			return errFrame
		}
		s := newStream(m, f.id, string(f.data))
		m.streams[f.id] = s
		m.order = append(m.order, f.id)
		m.mu.Unlock()
		select {
		case m.accepted <- s:
			return nil
		default:
			s.Fail(errors.New("too many pending opens"))
			return nil
		}
	}
	m.mu.Lock()
	s := m.streams[f.id]
	m.mu.Unlock()
	if s == nil {
		// A peer may still be consuming queued data after this side has
		// gracefully closed and removed the stream. Late window updates must
		// not reset the peer before it has drained those bytes.
		if f.kind == frameReset || f.kind == frameWindow {
			return nil
		}
		return m.controlFrame(m.ctx, frame{kind: frameReset, id: f.id})
	}
	switch f.kind {
	case frameOpenOK:
		select {
		case s.openResult <- nil:
		default:
		}
	case frameOpenFail:
		select {
		case s.openResult <- fmt.Errorf("remote connect failed: %s", string(f.data)):
		default:
		}
	case frameData:
		if err := s.receiveData(f.offset, f.data); err != nil {
			s.Close()
		}
	case frameFin:
		s.receiveFin(f.offset)
	case frameReset:
		s.resetLocal()
		m.remove(s.id)
	case frameWindow:
		if f.offset == 0 || f.offset > streamWindow {
			s.Close()
			return nil
		}
		s.addCredit(int(f.offset))
	}
	return nil
}

func (m *Mux) sendLoop() {
	for {
		select {
		case <-m.done:
			return
		default:
		}
		var f frame
		have := false
		select {
		case f = <-m.control:
			have = true
		default:
		}
		if !have {
			m.mu.Lock()
			n := len(m.order)
			for i := 0; i < n; i++ {
				idx := (m.round + i) % n
				id := m.order[idx]
				s := m.streams[id]
				if s == nil {
					continue
				}
				select {
				case f = <-s.out:
					have = true
					m.round = (idx + 1) % n
				default:
				}
				if have {
					break
				}
			}
			m.mu.Unlock()
		}
		if !have {
			select {
			case f = <-m.control:
				have = true
			case <-m.wake:
				continue
			case <-m.done:
				return
			}
		}
		if !have {
			continue
		}
		b := f.encode()
		for {
			err := m.transport.Send(m.ctx, b)
			if err == nil {
				break
			}
			if !errors.Is(err, session.ErrQueueFull) {
				m.Close()
				return
			}
			select {
			case <-time.After(5 * time.Millisecond):
			case <-m.done:
				return
			}
		}
	}
}

func (m *Mux) remove(id uint64) {
	m.mu.Lock()
	delete(m.streams, id)
	for i, v := range m.order {
		if v == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			if m.round >= len(m.order) {
				m.round = 0
			}
			break
		}
	}
	m.mu.Unlock()
}
