package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/agentprofile"
	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
	"undertow/internal/security"
	"undertow/internal/transport"
	"undertow/internal/transport/relay"
	"undertow/internal/transport/websocket"
)

func TestAgentHostedPayloadStreamsServerArtifactOnlyWhileListenerActive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
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
	identity := testKey(t)
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	manager := control.NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	servers := newServerTransports(ctx, manager, identity, token, "t.undertow.invalid", "/undertow", func(peer transport.Peer) { handleServerPeer(ctx, manager, "", false, peer) })
	defer servers.Close()
	listener, err := servers.Start("websocket", control.TransportStartRequest{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetServerInfo(control.ServerInfo{Transport: "websocket", Listen: listener.Listen, Fingerprint: security.Fingerprint(identity), Listeners: []control.ListenerInfo{listener}})
	parentKey := testKey(t)
	parent, err := websocket.Dial(ctx, websocket.DialOptions{Address: listener.Listen, Path: "/undertow", TLSInsecureSkipVerify: true}, security.Fingerprint(identity), token, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	parentMux := mux.New(ctx, parent, false)
	defer parentMux.Close()
	if err := sendIsolatedTestInventory(ctx, parentMux); err != nil {
		t.Fatal(err)
	}
	go pivot.ServeAgentWithCapabilities(ctx, parentMux, pivot.DefaultCapabilities())
	agentID := security.Fingerprint(parentKey)[:32]
	waitForAgent(t, manager, agentID)
	d := &agentDistribution{store: store, manager: manager, authMode: "none", credential: token}
	manager.SetRelayPayloadAcceptor(d.serveRelayPayload)
	manager.SetRelayAcceptor(func(_ context.Context, parentID, carrier string, upstream *mux.Stream) {
		peer, err := relay.Accept(ctx, upstream, parentID, identity, token)
		if err != nil {
			_ = upstream.Close()
			return
		}
		peer.Carrier = carrier
		handleServerPeer(ctx, manager, "", false, peer)
	})
	relayListener, err := manager.StartRelay(ctx, agentID, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		response := httptest.NewRecorder()
		d.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewReader(encoded)))
		return response
	}
	created := call(http.MethodPost, "/v1/agent-profiles", map[string]any{"name": "child", "server": "127.0.0.1:8443", "transport": "relay"})
	if created.Code != http.StatusCreated {
		t.Fatalf("profile: %d %s", created.Code, created.Body.String())
	}
	built := call(http.MethodPost, "/v1/agent-artifacts", map[string]any{"profile": "child", "platform": "linux", "architecture": "amd64"})
	if built.Code != http.StatusCreated {
		t.Fatalf("build: %d %s", built.Code, built.Body.String())
	}
	var artifact artifactInfo
	if err := json.Unmarshal(built.Body.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	hosted := call(http.MethodPost, "/v1/agent-artifacts/"+artifact.ID+"/agent-hosts", map[string]any{"agent_id": agentID, "bind": relayListener.Bind, "public_host": "127.0.0.1"})
	if hosted.Code != http.StatusCreated {
		t.Fatalf("host: %d %s", hosted.Code, hosted.Body.String())
	}
	var host agentPayloadHostInfo
	if err := json.Unmarshal(hosted.Body.Bytes(), &host); err != nil {
		t.Fatal(err)
	}
	if host.ID == "" || host.AgentID != agentID || !strings.HasPrefix(host.Retrieval, "https://127.0.0.1:") || len(host.TLSCertSHA256) != 64 || host.TLSPublicKeyPin == "" {
		t.Fatalf("host info: %+v", host)
	}
	childKey := testKey(t)
	child, err := relay.Dial(ctx, relayListener.Bind, security.Fingerprint(identity), token, childKey)
	if err != nil {
		t.Fatalf("child relay connection while downloads are enabled: %v", err)
	}
	childMux := mux.New(ctx, child, false)
	defer childMux.Close()
	if err := sendIsolatedTestInventory(ctx, childMux); err != nil {
		t.Fatal(err)
	}
	go pivot.ServeAgentWithCapabilities(ctx, childMux, pivot.DefaultCapabilities())
	childInfo := waitForAgent(t, manager, security.Fingerprint(childKey)[:32])
	if childInfo.Via != agentID || childInfo.Transport != "relay" {
		t.Fatalf("child topology during download hosting: %+v", childInfo)
	}
	consoleCall := func(_ context.Context, method, path string, body any) ([]byte, error) {
		result := call(method, path, body)
		if result.Code >= 400 {
			return nil, errors.New(result.Body.String())
		}
		return result.Body.Bytes(), nil
	}
	var consoleOutput bytes.Buffer
	if err := runPayloadAgentHost(ctx, &consoleOutput, consoleCall, []string{"payload", "agent-hosts", artifact.ID}); err != nil || !strings.Contains(consoleOutput.String(), host.ID) {
		t.Fatalf("console list: %v %s", err, consoleOutput.String())
	}
	consoleOutput.Reset()
	if err := runPayloadAgentHost(ctx, &consoleOutput, consoleCall, []string{"payload", "deploy-script-agent", host.ID, "shell"}); err != nil || !strings.Contains(consoleOutput.String(), "--pinnedpubkey") {
		t.Fatalf("console script: %v %s", err, consoleOutput.String())
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) != 1 {
			return errors.New("missing payload certificate")
		}
		actual := sha256.Sum256(raw[0])
		if hex.EncodeToString(actual[:]) != host.TLSCertSHA256 {
			return errors.New("payload certificate pin mismatch")
		}
		return nil
	}}}} //nolint:gosec -- the test verifies the exact returned certificate pin
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, host.Retrieval, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(io.LimitReader(response.Body, 10<<20))
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("X-Artifact-SHA256") != artifact.SHA256 {
		t.Fatalf("download: %d %v", response.StatusCode, err)
	}
	head, err := client.Head(host.Retrieval)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK || head.ContentLength != artifact.Size {
		t.Fatalf("HEAD: status=%d length=%d", head.StatusCode, head.ContentLength)
	}
	want, err := os.ReadFile(store.ArtifactPath(artifact.Artifact))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("download bytes: %d wanted %d: %v", len(got), len(want), err)
	}
	bad, err := client.Get(strings.TrimSuffix(host.Retrieval, host.RetrievalPath) + "/unrelated")
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusNotFound {
		t.Fatalf("unrelated path: %d", bad.StatusCode)
	}
	stopped := call(http.MethodDelete, "/v1/agent-hosts/"+host.ID, nil)
	if stopped.Code != http.StatusNoContent || len(d.agentHostList("")) != 0 {
		t.Fatalf("stop: %d %s", stopped.Code, stopped.Body.String())
	}
	request, _ = http.NewRequestWithContext(ctx, http.MethodGet, host.Retrieval, nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound || len(manager.RelayList(agentID)) != 1 {
		t.Fatalf("payload stop must invalidate token and preserve relay: status=%d relays=%d", response.StatusCode, len(manager.RelayList(agentID)))
	}
}
