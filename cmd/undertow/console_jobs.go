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

func runConsoleJobCommand(ctx context.Context, output io.Writer, call consoleCaller, args []string) error {
	if args[0] == "jobs" {
		if len(args) > 2 {
			return errors.New("use jobs [AGENT_ID]")
		}
		path := "/v1/jobs"
		if len(args) == 2 {
			path += "?agent_id=" + url.QueryEscape(args[1])
		}
		data, err := call(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var jobs []control.JobInfo
		if err := json.Unmarshal(data, &jobs); err != nil {
			return err
		}
		fmt.Fprintf(output, "Jobs (%d):\n", len(jobs))
		for _, job := range jobs {
			fmt.Fprintf(output, "  %s  %-10s  %-12s  %s  %s\n", job.ID, job.State, shortAgentID(job.AgentID), job.Started.Local().Format("2006-01-02 15:04:05"), strings.Join(job.Argv, " "))
		}
		return nil
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
		fmt.Fprintf(output, "Job %s started on %s. Use job output %s to inspect it.\n", job.ID, shortAgentID(job.AgentID), job.ID)
		return nil
	}
	if len(args) != 3 {
		return errors.New("use job show|output|cancel ID")
	}
	path := "/v1/jobs/" + url.PathEscape(args[2])
	if args[1] == "cancel" {
		_, err := call(ctx, http.MethodPost, path+"/cancel", nil)
		if err == nil {
			fmt.Fprintf(output, "Job %s cancelled.\n", args[2])
		}
		return err
	}
	if args[1] == "output" {
		path += "/output"
	} else if args[1] != "show" {
		return errors.New("use job show|output|cancel ID")
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
		fmt.Fprint(output, job.Output)
		if job.OutputTruncated {
			fmt.Fprintln(output, "\n[earlier output truncated]")
		}
		return nil
	}
	fmt.Fprintf(output, "Job %s  agent=%s  state=%s  started=%s\n", job.ID, job.AgentID, job.State, job.Started.Local().Format(time.RFC3339))
	if job.Ended != nil {
		fmt.Fprintf(output, "Ended: %s\n", job.Ended.Local().Format(time.RFC3339))
	}
	if job.ExitCode != nil {
		fmt.Fprintf(output, "Exit: %d\n", *job.ExitCode)
	}
	fmt.Fprintf(output, "Output: %d bytes (retained up to 256 KiB)\n", job.OutputBytes)
	return nil
}
