package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"undertow/internal/pivot"
)

const screenshotChunkSize = 256 << 10
const screenshotRetentionCount = 1000
const screenshotRetentionDays = 90

var screenshotIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type ScreenshotInfo struct {
	ID              string    `json:"id"`
	AgentID         string    `json:"agent_id"`
	Screen          int       `json:"screen"`
	At              time.Time `json:"at"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"sha256"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	ClientID        string    `json:"client_id,omitempty"`
	ClientSessionID uint64    `json:"client_session_id,omitempty"`
	OperatorID      string    `json:"operator_id,omitempty"`
	DisplayName     string    `json:"display_name,omitempty"`
}

func (m *Manager) screensHandler(w http.ResponseWriter, r *http.Request) {
	agent := m.Get(r.PathValue("id"))
	if agent == nil {
		http.Error(w, "agent is not connected", http.StatusNotFound)
		return
	}
	// Older agents already report a JSON screen list through this typed host
	// operation. The browser receives only the validated server contract.
	result, err := pivot.ExecuteRequest(r.Context(), agent, pivot.ExecRequest{Builtin: "screens"})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if result.Error != "" {
		http.Error(w, result.Error, http.StatusUnprocessableEntity)
		return
	}
	screens := result.Screens
	if screens == nil {
		// Compatibility with agents enrolled before the structured result.
		if err := json.Unmarshal([]byte(result.Stdout), &screens); err != nil {
			http.Error(w, "invalid agent screen list", http.StatusBadGateway)
			return
		}
	}
	if screens == nil {
		screens = []pivot.ScreenInfo{}
	}
	jsonReply(w, http.StatusOK, screens)
}

func (m *Manager) captureScreenshotHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Screen int `json:"screen"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request) != nil || request.Screen < 1 || request.Screen > 64 {
		http.Error(w, "select a valid screen number", http.StatusBadRequest)
		return
	}
	agentID := r.PathValue("id")
	agent := m.Get(agentID)
	if agent == nil {
		http.Error(w, "agent is not connected", http.StatusNotFound)
		return
	}
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		http.Error(w, "screenshot store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := os.MkdirAll(store.screenshotsDir, 0700); err != nil {
		http.Error(w, "screenshot store unavailable", http.StatusInternalServerError)
		return
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		http.Error(w, "screenshot ID unavailable", http.StatusInternalServerError)
		return
	}
	id := hex.EncodeToString(random[:])
	path := store.screenshotPath(id)
	transfer, err := pivot.TransferFile(r.Context(), agent, agentID, "screenshot", path, strconv.Itoa(request.Screen))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "captured image unavailable", http.StatusInternalServerError)
		return
	}
	config, decodeErr := png.DecodeConfig(file)
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil || config.Width < 1 || config.Height < 1 || transfer.Size < 8 {
		_ = os.Remove(path)
		http.Error(w, "agent returned an invalid PNG", http.StatusBadGateway)
		return
	}
	actor := boundActionFromContext(r.Context())
	entry := ScreenshotInfo{ID: id, AgentID: agentID, Screen: request.Screen, At: time.Now().UTC(), Size: transfer.Size, SHA256: transfer.SHA256, Width: config.Width, Height: config.Height, ClientID: actor.ClientID, ClientSessionID: actor.ClientSessionID, OperatorID: actor.OperatorID, DisplayName: actor.DisplayName}
	if err := store.SaveScreenshot(entry); err != nil {
		_ = os.Remove(path)
		http.Error(w, "could not retain screenshot metadata", http.StatusInternalServerError)
		return
	}
	m.PublishEvent("screenshot.created", agentID)
	jsonReply(w, http.StatusCreated, entry)
}

func (m *Manager) screenshotsHandler(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		jsonReply(w, http.StatusOK, []ScreenshotInfo{})
		return
	}
	entries, err := store.Screenshots(r.URL.Query().Get("agent_id"))
	if err != nil {
		http.Error(w, "screenshot history unavailable", http.StatusInternalServerError)
		return
	}
	jsonReply(w, http.StatusOK, entries)
}

func (m *Manager) screenshotHandler(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		http.Error(w, "screenshot not found", http.StatusNotFound)
		return
	}
	entry, err := store.Screenshot(r.PathValue("id"))
	if err != nil {
		http.Error(w, "screenshot not found", http.StatusNotFound)
		return
	}
	jsonReply(w, http.StatusOK, entry)
}

