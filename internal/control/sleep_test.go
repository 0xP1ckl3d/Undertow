package control

import (
	"context"
	"encoding/json"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

func TestSleepPolicyAndOverrideSurviveReconnect(t *testing.T) {
	for _, policy := range []SleepPolicy{{IntervalSeconds: -1}, {IntervalSeconds: 86401}, {JitterPercent: -1}, {JitterPercent: 51}} {
		if policy.Validate() == nil {
			t.Fatalf("accepted invalid policy %+v", policy)
		}
	}
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownAgentSessions()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer agent.Close()
	var keys security.Keys
	sess, err := session.New(1001, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	m.Register(&dns.Peer{Session: sess, AgentID: "agent-a", Connected: time.Now()}, server)
	m.UpdateInventory("agent-a", server, []byte(`{"hostname":"older-agent"}`))
	if err := m.SetAgentSleep("agent-a", SleepPolicy{IntervalSeconds: 7}); err == nil {
		t.Fatal("older agent accepted a live sleep update")
	}
	m.UpdateInventory("agent-a", server, []byte(`{"hostname":"test","sleep_supported":true,"sleep":{"interval_seconds":4,"jitter_percent":10}}`))
	if got := m.AgentList()[0].Sleep.IntervalSeconds; got != 4 {
		t.Fatalf("embedded policy missing: %d", got)
	}
	policy := SleepPolicy{IntervalSeconds: 7, JitterPercent: 20}
	if err := m.SetAgentSleep("agent-a", policy); err != nil {
		t.Fatal(err)
	}
	message, err := agent.RecvControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var update SleepMessage
	if json.Unmarshal(message, &update) != nil || update.Kind != "policy" || update.Policy == nil || *update.Policy != policy {
		t.Fatalf("policy update: %s", message)
	}
	reloaded, err := store.LoadAgentSleepOverrides()
	if err != nil || reloaded["agent-a"] != policy {
		t.Fatalf("saved override: %+v %v", reloaded, err)
	}
	m.UpdateInventory("agent-a", server, []byte(`{"hostname":"test","sleep_supported":true,"sleep":{"interval_seconds":4,"jitter_percent":10}}`))
	if got := m.AgentList()[0].Sleep; got != policy {
		t.Fatalf("override lost on reconnect inventory: %+v", got)
	}
}

func TestSleepTimingAndMissedCheckIns(t *testing.T) {
	for _, test := range []struct {
		seconds int
		want    time.Duration
	}{{0, 0}, {1, time.Second}, {60, 6 * time.Second}, {3600, 15 * time.Second}} {
		if got := SleepIdleGrace(SleepPolicy{IntervalSeconds: test.seconds}); got != test.want {
			t.Fatalf("idle grace for %ds = %s, want %s", test.seconds, got, test.want)
		}
	}
	policy := SleepPolicy{IntervalSeconds: 3600, JitterPercent: 20}
	if !validSleepDelay(policy, (48*time.Minute).Milliseconds()) || !validSleepDelay(policy, (72*time.Minute).Milliseconds()) {
		t.Fatal("valid jittered callback delay rejected")
	}
	if validSleepDelay(policy, (47*time.Minute).Milliseconds()) || validSleepDelay(policy, (73*time.Minute).Milliseconds()) {
		t.Fatal("out-of-range callback delay accepted")
	}
	expected := time.Now().UTC()
	if got, want := sleepLostAfter(expected, policy), expected.Add(2*72*time.Minute+30*time.Second); !got.Equal(want) {
		t.Fatalf("lost threshold = %s, want %s", got, want)
	}
}

func TestSleepingSnapshotSurvivesRestartAndExpires(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	deadline := time.Now().Add(350 * time.Millisecond).UTC()
	snapshot := AgentInfo{ID: "sleeping-agent", Hostname: "test", SleepSupported: true, SleepProtocolVersion: 2, Sleep: SleepPolicy{IntervalSeconds: 10}, ConnectionMode: "checkin", ConnectionState: "sleeping", ExpectedCheckIn: deadline.Add(-200 * time.Millisecond), SleepLostAfter: deadline}
	if err := store.SaveAgentSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	defer m.ShutdownAgentSessions()
	if got := m.AgentCatalog(); len(got) != 1 || got[0].ConnectionState != "sleeping" {
		t.Fatalf("sleep was lost during restart: %+v", got)
	}
	limit := time.Now().Add(2 * time.Second)
	for time.Now().Before(limit) {
		if got := m.AgentCatalog(); len(got) == 1 && got[0].ConnectionState == "disconnected" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := m.AgentCatalog(); len(got) != 1 || got[0].ConnectionState != "disconnected" || got[0].ConnectionReason != "Three expected check-ins missed" {
		t.Fatalf("missed callback did not become disconnected: %+v", got)
	}
	for time.Now().Before(limit) {
		stored, err := store.LoadAgentSnapshots()
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) == 1 && stored[0].ConnectionState == "disconnected" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.ShutdownAgentSessions()
	reloaded := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := reloaded.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AgentCatalog(); len(got) != 1 || got[0].ConnectionState != "disconnected" {
		t.Fatalf("expired state was not durable: %+v", got)
	}
}

func TestSleepGrantRequiresNoLiveServerWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer agent.Close()
	var keys security.Keys
	sess, err := session.New(1002, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	m.Register(&dns.Peer{Session: sess, AgentID: "agent-a", Connected: time.Now()}, server)
	m.UpdateInventory("agent-a", server, []byte(`{"sleep_supported":true,"sleep_protocol_version":2,"sleep":{"interval_seconds":2}}`))
	prefix := netip.MustParsePrefix("10.55.0.0/16")
	if err := m.routes.Add(prefix, "agent-a"); err != nil {
		t.Fatal(err)
	}
	check := func(want bool, condition string) {
		t.Helper()
		m.mu.Lock()
		got := m.canSleepLocked("agent-a", server)
		m.mu.Unlock()
		if got != want {
			t.Fatalf("sleep eligibility with %s = %t, want %t", condition, got, want)
		}
	}
	check(true, "no live work")
	m.routes.SetRouteActive(prefix, true)
	check(false, "active server route")
	m.routes.SetRouteActive(prefix, false)
	m.mu.Lock()
	m.clients[42] = &clientState{accepted: map[netip.Prefix]AcceptedRoute{prefix: {AgentID: "agent-a"}}}
	m.mu.Unlock()
	check(false, "client-accepted route")
	m.mu.Lock()
	delete(m.clients, 42)
	m.relays["agent-a"] = map[string]*relayState{"127.0.0.1:8000": {}}
	m.mu.Unlock()
	check(false, "active relay listener")
	m.mu.Lock()
	delete(m.relays, "agent-a")
	m.restoringRelays["agent-a"] = map[string]bool{"127.0.0.1:8000": true}
	m.mu.Unlock()
	check(false, "restoring relay listener")
	m.mu.Lock()
	delete(m.restoringRelays, "agent-a")
	m.forwards["forward"] = &forwardState{ForwardInfo: ForwardInfo{AgentID: "agent-a"}}
	m.mu.Unlock()
	check(false, "active reverse forward")
	m.mu.Lock()
	delete(m.forwards, "forward")
	m.agents["child"] = &agentState{inventory: AgentInfo{Via: "agent-a"}}
	m.mu.Unlock()
	check(false, "connected relay child")
	m.mu.Lock()
	delete(m.agents, "child")
	m.mu.Unlock()
	check(true, "live dependencies cleared")
	m.mu.Lock()
	m.jobs["running"] = &jobState{info: JobInfo{AgentID: "agent-a", State: "running"}}
	m.mu.Unlock()
	m.handleSleepRequest("agent-a", server)
	data, err := agent.RecvControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message SleepMessage
	if json.Unmarshal(data, &message) != nil || message.Kind != "denied" {
		t.Fatalf("active job allowed sleep: %s", data)
	}
	m.mu.Lock()
	delete(m.jobs, "running")
	m.mu.Unlock()
	m.handleSleepRequest("agent-a", server)
	data, err = agent.RecvControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(data, &message) != nil || message.Kind != "granted" {
		t.Fatalf("idle sleep denied: %s", data)
	}
	if server.TryQuiesce() {
		t.Fatal("server accepted work after granting sleep")
	}
	m.mu.Lock()
	m.jobs["new-work"] = &jobState{info: JobInfo{AgentID: "agent-a", State: "running"}}
	m.mu.Unlock()
	m.commitSleep("agent-a", server)
	select {
	case <-server.Done():
		t.Fatal("sleep commit closed session after new work started")
	default:
	}
	if server.IsQuiesced() {
		t.Fatal("denied commit left server quiesced")
	}
	m.mu.Lock()
	delete(m.jobs, "new-work")
	m.mu.Unlock()
	// Drain the denial and policy refresh from the cancelled grant.
	for i := 0; i < 2; i++ {
		if _, err := agent.RecvControl(ctx); err != nil {
			t.Fatal(err)
		}
	}
	m.handleSleepRequest("agent-a", server)
	if data, err := agent.RecvControl(ctx); err != nil || json.Unmarshal(data, &message) != nil || message.Kind != "granted" {
		t.Fatalf("second idle grant: %s %v", data, err)
	}
	m.commitSleep("agent-a", server)
	if data, err := agent.RecvControl(ctx); err != nil || json.Unmarshal(data, &message) != nil || message.Kind != "committed" {
		t.Fatalf("commit acknowledgment: %s %v", data, err)
	}
	if err := agent.SendControl(ctx, EncodeSleepNotice(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if data, err := agent.RecvControl(ctx); err != nil || json.Unmarshal(data, &message) != nil || message.Kind != "final" {
		t.Fatalf("sleep final: %s %v", data, err)
	}
	deadline := time.Now().Add(time.Second)
	for len(m.AgentList()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.AgentList()) != 0 {
		t.Fatal("sleeping agent stayed online in server inventory")
	}
	records := m.AgentCatalog()
	if len(records) != 1 || records[0].ConnectionState != "sleeping" || records[0].ConnectionMode != "checkin" || records[0].ExpectedCheckIn.IsZero() || records[0].SleepLostAfter.IsZero() {
		t.Fatalf("intentional sleep status missing: %+v", records)
	}
	if records[0].SleepLostAfter.Sub(records[0].ExpectedCheckIn) != 4*time.Second+30*time.Second {
		t.Fatalf("missed check-in grace: %s", records[0].SleepLostAfter.Sub(records[0].ExpectedCheckIn))
	}
}
