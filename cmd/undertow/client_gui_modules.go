//go:build linux || windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"undertow/internal/assembly"
	"undertow/internal/bof"
	"undertow/internal/control"
	"undertow/internal/nativemodule"
	"undertow/internal/pivot"
)

type guiModuleBank struct {
	mu            sync.Mutex
	artifacts     *loadedArtifactRegistry
	bofs          *loadedBOFRegistry
	store         *clientGUIStore
	cacheDir      string
	imports       map[string]guiModuleRef
	preloadErrors []string
}

type guiModuleInfo struct {
	Name        string             `json:"name"`
	Kind        string             `json:"kind"`
	Description string             `json:"description,omitempty"`
	Usage       string             `json:"usage"`
	Help        string             `json:"help,omitempty"`
	Source      string             `json:"source"`
	Path        string             `json:"path"`
	OS          string             `json:"os,omitempty"`
	Arch        string             `json:"arch,omitempty"`
	Format      string             `json:"format,omitempty"`
	SchemaKnown bool               `json:"schema_known,omitempty"`
	Arguments   []bof.ArgumentSpec `json:"arguments,omitempty"`
}

func openGUIModuleBank(storePath string, store *clientGUIStore) (*guiModuleBank, error) {
	absolute, err := filepath.Abs(storePath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(filepath.Dir(absolute), "gui-modules")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	bank := &guiModuleBank{artifacts: newLoadedArtifactRegistry(), bofs: newLoadedBOFRegistry(), store: store, cacheDir: dir, imports: make(map[string]guiModuleRef), preloadErrors: []string{}}
	for _, problem := range preloadModuleBank(bank.artifacts, bank.bofs) {
		bank.preloadErrors = append(bank.preloadErrors, problem.Error())
		log.Printf("GUI module preload: %v", problem)
	}
	refs, err := store.ModuleRefs()
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if err := bank.loadPath(ref.Kind, ref.Path, ref.Name, ref.Format); err != nil {
			bank.preloadErrors = append(bank.preloadErrors, fmt.Sprintf("%s: %v", ref.Name, err))
			log.Printf("GUI module restore %s: %v", ref.Name, err)
			continue
		}
		bank.imports[ref.Name] = ref
	}
	return bank, nil
}

func (b *guiModuleBank) loadPath(kind, path, name, format string) error {
	switch kind {
	case "bof":
		_, err := b.bofs.load(path, name, format, format != "", b.artifacts)
		return err
	case "module", "wasm", "assembly":
		_, err := b.artifacts.load(kind, path, name, b.bofs)
		return err
	default:
		return errors.New("module type must be bof, module, wasm, or assembly")
	}
}

