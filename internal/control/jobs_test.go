package control

import (
	"context"
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
	module, err := os.ReadFile(filepath.Join("..", "..", "examples", "wasm", "triage", "triage.wasm"))
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
}