func (m *Manager) screenshotChunkHandler(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	store := m.operations
	m.mu.RUnlock()
	if store == nil {
		http.Error(w, "screenshot not found", http.StatusNotFound)
		return
	}
	entry, err := store.Screenshot(r.PathValue("id"))
	if err != nil {
		http.Error(w, "screenshot not found", http.StatusNotFound)
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 || offset >= entry.Size {
		http.Error(w, "invalid chunk offset", http.StatusBadRequest)
		return
	}
	file, err := os.Open(store.screenshotPath(entry.ID))
	if err != nil {
		http.Error(w, "screenshot image unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	length := entry.Size - offset
	if length > screenshotChunkSize {
		length = screenshotChunkSize
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.CopyN(w, io.NewSectionReader(file, offset, length), length)
}

func (s *OperationsStore) screenshotPath(id string) string {
	return filepath.Join(s.screenshotsDir, id+".png")
}

func (s *OperationsStore) SaveScreenshot(entry ScreenshotInfo) error {
	if !screenshotIDPattern.MatchString(entry.ID) {
		return errors.New("invalid screenshot ID")
	}
	_, err := s.db.Exec(`INSERT INTO screenshots(id,agent_id,screen,at,size,sha256,width,height,client_id,client_session_id,operator_id,display_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, entry.ID, entry.AgentID, entry.Screen, entry.At.Format(time.RFC3339Nano), entry.Size, entry.SHA256, entry.Width, entry.Height, entry.ClientID, strconv.FormatUint(entry.ClientSessionID, 10), entry.OperatorID, entry.DisplayName)
	if err != nil {
		return err
	}
	if err := s.pruneScreenshots(); err != nil {
		log.Printf("prune screenshots: %v", err)
	}
	return nil
}

func (s *OperationsStore) Screenshots(agentID string) ([]ScreenshotInfo, error) {
	query := `SELECT id,agent_id,screen,at,size,sha256,width,height,client_id,client_session_id,operator_id,display_name FROM screenshots`
	var args []any
	if agentID != "" {
		query += ` WHERE agent_id=?`
		args = append(args, agentID)
	}
	query += ` ORDER BY at DESC LIMIT 1000`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScreenshotInfo, 0)
	for rows.Next() {
		var entry ScreenshotInfo
		var at, session string
		if err := rows.Scan(&entry.ID, &entry.AgentID, &entry.Screen, &at, &entry.Size, &entry.SHA256, &entry.Width, &entry.Height, &entry.ClientID, &session, &entry.OperatorID, &entry.DisplayName); err != nil {
			return nil, err
		}
		entry.At, _ = time.Parse(time.RFC3339Nano, at)
		entry.ClientSessionID, _ = strconv.ParseUint(session, 10, 64)
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *OperationsStore) Screenshot(id string) (ScreenshotInfo, error) {
	var entry ScreenshotInfo
	if !screenshotIDPattern.MatchString(id) {
		return entry, errors.New("invalid screenshot ID")
	}
	var at, session string
	err := s.db.QueryRow(`SELECT id,agent_id,screen,at,size,sha256,width,height,client_id,client_session_id,operator_id,display_name FROM screenshots WHERE id=?`, id).Scan(&entry.ID, &entry.AgentID, &entry.Screen, &at, &entry.Size, &entry.SHA256, &entry.Width, &entry.Height, &entry.ClientID, &session, &entry.OperatorID, &entry.DisplayName)
	if err != nil {
		return entry, err
	}
	entry.At, _ = time.Parse(time.RFC3339Nano, at)
	entry.ClientSessionID, _ = strconv.ParseUint(session, 10, 64)
	return entry, nil
}

func (s *OperationsStore) pruneScreenshots() error {
	cutoff := time.Now().UTC().AddDate(0, 0, -screenshotRetentionDays).Format(time.RFC3339Nano)
	rows, err := s.db.Query(`SELECT id FROM screenshots WHERE at < ? OR id NOT IN (SELECT id FROM screenshots ORDER BY at DESC LIMIT ?)`, cutoff, screenshotRetentionCount)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !screenshotIDPattern.MatchString(id) {
			continue
		}
		if _, err := s.db.Exec(`DELETE FROM screenshots WHERE id=?`, id); err != nil {
			return err
		}
		_ = os.Remove(s.screenshotPath(id))
	}
	return nil
}
