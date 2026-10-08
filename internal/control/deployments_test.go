package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
	"undertow/internal/routing"
)

type deploymentExecutorStub struct{ preflightErr error }

func (s deploymentExecutorStub) Preflight(_ context.Context, record DeploymentRecord, request DeploymentStartRequest) (DeploymentExecutionPlan, error) {
	if s.preflightErr != nil {
		return DeploymentExecutionPlan{}, s.preflightErr
	}
	credential, err := pivot.NewWindowsCredentialSecret(record.Target, request.Username, request.Password, request.NTHash)
	if err != nil {
		return DeploymentExecutionPlan{}, err
	}
	return DeploymentExecutionPlan{DeliveryType: "direct-share", DeliveryID: "source", InstallPath: `C:\ProgramData\Undertow\agent.exe`, Credential: credential}, nil
}

func (deploymentExecutorStub) Start(_ context.Context, _ DeploymentRecord, plan DeploymentExecutionPlan, _ *mux.Mux, existingJobID string, report func(DeploymentProgress) error) error {
	jobID := existingJobID
	if jobID == "" {
		jobID = "job-one"
	}
	return report(DeploymentProgress{State: "waiting", Progress: "Method Job accepted", JobID: jobID, InstallPath: plan.InstallPath, ServiceName: "Undertow-test"})
}

func deploymentTestManager(t *testing.T) (*Manager, *OperationsStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operations.db")
	store, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(routing.New(nil), nil, netip.MustParsePrefix("172.16.254.0/24"), netip.MustParseAddr("172.16.254.1"))
	if err := m.SetOperationsStore(store); err != nil {
		store.Close()
		t.Fatal(err)
	}
	m.SetDeploymentArtifactLookup(func(id string) (DeploymentArtifact, error) {
		if id == "build-one" {
			return DeploymentArtifact{ID: id, ProfileID: "profile-one", Profile: "office", Platform: "windows", SHA256: "abc", UndertowVersion: "test"}, nil
		}
		return DeploymentArtifact{}, nil
	})
	report := pivot.DefaultCapabilities().Report()
	m.agents["source"] = &agentState{inventoryReady: true, inventory: AgentInfo{ID: "source", OS: "windows", ArtifactIdentity: ArtifactIdentity{UndertowVersion: "test"}, Capabilities: &report}}
	return m, store, path
}

func TestCreateDeploymentRejectsSourceArtifactVersionMismatch(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	m.agents["source"].inventory.ArtifactIdentity.UndertowVersion = "older"

	_, err := m.createDeployment(context.Background(), createDeploymentRequest{
		SourceAgentID: "source",
		Target:        "ws02",
		ArtifactID:    "build-one",
		Method:        "wmi",
		Context:       "current-user",
	})
	if err == nil || !strings.Contains(err.Error(), "source agent runs Undertow older but selected artifact uses test") {
		t.Fatalf("expected explicit source/artifact version mismatch, got %v", err)
	}
}

