package control

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// DeploymentRecord is the server-owned intent and result of one Windows deployment.
// It contains identifiers and status only; credentials and artifact bytes live elsewhere.
type DeploymentRecord struct {
	TokenContextID     string     `json:"token_context_id,omitempty"`
	CredentialID       string     `json:"credential_id,omitempty"`
	ID                 string     `json:"id"`
	SourceAgentID      string     `json:"source_agent_id"`
	Target             string     `json:"target"`
	ArtifactID         string     `json:"artifact_id"`
	ProfileID          string     `json:"profile_id"`
	Profile            string     `json:"profile"`
	ArtifactSHA256     string     `json:"artifact_sha256"`
	CustomArtifact     bool       `json:"custom_artifact,omitempty"`
	Method             string     `json:"method"`
	Context            string     `json:"context"`
	Account            string     `json:"account,omitempty"`
	OperatorID         string     `json:"operator_id,omitempty"`
	OperatorName       string     `json:"operator_name,omitempty"`
	RequestedFrom      string     `json:"requested_from"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	PreparedAt         *time.Time `json:"prepared_at,omitempty"`
	WaitingAt          *time.Time `json:"waiting_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	State              string     `json:"state"`
	Progress           string     `json:"progress,omitempty"`
	Error              string     `json:"error,omitempty"`
	ResultAgentID      string     `json:"result_agent_id,omitempty"`
	ResultRelationship string     `json:"result_relationship,omitempty"`
	JobID              string     `json:"job_id,omitempty"`
	TransferID         string     `json:"transfer_id,omitempty"`
	DeliveryType       string     `json:"delivery_type,omitempty"`
	DeliveryID         string     `json:"delivery_id,omitempty"`
	InstallPath        string     `json:"install_path,omitempty"`
	ServiceName        string     `json:"service_name,omitempty"`
	TaskName           string     `json:"task_name,omitempty"`
}

func (s *OperationsStore) CreateDeployment(record DeploymentRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO deployments(id,record_json,state,source_agent_id,artifact_id,target,result_agent_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, record.ID, data, record.State, record.SourceAgentID, record.ArtifactID, record.Target, record.ResultAgentID, record.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *OperationsStore) Deployment(id string) (DeploymentRecord, error) {
	var data []byte
	if err := s.db.QueryRow(`SELECT record_json FROM deployments WHERE id=?`, id).Scan(&data); err != nil {
		return DeploymentRecord{}, err
	}
	var record DeploymentRecord
	err := json.Unmarshal(data, &record)
	return record, err
}

func (s *OperationsStore) Deployments(sourceAgentID string) ([]DeploymentRecord, error) {
	query := `SELECT record_json FROM deployments ORDER BY created_at DESC`
	var args []any
	if sourceAgentID != "" {
		query = `SELECT record_json FROM deployments WHERE source_agent_id=? ORDER BY created_at DESC`
		args = append(args, sourceAgentID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DeploymentRecord, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var record DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

// ChangeDeployment serializes transitions across clients and preserves the
// previous record if validation or persistence fails.
func (s *OperationsStore) ChangeDeployment(id string, change func(*DeploymentRecord) error) (DeploymentRecord, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return DeploymentRecord{}, err
	}
	defer tx.Rollback()
	var data []byte
	if err := tx.QueryRow(`SELECT record_json FROM deployments WHERE id=?`, id).Scan(&data); err != nil {
		return DeploymentRecord{}, err
	}
	var record DeploymentRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return DeploymentRecord{}, err
	}
	if err := change(&record); err != nil {
		return DeploymentRecord{}, err
	}
	data, err = json.Marshal(record)
	if err != nil {
		return DeploymentRecord{}, err
	}
	if _, err := tx.Exec(`UPDATE deployments SET record_json=?,state=?,result_agent_id=? WHERE id=?`, data, record.State, record.ResultAgentID, id); err != nil {
		return DeploymentRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeploymentRecord{}, err
	}
	return record, nil
}

func (s *OperationsStore) WaitingDeployments(artifactID string) ([]DeploymentRecord, error) {
	rows, err := s.db.Query(`SELECT record_json FROM deployments WHERE state='waiting' AND artifact_id=?`, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DeploymentRecord
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var record DeploymentRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *OperationsStore) QueuedJobExists(id string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM queued_job_requests WHERE id=? LIMIT 1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// A server restart cannot establish whether an in-flight start reached the
// endpoint. Preserve the record and require operator review before retrying.
func (s *OperationsStore) RecoverDispatchingDeployments() error {
	items, err := s.Deployments("")
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.State != "dispatching" {
			continue
		}
		if item.JobID != "" {
			queued, err := s.QueuedJobExists(item.JobID)
			if err != nil {
				return err
			}
			if queued {
				continue
			}
		}
		_, err := s.ChangeDeployment(item.ID, func(record *DeploymentRecord) error {
			if record.State != "dispatching" {
				return nil
			}
			now := time.Now().UTC()
			record.State, record.UpdatedAt, record.CompletedAt = "failed", now, &now
			record.Progress = "Server restarted during method start"
			record.Error = "Execution may have started. Review the source agent and target before creating another deployment."
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

var ErrDeploymentConflict = errors.New("deployment state changed or the requested transition is unavailable")
var ErrDeploymentMissing = sql.ErrNoRows