func (b *guiModuleBank) list() []guiModuleInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]guiModuleInfo, 0, len(b.artifacts.names())+len(b.bofs.names()))
	for _, name := range b.artifacts.names() {
		entry := b.artifacts.get(name)
		if entry == nil {
			continue
		}
		source := "packaged"
		if _, ok := b.imports[name]; ok {
			source = "imported"
		}
		out = append(out, guiModuleInfo{Name: name, Kind: entry.Kind, Description: entry.Help.Description, Usage: entry.usage(), Help: entry.Help.Help, Source: source, Path: entry.Path, OS: entry.OS, Arch: entry.Arch})
	}
	for _, name := range b.bofs.names() {
		entry := b.bofs.get(name)
		if entry == nil {
			continue
		}
		source := "packaged"
		if _, ok := b.imports[name]; ok {
			source = "imported"
		}
		out = append(out, guiModuleInfo{Name: name, Kind: "bof", Description: entry.Manifest.Description, Usage: entry.usage(), Help: entry.Manifest.Help, Source: source, Path: entry.Path, OS: "windows", Arch: entry.Compat.Architecture, Format: entry.Format, SchemaKnown: entry.SchemaKnown, Arguments: entry.Manifest.Arguments})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Name < out[j].Name
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func (g *guiServer) listModules(w http.ResponseWriter, r *http.Request) {
	guiJSON(w, http.StatusOK, map[string]any{"modules": g.modules.list(), "preload_errors": g.modules.preloadErrors})
}

func (b *guiModuleBank) unload(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	ref, imported := b.imports[name]
	if entry := b.artifacts.get(name); entry != nil {
		if err := b.artifacts.unload(entry.Kind, name); err != nil {
			return err
		}
	} else if b.bofs.get(name) != nil {
		if err := b.bofs.unload(name); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("module %q is not loaded", name)
	}
	if imported {
		if err := b.store.DeleteModuleRef(name); err != nil {
			return err
		}
		delete(b.imports, name)
		// Imported bytes live only in the client-local module cache.
		_ = os.Remove(ref.Path)
		_ = os.Remove(strings.TrimSuffix(ref.Path, filepath.Ext(ref.Path)) + ".json")
	}
	return nil
}

func (g *guiServer) unloadModule(w http.ResponseWriter, r *http.Request) {
	if err := g.modules.unload(r.PathValue("name")); err != nil {
		guiJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *guiServer) importModule(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "invalid or oversized module upload", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	kind, name, format := r.FormValue("kind"), strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("format"))
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "module file required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	filename := filepath.Base(header.Filename)
	if len(filename) > 200 || filename == "" || strings.ContainsAny(filename, "\r\n\x00") {
		http.Error(w, "invalid module filename", http.StatusBadRequest)
		return
	}
	extension := strings.ToLower(filepath.Ext(filename))
	if (kind == "assembly" && extension != ".dll" && extension != ".exe") || (kind != "assembly" && map[string]string{"bof": ".o", "module": ".module", "wasm": ".wasm"}[kind] != extension) {
		http.Error(w, "file extension does not match module type", http.StatusBadRequest)
		return
	}
	if name == "" {
		name = kind + "-" + deriveBOFCommandName(filename)
	}
	if !validBOFCommandName(name) || builtinConsoleCommands[name] {
		http.Error(w, "invalid or reserved module name", http.StatusBadRequest)
		return
	}
	if kind != "bof" && format != "" {
		http.Error(w, "argument format applies only to BOFs", http.StatusBadRequest)
		return
	}
	if kind == "bof" && format != "" {
		if err := bof.ValidateFormat(format); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	limit := int64(pivot.WASMModuleLimit)
	if kind == "module" {
		limit = pivot.NativeModuleLimit
	} else if kind == "assembly" {
		limit = assembly.MaxSize
	} else if kind == "bof" {
		limit = bof.MaxObjectSize
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || len(data) == 0 {
		http.Error(w, "module file is empty or exceeds its runtime limit", http.StatusBadRequest)
		return
	}
	var manifest []byte
	if sidecar, _, sidecarErr := r.FormFile("manifest"); sidecarErr == nil {
		defer sidecar.Close()
		manifest, err = io.ReadAll(io.LimitReader(sidecar, (16<<10)+1))
		if err != nil || len(manifest) > 16<<10 {
			http.Error(w, "module sidecar exceeds 16 KiB", http.StatusBadRequest)
			return
		}
		if !json.Valid(manifest) {
			http.Error(w, "invalid module sidecar JSON", http.StatusBadRequest)
			return
		}
	} else if !errors.Is(sidecarErr, http.ErrMissingFile) {
		http.Error(w, "invalid module sidecar", http.StatusBadRequest)
		return
	}
	secret, err := randomGUISecret()
	if err != nil {
		http.Error(w, "module cache unavailable", http.StatusInternalServerError)
		return
	}
	path := filepath.Join(g.modules.cacheDir, secret[:24]+extension)
	if err := os.WriteFile(path, data, 0600); err != nil {
		http.Error(w, "module cache unavailable", http.StatusInternalServerError)
		return
	}
	sidecarPath := strings.TrimSuffix(path, extension) + ".json"
	defer func() {
		if err != nil {
			_ = os.Remove(path)
			_ = os.Remove(sidecarPath)
		}
	}()
	if len(manifest) > 0 {
		if err = os.WriteFile(sidecarPath, manifest, 0600); err != nil {
			http.Error(w, "module sidecar unavailable", http.StatusInternalServerError)
			return
		}
	}
	g.modules.mu.Lock()
	err = g.modules.loadPath(kind, path, name, format)
	if err == nil {
		ref := guiModuleRef{Name: name, Kind: kind, Path: path, Filename: filename, Format: format}
		if err = g.modules.store.SaveModuleRef(ref); err == nil {
			g.modules.imports[name] = ref
		} else {
			if kind == "bof" {
				_ = g.modules.bofs.unload(name)
			} else {
				_ = g.modules.artifacts.unload(kind, name)
			}
		}
	}
	g.modules.mu.Unlock()
	if err != nil {
		guiJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	for _, item := range g.modules.list() {
		if item.Name == name {
			guiJSON(w, http.StatusCreated, item)
			return
		}
	}
	http.Error(w, "module imported but unavailable", http.StatusInternalServerError)
}

type guiModuleRunRequest struct {
	TokenContextID string   `json:"token_context_id,omitempty"`
	Args           string   `json:"args"`
	Values         []string `json:"values,omitempty"`
	Background     bool     `json:"background"`
	Input          []byte   `json:"input,omitempty"`
}

func (g *guiServer) runModule(w http.ResponseWriter, r *http.Request) {
	var request guiModuleRunRequest
	if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request) != nil {
		http.Error(w, "invalid module request", http.StatusBadRequest)
		return
	}
	if len(request.Args) > 8192 || len(request.Input) > 64<<10 {
		http.Error(w, "module arguments or input exceed local limit", http.StatusBadRequest)
		return
	}
	if request.Args != "" && request.Values != nil {
		http.Error(w, "use args or values, not both", http.StatusBadRequest)
		return
	}
	args := request.Values
	if request.Values == nil {
		parsed, parseErr := splitConsoleCommand(request.Args)
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		args = parsed
	}
	claims, err := g.actionClaims()
	if err != nil {
		http.Error(w, "preferences unavailable", http.StatusInternalServerError)
		return
	}
	ctx := pivot.WithTokenContext(control.WithActionClaims(r.Context(), claims), request.TokenContextID)
	_ = g.store.AppendConsoleEntry(r.PathValue("id"), "modules", "command", "module "+r.PathValue("name")+map[bool]string{true: " (background)", false: " (foreground)"}[request.Background])
	job, session, err := g.startModule(ctx, r.PathValue("id"), r.PathValue("name"), args, request.Input, request.Background)
	if err != nil {
		_ = g.store.AppendConsoleEntry(r.PathValue("id"), "modules", "error", err.Error())
		guiJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if session == nil {
		verb := "Started"
		if job.State == "queued" {
			verb = "Queued"
		}
		_ = g.store.AppendConsoleEntry(r.PathValue("id"), "modules", "output", verb+" job "+job.ID+". Output is retained in Jobs.\n")
		guiJSON(w, http.StatusCreated, map[string]any{"job": job})
		return
	}
	defer session.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	stream := guiCommandEventWriter{encode: json.NewEncoder(w), flush: flusher, store: g.store, agentID: r.PathValue("id"), source: "modules"}
	g.streamGUISession(&stream, session)
}

func (g *guiServer) startModule(ctx context.Context, agentID, name string, args []string, input []byte, background bool) (control.JobInfo, *pivot.InteractiveSession, error) {
	g.modules.mu.Lock()
	artifact, bofEntry := g.modules.artifacts.get(name), g.modules.bofs.get(name)
	g.modules.mu.Unlock()
	if artifact == nil && bofEntry == nil {
		return control.JobInfo{}, nil, fmt.Errorf("module %q is not loaded", name)
	}
	g.client.mu.RLock()
	session := g.client.session
	g.client.mu.RUnlock()
	if session == nil {
		return control.JobInfo{}, nil, errors.New("client is disconnected")
	}
	// Inventory and capability checks are advisory here; the server and agent
	// still enforce the operation when the structured request is opened.
	data, err := g.client.call(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return control.JobInfo{}, nil, err
	}
	var status struct {
		Agents []control.AgentInfo `json:"agents"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return control.JobInfo{}, nil, err
	}
	var agent control.AgentInfo
	for _, candidate := range status.Agents {
		if candidate.ID == agentID {
			agent = candidate
			break
		}
	}
	if agent.ID == "" {
		return control.JobInfo{}, nil, fmt.Errorf("agent %s is not connected", agentID)
	}
	// Only an explicit background run creates a server Job. Foreground module
	// streams wait for the next check-in through the existing interactive relay.
	deferAsJob := background
	capability := "native"
	if artifact != nil && artifact.Kind == "wasm" {
		capability = "wasm"
	}
	if agent.Capabilities != nil {
		allowed := false
		for _, item := range agent.Capabilities.Allowed {
			if item == capability {
				allowed = true
				break
			}
		}
		if !allowed {
			return control.JobInfo{}, nil, fmt.Errorf("%s capability is disabled on this agent", capability)
		}
	}
	if bofEntry != nil || artifact != nil && (artifact.Kind == "module" || artifact.Kind == "assembly") {
		osName, arch := "windows", "amd64"
		if bofEntry != nil {
			arch = bofEntry.Compat.Architecture
		}
		if artifact != nil {
			osName, arch = artifact.OS, artifact.Arch
		}
		if !strings.EqualFold(agent.OS, osName) || !strings.EqualFold(agent.Arch, arch) {
			return control.JobInfo{}, nil, fmt.Errorf("%s requires %s/%s; selected agent is %s/%s", name, osName, arch, agent.OS, agent.Arch)
		}
	}
	base := "/v1/agents/" + url.PathEscape(agentID)
	var path string
	var body any
	if bofEntry != nil {
		if len(input) > 0 {
			return control.JobInfo{}, nil, errors.New("BOF input is provided through typed arguments")
		}
		packed, err := bofEntry.encode(args)
		if err != nil {
			return control.JobInfo{}, nil, err
		}
		path = base + "/bof/jobs"
		body = map[string]any{"source": bofEntry.Object, "arguments": packed}
		if !deferAsJob {
			stream, err := control.OpenClientBOF(ctx, session, agentID, bofEntry.Object, packed)
			return control.JobInfo{}, stream, err
		}
	} else if artifact.Kind == "wasm" {
		path = base + "/wasm/jobs"
		body = map[string]any{"source": artifact.Data, "stdin": input, "args": args}
		if !deferAsJob {
			stream, err := control.OpenClientWASM(ctx, session, agentID, artifact.Data, args, input)
			return control.JobInfo{}, stream, err
		}
	} else if artifact.Kind == "assembly" {
		if len(input) != 0 {
			return control.JobInfo{}, nil, errors.New("assembly modules accept command-line arguments only")
		}
		path = base + "/assembly/jobs"
		body = map[string]any{"source": artifact.Data, "args": args}
		if !deferAsJob {
			stream, err := control.OpenClientAssembly(ctx, session, agentID, artifact.Data, args)
			return control.JobInfo{}, stream, err
		}
	} else {
		if _, err := nativemodule.EncodeArgs(args, input); err != nil {
			return control.JobInfo{}, nil, err
		}
		path = base + "/native/jobs"
		body = map[string]any{"source": artifact.Data, "data": input, "args": args}
		if !deferAsJob {
			stream, err := control.OpenClientNative(ctx, session, agentID, artifact.Data, args, input)
			return control.JobInfo{}, stream, err
		}
	}
	result, err := g.client.call(ctx, http.MethodPost, path, body)
	if err != nil {
		return control.JobInfo{}, nil, err
	}
	var job control.JobInfo
	if err := json.Unmarshal(result, &job); err != nil {
		return control.JobInfo{}, nil, err
	}
	return job, nil, nil
}
