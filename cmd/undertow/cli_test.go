//go:build linux || windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/control"
	"undertow/internal/pivot"
)

func TestLocalClientConsoleRPC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.pid")
	cleanup, err := startBackgroundControl(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	setBackgroundConsoleHandler(func(_ context.Context, request consoleRPCRequest) consoleRPCResponse {
		if request.Action == "session" {
			return consoleRPCResponse{SessionID: 42}
		}
		return consoleRPCResponse{Error: "unexpected action"}
	})
	defer setBackgroundConsoleHandler(nil)
	response, err := callClientConsole(path, consoleRPCRequest{Action: "session"})
	if err != nil || response.SessionID != 42 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestLocalClientInteractiveAttach(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.pid")
	cleanup, err := startBackgroundControl(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	setBackgroundInteractiveHandler(func(_ context.Context, id string, conn net.Conn) error {
		if id != "agent-a" {
			return fmt.Errorf("unexpected agent %q", id)
		}
		if _, err := io.WriteString(conn, "OK\n"); err != nil {
			return err
		}
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadBytes('\n'); err != nil {
			return err
		}
		if _, err := conn.Write([]byte{'R', 0, 0, 0, 0}); err != nil {
			return err
		}
		var frame [6]byte
		if _, err := io.ReadFull(reader, frame[:]); err != nil {
			return err
		}
		if frame[0] != 'I' || frame[5] != 'x' {
			return fmt.Errorf("input frame=%v", frame)
		}
		if _, err := conn.Write([]byte{'O', 0, 0, 0, 1, 'y', 'X', 0, 0, 0, 4, 0, 0, 0, 0}); err != nil {
			return err
		}
		_ = conn.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, reader)
		return nil
	})
	defer setBackgroundInteractiveHandler(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	live, err := openAttachedInteractive(ctx, path, "agent-a", pivot.InteractiveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.Send([]byte("x")); err != nil {
		t.Fatal(err)
	}
	kind, payload, err := live.Read()
	if err != nil || kind != pivot.InteractiveOutput || string(payload) != "y" {
		t.Fatalf("output=%q %q %v", kind, payload, err)
	}
	kind, _, err = live.Read()
	if err != nil || kind != pivot.InteractiveExit {
		t.Fatalf("exit=%q %v", kind, err)
	}
}

func TestAttachedTransferStreamsProgressAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.pid")
	cleanup, err := startBackgroundControl(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	setBackgroundTransferHandler(func(_ context.Context, body json.RawMessage, progress func(pivot.TransferProgress)) (pivot.FileMessage, error) {
		var request clientFileRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return pivot.FileMessage{}, err
		}
		if request.AgentID != "agent-a" {
			return pivot.FileMessage{}, fmt.Errorf("wrong agent %q", request.AgentID)
		}
		progress(pivot.TransferProgress{Bytes: 0, Total: 100})
		progress(pivot.TransferProgress{Bytes: 100, Total: 100, Percent: 100, Rate: 1024})
		return pivot.FileMessage{OK: true, Size: 100, SHA256: "abc"}, nil
	})
	defer setBackgroundTransferHandler(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var updates []pivot.TransferProgress
	result, err := attachedTransfer(ctx, path, clientFileRequest{AgentID: "agent-a", Operation: "upload", LocalPath: "source", RemotePath: "target"}, func(p pivot.TransferProgress) { updates = append(updates, p) })
	if err != nil || !result.OK || len(updates) != 2 || updates[1].Bytes != 100 {
		t.Fatalf("result=%+v progress=%+v err=%v", result, updates, err)
	}
	cancelled := make(chan struct{}, 1)
	setBackgroundTransferHandler(func(ctx context.Context, _ json.RawMessage, progress func(pivot.TransferProgress)) (pivot.FileMessage, error) {
		progress(pivot.TransferProgress{Bytes: 0, Total: 100})
		<-ctx.Done()
		cancelled <- struct{}{}
		return pivot.FileMessage{}, ctx.Err()
	})
	cancelCtx, cancelTransfer := context.WithCancel(context.Background())
	_, err = attachedTransfer(cancelCtx, path, clientFileRequest{AgentID: "agent-a", Operation: "upload"}, func(p pivot.TransferProgress) { cancelTransfer() })
	if err == nil {
		t.Fatal("cancelled attached transfer succeeded")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("background transfer did not receive cancellation")
	}
}

func TestStatusShowsAgentAndVPNHostnames(t *testing.T) {
	report := pivot.Capabilities{Pivot: true, Exec: false, Upload: false, Download: true}.Report()
	data, err := json.Marshal(map[string]any{
		"agents":  []control.AgentInfo{{ID: "agent-one", Hostname: "agent-host", Capabilities: &report}},
		"clients": []control.ClientInfo{{SessionID: 7, Hostname: "vpn-host"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := renderStatus(&output, data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "agent-host") || !strings.Contains(output.String(), "vpn-host") {
		t.Fatalf("hostnames missing from status: %s", output.String())
	}
	if !strings.Contains(output.String(), "supported=pivot,exec,hostops,interactive,scripts,wasm,upload,download,listeners allowed=pivot,download") {
		t.Fatalf("agent capabilities missing from status: %s", output.String())
	}
}

func TestAgentShowRendersDetailedTelemetry(t *testing.T) {
	report := pivot.Capabilities{Pivot: true, HostOps: true, Interactive: true}.Report()
	agent := control.AgentInfo{ID: "agent-a", Hostname: "pivot-host", OS: "windows", Arch: "amd64", VirtualIP: "172.16.254.2", Connected: time.Now().Add(-time.Minute), LastSeen: time.Now().Add(-time.Second), RTT: 25 * time.Millisecond, RXBytes: 12345, TXBytes: 6789, RXRate: 2048, TXRate: 1024, Retransmits: 3, Duplicates: 2, Window: 16, Queued: 5, InFlight: 7, Streams: 4, ActiveJobs: 1, ActiveForwards: 2, AdvertisedRoutes: []string{"192.168.50.0/24"}, Routes: []control.NetworkRoute{{Prefix: "10.20.0.0/16", Gateway: "192.168.50.1", Interface: "Ethernet", Source: "manual"}}, DefaultRoute: &control.NetworkRoute{Prefix: "0.0.0.0/0", Gateway: "192.168.50.1", Interface: "Ethernet"}, Capabilities: &report, Forwards: []control.ForwardInfo{{Bind: "0.0.0.0:8080", Target: "127.0.0.1:8080"}}}
	var output bytes.Buffer
	if err := renderAgentShow(&output, agent); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"pivot-host", "windows/amd64", "172.16.254.2", "25ms", "12345/6789", "Retransmits: 3", "Duplicates: 2", "CWND: 16", "Active jobs: 1", "Active TCP forwards: 2", "192.168.50.0/24", "10.20.0.0/16", "192.168.50.1", "0.0.0.0:8080 -> 127.0.0.1:8080", "pivot, hostops, interactive"} {
		if !strings.Contains(output.String(), part) {
			t.Fatalf("missing %q in %s", part, output.String())
		}
	}
}

func TestDuplicateModeArgumentIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func([]string) error
	}{
		{"server", serve}, {"client", clientCommand}, {"agent", agent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run([]string{tc.name, "--tun"})
			if err == nil || !strings.Contains(err.Error(), "unexpected "+tc.name+" argument") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestClientRequiresRoutingMode(t *testing.T) {
	if err := clientCommand(nil); err == nil || !strings.Contains(err.Error(), "--vpn or --internal") {
		t.Fatalf("missing mode error: %v", err)
	}
}

func TestClientStopDoesNotRequireRoutingMode(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-client.pid")
	err := clientCommand([]string{"--stop", "--pid-file", missing})
	if err == nil || strings.Contains(err.Error(), "--vpn or --internal") {
		t.Fatalf("stop should reach lifecycle handler without a routing mode: %v", err)
	}
}

func TestVerificationTarget(t *testing.T) {
	got, err := resolveVerificationTarget(context.Background(), "https://127.0.0.1/check")
	if err != nil || got != "127.0.0.1:443" {
		t.Fatalf("target=%q err=%v", got, err)
	}
	if _, err := resolveVerificationTarget(context.Background(), "file:///tmp/address"); err == nil {
		t.Fatal("accepted non-HTTP URL")
	}
}
