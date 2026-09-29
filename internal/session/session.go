package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	mathbits "math/bits"
	"sync"
	"time"

	"undertow/internal/security"
)

const (
	headerSize   = 50
	maxPacket    = 1050
	fragmentSize = 800
	maxMessage   = 64 << 10
	maxQueued    = 4096
	maxPriority  = 256
	maxPending   = 64
)

var ErrClosed = errors.New("session closed")
var ErrWindow = errors.New("session receive window exceeded")
var ErrQueueFull = errors.New("session send queue full")

type fragment struct {
	id     uint64
	total  uint32
	offset uint32
	data   []byte
}
type pending struct {
	wire         []byte
	sent         time.Time
	retries      uint32
	fast         bool
	fastEvidence uint64
}
type assembly struct {
	total   uint32
	chunks  map[uint32][]byte
	count   uint32
	created time.Time
}

type Stats struct {
	TXBytes            uint64
	RXBytes            uint64
	TXPackets          uint64
	RXPackets          uint64
	Retransmits        uint64
	Duplicates         uint64
	RTT                time.Duration
	InFlight           int
	Queued             int
	CongestionWindow   int
	PeerReceiveWindow  int
	ReceiveWindow      int
	FragmentSize       int
	PayloadAdjustments uint64
}

type Session struct {
	mu              sync.Mutex
	id              uint64
	tx              cipher.AEAD
	rx              cipher.AEAD
	txPrefix        [4]byte
	rxPrefix        [4]byte
	nonceSeq        uint64
	dataSeq         uint64
	peerAckBase     uint64
	msgSeq          uint64
	ackBase         uint64
	received        map[uint64]bool
	seen            map[uint64]bool
	maxSeen         uint64
	ackDirty        bool
	pending         map[uint64]*pending
	queue           []fragment
	priority        []fragment
	reassembly      map[uint64]*assembly
	deliver         chan []byte
	wake            chan struct{}
	done            chan struct{}
	closed          bool
	stats           Stats
	cwnd            int
	ackCount        int
	fragmentSize    int
	peerWindow      int
	fragmentCeiling int
	adaptivePayload bool
	successAcks     int
}

func New(id uint64, keys security.Keys, client bool) (*Session, error) {
	return NewWithFragment(id, keys, client, fragmentSize)
}

func NewWithFragment(id uint64, keys security.Keys, client bool, size int) (*Session, error) {
	if id == 0 {
		return nil, errors.New("zero session ID")
	}
	if size < 128 || size > fragmentSize {
		return nil, errors.New("invalid fragment size")
	}
	var txKey, rxKey []byte
	var txPrefix, rxPrefix [4]byte
	if client {
		txKey = keys.ClientToServer[:]
		rxKey = keys.ServerToClient[:]
		txPrefix = keys.ClientPrefix
		rxPrefix = keys.ServerPrefix
	} else {
		txKey = keys.ServerToClient[:]
		rxKey = keys.ClientToServer[:]
		txPrefix = keys.ServerPrefix
		rxPrefix = keys.ClientPrefix
	}
	tb, err := aes.NewCipher(txKey)
	if err != nil {
		return nil, err
	}
	rb, err := aes.NewCipher(rxKey)
	if err != nil {
		return nil, err
	}
	tx, err := cipher.NewGCM(tb)
	if err != nil {
		return nil, err
	}
	rx, err := cipher.NewGCM(rb)
	if err != nil {
		return nil, err
	}
	return &Session{id: id, tx: tx, rx: rx, txPrefix: txPrefix, rxPrefix: rxPrefix, received: make(map[uint64]bool), seen: make(map[uint64]bool), pending: make(map[uint64]*pending), reassembly: make(map[uint64]*assembly), deliver: make(chan []byte, 64), wake: make(chan struct{}, 1), done: make(chan struct{}), cwnd: 16, fragmentSize: size, fragmentCeiling: size, peerWindow: maxPending}, nil
}

func NewAdaptive(id uint64, keys security.Keys, client bool, size int) (*Session, error) {
	s, err := NewWithFragment(id, keys, client, size)
	if err == nil {
		s.adaptivePayload = true
	}
	return s, err
}

func (s *Session) ID() uint64            { return s.id }
func (s *Session) Wake() <-chan struct{} { return s.wake }
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) Send(ctx context.Context, b []byte) error {
	return s.enqueue(ctx, b, false)
}

