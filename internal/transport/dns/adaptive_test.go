package dns

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"undertow/internal/security"
	"undertow/internal/session"
)

func TestPayloadDiscoveryThroughLimitedPath(t *testing.T) {
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv, err := Listen("127.0.0.1:0", "t.undertow.invalid", identity, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	proxy, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	upstream, err := net.Dial("udp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	name, err := RandomName("t.undertow.invalid.")
	if err != nil {
		t.Fatal(err)
	}
	maxWire, err := Encode(Message{Name: name, Payload: make([]byte, 84+512)})
	if err != nil {
		t.Fatal(err)
	}
	limit := len(maxWire)
	go func() {
		var request, response [maxDNS]byte
		for {
			n, client, readErr := proxy.ReadFromUDP(request[:])
			if readErr != nil {
				return
			}
			if n > limit {
				continue
			}
			_ = upstream.SetDeadline(time.Now().Add(time.Second))
			if _, err := upstream.Write(request[:n]); err != nil {
				continue
			}
			m, err := upstream.Read(response[:])
			if err == nil && m <= limit {
				_, _ = proxy.WriteToUDP(response[:m], client)
			}
		}
	}()
	size, err := DiscoverPayloadSize(ctx, proxy.LocalAddr().String(), "t.undertow.invalid")
	if err != nil || size != 512 {
		t.Fatalf("discovered fragment size=%d err=%v", size, err)
	}
}

func TestPollTarget(t *testing.T) {
	cases := []struct {
		name   string
		stats  session.Stats
		health int
		want   int
	}{
		{"idle", session.Stats{CongestionWindow: 16, PeerReceiveWindow: 64}, 16, 1},
		{"first queued packet", session.Stats{Queued: 1, CongestionWindow: 16, PeerReceiveWindow: 64}, 16, 2},
		{"one packet in flight", session.Stats{InFlight: 1, CongestionWindow: 16, PeerReceiveWindow: 64}, 16, 2},
		{"initial backlog", session.Stats{Queued: 100, CongestionWindow: 16, PeerReceiveWindow: 64}, 16, 16},
		{"healthy backlog", session.Stats{Queued: 100, CongestionWindow: 64, PeerReceiveWindow: 64, RTT: 100 * time.Millisecond}, 64, 64},
		{"loss backoff", session.Stats{Queued: 100, CongestionWindow: 32, PeerReceiveWindow: 64}, 4, 4},
		{"receive window", session.Stats{Queued: 100, InFlight: 2, CongestionWindow: 64, PeerReceiveWindow: 3}, 64, 5},
		{"low RTT", session.Stats{Queued: 100, CongestionWindow: 64, PeerReceiveWindow: 64, RTT: 5 * time.Millisecond}, 64, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pollTarget(tc.stats, tc.health); got != tc.want {
				t.Fatalf("target=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestQueryErrorBackoffCoalescesBurst(t *testing.T) {
	now := time.Now()
	if !shouldBackOffQueryError(now, time.Time{}, 20*time.Millisecond) {
		t.Fatal("first query error did not back off")
	}
	if shouldBackOffQueryError(now.Add(499*time.Millisecond), now, 20*time.Millisecond) {
		t.Fatal("query errors in the same burst backed off twice")
	}
	if !shouldBackOffQueryError(now.Add(500*time.Millisecond), now, 20*time.Millisecond) {
		t.Fatal("query error after low-RTT interval did not back off")
	}
	if shouldBackOffQueryError(now.Add(799*time.Millisecond), now, 400*time.Millisecond) {
		t.Fatal("high-RTT path backed off too soon")
	}
	if !shouldBackOffQueryError(now.Add(800*time.Millisecond), now, 400*time.Millisecond) {
		t.Fatal("query error after high-RTT interval did not back off")
	}
}

func TestQueuedDataBypassesIdlePoll(t *testing.T) {
	_, identity, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token := bytes.Repeat([]byte{17}, 32)
	srv, err := Listen("127.0.0.1:0", "t.undertow.invalid", identity, token)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ctx) }()
	client, err := DialProfile(ctx, srv.Addr().String(), "t.undertow.invalid", security.Fingerprint(identity), token, agentKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var peer *Peer
	select {
	case peer = <-srv.Accepted():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	deadline := time.Now().Add(time.Second)
	for client.AdaptiveStats().Outstanding != 1 {
		if time.Now().After(deadline) {
			t.Fatal("idle DNS poll did not start")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if err := client.Send(ctx, []byte("first interactive packet")); err != nil {
		t.Fatal(err)
	}
	packet, err := peer.Session.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet, []byte("first interactive packet")) {
		t.Fatalf("unexpected packet %q", packet)
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("first packet waited behind idle poll: %v", elapsed)
	}
}
