package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"undertow/internal/mux"
)

// SleepPolicy is the idle callback interval. Zero preserves persistent sessions.
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

type SleepMessage struct {
	Kind   string       `json:"sleep_control"`
	Policy *SleepPolicy `json:"policy,omitempty"`
}

func EncodeSleepMessage(kind string, policy *SleepPolicy) []byte {
	b, _ := json.Marshal(SleepMessage{Kind: kind, Policy: policy})
	return b
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
	state := m.agents[id]
	allowed := state != nil && state.mux == stream && state.inventoryReady && state.inventory.SleepSupported && state.inventory.Sleep.IntervalSeconds > 0
	if allowed {
		for _, route := range m.routes.List() {
			if route.AgentID == id && route.Active {
				allowed = false
				break
			}
		}
	}
	if allowed {
		for _, client := range m.clients {
			for _, route := range client.accepted {
				if route.AgentID == id {
					allowed = false
				}
			}
		}
	}
	if allowed {
		for childID, child := range m.agents {
			if childID != id && child.inventory.Via == id {
				allowed = false
				break
			}
		}
	}
	if allowed && (len(m.relays[id]) != 0 || len(m.restoringRelays[id]) != 0) {
		allowed = false
	}
	if allowed {
		for _, forward := range m.forwards {
			if forward.AgentID == id {
				allowed = false
				break
			}
		}
	}
	if allowed {
		for _, job := range m.jobs {
			if job.info.AgentID == id && job.info.State == "running" {
				allowed = false
				break
			}
		}
	}
	return allowed && stream.StreamCount() == 0
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
