package pivot

import (
	"context"
	_ "embed"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// Tiny WASI fixtures from wazero v1.12.0 testdata (Apache-2.0).
//
//go:embed testdata/wasm_args.wasm
var wasmArgsFixture []byte

//go:embed testdata/wasm_loop.wasm
var wasmLoopFixture []byte

//go:embed testdata/wasm_stdin.wasm
var wasmStdinFixture []byte

func TestWASMFromMemoryAndCapabilityIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	caps := DefaultCapabilities()
	caps.Exec, caps.Scripts, caps.Interactive = false, false, false
	go ServeAgentWithCapabilities(ctx, agent, caps)
	session, err := OpenWASM(ctx, server, wasmArgsFixture, []string{"audit"}, []byte("controlled stdin"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var output strings.Builder
	for {
		kind, data, err := session.Read()
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case InteractiveOutput:
			output.Write(data)
		case InteractiveError:
			t.Fatalf("WASM error: %s", data)
		case InteractiveExit:
			if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 || !strings.Contains(output.String(), "audit\x00") {
				t.Fatalf("exit=%v output=%q", data, output.String())
			}
			goto denied
		}
	}
denied:
	caps.WASM = false
	deniedServer, deniedAgent := execTestMuxPair(ctx)
	defer deniedServer.Close()
	defer deniedAgent.Close()
	go ServeAgentWithCapabilities(ctx, deniedAgent, caps)
	if _, err := OpenWASM(ctx, deniedServer, wasmArgsFixture, nil, nil); err == nil || !strings.Contains(err.Error(), "wasm execution is disabled") {
		t.Fatalf("WASM denial=%v", err)
	}
	if _, err := OpenWASM(ctx, server, []byte("not wasm"), nil, nil); err == nil {
		t.Fatal("invalid module was accepted")
	}
	if _, err := OpenWASM(ctx, server, wasmArgsFixture, nil, make([]byte, WASMStdinLimit+1)); err == nil {
		t.Fatal("oversized stdin was accepted")
	}
}

func TestWASMConcurrencyAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgent(ctx, agent)
	first, err := OpenWASM(ctx, server, wasmLoopFixture, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenWASM(ctx, server, wasmLoopFixture, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := OpenWASM(ctx, server, wasmArgsFixture, nil, nil); err == nil || !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("third WASM run=%v", err)
	}
	first.Close()
	second.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		session, err := OpenWASM(ctx, server, wasmArgsFixture, nil, nil)
		if err == nil {
			session.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled WASM slots were not released: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWASMControlledStdinAndStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgent(ctx, agent)
	readOutput := func(session *InteractiveSession) (string, string) {
		t.Helper()
		defer session.Close()
		var stdout, stderr strings.Builder
		for {
			kind, data, err := session.Read()
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case InteractiveOutput:
				stdout.Write(data)
			case InteractiveStderr:
				stderr.Write(data)
			case InteractiveError:
				t.Fatalf("WASM error: %s", data)
			case InteractiveExit:
				if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 {
					t.Fatalf("exit=%v", data)
				}
				return stdout.String(), stderr.String()
			}
		}
	}
	stdinRun, err := OpenWASM(ctx, server, wasmStdinFixture, nil, []byte("stdin-payload"))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := readOutput(stdinRun)
	if stdout != "stdin-payload" || stderr != "" {
		t.Fatalf("stdin stdout=%q stderr=%q", stdout, stderr)
	}
	// This 219-byte fixture writes args via fd_write(1). Change its one-byte
	// fd constant to fd_write(2) to exercise the same WASI stderr path.
	stderrModule := append([]byte(nil), wasmArgsFixture...)
	if len(stderrModule) != 219 || stderrModule[207] != 0x41 || stderrModule[208] != 1 {
		t.Fatal("WASM stderr fixture layout changed")
	}
	stderrModule[208] = 2
	stderrRun, err := OpenWASM(ctx, server, stderrModule, []string{"stderr-payload"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr = readOutput(stderrRun)
	if stdout != "" || !strings.Contains(stderr, "stderr-payload") {
		t.Fatalf("stderr stdout=%q stderr=%q", stdout, stderr)
	}
}
