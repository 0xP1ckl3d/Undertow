//go:build windows && amd64

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"undertow/internal/bof"
	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/pivot"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "_bof-worker" {
		if err := bof.WorkerMain(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

type consoleBOFTransport struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func (t *consoleBOFTransport) Send(ctx context.Context, packet []byte) error {
	select {
	case t.out <- append([]byte(nil), packet...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return io.EOF
	}
}
func (t *consoleBOFTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case packet := <-t.in:
		return packet, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}
func (t *consoleBOFTransport) Close() error { t.once.Do(func() { close(t.done) }); return nil }

func TestLoadedBOFForegroundThroughAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &consoleBOFTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &consoleBOFTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer server.Close()
	defer agent.Close()
	go pivot.ServeAgentWithCapabilities(ctx, agent, pivot.DefaultCapabilities())
	path := copyBOFFixture(t, "hello")
	object, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	caller := func(_ context.Context, _, route string, _ any) ([]byte, error) {
		if route != "/v1/status" {
			return nil, fmt.Errorf("unexpected route %s", route)
		}
		return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a", Hostname: "TALON"}}})
	}
	opener := func(ctx context.Context, agentID string, source, arguments []byte) (*pivot.InteractiveSession, error) {
		if agentID != "agent-a" || !bytes.Equal(source, object) {
			return nil, fmt.Errorf("unexpected BOF request for %s", agentID)
		}
		return pivot.OpenBOF(ctx, server, source, arguments)
	}
	var output strings.Builder
	input := fmt.Sprintf("load bof %q hello\nuse 1\nhello\nquit\n", path)
	if err := runConsole(ctx, strings.NewReader(input), &output, caller, nil, nil, nil, nil, consoleFeatures{bof: opener}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "hello BOF pid=") || !strings.Contains(output.String(), "[exit 0]") {
		t.Fatalf("foreground output: %s", output.String())
	}
}
