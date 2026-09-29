package pivot

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/security"
	"undertow/internal/transport/dns"
)

func TestUDPAgentOverDirectDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	remote, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, e := remote.ReadFrom(buf)
			if e != nil {
				return
			}
			_, _ = remote.WriteTo(buf[:n], addr)
		}
	}()
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{55}, 32)
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
	client, err := dns.Dial(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	agentMux := mux.New(ctx, client, false)
	defer agentMux.Close()
	go ServeAgent(ctx, agentMux)
	var serverMux *mux.Mux
	select {
	case serverMux = <-serverMuxCh:
	case <-ctx.Done():
		t.Fatal("server mux not ready")
	}
	defer serverMux.Close()
	stream, err := serverMux.Open(ctx, "udp://"+remote.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for _, size := range []int{64, 1200, 517} {
		want := bytes.Repeat([]byte{byte(size)}, size)
		frame := make([]byte, 2+size)
		binary.BigEndian.PutUint16(frame[:2], uint16(size))
		copy(frame[2:], want)
		if _, err = stream.Write(frame); err != nil {
			t.Fatal(err)
		}
		var header [2]byte
		if _, err = io.ReadFull(stream, header[:]); err != nil {
			t.Fatal(err)
		}
		if int(binary.BigEndian.Uint16(header[:])) != size {
			t.Fatal("UDP length mismatch")
		}
		got := make([]byte, size)
		if _, err = io.ReadFull(stream, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("UDP datagram mismatch")
		}
	}
}
