package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"undertow/internal/routing"
)

func TestScreenshotCanBeSubmittedDuringIntentionalSleep(t *testing.T) {
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := m.ConfigureJobOutput(t.TempDir(), 1<<20, 2<<20); err != nil {
		t.Fatal(err)
	}
	if err := m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.offlineAgents["sleeping-screen"] = AgentInfo{ID: "sleeping-screen", ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute)}
	m.mu.Unlock()
	ctx := context.WithValue(context.Background(), actionContextKey{}, actionContext{ClientID: "client-a", ClientSessionID: 42, ActionClaims: ActionClaims{OperatorID: "talon", DisplayName: "Talon"}})
	request := httptest.NewRequest(http.MethodPost, "/v1/agents/sleeping-screen/screenshots", bytes.NewBufferString(`{"screen":1}`)).WithContext(ctx)
	request.SetPathValue("id", "sleeping-screen")
	response := httptest.NewRecorder()
	m.captureScreenshotHandler(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("capture submission: %d %s", response.Code, response.Body.String())
	}
	var job JobInfo
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Kind != "screenshot" || job.State != "queued" {
		t.Fatalf("capture job: %+v", job)
	}
	queued, err := store.LoadQueuedJobs()
	if err != nil {
		t.Fatal(err)
	}
	if queued[job.ID].Actor.ClientSessionID != 42 || queued[job.ID].Actor.OperatorID != "talon" {
		t.Fatalf("queued attribution lost: %+v", queued[job.ID].Actor)
	}
}

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
