//go:build windows && amd64

package control

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/pivot"
	"undertow/internal/routing"
)

func assemblyExample(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	input, output := filepath.Join(dir, "example.cs"), filepath.Join(dir, "example.exe")
	source := `using System; using System.Threading; class Program { static int Main(string[] args) { if (args.Length>0 && args[0]=="wait") Thread.Sleep(30000); Console.WriteLine("managed="+String.Join("|",args)); Console.WriteLine("windows="+Environment.OSVersion.Platform); return 0; } }`
	if err := os.WriteFile(input, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	compiler := filepath.Join(os.Getenv("SystemRoot"), "Microsoft.NET", "Framework64", "v4.0.30319", "csc.exe")
	if _, err := os.Stat(compiler); err != nil {
		t.Skip(".NET Framework compiler unavailable")
	}
	if message, err := exec.Command(compiler, "/nologo", "/target:exe", "/out:"+output, input).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, message)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAssemblyOperatorServerAgentAndJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	server, agent := forwardAuditAgent(t, ctx, manager, "assembly-agent", 951, pivot.DefaultCapabilities())
	defer server.Close()
	defer agent.Close()
	source := assemblyExample(t)
	session, err := pivot.OpenAssembly(ctx, server, source, []string{"one", "two words"})
	if err != nil {
		t.Fatal(err)
	}
	code, output := nativeResult(t, session)
	if code != 0 || !strings.Contains(output, "managed=one|two words") || !strings.Contains(output, "windows=Win32NT") {
		t.Fatalf("foreground code=%d output=%q", code, output)
	}
	job, err := manager.StartAssemblyJob(ctx, 952, "assembly-agent", source, []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitJob(t, manager, 952, job.ID, func(j JobInfo) bool { return j.State == "completed" || j.State == "failed" })
	if finished.State != "completed" || finished.ExitCode == nil || *finished.ExitCode != 0 || !strings.Contains(finished.Output, "managed=job") {
		t.Fatalf("job=%+v", finished)
	}
	running, err := manager.StartAssemblyJob(ctx, 952, "assembly-agent", source, []string{"wait"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CancelJob(952, running.ID); err != nil {
		t.Fatal(err)
	}
	stopped := waitJob(t, manager, 952, running.ID, func(j JobInfo) bool { return j.State == "cancelled" })
	if stopped.Ended == nil {
		t.Fatalf("cancelled job=%+v", stopped)
	}
}
