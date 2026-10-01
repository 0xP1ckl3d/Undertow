//go:build windows

package pivot

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWindowsConPTYEchoEditAndResize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	process, terminal, resize, err := startInteractiveProcess(ctx, InteractiveRequest{Argv: []string{"cmd.exe"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = terminal.Close()
	})
	output := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				output <- string(buf[:n])
			}
			if err != nil {
				close(output)
				return
			}
		}
	}()
	waitFor := func(want string) string {
		t.Helper()
		var seen strings.Builder
		for !strings.Contains(seen.String(), want) {
			select {
			case chunk, ok := <-output:
				if !ok {
					t.Fatalf("ConPTY closed before %q; output=%q", want, seen.String())
				}
				seen.WriteString(chunk)
			case <-ctx.Done():
				t.Fatalf("ConPTY timeout waiting for %q; output=%q", want, seen.String())
			}
		}
		return seen.String()
	}
	waitFor(">")
	if _, err := terminal.Write([]byte("echo heX")); err != nil {
		t.Fatal(err)
	}
	if seen := waitFor("echo heX"); !strings.Contains(seen, "echo heX") {
		t.Fatalf("input was not echoed before Enter: %q", seen)
	}
	if _, err := terminal.Write([]byte("\x7fllo\r")); err != nil {
		t.Fatal(err)
	}
	if seen := waitFor("\r\nhello"); !strings.Contains(seen, "\r\nhello") {
		t.Fatalf("edited command result missing: %q", seen)
	}
	if err := resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("echo resized\r")); err != nil {
		t.Fatal(err)
	}
	if seen := waitFor("\r\nresized"); !strings.Contains(seen, "\r\nresized") {
		t.Fatalf("output after resize missing: %q", seen)
	}
	cancel()
	exited := make(chan struct{})
	go func() { _ = process.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("shell remained alive after session cancellation")
	}
}

func TestWindowsPowerShellConPTY(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	process, terminal, resize, err := startInteractiveProcess(ctx, InteractiveRequest{
		Argv: []string{"powershell.exe", "-NoProfile", "-NoLogo"}, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = terminal.Close()
	})
	output := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				output <- string(buf[:n])
			}
			if err != nil {
				close(output)
				return
			}
		}
	}()
	waitFor := func(want string) string {
		t.Helper()
		var seen strings.Builder
		for !strings.Contains(seen.String(), want) {
			select {
			case chunk, ok := <-output:
				if !ok {
					t.Fatalf("PowerShell closed before %q; output=%q", want, seen.String())
				}
				seen.WriteString(chunk)
			case <-ctx.Done():
				t.Fatalf("PowerShell timeout waiting for %q; output=%q", want, seen.String())
			}
		}
		return seen.String()
	}
	waitFor("PS ")
	if _, err := terminal.Write([]byte("Write-Output hello")); err != nil {
		t.Fatal(err)
	}
	waitFor("Write-Output ")
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	waitFor("hello")
	if err := resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("PowerShell did not exit")
	}
}

func TestWindowsConPTYCtrlC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	process, terminal, _, err := startInteractiveProcess(ctx, InteractiveRequest{Argv: []string{"cmd.exe"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = terminal.Close()
	})
	output := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				output <- string(buf[:n])
			}
			if err != nil {
				close(output)
				return
			}
		}
	}()
	waitFor := func(want string) {
		t.Helper()
		var seen strings.Builder
		for !strings.Contains(seen.String(), want) {
			select {
			case chunk, ok := <-output:
				if !ok {
					t.Fatalf("ConPTY closed before %q; output=%q", want, seen.String())
				}
				seen.WriteString(chunk)
			case <-ctx.Done():
				t.Fatalf("ConPTY timeout waiting for %q; output=%q", want, seen.String())
			}
		}
	}
	waitFor(">")
	if _, err := terminal.Write([]byte("ping 127.0.0.1 -t\r")); err != nil {
		t.Fatal(err)
	}
	waitFor("Pinging 127.0.0.1")
	if _, err := terminal.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	waitFor(">")
	if _, err := terminal.Write([]byte("echo after-interrupt\r")); err != nil {
		t.Fatal(err)
	}
	waitFor("\r\nafter-interrupt")
	if _, err := terminal.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Logf("cmd retained Ctrl-C exit status: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("shell did not exit after Ctrl-C")
	}
}
