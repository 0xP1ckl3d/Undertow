//go:build windows && amd64

package pivot

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/nativemodule"
)

func TestPackagedShellPower(t *testing.T) {
	for _, mode := range []string{"oneshot", "live", "live-cancel"} {
		command := exec.Command(os.Args[0], "-test.run=^TestShellPowerHelperProcess$", "-test.v")
		command.Env = append(os.Environ(), "UNDERTOW_SHELLPOWER_TEST_HELPER="+mode)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("shellpower %s helper: %v\n%s", mode, err, output)
		}
	}
}

func TestShellPowerHelperProcess(t *testing.T) {
	mode := os.Getenv("UNDERTOW_SHELLPOWER_TEST_HELPER")
	if mode != "oneshot" && mode != "live" && mode != "live-cancel" {
		return
	}
	container, err := os.ReadFile(filepath.Join("..", "..", "modules", "native", "shellpower", "shellpower.module"))
	if err != nil {
		t.Fatal(err)
	}
	_, dll, err := nativemodule.Parse(container)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "oneshot" {
		for _, test := range []struct {
			args []string
			data []byte
			want string
		}{
			{args: []string{"Write-Output 'undertow-inline-ok'"}, want: "undertow-inline-ok"},
			{data: []byte("Write-Output 'undertow-script-ok'"), want: "undertow-script-ok"},
		} {
			encoded, err := nativemodule.EncodeArgs(test.args, test.data)
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			code, err := executeNative(context.Background(), dll, encoded, func(_ byte, data []byte) error {
				output.Write(data)
				return nil
			})
			if err != nil || code != 0 {
				t.Fatalf("code=%d err=%v output=%s", code, err, output.String())
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("output=%s", output.String())
			}
		}
		return
	}
	if mode == "live-cancel" {
		ctx, cancel := context.WithCancel(context.Background())
		input := make(chan []byte, 1)
		input <- []byte("Start-Sleep -Seconds 30\r")
		started := time.Now()
		time.AfterFunc(500*time.Millisecond, cancel)
		code, err := executeNativeShell(ctx, dll, input, func(_ byte, _ []byte) error { return nil })
		if err != nil || code != 0 {
			t.Fatalf("cancelled native live shell: code=%d err=%v", code, err)
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("cancelled native live shell took %s", elapsed)
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithCapabilities(ctx, agent, DefaultCapabilities())
	for attempt := 0; attempt < 2; attempt++ {
		session, err := OpenNativeShell(ctx, server, container)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.Send([]byte("$undertowValue = 41\rWrite-Output ($undertowValue + 1)\rexit\r")); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
	readSession:
		for {
			kind, data, err := session.Read()
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case InteractiveOutput, InteractiveStderr:
				output.Write(data)
			case InteractiveError:
				t.Fatalf("native live shell error: %s; output=%s", data, output.String())
			case InteractiveExit:
				if len(data) != 4 || int32(binary.BigEndian.Uint32(data)) != 0 {
					t.Fatalf("native live shell exit=%x output=%s", data, output.String())
				}
				if !strings.Contains(output.String(), "Windows ShellPower for Undertow") || !strings.Contains(output.String(), "42") {
					t.Fatalf("native live shell output=%s", output.String())
				}
				break readSession
			}
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
