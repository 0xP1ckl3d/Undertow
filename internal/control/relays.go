package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type RelayInfo struct {
	AgentID string `json:"agent_id"`
	Bind    string `json:"bind"`
	State   string `json:"state,omitempty"`
}

type relayState struct {
	stream   *mux.Stream
	commands sync.Mutex
}

func relayAllowed(agent *agentState) bool {
	if agent == nil || !agent.inventoryReady || agent.inventory.Capabilities == nil {
		return false
	}
	for _, name := range agent.inventory.Capabilities.Allowed {
		if name == "relay" {
			return true
		}
	}
	return false
}

func (m *Manager) StartRelay(ctx context.Context, agentID, bind string) (RelayInfo, error) {
	if bind == "" {
		bind = "0.0.0.0:8443"
	}
	if err := pivot.ValidateRelayBind(bind); err != nil {
		return RelayInfo{}, err
	}
	m.mu.Lock()
	live := m.agents[agentID]
	retained := m.offlineAgents[agentID]
	checkin := live != nil && live.inventoryReady && live.inventory.SleepSupported && live.inventory.Sleep.IntervalSeconds > 0
	sleeping := live == nil && retained.ConnectionState == "sleeping" && !retained.SleepLostAfter.IsZero() && time.Now().Before(retained.SleepLostAfter)
	if checkin || sleeping {
		info := retained
		if checkin {
			info = live.inventory
		}
		if !relayInfoAllowed(info) {
			m.mu.Unlock()
			return RelayInfo{}, errors.New("agent relay capability is unavailable or disabled")
		}
		if m.desiredRelays[agentID][bind] {
			m.mu.Unlock()
			return RelayInfo{}, fmt.Errorf("relay %s is already configured", bind)
		}
		if m.operations == nil {
			m.mu.Unlock()
			return RelayInfo{}, errors.New("operations store unavailable")
		}
		if err := m.operations.SetRelayListener(agentID, bind, true); err != nil {
			m.mu.Unlock()
			return RelayInfo{}, err
		}
		if m.desiredRelays[agentID] == nil {
			m.desiredRelays[agentID] = make(map[string]bool)
		}
		m.desiredRelays[agentID][bind] = true
		var stream *mux.Mux
		if live != nil {
			stream = live.mux
		}
		m.mu.Unlock()
		m.PublishEvent("relay.pending", agentID)
		if stream != nil {
			m.restoreRelays(agentID, stream)
		}
		return RelayInfo{AgentID: agentID, Bind: bind, State: "pending"}, nil
	}
	m.mu.Unlock()
	return m.startRelay(ctx, agentID, bind, true)
}

func relayInfoAllowed(info AgentInfo) bool {
	if info.Capabilities == nil {
		return false
	}
	for _, name := range info.Capabilities.Allowed {
		if name == "relay" {
			return true
		}
	}
	return false
}

func (m *Manager) startRelay(ctx context.Context, agentID, bind string, remember bool) (RelayInfo, error) {
	if bind == "" {
		bind = "0.0.0.0:8443"
	}
	if err := pivot.ValidateRelayBind(bind); err != nil {
		return RelayInfo{}, err
	}
	m.mu.RLock()
	agent := m.agents[agentID]
	if !relayAllowed(agent) {
		m.mu.RUnlock()
		return RelayInfo{}, errors.New("agent relay capability is unavailable or disabled")
	}
	if m.relays[agentID][bind] != nil {
		m.mu.RUnlock()
		return RelayInfo{}, fmt.Errorf("relay already listening on %s", bind)
	}
	if remember && m.restoringRelays[agentID][bind] {
		m.mu.RUnlock()
		return RelayInfo{}, fmt.Errorf("relay %s is being restored", bind)
	}
	agentMux := agent.mux
	m.mu.RUnlock()
	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	stream, err := agentMux.Open(openCtx, pivot.RelayListenerDestination)
	cancel()
	if err != nil {
		return RelayInfo{}, err
	}
	if err := json.NewEncoder(stream).Encode(pivot.RelayListenerRequest{Bind: bind}); err != nil {
		_ = stream.Close()
		return RelayInfo{}, err
	}
	waitDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-time.After(15 * time.Second):
			_ = stream.Close()
		case <-waitDone:
		}
	}()
	var result pivot.RelayListenerResult
	err = json.NewDecoder(stream).Decode(&result)
	close(waitDone)
	if err != nil {
		_ = stream.Close()
		return RelayInfo{}, err
	}
	if result.Error != "" {
		_ = stream.Close()
		return RelayInfo{}, errors.New(result.Error)
	}
	if result.Bind == "" {
		_ = stream.Close()
		return RelayInfo{}, errors.New("agent returned an empty relay bind")
	}
	m.mu.Lock()
	if m.agents[agentID] == nil || m.agents[agentID].mux != agentMux {
		m.mu.Unlock()
		_ = stream.Close()
		return RelayInfo{}, errors.New("agent disconnected while starting relay")
	}
	if !remember && !m.desiredRelays[agentID][bind] {
		m.mu.Unlock()
		_ = stream.Close()
		return RelayInfo{}, errors.New("relay restoration was cancelled")
	}
	if remember {
		if m.operations != nil {
			if err := m.operations.SetRelayListener(agentID, result.Bind, true); err != nil {
				m.mu.Unlock()
				_ = stream.Close()
				return RelayInfo{}, err
			}
		}
		if m.desiredRelays[agentID] == nil {
			m.desiredRelays[agentID] = make(map[string]bool)
		}
		m.desiredRelays[agentID][result.Bind] = true
	}
	if m.relays[agentID] == nil {
		m.relays[agentID] = make(map[string]*relayState)
	}
	state := &relayState{stream: stream}
	m.relays[agentID][result.Bind] = state
	m.mu.Unlock()
	m.PublishEvent("relay.started", agentID)
	go func() {
		<-stream.Done()
		m.mu.Lock()
		if m.relays[agentID][result.Bind] == state {
			delete(m.relays[agentID], result.Bind)
		}
		m.mu.Unlock()
		m.PublishEvent("relay.stopped", agentID)
	}()
	return RelayInfo{AgentID: agentID, Bind: result.Bind, State: "active"}, nil
}

