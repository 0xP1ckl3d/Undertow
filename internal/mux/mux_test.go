package mux

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"

	"undertow/internal/security"
	"undertow/internal/session"
)

type memoryTransport struct {
	in   chan []byte
	out  chan []byte
	done chan struct{}
	once sync.Once
}

type fullDataQueueTransport struct{ sent []byte }

func (t *fullDataQueueTransport) Send(_ context.Context, b []byte) error {
	if len(t.sent) == 0 {
		return session.ErrQueueFull
	}
	t.sent = append(t.sent, b[1])
	return nil
}

func (t *fullDataQueueTransport) SendPriority(_ context.Context, b []byte) error {
	t.sent = append(t.sent, b[1])
	return nil
}

func (t *fullDataQueueTransport) Recv(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (t *fullDataQueueTransport) Close() error { return nil }

func TestControlBypassesBlockedDataSend(t *testing.T) {
	transport := &fullDataQueueTransport{}
	m := &Mux{transport: transport, ctx: context.Background(), control: make(chan frame, 1), done: make(chan struct{})}
	m.control <- frame{kind: frameOpen, id: 2, data: []byte("example.test:443")}
	if !m.sendFrame(frame{kind: frameData, id: 2, data: []byte("bulk")}) {
		t.Fatal("send loop stopped")
	}
	if !bytes.Equal(transport.sent, []byte{frameOpen, frameData}) {
		t.Fatalf("send order = %v, want control before blocked data", transport.sent)
	}
}

func memoryPair() (*memoryTransport, *memoryTransport) {
	a := make(chan []byte, 4096)
	b := make(chan []byte, 4096)
	return &memoryTransport{in: b, out: a, done: make(chan struct{})}, &memoryTransport{in: a, out: b, done: make(chan struct{})}
}

func TestSleepGrantCancelsWhenNewStreamOpens(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := memoryPair()
	server := New(ctx, a, true)
	agent := New(ctx, b, false)
	defer server.Close()
	defer agent.Close()
	if !server.TryQuiesce() {
		t.Fatal("idle server could not quiesce")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s, err := agent.Accept(ctx)
		if err == nil {
			_ = s.AcceptOpen(ctx)
			_ = s.Close()
		}
	}()
	s, err := server.Open(ctx, "example.invalid:1")
	if err != nil {
		t.Fatal(err)
	}
	if server.IsQuiesced() || server.CommitQuiesce() {
		t.Fatal("new live stream did not cancel sleep grant")
	}
	_ = s.Close()
	<-done
}
func (m *memoryTransport) Send(ctx context.Context, b []byte) error {
	select {
	case m.out <- append([]byte(nil), b...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.done:
		return io.EOF
	}
}
func (m *memoryTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-m.in:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.done:
		return nil, io.EOF
	}
}
func (m *memoryTransport) Close() error { m.once.Do(func() { close(m.done) }); return nil }

func TestTwoHundredConcurrentStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, b := memoryPair()
	proxy := New(ctx, a, true)
	agent := New(ctx, b, false)
	defer proxy.Close()
	defer agent.Close()
	go func() {
		for {
			s, err := agent.Accept(ctx)
			if err != nil {
				return
			}
			go func() {
				defer s.Close()
				if err := s.AcceptOpen(ctx); err != nil {
					return
				}
				_, _ = io.Copy(s, s)
				_ = s.CloseWrite()
			}()
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := proxy.Open(ctx, fmt.Sprintf("127.0.0.1:%d", 10000+i))
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			want := bytes.Repeat([]byte{byte(i)}, 4096+i%17)
			if _, err = s.Write(want); err != nil {
				errs <- err
				return
			}
			if err = s.CloseWrite(); err != nil {
				errs <- err
				return
			}
			got, err := io.ReadAll(s)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, want) {
				errs <- fmt.Errorf("stream %d mismatch: got %d bytes, want %d", i, len(got), len(want))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSlowStreamDoesNotBlockInteractive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := memoryPair()
	proxy := New(ctx, a, true)
	agent := New(ctx, b, false)
	defer proxy.Close()
	defer agent.Close()
	release := make(chan struct{})
	go func() {
		for {
			s, err := agent.Accept(ctx)
			if err != nil {
				return
			}
			go func() {
				defer s.Close()
				_ = s.AcceptOpen(ctx)
				if s.Destination() == "slow.example:1" {
					<-release
					_, _ = io.Copy(io.Discard, s)
					_ = s.CloseWrite()
					return
				}
				_, _ = io.Copy(s, s)
				_ = s.CloseWrite()
			}()
		}
	}()
	slow, err := proxy.Open(ctx, "slow.example:1")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	bulkDone := make(chan error, 1)
	go func() { _, err := slow.Write(bytes.Repeat([]byte("B"), streamWindow*3)); bulkDone <- err }()
	select {
	case <-bulkDone:
		t.Fatal("bulk stream did not backpressure")
	case <-time.After(50 * time.Millisecond):
	}
	interactive, err := proxy.Open(ctx, "fast.example:2")
	if err != nil {
		t.Fatal(err)
	}
	defer interactive.Close()
	start := time.Now()
	_, err = interactive.Write([]byte("ping"))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err = io.ReadFull(interactive, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("got %q", buf)
	}
	if time.Since(start) > time.Second {
		t.Fatal("interactive stream stalled behind bulk stream")
	}
	close(release)
	select {
	case err := <-bulkDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("bulk stream did not recover")
	}
}

func TestOutOfOrderDataAndFin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := memoryPair()
	proxy := New(ctx, a, true)
	defer proxy.Close()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		openBytes := <-b.in
		open, err := decodeFrame(openBytes)
		if err != nil {
			done <- err
			return
		}
		for _, f := range []frame{{kind: frameOpenOK, id: open.id}, {kind: frameData, id: open.id, offset: 5, data: []byte("world")}, {kind: frameFin, id: open.id, offset: 10}, {kind: frameData, id: open.id, offset: 0, data: []byte("hello")}} {
			b.out <- f.encode()
		}
		done <- nil
	}()
	s, err := proxy.Open(ctx, "example.com:80")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := io.ReadAll(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "helloworld" {
		t.Fatalf("got %q", got)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestResetOnlyClosesItsStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := memoryPair()
	proxy := New(ctx, a, true)
	agent := New(ctx, b, false)
	defer proxy.Close()
	defer agent.Close()
	accepted := make(chan *Stream, 2)
	go func() {
		for i := 0; i < 2; i++ {
			s, err := agent.Accept(ctx)
			if err != nil {
				return
			}
			accepted <- s
			_ = s.AcceptOpen(ctx)
		}
	}()
	first, err := proxy.Open(ctx, "first.example:1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := proxy.Open(ctx, "second.example:2")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	remote := map[string]*Stream{}
	for i := 0; i < 2; i++ {
		select {
		case s := <-accepted:
			remote[s.Destination()] = s
		case <-ctx.Done():
			t.Fatal("accept timed out")
		}
	}
	first.Close()
	select {
	case <-remote["first.example:1"].Done():
	case <-ctx.Done():
		t.Fatal("reset did not reach peer")
	}
	if _, err := second.Write([]byte("still alive")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("still alive"))
	if _, err := io.ReadFull(remote["second.example:2"], buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "still alive" {
		t.Fatalf("got %q", buf)
	}
	remote["second.example:2"].Close()
}

func TestControlChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := memoryPair()
	proxy, agent := New(ctx, a, true), New(ctx, b, false)
	defer proxy.Close()
	defer agent.Close()
	if err := proxy.SendControl(ctx, []byte("agent-info-request")); err != nil {
		t.Fatal(err)
	}
	got, err := agent.RecvControl(ctx)
	if err != nil || string(got) != "agent-info-request" {
		t.Fatalf("control: %q, %v", got, err)
	}
	if err := agent.SendControl(ctx, []byte("agent-info-response")); err != nil {
		t.Fatal(err)
	}
	got, err = proxy.RecvControl(ctx)
	if err != nil || string(got) != "agent-info-response" {
		t.Fatalf("control: %q, %v", got, err)
	}
}

func TestDisconnectResetsOldStreamsAndNewSessionWorks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := memoryPair()
	proxy, agent := New(ctx, a, true), New(ctx, b, false)
	go func() {
		s, err := agent.Accept(ctx)
		if err == nil {
			_ = s.AcceptOpen(ctx)
		}
	}()
	old, err := proxy.Open(ctx, "example.com:80")
	if err != nil {
		t.Fatal(err)
	}
	proxy.Close()
	agent.Close()
	select {
	case <-old.Done():
	case <-ctx.Done():
		t.Fatal("old stream remained live")
	}
	if _, err := old.Write([]byte("stale")); err == nil {
		t.Fatal("write on disconnected stream succeeded")
	}
	c, d := memoryPair()
	freshProxy, freshAgent := New(ctx, c, true), New(ctx, d, false)
	defer freshProxy.Close()
	defer freshAgent.Close()
	go func() {
		s, err := freshAgent.Accept(ctx)
		if err == nil {
			_ = s.AcceptOpen(ctx)
			_, _ = io.Copy(s, s)
			_ = s.CloseWrite()
		}
	}()
	fresh, err := freshProxy.Open(ctx, "example.com:80")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := fresh.Write([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	if err := fresh.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(fresh)
	if err != nil || string(got) != "fresh" {
		t.Fatalf("fresh session: %q, %v", got, err)
	}
}

func TestStreamsOverLossyReliableSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var keys security.Keys
	for i := range keys.ClientToServer {
		keys.ClientToServer[i], keys.ServerToClient[i] = byte(i+1), byte(i+51)
	}
	copy(keys.ClientPrefix[:], []byte{1, 2, 3, 4})
	copy(keys.ServerPrefix[:], []byte{5, 6, 7, 8})
	a, err := session.New(99, keys, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := session.New(99, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	proxy, agent := New(ctx, a, true), New(ctx, b, false)
	defer proxy.Close()
	defer agent.Close()
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		rng := rand.New(rand.NewSource(79))
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
	go func() {
		for {
			s, err := agent.Accept(ctx)
			if err != nil {
				return
			}
			go func() { defer s.Close(); _ = s.AcceptOpen(ctx); _, _ = io.Copy(s, s); _ = s.CloseWrite() }()
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := proxy.Open(ctx, fmt.Sprintf("127.0.0.1:%d", 1000+i))
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			want := bytes.Repeat([]byte{byte(i + 1)}, 4096)
			if _, err = s.Write(want); err != nil {
				errs <- err
				return
			}
			if err = s.CloseWrite(); err != nil {
				errs <- err
				return
			}
			got, err := io.ReadAll(s)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, want) {
				errs <- fmt.Errorf("flow %d mismatch", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if a.Stats().Retransmits+b.Stats().Retransmits == 0 {
		t.Error("loss did not trigger retransmission")
	}
}

func FuzzDecodeFrame(f *testing.F) {
	for _, seed := range []frame{
		{kind: frameOpen, id: 2, data: []byte("127.0.0.1:80")},
		{kind: frameData, id: 2, offset: 12, data: []byte("hello")},
		{kind: frameFin, id: 2, offset: 17},
		{kind: frameWindow, id: 2, offset: 5},
		{kind: frameControl, id: 0, data: []byte("control")},
	} {
		f.Add(seed.encode())
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		decoded, err := decodeFrame(b)
		if err != nil {
			return
		}
		if !bytes.Equal(decoded.encode(), b) {
			t.Fatal("frame round trip mismatch")
		}
	})
}
