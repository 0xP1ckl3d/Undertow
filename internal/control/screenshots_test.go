package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScreenshotCatalogAndChunkRetrieval(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.MkdirAll(store.screenshotsDir, 0700); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	image := bytes.Repeat([]byte("png-test-data"), 30000)
	if err := os.WriteFile(store.screenshotPath(id), image, 0600); err != nil {
		t.Fatal(err)
	}
	entry := ScreenshotInfo{ID: id, AgentID: "agent-a", Screen: 2, At: time.Now().UTC(), Size: int64(len(image)), SHA256: "hash", Width: 100, Height: 50, ClientID: "client-a", ClientSessionID: 42}
	if err := store.SaveScreenshot(entry); err != nil {
		t.Fatal(err)
	}
	history, err := store.Screenshots("agent-a")
	if err != nil || len(history) != 1 || history[0].ID != id || history[0].ClientSessionID != 42 {
		t.Fatalf("history: %+v %v", history, err)
	}
	manager := &Manager{operations: store}
	request := httptest.NewRequest(http.MethodGet, "/v1/screenshots/"+id+"/chunk?offset=262144", nil)
	request.SetPathValue("id", id)
	response := httptest.NewRecorder()
	manager.screenshotChunkHandler(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), image[256<<10:]) {
		t.Fatalf("chunk status %d length %d", response.Code, response.Body.Len())
	}
	invalid := httptest.NewRequest(http.MethodGet, "/v1/screenshots/../chunk?offset=0", nil)
	invalid.SetPathValue("id", "../")
	response = httptest.NewRecorder()
	manager.screenshotChunkHandler(response, invalid)
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid ID status %d", response.Code)
	}
}
