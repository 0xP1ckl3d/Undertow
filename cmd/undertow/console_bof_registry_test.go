package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/bof"
	"undertow/internal/control"
	"undertow/internal/pivot"
)

func copyBOFFixture(t *testing.T, name string) string {
	t.Helper()
	object, err := os.ReadFile(filepath.Join("..", "..", "examples", "bof", name+".o"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name+".x64.o")
	if err := os.WriteFile(path, object, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadedBOFRegistry(t *testing.T) {
	path := copyBOFFixture(t, "hello")
	registry := newLoadedBOFRegistry()
	entry, err := registry.load(path, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "hello" || entry.SchemaKnown || entry.Compat.Architecture != "amd64" {
		t.Fatalf("entry=%+v", entry)
	}
	if _, err := entry.encode(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := entry.encode([]string{"unexpected"}); err == nil || !strings.Contains(err.Error(), "schema is unknown") {
		t.Fatalf("unknown schema: %v", err)
	}
	if _, err := registry.load(path, "hello", "", false); err == nil || !strings.Contains(err.Error(), "already loaded") {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := registry.load(path, "help", "", false); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("collision: %v", err)
	}
	if _, err := registry.load(path, "bad_name", "", false); err == nil {
		t.Fatal("invalid alias accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if len(entry.Object) == 0 {
		t.Fatal("loaded BOF lost its object")
	}
	var listing strings.Builder
	if err := runLoadedBOFManagement(&listing, registry, []string{"bofs"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing.String(), "hello") || !strings.Contains(listing.String(), path) {
		t.Fatalf("BOF list: %s", listing.String())
	}
	if err := registry.unload("hello"); err != nil {
		t.Fatal(err)
	}
	if registry.get("hello") != nil {
		t.Fatal("unload retained alias")
	}
	if err := registry.unload("hello"); err == nil {
		t.Fatal("missing alias unloaded")
	}
	if len(newLoadedBOFRegistry().names()) != 0 {
		t.Fatal("new console inherited BOFs")
	}
}

func TestLoadedBOFManifestAndArguments(t *testing.T) {
	path := copyBOFFixture(t, "arguments")
	manifest := `{"name":"argument-demo","description":"Argument demonstration","usage":"demo <target> <count>","help":"Long help text.","entrypoint":"go","arguments":[{"name":"target","type":"string","required":true},{"name":"count","type":"int","required":true}]}`
	if err := os.WriteFile(path+".json", []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	registry := newLoadedBOFRegistry()
	entry, err := registry.load(path, "demo", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Format != "zi" || !entry.SchemaKnown {
		t.Fatalf("entry=%+v", entry)
	}
	packet, err := entry.encode([]string{"server01", "5"})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := bof.EncodeArguments("zi", []string{"server01", "5"}, bof.ReadBinaryFile)
	if !bytes.Equal(packet, want) {
		t.Fatalf("packet=%x want=%x", packet, want)
	}
	if _, err := entry.encode([]string{"server01"}); err == nil || !strings.Contains(err.Error(), "requires 2 arguments") {
		t.Fatalf("count: %v", err)
	}
	if _, err := entry.encode([]string{"server01", "bad"}); err == nil || !strings.Contains(err.Error(), `argument "count" requires an integer`) {
		t.Fatalf("type: %v", err)
	}
	var help strings.Builder
	if err := printConsoleHelp(&help, false, false, false, "demo", consoleHelpOptions{loadedBOFs: registry}); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Argument demonstration", "demo <target> <count>", "Long help text.", "windows/amd64", path} {
		if !strings.Contains(help.String(), part) {
			t.Fatalf("help missing %q: %s", part, help.String())
		}
	}
	var overview strings.Builder
	if err := printConsoleHelp(&overview, false, false, false, "", consoleHelpOptions{loadedBOFs: registry}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overview.String(), "Loaded BOFs") || !strings.Contains(overview.String(), "Argument demonstration") {
		t.Fatalf("BOF overview: %s", overview.String())
	}
	var emptyOverview strings.Builder
	if err := printConsoleHelp(&emptyOverview, false, false, false, "", consoleHelpOptions{loadedBOFs: newLoadedBOFRegistry()}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(emptyOverview.String(), "Loaded BOFs") {
		t.Fatalf("empty overview has BOF section: %s", emptyOverview.String())
	}
	entry2, err := registry.load(path, "other", "i", true)
	if err != nil || entry2.Format != "i" || len(entry2.Manifest.Arguments) != 0 || entry2.usage() != "other <arg1> [--background]" {
		t.Fatalf("explicit format: %+v %v", entry2, err)
	}
	if _, err := registry.load(path, "invalid", "q", true); err == nil {
		t.Fatal("invalid format accepted")
	}
}

func TestLoadedBOFRejectsUnsupportedObject(t *testing.T) {
	path := copyBOFFixture(t, "hello")
	object, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	object[0] = 0
	if err := os.WriteFile(path, object, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newLoadedBOFRegistry().load(path, "test", "", false); err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Fatalf("unsupported object: %v", err)
	}
}

func TestLoadedBOFOptionalTrailingArgument(t *testing.T) {
	manifest, format, err := bof.ParseManifest([]byte(`{"arguments":[{"name":"target","type":"string"},{"name":"count","type":"int","required":false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	entry := &loadedBOF{Name: "demo", Manifest: manifest, Format: format, SchemaKnown: true}
	if _, err := entry.encode(nil); err == nil {
		t.Fatal("required target omitted")
	}
	packet, err := entry.encode([]string{"server01"})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := bof.EncodeArguments("z", []string{"server01"}, bof.ReadBinaryFile)
	if !bytes.Equal(packet, want) {
		t.Fatalf("optional packet=%x want %x", packet, want)
	}
	if entry.usage() != "demo <target> [count] [--background]" {
		t.Fatalf("usage=%q", entry.usage())
	}
}

func TestLoadedBOFCompletion(t *testing.T) {
	registry := newLoadedBOFRegistry()
	path := copyBOFFixture(t, "hello")
	if _, err := registry.load(path, "example", "", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		before       []string
		prefix, want string
	}{
		{nil, "ex", "example"},
		{[]string{"help"}, "ex", "example"},
		{[]string{"unload", "bof"}, "ex", "example"},
		{[]string{"load"}, "bo", "bof"},
	} {
		matches := consoleCompletions(tc.before, tc.prefix, 0, false, false, false, registry)
		if len(matches) != 1 || matches[0].value != tc.want {
			t.Fatalf("completion %+v = %+v", tc, matches)
		}
	}
}

func TestLoadedBOFConsoleSwitchAgentsAndJobs(t *testing.T) {
	path := copyBOFFixture(t, "hello")
	object, _ := os.ReadFile(path)
	var requested []string
	statusCalls := 0
	caller := func(_ context.Context, method, route string, body any) ([]byte, error) {
		if route == "/v1/status" {
			statusCalls++
			if statusCalls == 1 {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a", Hostname: "TALON"}, {ID: "agent-b", Hostname: "WOLF"}}})
		}
		if method == "POST" && strings.HasSuffix(route, "/bof/jobs") {
			encoded, _ := json.Marshal(body)
			var request struct{ Source, Arguments []byte }
			if err := json.Unmarshal(encoded, &request); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(request.Source, object) {
				t.Fatal("loaded object changed after source deletion")
			}
			want, _ := bof.EncodeArguments("zi", []string{"server01", "5"}, bof.ReadBinaryFile)
			if !bytes.Equal(request.Arguments, want) {
				t.Fatalf("arguments=%x want %x", request.Arguments, want)
			}
			requested = append(requested, route)
			return json.Marshal(control.JobInfo{ID: "job-4", AgentID: "agent-a", Kind: "bof"})
		}
		return nil, fmt.Errorf("unexpected %s %s", method, route)
	}
	var foreground []string
	opener := func(_ context.Context, agentID string, source, args []byte) (*pivot.InteractiveSession, error) {
		if !bytes.Equal(source, object) {
			t.Fatal("foreground source changed")
		}
		foreground = append(foreground, agentID)
		return nil, errors.New("foreground called")
	}
	var output strings.Builder
	commands := fmt.Sprintf("load bof %q example --format zi\nhelp\nhelp example\nexample server01 5\nuse 1\nexample server01 5 --background\nback\nuse 2\nexample server02 6\nbofs\nunload bof example\nbofs\nquit\n", path)
	if err := runConsole(context.Background(), strings.NewReader(commands), &output, caller, nil, nil, nil, nil, consoleFeatures{bof: opener}); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 || requested[0] != "/v1/agents/agent-a/bof/jobs" {
		t.Fatalf("job routes=%v", requested)
	}
	if len(foreground) != 1 || foreground[0] != "agent-b" {
		t.Fatalf("foreground agents=%v", foreground)
	}
	for _, part := range []string{"Loaded BOF: example", "Loaded BOFs", "select an agent first", "BOF job job-4 started on TALON", "foreground called", "Unloaded BOF: example"} {
		if !strings.Contains(output.String(), part) {
			t.Fatalf("output missing %q: %s", part, output.String())
		}
	}
	var fresh strings.Builder
	if err := runConsole(context.Background(), strings.NewReader("bofs\nquit\n"), &fresh, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fresh.String(), "none") {
		t.Fatalf("new session retained BOF: %s", fresh.String())
	}
}
