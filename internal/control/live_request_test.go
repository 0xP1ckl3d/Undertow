package control

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"undertow/internal/pivot"
	"undertow/internal/routing"
)

func TestExplicitStreamWaitsForCheckInAndHoldsAgentAwake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	m.mu.Lock()
	m.offlineAgents["sleeping-stream"] = AgentInfo{ID: "sleeping-stream", ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute), SleepSupported: true, Sleep: SleepPolicy{IntervalSeconds: 15}}
	m.mu.Unlock()
	result := make(chan error, 1)
	release := make(chan struct{})
	requesterDone := make(chan struct{})
	go func() {
		stream, err := m.openAgentForOperator(ctx, requesterDone, "sleeping-stream", pivot.HostOpsDestination)
		result <- err
		<-release
		if stream != nil {
			_ = stream.Close()
		}
	}()
	defer close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		pending := m.pendingLive["sleeping-stream"]
		m.mu.RUnlock()
		if pending == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	m.mu.RLock()
	pending := m.pendingLive["sleeping-stream"]
	m.mu.RUnlock()
	if pending != 1 {
		t.Fatal("operator stream was not retained while sleeping")
	}
	server, agent := forwardAuditAgent(t, ctx, m, "sleeping-stream", 836, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	m.UpdateInventory("sleeping-stream", server, []byte(`{"sleep_supported":true,"sleep":{"interval_seconds":15},"capabilities":{"allowed":["hostops"],"supported":["hostops"]}}`))
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("operator stream did not start on callback")
	}
	m.mu.Lock()
	reason := m.sleepBlockReasonLocked("sleeping-stream", server)
	m.mu.Unlock()
	if !strings.Contains(reason, "operation stream") {
		t.Fatalf("agent could sleep during active stream: %q", reason)
	}
}

func TestExplicitStreamRequestCanBeCancelledBeforeCheckIn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	m.mu.Lock()
	m.offlineAgents["sleeping-stream"] = AgentInfo{ID: "sleeping-stream", ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute)}
	m.mu.Unlock()
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := m.openAgentForOperator(ctx, done, "sleeping-stream", pivot.FileDestination)
		result <- err
	}()
	close(done)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled request opened a stream")
		}
	case <-ctx.Done():
		t.Fatal("cancelled request remained queued")
	}
	m.mu.RLock()
	pending := m.pendingLive["sleeping-stream"]
	m.mu.RUnlock()
	if pending != 0 {
		t.Fatalf("cancelled request remained pending: %d", pending)
	}
}
