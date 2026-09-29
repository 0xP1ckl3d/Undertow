package pivot

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInteractiveHelperProcess(t *testing.T) {
	if os.Getenv("UNDERTOW_INTERACTIVE_HELPER") != "1" {
		return
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Printf("reply:%s", line)
	os.Exit(0)
}

func TestInteractiveSessionStreamsAndExits(t *testing.T) {
	t.Setenv("UNDERTOW_INTERACTIVE_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithCapabilities(ctx, agent, DefaultCapabilities())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session, err := OpenInteractive(ctx, server, InteractiveRequest{Argv: []string{exe, "-test.run=TestInteractiveHelperProcess"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if err := session.Send([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for {
		kind, data, err := session.Read()
		if err != nil {
			t.Fatal(err)
		}
		if kind == InteractiveOutput {
			output.Write(data)
		}
		if kind == InteractiveExit {
			if !strings.Contains(output.String(), "reply:ping") {
				t.Fatalf("output=%q", output.String())
			}
			break
		}
	}
}

func TestInteractiveCapabilityIndependentOfExec(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	caps := DefaultCapabilities()
	caps.Interactive = false
	go ServeAgentWithCapabilities(ctx, agent, caps)
	_, err := OpenInteractive(ctx, server, InteractiveRequest{})
	if err == nil || !strings.Contains(err.Error(), "interactive sessions are disabled") {
		t.Fatalf("denial=%v", err)
	}
	if !caps.Exec {
		t.Fatal("exec capability unexpectedly changed")
	}
}
