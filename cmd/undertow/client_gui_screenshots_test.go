//go:build linux || windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"undertow/internal/control"
)

func TestGUIScreenshotRetrievalVerifiesServerChunks(t *testing.T) {
	var imageBytes bytes.Buffer
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageBytes, imageData); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(imageBytes.Bytes())
	id := "0123456789abcdef0123456789abcdef"
	entry := control.ScreenshotInfo{ID: id, AgentID: "agent-a", Screen: 1, At: time.Now(), Size: int64(imageBytes.Len()), SHA256: hex.EncodeToString(sum[:])}
	metadata, _ := json.Marshal(entry)
	client := &liveClientConsole{request: func(_ context.Context, method, path string, _ any) ([]byte, error) {
		if method != http.MethodGet {
			t.Fatalf("method %s", method)
		}
		if path == "/v1/screenshots/"+id {
			return metadata, nil
		}
		if path == "/v1/screenshots/"+id+"/chunk?offset=0" {
			return imageBytes.Bytes(), nil
		}
		t.Fatalf("unexpected path %s", path)
		return nil, nil
	}}
	store, err := openClientGUIStore(filepath.Join(t.TempDir(), "ui.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	gui := &guiServer{client: client, store: store}
	request := httptest.NewRequest(http.MethodGet, "/api/screenshots/"+id+"/image?download=1", nil)
	request.SetPathValue("id", id)
	response := httptest.NewRecorder()
	gui.screenshotImage(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), imageBytes.Bytes()) {
		t.Fatalf("image status %d length %s", response.Code, strconv.Itoa(response.Body.Len()))
	}
	if !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("missing download disposition")
	}
	entry.SHA256 = strings.Repeat("0", 64)
	metadata, _ = json.Marshal(entry)
	response = httptest.NewRecorder()
	gui.screenshotImage(response, request)
	if response.Code != http.StatusBadGateway || bytes.Equal(response.Body.Bytes(), imageBytes.Bytes()) {
		t.Fatalf("hash mismatch status %d", response.Code)
	}
}
