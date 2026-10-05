package main

import (
	"bufio"
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
	"runtime"
	"strings"
	"testing"
	"time"

	"undertow/internal/agentprofile"
	"undertow/internal/control"
	"undertow/internal/mux"
	"undertow/internal/namedpipe"
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
	if err := os.WriteFile(filepath.Join(templates, "undertow-agent-windows-amd64.exe"), []byte("PE-template"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := agentprofile.WriteTemplateManifest(templates, "test", []string{"undertow-agent-linux-amd64", "undertow-agent-windows-amd64.exe"}); err != nil {
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
	manager.SetRelayAcceptor(func(_ context.Context, parentID, carrier, bind string, upstream *mux.Stream) {
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
	if err := runPayloadAgentHost(ctx, &consoleOutput, consoleCall, []string{"payload", "verify-script-agent", host.ID, "shell"}); err != nil || !strings.Contains(consoleOutput.String(), "--head") {
		t.Fatalf("console optional diagnostic script: %v %s", err, consoleOutput.String())
	}
	consoleOutput.Reset()
	if err := runPayloadAgentHost(ctx, &consoleOutput, consoleCall, []string{"payload", "deploy-script-agent", host.ID, "shell"}); err != nil || !strings.Contains(consoleOutput.String(), "--pinnedpubkey") {
		t.Fatalf("console deploy script: %v %s", err, consoleOutput.String())
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
	if runtime.GOOS != "windows" {
		return
	}
	pipeID, err := agentprofile.ID()
	if err != nil {
		t.Fatal(err)
	}
	pipeBind := `\\.\pipe\undertow-test-` + pipeID
	pipeListener, err := manager.StartRelay(ctx, agentID, pipeBind)
	if err != nil {
		t.Fatal(err)
	}
	pipeProfile := call(http.MethodPost, "/v1/agent-profiles", map[string]any{"name": "pipe-child", "server": pipeBind, "transport": "relay-smb"})
	if pipeProfile.Code != http.StatusCreated {
		t.Fatalf("pipe profile: %d %s", pipeProfile.Code, pipeProfile.Body.String())
	}
	pipeBuild := call(http.MethodPost, "/v1/agent-artifacts", map[string]any{"profile": "pipe-child", "platform": "windows", "architecture": "amd64"})
	if pipeBuild.Code != http.StatusCreated {
		t.Fatalf("pipe build: %d %s", pipeBuild.Code, pipeBuild.Body.String())
	}
	var pipeArtifact artifactInfo
	if err := json.Unmarshal(pipeBuild.Body.Bytes(), &pipeArtifact); err != nil {
		t.Fatal(err)
	}
	pipeHosted := call(http.MethodPost, "/v1/agent-artifacts/"+pipeArtifact.ID+"/agent-hosts", map[string]any{"agent_id": agentID, "bind": pipeListener.Bind, "public_host": "."})
	if pipeHosted.Code != http.StatusCreated {
		t.Fatalf("pipe host: %d %s", pipeHosted.Code, pipeHosted.Body.String())
	}
	var pipeHost agentPayloadHostInfo
	if err := json.Unmarshal(pipeHosted.Body.Bytes(), &pipeHost); err != nil || pipeHost.PipePath != `\\.\pipe\undertow-test-`+pipeID || !strings.HasPrefix(pipeHost.Retrieval, "smb-pipe://./") {
		t.Fatalf("pipe host info: %+v %v", pipeHost, err)
	}
	consoleOutput.Reset()
	if err := runPayloadAgentHost(ctx, &consoleOutput, consoleCall, []string{"payload", "deploy-script-agent", pipeHost.ID, "powershell"}); err != nil || !strings.Contains(consoleOutput.String(), "$pipeHost = '.'") {
		t.Fatalf("pipe deploy helper: %v %s", err, consoleOutput.String())
	}
	consoleOutput.Reset()
	if err := runPayloadAgentHost(ctx, &consoleOutput, consoleCall, []string{"payload", "verify-script-agent", pipeHost.ID, "powershell"}); err != nil || !strings.Contains(consoleOutput.String(), "$pipeHost = '.'") {
		t.Fatalf("pipe verification helper: %v %s", err, consoleOutput.String())
	}
	consoleOutput.Reset()
	if err := runPayloadAgentHost(ctx, &consoleOutput, consoleCall, []string{"payload", "deploy-script-agent", pipeHost.ID, "shell"}); err == nil {
		t.Fatal("pipe host accepted a POSIX shell helper")
	}
	pipeChildKey := testKey(t)
	pipeChild, err := relay.DialPipe(ctx, pipeHost.PipePath, security.Fingerprint(identity), token, pipeChildKey)
	if err != nil {
		t.Fatalf("child pipe session while downloads are enabled: %v", err)
	}
	pipeChildMux := mux.New(ctx, pipeChild, false)
	defer pipeChildMux.Close()
	if err := sendIsolatedTestInventory(ctx, pipeChildMux); err != nil {
		t.Fatal(err)
	}
	go pivot.ServeAgentWithCapabilities(ctx, pipeChildMux, pivot.DefaultCapabilities())
	pipeChildInfo := waitForAgent(t, manager, security.Fingerprint(pipeChildKey)[:32])
	if pipeChildInfo.Via != agentID || pipeChildInfo.Transport != "relay-smb" {
		t.Fatalf("pipe child topology during download hosting: %+v", pipeChildInfo)
	}
	probePipe, err := namedpipe.Dial(ctx, pipeHost.PipePath)
	if err != nil {
		t.Fatal(err)
	}
	probeTLS := tls.Client(probePipe, &tls.Config{ServerName: "undertow", InsecureSkipVerify: true, VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) != 1 {
			return errors.New("missing pipe certificate")
		}
		actual := sha256.Sum256(raw[0])
		if hex.EncodeToString(actual[:]) != pipeHost.TLSCertSHA256 {
			return errors.New("pipe certificate pin mismatch")
		}
		return nil
	}}) //nolint:gosec -- the test verifies the exact returned certificate pin
	probeRequest, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://undertow"+pipeHost.RetrievalPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := probeRequest.Write(probeTLS); err != nil {
		t.Fatal(err)
	}
	probeReply, err := http.ReadResponse(bufio.NewReader(probeTLS), probeRequest)
	if err != nil {
		t.Fatal(err)
	}
	probeReply.Body.Close()
	probeTLS.Close()
	if probeReply.StatusCode != http.StatusOK || probeReply.Header.Get("X-Artifact-SHA256") != pipeArtifact.SHA256 {
		t.Fatalf("pipe verification failed: status=%d hash=%s", probeReply.StatusCode, probeReply.Header.Get("X-Artifact-SHA256"))
	}
	pipeConn, err := namedpipe.Dial(ctx, pipeHost.PipePath)
	if err != nil {
		t.Fatal(err)
	}
	tlsPipe := tls.Client(pipeConn, &tls.Config{ServerName: "undertow", InsecureSkipVerify: true, VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) != 1 {
			return errors.New("missing pipe certificate")
		}
		actual := sha256.Sum256(raw[0])
		if hex.EncodeToString(actual[:]) != pipeHost.TLSCertSHA256 {
			return errors.New("pipe certificate pin mismatch")
		}
		return nil
	}}) //nolint:gosec -- the test verifies the exact returned certificate pin
	defer tlsPipe.Close()
	pipeRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://undertow"+pipeHost.RetrievalPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipeRequest.Write(tlsPipe); err != nil {
		t.Fatal(err)
	}
	pipeResponse, err := http.ReadResponse(bufio.NewReader(tlsPipe), pipeRequest)
	if err != nil {
		t.Fatal(err)
	}
	pipeBytes, err := io.ReadAll(io.LimitReader(pipeResponse.Body, 10<<20))
	pipeResponse.Body.Close()
	wantPipe, readErr := os.ReadFile(store.ArtifactPath(pipeArtifact.Artifact))
	if err != nil || readErr != nil || pipeResponse.StatusCode != http.StatusOK || !bytes.Equal(pipeBytes, wantPipe) {
		t.Fatalf("pipe download: status=%d read=%v artifact=%v", pipeResponse.StatusCode, err, readErr)
	}
	if stopped := call(http.MethodDelete, "/v1/agent-hosts/"+pipeHost.ID, nil); stopped.Code != http.StatusNoContent || len(manager.RelayList(agentID)) != 2 {
		t.Fatalf("pipe host stop: %d relays=%d", stopped.Code, len(manager.RelayList(agentID)))
	}
}
