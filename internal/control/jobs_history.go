package control

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
				info.OutputError = "retained output file is missing"
			}
		}
		if info.State == "running" {
			now := time.Now().UTC()
			info.State = "interrupted"
			info.Ended = &now
		}
		job := &jobState{info: info, ownerKey: entry.OwnerKey, outputPath: path, diskBytes: diskBytes, output: preview}
		m.mu.Lock()
		m.jobs[info.ID] = job
		m.mu.Unlock()
		if err := store.SaveJob(info, entry.OwnerKey, path); err != nil {
			return err
		}
	}
	return nil
}
