package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

// Event is a state-change notification. Consumers refetch the affected object;
// event payloads intentionally contain no file contents or operation secrets.
type Event struct {
	Seq    uint64    `json:"seq"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Target string    `json:"target,omitempty"`
}

type EventBroker struct {
	mu        sync.Mutex
	seq       uint64
	history   []Event
	listeners map[chan Event]struct{}
}

func NewEventBroker() *EventBroker { return &EventBroker{listeners: make(map[chan Event]struct{})} }

func (b *EventBroker) Publish(kind, target string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	event := Event{Seq: b.seq, At: time.Now().UTC(), Kind: kind, Target: target}
	b.history = append(b.history, event)
	if len(b.history) > 2048 {
		b.history = append([]Event(nil), b.history[len(b.history)-2048:]...)
	}
	for ch := range b.listeners {
		select {
		case ch <- event:
		default:
			delete(b.listeners, ch)
			close(ch)
		}
	}
}

func (b *EventBroker) Subscribe(after uint64) ([]Event, <-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	replay := make([]Event, 0)
	if after > b.seq || len(b.history) > 0 && after > 0 && after+1 < b.history[0].Seq {
		replay = append(replay, Event{Seq: b.seq, At: time.Now().UTC(), Kind: "resync_required"})
	} else {
		for _, event := range b.history {
			if event.Seq > after {
				replay = append(replay, event)
			}
		}
	}
	ch := make(chan Event, 128)
	b.listeners[ch] = struct{}{}
	return replay, ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.listeners[ch]; ok {
			delete(b.listeners, ch)
			close(ch)
		}
	}
}

func (m *Manager) PublishEvent(kind, target string) { m.eventBus.Publish(kind, target) }

func (m *Manager) ServeEvents(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	var request struct {
		After uint64 `json:"after"`
	}
	if err := json.NewDecoder(io.LimitReader(stream, 128)).Decode(&request); err != nil {
		return
	}
	replay, updates, unsubscribe := m.eventBus.Subscribe(request.After)
	defer unsubscribe()
	encoder := json.NewEncoder(stream)
	for _, event := range replay {
		if encoder.Encode(event) != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-stream.Done():
			return
		case event, ok := <-updates:
			if !ok {
				return
			}
			if encoder.Encode(event) != nil {
				return
			}
		}
	}
}

func StreamEvents(ctx context.Context, session *mux.Mux, after uint64, deliver func(Event) error) error {
	if session == nil {
		return errors.New("client is disconnected")
	}
	stream, err := session.Open(ctx, pivot.EventDestination)
	if err != nil {
		return err
	}
	defer stream.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-done:
		}
	}()
	if err := json.NewEncoder(stream).Encode(map[string]uint64{"after": after}); err != nil {
		return err
	}
	if err := stream.CloseWrite(); err != nil {
		return err
	}
	decoder := json.NewDecoder(bufio.NewReader(stream))
	for {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			return err
		}
		if err := deliver(event); err != nil {
			return err
		}
	}
}

func EventCursor(value string) uint64 { cursor, _ := strconv.ParseUint(value, 10, 64); return cursor }
