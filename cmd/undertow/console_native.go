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
	"undertow/internal/nativemodule"
	"undertow/internal/pivot"
)

type nativeOpener func(context.Context, string, []byte, []string, []byte) (*pivot.InteractiveSession, error)

func runConsoleNative(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, open nativeOpener, args []string) error {
	usage := errors.New("use run-native AGENT_ID [--background] [--data LOCAL_FILE] MODULE_FILE [ARGS]")
	if len(args) < 3 {
		return usage
	}
	agentID, parts := args[1], args[2:]
	background, dataPath := false, ""
	for len(parts) > 0 {
		switch parts[0] {
		case "--background":
			background, parts = true, parts[1:]
		case "--data":
			if len(parts) < 2 || dataPath != "" {
				return usage
			}
			dataPath, parts = parts[1], parts[2:]
		default:
			goto modulePath
		}
	}
modulePath:
	if len(parts) == 0 || strings.HasPrefix(parts[0], "--") {
		return usage
	}
	module, err := readMemoryFile(parts[0], pivot.NativeModuleLimit)
	if err != nil {
		return err
	}
	if _, _, err := nativemodule.Parse(module); err != nil {
		return err
	}
	var data []byte
	if dataPath != "" {
		data, err = readMemoryFileOption(dataPath, 64<<10, true)
		if err != nil {
			return err
		}
	}
	moduleArgs := parts[1:]
	if _, err := nativemodule.EncodeArgs(moduleArgs, data); err != nil {
		return err
	}
	if background {
		response, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/native/jobs", map[string]any{"source": module, "args": moduleArgs, "data": data})
		if err != nil {
			return err
		}
		var job control.JobInfo
		if err := json.Unmarshal(response, &job); err != nil {
			return err
		}
		fmt.Fprintf(output, "Native job %s started on %s. Use job output %s, or run jobs for a numbered list.\n", job.ID, shortAgentID(job.AgentID), job.ID)
		return nil
	}
	if open == nil {
		return errors.New("native streaming is unavailable")
	}
	session, err := open(ctx, agentID, module, moduleArgs, data)
	if err != nil {
		return err
	}
	return runMemoryForeground(ctx, output, editor, session)
}
