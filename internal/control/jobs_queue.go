package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

// queuedJobRequest is retained by the server while an agent is intentionally
// sleeping. The original typed job API validates the payload before enqueue.
type queuedJobRequest struct {
	Kind      string             `json:"kind"`
	Language  string             `json:"language,omitempty"`
	Argv      []string           `json:"argv,omitempty"`
	Source    []byte             `json:"source,omitempty"`
	Input     []byte             `json:"input,omitempty"`
	Arguments []byte             `json:"arguments,omitempty"`
	Exec      *pivot.ExecRequest `json:"exec,omitempty"`
	Screen    int                `json:"screen,omitempty"`
	Actor     actionContext      `json:"actor,omitempty"`
}

const maxQueuedJobs = 32
const maxQueuedJobBytes = 64 << 20

func (r queuedJobRequest) size() int {
	n := len(r.Source) + len(r.Input) + len(r.Arguments) + len(r.Language)
	for _, arg := range r.Argv {
		n += len(arg)
	}
	if r.Exec != nil {
		n += len(r.Exec.Builtin)
		for _, arg := range r.Exec.Argv {
			n += len(arg)
		}
		for _, arg := range r.Exec.Args {
			n += len(arg)
		}
	}
	return n
}

// Check-in agents always submit through the durable queue, including during
// their brief online window. This removes a race between the operator click
// and the agent's sleep handshake. A truly lost agent still rejects new work.
func (m *Manager) queueJobIfSleeping(owner uint64, agentID string, request queuedJobRequest) (JobInfo, bool, error) {
	m.mu.Lock()
	live := m.agents[agentID]
	offline := m.offlineAgents[agentID]
	checkin := live != nil && live.inventoryReady && live.inventory.SleepSupported && live.inventory.Sleep.IntervalSeconds > 0
	sleeping := live == nil && offline.ConnectionState == "sleeping" && !offline.SleepLostAfter.IsZero() && time.Now().Before(offline.SleepLostAfter)
	if !checkin && !sleeping {
		m.mu.Unlock()
		return JobInfo{}, false, nil
	}
	if m.operations == nil {
		m.mu.Unlock()
		return JobInfo{}, true, errors.New("operations store unavailable")
	}
	for _, job := range m.jobs {
		if job.info.AgentID == agentID && job.request != nil && (job.request.Kind == "shutdown" || job.request.Kind == "session-kill") && (job.info.State == "queued" || job.info.State == "dispatching") {
			m.mu.Unlock()
			return JobInfo{}, true, errors.New("an agent lifecycle action is already queued; wait for it before submitting more work")
		}
	}
	count, bytes := 0, request.size()
	for _, job := range m.jobs {
		if job.request != nil && (job.info.State == "queued" || job.info.State == "dispatching") {
			count++
			bytes += job.request.size()
		}
	}
	if len(m.jobs) >= 512 || count >= maxQueuedJobs || bytes > maxQueuedJobBytes {
		m.mu.Unlock()
		return JobInfo{}, true, errors.New("queued job limit reached")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		m.mu.Unlock()
		return JobInfo{}, true, err
	}
	now := time.Now().UTC()
	info := JobInfo{ID: hex.EncodeToString(random[:]), AgentID: agentID, Kind: request.Kind, Language: request.Language, Argv: append([]string(nil), request.Argv...), Started: now, QueuedAt: &now, State: "queued"}
	if request.Exec != nil {
		info.Builtin = request.Exec.Builtin
		info.Argv = append([]string(nil), request.Exec.Argv...)
		info.Args = append([]string(nil), request.Exec.Args...)
	}
	ownerKey := ""
	if client := m.clients[owner]; owner != 0 && client != nil {
		ownerKey = client.peer.Snapshot().AgentID
	}
	if err := m.operations.SaveQueuedJob(info, ownerKey, request); err != nil {
		m.mu.Unlock()
		return JobInfo{}, true, fmt.Errorf("persist queued job: %w", err)
	}
	m.jobs[info.ID] = &jobState{info: info, owner: owner, ownerKey: ownerKey, request: &request}
	var stream *mux.Mux
	if live != nil {
		stream = live.mux
	}
	m.mu.Unlock()
	m.PublishEvent("job.queued", info.ID)
	if stream != nil {
		go m.dispatchQueuedJobs(agentID, stream)
	}
	return info, true, nil
}

