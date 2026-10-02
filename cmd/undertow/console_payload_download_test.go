package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"undertow/internal/agentprofile"
)

func TestPayloadDownloadPathsAndHash(t *testing.T) {
	t.Chdir(t.TempDir())
	payload := bytes.Repeat([]byte("artifact bytes"), 22000)
	sum := sha256.Sum256(payload)
	id := strings.Repeat("a", 24)
	a := artifactInfo{Artifact: agentprofile.Artifact{ID: id, Filename: "worker.exe", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}}
	call := func(_ context.Context, method, path string, _ any) ([]byte, error) {
		if method != http.MethodGet {
			return nil, errors.New("unexpected method")
		}
		if path == "/v1/agent-artifacts" {
			return json.Marshal([]artifactInfo{a})
		}
		prefix := "/v1/agent-artifacts/" + id + "/download/chunk?offset="
		if !strings.HasPrefix(path, prefix) {
			return nil, fmt.Errorf("unexpected path %s", path)
		}
		offset, err := strconv.Atoi(strings.TrimPrefix(path, prefix))
		if err != nil {
			return nil, err
		}
		end := min(offset+payloadDownloadChunkSize, len(payload))
		return json.Marshal(payloadDownloadChunk{Offset: int64(offset), Total: int64(len(payload)), Data: payload[offset:end]})
	}
	for _, tc := range []struct {
		name, output, expected string
	}{
		{"default", "", filepath.Join("payloads", "worker.exe")},
		{"explicit file", filepath.Join("staging", "copy.exe"), filepath.Join("staging", "copy.exe")},
		{"existing directory", "staging", filepath.Join("staging", "worker.exe")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			args := []string{"payload", "download", id}
			if tc.output != "" {
				args = append(args, tc.output)
			}
			if err := runConsolePayload(context.Background(), &out, call, args); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(tc.expected)
			if err != nil || !bytes.Equal(got, payload) || !strings.Contains(out.String(), "SHA-256 "+a.SHA256) {
				t.Fatalf("download result: %v, bytes=%d, output=%s", err, len(got), out.String())
			}
		})
	}
	if err := runConsolePayload(context.Background(), io.Discard, call, []string{"payload", "download", id}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing payload was replaced: %v", err)
	}
	a.SHA256 = strings.Repeat("0", 64)
	bad := filepath.Join("staging", "bad.exe")
	if err := runConsolePayload(context.Background(), io.Discard, call, []string{"payload", "download", id, bad}); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("bad hash was accepted: %v", err)
	}
	if _, err := os.Stat(bad); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad hash produced a payload file: %v", err)
	}
}
