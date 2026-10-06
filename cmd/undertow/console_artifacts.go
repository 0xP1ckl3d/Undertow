package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"undertow/internal/assembly"
	"undertow/internal/control"
	"undertow/internal/nativemodule"
	"undertow/internal/pivot"
)

type artifactHelp struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
	Help        string `json:"help"`
}

type loadedArtifact struct {
	Name string
	Kind string // module, wasm or assembly
	Path string
	Data []byte
	Help artifactHelp
	OS   string
	Arch string
}

type loadedArtifactRegistry struct {
	mu    sync.RWMutex
	items map[string]*loadedArtifact
}

func newLoadedArtifactRegistry() *loadedArtifactRegistry {
	return &loadedArtifactRegistry{items: make(map[string]*loadedArtifact)}
}
func (r *loadedArtifactRegistry) get(name string) *loadedArtifact {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.items[name]
}
func (r *loadedArtifactRegistry) names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.items))
	for name := range r.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func readArtifactHelp(path string) (artifactHelp, error) {
	for _, candidate := range []string{path + ".json", strings.TrimSuffix(path, filepath.Ext(path)) + ".json"} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return artifactHelp{}, err
		}
		if len(data) > 16<<10 {
			return artifactHelp{}, errors.New("module help sidecar exceeds 16 KiB")
		}
		var help artifactHelp
		if err := json.Unmarshal(data, &help); err != nil {
			return artifactHelp{}, fmt.Errorf("module help sidecar %s: %w", candidate, err)
		}
		return help, nil
	}
	return artifactHelp{}, nil
}

