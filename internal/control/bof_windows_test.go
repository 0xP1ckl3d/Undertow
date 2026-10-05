//go:build windows && amd64

package control

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/bof"
	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
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

func bofFixture(t *testing.T, name string) []byte {
	t.Helper()
	object, err := os.ReadFile(filepath.Join("..", "bof", "testdata", "examples", name+".o"))
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func TestBOFOperatorServerAgentAndJobs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := manager.ConfigureJobOutput(t.TempDir(), 2<<20, 4<<20); err != nil {
		t.Fatal(err)
	}
	serverAgent, agent := forwardAuditAgent(t, ctx, manager, "bof-agent", 981, pivot.DefaultCapabilities())
	defer serverAgent.Close()
	defer agent.Close()
	serverVPN, clientVPN := forwardAuditPair(ctx)
	defer serverVPN.Close()
	defer clientVPN.Close()
	go pivot.ServeVPNInteractive(ctx, serverVPN, manager.ResolveEgress, func() bool { return false }, func(ctx context.Context, stream *mux.Stream) { manager.ServeInteractiveRelay(ctx, stream) })
	packed, err := bof.EncodeArguments("iszZb", []string{"123", "-7", "hello", "雪", "base64:AP8="}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := OpenClientBOF(ctx, clientVPN, "bof-agent", bofFixture(t, "arguments"), packed)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	var exit bool
	for !exit {
		kind, data, err := session.Read()
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case pivot.InteractiveOutput:
			output.Write(data)
		case pivot.InteractiveError:
			t.Fatalf("BOF error: %s", data)
		case pivot.InteractiveExit:
			if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 {
				t.Fatalf("exit=%v", data)
			}
			exit = true
		}
	}
	session.Close()
	if !strings.Contains(output.String(), "int=123 short=-7 ansi=hello wide0=96ea binary=2 remain=0") {
		t.Fatalf("BOF output=%q", output.String())
	}
	fileSession, err := OpenClientBOF(ctx, clientVPN, "bof-agent", bofFixture(t, "file"), mustEmptyBOFArgs(t))
	if err != nil {
		t.Fatal(err)
	}
	files := bof.NewFileCollector(t.TempDir())
	defer files.Close()
	var fileOutput strings.Builder
	var received []bof.FileArtifact
	for {
		kind, data, err := fileSession.Read()
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case pivot.InteractiveOutput:
			fileOutput.Write(data)
		case pivot.InteractiveBOFCallback:
			complete, err := files.Consume(data)
			if err != nil {
				t.Fatal(err)
			}
			received = append(received, complete...)
		case pivot.InteractiveError:
			t.Fatalf("file BOF: %s", data)
		case pivot.InteractiveExit:
			goto fileDone
		}
	}
fileDone:
	fileSession.Close()
	if len(received) != 1 || received[0].Size != 100000 || !strings.Contains(fileOutput.String(), "file transfer complete") {
		t.Fatalf("file callbacks=%+v output=%q", received, fileOutput.String())
	}
	contents, err := os.ReadFile(received[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range contents {
		if b != byte(i) {
			t.Fatalf("file byte %d=%d", i, b)
		}
	}
	remoteServer, remoteClient := forwardAuditClient(t, ctx, manager, 982)
	defer remoteServer.Close()
	defer remoteClient.Close()
	empty, _ := bof.EncodeArguments("", nil, nil)
	result, err := CallRemote(ctx, remoteClient, "POST", "/v1/agents/bof-agent/bof/jobs", map[string]any{"source": bofFixture(t, "imports"), "arguments": empty})
	if err != nil {
		t.Fatal(err)
	}
	var job JobInfo
	if err := json.Unmarshal(result, &job); err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, manager, 982, job.ID, func(j JobInfo) bool { return j.State == "completed" || j.State == "failed" })
	if finished.State != "completed" || finished.Kind != "bof" || !strings.Contains(finished.Output, "imports pid=") {
		t.Fatalf("BOF job=%+v", finished)
	}
	fileJob, err := manager.StartBOFJob(ctx, 982, "bof-agent", bofFixture(t, "file"), mustEmptyBOFArgs(t))
	if err != nil {
		t.Fatal(err)
	}
	fileFinished := waitJob(t, manager, 982, fileJob.ID, func(j JobInfo) bool { return j.State == "completed" || j.State == "failed" })
	if fileFinished.State != "completed" || len(fileFinished.Files) != 1 || fileFinished.Files[0].Size != 100000 {
		t.Fatalf("file job=%+v", fileFinished)
	}
	chunk, err := manager.JobFileChunk(982, fileJob.ID, 7, 0)
	if err != nil || len(chunk.Data) != 100000 || !bytes.Equal(chunk.Data[:4], []byte{0, 1, 2, 3}) {
		t.Fatalf("file chunk=%d err=%v", len(chunk.Data), err)
	}
	remoteChunk, err := CallRemote(ctx, remoteClient, "GET", fmt.Sprintf("/v1/jobs/%s/files/7/chunk?offset=0", fileJob.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	var fetched JobOutputChunk
	if err := json.Unmarshal(remoteChunk, &fetched); err != nil || !bytes.Equal(fetched.Data, chunk.Data) {
		t.Fatalf("remote file chunk=%d err=%v", len(fetched.Data), err)
	}
	loop, err := manager.StartBOFJob(ctx, 982, "bof-agent", bofFixture(t, "loop"), empty)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CancelJob(982, loop.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitJob(t, manager, 982, loop.ID, func(j JobInfo) bool { return j.State == "cancelled" })
	if cancelled.Ended == nil {
		t.Fatalf("BOF cancellation=%+v", cancelled)
	}
	disconnected, err := manager.StartBOFJob(ctx, 982, "bof-agent", bofFixture(t, "loop"), empty)
	if err != nil {
		t.Fatal(err)
	}
	_ = serverAgent.Close()
	_ = agent.Close()
	lost := waitJob(t, manager, 982, disconnected.ID, func(j JobInfo) bool { return j.State == "failed" })
	if lost.Ended == nil {
		t.Fatalf("BOF disconnect=%+v", lost)
	}
}

func mustEmptyBOFArgs(t *testing.T) []byte {
	t.Helper()
	args, err := bof.EncodeArguments("", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return args
}
