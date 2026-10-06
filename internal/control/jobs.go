package control

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"undertow/internal/bof"
	"undertow/internal/mux"
	"undertow/internal/nativemodule"
	"undertow/internal/pivot"
)

const jobOutputLimit = 256 << 10

type JobInfo struct {
	ID              string             `json:"id"`
	AgentID         string             `json:"agent_id"`
	Kind            string             `json:"kind,omitempty"`
	Language        string             `json:"language,omitempty"`
	Builtin         string             `json:"builtin,omitempty"`
	Argv            []string           `json:"argv"`
	Args            []string           `json:"args,omitempty"`
	QueuedAt        *time.Time         `json:"queued_at,omitempty"`
	Started         time.Time          `json:"started"`
	Ended           *time.Time         `json:"ended,omitempty"`
	State           string             `json:"state"`
	ExitCode        *int               `json:"exit_code,omitempty"`
	Output          string             `json:"output,omitempty"`
	OutputBytes     uint64             `json:"output_bytes"`
	OutputTruncated bool               `json:"output_truncated,omitempty"`
	OutputFile      string             `json:"output_file,omitempty"`
	OutputError     string             `json:"output_error,omitempty"`
	Files           []bof.FileArtifact `json:"files,omitempty"`
	ExecResult      *pivot.ExecResult  `json:"exec_result,omitempty"`
	ScreenshotID    string             `json:"screenshot_id,omitempty"`
	DeploymentID    string             `json:"deployment_id,omitempty"`
}

type jobState struct {
	info       JobInfo
	owner      uint64
	ownerKey   string
	agent      *mux.Mux
	session    *pivot.InteractiveSession
	output     []byte
	outputFile *os.File
	outputPath string
	diskBytes  uint64
	fileBytes  uint64
	request    *queuedJobRequest
}

func (m *Manager) QueueExecIfCheckIn(owner uint64, agentID string, request pivot.ExecRequest) (JobInfo, bool, error) {
	if err := pivot.ValidateExecRequest(request); err != nil {
		return JobInfo{}, false, err
	}
	return m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: "exec", Exec: &request})
}

func (m *Manager) QueueLifecycleIfCheckIn(owner uint64, agentID, kind string) (JobInfo, bool, error) {
	if kind != "shutdown" && kind != "session-kill" {
		return JobInfo{}, false, errors.New("invalid lifecycle action")
	}
	m.mu.RLock()
	info := m.offlineAgents[agentID]
	if live := m.agents[agentID]; live != nil {
		info = live.inventory
	}
	m.mu.RUnlock()
	if kind == "shutdown" && info.ArtifactID == "" {
		return JobInfo{}, false, errors.New("agent shutdown requires a configured agent; use session kill for a manual agent")
	}
	return m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: kind})
}

