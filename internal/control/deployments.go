package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"undertow/internal/mux"
)

type DeploymentArtifact struct {
	ID             string
	ProfileID      string
	Profile        string
	Platform       string
	SHA256         string
	Revoked        bool
	ServiceCapable bool
}

type DeploymentStartRequest struct {
	Delivery    string `json:"delivery"`
	InstallPath string `json:"install_path,omitempty"`
}

type DeploymentExecutionPlan struct {
	DeliveryType string
	DeliveryID   string
	InstallPath  string
	Opaque       any
}

type DeploymentProgress struct {
	State       string
	Progress    string
	Failure     string
	JobID       string
	TransferID  string
	InstallPath string
	ServiceName string
	TaskName    string
}

// DeploymentMethodExecutor is the single connection point between the
// durable workflow and Windows method implementations. Preflight must not
// touch the target; Start must report waiting or failed before returning.
type DeploymentMethodExecutor interface {
	Preflight(context.Context, DeploymentRecord, DeploymentStartRequest) (DeploymentExecutionPlan, error)
	Start(context.Context, DeploymentRecord, DeploymentExecutionPlan, *mux.Mux, func(DeploymentProgress) error) error
}

type createDeploymentRequest struct {
	SourceAgentID string `json:"source_agent_id"`
	Target        string `json:"target"`
	ArtifactID    string `json:"artifact_id"`
	Method        string `json:"method"`
	Context       string `json:"context"`
	Account       string `json:"account,omitempty"`
}

var deploymentHostLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func normalizeDeploymentTarget(value string) (string, error) {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip.String(), nil
	}
	value = strings.TrimSuffix(value, ".")
	if len(value) == 0 || len(value) > 253 || strings.ContainsAny(value, "/\\:@?#% \t\r\n") {
		return "", errors.New("target must be a hostname or IP address without a port")
	}
	for _, label := range strings.Split(value, ".") {
		if !deploymentHostLabel.MatchString(label) {
			return "", errors.New("target must be a valid hostname or IP address")
		}
	}
	return strings.ToLower(value), nil
}

func validateDeploymentChoice(method, contextName, account string) error {
	switch method {
	case "winrm", "wmi", "service-control", "scheduled-task":
	default:
		return errors.New("choose WinRM, WMI, Service Control, or Scheduled Task")
	}
	switch contextName {
	case "current-user", "local-system":
	default:
		return errors.New("choose a target execution context")
	}
	if method == "service-control" && contextName != "local-system" || (method == "winrm" || method == "wmi") && contextName != "current-user" {
		return errors.New("selected context is unavailable for this Windows method")
	}
	if account != "" {
		return errors.New("named-account deployments require a future credential integration")
	}
	return nil
}

func (m *Manager) SetDeploymentArtifactLookup(lookup func(string) (DeploymentArtifact, error)) {
	m.mu.Lock()
	m.deploymentArtifactLookup = lookup
	m.mu.Unlock()
}

func (m *Manager) SetDeploymentMethodExecutor(executor DeploymentMethodExecutor) {
	m.mu.Lock()
	m.deploymentExecutor = executor
	m.mu.Unlock()
}

func (m *Manager) deploymentStore() (*OperationsStore, error) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		return nil, errors.New("deployment storage is unavailable")
	}
	return store, nil
}

func (m *Manager) createDeployment(ctx context.Context, req createDeploymentRequest) (DeploymentRecord, error) {
	target, err := normalizeDeploymentTarget(req.Target)
	if err != nil {
		return DeploymentRecord{}, err
	}
	req.Account = strings.TrimSpace(req.Account)
	if err := validateDeploymentChoice(req.Method, req.Context, req.Account); err != nil {
		return DeploymentRecord{}, err
	}
	m.mu.RLock()
	state := m.agents[req.SourceAgentID]
	previous, retained := m.offlineAgents[req.SourceAgentID]
	if state != nil {
		previous = state.inventory
	}
	lookup := m.deploymentArtifactLookup
	m.mu.RUnlock()
	if state == nil && !retained {
		return DeploymentRecord{}, errors.New("source agent does not exist")
	}
	if previous.OS != "windows" {
		return DeploymentRecord{}, errors.New("source agent must report Windows")
	}
	if lookup == nil {
		return DeploymentRecord{}, errors.New("artifact catalog is unavailable")
	}
	artifact, err := lookup(req.ArtifactID)
	if err != nil || artifact.ID == "" {
		return DeploymentRecord{}, errors.New("selected artifact does not exist")
	}
	if artifact.Platform != "windows" || artifact.Revoked {
		return DeploymentRecord{}, errors.New("select an active Windows artifact")
	}
	if req.Method == "service-control" && !artifact.ServiceCapable {
		return DeploymentRecord{}, errors.New("Service Control requires a service-capable Windows artifact")
	}
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return DeploymentRecord{}, err
	}
	now := time.Now().UTC()
	actor := boundActionFromContext(ctx)
	source := actor.Source
	if source == "" {
		source = "server_console"
	}
	record := DeploymentRecord{ID: hex.EncodeToString(key[:]), SourceAgentID: req.SourceAgentID, Target: target, ArtifactID: artifact.ID, ProfileID: artifact.ProfileID, Profile: artifact.Profile, ArtifactSHA256: artifact.SHA256, Method: req.Method, Context: req.Context, Account: req.Account, OperatorID: actor.OperatorID, OperatorName: actor.DisplayName, RequestedFrom: source, CreatedAt: now, UpdatedAt: now, State: "created", Progress: "Deployment request recorded"}
	store, err := m.deploymentStore()
	if err != nil {
		return DeploymentRecord{}, err
	}
	if err := store.CreateDeployment(record); err != nil {
		return DeploymentRecord{}, err
	}
	m.PublishEvent("deployment.changed", record.ID)
	return record, nil
}

