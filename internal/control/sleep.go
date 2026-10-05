package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"undertow/internal/mux"
)

// SleepPolicy controls the callback interval after a short idle grace. Zero
// preserves a continuous session.
type SleepPolicy struct {
	IntervalSeconds int `json:"interval_seconds"`
	JitterPercent   int `json:"jitter_percent"`
}

func (p SleepPolicy) Validate() error {
	if p.IntervalSeconds < 0 || p.IntervalSeconds > 86400 {
		return errors.New("sleep interval must be between 0 and 86400 seconds")
	}
	if p.JitterPercent < 0 || p.JitterPercent > 50 {
		return errors.New("sleep jitter must be between 0 and 50 percent")
	}
	return nil
}

// SleepIdleGrace gives a check-in agent a short work window after connection
// or its last active stream, independently of its callback interval.
func SleepIdleGrace(policy SleepPolicy) time.Duration {
	if policy.IntervalSeconds <= 0 {
		return 0
	}
	grace := time.Duration(policy.IntervalSeconds) * time.Second / 10
	if grace < time.Second {
		return time.Second
	}
	if grace > 15*time.Second {
		return 15 * time.Second
	}
	return grace
}

type SleepMessage struct {
	Kind            string       `json:"sleep_control"`
	Policy          *SleepPolicy `json:"policy,omitempty"`
	WakeAfterMillis int64        `json:"wake_after_ms,omitempty"`
}

func EncodeSleepMessage(kind string, policy *SleepPolicy) []byte {
	b, _ := json.Marshal(SleepMessage{Kind: kind, Policy: policy})
	return b
}

func EncodeSleepNotice(delay time.Duration) []byte {
	b, _ := json.Marshal(SleepMessage{Kind: "sleeping", WakeAfterMillis: delay.Milliseconds()})
	return b
}

// Three missed callback opportunities, including the first expected callback,
// are required before an intentional sleep becomes a lost connection. The
// jitter upper bound and a small transport grace avoid an early false alarm.
func sleepLostAfter(expected time.Time, policy SleepPolicy) time.Time {
	maximum := time.Duration(policy.IntervalSeconds) * time.Second * time.Duration(100+policy.JitterPercent) / 100
	return expected.Add(2*maximum + 30*time.Second)
}

func validSleepDelay(policy SleepPolicy, millis int64) bool {
	if policy.IntervalSeconds <= 0 || millis <= 0 {
		return false
	}
	base := time.Duration(policy.IntervalSeconds) * time.Second
	minimum := base * time.Duration(100-policy.JitterPercent) / 100
	maximum := base * time.Duration(100+policy.JitterPercent) / 100
	if millis > (maximum + time.Millisecond).Milliseconds() {
		return false
	}
	delay := time.Duration(millis) * time.Millisecond
	return delay >= minimum-time.Millisecond && delay <= maximum+time.Millisecond
}

func (m *Manager) recordSleepNotice(id string, stream *mux.Mux, message SleepMessage) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.agents[id]
	if state == nil || state.mux != stream || !stream.IsSleepCommitted() || !state.inventory.SleepSupported {
		return false
	}
	policy := state.inventory.Sleep
	if policy.IntervalSeconds <= 0 {
		return false
	}
	delay := time.Duration(policy.IntervalSeconds) * time.Second
	if state.inventory.SleepProtocolVersion >= 2 {
		if !validSleepDelay(policy, message.WakeAfterMillis) {
			return false
		}
		delay = time.Duration(message.WakeAfterMillis) * time.Millisecond
	}
	started := time.Now().UTC()
	state.inventory.ConnectionMode = "checkin"
	state.inventory.ConnectionState = "sleeping"
	state.inventory.ConnectionReason = "Intentional sleep confirmed by the server"
	state.inventory.SleepStartedAt = started
	state.inventory.ExpectedCheckIn = started.Add(delay)
	state.inventory.SleepLostAfter = sleepLostAfter(state.inventory.ExpectedCheckIn, policy)
	return true
}

func (m *Manager) scheduleSleepExpiry(id string, deadline time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shuttingDown || !deadline.After(time.Now()) {
		return
	}
	if timer := m.sleepTimers[id]; timer != nil {
		timer.Stop()
	}
	m.sleepTimers[id] = time.AfterFunc(time.Until(deadline), func() { m.expireSleep(id, deadline) })
}

