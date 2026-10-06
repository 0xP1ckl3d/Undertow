package control

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type AuditRecord struct {
	ID              string    `json:"id"`
	ActionID        string    `json:"action_id,omitempty"`
	At              time.Time `json:"at"`
	Action          string    `json:"action"`
	Target          string    `json:"target"`
	ClientID        string    `json:"client_id,omitempty"`
	ClientSessionID uint64    `json:"client_session_id,omitempty"`
	OperatorID      string    `json:"operator_id,omitempty"`
	DisplayName     string    `json:"display_name,omitempty"`
	Source          string    `json:"source"`
	IdentityTrust   string    `json:"identity_trust"`
	Status          int       `json:"status"`
}

type OperationsStore struct {
	db             *sql.DB
	screenshotsDir string
}

func OpenOperationsStore(path string) (*OperationsStore, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		`CREATE TABLE IF NOT EXISTS audit (id TEXT PRIMARY KEY, action_id TEXT NOT NULL, at TEXT NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, client_id TEXT NOT NULL, client_session_id TEXT NOT NULL, operator_id TEXT NOT NULL, display_name TEXT NOT NULL, source TEXT NOT NULL, identity_trust TEXT NOT NULL, status INTEGER NOT NULL)`,
		"CREATE INDEX IF NOT EXISTS audit_at ON audit(at DESC)",
		`CREATE TABLE IF NOT EXISTS job_history (id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, owner_client_id TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL, started TEXT NOT NULL, ended TEXT, output_bytes INTEGER NOT NULL, output_path TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS job_records (id TEXT PRIMARY KEY, info_json BLOB NOT NULL, owner_key TEXT NOT NULL, output_path TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS queued_job_requests (id TEXT PRIMARY KEY, request_json BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS host_results (agent_id TEXT NOT NULL, operation TEXT NOT NULL, session_id TEXT NOT NULL, at TEXT NOT NULL, result_json BLOB NOT NULL, PRIMARY KEY(agent_id,operation))`,
		`CREATE TABLE IF NOT EXISTS screenshots (id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, screen INTEGER NOT NULL, at TEXT NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL, client_id TEXT NOT NULL, client_session_id TEXT NOT NULL, operator_id TEXT NOT NULL, display_name TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS server_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS operator_accounts (id TEXT PRIMARY KEY, display_name TEXT NOT NULL, role TEXT NOT NULL, password_hash BLOB NOT NULL, disabled INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE IF NOT EXISTS team_messages (id INTEGER PRIMARY KEY AUTOINCREMENT, sent_at TEXT NOT NULL, sender_id TEXT NOT NULL, sender_name TEXT NOT NULL, recipient_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, body TEXT NOT NULL, task_id TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX IF NOT EXISTS team_messages_room ON team_messages(recipient_id,id DESC)`,
		`CREATE INDEX IF NOT EXISTS team_messages_sender ON team_messages(sender_id,id DESC)`,
		`CREATE TABLE IF NOT EXISTS team_tasks (id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT NOT NULL, creator_id TEXT NOT NULL, creator_name TEXT NOT NULL, assignee_id TEXT NOT NULL, assignee_name TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS team_tasks_updated ON team_tasks(updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS agent_snapshots (id TEXT PRIMARY KEY, saved_at TEXT NOT NULL, info_json BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_nicknames (id TEXT PRIMARY KEY, nickname TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_archives (id TEXT PRIMARY KEY, archived_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_sleep (id TEXT PRIMARY KEY, interval_seconds INTEGER NOT NULL, jitter_percent INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS relay_listeners (agent_id TEXT NOT NULL, bind TEXT NOT NULL, PRIMARY KEY(agent_id,bind))`,
		`CREATE TABLE IF NOT EXISTS transfers (id TEXT PRIMARY KEY, record_json BLOB NOT NULL, client_session_id TEXT NOT NULL, started TEXT NOT NULL)`,
		"CREATE INDEX IF NOT EXISTS transfers_started ON transfers(started DESC)",
		"CREATE INDEX IF NOT EXISTS screenshots_agent_at ON screenshots(agent_id, at DESC)",
		"PRAGMA user_version=13",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize operations database: %w", err)
		}
	}
	if err := os.Chmod(abs, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return &OperationsStore{db: db, screenshotsDir: filepath.Join(filepath.Dir(abs), "screenshots")}, nil
}

