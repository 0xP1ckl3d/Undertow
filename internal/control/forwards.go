package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

type ForwardInfo struct {
	AgentID string `json:"agent_id"`
	Bind    string `json:"bind"`
	Target  string `json:"target"`
	ID      string `json:"id"`
}

type forwardState struct {
	ForwardInfo
	ClientID  uint64
	agentMux  *mux.Mux
	clientMux *mux.Mux
	control   *mux.Stream
}

func (m *Manager) AddClientForward(ctx context.Context, clientID uint64, agentID, bind, target string) (ForwardInfo, error) {
	if err := pivot.ValidateForwardAddress(bind, false); err != nil {
		return ForwardInfo{}, err
	}
	if err := pivot.ValidateForwardAddress(target, true); err != nil {
		return ForwardInfo{}, err
	}
	m.mu.RLock()
	client := m.clients[clientID]
	agent := m.agents[agentID]
	if client == nil || agent == nil {
		m.mu.RUnlock()
		return ForwardInfo{}, errors.New("client or agent is not connected")
	}
	if agent.inventory.Capabilities != nil {
		allowed := false
		for _, capability := range agent.inventory.Capabilities.Allowed {
			allowed = allowed || capability == "listeners"
		}
		if !allowed {
			m.mu.RUnlock()
			return ForwardInfo{}, errors.New("agent listeners capability is disabled")
		}
	}
	for _, existing := range m.forwards {
		if existing.AgentID == agentID && existing.Bind == bind {
			m.mu.RUnlock()
			return ForwardInfo{}, errors.New("agent bind address is already forwarded")
		}
	}
	clientMux, agentMux := client.mux, agent.mux
	m.mu.RUnlock()
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return ForwardInfo{}, err
	}
	id := hex.EncodeToString(randomID[:])
	openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stream, err := agentMux.Open(openCtx, pivot.ListenerDestination)
	if err != nil {
		return ForwardInfo{}, err
	}
	closeOnTimeout := context.AfterFunc(openCtx, func() { _ = stream.Close() })
	defer closeOnTimeout()
	if err := json.NewEncoder(stream).Encode(pivot.ListenerRequest{ID: id, Bind: bind}); err != nil {
		_ = stream.Close()
		return ForwardInfo{}, err
	}
	var result pivot.ListenerResult
	if err := json.NewDecoder(io.LimitReader(stream, 1024)).Decode(&result); err != nil {
		_ = stream.Close()
		return ForwardInfo{}, err
	}
	if result.Error != "" {
		_ = stream.Close()
		return ForwardInfo{}, errors.New(result.Error)
	}
	if err := pivot.ValidateForwardAddress(result.Bind, false); err != nil {
		_ = stream.Close()
		return ForwardInfo{}, errors.New("agent returned invalid listener address")
	}
	info := ForwardInfo{AgentID: agentID, Bind: result.Bind, Target: target, ID: id}
	forward := &forwardState{ForwardInfo: info, ClientID: clientID, agentMux: agentMux, clientMux: clientMux, control: stream}
	m.mu.Lock()
	if m.clients[clientID] == nil || m.clients[clientID].mux != clientMux || m.agents[agentID] == nil || m.agents[agentID].mux != agentMux {
		m.mu.Unlock()
		_ = stream.Close()
		return ForwardInfo{}, errors.New("client or agent disconnected during listener setup")
	}
	m.forwards[id] = forward
	m.mu.Unlock()
	go func() {
		<-stream.Done()
		m.mu.Lock()
		if m.forwards[id] == forward {
			delete(m.forwards, id)
		}
		m.mu.Unlock()
	}()
	return info, nil
}

func (m *Manager) ClientForwards(clientID uint64) []ForwardInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var forwards []ForwardInfo
	for _, forward := range m.forwards {
		if forward.ClientID == clientID {
			forwards = append(forwards, forward.ForwardInfo)
		}
	}
	sort.Slice(forwards, func(i, j int) bool {
		if forwards[i].AgentID != forwards[j].AgentID {
			return forwards[i].AgentID < forwards[j].AgentID
		}
		return forwards[i].Bind < forwards[j].Bind
	})
	return forwards
}

func (m *Manager) AgentForwards(agentID string) []ForwardInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ForwardInfo
	for _, forward := range m.forwards {
		if forward.AgentID == agentID {
			out = append(out, forward.ForwardInfo)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bind < out[j].Bind })
	return out
}

func (m *Manager) DeleteClientForward(clientID uint64, agentID, bind string) error {
	m.mu.Lock()
	var stream *mux.Stream
	for id, forward := range m.forwards {
		if forward.ClientID == clientID && forward.AgentID == agentID && forward.Bind == bind {
			stream = forward.control
			delete(m.forwards, id)
			break
		}
	}
	m.mu.Unlock()
	if stream == nil {
		return errors.New("forward not found")
	}
	return stream.Close()
}

func (m *Manager) serveAgentForwards(agentMux *mux.Mux) {
	ctx := context.Background()
	for {
		stream, err := agentMux.Accept(ctx)
		if err != nil {
			return
		}
		id, valid := pivot.ForwardID(stream.Destination(), "forward")
		if !valid {
			stream.Fail(errors.New("unknown agent stream"))
			continue
		}
		go m.handleAgentForward(agentMux, id, stream)
	}
}

func (m *Manager) handleAgentForward(agentMux *mux.Mux, id string, upstream *mux.Stream) {
	m.mu.RLock()
	forward := m.forwards[id]
	if forward == nil || forward.agentMux != agentMux {
		m.mu.RUnlock()
		upstream.Fail(errors.New("forward is no longer active"))
		return
	}
	clientMux, target := forward.clientMux, forward.Target
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := upstream.AcceptOpen(ctx); err != nil {
		_ = upstream.Close()
		return
	}
	downstream, err := clientMux.Open(ctx, pivot.ClientForwardDestination(id))
	if err != nil {
		_ = upstream.Close()
		return
	}
	defer downstream.Close()
	if err := json.NewEncoder(downstream).Encode(pivot.ClientForwardRequest{Target: target}); err != nil {
		_ = upstream.Close()
		return
	}
	reader, err := pivot.ForwardReady(downstream)
	if err != nil {
		_ = upstream.Close()
		return
	}
	pivot.BridgeForwardStreams(context.Background(), upstream, downstream, reader)
}
