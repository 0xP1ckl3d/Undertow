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

	"undertow/internal/control"
	"undertow/internal/pivot"
)

type wasmOpener func(context.Context, string, []byte, []string, []byte) (*pivot.InteractiveSession, error)

func runConsoleWASM(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, open wasmOpener, args []string) error {
	usage := errors.New("use run-wasm AGENT_ID [--background] [--stdin LOCAL_FILE] MODULE_FILE [ARGS]")
	if len(args) < 3 {
		return usage
	}
	agentID, parts := args[1], args[2:]
	background, stdinPath := false, ""
	for len(parts) > 0 {
		if parts[0] == "--background" {
			background, parts = true, parts[1:]
			continue
		}
		if parts[0] == "--stdin" {
			if len(parts) < 2 || stdinPath != "" {
				return usage
			}
			stdinPath, parts = parts[1], parts[2:]
			continue
		}
		break
	}
	if len(parts) == 0 || strings.HasPrefix(parts[0], "--") {
		return usage
	}
	module, err := readMemoryFile(parts[0], pivot.WASMModuleLimit)
	if err != nil {
		return err
	}
	var stdin []byte
	if stdinPath != "" {
		stdin, err = readMemoryFileOption(stdinPath, pivot.WASMStdinLimit, true)
		if err != nil {
			return err
		}
	}
	moduleArgs := parts[1:]
	if background {
		data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/wasm/jobs", map[string]any{"source": module, "stdin": stdin, "args": moduleArgs})
		if err != nil {
			return err
		}
		var job control.JobInfo
		if err := json.Unmarshal(data, &job); err != nil {
			return err
		}
		fmt.Fprintf(output, "WASM job %s started on %s. Use job output %s, or run jobs for a numbered list.\n", job.ID, shortAgentID(job.AgentID), job.ID)
		return nil
	}
	if open == nil {
		return errors.New("WASM streaming is unavailable")
	}
	session, err := open(ctx, agentID, module, moduleArgs, stdin)
	if err != nil {
		return err
	}
	return runMemoryForeground(ctx, output, editor, session)
}
