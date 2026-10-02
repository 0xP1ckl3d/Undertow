package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInteractiveLinuxLaunchSurvivesTerminal(t *testing.T) {
	const stage = "_DETACH_TEST_STAGE"
	if os.Getenv(stage) == "launcher" {
		interactive := hasTerminal()
		launched, err := detachIfInteractive()
		if err != nil {
			t.Fatal(err)
		}
		if !interactive {
			if launched {
				t.Fatal("detached child launched another copy")
			}
			time.Sleep(300 * time.Millisecond)
			if err := os.WriteFile(os.Getenv("_DETACH_TEST_MARKER"), []byte("survived"), 0600); err != nil {
				t.Fatal(err)
			}
			os.Exit(0)
		}
		if !launched {
			t.Fatal("interactive launcher did not detach")
		}
		return
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script utility is unavailable")
	}
	marker := filepath.Join(t.TempDir(), "child-marker")
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "'" + strings.ReplaceAll(path, "'", "'\\''") + "' -test.run=TestInteractiveLinuxLaunchSurvivesTerminal"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "script", "-q", "-c", command, os.DevNull)
	cmd.Env = append(os.Environ(), stage+"=launcher", "_DETACH_TEST_MARKER="+marker)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal launcher: %v: %s", err, output)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "survived" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("detached child did not survive terminal exit")
}