func TestCustomArtifactDeploymentSkipsBuildVersionAndCompletesOnLaunch(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	m.agents["source"].inventory.ArtifactIdentity.UndertowVersion = "older"
	m.SetDeploymentArtifactLookup(func(id string) (DeploymentArtifact, error) {
		if id == "custom-one" {
			return DeploymentArtifact{ID: id, Profile: "Approved utility", Platform: "windows", SHA256: "def", ServiceCapable: true, Custom: true}, nil
		}
		return DeploymentArtifact{}, nil
	})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{
		SourceAgentID: "source", Target: "ws02", ArtifactID: "custom-one", Method: "service-control", Context: "local-system",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !record.CustomArtifact || record.Profile != "Approved utility" || record.ProfileID != "" {
		t.Fatalf("custom Jump record: %+v", record)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChangeDeployment(record.ID, func(item *DeploymentRecord) error { item.State = "dispatching"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.AdvanceDeployment(record.ID, DeploymentProgress{State: "waiting", Progress: "Method Job accepted", JobID: "job-custom"}); err != nil {
		t.Fatal(err)
	}
	if err := m.AdvanceDeployment(record.ID, DeploymentProgress{State: "completed", Progress: "Custom artifact launch completed", JobID: "job-custom"}); err != nil {
		t.Fatal(err)
	}
	completed, err := store.Deployment(record.ID)
	if err != nil || completed.State != "completed" || completed.CompletedAt == nil || completed.ResultAgentID != "" {
		t.Fatalf("completed custom Jump: %+v err=%v", completed, err)
	}
}

func TestDeploymentStageOneAndUniqueEnrollment(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	req := createDeploymentRequest{SourceAgentID: "source", Target: "WS01.example.test", ArtifactID: "build-one", Method: "scheduled-task", Context: "local-system"}
	record, err := m.createDeployment(context.Background(), req)
	if err != nil || record.State != "created" || record.Target != "ws01.example.test" || record.ProfileID != "profile-one" {
		t.Fatalf("create: %+v %v", record, err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{}); err == nil {
		t.Fatal("stage one unexpectedly started a Windows method")
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/deployments/"+record.ID+"/start", strings.NewReader(`{}`))
	httpRequest.Header.Set("Authorization", "Bearer test-token")
	httpResult := httptest.NewRecorder()
	m.handler("test-token").ServeHTTP(httpResult, httpRequest)
	if httpResult.Code != http.StatusNotImplemented {
		t.Fatalf("stage-one start status=%d body=%s", httpResult.Code, httpResult.Body.String())
	}
	stillPrepared, err := store.Deployment(record.ID)
	if err != nil || stillPrepared.State != "prepared" {
		t.Fatalf("prepared after unsupported start: %+v %v", stillPrepared, err)
	}
	if _, err := store.ChangeDeployment(record.ID, func(item *DeploymentRecord) error { item.State = "dispatching"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.AdvanceDeployment(record.ID, DeploymentProgress{State: "waiting", Progress: "Method started", JobID: "job-one"}); err != nil {
		t.Fatal(err)
	}
	other, err := m.createDeployment(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChangeDeployment(other.ID, func(item *DeploymentRecord) error { item.State = "dispatching"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.AdvanceDeployment(other.ID, DeploymentProgress{State: "waiting", Progress: "Method started", JobID: "job-two"}); err != nil {
		t.Fatal(err)
	}
	m.agents["result"] = &agentState{newIdentity: true, inventoryReady: true, inventory: AgentInfo{ID: "result", Hostname: "WS01.example.test", OS: "windows", ArtifactIdentity: ArtifactIdentity{ArtifactID: "build-one"}, Connected: time.Now().UTC()}}
	m.correlateDeployment("result")
	unlinked, _ := store.Deployment(record.ID)
	if unlinked.State != "waiting" {
		t.Fatal("ambiguous enrolment was linked automatically")
	}
	linked, err := m.linkDeployment(record.ID, "result", false)
	if err != nil || linked.State != "completed" || linked.ResultAgentID != "result" || linked.JobID != "job-one" {
		t.Fatalf("manual link: %+v %v", linked, err)
	}
	if _, err := m.linkDeployment(other.ID, "result", false); err == nil {
		t.Fatal("one agent linked to two deployments")
	}
}

func TestDeploymentValidationAndPersistence(t *testing.T) {
	m, store, path := deploymentTestManager(t)
	bad := []createDeploymentRequest{
		{SourceAgentID: "source", Target: "https://ws01", ArtifactID: "build-one", Method: "winrm", Context: "current-user"},
		{SourceAgentID: "source", Target: "ws01", ArtifactID: "build-one", Method: "winrm", Context: "local-system"},
		{SourceAgentID: "source", Target: "ws01", ArtifactID: "build-one", Method: "scheduled-task", Context: "named-account"},
		{SourceAgentID: "source", Target: "ws01", ArtifactID: "missing", Method: "wmi", Context: "current-user"},
	}
	for _, req := range bad {
		if _, err := m.createDeployment(context.Background(), req); err == nil {
			t.Fatalf("accepted invalid request %+v", req)
		}
	}
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws01", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Deployment(record.ID)
	if err != nil || got.ArtifactID != record.ArtifactID || got.SourceAgentID != "source" {
		t.Fatalf("reload: %+v %v", got, err)
	}
}

func TestDeploymentAutoLinksOnlyOneNewAgent(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws02", ArtifactID: "build-one", Method: "wmi", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.AdvanceDeployment(record.ID, DeploymentProgress{State: "waiting", Progress: "Method started"}); err == nil {
		t.Fatal("prepared deployment entered waiting without a dispatch reservation")
	}
	if _, err := store.ChangeDeployment(record.ID, func(item *DeploymentRecord) error { item.State = "dispatching"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.AdvanceDeployment(record.ID, DeploymentProgress{State: "waiting", Progress: "Method started", JobID: "job-two"}); err != nil {
		t.Fatal(err)
	}
	m.agents["new-agent"] = &agentState{newIdentity: true, inventoryReady: true, inventory: AgentInfo{ID: "new-agent", Hostname: "ws02", OS: "windows", ArtifactIdentity: ArtifactIdentity{ArtifactID: "build-one"}, Connected: time.Now().UTC()}}
	m.correlateDeployment("new-agent")
	linked, err := store.Deployment(record.ID)
	if err != nil || linked.State != "completed" || linked.ResultAgentID != "new-agent" {
		t.Fatalf("unique callback: %+v %v", linked, err)
	}
}

func TestDispatchingDeploymentRecoversAsFailed(t *testing.T) {
	m, store, path := deploymentTestManager(t)
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws03", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChangeDeployment(record.ID, func(item *DeploymentRecord) error { item.State = "dispatching"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecoverDispatchingDeployments(); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Deployment(record.ID)
	if err != nil || got.State != "failed" || got.Error == "" {
		t.Fatalf("recovered dispatch: %+v %v", got, err)
	}
}

func TestSleepingDeploymentQueuesUntilCheckIn(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	delete(m.agents, "source")
	report := pivot.DefaultCapabilities().Report()
	m.offlineAgents["source"] = AgentInfo{ID: "source", OS: "windows", ArtifactIdentity: ArtifactIdentity{UndertowVersion: "test"}, Capabilities: &report, ConnectionState: "sleeping", SleepSupported: true, Sleep: SleepPolicy{IntervalSeconds: 15}, SleepLostAfter: time.Now().Add(time.Minute)}
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws06", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal("prepare sleeping source: ", err)
	}
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Delivery: "direct-share"}); err != nil {
		t.Fatal("queue sleeping source: ", err)
	}
	got, err := store.Deployment(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.LoadQueuedJobs()
	request, present := queued[got.JobID]
	if err != nil || len(queued) != 1 || !present || request.Kind != "deployment" {
		t.Fatalf("queued deployment: %+v err=%v", queued, err)
	}
	if got.State != "dispatching" || got.JobID == "" || !strings.Contains(got.Progress, "next check-in") {
		t.Fatalf("queued record: %+v err=%v", got, err)
	}
}

func TestSleepingDeploymentKeepsPasswordOnlyInMemory(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	delete(m.agents, "source")
	report := pivot.DefaultCapabilities().Report()
	m.offlineAgents["source"] = AgentInfo{ID: "source", OS: "windows", ArtifactIdentity: ArtifactIdentity{UndertowVersion: "test"}, Capabilities: &report, ConnectionState: "sleeping", SleepSupported: true, Sleep: SleepPolicy{IntervalSeconds: 15}, SleepLostAfter: time.Now().Add(time.Minute)}
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws06", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	const password = "never persist this password"
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Username: `LAB\operator`, Password: password}); err != nil {
		t.Fatal(err)
	}
	queuedRecord, err := store.Deployment(record.ID)
	if err != nil || queuedRecord.Account != `LAB\operator` || queuedRecord.JobID == "" {
		t.Fatalf("queued credential record=%+v err=%v", queuedRecord, err)
	}
	queued, err := store.LoadQueuedJobs()
	if err != nil {
		t.Fatal(err)
	}
	request := queued[queuedRecord.JobID]
	encoded, err := json.Marshal(request)
	if err != nil || strings.Contains(string(encoded), password) || request.CredentialAccount != `LAB\operator` {
		t.Fatalf("persisted queued credential request=%s err=%v", encoded, err)
	}
	credential, ok := m.deploymentCredentials[record.ID]
	if !ok || credential.Password != password {
		t.Fatal("credential was not retained in memory for queued dispatch")
	}
	if err := m.CancelJob(0, queuedRecord.JobID); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.deploymentCredentials[record.ID]; ok {
		t.Fatal("cancelled Jump retained supplied credentials")
	}
}

func TestSleepingDeploymentKeepsNTHashOnlyInMemory(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	delete(m.agents, "source")
	report := pivot.DefaultCapabilities().Report()
	m.offlineAgents["source"] = AgentInfo{ID: "source", OS: "windows", ArtifactIdentity: ArtifactIdentity{UndertowVersion: "test"}, Capabilities: &report, ConnectionState: "sleeping", SleepSupported: true, Sleep: SleepPolicy{IntervalSeconds: 15}, SleepLostAfter: time.Now().Add(time.Minute)}
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws06", ArtifactID: "build-one", Method: "wmi", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Username: `LAB\operator`, NTHash: hash}); err != nil {
		t.Fatal(err)
	}
	queuedRecord, err := store.Deployment(record.ID)
	if err != nil || queuedRecord.Account != `LAB\operator` || queuedRecord.JobID == "" {
		t.Fatalf("queued hash record=%+v err=%v", queuedRecord, err)
	}
	queued, err := store.LoadQueuedJobs()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(queued[queuedRecord.JobID])
	if err != nil || strings.Contains(string(encoded), hash) {
		t.Fatalf("persisted NT hash in queued request=%s err=%v", encoded, err)
	}
	credential, ok := m.deploymentCredentials[record.ID]
	if !ok || credential.NTHash != hash || credential.Password != "" {
		t.Fatal("NT hash was not retained only in memory for queued dispatch")
	}
}

func TestSuppliedCredentialRejectsOlderSourceWithoutCapability(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	m.agents["source"].inventory.Capabilities = nil
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws06", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	err = m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Username: `LAB\operator`, Password: "test password"})
	if err == nil || !strings.Contains(err.Error(), "does not support supplied Jump credentials") {
		t.Fatalf("older source credential start error=%v", err)
	}
	prepared, err := store.Deployment(record.ID)
	if err != nil || prepared.State != "prepared" || prepared.Account != "" {
		t.Fatalf("older source mutated record=%+v err=%v", prepared, err)
	}
}

func TestNTHashRejectsSourceWithoutHashCapability(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	report := pivot.DefaultCapabilities().Report()
	allowed := report.Allowed[:0]
	for _, capability := range report.Allowed {
		if capability != "jump-nt-hash" {
			allowed = append(allowed, capability)
		}
	}
	report.Allowed = allowed
	m.agents["source"].inventory.Capabilities = &report
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws06", ArtifactID: "build-one", Method: "wmi", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	err = m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Username: `LAB\operator`, NTHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err == nil || !strings.Contains(err.Error(), "does not support NT-hash") {
		t.Fatalf("source without hash capability error=%v", err)
	}
}

func TestSourceIdentityStillAllowsSourceWithoutCredentialCapability(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.agents["source"].inventory.Capabilities = nil
	m.agents["source"].mux = mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws06", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{}); err != nil {
		t.Fatalf("source-identity Jump regressed for an older source: %v", err)
	}
	started, err := store.Deployment(record.ID)
	if err != nil || started.State != "waiting" || started.Account != "" {
		t.Fatalf("source-identity Jump=%+v err=%v", started, err)
	}
}

func TestQueuedDeploymentSurvivesDispatchRecovery(t *testing.T) {
	m, store, path := deploymentTestManager(t)
	delete(m.agents, "source")
	m.offlineAgents["source"] = AgentInfo{ID: "source", OS: "windows", ArtifactIdentity: ArtifactIdentity{UndertowVersion: "test"}, ConnectionState: "sleeping", SleepSupported: true, Sleep: SleepPolicy{IntervalSeconds: 15}, SleepLostAfter: time.Now().Add(time.Minute)}
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws07", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Delivery: "direct-share"}); err != nil {
		t.Fatal(err)
	}
	queuedRecord, err := store.Deployment(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOperationsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecoverDispatchingDeployments(); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Deployment(record.ID)
	if err != nil || got.State != "dispatching" || got.JobID != queuedRecord.JobID {
		t.Fatalf("queued recovery: %+v err=%v", got, err)
	}
}

func TestDeploymentExecutorPersistsPlanAndRejectsDuplicateStart(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.agents["source"].mux = mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws04", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{})
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Delivery: "direct-share"}); err != nil {
		t.Fatal(err)
	}
	started, err := store.Deployment(record.ID)
	if err != nil || started.State != "waiting" || started.DeliveryType != "direct-share" || started.JobID != "job-one" || started.InstallPath == "" || started.ServiceName != "Undertow-test" {
		t.Fatalf("started deployment: %+v err=%v", started, err)
	}
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Delivery: "direct-share"}); err == nil {
		t.Fatal("duplicate deployment start succeeded")
	}
}

func TestDeploymentPreflightFailureLeavesPrepared(t *testing.T) {
	m, store, _ := deploymentTestManager(t)
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.agents["source"].mux = mux.New(ctx, &idleTransport{done: make(chan struct{})}, true)
	record, err := m.createDeployment(context.Background(), createDeploymentRequest{SourceAgentID: "source", Target: "ws05", ArtifactID: "build-one", Method: "winrm", Context: "current-user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.prepareDeployment(record.ID); err != nil {
		t.Fatal(err)
	}
	m.SetDeploymentMethodExecutor(deploymentExecutorStub{preflightErr: errors.New("host unavailable")})
	if err := m.startDeployment(context.Background(), record.ID, DeploymentStartRequest{Delivery: "server"}); err == nil {
		t.Fatal("preflight unexpectedly succeeded")
	}
	prepared, err := store.Deployment(record.ID)
	if err != nil || prepared.State != "prepared" || prepared.DeliveryType != "" {
		t.Fatalf("preflight mutated record: %+v err=%v", prepared, err)
	}
}
