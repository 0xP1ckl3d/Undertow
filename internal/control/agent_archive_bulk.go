package control

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
)

type agentPurgeFiles struct {
	screenshots []string
	deployments []string
}

// ChangeArchivedAgents validates the complete selection before changing any
// record. Deletion is limited to disconnected, explicitly archived agents.
func (m *Manager) ChangeArchivedAgents(ids []string, action string) error {
	if action != "restore" && action != "delete" {
		return errors.New("choose restore or delete")
	}
	if len(ids) == 0 || len(ids) > 5000 {
		return errors.New("select between 1 and 5000 archived agents")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !safeJobPathComponent(id) || seen[id] {
			return errors.New("invalid or duplicate agent ID")
		}
		seen[id] = true
	}
	m.mu.Lock()
	if m.operations == nil {
		m.mu.Unlock()
		return errors.New("operations store unavailable")
	}
	for _, id := range ids {
		if m.agents[id] != nil || !m.archivedAgents[id] {
			m.mu.Unlock()
			return fmt.Errorf("agent %s is not an archived disconnected agent", id)
		}
		if _, ok := m.offlineAgents[id]; !ok {
			m.mu.Unlock()
			return fmt.Errorf("agent %s is not retained", id)
		}
	}
	if action == "restore" {
		if err := m.operations.restoreAgentArchives(ids); err != nil {
			m.mu.Unlock()
			return err
		}
		for _, id := range ids {
			delete(m.archivedAgents, id)
		}
		m.mu.Unlock()
		for _, id := range ids {
			m.PublishEvent("agent.updated", id)
		}
		return nil
	}
	files, err := m.operations.purgeAgentRecords(ids)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	var cleanupErrors []error
	store := m.operations
	for _, id := range ids {
		delete(m.offlineAgents, id)
		delete(m.archivedAgents, id)
		delete(m.nicknames, id)
		delete(m.sleepOverrides, id)
		delete(m.desiredRelays, id)
		delete(m.restoringRelays, id)
		delete(m.relays, id)
		delete(m.pendingLive, id)
		delete(m.foregroundTails, id)
		delete(m.jobDispatching, id)
		delete(m.tokenSnapshots, id)
		for key := range m.tokenDefaults {
			if key.agent == id {
				delete(m.tokenDefaults, key)
			}
		}
		if timer := m.sleepTimers[id]; timer != nil {
			timer.Stop()
			delete(m.sleepTimers, id)
		}
		if m.selected == id {
			m.selected = ""
		}
		if address, ok := m.virtualByAgent[id]; ok {
			delete(m.virtualByAgent, id)
			delete(m.virtualUsed, address)
		}
		for key, forward := range m.pendingForwards {
			if forward.AgentID == id {
				delete(m.pendingForwards, key)
			}
		}
		for key, forward := range m.forwards {
			if forward.AgentID == id {
				delete(m.forwards, key)
			}
		}
		for jobID, job := range m.jobs {
			if job.info.AgentID != id {
				continue
			}
			if m.jobOutput != nil {
				if job.diskBytes <= m.jobOutput.used {
					m.jobOutput.used -= job.diskBytes
				} else {
					m.jobOutput.used = 0
				}
			}
			delete(m.jobs, jobID)
		}
		if m.jobOutput != nil {
			if err := os.RemoveAll(filepath.Join(m.jobOutput.root, id)); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("job output for %s: %w", id, err))
			}
		}
		for _, client := range m.clients {
			for prefix, route := range client.accepted {
				if route.AgentID == id {
					delete(client.accepted, prefix)
				}
			}
		}
		for _, route := range m.routes.List() {
			if route.AgentID == id {
				m.routes.Delete(route.Prefix)
			}
		}
	}
	for _, deploymentID := range files.deployments {
		delete(m.deploymentCredentials, deploymentID)
	}
	m.lifecycleEvents = slices.DeleteFunc(m.lifecycleEvents, func(event LifecycleEvent) bool { return seen[event.AgentID] })
	m.mu.Unlock()
	for _, id := range files.screenshots {
		if err := os.Remove(store.screenshotPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("screenshot %s: %w", id, err))
		}
	}
	for _, id := range ids {
		m.PublishEvent("agent.deleted", id)
	}
	if len(cleanupErrors) != 0 {
		return fmt.Errorf("agent records deleted, but some files remain: %w", errors.Join(cleanupErrors...))
	}
	return nil
}