func (m *Manager) dispatchQueuedJobs(agentID string, stream *mux.Mux) {
	m.mu.Lock()
	if m.jobDispatching[agentID] {
		m.mu.Unlock()
		return
	}
	m.jobDispatching[agentID] = true
	for {
		state := m.agents[agentID]
		if state == nil || state.mux != stream || !state.inventoryReady {
			var next *mux.Mux
			if state != nil && state.mux != stream && state.inventoryReady {
				next = state.mux
			}
			delete(m.jobDispatching, agentID)
			m.mu.Unlock()
			if next != nil {
				go m.dispatchQueuedJobs(agentID, next)
			}
			return
		}
		// A request can arrive after sleep commit but before transport teardown.
		// Leave it queued for the next authenticated callback.
		stream.Unquiesce()
		if stream.IsSleepCommitted() {
			delete(m.jobDispatching, agentID)
			m.mu.Unlock()
			return
		}
		var jobs []*jobState
		for _, job := range m.jobs {
			if job.info.AgentID == agentID && job.info.State == "queued" && job.request != nil {
				jobs = append(jobs, job)
			}
		}
		if len(jobs) == 0 {
			delete(m.jobDispatching, agentID)
			m.mu.Unlock()
			return
		}
		sort.Slice(jobs, func(i, j int) bool {
			terminal := func(kind string) bool { return kind == "shutdown" || kind == "session-kill" }
			if terminal(jobs[i].info.Kind) != terminal(jobs[j].info.Kind) {
				return !terminal(jobs[i].info.Kind)
			}
			return jobs[i].info.Started.Before(jobs[j].info.Started)
		})
		job := jobs[0]
		job.info.State = "dispatching"
		if m.operations == nil || m.operations.SaveJob(job.info, job.ownerKey, "") != nil {
			job.info.State = "queued"
			delete(m.jobDispatching, agentID)
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		m.dispatchQueuedJob(agentID, stream, job)
		m.mu.Lock()
	}
}

func (m *Manager) dispatchQueuedJob(agentID string, stream *mux.Mux, job *jobState) {
	m.mu.RLock()
	if job.info.State != "dispatching" || job.request == nil {
		m.mu.RUnlock()
		return
	}
	request := *job.request
	m.mu.RUnlock()
	if request.Kind == "exec" {
		m.dispatchQueuedExec(agentID, stream, job, request.Exec)
		return
	}
	if request.Kind == "screenshot" {
		m.dispatchQueuedScreenshot(agentID, stream, job, request)
		return
	}
	if request.Kind == "shutdown" || request.Kind == "session-kill" {
		m.dispatchQueuedLifecycle(agentID, stream, job, request.Kind)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var session *pivot.InteractiveSession
	var err error
	switch request.Kind {
	case "command":
		session, err = pivot.OpenInteractive(ctx, stream, pivot.InteractiveRequest{Argv: request.Argv})
	case "script":
		session, err = pivot.OpenScript(ctx, stream, request.Language, request.Source)
	case "wasm":
		session, err = pivot.OpenWASM(ctx, stream, request.Source, request.Argv, request.Input)
	case "native":
		session, err = pivot.OpenNative(ctx, stream, request.Source, request.Argv, request.Input)
	case "bof":
		session, err = pivot.OpenBOF(ctx, stream, request.Source, request.Arguments)
	default:
		err = fmt.Errorf("unknown queued job kind %q", request.Kind)
	}
	m.mu.Lock()
	if job.info.State != "dispatching" {
		m.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
		return
	}
	current := m.agents[agentID]
	store := m.operations
	if err != nil && stream.IsSleepCommitted() && session == nil {
		job.info.State = "queued"
		if store != nil {
			_ = store.SaveJob(job.info, job.ownerKey, "")
		}
		m.mu.Unlock()
		m.PublishEvent("job.queued", job.info.ID)
		return
	}
	if current == nil || current.mux != stream || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		m.interruptQueuedDispatchLocked(job, "Agent connection ended during dispatch; execution may have started. Review before retrying.")
		if session != nil {
			_ = session.Close()
		}
		return
	}
	if err != nil {
		now := time.Now().UTC()
		job.info.State, job.info.Ended, job.info.OutputError = "failed", &now, err.Error()
		info, ownerKey := job.info, job.ownerKey
		job.request = nil
		if store != nil {
			if saveErr := store.SaveJob(info, ownerKey, ""); saveErr != nil {
				log.Printf("persist queued job failure: %v", saveErr)
			}
			if deleteErr := store.DeleteQueuedJob(info.ID); deleteErr != nil {
				log.Printf("remove queued job request: %v", deleteErr)
			}
		}
		m.mu.Unlock()
		m.PublishEvent("job.failed", info.ID)
		return
	}
	previous := job.info.Started
	job.info.Started = time.Now().UTC()
	job.info.State = "running"
	job.agent, job.session = stream, session
	info, ownerKey := job.info, job.ownerKey
	if store != nil {
		if saveErr := store.SaveJob(info, ownerKey, ""); saveErr != nil {
			job.info.Started = previous
			job.agent, job.session = nil, nil
			m.interruptQueuedDispatchLocked(job, "The agent accepted this task, but the server could not persist its running state. Review before retrying.")
			_ = session.Close()
			return
		}
		if deleteErr := store.DeleteQueuedJob(info.ID); deleteErr != nil {
			log.Printf("remove dispatched job request: %v", deleteErr)
		}
	}
	job.request = nil
	m.mu.Unlock()
	m.PublishEvent("job.running", info.ID)
	go m.collectJob(job)
}

func (m *Manager) dispatchQueuedExec(agentID string, stream *mux.Mux, job *jobState, request *pivot.ExecRequest) {
	if request == nil {
		m.mu.Lock()
		m.interruptQueuedDispatchLocked(job, "Queued command request is missing.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	result, err := pivot.ExecuteRequest(ctx, stream, *request)
	cancel()
	m.mu.Lock()
	if job.info.State != "dispatching" {
		m.mu.Unlock()
		return
	}
	current := m.agents[agentID]
	if err != nil || current == nil || current.mux != stream {
		m.interruptQueuedDispatchLocked(job, "Agent connection ended during command dispatch; execution may have started. Review before retrying.")
		return
	}
	now := time.Now().UTC()
	job.info.Started, job.info.Ended, job.info.ExecResult = now, &now, &result
	job.info.Output = result.Stdout + result.Stderr
	if result.Error != "" {
		job.info.OutputError = result.Error
		job.info.State = "failed"
	} else {
		job.info.State = "completed"
	}
	job.info.OutputBytes = uint64(len(job.info.Output))
	if len(job.info.Output) > jobOutputLimit {
		job.info.Output = job.info.Output[len(job.info.Output)-jobOutputLimit:]
		job.info.OutputTruncated = true
	}
	job.info.ExitCode = &result.ExitCode
	job.request = nil
	info, ownerKey, store := job.info, job.ownerKey, m.operations
	if store != nil {
		if err := store.SaveJob(info, ownerKey, ""); err != nil {
			log.Printf("persist queued command result: %v", err)
		}
		if err := store.DeleteQueuedJob(info.ID); err != nil {
			log.Printf("remove queued command request: %v", err)
		}
	}
	m.mu.Unlock()
	if request.Builtin != "" && retainedHostOperation(request.Builtin) && len(request.Args) == 0 {
		m.retainHostResult(agentID, request.Builtin, result)
	}
	m.PublishEvent("job."+info.State, info.ID)
}

func (m *Manager) dispatchQueuedScreenshot(agentID string, stream *mux.Mux, job *jobState, request queuedJobRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	entry, err := m.captureScreenshot(ctx, agentID, request.Screen, request.Actor)
	cancel()
	m.mu.Lock()
	if job.info.State != "dispatching" {
		m.mu.Unlock()
		return
	}
	current := m.agents[agentID]
	if current == nil || current.mux != stream {
		m.interruptQueuedDispatchLocked(job, "Agent connection ended during screenshot capture; review server history before retrying.")
		return
	}
	now := time.Now().UTC()
	job.info.Started, job.info.Ended = now, &now
	if err != nil {
		job.info.State, job.info.OutputError = "failed", err.Error()
	} else {
		job.info.State, job.info.ScreenshotID = "completed", entry.ID
		job.info.Output = fmt.Sprintf("Screen %d captured to server history: %s", entry.Screen, entry.ID)
		job.info.OutputBytes = uint64(len(job.info.Output))
	}
	job.request = nil
	info, ownerKey, store := job.info, job.ownerKey, m.operations
	if store != nil {
		if saveErr := store.SaveJob(info, ownerKey, ""); saveErr != nil {
			log.Printf("persist screenshot job: %v", saveErr)
		}
		if deleteErr := store.DeleteQueuedJob(info.ID); deleteErr != nil {
			log.Printf("remove screenshot job request: %v", deleteErr)
		}
	}
	m.mu.Unlock()
	m.PublishEvent("job."+info.State, info.ID)
}

func (m *Manager) dispatchQueuedLifecycle(agentID string, stream *mux.Mux, job *jobState, kind string) {
	// A lifecycle action is terminal for this session. Let earlier queued jobs
	// finish on the live connection before closing it.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.RLock()
		active := false
		for _, other := range m.jobs {
			if other != job && other.info.AgentID == agentID && (other.info.State == "running" || other.info.State == "dispatching") {
				active = true
				break
			}
		}
		m.mu.RUnlock()
		if !active {
			break
		}
		select {
		case <-stream.Done():
			m.requeueUnstartedLifecycle(job)
			return
		case <-ticker.C:
		}
	}
	m.mu.Lock()
	if job.info.State != "dispatching" || job.request == nil {
		m.mu.Unlock()
		return
	}
	current := m.agents[agentID]
	if current == nil || current.mux != stream || stream.IsSleepCommitted() {
		job.info.State = "queued"
		if m.operations != nil {
			_ = m.operations.SaveJob(job.info, job.ownerKey, "")
		}
		m.mu.Unlock()
		m.PublishEvent("job.queued", job.info.ID)
		return
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	var err error
	if kind == "shutdown" {
		err = m.shutdownAgentOnStream(ctx, agentID, stream)
	} else {
		select {
		case <-stream.Done():
			err = errors.New("session ended before the queued kill")
		default:
			err = stream.Close()
		}
	}
	cancel()
	m.mu.Lock()
	if job.info.State != "dispatching" {
		m.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	job.info.Started, job.info.Ended = now, &now
	if err != nil {
		job.info.State, job.info.OutputError = "interrupted", "Lifecycle request may have reached the agent: "+err.Error()
	} else {
		job.info.State = "completed"
		job.info.Output = "Requested agent lifecycle action completed."
		job.info.OutputBytes = uint64(len(job.info.Output))
	}
	job.request = nil
	info, ownerKey, store := job.info, job.ownerKey, m.operations
	if store != nil {
		if saveErr := store.SaveJob(info, ownerKey, ""); saveErr != nil {
			log.Printf("persist lifecycle job: %v", saveErr)
		}
		if deleteErr := store.DeleteQueuedJob(info.ID); deleteErr != nil {
			log.Printf("remove lifecycle job request: %v", deleteErr)
		}
	}
	m.mu.Unlock()
	m.PublishEvent("job."+info.State, info.ID)
}

func (m *Manager) requeueUnstartedLifecycle(job *jobState) {
	m.mu.Lock()
	if job.info.State == "dispatching" && job.request != nil {
		job.info.State = "queued"
		if m.operations != nil {
			_ = m.operations.SaveJob(job.info, job.ownerKey, "")
		}
		m.mu.Unlock()
		m.PublishEvent("job.queued", job.info.ID)
		return
	}
	m.mu.Unlock()
}

// The manager lock is held. An attempted dispatch is not replayed when its
// outcome cannot be established; replay could execute a command twice.
func (m *Manager) interruptQueuedDispatchLocked(job *jobState, reason string) {
	now := time.Now().UTC()
	job.info.State, job.info.Ended, job.info.OutputError = "interrupted", &now, reason
	job.request = nil
	info, ownerKey, store := job.info, job.ownerKey, m.operations
	if store != nil {
		if err := store.SaveJob(info, ownerKey, ""); err != nil {
			log.Printf("persist interrupted queued job: %v", err)
		}
		if err := store.DeleteQueuedJob(info.ID); err != nil {
			log.Printf("remove interrupted queued job request: %v", err)
		}
	}
	m.mu.Unlock()
	m.PublishEvent("job.interrupted", info.ID)
}