func (m *Manager) StartJob(ctx context.Context, owner uint64, agentID string, argv []string) (JobInfo, error) {
	if len(argv) == 0 {
		return JobInfo{}, errors.New("job start needs a program")
	}
	if info, queued, err := m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: "command", Argv: argv}); queued {
		return info, err
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
	if err := m.jobOutputReady(); err != nil {
		return JobInfo{}, err
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
	return m.startScriptJob(ctx, owner, agentID, language, source, "", true)
}

// StartDeploymentCommandJob runs one native Windows management command as the
// durable Job for a deployment. argv is sent only to the live agent session;
// deployment history retains output and method metadata without raw command
// content or artifact retrieval capabilities.
func (m *Manager) StartDeploymentCommandJob(ctx context.Context, agentID, deploymentID, existingJobID string, argv []string) (JobInfo, error) {
	m.mu.RLock()
	state := m.agents[agentID]
	count := len(m.jobs)
	m.mu.RUnlock()
	if state == nil || state.mux == nil {
		return JobInfo{}, errors.New("agent disconnected")
	}
	if existingJobID == "" && count >= 512 {
		return JobInfo{}, errors.New("job limit reached")
	}
	if err := m.jobOutputReady(); err != nil {
		return JobInfo{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenInteractive(startCtx, state.mux, pivot.InteractiveRequest{Argv: argv, NoPTY: true})
	if err != nil {
		return JobInfo{}, err
	}
	if existingJobID == "" {
		return m.registerJob(0, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "deployment", DeploymentID: deploymentID})
	}
	m.mu.Lock()
	job := m.jobs[existingJobID]
	if job == nil || job.info.AgentID != agentID || job.info.DeploymentID != deploymentID || job.info.State != "dispatching" || job.request == nil || job.request.Kind != "deployment" {
		m.mu.Unlock()
		_ = session.Close()
		return JobInfo{}, errors.New("queued deployment Job is unavailable")
	}
	if current := m.agents[agentID]; current == nil || current.mux != state.mux {
		m.mu.Unlock()
		_ = session.Close()
		return JobInfo{}, errors.New("agent disconnected")
	}
	job.info.State = "running"
	job.info.Started = time.Now().UTC()
	queuedRequest := *job.request
	job.agent, job.session, job.request = state.mux, session, nil
	info, ownerKey, store := job.info, job.ownerKey, m.operations
	if store != nil {
		if err := store.SaveJob(info, ownerKey, ""); err != nil {
			job.info.State = "dispatching"
			job.agent, job.session = nil, nil
			job.request = &queuedRequest
			m.mu.Unlock()
			_ = session.Close()
			return JobInfo{}, err
		}
		if err := store.DeleteQueuedJob(info.ID); err != nil {
			log.Printf("remove dispatched deployment request: %v", err)
		}
	}
	m.mu.Unlock()
	m.PublishEvent("job.running", info.ID)
	go m.collectJob(job)
	return info, nil
}

func (m *Manager) startScriptJob(ctx context.Context, owner uint64, agentID, language string, source []byte, deploymentID string, allowQueue bool) (JobInfo, error) {
	if len(source) == 0 || len(source) > pivot.ScriptSourceLimit {
		return JobInfo{}, errors.New("script source exceeds the 1 MiB limit or is empty")
	}
	if allowQueue {
		if info, queued, err := m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: "script", Language: language, Source: source}); queued {
			return info, err
		}
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
	if err := m.jobOutputReady(); err != nil {
		return JobInfo{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenScript(startCtx, state.mux, language, source)
	if err != nil {
		return JobInfo{}, err
	}
	return m.registerJob(owner, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "script", Language: language, DeploymentID: deploymentID})
}

func (m *Manager) StartWASMJob(ctx context.Context, owner uint64, agentID string, module []byte, args []string, stdin []byte) (JobInfo, error) {
	if len(module) == 0 || len(module) > pivot.WASMModuleLimit || len(stdin) > pivot.WASMStdinLimit {
		return JobInfo{}, errors.New("WASM module or stdin exceeds its size limit")
	}
	if info, queued, err := m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: "wasm", Argv: args, Source: module, Input: stdin}); queued {
		return info, err
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
	if err := m.jobOutputReady(); err != nil {
		return JobInfo{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenWASM(startCtx, state.mux, module, args, stdin)
	if err != nil {
		return JobInfo{}, err
	}
	return m.registerJob(owner, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "wasm", Argv: append([]string(nil), args...)})
}

func (m *Manager) StartNativeJob(ctx context.Context, owner uint64, agentID string, module []byte, args []string, data []byte) (JobInfo, error) {
	if _, _, err := nativemodule.Parse(module); err != nil {
		return JobInfo{}, err
	}
	if _, err := nativemodule.EncodeArgs(args, data); err != nil {
		return JobInfo{}, err
	}
	if info, queued, err := m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: "native", Argv: args, Source: module, Input: data}); queued {
		return info, err
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
	if err := m.jobOutputReady(); err != nil {
		return JobInfo{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenNative(startCtx, state.mux, module, args, data)
	if err != nil {
		return JobInfo{}, err
	}
	return m.registerJob(owner, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "native", Argv: append([]string(nil), args...)})
}

func (m *Manager) StartAssemblyJob(ctx context.Context, owner uint64, agentID string, source []byte, args []string) (JobInfo, error) {
	if err := pivot.ValidateAssembly(source, args); err != nil {
		return JobInfo{}, err
	}
	if info, queued, err := m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: "assembly", Argv: args, Source: source}); queued {
		return info, err
	}
	m.mu.RLock()
	state, count := m.agents[agentID], len(m.jobs)
	m.mu.RUnlock()
	if state == nil {
		return JobInfo{}, errors.New("agent is not connected")
	}
	if count >= 512 {
		return JobInfo{}, errors.New("job limit reached")
	}
	if err := m.jobOutputReady(); err != nil {
		return JobInfo{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenAssembly(startCtx, state.mux, source, args)
	if err != nil {
		return JobInfo{}, err
	}
	return m.registerJob(owner, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "assembly", Argv: append([]string(nil), args...)})
}

func (m *Manager) StartBOFJob(ctx context.Context, owner uint64, agentID string, object, arguments []byte) (JobInfo, error) {
	compat, err := bof.Parse(object)
	if err != nil {
		return JobInfo{}, err
	}
	if !compat.Supported {
		return JobInfo{}, errors.New("unsupported BOF: " + compat.Errors[0])
	}
	if len(arguments) < 4 || len(arguments) > bof.MaxArguments+4 {
		return JobInfo{}, errors.New("invalid BOF argument buffer")
	}
	if info, queued, err := m.queueJobIfSleeping(owner, agentID, queuedJobRequest{Kind: "bof", Source: object, Arguments: arguments}); queued {
		return info, err
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
	if err := m.jobOutputReady(); err != nil {
		return JobInfo{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := pivot.OpenBOF(startCtx, state.mux, object, arguments)
	if err != nil {
		return JobInfo{}, err
	}
	return m.registerJob(owner, agentID, state.mux, session, JobInfo{AgentID: agentID, Kind: "bof"})
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
	if client := m.clients[owner]; owner != 0 && client != nil {
		job.ownerKey = client.peer.Snapshot().AgentID
	}
	if current := m.agents[agentID]; current == nil || current.mux != agent {
		m.mu.Unlock()
		session.Close()
		return JobInfo{}, errors.New("agent disconnected")
	}
	if m.jobOutput != nil && m.jobOutput.used >= m.jobOutput.totalLimit {
		m.mu.Unlock()
		session.Close()
		return JobInfo{}, errors.New("server job output storage is full; remove old job output before starting another job")
	}
	m.jobs[job.info.ID] = job
	store := m.operations
	m.mu.Unlock()
	if store != nil {
		if err := store.SaveJob(job.info, job.ownerKey, ""); err != nil {
			m.mu.Lock()
			delete(m.jobs, job.info.ID)
			m.mu.Unlock()
			_ = session.Close()
			return JobInfo{}, fmt.Errorf("persist job index: %w", err)
		}
	}
	go m.collectJob(job)
	return job.info, nil
}

func (m *Manager) collectJob(job *jobState) {
	defer job.session.Close()
	defer m.closeJobOutput(job)
	var files *bof.FileCollector
	defer func() {
		if files != nil {
			files.Close()
		}
	}()
	taskError := false
	for {
		kind, data, err := job.session.Read()
		if err != nil {
			m.closeJobOutput(job)
			m.finishJob(job, "failed", nil)
			return
		}
		switch kind {
		case pivot.InteractiveBOFCallback:
			m.mu.RLock()
			running := job.info.State == "running"
			m.mu.RUnlock()
			if !running {
				return
			}
			if files == nil {
				m.mu.RLock()
				store := m.jobOutput
				m.mu.RUnlock()
				if store == nil {
					m.failJobOutput(job, errors.New("job file storage is unavailable"))
					return
				}
				files = bof.NewFileCollector(filepath.Join(store.root, job.info.AgentID, job.info.ID+".files"))
				files.MaxBytes = store.perJobLimit
				files.OnReserve = func(size uint64) error {
					m.mu.Lock()
					defer m.mu.Unlock()
					if job.info.State != "running" {
						return errors.New("job is no longer running")
					}
					if job.fileBytes+job.info.OutputBytes > store.perJobLimit || size > store.perJobLimit-job.fileBytes-job.info.OutputBytes || size > store.totalLimit-store.used {
						return errors.New("server job file storage limit reached")
					}
					store.used += size
					job.diskBytes += size
					job.fileBytes += size
					return nil
				}
				files.OnRelease = func(size uint64) {
					m.mu.Lock()
					store.used -= size
					job.diskBytes -= size
					job.fileBytes -= size
					m.mu.Unlock()
				}
			}
			completed, fileErr := files.Consume(data)
			if fileErr != nil {
				m.failJobOutput(job, fileErr)
				return
			}
			if len(completed) != 0 {
				m.mu.Lock()
				job.info.Files = append(job.info.Files, completed...)
				info, ownerKey, outputPath, store := job.info, job.ownerKey, job.outputPath, m.operations
				m.mu.Unlock()
				if store != nil {
					if err := store.SaveJob(info, ownerKey, outputPath); err != nil {
						log.Printf("persist job file: %v", err)
					}
				}
			}
		case pivot.InteractiveOutput, pivot.InteractiveStderr:
			m.mu.Lock()
			if job.info.State != "running" {
				m.mu.Unlock()
				return
			}
			writeErr := m.appendJobOutput(job, data)
			m.mu.Unlock()
			if writeErr != nil {
				m.failJobOutput(job, writeErr)
				return
			}
		case pivot.InteractiveExit:
			if len(data) != 4 {
				m.closeJobOutput(job)
				m.finishJob(job, "failed", nil)
				return
			}
			code := int(int32(binary.BigEndian.Uint32(data)))
			state := "completed"
			if code != 0 || taskError {
				state = "failed"
			}
			if err := m.closeJobOutput(job); err != nil {
				m.failJobOutput(job, err)
				return
			}
			m.finishJob(job, state, &code)
			return
		case pivot.InteractiveError:
			m.mu.Lock()
			if job.info.State != "running" {
				m.mu.Unlock()
				return
			}
			writeErr := m.appendJobOutput(job, data)
			m.mu.Unlock()
			if writeErr != nil {
				m.failJobOutput(job, writeErr)
				return
			}
			taskError = true
		}
	}
}

func (m *Manager) appendJobOutput(job *jobState, data []byte) error {
	if store := m.jobOutput; store != nil {
		if store.used > store.totalLimit {
			return fmt.Errorf("server job output storage limit reached (%d bytes total)", store.totalLimit)
		}
		length := uint64(len(data))
		if job.info.OutputBytes+job.fileBytes > store.perJobLimit || length > store.perJobLimit-job.info.OutputBytes-job.fileBytes {
			return fmt.Errorf("server job output limit reached (%d bytes per job)", store.perJobLimit)
		}
		if job.outputFile == nil {
			if job.info.OutputBytes+length > store.totalLimit-store.used {
				return fmt.Errorf("server job output storage limit reached (%d bytes total)", store.totalLimit)
			}
			file, path, err := m.createJobOutput(job.info)
			if err != nil {
				return err
			}
			job.outputFile, job.outputPath = file, path
			job.info.OutputFile = path
			if len(job.output) > 0 {
				n, err := file.Write(job.output)
				store.used += uint64(n)
				job.diskBytes += uint64(n)
				if err != nil {
					return fmt.Errorf("spill existing job output: %w", err)
				}
				if n != len(job.output) {
					return errors.New("short write while spilling job output")
				}
			}
		}
	}
	if job.outputFile != nil {
		store := m.jobOutput
		length := uint64(len(data))
		if length > store.totalLimit-store.used {
			return fmt.Errorf("server job output storage limit reached (%d bytes total)", store.totalLimit)
		}
		n, err := job.outputFile.Write(data)
		if n > 0 {
			store.used += uint64(n)
			job.diskBytes += uint64(n)
			job.info.OutputBytes += uint64(n)
			appendJobPreview(job, data[:n])
		}
		if err != nil {
			return fmt.Errorf("write server job output: %w", err)
		}
		if n != len(data) {
			return errors.New("short write to server job output")
		}
		return nil
	}
	job.info.OutputBytes += uint64(len(data))
	appendJobPreview(job, data)
	return nil
}

func appendJobPreview(job *jobState, data []byte) {
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

func (m *Manager) failJobOutput(job *jobState, err error) {
	_ = m.closeJobOutput(job)
	m.mu.Lock()
	job.info.OutputError = err.Error()
	m.mu.Unlock()
	m.finishJob(job, "failed", nil)
}

func (m *Manager) closeJobOutput(job *jobState) error {
	m.mu.Lock()
	file := job.outputFile
	job.outputFile = nil
	m.mu.Unlock()
	if file == nil {
		return nil
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync server job output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close server job output: %w", err)
	}
	return nil
}

func (m *Manager) finishJob(job *jobState, state string, exitCode *int) {
	m.mu.Lock()
	if job.info.State != "running" {
		m.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	job.info.State, job.info.Ended, job.info.ExitCode = state, &now, exitCode
	info, ownerKey, outputPath, store := job.info, job.ownerKey, job.outputPath, m.operations
	m.mu.Unlock()
	if store != nil {
		if err := store.SaveJob(info, ownerKey, outputPath); err != nil {
			log.Printf("persist job result: %v", err)
		}
	}
	m.PublishEvent("job."+state, info.ID)
}

func (m *Manager) CancelJob(owner uint64, id string) error {
	m.mu.Lock()
	job := m.jobs[id]
	if job == nil || !m.jobVisibleTo(job, owner) {
		m.mu.Unlock()
		return errors.New("job not found")
	}
	if job.info.State != "running" && job.info.State != "queued" && job.info.State != "dispatching" {
		m.mu.Unlock()
		return errors.New("job is not running or queued")
	}
	wasQueued := job.info.State != "running"
	now := time.Now().UTC()
	job.info.State, job.info.Ended = "cancelled", &now
	job.request = nil
	file := job.outputFile
	job.outputFile = nil
	session := job.session
	info, ownerKey, outputPath, store := job.info, job.ownerKey, job.outputPath, m.operations
	m.mu.Unlock()
	if file != nil {
		_ = file.Sync()
		_ = file.Close()
	}
	if store != nil {
		if err := store.SaveJob(info, ownerKey, outputPath); err != nil {
			log.Printf("persist cancelled job: %v", err)
		}
		if wasQueued {
			if err := store.DeleteQueuedJob(id); err != nil {
				log.Printf("remove cancelled job request: %v", err)
			}
		}
	}
	m.PublishEvent("job.cancelled", id)
	if info.DeploymentID != "" {
		_ = m.AdvanceDeployment(info.DeploymentID, DeploymentProgress{
			State: "failed", Progress: "Deployment Job cancelled",
			Failure: "The queued source-agent Job was cancelled.", JobID: info.ID,
		})
	}
	if session != nil {
		return session.Close()
	}
	return nil
}

func (m *Manager) DeleteJob(owner uint64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || !m.jobVisibleTo(job, owner) {
		return errors.New("job not found")
	}
	if job.info.State == "running" || job.info.State == "queued" || job.info.State == "dispatching" {
		return errors.New("stop the running job before deleting its output")
	}
	if job.outputPath != "" {
		if err := os.Remove(job.outputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = os.Remove(filepath.Dir(job.outputPath))
	}
	for _, file := range job.info.Files {
		path := filepath.Join(m.jobOutput.root, job.info.AgentID, job.info.ID+".files", fmt.Sprintf("%08x-%s", file.ID, file.Name))
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(job.info.Files) != 0 {
		_ = os.Remove(filepath.Join(m.jobOutput.root, job.info.AgentID, job.info.ID+".files"))
	}
	if m.jobOutput != nil {
		if job.diskBytes <= m.jobOutput.used {
			m.jobOutput.used -= job.diskBytes
		} else {
			m.jobOutput.used = 0
		}
	}
	if m.operations != nil {
		if err := m.operations.DeleteJob(id); err != nil {
			return err
		}
	}
	delete(m.jobs, id)
	return nil
}

func (m *Manager) Job(owner uint64, id string, includeOutput bool) (JobInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job := m.jobs[id]
	if job == nil || !m.jobVisibleTo(job, owner) {
		return JobInfo{}, errors.New("job not found")
	}
	info := job.info
	info.Argv = append([]string(nil), info.Argv...)
	info.Files = append([]bof.FileArtifact(nil), info.Files...)
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
		if !m.jobVisibleTo(job, owner) || agentID != "" && job.info.AgentID != agentID {
			continue
		}
		info := job.info
		info.Argv = append([]string(nil), info.Argv...)
		info.Files = append([]bof.FileArtifact(nil), info.Files...)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// Jobs are server-owned team history. Under the current enrollment trust model,
// a connected operator client may inspect or manage them; future authenticated
// permissions can narrow this without moving the job index to the client.
func (m *Manager) jobVisibleTo(job *jobState, owner uint64) bool {
	if owner == 0 || owner == job.owner {
		return true
	}
	return m.clients[owner] != nil
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
	muxer.HandleFunc("POST /v1/agents/{id}/wasm/jobs", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Source []byte   `json:"source"`
			Stdin  []byte   `json:"stdin,omitempty"`
			Args   []string `json:"args,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 6<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid WASM job request", 400)
			return
		}
		job, err := m.StartWASMJob(r.Context(), jobOwner(r.Context()), r.PathValue("id"), request.Source, request.Args, request.Stdin)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonReply(w, http.StatusCreated, job)
	})
	muxer.HandleFunc("POST /v1/agents/{id}/native/jobs", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Source []byte   `json:"source"`
			Data   []byte   `json:"data,omitempty"`
			Args   []string `json:"args,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid native job request", 400)
			return
		}
		job, err := m.StartNativeJob(r.Context(), jobOwner(r.Context()), r.PathValue("id"), request.Source, request.Args, request.Data)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonReply(w, http.StatusCreated, job)
	})
	muxer.HandleFunc("POST /v1/agents/{id}/assembly/jobs", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Source []byte   `json:"source"`
			Args   []string `json:"args,omitempty"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid assembly job request", 400)
			return
		}
		job, err := m.StartAssemblyJob(r.Context(), jobOwner(r.Context()), r.PathValue("id"), request.Source, request.Args)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		jsonReply(w, http.StatusCreated, job)
	})
	muxer.HandleFunc("POST /v1/agents/{id}/bof/jobs", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Source    []byte `json:"source"`
			Arguments []byte `json:"arguments"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid BOF job request", 400)
			return
		}
		job, err := m.StartBOFJob(r.Context(), jobOwner(r.Context()), r.PathValue("id"), request.Source, request.Arguments)
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
	muxer.HandleFunc("GET /v1/jobs/{id}/output/chunk", func(w http.ResponseWriter, r *http.Request) {
		offset, err := strconv.ParseUint(r.URL.Query().Get("offset"), 10, 64)
		if err != nil {
			http.Error(w, "invalid output offset", http.StatusBadRequest)
			return
		}
		chunk, err := m.JobChunk(jobOwner(r.Context()), r.PathValue("id"), offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		jsonReply(w, 200, chunk)
	})
	muxer.HandleFunc("GET /v1/jobs/{id}/files/{fileid}/chunk", func(w http.ResponseWriter, r *http.Request) {
		offset, err := strconv.ParseUint(r.URL.Query().Get("offset"), 10, 64)
		if err != nil {
			http.Error(w, "invalid file offset", 400)
			return
		}
		fileID, err := strconv.ParseUint(r.PathValue("fileid"), 10, 32)
		if err != nil {
			http.Error(w, "invalid file ID", 400)
			return
		}
		chunk, err := m.JobFileChunk(jobOwner(r.Context()), r.PathValue("id"), uint32(fileID), offset)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		jsonReply(w, 200, chunk)
	})
	muxer.HandleFunc("POST /v1/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := m.CancelJob(jobOwner(r.Context()), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	muxer.HandleFunc("DELETE /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := m.DeleteJob(jobOwner(r.Context()), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
