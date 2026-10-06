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
	"strings"

	"undertow/internal/bof"
	"undertow/internal/control"
	"undertow/internal/pivot"
)

type bofOpener func(context.Context, string, []byte, []byte) (*pivot.InteractiveSession, error)

func bofArguments(path, format, manifestPath string, values []string) ([]byte, error) {
	if manifestPath == "" {
		for _, candidate := range []string{path + ".json", strings.TrimSuffix(path, filepath.Ext(path)) + ".json"} {
			if _, err := os.Stat(candidate); err == nil {
				manifestPath = candidate
				break
			}
		}
	}
	if manifestPath != "" {
		source, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		manifest, manifestFormat, err := bof.ParseManifest(source)
		if err != nil {
			return nil, err
		}
		if format == "" {
			format = manifestFormat
		}
		if format == "" && len(values) != 0 && manifest.Arguments == nil {
			format = strings.Repeat("z", len(values))
		}
	}
	if format == "" && len(values) != 0 {
		return nil, errors.New("BOF arguments require --format or a sidecar manifest")
	}
	return bof.EncodeArguments(format, values, bof.ReadBinaryFile)
}

func runConsoleBOF(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, open bofOpener, args []string) error {
	usage := errors.New("use run-bof AGENT_ID [--background] [--format FORMAT] [--manifest FILE] OBJECT.o [--format FORMAT] [ARGS]")
	if len(args) < 3 {
		return usage
	}
	agentID, parts := args[1], args[2:]
	background, format, manifestPath := false, "", ""
	for len(parts) > 0 {
		switch parts[0] {
		case "--background":
			background = true
			parts = parts[1:]
		case "--format":
			if len(parts) < 2 || format != "" {
				return usage
			}
			format = parts[1]
			parts = parts[2:]
		case "--manifest":
			if len(parts) < 2 || manifestPath != "" {
				return usage
			}
			manifestPath = parts[1]
			parts = parts[2:]
		default:
			goto objectPath
		}
	}
objectPath:
	if len(parts) == 0 || strings.HasPrefix(parts[0], "--") {
		return usage
	}
	path := parts[0]
	parts = parts[1:]
	if len(parts) > 0 && parts[0] == "--format" {
		if len(parts) < 2 || format != "" {
			return usage
		}
		format = parts[1]
		parts = parts[2:]
	}
	if len(parts) > 0 && parts[0] == "--" {
		parts = parts[1:]
	}
	object, err := readMemoryFile(path, pivot.BOFObjectLimit)
	if err != nil {
		return err
	}
	compat, err := bof.Parse(object)
	if err != nil {
		return err
	}
	if !compat.Supported {
		return fmt.Errorf("unsupported BOF: %s", compat.Errors[0])
	}
	packed, err := bofArguments(path, format, manifestPath, parts)
	if err != nil {
		return err
	}
	return executeConsoleBOF(ctx, output, editor, call, open, agentID, "", object, packed, background)
}

func executeConsoleBOF(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, open bofOpener, agentID, agentLabel string, object, packed []byte, background bool) error {
	if background {
		response, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/bof/jobs", map[string]any{"source": object, "arguments": packed})
		if err != nil {
			return err
		}
		var job control.JobInfo
		if err := json.Unmarshal(response, &job); err != nil {
			return err
		}
		if agentLabel == "" {
			agentLabel = shortAgentID(job.AgentID)
		}
		fmt.Fprintf(output, "BOF job %s started on %s. Use job output %s, or run jobs for a numbered list.\n", job.ID, agentLabel, job.ID)
		return nil
	}
	if open == nil {
		return errors.New("BOF streaming is unavailable")
	}
	session, err := open(ctx, agentID, object, packed)
	if err != nil {
		return err
	}
	return runMemoryForeground(ctx, output, editor, session)
}

func bofCommand(args []string, output io.Writer) error {
	if len(args) != 2 || args[0] != "inspect" {
		return errors.New("use undertow bof inspect FILE.o")
	}
	object, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	compat, err := bof.Parse(object)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "BOF\n===\nFile           : %s\nArchitecture   : %s\nEntrypoint     : %s\nSections       : %d\nSymbols        : %d\nRelocations    : %d\n", args[1], compat.Architecture, compat.Entrypoint, len(compat.Sections), len(compat.Symbols), len(compat.Relocations))
	fmt.Fprintln(output, "\nWindows Imports\n---------------")
	for _, imp := range compat.WindowsImports {
		fmt.Fprintln(output, imp.Name)
	}
	fmt.Fprintln(output, "\nBeacon Imports\n--------------")
	for _, imp := range compat.BeaconImports {
		fmt.Fprintln(output, imp.Name)
	}
	if len(compat.UnknownImports) > 0 {
		fmt.Fprintln(output, "\nUnresolved Symbols\n------------------")
		for _, imp := range compat.UnknownImports {
			fmt.Fprintln(output, imp.Name)
		}
	}
	if compat.Supported {
		fmt.Fprintln(output, "\nCompatibility : Supported")
	} else {
		fmt.Fprintln(output, "\nCompatibility : Unsupported\n\nReasons:")
		for _, reason := range compat.Errors {
			fmt.Fprintln(output, "  "+reason)
		}
	}
	return nil
}
