package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"undertow/internal/pivot"
	"undertow/internal/routing"
)

func TestForegroundCommandsWaitForCheckInWithoutJobs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	m.mu.Lock()
	m.offlineAgents["checkin-console"] = AgentInfo{ID: "checkin-console", ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute), SleepSupported: true, Sleep: SleepPolicy{IntervalSeconds: 15}}
	m.mu.Unlock()
	type response struct {
		name   string
		code   int
		result pivot.ExecResult
	}
	results := make(chan response, 2)
	submit := func(name string) {
		body, _ := json.Marshal(pivot.ExecRequest{Builtin: name})
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/checkin-console/exec", bytes.NewReader(body)).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer test-token")
		recorder := httptest.NewRecorder()
		m.handler("test-token").ServeHTTP(recorder, request)
		var result pivot.ExecResult
		_ = json.Unmarshal(recorder.Body.Bytes(), &result)
		results <- response{name: name, code: recorder.Code, result: result}
	}
	go submit("pwd")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		pending := m.pendingLive["checkin-console"]
		m.mu.RUnlock()
		if pending > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	go submit("whoami")
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		pending := m.pendingLive["checkin-console"]
		m.mu.RUnlock()
		if pending >= 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case result := <-results:
		t.Fatalf("foreground request completed before check-in: %+v", result)
	default:
	}
	server, agent := forwardAuditAgent(t, ctx, m, "checkin-console", 837, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	for _, want := range []string{"pwd", "whoami"} {
		select {
		case result := <-results:
			if result.name != want || result.code != http.StatusOK || result.result.Error != "" || result.result.Stdout == "" || result.result.QueuedJobID != "" {
				t.Fatalf("foreground %s: %+v", want, result)
			}
		case <-ctx.Done():
			t.Fatalf("foreground %s did not complete", want)
		}
	}
	m.mu.RLock()
	jobs := len(m.jobs)
	pending := m.pendingLive["checkin-console"]
	m.mu.RUnlock()
	if jobs != 0 || pending != 0 {
		t.Fatalf("foreground commands left jobs=%d pending=%d", jobs, pending)
	}
}

func TestCancelledForegroundTurnDoesNotOvertakeEarlierCommand(t *testing.T) {
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	first, err := m.foregroundTurn(context.Background(), "ordered")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { _, err := m.foregroundTurn(ctx, "ordered"); secondDone <- err }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		pending := m.pendingLive["ordered"]
		m.mu.RUnlock()
		if pending == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	thirdDone := make(chan error, 1)
	go func() {
		release, err := m.foregroundTurn(context.Background(), "ordered")
		if err == nil {
			release()
		}
		thirdDone <- err
	}()
	cancel()
	if err := <-secondDone; err == nil {
		t.Fatal("cancelled turn completed")
	}
	select {
	case <-thirdDone:
		t.Fatal("third command overtook the first")
	default:
	}
	first()
	select {
	case err := <-thirdDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("third command did not proceed")
	}
}

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
