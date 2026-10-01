package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"undertow/internal/control"
)

// consoleJobSelection keeps the numbers printed by the last jobs listing stable
// even if another job starts before the operator inspects one.
type consoleJobSelection struct {
	filter string
	ids    []string
}

func consoleJobs(ctx context.Context, call consoleCaller, agentID string) ([]control.JobInfo, error) {
	path := "/v1/jobs"
	if agentID != "" {
		path += "?agent_id=" + url.QueryEscape(agentID)
	}
	data, err := call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var jobs []control.JobInfo
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func runConsoleJobCommand(ctx context.Context, output io.Writer, call consoleCaller, args []string, selectedAgentID string, selection *consoleJobSelection) error {
	if args[0] == "jobs" {
		if len(args) >= 2 && (args[1] == "show" || args[1] == "output" || args[1] == "cancel") {
			args = append([]string{"job"}, args[1:]...)
		} else if len(args) == 2 && (selectedAgentID != "" || isJobNumber(args[1])) {
			args = []string{"job", "show", args[1]}
		} else {
			if len(args) > 2 {
				return errors.New("use jobs [AGENT_ID], jobs NUMBER, or jobs show|output|cancel NUMBER|ID")
			}
			filter := selectedAgentID
			if len(args) == 2 {
				filter = args[1]
			}
			jobs, err := consoleJobs(ctx, call, filter)
			if err != nil {
				return err
			}
			if selection != nil {
				selection.filter = filter
				selection.ids = make([]string, len(jobs))
			}
			fmt.Fprintf(output, "Jobs (%d):\n", len(jobs))
			for i, job := range jobs {
				if selection != nil {
					selection.ids[i] = job.ID
				}
				label := strings.Join(job.Argv, " ")
				if job.Kind == "script" {
					label = job.Language + " script"
				} else if job.Kind == "wasm" {
					label = strings.TrimSpace("WASM module " + label)
				}
				status := job.State
				if job.ExitCode != nil {
					status += fmt.Sprintf(" (%d)", *job.ExitCode)
				}
				fmt.Fprintf(output, "  %d  %-16s  %-14s  %-12s  %s  %s\n", i+1, job.ID, status, shortAgentID(job.AgentID), job.Started.Local().Format("2006-01-02 15:04:05"), label)
			}
			if len(jobs) > 0 {
				fmt.Fprintln(output, "Use job output NUMBER to read output, or job show NUMBER for details.")
			}
			return nil
		}
	}
	if args[0] == "job" && len(args) == 2 && isJobNumber(args[1]) {
		args = []string{"job", "show", args[1]}
	}
	if len(args) < 3 {
		return errors.New("use job start AGENT_ID PROGRAM [ARGS], job show ID, job output ID, or job cancel ID")
	}
	if args[1] == "start" {
		if len(args) < 4 {
			return errors.New("use job start AGENT_ID PROGRAM [ARGS]")
		}
		data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(args[2])+"/jobs", map[string]any{"argv": args[3:]})
		if err != nil {
			return err
		}
		var job control.JobInfo
		if err := json.Unmarshal(data, &job); err != nil {
			return err
		}
		fmt.Fprintf(output, "Job %s started on %s. Use job output %s, or run jobs for a numbered list.\n", job.ID, shortAgentID(job.AgentID), job.ID)
		return nil
	}
	if len(args) != 3 {
		return errors.New("use job show|output|cancel NUMBER|ID")
	}
	if args[1] != "show" && args[1] != "output" && args[1] != "cancel" {
		return errors.New("use job show|output|cancel NUMBER|ID")
	}
	jobID := args[2]
	if isJobNumber(jobID) {
		number, _ := strconv.Atoi(jobID)
		ids := []string(nil)
		if selection != nil && selection.ids != nil && selection.filter == selectedAgentID {
			ids = selection.ids
		} else {
			jobs, err := consoleJobs(ctx, call, selectedAgentID)
			if err != nil {
				return err
			}
			for _, job := range jobs {
				ids = append(ids, job.ID)
			}
			if selection != nil {
				selection.filter, selection.ids = selectedAgentID, ids
			}
		}
		if number < 1 || number > len(ids) {
			return fmt.Errorf("job number %s is not in the current jobs list; run jobs", jobID)
		}
		jobID = ids[number-1]
	}
	path := "/v1/jobs/" + url.PathEscape(jobID)
	if args[1] == "cancel" {
		_, err := call(ctx, http.MethodPost, path+"/cancel", nil)
		if err == nil {
			fmt.Fprintf(output, "Job %s cancelled.\n", jobID)
		}
		return err
	}
	if args[1] == "output" {
		path += "/output"
	}
	data, err := call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var job control.JobInfo
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	if args[1] == "output" {
		if job.Output == "" && !job.OutputTruncated {
			fmt.Fprintln(output, "[no output retained]")
			return nil
		}
		fmt.Fprint(output, job.Output)
		if job.OutputTruncated {
			fmt.Fprintln(output, "\n[earlier output truncated]")
		}
		return nil
	}
	fmt.Fprintf(output, "Job %s  agent=%s  type=%s  state=%s  started=%s\n", job.ID, job.AgentID, job.Kind, job.State, job.Started.Local().Format(time.RFC3339))
	if job.Ended != nil {
		fmt.Fprintf(output, "Ended: %s\n", job.Ended.Local().Format(time.RFC3339))
	}
	if job.ExitCode != nil {
		fmt.Fprintf(output, "Exit: %d\n", *job.ExitCode)
	}
	fmt.Fprintf(output, "Output: %d bytes (retained up to 256 KiB)\n", job.OutputBytes)
	return nil
}

func isJobNumber(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