func (r *loadedArtifactRegistry) load(kind, path, name string, bofs *loadedBOFRegistry) (*loadedArtifact, error) {
	if kind != "module" && kind != "wasm" && kind != "assembly" {
		return nil, errors.New("use load module|wasm|assembly FILE [NAME]")
	}
	if name == "" {
		name = deriveBOFCommandName(path)
	}
	if !validBOFCommandName(name) {
		return nil, fmt.Errorf("invalid module command name %q (use lowercase letters, digits and hyphens)", name)
	}
	if builtinConsoleCommands[name] {
		return nil, fmt.Errorf("%q is a built-in command", name)
	}
	if r.get(name) != nil || bofs.get(name) != nil {
		return nil, fmt.Errorf("module command %q is already loaded; unload it first", name)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	limit := int64(pivot.WASMModuleLimit)
	if kind == "module" {
		limit = pivot.NativeModuleLimit
	} else if kind == "assembly" {
		limit = assembly.MaxSize
	}
	data, err := readMemoryFile(absolute, limit)
	if err != nil {
		return nil, err
	}
	entry := &loadedArtifact{Name: name, Kind: kind, Path: absolute, Data: data}
	if kind == "module" {
		metadata, _, err := nativemodule.Parse(data)
		if err != nil {
			return nil, err
		}
		entry.OS, entry.Arch = metadata.OS, metadata.Arch
		entry.Help.Description = metadata.Description
	} else if kind == "assembly" {
		meta, err := assembly.Inspect(data)
		if err != nil {
			return nil, err
		}
		entry.OS, entry.Arch = "windows", meta.Architecture
	} else if len(data) < 8 || !bytes.Equal(data[:4], []byte("\x00asm")) || !bytes.Equal(data[4:8], []byte{1, 0, 0, 0}) {
		return nil, errors.New("invalid WASM module header")
	}
	help, err := readArtifactHelp(absolute)
	if err != nil {
		return nil, err
	}
	if help.Description != "" {
		entry.Help.Description = help.Description
	}
	entry.Help.Usage, entry.Help.Help = help.Usage, help.Help
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items[name] != nil {
		return nil, fmt.Errorf("module command %q is already loaded; unload it first", name)
	}
	r.items[name] = entry
	return entry, nil
}

func (r *loadedArtifactRegistry) unload(kind, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.items[name]
	if entry == nil || entry.Kind != kind {
		return fmt.Errorf("%s command %q is not loaded", kind, name)
	}
	delete(r.items, name)
	return nil
}

func (entry *loadedArtifact) usage() string {
	usage := strings.TrimSpace(entry.Help.Usage)
	if usage == "" {
		usage = entry.Name + " [ARGS]"
	}
	if words := strings.Fields(usage); len(words) > 0 && words[0] != entry.Name {
		usage = entry.Name + strings.TrimPrefix(usage, words[0])
	}
	if !strings.Contains(usage, "--background") {
		usage += " [--background]"
	}
	return usage
}

func printLoadedArtifactHelp(output io.Writer, entry *loadedArtifact) {
	fmt.Fprintf(output, "%s (%s)\n\n", entry.Name, entry.Kind)
	if entry.Help.Description != "" {
		fmt.Fprintf(output, "Description:\n  %s\n\n", entry.Help.Description)
	}
	fmt.Fprintf(output, "Usage:\n  %s\n", entry.usage())
	if entry.Help.Help != "" {
		fmt.Fprintf(output, "\n%s\n", entry.Help.Help)
	}
	fmt.Fprintf(output, "\nSource:\n  %s\n", entry.Path)
	if entry.OS != "" {
		fmt.Fprintf(output, "\nArchitecture:\n  %s/%s\n", entry.OS, entry.Arch)
	} else {
		fmt.Fprintln(output, "\nArchitecture:\n  portable WASM")
	}
}

func runArtifactManagement(output io.Writer, artifacts *loadedArtifactRegistry, bofs *loadedBOFRegistry, args []string) error {
	switch args[0] {
	case "load":
		if len(args) < 3 || len(args) > 4 || args[1] != "module" && args[1] != "wasm" && args[1] != "assembly" {
			return errors.New("use load module|wasm|assembly FILE [NAME]")
		}
		name := ""
		if len(args) == 4 {
			name = args[3]
		}
		entry, err := artifacts.load(args[1], args[2], name, bofs)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "Loaded %s: %s\n", entry.Kind, entry.Name)
		return nil
	case "unload":
		if len(args) != 3 || args[1] != "module" && args[1] != "wasm" && args[1] != "assembly" {
			return errors.New("use unload module|wasm|assembly NAME")
		}
		if err := artifacts.unload(args[1], args[2]); err != nil {
			return err
		}
		fmt.Fprintf(output, "Unloaded %s: %s\n", args[1], args[2])
		return nil
	case "modules":
		if len(args) != 1 {
			return errors.New("use modules")
		}
		fmt.Fprintln(output, "Loaded modules\n--------------")
		for _, name := range artifacts.names() {
			entry := artifacts.get(name)
			fmt.Fprintf(output, "%-24s %-8s %s\n", name, entry.Kind, entry.Path)
		}
		for _, name := range bofs.names() {
			entry := bofs.get(name)
			fmt.Fprintf(output, "%-24s %-8s %s\n", name, "bof", entry.Path)
		}
		return nil
	}
	return errors.New("unknown module command")
}