func (m *Manager) prepareDeployment(id string) (DeploymentRecord, error) {
	store, err := m.deploymentStore()
	if err != nil {
		return DeploymentRecord{}, err
	}
	record, err := store.Deployment(id)
	if err != nil {
		return DeploymentRecord{}, err
	}
	if record.State != "created" {
		return DeploymentRecord{}, ErrDeploymentConflict
	}
	m.mu.RLock()
	state := m.agents[record.SourceAgentID]
	sourceReady := state != nil && state.inventoryReady && state.inventory.OS == "windows"
	lookup := m.deploymentArtifactLookup
	m.mu.RUnlock()
	if !sourceReady {
		return DeploymentRecord{}, errors.New("source Windows agent must be connected and inventory ready")
	}
	if lookup == nil {
		return DeploymentRecord{}, errors.New("artifact catalog is unavailable")
	}
	artifact, err := lookup(record.ArtifactID)
	if err != nil || artifact.ID == "" || artifact.Revoked || artifact.Platform != "windows" || artifact.SHA256 != record.ArtifactSHA256 {
		return DeploymentRecord{}, errors.New("selected Windows artifact is unavailable or changed")
	}
	if record.Method == "service-control" && !artifact.ServiceCapable {
		return DeploymentRecord{}, errors.New("Service Control requires a service-capable Windows artifact")
	}
	now := time.Now().UTC()
	record, err = store.ChangeDeployment(id, func(item *DeploymentRecord) error {
		if item.State != "created" {
			return ErrDeploymentConflict
		}
		item.State, item.UpdatedAt, item.PreparedAt = "prepared", now, &now
		item.Progress, item.Error = "Ready for Windows method execution", ""
		return nil
	})
	if err == nil {
		m.PublishEvent("deployment.changed", id)
	}
	return record, err
}

// AdvanceDeployment is called by the method executor, never directly by the
// operator UI. Waiting starts only after a real method Job has been accepted.
func (m *Manager) AdvanceDeployment(id string, update DeploymentProgress) error {
	store, err := m.deploymentStore()
	if err != nil {
		return err
	}
	if len(update.Progress) > 1024 || len(update.Failure) > 1024 || len(update.JobID) > 128 || len(update.TransferID) > 128 || len(update.InstallPath) > 1024 || len(update.ServiceName) > 256 || len(update.TaskName) > 256 {
		return errors.New("deployment status text is too long")
	}
	_, err = store.ChangeDeployment(id, func(item *DeploymentRecord) error {
		if item.State != "dispatching" && item.State != "waiting" {
			return ErrDeploymentConflict
		}
		if update.State != "waiting" && update.State != "failed" {
			return ErrDeploymentConflict
		}
		if update.State == "waiting" && update.Failure != "" {
			return errors.New("waiting deployment cannot contain a failure")
		}
		now := time.Now().UTC()
		item.State, item.UpdatedAt = update.State, now
		item.Progress, item.Error = update.Progress, update.Failure
		if update.JobID != "" {
			item.JobID = update.JobID
		}
		if update.TransferID != "" {
			item.TransferID = update.TransferID
		}
		if update.InstallPath != "" {
			item.InstallPath = update.InstallPath
		}
		if update.ServiceName != "" {
			item.ServiceName = update.ServiceName
		}
		if update.TaskName != "" {
			item.TaskName = update.TaskName
		}
		if update.State == "waiting" && item.WaitingAt == nil {
			item.WaitingAt = &now
		}
		if update.State == "failed" {
			if update.Failure == "" {
				return errors.New("failed deployment needs an error")
			}
			item.CompletedAt = &now
		}
		return nil
	})
	if err == nil {
		m.PublishEvent("deployment.changed", id)
	}
	return err
}

