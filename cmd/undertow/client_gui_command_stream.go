//go:build linux || windows

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"undertow/internal/bof"
	"undertow/internal/control"
	"undertow/internal/pivot"
)

// agentCommandStream is the GUI transport for the bound Undertow command
// console. Command parsing and agent operations remain in Go. No command is
// sent when a workspace is opened or an entry is selected.
func (g *guiServer) agentCommandStream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Line string `json:"line"`
	}
	if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || len(body.Line) > 4096 {
		http.Error(w, "invalid command", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	stream := guiCommandEventWriter{encode: json.NewEncoder(w), flush: flusher, store: g.store, agentID: r.PathValue("id"), source: "console"}
	if strings.TrimSpace(body.Line) != "" {
		_ = g.store.AppendConsoleEntry(r.PathValue("id"), "console", "command", body.Line)
	}
	args, err := splitConsoleCommand(body.Line)
	if err != nil {
		stream.event("error", err.Error())
		return
	}
	if len(args) == 0 {
		return
	}
	if args[0] == "run-script" || args[0] == "run-wasm" || args[0] == "run-native" || args[0] == "run-assembly" || args[0] == "run-bof" {
		claims, err := g.actionClaims()
		if err != nil {
			stream.event("error", err.Error())
			return
		}
		ctx := control.WithActionClaims(r.Context(), claims)
		if err := g.runGUIOneShot(ctx, &stream, r.PathValue("id"), args); err != nil {
			stream.event("error", err.Error())
		}
		return
	}
	result, err := g.runAgentGUICommand(r.Context(), r.PathValue("id"), body.Line)
	if result.Output != "" {
		stream.event("output", result.Output)
	}
	if err != nil {
		stream.event("error", err.Error())
		return
	}
	if result.OpenShell {
		stream.event("shell", "")
	}
	if result.ModuleRun == nil {
		return
	}
	claims, err := g.actionClaims()
	if err != nil {
		stream.event("error", err.Error())
		return
	}
	ctx := control.WithActionClaims(r.Context(), claims)
	_, session, err := g.startModule(ctx, r.PathValue("id"), result.ModuleRun.Name, result.ModuleRun.Args, result.ModuleRun.Input, false)
	if err != nil {
		stream.event("error", err.Error())
		return
	}
	defer session.Close()
	g.streamGUISession(&stream, session)
}

type guiCommandEventWriter struct {
	encode  *json.Encoder
	flush   http.Flusher
	store   *clientGUIStore
	agentID string
	source  string
}

func (w *guiCommandEventWriter) event(kind, data string) error {
	if w.store != nil && kind != "shell" {
		_ = w.store.AppendConsoleEntry(w.agentID, w.source, kind, data)
	}
	if err := w.encode.Encode(map[string]string{"kind": kind, "data": data}); err != nil {
		return err
	}
	w.flush.Flush()
	return nil
}

func (g *guiServer) consoleHistory(w http.ResponseWriter, r *http.Request) {
	entries, err := g.store.ConsoleEntries(r.PathValue("id"))
	if err != nil {
		http.Error(w, "console history unavailable", http.StatusInternalServerError)
		return
	}
	guiJSON(w, http.StatusOK, entries)
}

func (w *guiCommandEventWriter) Write(data []byte) (int, error) {
	if err := w.event("output", string(data)); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (g *guiServer) streamGUISession(writer *guiCommandEventWriter, session *pivot.InteractiveSession) {
	var files *bof.FileCollector
	defer func() {
		if files != nil {
			files.Close()
		}
	}()
	for {
		kind, data, err := session.Read()
		if err != nil {
			writer.event("error", err.Error())
			return
		}
		if kind == pivot.InteractiveBOFCallback {
			if files == nil {
				files = bof.NewFileCollector(filepath.Join(g.transferDir, fmt.Sprintf("bof-%d", time.Now().UnixNano())))
			}
			completed, err := files.Consume(data)
			if err != nil {
				writer.event("error", err.Error())
				return
			}
			for _, file := range completed {
				id, err := randomGUISecret()
				if err != nil {
					writer.event("error", err.Error())
					return
				}
				g.transferMu.Lock()
				g.downloads[id] = guiDownload{path: file.Path, name: file.Name, expires: time.Now().Add(10 * time.Minute)}
				g.transferMu.Unlock()
				message, err := json.Marshal(map[string]any{"name": file.Name, "size": file.Size, "download": "/api/transfers/" + id + "/download"})
				if err != nil {
					writer.event("error", err.Error())
					return
				}
				if writer.event("file", string(message)) != nil {
					return
				}
			}
			continue
		}
		label := "output"
		switch kind {
		case pivot.InteractiveStderr:
			label = "stderr"
		case pivot.InteractiveError:
			label = "error"
		case pivot.InteractiveExit:
			label = "exit"
		}
		value := string(data)
		if kind == pivot.InteractiveExit && len(data) == 4 {
			value = fmt.Sprint(int32(binary.BigEndian.Uint32(data)))
		}
		if writer.event(label, value) != nil {
			return
		}
		if kind == pivot.InteractiveExit || kind == pivot.InteractiveError {
			return
		}
	}
}

func (g *guiServer) runGUIOneShot(ctx context.Context, writer *guiCommandEventWriter, agentID string, args []string) error {
	g.client.mu.RLock()
	session := g.client.session
	g.client.mu.RUnlock()
	if session == nil {
		return errors.New("client is disconnected")
	}
	bound := make([]string, 0, len(args)+1)
	bound = append(bound, args[0], agentID)
	bound = append(bound, args[1:]...)
	call := func(ctx context.Context, method, path string, body any) ([]byte, error) {
		return g.client.call(ctx, method, path, body)
	}
	switch args[0] {
	case "run-script":
		return runConsoleScript(ctx, writer, nil, call, func(ctx context.Context, id, language string, source []byte) (*pivot.InteractiveSession, error) {
			return control.OpenClientScript(ctx, session, id, language, source)
		}, bound)
	case "run-wasm":
		return runConsoleWASM(ctx, writer, nil, call, func(ctx context.Context, id string, source []byte, values []string, input []byte) (*pivot.InteractiveSession, error) {
			return control.OpenClientWASM(ctx, session, id, source, values, input)
		}, bound)
	case "run-native":
		return runConsoleNative(ctx, writer, nil, call, func(ctx context.Context, id string, source []byte, values []string, input []byte) (*pivot.InteractiveSession, error) {
			return control.OpenClientNative(ctx, session, id, source, values, input)
		}, bound)
	case "run-assembly":
		return runConsoleAssembly(ctx, writer, nil, call, func(ctx context.Context, id string, source []byte, values []string) (*pivot.InteractiveSession, error) {
			return control.OpenClientAssembly(ctx, session, id, source, values)
		}, bound)
	case "run-bof":
		return runConsoleBOF(ctx, writer, nil, call, func(ctx context.Context, id string, source, packed []byte) (*pivot.InteractiveSession, error) {
			return control.OpenClientBOF(ctx, session, id, source, packed)
		}, bound)
	default:
		return fmt.Errorf("unknown one-shot command %q", args[0])
	}
}
