package control

import (
	"context"
	"errors"
	"time"

	"undertow/internal/mux"
)

// foregroundTurn serializes submitted foreground actions for one agent. A
// cancelled waiter releases its place so later console commands can proceed.
func (m *Manager) foregroundTurn(ctx context.Context, agentID string) (func(), error) {
	m.mu.Lock()
	if m.foregroundTails == nil {
		m.foregroundTails = make(map[string]chan struct{})
	}
	previous := m.foregroundTails[agentID]
	next := make(chan struct{})
	m.foregroundTails[agentID] = next
	if m.pendingLive == nil {
		m.pendingLive = make(map[string]int)
	}
	m.pendingLive[agentID]++
	m.mu.Unlock()
	release := func() {
		m.mu.Lock()
		if m.foregroundTails[agentID] == next {
			delete(m.foregroundTails, agentID)
		}
		m.pendingLive[agentID]--
		if m.pendingLive[agentID] == 0 {
			delete(m.pendingLive, agentID)
		}
		close(next)
		m.mu.Unlock()
	}
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			go func() { <-previous; release() }()
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// openAgentForOperator keeps an explicitly requested live operation pending
// across an intentional sleep. The pending request prevents the next check-in
// from immediately sleeping again. Closing the requesting stream cancels it.
func (m *Manager) openAgentForOperator(ctx context.Context, requesterDone <-chan struct{}, agentID, destination string) (*mux.Stream, error) {
	m.mu.Lock()
	state := m.agents[agentID]
	retained := m.offlineAgents[agentID]
	sleeping := state == nil && retained.ConnectionState == "sleeping" && time.Now().Before(retained.SleepLostAfter)
	if state == nil && !sleeping {
		m.mu.Unlock()
		return nil, errors.New("agent has missed its check-ins or is disconnected")
	}
	if m.pendingLive == nil {
		m.pendingLive = make(map[string]int)
	}
	m.pendingLive[agentID]++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.pendingLive[agentID]--
		if m.pendingLive[agentID] == 0 {
			delete(m.pendingLive, agentID)
		}
		m.mu.Unlock()
	}()
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopWatcher := make(chan struct{})
	go func() {
		select {
		case <-requesterDone:
			cancel()
		case <-stopWatcher:
		}
	}()
	defer close(stopWatcher)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := waitCtx.Err(); err != nil {
			return nil, err
		}
		m.mu.RLock()
		state = m.agents[agentID]
		retained = m.offlineAgents[agentID]
		m.mu.RUnlock()
		if state == nil && (retained.ConnectionState != "sleeping" || !time.Now().Before(retained.SleepLostAfter)) {
			return nil, errors.New("agent missed its expected check-ins")
		}
		if state != nil {
			state.mux.Unquiesce()
			if !state.mux.IsSleepCommitted() {
				openCtx, stop := context.WithTimeout(waitCtx, 15*time.Second)
				stream, err := state.mux.Open(openCtx, destination)
				stop()
				if err == nil {
					return stream, nil
				}
				if waitCtx.Err() != nil {
					return nil, waitCtx.Err()
				}
				// Retry only if this check-in ended or committed to sleep while
				// the request was opening. A healthy session's error is real.
				if !state.mux.IsSleepCommitted() {
					select {
					case <-state.mux.Done():
					default:
						return nil, err
					}
				}
			}
		}
		select {
		case <-waitCtx.Done():
			return nil, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

// holdAgentForOperator waits for a check-in and keeps the session awake until
// the foreground operation is finished. It does not create a background Job.
func (m *Manager) holdAgentForOperator(ctx context.Context, agentID string) (*mux.Mux, func(), error) {
	m.mu.Lock()
	state := m.agents[agentID]
	retained := m.offlineAgents[agentID]
	if state == nil && (retained.ConnectionState != "sleeping" || !time.Now().Before(retained.SleepLostAfter)) {
		m.mu.Unlock()
		return nil, nil, errors.New("agent has missed its check-ins or is disconnected")
	}
	if m.pendingLive == nil {
		m.pendingLive = make(map[string]int)
	}
	m.pendingLive[agentID]++
	m.mu.Unlock()
	release := func() {
		m.mu.Lock()
		m.pendingLive[agentID]--
		if m.pendingLive[agentID] == 0 {
			delete(m.pendingLive, agentID)
		}
		m.mu.Unlock()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			release()
			return nil, nil, err
		}
		m.mu.RLock()
		state = m.agents[agentID]
		retained = m.offlineAgents[agentID]
		m.mu.RUnlock()
		if state != nil {
			state.mux.Unquiesce()
			if !state.mux.IsSleepCommitted() {
				return state.mux, release, nil
			}
		} else if retained.ConnectionState != "sleeping" || !time.Now().Before(retained.SleepLostAfter) {
			release()
			return nil, nil, errors.New("agent missed its expected check-ins")
		}
		select {
		case <-ctx.Done():
			release()
			return nil, nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
