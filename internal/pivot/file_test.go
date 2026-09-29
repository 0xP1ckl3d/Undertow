package pivot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileTransferBothDirectionsAndNoOverwrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithExec(ctx, agent, true)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.bin")
	remote := filepath.Join(dir, "remote.bin")
	download := filepath.Join(dir, "download.bin")
	content := bytes.Repeat([]byte{0, 1, 2, 3, 255}, 64*1024)
	if err := os.WriteFile(source, content, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := TransferFile(ctx, server, "agent-id", "upload", source, remote)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(remote)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("upload content mismatch: %v", err)
	}
	hash := sha256.Sum256(content)
	if result.Size != int64(len(content)) || result.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("upload result: %+v", result)
	}
	if _, err := TransferFile(ctx, server, "agent-id", "upload", source, remote); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("upload overwrote existing file: %v", err)
	}
	result, err = TransferFile(ctx, server, "agent-id", "download", download, remote)
	if err != nil {
		t.Fatal(err)
	}
	actual, err = os.ReadFile(download)
	if err != nil || !bytes.Equal(actual, content) || result.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("download content mismatch: %v, %+v", err, result)
	}
	if _, err := TransferFile(ctx, server, "agent-id", "download", download, remote); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("download overwrote existing file: %v", err)
	}
}

func TestFileTransferDisabledWithExec(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithExec(ctx, agent, false)
	_, err := TransferFile(ctx, server, "agent-id", "download", filepath.Join(t.TempDir(), "local"), "remote")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected denial: %v", err)
	}
}