type StoredJob struct {
	Info       JobInfo
	OwnerKey   string
	OutputPath string
}

func (s *OperationsStore) SaveJob(info JobInfo, ownerKey, outputPath string) error {
	if s == nil {
		return errors.New("operations store is not configured")
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO job_records(id,info_json,owner_key,output_path) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET info_json=excluded.info_json,owner_key=excluded.owner_key,output_path=excluded.output_path`, info.ID, data, ownerKey, outputPath)
	return err
}

func (s *OperationsStore) DeleteJob(id string) error {
	if s == nil {
		return errors.New("operations store is not configured")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM queued_job_requests WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM job_records WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OperationsStore) SaveQueuedJob(info JobInfo, ownerKey string, request queuedJobRequest) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	requestData, err := json.Marshal(request)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO job_records(id,info_json,owner_key,output_path) VALUES(?,?,?,?)`, info.ID, data, ownerKey, ""); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO queued_job_requests(id,request_json) VALUES(?,?)`, info.ID, requestData); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OperationsStore) LoadQueuedJobs() (map[string]queuedJobRequest, error) {
	rows, err := s.db.Query(`SELECT id,request_json FROM queued_job_requests`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]queuedJobRequest)
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var request queuedJobRequest
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, err
		}
		out[id] = request
	}
	return out, rows.Err()
}

func (s *OperationsStore) DeleteQueuedJob(id string) error {
	_, err := s.db.Exec(`DELETE FROM queued_job_requests WHERE id=?`, id)
	return err
}

func (s *OperationsStore) LoadJobs() ([]StoredJob, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT info_json,owner_key,output_path FROM job_records ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredJob
	for rows.Next() {
		var item StoredJob
		var data []byte
		if err := rows.Scan(&data, &item.OwnerKey, &item.OutputPath); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &item.Info); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *OperationsStore) Close() error {
	if s == nil {
		return nil
	}
	return s.db.Close()
}

func (s *OperationsStore) PublicHost() (string, error) {
	var host string
	err := s.db.QueryRow("SELECT value FROM server_settings WHERE key='public_host'").Scan(&host)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return host, err
}

func (s *OperationsStore) SetPublicHost(host string) error {
	_, err := s.db.Exec("INSERT INTO server_settings(key,value) VALUES('public_host',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", host)
	return err
}

func (s *OperationsStore) SaveAgentSnapshot(agent AgentInfo) error {
	if agent.ID == "" {
		return errors.New("agent ID required")
	}
	data, err := json.Marshal(agent)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO agent_snapshots(id,saved_at,info_json) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET saved_at=excluded.saved_at,info_json=excluded.info_json", agent.ID, time.Now().UTC().Format(time.RFC3339Nano), data)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM agent_snapshots WHERE id NOT IN (SELECT id FROM agent_snapshots ORDER BY saved_at DESC LIMIT 5000) AND id NOT IN (SELECT id FROM agent_archives)")
	return err
}

func (s *OperationsStore) LoadAgentSnapshots() ([]AgentInfo, error) {
	rows, err := s.db.Query("SELECT info_json FROM agent_snapshots WHERE id IN (SELECT id FROM agent_snapshots ORDER BY saved_at DESC LIMIT 5000) OR id IN (SELECT id FROM agent_archives) ORDER BY saved_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgentInfo, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var agent AgentInfo
		if err := json.Unmarshal(data, &agent); err != nil {
			return nil, err
		}
		agent.Online = false
		agent.Offline = true
		out = append(out, agent)
	}
	return out, rows.Err()
}

func (s *OperationsStore) LoadAgentNicknames() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id,nickname FROM agent_nicknames`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var id, nickname string
		if err := rows.Scan(&id, &nickname); err != nil {
			return nil, err
		}
		out[id] = nickname
	}
	return out, rows.Err()
}

