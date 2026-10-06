package control

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Team messages and assignments are shared server state. Names are snapshots;
// IDs are bound to the authenticated operator session by the server.
type TeamMessage struct {
	ID          int64     `json:"id"`
	SentAt      time.Time `json:"sent_at"`
	SenderID    string    `json:"sender_id"`
	SenderName  string    `json:"sender_name"`
	RecipientID string    `json:"recipient_id,omitempty"`
	Kind        string    `json:"kind"`
	Body        string    `json:"body"`
	TaskID      string    `json:"task_id,omitempty"`
}

type TeamTask struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	CreatorID    string    `json:"creator_id"`
	CreatorName  string    `json:"creator_name"`
	AssigneeID   string    `json:"assignee_id"`
	AssigneeName string    `json:"assignee_name"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

var ErrTeamTaskNotFound = errors.New("task not found")
var ErrTeamTaskForbidden = errors.New("only the assignee, creator, or Team Leader can update this task")

func validTeamText(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 32 && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func teamTime(value string) (time.Time, error) { return time.Parse(time.RFC3339Nano, value) }

func scanTeamMessage(rows *sql.Rows) (TeamMessage, error) {
	var item TeamMessage
	var at string
	err := rows.Scan(&item.ID, &at, &item.SenderID, &item.SenderName, &item.RecipientID, &item.Kind, &item.Body, &item.TaskID)
	if err == nil {
		item.SentAt, err = teamTime(at)
	}
	return item, err
}

func scanTeamTask(row interface{ Scan(...any) error }) (TeamTask, error) {
	var item TeamTask
	var created, updated string
	err := row.Scan(&item.ID, &item.Title, &item.Description, &item.CreatorID, &item.CreatorName, &item.AssigneeID, &item.AssigneeName, &item.Status, &created, &updated)
	if err == nil {
		item.CreatedAt, err = teamTime(created)
	}
	if err == nil {
		item.UpdatedAt, err = teamTime(updated)
	}
	return item, err
}

func (s *OperationsStore) TeamMessages(actorID, peer string, before, after int64) ([]TeamMessage, error) {
	if !operatorIDPattern.MatchString(actorID) || peer != "" && (!operatorIDPattern.MatchString(peer) || peer == actorID) || before < 0 || after < 0 || before > 0 && after > 0 {
		return nil, errors.New("invalid team conversation cursor")
	}
	query := `SELECT id,sent_at,sender_id,sender_name,recipient_id,kind,body,task_id FROM team_messages WHERE `
	args := []any{}
	if peer == "" {
		query += `recipient_id=''`
	} else {
		query += `((sender_id=? AND recipient_id=?) OR (sender_id=? AND recipient_id=?))`
		args = append(args, actorID, peer, peer, actorID)
	}
	if before > 0 {
		query += ` AND id<?`
		args = append(args, before)
	}
	if after > 0 {
		query += ` AND id>?`
		args = append(args, after)
	}
	ascending := after > 0
	if ascending {
		query += ` ORDER BY id ASC LIMIT 100`
	} else {
		query += ` ORDER BY id DESC LIMIT 100`
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMessage{}
	for rows.Next() {
		item, err := scanTeamMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !ascending {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

func (s *OperationsStore) PostTeamMessage(sender OperatorAccount, recipient, body string) (TeamMessage, error) {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if !validTeamText(body, 4096) {
		return TeamMessage{}, errors.New("message must contain 1-4096 UTF-8 bytes")
	}
	if recipient != "" {
		if !operatorIDPattern.MatchString(recipient) || recipient == sender.ID {
			return TeamMessage{}, errors.New("invalid message recipient")
		}
		a, err := s.Operator(recipient)
		if err != nil || a.Disabled || a.Revoked {
			return TeamMessage{}, errors.New("message recipient is unavailable")
		}
	}
	at := time.Now().UTC()
	result, err := s.db.Exec(`INSERT INTO team_messages(sent_at,sender_id,sender_name,recipient_id,kind,body,task_id) VALUES(?,?,?,?,?,?,?)`, at.Format(time.RFC3339Nano), sender.ID, sender.DisplayName, recipient, "text", body, "")
	if err != nil {
		return TeamMessage{}, err
	}
	id, err := result.LastInsertId()
	return TeamMessage{ID: id, SentAt: at, SenderID: sender.ID, SenderName: sender.DisplayName, RecipientID: recipient, Kind: "text", Body: body}, err
}

func (s *OperationsStore) TeamTasks() ([]TeamTask, error) {
	rows, err := s.db.Query(`SELECT id,title,description,creator_id,creator_name,assignee_id,assignee_name,status,created_at,updated_at FROM team_tasks ORDER BY updated_at DESC,id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamTask{}
	for rows.Next() {
		item, err := scanTeamTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *OperationsStore) CreateTeamTask(creator OperatorAccount, assigneeID, title, description string) (TeamTask, error) {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(strings.ReplaceAll(description, "\r\n", "\n"))
	if !validTeamText(title, 160) || strings.ContainsAny(title, "\n\t") || description != "" && !validTeamText(description, 4000) {
		return TeamTask{}, errors.New("task title must be 1-160 bytes and description at most 4000 bytes")
	}
	if !operatorIDPattern.MatchString(assigneeID) {
		return TeamTask{}, errors.New("invalid assignee")
	}
	assignee, err := s.Operator(assigneeID)
	if err != nil || assignee.Disabled || assignee.Revoked {
		return TeamTask{}, errors.New("assignee is unavailable")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return TeamTask{}, err
	}
	at := time.Now().UTC()
	task := TeamTask{ID: hex.EncodeToString(random[:]), Title: title, Description: description, CreatorID: creator.ID, CreatorName: creator.DisplayName, AssigneeID: assignee.ID, AssigneeName: assignee.DisplayName, Status: "open", CreatedAt: at, UpdatedAt: at}
	tx, err := s.db.Begin()
	if err != nil {
		return TeamTask{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO team_tasks(id,title,description,creator_id,creator_name,assignee_id,assignee_name,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, task.ID, task.Title, task.Description, task.CreatorID, task.CreatorName, task.AssigneeID, task.AssigneeName, task.Status, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		return TeamTask{}, err
	}
	if _, err := tx.Exec(`INSERT INTO team_messages(sent_at,sender_id,sender_name,recipient_id,kind,body,task_id) VALUES(?,?,?,?,?,?,?)`, at.Format(time.RFC3339Nano), creator.ID, creator.DisplayName, "", "task_created", title, task.ID); err != nil {
		return TeamTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return TeamTask{}, err
	}
	return task, nil
}

func (s *OperationsStore) UpdateTeamTask(actor OperatorAccount, id, status string) (TeamTask, error) {
	if len(id) != 24 || strings.Trim(id, "0123456789abcdef") != "" {
		return TeamTask{}, errors.New("invalid task ID")
	}
	switch status {
	case "open", "in_progress", "done", "cancelled":
	default:
		return TeamTask{}, errors.New("invalid task status")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TeamTask{}, err
	}
	defer tx.Rollback()
	task, err := scanTeamTask(tx.QueryRow(`SELECT id,title,description,creator_id,creator_name,assignee_id,assignee_name,status,created_at,updated_at FROM team_tasks WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return TeamTask{}, ErrTeamTaskNotFound
	}
	if err != nil {
		return TeamTask{}, err
	}
	if actor.ID != task.CreatorID && actor.ID != task.AssigneeID && actor.Role != TeamLeaderRole {
		return TeamTask{}, ErrTeamTaskForbidden
	}
	if task.Status == status {
		return task, nil
	}
	task.Status = status
	task.UpdatedAt = time.Now().UTC()
	if _, err := tx.Exec(`UPDATE team_tasks SET status=?,updated_at=? WHERE id=?`, status, task.UpdatedAt.Format(time.RFC3339Nano), id); err != nil {
		return TeamTask{}, err
	}
	if _, err := tx.Exec(`INSERT INTO team_messages(sent_at,sender_id,sender_name,recipient_id,kind,body,task_id) VALUES(?,?,?,?,?,?,?)`, task.UpdatedAt.Format(time.RFC3339Nano), actor.ID, actor.DisplayName, "", "task_updated", fmt.Sprintf("%s: %s", task.Title, status), id); err != nil {
		return TeamTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return TeamTask{}, err
	}
	return task, nil
}
