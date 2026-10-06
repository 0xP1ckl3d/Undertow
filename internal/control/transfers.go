package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"undertow/internal/mux"
	"undertow/internal/pivot"
)

// TransferRecord is server-owned metadata. The file bytes continue to use the
// existing encrypted transfer stream and are never written to this table.
type TransferRecord struct {
	ID              string    `json:"id"`
	AgentID         string    `json:"agent_id"`
	ClientID        string    `json:"client_id"`
	ClientSessionID uint64    `json:"client_session_id"`
	OperatorID      string    `json:"operator_id,omitempty"`
	DisplayName     string    `json:"display_name,omitempty"`
	Operation       string    `json:"operation"`
	RemotePath      string    `json:"remote_path"`
	State           string    `json:"state"`
	Bytes           int64     `json:"bytes"`
	Total           int64     `json:"total"`
	SHA256          string    `json:"sha256,omitempty"`
	Error           string    `json:"error,omitempty"`
	Started         time.Time `json:"started"`
	Ended           time.Time `json:"ended,omitempty"`
}

// StreamDeploymentArtifact reuses the authenticated file stream and durable
// Transfers history for a server-owned deployment upload. The remote path may
// be a Windows administrative share resolved by the source agent.
func (m *Manager) StreamDeploymentArtifact(ctx context.Context, agentID string, source *mux.Mux, localPath, remotePath string) (pivot.FileMessage, TransferRecord, error) {
	var empty pivot.FileMessage
	if source == nil {
		return empty, TransferRecord{}, errors.New("source agent is disconnected")
	}
	info, err := os.Stat(localPath)
	if err != nil || !info.Mode().IsRegular() {
		return empty, TransferRecord{}, errors.New("deployment artifact is unavailable")
	}
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		return empty, TransferRecord{}, errors.New("transfer history unavailable")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return empty, TransferRecord{}, err
	}
	actor := boundActionFromContext(ctx)
	record := TransferRecord{ID: hex.EncodeToString(random[:]), AgentID: agentID, ClientID: actor.ClientID, ClientSessionID: actor.ClientSessionID, OperatorID: actor.OperatorID, DisplayName: actor.DisplayName, Operation: "upload", RemotePath: remotePath, State: "running", Total: info.Size(), Started: time.Now().UTC()}
	if err := store.saveTransfer(record); err != nil {
		return empty, TransferRecord{}, err
	}
	m.PublishEvent("transfer.changed", record.ID)
	result, transferErr := pivot.TransferFileDirectProgress(ctx, source, agentID, "upload", localPath, remotePath, func(progress pivot.TransferProgress) {
		record.Bytes = progress.Bytes
		if store.saveTransfer(record) == nil {
			m.PublishEvent("transfer.changed", record.ID)
		}
	})
	record.Ended = time.Now().UTC()
	if transferErr != nil {
		record.State, record.Error = "failed", transferErr.Error()
		if len(record.Error) > 512 {
			record.Error = record.Error[:512]
		}
	} else {
		record.State, record.Bytes, record.SHA256 = "completed", result.Size, result.SHA256
	}
	if err := store.saveTransfer(record); err != nil {
		return result, record, err
	}
	m.PublishEvent("transfer.changed", record.ID)
	return result, record, transferErr
}

func (s *OperationsStore) saveTransfer(record TransferRecord) error {
	if s == nil {
		return errors.New("operations store is not configured")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO transfers(id,record_json,client_session_id,started) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET record_json=excluded.record_json`, record.ID, data, strconv.FormatUint(record.ClientSessionID, 10), record.Started.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM transfers WHERE id NOT IN (SELECT id FROM transfers ORDER BY started DESC LIMIT 500)`)
	return err
}

func (s *OperationsStore) transfer(id string) (TransferRecord, error) {
	var data []byte
	if s == nil {
		return TransferRecord{}, errors.New("operations store is not configured")
	}
	if err := s.db.QueryRow(`SELECT record_json FROM transfers WHERE id=?`, id).Scan(&data); err != nil {
		return TransferRecord{}, err
	}
	var record TransferRecord
	err := json.Unmarshal(data, &record)
	return record, err
}

func (s *OperationsStore) TransferHistory(limit int) ([]TransferRecord, error) {
	if s == nil {
		return []TransferRecord{}, nil
	}
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT record_json FROM transfers ORDER BY started DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]TransferRecord, 0, limit)
	for rows.Next() {
		var data []byte
		var record TransferRecord
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *OperationsStore) RecoverTransfers() error {
	records, err := s.TransferHistory(500)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.State != "running" && record.State != "pending" {
			continue
		}
		record.State, record.Error, record.Ended = "interrupted", "server restarted before transfer completed", time.Now().UTC()
		if err := s.saveTransfer(record); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) interruptTransfers(sessionID uint64) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		return
	}
	records, err := store.TransferHistory(500)
	if err != nil {
		return
	}
	for _, record := range records {
		if record.ClientSessionID != sessionID || record.State != "running" && record.State != "pending" {
			continue
		}
		record.State, record.Error, record.Ended = "interrupted", "client session disconnected", time.Now().UTC()
		if store.saveTransfer(record) == nil {
			m.PublishEvent("transfer.changed", record.ID)
		}
	}
}

