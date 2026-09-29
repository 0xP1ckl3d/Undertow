package control

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

const jobOutputLimit = 256 << 10

type JobInfo struct {
	ID              string     `json:"id"`
	AgentID         string     `json:"agent_id"`
	Kind            string     `json:"kind,omitempty"`
	Language        string     `json:"language,omitempty"`
	Argv            []string   `json:"argv"`
	Started         time.Time  `json:"started"`
	Ended           *time.Time `json:"ended,omitempty"`
	State           string     `json:"state"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	Output          string     `json:"output,omitempty"`
	OutputBytes     uint64     `json:"output_bytes"`
	OutputTruncated bool       `json:"output_truncated,omitempty"`
}

type jobState struct {
	info    JobInfo
	owner   uint64
	agent   *mux.Mux
	session *pivot.InteractiveSession
	output  []byte
}

func (m *Manager) StartJob(ctx context.Context, owner uint64, agentID string, argv []string) (JobInfo, error) {
	if len(argv) == 0 {
		return JobInfo{}, errors.New("job start needs a program")
	}
	m.mu.RLock()
	state := m.agents[agentID]
	count := len(m.jobs)
	m.mu.RUnlock()
	if state == nil {
		return JobInfo{}, errors.New("agent is not connected")
	}
	if count >= 512 {
		return JobInfo{}, errors.New("job limit reached")
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenInteractive(startCtx, state.mux, pivot.InteractiveRequest{Argv: argv})
	if err != nil {
		return JobInfo{}, err
	}
	return m.registerJob(owner, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "command", Argv: append([]string(nil), argv...)})
}

func (m *Manager) StartScriptJob(ctx context.Context, owner uint64, agentID, language string, source []byte) (JobInfo, error) {
	if len(source) == 0 || len(source) > pivot.ScriptSourceLimit {
		return JobInfo{}, errors.New("script source exceeds the 1 MiB limit or is empty")
	}
	m.mu.RLock()
	state := m.agents[agentID]
	count := len(m.jobs)
	m.mu.RUnlock()
	if state == nil {
		return JobInfo{}, errors.New("agent is not connected")
	}
	if count >= 512 {
		return JobInfo{}, errors.New("job limit reached")
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenScript(startCtx, state.mux, language, source)
	if err != nil {
		return JobInfo{}, err
	}
	return m.registerJob(owner, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "script", Language: language})
}

func (m *Manager) registerJob(owner uint64, agentID string, agent *mux.Mux, session *pivot.InteractiveSession, info JobInfo) (JobInfo, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		session.Close()
		return JobInfo{}, err
	}
	now := time.Now().UTC()
	info.ID, info.Started, info.State = hex.EncodeToString(random[:]), now, "running"
	job := &jobState{info: info, owner: owner, agent: agent, session: session}
	m.mu.Lock()
	if current := m.agents[agentID]; current == nil || current.mux != agent {
		m.mu.Unlock()
		session.Close()
		return JobInfo{}, errors.New("agent disconnected")
	}
	m.jobs[job.info.ID] = job
	m.mu.Unlock()
	go m.collectJob(job)
	return job.info, nil
}

func (m *Manager) collectJob(job *jobState) {
	defer job.session.Close()
	for {
		kind, data, err := job.session.Read()
		if err != nil {
			m.finishJob(job, "failed", nil)
			return
		}
		switch kind {
		case pivot.InteractiveOutput, pivot.InteractiveStderr:
			m.mu.Lock()
			appendJobOutput(job, data)
			m.mu.Unlock()
		case pivot.InteractiveExit:
			if len(data) != 4 {
				m.finishJob(job, "failed", nil)
				return
			}
			code := int(int32(binary.BigEndian.Uint32(data)))
			state := "completed"
			if code != 0 {
				state = "failed"
			}
			m.finishJob(job, state, &code)
			return
		case pivot.InteractiveError:
			m.mu.Lock()
			appendJobOutput(job, data)
			m.mu.Unlock()
			m.finishJob(job, "failed", nil)
			return
		}
	}
}

func appendJobOutput(job *jobState, data []byte) {
	job.info.OutputBytes += uint64(len(data))
	if len(data) >= jobOutputLimit {
		job.output = append(job.output[:0], data[len(data)-jobOutputLimit:]...)
		job.info.OutputTruncated = true
		return
	}
	if excess := len(job.output) + len(data) - jobOutputLimit; excess > 0 {
		copy(job.output, job.output[excess:])
		job.output = job.output[:len(job.output)-excess]
		job.info.OutputTruncated = true
	}
	job.output = append(job.output, data...)
}

func (m *Manager) finishJob(job *jobState, state string, exitCode *int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job.info.State != "running" {
		return
	}
	now := time.Now().UTC()
	job.info.State, job.info.Ended, job.info.ExitCode = state, &now, exitCode
}

func (m *Manager) CancelJob(owner uint64, id string) error {
	m.mu.Lock()
	job := m.jobs[id]
	if job == nil || owner != 0 && job.owner != owner {
		m.mu.Unlock()
		return errors.New("job not found")
	}
	if job.info.State != "running" {
		m.mu.Unlock()
		return errors.New("job is not running")
	}
	now := time.Now().UTC()
	job.info.State, job.info.Ended = "cancelled", &now
	session := job.session
	m.mu.Unlock()
	return session.Close()
}

func (m *Manager) Job(owner uint64, id string, includeOutput bool) (JobInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job := m.jobs[id]
	if job == nil || owner != 0 && job.owner != owner {
		return JobInfo{}, errors.New("job not found")
	}
	info := job.info
	info.Argv = append([]string(nil), info.Argv...)
	if includeOutput {
		info.Output = string(job.output)
	}
	return info, nil
}

func (m *Manager) Jobs(owner uint64, agentID string) []JobInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]JobInfo, 0, len(m.jobs))
	for _, job := range m.jobs {
		if owner != 0 && job.owner != owner || agentID != "" && job.info.AgentID != agentID {
			continue
		}
		info := job.info
		info.Argv = append([]string(nil), info.Argv...)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

type jobOwnerKey struct{}

func jobOwner(ctx context.Context) uint64 {
	value, _ := ctx.Value(jobOwnerKey{}).(uint64)
	return value
}

func (m *Manager) jobHTTPHandlers(muxer *http.ServeMux) {
	muxer.HandleFunc("POST /v1/agents/{id}/jobs", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Argv []string `json:"argv"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8193)).Decode(&request); err != nil {
			http.Error(w, "invalid job request", 400)
			return
		}
		job, err := m.StartJob(r.Context(), jobOwner(r.Context()), r.PathValue("id"), request.Argv)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonReply(w, http.StatusCreated, job)
	})
	muxer.HandleFunc("POST /v1/agents/{id}/scripts/jobs", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Language string `json:"language"`
			Source   []byte `json:"source"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid script job request", 400)
			return
		}
		job, err := m.StartScriptJob(r.Context(), jobOwner(r.Context()), r.PathValue("id"), request.Language, request.Source)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonReply(w, http.StatusCreated, job)
	})
	muxer.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, m.Jobs(jobOwner(r.Context()), r.URL.Query().Get("agent_id")))
	})
	muxer.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, err := m.Job(jobOwner(r.Context()), r.PathValue("id"), false)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		jsonReply(w, 200, job)
	})
	muxer.HandleFunc("GET /v1/jobs/{id}/output", func(w http.ResponseWriter, r *http.Request) {
		job, err := m.Job(jobOwner(r.Context()), r.PathValue("id"), true)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		jsonReply(w, 200, job)
	})
	muxer.HandleFunc("POST /v1/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := m.CancelJob(jobOwner(r.Context()), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