func (s *OperationsStore) restoreAgentArchives(ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM agent_archives WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *OperationsStore) purgeAgentRecords(ids []string) (agentPurgeFiles, error) {
	var files agentPurgeFiles
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return files, err
	}
	defer tx.Rollback()
	jobs, err := tx.Query(`SELECT id,info_json FROM job_records`)
	if err != nil {
		return files, err
	}
	var jobIDs []string
	for jobs.Next() {
		var id string
		var data []byte
		var info struct {
			AgentID string `json:"agent_id"`
		}
		if err = jobs.Scan(&id, &data); err == nil {
			err = json.Unmarshal(data, &info)
		}
		if err != nil {
			break
		}
		if selected[info.AgentID] {
			jobIDs = append(jobIDs, id)
		}
	}
	if err == nil {
		err = jobs.Err()
	}
	jobs.Close()
	if err != nil {
		return files, err
	}
	transfers, err := tx.Query(`SELECT id,record_json FROM transfers`)
	if err != nil {
		return files, err
	}
	var transferIDs []string
	for transfers.Next() {
		var id string
		var data []byte
		var record struct {
			AgentID string `json:"agent_id"`
		}
		if err = transfers.Scan(&id, &data); err == nil {
			err = json.Unmarshal(data, &record)
		}
		if err != nil {
			break
		}
		if selected[record.AgentID] {
			transferIDs = append(transferIDs, id)
		}
	}
	if err == nil {
		err = transfers.Err()
	}
	transfers.Close()
	if err != nil {
		return files, err
	}
	for _, id := range ids {
		rows, err := tx.Query(`SELECT id FROM screenshots WHERE agent_id=?`, id)
		if err != nil {
			return files, err
		}
		for rows.Next() {
			var screenshotID string
			if err = rows.Scan(&screenshotID); err != nil || !screenshotIDPattern.MatchString(screenshotID) {
				rows.Close()
				return files, errors.New("invalid retained screenshot ID")
			}
			files.screenshots = append(files.screenshots, screenshotID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return files, err
		}
		rows, err = tx.Query(`SELECT id FROM deployments WHERE source_agent_id=? OR result_agent_id=?`, id, id)
		if err != nil {
			return files, err
		}
		for rows.Next() {
			var deploymentID string
			if err = rows.Scan(&deploymentID); err != nil {
				rows.Close()
				return files, err
			}
			if !slices.Contains(files.deployments, deploymentID) {
				files.deployments = append(files.deployments, deploymentID)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return files, err
		}
		for _, table := range []string{"agent_snapshots", "agent_nicknames", "agent_archives", "agent_sleep"} {
			if _, err := tx.Exec("DELETE FROM "+table+" WHERE id=?", id); err != nil {
				return files, err
			}
		}
		for _, table := range []string{"host_results", "relay_listeners", "screenshots", "job_history"} {
			if _, err := tx.Exec("DELETE FROM "+table+" WHERE agent_id=?", id); err != nil {
				return files, err
			}
		}
		if err := purgeAuditTarget(tx, "/v1/agents/"+id); err != nil {
			return files, err
		}
	}
	for _, id := range jobIDs {
		if _, err := tx.Exec(`DELETE FROM queued_job_requests WHERE id=?`, id); err != nil {
			return files, err
		}
		if _, err := tx.Exec(`DELETE FROM job_records WHERE id=?`, id); err != nil {
			return files, err
		}
		if err := purgeAuditTarget(tx, "/v1/jobs/"+id); err != nil {
			return files, err
		}
	}
	for _, id := range transferIDs {
		if _, err := tx.Exec(`DELETE FROM transfers WHERE id=?`, id); err != nil {
			return files, err
		}
		if err := purgeAuditTarget(tx, "/v1/transfers/"+id); err != nil {
			return files, err
		}
	}
	for _, id := range files.deployments {
		if _, err := tx.Exec(`DELETE FROM deployments WHERE id=?`, id); err != nil {
			return files, err
		}
		if err := purgeAuditTarget(tx, "/v1/deployments/"+id); err != nil {
			return files, err
		}
	}
	for _, id := range files.screenshots {
		if err := purgeAuditTarget(tx, "/v1/screenshots/"+id); err != nil {
			return files, err
		}
	}
	return files, tx.Commit()
}

func purgeAuditTarget(tx *sql.Tx, target string) error {
	_, err := tx.Exec(`DELETE FROM audit WHERE target=? OR substr(target,1,?)=?`, target, len(target)+1, target+"/")
	return err
}

func (m *Manager) archivedAgentsBulkHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid archived-agent request", http.StatusBadRequest)
		return
	}
	if request.Action == "delete" {
		actor := boundActionFromContext(r.Context())
		if actor.ClientSessionID != 0 {
			operator, err := m.ActiveOperator(actor.ClientSessionID)
			if err != nil || operator.Role != TeamLeaderRole {
				http.Error(w, "Team Leader required to permanently delete agents", http.StatusForbidden)
				return
			}
		}
	}
	if err := m.ChangeArchivedAgents(request.IDs, request.Action); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"action": request.Action, "count": len(request.IDs)})
}
