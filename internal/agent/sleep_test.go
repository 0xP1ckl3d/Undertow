package agent

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"undertow/internal/control"
	"undertow/internal/mux"
)

type sleepTestTransport struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func (t *sleepTestTransport) Send(ctx context.Context, data []byte) error {
	select {
	case t.out <- append([]byte(nil), data...):
		return nil
	case <-t.done:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (t *sleepTestTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case data := <-t.in:
		return data, nil
	case <-t.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (t *sleepTestTransport) Close() error { t.once.Do(func() { close(t.done) }); return nil }

func TestIdleSleepWaitsForLiveStreamAndServerGrant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &sleepTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &sleepTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer agent.Close()
	accepted := make(chan *mux.Stream, 1)
	go func() {
		stream, err := server.Accept(ctx)
		if err == nil {
			_ = stream.AcceptOpen(ctx)
			accepted <- stream
		}
	}()
	stream, err := agent.Open(ctx, "example.invalid:1")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	serverStream := <-accepted
	defer serverStream.Close()
	sleep, stop := idleSleep(ctx, agent, control.SleepPolicy{IntervalSeconds: 1})
	defer stop()
	noRequest, stop := context.WithTimeout(ctx, 1300*time.Millisecond)
	defer stop()
	if data, err := server.RecvControl(noRequest); err == nil {
		t.Fatalf("sleep requested during live stream: %s", data)
	}
	_ = stream.Close()
	_ = serverStream.Close()
	data, err := server.RecvControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var request control.SleepMessage
	if json.Unmarshal(data, &request) != nil || request.Kind != "request" {
		t.Fatalf("sleep request: %s", data)
	}
	if err := server.SendControl(ctx, control.EncodeSleepMessage("granted", nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case delay := <-sleep:
		if delay != time.Second {
			t.Fatalf("sleep delay = %s", delay)
		}
	case <-ctx.Done():
		t.Fatal("sleep grant was not applied")
	}
}
