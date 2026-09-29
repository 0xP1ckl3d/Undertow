//go:build windows

package netstack

import (
	"context"
	"encoding/binary"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type packetTransport struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func pair() (*packetTransport, *packetTransport) {
	a, b := make(chan []byte, 512), make(chan []byte, 512)
	return &packetTransport{in: b, out: a, done: make(chan struct{})}, &packetTransport{in: a, out: b, done: make(chan struct{})}
}
func (p *packetTransport) Send(ctx context.Context, b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return io.EOF
	}
}
func (p *packetTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *packetTransport) Close() error { p.once.Do(func() { close(p.done) }); return nil }

func TestICMPEchoThroughAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	a, b := pair()
	proxy, agent := mux.New(ctx, a, true), mux.New(ctx, b, false)
	defer proxy.Close()
	defer agent.Close()
	go pivot.ServeAgent(ctx, agent)
	source := netip.MustParseAddr("172.16.254.2")
	target := netip.MustParseAddr("127.0.0.1")
	body := make([]byte, 8+12)
	body[0] = 8
	binary.BigEndian.PutUint16(body[4:6], 123)
	binary.BigEndian.PutUint16(body[6:8], 1)
	copy(body[8:], []byte("undertow-icmp"))
	binary.BigEndian.PutUint16(body[2:4], packetChecksum(body))
	request := makeIPv4ICMP(source, target, body)
	results := make(chan []byte, 1)
	if !handleICMPEcho(ctx, request, netip.MustParseAddr("172.16.254.1"), func(netip.Addr) *mux.Mux { return proxy }, func(b []byte) error { results <- append([]byte(nil), b...); return nil }) {
		t.Fatal("echo not handled")
	}
	select {
	case reply := <-results:
		if len(reply) != len(request) || reply[20] != 0 || packetChecksum(reply[:20]) != 0 || packetChecksum(reply[20:]) != 0 {
			t.Fatalf("invalid echo reply: %x", reply)
		}
		if reply[12] != 127 || reply[16] != 172 {
			t.Fatal("wrong reply addresses")
		}
	case <-ctx.Done():
		t.Fatal("agent echo timed out")
	}
}
