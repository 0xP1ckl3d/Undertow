package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"undertow/internal/bof"
	"undertow/internal/control"
)

// consoleJobSelection keeps the numbers printed by the last jobs listing stable
// even if another job starts before the operator inspects one.
type consoleJobSelection struct {
	ids []string
}

const jobConsoleOutputLimit = 64 << 10

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

func runConsoleJobCommand(ctx context.Context, output io.Writer, call consoleCaller, args []string, selectedAgentID string, selection *consoleJobSelection, confirmDownload ...func(string) bool) error {
	if args[0] == "jobs" {
		if len(args) >= 2 && (args[1] == "show" || args[1] == "output" || args[1] == "save" || args[1] == "file" || args[1] == "delete" || args[1] == "cancel" || args[1] == "stop") {
			args = append([]string{"job"}, args[1:]...)
		} else if len(args) == 2 && (selectedAgentID != "" || isJobNumber(args[1])) {
			args = []string{"job", "show", args[1]}
		} else {
			if len(args) > 2 {
				return errors.New("use jobs [AGENT_ID], jobs NUMBER, or jobs show|output|cancel|stop NUMBER|ID")
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
				} else if job.Kind == "native" {
					label = strings.TrimSpace("native module " + label)
				} else if job.Kind == "assembly" {
					label = strings.TrimSpace(".NET assembly " + label)
				} else if job.Kind == "bof" {
					label = strings.TrimSpace("BOF " + label)
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
		return errors.New("use job start AGENT_ID PROGRAM [ARGS], job show ID, job output ID, or job stop ID")
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
	if len(args) != 3 && !(len(args) == 4 && (args[1] == "save" || args[1] == "file")) && !(len(args) == 5 && args[1] == "file") {
		return errors.New("use job show|output|save|file|delete|cancel|stop NUMBER|ID [FILE_ID] [LOCAL_FILE]")
	}
	if args[1] != "show" && args[1] != "output" && args[1] != "save" && args[1] != "file" && args[1] != "delete" && args[1] != "cancel" && args[1] != "stop" {
		return errors.New("use job show|output|save|file|delete|cancel|stop NUMBER|ID [FILE_ID] [LOCAL_FILE]")
	}
	jobID := args[2]
	if isJobNumber(jobID) {
		number, _ := strconv.Atoi(jobID)
		ids := []string(nil)
		if selection != nil && selection.ids != nil {
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
				selection.ids = ids
			}
		}
		if number < 1 || number > len(ids) {
			return fmt.Errorf("job number %s is not in the current jobs list; run jobs", jobID)
		}
		jobID = ids[number-1]
	}
	path := "/v1/jobs/" + url.PathEscape(jobID)
	if args[1] == "cancel" || args[1] == "stop" {
		_, err := call(ctx, http.MethodPost, path+"/cancel", nil)
		if err == nil {
			fmt.Fprintf(output, "Job %s cancelled.\n", jobID)
		}
		return err
	}
	if args[1] == "delete" {
		_, err := call(ctx, http.MethodDelete, path, nil)
		if err == nil {
			fmt.Fprintf(output, "Job %s and its server output deleted.\n", jobID)
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
	if args[1] == "save" {
		localPath := ""
		if len(args) == 4 {
			localPath = args[3]
		}
		return saveJobOutput(ctx, output, call, job, localPath)
	}
	if args[1] == "file" {
		if len(args) < 4 {
			return errors.New("use job file NUMBER|ID FILE_ID [LOCAL_FILE]")
		}
		id, err := strconv.ParseUint(args[3], 10, 32)
		if err != nil {
			return errors.New("invalid BOF file ID")
		}
		for _, file := range job.Files {
			if file.ID == uint32(id) {
				localPath := ""
				if len(args) == 5 {
					localPath = args[4]
				}
				return saveJobFile(ctx, output, call, job, file, localPath)
			}
		}
		return fmt.Errorf("job file %d not found", id)
	}
	if args[1] == "output" {
		if job.OutputBytes > jobConsoleOutputLimit {
			question := fmt.Sprintf("Job output is %d bytes, too large for the console. Download the complete output to this client? [Y/n] ", job.OutputBytes)
			if len(confirmDownload) > 0 && confirmDownload[0] != nil && confirmDownload[0](question) {
				return saveJobOutput(ctx, output, call, job, "")
			}
			fmt.Fprintf(output, "Use job save %s [LOCAL_FILE] to download it.\n", job.ID)
			return nil
		}
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
	fmt.Fprintf(output, "Output: %d bytes\n", job.OutputBytes)
	for _, file := range job.Files {
		fmt.Fprintf(output, "File %d: %s (%d bytes). Download with job file %s %d\n", file.ID, file.Name, file.Size, job.ID, file.ID)
	}
	if job.OutputFile != "" {
		fmt.Fprintf(output, "Server file: %s\n", job.OutputFile)
	}
	if job.OutputError != "" {
		fmt.Fprintf(output, "Output error: %s\n", job.OutputError)
	}
	return nil
}

func saveJobOutput(ctx context.Context, output io.Writer, call consoleCaller, job control.JobInfo, destination string) error {
	if destination == "" {
		name := job.ID + ".out"
		if job.State != "completed" {
			name = job.ID + ".partial.out"
		}
		destination = filepath.Join("outputs", "jobs", job.AgentID, name)
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("output file already exists: %s", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := prepareClientOutputDirectory(filepath.Dir(abs)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(abs), ".undertow-job-*.partial")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	var offset uint64
	var nextProgress uint64 = 8 << 20
	if job.OutputBytes >= 8<<20 {
		fmt.Fprintf(output, "Downloading %d bytes of job output...\n", job.OutputBytes)
	}
	for offset < job.OutputBytes {
		data, err := call(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(job.ID)+"/output/chunk?offset="+strconv.FormatUint(offset, 10), nil)
		if err != nil {
			return err
		}
		var chunk control.JobOutputChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		if chunk.Offset != offset || len(chunk.Data) == 0 {
			return errors.New("invalid or incomplete job output chunk")
		}
		if uint64(len(chunk.Data)) > job.OutputBytes-offset {
			chunk.Data = chunk.Data[:job.OutputBytes-offset]
		}
		if _, err := temporary.Write(chunk.Data); err != nil {
			return err
		}
		offset += uint64(len(chunk.Data))
		if offset >= nextProgress && offset < job.OutputBytes {
			fmt.Fprintf(output, "Downloaded %d / %d MiB\n", offset>>20, job.OutputBytes>>20)
			nextProgress += 8 << 20
		}
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := publishJobDownload(temporary.Name(), abs); err != nil {
		return err
	}
	if err := setLocalOutputOwner(abs); err != nil {
		_ = os.Remove(abs)
		return err
	}
	fmt.Fprintf(output, "Saved %d bytes to %s\n", offset, abs)
	if job.State != "completed" {
		fmt.Fprintf(output, "Job state is %s; this is a snapshot of the output available now.\n", job.State)
	}
	return nil
}

func saveJobFile(ctx context.Context, output io.Writer, call consoleCaller, job control.JobInfo, artifact bof.FileArtifact, destination string) error {
	if destination == "" {
		destination = filepath.Join("outputs", "jobs", job.AgentID, job.ID, artifact.Name)
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("output file already exists: %s", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := prepareClientOutputDirectory(filepath.Dir(abs)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(abs), ".undertow-file-*.partial")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	var offset uint64
	for offset < artifact.Size {
		path := fmt.Sprintf("/v1/jobs/%s/files/%d/chunk?offset=%d", url.PathEscape(job.ID), artifact.ID, offset)
		data, err := call(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var chunk control.JobOutputChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		if chunk.Offset != offset || len(chunk.Data) == 0 || uint64(len(chunk.Data)) > artifact.Size-offset {
			return errors.New("invalid or incomplete BOF file chunk")
		}
		if _, err := temporary.Write(chunk.Data); err != nil {
			return err
		}
		offset += uint64(len(chunk.Data))
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := publishJobDownload(temporary.Name(), abs); err != nil {
		return err
	}
	if err := setLocalOutputOwner(abs); err != nil {
		_ = os.Remove(abs)
		return err
	}
	fmt.Fprintf(output, "Saved %s (%d bytes) to %s\n", artifact.Name, artifact.Size, abs)
	return nil
}

func publishJobDownload(source, destination string) error {
	if err := os.Link(source, destination); err == nil {
		return nil
	} else if _, statErr := os.Stat(destination); statErr == nil {
		return fmt.Errorf("output file already exists: %s", destination)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
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
