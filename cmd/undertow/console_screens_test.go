package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/pivot"
)

func TestScreenshotCapturesAllScreensAndOneSelectedScreen(t *testing.T) {
	screens := []pivot.ScreenInfo{{Number: 1, Name: "DISPLAY1", Width: 1920, Height: 1080, Foreground: "Browser"}, {Number: 2, Name: "DISPLAY2", Width: 1280, Height: 1024, Foreground: "Editor"}}
	encoded, _ := json.Marshal(screens)
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		if method != http.MethodPost || path != "/v1/agents/agent-one/exec" || body.(pivot.ExecRequest).Builtin != "screens" {
			t.Fatalf("unexpected screen request: %s %s %+v", method, path, body)
		}
		return json.Marshal(pivot.ExecResult{Stdout: string(encoded)})
	}
	var requests []clientFileRequest
	transfer := func(_ context.Context, request clientFileRequest, _ func(pivot.TransferProgress)) (pivot.FileMessage, error) {
		requests = append(requests, request)
		return pivot.FileMessage{OK: true, Size: 5, SHA256: "digest"}, nil
	}
	outputDir := t.TempDir()
	var output bytes.Buffer
	if err := runConsoleScreenshot(context.Background(), &output, caller, transfer, "agent-one", []string{"--output", outputDir}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0].RemotePath != "1" || requests[1].RemotePath != "2" {
		t.Fatalf("captures: %+v", requests)
	}
	for _, request := range requests {
		if request.Operation != "screenshot" || request.AgentID != "agent-one" || filepath.Dir(request.LocalPath) != outputDir || filepath.Ext(request.LocalPath) != ".png" {
			t.Fatalf("capture path: %+v", request)
		}
	}
	requests = nil
	if err := runConsoleScreenshot(context.Background(), &output, caller, transfer, "agent-one", []string{"2", "--output", outputDir}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].RemotePath != "2" {
		t.Fatalf("selected capture: %+v", requests)
	}
	if err := runConsoleScreenshot(context.Background(), &output, caller, transfer, "agent-one", []string{"3", "--output", outputDir}); err == nil {
		t.Fatal("missing screen accepted")
	}
	output.Reset()
	if err := runConsoleScreens(context.Background(), &output, caller, "agent-one"); err != nil || !strings.Contains(output.String(), "DISPLAY2  1280x1024  Editor") {
		t.Fatalf("screen list: %q, %v", output.String(), err)
	}
}

func TestDefaultScreenshotOutputDirectoryIsIgnored(t *testing.T) {
	if content, err := os.ReadFile(filepath.Join("..", "..", ".gitignore")); err != nil || !strings.Contains(string(content), "outputs/") {
		t.Fatalf("output directory is not ignored: %v", err)
	}
}

func TestSelectedAgentBareScreenshotCapturesEveryScreen(t *testing.T) {
	t.Chdir(t.TempDir())
	screens, _ := json.Marshal([]pivot.ScreenInfo{{Number: 1, Name: "ONE"}, {Number: 2, Name: "TWO"}})
	caller := func(_ context.Context, method, path string, body any) ([]byte, error) {
		if path == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []map[string]string{{"id": "agent-one", "hostname": "desktop"}}})
		}
		if method == http.MethodPost && path == "/v1/agents/agent-one/exec" {
			return json.Marshal(pivot.ExecResult{Stdout: string(screens)})
		}
		t.Fatalf("unexpected request: %s %s %+v", method, path, body)
		return nil, nil
	}
	var requests []clientFileRequest
	transfer := func(_ context.Context, request clientFileRequest, _ func(pivot.TransferProgress)) (pivot.FileMessage, error) {
		requests = append(requests, request)
		return pivot.FileMessage{OK: true, Size: 10, SHA256: "digest"}, nil
	}
	var output bytes.Buffer
	if err := runConsole(context.Background(), strings.NewReader("use 1\nscreenshot\nquit\n"), &output, caller, func() uint64 { return 1 }, nil, nil, nil, consoleFeatures{transfer: transfer}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0].RemotePath != "1" || requests[1].RemotePath != "2" {
		t.Fatalf("bare screenshot requests: %+v; output=%s", requests, output.String())
	}
	for _, request := range requests {
		if filepath.Dir(request.LocalPath) != filepath.Join(mustWorkingDirectory(t), "outputs", "screenshots") {
			t.Fatalf("default path: %s", request.LocalPath)
		}
	}
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	path, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultDownloadPathAcceptsWindowsRemotePathOnLinuxClient(t *testing.T) {
	got, err := defaultDownloadPath("agent-one", `C:\Reports\result.txt`)
	want := filepath.Join("outputs", "downloads", "agent-one", "result.txt")
	if err != nil || got != want {
		t.Fatalf("default download path: %q %v; want %q", got, err, want)
	}
	if _, err := defaultDownloadPath("../other", "/tmp/result.txt"); err == nil {
		t.Fatal("path traversal agent ID accepted")
	}
}