func runLoadedArtifact(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, native nativeOpener, wasm wasmOpener, assemblyOpen assemblyOpener, entry *loadedArtifact, agentID, agentLabel string, args []string) error {
	if agentID == "" {
		return errors.New("select an agent first with use AGENT_NUMBER")
	}
	background, inputPath := false, ""
	values := make([]string, 0, len(args))
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--" && options:
			options = false
		case arg == "--background" && options:
			background = true
		case options && (arg == "--data" && entry.Kind == "module" || arg == "--stdin" && entry.Kind == "wasm"):
			if inputPath != "" || i+1 >= len(args) {
				return fmt.Errorf("%s requires one local file", arg)
			}
			i++
			inputPath = args[i]
		default:
			values = append(values, arg)
		}
	}
	var extra []byte
	if inputPath != "" {
		limit := int64(pivot.WASMStdinLimit)
		if entry.Kind == "module" {
			limit = 64 << 10
		}
		var err error
		extra, err = readMemoryFileOption(inputPath, limit, true)
		if err != nil {
			return err
		}
	}
	if entry.Kind == "wasm" {
		if background {
			response, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/wasm/jobs", map[string]any{"source": entry.Data, "stdin": extra, "args": values})
			if err != nil {
				return err
			}
			var job control.JobInfo
			if err := json.Unmarshal(response, &job); err != nil {
				return err
			}
			fmt.Fprintf(output, "WASM job %s started on %s.\n", job.ID, agentLabel)
			return nil
		}
		if wasm == nil {
			return errors.New("WASM streaming is unavailable")
		}
		session, err := wasm(ctx, agentID, entry.Data, values, extra)
		if err != nil {
			return err
		}
		return runMemoryForeground(ctx, output, editor, session)
	}
	if entry.Kind == "assembly" {
		if background {
			response, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/assembly/jobs", map[string]any{"source": entry.Data, "args": values})
			if err != nil {
				return err
			}
			var job control.JobInfo
			if err := json.Unmarshal(response, &job); err != nil {
				return err
			}
			fmt.Fprintf(output, "Assembly job %s started on %s.\n", job.ID, agentLabel)
			return nil
		}
		if assemblyOpen == nil {
			return errors.New("assembly streaming is unavailable")
		}
		session, err := assemblyOpen(ctx, agentID, entry.Data, values)
		if err != nil {
			return err
		}
		return runMemoryForeground(ctx, output, editor, session)
	}
	if _, err := nativemodule.EncodeArgs(values, extra); err != nil {
		return err
	}
	if background {
		response, err := call(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+"/native/jobs", map[string]any{"source": entry.Data, "data": extra, "args": values})
		if err != nil {
			return err
		}
		var job control.JobInfo
		if err := json.Unmarshal(response, &job); err != nil {
			return err
		}
		fmt.Fprintf(output, "Native job %s started on %s.\n", job.ID, agentLabel)
		return nil
	}
	if native == nil {
		return errors.New("native streaming is unavailable")
	}
	session, err := native(ctx, agentID, entry.Data, values, extra)
	if err != nil {
		return err
	}
	return runMemoryForeground(ctx, output, editor, session)
}

func moduleBankDirectory() string {
	if value := os.Getenv("UNDERTOW_MODULES_DIR"); value != "" {
		return value
	}
	if info, err := os.Stat("modules"); err == nil && info.IsDir() {
		return "modules"
	}
	if executable, err := os.Executable(); err == nil {
		for _, candidate := range []string{filepath.Join(filepath.Dir(executable), "modules"), filepath.Join(filepath.Dir(executable), "..", "modules")} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func preloadModuleBank(artifacts *loadedArtifactRegistry, bofs *loadedBOFRegistry) []error {
	root := moduleBankDirectory()
	if root == "" {
		return nil
	}
	var problems []error
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			problems = append(problems, err)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		kind := ""
		switch strings.ToLower(filepath.Ext(path)) {
		case ".o":
			kind = "bof"
		case ".module":
			kind = "module"
		case ".wasm":
			kind = "wasm"
		case ".dll", ".exe":
			// Only the assembly bank admits managed PE files; other binaries
			// elsewhere under modules/ are build inputs, not packaged modules.
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil || strings.Split(filepath.ToSlash(relative), "/")[0] != "assembly" {
				return nil
			}
			kind = "assembly"
		default:
			return nil
		}
		name := kind + "-" + deriveBOFCommandName(path)
		if kind == "bof" {
			_, err = bofs.load(path, name, "", false, artifacts)
		} else {
			_, err = artifacts.load(kind, path, name, bofs)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", path, err))
		}
		return nil
	})
	return problems
}
