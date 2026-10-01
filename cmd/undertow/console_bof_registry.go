package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"undertow/internal/bof"
)

type loadedBOF struct {
	Name        string
	Path        string
	Object      []byte
	Compat      bof.Compatibility
	Manifest    bof.Manifest
	Format      string
	SchemaKnown bool
}

type loadedBOFRegistry struct {
	mu    sync.RWMutex
	items map[string]*loadedBOF
}

func newLoadedBOFRegistry() *loadedBOFRegistry {
	return &loadedBOFRegistry{items: make(map[string]*loadedBOF)}
}

func (r *loadedBOFRegistry) get(name string) *loadedBOF {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.items[name]
}

func (r *loadedBOFRegistry) names() []string {
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

// These are the commands and aliases interpreted by the interactive console.
// A loaded alias cannot shadow one, even if it is only valid in another menu.
var builtinConsoleCommands = map[string]bool{
	"agents": true, "use": true, "select": true, "show": true, "back": true,
	"status": true, "help": true, "clear": true, "cls": true, "quit": true,
	"exit": true, "background": true, "stop": true, "logs": true,
	"route": true, "routes": true, "relay": true, "topology": true,
	"transports": true, "start": true, "shell": true, "interactive": true,
	"exec": true, "run-script": true, "run-wasm": true, "run-native": true,
	"run-bof": true, "job": true, "jobs": true, "host": true,
	"pwd": true, "ls": true, "stat": true, "mkdir": true, "rm": true,
	"whoami": true, "ps": true, "privileges": true, "env": true,
	"interfaces": true, "dns": true, "route-table": true,
	"upload": true, "download": true, "forward": true,
	"internal": true, "vpn": true, "agent": true, "clients": true,
	"load": true, "unload": true, "bofs": true,
	"bof": true, "script": true, "scripts": true, "wasm": true,
	"native": true, "hostops": true, "files": true,
	"navigation": true, "routing": true, "transport": true,
	"lifecycle": true,
}

func validBOFCommandName(name string) bool {
	if name == "" || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, char := range name[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func deriveBOFCommandName(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, suffix := range []string{".x64", ".amd64", ".win64", ".windows-amd64"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			name = name[:len(name)-len(suffix)]
			break
		}
	}
	return strings.ToLower(name)
}

func sidecarBOFManifest(path string) (bof.Manifest, string, bool, error) {
	for _, candidate := range []string{path + ".json", strings.TrimSuffix(path, filepath.Ext(path)) + ".json"} {
		contents, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return bof.Manifest{}, "", false, fmt.Errorf("BOF manifest %s: %w", candidate, err)
		}
		manifest, format, err := bof.ParseManifest(contents)
		if err != nil {
			return bof.Manifest{}, "", false, fmt.Errorf("BOF manifest %s: %w", candidate, err)
		}
		return manifest, format, true, nil
	}
	return bof.Manifest{}, "", false, nil
}

func (r *loadedBOFRegistry) load(path, name, explicitFormat string, hasFormat bool) (*loadedBOF, error) {
	if name == "" {
		name = deriveBOFCommandName(path)
	}
	if !validBOFCommandName(name) {
		return nil, fmt.Errorf("invalid BOF command name %q (use lowercase letters, digits and hyphens)", name)
	}
	if builtinConsoleCommands[name] {
		return nil, fmt.Errorf("%q is a built-in command", name)
	}
	if r.get(name) != nil {
		return nil, fmt.Errorf("BOF command %q is already loaded; unload it first", name)
	}
	if hasFormat {
		if err := bof.ValidateFormat(explicitFormat); err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	object, err := readMemoryFile(absolute, bof.MaxObjectSize)
	if err != nil {
		return nil, err
	}
	compat, err := bof.Parse(object)
	if err != nil {
		return nil, err
	}
	if !compat.Supported {
		return nil, fmt.Errorf("unsupported BOF: %s", strings.Join(compat.Errors, "; "))
	}
	manifest, format, found, err := sidecarBOFManifest(absolute)
	if err != nil {
		return nil, err
	}
	if hasFormat {
		format = explicitFormat
		manifest.Arguments = nil
		manifest.Usage = ""
	}
	entry := &loadedBOF{Name: name, Path: absolute, Object: object, Compat: compat, Manifest: manifest, Format: format, SchemaKnown: found || hasFormat}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items[name] != nil {
		return nil, fmt.Errorf("BOF command %q is already loaded; unload it first", name)
	}
	r.items[name] = entry
	return entry, nil
}

func (r *loadedBOFRegistry) unload(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items[name] == nil {
		return fmt.Errorf("BOF command %q is not loaded", name)
	}
	delete(r.items, name)
	return nil
}

func runLoadedBOFManagement(output io.Writer, registry *loadedBOFRegistry, args []string) error {
	switch args[0] {
	case "load":
		if len(args) < 3 || args[1] != "bof" {
			return errors.New("use load bof FILE [NAME] [--format FORMAT]")
		}
		path, name, format, hasFormat := "", "", "", false
		for i := 2; i < len(args); i++ {
			if args[i] == "--format" {
				if hasFormat || i+1 >= len(args) {
					return errors.New("use load bof FILE [NAME] [--format FORMAT]")
				}
				format, hasFormat = args[i+1], true
				i++
				continue
			}
			if path == "" {
				path = args[i]
			} else if name == "" {
				name = args[i]
			} else {
				return errors.New("use load bof FILE [NAME] [--format FORMAT]")
			}
		}
		if path == "" {
			return errors.New("use load bof FILE [NAME] [--format FORMAT]")
		}
		entry, err := registry.load(path, name, format, hasFormat)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "Loaded BOF: %s\nArchitecture : windows/%s\nArguments    : %d\n", entry.Name, entry.Compat.Architecture, len(entry.Format))
		return nil
	case "unload":
		if len(args) != 3 || args[1] != "bof" {
			return errors.New("use unload bof NAME")
		}
		if err := registry.unload(args[2]); err != nil {
			return err
		}
		fmt.Fprintf(output, "Unloaded BOF: %s\n", args[2])
		return nil
	case "bofs":
		if len(args) != 1 {
			return errors.New("use bofs")
		}
		fmt.Fprintln(output, "Loaded BOFs\n-----------")
		names := registry.names()
		if len(names) == 0 {
			fmt.Fprintln(output, "  none")
			return nil
		}
		fmt.Fprintf(output, "%-18s %-45s %s\n", "NAME", "FILE", "ARGS")
		for _, name := range names {
			entry := registry.get(name)
			fmt.Fprintf(output, "%-18s %-45s %s\n", name, entry.Path, entry.argumentSummary())
		}
		return nil
	}
	return errors.New("unknown BOF management command")
}

func (entry *loadedBOF) argumentSummary() string {
	if !entry.SchemaKnown {
		return "none"
	}
	if len(entry.Format) == 0 {
		return "none"
	}
	words := make([]string, len(entry.Format))
	for i := range words {
		if i < len(entry.Manifest.Arguments) {
			words[i] = entry.Manifest.Arguments[i].Type
		} else {
			words[i] = bofFormatType(entry.Format[i])
		}
	}
	return strings.Join(words, " ")
}

func bofFormatType(char byte) string {
	switch char {
	case 'i':
		return "int"
	case 's':
		return "short"
	case 'z':
		return "string"
	case 'Z':
		return "wstring"
	case 'b':
		return "binary"
	}
	return "unknown"
}

func (entry *loadedBOF) usage() string {
	usage := strings.TrimSpace(entry.Manifest.Usage)
	if usage == "" {
		usage = entry.Name
		for i := range entry.Format {
			name := fmt.Sprintf("arg%d", i+1)
			required := true
			if i < len(entry.Manifest.Arguments) {
				if entry.Manifest.Arguments[i].Name != "" {
					name = entry.Manifest.Arguments[i].Name
				}
				required = entry.Manifest.Arguments[i].IsRequired()
			}
			if required {
				usage += " <" + name + ">"
			} else {
				usage += " [" + name + "]"
			}
		}
	} else if first := strings.Fields(usage); len(first) > 0 && first[0] != entry.Name {
		usage = entry.Name + strings.TrimPrefix(usage, first[0])
	}
	if !strings.Contains(usage, "--background") {
		usage += " [--background]"
	}
	return usage
}

func (entry *loadedBOF) requiredCount() int {
	if len(entry.Manifest.Arguments) != len(entry.Format) {
		return len(entry.Format)
	}
	count := 0
	for _, arg := range entry.Manifest.Arguments {
		if arg.IsRequired() {
			count++
		}
	}
	return count
}

func (entry *loadedBOF) encode(values []string) ([]byte, error) {
	if !entry.SchemaKnown && len(values) != 0 {
		return nil, fmt.Errorf("argument schema is unknown for loaded BOF %q; reload it with --format or provide a manifest", entry.Name)
	}
	if len(values) < entry.requiredCount() || len(values) > len(entry.Format) {
		count := entry.requiredCount()
		message := fmt.Sprintf("%s requires %d arguments", entry.Name, count)
		if count != len(entry.Format) {
			message = fmt.Sprintf("%s requires %d to %d arguments", entry.Name, count, len(entry.Format))
		}
		return nil, fmt.Errorf("%s\n\nUsage:\n  %s", message, entry.usage())
	}
	packet, err := bof.EncodeArguments(entry.Format[:len(values)], values, bof.ReadBinaryFile)
	if err == nil {
		return packet, nil
	}
	for i, value := range values {
		if _, testErr := bof.EncodeArguments(entry.Format[i:i+1], []string{value}, bof.ReadBinaryFile); testErr != nil {
			name := fmt.Sprintf("arg%d", i+1)
			if i < len(entry.Manifest.Arguments) && entry.Manifest.Arguments[i].Name != "" {
				name = entry.Manifest.Arguments[i].Name
			}
			if entry.Format[i] == 'i' || entry.Format[i] == 's' {
				return nil, fmt.Errorf("argument %q requires an integer: %w", name, testErr)
			}
			return nil, fmt.Errorf("argument %q: %w", name, testErr)
		}
	}
	return nil, err
}

func runLoadedBOF(ctx context.Context, output io.Writer, editor *consoleEditor, call consoleCaller, open bofOpener, entry *loadedBOF, agentID, agentLabel string, args []string) error {
	if agentID == "" {
		return errors.New("select an agent first with use AGENT_NUMBER")
	}
	values := make([]string, 0, len(args))
	background := false
	options := true
	for _, arg := range args {
		if arg == "--" && options {
			options = false
		} else if arg == "--background" && options {
			background = true
		} else {
			values = append(values, arg)
		}
	}
	packed, err := entry.encode(values)
	if err != nil {
		return err
	}
	return executeConsoleBOF(ctx, output, editor, call, open, agentID, agentLabel, entry.Object, packed, background)
}

func printLoadedBOFHelp(output io.Writer, entry *loadedBOF) {
	fmt.Fprintf(output, "%s\n\n", entry.Name)
	if entry.Manifest.Description != "" {
		fmt.Fprintf(output, "Description:\n  %s\n\n", entry.Manifest.Description)
	}
	fmt.Fprintf(output, "Usage:\n  %s\n", entry.usage())
	if !entry.SchemaKnown {
		fmt.Fprintln(output, "\nArgument schema: unspecified (zero arguments accepted)")
	}
	if len(entry.Format) > 0 {
		fmt.Fprintln(output, "\nArguments:")
		for i := range entry.Format {
			name := fmt.Sprintf("arg%d", i+1)
			kind := bofFormatType(entry.Format[i])
			if i < len(entry.Manifest.Arguments) {
				arg := entry.Manifest.Arguments[i]
				if arg.Name != "" {
					name = arg.Name
				}
				kind = arg.Type
				if !arg.IsRequired() {
					kind += " (optional)"
				}
			}
			fmt.Fprintf(output, "  %-14s %s\n", name, kind)
		}
	}
	if entry.Manifest.Help != "" {
		fmt.Fprintf(output, "\n%s\n", entry.Manifest.Help)
	}
	fmt.Fprintf(output, "\nSource:\n  %s\n\nArchitecture:\n  windows/%s\n", entry.Path, entry.Compat.Architecture)
}
