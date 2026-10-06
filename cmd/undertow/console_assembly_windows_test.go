//go:build windows && amd64

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"undertow/internal/control"
)

func TestGUIAssemblyImportAndCatalog(t *testing.T) {
	root := t.TempDir()
	path, data := assemblyConsoleFixture(t, filepath.Join(root, "fixture"))
	t.Setenv("UNDERTOW_MODULES_DIR", filepath.Join(root, "empty"))
	storePath := filepath.Join(root, "client.db")
	store, err := openClientGUIStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bank, err := openGUIModuleBank(storePath, store)
	if err != nil {
		t.Fatal(err)
	}
	gui := &guiServer{modules: bank}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("kind", "assembly")
	_ = form.WriteField("name", "managed-audit")
	part, err := form.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/modules", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	gui.importModule(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import=%d %s", response.Code, response.Body.String())
	}
	items := bank.list()
	if len(items) != 1 || items[0].Kind != "assembly" || items[0].Name != "managed-audit" || items[0].OS != "windows" || items[0].Arch != "amd64" {
		t.Fatalf("catalog=%+v", items)
	}
	if help := gui.agentGUIHelp(); !strings.Contains(help, "LOADED .NET ASSEMBLIES") || !strings.Contains(help, "managed-audit") {
		t.Fatalf("help=%q", help)
	}
}

func assemblyConsoleFixture(t *testing.T, dir string) (string, []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	input, output := filepath.Join(dir, "fixture.cs"), filepath.Join(dir, "audit.exe")
	if err := os.WriteFile(input, []byte(`using System; class Program { static int Main(string[] args) { Console.WriteLine(String.Join("|",args)); return 0; } }`), 0600); err != nil {
		t.Fatal(err)
	}
	compiler := filepath.Join(os.Getenv("SystemRoot"), "Microsoft.NET", "Framework64", "v4.0.30319", "csc.exe")
	if _, err := os.Stat(compiler); err != nil {
		t.Skip(".NET Framework compiler unavailable")
	}
	if message, err := exec.Command(compiler, "/nologo", "/target:exe", "/out:"+output, input).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, message)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return output, data
}

func TestAssemblyLoadPreloadAndConsoleJob(t *testing.T) {
	root := t.TempDir()
	path, data := assemblyConsoleFixture(t, filepath.Join(root, "assembly"))
	if err := os.WriteFile(path+".json", []byte(`{"description":"Managed fixture","usage":"audit <value>","help":"Managed help."}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNDERTOW_MODULES_DIR", root)
	artifacts, bofs := newLoadedArtifactRegistry(), newLoadedBOFRegistry()
	if problems := preloadModuleBank(artifacts, bofs); len(problems) != 0 {
		t.Fatalf("preload: %v", problems)
	}
	entry := artifacts.get("assembly-audit")
	if entry == nil || entry.Kind != "assembly" || entry.OS != "windows" || entry.Arch != "amd64" || !bytes.Equal(entry.Data, data) {
		t.Fatalf("entry=%+v", entry)
	}
	if matches := consoleCompletions(nil, "assembly-a", 0, true, false, false, bofs, artifacts); len(matches) != 1 || matches[0].value != "assembly-audit" {
		t.Fatalf("completion=%+v", matches)
	}
	var routes []string
	caller := func(_ context.Context, method, route string, body any) ([]byte, error) {
		if route == "/v1/status" {
			return json.Marshal(map[string]any{"agents": []control.AgentInfo{{ID: "agent-a", Hostname: "TALON"}}})
		}
		if method == "POST" && route == "/v1/agents/agent-a/assembly/jobs" {
			encoded, _ := json.Marshal(body)
			var request struct {
				Source []byte
				Args   []string
			}
			if json.Unmarshal(encoded, &request) != nil || !bytes.Equal(request.Source, data) || len(request.Args) != 2 || request.Args[0] != "two words" || request.Args[1] != "héllo" {
				t.Fatalf("request=%s", encoded)
			}
			routes = append(routes, route)
			return json.Marshal(control.JobInfo{ID: "assembly-job", AgentID: "agent-a", Kind: "assembly"})
		}
		return nil, fmt.Errorf("unexpected %s %s", method, route)
	}
	var output strings.Builder
	commands := fmt.Sprintf("load assembly %q audit\nhelp audit\nuse 1\nhelp\naudit --background \"two words\" héllo\nunload assembly audit\nrun-assembly --background %q \"two words\" héllo\nquit\n", path, path)
	if err := runConsole(context.Background(), strings.NewReader(commands), &output, caller, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes=%v output=%s", routes, output.String())
	}
	for _, part := range []string{"Loaded assembly: audit", "Managed fixture", "Managed help.", "Loaded .NET Assemblies", "Assembly job assembly-job started", "Unloaded assembly: audit"} {
		if !strings.Contains(output.String(), part) {
			t.Fatalf("missing %q: %s", part, output.String())
		}
	}
}
