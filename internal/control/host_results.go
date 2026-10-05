package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"undertow/internal/pivot"
)

// HostResult is the last explicit result for a built-in host operation. A new
// run replaces the prior result; opening a workspace never contacts an agent.
type HostResult struct {
	AgentID   string           `json:"agent_id"`
	Operation string           `json:"operation"`
	SessionID uint64           `json:"session_id"`
	At        time.Time        `json:"at"`
	Result    pivot.ExecResult `json:"result"`
	Truncated bool             `json:"truncated,omitempty"`
}

func retainedHostOperation(operation string) bool {
	switch operation {
	case "whoami", "ps", "privileges", "interfaces", "dns", "route-table", "screens":
		return true
	default:
		return false
	}
}

var effectiveUID = regexp.MustCompile(`(?:^|\s)euid=(\d+)`)
var realUID = regexp.MustCompile(`(?:^|\s)uid=(\d+)`)

// This is an observation from an explicitly requested host operation, not a
// probe. Unknown formats remain unclassified rather than guessing privilege.
func classifyPrivileges(output string) string {
	if strings.Contains(output, "S-1-16-16384") || strings.Contains(output, "S-1-16-12288") {
		return "high"
	}
	if strings.Contains(output, "S-1-16-8192") || strings.Contains(output, "S-1-16-4096") {
		return "low"
	}
	for _, pattern := range []*regexp.Regexp{effectiveUID, realUID} {
		if match := pattern.FindStringSubmatch(output); len(match) == 2 {
			uid, err := strconv.Atoi(match[1])
			if err == nil {
				if uid == 0 {
					return "high"
				}
				return "low"
			}
		}
	}
	return ""
}

func (m *Manager) retainHostResult(agentID, operation string, result pivot.ExecResult) {
	m.mu.Lock()
	state := m.agents[agentID]
	store := m.operations
	var sessionID uint64
	if state != nil {
		sessionID = state.peer.Snapshot().ID
		if operation == "privileges" && result.Error == "" && result.ExitCode == 0 {
			if classification := classifyPrivileges(result.Stdout); classification != "" {
				state.privilege = classification
			}
		}
	}
	m.mu.Unlock()
	if state == nil || store == nil {
		return
	}
	entry := HostResult{AgentID: agentID, Operation: operation, SessionID: sessionID, At: time.Now().UTC(), Result: result}
	const maxRetainedOutput = 512 << 10
	if len(entry.Result.Stdout) > maxRetainedOutput {
		entry.Result.Stdout = entry.Result.Stdout[:maxRetainedOutput]
		entry.Truncated = true
	}
	if len(entry.Result.Stderr) > maxRetainedOutput {
		entry.Result.Stderr = entry.Result.Stderr[:maxRetainedOutput]
		entry.Truncated = true
	}
	if err := store.SaveHostResult(entry); err == nil {
		if operation == "privileges" && result.Error == "" && result.ExitCode == 0 {
			m.persistAgentSnapshot(agentID)
			m.PublishEvent("agent.updated", agentID)
		}
		m.PublishEvent("host.result", agentID)
	}
}

func (s *OperationsStore) SaveHostResult(entry HostResult) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO host_results(agent_id,operation,session_id,at,result_json) VALUES(?,?,?,?,?) ON CONFLICT(agent_id,operation) DO UPDATE SET session_id=excluded.session_id,at=excluded.at,result_json=excluded.result_json`, entry.AgentID, entry.Operation, fmt.Sprint(entry.SessionID), entry.At.Format(time.RFC3339Nano), data)
	return err
}

// Prior explicit privilege results are authoritative observations for legacy
// agents that do not send a coarse classification in their inventory.
func (s *OperationsStore) LoadPrivilegeClassifications() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT agent_id,result_json FROM host_results WHERE operation='privileges'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var agentID string
		var data []byte
		if err := rows.Scan(&agentID, &data); err != nil {
			return nil, err
		}
		var entry HostResult
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, err
		}
		if entry.Result.Error == "" && entry.Result.ExitCode == 0 {
			if classification := classifyPrivileges(entry.Result.Stdout); classification != "" {
				out[agentID] = classification
			}
		}
	}
	return out, rows.Err()
}

func (s *OperationsStore) HostResults(agentID string) ([]HostResult, error) {
	rows, err := s.db.Query(`SELECT operation,session_id,at,result_json FROM host_results WHERE agent_id=? ORDER BY operation`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]HostResult, 0)
	for rows.Next() {
		entry := HostResult{AgentID: agentID}
		var session, at string
		var data []byte
		if err := rows.Scan(&entry.Operation, &session, &at, &data); err != nil {
			return nil, err
		}
		entry.SessionID, _ = strconv.ParseUint(session, 10, 64)
		entry.At, _ = time.Parse(time.RFC3339Nano, at)
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (m *Manager) hostResultsHandler(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		jsonReply(w, http.StatusOK, []HostResult{})
		return
	}
	results, err := store.HostResults(r.PathValue("id"))
	if err != nil {
		http.Error(w, "host history unavailable", http.StatusInternalServerError)
		return
	}
	jsonReply(w, http.StatusOK, results)
}
