package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"undertow/internal/control"
)

func TestLargeJobOutputOffersDownloadAndSaveCopiesAllChunks(t *testing.T) {
	want := []byte(strings.Repeat("finding line\n", 30_000))
	info := control.JobInfo{ID: "abc123", AgentID: "agent-a", State: "completed", OutputBytes: uint64(len(want)), OutputFile: "server/jobs-output/agent-a/abc123.out", Output: string(want[len(want)-(256<<10):]), OutputTruncated: true}
	caller := func(_ context.Context, method, path string, _ any) ([]byte, error) {
		if method != http.MethodGet {
			return nil, fmt.Errorf("unexpected method %s", method)
		}
		switch path {
		case "/v1/jobs/abc123", "/v1/jobs/abc123/output":
			return json.Marshal(info)
		}
		if prefix := "/v1/jobs/abc123/output/chunk?offset="; strings.HasPrefix(path, prefix) {
			offset, err := strconv.ParseUint(strings.TrimPrefix(path, prefix), 10, 64)
			if err != nil || offset > uint64(len(want)) {
				return nil, fmt.Errorf("invalid offset %d", offset)
			}
			end := offset + 256<<10
			if end > uint64(len(want)) {
				end = uint64(len(want))
			}
			return json.Marshal(control.JobOutputChunk{Offset: offset, Total: uint64(len(want)), Data: want[offset:end], EOF: end == uint64(len(want))})
		}
		return nil, fmt.Errorf("unexpected request %s", path)
	}
	var output bytes.Buffer
	asked := false
	if err := runConsoleJobCommand(context.Background(), &output, caller, []string{"job", "output", info.ID}, "", nil, func(question string) bool {
		asked = strings.Contains(question, "too large for the console")
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if !asked || !strings.Contains(output.String(), "job save") || strings.Contains(output.String(), "finding line") {
		t.Fatalf("large output prompt=%q", output.String())
	}
	destination := filepath.Join(t.TempDir(), "download.out")
	output.Reset()
	if err := runConsoleJobCommand(context.Background(), &output, caller, []string{"job", "save", info.ID, destination}, "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("download size=%d error=%v", len(got), err)
	}
	if !strings.Contains(output.String(), destination) {
		t.Fatalf("save confirmation=%q", output.String())
	}
}

func TestPublishJobDownloadDoesNotReplaceExistingFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "download.partial")
	destination := filepath.Join(dir, "download.out")
	if err := os.WriteFile(source, []byte("new output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("previous output"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishJobDownload(source, destination); err == nil {
		t.Fatal("replaced an existing download")
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "previous output" {
		t.Fatalf("existing output changed: %q, %v", contents, err)
	}
}