func (m *Manager) registerTransferHandlers(muxer *http.ServeMux) {
	muxer.HandleFunc("GET /v1/transfers", func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		store := m.operations
		m.mu.RUnlock()
		if store == nil {
			jsonReply(w, http.StatusOK, []TransferRecord{})
			return
		}
		records, err := store.TransferHistory(200)
		if err != nil {
			http.Error(w, "transfer history unavailable", http.StatusInternalServerError)
			return
		}
		jsonReply(w, http.StatusOK, records)
	})
	muxer.HandleFunc("POST /v1/transfers", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			AgentID    string `json:"agent_id"`
			Operation  string `json:"operation"`
			RemotePath string `json:"remote_path"`
			Total      int64  `json:"total"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input) != nil || input.AgentID == "" || input.Operation != "upload" && input.Operation != "download" && input.Operation != "screenshot" || input.RemotePath == "" || len(input.RemotePath) > 2048 || strings.ContainsRune(input.RemotePath, 0) || input.Total < 0 {
			http.Error(w, "invalid transfer metadata", http.StatusBadRequest)
			return
		}
		m.mu.RLock()
		store := m.operations
		live := m.agents[input.AgentID]
		retained := m.offlineAgents[input.AgentID]
		m.mu.RUnlock()
		if live == nil && (retained.ConnectionState != "sleeping" || !time.Now().Before(retained.SleepLostAfter)) {
			http.Error(w, "agent missed its check-ins or is disconnected", http.StatusNotFound)
			return
		}
		if store == nil {
			http.Error(w, "transfer history unavailable", http.StatusServiceUnavailable)
			return
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			http.Error(w, "transfer ID unavailable", http.StatusInternalServerError)
			return
		}
		actor := boundActionFromContext(r.Context())
		state := "running"
		if live == nil || live.inventory.SleepSupported && live.inventory.Sleep.IntervalSeconds > 0 {
			state = "pending"
		}
		record := TransferRecord{ID: hex.EncodeToString(random[:]), AgentID: input.AgentID, ClientID: actor.ClientID, ClientSessionID: actor.ClientSessionID, OperatorID: actor.OperatorID, DisplayName: actor.DisplayName, Operation: input.Operation, RemotePath: input.RemotePath, State: state, Total: input.Total, Started: time.Now().UTC()}
		if err := store.saveTransfer(record); err != nil {
			http.Error(w, "transfer history unavailable", http.StatusInternalServerError)
			return
		}
		m.PublishEvent("transfer.changed", record.ID)
		jsonReply(w, http.StatusCreated, record)
	})
	muxer.HandleFunc("PUT /v1/transfers/{id}", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			State  string `json:"state"`
			Bytes  int64  `json:"bytes"`
			Total  int64  `json:"total"`
			SHA256 string `json:"sha256"`
			Error  string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&input) != nil || input.State != "running" && input.State != "completed" && input.State != "failed" && input.State != "cancelled" || input.Bytes < 0 || input.Total < 0 || input.Total > 0 && input.Bytes > input.Total || len(input.SHA256) > 64 || len(input.Error) > 512 {
			http.Error(w, "invalid transfer update", http.StatusBadRequest)
			return
		}
		if input.State == "completed" {
			if len(input.SHA256) != 64 || input.Bytes != input.Total {
				http.Error(w, "completed transfer requires verified size and checksum", http.StatusBadRequest)
				return
			}
			if _, err := hex.DecodeString(input.SHA256); err != nil {
				http.Error(w, "invalid transfer checksum", http.StatusBadRequest)
				return
			}
		}
		m.mu.RLock()
		store := m.operations
		m.mu.RUnlock()
		if store == nil {
			http.Error(w, "transfer history unavailable", http.StatusServiceUnavailable)
			return
		}
		record, err := store.transfer(r.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "transfer history unavailable", http.StatusInternalServerError)
			return
		}
		actor := boundActionFromContext(r.Context())
		if record.ClientSessionID == 0 || actor.ClientSessionID != record.ClientSessionID || record.State != "running" && record.State != "pending" || input.Bytes < record.Bytes || input.Total > 0 && record.Total > 0 && input.Total != record.Total {
			http.Error(w, "transfer update is not allowed", http.StatusForbidden)
			return
		}
		record.State, record.Bytes, record.SHA256, record.Error = input.State, input.Bytes, input.SHA256, input.Error
		if input.Total > 0 {
			record.Total = input.Total
		}
		if input.State != "running" {
			record.Ended = time.Now().UTC()
		}
		if err := store.saveTransfer(record); err != nil {
			http.Error(w, "transfer history unavailable", http.StatusInternalServerError)
			return
		}
		m.PublishEvent("transfer.changed", record.ID)
		jsonReply(w, http.StatusOK, record)
	})
}
