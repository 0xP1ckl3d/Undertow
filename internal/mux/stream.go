package mux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

type Stream struct {
	mux          *Mux
	id           uint64
	destination  string
	mu           sync.Mutex
	readBuffer   bytes.Buffer
	outOfOrder   map[uint64][]byte
	recvBuffered int
	recvNext     uint64
	recvFin      bool
	finalOffset  uint64
	sendOffset   uint64
	sendCredit   int
	sendClosed   bool
	openResult   chan error
	out          chan frame
	readWake     chan struct{}
	writeWake    chan struct{}
	done         chan struct{}
	once         sync.Once
}

func newStream(m *Mux, id uint64, destination string) *Stream {
	return &Stream{mux: m, id: id, destination: destination, outOfOrder: make(map[uint64][]byte), sendCredit: streamWindow, openResult: make(chan error, 1), out: make(chan frame, 8), readWake: make(chan struct{}, 1), writeWake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (s *Stream) ID() uint64            { return s.id }
func (s *Stream) Destination() string   { return s.destination }
func (s *Stream) Done() <-chan struct{} { return s.done }

// AcceptOpen confirms that the agent successfully opened the remote socket.
func (s *Stream) AcceptOpen(ctx context.Context) error {
	return s.mux.controlFrame(ctx, frame{kind: frameOpenOK, id: s.id})
}

func (s *Stream) Fail(err error) {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	_ = s.mux.controlFrame(s.mux.ctx, frame{kind: frameOpenFail, id: s.id, data: []byte(msg)})
	s.resetLocal()
	s.mux.remove(s.id)
}

func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		s.mu.Lock()
		if s.readBuffer.Len() > 0 {
			n, _ := s.readBuffer.Read(p)
			s.recvBuffered -= n
			s.mu.Unlock()
			_ = s.mux.controlFrame(s.mux.ctx, frame{kind: frameWindow, id: s.id, offset: uint64(n)})
			return n, nil
		}
		if s.recvFin && s.recvNext >= s.finalOffset {
			s.mu.Unlock()
			return 0, io.EOF
		}
		s.mu.Unlock()
		select {
		case <-s.readWake:
		case <-s.done:
			return 0, io.EOF
		case <-s.mux.done:
			return 0, io.EOF
		}
	}
}

func (s *Stream) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		s.mu.Lock()
		select {
		case <-s.done:
			s.mu.Unlock()
			return written, io.ErrClosedPipe
		default:
		}
		if s.sendClosed {
			s.mu.Unlock()
			return written, io.ErrClosedPipe
		}
		credit := s.sendCredit
		if credit == 0 {
			s.mu.Unlock()
			select {
			case <-s.writeWake:
				continue
			case <-s.done:
				return written, io.ErrClosedPipe
			case <-s.mux.done:
				return written, io.EOF
			}
		}
		n := len(p)
		if n > credit {
			n = credit
		}
		if n > maxFrameData {
			n = maxFrameData
		}
		off := s.sendOffset
		s.sendOffset += uint64(n)
		s.sendCredit -= n
		s.mu.Unlock()
		f := frame{kind: frameData, id: s.id, offset: off, data: append([]byte(nil), p[:n]...)}
		select {
		case s.out <- f:
			s.mux.signal()
			written += n
			p = p[n:]
		case <-s.done:
			return written, io.ErrClosedPipe
		case <-s.mux.done:
			return written, io.EOF
		}
	}
	return written, nil
}

func (s *Stream) CloseWrite() error {
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return io.ErrClosedPipe
	default:
	}
	if s.sendClosed {
		s.mu.Unlock()
		return nil
	}
	s.sendClosed = true
	offset := s.sendOffset
	s.mu.Unlock()
	return s.mux.controlFrame(s.mux.ctx, frame{kind: frameFin, id: s.id, offset: offset})
}

func (s *Stream) Close() error {
	s.mu.Lock()
	graceful := s.sendClosed && s.recvFin && s.recvNext >= s.finalOffset
	s.mu.Unlock()
	if graceful {
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
	drain:
		for len(s.out) > 0 {
			select {
			case <-s.mux.done:
				break drain
			case <-deadline.C:
				break drain
			case <-time.After(time.Millisecond):
			}
		}
		if len(s.out) == 0 {
			s.resetLocal()
			s.mux.remove(s.id)
			return nil
		}
	}
	_ = s.mux.controlFrame(s.mux.ctx, frame{kind: frameReset, id: s.id})
	s.resetLocal()
	s.mux.remove(s.id)
	return nil
}

func (s *Stream) resetLocal() { s.once.Do(func() { close(s.done); s.signalRead(); s.signalWrite() }) }

func (s *Stream) receiveData(offset uint64, data []byte) error {
	if len(data) == 0 || len(data) > maxFrameData {
		return errFrame
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return io.EOF
	default:
	}
	if offset < s.recvNext {
		return nil
	}
	if offset > s.recvNext+streamWindow || uint64(len(data)) > s.recvNext+streamWindow-offset {
		return errors.New("stream receive window exceeded")
	}
	if s.recvFin && offset+uint64(len(data)) > s.finalOffset {
		return errFrame
	}
	if s.recvBuffered+len(data) > streamWindow {
		return errors.New("stream buffer full")
	}
	if _, exists := s.outOfOrder[offset]; exists {
		return nil
	}
	for other, b := range s.outOfOrder {
		if offset < other+uint64(len(b)) && other < offset+uint64(len(data)) {
			return errFrame
		}
	}
	s.recvBuffered += len(data)
	if offset == s.recvNext {
		s.readBuffer.Write(data)
		s.recvNext += uint64(len(data))
		for {
			next, ok := s.outOfOrder[s.recvNext]
			if !ok {
				break
			}
			delete(s.outOfOrder, s.recvNext)
			s.readBuffer.Write(next)
			s.recvNext += uint64(len(next))
		}
		s.signalRead()
	} else {
		s.outOfOrder[offset] = append([]byte(nil), data...)
	}
	return nil
}

func (s *Stream) receiveFin(offset uint64) {
	s.mu.Lock()
	if !s.recvFin && offset >= s.recvNext && offset <= s.recvNext+streamWindow {
		s.recvFin = true
		s.finalOffset = offset
	}
	s.mu.Unlock()
	s.signalRead()
}

func (s *Stream) addCredit(n int) {
	s.mu.Lock()
	s.sendCredit += n
	if s.sendCredit > streamWindow {
		s.sendCredit = streamWindow
	}
	s.mu.Unlock()
	s.signalWrite()
}

func (s *Stream) signalRead() {
	select {
	case s.readWake <- struct{}{}:
	default:
	}
}
func (s *Stream) signalWrite() {
	select {
	case s.writeWake <- struct{}{}:
	default:
	}
}
