package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/authcontext"
	"undertow/internal/pivot"
	"undertow/internal/routing"
)

func tokenTestManager() *Manager {
	return NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
}
func TestTokenDefaultsIsolateSessionsAndFreezeQueuedSelection(t *testing.T) {
	m := tokenTestManager()
	id := strings.Repeat("a", 32)
	other := strings.Repeat("b", 32)
	report := pivot.CapabilityReport{Supported: []string{"tokens"}, Allowed: []string{"tokens"}}
	m.offlineAgents["agent"] = AgentInfo{ID: "agent", Capabilities: &report, ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute)}
	m.tokenDefaults = map[tokenDefaultKey]string{{11, "agent"}: id, {12, "agent"}: other}
	bind := func(session uint64, body string) *http.Request {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/agents/agent/jobs", strings.NewReader(body)).WithContext(context.WithValue(context.Background(), jobOwnerKey{}, session))
		r, err := m.bindTokenOperation(r)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1 := bind(11, `{"argv":["whoami.exe"]}`)
	r2 := bind(12, `{"argv":["whoami.exe"]}`)
	if pivot.TokenContextID(r1.Context()) != id || pivot.TokenContextID(r2.Context()) != other {
		t.Fatal("defaults crossed operator connections")
	}
	explicit := bind(11, `{"argv":["whoami.exe"],"token_context_id":"process"}`)
	if pivot.TokenContextID(explicit.Context()) != "process" {
		t.Fatal("explicit process override lost")
	}
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	m.offlineAgents["agent"] = AgentInfo{ID: "agent", Capabilities: &report, ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute)}
	job, err := m.StartJob(r1.Context(), 11, "agent", []string{"whoami.exe"})
	if err != nil || job.State != "queued" || job.TokenContextID != id {
		t.Fatal(job, err)
	}
	m.tokenDefaults[tokenDefaultKey{11, "agent"}] = other
	queued := m.jobs[job.ID].request
	if queued.TokenContextID != id {
		t.Fatal("queued identity followed mutable default")
	}
	data, _ := json.Marshal(queued)
	if strings.Contains(string(data), "password") || strings.Contains(string(data), "handle") {
		t.Fatal("secret-capable queue", string(data))
	}
	if err = m.RestoreJobHistory(); err != nil {
		t.Fatal(err)
	}
	if restored := m.jobs[job.ID]; restored == nil || restored.info.TokenContextID != id || restored.request.TokenContextID != id {
		t.Fatal("restart lost explicit selection")
	}
}
func TestTokenNegotiationAndStrictRemotePaths(t *testing.T) {
	m := tokenTestManager()
	id := strings.Repeat("a", 32)
	if _, err := m.resolveTokenContext(context.Background(), 0, "old-agent", id); err == nil {
		t.Fatal("old agent accepted context")
	}
	for _, path := range []string{"/v1/agents/agent/tokens", "/v1/agents/agent/tokens?password=x", "/v1/agents/a%2fb/tokens", "/v1/agents/agent/tokens/extra"} {
		r := httptest.NewRequest("POST", path, nil)
		allowed := clientRequestAllowed(r, 1)
		if allowed != (path == "/v1/agents/agent/tokens") {
			t.Fatal(path, allowed)
		}
	}
	r := httptest.NewRequest("POST", "/v1/agents/agent/jobs", strings.NewReader(`{"argv":["whoami"],"token_context_id":"17"}`))
	if _, err := m.bindTokenOperation(r); err == nil {
		t.Fatal("native handle accepted as ID")
	}
}
func TestTokenRelayHeaderPreservesPayloadAndProcessOverride(t *testing.T) {
	id := strings.Repeat("a", 32)
	payload := []byte{0, 1, 2, 3, 4, 255}
	reader := bufio.NewReader(bytes.NewReader(append([]byte("{\"size\":6,\"token_context_id\":\"process\"}\n"), payload...)))
	line, err := readTokenOperationLine(reader, id)
	if err != nil {
		t.Fatal(err)
	}
	var r pivot.MemoryRequest
	if json.Unmarshal(line, &r) != nil || r.TokenContextID != "process" {
		t.Fatal(string(line))
	}
	remaining := make([]byte, len(payload))
	if _, err = reader.Read(remaining); err != nil || !bytes.Equal(payload, remaining) {
		t.Fatal("module bytes changed", remaining, err)
	}
}
func TestTokenCreationWaitsInMemoryAndAuditExcludesCredentials(t *testing.T) {
	m := tokenTestManager()
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	keys := &authcontext.CreationKeys{}
	key, err := keys.Issue()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := authcontext.SealLogon(key, authcontext.LogonRequest{User: "fixture-user", Password: "fixture-secret", LogonType: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(pivot.TokenRequest{Action: "create", SealedLogon: &sealed})
	report := pivot.CapabilityReport{Supported: []string{"tokens"}, Allowed: []string{"tokens"}}
	m.offlineAgents["offline"] = AgentInfo{ID: "offline", Capabilities: &report, ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute)}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("POST", "/v1/agents/offline/tokens", bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	m.handler("test").ServeHTTP(w, request)
	if w.Code < 400 || len(m.jobs) != 0 {
		t.Fatal("creation entered a durable job", w.Code, m.jobs)
	}
	audits, err := store.AuditHistory(10)
	if err != nil || len(audits) != 1 {
		t.Fatal(audits, err)
	}
	data, _ := json.Marshal(audits)
	for _, secret := range []string{"fixture-secret", "fixture-user", "password", "logon"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("credential material in audit", string(data))
		}
	}
	var count int
	if err = store.db.QueryRow("SELECT count(*) FROM queued_job_requests").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestAuditRetainsContextIDAcrossDatabaseOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if err = store.RecordAudit(AuditRecord{ID: "audit", At: time.Now().UTC(), Action: "POST", Target: "/v1/agents/a/exec", TokenContextID: id}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	store, err = OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	records, err := store.AuditHistory(10)
	if err != nil || len(records) != 1 || records[0].TokenContextID != id {
		t.Fatal(records, err)
	}
}

func TestTokenSnapshotRetainsCandidateExpiryAndAllowsSleepingUse(t *testing.T) {
	m := tokenTestManager()
	contextID := strings.Repeat("c", 32)
	candidateID := strings.Repeat("d", 32)
	report := pivot.CapabilityReport{Supported: []string{"tokens"}, Allowed: []string{"tokens"}, TokenStoreInstanceID: "store-one"}
	m.offlineAgents["agent"] = AgentInfo{ID: "agent", Capabilities: &report, ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute)}
	m.rememberTokenResponse("agent", pivot.TokenResponse{StoreInstanceID: "store-one", Contexts: []authcontext.Metadata{{ID: contextID, Identity: `LAB\operator`}}, Candidates: []authcontext.Metadata{{ID: candidateID, Identity: `NT AUTHORITY\SYSTEM`, State: "available", ExpiresAt: time.Now().Add(-time.Second)}}})
	result, ok := m.cachedTokenResponse("agent")
	if !ok || len(result.Candidates) != 1 || result.Candidates[0].State != "expired" {
		t.Fatal("candidate expiry metadata did not survive navigation cache", result, ok)
	}
	r := httptest.NewRequest("POST", "/v1/agents/agent/tokens", nil)
	r.SetPathValue("id", "agent")
	r = r.WithContext(context.WithValue(r.Context(), jobOwnerKey{}, uint64(77)))
	w := httptest.NewRecorder()
	m.manageTokenRequest(w, r, pivot.TokenRequest{Action: "use", ID: contextID})
	if w.Code != http.StatusOK || m.tokenDefault(77, "agent") != contextID {
		t.Fatal("sleeping-agent selection was not immediate", w.Code, w.Body.String())
	}
}

func TestJumpFreezesSessionContextAndRejectsCredentialCombination(t *testing.T) {
	m := tokenTestManager()
	store, err := OpenOperationsStore(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = m.SetOperationsStore(store); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("e", 32)
	report := pivot.CapabilityReport{Supported: []string{"tokens"}, Allowed: []string{"tokens"}}
	m.offlineAgents["source"] = AgentInfo{ID: "source", Capabilities: &report, ConnectionState: "sleeping", SleepLostAfter: time.Now().Add(time.Minute)}
	m.tokenDefaults = map[tokenDefaultKey]string{{91, "source"}: id}
	record := DeploymentRecord{ID: "jump-one", SourceAgentID: "source", Target: "ws01", ArtifactID: "artifact", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), State: "prepared"}
	if err = store.CreateDeployment(record); err != nil {
		t.Fatal(err)
	}
	bind := func(body string) (*http.Request, error) {
		r := httptest.NewRequest(http.MethodPost, "/v1/deployments/jump-one/start", strings.NewReader(body))
		return m.bindTokenOperation(r.WithContext(context.WithValue(r.Context(), jobOwnerKey{}, uint64(91))))
	}
	r, err := bind(`{"install_path":""}`)
	if err != nil || pivot.TokenContextID(r.Context()) != id {
		t.Fatal("Jump did not freeze operator default", pivot.TokenContextID(r.Context()), err)
	}
	var body DeploymentStartRequest
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.TokenContextID != id {
		t.Fatal("Jump request did not retain selected context", body)
	}
	if _, err = bind(`{"username":"LAB\\operator","password":"fixture","token_context_id":"` + id + `"}`); err == nil {
		t.Fatal("Jump combined supplied credentials with a token context")
	}
	r, err = bind(`{"username":"LAB\\operator","password":"fixture"}`)
	if err != nil || pivot.TokenContextID(r.Context()) != "process" {
		t.Fatal("supplied Jump credentials did not become an explicit process-context override", err)
	}
}
