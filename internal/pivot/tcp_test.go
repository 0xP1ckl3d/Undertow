package pivot

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/security"
	"undertow/internal/transport/dns"
)

func TestTCPForwardOverDirectDNS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	remote, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	go func() {
		for {
			c, err := remote.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
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
	token := bytes.Repeat([]byte{44}, 32)
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
	forward, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer forward.Close()
	go ServeForward(ctx, forward, remote.Addr().String(), func() *mux.Mux { return serverMux })
	var wg sync.WaitGroup
	fail := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", forward.Addr().String(), 5*time.Second)
			if err != nil {
				fail <- err
				return
			}
			defer conn.Close()
			want := bytes.Repeat([]byte{byte(i + 1)}, 8192+i)
			if _, err = conn.Write(want); err != nil {
				fail <- err
				return
			}
			got := make([]byte, len(want))
			if _, err = io.ReadFull(conn, got); err != nil {
				fail <- err
				return
			}
			if !bytes.Equal(got, want) {
				fail <- io.ErrUnexpectedEOF
			}
		}(i)
	}
	wg.Wait()
	close(fail)
	for err := range fail {
		t.Error(err)
	}
}