func (s *OperationsStore) SetAgentNickname(id, nickname string) error {
	if nickname == "" {
		_, err := s.db.Exec(`DELETE FROM agent_nicknames WHERE id=?`, id)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO agent_nicknames(id,nickname) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET nickname=excluded.nickname`, id, nickname)
	return err
}

func (s *OperationsStore) LoadAgentSleepOverrides() (map[string]SleepPolicy, error) {
	rows, err := s.db.Query(`SELECT id,interval_seconds,jitter_percent FROM agent_sleep`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]SleepPolicy)
	for rows.Next() {
		var id string
		var policy SleepPolicy
		if err := rows.Scan(&id, &policy.IntervalSeconds, &policy.JitterPercent); err != nil {
			return nil, err
		}
		if err := policy.Validate(); err != nil {
			return nil, err
		}
		out[id] = policy
	}
	return out, rows.Err()
}

func (s *OperationsStore) SetAgentSleepOverride(id string, policy SleepPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO agent_sleep(id,interval_seconds,jitter_percent) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET interval_seconds=excluded.interval_seconds,jitter_percent=excluded.jitter_percent`, id, policy.IntervalSeconds, policy.JitterPercent)
	return err
}

func (s *OperationsStore) LoadArchivedAgents() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT id FROM agent_archives`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *OperationsStore) SetAgentArchived(id string, archived bool) error {
	if archived {
		_, err := s.db.Exec(`INSERT INTO agent_archives(id,archived_at) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, id, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	}
	_, err := s.db.Exec(`DELETE FROM agent_archives WHERE id=?`, id)
	return err
}

func (s *OperationsStore) LoadRelayListeners() ([]RelayInfo, error) {
	rows, err := s.db.Query(`SELECT agent_id,bind FROM relay_listeners ORDER BY agent_id,bind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelayInfo
	for rows.Next() {
		var item RelayInfo
		if err := rows.Scan(&item.AgentID, &item.Bind); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *OperationsStore) SetRelayListener(agentID, bind string, enabled bool) error {
	if enabled {
		_, err := s.db.Exec(`INSERT OR IGNORE INTO relay_listeners(agent_id,bind) VALUES(?,?)`, agentID, bind)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM relay_listeners WHERE agent_id=? AND bind=?`, agentID, bind)
	return err
}

func (s *OperationsStore) RecordAudit(record AuditRecord) error {
	if s == nil {
		return errors.New("operations store is not configured")
	}
	_, err := s.db.Exec(`INSERT INTO audit (id,action_id,at,action,target,client_id,client_session_id,operator_id,display_name,source,identity_trust,status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, record.ID, record.ActionID, record.At.Format(time.RFC3339Nano), record.Action, record.Target, record.ClientID, fmt.Sprint(record.ClientSessionID), record.OperatorID, record.DisplayName, record.Source, record.IdentityTrust, record.Status)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM audit WHERE id NOT IN (SELECT id FROM audit ORDER BY at DESC LIMIT 10000)`)
	return err
}

func (s *OperationsStore) CompleteAudit(id string, status int) error {
	if s == nil {
		return errors.New("operations store is not configured")
	}
	_, err := s.db.Exec(`UPDATE audit SET status=? WHERE id=?`, status, id)
	return err
}

func (s *OperationsStore) AuditHistory(limit int) ([]AuditRecord, error) {
	if s == nil {
		return []AuditRecord{}, nil
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,action_id,at,action,target,client_id,client_session_id,operator_id,display_name,source,identity_trust,status FROM audit ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuditRecord, 0, limit)
	for rows.Next() {
		var r AuditRecord
		var at, session string
		if err := rows.Scan(&r.ID, &r.ActionID, &at, &r.Action, &r.Target, &r.ClientID, &session, &r.OperatorID, &r.DisplayName, &r.Source, &r.IdentityTrust, &r.Status); err != nil {
			return nil, err
		}
		r.At, _ = time.Parse(time.RFC3339Nano, at)
		_, _ = fmt.Sscan(session, &r.ClientSessionID)
		out = append(out, r)
	}
	return out, rows.Err()
}
