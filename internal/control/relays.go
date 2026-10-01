package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type RelayInfo struct {
	AgentID string `json:"agent_id"`
	Bind    string `json:"bind"`
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
		bind = "127.0.0.1:8443"
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
		case <-ctx.Done(): _ = stream.Close()
		case <-time.After(15 * time.Second): _ = stream.Close()
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
	if m.relays[agentID] == nil {
		m.relays[agentID] = make(map[string]*mux.Stream)
	}
	m.relays[agentID][result.Bind] = stream
	m.mu.Unlock()
	go func() {
		<-stream.Done()
		m.mu.Lock()
		if m.relays[agentID][result.Bind] == stream {
			delete(m.relays[agentID], result.Bind)
		}
		m.mu.Unlock()
	}()
	return RelayInfo{AgentID: agentID, Bind: result.Bind}, nil
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
			out = append(out, RelayInfo{AgentID: id, Bind: bind})
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
		if len(listeners) != 1 {
			m.mu.Unlock()
			return errors.New("specify relay bind when zero or multiple listeners exist")
		}
		for value := range listeners {
			bind = value
		}
	}
	stream := listeners[bind]
	if stream != nil {
		delete(listeners, bind)
	}
	m.mu.Unlock()
	if stream == nil {
		return fmt.Errorf("relay %s is not listening on %s", agentID, bind)
	}
	return stream.Close()
}
