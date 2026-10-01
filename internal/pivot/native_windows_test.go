//go:build windows && amd64

package pivot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeFixture(t *testing.T) []byte {
	t.Helper()
	module, err := os.ReadFile(filepath.Join("..", "..", "examples", "native", "hello", "hello.module"))
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func waitNativeOutput(t *testing.T, session *InteractiveSession) {
	t.Helper()
	for {
		kind, data, err := session.Read()
		if err != nil {
			t.Fatal(err)
		}
		if kind == InteractiveError {
			t.Fatalf("native error: %s", data)
		}
		if kind == InteractiveOutput && strings.Contains(string(data), "--wait") {
			return
		}
	}
}

func TestNativeConcurrencyCleanupAndDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithCapabilities(ctx, agent, DefaultCapabilities())
	module := nativeFixture(t)
	first, err := OpenNative(ctx, server, module, []string{"--wait"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitNativeOutput(t, first)
	second, err := OpenNative(ctx, server, module, []string{"--wait"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitNativeOutput(t, second)
	if _, err := OpenNative(ctx, server, module, nil, nil); err == nil || !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("third run error=%v", err)
	}
	first.Close()
	second.Close()
	deadline := time.After(3 * time.Second)
	for len(nativeSlots) != 0 {
		select {
		case <-deadline:
			t.Fatal("native slots not released after cancellation")
		case <-time.After(10 * time.Millisecond):
		}
	}
	third, err := OpenNative(ctx, server, module, []string{"--wait"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitNativeOutput(t, third)
	agent.Close() // disconnect while the DLL is active
	third.Close()
	deadline = time.After(3 * time.Second)
	for len(nativeSlots) != 0 {
		select {
		case <-deadline:
			t.Fatal("native slot not released after disconnect")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestNativeCapabilityIsIndependent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	caps := DefaultCapabilities()
	caps.WASM = false
	caps.Native = false
	go ServeAgentWithCapabilities(ctx, agent, caps)
	if _, err := OpenNative(ctx, server, nativeFixture(t), nil, nil); err == nil || !strings.Contains(err.Error(), "native execution is disabled") {
		t.Fatalf("native denial=%v", err)
	}
}
