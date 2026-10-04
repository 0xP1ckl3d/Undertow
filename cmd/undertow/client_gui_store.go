//go:build linux || windows

package main

import (
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type guiPreferences struct {
	OperatorID  string `json:"operator_id"`
	DisplayName string `json:"display_name"`
}

type clientGUIStore struct{ db *sql.DB }

type guiModuleRef struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Format   string `json:"format,omitempty"`
}

type guiPosition struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

func openClientGUIStore(path string) (*clientGUIStore, error) {
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
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "CREATE TABLE IF NOT EXISTS preferences (key TEXT PRIMARY KEY, value TEXT NOT NULL)", "CREATE TABLE IF NOT EXISTS topology_layout (id TEXT PRIMARY KEY, x REAL NOT NULL, y REAL NOT NULL)", "CREATE TABLE IF NOT EXISTS gui_modules (name TEXT PRIMARY KEY, kind TEXT NOT NULL, path TEXT NOT NULL, filename TEXT NOT NULL, format TEXT NOT NULL)", "CREATE TABLE IF NOT EXISTS console_entries (id INTEGER PRIMARY KEY AUTOINCREMENT, agent_id TEXT NOT NULL, at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, source TEXT NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL)", "CREATE INDEX IF NOT EXISTS console_entries_agent ON console_entries(agent_id,id)", "PRAGMA user_version=4"} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := os.Chmod(abs, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return &clientGUIStore{db: db}, nil
}

func (s *clientGUIStore) Close() error { return s.db.Close() }

type guiConsoleEntry struct {
	ID     int64  `json:"id"`
	At     string `json:"at"`
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
}

func (s *clientGUIStore) ConsoleEntries(agentID string) ([]guiConsoleEntry, error) {
	rows, err := s.db.Query("SELECT id,at,source,kind,text FROM (SELECT id,at,source,kind,text FROM console_entries WHERE agent_id=? ORDER BY id DESC LIMIT 1000) ORDER BY id", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]guiConsoleEntry, 0)
	for rows.Next() {
		var entry guiConsoleEntry
		if err := rows.Scan(&entry.ID, &entry.At, &entry.Source, &entry.Kind, &entry.Text); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *clientGUIStore) AppendConsoleEntry(agentID, source, kind, value string) error {
	if agentID == "" || len(agentID) > 256 || strings.ContainsAny(agentID, "/\\\r\n\x00") {
		return errors.New("invalid agent ID")
	}
	if source != "console" && source != "modules" {
		return errors.New("invalid console source")
	}
	if kind != "command" && kind != "output" && kind != "error" && kind != "stderr" && kind != "exit" {
		return errors.New("invalid console entry kind")
	}
	if len(value) > 64<<10 {
		value = value[:64<<10] + "\n[output truncated in local history]"
	}
	_, err := s.db.Exec("INSERT INTO console_entries(agent_id,source,kind,text) VALUES(?,?,?,?)", agentID, source, kind, value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("DELETE FROM console_entries WHERE agent_id=? AND id NOT IN (SELECT id FROM console_entries WHERE agent_id=? ORDER BY id DESC LIMIT 1000)", agentID, agentID)
	return err
}

func (s *clientGUIStore) ModuleRefs() ([]guiModuleRef, error) {
	rows, err := s.db.Query("SELECT name,kind,path,filename,format FROM gui_modules ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]guiModuleRef, 0)
	for rows.Next() {
		var ref guiModuleRef
		if err := rows.Scan(&ref.Name, &ref.Kind, &ref.Path, &ref.Filename, &ref.Format); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (s *clientGUIStore) SaveModuleRef(ref guiModuleRef) error {
	_, err := s.db.Exec("INSERT INTO gui_modules(name,kind,path,filename,format) VALUES(?,?,?,?,?)", ref.Name, ref.Kind, ref.Path, ref.Filename, ref.Format)
	return err
}

func (s *clientGUIStore) DeleteModuleRef(name string) error {
	_, err := s.db.Exec("DELETE FROM gui_modules WHERE name=?", name)
	return err
}

func (s *clientGUIStore) Layout() ([]guiPosition, error) {
	rows, err := s.db.Query("SELECT id,x,y FROM topology_layout ORDER BY id LIMIT 5000")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]guiPosition, 0)
	for rows.Next() {
		var p guiPosition
		if err := rows.Scan(&p.ID, &p.X, &p.Y); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *clientGUIStore) SavePosition(p guiPosition) error {
	if p.ID == "" || len(p.ID) > 300 || strings.ContainsAny(p.ID, "\r\n\x00") || math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) || math.Abs(p.X) > 100000 || math.Abs(p.Y) > 100000 {
		return errors.New("invalid topology position")
	}
	_, err := s.db.Exec("INSERT INTO topology_layout(id,x,y) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET x=excluded.x,y=excluded.y", p.ID, p.X, p.Y)
	return err
}

func (s *clientGUIStore) Preferences() (guiPreferences, error) {
	var p guiPreferences
	rows, err := s.db.Query("SELECT key,value FROM preferences WHERE key IN ('operator_id','display_name')")
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return p, err
		}
		if key == "operator_id" {
			p.OperatorID = value
		} else {
			p.DisplayName = value
		}
	}
	return p, rows.Err()
}

func (s *clientGUIStore) SavePreferences(p guiPreferences) error {
	p.OperatorID = strings.TrimSpace(p.OperatorID)
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	if len(p.OperatorID) > 128 || len(p.DisplayName) > 128 || strings.ContainsAny(p.OperatorID, "\r\n\x00") || strings.ContainsAny(p.DisplayName, "\r\n\x00") {
		return errors.New("operator identity fields must be at most 128 characters without control characters")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{"operator_id": p.OperatorID, "display_name": p.DisplayName} {
		if _, err := tx.Exec("INSERT INTO preferences(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