func (m *Manager) startDeployment(ctx context.Context, id string, request DeploymentStartRequest) error {
	m.mu.RLock()
	executor := m.deploymentExecutor
	m.mu.RUnlock()
	if executor == nil {
		return errors.New("Windows deployment methods are not connected yet")
	}
	store, err := m.deploymentStore()
	if err != nil {
		return err
	}
	record, err := store.Deployment(id)
	if err != nil {
		return err
	}
	m.mu.RLock()
	state := m.agents[record.SourceAgentID]
	lookup := m.deploymentArtifactLookup
	sourceReady := state != nil && state.inventoryReady && state.inventory.OS == "windows"
	m.mu.RUnlock()
	if !sourceReady || lookup == nil {
		return errors.New("source Windows agent or artifact catalog is unavailable")
	}
	artifact, err := lookup(record.ArtifactID)
	if err != nil || artifact.ID == "" || artifact.Revoked || artifact.Platform != "windows" || artifact.SHA256 != record.ArtifactSHA256 {
		return errors.New("selected Windows artifact is unavailable or changed")
	}
	if record.Context == "named-account" {
		return errors.New("named-account deployments require a future credential integration")
	}
	plan, err := executor.Preflight(ctx, record, request)
	if err != nil {
		return err
	}
	source := m.Get(record.SourceAgentID)
	if source == nil {
		return errors.New("source agent is disconnected")
	}
	claimed, err := store.ChangeDeployment(id, func(item *DeploymentRecord) error {
		if item.State != "prepared" {
			return ErrDeploymentConflict
		}
		item.State, item.UpdatedAt, item.Progress = "dispatching", time.Now().UTC(), "Starting Windows method"
		item.DeliveryType, item.DeliveryID, item.InstallPath = plan.DeliveryType, plan.DeliveryID, plan.InstallPath
		return nil
	})
	if err != nil {
		return err
	}
	m.PublishEvent("deployment.changed", id)
	if err := executor.Start(ctx, claimed, plan, source, func(update DeploymentProgress) error {
		return m.AdvanceDeployment(id, update)
	}); err != nil {
		failure := err.Error()
		if len(failure) > 1024 {
			failure = failure[:1024]
		}
		_ = m.AdvanceDeployment(id, DeploymentProgress{State: "failed", Progress: "Method start failed", Failure: failure})
		return err
	}
	current, err := store.Deployment(id)
	if err != nil {
		return err
	}
	if current.State == "dispatching" {
		failure := "Windows method did not report that it started"
		_ = m.AdvanceDeployment(id, DeploymentProgress{State: "failed", Progress: "Method start failed", Failure: failure})
		return errors.New(failure)
	}
	return nil
}

func (m *Manager) linkDeployment(id, agentID string, automatic bool) (DeploymentRecord, error) {
	store, err := m.deploymentStore()
	if err != nil {
		return DeploymentRecord{}, err
	}
	m.mu.RLock()
	state := m.agents[agentID]
	var info AgentInfo
	ready := state != nil && state.inventoryReady
	if ready {
		info = state.inventory
	}
	m.mu.RUnlock()
	if !ready {
		return DeploymentRecord{}, errors.New("resulting agent must be connected and inventory ready")
	}
	record, err := store.ChangeDeployment(id, func(item *DeploymentRecord) error {
		if item.State != "waiting" || item.ArtifactID != info.ArtifactID || item.WaitingAt == nil {
			return ErrDeploymentConflict
		}
		if info.OS != "windows" || info.Connected.Before(*item.WaitingAt) || !deploymentAgentMatches(*item, info, false) {
			return errors.New("agent artifact or target does not match this deployment")
		}
		if automatic && !deploymentAgentMatches(*item, info, true) {
			return ErrDeploymentConflict
		}
		now := time.Now().UTC()
		item.State, item.UpdatedAt, item.CompletedAt = "completed", now, &now
		item.ResultAgentID = agentID
		item.ResultRelationship = "deployed_from"
		item.Progress, item.Error = "Agent enrolled and linked", ""
		return nil
	})
	if err == nil {
		m.PublishEvent("deployment.changed", id)
	}
	return record, err
}

