package pivot

import (
	"context"
	"encoding/binary"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMemoryScriptStreamsAndCapabilities(t *testing.T) {
	language := "bash"
	source := []byte("printf 'script-stdout\\n'; printf 'script-stderr\\n' >&2\n")
	if runtime.GOOS == "windows" {
		language = "powershell"
		source = []byte("[Console]::Out.WriteLine('script-stdout')\n[Console]::Error.WriteLine('script-stderr')\n")
	}
	name, _, err := scriptExecutable(language)
	if err != nil {
		t.Skip(err)
	}
	if _, err := exec.LookPath(name); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	caps := DefaultCapabilities()
	caps.Exec, caps.Interactive = false, false
	go ServeAgentWithCapabilities(ctx, agent, caps)
	session, err := OpenScript(ctx, server, language, source)
	if err != nil {
		t.Fatal(err)
	}
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
			t.Fatalf("agent error: %s", data)
		case InteractiveExit:
			if len(data) != 4 || binary.BigEndian.Uint32(data) != 0 {
				t.Fatalf("exit=%v", data)
			}
			if !strings.Contains(stdout.String(), "script-stdout") || !strings.Contains(stderr.String(), "script-stderr") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			caps.Scripts = false
			deniedServer, deniedAgent := execTestMuxPair(ctx)
			defer deniedServer.Close()
			defer deniedAgent.Close()
			go ServeAgentWithCapabilities(ctx, deniedAgent, caps)
			if _, err := OpenScript(ctx, deniedServer, language, source); err == nil || !strings.Contains(err.Error(), "scripts are disabled") {
				t.Fatalf("denial=%v", err)
			}
			return
		}
	}
}
