//go:build linux || windows

package netstack

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/security"
	"undertow/internal/transport/dns"
)

type memorySession struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func memoryPair() (*memorySession, *memorySession) {
	a, b := make(chan []byte, 128), make(chan []byte, 128)
	return &memorySession{in: b, out: a, done: make(chan struct{})}, &memorySession{in: a, out: b, done: make(chan struct{})}
}
func (m *memorySession) Send(ctx context.Context, p []byte) error {
	select {
	case m.out <- append([]byte(nil), p...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.done:
		return io.EOF
	}
}
func (m *memorySession) Recv(ctx context.Context) ([]byte, error) {
	select {
	case p := <-m.in:
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.done:
		return nil, io.EOF
	}
}
func (m *memorySession) Close() error { m.once.Do(func() { close(m.done) }); return nil }

type packetDevice struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func (d *packetDevice) Read(p []byte) (int, error) {
	select {
	case b := <-d.in:
		return copy(p, b), nil
	case <-d.done:
		return 0, io.EOF
	}
}
func (d *packetDevice) Write(p []byte) (int, error) {
	select {
	case d.out <- append([]byte(nil), p...):
		return len(p), nil
	case <-d.done:
		return 0, io.EOF
	}
}
func (d *packetDevice) Close() error { d.once.Do(func() { close(d.done) }); return nil }

func tcpTestPacket(src, dst netip.Addr, srcPort, dstPort uint16, seq, ack uint32, flags byte, payload []byte) []byte {
	b := make([]byte, 40+len(payload))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	b[8], b[9] = 64, 6
	a, z := src.As4(), dst.As4()
	copy(b[12:16], a[:])
	copy(b[16:20], z[:])
	binary.BigEndian.PutUint16(b[20:22], srcPort)
	binary.BigEndian.PutUint16(b[22:24], dstPort)
	binary.BigEndian.PutUint32(b[24:28], seq)
	binary.BigEndian.PutUint32(b[28:32], ack)
	b[32], b[33] = 0x50, flags
	binary.BigEndian.PutUint16(b[34:36], 65535)
	copy(b[40:], payload)
	pseudo := make([]byte, 12+len(b)-20)
	copy(pseudo[0:4], a[:])
	copy(pseudo[4:8], z[:])
	pseudo[9] = 6
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(b)-20))
	copy(pseudo[12:], b[20:])
	binary.BigEndian.PutUint16(b[36:38], packetChecksum(pseudo))
	binary.BigEndian.PutUint16(b[10:12], packetChecksum(b[:20]))
	return b
}

func runTCPPacketEgress(t *testing.T, ctx context.Context, clientMux *mux.Mux) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var dst netip.Addr
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			dst, _ = netip.AddrFromSlice(ip.To4())
			break
		}
	}
	if !dst.IsValid() {
		t.Skip("no non-loopback IPv4 address")
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	d := &packetDevice{in: make(chan []byte, 16), out: make(chan []byte, 16), done: make(chan struct{})}
	defer d.Close()
	result := make(chan error, 1)
	go func() {
		result <- ServeEgress(ctx, d, netip.MustParsePrefix("172.16.253.1/24"), func(netip.Addr) (*mux.Mux, bool) { return clientMux, true })
	}()
	src := netip.MustParseAddr("172.16.253.1")
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	d.in <- tcpTestPacket(src, dst, 51000, port, 1000, 0, 0x02, nil)
	for {
		select {
		case packet := <-d.out:
			if len(packet) < 40 || packet[9] != 6 {
				continue
			}
			if packet[33]&0x12 != 0x12 {
				t.Fatalf("expected SYN-ACK; got TCP flags %#x", packet[33])
			}
			headerLen := int(packet[0]&0x0f) * 4
			serverSeq := binary.BigEndian.Uint32(packet[headerLen+4 : headerLen+8])
			d.in <- tcpTestPacket(src, dst, 51000, port, 1001, serverSeq+1, 0x10, nil)
			want := []byte("packet-to-socket-echo")
			d.in <- tcpTestPacket(src, dst, 51000, port, 1001, serverSeq+1, 0x18, want)
			for {
				select {
				case reply := <-d.out:
					if len(reply) < 40 || reply[9] != 6 {
						continue
					}
					ipLen := int(reply[0]&0x0f) * 4
					tcpLen := int(reply[ipLen+12]>>4) * 4
					if len(reply) >= ipLen+tcpLen+len(want) && bytes.Equal(reply[ipLen+tcpLen:], want) {
						return
					}
				case <-ctx.Done():
					t.Fatal("no TCP echo data from packet stack")
				}
			}
		case err := <-result:
			t.Fatalf("netstack exited: %v", err)
		case <-ctx.Done():
			t.Fatal("no TCP SYN-ACK from packet stack")
		}
	}
}

func TestTCPPacketEgressHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	a, b := memoryPair()
	clientMux, serverMux := mux.New(ctx, a, false), mux.New(ctx, b, true)
	defer clientMux.Close()
	defer serverMux.Close()
	go pivot.ServeVPN(ctx, serverMux, func(netip.Addr) (*mux.Mux, bool) { return nil, false }, false)
	runTCPPacketEgress(t, ctx, clientMux)
}

func TestTCPPacketEgressOverDirectDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{39}, 32)
	srv, err := dns.Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	serverMuxCh := make(chan *mux.Mux, 1)
	go func() {
		select {
		case p := <-srv.Accepted():
			serverMuxCh <- mux.New(ctx, p.Session, true)
		case <-ctx.Done():
		}
	}()
	client, err := dns.Dial(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	clientMux := mux.New(ctx, client, false)
	defer clientMux.Close()
	var serverMux *mux.Mux
	select {
	case serverMux = <-serverMuxCh:
	case <-ctx.Done():
		t.Fatal("server mux not ready")
	}
	defer serverMux.Close()
	go pivot.ServeVPN(ctx, serverMux, func(netip.Addr) (*mux.Mux, bool) { return nil, false }, false)
	runTCPPacketEgress(t, ctx, clientMux)
}
