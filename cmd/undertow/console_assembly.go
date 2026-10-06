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

	"undertow/internal/assembly"
	"undertow/internal/control"
	"undertow/internal/pivot"
)

type assemblyOpener func(context.Context, string, []byte, []string) (*pivot.InteractiveSession, error)

func runConsoleAssembly(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, open assemblyOpener, args []string) error {
	usage := errors.New("use run-assembly AGENT_ID [--background] ASSEMBLY.exe|dll [ARGS]")
	if len(args) < 3 {
		return usage
	}
	agentID, parts, background := args[1], args[2:], false
	for len(parts) > 0 && parts[0] == "--background" {
		background, parts = true, parts[1:]
	}
	if len(parts) == 0 || strings.HasPrefix(parts[0], "--") {
		return usage
	}
	source, err := readMemoryFile(parts[0], assembly.MaxSize)
	if err != nil {
		return err
	}
	if _, err := assembly.Inspect(source); err != nil {
		return err
	}
	values := parts[1:]
	if background {
		response, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/assembly/jobs", map[string]any{"source": source, "args": values})
		if err != nil {
			return err
		}
		var job control.JobInfo
		if err := json.Unmarshal(response, &job); err != nil {
			return err
		}
		fmt.Fprintf(output, "Assembly job %s started on %s. Use job output %s.\n", job.ID, shortAgentID(job.AgentID), job.ID)
		return nil
	}
	if open == nil {
		return errors.New("assembly streaming is unavailable")
	}
	session, err := open(ctx, agentID, source, values)
	if err != nil {
		return err
	}
	return runMemoryForeground(ctx, output, editor, session)
}
