package control

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RestoreJobHistory recovers the server-owned job index after a restart. Live
// sessions cannot survive a restart, so unfinished jobs become interrupted.
func (m *Manager) RestoreJobHistory() error {
	m.mu.RLock()
	store, output := m.operations, m.jobOutput
	m.mu.RUnlock()
	if store == nil || output == nil {
		return nil
	}
	entries, err := store.LoadJobs()
	if err != nil {
		return fmt.Errorf("load job index: %w", err)
	}
	pending, err := store.LoadQueuedJobs()
	if err != nil {
		return fmt.Errorf("load queued job requests: %w", err)
	}
	for _, entry := range entries {
		info := entry.Info
		if !safeJobPathComponent(info.ID) || !safeJobPathComponent(info.AgentID) {
			return fmt.Errorf("invalid stored job ID %q", info.ID)
		}
		path := filepath.Join(output.root, info.AgentID, info.ID+".out")
		var preview []byte
		var diskBytes uint64
		if file, err := os.Open(path); err == nil {
			stat, err := file.Stat()
			if err != nil {
				file.Close()
				return err
			}
			if stat.Size() < 0 {
				file.Close()
				return fmt.Errorf("negative output size for job %s", info.ID)
			}
			diskBytes = uint64(stat.Size())
			info.OutputBytes = diskBytes
			info.OutputFile = path
			if diskBytes > 0 {
				start := int64(0)
				if diskBytes > jobOutputLimit {
					start = int64(diskBytes - jobOutputLimit)
					info.OutputTruncated = true
				}
				if _, err := file.Seek(start, io.SeekStart); err != nil {
					file.Close()
					return err
				}
				preview, err = io.ReadAll(io.LimitReader(file, jobOutputLimit))
				if err != nil {
					file.Close()
					return err
				}
			}
			file.Close()
		} else if !os.IsNotExist(err) {
			return err
		} else {
			path = ""
			info.OutputFile = ""
			if info.OutputBytes > 0 {
				if uint64(len(info.Output)) == info.OutputBytes {
					preview = []byte(info.Output)
				} else {
					info.OutputError = "retained output file is missing"
				}
			}
		}
		if info.State == "running" {
			now := time.Now().UTC()
			info.State = "interrupted"
			info.Ended = &now
		}
		var request *queuedJobRequest
		if info.State == "dispatching" && info.DeploymentID != "" {
			if value, ok := pending[info.ID]; ok {
				// A deployment Job can be in dispatching only because the
				// source check-in was being attached. Keep it queueable across
				// restart; no target action has been proven to start yet.
				info.State = "queued"
				request = &value
			} else {
				now := time.Now().UTC()
				info.State, info.Ended, info.OutputError = "interrupted", &now, "Server restarted during dispatch; execution may have started. Review before retrying."
			}
		} else if info.State == "dispatching" {
			now := time.Now().UTC()
			info.State, info.Ended, info.OutputError = "interrupted", &now, "Server restarted during dispatch; execution may have started. Review before retrying."
			if err := store.DeleteQueuedJob(info.ID); err != nil {
				return err
			}
		} else if info.State == "queued" {
			if value, ok := pending[info.ID]; ok {
				request = &value
			} else {
				now := time.Now().UTC()
				info.State, info.Ended, info.OutputError = "failed", &now, "queued job request is missing"
			}
		}
		var fileBytes uint64
		retainedFiles := info.Files[:0]
		for _, artifact := range info.Files {
			if artifact.Name == "" || filepath.Base(artifact.Name) != artifact.Name || strings.ContainsAny(artifact.Name, "\\/:") {
				return fmt.Errorf("invalid stored job file name %q", artifact.Name)
			}
			filePath := filepath.Join(output.root, info.AgentID, info.ID+".files", fmt.Sprintf("%08x-%s", artifact.ID, artifact.Name))
			stat, err := os.Stat(filePath)
			if os.IsNotExist(err) {
				info.OutputError = "one or more retained job files are missing"
				continue
			}
			if err != nil {
				return fmt.Errorf("restore job file %s: %w", filePath, err)
			}
			if stat.Size() < 0 || uint64(stat.Size()) != artifact.Size {
				return fmt.Errorf("stored job file %s has unexpected size", filePath)
			}
			fileBytes += artifact.Size
			retainedFiles = append(retainedFiles, artifact)
		}
		info.Files = retainedFiles
		job := &jobState{info: info, ownerKey: entry.OwnerKey, outputPath: path, diskBytes: diskBytes + fileBytes, fileBytes: fileBytes, output: preview, request: request}
		m.mu.Lock()
		m.jobs[info.ID] = job
		m.mu.Unlock()
		if err := store.SaveJob(info, entry.OwnerKey, path); err != nil {
			return err
		}
	}
	return nil
}