// SendPriority puts control traffic ahead of queued bulk messages. It has a
// separate bounded queue so a full data backlog cannot block stream opens,
// resets, or receive-window updates.
func (s *Session) SendPriority(ctx context.Context, b []byte) error {
	return s.enqueue(ctx, b, true)
}

func (s *Session) enqueue(ctx context.Context, b []byte, priority bool) error {
	if len(b) == 0 || len(b) > maxMessage {
		return fmt.Errorf("message size must be 1..%d", maxMessage)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	parts := (len(b) + s.fragmentSize - 1) / s.fragmentSize
	limit := maxQueued
	queued := len(s.queue)
	if priority {
		limit = maxPriority
		queued = len(s.priority)
	}
	if queued+parts > limit {
		return ErrQueueFull
	}
	s.msgSeq++
	payload := append([]byte(nil), b...)
	for o := 0; o < len(b); o += s.fragmentSize {
		end := o + s.fragmentSize
		if end > len(b) {
			end = len(b)
		}
		f := fragment{id: s.msgSeq, total: uint32(len(b)), offset: uint32(o), data: payload[o:end]}
		if priority {
			s.priority = append(s.priority, f)
		} else {
			s.queue = append(s.queue, f)
		}
	}
	s.signal()
	return nil
}

func (s *Session) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-s.deliver:
		return b, nil
	case <-s.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Session) TryRecv() ([]byte, bool) {
	select {
	case b := <-s.deliver:
		return b, true
	default:
		return nil, false
	}
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	return nil
}

func (s *Session) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.stats
	v.InFlight = len(s.pending)
	v.Queued = len(s.queue) + len(s.priority)
	v.CongestionWindow = s.cwnd
	v.PeerReceiveWindow = s.peerWindow
	v.ReceiveWindow = maxPending - len(s.received)
	v.FragmentSize = s.fragmentSize
	return v
}

// A selective ACK releases a packet buffer, but a gap still occupies the
// receiver's sequence window. Keep new data within that fixed window.
func (s *Session) canSendData() bool {
	return len(s.queue)+len(s.priority) > 0 && len(s.pending) < s.cwnd && s.dataSeq-s.peerAckBase < 64
}

func (s *Session) HasWork(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ackDirty {
		return true
	}
	if s.canSendData() {
		return true
	}
	for _, p := range s.pending {
		if s.retransmitReady(p, now) {
			return true
		}
	}
	return false
}

// NextPacket returns a retransmission, a new data packet, or an authenticated poll.
func (s *Session) NextPacket(now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	var oldest *pending
	for _, p := range s.pending {
		if s.retransmitReady(p, now) && (oldest == nil || p.sent.Before(oldest.sent)) {
			oldest = p
		}
	}
	if oldest != nil {
		oldest.fast = false
		oldest.sent = now
		oldest.retries++
		if s.adaptivePayload && s.fragmentSize > 128 {
			s.fragmentSize = max(128, s.fragmentSize-64)
			s.successAcks = 0
			s.stats.PayloadAdjustments++
		}
		s.cwnd /= 2
		if s.cwnd < 2 {
			s.cwnd = 2
		}
		s.stats.Retransmits++
		s.stats.TXPackets++
		s.stats.TXBytes += uint64(len(oldest.wire))
		return oldest.wire, nil
	}
	var data []byte
	var dataSeq uint64
	if s.canSendData() {
		var f fragment
		if len(s.priority) > 0 {
			f = s.priority[0]
			s.priority[0] = fragment{}
			s.priority = s.priority[1:]
		} else {
			f = s.queue[0]
			s.queue[0] = fragment{}
			s.queue = s.queue[1:]
		}
		s.dataSeq++
		dataSeq = s.dataSeq
		data = make([]byte, 18+len(f.data))
		binary.BigEndian.PutUint64(data[:8], f.id)
		binary.BigEndian.PutUint32(data[8:12], f.total)
		binary.BigEndian.PutUint32(data[12:16], f.offset)
		binary.BigEndian.PutUint16(data[16:18], uint16(len(f.data)))
		copy(data[18:], f.data)
	}
	s.nonceSeq++
	h := make([]byte, headerSize, headerSize+len(data)+s.tx.Overhead())
	h[0] = 1
	if dataSeq != 0 {
		h[1] = 1
	}
	binary.BigEndian.PutUint64(h[2:10], s.id)
	binary.BigEndian.PutUint16(h[10:12], 1)
	binary.BigEndian.PutUint64(h[12:20], s.nonceSeq)
	binary.BigEndian.PutUint64(h[20:28], dataSeq)
	binary.BigEndian.PutUint64(h[28:36], s.ackBase)
	var bits uint64
	for v := range s.received {
		if v > s.ackBase && v <= s.ackBase+64 {
			bits |= 1 << (v - s.ackBase - 1)
		}
	}
	binary.BigEndian.PutUint64(h[36:44], bits)
	binary.BigEndian.PutUint32(h[44:48], maxPending-uint32(len(s.received)))
	binary.BigEndian.PutUint16(h[48:50], uint16(len(data)))
	var nonce [12]byte
	copy(nonce[:], s.txPrefix[:])
	binary.BigEndian.PutUint64(nonce[4:], s.nonceSeq)
	wire := s.tx.Seal(h, nonce[:], data, h)
	s.ackDirty = false
	if len(wire) > maxPacket {
		return nil, errors.New("encrypted packet too large")
	}
	if dataSeq != 0 {
		s.pending[dataSeq] = &pending{wire: wire, sent: now}
	}
	// A single wake token only releases one pending DNS poll. Hand another
	// token to the next poll while sendable fragments remain.
	if s.canSendData() {
		s.signal()
	}
	s.stats.TXPackets++
	s.stats.TXBytes += uint64(len(wire))
	return wire, nil
}

