//go:build windows && amd64

package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/pivot"
	"undertow/internal/routing"
)

func nativeExample(t *testing.T, name string) []byte {
	t.Helper()
	module, err := os.ReadFile(filepath.Join("..", "..", "examples", "native", name, name+".module"))
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func nativeResult(t *testing.T, session *pivot.InteractiveSession) (int, string) {
	t.Helper()
	defer session.Close()
	var output strings.Builder
	for {
		kind, data, err := session.Read()
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case pivot.InteractiveOutput:
			output.Write(data)
		case pivot.InteractiveError:
			t.Fatalf("native execution error: %s", data)
		case pivot.InteractiveExit:
			if len(data) != 4 {
				t.Fatalf("invalid exit frame %v", data)
			}
			return int(int32(binary.BigEndian.Uint32(data))), output.String()
		}
	}
}

func TestNativeOperatorServerAgentWindowsAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	serverAgent, agent := forwardAuditAgent(t, ctx, manager, "native-agent", 931, pivot.DefaultCapabilities())
	defer serverAgent.Close()
	defer agent.Close()
	httpServer := httptest.NewServer(manager.handler("operator-secret"))
	defer httpServer.Close()
	module := nativeExample(t, "wininfo")
	for i := 0; i < 2; i++ {
		conn, err := net.Dial("tcp", strings.TrimPrefix(httpServer.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(conn, "CONNECT /v1/agents/native-agent/native HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer operator-secret\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(conn)
		response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("CONNECT response=%v error=%v", response, err)
		}
		session, err := pivot.StartMemorySession(ctx, conn.(*net.TCPConn), reader, pivot.MemoryRequest{Size: len(module)}, module)
		if err != nil {
			t.Fatal(err)
		}
		code, output := nativeResult(t, session)
		if code != 0 || !strings.Contains(output, "Windows computer=") || !strings.Contains(output, "processors=") || !strings.Contains(output, "memory_mb=") {
			t.Fatalf("code=%d output=%q", code, output)
		}
	}
	job, err := manager.StartNativeJob(ctx, 932, "native-agent", nativeExample(t, "hello"), []string{"from-job"}, []byte{0, 1, 255})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, manager, 932, job.ID, func(j JobInfo) bool { return j.State == "completed" || j.State == "failed" })
	if finished.State != "completed" || finished.ExitCode == nil || *finished.ExitCode != 0 || !strings.Contains(finished.Output, "from-job") || !strings.Contains(finished.Output, "opaque data bytes=3") {
		t.Fatalf("native job=%+v", finished)
	}
}

func TestNativeJobCancellationAndReturn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	server, agent := forwardAuditAgent(t, ctx, manager, "native-agent", 941, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	module := nativeExample(t, "hello")
	failed, err := manager.StartNativeJob(ctx, 942, "native-agent", module, []string{"--fail"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := waitJob(t, manager, 942, failed.ID, func(j JobInfo) bool { return j.State == "failed" })
	if result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatalf("non-zero result=%+v", result)
	}
	running, err := manager.StartNativeJob(ctx, 942, "native-agent", module, []string{"--wait"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CancelJob(942, running.ID); err != nil {
		t.Fatal(err)
	}
	result = waitJob(t, manager, 942, running.ID, func(j JobInfo) bool { return j.State == "cancelled" })
	if result.Ended == nil {
		t.Fatalf("cancellation=%+v", result)
	}
}

func TestNativeLoaderReportsMissingEntryAndImport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	server, agent := forwardAuditAgent(t, ctx, manager, "native-agent", 951, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	base := nativeExample(t, "hello")
	for _, tc := range []struct{ old, replacement, message string }{
		{"undertow_main", "undertow_none", "missing native entry point"},
		{"KERNEL32.dll", "MISSINGX.dll", "load native module"},
	} {
		if len(tc.old) != len(tc.replacement) {
			t.Fatal("fixture replacement must preserve PE offsets")
		}
		module := bytes.Replace(base, []byte(tc.old), []byte(tc.replacement), 1)
		if bytes.Equal(module, base) {
			t.Fatalf("fixture lacks %q", tc.old)
		}
		session, err := pivot.OpenNative(ctx, server, module, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var seen bool
		for {
			kind, data, err := session.Read()
			if err != nil {
				t.Fatal(err)
			}
			if kind == pivot.InteractiveError && strings.Contains(string(data), tc.message) {
				seen = true
			}
			if kind == pivot.InteractiveExit {
				break
			}
		}
		session.Close()
		if !seen {
			t.Fatalf("loader did not report %q", tc.message)
		}
	}
}
