package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/session"
	"undertow/internal/transport/dns"
)

func TestJobHelperProcess(t *testing.T) {
	if os.Getenv("UNDERTOW_JOB_HELPER") != "1" {
		return
	}
	fmt.Print("job started\n")
	if os.Getenv("UNDERTOW_JOB_MODE") == "wait" {
		time.Sleep(30 * time.Second)
	}
	if os.Getenv("UNDERTOW_JOB_MODE") == "large" {
		fmt.Print(strings.Repeat("x", 300<<10))
	}
	fmt.Print("job finished\n")
	os.Exit(0)
}

func TestScriptJobUsesExistingLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	server, agent := forwardAuditAgent(t, ctx, manager, "script-agent", 801, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	language, source := "bash", []byte("echo script-job-output\n")
	if runtime.GOOS == "windows" {
		language, source = "powershell", []byte("[Console]::Out.WriteLine('script-job-output')\n")
	}
	job, err := manager.StartScriptJob(ctx, 802, "script-agent", language, source)
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "script" || job.Language != language || len(job.Argv) != 0 {
		t.Fatalf("script job metadata=%+v", job)
	}
	finished := waitJob(t, manager, 802, job.ID, func(j JobInfo) bool { return j.State == "completed" })
	if finished.ExitCode == nil || *finished.ExitCode != 0 || !strings.Contains(finished.Output, "script-job-output") {
		t.Fatalf("script job result=%+v", finished)
	}
	if _, err := manager.Job(803, job.ID, true); err == nil {
		t.Fatal("script job crossed client ownership")
	}
}

func TestWASMJobExitAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	server, agent := forwardAuditAgent(t, ctx, manager, "wasm-agent", 811, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	exitModule, err := os.ReadFile(filepath.Join("..", "pivot", "testdata", "wasm_exit2.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	exited, err := manager.StartWASMJob(ctx, 812, "wasm-agent", exitModule, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, 812, exited.ID, func(j JobInfo) bool { return j.State == "failed" })
	if failed.ExitCode == nil || *failed.ExitCode != 2 || failed.Kind != "wasm" {
		t.Fatalf("WASM exit job=%+v", failed)
	}
	loopModule, err := os.ReadFile(filepath.Join("..", "pivot", "testdata", "wasm_loop.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	running, err := manager.StartWASMJob(ctx, 812, "wasm-agent", loopModule, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if manager.AgentList()[0].ActiveJobs != 1 {
		t.Fatal("running WASM job not counted")
	}
	if err := manager.CancelJob(812, running.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitJob(t, manager, 812, running.ID, func(j JobInfo) bool { return j.State == "cancelled" })
	if cancelled.Ended == nil || manager.AgentList()[0].ActiveJobs != 0 {
		t.Fatalf("WASM job cancellation=%+v", cancelled)
	}
}

func TestPackagedWASMBackgroundJobOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	server, agent := forwardAuditAgent(t, ctx, manager, "example-agent", 821, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	module, err := os.ReadFile(filepath.Join("..", "..", "modules", "wasm", "triage", "triage.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.StartWASMJob(ctx, 822, "example-agent", module, []string{"audit"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, manager, 822, job.ID, func(j JobInfo) bool { return j.State == "completed" })
	if finished.Kind != "wasm" || finished.ExitCode == nil || *finished.ExitCode != 0 || !strings.Contains(finished.Output, "Host triage:") {
		t.Fatalf("job output=%+v", finished)
	}
}

func waitJob(t *testing.T, manager *Manager, owner uint64, id string, predicate func(JobInfo) bool) JobInfo {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		job, err := manager.Job(owner, id, true)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(job) {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	job, _ := manager.Job(owner, id, true)
	t.Fatalf("job did not reach expected state: %+v", job)
	return JobInfo{}
}

func TestJobLifecycleOutputAndOwnership(t *testing.T) {
	t.Setenv("UNDERTOW_JOB_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	outputRoot := t.TempDir()
	if err := manager.ConfigureJobOutput(outputRoot, 1<<20, 2<<20); err != nil {
		t.Fatal(err)
	}
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	serverAgent := mux.New(ctx, &remoteTestTransport{in: a, out: b, done: make(chan struct{})}, true)
	agent := mux.New(ctx, &remoteTestTransport{in: b, out: a, done: make(chan struct{})}, false)
	defer serverAgent.Close()
	defer agent.Close()
	go pivot.ServeAgent(ctx, agent)
	var keys security.Keys
	transport, err := session.New(704, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	manager.Register(&dns.Peer{Session: transport, AgentID: "agent-a", Connected: time.Now()}, serverAgent)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{exe, "-test.run=TestJobHelperProcess"}
	t.Setenv("UNDERTOW_JOB_MODE", "wait")
	running, err := manager.StartJob(ctx, 77, "agent-a", argv)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, manager, 77, running.ID, func(j JobInfo) bool { return j.State == "running" && strings.Contains(j.Output, "job started") })
	if _, err := manager.Job(88, running.ID, true); err == nil {
		t.Fatal("other client read job")
	}
	if err := manager.CancelJob(88, running.ID); err == nil {
		t.Fatal("other client cancelled job")
	}
	if len(manager.Jobs(88, "")) != 0 || manager.AgentList()[0].ActiveJobs != 1 {
		t.Fatal("job ownership or status count wrong")
	}
	if err := manager.CancelJob(77, running.ID); err != nil {
		t.Fatal(err)
	}
	waitJob(t, manager, 77, running.ID, func(j JobInfo) bool { return j.State == "cancelled" && j.Ended != nil })
	t.Setenv("UNDERTOW_JOB_MODE", "finish")
	completed, err := manager.StartJob(ctx, 77, "agent-a", argv)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, manager, 77, completed.ID, func(j JobInfo) bool {
		return j.State == "completed" && j.ExitCode != nil && *j.ExitCode == 0 && strings.Contains(j.Output, "job finished")
	})
	if manager.AgentList()[0].ActiveJobs != 0 {
		t.Fatal("completed job counted active")
	}
	t.Setenv("UNDERTOW_JOB_MODE", "large")
	large, err := manager.StartJob(ctx, 77, "agent-a", argv)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, manager, 77, large.ID, func(j JobInfo) bool { return j.State == "completed" })
	retained, err := manager.Job(77, large.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained.Output) > jobOutputLimit || !retained.OutputTruncated || retained.OutputBytes <= jobOutputLimit {
		t.Fatalf("unbounded output: size=%d total=%d truncated=%t", len(retained.Output), retained.OutputBytes, retained.OutputTruncated)
	}
	if retained.OutputFile != filepath.Join(outputRoot, "agent-a", large.ID+".out") {
		t.Fatalf("output file=%q", retained.OutputFile)
	}
	contents, err := os.ReadFile(retained.OutputFile)
	if err != nil || uint64(len(contents)) != retained.OutputBytes || !bytes.Contains(contents, []byte("job started")) || !bytes.Contains(contents, []byte("job finished")) {
		t.Fatalf("spilled output size=%d read error=%v", len(contents), err)
	}
	chunk, err := manager.JobChunk(77, large.ID, 0)
	if err != nil || len(chunk.Data) != jobOutputChunkSize || !bytes.Contains(chunk.Data, []byte("job started")) {
		t.Fatalf("first chunk size=%d error=%v", len(chunk.Data), err)
	}
	if _, err := manager.JobChunk(88, large.ID, 0); err == nil {
		t.Fatal("other client downloaded job output")
	}
}

func TestConnectedOperatorsShareServerJobHistory(t *testing.T) {
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	m.jobs["shared"] = &jobState{info: JobInfo{ID: "shared", AgentID: "agent-a", State: "completed"}, owner: 1}
	m.clients[2] = &clientState{}
	if jobs := m.Jobs(2, "agent-a"); len(jobs) != 1 || jobs[0].ID != "shared" {
		t.Fatalf("second operator cannot see team job: %+v", jobs)
	}
	if _, err := m.Job(2, "shared", true); err != nil {
		t.Fatalf("second operator cannot read team job: %v", err)
	}
	if _, err := m.Job(3, "shared", false); err == nil {
		t.Fatal("disconnected client read team job")
	}
	if err := m.DeleteJob(2, "shared"); err != nil {
		t.Fatalf("second operator cannot manage team job: %v", err)
	}
}

func TestJobOutputIsDurableFromFirstChunkAndStopsAtQuota(t *testing.T) {
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	root := t.TempDir()
	if err := m.ConfigureJobOutput(root, 300<<10, 400<<10); err != nil {
		t.Fatal(err)
	}
	job := &jobState{info: JobInfo{ID: "abc123", AgentID: "agent-a"}}
	m.jobs[job.info.ID] = job
	first := []byte(strings.Repeat("a", 200<<10))
	if err := m.appendJobOutput(job, first); err != nil || job.outputFile == nil {
		t.Fatalf("small output was not retained on disk: %v", err)
	}
	chunk, err := m.JobChunk(0, job.info.ID, 0)
	if err != nil || len(chunk.Data) != len(first) {
		t.Fatalf("retained chunk size=%d error=%v", len(chunk.Data), err)
	}
	if err := m.appendJobOutput(job, []byte(strings.Repeat("b", 80<<10))); err != nil {
		t.Fatal(err)
	}
	if job.outputFile == nil || job.info.OutputFile == "" {
		t.Fatal("large output did not spill")
	}
	if err := m.appendJobOutput(job, []byte(strings.Repeat("c", 30<<10))); err == nil || !strings.Contains(err.Error(), "per job") {
		t.Fatalf("quota error=%v", err)
	}
	if job.info.OutputBytes != 280<<10 {
		t.Fatalf("quota wrote extra output: %d", job.info.OutputBytes)
	}
	if err := job.outputFile.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(job.info.OutputFile)
	if err != nil || len(contents) != 280<<10 || string(contents[:len(first)]) != string(first) {
		t.Fatalf("spilled content size=%d error=%v", len(contents), err)
	}
	second := &jobState{info: JobInfo{ID: "def456", AgentID: "agent-a"}}
	if err := m.appendJobOutput(second, first); err == nil || !strings.Contains(err.Error(), "storage limit") {
		t.Fatalf("total quota error=%v", err)
	}
	if second.outputFile != nil {
		t.Fatal("total quota created another output file")
	}
	if err := m.DeleteJob(0, job.info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(job.info.OutputFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted output still exists: %v", err)
	}
	if err := m.appendJobOutput(second, first); err != nil {
		t.Fatalf("quota was not released: %v", err)
	}
	if err := m.appendJobOutput(second, []byte(strings.Repeat("b", 80<<10))); err != nil {
		t.Fatalf("quota was not released: %v", err)
	}
	if err := second.outputFile.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestJobOutputAvailableToConnectedOperatorsAfterReconnect(t *testing.T) {
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	var keys security.Keys
	same, err := session.New(2, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := session.New(3, keys, false)
	if err != nil {
		t.Fatal(err)
	}
	m.clients[2] = &clientState{peer: &dns.Peer{Session: same, AgentID: "same-key"}}
	m.clients[3] = &clientState{peer: &dns.Peer{Session: other, AgentID: "other-key"}}
	m.jobs["saved"] = &jobState{owner: 1, ownerKey: "same-key", output: []byte("result"), info: JobInfo{ID: "saved", OutputBytes: 6}}
	if _, err := m.Job(2, "saved", true); err != nil {
		t.Fatalf("same key could not read job: %v", err)
	}
	chunk, err := m.JobChunk(2, "saved", 0)
	if err != nil || string(chunk.Data) != "result" {
		t.Fatalf("same key could not download job: %q, %v", chunk.Data, err)
	}
	if _, err := m.Job(3, "saved", true); err != nil {
		t.Fatalf("second connected operator could not read team job: %v", err)
	}
	if _, err := m.Job(4, "saved", true); err == nil {
		t.Fatal("disconnected client read job")
	}
}
