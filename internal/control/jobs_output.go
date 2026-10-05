package control

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"undertow/internal/bof"
)

const jobOutputChunkSize = 256 << 10

type jobOutputStore struct {
	root        string
	perJobLimit uint64
	totalLimit  uint64
	used        uint64 // guarded by Manager.mu
}

// ConfigureJobOutput enables durable output for background jobs. Call before
// accepting clients or starting jobs.
func (m *Manager) ConfigureJobOutput(root string, perJobLimit, totalLimit uint64) error {
	if root == "" || perJobLimit == 0 || totalLimit < perJobLimit {
		return errors.New("invalid job output directory or limits")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return fmt.Errorf("create job output directory: %w", err)
	}
	probe, err := os.CreateTemp(abs, ".write-check-*")
	if err != nil {
		return fmt.Errorf("job output directory is not writable: %w", err)
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return err
	}
	if err := os.Remove(probePath); err != nil {
		return err
	}
	var used uint64
	if err := filepath.WalkDir(abs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".out") || strings.HasSuffix(filepath.Dir(path), ".files")) {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".partial") {
			return os.Remove(path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() < 0 || uint64(info.Size()) > ^uint64(0)-used {
			return errors.New("job output usage overflow")
		}
		used += uint64(info.Size())
		return nil
	}); err != nil {
		return fmt.Errorf("inspect job output directory: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.jobs) != 0 {
		return errors.New("configure job output before starting jobs")
	}
	m.jobOutput = &jobOutputStore{root: abs, perJobLimit: perJobLimit, totalLimit: totalLimit, used: used}
	return nil
}

func (m *Manager) JobFileChunk(owner uint64, id string, fileID uint32, offset uint64) (JobOutputChunk, error) {
	m.mu.RLock()
	job := m.jobs[id]
	if job == nil || !m.jobVisibleTo(job, owner) || m.jobOutput == nil {
		m.mu.RUnlock()
		return JobOutputChunk{}, errors.New("job not found")
	}
	var artifact *bof.FileArtifact
	for i := range job.info.Files {
		if job.info.Files[i].ID == fileID {
			copy := job.info.Files[i]
			artifact = &copy
			break
		}
	}
	if artifact == nil {
		m.mu.RUnlock()
		return JobOutputChunk{}, errors.New("job file not found")
	}
	path := filepath.Join(m.jobOutput.root, job.info.AgentID, id+".files", fmt.Sprintf("%08x-%s", fileID, artifact.Name))
	total := artifact.Size
	m.mu.RUnlock()
	if offset > total {
		return JobOutputChunk{}, errors.New("file offset exceeds size")
	}
	want := uint64(jobOutputChunkSize)
	if total-offset < want {
		want = total - offset
	}
	result := JobOutputChunk{Offset: offset, Total: total, EOF: offset+want == total}
	if want == 0 {
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return JobOutputChunk{}, err
	}
	defer file.Close()
	result.Data = make([]byte, want)
	if n, err := file.ReadAt(result.Data, int64(offset)); err != nil || n != len(result.Data) {
		return JobOutputChunk{}, io.ErrUnexpectedEOF
	}
	return result, nil
}

func (m *Manager) jobOutputReady() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.jobOutput != nil && m.jobOutput.used >= m.jobOutput.totalLimit {
		return fmt.Errorf("server job output storage limit reached (%d bytes total); remove old job output before starting another job", m.jobOutput.totalLimit)
	}
	return nil
}

func safeJobPathComponent(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func (m *Manager) createJobOutput(info JobInfo) (*os.File, string, error) {
	store := m.jobOutput
	if store == nil {
		return nil, "", nil
	}
	if !safeJobPathComponent(info.AgentID) || !safeJobPathComponent(info.ID) {
		return nil, "", errors.New("job ID cannot be used as an output path")
	}
	if store.used >= store.totalLimit {
		return nil, "", fmt.Errorf("server job output limit reached (%d bytes); remove old job output before starting another job", store.totalLimit)
	}
	dir := filepath.Join(store.root, info.AgentID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", fmt.Errorf("create agent job output directory: %w", err)
	}
	path := filepath.Join(dir, info.ID+".out")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, "", fmt.Errorf("create job output file: %w", err)
	}
	return file, path, nil
}

type JobOutputChunk struct {
	Offset uint64 `json:"offset"`
	Data   []byte `json:"data"`
	Total  uint64 `json:"total"`
	EOF    bool   `json:"eof"`
}

func (m *Manager) JobChunk(owner uint64, id string, offset uint64) (JobOutputChunk, error) {
	m.mu.RLock()
	job := m.jobs[id]
	if job == nil || !m.jobVisibleTo(job, owner) {
		m.mu.RUnlock()
		return JobOutputChunk{}, errors.New("job not found")
	}
	path, total := job.outputPath, job.info.OutputBytes
	var memory []byte
	if path == "" && uint64(len(job.output)) == total {
		memory = append([]byte(nil), job.output...)
	}
	m.mu.RUnlock()
	if path == "" && memory == nil && total != 0 {
		return JobOutputChunk{}, errors.New("full output is unavailable for this job")
	}
	if offset > total {
		return JobOutputChunk{}, errors.New("output offset exceeds current size")
	}
	want := uint64(jobOutputChunkSize)
	if remain := total - offset; remain < want {
		want = remain
	}
	result := JobOutputChunk{Offset: offset, Total: total, EOF: offset+want == total}
	if want == 0 {
		return result, nil
	}
	if path == "" {
		result.Data = memory[offset : offset+want]
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return JobOutputChunk{}, err
	}
	defer file.Close()
	result.Data = make([]byte, want)
	if n, err := file.ReadAt(result.Data, int64(offset)); err != nil || n != len(result.Data) {
		if err == nil || err == io.EOF {
			return JobOutputChunk{}, io.ErrUnexpectedEOF
		}
		return JobOutputChunk{}, err
	}
	return result, nil
}
