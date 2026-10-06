package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"undertow/internal/control"
)

func runConsoleTeam(ctx context.Context, output io.Writer, call consoleCaller, args []string) error {
	usage := errors.New(`use team [roster|say MESSAGE|dm OPERATOR [MESSAGE]|tasks|task add ASSIGNEE "TITLE" ["DESCRIPTION"]|task show ID|task start|done|reopen|cancel ID]`)
	if len(args) == 1 || len(args) == 2 && args[1] == "show" {
		return printTeamConversation(ctx, output, call, "")
	}
	switch args[1] {
	case "roster":
		if len(args) != 2 {
			return usage
		}
		data, err := call(ctx, http.MethodGet, "/v1/team/operators", nil)
		if err != nil {
			return err
		}
		var operators []control.OperatorAccount
		if err := json.Unmarshal(data, &operators); err != nil {
			return err
		}
		for _, operator := range operators {
			fmt.Fprintf(output, "%-20s %-14s %s\n", operator.ID, operator.Role, operator.DisplayName)
		}
		return nil
	case "say":
		if len(args) < 3 {
			return usage
		}
		return sendTeamMessage(ctx, output, call, "", strings.Join(args[2:], " "))
	case "dm":
		if len(args) < 3 {
			return usage
		}
		if len(args) == 3 {
			return printTeamConversation(ctx, output, call, args[2])
		}
		return sendTeamMessage(ctx, output, call, args[2], strings.Join(args[3:], " "))
	case "tasks":
		if len(args) != 2 {
			return usage
		}
		return printTeamTasks(ctx, output, call, "")
	case "task":
		if len(args) < 3 {
			return usage
		}
		switch args[2] {
		case "add":
			if len(args) != 5 && len(args) != 6 {
				return usage
			}
			description := ""
			if len(args) == 6 {
				description = args[5]
			}
			data, err := call(ctx, http.MethodPost, "/v1/team/tasks", map[string]string{"assignee_id": args[3], "title": args[4], "description": description})
			if err != nil {
				return err
			}
			var task control.TeamTask
			if err := json.Unmarshal(data, &task); err != nil {
				return err
			}
			fmt.Fprintf(output, "Assigned %s to %s: %s\n", task.ID, task.AssigneeName, task.Title)
			return nil
		case "show":
			if len(args) != 4 {
				return usage
			}
			return printTeamTasks(ctx, output, call, args[3])
		case "start", "done", "reopen", "cancel":
			if len(args) != 4 {
				return usage
			}
			status := map[string]string{"start": "in_progress", "done": "done", "reopen": "open", "cancel": "cancelled"}[args[2]]
			data, err := call(ctx, http.MethodPut, "/v1/team/tasks/"+url.PathEscape(args[3]), map[string]string{"status": status})
			if err != nil {
				return err
			}
			var task control.TeamTask
			if err := json.Unmarshal(data, &task); err != nil {
				return err
			}
			fmt.Fprintf(output, "%s: %s — %s\n", task.ID, task.Title, task.Status)
			return nil
		}
	}
	return usage
}

func sendTeamMessage(ctx context.Context, output io.Writer, call consoleCaller, recipient, body string) error {
	_, err := call(ctx, http.MethodPost, "/v1/team/messages", map[string]string{"recipient_id": recipient, "body": body})
	if err == nil {
		if recipient == "" {
			fmt.Fprintln(output, "Message posted to Team.")
		} else {
			fmt.Fprintf(output, "Direct message sent to %s.\n", recipient)
		}
	}
	return err
}

func printTeamConversation(ctx context.Context, output io.Writer, call consoleCaller, peer string) error {
	path := "/v1/team/messages"
	if peer != "" {
		path += "?peer=" + url.QueryEscape(peer)
	}
	data, err := call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var messages []control.TeamMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return err
	}
	if len(messages) == 0 {
		fmt.Fprintln(output, "No messages in this conversation.")
		return nil
	}
	for _, message := range messages {
		body := message.Body
		if message.TaskID != "" {
			body = fmt.Sprintf("[%s %s] %s", strings.ReplaceAll(message.Kind, "_", " "), message.TaskID, body)
		}
		fmt.Fprintf(output, "%s  %-18s %s\n", message.SentAt.Local().Format(time.DateTime), message.SenderName, body)
	}
	return nil
}

func printTeamTasks(ctx context.Context, output io.Writer, call consoleCaller, id string) error {
	data, err := call(ctx, http.MethodGet, "/v1/team/tasks", nil)
	if err != nil {
		return err
	}
	var tasks []control.TeamTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		return err
	}
	for _, task := range tasks {
		if id != "" && task.ID != id {
			continue
		}
		fmt.Fprintf(output, "%-24s %-12s %-18s %s\n", task.ID, task.Status, task.AssigneeName, task.Title)
		if id != "" {
			fmt.Fprintf(output, "Created by %s · %s\n%s\n", task.CreatorName, task.CreatedAt.Local().Format(time.DateTime), task.Description)
			return nil
		}
	}
	if id != "" {
		return errors.New("task not found in recent assignments")
	}
	if len(tasks) == 0 {
		fmt.Fprintln(output, "No team assignments yet.")
	}
	return nil
}