func (m *Manager) RelayList(agentID string) []RelayInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []RelayInfo
	for id, listeners := range m.relays {
		if agentID != "" && id != agentID {
			continue
		}
		for bind := range listeners {
			out = append(out, RelayInfo{AgentID: id, Bind: bind, State: "active"})
		}
	}
	for id, configured := range m.desiredRelays {
		if agentID != "" && id != agentID {
			continue
		}
		for bind := range configured {
			if m.relays[id][bind] == nil {
				out = append(out, RelayInfo{AgentID: id, Bind: bind, State: "pending"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AgentID != out[j].AgentID {
			return out[i].AgentID < out[j].AgentID
		}
		return out[i].Bind < out[j].Bind
	})
	if out == nil {
		return []RelayInfo{}
	}
	return out
}

func (m *Manager) StopRelay(agentID, bind string) error {
	m.mu.Lock()
	listeners := m.relays[agentID]
	if bind == "" {
		choices := make(map[string]bool)
		for value := range listeners {
			choices[value] = true
		}
		for value := range m.desiredRelays[agentID] {
			choices[value] = true
		}
		if len(choices) != 1 {
			m.mu.Unlock()
			return errors.New("specify relay bind when zero or multiple listeners are configured")
		}
		for value := range choices {
			bind = value
		}
	}
	state := listeners[bind]
	configured := m.desiredRelays[agentID][bind]
	if state == nil && !configured {
		m.mu.Unlock()
		return fmt.Errorf("relay %s is not configured on %s", agentID, bind)
	}
	if configured && m.operations != nil {
		if err := m.operations.SetRelayListener(agentID, bind, false); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	delete(m.desiredRelays[agentID], bind)
	if state != nil {
		delete(listeners, bind)
	}
	m.mu.Unlock()
	if state == nil {
		m.PublishEvent("relay.stopped", agentID)
		return nil
	}
	return state.stream.Close()
}

// Relay binds are an operator's durable configuration. Recreate their streams
// only after the same parent agent has reconnected and sent its capabilities.
func (m *Manager) restoreRelays(agentID string, agentMux *mux.Mux) {
	m.mu.Lock()
	if m.agents[agentID] == nil || m.agents[agentID].mux != agentMux || !relayAllowed(m.agents[agentID]) {
		m.mu.Unlock()
		return
	}
	var pending []string
	for bind := range m.desiredRelays[agentID] {
		if m.relays[agentID][bind] != nil || m.restoringRelays[agentID][bind] {
			continue
		}
		if m.restoringRelays[agentID] == nil {
			m.restoringRelays[agentID] = make(map[string]bool)
		}
		m.restoringRelays[agentID][bind] = true
		pending = append(pending, bind)
	}
	m.mu.Unlock()
	for _, bind := range pending {
		go func(bind string) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			var err error
			for attempt := 0; attempt < 4; attempt++ {
				m.mu.RLock()
				current := m.agents[agentID] != nil && m.agents[agentID].mux == agentMux && m.desiredRelays[agentID][bind]
				m.mu.RUnlock()
				if !current {
					break
				}
				_, err = m.startRelay(ctx, agentID, bind, false)
				if err == nil {
					break
				}
				if attempt < 3 {
					select {
					case <-ctx.Done():
					case <-agentMux.Done():
					case <-time.After(time.Duration(attempt+1) * time.Second):
					}
				}
			}
			m.mu.Lock()
			delete(m.restoringRelays[agentID], bind)
			stillWanted := m.agents[agentID] != nil && m.agents[agentID].mux == agentMux && m.desiredRelays[agentID][bind]
			m.mu.Unlock()
			if err != nil && stillWanted {
				log.Printf("restore relay %s on agent %s: %v", bind, agentID, err)
				m.PublishEvent("relay.restore_failed", agentID)
			} else {
				log.Printf("restored relay %s on agent %s", bind, agentID)
			}
		}(bind)
	}
}

// SetRelayPayload adds or removes an opaque artifact token on an already
// running relay listener. Commands and results share its authenticated control
// stream; no second listener is created.
func (m *Manager) SetRelayPayload(ctx context.Context, agentID, bind, token string, enabled bool) (pivot.RelayPayloadResult, <-chan struct{}, error) {
	m.mu.RLock()
	state := m.relays[agentID][bind]
	m.mu.RUnlock()
	if state == nil {
		return pivot.RelayPayloadResult{}, nil, errors.New("start the selected agent relay listener first")
	}
	state.commands.Lock()
	defer state.commands.Unlock()
	operation := "remove"
	if enabled {
		operation = "add"
	}
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		select {
		case <-deadline.Done():
			_ = state.stream.Close()
		case <-done:
		}
	}()
	defer close(done)
	if err := json.NewEncoder(state.stream).Encode(pivot.RelayPayloadCommand{Operation: operation, Token: token}); err != nil {
		return pivot.RelayPayloadResult{}, nil, err
	}
	var result pivot.RelayPayloadResult
	if err := json.NewDecoder(state.stream).Decode(&result); err != nil {
		return pivot.RelayPayloadResult{}, nil, err
	}
	if result.Error != "" {
		return pivot.RelayPayloadResult{}, nil, errors.New(result.Error)
	}
	return result, state.stream.Done(), nil
}
