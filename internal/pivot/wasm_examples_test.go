package pivot

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPackagedWASMExamplesOnAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgent(ctx, agent)
	artifactRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactRoot, "custom-audit.txt"), []byte("metadata only"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		stdin []byte
		want  string
	}{
		{"triage", []string{"audit"}, nil, "Host triage:"},
		{"privilege-audit", nil, nil, "Privilege and configuration audit:"},
		{"inventory", []string{"tcp"}, nil, "Process, service and network inventory"},
		{"artifact-discovery", []string{artifactRoot}, []byte("custom-audit\n"), "custom-audit.txt"},
		{"persistence-audit", nil, nil, "Startup and autorun audit:"},
		{"enterprise-posture", nil, nil, "Enterprise posture:"},
	} {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "examples", "wasm", name, name+".wasm")
			module, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			session, err := OpenWASM(ctx, server, module, tc.args, tc.stdin)
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
				if kind == InteractiveOutput {
					output.Write(data)
				}
				if kind == InteractiveError {
					t.Logf("agent error: %s", data)
				}
				if kind == InteractiveExit {
					if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 {
						t.Fatalf("exit %v, output: %s", data, output.String())
					}
					if !strings.Contains(output.String(), tc.want) {
						t.Fatalf("unexpected output: %s", output.String())
					}
					return
				}
			}
		})
	}
}

func TestWASMInventoryOperations(t *testing.T) {
	operations := []string{"processes", "privileges", "routes", "dns_config", "users", "groups", "neighbours", "connections"}
	if runtime.GOOS == "windows" {
		operations = append(operations, "services", "startup")
	}
	for _, op := range operations {
		t.Run(op, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			value, err := wasmHostOperation(ctx, op, wasmHostRequest{})
			if err != nil || value == nil {
				t.Fatalf("duration=%s value=%v err=%v", time.Since(start), value, err)
			}
		})
	}
}

func TestWASMHostPrimitives(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("host-api-sample"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"version", "system", "environment", "interfaces", "fs.list", "fs.stat", "fs.walk", "fs.read", "dns.resolve"} {
		req := wasmHostRequest{Path: path, Host: "localhost", Limit: 4}
		if op == "fs.list" || op == "fs.walk" {
			req.Path = root
		}
		value, err := wasmHostOperation(ctx, op, req)
		if err != nil || value == nil {
			t.Fatalf("%s: value=%v err=%v", op, value, err)
		}
		if op == "fs.read" {
			encoded := value.(map[string]any)["data_base64"].(string)
			data, _ := base64.StdEncoding.DecodeString(encoded)
			if string(data) != "host" {
				t.Fatalf("fs.read=%q", data)
			}
		}
	}
	if _, err := wasmHostOperation(ctx, "fs.read", wasmHostRequest{Path: path, Offset: -1}); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err := wasmHostOperation(ctx, "future.operation", wasmHostRequest{}); err != errWASMUnsupported {
		t.Fatalf("unknown operation: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			buf := make([]byte, 4)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte("pong"))
		}
	}()
	value, err := wasmHostOperation(ctx, "net.tcp_exchange", wasmHostRequest{Address: listener.Addr().String(), Data: base64.StdEncoding.EncodeToString([]byte("ping")), Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	response, _ := base64.StdEncoding.DecodeString(value.(map[string]any)["data_base64"].(string))
	if string(response) != "pong" {
		t.Fatalf("tcp response=%q", response)
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buf := make([]byte, 4)
		_, address, err := udp.ReadFrom(buf)
		if err == nil {
			_, _ = udp.WriteTo([]byte("pong"), address)
		}
	}()
	value, err = wasmHostOperation(ctx, "net.udp_exchange", wasmHostRequest{Address: udp.LocalAddr().String(), Data: base64.StdEncoding.EncodeToString([]byte("ping")), Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	response, _ = base64.StdEncoding.DecodeString(value.(map[string]any)["data_base64"].(string))
	if string(response) != "pong" {
		t.Fatalf("udp response=%q", response)
	}
	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener2.Close()
	go func() {
		conn, err := listener2.Accept()
		if err == nil {
			defer conn.Close()
			buf := make([]byte, 4)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte("pong"))
		}
	}()
	sockets := &wasmSocketState{connections: make(map[uint32]net.Conn)}
	defer sockets.closeAll()
	opened, err := sockets.operation(ctx, "net.open", wasmHostRequest{Network: "tcp", Address: listener2.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	handle := opened.(map[string]any)["handle"].(uint32)
	if _, err := sockets.operation(ctx, "net.write", wasmHostRequest{Handle: handle, Data: base64.StdEncoding.EncodeToString([]byte("ping"))}); err != nil {
		t.Fatal(err)
	}
	value, err = sockets.operation(ctx, "net.read", wasmHostRequest{Handle: handle, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	response, _ = base64.StdEncoding.DecodeString(value.(map[string]any)["data_base64"].(string))
	if string(response) != "pong" {
		t.Fatalf("socket response=%q", response)
	}
	if _, err := sockets.operation(ctx, "net.close", wasmHostRequest{Handle: handle}); err != nil {
		t.Fatal(err)
	}
	if _, err := sockets.operation(ctx, "net.read", wasmHostRequest{Handle: handle}); err == nil {
		t.Fatal("closed handle remained readable")
	}
	for id := uint32(1); id <= 8; id++ {
		left, right := net.Pipe()
		sockets.connections[id] = left
		defer right.Close()
	}
	if _, err := sockets.operation(ctx, "net.open", wasmHostRequest{Network: "tcp", Address: listener2.Addr().String()}); err == nil {
		t.Fatal("ninth socket handle was accepted")
	}
}

// Set UNDERTOW_WASM_TEST_MODULE to smoke-test an arbitrary third-party module
// against a real in-process agent without starting a server or VPN client.
func TestCustomWASMOnAgent(t *testing.T) {
	path := os.Getenv("UNDERTOW_WASM_TEST_MODULE")
	if path == "" {
		t.Skip("set UNDERTOW_WASM_TEST_MODULE to a compiled WASM path")
	}
	module, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgent(ctx, agent)
	session, err := OpenWASM(ctx, server, module, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for {
		kind, data, err := session.Read()
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case InteractiveOutput, InteractiveStderr:
			t.Logf("%s", data)
		case InteractiveError:
			t.Errorf("agent: %s", data)
		case InteractiveExit:
			if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 {
				t.Fatalf("exit code: %v", data)
			}
			return
		}
	}
}