func (s *Session) Process(wire []byte, now time.Time) error {
	if len(wire) < headerSize+16 || len(wire) > maxPacket {
		return errors.New("invalid encrypted packet length")
	}
	h := wire[:headerSize]
	if h[0] != 1 || binary.BigEndian.Uint64(h[2:10]) != s.id || binary.BigEndian.Uint16(h[10:12]) != 1 {
		return errors.New("packet version/session mismatch")
	}
	nonceSeq := binary.BigEndian.Uint64(h[12:20])
	if nonceSeq == 0 {
		return errors.New("zero packet nonce")
	}
	var nonce [12]byte
	copy(nonce[:], s.rxPrefix[:])
	binary.BigEndian.PutUint64(nonce[4:], nonceSeq)
	plain, err := s.rx.Open(nil, nonce[:], wire[headerSize:], h)
	if err != nil {
		return err
	}
	if int(binary.BigEndian.Uint16(h[48:50])) != len(plain) {
		return errors.New("packet plaintext length mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.seen[nonceSeq] {
		s.stats.Duplicates++
		return nil
	}
	if s.maxSeen > 4096 && nonceSeq+4096 < s.maxSeen {
		s.stats.Duplicates++
		return nil
	}
	if nonceSeq > s.maxSeen {
		s.maxSeen = nonceSeq
	}
	if len(s.seen) > 8192 {
		for v := range s.seen {
			if v+4096 < s.maxSeen {
				delete(s.seen, v)
			}
		}
	}
	ack := binary.BigEndian.Uint64(h[28:36])
	bits := binary.BigEndian.Uint64(h[36:44])
	window := binary.BigEndian.Uint32(h[44:48])
	if window > maxPending {
		return errors.New("invalid advertised receive window")
	}
	if ack > s.dataSeq {
		return errors.New("ACK beyond sent data")
	}
	ackAdvanced := ack > s.peerAckBase
	if ackAdvanced {
		s.peerAckBase = ack
	}
	s.peerWindow = int(window)
	newAcks := 0
	for seq, p := range s.pending {
		acknowledged := seq <= ack
		if !acknowledged && seq > ack && seq <= ack+64 {
			acknowledged = bits&(1<<(seq-ack-1)) != 0
		}
		if acknowledged {
			newAcks++
			if p.retries == 0 {
				sample := now.Sub(p.sent)
				if s.stats.RTT == 0 {
					s.stats.RTT = sample
				} else {
					s.stats.RTT = (7*s.stats.RTT + sample) / 8
				}
			}
			delete(s.pending, seq)
		}
	}
	s.ackCount += newAcks
	if s.adaptivePayload && newAcks > 0 {
		s.successAcks += newAcks
		if s.successAcks >= 64 && s.fragmentSize < s.fragmentCeiling {
			s.fragmentSize = min(s.fragmentCeiling, s.fragmentSize+64)
			s.successAcks = 0
			s.stats.PayloadAdjustments++
		}
	}
	if s.ackCount >= s.cwnd && s.cwnd < maxPending {
		s.cwnd++
		s.ackCount = 0
	}
	if bits != 0 {
		highest := ack + uint64(64-mathbits.LeadingZeros64(bits))
		for seq, p := range s.pending {
			if seq <= ack || seq > ack+64 {
				continue
			}
			higher := 0
			for j := seq - ack; j < 64; j++ {
				if bits&(1<<j) != 0 {
					higher++
				}
			}
			if higher >= 3 && highest > p.fastEvidence {
				p.fast = true
				p.fastEvidence = highest
			}
		}
	}
	if (newAcks > 0 || ackAdvanced) && s.canSendData() {
		s.signal()
	}
	s.stats.RXPackets++
	s.stats.RXBytes += uint64(len(wire))
	seq := binary.BigEndian.Uint64(h[20:28])
	if seq == 0 {
		if len(plain) != 0 || h[1] != 0 {
			return errors.New("invalid poll payload")
		}
		s.seen[nonceSeq] = true
		return nil
	}
	if h[1] != 1 || len(plain) < 18 {
		return errors.New("invalid data payload")
	}
	if seq <= s.ackBase || s.received[seq] {
		s.seen[nonceSeq] = true
		s.stats.Duplicates++
		s.ackDirty = true
		s.signal()
		return nil
	}
	if seq > s.ackBase+64 {
		return ErrWindow
	}
	id := binary.BigEndian.Uint64(plain[:8])
	total := binary.BigEndian.Uint32(plain[8:12])
	off := binary.BigEndian.Uint32(plain[12:16])
	n := int(binary.BigEndian.Uint16(plain[16:18]))
	if id == 0 || total == 0 || total > maxMessage || n == 0 || n > s.fragmentCeiling || n != len(plain)-18 || uint64(off)+uint64(n) > uint64(total) {
		return errors.New("invalid fragment")
	}
	if err = s.addFragment(id, total, off, plain[18:], now); err != nil {
		return err
	}
	s.seen[nonceSeq] = true
	s.received[seq] = true
	for s.received[s.ackBase+1] {
		s.ackBase++
		delete(s.received, s.ackBase)
	}
	s.ackDirty = true
	s.signal()
	return nil
}

func (s *Session) addFragment(id uint64, total, off uint32, data []byte, now time.Time) error {
	for k, v := range s.reassembly {
		if now.Sub(v.created) > 30*time.Second {
			delete(s.reassembly, k)
		}
	}
	a := s.reassembly[id]
	if a == nil {
		if len(s.reassembly) >= 32 {
			return errors.New("too many incomplete messages")
		}
		a = &assembly{total: total, chunks: make(map[uint32][]byte), created: now}
		s.reassembly[id] = a
	}
	if a.total != total {
		return errors.New("inconsistent fragment total")
	}
	if old, ok := a.chunks[off]; ok {
		if string(old) != string(data) {
			return errors.New("conflicting duplicate fragment")
		}
		if a.count == a.total {
			return s.deliverAssembly(id, a)
		}
		return nil
	}
	for p, v := range a.chunks {
		if uint64(off) < uint64(p)+uint64(len(v)) && uint64(p) < uint64(off)+uint64(len(data)) {
			return errors.New("overlapping fragments")
		}
	}
	a.chunks[off] = append([]byte(nil), data...)
	a.count += uint32(len(data))
	if a.count == a.total {
		return s.deliverAssembly(id, a)
	}
	return nil
}

func (s *Session) deliverAssembly(id uint64, a *assembly) error {
	b := make([]byte, a.total)
	for p, v := range a.chunks {
		copy(b[p:], v)
	}
	select {
	case s.deliver <- b:
		delete(s.reassembly, id)
		return nil
	default:
		return errors.New("receive queue full")
	}
}

func (s *Session) rto() time.Duration {
	if s.stats.RTT == 0 {
		return 500 * time.Millisecond
	}
	v := 3 * s.stats.RTT
	if v < 150*time.Millisecond {
		return 150 * time.Millisecond
	}
	if v > 3*time.Second {
		return 3 * time.Second
	}
	return v
}
func (s *Session) rtoFor(p *pending) time.Duration {
	v := s.rto()
	for i := uint32(0); i < p.retries && i < 4; i++ {
		v *= 2
	}
	if v > 5*time.Second {
		return 5 * time.Second
	}
	return v
}
func (s *Session) retransmitReady(p *pending, now time.Time) bool {
	age := now.Sub(p.sent)
	if age >= s.rtoFor(p) {
		return true
	}
	if !p.fast {
		return false
	}
	// Concurrent DNS responses can reorder by a few milliseconds. Wait for
	// the gap to persist before treating selective ACK evidence as loss.
	delay := 2 * s.stats.RTT
	if delay < 25*time.Millisecond {
		delay = 25 * time.Millisecond
	}
	return age >= delay
}
func (s *Session) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