func deploymentAgentMatches(record DeploymentRecord, agent AgentInfo, automatic bool) bool {
	if record.ArtifactID != agent.ArtifactID || agent.ID == record.SourceAgentID {
		return false
	}
	if strings.EqualFold(strings.TrimSuffix(agent.Hostname, "."), record.Target) {
		return true
	}
	// An IP target is not the same as an observed hostname. A relay lineage
	// plus the exact peer address is the only safe automatic IP alternative.
	if ip := net.ParseIP(record.Target); ip != nil {
		for _, address := range agent.Interfaces {
			_, value, ok := strings.Cut(address, "=")
			if !ok {
				continue
			}
			if candidate, _, err := net.ParseCIDR(value); err == nil && candidate.Equal(ip) {
				return !automatic || agent.Via == record.SourceAgentID
			}
		}
		remote, _, err := net.SplitHostPort(agent.Remote)
		return err == nil && net.ParseIP(remote) != nil && net.ParseIP(remote).Equal(ip) && (!automatic || agent.Via == record.SourceAgentID)
	}
	return false
}

func (m *Manager) correlateDeployment(agentID string) {
	m.mu.RLock()
	state := m.agents[agentID]
	store := m.operations
	if state == nil || !state.inventoryReady || !state.newIdentity || store == nil {
		m.mu.RUnlock()
		return
	}
	info := state.inventory
	m.mu.RUnlock()
	if info.ArtifactID == "" {
		return
	}
	items, err := store.WaitingDeployments(info.ArtifactID)
	if err != nil {
		return
	}
	var matches []DeploymentRecord
	for _, item := range items {
		if item.WaitingAt != nil && !info.Connected.Before(*item.WaitingAt) && deploymentAgentMatches(item, info, true) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		_, _ = m.linkDeployment(matches[0].ID, agentID, true)
	}
}

func (m *Manager) deploymentHTTPHandlers(muxer *http.ServeMux) {
	muxer.HandleFunc("GET /v1/deployments", func(w http.ResponseWriter, r *http.Request) {
		store, err := m.deploymentStore()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		query := r.URL.Query()
		if len(query) > 1 || len(query) == 1 && len(query["source_agent_id"]) != 1 {
			http.Error(w, "invalid deployment filter", http.StatusBadRequest)
			return
		}
		items, err := store.Deployments(query.Get("source_agent_id"))
		if err != nil {
			http.Error(w, "deployments unavailable", http.StatusInternalServerError)
			return
		}
		jsonReply(w, http.StatusOK, items)
	})
	muxer.HandleFunc("POST /v1/deployments", func(w http.ResponseWriter, r *http.Request) {
		var req createDeploymentRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid deployment request", http.StatusBadRequest)
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			http.Error(w, "unexpected request data", http.StatusBadRequest)
			return
		}
		item, err := m.createDeployment(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonReply(w, http.StatusCreated, item)
	})
	muxer.HandleFunc("GET /v1/deployments/{id}", func(w http.ResponseWriter, r *http.Request) {
		store, err := m.deploymentStore()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		item, err := store.Deployment(r.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "deployment unavailable", http.StatusInternalServerError)
			return
		}
		jsonReply(w, http.StatusOK, item)
	})
	muxer.HandleFunc("POST /v1/deployments/{id}/prepare", func(w http.ResponseWriter, r *http.Request) {
		item, err := m.prepareDeployment(r.PathValue("id"))
		deploymentTransitionReply(w, r, item, err)
	})
	muxer.HandleFunc("POST /v1/deployments/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		var request DeploymentStartRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil {
			http.Error(w, "invalid deployment start request", http.StatusBadRequest)
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			http.Error(w, "unexpected request data", http.StatusBadRequest)
			return
		}
		if err := m.startDeployment(r.Context(), r.PathValue("id"), request); err != nil {
			code := http.StatusConflict
			if strings.Contains(err.Error(), "not connected yet") {
				code = http.StatusNotImplemented
			}
			http.Error(w, err.Error(), code)
			return
		}
		store, _ := m.deploymentStore()
		item, err := store.Deployment(r.PathValue("id"))
		deploymentTransitionReply(w, r, item, err)
	})
	muxer.HandleFunc("POST /v1/deployments/{id}/link", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AgentID string `json:"agent_id"`
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 256))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || req.AgentID == "" {
			http.Error(w, "agent_id is required", http.StatusBadRequest)
			return
		}
		item, err := m.linkDeployment(r.PathValue("id"), req.AgentID, false)
		deploymentTransitionReply(w, r, item, err)
	})
}

func deploymentTransitionReply(w http.ResponseWriter, r *http.Request, item DeploymentRecord, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
	} else {
		jsonReply(w, http.StatusOK, item)
	}
}

func (r DeploymentRecord) String() string {
	return fmt.Sprintf("%s %s → %s (%s, %s)", r.ID, r.SourceAgentID, r.Target, r.Method, r.State)
}
