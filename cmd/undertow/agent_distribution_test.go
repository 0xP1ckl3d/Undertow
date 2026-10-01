package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"net/netip"
	"undertow/internal/agentprofile"
	"undertow/internal/control"
	"undertow/internal/routing"
)

func TestAgentDistributionAPIAndRetrieval(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "undertow-agent-linux-amd64"), []byte("ELF-template"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := agentprofile.WriteTemplateManifest(templates, "test", []string{"undertow-agent-linux-amd64"}); err != nil {
		t.Fatal(err)
	}
	store, err := agentprofile.OpenStore(filepath.Join(dir, "store"), templates, "test")
	if err != nil {
		t.Fatal(err)
	}
	manager := control.NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	manager.SetServerInfo(control.ServerInfo{Transport: "websocket", Listen: "127.0.0.1:443", Fingerprint: strings.Repeat("a", 64), WebSocketPath: "/undertow", Listeners: []control.ListenerInfo{{Transport: "websocket", Listen: "127.0.0.1:443"}}})
	d := &agentDistribution{store: store, manager: manager, authMode: "none", credential: make([]byte, 32)}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		w := httptest.NewRecorder()
		d.ServeHTTP(w, req)
		return w
	}
	w := call(http.MethodPost, "/v1/agent-profiles", map[string]any{"name": "office"})
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "credential") {
		t.Fatal("profile response leaked credential")
	}
	w = call(http.MethodGet, "/v1/agent-profiles", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "office") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(http.MethodGet, "/v1/agent-profiles/office", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "credential") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(http.MethodPut, "/v1/agent-profiles/office", map[string]any{"server": "127.0.0.2:443"})
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(http.MethodPost, "/v1/agent-artifacts", map[string]any{"profile": "office", "platform": "linux", "architecture": "amd64"})
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	var a agentprofile.Artifact
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a.ProfileFormatVersion != 1 || a.UndertowVersion != "test" {
		t.Fatalf("artifact version metadata missing: %+v", a)
	}
	w = call(http.MethodGet, "/v1/agent-artifacts", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), a.ID) || strings.Contains(w.Body.String(), "credential") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(http.MethodPost, "/v1/agent-artifacts/"+a.ID+"/host", nil)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var hosted hostedArtifactInfo
	if err := json.Unmarshal(w.Body.Bytes(), &hosted); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hosted.Retrieval, "https://127.0.0.2:443/.undertow/artifacts/") {
		t.Fatal(hosted.Retrieval)
	}
	path := "/.undertow/artifacts/" + a.ID + "/" + a.Filename
	request := httptest.NewRequest(http.MethodGet, path, nil)
	download := httptest.NewRecorder()
	d.Retrieve(download, request)
	if download.Code != http.StatusOK {
		t.Fatal(download.Code)
	}
	sum := sha256.Sum256(download.Body.Bytes())
	if hex.EncodeToString(sum[:]) != a.SHA256 {
		t.Fatal("retrieval hash mismatch")
	}
	w = call(http.MethodDelete, "/v1/agent-artifacts/"+a.ID+"/host", nil)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	download = httptest.NewRecorder()
	d.Retrieve(download, request)
	if download.Code != http.StatusNotFound {
		t.Fatal("unhost did not revoke retrieval")
	}
	w = call(http.MethodDelete, "/v1/agent-profiles/office", nil)
	if w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(http.MethodGet, "/v1/agent-artifacts/"+a.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatal("profile deletion removed artifact")
	}
}

func TestDeployScriptsVerifyHashAndStartWithoutArguments(t *testing.T) {
	h := hostedArtifactInfo{Artifact: agentprofile.Artifact{ID: "abc", Filename: "office-linux-amd64", SHA256: strings.Repeat("a", 64)}, Retrieval: "https://example.com/file"}
	for _, kind := range []string{"shell", "powershell"} {
		var out bytes.Buffer
		if err := printDeployScript(&out, h, kind); err != nil {
			t.Fatal(err)
		}
		script := out.String()
		if !strings.Contains(script, h.SHA256) || !strings.Contains(script, h.Retrieval) {
			t.Fatal("script lacks retrieval or hash")
		}
		if strings.Contains(script, "--server") || strings.Contains(script, "--token") {
			t.Fatal("script supplies CLI agent options")
		}
	}
}

func TestConnectedArtifactMetadataShown(t *testing.T) {
	var out bytes.Buffer
	a := control.AgentInfo{ID: "agent-one", ArtifactIdentity: control.ArtifactIdentity{ProfileID: "profile-one", Profile: "office", ArtifactID: "artifact-one", UndertowVersion: "1.0"}}
	if err := renderAgentShow(&out, a); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"office", "profile-one", "artifact-one", "1.0"} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("missing %s from show output", value)
		}
	}
}
