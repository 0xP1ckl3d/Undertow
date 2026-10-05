package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"undertow/internal/bof"
	"undertow/internal/control"
	"undertow/internal/pivot"
)

type scriptOpener func(context.Context, string, string, []byte) (*pivot.InteractiveSession, error)

func readMemoryFile(path string, limit int64) ([]byte, error) {
	return readMemoryFileOption(path, limit, false)
}

func readMemoryFileOption(path string, limit int64, allowEmpty bool) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	source, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if (!allowEmpty && len(source) == 0) || int64(len(source)) > limit {
		return nil, fmt.Errorf("source must contain 1 to %d bytes", limit)
	}
	return source, nil
}

func runConsoleScript(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, open scriptOpener, args []string) error {
	if len(args) < 4 {
		return errors.New("use run-script AGENT_ID [--background] bash|powershell LOCAL_FILE")
	}
	agentID := args[1]
	parts := args[2:]
	background := false
	if len(parts) > 0 && parts[0] == "--background" {
		background, parts = true, parts[1:]
	}
	if len(parts) != 2 || parts[0] != "bash" && parts[0] != "powershell" {
		return errors.New("use run-script AGENT_ID [--background] bash|powershell LOCAL_FILE")
	}
	source, err := readMemoryFile(parts[1], pivot.ScriptSourceLimit)
	if err != nil {
		return err
	}
	if background {
		data, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/scripts/jobs", map[string]any{"language": parts[0], "source": source})
		if err != nil {
			return err
		}
		var job control.JobInfo
		if err := json.Unmarshal(data, &job); err != nil {
			return err
		}
		fmt.Fprintf(output, "Script job %s started on %s. Use job output %s, or run jobs for a numbered list.\n", job.ID, shortAgentID(job.AgentID), job.ID)
		return nil
	}
	if open == nil {
		return errors.New("script streaming is unavailable")
	}
	session, err := open(ctx, agentID, parts[0], source)
	if err != nil {
		return err
	}
	return runMemoryForeground(ctx, output, editor, session)
}

func runMemoryForeground(ctx context.Context, output io.Writer, editor *consoleEditor, session *pivot.InteractiveSession) error {
	var files *bof.FileCollector
	done := make(chan struct{})
	defer func() {
		_ = session.Close()
		<-done
		if files != nil {
			files.Close()
		}
	}()
	var detach <-chan struct{}
	if editor != nil {
		_, detach = editor.beginInteractive()
		defer editor.endInteractive()
		fmt.Fprintln(output, "\r\nTask running. Press Ctrl-] to cancel.")
	}
	finished := make(chan error, 1)
	go func() {
		defer close(done)
		for {
			kind, data, err := session.Read()
			if err != nil {
				finished <- err
				return
			}
			switch kind {
			case pivot.InteractiveOutput, pivot.InteractiveStderr:
				_, _ = output.Write(data)
			case pivot.InteractiveBOFCallback:
				if files == nil {
					root, err := filepath.Abs(filepath.Join("outputs", "bof", fmt.Sprintf("run-%d", time.Now().UnixNano())))
					if err != nil {
						finished <- err
						return
					}
					files = bof.NewFileCollector(root)
				}
				completed, err := files.Consume(data)
				if err != nil {
					finished <- err
					return
				}
				for _, file := range completed {
					fmt.Fprintf(output, "\n[received file %s: %d bytes, %s]\n", file.Name, file.Size, file.Path)
				}
			case pivot.InteractiveExit:
				if len(data) != 4 {
					finished <- errors.New("invalid task exit status")
					return
				}
				code := int(int32(binary.BigEndian.Uint32(data)))
				fmt.Fprintf(output, "\n[exit %d]\n", code)
				if code != 0 {
					finished <- fmt.Errorf("exit status %d", code)
				} else {
					finished <- nil
				}
				return
			case pivot.InteractiveError:
				finished <- errors.New(string(data))
				return
			}
		}
	}()
	select {
	case err := <-finished:
		return err
	case <-detach:
		return errors.New("task cancelled")
	case <-ctx.Done():
		return ctx.Err()
	}
}
