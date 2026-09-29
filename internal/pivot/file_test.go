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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	var uploadProgress []TransferProgress
	result, err := TransferFileProgress(ctx, server, "agent-id", "upload", source, remote, func(p TransferProgress) { uploadProgress = append(uploadProgress, p) })
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
	if len(uploadProgress) < 2 || uploadProgress[0].Bytes != 0 || uploadProgress[len(uploadProgress)-1].Bytes != int64(len(content)) || len(uploadProgress) > 30 {
		t.Fatalf("upload progress=%+v", uploadProgress)
	}
	if _, err := TransferFile(ctx, server, "agent-id", "upload", source, remote); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("upload overwrote existing file: %v", err)
	}
	var downloadProgress []TransferProgress
	result, err = TransferFileProgress(ctx, server, "agent-id", "download", download, remote, func(p TransferProgress) { downloadProgress = append(downloadProgress, p) })
	if err != nil {
		t.Fatal(err)
	}
	actual, err = os.ReadFile(download)
	if err != nil || !bytes.Equal(actual, content) || result.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("download content mismatch: %v, %+v", err, result)
	}
	if len(downloadProgress) < 2 || downloadProgress[0].Bytes != 0 || downloadProgress[len(downloadProgress)-1].Bytes != int64(len(content)) || len(downloadProgress) > 30 {
		t.Fatalf("download progress=%+v", downloadProgress)
	}
	if _, err := TransferFile(ctx, server, "agent-id", "download", download, remote); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("download overwrote existing file: %v", err)
	}
}

func TestCancelledTransferLeavesNoDestination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, agent := execTestMuxPair(ctx)
	defer server.Close()
	defer agent.Close()
	go ServeAgentWithCapabilities(ctx, agent, DefaultCapabilities())
	dir := t.TempDir()
	source, remote := filepath.Join(dir, "source.bin"), filepath.Join(dir, "remote.bin")
	if err := os.WriteFile(source, bytes.Repeat([]byte("x"), 64<<10), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := TransferFileProgress(ctx, server, "agent-id", "upload", source, remote, func(p TransferProgress) {
		if p.Bytes == 0 {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("cancelled transfer completed")
	}
	if _, err := os.Stat(remote); !os.IsNotExist(err) {
		t.Fatalf("cancelled upload created destination: %v", err)
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