func (m *Manager) expireSleep(id string, deadline time.Time) {
	m.mu.Lock()
	if m.shuttingDown || m.agents[id] != nil {
		m.mu.Unlock()
		return
	}
	info, ok := m.offlineAgents[id]
	if !ok || info.ConnectionState != "sleeping" || !info.SleepLostAfter.Equal(deadline) {
		m.mu.Unlock()
		return
	}
	info.ConnectionState = "disconnected"
	info.ConnectionReason = "Three expected check-ins missed"
	m.offlineAgents[id] = info
	delete(m.sleepTimers, id)
	m.recordLifecycleLocked(LifecycleEvent{AgentID: id, Kind: "sleep_missed", Transport: info.Transport, SessionID: info.SessionID})
	store := m.operations
	m.mu.Unlock()
	if store != nil {
		_ = store.SaveAgentSnapshot(info)
	}
	m.PublishEvent("agent.updated", id)
}

// SetAgentSleep updates one connected agent and retains its override for later callbacks.
func (m *Manager) SetAgentSleep(id string, policy SleepPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	state := m.agents[id]
	if state == nil || !state.inventoryReady {
		m.mu.Unlock()
		return errors.New("agent is not connected or inventory is pending")
	}
	if !state.inventory.SleepSupported {
		m.mu.Unlock()
		return errors.New("agent does not support idle sleep; rebuild its payload")
	}
	if m.operations == nil {
		m.mu.Unlock()
		return errors.New("operations store unavailable")
	}
	if err := m.operations.SetAgentSleepOverride(id, policy); err != nil {
		m.mu.Unlock()
		return err
	}
	m.sleepOverrides[id] = policy
	state.inventory.Sleep = policy
	stream := state.mux
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := stream.SendControl(ctx, EncodeSleepMessage("policy", &policy)); err != nil {
		return err
	}
	m.PublishEvent("agent.updated", id)
	return nil
}

func (m *Manager) handleSleepRequest(id string, stream *mux.Mux) {
	m.mu.Lock()
	allowed := m.canSleepLocked(id, stream)
	if allowed {
		allowed = stream.TryQuiesce()
	}
	m.mu.Unlock()
	kind := "denied"
	if allowed {
		kind = "granted"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := stream.SendControl(ctx, EncodeSleepMessage(kind, nil))
	cancel()
	if err != nil && allowed {
		stream.Unquiesce()
	} else if allowed {
		time.AfterFunc(10*time.Second, stream.Unquiesce)
	}
}

func (m *Manager) canSleepLocked(id string, stream *mux.Mux) bool {
	return m.sleepBlockReasonLocked(id, stream) == ""
}

// sleepBlockReasonLocked explains why a configured check-in agent remains
// connected. It is also the eligibility check for the sleep handshake.
func (m *Manager) sleepBlockReasonLocked(id string, stream *mux.Mux) string {
	state := m.agents[id]
	if state == nil || state.mux != stream || !state.inventoryReady {
		return "inventory is pending"
	}
	if !state.inventory.SleepSupported {
		return "this agent build does not support sleep"
	}
	if state.inventory.Sleep.IntervalSeconds == 0 {
		return "continuous connection is configured"
	}
	for _, route := range m.routes.List() {
		if route.AgentID == id && route.Active {
			return "an active server route depends on this agent"
		}
	}
	for _, client := range m.clients {
		for _, route := range client.accepted {
			if route.AgentID == id {
				return "a client-accepted route depends on this agent"
			}
		}
	}
	for childID, child := range m.agents {
		if childID != id && child.inventory.Via == id {
			return "a connected child agent depends on this relay path"
		}
	}
	if len(m.relays[id]) != 0 || len(m.restoringRelays[id]) != 0 {
		return "a relay listener is active"
	}
	for _, forward := range m.forwards {
		if forward.AgentID == id {
			return "an active forward depends on this agent"
		}
	}
	for _, job := range m.jobs {
		if job.info.AgentID == id && job.info.State == "running" {
			return "a background job is running"
		}
	}
	if stream.StreamCount() != 0 {
		return "an operation stream is active"
	}
	return ""
}

// A condition may have become live after the grant. Recheck before closing.
func (m *Manager) commitSleep(id string, stream *mux.Mux) {
	m.mu.Lock()
	allowed := m.canSleepLocked(id, stream) && stream.CommitQuiesce()
	m.mu.Unlock()
	if allowed {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := stream.SendControl(ctx, EncodeSleepMessage("committed", nil))
		cancel()
		if err != nil {
			stream.Close()
			return
		}
		// DNS needs another client poll to receive the acknowledgment. The
		// timeout bounds a peer that never finishes the handshake.
		time.AfterFunc(10*time.Second, func() { stream.Close() })
		return
	}
	stream.Unquiesce()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = stream.SendControl(ctx, EncodeSleepMessage("denied", nil))
	m.mu.RLock()
	policy := SleepPolicy{}
	if state := m.agents[id]; state != nil && state.mux == stream {
		policy = state.inventory.Sleep
	}
	m.mu.RUnlock()
	_ = stream.SendControl(ctx, EncodeSleepMessage("policy", &policy))
}
