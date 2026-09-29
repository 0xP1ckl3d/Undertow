package pivot

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"undertow/internal/mux"
)

type execTestTransport struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func (t *execTestTransport) Send(ctx context.Context, b []byte) error {
	select {
	case t.out <- append([]byte(nil), b...):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return io.EOF
	}
}
func (t *execTestTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.in:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, io.EOF
	}
}
func (t *execTestTransport) Close() error { t.once.Do(func() { close(t.done) }); return nil }

func execTestMuxPair(ctx context.Context) (*mux.Mux, *mux.Mux) {
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	server := mux.New(ctx, &execTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &execTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	return server, agent
}

func TestExecHelperProcess(t *testing.T) {
	if os.Getenv("UNDERTOW_EXEC_HELPER") != "1" {
		return
	}
	fmt.Print("executed without a shell")
	os.Exit(0)
}

func TestAgentCanDisableExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithExec(ctx, agent, false)
	_, err := Execute(ctx, server, []string{"unused-command"})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("execution was not rejected: %v", err)
	}
}

func TestAgentExecutesArgvWithoutShell(t *testing.T) {
	t.Setenv("UNDERTOW_EXEC_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithExec(ctx, agent, true)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := Execute(ctx, server, []string{exe, "-test.run=TestExecHelperProcess"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Error != "" || result.Stdout != "executed without a shell" {
		t.Fatalf("unexpected command result: %+v", result)
	}
}

func TestAgentBuiltinsUseExecCapability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithExec(ctx, agent, true)
	result, err := ExecuteRequest(ctx, server, ExecRequest{Builtin: "pwd"})
	if err != nil || result.Error != "" || strings.TrimSpace(result.Stdout) == "" {
		t.Fatalf("remote pwd: %+v, %v", result, err)
	}
}
